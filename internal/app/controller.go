package app

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"oneapp/internal/config"
	"oneapp/internal/domain"
	"oneapp/internal/service"
	"oneapp/internal/storage"
)

const (
	trayPopoverWidth  = 368
	trayPopoverHeight = 508
	trayPopoverOffset = 8
)

// Controller coordinates Wails v3 windows, system tray, global shortcuts, dialogs, and OS file drops.
type Controller struct {
	app         *application.App
	mainWin     *application.WebviewWindow
	launcherWin *application.WebviewWindow
	trayWin     *application.WebviewWindow
	tray        *application.SystemTray
	settings    *config.SettingsStore
	repo        *storage.Repo
	ingest      *service.IngestService
	launch      *service.LaunchService
	appSvc      *service.AppService

	mu               sync.Mutex
	registeredHotkey string
	hotkeyStatus     domain.HotkeyStatus
	quitting         atomic.Bool
	started          atomic.Bool
	trayPinned       atomic.Bool
	lastLauncherShow atomic.Int64
	lastTrayShow     atomic.Int64
	lastTrayBlurHide atomic.Int64
	blurTimer        *time.Timer
	trayBlurTimer    *time.Timer
}

// NewController constructs a Controller for Wails v3 desktop orchestration.
func NewController(
	settings *config.SettingsStore,
	repo *storage.Repo,
	ingest *service.IngestService,
	launch *service.LaunchService,
	appSvc *service.AppService,
) *Controller {
	return &Controller{
		settings: settings,
		repo:     repo,
		ingest:   ingest,
		launch:   launch,
		appSvc:   appSvc,
		hotkeyStatus: domain.HotkeyStatus{
			Accelerator: settings.Get().LauncherHotkey,
		},
	}
}

// AttachApp binds the Wails application instance and configures windows, tray, drops, and shortcuts.
func (c *Controller) AttachApp(app *application.App, iconBytes []byte, silentStart bool) {
	c.app = app

	c.mainWin = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "OneApp",
		Width:            1280,
		Height:           820,
		MinWidth:         1000,
		MinHeight:        640,
		Hidden:           silentStart,
		URL:              "/",
		EnableFileDrop:   true,
		InitialPosition:  application.WindowCentered,
		BackgroundColour: application.NewRGB(13, 14, 18),
	})

	c.mainWin.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if !c.quitting.Load() && c.settings.Get().CloseToTray {
			e.Cancel()
			c.mainWin.Hide()
		}
	})

	c.mainWin.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
		files := e.Context().DroppedFiles()
		if len(files) == 0 {
			return
		}
		go c.handleDroppedFiles(files)
	})

	c.launcherWin = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "launcher",
		Title:            "OneApp Launcher",
		Width:            820,
		Height:           520,
		Frameless:        true,
		AlwaysOnTop:      true,
		DisableResize:    true,
		Hidden:           true,
		InitialPosition:  application.WindowCentered,
		URL:              "/#/launcher",
		BackgroundColour: application.NewRGB(17, 19, 24),
		Windows: application.WindowsWindow{
			HiddenOnTaskbar: true,
		},
	})

	c.launcherWin.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if !c.quitting.Load() {
			e.Cancel()
			c.launcherWin.Hide()
		}
	})

	c.launcherWin.OnWindowEvent(events.Common.WindowFocus, func(_ *application.WindowEvent) {
		c.cancelBlurHide()
	})

	c.launcherWin.OnWindowEvent(events.Common.WindowLostFocus, func(_ *application.WindowEvent) {
		c.scheduleBlurHide()
	})

	c.trayWin = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "tray-popover",
		Title:            "OneApp Quick Launch",
		Width:            trayPopoverWidth,
		Height:           trayPopoverHeight,
		Frameless:        true,
		AlwaysOnTop:      true,
		DisableResize:    true,
		Hidden:           true,
		EnableFileDrop:   true,
		URL:              "/#/tray",
		BackgroundColour: application.NewRGB(17, 19, 24),
		Windows: application.WindowsWindow{
			HiddenOnTaskbar: true,
		},
	})

	c.trayWin.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if !c.quitting.Load() {
			e.Cancel()
			c.trayWin.Hide()
		}
	})

	c.trayWin.OnWindowEvent(events.Common.WindowFocus, func(_ *application.WindowEvent) {
		c.cancelTrayBlurHide()
	})

	c.trayWin.OnWindowEvent(events.Common.WindowLostFocus, func(_ *application.WindowEvent) {
		c.scheduleTrayBlurHide()
	})

	c.trayWin.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
		files := e.Context().DroppedFiles()
		if len(files) == 0 {
			return
		}
		go c.handleDroppedFiles(files)
	})

	c.tray = app.SystemTray.New()
	if len(iconBytes) > 0 {
		c.tray.SetIcon(iconBytes)
	}
	c.tray.SetTooltip("OneApp — 免安装软件库")
	c.tray.OnClick(func() {
		switch c.settings.Get().TrayClickAction {
		case "main":
			c.ShowMain()
		case "launcher":
			c.ToggleLauncher()
		default:
			c.ToggleTrayPopover()
		}
	})
	c.RefreshTrayMenu()

	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(_ *application.ApplicationEvent) {
		c.started.Store(true)
		c.RebindLauncherHotkey(c.settings.Get().LauncherHotkey)
	})
}

func (c *Controller) handleDroppedFiles(files []string) {
	analyses, err := c.ingest.AnalyzePaths(files)
	if err != nil {
		c.EmitEvent("app:error", err.Error())
		return
	}
	if len(analyses) == 0 {
		return
	}

	if c.settings.Get().SilentIngest {
		hasInteractiveItem := false
		for _, a := range analyses {
			if a.SourceKind == "project" || len(a.Candidates) == 0 {
				hasInteractiveItem = true
				break
			}
		}
		if !hasInteractiveItem {
			items := make([]domain.IngestCommitItem, 0, len(analyses))
			for _, a := range analyses {
				if a.Error != "" || len(a.Candidates) == 0 {
					continue
				}
				items = append(items, domain.IngestCommitItem{
					SourcePath:  a.SourcePath,
					Name:        a.SuggestedName,
					Version:     a.Version,
					Description: a.Description,
					Action:      a.SuggestedAction,
					PrimaryPath: a.Candidates[0].RelPath,
				})
			}
			if len(items) > 0 {
				res := c.appSvc.CommitIngest(items)
				c.EmitEvent("ingest:silent", res)
				if len(res.Errors) == 0 {
					return
				}
			}
		}
	}

	c.ShowMain()
	c.EmitEvent("ingest:dropped", analyses)
}

func (c *Controller) scheduleBlurHide() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.blurTimer != nil {
		c.blurTimer.Stop()
	}
	openedRecently := time.Now().UnixNano()-c.lastLauncherShow.Load() < int64(220*time.Millisecond)
	if openedRecently {
		return
	}
	c.blurTimer = time.AfterFunc(110*time.Millisecond, func() {
		if c.launcherWin != nil && c.launcherWin.IsVisible() {
			c.launcherWin.Hide()
		}
	})
}

func (c *Controller) cancelBlurHide() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.blurTimer != nil {
		c.blurTimer.Stop()
		c.blurTimer = nil
	}
}

func (c *Controller) scheduleTrayBlurHide() {
	if c.trayPinned.Load() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.trayBlurTimer != nil {
		c.trayBlurTimer.Stop()
	}
	openedRecently := time.Now().UnixNano()-c.lastTrayShow.Load() < int64(220*time.Millisecond)
	if openedRecently {
		return
	}
	c.trayBlurTimer = time.AfterFunc(120*time.Millisecond, func() {
		if !c.trayPinned.Load() && c.trayWin != nil && c.trayWin.IsVisible() {
			c.lastTrayBlurHide.Store(time.Now().UnixNano())
			c.trayWin.Hide()
		}
	})
}

func (c *Controller) cancelTrayBlurHide() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.trayBlurTimer != nil {
		c.trayBlurTimer.Stop()
		c.trayBlurTimer = nil
	}
}

func (c *Controller) positionTrayPopover() {
	if c.trayWin == nil {
		return
	}
	if c.tray != nil {
		if err := c.tray.PositionWindow(c.trayWin, trayPopoverOffset); err == nil {
			return
		}
	}
	if c.app == nil || c.app.Screen == nil {
		return
	}
	screen := c.app.Screen.GetPrimary()
	if screen == nil {
		return
	}
	bounds := c.trayWin.Bounds()
	w := bounds.Width
	if w <= 0 {
		w = trayPopoverWidth
	}
	h := bounds.Height
	if h <= 0 {
		h = trayPopoverHeight
	}
	bounds.X = screen.WorkArea.X + screen.WorkArea.Width - w - trayPopoverOffset
	bounds.Y = screen.WorkArea.Y + screen.WorkArea.Height - h - trayPopoverOffset
	c.trayWin.SetBounds(bounds)
}

// ShowTrayPopover positions, displays, and focuses the tray quick-launch popover.
func (c *Controller) ShowTrayPopover() {
	if c.trayWin == nil {
		return
	}
	c.HideLauncher()
	c.cancelTrayBlurHide()
	c.lastTrayShow.Store(time.Now().UnixNano())
	c.positionTrayPopover()
	c.trayWin.Show()
	c.trayWin.Focus()
	c.EmitEvent("tray:opened", time.Now().UnixMilli())
}

// HideTrayPopover hides the tray quick-launch popover.
func (c *Controller) HideTrayPopover() {
	c.cancelTrayBlurHide()
	if c.trayWin != nil {
		c.trayWin.Hide()
	}
}

// HideTrayPopoverIfUnpinned hides the tray popover unless the user pinned it open.
func (c *Controller) HideTrayPopoverIfUnpinned() {
	if !c.trayPinned.Load() {
		c.HideTrayPopover()
	}
}

// ToggleTrayPopover toggles visibility of the tray quick-launch popover with click/blur anti-bounce.
func (c *Controller) ToggleTrayPopover() {
	if c.trayWin == nil {
		return
	}
	if c.trayWin.IsVisible() {
		c.HideTrayPopover()
		return
	}
	if time.Now().UnixNano()-c.lastTrayBlurHide.Load() < int64(280*time.Millisecond) {
		return
	}
	c.ShowTrayPopover()
}

// SetTrayPopoverPinned controls whether the tray popover stays open on blur and launch.
func (c *Controller) SetTrayPopoverPinned(pinned bool) {
	c.trayPinned.Store(pinned)
	if pinned {
		c.cancelTrayBlurHide()
	}
}

// GetTrayPopoverPinned returns whether the tray popover is currently pinned open.
func (c *Controller) GetTrayPopoverPinned() bool {
	return c.trayPinned.Load()
}

// ShowMain displays and focuses the main management window.
func (c *Controller) ShowMain() {
	if c.mainWin == nil {
		return
	}
	c.HideTrayPopoverIfUnpinned()
	c.mainWin.Show()
	c.mainWin.Restore()
	c.mainWin.Focus()
}

// ShowLauncher centers, shows, and focuses the instant launcher bar.
func (c *Controller) ShowLauncher() {
	if c.launcherWin == nil {
		return
	}
	c.HideTrayPopoverIfUnpinned()
	c.cancelBlurHide()
	c.lastLauncherShow.Store(time.Now().UnixNano())
	c.launcherWin.Center()
	c.launcherWin.Show()
	c.launcherWin.Focus()
	c.EmitEvent("launcher:opened", time.Now().UnixMilli())
}

// HideLauncher hides the instant launcher bar.
func (c *Controller) HideLauncher() {
	c.cancelBlurHide()
	if c.launcherWin != nil {
		c.launcherWin.Hide()
	}
}

// ToggleLauncher toggles visibility of the instant launcher bar.
func (c *Controller) ToggleLauncher() {
	if c.launcherWin == nil {
		return
	}
	if c.launcherWin.IsVisible() {
		c.HideLauncher()
	} else {
		c.ShowLauncher()
	}
}

// OpenAppInMain brings up the main window and directs the frontend to select appID.
func (c *Controller) OpenAppInMain(appID string) {
	c.HideTrayPopoverIfUnpinned()
	c.HideLauncher()
	c.ShowMain()
	c.EmitEvent("open:app", appID)
}

// OpenSettingsInMain brings up the main window and opens the settings modal.
func (c *Controller) OpenSettingsInMain() {
	c.HideTrayPopoverIfUnpinned()
	c.HideLauncher()
	c.ShowMain()
	c.EmitEvent("open:settings", true)
}

// QuitApp exits OneApp cleanly.
func (c *Controller) QuitApp() {
	c.quitting.Store(true)
	if c.app != nil {
		c.app.Quit()
	}
}

// PickFiles opens an OS multi-file picker attached to the main window.
func (c *Controller) PickFiles() ([]string, error) {
	if c.app == nil {
		return nil, fmt.Errorf("app not ready")
	}
	d := c.app.Dialog.OpenFile().
		SetTitle("选择免安装程序、脚本或压缩包").
		CanChooseFiles(true).
		CanChooseDirectories(false).
		AddFilter("可执行程序与压缩包 (*.exe;*.zip;*.7z;*.bat;*.cmd;*.ps1;*.lnk)", "*.exe;*.zip;*.7z;*.bat;*.cmd;*.ps1;*.lnk").
		AddFilter("所有文件 (*.*)", "*.*")
	if c.mainWin != nil {
		d.AttachToWindow(c.mainWin)
	}
	return d.PromptForMultipleSelection()
}

// PickFolder opens an OS directory picker attached to the main window.
func (c *Controller) PickFolder() (string, error) {
	if c.app == nil {
		return "", fmt.Errorf("app not ready")
	}
	d := c.app.Dialog.OpenFile().
		SetTitle("选择免安装软件或源码项目文件夹").
		CanChooseFiles(false).
		CanChooseDirectories(true)
	if c.mainWin != nil {
		d.AttachToWindow(c.mainWin)
	}
	return d.PromptForSingleSelection()
}

// PickIconFile opens a picker for PNG/ICO/EXE icon sources.
func (c *Controller) PickIconFile() (string, error) {
	if c.app == nil {
		return "", fmt.Errorf("app not ready")
	}
	d := c.app.Dialog.OpenFile().
		SetTitle("选择自定义图标或程序").
		CanChooseFiles(true).
		CanChooseDirectories(false).
		AddFilter("图标与程序 (*.png;*.ico;*.exe)", "*.png;*.ico;*.exe")
	if c.mainWin != nil {
		d.AttachToWindow(c.mainWin)
	}
	return d.PromptForSingleSelection()
}

// PickEntryFile opens a file picker for choosing a secondary executable/script entry.
func (c *Controller) PickEntryFile(initialDir string) (string, error) {
	if c.app == nil {
		return "", fmt.Errorf("app not ready")
	}
	d := c.app.Dialog.OpenFile().
		SetTitle("选择启动入口文件").
		CanChooseFiles(true).
		CanChooseDirectories(false).
		AddFilter("可执行文件与脚本 (*.exe;*.bat;*.cmd;*.ps1;*.lnk)", "*.exe;*.bat;*.cmd;*.ps1;*.lnk")
	if strings.TrimSpace(initialDir) != "" {
		d.SetDirectory(initialDir)
	}
	if c.mainWin != nil {
		d.AttachToWindow(c.mainWin)
	}
	return d.PromptForSingleSelection()
}

// GetHotkeyStatus returns the current global hotkey registration status.
func (c *Controller) GetHotkeyStatus() domain.HotkeyStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hotkeyStatus
}

// RebindLauncherHotkey unregisters the previous global shortcut and registers the new accelerator.
func (c *Controller) RebindLauncherHotkey(accelerator string) domain.HotkeyStatus {
	accel := strings.TrimSpace(accelerator)
	if accel == "" {
		accel = "Alt+Space"
	}

	c.mu.Lock()
	prev := c.registeredHotkey
	c.mu.Unlock()

	if c.app == nil || !c.started.Load() {
		status := domain.HotkeyStatus{Accelerator: accel}
		c.mu.Lock()
		c.hotkeyStatus = status
		c.mu.Unlock()
		return status
	}

	if prev != "" {
		_ = c.app.GlobalShortcut.Unregister(prev)
	}

	err := c.app.GlobalShortcut.Register(accel, func() {
		c.ToggleLauncher()
	})

	status := domain.HotkeyStatus{
		Accelerator: accel,
		Registered:  err == nil,
	}
	if err != nil {
		status.Error = fmt.Sprintf("快捷键 %s 注册失败（可能与其他软件冲突）: %v", accel, err)
		c.mu.Lock()
		c.registeredHotkey = ""
		c.hotkeyStatus = status
		c.mu.Unlock()
	} else {
		c.mu.Lock()
		c.registeredHotkey = accel
		c.hotkeyStatus = status
		c.mu.Unlock()
	}

	c.RefreshTrayMenu()
	c.EmitEvent("hotkey:status", status)
	return status
}

// EmitEvent broadcasts an event to all active Wails windows.
func (c *Controller) EmitEvent(name string, payload any) {
	if c.app == nil {
		return
	}
	c.app.Event.Emit(name, payload)
}

// RefreshTrayMenu rebuilds the system tray context menu including recent apps.
func (c *Controller) RefreshTrayMenu() {
	if c.app == nil || c.tray == nil {
		return
	}
	menu := c.app.NewMenu()
	menu.Add("显示管理主窗口").OnClick(func(_ *application.Context) {
		c.ShowMain()
	})
	menu.Add("打开托盘快捷浮窗").OnClick(func(_ *application.Context) {
		c.ShowTrayPopover()
	})
	hk := c.settings.Get().LauncherHotkey
	menu.Add(fmt.Sprintf("打开极速启动条 (%s)", hk)).OnClick(func(_ *application.Context) {
		c.ShowLauncher()
	})

	if c.repo != nil {
		if apps, err := c.repo.ListApps(); err == nil && len(apps) > 0 {
			recentSub := menu.AddSubmenu("最近使用")
			count := 0
			for _, a := range apps {
				if a.LastLaunchedAt == 0 && count >= 5 {
					continue
				}
				appCopy := a
				recentSub.Add(appCopy.Name).OnClick(func(_ *application.Context) {
					_, _ = c.launch.LaunchApp(domain.LaunchRequest{AppID: appCopy.ID})
				})
				count++
				if count >= 5 {
					break
				}
			}
			if count == 0 {
				recentSub.Add("暂无启动记录").SetEnabled(false)
			}
		}
	}

	menu.AddSeparator()
	menu.Add("退出 OneApp").OnClick(func(_ *application.Context) {
		c.QuitApp()
	})
	c.tray.SetMenu(menu)
}

// WindowService exposes window control commands as clean Wails RPC bindings for the frontend.
type WindowService struct {
	ctrl *Controller
}

// NewWindowService creates a WindowService backed by Controller.
func NewWindowService(ctrl *Controller) *WindowService {
	return &WindowService{ctrl: ctrl}
}

func (w *WindowService) ShowMain()                            { w.ctrl.ShowMain() }
func (w *WindowService) ShowLauncher()                        { w.ctrl.ShowLauncher() }
func (w *WindowService) HideLauncher()                        { w.ctrl.HideLauncher() }
func (w *WindowService) ToggleLauncher()                      { w.ctrl.ToggleLauncher() }
func (w *WindowService) ShowTrayPopover()                     { w.ctrl.ShowTrayPopover() }
func (w *WindowService) HideTrayPopover()                     { w.ctrl.HideTrayPopover() }
func (w *WindowService) ToggleTrayPopover()                   { w.ctrl.ToggleTrayPopover() }
func (w *WindowService) SetTrayPopoverPinned(pinned bool)     { w.ctrl.SetTrayPopoverPinned(pinned) }
func (w *WindowService) GetTrayPopoverPinned() bool           { return w.ctrl.GetTrayPopoverPinned() }
func (w *WindowService) OpenAppInMain(appID string)           { w.ctrl.OpenAppInMain(appID) }
func (w *WindowService) OpenSettingsInMain()                  { w.ctrl.OpenSettingsInMain() }
func (w *WindowService) QuitApp()                             { w.ctrl.QuitApp() }
func (w *WindowService) GetHotkeyStatus() domain.HotkeyStatus { return w.ctrl.GetHotkeyStatus() }

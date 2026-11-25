package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"oneapp/internal/archive"
	"oneapp/internal/config"
	"oneapp/internal/domain"
	"oneapp/internal/platform"
	"oneapp/internal/storage"
)

// UIHost decouples services from Wails-specific window, dialog, hotkey, and event primitives.
type UIHost interface {
	PickFiles() ([]string, error)
	PickFolder() (string, error)
	PickIconFile() (string, error)
	PickEntryFile(initialDir string) (string, error)
	GetHotkeyStatus() domain.HotkeyStatus
	RebindLauncherHotkey(accelerator string) domain.HotkeyStatus
	EmitEvent(name string, payload any)
	RefreshTrayMenu()
}

// AppService exposes OneApp's core software management, search, ingest, and launch operations.
type AppService struct {
	paths    *config.Paths
	settings *config.SettingsStore
	repo     *storage.Repo
	bridge   platform.OSBridge
	ingest   *IngestService
	search   *SearchService
	launch   *LaunchService
	ui       UIHost
}

// NewAppService constructs the primary bound application service.
func NewAppService(
	paths *config.Paths,
	settings *config.SettingsStore,
	repo *storage.Repo,
	bridge platform.OSBridge,
	ingest *IngestService,
	search *SearchService,
	launch *LaunchService,
) *AppService {
	return &AppService{
		paths:    paths,
		settings: settings,
		repo:     repo,
		bridge:   bridge,
		ingest:   ingest,
		search:   search,
		launch:   launch,
	}
}

// AttachUIHosts injects the Wails UI host into AppService and SettingsService without exposing it via RPC bindings.
func AttachUIHosts(appSvc *AppService, settingsSvc *SettingsService, ui UIHost) {
	if appSvc != nil {
		appSvc.ui = ui
	}
	if settingsSvc != nil {
		settingsSvc.ui = ui
	}
}

// RefreshIndex reloads apps from SQLite, rebuilds the in-memory search index, and notifies listeners.
func (s *AppService) RefreshIndex() {
	apps, err := s.repo.ListApps()
	if err != nil {
		return
	}
	cfg := s.settings.Get()
	running := s.launch.GetRunningMap()
	hydrated := make([]domain.AppOverview, 0, len(apps))
	for _, a := range apps {
		hydrated = append(hydrated, s.hydrateApp(a, cfg, running, false))
	}
	s.search.Rebuild(hydrated, running)
	if s.ui != nil {
		s.ui.RefreshTrayMenu()
		s.ui.EmitEvent("apps:changed", hydrated)
	}
}

// GetBootstrapData returns initial hydration payload for both windows.
func (s *AppService) GetBootstrapData() (domain.AppBootstrapData, error) {
	apps, err := s.ListApps()
	if err != nil {
		return domain.AppBootstrapData{}, err
	}
	cats, err := s.repo.ListCategories()
	if err != nil {
		return domain.AppBootstrapData{}, err
	}
	tags, err := s.repo.ListTags()
	if err != nil {
		return domain.AppBootstrapData{}, err
	}
	cfg := s.settings.Get()
	var hk domain.HotkeyStatus
	if s.ui != nil {
		hk = s.ui.GetHotkeyStatus()
	} else {
		hk = domain.HotkeyStatus{Accelerator: cfg.LauncherHotkey}
	}

	return domain.AppBootstrapData{
		Apps:         apps,
		Categories:   cats,
		Tags:         tags,
		Running:      s.launch.GetRunningStates(),
		HotkeyStatus: hk,
		PortableRoot: s.paths.RootDir,
		LibraryRoot:  s.paths.LibraryRoot(cfg),
	}, nil
}

// ListApps returns all apps hydrated with icon URLs, resolved paths, and running status.
func (s *AppService) ListApps() ([]domain.AppOverview, error) {
	apps, err := s.repo.ListApps()
	if err != nil {
		return nil, err
	}
	cfg := s.settings.Get()
	running := s.launch.GetRunningMap()
	out := make([]domain.AppOverview, 0, len(apps))
	for _, a := range apps {
		out = append(out, s.hydrateApp(a, cfg, running, false))
	}
	s.search.Rebuild(out, running)
	return out, nil
}

// GetApp returns detailed hydrated state for a single app (including computed disk size).
func (s *AppService) GetApp(id string) (*domain.AppOverview, error) {
	ov, err := s.repo.GetApp(id)
	if err != nil {
		return nil, err
	}
	cfg := s.settings.Get()
	running := s.launch.GetRunningMap()
	hydrated := s.hydrateApp(*ov, cfg, running, true)
	return &hydrated, nil
}

// UpdateApp updates editable metadata and tags for an app.
func (s *AppService) UpdateApp(req domain.UpdateAppRequest) (*domain.AppOverview, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("软件名称不能为空")
	}
	if err := s.repo.UpdateApp(req); err != nil {
		return nil, err
	}
	s.RefreshIndex()
	return s.GetApp(req.ID)
}

// SaveEntries replaces the launch entries of an app.
func (s *AppService) SaveEntries(appID string, inputs []domain.EntryInput) (*domain.AppOverview, error) {
	if len(inputs) == 0 {
		return nil, fmt.Errorf("至少保留一个启动入口")
	}
	entries := make([]domain.AppEntry, 0, len(inputs))
	for i, in := range inputs {
		id := strings.TrimSpace(in.ID)
		if id == "" {
			id = uuid.NewString()
		}
		name := strings.TrimSpace(in.Name)
		if name == "" {
			name = fmt.Sprintf("入口 %d", i+1)
		}
		et := in.EntryType
		if et == "" {
			if detected, ok := DetectEntryType(in.RelPath); ok {
				et = detected
			} else {
				et = domain.EntryExe
			}
		}
		envVars := in.EnvVars
		if envVars == nil {
			envVars = map[string]string{}
		}
		relPath := strings.TrimSpace(in.RelPath)
		if et != domain.EntryCommand {
			relPath = filepath.ToSlash(relPath)
		}
		entries = append(entries, domain.AppEntry{
			ID:                 id,
			AppID:              appID,
			Name:               name,
			EntryType:          et,
			RelPath:            relPath,
			ArgsTemplate:       strings.TrimSpace(in.ArgsTemplate),
			WorkingDir:         strings.TrimSpace(in.WorkingDir),
			EnvVars:            envVars,
			RunAsAdmin:         in.RunAsAdmin,
			HideConsole:        in.HideConsole,
			PromptArgs:         in.PromptArgs,
			IsPrimary:          in.IsPrimary,
			SortOrder:          i,
			PreLaunchCommand:   strings.TrimSpace(in.PreLaunchCommand),
			PreLaunchCondition: normalizePreLaunchCondition(in.PreLaunchCondition),
			ReadyCheckPath:     strings.TrimSpace(in.ReadyCheckPath),
		})
	}
	if err := s.repo.ReplaceAppEntries(appID, entries); err != nil {
		return nil, err
	}
	s.RefreshIndex()
	return s.GetApp(appID)
}

// TogglePin pins or unpins an app.
func (s *AppService) TogglePin(appID string, pinned bool) error {
	if err := s.repo.SetPinned(appID, pinned); err != nil {
		return err
	}
	s.RefreshIndex()
	return nil
}

// DeleteApp removes an app from OneApp and optionally deletes its managed library folder.
func (s *AppService) DeleteApp(appID string, deleteManagedFiles bool) error {
	ov, err := s.repo.GetApp(appID)
	if err != nil {
		return err
	}
	_ = s.launch.TerminateApp(appID)

	cfg := s.settings.Get()
	absRoot := storage.ResolveAppRoot(s.paths, cfg, ov.App)
	iconPath := ""
	if ov.IconRelPath != "" {
		iconPath = filepath.Join(s.paths.IconsDir, ov.IconRelPath)
	}

	if err := s.repo.DeleteApp(appID); err != nil {
		return err
	}

	if iconPath != "" {
		_ = os.Remove(iconPath)
	}
	if deleteManagedFiles && ov.StorageMode == domain.StorageManaged && absRoot != "" {
		libApps := filepath.Clean(s.paths.LibraryAppsDir(cfg))
		if strings.HasPrefix(strings.ToLower(absRoot), strings.ToLower(libApps+string(filepath.Separator))) {
			_ = os.RemoveAll(absRoot)
		}
	}

	s.RefreshIndex()
	return nil
}

// ListCategories returns all user categories.
func (s *AppService) ListCategories() ([]domain.Category, error) {
	return s.repo.ListCategories()
}

// CreateCategory creates a new category.
func (s *AppService) CreateCategory(name string) (*domain.Category, error) {
	c, err := s.repo.CreateCategory(name)
	if err != nil {
		return nil, err
	}
	s.RefreshIndex()
	return c, nil
}

// RenameCategory renames an existing category.
func (s *AppService) RenameCategory(id, name string) error {
	if err := s.repo.RenameCategory(id, name); err != nil {
		return err
	}
	s.RefreshIndex()
	return nil
}

// DeleteCategory removes a category.
func (s *AppService) DeleteCategory(id string) error {
	if err := s.repo.DeleteCategory(id); err != nil {
		return err
	}
	s.RefreshIndex()
	return nil
}

// AnalyzePaths inspects dropped or picked file/folder/archive paths.
func (s *AppService) AnalyzePaths(paths []string) ([]domain.IngestAnalysis, error) {
	return s.ingest.AnalyzePaths(paths)
}

// CommitIngest commits confirmed items into OneApp.
func (s *AppService) CommitIngest(items []domain.IngestCommitItem) domain.IngestResult {
	res := s.ingest.CommitIngest(items)
	if len(res.Apps) > 0 {
		s.RefreshIndex()
	}
	return res
}

// CreateCommandApp directly creates a linked project/command application.
func (s *AppService) CreateCommandApp(req domain.CreateCommandAppRequest) (*domain.AppOverview, error) {
	return s.ingest.CreateCommandApp(req)
}

// PickDirectoryPath opens a native directory picker and returns the chosen path.
func (s *AppService) PickDirectoryPath() (string, error) {
	if s.ui == nil {
		return "", fmt.Errorf("UI host unavailable")
	}
	return s.ui.PickFolder()
}

// GetAppLogs returns the captured build and runtime logs for an app.
func (s *AppService) GetAppLogs(appID string) domain.AppLogSnapshot {
	return s.launch.GetAppLogs(appID)
}

// ClearAppLogs clears the captured build and runtime logs for an app.
func (s *AppService) ClearAppLogs(appID string) error {
	return s.launch.ClearAppLogs(appID)
}

// Search queries the fast in-memory search index.
func (s *AppService) Search(query string, limit int) []domain.SearchHit {
	return s.search.Search(query, limit)
}

// LaunchApp executes an app entry.
func (s *AppService) LaunchApp(req domain.LaunchRequest) (int, error) {
	return s.launch.LaunchApp(req)
}

// TerminateApp terminates active processes for appID.
func (s *AppService) TerminateApp(appID string) error {
	return s.launch.TerminateApp(appID)
}

// RestartApp restarts appID.
func (s *AppService) RestartApp(appID string) (int, error) {
	return s.launch.RestartApp(appID)
}

// RevealAppDir opens Explorer highlighting the app's executable/root directory.
func (s *AppService) RevealAppDir(appID string) error {
	return s.launch.RevealAppDir(appID)
}

// OpenAppTerminal opens a terminal inside the app's working directory.
func (s *AppService) OpenAppTerminal(appID string) error {
	return s.launch.OpenAppTerminal(appID)
}

// ReextractIcon extracts the icon again from the app's primary executable, shortcut, or project root.
func (s *AppService) ReextractIcon(appID string) (*domain.AppOverview, error) {
	ov, err := s.repo.GetApp(appID)
	if err != nil {
		return nil, err
	}
	entry, ok := pickEntry(ov.Entries, "")
	if !ok {
		return nil, fmt.Errorf("未找到主入口")
	}
	cfg := s.settings.Get()
	appRoot := storage.ResolveAppRoot(s.paths, cfg, ov.App)
	var iconSource string
	if entry.EntryType == domain.EntryCommand {
		if proj, ok := analyzeNodeOrElectronProject(appRoot); ok {
			iconSource = proj.IconSourcePath
		} else {
			iconSource = discoverProjectIcon(appRoot, packageJSON{}, nil, "", false)
		}
		if iconSource == "" {
			return nil, fmt.Errorf("命令入口未检测到项目图标，请使用「自定义图标」")
		}
	} else {
		iconSource = storage.ResolveEntryPath(appRoot, entry)
		if entry.EntryType == domain.EntryLnk {
			if sc, scErr := s.bridge.ResolveShortcut(iconSource); scErr == nil && sc.TargetPath != "" {
				iconSource = sc.TargetPath
			}
		}
	}
	outPath := filepath.Join(s.paths.IconsDir, appID+".png")
	if strings.EqualFold(filepath.Ext(iconSource), ".png") {
		if err := archive.CopyFile(iconSource, outPath); err != nil {
			return nil, err
		}
	} else {
		if err := s.bridge.ExtractIcon(iconSource, outPath); err != nil {
			return nil, err
		}
	}
	if _, statErr := os.Stat(outPath); statErr == nil {
		_ = s.repo.UpdateIconRelPath(appID, appID+".png")
	}
	s.RefreshIndex()
	return s.GetApp(appID)
}

// PickFilesForIngest opens a native multi-file picker and returns path analyses.
func (s *AppService) PickFilesForIngest() ([]domain.IngestAnalysis, error) {
	if s.ui == nil {
		return nil, fmt.Errorf("UI host unavailable")
	}
	paths, err := s.ui.PickFiles()
	if err != nil || len(paths) == 0 {
		return []domain.IngestAnalysis{}, err
	}
	return s.ingest.AnalyzePaths(paths)
}

// PickFolderForIngest opens a native folder picker and returns path analysis.
func (s *AppService) PickFolderForIngest() ([]domain.IngestAnalysis, error) {
	if s.ui == nil {
		return nil, fmt.Errorf("UI host unavailable")
	}
	dir, err := s.ui.PickFolder()
	if err != nil || strings.TrimSpace(dir) == "" {
		return []domain.IngestAnalysis{}, err
	}
	return s.ingest.AnalyzePaths([]string{dir})
}

// PickCustomIcon opens a picker for a PNG/ICO/EXE file and updates the app icon.
func (s *AppService) PickCustomIcon(appID string) (*domain.AppOverview, error) {
	if s.ui == nil {
		return nil, fmt.Errorf("UI host unavailable")
	}
	picked, err := s.ui.PickIconFile()
	if err != nil || strings.TrimSpace(picked) == "" {
		return s.GetApp(appID)
	}
	outPath := filepath.Join(s.paths.IconsDir, appID+".png")
	if strings.EqualFold(filepath.Ext(picked), ".png") {
		if err := archive.CopyFile(picked, outPath); err != nil {
			return nil, err
		}
	} else {
		if err := s.bridge.ExtractIcon(picked, outPath); err != nil {
			return nil, err
		}
	}
	if _, statErr := os.Stat(outPath); statErr == nil {
		_ = s.repo.UpdateIconRelPath(appID, appID+".png")
	}
	s.RefreshIndex()
	return s.GetApp(appID)
}

// PickEntryFile opens a file picker anchored to the app's root directory and returns a relative path when inside root.
func (s *AppService) PickEntryFile(appID string) (string, error) {
	if s.ui == nil {
		return "", fmt.Errorf("UI host unavailable")
	}
	ov, err := s.repo.GetApp(appID)
	if err != nil {
		return "", err
	}
	cfg := s.settings.Get()
	appRoot := storage.ResolveAppRoot(s.paths, cfg, ov.App)
	picked, err := s.ui.PickEntryFile(appRoot)
	if err != nil || strings.TrimSpace(picked) == "" {
		return "", err
	}
	if appRoot != "" {
		if rel, relErr := filepath.Rel(appRoot, picked); relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel), nil
		}
	}
	return picked, nil
}

func (s *AppService) hydrateApp(a domain.AppOverview, cfg config.Settings, running map[string][]int, withSize bool) domain.AppOverview {
	a.ResolvedRoot = storage.ResolveAppRoot(s.paths, cfg, a.App)
	if a.IconRelPath != "" {
		a.IconURL = fmt.Sprintf("/icons/%s?v=%d", a.IconRelPath, a.UpdatedAt)
	}
	if pids := running[a.ID]; len(pids) > 0 {
		a.IsRunning = true
		a.RunningPIDs = append([]int(nil), pids...)
	} else {
		a.IsRunning = false
		a.RunningPIDs = []int{}
	}
	a.IsBuilding, a.BuildMessage = s.launch.GetBuildingState(a.ID)
	if withSize && a.ResolvedRoot != "" && a.StorageMode == domain.StorageManaged {
		a.DiskSizeBytes = archive.DirSize(a.ResolvedRoot)
	}
	return a
}

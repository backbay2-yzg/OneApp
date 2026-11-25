package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"oneapp/internal/config"
	"oneapp/internal/domain"
	"oneapp/internal/platform"
	"oneapp/internal/storage"
)

const maxAppLogLines = 500

type runningProc struct {
	pid         int
	expectedExe string
	entryID     string
}

type buildTask struct {
	cancel  context.CancelFunc
	message string
}

type appLogState struct {
	status      string
	lastCommand string
	updatedAt   int64
	lines       []string
	lastError   string
}

// LaunchService executes app entries, handles pre-launch builds, tracks running PIDs/logs, and handles directory/terminal actions.
type LaunchService struct {
	paths            *config.Paths
	settings         *config.SettingsStore
	repo             *storage.Repo
	bridge           platform.OSBridge
	mu               sync.Mutex
	logFileMu        sync.Mutex
	running          map[string][]runningProc
	building         map[string]*buildTask
	logs             map[string]*appLogState
	onAppChanged     func()
	onProcessChanged func(map[string][]int)
	onLogChanged     func(domain.AppLogSnapshot)
	onError          func(string)
}

// NewLaunchService constructs a LaunchService.
func NewLaunchService(
	paths *config.Paths,
	settings *config.SettingsStore,
	repo *storage.Repo,
	bridge platform.OSBridge,
	onAppChanged func(),
	onProcessChanged func(map[string][]int),
) *LaunchService {
	return &LaunchService{
		paths:            paths,
		settings:         settings,
		repo:             repo,
		bridge:           bridge,
		running:          make(map[string][]runningProc),
		building:         make(map[string]*buildTask),
		logs:             make(map[string]*appLogState),
		onAppChanged:     onAppChanged,
		onProcessChanged: onProcessChanged,
	}
}

// SetLogCallbacks attaches optional callbacks for log updates and async build/launch errors.
func (l *LaunchService) SetLogCallbacks(onLogChanged func(domain.AppLogSnapshot), onError func(string)) {
	l.mu.Lock()
	l.onLogChanged = onLogChanged
	l.onError = onError
	l.mu.Unlock()
}

// StartMonitor polls tracked PIDs every 2 seconds until ctx is cancelled.
func (l *LaunchService) StartMonitor(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.pruneDeadProcesses()
		}
	}
}

func (l *LaunchService) pruneDeadProcesses() {
	l.mu.Lock()
	snapshot := make(map[string][]runningProc, len(l.running))
	for appID, procs := range l.running {
		snapshot[appID] = append([]runningProc(nil), procs...)
	}
	l.mu.Unlock()

	if len(snapshot) == 0 {
		return
	}

	changed := false
	nextMap := make(map[string][]runningProc)
	for appID, procs := range snapshot {
		alive := make([]runningProc, 0, len(procs))
		for _, p := range procs {
			if l.bridge.IsProcessAlive(p.pid, p.expectedExe) {
				alive = append(alive, p)
			} else {
				changed = true
			}
		}
		if len(alive) > 0 {
			nextMap[appID] = alive
		}
	}

	if !changed {
		return
	}

	var exitedLogSnaps []domain.AppLogSnapshot
	l.mu.Lock()
	// Merge any newly launched processes started while checking alive status.
	for appID, procs := range l.running {
		for _, p := range procs {
			alreadyKept := false
			for _, np := range nextMap[appID] {
				if np.pid == p.pid {
					alreadyKept = true
					break
				}
			}
			inSnapshot := false
			for _, sp := range snapshot[appID] {
				if sp.pid == p.pid {
					inSnapshot = true
					break
				}
			}
			if !alreadyKept && !inSnapshot {
				nextMap[appID] = append(nextMap[appID], p)
			}
		}
	}
	l.running = nextMap
	runningSnapshot := l.snapshotLocked()

	for appID := range snapshot {
		if len(nextMap[appID]) == 0 && l.building[appID] == nil {
			if st, ok := l.logs[appID]; ok && st.status == "running" {
				st.status = "exited"
				st.updatedAt = time.Now().UnixMilli()
				exitedLogSnaps = append(exitedLogSnaps, cloneLogSnapshot(appID, st))
			}
		}
	}
	onProc := l.onProcessChanged
	onLog := l.onLogChanged
	l.mu.Unlock()

	if onProc != nil {
		onProc(runningSnapshot)
	}
	if onLog != nil {
		for _, snap := range exitedLogSnaps {
			onLog(snap)
		}
	}
}

// GetRunningMap returns a copy of active appID -> PIDs.
func (l *LaunchService) GetRunningMap() map[string][]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.snapshotLocked()
}

// GetBuildingState returns whether appID is currently executing a pre-launch build and its message.
func (l *LaunchService) GetBuildingState(appID string) (bool, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if bt, ok := l.building[appID]; ok && bt != nil {
		return true, bt.message
	}
	return false, ""
}

// GetRunningStates returns a slice of RunningAppState for frontend hydration (including building apps).
func (l *LaunchService) GetRunningStates() []domain.RunningAppState {
	l.mu.Lock()
	defer l.mu.Unlock()

	seen := make(map[string]bool, len(l.running)+len(l.building))
	ids := make([]string, 0, len(l.running)+len(l.building))
	for appID := range l.running {
		seen[appID] = true
		ids = append(ids, appID)
	}
	for appID := range l.building {
		if !seen[appID] {
			seen[appID] = true
			ids = append(ids, appID)
		}
	}
	sort.Strings(ids)

	out := make([]domain.RunningAppState, 0, len(ids))
	for _, appID := range ids {
		procs := l.running[appID]
		pids := make([]int, 0, len(procs))
		for _, p := range procs {
			pids = append(pids, p.pid)
		}
		var isBuilding bool
		var buildMsg string
		if bt := l.building[appID]; bt != nil {
			isBuilding = true
			buildMsg = bt.message
		}
		if len(pids) > 0 || isBuilding {
			out = append(out, domain.RunningAppState{
				AppID:        appID,
				PIDs:         pids,
				IsBuilding:   isBuilding,
				BuildMessage: buildMsg,
			})
		}
	}
	return out
}

func (l *LaunchService) snapshotLocked() map[string][]int {
	out := make(map[string][]int, len(l.running))
	for appID, procs := range l.running {
		pids := make([]int, 0, len(procs))
		for _, p := range procs {
			pids = append(pids, p.pid)
		}
		if len(pids) > 0 {
			out[appID] = pids
		}
	}
	return out
}

func (l *LaunchService) notifyProcessChanged() {
	l.mu.Lock()
	snap := l.snapshotLocked()
	cb := l.onProcessChanged
	l.mu.Unlock()
	if cb != nil {
		cb(snap)
	}
}

// LaunchApp executes the requested entry (or primary entry if EntryID is empty), running any configured pre-launch build asynchronously when needed.
func (l *LaunchService) LaunchApp(req domain.LaunchRequest) (int, error) {
	ov, err := l.repo.GetApp(req.AppID)
	if err != nil {
		return 0, err
	}
	entry, ok := pickEntry(ov.Entries, req.EntryID)
	if !ok {
		return 0, fmt.Errorf("该软件尚未配置可启动入口")
	}

	l.mu.Lock()
	if bt, isBuilding := l.building[ov.ID]; isBuilding && bt != nil {
		msg := bt.message
		l.mu.Unlock()
		if msg == "" {
			msg = "正在构建中"
		}
		return 0, fmt.Errorf("%s，请稍候或先取消当前构建", msg)
	}
	l.mu.Unlock()

	cfg := l.settings.Get()
	appRoot := storage.ResolveAppRoot(l.paths, cfg, ov.App)
	entryFull := storage.ResolveEntryPath(appRoot, entry)
	workDir := storage.ResolveWorkingDir(appRoot, entry, entryFull)

	if entry.EntryType == domain.EntryCommand {
		if strings.TrimSpace(entryFull) == "" {
			return 0, fmt.Errorf("启动命令不能为空")
		}
		if workDir != "" {
			if info, statErr := os.Stat(workDir); statErr != nil || !info.IsDir() {
				return 0, fmt.Errorf("工作目录不存在: %s", workDir)
			}
		}
	} else {
		if _, statErr := os.Stat(entryFull); statErr != nil {
			if strings.EqualFold(filepath.Base(entryFull), "electron.exe") {
				return 0, fmt.Errorf("未找到本地 Electron 可执行文件 (%s)，请先在项目目录安装依赖（如 pnpm install）", entryFull)
			}
			return 0, fmt.Errorf("入口文件不存在: %s", entryFull)
		}
	}

	preCmd := strings.TrimSpace(entry.PreLaunchCommand)
	preCond := strings.ToLower(strings.TrimSpace(entry.PreLaunchCondition))
	needsBuild := false
	restartAfterBuild := false

	if preCmd != "" {
		switch preCond {
		case "always":
			needsBuild = true
			restartAfterBuild = true
		case "if_missing":
			readyPath := storage.ResolvePathInRoot(appRoot, entry.ReadyCheckPath)
			if readyPath == "" {
				needsBuild = true
			} else if _, statErr := os.Stat(readyPath); statErr != nil {
				needsBuild = true
			}
		}
	}

	entryDirAnchor := entryFull
	if entry.EntryType == domain.EntryCommand {
		entryDirAnchor = filepath.Join(appRoot, "command")
	}

	if needsBuild {
		oneAppRoot := ""
		if l.paths != nil {
			oneAppRoot = l.paths.RootDir
		}
		expandedPreCmd := ExpandArgsTemplate(preCmd, appRoot, entryDirAnchor, oneAppRoot)
		buildCtx, cancel := context.WithCancel(context.Background())
		buildMsg := fmt.Sprintf("正在构建 (%s)...", expandedPreCmd)

		l.mu.Lock()
		l.building[ov.ID] = &buildTask{
			cancel:  cancel,
			message: buildMsg,
		}
		l.mu.Unlock()

		l.initLogSession(ov.ID, "building", expandedPreCmd, false)
		l.appendLogLine(ov.ID, fmt.Sprintf("[OneApp] 执行前置构建: %s", expandedPreCmd), true)
		l.notifyProcessChanged()

		go l.runBuildAndLaunch(buildCtx, cancel, ov, entry, appRoot, entryFull, workDir, req, expandedPreCmd, restartAfterBuild)
		return 0, nil
	}

	cmdDisplay := formatEntryDisplayCommand(entry, entryFull, req.ExtraArgs)
	l.initLogSession(ov.ID, "running", cmdDisplay, false)
	l.appendLogLine(ov.ID, fmt.Sprintf("[OneApp] 启动: %s", cmdDisplay), true)

	pid, launchErr := l.executeLaunch(ov, entry, appRoot, entryFull, workDir, req, true)
	if launchErr != nil {
		l.appendLogLine(ov.ID, "[OneApp] 启动失败: "+launchErr.Error(), true)
		l.setLogStatus(ov.ID, "failed", launchErr.Error())
		return 0, launchErr
	}
	return pid, nil
}

func (l *LaunchService) runBuildAndLaunch(
	ctx context.Context,
	cancel context.CancelFunc,
	ov *domain.AppOverview,
	entry domain.AppEntry,
	appRoot string,
	entryFull string,
	workDir string,
	req domain.LaunchRequest,
	expandedPreCmd string,
	restartAfterBuild bool,
) {
	defer cancel()

	var unsetVars []string
	if !hasEnvKeyCI(entry.EnvVars, "ELECTRON_RUN_AS_NODE") {
		unsetVars = []string{"ELECTRON_RUN_AS_NODE"}
	}

	buildSpec := platform.LaunchSpec{
		EntryType:    domain.EntryCommand,
		TargetPath:   expandedPreCmd,
		WorkingDir:   workDir,
		EnvVars:      entry.EnvVars,
		UnsetEnvVars: unsetVars,
		HideConsole:  true,
	}

	buildErr := l.bridge.RunCommand(ctx, buildSpec, func(line string) {
		l.appendLogLine(ov.ID, line, true)
	})

	l.mu.Lock()
	if cur, ok := l.building[ov.ID]; ok && cur != nil {
		delete(l.building, ov.ID)
	}
	onErr := l.onError
	l.mu.Unlock()

	if buildErr != nil {
		if errors.Is(buildErr, context.Canceled) || ctx.Err() == context.Canceled {
			l.appendLogLine(ov.ID, "[OneApp] 已取消构建", true)
			l.setLogStatus(ov.ID, "exited", "已取消构建")
			l.notifyProcessChanged()
			return
		}
		summary := l.recentNonEmptySummary(ov.ID, 3)
		errText := fmt.Sprintf("构建失败 (%s): %v", expandedPreCmd, buildErr)
		if summary != "" {
			errText = fmt.Sprintf("%s\n%s", errText, summary)
		}
		l.appendLogLine(ov.ID, "[OneApp] "+errText, true)
		l.setLogStatus(ov.ID, "failed", errText)
		l.notifyProcessChanged()
		if onErr != nil {
			onErr(errText)
		}
		return
	}

	l.appendLogLine(ov.ID, "[OneApp] 构建成功，正在启动...", true)
	l.notifyProcessChanged()

	if restartAfterBuild {
		l.mu.Lock()
		hasRunning := len(l.running[ov.ID]) > 0
		l.mu.Unlock()
		if hasRunning {
			l.appendLogLine(ov.ID, "[OneApp] 正在关闭旧实例...", true)
			_ = l.terminateRunningOnly(ov.ID)
			time.Sleep(250 * time.Millisecond)
		}
	}

	cmdDisplay := formatEntryDisplayCommand(entry, entryFull, req.ExtraArgs)
	l.appendLogLine(ov.ID, fmt.Sprintf("[OneApp] 启动: %s", cmdDisplay), true)
	l.setLogCommandAndStatus(ov.ID, cmdDisplay, "running")

	_, launchErr := l.executeLaunch(ov, entry, appRoot, entryFull, workDir, req, true)
	if launchErr != nil {
		errText := "启动失败: " + launchErr.Error()
		l.appendLogLine(ov.ID, "[OneApp] "+errText, true)
		l.setLogStatus(ov.ID, "failed", errText)
		l.notifyProcessChanged()
		if onErr != nil {
			onErr(errText)
		}
	}
}

func (l *LaunchService) executeLaunch(
	ov *domain.AppOverview,
	entry domain.AppEntry,
	appRoot string,
	entryFull string,
	workDir string,
	req domain.LaunchRequest,
	logAppend bool,
) (int, error) {
	entryDirAnchor := entryFull
	if entry.EntryType == domain.EntryCommand {
		entryDirAnchor = filepath.Join(appRoot, "command")
	}
	oneAppRoot := ""
	if l.paths != nil {
		oneAppRoot = l.paths.RootDir
	}

	expandedTpl := ExpandArgsTemplate(entry.ArgsTemplate, appRoot, entryDirAnchor, oneAppRoot)
	rawArgsLine := strings.TrimSpace(strings.TrimSpace(expandedTpl) + " " + strings.TrimSpace(req.ExtraArgs))
	argsSlice := SplitArgs(rawArgsLine)

	elevated := entry.RunAsAdmin || req.ForceAdmin

	var unsetVars []string
	if shouldStripElectronRunAsNode(entry, entryFull) && !hasEnvKeyCI(entry.EnvVars, "ELECTRON_RUN_AS_NODE") {
		unsetVars = []string{"ELECTRON_RUN_AS_NODE"}
	}

	logPath := ""
	if entry.HideConsole && !elevated {
		logPath = l.logFilePath(ov.ID)
	}
	var startOffset int64
	if logPath != "" && logAppend {
		if info, err := os.Stat(logPath); err == nil {
			startOffset = info.Size()
		}
	}

	pid, err := l.bridge.Launch(platform.LaunchSpec{
		EntryType:    entry.EntryType,
		TargetPath:   entryFull,
		Args:         argsSlice,
		RawArgsLine:  rawArgsLine,
		WorkingDir:   workDir,
		EnvVars:      entry.EnvVars,
		UnsetEnvVars: unsetVars,
		RunAsAdmin:   elevated,
		HideConsole:  entry.HideConsole,
		LogFilePath:  logPath,
		LogAppend:    logAppend,
	})
	if err != nil {
		return 0, err
	}

	expectedExe := ""
	switch entry.EntryType {
	case domain.EntryExe:
		expectedExe = entryFull
	case domain.EntryLnk:
		if sc, scErr := l.bridge.ResolveShortcut(entryFull); scErr == nil && sc.TargetPath != "" {
			expectedExe = sc.TargetPath
		}
	}

	if pid > 0 {
		l.mu.Lock()
		l.running[ov.ID] = append(l.running[ov.ID], runningProc{
			pid:         pid,
			expectedExe: expectedExe,
			entryID:     entry.ID,
		})
		snap := l.snapshotLocked()
		onProc := l.onProcessChanged
		l.mu.Unlock()
		if onProc != nil {
			onProc(snap)
		}
		if logPath != "" {
			go l.tailProcessLog(ov.ID, pid, expectedExe, logPath, startOffset)
		}
	}

	_ = l.repo.RecordLaunch(ov.ID, entry.ID, elevated, time.Now().UnixMilli())
	if l.onAppChanged != nil {
		l.onAppChanged()
	}
	return pid, nil
}

// TerminateApp cancels any active build and stops all tracked running PIDs for appID.
func (l *LaunchService) TerminateApp(appID string) error {
	l.mu.Lock()
	bt := l.building[appID]
	if bt != nil {
		delete(l.building, appID)
	}
	l.mu.Unlock()

	if bt != nil && bt.cancel != nil {
		bt.cancel()
		l.appendLogLine(appID, "[OneApp] 已请求取消构建", true)
		l.setLogStatus(appID, "exited", "已取消构建")
		l.notifyProcessChanged()
	}

	return l.terminateRunningOnly(appID)
}

func (l *LaunchService) terminateRunningOnly(appID string) error {
	l.mu.Lock()
	procs := append([]runningProc(nil), l.running[appID]...)
	l.mu.Unlock()

	if len(procs) == 0 {
		return nil
	}

	var firstErr error
	for _, p := range procs {
		if l.bridge.IsProcessAlive(p.pid, p.expectedExe) {
			if err := l.bridge.TerminateProcess(p.pid); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}

	l.mu.Lock()
	delete(l.running, appID)
	snap := l.snapshotLocked()
	if st, ok := l.logs[appID]; ok && st.status == "running" && l.building[appID] == nil {
		st.status = "exited"
		st.updatedAt = time.Now().UnixMilli()
	}
	logSnap, hasLog := l.logs[appID]
	var copiedLog domain.AppLogSnapshot
	if hasLog {
		copiedLog = cloneLogSnapshot(appID, logSnap)
	}
	onProc := l.onProcessChanged
	onLog := l.onLogChanged
	l.mu.Unlock()

	if onProc != nil {
		onProc(snap)
	}
	if hasLog && onLog != nil {
		onLog(copiedLog)
	}
	return firstErr
}

// RestartApp terminates tracked processes for appID and launches its primary entry.
func (l *LaunchService) RestartApp(appID string) (int, error) {
	_ = l.TerminateApp(appID)
	time.Sleep(250 * time.Millisecond)
	return l.LaunchApp(domain.LaunchRequest{AppID: appID})
}

// RevealAppDir opens the app's primary entry or root folder in the OS file manager.
func (l *LaunchService) RevealAppDir(appID string) error {
	ov, err := l.repo.GetApp(appID)
	if err != nil {
		return err
	}
	cfg := l.settings.Get()
	appRoot := storage.ResolveAppRoot(l.paths, cfg, ov.App)
	if entry, ok := pickEntry(ov.Entries, ""); ok && entry.EntryType != domain.EntryCommand {
		entryFull := storage.ResolveEntryPath(appRoot, entry)
		if _, err := os.Stat(entryFull); err == nil {
			return l.bridge.RevealInFileManager(entryFull)
		}
	}
	return l.bridge.RevealInFileManager(appRoot)
}

// OpenAppTerminal opens a terminal inside the app's working directory.
func (l *LaunchService) OpenAppTerminal(appID string) error {
	ov, err := l.repo.GetApp(appID)
	if err != nil {
		return err
	}
	cfg := l.settings.Get()
	appRoot := storage.ResolveAppRoot(l.paths, cfg, ov.App)
	dir := appRoot
	if entry, ok := pickEntry(ov.Entries, ""); ok {
		entryFull := storage.ResolveEntryPath(appRoot, entry)
		wd := storage.ResolveWorkingDir(appRoot, entry, entryFull)
		if wd != "" {
			dir = wd
		}
	}
	return l.bridge.OpenTerminal(dir)
}

// GetAppLogs returns the captured build and runtime log snapshot for appID.
func (l *LaunchService) GetAppLogs(appID string) domain.AppLogSnapshot {
	l.mu.Lock()
	if st, ok := l.logs[appID]; ok && st != nil {
		snap := cloneLogSnapshot(appID, st)
		l.mu.Unlock()
		return snap
	}
	l.mu.Unlock()

	logPath := l.logFilePath(appID)
	if logPath != "" {
		if data, err := os.ReadFile(logPath); err == nil {
			rawLines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
			for len(rawLines) > 0 && rawLines[len(rawLines)-1] == "" {
				rawLines = rawLines[:len(rawLines)-1]
			}
			if len(rawLines) > maxAppLogLines {
				rawLines = rawLines[len(rawLines)-maxAppLogLines:]
			}
			var updatedAt int64
			if info, statErr := os.Stat(logPath); statErr == nil {
				updatedAt = info.ModTime().UnixMilli()
			}
			return domain.AppLogSnapshot{
				AppID:     appID,
				Status:    "idle",
				UpdatedAt: updatedAt,
				Lines:     rawLines,
			}
		}
	}

	return domain.AppLogSnapshot{
		AppID:  appID,
		Status: "idle",
		Lines:  []string{},
	}
}

// ClearAppLogs clears in-memory and on-disk logs for appID.
func (l *LaunchService) ClearAppLogs(appID string) error {
	l.mu.Lock()
	st, ok := l.logs[appID]
	if !ok || st == nil {
		st = &appLogState{status: "idle"}
		l.logs[appID] = st
	}
	if l.building[appID] != nil {
		st.status = "building"
	} else if len(l.running[appID]) > 0 {
		st.status = "running"
	} else {
		st.status = "idle"
	}
	st.lines = []string{}
	st.lastError = ""
	st.updatedAt = time.Now().UnixMilli()
	snap := cloneLogSnapshot(appID, st)
	onLog := l.onLogChanged
	l.mu.Unlock()

	if logPath := l.logFilePath(appID); logPath != "" {
		l.logFileMu.Lock()
		_ = os.Remove(logPath)
		l.logFileMu.Unlock()
	}

	if onLog != nil {
		onLog(snap)
	}
	return nil
}

func (l *LaunchService) logFilePath(appID string) string {
	if l.paths == nil || strings.TrimSpace(l.paths.LogsDir) == "" || strings.TrimSpace(appID) == "" {
		return ""
	}
	return filepath.Join(l.paths.LogsDir, fmt.Sprintf("app-%s.log", appID))
}

func (l *LaunchService) initLogSession(appID, status, command string, appendMode bool) {
	now := time.Now().UnixMilli()
	l.mu.Lock()
	st, ok := l.logs[appID]
	if !ok || st == nil {
		st = &appLogState{}
		l.logs[appID] = st
	}
	if !appendMode {
		st.lines = []string{}
		st.lastError = ""
	}
	st.status = status
	st.lastCommand = command
	st.updatedAt = now
	snap := cloneLogSnapshot(appID, st)
	onLog := l.onLogChanged
	l.mu.Unlock()

	if !appendMode {
		if logPath := l.logFilePath(appID); logPath != "" {
			l.logFileMu.Lock()
			if mkErr := os.MkdirAll(filepath.Dir(logPath), 0o755); mkErr == nil {
				_ = os.WriteFile(logPath, nil, 0o644)
			}
			l.logFileMu.Unlock()
		}
	}

	if onLog != nil {
		onLog(snap)
	}
}

func (l *LaunchService) setLogStatus(appID, status, lastError string) {
	l.mu.Lock()
	st, ok := l.logs[appID]
	if !ok || st == nil {
		st = &appLogState{}
		l.logs[appID] = st
	}
	st.status = status
	st.lastError = lastError
	st.updatedAt = time.Now().UnixMilli()
	snap := cloneLogSnapshot(appID, st)
	onLog := l.onLogChanged
	l.mu.Unlock()

	if onLog != nil {
		onLog(snap)
	}
}

func (l *LaunchService) setLogCommandAndStatus(appID, command, status string) {
	l.mu.Lock()
	st, ok := l.logs[appID]
	if !ok || st == nil {
		st = &appLogState{}
		l.logs[appID] = st
	}
	st.lastCommand = command
	st.status = status
	st.updatedAt = time.Now().UnixMilli()
	snap := cloneLogSnapshot(appID, st)
	onLog := l.onLogChanged
	l.mu.Unlock()

	if onLog != nil {
		onLog(snap)
	}
}

func (l *LaunchService) appendLogLine(appID, line string, writeToFile bool) {
	cleanLine := strings.TrimRight(line, "\r\n")
	l.mu.Lock()
	st, ok := l.logs[appID]
	if !ok || st == nil {
		st = &appLogState{status: "idle"}
		l.logs[appID] = st
	}
	st.lines = append(st.lines, cleanLine)
	if len(st.lines) > maxAppLogLines {
		st.lines = append([]string(nil), st.lines[len(st.lines)-maxAppLogLines:]...)
	}
	st.updatedAt = time.Now().UnixMilli()
	snap := cloneLogSnapshot(appID, st)
	onLog := l.onLogChanged
	l.mu.Unlock()

	if writeToFile {
		if logPath := l.logFilePath(appID); logPath != "" {
			l.logFileMu.Lock()
			if mkErr := os.MkdirAll(filepath.Dir(logPath), 0o755); mkErr == nil {
				if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
					_, _ = f.WriteString(cleanLine + "\n")
					_ = f.Close()
				}
			}
			l.logFileMu.Unlock()
		}
	}

	if onLog != nil {
		onLog(snap)
	}
}

func (l *LaunchService) recentNonEmptySummary(appID string, limit int) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.logs[appID]
	if !ok || st == nil || len(st.lines) == 0 {
		return ""
	}
	out := make([]string, 0, limit)
	for i := len(st.lines) - 1; i >= 0 && len(out) < limit; i-- {
		trimmed := strings.TrimSpace(st.lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "[OneApp]") {
			continue
		}
		out = append(out, trimmed)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return strings.Join(out, "\n")
}

func (l *LaunchService) tailProcessLog(appID string, pid int, expectedExe string, logPath string, offset int64) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	var carry string
	readNew := func() {
		f, err := os.Open(logPath)
		if err != nil {
			return
		}
		defer f.Close()

		info, err := f.Stat()
		if err != nil || info.Size() <= offset {
			return
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return
		}
		chunk, err := io.ReadAll(io.LimitReader(f, 512*1024))
		if err != nil || len(chunk) == 0 {
			return
		}
		offset += int64(len(chunk))

		text := carry + strings.ReplaceAll(string(chunk), "\r\n", "\n")
		parts := strings.Split(text, "\n")
		carry = parts[len(parts)-1]
		for _, ln := range parts[:len(parts)-1] {
			if idx := strings.LastIndexByte(ln, '\r'); idx >= 0 {
				if tail := strings.TrimSpace(ln[idx+1:]); tail != "" {
					ln = tail
				} else {
					ln = strings.TrimSpace(strings.ReplaceAll(ln, "\r", ""))
				}
			}
			l.appendLogLine(appID, ln, false)
		}
	}

	// Perform an initial read after a short delay, then poll until process exits.
	for {
		<-ticker.C
		readNew()
		if !l.bridge.IsProcessAlive(pid, expectedExe) {
			// Final drain after process exits.
			time.Sleep(100 * time.Millisecond)
			readNew()
			if strings.TrimSpace(carry) != "" {
				l.appendLogLine(appID, carry, false)
				carry = ""
			}
			l.mu.Lock()
			stillRunning := false
			for _, p := range l.running[appID] {
				if p.pid != pid && l.bridge.IsProcessAlive(p.pid, p.expectedExe) {
					stillRunning = true
					break
				}
			}
			isBuilding := l.building[appID] != nil
			if !stillRunning && !isBuilding {
				if st, ok := l.logs[appID]; ok && st.status == "running" {
					st.status = "exited"
					st.updatedAt = time.Now().UnixMilli()
					snap := cloneLogSnapshot(appID, st)
					onLog := l.onLogChanged
					l.mu.Unlock()
					if onLog != nil {
						onLog(snap)
					}
					return
				}
			}
			l.mu.Unlock()
			return
		}
	}
}

func cloneLogSnapshot(appID string, st *appLogState) domain.AppLogSnapshot {
	if st == nil {
		return domain.AppLogSnapshot{AppID: appID, Status: "idle", Lines: []string{}}
	}
	lines := make([]string, len(st.lines))
	copy(lines, st.lines)
	return domain.AppLogSnapshot{
		AppID:       appID,
		Status:      st.status,
		LastCommand: st.lastCommand,
		UpdatedAt:   st.updatedAt,
		Lines:       lines,
		LastError:   st.lastError,
	}
}

func shouldStripElectronRunAsNode(entry domain.AppEntry, target string) bool {
	if entry.EntryType == domain.EntryCommand {
		return true
	}
	if strings.TrimSpace(entry.PreLaunchCommand) != "" {
		return true
	}
	return strings.EqualFold(filepath.Base(target), "electron.exe")
}

func hasEnvKeyCI(env map[string]string, targetKey string) bool {
	for k := range env {
		if strings.EqualFold(strings.TrimSpace(k), targetKey) {
			return true
		}
	}
	return false
}

func formatEntryDisplayCommand(entry domain.AppEntry, entryFull, extraArgs string) string {
	base := entryFull
	if entry.EntryType != domain.EntryCommand {
		base = entry.RelPath
	}
	parts := []string{base}
	if strings.TrimSpace(entry.ArgsTemplate) != "" {
		parts = append(parts, strings.TrimSpace(entry.ArgsTemplate))
	}
	if strings.TrimSpace(extraArgs) != "" {
		parts = append(parts, strings.TrimSpace(extraArgs))
	}
	return strings.Join(parts, " ")
}

func pickEntry(entries []domain.AppEntry, entryID string) (domain.AppEntry, bool) {
	if len(entries) == 0 {
		return domain.AppEntry{}, false
	}
	if entryID != "" {
		for _, e := range entries {
			if e.ID == entryID {
				return e, true
			}
		}
	}
	for _, e := range entries {
		if e.IsPrimary {
			return e, true
		}
	}
	return entries[0], true
}

// ExpandArgsTemplate substitutes built-in directory variables into a launch argument string.
func ExpandArgsTemplate(tpl, appRoot, entryFullPath, oneAppRoot string) string {
	out := strings.ReplaceAll(tpl, "{{APP_DIR}}", appRoot)
	out = strings.ReplaceAll(out, "{{ENTRY_DIR}}", filepath.Dir(entryFullPath))
	out = strings.ReplaceAll(out, "{{ONEAPP_DIR}}", oneAppRoot)
	return out
}

// SplitArgs splits a command-line string into tokens while preserving quoted substrings.
func SplitArgs(raw string) []string {
	var args []string
	var cur strings.Builder
	var quote rune
	inToken := false

	for _, r := range raw {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
			inToken = true
		case unicode.IsSpace(r):
			if inToken {
				args = append(args, cur.String())
				cur.Reset()
				inToken = false
			}
		default:
			cur.WriteRune(r)
			inToken = true
		}
	}
	if inToken {
		args = append(args, cur.String())
	}
	return args
}

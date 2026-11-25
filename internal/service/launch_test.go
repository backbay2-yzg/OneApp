package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"oneapp/internal/config"
	"oneapp/internal/domain"
	"oneapp/internal/platform"
	winbridge "oneapp/internal/platform/windows"
	"oneapp/internal/storage"
)

type mockLaunchBridge struct {
	mu           sync.Mutex
	launchCalls  []platform.LaunchSpec
	commandCalls []platform.LaunchSpec
	nextPID      int
	alivePIDs    map[int]bool
	terminated   []int
	revealed     []string
	terminalDirs []string
	runCmdFunc   func(ctx context.Context, spec platform.LaunchSpec, onOutput func(line string)) error
}

func newMockLaunchBridge() *mockLaunchBridge {
	return &mockLaunchBridge{
		nextPID:   2000,
		alivePIDs: make(map[int]bool),
	}
}

func (m *mockLaunchBridge) ExtractIcon(string, string) error { return nil }
func (m *mockLaunchBridge) ReadExecutableMeta(string) (platform.ExecMeta, error) {
	return platform.ExecMeta{}, nil
}
func (m *mockLaunchBridge) ResolveShortcut(string) (platform.ShortcutInfo, error) {
	return platform.ShortcutInfo{}, nil
}
func (m *mockLaunchBridge) Launch(spec platform.LaunchSpec) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.launchCalls = append(m.launchCalls, spec)
	m.nextPID++
	pid := m.nextPID
	m.alivePIDs[pid] = true
	return pid, nil
}
func (m *mockLaunchBridge) RunCommand(ctx context.Context, spec platform.LaunchSpec, onOutput func(line string)) error {
	m.mu.Lock()
	m.commandCalls = append(m.commandCalls, spec)
	fn := m.runCmdFunc
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, spec, onOutput)
	}
	if onOutput != nil {
		onOutput("build ok")
	}
	return nil
}
func (m *mockLaunchBridge) IsProcessAlive(pid int, _ string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.alivePIDs[pid]
}
func (m *mockLaunchBridge) TerminateProcess(pid int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.terminated = append(m.terminated, pid)
	delete(m.alivePIDs, pid)
	return nil
}
func (m *mockLaunchBridge) RevealInFileManager(path string) error {
	m.mu.Lock()
	m.revealed = append(m.revealed, path)
	m.mu.Unlock()
	return nil
}
func (m *mockLaunchBridge) OpenTerminal(dir string) error {
	m.mu.Lock()
	m.terminalDirs = append(m.terminalDirs, dir)
	m.mu.Unlock()
	return nil
}
func (m *mockLaunchBridge) SetAutoStart(string, string, bool) error { return nil }
func (m *mockLaunchBridge) ProtectSecret(p string) (string, error)  { return p, nil }
func (m *mockLaunchBridge) UnprotectSecret(c string) (string, error) {
	return c, nil
}

func setupLaunchTestEnv(t *testing.T, bridge platform.OSBridge) (*LaunchService, *storage.Repo, string, *config.Paths) {
	t.Helper()
	root := t.TempDir()
	paths, err := config.ResolvePaths(root)
	require.NoError(t, err)
	settingsStore, err := config.NewSettingsStore(paths.SettingsPath)
	require.NoError(t, err)
	require.NoError(t, paths.EnsureDirs(settingsStore.Get()))

	db, err := storage.OpenDB(paths.DBPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := storage.NewRepo(db)

	projDir := filepath.Join(root, "proj")
	require.NoError(t, os.MkdirAll(filepath.Join(projDir, "node_modules", "electron", "dist"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projDir, "node_modules", "electron", "dist", "electron.exe"), []byte("MZ"), 0o644))

	launchSvc := NewLaunchService(paths, settingsStore, repo, bridge, nil, nil)
	return launchSvc, repo, projDir, paths
}

func TestLaunchCommandAndPreBuildLifecycle(t *testing.T) {
	bridge := newMockLaunchBridge()
	launchSvc, repo, projDir, _ := setupLaunchTestEnv(t, bridge)

	var errMu sync.Mutex
	var asyncErrors []string
	launchSvc.SetLogCallbacks(nil, func(msg string) {
		errMu.Lock()
		asyncErrors = append(asyncErrors, msg)
		errMu.Unlock()
	})

	now := time.Now().UnixMilli()
	app := &domain.App{
		ID:          "electron-app",
		Slug:        "electron-app",
		Name:        "ElectronApp",
		StorageMode: domain.StorageLinked,
		Status:      domain.StatusReady,
		RootPath:    projDir,
		SourceType:  domain.SourceLocal,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	entries := []domain.AppEntry{
		{
			ID:                 "entry-direct",
			AppID:              app.ID,
			Name:               "直接启动 (Electron)",
			EntryType:          domain.EntryExe,
			RelPath:            "node_modules/electron/dist/electron.exe",
			ArgsTemplate:       ".",
			WorkingDir:         "{{APP_DIR}}",
			HideConsole:        true,
			IsPrimary:          true,
			PreLaunchCommand:   "pnpm run build",
			PreLaunchCondition: "if_missing",
			ReadyCheckPath:     "out/main/index.js",
		},
		{
			ID:                 "entry-rebuild",
			AppID:              app.ID,
			Name:               "重新构建并启动",
			EntryType:          domain.EntryExe,
			RelPath:            "node_modules/electron/dist/electron.exe",
			ArgsTemplate:       ".",
			WorkingDir:         "{{APP_DIR}}",
			HideConsole:        true,
			PreLaunchCommand:   "pnpm run build",
			PreLaunchCondition: "always",
			ReadyCheckPath:     "out/main/index.js",
		},
		{
			ID:          "entry-dev",
			AppID:       app.ID,
			Name:        "开发模式 (dev)",
			EntryType:   domain.EntryCommand,
			RelPath:     "pnpm run dev",
			WorkingDir:  "{{APP_DIR}}",
			HideConsole: true,
		},
	}
	require.NoError(t, repo.CreateApp(app, entries, nil, nil))

	// 1. EntryCommand launch uses project root and strips ELECTRON_RUN_AS_NODE.
	pid, err := launchSvc.LaunchApp(domain.LaunchRequest{AppID: app.ID, EntryID: "entry-dev"})
	require.NoError(t, err)
	require.Greater(t, pid, 0)
	require.Len(t, bridge.launchCalls, 1)
	require.Equal(t, domain.EntryCommand, bridge.launchCalls[0].EntryType)
	require.Equal(t, "pnpm run dev", bridge.launchCalls[0].TargetPath)
	require.Equal(t, projDir, bridge.launchCalls[0].WorkingDir)
	require.Contains(t, bridge.launchCalls[0].UnsetEnvVars, "ELECTRON_RUN_AS_NODE")

	// RevealAppDir & OpenAppTerminal handle command primary cleanly.
	require.NoError(t, launchSvc.RevealAppDir(app.ID))
	require.NoError(t, launchSvc.OpenAppTerminal(app.ID))
	require.Equal(t, projDir, bridge.terminalDirs[0])

	// 2. if_missing when out/main/index.js is missing -> triggers async build, then launches.
	buildDone := make(chan struct{})
	bridge.mu.Lock()
	bridge.runCmdFunc = func(_ context.Context, spec platform.LaunchSpec, onOutput func(string)) error {
		onOutput("building main & renderer...")
		require.NoError(t, os.MkdirAll(filepath.Join(projDir, "out", "main"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(projDir, "out", "main", "index.js"), []byte("ok"), 0o644))
		close(buildDone)
		return nil
	}
	bridge.mu.Unlock()

	pid, err = launchSvc.LaunchApp(domain.LaunchRequest{AppID: app.ID, EntryID: "entry-direct"})
	require.NoError(t, err)
	require.Equal(t, 0, pid)

	select {
	case <-buildDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for if_missing build")
	}
	require.Eventually(t, func() bool {
		bridge.mu.Lock()
		defer bridge.mu.Unlock()
		return len(bridge.launchCalls) == 2
	}, 2*time.Second, 20*time.Millisecond)
	require.True(t, bridge.launchCalls[1].LogAppend)

	// 3. if_missing when out/main/index.js ALREADY exists -> skips build and launches immediately.
	pid, err = launchSvc.LaunchApp(domain.LaunchRequest{AppID: app.ID, EntryID: "entry-direct"})
	require.NoError(t, err)
	require.Greater(t, pid, 0)
	require.Len(t, bridge.commandCalls, 1) // still only 1 build call

	// 4. always build failure must NOT terminate existing running instance!
	failDone := make(chan struct{})
	bridge.mu.Lock()
	bridge.runCmdFunc = func(_ context.Context, _ platform.LaunchSpec, onOutput func(string)) error {
		onOutput("TypeScript error TS2304")
		close(failDone)
		return fmt.Errorf("exit status 1")
	}
	bridge.mu.Unlock()

	_, err = launchSvc.LaunchApp(domain.LaunchRequest{AppID: app.ID, EntryID: "entry-rebuild"})
	require.NoError(t, err)
	<-failDone

	require.Eventually(t, func() bool {
		errMu.Lock()
		defer errMu.Unlock()
		return len(asyncErrors) > 0
	}, 2*time.Second, 20*time.Millisecond)
	require.Empty(t, bridge.terminated)
	logSnap := launchSvc.GetAppLogs(app.ID)
	require.Equal(t, "failed", logSnap.Status)
	require.Contains(t, logSnap.LastError, "TypeScript error TS2304")

	// 5. always build success terminates old running PIDs before launching rebuilt binary.
	okDone := make(chan struct{})
	bridge.mu.Lock()
	bridge.runCmdFunc = func(_ context.Context, _ platform.LaunchSpec, onOutput func(string)) error {
		onOutput("rebuild finished")
		close(okDone)
		return nil
	}
	bridge.mu.Unlock()

	_, err = launchSvc.LaunchApp(domain.LaunchRequest{AppID: app.ID, EntryID: "entry-rebuild"})
	require.NoError(t, err)
	<-okDone

	require.Eventually(t, func() bool {
		bridge.mu.Lock()
		defer bridge.mu.Unlock()
		return len(bridge.terminated) > 0 && len(bridge.launchCalls) == 4
	}, 2*time.Second, 20*time.Millisecond)
}

func TestCancelActiveBuildAndLogs(t *testing.T) {
	bridge := newMockLaunchBridge()
	launchSvc, repo, projDir, _ := setupLaunchTestEnv(t, bridge)

	started := make(chan struct{})
	bridge.runCmdFunc = func(ctx context.Context, _ platform.LaunchSpec, onOutput func(string)) error {
		onOutput("compiling...")
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}

	now := time.Now().UnixMilli()
	app := &domain.App{
		ID:          "cancel-app",
		Slug:        "cancel-app",
		Name:        "CancelApp",
		StorageMode: domain.StorageLinked,
		Status:      domain.StatusReady,
		RootPath:    projDir,
		SourceType:  domain.SourceLocal,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	require.NoError(t, repo.CreateApp(app, []domain.AppEntry{
		{
			ID:                 "e1",
			AppID:              app.ID,
			Name:               "Cmd",
			EntryType:          domain.EntryCommand,
			RelPath:            "echo running",
			HideConsole:        true,
			IsPrimary:          true,
			PreLaunchCommand:   "pnpm run build",
			PreLaunchCondition: "always",
		},
	}, nil, nil))

	_, err := launchSvc.LaunchApp(domain.LaunchRequest{AppID: app.ID})
	require.NoError(t, err)
	<-started

	building, msg := launchSvc.GetBuildingState(app.ID)
	require.True(t, building)
	require.Contains(t, msg, "pnpm run build")

	require.NoError(t, launchSvc.TerminateApp(app.ID))
	buildingAfter, _ := launchSvc.GetBuildingState(app.ID)
	require.False(t, buildingAfter)

	require.Eventually(t, func() bool {
		snap := launchSvc.GetAppLogs(app.ID)
		return snap.Status == "exited"
	}, 2*time.Second, 20*time.Millisecond)

	require.NoError(t, launchSvc.ClearAppLogs(app.ID))
	cleared := launchSvc.GetAppLogs(app.ID)
	require.Empty(t, cleared.Lines)
}

func TestWindowsBridgeCommandAndElectronEnvStrip(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific bridge execution test")
	}

	t.Setenv("ELECTRON_RUN_AS_NODE", "1")
	wb := winbridge.New()

	var lines []string
	err := wb.RunCommand(context.Background(), platform.LaunchSpec{
		EntryType:    domain.EntryCommand,
		TargetPath:   "if defined ELECTRON_RUN_AS_NODE (echo DEFINED) else (echo UNSET)",
		WorkingDir:   t.TempDir(),
		UnsetEnvVars: []string{"ELECTRON_RUN_AS_NODE"},
		HideConsole:  true,
	}, func(line string) {
		lines = append(lines, line)
	})
	require.NoError(t, err)
	require.Contains(t, lines, "UNSET")
}

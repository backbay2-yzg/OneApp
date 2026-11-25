package service

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"oneapp/internal/config"
	"oneapp/internal/domain"
	"oneapp/internal/platform"
	"oneapp/internal/storage"
)

type stubBridge struct{}

func (s *stubBridge) ExtractIcon(string, string) error { return nil }
func (s *stubBridge) ReadExecutableMeta(string) (platform.ExecMeta, error) {
	return platform.ExecMeta{ProductName: "DemoTool", Version: "1.2.0", Description: "Demo desc"}, nil
}
func (s *stubBridge) ResolveShortcut(string) (platform.ShortcutInfo, error) {
	return platform.ShortcutInfo{}, nil
}
func (s *stubBridge) Launch(platform.LaunchSpec) (int, error) { return 1001, nil }
func (s *stubBridge) RunCommand(context.Context, platform.LaunchSpec, func(string)) error {
	return nil
}
func (s *stubBridge) IsProcessAlive(int, string) bool           { return false }
func (s *stubBridge) TerminateProcess(int) error                { return nil }
func (s *stubBridge) RevealInFileManager(string) error          { return nil }
func (s *stubBridge) OpenTerminal(string) error                 { return nil }
func (s *stubBridge) SetAutoStart(string, string, bool) error   { return nil }
func (s *stubBridge) ProtectSecret(p string) (string, error)    { return p, nil }
func (s *stubBridge) UnprotectSecret(c string) (string, error)  { return c, nil }

func TestScoreCandidateHeuristic(t *testing.T) {
	mainScore := ScoreCandidate("MyTool.exe", "MyTool", true)
	uninsScore := ScoreCandidate("uninstall.exe", "MyTool", true)
	updaterScore := ScoreCandidate("updater.exe", "MyTool", true)
	nestedHelper := ScoreCandidate("bin/CrashReporter.exe", "MyTool", false)

	require.Greater(t, mainScore, uninsScore)
	require.Greater(t, mainScore, updaterScore)
	require.Greater(t, mainScore, nestedHelper)
}

func TestAnalyzeAndCommitIngest(t *testing.T) {
	root := t.TempDir()
	paths, err := config.ResolvePaths(root)
	require.NoError(t, err)
	settingsStore, err := config.NewSettingsStore(paths.SettingsPath)
	require.NoError(t, err)
	require.NoError(t, paths.EnsureDirs(settingsStore.Get()))

	db, err := storage.OpenDB(paths.DBPath)
	require.NoError(t, err)
	defer db.Close()
	repo := storage.NewRepo(db)

	ingest := NewIngestService(paths, settingsStore, repo, &stubBridge{}, nil)

	// 1. Copy single executable into managed library
	srcDir := filepath.Join(root, "source")
	require.NoError(t, os.MkdirAll(srcDir, 0o755))
	exeSrc := filepath.Join(srcDir, "DemoTool.exe")
	require.NoError(t, os.WriteFile(exeSrc, []byte("MZ-demo"), 0o644))

	analyses, err := ingest.AnalyzePaths([]string{exeSrc})
	require.NoError(t, err)
	require.Len(t, analyses, 1)
	require.Equal(t, "DemoTool", analyses[0].SuggestedName)
	require.Equal(t, "1.2.0", analyses[0].Version)

	res := ingest.CommitIngest([]domain.IngestCommitItem{
		{
			SourcePath:  exeSrc,
			Name:        analyses[0].SuggestedName,
			Version:     analyses[0].Version,
			Action:      domain.IngestCopy,
			PrimaryPath: "DemoTool.exe",
			Tags:        []string{"自研"},
		},
	})
	require.Empty(t, res.Errors)
	require.Len(t, res.Apps, 1)
	managedApp := res.Apps[0]
	require.Equal(t, domain.StorageManaged, managedApp.StorageMode)
	require.Contains(t, managedApp.RootPath, "apps/")
	require.FileExists(t, filepath.Join(paths.LibraryRoot(settingsStore.Get()), filepath.FromSlash(managedApp.RootPath), "DemoTool.exe"))

	mf, err := repo.GetInstallManifest(managedApp.ID)
	require.NoError(t, err)
	require.Len(t, mf, 1)

	// 2. Link single executable in place
	linkSrc := filepath.Join(srcDir, "LinkedDev.exe")
	require.NoError(t, os.WriteFile(linkSrc, []byte("MZ-link"), 0o644))
	linkRes := ingest.CommitIngest([]domain.IngestCommitItem{
		{
			SourcePath:  linkSrc,
			Name:        "LinkedDev",
			Action:      domain.IngestLink,
			PrimaryPath: "LinkedDev.exe",
		},
	})
	require.Empty(t, linkRes.Errors)
	require.Len(t, linkRes.Apps, 1)
	require.Equal(t, domain.StorageLinked, linkRes.Apps[0].StorageMode)
	require.Equal(t, srcDir, linkRes.Apps[0].RootPath)

	// 3. Extract .zip with nested single root and pick main exe over uninstall.exe
	zipPath := filepath.Join(srcDir, "NetTool-v2.zip")
	zf, err := os.Create(zipPath)
	require.NoError(t, err)
	zw := zip.NewWriter(zf)
	for name, body := range map[string]string{
		"NetTool-v2/NetTool.exe":   "MZ-net",
		"NetTool-v2/uninstall.exe": "MZ-unins",
		"NetTool-v2/helper.bat":    "@echo off",
	} {
		w, wErr := zw.Create(name)
		require.NoError(t, wErr)
		_, wErr = w.Write([]byte(body))
		require.NoError(t, wErr)
	}
	require.NoError(t, zw.Close())
	require.NoError(t, zf.Close())

	zipAnalyses, err := ingest.AnalyzePaths([]string{zipPath})
	require.NoError(t, err)
	require.Len(t, zipAnalyses, 1)
	require.NotEmpty(t, zipAnalyses[0].Candidates)
	require.Equal(t, "NetTool.exe", zipAnalyses[0].Candidates[0].RelPath)

	zipRes := ingest.CommitIngest([]domain.IngestCommitItem{
		{
			SourcePath:  zipPath,
			Name:        "NetTool",
			Action:      domain.IngestCopy,
			PrimaryPath: zipAnalyses[0].Candidates[0].RelPath,
			ExtraPaths:  []string{"helper.bat"},
		},
	})
	require.Empty(t, zipRes.Errors)
	require.Len(t, zipRes.Apps, 1)
	require.Len(t, zipRes.Apps[0].Entries, 2)
}

func TestDetectElectronNodeAndCommandProjects(t *testing.T) {
	root := t.TempDir()
	paths, err := config.ResolvePaths(root)
	require.NoError(t, err)
	settingsStore, err := config.NewSettingsStore(paths.SettingsPath)
	require.NoError(t, err)
	require.NoError(t, paths.EnsureDirs(settingsStore.Get()))

	db, err := storage.OpenDB(paths.DBPath)
	require.NoError(t, err)
	defer db.Close()
	repo := storage.NewRepo(db)

	ingest := NewIngestService(paths, settingsStore, repo, &stubBridge{}, nil)

	// 1. Mock Electron + electron-vite + pnpm project.
	electronDir := filepath.Join(root, "electron-proj", "app")
	require.NoError(t, os.MkdirAll(filepath.Join(electronDir, "node_modules", "electron", "dist"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(electronDir, "out", "main"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(electronDir, "node_modules", "electron", "dist", "electron.exe"), []byte("MZ"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(electronDir, "out", "main", "index.js"), []byte("// built"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(electronDir, "pnpm-lock.yaml"), []byte("lockfileVersion: '9.0'"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(electronDir, "electron-builder.yml"), []byte("productName: NovelWorkbench\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(electronDir, "package.json"), []byte(`{
		"name": "novel-workbench",
		"version": "0.0.1",
		"description": "Writing desk",
		"main": "./out/main/index.js",
		"scripts": {
			"dev": "electron-vite dev",
			"build": "electron-vite build",
			"preview": "electron-vite preview"
		},
		"devDependencies": {
			"electron": "^44.0.0",
			"electron-vite": "^6.0.0"
		}
	}`), 0o644))

	analyses, err := ingest.AnalyzePaths([]string{electronDir})
	require.NoError(t, err)
	require.Len(t, analyses, 1)
	el := analyses[0]
	require.Equal(t, "project", el.SourceKind)
	require.Equal(t, "electron", el.ProjectKind)
	require.Equal(t, "NovelWorkbench", el.SuggestedName)
	require.Equal(t, "0.0.1", el.Version)
	require.Equal(t, domain.IngestLink, el.SuggestedAction)
	require.False(t, el.CanCopy)
	require.False(t, el.CanMove)
	require.True(t, el.CanLink)
	require.Empty(t, el.ProjectWarning)
	require.GreaterOrEqual(t, len(el.Candidates), 4)

	require.True(t, el.Candidates[0].IsPrimary)
	require.Equal(t, domain.EntryExe, el.Candidates[0].EntryType)
	require.Equal(t, "node_modules/electron/dist/electron.exe", el.Candidates[0].RelPath)
	require.Equal(t, ".", el.Candidates[0].ArgsTemplate)
	require.Equal(t, "pnpm run build", el.Candidates[0].PreLaunchCommand)
	require.Equal(t, "if_missing", el.Candidates[0].PreLaunchCondition)
	require.Equal(t, "out/main/index.js", el.Candidates[0].ReadyCheckPath)

	require.Equal(t, "重新构建并启动", el.Candidates[1].Name)
	require.Equal(t, "always", el.Candidates[1].PreLaunchCondition)
	require.Equal(t, domain.EntryCommand, el.Candidates[2].EntryType)
	require.Equal(t, "pnpm run dev", el.Candidates[2].RelPath)

	// Reject copying a source project directory.
	badRes := ingest.CommitIngest([]domain.IngestCommitItem{
		{
			SourcePath: electronDir,
			Name:       "NovelWorkbench",
			Action:     domain.IngestCopy,
			Entries: []domain.EntryInput{
				{Name: "主程序", EntryType: domain.EntryExe, RelPath: el.Candidates[0].RelPath, IsPrimary: true},
			},
		},
	})
	require.NotEmpty(t, badRes.Errors)

	// Commit Electron project via link mode with all 3 default entries (including two sharing electron.exe relPath).
	entryInputs := make([]domain.EntryInput, 0, 3)
	for i := 0; i < 3; i++ {
		c := el.Candidates[i]
		entryInputs = append(entryInputs, domain.EntryInput{
			Name:               c.Name,
			EntryType:          c.EntryType,
			RelPath:            c.RelPath,
			ArgsTemplate:       c.ArgsTemplate,
			WorkingDir:         c.WorkingDir,
			HideConsole:        c.HideConsole,
			IsPrimary:          c.IsPrimary,
			PreLaunchCommand:   c.PreLaunchCommand,
			PreLaunchCondition: c.PreLaunchCondition,
			ReadyCheckPath:     c.ReadyCheckPath,
		})
	}
	goodRes := ingest.CommitIngest([]domain.IngestCommitItem{
		{
			SourcePath:     electronDir,
			Name:           el.SuggestedName,
			Version:        el.Version,
			Description:    el.Description,
			Action:         domain.IngestLink,
			Entries:        entryInputs,
			IconSourcePath: el.IconSourcePath,
		},
	})
	require.Empty(t, goodRes.Errors)
	require.Len(t, goodRes.Apps, 1)
	require.Equal(t, domain.StorageLinked, goodRes.Apps[0].StorageMode)
	require.Len(t, goodRes.Apps[0].Entries, 3)
	require.Equal(t, "if_missing", goodRes.Apps[0].Entries[0].PreLaunchCondition)
	require.Equal(t, "always", goodRes.Apps[0].Entries[1].PreLaunchCondition)
	require.Equal(t, domain.EntryCommand, goodRes.Apps[0].Entries[2].EntryType)

	// 2. General Node project.
	nodeDir := filepath.Join(root, "node-api")
	require.NoError(t, os.MkdirAll(nodeDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(nodeDir, "package-lock.json"), []byte("{}"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(nodeDir, "package.json"), []byte(`{
		"name": "@demo/web-api",
		"version": "2.0.0",
		"scripts": {"dev": "tsx watch src/index.ts", "start": "node dist/index.js"}
	}`), 0o644))
	nodeRes, err := ingest.AnalyzePaths([]string{nodeDir})
	require.NoError(t, err)
	require.Len(t, nodeRes, 1)
	require.Equal(t, "node", nodeRes[0].ProjectKind)
	require.Equal(t, "web-api", nodeRes[0].SuggestedName)
	require.Len(t, nodeRes[0].Candidates, 2)
	require.Equal(t, "npm run dev", nodeRes[0].Candidates[0].RelPath)
	require.Equal(t, domain.EntryCommand, nodeRes[0].Candidates[0].EntryType)

	// 3. Generic Go/custom project with no exe + CreateCommandApp.
	goDir := filepath.Join(root, "go-svc")
	require.NoError(t, os.MkdirAll(goDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(goDir, "go.mod"), []byte("module example.com/svc\n"), 0o644))
	goAnalyses, err := ingest.AnalyzePaths([]string{goDir})
	require.NoError(t, err)
	require.Len(t, goAnalyses, 1)
	require.Equal(t, "project", goAnalyses[0].SourceKind)
	require.Equal(t, "custom", goAnalyses[0].ProjectKind)
	require.False(t, goAnalyses[0].CanCopy)
	require.Empty(t, goAnalyses[0].Error)
	require.Empty(t, goAnalyses[0].Candidates)

	createdCmdApp, err := ingest.CreateCommandApp(domain.CreateCommandAppRequest{
		Name:               "GoService",
		RootPath:           goDir,
		Command:            "go run .",
		PreLaunchCommand:   "go generate ./...",
		PreLaunchCondition: "if_missing",
		ReadyCheckPath:     "gen.go",
		HideConsole:        true,
		Tags:               []string{"Go"},
	})
	require.NoError(t, err)
	require.Equal(t, "GoService", createdCmdApp.Name)
	require.Equal(t, domain.StorageLinked, createdCmdApp.StorageMode)
	require.Len(t, createdCmdApp.Entries, 1)
	require.Equal(t, domain.EntryCommand, createdCmdApp.Entries[0].EntryType)
	require.Equal(t, "go run .", createdCmdApp.Entries[0].RelPath)
}

func TestAnalyzeRealNovelWorkbenchProject(t *testing.T) {
	targetDir := `D:\小说写作台\app`
	if _, err := os.Stat(targetDir); err != nil {
		t.Skip("real Electron test project not present on this host")
	}

	root := t.TempDir()
	paths, err := config.ResolvePaths(root)
	require.NoError(t, err)
	settingsStore, err := config.NewSettingsStore(paths.SettingsPath)
	require.NoError(t, err)
	require.NoError(t, paths.EnsureDirs(settingsStore.Get()))

	db, err := storage.OpenDB(paths.DBPath)
	require.NoError(t, err)
	defer db.Close()
	repo := storage.NewRepo(db)

	ingest := NewIngestService(paths, settingsStore, repo, &stubBridge{}, nil)
	analyses, err := ingest.AnalyzePaths([]string{targetDir})
	require.NoError(t, err)
	require.Len(t, analyses, 1)

	a := analyses[0]
	require.Empty(t, a.Error)
	require.Equal(t, "project", a.SourceKind)
	require.Equal(t, "electron", a.ProjectKind)
	require.Equal(t, "NovelWorkbench", a.SuggestedName)
	require.Equal(t, domain.IngestLink, a.SuggestedAction)
	require.False(t, a.CanCopy)
	require.False(t, a.CanMove)
	require.True(t, a.CanLink)
	require.Empty(t, a.ProjectWarning)
	require.GreaterOrEqual(t, len(a.Candidates), 4)
	require.Equal(t, "node_modules/electron/dist/electron.exe", a.Candidates[0].RelPath)
	require.Equal(t, "pnpm run build", a.Candidates[0].PreLaunchCommand)
	require.Equal(t, "if_missing", a.Candidates[0].PreLaunchCondition)
	require.Equal(t, "out/main/index.js", a.Candidates[0].ReadyCheckPath)
	require.Equal(t, "重新构建并启动", a.Candidates[1].Name)
	require.Equal(t, "开发模式 (dev)", a.Candidates[2].Name)
}

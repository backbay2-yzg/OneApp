package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
	"oneapp/internal/config"
	"oneapp/internal/domain"
)

func TestRepoLifecycleAndPortablePaths(t *testing.T) {
	root := t.TempDir()
	paths, err := config.ResolvePaths(root)
	require.NoError(t, err)
	settings := config.DefaultSettings()
	require.NoError(t, paths.EnsureDirs(settings))

	db, err := OpenDB(paths.DBPath)
	require.NoError(t, err)
	defer db.Close()

	repo := NewRepo(db)

	// Default categories seeded on fresh DB.
	cats, err := repo.ListCategories()
	require.NoError(t, err)
	require.Len(t, cats, 3)

	now := time.Now().UnixMilli()
	app := &domain.App{
		ID:             "app-1",
		Slug:           "mytool",
		Name:           "MyTool",
		Aliases:        []string{"mt"},
		Description:    "A self-made utility",
		CategoryID:     cats[0].ID,
		StorageMode:    domain.StorageManaged,
		Status:         domain.StatusReady,
		RootPath:       "apps/mytool-123456",
		SourceType:     domain.SourceLocal,
		CurrentVersion: "1.0.0",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	entries := []domain.AppEntry{
		{
			ID:        "entry-1",
			AppID:     app.ID,
			Name:      "主程序",
			EntryType: domain.EntryExe,
			RelPath:   "mytool.exe",
			EnvVars:   map[string]string{"MODE": "fast"},
			IsPrimary: true,
		},
		{
			ID:          "entry-2",
			AppID:       app.ID,
			Name:        "CLI 脚本",
			EntryType:   domain.EntryBat,
			RelPath:     "run.bat",
			HideConsole: true,
		},
	}
	manifest := []domain.InstallManifestFile{
		{AppID: app.ID, RelPath: "mytool.exe", SHA256: "deadbeef", FileSize: 100},
	}

	require.NoError(t, repo.CreateApp(app, entries, []string{"自研", "CLI"}, manifest))

	got, err := repo.GetApp(app.ID)
	require.NoError(t, err)
	require.Equal(t, "MyTool", got.Name)
	require.Equal(t, cats[0].Name, got.CategoryName)
	require.Equal(t, []string{"CLI", "自研"}, got.Tags)
	require.Len(t, got.Entries, 2)
	require.Equal(t, "mytool.exe", got.PrimaryExe)

	// Portable relative path resolution.
	resolvedRoot := ResolveAppRoot(paths, settings, got.App)
	require.Equal(t, filepath.Join(root, "library", "apps", "mytool-123456"), resolvedRoot)
	require.Equal(t, filepath.Join(resolvedRoot, "mytool.exe"), ResolveEntryPath(resolvedRoot, got.Entries[0]))

	// Linked mode path resolution.
	linkedApp := domain.App{StorageMode: domain.StorageLinked, RootPath: filepath.Join(root, "external", "dist")}
	require.Equal(t, filepath.Join(root, "external", "dist"), ResolveAppRoot(paths, settings, linkedApp))

	// Update app & verify orphan tag pruning.
	require.NoError(t, repo.UpdateApp(domain.UpdateAppRequest{
		ID:             app.ID,
		Name:           "MyTool Pro",
		Aliases:        []string{"mtp"},
		Description:    "Updated",
		NotesMD:        "# Note",
		CategoryID:     cats[0].ID,
		Tags:           []string{"自研"},
		IsPinned:       true,
		CurrentVersion: "1.1.0",
	}))
	tags, err := repo.ListTags()
	require.NoError(t, err)
	require.Len(t, tags, 1)
	require.Equal(t, "自研", tags[0].Name)

	// Record launch.
	launchTS := now + 5000
	require.NoError(t, repo.RecordLaunch(app.ID, "entry-1", false, launchTS))
	gotAfterLaunch, err := repo.GetApp(app.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), gotAfterLaunch.LaunchCount)
	require.Equal(t, launchTS, gotAfterLaunch.LastLaunchedAt)

	// Delete app cascades and prunes orphan tags.
	require.NoError(t, repo.DeleteApp(app.ID))
	tagsAfterDel, err := repo.ListTags()
	require.NoError(t, err)
	require.Empty(t, tagsAfterDel)
}

func TestLegacyDBMigrationAndCommandEntries(t *testing.T) {
	root := t.TempDir()
	paths, err := config.ResolvePaths(root)
	require.NoError(t, err)
	settings := config.DefaultSettings()
	require.NoError(t, paths.EnsureDirs(settings))

	// Simulate an existing legacy database before command entries and pre-launch columns were added.
	legacyDB, err := sql.Open("sqlite", paths.DBPath)
	require.NoError(t, err)
	_, err = legacyDB.Exec(`
		PRAGMA foreign_keys = ON;
		CREATE TABLE categories (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, sort_order INTEGER NOT NULL DEFAULT 0);
		CREATE TABLE apps (
			id TEXT PRIMARY KEY, slug TEXT NOT NULL, name TEXT NOT NULL, aliases_json TEXT NOT NULL DEFAULT '[]',
			description TEXT NOT NULL DEFAULT '', notes_md TEXT NOT NULL DEFAULT '', icon_rel_path TEXT NOT NULL DEFAULT '',
			category_id TEXT REFERENCES categories(id) ON DELETE SET NULL, is_pinned INTEGER NOT NULL DEFAULT 0,
			hotkey TEXT NOT NULL DEFAULT '', storage_mode TEXT NOT NULL CHECK (storage_mode IN ('managed', 'linked')),
			status TEXT NOT NULL DEFAULT 'ready', root_path TEXT NOT NULL DEFAULT '',
			source_type TEXT NOT NULL DEFAULT 'local', source_url TEXT NOT NULL DEFAULT '',
			asset_pattern TEXT NOT NULL DEFAULT '', include_prerelease INTEGER NOT NULL DEFAULT 0,
			current_version TEXT NOT NULL DEFAULT '', latest_version TEXT NOT NULL DEFAULT '',
			remote_etag TEXT NOT NULL DEFAULT '', persist_rules_json TEXT NOT NULL DEFAULT '[]',
			launch_count INTEGER NOT NULL DEFAULT 0, last_launched_at INTEGER NOT NULL DEFAULT 0,
			last_checked_at INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
		);
		CREATE TABLE app_entries (
			id TEXT PRIMARY KEY, app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
			name TEXT NOT NULL, entry_type TEXT NOT NULL CHECK (entry_type IN ('exe', 'bat', 'cmd', 'ps1', 'lnk')),
			rel_path TEXT NOT NULL, args_template TEXT NOT NULL DEFAULT '', working_dir TEXT NOT NULL DEFAULT '',
			env_vars_json TEXT NOT NULL DEFAULT '{}', run_as_admin INTEGER NOT NULL DEFAULT 0,
			hide_console INTEGER NOT NULL DEFAULT 0, prompt_args INTEGER NOT NULL DEFAULT 0,
			is_primary INTEGER NOT NULL DEFAULT 0, sort_order INTEGER NOT NULL DEFAULT 0
		);
		INSERT INTO apps (id, slug, name, storage_mode, root_path, created_at, updated_at)
		VALUES ('legacy-app', 'legacy', 'LegacyApp', 'managed', 'apps/legacy', 1000, 1000);
		INSERT INTO app_entries (id, app_id, name, entry_type, rel_path, is_primary)
		VALUES ('legacy-entry', 'legacy-app', '主程序', 'exe', 'legacy.exe', 1);
	`)
	require.NoError(t, err)
	require.NoError(t, legacyDB.Close())

	// Re-open via OpenDB and verify migration preserves legacy data and supports new fields.
	db, err := OpenDB(paths.DBPath)
	require.NoError(t, err)
	defer db.Close()

	repo := NewRepo(db)
	legacyGot, err := repo.GetApp("legacy-app")
	require.NoError(t, err)
	require.Equal(t, "LegacyApp", legacyGot.Name)
	require.Len(t, legacyGot.Entries, 1)
	require.Equal(t, "legacy.exe", legacyGot.Entries[0].RelPath)

	// Verify foreign keys remain enabled after migration.
	var fkEnabled int
	require.NoError(t, db.QueryRow(`PRAGMA foreign_keys`).Scan(&fkEnabled))
	require.Equal(t, 1, fkEnabled)

	// Insert a new project app with EntryCommand and pre-launch build fields.
	projRoot := filepath.Join(root, "novel-app")
	now := time.Now().UnixMilli()
	projApp := &domain.App{
		ID:          "proj-1",
		Slug:        "novel-workbench",
		Name:        "NovelWorkbench",
		StorageMode: domain.StorageLinked,
		Status:      domain.StatusReady,
		RootPath:    projRoot,
		SourceType:  domain.SourceLocal,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	projEntries := []domain.AppEntry{
		{
			ID:                 "proj-entry-1",
			AppID:              projApp.ID,
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
			ID:          "proj-entry-2",
			AppID:       projApp.ID,
			Name:        "开发模式 (dev)",
			EntryType:   domain.EntryCommand,
			RelPath:     "pnpm run dev",
			HideConsole: true,
		},
	}
	require.NoError(t, repo.CreateApp(projApp, projEntries, []string{"Electron"}, nil))

	gotProj, err := repo.GetApp(projApp.ID)
	require.NoError(t, err)
	require.Len(t, gotProj.Entries, 2)
	require.Equal(t, "pnpm run build", gotProj.Entries[0].PreLaunchCommand)
	require.Equal(t, "if_missing", gotProj.Entries[0].PreLaunchCondition)
	require.Equal(t, "out/main/index.js", gotProj.Entries[0].ReadyCheckPath)
	require.Equal(t, domain.EntryCommand, gotProj.Entries[1].EntryType)
	require.Equal(t, "pnpm run dev", gotProj.Entries[1].RelPath)

	// Verify command path & working dir resolution.
	cmdEntry := gotProj.Entries[1]
	require.Equal(t, "pnpm run dev", ResolveEntryPath(projRoot, cmdEntry))
	require.Equal(t, projRoot, ResolveWorkingDir(projRoot, cmdEntry, "pnpm run dev"))
	require.Equal(t, filepath.Join(projRoot, "out", "main", "index.js"), ResolvePathInRoot(projRoot, "out/main/index.js"))
}

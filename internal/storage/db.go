package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

const schemaSQL = `
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;

CREATE TABLE IF NOT EXISTS categories (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    sort_order INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS apps (
    id                 TEXT PRIMARY KEY,
    slug               TEXT NOT NULL,
    name               TEXT NOT NULL,
    aliases_json       TEXT NOT NULL DEFAULT '[]',
    description        TEXT NOT NULL DEFAULT '',
    notes_md           TEXT NOT NULL DEFAULT '',
    icon_rel_path      TEXT NOT NULL DEFAULT '',
    category_id        TEXT REFERENCES categories(id) ON DELETE SET NULL,
    is_pinned          INTEGER NOT NULL DEFAULT 0,
    hotkey             TEXT NOT NULL DEFAULT '',
    storage_mode       TEXT NOT NULL CHECK (storage_mode IN ('managed', 'linked')),
    status             TEXT NOT NULL DEFAULT 'ready' CHECK (status IN ('ready', 'pending_files', 'missing')),
    root_path          TEXT NOT NULL DEFAULT '',
    source_type        TEXT NOT NULL DEFAULT 'local' CHECK (source_type IN ('local', 'github', 'direct_url')),
    source_url         TEXT NOT NULL DEFAULT '',
    asset_pattern      TEXT NOT NULL DEFAULT '',
    include_prerelease INTEGER NOT NULL DEFAULT 0,
    current_version    TEXT NOT NULL DEFAULT '',
    latest_version     TEXT NOT NULL DEFAULT '',
    remote_etag        TEXT NOT NULL DEFAULT '',
    persist_rules_json TEXT NOT NULL DEFAULT '[]',
    launch_count       INTEGER NOT NULL DEFAULT 0,
    last_launched_at   INTEGER NOT NULL DEFAULT 0,
    last_checked_at    INTEGER NOT NULL DEFAULT 0,
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_apps_category ON apps(category_id);
CREATE INDEX IF NOT EXISTS idx_apps_status ON apps(status);
CREATE INDEX IF NOT EXISTS idx_apps_frecency ON apps(is_pinned DESC, last_launched_at DESC, launch_count DESC);

CREATE TABLE IF NOT EXISTS app_entries (
    id                   TEXT PRIMARY KEY,
    app_id               TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name                 TEXT NOT NULL,
    entry_type           TEXT NOT NULL CHECK (entry_type IN ('exe', 'bat', 'cmd', 'ps1', 'lnk', 'command')),
    rel_path             TEXT NOT NULL,
    args_template        TEXT NOT NULL DEFAULT '',
    working_dir          TEXT NOT NULL DEFAULT '',
    env_vars_json        TEXT NOT NULL DEFAULT '{}',
    run_as_admin         INTEGER NOT NULL DEFAULT 0,
    hide_console         INTEGER NOT NULL DEFAULT 0,
    prompt_args          INTEGER NOT NULL DEFAULT 0,
    is_primary           INTEGER NOT NULL DEFAULT 0,
    sort_order           INTEGER NOT NULL DEFAULT 0,
    pre_launch_command   TEXT NOT NULL DEFAULT '',
    pre_launch_condition TEXT NOT NULL DEFAULT '',
    ready_check_path     TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_entries_app ON app_entries(app_id, is_primary DESC, sort_order ASC);

CREATE TABLE IF NOT EXISTS tags (
    id   TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS app_tags (
    app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    tag_id TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (app_id, tag_id)
);

CREATE TABLE IF NOT EXISTS install_manifests (
    app_id    TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    rel_path  TEXT NOT NULL,
    sha256    TEXT NOT NULL,
    file_size INTEGER NOT NULL,
    PRIMARY KEY (app_id, rel_path)
);

CREATE TABLE IF NOT EXISTS update_backups (
    id                TEXT PRIMARY KEY,
    app_id            TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    version           TEXT NOT NULL,
    backup_rel_path   TEXT NOT NULL,
    manifest_snapshot TEXT NOT NULL DEFAULT '[]',
    created_at        INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_backups_app ON update_backups(app_id, created_at DESC);

CREATE TABLE IF NOT EXISTS launch_history (
    id          TEXT PRIMARY KEY,
    app_id      TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    entry_id    TEXT NOT NULL,
    launched_at INTEGER NOT NULL,
    elevated    INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_launch_history_time ON launch_history(launched_at DESC);
`

// OpenDB initializes the pure-Go SQLite database at dbPath and runs schema migrations.
func OpenDB(dbPath string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db parent dir: %w", err)
	}

	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	if _, err := db.Exec(schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init sqlite schema: %w", err)
	}

	if err := migrateSchema(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate sqlite schema: %w", err)
	}

	if err := seedDefaults(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("seed defaults: %w", err)
	}

	return db, nil
}

func migrateSchema(db *sql.DB) error {
	var ddl string
	if err := db.QueryRow(`SELECT COALESCE(sql, '') FROM sqlite_master WHERE type = 'table' AND name = 'app_entries'`).Scan(&ddl); err != nil {
		return err
	}
	if ddl == "" {
		return nil
	}

	cols := make(map[string]bool)
	rows, err := db.Query(`PRAGMA table_info(app_entries)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt sql.NullString
		if scanErr := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); scanErr == nil {
			cols[strings.ToLower(name)] = true
		}
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	hasCommandConstraint := strings.Contains(strings.ToLower(ddl), "'command'")
	hasPreLaunchCmd := cols["pre_launch_command"]
	hasPreLaunchCond := cols["pre_launch_condition"]
	hasReadyCheckPath := cols["ready_check_path"]

	if hasCommandConstraint && hasPreLaunchCmd && hasPreLaunchCond && hasReadyCheckPath {
		return nil
	}

	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	defer func() {
		_, _ = db.Exec(`PRAGMA foreign_keys = ON`)
	}()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DROP TABLE IF EXISTS app_entries_new`); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		CREATE TABLE app_entries_new (
			id                   TEXT PRIMARY KEY,
			app_id               TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
			name                 TEXT NOT NULL,
			entry_type           TEXT NOT NULL CHECK (entry_type IN ('exe', 'bat', 'cmd', 'ps1', 'lnk', 'command')),
			rel_path             TEXT NOT NULL,
			args_template        TEXT NOT NULL DEFAULT '',
			working_dir          TEXT NOT NULL DEFAULT '',
			env_vars_json        TEXT NOT NULL DEFAULT '{}',
			run_as_admin         INTEGER NOT NULL DEFAULT 0,
			hide_console         INTEGER NOT NULL DEFAULT 0,
			prompt_args          INTEGER NOT NULL DEFAULT 0,
			is_primary           INTEGER NOT NULL DEFAULT 0,
			sort_order           INTEGER NOT NULL DEFAULT 0,
			pre_launch_command   TEXT NOT NULL DEFAULT '',
			pre_launch_condition TEXT NOT NULL DEFAULT '',
			ready_check_path     TEXT NOT NULL DEFAULT ''
		)
	`); err != nil {
		return err
	}

	preCmdExpr := "''"
	if hasPreLaunchCmd {
		preCmdExpr = "COALESCE(pre_launch_command, '')"
	}
	preCondExpr := "''"
	if hasPreLaunchCond {
		preCondExpr = "COALESCE(pre_launch_condition, '')"
	}
	readyCheckExpr := "''"
	if hasReadyCheckPath {
		readyCheckExpr = "COALESCE(ready_check_path, '')"
	}

	copySQL := fmt.Sprintf(`
		INSERT INTO app_entries_new (
			id, app_id, name, entry_type, rel_path, args_template, working_dir,
			env_vars_json, run_as_admin, hide_console, prompt_args, is_primary, sort_order,
			pre_launch_command, pre_launch_condition, ready_check_path
		)
		SELECT
			id, app_id, name, entry_type, rel_path, args_template, working_dir,
			env_vars_json, run_as_admin, hide_console, prompt_args, is_primary, sort_order,
			%s, %s, %s
		FROM app_entries
	`, preCmdExpr, preCondExpr, readyCheckExpr)
	if _, err := tx.Exec(copySQL); err != nil {
		return err
	}

	if _, err := tx.Exec(`DROP TABLE app_entries`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE app_entries_new RENAME TO app_entries`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_entries_app ON app_entries(app_id, is_primary DESC, sort_order ASC)`); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return err
	}
	return nil
}

func seedDefaults(db *sql.DB) error {
	var catCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM categories`).Scan(&catCount); err != nil {
		return err
	}
	var appCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM apps`).Scan(&appCount); err != nil {
		return err
	}
	if catCount > 0 || appCount > 0 {
		return nil
	}

	defaults := []string{"自研工具", "效率工具", "系统调试"}
	for idx, name := range defaults {
		if _, err := db.Exec(
			`INSERT INTO categories (id, name, sort_order) VALUES (?, ?, ?)`,
			uuid.NewString(), name, idx+1,
		); err != nil {
			return err
		}
	}
	return nil
}

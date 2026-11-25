package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"oneapp/internal/domain"
)

var ErrNotFound = errors.New("record not found")

// Repo provides transactional persistence operations over SQLite.
type Repo struct {
	db *sql.DB
}

// NewRepo wraps an initialized SQLite DB handle.
func NewRepo(db *sql.DB) *Repo {
	return &Repo{db: db}
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func marshalStringSlice(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	b, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func unmarshalStringSlice(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return []string{}
	}
	return out
}

func marshalStringMap(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func unmarshalStringMap(raw string) map[string]string {
	if strings.TrimSpace(raw) == "" {
		return map[string]string{}
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return map[string]string{}
	}
	return out
}

// ListApps returns all stored apps hydrated with category names, tags, and entries.
func (r *Repo) ListApps() ([]domain.AppOverview, error) {
	catNames := make(map[string]string)
	catRows, err := r.db.Query(`SELECT id, name FROM categories`)
	if err != nil {
		return nil, err
	}
	for catRows.Next() {
		var id, name string
		if scanErr := catRows.Scan(&id, &name); scanErr == nil {
			catNames[id] = name
		}
	}
	_ = catRows.Close()

	tagsByApp := make(map[string][]string)
	tagRows, err := r.db.Query(`
		SELECT at.app_id, t.name
		FROM app_tags at
		JOIN tags t ON t.id = at.tag_id
		ORDER BY t.name ASC
	`)
	if err != nil {
		return nil, err
	}
	for tagRows.Next() {
		var appID, tagName string
		if scanErr := tagRows.Scan(&appID, &tagName); scanErr == nil {
			tagsByApp[appID] = append(tagsByApp[appID], tagName)
		}
	}
	_ = tagRows.Close()

	entriesByApp := make(map[string][]domain.AppEntry)
	entryRows, err := r.db.Query(`
		SELECT id, app_id, name, entry_type, rel_path, args_template, working_dir,
		       env_vars_json, run_as_admin, hide_console, prompt_args, is_primary, sort_order,
		       pre_launch_command, pre_launch_condition, ready_check_path
		FROM app_entries
		ORDER BY is_primary DESC, sort_order ASC, name ASC
	`)
	if err != nil {
		return nil, err
	}
	for entryRows.Next() {
		e, scanErr := scanAppEntry(entryRows)
		if scanErr == nil {
			entriesByApp[e.AppID] = append(entriesByApp[e.AppID], e)
		}
	}
	_ = entryRows.Close()

	rows, err := r.db.Query(`
		SELECT id, slug, name, aliases_json, description, notes_md, icon_rel_path,
		       COALESCE(category_id, ''), is_pinned, hotkey, storage_mode, status, root_path,
		       source_type, source_url, asset_pattern, include_prerelease,
		       current_version, latest_version, remote_etag, persist_rules_json,
		       launch_count, last_launched_at, last_checked_at, created_at, updated_at
		FROM apps
		ORDER BY is_pinned DESC, last_launched_at DESC, created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]domain.AppOverview, 0)
	for rows.Next() {
		a, scanErr := scanApp(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		tags := tagsByApp[a.ID]
		if tags == nil {
			tags = []string{}
		}
		entries := entriesByApp[a.ID]
		if entries == nil {
			entries = []domain.AppEntry{}
		}
		ov := domain.AppOverview{
			App:          a,
			CategoryName: catNames[a.CategoryID],
			Tags:         tags,
			Entries:      entries,
			RunningPIDs:  []int{},
		}
		for _, e := range entries {
			if e.IsPrimary {
				ov.PrimaryExe = e.RelPath
				break
			}
		}
		if ov.PrimaryExe == "" && len(entries) > 0 {
			ov.PrimaryExe = entries[0].RelPath
		}
		result = append(result, ov)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// GetApp returns a single hydrated AppOverview by ID.
func (r *Repo) GetApp(id string) (*domain.AppOverview, error) {
	row := r.db.QueryRow(`
		SELECT id, slug, name, aliases_json, description, notes_md, icon_rel_path,
		       COALESCE(category_id, ''), is_pinned, hotkey, storage_mode, status, root_path,
		       source_type, source_url, asset_pattern, include_prerelease,
		       current_version, latest_version, remote_etag, persist_rules_json,
		       launch_count, last_launched_at, last_checked_at, created_at, updated_at
		FROM apps
		WHERE id = ?
	`, id)
	a, err := scanApp(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	var catName string
	if a.CategoryID != "" {
		_ = r.db.QueryRow(`SELECT name FROM categories WHERE id = ?`, a.CategoryID).Scan(&catName)
	}

	tags := make([]string, 0)
	tagRows, err := r.db.Query(`
		SELECT t.name FROM app_tags at
		JOIN tags t ON t.id = at.tag_id
		WHERE at.app_id = ?
		ORDER BY t.name ASC
	`, id)
	if err == nil {
		for tagRows.Next() {
			var tn string
			if tagRows.Scan(&tn) == nil {
				tags = append(tags, tn)
			}
		}
		_ = tagRows.Close()
	}

	entries := make([]domain.AppEntry, 0)
	entryRows, err := r.db.Query(`
		SELECT id, app_id, name, entry_type, rel_path, args_template, working_dir,
		       env_vars_json, run_as_admin, hide_console, prompt_args, is_primary, sort_order,
		       pre_launch_command, pre_launch_condition, ready_check_path
		FROM app_entries
		WHERE app_id = ?
		ORDER BY is_primary DESC, sort_order ASC, name ASC
	`, id)
	if err == nil {
		for entryRows.Next() {
			if e, scanErr := scanAppEntry(entryRows); scanErr == nil {
				entries = append(entries, e)
			}
		}
		_ = entryRows.Close()
	}

	ov := &domain.AppOverview{
		App:          a,
		CategoryName: catName,
		Tags:         tags,
		Entries:      entries,
		RunningPIDs:  []int{},
	}
	for _, e := range entries {
		if e.IsPrimary {
			ov.PrimaryExe = e.RelPath
			break
		}
	}
	if ov.PrimaryExe == "" && len(entries) > 0 {
		ov.PrimaryExe = entries[0].RelPath
	}
	return ov, nil
}

// CreateApp inserts a new app, its entries, tags, and install manifest in one transaction.
func (r *Repo) CreateApp(app *domain.App, entries []domain.AppEntry, tagNames []string, manifest []domain.InstallManifestFile) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var catID any
	if strings.TrimSpace(app.CategoryID) != "" {
		catID = app.CategoryID
	}

	_, err = tx.Exec(`
		INSERT INTO apps (
			id, slug, name, aliases_json, description, notes_md, icon_rel_path,
			category_id, is_pinned, hotkey, storage_mode, status, root_path,
			source_type, source_url, asset_pattern, include_prerelease,
			current_version, latest_version, remote_etag, persist_rules_json,
			launch_count, last_launched_at, last_checked_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		app.ID, app.Slug, app.Name, marshalStringSlice(app.Aliases), app.Description, app.NotesMD, app.IconRelPath,
		catID, boolToInt(app.IsPinned), app.Hotkey, string(app.StorageMode), string(app.Status), app.RootPath,
		string(app.SourceType), app.SourceURL, app.AssetPattern, boolToInt(app.IncludePrerelease),
		app.CurrentVersion, app.LatestVersion, app.RemoteETag, marshalStringSlice(app.PersistRules),
		app.LaunchCount, app.LastLaunchedAt, app.LastCheckedAt, app.CreatedAt, app.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert app: %w", err)
	}

	if err := insertEntriesTx(tx, app.ID, entries); err != nil {
		return err
	}
	if err := setAppTagsTx(tx, app.ID, tagNames); err != nil {
		return err
	}
	for _, mf := range manifest {
		if _, err := tx.Exec(
			`INSERT INTO install_manifests (app_id, rel_path, sha256, file_size) VALUES (?, ?, ?, ?)`,
			app.ID, mf.RelPath, mf.SHA256, mf.FileSize,
		); err != nil {
			return fmt.Errorf("insert manifest file: %w", err)
		}
	}

	return tx.Commit()
}

// UpdateApp updates editable app metadata and tags.
func (r *Repo) UpdateApp(req domain.UpdateAppRequest) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var catID any
	if strings.TrimSpace(req.CategoryID) != "" {
		catID = req.CategoryID
	}
	now := time.Now().UnixMilli()
	res, err := tx.Exec(`
		UPDATE apps
		SET name = ?, aliases_json = ?, description = ?, notes_md = ?, category_id = ?,
		    is_pinned = ?, hotkey = ?, current_version = ?, persist_rules_json = ?, updated_at = ?
		WHERE id = ?
	`,
		strings.TrimSpace(req.Name),
		marshalStringSlice(cleanStringSlice(req.Aliases)),
		req.Description,
		req.NotesMD,
		catID,
		boolToInt(req.IsPinned),
		strings.TrimSpace(req.Hotkey),
		strings.TrimSpace(req.CurrentVersion),
		marshalStringSlice(cleanStringSlice(req.PersistRules)),
		now,
		req.ID,
	)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}

	if err := setAppTagsTx(tx, req.ID, req.Tags); err != nil {
		return err
	}
	if err := pruneOrphanTagsTx(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// SetPinned toggles pinned status on an app.
func (r *Repo) SetPinned(appID string, pinned bool) error {
	res, err := r.db.Exec(
		`UPDATE apps SET is_pinned = ?, updated_at = ? WHERE id = ?`,
		boolToInt(pinned), time.Now().UnixMilli(), appID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateIconRelPath updates the cached icon relative path for an app.
func (r *Repo) UpdateIconRelPath(appID, iconRelPath string) error {
	_, err := r.db.Exec(
		`UPDATE apps SET icon_rel_path = ?, updated_at = ? WHERE id = ?`,
		iconRelPath, time.Now().UnixMilli(), appID,
	)
	return err
}

// ReplaceAppEntries replaces all launch entries for an app inside a single transaction.
func (r *Repo) ReplaceAppEntries(appID string, entries []domain.AppEntry) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM app_entries WHERE app_id = ?`, appID); err != nil {
		return err
	}
	if err := insertEntriesTx(tx, appID, entries); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE apps SET updated_at = ? WHERE id = ?`, time.Now().UnixMilli(), appID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteApp removes an app and prunes unused tags.
func (r *Repo) DeleteApp(appID string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(`DELETE FROM apps WHERE id = ?`, appID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if err := pruneOrphanTagsTx(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordLaunch increments launch stats and inserts a launch_history entry.
func (r *Repo) RecordLaunch(appID, entryID string, elevated bool, ts int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
		UPDATE apps
		SET launch_count = launch_count + 1,
		    last_launched_at = ?,
		    updated_at = ?
		WHERE id = ?
	`, ts, ts, appID); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO launch_history (id, app_id, entry_id, launched_at, elevated)
		VALUES (?, ?, ?, ?, ?)
	`, uuid.NewString(), appID, entryID, ts, boolToInt(elevated)); err != nil {
		return err
	}
	return tx.Commit()
}

// ListCategories returns all categories sorted by sort_order and name.
func (r *Repo) ListCategories() ([]domain.Category, error) {
	rows, err := r.db.Query(`SELECT id, name, sort_order FROM categories ORDER BY sort_order ASC, name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.Category, 0)
	for rows.Next() {
		var c domain.Category
		if err := rows.Scan(&c.ID, &c.Name, &c.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateCategory adds a new category or returns existing if duplicate name? Returns error on duplicate.
func (r *Repo) CreateCategory(name string) (*domain.Category, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil, fmt.Errorf("category name cannot be empty")
	}
	var maxOrder int
	_ = r.db.QueryRow(`SELECT COALESCE(MAX(sort_order), 0) FROM categories`).Scan(&maxOrder)
	c := &domain.Category{
		ID:        uuid.NewString(),
		Name:      trimmed,
		SortOrder: maxOrder + 1,
	}
	if _, err := r.db.Exec(
		`INSERT INTO categories (id, name, sort_order) VALUES (?, ?, ?)`,
		c.ID, c.Name, c.SortOrder,
	); err != nil {
		return nil, err
	}
	return c, nil
}

// RenameCategory renames an existing category.
func (r *Repo) RenameCategory(id, name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return fmt.Errorf("category name cannot be empty")
	}
	res, err := r.db.Exec(`UPDATE categories SET name = ? WHERE id = ?`, trimmed, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteCategory removes a category (apps referencing it have category_id set to NULL).
func (r *Repo) DeleteCategory(id string) error {
	res, err := r.db.Exec(`DELETE FROM categories WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListTags returns all active tags ordered by name.
func (r *Repo) ListTags() ([]domain.Tag, error) {
	rows, err := r.db.Query(`SELECT id, name FROM tags ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.Tag, 0)
	for rows.Next() {
		var t domain.Tag
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetInstallManifest returns the recorded file hashes for an app.
func (r *Repo) GetInstallManifest(appID string) ([]domain.InstallManifestFile, error) {
	rows, err := r.db.Query(
		`SELECT app_id, rel_path, sha256, file_size FROM install_manifests WHERE app_id = ? ORDER BY rel_path ASC`,
		appID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.InstallManifestFile, 0)
	for rows.Next() {
		var mf domain.InstallManifestFile
		if err := rows.Scan(&mf.AppID, &mf.RelPath, &mf.SHA256, &mf.FileSize); err != nil {
			return nil, err
		}
		out = append(out, mf)
	}
	return out, rows.Err()
}

func insertEntriesTx(tx *sql.Tx, appID string, entries []domain.AppEntry) error {
	if len(entries) == 0 {
		return nil
	}
	hasPrimary := false
	for _, e := range entries {
		if e.IsPrimary {
			hasPrimary = true
			break
		}
	}
	for i, e := range entries {
		id := strings.TrimSpace(e.ID)
		if id == "" {
			id = uuid.NewString()
		}
		isPrimary := e.IsPrimary
		if !hasPrimary && i == 0 {
			isPrimary = true
		}
		_, err := tx.Exec(`
			INSERT INTO app_entries (
				id, app_id, name, entry_type, rel_path, args_template, working_dir,
				env_vars_json, run_as_admin, hide_console, prompt_args, is_primary, sort_order,
				pre_launch_command, pre_launch_condition, ready_check_path
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			id, appID, strings.TrimSpace(e.Name), string(e.EntryType), e.RelPath,
			e.ArgsTemplate, e.WorkingDir, marshalStringMap(e.EnvVars),
			boolToInt(e.RunAsAdmin), boolToInt(e.HideConsole), boolToInt(e.PromptArgs),
			boolToInt(isPrimary), i,
			e.PreLaunchCommand, e.PreLaunchCondition, e.ReadyCheckPath,
		)
		if err != nil {
			return fmt.Errorf("insert app entry: %w", err)
		}
	}
	return nil
}

func setAppTagsTx(tx *sql.Tx, appID string, tagNames []string) error {
	if _, err := tx.Exec(`DELETE FROM app_tags WHERE app_id = ?`, appID); err != nil {
		return err
	}
	cleaned := cleanStringSlice(tagNames)
	for _, tn := range cleaned {
		var tagID string
		err := tx.QueryRow(`SELECT id FROM tags WHERE name = ?`, tn).Scan(&tagID)
		if errors.Is(err, sql.ErrNoRows) {
			tagID = uuid.NewString()
			if _, insErr := tx.Exec(`INSERT INTO tags (id, name) VALUES (?, ?)`, tagID, tn); insErr != nil {
				return insErr
			}
		} else if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO app_tags (app_id, tag_id) VALUES (?, ?)`, appID, tagID); err != nil {
			return err
		}
	}
	return nil
}

func pruneOrphanTagsTx(tx *sql.Tx) error {
	_, err := tx.Exec(`DELETE FROM tags WHERE id NOT IN (SELECT DISTINCT tag_id FROM app_tags)`)
	return err
}

func cleanStringSlice(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		t := strings.TrimSpace(item)
		t = strings.TrimPrefix(t, "#")
		if t == "" {
			continue
		}
		key := strings.ToLower(t)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, t)
	}
	return out
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanApp(s rowScanner) (domain.App, error) {
	var a domain.App
	var aliasesJSON, persistJSON string
	var storageMode, status, sourceType string
	var isPinned, includePre int

	err := s.Scan(
		&a.ID, &a.Slug, &a.Name, &aliasesJSON, &a.Description, &a.NotesMD, &a.IconRelPath,
		&a.CategoryID, &isPinned, &a.Hotkey, &storageMode, &status, &a.RootPath,
		&sourceType, &a.SourceURL, &a.AssetPattern, &includePre,
		&a.CurrentVersion, &a.LatestVersion, &a.RemoteETag, &persistJSON,
		&a.LaunchCount, &a.LastLaunchedAt, &a.LastCheckedAt, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return a, err
	}
	a.Aliases = unmarshalStringSlice(aliasesJSON)
	a.PersistRules = unmarshalStringSlice(persistJSON)
	a.StorageMode = domain.StorageMode(storageMode)
	a.Status = domain.AppStatus(status)
	a.SourceType = domain.SourceType(sourceType)
	a.IsPinned = isPinned == 1
	a.IncludePrerelease = includePre == 1
	return a, nil
}

func scanAppEntry(s rowScanner) (domain.AppEntry, error) {
	var e domain.AppEntry
	var entryType, envJSON string
	var runAdmin, hideConsole, promptArgs, isPrimary int

	err := s.Scan(
		&e.ID, &e.AppID, &e.Name, &entryType, &e.RelPath, &e.ArgsTemplate, &e.WorkingDir,
		&envJSON, &runAdmin, &hideConsole, &promptArgs, &isPrimary, &e.SortOrder,
		&e.PreLaunchCommand, &e.PreLaunchCondition, &e.ReadyCheckPath,
	)
	if err != nil {
		return e, err
	}
	e.EntryType = domain.EntryType(entryType)
	e.EnvVars = unmarshalStringMap(envJSON)
	e.RunAsAdmin = runAdmin == 1
	e.HideConsole = hideConsole == 1
	e.PromptArgs = promptArgs == 1
	e.IsPrimary = isPrimary == 1
	return e, nil
}

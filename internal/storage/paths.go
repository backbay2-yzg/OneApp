package storage

import (
	"os"
	"path/filepath"
	"strings"

	"oneapp/internal/config"
	"oneapp/internal/domain"
)

// ResolveAppRoot computes the on-disk directory path for an app based on its storage mode.
func ResolveAppRoot(paths *config.Paths, settings config.Settings, app domain.App) string {
	raw := strings.TrimSpace(app.RootPath)
	if raw == "" {
		return ""
	}
	if app.StorageMode == domain.StorageManaged {
		return filepath.Clean(filepath.Join(paths.LibraryRoot(settings), filepath.FromSlash(raw)))
	}
	expanded := os.ExpandEnv(raw)
	return filepath.Clean(expanded)
}

// ResolveEntryPath computes the full on-disk path of an app entry, or returns the command string for EntryCommand.
func ResolveEntryPath(appRoot string, entry domain.AppEntry) string {
	if entry.EntryType == domain.EntryCommand {
		return strings.TrimSpace(entry.RelPath)
	}
	rel := strings.TrimSpace(entry.RelPath)
	if rel == "" {
		return ""
	}
	expanded := os.ExpandEnv(rel)
	if filepath.IsAbs(expanded) {
		return filepath.Clean(expanded)
	}
	if appRoot == "" {
		return filepath.Clean(filepath.FromSlash(expanded))
	}
	return filepath.Clean(filepath.Join(appRoot, filepath.FromSlash(expanded)))
}

// ResolveWorkingDir returns the effective working directory for launching an entry.
func ResolveWorkingDir(appRoot string, entry domain.AppEntry, entryFullPath string) string {
	entryDir := appRoot
	if entry.EntryType != domain.EntryCommand && entryFullPath != "" {
		entryDir = filepath.Dir(entryFullPath)
	}
	wd := strings.TrimSpace(entry.WorkingDir)
	if wd != "" {
		wd = strings.ReplaceAll(wd, "{{APP_DIR}}", appRoot)
		wd = strings.ReplaceAll(wd, "{{ENTRY_DIR}}", entryDir)
		expanded := os.ExpandEnv(wd)
		if filepath.IsAbs(expanded) {
			return filepath.Clean(expanded)
		}
		if appRoot != "" {
			return filepath.Clean(filepath.Join(appRoot, filepath.FromSlash(expanded)))
		}
		return filepath.Clean(expanded)
	}
	if entry.EntryType == domain.EntryCommand {
		return appRoot
	}
	if entryFullPath != "" {
		return filepath.Dir(entryFullPath)
	}
	return appRoot
}

// ResolvePathInRoot resolves a relative or absolute path (supporting {{APP_DIR}} and env vars) against appRoot.
func ResolvePathInRoot(appRoot, relOrAbs string) string {
	raw := strings.TrimSpace(relOrAbs)
	if raw == "" {
		return ""
	}
	if appRoot != "" {
		raw = strings.ReplaceAll(raw, "{{APP_DIR}}", appRoot)
	}
	expanded := os.ExpandEnv(raw)
	if filepath.IsAbs(expanded) {
		return filepath.Clean(expanded)
	}
	if appRoot == "" {
		return filepath.Clean(filepath.FromSlash(expanded))
	}
	return filepath.Clean(filepath.Join(appRoot, filepath.FromSlash(expanded)))
}

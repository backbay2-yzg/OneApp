package config

import (
	"os"
	"path/filepath"
	"strings"
)

// Paths holds all resolved directory and file paths for OneApp's portable layout.
type Paths struct {
	RootDir           string
	DataDir           string
	IconsDir          string
	CacheDir          string
	DownloadsDir      string
	StagingDir        string
	BackupsDir        string
	LogsDir           string
	DBPath            string
	SettingsPath      string
	DefaultLibraryDir string
}

// ResolvePaths determines the portable root directory and computes standard subdirectories.
// When rootOverride is empty, it uses the executable's directory unless running from a
// temporary Go build/test binary, in which case it falls back to the current working directory.
func ResolvePaths(rootOverride string) (*Paths, error) {
	var root string
	if strings.TrimSpace(rootOverride) != "" {
		abs, err := filepath.Abs(rootOverride)
		if err != nil {
			return nil, err
		}
		root = abs
	} else {
		exePath, err := os.Executable()
		if err == nil {
			exeDir := filepath.Dir(exePath)
			tempDir := filepath.Clean(os.TempDir())
			exeLower := strings.ToLower(exePath)
			if strings.HasPrefix(strings.ToLower(filepath.Clean(exeDir)), strings.ToLower(tempDir)) ||
				strings.HasSuffix(exeLower, ".test.exe") ||
				strings.HasSuffix(exeLower, ".test") {
				wd, wdErr := os.Getwd()
				if wdErr == nil {
					root = wd
				} else {
					root = exeDir
				}
			} else {
				root = exeDir
			}
		} else {
			wd, wdErr := os.Getwd()
			if wdErr != nil {
				return nil, wdErr
			}
			root = wd
		}
	}

	dataDir := filepath.Join(root, "data")
	cacheDir := filepath.Join(dataDir, "cache")
	p := &Paths{
		RootDir:           root,
		DataDir:           dataDir,
		IconsDir:          filepath.Join(dataDir, "icons"),
		CacheDir:          cacheDir,
		DownloadsDir:      filepath.Join(cacheDir, "downloads"),
		StagingDir:        filepath.Join(cacheDir, "staging"),
		BackupsDir:        filepath.Join(dataDir, "backups"),
		LogsDir:           filepath.Join(dataDir, "logs"),
		DBPath:            filepath.Join(dataDir, "oneapp.db"),
		SettingsPath:      filepath.Join(dataDir, "settings.json"),
		DefaultLibraryDir: filepath.Join(root, "library"),
	}
	return p, nil
}

// LibraryRoot resolves the active library root directory from settings.
// Relative paths are anchored to RootDir so the entire OneApp folder stays portable.
func (p *Paths) LibraryRoot(settings Settings) string {
	lib := strings.TrimSpace(settings.LibraryDir)
	if lib == "" {
		return p.DefaultLibraryDir
	}
	expanded := os.ExpandEnv(lib)
	if filepath.IsAbs(expanded) {
		return filepath.Clean(expanded)
	}
	return filepath.Clean(filepath.Join(p.RootDir, expanded))
}

// LibraryAppsDir returns the directory containing managed app folders.
func (p *Paths) LibraryAppsDir(settings Settings) string {
	return filepath.Join(p.LibraryRoot(settings), "apps")
}

// EnsureDirs creates all required runtime directories if they do not exist.
func (p *Paths) EnsureDirs(settings Settings) error {
	dirs := []string{
		p.DataDir,
		p.IconsDir,
		p.CacheDir,
		p.DownloadsDir,
		p.StagingDir,
		p.BackupsDir,
		p.LogsDir,
		p.LibraryAppsDir(settings),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

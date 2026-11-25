package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Settings stores user preferences persisted in data/settings.json.
type Settings struct {
	LibraryDir          string `json:"libraryDir"`
	LauncherHotkey      string `json:"launcherHotkey"`
	DefaultIngestMode   string `json:"defaultIngestMode"` // "copy" | "move" | "link"
	SilentIngest        bool   `json:"silentIngest"`
	Theme               string `json:"theme"`               // "system" | "dark" | "light"
	TrayClickAction     string `json:"trayClickAction"`     // "popover" | "main" | "launcher"
	CloseToTray         bool   `json:"closeToTray"`
	ShowLauncherPreview bool   `json:"showLauncherPreview"`
	AutoStart           bool   `json:"autoStart"`
}

// DefaultSettings returns out-of-the-box sensible defaults.
func DefaultSettings() Settings {
	return Settings{
		LibraryDir:          "library",
		LauncherHotkey:      "Alt+Space",
		DefaultIngestMode:   "copy",
		SilentIngest:        false,
		Theme:               "system",
		TrayClickAction:     "popover",
		CloseToTray:         true,
		ShowLauncherPreview: true,
		AutoStart:           false,
	}
}

func normalizeSettings(s Settings) Settings {
	def := DefaultSettings()
	if strings.TrimSpace(s.LibraryDir) == "" {
		s.LibraryDir = def.LibraryDir
	}
	if strings.TrimSpace(s.LauncherHotkey) == "" {
		s.LauncherHotkey = def.LauncherHotkey
	}
	switch s.DefaultIngestMode {
	case "copy", "move", "link":
	default:
		s.DefaultIngestMode = def.DefaultIngestMode
	}
	switch s.Theme {
	case "system", "dark", "light":
	default:
		s.Theme = def.Theme
	}
	switch s.TrayClickAction {
	case "popover", "main", "launcher":
	default:
		s.TrayClickAction = def.TrayClickAction
	}
	return s
}

// SettingsStore provides thread-safe reading and atomic writing for Settings.
type SettingsStore struct {
	mu       sync.RWMutex
	path     string
	settings Settings
}

// NewSettingsStore loads existing settings from path or creates defaults.
func NewSettingsStore(path string) (*SettingsStore, error) {
	store := &SettingsStore{
		path:     path,
		settings: DefaultSettings(),
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SettingsStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s.writeLocked(s.settings)
		}
		return err
	}
	loaded := DefaultSettings()
	if err := json.Unmarshal(data, &loaded); err != nil {
		return err
	}
	s.settings = normalizeSettings(loaded)
	return nil
}

// Get returns a copy of the current settings.
func (s *SettingsStore) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

// Save normalizes and atomically persists updated settings.
func (s *SettingsStore) Save(next Settings) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	normalized := normalizeSettings(next)
	if err := s.writeLocked(normalized); err != nil {
		return s.settings, err
	}
	s.settings = normalized
	return s.settings, nil
}

func (s *SettingsStore) writeLocked(cfg Settings) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.path)
}

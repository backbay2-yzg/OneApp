package service

import (
	"os"

	"oneapp/internal/config"
	"oneapp/internal/domain"
	"oneapp/internal/platform"
)

// SettingsSaveResult returns persisted settings along with live global hotkey binding state.
type SettingsSaveResult struct {
	Settings     config.Settings     `json:"settings"`
	HotkeyStatus domain.HotkeyStatus `json:"hotkeyStatus"`
}

// SettingsService exposes settings read/write and global hotkey rebinding.
type SettingsService struct {
	store  *config.SettingsStore
	bridge platform.OSBridge
	ui     UIHost
}

// NewSettingsService creates a SettingsService.
func NewSettingsService(store *config.SettingsStore, bridge platform.OSBridge) *SettingsService {
	return &SettingsService{
		store:  store,
		bridge: bridge,
	}
}

// GetSettings returns current persisted user settings.
func (s *SettingsService) GetSettings() config.Settings {
	return s.store.Get()
}

// GetHotkeyStatus returns current registration state of the launcher global hotkey.
func (s *SettingsService) GetHotkeyStatus() domain.HotkeyStatus {
	if s.ui != nil {
		return s.ui.GetHotkeyStatus()
	}
	return domain.HotkeyStatus{Accelerator: s.store.Get().LauncherHotkey}
}

// SaveSettings persists settings, updates auto-start registry if changed, and rebinds global hotkey.
func (s *SettingsService) SaveSettings(next config.Settings) (SettingsSaveResult, error) {
	prev := s.store.Get()
	saved, err := s.store.Save(next)
	if err != nil {
		return SettingsSaveResult{Settings: prev}, err
	}

	if prev.AutoStart != saved.AutoStart || saved.AutoStart {
		if exePath, exeErr := os.Executable(); exeErr == nil {
			_ = s.bridge.SetAutoStart("OneApp", exePath, saved.AutoStart)
		}
	}

	hk := s.GetHotkeyStatus()
	if s.ui != nil {
		if prev.LauncherHotkey != saved.LauncherHotkey || !hk.Registered {
			hk = s.ui.RebindLauncherHotkey(saved.LauncherHotkey)
		}
		s.ui.EmitEvent("settings:changed", saved)
	}

	return SettingsSaveResult{
		Settings:     saved,
		HotkeyStatus: hk,
	}, nil
}

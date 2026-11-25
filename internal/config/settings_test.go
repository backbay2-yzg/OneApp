package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultSettingsTrayClickAction(t *testing.T) {
	def := DefaultSettings()
	require.Equal(t, "popover", def.TrayClickAction)
}

func TestSettingsStoreTrayClickActionNormalization(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	store, err := NewSettingsStore(settingsPath)
	require.NoError(t, err)
	require.Equal(t, "popover", store.Get().TrayClickAction)

	for _, action := range []string{"popover", "main", "launcher"} {
		next := store.Get()
		next.TrayClickAction = action
		saved, saveErr := store.Save(next)
		require.NoError(t, saveErr)
		require.Equal(t, action, saved.TrayClickAction)

		reloaded, reloadErr := NewSettingsStore(settingsPath)
		require.NoError(t, reloadErr)
		require.Equal(t, action, reloaded.Get().TrayClickAction)
	}

	invalid := store.Get()
	invalid.TrayClickAction = "unknown-action"
	saved, err := store.Save(invalid)
	require.NoError(t, err)
	require.Equal(t, "popover", saved.TrayClickAction)
}

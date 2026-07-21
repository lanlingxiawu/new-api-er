package system_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// saveThemeState snapshots the theme setting var and the common theme atomic
// so each test is independent (syncThemeToCommon mutates common's global).
func saveThemeState(t *testing.T) {
	t.Helper()
	origSetting := themeSettings
	origCommon := common.GetTheme()
	t.Cleanup(func() {
		themeSettings = origSetting
		common.SetTheme(origCommon)
	})
}

func TestGetThemeSettings_ReturnsLivePointerAndDefault(t *testing.T) {
	got := GetThemeSettings()
	require.NotNil(t, got)
	assert.Same(t, &themeSettings, got)
	assert.Same(t, got, GetThemeSettings())
	// Default frontend is "classic" per theme.go.
	assert.Equal(t, "classic", themeSettings.Frontend)
}

// UpdateAndSyncTheme -> syncThemeToCommon -> common.SetTheme. common.SetTheme
// only accepts "default"/"classic" and silently ignores anything else
// (decision coverage across the accepted set and the ignored path).
func TestUpdateAndSyncTheme(t *testing.T) {
	tests := []struct {
		name        string
		frontend    string
		seedCommon  string // common theme before the sync
		wantCommon  string
		description string
	}{
		{
			name:       "default_is_synced",
			frontend:   "default",
			seedCommon: "classic",
			wantCommon: "default",
		},
		{
			name:       "classic_is_synced",
			frontend:   "classic",
			seedCommon: "default",
			wantCommon: "classic",
		},
		{
			name:       "unknown_value_ignored_common_unchanged",
			frontend:   "neon", // not accepted by common.SetTheme
			seedCommon: "classic",
			wantCommon: "classic", // stays at seeded value
		},
		{
			name:       "empty_value_ignored_common_unchanged",
			frontend:   "",
			seedCommon: "default",
			wantCommon: "default",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			saveThemeState(t)
			common.SetTheme(tc.seedCommon)
			themeSettings.Frontend = tc.frontend

			UpdateAndSyncTheme()

			assert.Equal(t, tc.wantCommon, common.GetTheme())
			// The setting struct itself is never mutated by the sync.
			assert.Equal(t, tc.frontend, GetThemeSettings().Frontend)
		})
	}
}

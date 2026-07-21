package console_setting

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// marshalArray builds a JSON array string for validator input. A single
// map[string]interface{} is wrapped into a one-element array; a
// []map[string]interface{} is marshaled as-is.
func marshalArray(t *testing.T, v interface{}) string {
	t.Helper()
	switch item := v.(type) {
	case map[string]interface{}:
		b, err := json.Marshal([]map[string]interface{}{item})
		require.NoError(t, err)
		return string(b)
	default:
		b, err := json.Marshal(v)
		require.NoError(t, err)
		return string(b)
	}
}

// saveConsoleSetting snapshots the package-global consoleSetting and restores
// it on cleanup so getter tests can mutate the fields freely.
func saveConsoleSetting(t *testing.T) {
	t.Helper()
	orig := consoleSetting
	t.Cleanup(func() { consoleSetting = orig })
}

func TestGetConsoleSetting_ReturnsGlobalPointer(t *testing.T) {
	saveConsoleSetting(t)
	got := GetConsoleSetting()
	assert.Same(t, &consoleSetting, got)

	// Mutation through the pointer is visible on the global (documents that the
	// getter hands out the live instance, not a copy).
	got.ApiInfo = "changed"
	assert.Equal(t, "changed", consoleSetting.ApiInfo)
}

func TestDefaultConsoleSetting_Defaults(t *testing.T) {
	// All four panels default to enabled; string payloads default empty.
	assert.True(t, defaultConsoleSetting.ApiInfoEnabled)
	assert.True(t, defaultConsoleSetting.UptimeKumaEnabled)
	assert.True(t, defaultConsoleSetting.AnnouncementsEnabled)
	assert.True(t, defaultConsoleSetting.FAQEnabled)
	assert.Empty(t, defaultConsoleSetting.ApiInfo)
	assert.Empty(t, defaultConsoleSetting.UptimeKumaGroups)
	assert.Empty(t, defaultConsoleSetting.Announcements)
	assert.Empty(t, defaultConsoleSetting.FAQ)
}

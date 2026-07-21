package model_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withQwenSettings(t *testing.T, s QwenSettings) {
	t.Helper()
	original := qwenSettings
	t.Cleanup(func() { qwenSettings = original })
	qwenSettings = s
}

func TestGetQwenSettings_ReturnsPointerToGlobal(t *testing.T) {
	require.Same(t, &qwenSettings, GetQwenSettings())
}

// IsSyncImageModel uses substring (strings.Contains) matching, not equality.
func TestIsSyncImageModel(t *testing.T) {
	withQwenSettings(t, QwenSettings{SyncImageModels: []string{
		"qwen-image",
		"wan2.6",
	}})

	// exact
	assert.True(t, IsSyncImageModel("qwen-image"))
	// substring: configured entry is contained in the queried model name
	assert.True(t, IsSyncImageModel("qwen-image-edit-plus-2025-12-15"))
	assert.True(t, IsSyncImageModel("prefix-wan2.6-suffix"))
	// no configured entry is a substring
	assert.False(t, IsSyncImageModel("gpt-image"))
	assert.False(t, IsSyncImageModel(""))
}

func TestIsSyncImageModel_EmptyList(t *testing.T) {
	withQwenSettings(t, QwenSettings{SyncImageModels: nil})
	assert.False(t, IsSyncImageModel("qwen-image"))
}

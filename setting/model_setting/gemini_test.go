package model_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withGeminiSettings(t *testing.T, s GeminiSettings) {
	t.Helper()
	original := geminiSettings
	t.Cleanup(func() { geminiSettings = original })
	geminiSettings = s
}

func TestGetGeminiSettings_ReturnsPointerToGlobal(t *testing.T) {
	require.Same(t, &geminiSettings, GetGeminiSettings())
}

func TestGetGeminiSettings_Defaults(t *testing.T) {
	// Sanity-check the shipped defaults so a regression in the literal is caught.
	got := GetGeminiSettings()
	assert.Equal(t, "OFF", got.SafetySettings["default"])
	assert.Equal(t, "v1beta", got.VersionSettings["default"])
	assert.Equal(t, "v1", got.VersionSettings["gemini-1.0-pro"])
	assert.False(t, got.ThinkingAdapterEnabled)
	assert.True(t, got.FunctionCallThoughtSignatureEnabled)
	assert.True(t, got.RemoveFunctionResponseIdEnabled)
}

// ---------------------------------------------------------------------------
// GetGeminiSafetySetting — key hit vs default fallback
// ---------------------------------------------------------------------------

func TestGetGeminiSafetySetting(t *testing.T) {
	withGeminiSettings(t, GeminiSettings{SafetySettings: map[string]string{
		"default":        "OFF",
		"gemini-2.5-pro": "BLOCK_NONE",
	}})

	assert.Equal(t, "BLOCK_NONE", GetGeminiSafetySetting("gemini-2.5-pro")) // hit
	assert.Equal(t, "OFF", GetGeminiSafetySetting("unknown-model"))         // miss -> default
	assert.Equal(t, "OFF", GetGeminiSafetySetting(""))                      // empty -> default
}

func TestGetGeminiSafetySetting_MissingDefaultReturnsEmpty(t *testing.T) {
	// No "default" key configured; a miss returns the zero value of the map.
	withGeminiSettings(t, GeminiSettings{SafetySettings: map[string]string{"a": "B"}})
	assert.Equal(t, "", GetGeminiSafetySetting("nope"))
}

// ---------------------------------------------------------------------------
// GetGeminiVersionSetting — key hit vs default fallback
// ---------------------------------------------------------------------------

func TestGetGeminiVersionSetting(t *testing.T) {
	withGeminiSettings(t, GeminiSettings{VersionSettings: map[string]string{
		"default":        "v1beta",
		"gemini-1.0-pro": "v1",
	}})

	assert.Equal(t, "v1", GetGeminiVersionSetting("gemini-1.0-pro")) // hit
	assert.Equal(t, "v1beta", GetGeminiVersionSetting("gemini-x"))   // miss -> default
}

// ---------------------------------------------------------------------------
// IsGeminiModelSupportImagine — membership over the slice
// ---------------------------------------------------------------------------

func TestIsGeminiModelSupportImagine(t *testing.T) {
	withGeminiSettings(t, GeminiSettings{SupportedImagineModels: []string{
		"gemini-2.0-flash-exp",
		"gemini-2.5-flash-image",
	}})

	assert.True(t, IsGeminiModelSupportImagine("gemini-2.0-flash-exp"))   // first
	assert.True(t, IsGeminiModelSupportImagine("gemini-2.5-flash-image")) // last
	assert.False(t, IsGeminiModelSupportImagine("gemini-2.0-flash"))      // substring, not exact -> false
	assert.False(t, IsGeminiModelSupportImagine(""))                      // empty
}

func TestIsGeminiModelSupportImagine_EmptyList(t *testing.T) {
	withGeminiSettings(t, GeminiSettings{SupportedImagineModels: nil})
	assert.False(t, IsGeminiModelSupportImagine("anything"))
}

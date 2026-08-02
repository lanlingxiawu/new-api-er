package model_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withGeminiSettings(t *testing.T, s GeminiSettings) {
	t.Helper()
	original := geminiSettings
	t.Cleanup(func() { ReplaceGeminiSettings(original) })
	// 必须走 Replace：直接改包级变量不会重新发布快照。
	ReplaceGeminiSettings(s)
}

// 返回不可变快照，而不是可变全局的指针。
func TestGetGeminiSettings_ReturnsImmutableSnapshot(t *testing.T) {
	got := GetGeminiSettings()
	require.NotNil(t, got)
	require.NotSame(t, &geminiSettings, got)
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

func TestGetGeminiSafetySetting_MissingDefaultFallsBackToOff(t *testing.T) {
	// 没有配置 "default" 键时，读取回落到 defaultGeminiSafetySetting 而不是空串，
	// 避免把空阈值发给上游。
	withGeminiSettings(t, GeminiSettings{SafetySettings: map[string]string{"a": "B"}})
	assert.Equal(t, defaultGeminiSafetySetting, GetGeminiSafetySetting("nope"))
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

func TestGeminiSafetySettingsReadNormalization(t *testing.T) {
	original := geminiSettings.SafetySettings
	t.Cleanup(func() {
		geminiSettings.SafetySettings = original
	})

	tests := []struct {
		name     string
		settings map[string]string
		key      string
		want     string
	}{
		{
			name:     "nil map gets OFF default",
			settings: nil,
			key:      "HARM_CATEGORY_HATE_SPEECH",
			want:     "OFF",
		},
		{
			name: "missing default gets OFF without replacing existing values",
			settings: map[string]string{
				"HARM_CATEGORY_HATE_SPEECH": "BLOCK_SOME",
			},
			key:  "HARM_CATEGORY_HATE_SPEECH",
			want: "BLOCK_SOME",
		},
		{
			name: "empty default gets OFF",
			settings: map[string]string{
				"default": "",
			},
			key:  "HARM_CATEGORY_HATE_SPEECH",
			want: "OFF",
		},
		{
			name: "empty override falls back to configured default",
			settings: map[string]string{
				"default":                   "BLOCK_ONLY_HIGH",
				"HARM_CATEGORY_HATE_SPEECH": "",
			},
			key:  "HARM_CATEGORY_HATE_SPEECH",
			want: "BLOCK_ONLY_HIGH",
		},
		{
			name: "historical invalid nonempty default is preserved",
			settings: map[string]string{
				"default": "BLOCK_SOME",
			},
			key:  "HARM_CATEGORY_HATE_SPEECH",
			want: "BLOCK_SOME",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			geminiSettings.SafetySettings = test.settings

			assert.Equal(t, test.want, GetGeminiSafetySetting(test.key))
		})
	}
}

func TestValidateGeminiSafetySettings(t *testing.T) {
	valid := []string{
		`{}`,
		`{"default":""}`,
		`{"HARM_CATEGORY_HATE_SPEECH":""}`,
		`{"default":"OFF"}`,
		`{"default":"BLOCK_NONE"}`,
		`{"default":"BLOCK_ONLY_HIGH"}`,
		`{"default":"BLOCK_MEDIUM_AND_ABOVE"}`,
		`{"default":"BLOCK_LOW_AND_ABOVE"}`,
		`{"default":"HARM_BLOCK_THRESHOLD_UNSPECIFIED"}`,
	}
	for _, value := range valid {
		require.NoError(t, ValidateGeminiSafetySettings(value), value)
	}

	invalid := []string{
		`null`,
		`[]`,
		`{"default":1}`,
		`{"default":"BLOCK_SOME"}`,
		`{"default":" off "}`,
		`{"default":`,
	}
	for _, value := range invalid {
		assert.Error(t, ValidateGeminiSafetySettings(value), value)
	}
}

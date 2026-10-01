package gemini

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThinkingConfigForEffort_ModelFamilies(t *testing.T) {
	budget := func(v int) *int { return &v }
	for _, tc := range []struct {
		model, effort string
		ok            bool
		budget        *int
		level         string
		applied       string
	}{
		// Budget families, including clamping into the model's range.
		{"gemini-2.5-flash-native-audio-preview", "none", true, budget(0), "", "none"},
		{"gemini-2.5-flash-lite", "minimal", true, budget(1024), "", "low"},
		{"gemini-2.5-pro", "none", true, budget(128), "", "low"},
		{"gemini-2.5-pro", "medium", true, budget(8192), "", "medium"},
		{"GEMINI-2.5-FLASH", "high", true, budget(24576), "", "high"},
		// Not configurable.
		{"gemini-2.5-flash-image", "low", false, nil, "", ""},
		{"gemini-2.5-flash-preview-tts", "low", false, nil, "", ""},
		{"gemini-2.0-flash-live-001", "low", false, nil, "", ""},
		{"gemini-3-pro-image-preview", "low", false, nil, "", ""},
		{"nano-banana-pro", "low", false, nil, "", ""},
		// Level families.
		{"gemini-flash-latest", "low", true, nil, "low", "low"},
		{"gemini-flash-lite-latest", "none", true, nil, "minimal", "minimal"},
		{"gemini-pro-latest", "minimal", true, nil, "low", "low"},
		{"gemini-3.1-flash-image-preview", "low", true, nil, "minimal", "minimal"},
		{"gemini-3.1-flash-image-preview", "medium", true, nil, "high", "high"},
		{"gemini-3.1-flash-image-preview", "turbo", false, nil, "", ""},
		{"gemini-3-pro-preview", "xhigh", true, nil, "high", "high"},
		{"gemini-3.1-pro-preview", "high", true, nil, "high", "high"},
		// Unknown family and unknown effort.
		{"gemini-2.0-flash", "low", false, nil, "", ""},
		{"gemini-2.5-flash", "turbo", false, nil, "", ""},
	} {
		t.Run(tc.model+"/"+tc.effort, func(t *testing.T) {
			config, applied, ok := thinkingConfigForEffort(tc.model, tc.effort)
			require.Equal(t, tc.ok, ok)
			if !ok {
				assert.Nil(t, config)
				return
			}
			assert.Equal(t, tc.budget, config.ThinkingBudget)
			assert.Equal(t, tc.level, config.ThinkingLevel)
			assert.Equal(t, tc.applied, applied)
		})
	}
}

func TestApplyReasoningEffort_NilRequestAndNilInfo(t *testing.T) {
	assert.NotPanics(t, func() { ApplyReasoningEffort(nil, nil, "gemini-2.5-flash", "none") })

	req := &dto.GeminiChatRequest{}
	ApplyReasoningEffort(req, nil, "gemini-2.5-flash", " Low ")
	require.NotNil(t, req.GenerationConfig.ThinkingConfig)
	require.NotNil(t, req.GenerationConfig.ThinkingConfig.ThinkingBudget)
	assert.Equal(t, 1024, *req.GenerationConfig.ThinkingConfig.ThinkingBudget)
}

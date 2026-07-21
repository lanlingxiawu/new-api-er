package operation_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// saveToolPrices snapshots the config + rebuilds the atomic index afterwards.
func saveToolPrices(t *testing.T) {
	t.Helper()
	orig := toolPriceSetting.Prices
	t.Cleanup(func() {
		toolPriceSetting.Prices = orig
		RebuildToolPriceIndex()
	})
}

// GetToolPriceForModel: default price when no override matches.
func TestGetToolPriceForModel_DefaultLookup(t *testing.T) {
	saveToolPrices(t)
	toolPriceSetting.Prices = map[string]float64{"web_search": 10.0}
	RebuildToolPriceIndex()
	assert.Equal(t, 10.0, GetToolPriceForModel("web_search", "gpt-4o"))
	assert.Equal(t, 10.0, GetToolPriceForModel("web_search", ""))
}

// GetToolPriceForModel: longest-prefix override wins over shorter one and default.
func TestGetToolPriceForModel_LongestPrefixWins(t *testing.T) {
	saveToolPrices(t)
	toolPriceSetting.Prices = map[string]float64{
		"web_search_preview":               10.0, // default for the tool
		"web_search_preview:gpt-4o*":       25.0,
		"web_search_preview:gpt-4o-mini*":  30.0, // longer prefix
	}
	RebuildToolPriceIndex()

	// "gpt-4o-mini-2024" matches both prefixes; longer (gpt-4o-mini) must win.
	assert.Equal(t, 30.0, GetToolPriceForModel("web_search_preview", "gpt-4o-mini-2024"))
	// "gpt-4o-2024" matches only the shorter prefix.
	assert.Equal(t, 25.0, GetToolPriceForModel("web_search_preview", "gpt-4o-2024"))
	// A model matching no prefix falls back to the tool default.
	assert.Equal(t, 10.0, GetToolPriceForModel("web_search_preview", "claude-3"))
	// Empty model name skips the prefix loop and uses the default.
	assert.Equal(t, 10.0, GetToolPriceForModel("web_search_preview", ""))
}

// GetToolPriceForModel: unknown tool with neither prefix nor default → 0.
func TestGetToolPriceForModel_UnknownToolReturnsZero(t *testing.T) {
	saveToolPrices(t)
	toolPriceSetting.Prices = map[string]float64{"web_search": 10.0}
	RebuildToolPriceIndex()
	assert.Equal(t, 0.0, GetToolPriceForModel("nonexistent_tool", "gpt-4o"))
}

// GetToolPriceForModel: a tool that has prefixes but the model matches none,
// and there is no default entry → 0.
func TestGetToolPriceForModel_PrefixMissNoDefaultReturnsZero(t *testing.T) {
	saveToolPrices(t)
	toolPriceSetting.Prices = map[string]float64{
		"only_override:gpt-4o*": 5.0,
	}
	RebuildToolPriceIndex()
	assert.Equal(t, 0.0, GetToolPriceForModel("only_override", "claude-3"))
	assert.Equal(t, 5.0, GetToolPriceForModel("only_override", "gpt-4o-2024"))
}

// GetToolPriceForModel: nil index fallback path (index not yet built).
func TestGetToolPriceForModel_NilIndexFallsBackToDefaults(t *testing.T) {
	prev := currentIndex.Load()
	t.Cleanup(func() { currentIndex.Store(prev) })
	currentIndex.Store(nil)

	// Known default tool resolves from the hardcoded defaultToolPrices map.
	assert.Equal(t, 10.0, GetToolPriceForModel("web_search", "gpt-4o"))
	// Unknown tool with nil index → 0.
	assert.Equal(t, 0.0, GetToolPriceForModel("unknown", "gpt-4o"))
}

func TestGetToolPrice_Wrapper(t *testing.T) {
	saveToolPrices(t)
	toolPriceSetting.Prices = map[string]float64{"file_search": 2.5}
	RebuildToolPriceIndex()
	assert.Equal(t, 2.5, GetToolPrice("file_search"))
	assert.Equal(t, 0.0, GetToolPrice("nope"))
}

// RebuildToolPriceIndex merges defaults + hardcoded overrides + admin config,
// with admin config taking precedence.
func TestRebuildToolPriceIndex_AdminOverridesDefault(t *testing.T) {
	saveToolPrices(t)
	toolPriceSetting.Prices = map[string]float64{"web_search": 99.0}
	RebuildToolPriceIndex()
	assert.Equal(t, 99.0, GetToolPriceForModel("web_search", "gpt-4o"))
	// A default-only key still resolves (google_search not overridden).
	assert.Equal(t, 14.0, GetToolPriceForModel("google_search", "gemini"))
}

func TestRebuildToolPriceIndex_DefaultOverridesPresent(t *testing.T) {
	saveToolPrices(t)
	// Empty admin config: hardcoded defaultToolPriceOverrides must still apply.
	toolPriceSetting.Prices = map[string]float64{}
	RebuildToolPriceIndex()
	assert.Equal(t, 25.0, GetToolPriceForModel("web_search_preview", "gpt-4o-2024"))
	assert.Equal(t, 10.0, GetToolPriceForModel("web_search_preview", "o1"))
}

// ── GetGPTImage1PriceOnceCall ─────────────────────────────────────────────

func TestGetGPTImage1PriceOnceCall_KnownCombinations(t *testing.T) {
	cases := []struct {
		quality, size string
		want          float64
	}{
		{"low", "1024x1024", GPTImage1Low1024x1024},
		{"low", "1024x1536", GPTImage1Low1024x1536},
		{"low", "1536x1024", GPTImage1Low1536x1024},
		{"medium", "1024x1024", GPTImage1Medium1024x1024},
		{"medium", "1024x1536", GPTImage1Medium1024x1536},
		{"medium", "1536x1024", GPTImage1Medium1536x1024},
		{"high", "1024x1024", GPTImage1High1024x1024},
		{"high", "1024x1536", GPTImage1High1024x1536},
		{"high", "1536x1024", GPTImage1High1536x1024},
	}
	for _, c := range cases {
		t.Run(c.quality+"_"+c.size, func(t *testing.T) {
			assert.Equal(t, c.want, GetGPTImage1PriceOnceCall(c.quality, c.size))
		})
	}
}

func TestGetGPTImage1PriceOnceCall_Fallbacks(t *testing.T) {
	// Unknown quality → fallback to High1024x1024.
	assert.Equal(t, GPTImage1High1024x1024, GetGPTImage1PriceOnceCall("ultra", "1024x1024"))
	// Known quality but unknown size → fallback.
	assert.Equal(t, GPTImage1High1024x1024, GetGPTImage1PriceOnceCall("low", "999x999"))
	// Both unknown → fallback.
	assert.Equal(t, GPTImage1High1024x1024, GetGPTImage1PriceOnceCall("", ""))
}

// ── GetGeminiInputAudioPricePerMillionTokens ──────────────────────────────

func TestGetGeminiInputAudioPricePerMillionTokens_AllPrefixes(t *testing.T) {
	cases := []struct {
		model string
		want  float64
	}{
		// Order matters: native-audio checked before the generic 2.5-flash prefixes.
		{"gemini-2.5-flash-preview-native-audio-x", Gemini25FlashNativeAudioInputAudioPrice},
		{"gemini-2.5-flash-preview-lite-y", Gemini25FlashLitePreviewInputAudioPrice},
		{"gemini-2.5-flash-preview-05", Gemini25FlashPreviewInputAudioPrice},
		{"gemini-2.5-flash-prod", Gemini25FlashProductionInputAudioPrice},
		{"gemini-2.0-flash-exp", Gemini20FlashInputAudioPrice},
		{"gemini-robotics-er-1.5-preview", GeminiRoboticsER15InputAudioPrice},
		{"gpt-4o", 0},
		{"", 0},
	}
	for _, c := range cases {
		t.Run(c.model, func(t *testing.T) {
			assert.Equal(t, c.want, GetGeminiInputAudioPricePerMillionTokens(c.model))
		})
	}
}

func TestDefaultToolPriceSetting_Shipped(t *testing.T) {
	// The registered default merges the base prices and the non-reasoning overrides.
	require.NotNil(t, toolPriceSetting.Prices)
	assert.Equal(t, 10.0, defaultToolPrices["web_search"])
	assert.Equal(t, 25.0, defaultToolPriceOverrides["web_search_preview:gpt-4o*"])
}

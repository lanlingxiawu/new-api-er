package service

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// billing_usage.go — pure usage normalization transforms.
// No DB / Redis / network. Covers effectiveBillingUsage, usageBillingPathForLog,
// appendUsageBillingPathForLog, usageFromBillingUsage and the three
// provider-specific converters.
// ===========================================================================

// --- usageBillingPathForLog -------------------------------------------------

func TestBillusage_PathForLog_LocalWins(t *testing.T) {
	// local flag short-circuits everything, even a populated billing usage.
	u := &dto.Usage{BillingUsage: &dto.BillingUsage{Source: dto.BillingUsageSourceOAIChat}}
	assert.Equal(t, usageBillingPathLocal, usageBillingPathForLog(true, u))
}

func TestBillusage_PathForLog_NilUsageUpstream(t *testing.T) {
	assert.Equal(t, usageBillingPathUpstream, usageBillingPathForLog(false, nil))
	assert.Equal(t, usageBillingPathUpstream, usageBillingPathForLog(false, &dto.Usage{}))
}

func TestBillusage_PathForLog_SourceMatrix(t *testing.T) {
	cases := []struct {
		name      string
		source    string
		semantic  string
		estimated bool
		want      string
	}{
		{"oai-chat", dto.BillingUsageSourceOAIChat, "", false, usageBillingPathOpenAI},
		{"oai-responses", dto.BillingUsageSourceOAIResponses, "", false, usageBillingPathOpenAI},
		{"oai-semantic", "", dto.BillingUsageSemanticOpenAI, false, usageBillingPathOpenAI},
		{"oai-estimated", dto.BillingUsageSourceOAIChat, "", true, usageBillingPathOpenAIEstimated},
		{"claude-source", dto.BillingUsageSourceClaudeMessages, "", false, usageBillingPathAnthropic},
		{"claude-semantic", "", dto.BillingUsageSemanticAnthropic, false, usageBillingPathAnthropic},
		{"claude-estimated", dto.BillingUsageSourceClaudeMessages, "", true, usageBillingPathAnthropicEstimated},
		{"gemini-source", dto.BillingUsageSourceGeminiChat, "", false, usageBillingPathGemini},
		{"gemini-semantic", "", dto.BillingUsageSemanticGemini, false, usageBillingPathGemini},
		{"gemini-estimated", dto.BillingUsageSourceGeminiChat, "", true, usageBillingPathGeminiEstimated},
		{"unknown", "something-else", "", false, usageBillingPathUpstream},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u := &dto.Usage{BillingUsage: &dto.BillingUsage{
				Source:    c.source,
				Semantic:  c.semantic,
				Estimated: c.estimated,
			}}
			assert.Equal(t, c.want, usageBillingPathForLog(false, u))
		})
	}
}

func TestBillusage_PathForLog_CaseInsensitiveAndTrimmed(t *testing.T) {
	u := &dto.Usage{BillingUsage: &dto.BillingUsage{Source: "  OAI_CHAT  "}}
	assert.Equal(t, usageBillingPathOpenAI, usageBillingPathForLog(false, u))
}

// --- appendUsageBillingPathForLog ------------------------------------------

func TestBillusage_AppendPath_NilOtherNoPanic(t *testing.T) {
	require.NotPanics(t, func() { appendUsageBillingPathForLog(nil, false, nil) })
}

func TestBillusage_AppendPath_CreatesAdminInfo(t *testing.T) {
	other := map[string]interface{}{}
	appendUsageBillingPathForLog(other, true, nil)
	admin, ok := other["admin_info"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, usageBillingPathLocal, admin["usage_billing_path"])
}

func TestBillusage_AppendPath_PreservesExistingAdminInfo(t *testing.T) {
	other := map[string]interface{}{
		"admin_info": map[string]interface{}{"foo": "bar"},
	}
	appendUsageBillingPathForLog(other, false, &dto.Usage{BillingUsage: &dto.BillingUsage{Source: dto.BillingUsageSourceGeminiChat}})
	admin := other["admin_info"].(map[string]interface{})
	assert.Equal(t, "bar", admin["foo"])
	assert.Equal(t, usageBillingPathGemini, admin["usage_billing_path"])
}

// --- effectiveBillingUsage / usageFromBillingUsage --------------------------

func TestBillusage_Effective_NoBillingUsageReturnsOriginal(t *testing.T) {
	u := &dto.Usage{PromptTokens: 10}
	assert.Same(t, u, effectiveBillingUsage(u))
	assert.Nil(t, effectiveBillingUsage(nil))
}

func TestBillusage_Effective_MismatchedInnerReturnsOriginal(t *testing.T) {
	// Source claims openai but no OpenAIUsage payload -> falls through to original.
	u := &dto.Usage{PromptTokens: 5, BillingUsage: &dto.BillingUsage{Source: dto.BillingUsageSourceOAIChat}}
	assert.Same(t, u, effectiveBillingUsage(u))
}

func TestBillusage_FromOpenAI_FillsSymmetricCounts(t *testing.T) {
	bu := &dto.BillingUsage{
		Source:      dto.BillingUsageSourceOAIResponses,
		OpenAIUsage: &dto.Usage{InputTokens: 100, OutputTokens: 40},
	}
	u := &dto.Usage{BillingUsage: bu}
	got := effectiveBillingUsage(u)
	require.NotSame(t, u, got)
	assert.Equal(t, 100, got.PromptTokens, "prompt filled from input")
	assert.Equal(t, 40, got.CompletionTokens, "completion filled from output")
	assert.Equal(t, 140, got.TotalTokens)
	assert.Equal(t, dto.BillingUsageSemanticOpenAI, got.UsageSemantic)
	assert.Equal(t, dto.BillingUsageSourceOAIResponses, got.UsageSource)
}

func TestBillusage_FromOpenAI_PromptFillsInputWhenReversed(t *testing.T) {
	bu := &dto.BillingUsage{
		Source:      dto.BillingUsageSourceOAIChat,
		OpenAIUsage: &dto.Usage{PromptTokens: 7, CompletionTokens: 3},
	}
	got := effectiveBillingUsage(&dto.Usage{BillingUsage: bu})
	assert.Equal(t, 7, got.InputTokens)
	assert.Equal(t, 3, got.OutputTokens)
	assert.Equal(t, 10, got.TotalTokens)
}

func TestBillusage_FromClaude_TextOnlyPromptAndCacheDetails(t *testing.T) {
	bu := &dto.BillingUsage{
		Source: dto.BillingUsageSourceClaudeMessages,
		ClaudeUsage: &dto.ClaudeUsage{
			InputTokens:              50,
			OutputTokens:             20,
			CacheReadInputTokens:     8,
			CacheCreationInputTokens: 5,
		},
	}
	got := effectiveBillingUsage(&dto.Usage{BillingUsage: bu})
	assert.Equal(t, 50, got.PromptTokens, "claude prompt is text-only input_tokens")
	assert.Equal(t, 20, got.CompletionTokens)
	assert.Equal(t, 70, got.TotalTokens)
	// InputTokens = input + cache read + cache creation
	assert.Equal(t, 63, got.InputTokens)
	assert.Equal(t, 8, got.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 5, got.PromptTokensDetails.CachedCreationTokens)
	assert.Equal(t, dto.BillingUsageSemanticAnthropic, got.UsageSemantic)
}

func TestBillusage_FromClaude_SplitCacheCreationPrefersStructWithFallback(t *testing.T) {
	bu := &dto.BillingUsage{
		Semantic: dto.BillingUsageSemanticAnthropic,
		ClaudeUsage: &dto.ClaudeUsage{
			InputTokens:                 10,
			OutputTokens:                2,
			CacheCreation:               &dto.ClaudeCacheCreationUsage{Ephemeral5mInputTokens: 4},
			ClaudeCacheCreation1hTokens: 9, // struct 1h is zero -> fallback to flat field
		},
	}
	got := effectiveBillingUsage(&dto.Usage{BillingUsage: bu})
	assert.Equal(t, 4, got.ClaudeCacheCreation5mTokens, "from cache_creation struct")
	assert.Equal(t, 9, got.ClaudeCacheCreation1hTokens, "fallback to flat field when struct is 0")
}

func TestBillusage_FromGemini_AggregatesModalitiesAndThoughts(t *testing.T) {
	bu := &dto.BillingUsage{
		Source: dto.BillingUsageSourceGeminiChat,
		GeminiUsageMetadata: &dto.GeminiUsageMetadata{
			PromptTokenCount:        30,
			ToolUsePromptTokenCount: 5,
			CandidatesTokenCount:    12,
			ThoughtsTokenCount:      3,
			CachedContentTokenCount: 7,
			TotalTokenCount:         50,
			PromptTokensDetails: []dto.GeminiPromptTokensDetails{
				{Modality: "TEXT", TokenCount: 20},
				{Modality: "AUDIO", TokenCount: 10},
			},
			CandidatesTokensDetails: []dto.GeminiPromptTokensDetails{
				{Modality: "IMAGE", TokenCount: 2},
			},
		},
	}
	got := effectiveBillingUsage(&dto.Usage{BillingUsage: bu})
	assert.Equal(t, 35, got.PromptTokens, "prompt + tool-use prompt")
	assert.Equal(t, 15, got.CompletionTokens, "candidates + thoughts")
	assert.Equal(t, 50, got.TotalTokens)
	assert.Equal(t, 3, got.CompletionTokenDetails.ReasoningTokens)
	assert.Equal(t, 7, got.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 20, got.PromptTokensDetails.TextTokens)
	assert.Equal(t, 10, got.PromptTokensDetails.AudioTokens)
	assert.Equal(t, 2, got.CompletionTokenDetails.ImageTokens)
}

func TestBillusage_FromGemini_TotalDerivedWhenZero(t *testing.T) {
	bu := &dto.BillingUsage{
		Semantic: dto.BillingUsageSemanticGemini,
		GeminiUsageMetadata: &dto.GeminiUsageMetadata{
			PromptTokenCount:     40,
			CandidatesTokenCount: 10,
			// TotalTokenCount omitted (0) -> derived as prompt + completion
		},
	}
	got := effectiveBillingUsage(&dto.Usage{BillingUsage: bu})
	assert.Equal(t, 50, got.TotalTokens)
	// prompt has no detail breakdown -> TextTokens defaults to prompt total
	assert.Equal(t, 40, got.PromptTokensDetails.TextTokens)
}

func TestBillusage_FromGemini_CompletionDerivedFromTotal(t *testing.T) {
	bu := &dto.BillingUsage{
		Semantic: dto.BillingUsageSemanticGemini,
		GeminiUsageMetadata: &dto.GeminiUsageMetadata{
			PromptTokenCount: 30,
			TotalTokenCount:  45,
			// CandidatesTokenCount 0 -> completion derived as total - prompt
		},
	}
	got := effectiveBillingUsage(&dto.Usage{BillingUsage: bu})
	assert.Equal(t, 15, got.CompletionTokens)
}

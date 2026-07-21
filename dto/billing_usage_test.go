package dto

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// HasClaudeUsageTokens
// ---------------------------------------------------------------------------

func TestHasClaudeUsageTokens(t *testing.T) {
	assert.False(t, HasClaudeUsageTokens(nil))
	assert.False(t, HasClaudeUsageTokens(&ClaudeUsage{}), "all-zero => false")
	assert.True(t, HasClaudeUsageTokens(&ClaudeUsage{InputTokens: 1}))
	assert.True(t, HasClaudeUsageTokens(&ClaudeUsage{OutputTokens: 1}))
	assert.True(t, HasClaudeUsageTokens(&ClaudeUsage{CacheCreationInputTokens: 1}))
	assert.True(t, HasClaudeUsageTokens(&ClaudeUsage{CacheReadInputTokens: 1}))
	assert.True(t, HasClaudeUsageTokens(&ClaudeUsage{ClaudeCacheCreation5mTokens: 1}))
	assert.True(t, HasClaudeUsageTokens(&ClaudeUsage{ClaudeCacheCreation1hTokens: 1}))
	// nested CacheCreation
	assert.True(t, HasClaudeUsageTokens(&ClaudeUsage{CacheCreation: &ClaudeCacheCreationUsage{Ephemeral5mInputTokens: 1}}))
	assert.True(t, HasClaudeUsageTokens(&ClaudeUsage{CacheCreation: &ClaudeCacheCreationUsage{Ephemeral1hInputTokens: 1}}))
	assert.False(t, HasClaudeUsageTokens(&ClaudeUsage{CacheCreation: &ClaudeCacheCreationUsage{}}))
}

func TestNewClaudeMessagesBillingUsage(t *testing.T) {
	// all-zero => nil (must not shadow a non-zero top-level usage)
	assert.Nil(t, NewClaudeMessagesBillingUsage(&ClaudeUsage{}))
	assert.Nil(t, NewClaudeMessagesBillingUsage(nil))

	src := &ClaudeUsage{InputTokens: 10, ServerToolUse: &ClaudeServerToolUse{WebSearchRequests: 2}}
	bu := NewClaudeMessagesBillingUsage(src)
	require.NotNil(t, bu)
	assert.Equal(t, BillingUsageSourceClaudeMessages, bu.Source)
	assert.Equal(t, BillingUsageSemanticAnthropic, bu.Semantic)
	require.NotNil(t, bu.ClaudeUsage)
	assert.Equal(t, 10, bu.ClaudeUsage.InputTokens)
	// deep clone: mutating original does not change clone
	src.InputTokens = 999
	assert.Equal(t, 10, bu.ClaudeUsage.InputTokens)
	// ServerToolUse deep-cloned
	require.NotNil(t, bu.ClaudeUsage.ServerToolUse)
	assert.NotSame(t, src.ServerToolUse, bu.ClaudeUsage.ServerToolUse)
}

// ---------------------------------------------------------------------------
// HasOpenAIUsageTokens + OpenAI billing usage constructors
// ---------------------------------------------------------------------------

func TestHasOpenAIUsageTokens(t *testing.T) {
	assert.False(t, HasOpenAIUsageTokens(nil))
	assert.False(t, HasOpenAIUsageTokens(&Usage{}))
	assert.True(t, HasOpenAIUsageTokens(&Usage{PromptTokens: 1}))
	assert.True(t, HasOpenAIUsageTokens(&Usage{CompletionTokens: 1}))
	assert.True(t, HasOpenAIUsageTokens(&Usage{TotalTokens: 1}))
	assert.True(t, HasOpenAIUsageTokens(&Usage{InputTokens: 1}))
	assert.True(t, HasOpenAIUsageTokens(&Usage{OutputTokens: 1}))
	assert.True(t, HasOpenAIUsageTokens(&Usage{PromptCacheHitTokens: 1}))
	assert.True(t, HasOpenAIUsageTokens(&Usage{ClaudeCacheCreation5mTokens: 1}))
	assert.True(t, HasOpenAIUsageTokens(&Usage{ClaudeCacheCreation1hTokens: 1}))
	// prompt token details
	assert.True(t, HasOpenAIUsageTokens(&Usage{PromptTokensDetails: InputTokenDetails{CachedTokens: 1}}))
	assert.True(t, HasOpenAIUsageTokens(&Usage{PromptTokensDetails: InputTokenDetails{CacheWriteTokens: 1}}))
	assert.True(t, HasOpenAIUsageTokens(&Usage{PromptTokensDetails: InputTokenDetails{TextTokens: 1}}))
	assert.True(t, HasOpenAIUsageTokens(&Usage{PromptTokensDetails: InputTokenDetails{ImageTokens: 1}}))
	assert.True(t, HasOpenAIUsageTokens(&Usage{PromptTokensDetails: InputTokenDetails{AudioTokens: 1}}))
	assert.True(t, HasOpenAIUsageTokens(&Usage{PromptTokensDetails: InputTokenDetails{CachedCreationTokens: 1}}))
	// completion token details
	assert.True(t, HasOpenAIUsageTokens(&Usage{CompletionTokenDetails: OutputTokenDetails{ReasoningTokens: 1}}))
	assert.True(t, HasOpenAIUsageTokens(&Usage{CompletionTokenDetails: OutputTokenDetails{TextTokens: 1}}))
	assert.True(t, HasOpenAIUsageTokens(&Usage{CompletionTokenDetails: OutputTokenDetails{ImageTokens: 1}}))
	assert.True(t, HasOpenAIUsageTokens(&Usage{CompletionTokenDetails: OutputTokenDetails{AudioTokens: 1}}))
	// InputTokensDetails present (non-nil) => true
	assert.True(t, HasOpenAIUsageTokens(&Usage{InputTokensDetails: &InputTokenDetails{}}))
}

func TestNewOpenAIChatBillingUsage(t *testing.T) {
	assert.Nil(t, NewOpenAIChatBillingUsage(&Usage{}))
	src := &Usage{PromptTokens: 5, InputTokensDetails: &InputTokenDetails{CachedTokens: 2}}
	bu := NewOpenAIChatBillingUsage(src)
	require.NotNil(t, bu)
	assert.Equal(t, BillingUsageSourceOAIChat, bu.Source)
	assert.Equal(t, BillingUsageSemanticOpenAI, bu.Semantic)
	require.NotNil(t, bu.OpenAIUsage)
	assert.Equal(t, 5, bu.OpenAIUsage.PromptTokens)
	// clone: InputTokensDetails deep-copied
	require.NotNil(t, bu.OpenAIUsage.InputTokensDetails)
	assert.NotSame(t, src.InputTokensDetails, bu.OpenAIUsage.InputTokensDetails)
	// clone must drop nested BillingUsage
	assert.Nil(t, bu.OpenAIUsage.BillingUsage)
}

func TestNewOpenAIResponsesBillingUsage(t *testing.T) {
	assert.Nil(t, NewOpenAIResponsesBillingUsage(&Usage{}))
	bu := NewOpenAIResponsesBillingUsage(&Usage{OutputTokens: 3})
	require.NotNil(t, bu)
	assert.Equal(t, BillingUsageSourceOAIResponses, bu.Source)
	assert.Equal(t, BillingUsageSemanticOpenAI, bu.Semantic)
}

// ---------------------------------------------------------------------------
// HasGeminiUsageMetadataTokens + Gemini billing usage
// ---------------------------------------------------------------------------

func TestHasGeminiUsageMetadataTokens(t *testing.T) {
	assert.False(t, HasGeminiUsageMetadataTokens(nil))
	assert.False(t, HasGeminiUsageMetadataTokens(&GeminiUsageMetadata{}))
	assert.True(t, HasGeminiUsageMetadataTokens(&GeminiUsageMetadata{PromptTokenCount: 1}))
	assert.True(t, HasGeminiUsageMetadataTokens(&GeminiUsageMetadata{ToolUsePromptTokenCount: 1}))
	assert.True(t, HasGeminiUsageMetadataTokens(&GeminiUsageMetadata{CandidatesTokenCount: 1}))
	assert.True(t, HasGeminiUsageMetadataTokens(&GeminiUsageMetadata{TotalTokenCount: 1}))
	assert.True(t, HasGeminiUsageMetadataTokens(&GeminiUsageMetadata{ThoughtsTokenCount: 1}))
	assert.True(t, HasGeminiUsageMetadataTokens(&GeminiUsageMetadata{CachedContentTokenCount: 1}))
	// detail slices
	assert.True(t, HasGeminiUsageMetadataTokens(&GeminiUsageMetadata{PromptTokensDetails: []GeminiPromptTokensDetails{{TokenCount: 1}}}))
	assert.True(t, HasGeminiUsageMetadataTokens(&GeminiUsageMetadata{ToolUsePromptTokensDetails: []GeminiPromptTokensDetails{{TokenCount: 1}}}))
	assert.True(t, HasGeminiUsageMetadataTokens(&GeminiUsageMetadata{CandidatesTokensDetails: []GeminiPromptTokensDetails{{TokenCount: 1}}}))
	// all-zero detail => false
	assert.False(t, HasGeminiUsageMetadataTokens(&GeminiUsageMetadata{PromptTokensDetails: []GeminiPromptTokensDetails{{TokenCount: 0}}}))
}

func TestNewGeminiChatBillingUsage(t *testing.T) {
	assert.Nil(t, NewGeminiChatBillingUsage(&GeminiUsageMetadata{}))
	assert.Nil(t, NewGeminiChatBillingUsage(nil))
	md := &GeminiUsageMetadata{PromptTokenCount: 10, PromptTokensDetails: []GeminiPromptTokensDetails{{Modality: "TEXT", TokenCount: 10}}}
	bu := NewGeminiChatBillingUsage(md)
	require.NotNil(t, bu)
	assert.Equal(t, BillingUsageSourceGeminiChat, bu.Source)
	assert.Equal(t, BillingUsageSemanticGemini, bu.Semantic)
	assert.False(t, bu.Estimated)
	require.NotNil(t, bu.GeminiUsageMetadata)
	// deep clone of detail slice
	md.PromptTokensDetails[0].TokenCount = 99
	assert.Equal(t, 10, bu.GeminiUsageMetadata.PromptTokensDetails[0].TokenCount)
}

func TestNewEstimatedGeminiChatBillingUsage(t *testing.T) {
	assert.Nil(t, NewEstimatedGeminiChatBillingUsage(nil))
	// all-zero usage => nil
	assert.Nil(t, NewEstimatedGeminiChatBillingUsage(&Usage{}))

	// totalTokens present
	bu := NewEstimatedGeminiChatBillingUsage(&Usage{TotalTokens: 30, PromptTokens: 10, CompletionTokens: 20})
	require.NotNil(t, bu)
	assert.True(t, bu.Estimated)
	assert.Equal(t, 30, bu.GeminiUsageMetadata.TotalTokenCount)
	assert.Equal(t, 10, bu.GeminiUsageMetadata.PromptTokenCount)
	assert.Equal(t, 20, bu.GeminiUsageMetadata.CandidatesTokenCount)

	// totalTokens zero => derived from prompt+completion
	bu2 := NewEstimatedGeminiChatBillingUsage(&Usage{PromptTokens: 4, CompletionTokens: 6})
	require.NotNil(t, bu2)
	assert.Equal(t, 10, bu2.GeminiUsageMetadata.TotalTokenCount)
}

// ---------------------------------------------------------------------------
// CloneBillingUsage
// ---------------------------------------------------------------------------

func TestCloneBillingUsage(t *testing.T) {
	assert.Nil(t, CloneBillingUsage(nil))

	orig := &BillingUsage{
		Source:              "s",
		Semantic:            "sem",
		Estimated:           true,
		OpenAIUsage:         &Usage{PromptTokens: 1},
		ClaudeUsage:         &ClaudeUsage{InputTokens: 2, CacheCreation: &ClaudeCacheCreationUsage{Ephemeral5mInputTokens: 1}},
		GeminiUsageMetadata: &GeminiUsageMetadata{PromptTokenCount: 3, PromptTokensDetails: []GeminiPromptTokensDetails{{TokenCount: 3}}},
	}
	clone := CloneBillingUsage(orig)
	require.NotNil(t, clone)
	assert.Equal(t, "s", clone.Source)
	assert.True(t, clone.Estimated)

	// deep-clone: nested pointers differ
	assert.NotSame(t, orig.OpenAIUsage, clone.OpenAIUsage)
	assert.NotSame(t, orig.ClaudeUsage, clone.ClaudeUsage)
	assert.NotSame(t, orig.GeminiUsageMetadata, clone.GeminiUsageMetadata)

	// mutate original nested values -> clone unaffected
	orig.OpenAIUsage.PromptTokens = 100
	orig.ClaudeUsage.InputTokens = 100
	orig.GeminiUsageMetadata.PromptTokenCount = 100
	assert.Equal(t, 1, clone.OpenAIUsage.PromptTokens)
	assert.Equal(t, 2, clone.ClaudeUsage.InputTokens)
	assert.Equal(t, 3, clone.GeminiUsageMetadata.PromptTokenCount)
}

func TestCloneBillingUsage_NilNestedFields(t *testing.T) {
	// nested fields nil => clone keeps them nil, no panic
	clone := CloneBillingUsage(&BillingUsage{Source: "x"})
	require.NotNil(t, clone)
	assert.Nil(t, clone.OpenAIUsage)
	assert.Nil(t, clone.ClaudeUsage)
	assert.Nil(t, clone.GeminiUsageMetadata)
}

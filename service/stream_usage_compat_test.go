package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestStreamUsageEvidenceIsolatedByAttempt(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}}
	BeginStreamAttempt(c, info)
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			BeginStreamAttempt(c, info)
		}
		info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
		require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"usage":{"prompt_tokens":40,"prompt_tokens_details":{"cache_creation_input_tokens":30}}}`)))
		info.StreamSession.CommitDelivery([]byte(`{"choices":[{"delta":{"content":"hello"}}]}`))
		info.StreamSession.EndRead(io.EOF)
		usage := FinalizeStreamUsage(c, info, nil)
		require.Equal(t, 30, usage.PromptTokensDetails.CacheCreationTokensTotal())
	}
}

func TestStreamUsageCompatibilityReconstruction(t *testing.T) {
	evidence := map[string]int{
		"input_tokens": 100, "output_tokens": 2, "cache_write_tokens": 20, "cached_creation_tokens": 30,
		"openai_cache_creation_input_tokens": 25, "cache_write_tokens_details.ttl_12h_tokens": 90,
		"cached_tokens_details.text_tokens": 0, "cached_tokens_details.image_tokens": 12,
	}
	usage := BuildConfirmedStreamUsage(&relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI}, evidence)
	require.Equal(t, 30, usage.PromptTokensDetails.CacheCreationTokensTotal(), "cache aliases overlap, never add them")
	require.Equal(t, 30, usage.PromptTokensDetails.CacheWrite12hTokens(), "TTL subset is bounded by total writes")
	require.NotNil(t, usage.PromptTokensDetails.CachedTokensDetails.TextTokens)
	require.Zero(t, *usage.PromptTokensDetails.CachedTokensDetails.TextTokens)
	require.Nil(t, usage.PromptTokensDetails.CachedTokensDetails.AudioTokens)
	require.Equal(t, 12, *usage.PromptTokensDetails.CachedTokensDetails.ImageTokens)

	gemini := &relaycommon.RelayInfo{StreamUsageFormat: types.RelayFormatGemini}
	u := BuildConfirmedStreamUsage(gemini, map[string]int{"input_tokens": 10, "tool_use_prompt_tokens": 3, "output_tokens": 0, "total_tokens": 999})
	require.Equal(t, 13, u.PromptTokens)
	require.Zero(t, u.CompletionTokens, "a total never replaces explicit zero or absent component counts")
	require.Equal(t, 13, u.TotalTokens)
	u = BuildConfirmedStreamUsage(gemini, map[string]int{"output_tokens": 2, "tool_use_prompt_tokens": 3})
	applyStreamInputEstimate(u, 10)
	canonical, ok := u.BillingUsage.CanonicalUsage()
	require.True(t, ok)
	require.Equal(t, 13, canonical.PromptTokens)
	require.Equal(t, 2, canonical.CompletionTokens)
	require.True(t, u.BillingUsage.Estimated)
	gemini.SetEstimatePromptTokens(10)
	input, estimated := streamTimeoutInputUsage(gemini, map[string]int{"output_tokens": 2, "tool_use_prompt_tokens": 3}, true)
	require.True(t, estimated)
	canonical, ok = input.BillingUsage.CanonicalUsage()
	require.True(t, ok)
	require.Equal(t, 13, canonical.PromptTokens)
	require.Zero(t, canonical.CompletionTokens)
}

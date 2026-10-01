package relay

import (
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// An OpenAI-type channel in front of llama.cpp reports cached prompt tokens
// only as timings.cache_n. The plain handlers read it; an adapted request must
// too, or the cached part is billed at the full input price.
func TestHandleAdaptedUpstreamStream_LlamaCachedTokens(t *testing.T) {
	c, rec, info := bufferedCase(t)
	info.ChannelType = constant.ChannelTypeOpenAI
	body := strings.Join([]string{
		`data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"}}]}`,
		`data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11},"timings":{"cache_n":6,"prompt_n":4}}`,
		`data: [DONE]`,
		"",
	}, "\n")
	usage, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 6, usage.PromptTokensDetails.CachedTokens, "settled usage carries the cached tokens")
	// The body keeps upstream's own usage, as the plain handler returns
	// upstream's body unchanged.
	assert.Equal(t, int64(10), gjson.Get(rec.Body.String(), "usage.prompt_tokens").Int())
	assert.False(t, gjson.Get(rec.Body.String(), "usage.prompt_tokens_details").Exists())
}

// finish_reason=content_filter is recorded as the admin-visible reject reason,
// as the plain non-stream handler does.
func TestHandleAdaptedUpstreamStream_ContentFilterRecorded(t *testing.T) {
	c, _, info := bufferedCase(t)
	body := strings.Join([]string{
		`data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":"content_filter"}]}`,
		`data: [DONE]`,
		"",
	}, "\n")
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	assert.Equal(t, "openai_finish_reason=content_filter",
		common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason))
}

func TestHandleAdaptedUpstreamStream_NoRejectReasonOnStop(t *testing.T) {
	c, _, info := bufferedCase(t)
	body := `data: {"id":"c1","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}` + "\n" + `data: [DONE]` + "\n"
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	assert.Empty(t, common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason))
}

// Upstream response headers reach the client as on the plain non-stream path;
// only the SSE Content-Type is replaced, since the body is JSON now.
func TestHandleAdaptedUpstreamStream_CopiesUpstreamHeaders(t *testing.T) {
	c, rec, info := bufferedCase(t)
	resp := upstreamSSE(`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}` + "\n" + `data: [DONE]` + "\n")
	resp.Header.Set("X-Ratelimit-Remaining-Requests", "99")
	resp.Header.Set("Content-Length", "12345")
	_, apiErr := handleAdaptedUpstreamStream(c, info, resp)
	require.Nil(t, apiErr)
	assert.Equal(t, "99", rec.Header().Get("X-Ratelimit-Remaining-Requests"))
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.NotEqual(t, "12345", rec.Header().Get("Content-Length"), "length is our body's, not upstream's")
}

// The plain handler bills the estimated input when upstream reports
// prompt_tokens 0; the adapted path must bill the same, and return the usage
// it billed rather than upstream's zero.
func TestHandleAdaptedUpstreamStream_ZeroPromptTokensEstimated(t *testing.T) {
	c, rec, info := bufferedCase(t)
	info.SetEstimatePromptTokens(42)
	body := strings.Join([]string{
		`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		`data: {"id":"c1","choices":[],"usage":{"prompt_tokens":0,"completion_tokens":50,"total_tokens":50}}`,
		`data: [DONE]`,
		"",
	}, "\n")
	usage, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	assert.Equal(t, 42, usage.PromptTokens)
	assert.Equal(t, 50, usage.CompletionTokens)
	assert.Equal(t, 92, usage.TotalTokens)
	assert.Equal(t, int64(42), gjson.Get(rec.Body.String(), "usage.prompt_tokens").Int(), "the body shows the usage that was billed")
}

// Estimated usage (no usage from upstream, e.g. a timeout) still gets the
// channel-specific fields, as on the plain stream path.
func TestAdaptedStreamUsage_EstimatedUsageGetsLlamaCache(t *testing.T) {
	c, _, info := bufferedCase(t)
	info.ChannelType = constant.ChannelTypeOpenAI
	a := newChatStreamAggregator("m")
	text := "partial answer"
	a.AddChunk(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &text},
	}}})
	usage := adaptedStreamUsage(c, info, a, `{"choices":[{"index":0,"delta":{"content":"x"}}],"timings":{"cache_n":7}}`)
	assert.Equal(t, 7, usage.PromptTokensDetails.CachedTokens)
}

// Some upstreams send the same reasoning text under both reasoning_content and
// reasoning; it is one text, not two. And the field keeps upstream's name, as
// the plain path forwards upstream's message unchanged.
func TestHandleAdaptedUpstreamStream_ReasoningFields(t *testing.T) {
	c, rec, info := bufferedCase(t)
	body := strings.Join([]string{
		`data: {"id":"c1","choices":[{"index":0,"delta":{"reasoning_content":"think","reasoning":"think"}}]}`,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n")
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	assert.Equal(t, "think", gjson.Get(rec.Body.String(), "choices.0.message.reasoning_content").String())
	assert.False(t, gjson.Get(rec.Body.String(), "choices.0.message.reasoning").Exists())

	c2, rec2, info2 := bufferedCase(t)
	body2 := strings.Join([]string{
		`data: {"id":"c1","choices":[{"index":0,"delta":{"reasoning":"th"}}]}`,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"reasoning":"ink"}}]}`,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n")
	_, apiErr = handleAdaptedUpstreamStream(c2, info2, upstreamSSE(body2))
	require.Nil(t, apiErr)
	assert.Equal(t, "think", gjson.Get(rec2.Body.String(), "choices.0.message.reasoning").String())
	assert.False(t, gjson.Get(rec2.Body.String(), "choices.0.message.reasoning_content").Exists())
}

// The adapted 504 carries our request id like every other relay error, so a
// user can report it.
func TestHandleAdaptedUpstreamStream_TimeoutCarriesRequestID(t *testing.T) {
	c, rec, info := bufferedCase(t)
	c.Set(common.RequestIdKey, "rid-timeout-1")
	markTimedOut(c)
	restore := stubSettlement(func(*gin.Context, *relaycommon.RelayInfo, *dto.Usage, []string) {})
	defer restore()
	// A role-only frame is no output, so the timeout is answered with the 504.
	body := `data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant"}}]}` + "\n"
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	assert.Equal(t, http.StatusGatewayTimeout, rec.Code)
	assert.Contains(t, gjson.Get(rec.Body.String(), "error.message").String(), "(request id: rid-timeout-1)")
}

// An empty reasoning_content next to a non-empty reasoning must not hide the
// text.
func TestHandleAdaptedUpstreamStream_EmptyReasoningContentDoesNotHideReasoning(t *testing.T) {
	c, rec, info := bufferedCase(t)
	body := strings.Join([]string{
		`data: {"id":"c1","choices":[{"index":0,"delta":{"reasoning_content":"","reasoning":"think"}}]}`,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n")
	_, apiErr := handleAdaptedUpstreamStream(c, info, upstreamSSE(body))
	require.Nil(t, apiErr)
	assert.Equal(t, "think", gjson.Get(rec.Body.String(), "choices.0.message.reasoning").String())
}

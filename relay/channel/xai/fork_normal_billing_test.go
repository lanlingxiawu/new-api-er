package xai

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Normal xAI wire usage is finalized by the real handler before pure pricing.
// Synthetic USD/million rates avoid live model-price dependencies and balances.
func forkXAINormalUsage(t *testing.T, metadata string, stream bool) (*relaycommon.RelayInfo, *dto.Usage) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeXai)
	info := &relaycommon.RelayInfo{IsStream: stream, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, FinalRequestRelayFormat: types.RelayFormatOpenAI, OriginModelName: "grok-4", ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeXai, UpstreamModelName: "grok-4"}}
	info.SetEstimatePromptTokens(999)
	contentType := "application/json"
	frame := `{"id":"normal_fixture","model":"grok-4","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]`
	if stream {
		contentType = "text/event-stream"
		frame = `{"id":"normal_fixture","model":"grok-4","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]`
		service.BeginStreamAttempt(c, info)
	}
	if metadata != "" {
		frame += `,"usage":` + metadata
	}
	frame += `}`
	if stream {
		frame = "data: " + frame + "\n\ndata: [DONE]\n\n"
	}
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(frame))}
	var usage *dto.Usage
	var apiErr *types.NewAPIError
	if stream {
		info.StreamSession.ObserveTransport(resp, nil)
		info.StreamSession.ObserveHTTP(resp)
		usage, apiErr = xAIStreamHandler(c, info, resp)
	} else {
		usage, apiErr = xAIHandler(c, info, resp)
	}
	require.Nil(t, apiErr)
	require.Contains(t, rec.Body.String(), "hello")
	usage = service.FinalizeStreamUsage(c, info, usage)
	if stream {
		require.False(t, info.StreamResult.Failed)
		require.True(t, info.StreamResult.EffectiveContent)
	}
	return info, usage
}

func TestForkXAINormalBilling(t *testing.T) {
	previousGin := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() {
		gin.SetMode(previousGin)
	})

	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name, metadata                                                   string
			prompt, completion, cache, reasoning, inclusiveQuota, splitQuota int
		}{
			// Cache is inside input and reasoning is inside total-minus-prompt.
			// Inclusive (100*4+20*12)*.25=160; split (20*4+20*12+80*.8)*.25=96.
			{"cache and reasoning", `{"prompt_tokens":100,"completion_tokens":13,"total_tokens":120,"prompt_tokens_details":{"cached_tokens":80},"completion_tokens_details":{"reasoning_tokens":7}}`, 100, 20, 80, 7, 160, 96},
			{"explicit zero input", `{"prompt_tokens":0,"completion_tokens":20,"total_tokens":20}`, 0, 20, 0, 0, 60, 60},
			// Normal xAI arithmetic retains absent prompt as zero when total is present.
			{"missing input with total", `{"completion_tokens":20,"total_tokens":20}`, 0, 20, 0, 0, 60, 60},
			// The grok estimator rounds the five Latin characters to 2 tokens.
			{"missing all usage", "", 999, 2, 0, 0, 1005, 1005},
		} {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, tc.name), func(t *testing.T) {
				info, usage := forkXAINormalUsage(t, tc.metadata, stream)
				if !stream && tc.metadata == "" {
					// The nonstream adapter returns nil, leaving PostText's request
					// input fallback to the service-level accounting tests.
					require.Nil(t, usage)
					return
				}
				require.NotNil(t, usage)
				wantCache, wantReasoning, wantSplit := tc.cache, tc.reasoning, tc.splitQuota

				require.Equal(t, tc.prompt, usage.PromptTokens)
				require.Equal(t, tc.completion, usage.CompletionTokens)
				require.Equal(t, wantCache, usage.PromptTokensDetails.CachedTokens)
				require.Equal(t, wantReasoning, usage.CompletionTokenDetails.ReasoningTokens)
				for _, pricing := range []struct {
					expression string
					expected   int
					split      bool
				}{
					{`tier("inclusive", p * 4 + c * 12)`, tc.inclusiveQuota, false},
					{`tier("split", p * 4 + c * 12 + cr * 0.8)`, wantSplit, true},
				} {
					info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: pricing.expression, ExprHash: billingexpr.ExprHashString(pricing.expression), GroupRatio: 0.5, QuotaPerUnit: 500000}
					params := service.BuildTieredTokenParams(usage, false, billingexpr.UsedVarsByHash(pricing.expression, info.TieredBillingSnapshot.ExprHash))
					require.Equal(t, float64(tc.prompt), params.Len)
					if pricing.split {
						require.Equal(t, float64(tc.prompt-wantCache), params.P)
						require.Equal(t, float64(wantCache), params.CR)
					}
					require.Equal(t, float64(tc.completion), params.C, "reasoning is billed exactly once")
					applied, quota, result := service.TryTieredSettle(info, params)
					require.True(t, applied)
					require.NotNil(t, result)
					require.Equal(t, pricing.expected, quota, pricing.expression)
				}
			})
		}
	}

}

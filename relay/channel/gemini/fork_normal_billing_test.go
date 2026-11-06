package gemini

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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Normal vendor responses traverse the production adapter and the same original
// usage selection used by PostTextConsumeQuota, then pure tiered settlement.
// Prices below are synthetic USD/million; no balance, database or AI is accessed.
func forkGeminiNormalUsage(t *testing.T, metadata string, stream bool, format types.RelayFormat) (*relaycommon.RelayInfo, *dto.Usage) {
	t.Helper()
	frame := `{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}]`
	if metadata != "" {
		frame += `,"usageMetadata":` + metadata
	}
	frame += `}`
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1beta/models/gemini:generateContent", nil)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeGemini)
	info := &relaycommon.RelayInfo{IsStream: stream, DisablePing: true, ShouldIncludeUsage: true, RelayFormat: types.RelayFormatGemini, FinalRequestRelayFormat: types.RelayFormatGemini, OriginModelName: "gemini-2.5-flash", ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeGemini, UpstreamModelName: "gemini-2.5-flash"}}
	info.RelayFormat = format
	info.SetEstimatePromptTokens(999)
	contentType := "application/json"
	if stream {
		contentType, frame = "text/event-stream", "data: "+frame+"\n\n"
		service.BeginStreamAttempt(c, info)
	}
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(frame))}
	if stream {
		info.StreamSession.ObserveTransport(resp, nil)
		info.StreamSession.ObserveHTTP(resp)
	}
	var usage *dto.Usage
	var apiErr *types.NewAPIError
	switch {
	case stream && format == types.RelayFormatOpenAIResponses:
		usage, apiErr = GeminiResponsesStreamHandler(c, info, resp)
	case stream && format == types.RelayFormatOpenAI:
		usage, apiErr = GeminiChatStreamHandler(c, info, resp)
	case stream:
		usage, apiErr = GeminiTextGenerationStreamHandler(c, info, resp)
	case format == types.RelayFormatOpenAIResponses:
		usage, apiErr = GeminiResponsesHandler(c, info, resp)
	case format == types.RelayFormatOpenAI:
		usage, apiErr = GeminiChatHandler(c, info, resp)
	default:
		usage, apiErr = GeminiTextGenerationHandler(c, info, resp)
	}
	require.Nil(t, apiErr)
	require.Contains(t, rec.Body.String(), "hello")
	usage = service.FinalizeStreamUsage(c, info, usage)
	require.NotNil(t, usage)
	if stream {
		require.False(t, info.StreamResult.Failed)
		require.True(t, info.StreamResult.EffectiveContent)
	}
	if metadata != "" && !gjson.Get(metadata, "promptTokenCount").Exists() {
		require.NotNil(t, usage.BillingUsage)
		require.True(t, usage.BillingUsage.Estimated, "the missing input is local estimation")
		require.True(t, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens))
		if stream {
			_, confirmed := info.StreamResult.Diagnostic.UsageEvidence["input_tokens"]
			require.False(t, confirmed, "estimated input must not become upstream evidence")
		}
		if format == types.RelayFormatGemini {
			require.NotContains(t, rec.Body.String(), `"promptTokenCount":999`, "native upstream bytes retain their original absence")
		}
	}
	if usage.BillingUsage != nil {
		canonical, ok := usage.BillingUsage.CanonicalUsage()
		require.True(t, ok)
		usage = canonical
	}
	// Only the missing-input repair promises agreement with converted wire usage.
	// Reported zero keeps upstream's existing nonstream flat-usage fallback, while
	// its original billing snapshot remains authoritative for settlement.
	if metadata != "" && !gjson.Get(metadata, "promptTokenCount").Exists() && format != types.RelayFormatGemini {
		wireUsage := gjson.Get(rec.Body.String(), "usage")
		if stream {
			for _, line := range strings.Split(rec.Body.String(), "\n") {
				data, ok := strings.CutPrefix(line, "data: ")
				if !ok {
					continue
				}
				if format == types.RelayFormatOpenAIResponses {
					if gjson.Get(data, "type").String() == "response.completed" {
						wireUsage = gjson.Get(data, "response.usage")
					}
				} else if candidate := gjson.Get(data, "usage"); candidate.IsObject() {
					wireUsage = candidate
				}
			}
		}
		require.True(t, wireUsage.IsObject(), "converted terminal usage must be delivered: %s", rec.Body.String())
		inputKey, outputKey, detailsKey := "prompt_tokens", "completion_tokens", "prompt_tokens_details"
		if format == types.RelayFormatOpenAIResponses {
			inputKey, outputKey, detailsKey = "input_tokens", "output_tokens", "input_tokens_details"
		}
		require.Equal(t, usage.PromptTokens, int(wireUsage.Get(inputKey).Int()))
		require.Equal(t, usage.CompletionTokens, int(wireUsage.Get(outputKey).Int()))
		require.Equal(t, usage.TotalTokens, int(wireUsage.Get("total_tokens").Int()))
		require.Equal(t, usage.PromptTokensDetails.CachedTokens, int(wireUsage.Get(detailsKey+".cached_tokens").Int()))
		require.Equal(t, usage.PromptTokensDetails.AudioTokens, int(wireUsage.Get(detailsKey+".audio_tokens").Int()))
		require.Equal(t, usage.CompletionTokenDetails.AudioTokens, int(wireUsage.Get("completion_tokens_details.audio_tokens").Int()))
		require.Equal(t, usage.CompletionTokenDetails.ReasoningTokens, int(wireUsage.Get("completion_tokens_details.reasoning_tokens").Int()))
	}
	return info, usage
}

func TestForkGeminiNormalBilling(t *testing.T) {
	previousGin := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() {
		gin.SetMode(previousGin)
	})

	for _, stream := range []bool{false, true} {
		for _, format := range []types.RelayFormat{types.RelayFormatGemini, types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses} {
			for _, tc := range []struct {
				name, metadata                                          string
				prompt, completion, cache, reasoning, audioIn, audioOut int
				inclusiveQuota, splitQuota                              int
			}{
				// Inclusive: (130*4 + 27*12)*0.25=211. Split:
				// ((130-40-40)*4+(27-8)*12+40*.8+40*2+8*20)*.25=175.
				{"multimodal", `{"promptTokenCount":100,"toolUsePromptTokenCount":30,"candidatesTokenCount":20,"thoughtsTokenCount":7,"totalTokenCount":157,"cachedContentTokenCount":40,"promptTokensDetails":[{"modality":"TEXT","tokenCount":60},{"modality":"AUDIO","tokenCount":40}],"toolUsePromptTokensDetails":[{"modality":"TEXT","tokenCount":30}],"candidatesTokensDetails":[{"modality":"TEXT","tokenCount":12},{"modality":"AUDIO","tokenCount":8}]}`, 130, 27, 40, 7, 40, 8, 211, 175},
				{"explicit zero prompt", `{"promptTokenCount":0,"candidatesTokenCount":20}`, 0, 20, 0, 0, 0, 0, 60, 60},
				{"explicit zero with tool input", `{"promptTokenCount":0,"toolUsePromptTokenCount":30,"candidatesTokenCount":20,"toolUsePromptTokensDetails":[{"modality":"TEXT","tokenCount":20},{"modality":"AUDIO","tokenCount":10}]}`, 30, 20, 0, 0, 10, 0, 90, 85},
				// Missing input uses the request's 999-token estimate: 999+20*3=1059.
				{"missing prompt", `{"candidatesTokenCount":20}`, 999, 20, 0, 0, 0, 0, 1059, 1059},
				// The 999 estimated base input and 30 confirmed tool input are added once.
				{"missing prompt with details", `{"toolUsePromptTokenCount":30,"candidatesTokenCount":20,"thoughtsTokenCount":7,"cachedContentTokenCount":40,"promptTokensDetails":[{"modality":"AUDIO","tokenCount":40}],"toolUsePromptTokensDetails":[{"modality":"TEXT","tokenCount":30}],"candidatesTokensDetails":[{"modality":"AUDIO","tokenCount":8}]}`, 1029, 27, 40, 7, 40, 8, 1110, 1074},
				// "hello" is one locally estimated token: 999+1*3=1002.
				{"missing metadata", "", 999, 1, 0, 0, 0, 0, 1002, 1002},
			} {
				t.Run(fmt.Sprintf("stream=%t/format=%s/%s", stream, format, tc.name), func(t *testing.T) {
					info, usage := forkGeminiNormalUsage(t, tc.metadata, stream, format)
					wantPrompt, inclusiveQuota, splitQuota := tc.prompt, tc.inclusiveQuota, tc.splitQuota

					assert.Equal(t, wantPrompt, usage.PromptTokens)
					require.Equal(t, tc.completion, usage.CompletionTokens)
					require.Equal(t, tc.cache, usage.PromptTokensDetails.CachedTokens)
					require.Equal(t, tc.reasoning, usage.CompletionTokenDetails.ReasoningTokens)
					require.Equal(t, tc.audioIn, usage.PromptTokensDetails.AudioTokens)
					require.Equal(t, tc.audioOut, usage.CompletionTokenDetails.AudioTokens)
					for _, pricing := range []struct {
						expression string
						expected   int
						split      bool
					}{
						{`tier("inclusive", p * 4 + c * 12)`, inclusiveQuota, false},
						{`tier("split", p * 4 + c * 12 + cr * 0.8 + ai * 2 + ao * 20)`, splitQuota, true},
					} {
						info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: pricing.expression, ExprHash: billingexpr.ExprHashString(pricing.expression), GroupRatio: 0.5, QuotaPerUnit: 500000}
						params := service.BuildTieredTokenParams(usage, false, billingexpr.UsedVarsByHash(pricing.expression, info.TieredBillingSnapshot.ExprHash))
						assert.Equal(t, float64(wantPrompt), params.Len)
						if pricing.split {
							assert.Equal(t, float64(max(0, wantPrompt-tc.cache-tc.audioIn)), params.P)
							require.Equal(t, float64(tc.completion-tc.audioOut), params.C)
						} else {
							assert.Equal(t, float64(wantPrompt), params.P)
							require.Equal(t, float64(tc.completion), params.C)
						}
						applied, quota, result := service.TryTieredSettle(info, params)
						require.True(t, applied)
						require.NotNil(t, result)
						require.Equal(t, pricing.expected, quota, pricing.expression)
					}
				})
			}
		}
	}

}

func TestForkGeminiPromptEstimatePreservesOriginalSnapshot(t *testing.T) {
	for _, original := range []bool{false, true} {
		for _, estimate := range []int{0, 999} {
			t.Run(fmt.Sprintf("original=%t/estimate=%d", original, estimate), func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				info := &relaycommon.RelayInfo{}
				info.SetEstimatePromptTokens(estimate)
				metadata := &dto.GeminiUsageMetadata{ToolUsePromptTokenCount: 30, CandidatesTokenCount: 20}
				if original {
					metadata.BillingUsage = dto.NewGeminiChatBillingUsage(&dto.GeminiUsageMetadata{PromptTokenCount: 0, CandidatesTokenCount: 20})
				}
				usage := buildUsageFromGeminiMetadata(metadata, estimate)
				before := usage.BillingUsage
				supplementGeminiMissingPrompt(c, info, &usage, metadata, false)
				require.Zero(t, metadata.PromptTokenCount, "raw metadata must not be mutated")
				require.Zero(t, before.GeminiUsageMetadata.PromptTokenCount, "previous billing snapshot must not be mutated")
				canonical, ok := usage.BillingUsage.CanonicalUsage()
				require.True(t, ok)
				switch {
				case original:
					require.Same(t, before, usage.BillingUsage)
					require.Zero(t, canonical.PromptTokens, "an original confirmed zero stays authoritative")
					require.False(t, usage.BillingUsage.Estimated)
				case estimate == 0:
					require.Same(t, before, usage.BillingUsage)
					require.Equal(t, 30, canonical.PromptTokens)
					require.False(t, usage.BillingUsage.Estimated)
				default:
					require.NotSame(t, before, usage.BillingUsage)
					require.Equal(t, 1029, canonical.PromptTokens)
					require.Equal(t, 1049, canonical.TotalTokens)
					require.True(t, usage.BillingUsage.Estimated)
					require.True(t, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens))
				}
			})
		}
	}
}

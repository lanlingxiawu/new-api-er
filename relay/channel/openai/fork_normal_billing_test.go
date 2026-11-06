package openai

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const forkNormalToolName = "fork_normal_priced_tool"
const forkNormalTierExpression = `len <= 1000 ? tier("small", p * 2 + c * 8 + cr * 0.5 + cc * 2.5 + cc12h * 5) : tier("large", p * 4 + c * 16 + cr + cc * 5 + cc12h * 10)`

type forkNormalUsageCase struct {
	name                                       string
	input, cached, shortWrite, longWrite       int
	reasoning, tools, normalizedP, tieredQuota int
}

// This fixture exercises real Chat/Responses handlers and the production pure
// pricing path. Wallets, counters, logs and tool surcharges are checked by the
// service integration matrix; this suite does not call settlement side effects.
func TestForkNormalChatResponsesBilling(t *testing.T) {
	operation_setting.SetToolPriceForTest(forkNormalToolName, 6)
	t.Cleanup(func() { operation_setting.DeleteToolPriceForTest(forkNormalToolName) })
	// Hand calculations at 500,000 quota/USD and group 1.5 (factor 0.75 for
	// per-million-token expressions), always 200 output tokens:
	// plain/reasoning/tools: (1000*2 + 200*8)*0.75 = 2700.
	// cache: (600*2 + 400*0.5 + 200*8)*0.75 = 2250.
	// writes: (500*2 + 300*0.5 + 100*2.5 + 100*5 + 200*8)*0.75 = 2625.
	// boundary: (601*4 + 400 + 200*16)*0.75 = 4503; len=1001, not p=601.
	cases := []forkNormalUsageCase{
		{"plain", 1000, 0, 0, 0, 0, 0, 1000, 2700},
		{"reasoning_included_once", 1000, 0, 0, 0, 80, 0, 1000, 2700},
		{"cache_read", 1000, 400, 0, 0, 0, 0, 600, 2250},
		{"cache_write_TTL", 1000, 300, 100, 100, 80, 0, 500, 2625},
		{"two_priced_tool_calls", 1000, 0, 0, 0, 0, 2, 1000, 2700},
		{"above_context_threshold", 1001, 400, 0, 0, 80, 0, 601, 4503},
	}
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses} {
		for _, stream := range []bool{false, true} {
			for _, tc := range cases {
				for _, pricing := range []string{"tiered_tokens", "fixed_request", "fixed_zero"} {
					t.Run(fmt.Sprintf("%s/stream=%t/%s/%s", format, stream, tc.name, pricing), func(t *testing.T) {
						expr, wantQuota, wantTier := forkNormalTierExpression, tc.tieredQuota, "small"
						if tc.input > 1000 {
							wantTier = "large"
						}
						if pricing == "fixed_request" {
							// 0.02 USD * 500000 * 1.5 = 15000, once per request.
							expr, wantQuota, wantTier = `tier("request", fixed(0.02))`, 15000, "request"
						} else if pricing == "fixed_zero" {
							expr, wantQuota, wantTier = `tier("free", fixed(0))`, 0, "free"
						}
						body := forkNormalTextBody(format, stream, tc)
						contentType, path := "application/json", "/v1/chat/completions"
						if stream {
							contentType = "text/event-stream"
						}
						if format == types.RelayFormatOpenAIResponses {
							path = "/v1/responses"
						}
						recorder := httptest.NewRecorder()
						ctx, _ := gin.CreateTestContext(recorder)
						ctx.Request = httptest.NewRequest(http.MethodPost, path, nil)
						info := &relaycommon.RelayInfo{IsStream: stream, DisablePing: true, RelayFormat: format, FinalRequestRelayFormat: format, OriginModelName: "gpt-4o", ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
						if format == types.RelayFormatOpenAIResponses {
							info.RelayMode = relayconstant.RelayModeResponses
						}
						info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: expr, ExprHash: billingexpr.ExprHashString(expr), GroupRatio: 1.5, QuotaPerUnit: 500000}
						info.PriceData.GroupRatioInfo.GroupRatio = 1.5
						info.SetEstimatePromptTokens(17) // A deliberately wrong estimate must not replace actual usage.
						service.BeginStreamAttempt(ctx, info)
						resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}
						if stream {
							info.StreamSession.ObserveTransport(resp, nil)
							info.StreamDiagnostic.Observe(resp)
							info.StreamSession.ObserveHTTP(resp)
						}
						var usage *dto.Usage
						var apiErr *types.NewAPIError
						switch {
						case format == types.RelayFormatOpenAIResponses && stream:
							usage, apiErr = OaiResponsesStreamHandler(ctx, info, resp)
						case format == types.RelayFormatOpenAIResponses:
							usage, apiErr = OaiResponsesHandler(ctx, info, resp)
						case stream:
							usage, apiErr = OaiStreamHandler(ctx, info, resp)
						default:
							usage, apiErr = OpenaiHandler(ctx, info, resp)
						}
						require.Nil(t, apiErr)
						usage = service.FinalizeStreamUsage(ctx, info, usage)
						require.NotNil(t, usage)
						require.Equal(t, tc.input, usage.PromptTokens)
						require.Equal(t, 200, usage.CompletionTokens)
						require.Equal(t, tc.input+200, usage.TotalTokens)
						require.Equal(t, tc.cached, usage.PromptTokensDetails.CachedTokens)
						require.Equal(t, tc.shortWrite+tc.longWrite, usage.PromptTokensDetails.CacheCreationTokensTotal())
						require.Equal(t, tc.longWrite, usage.PromptTokensDetails.CacheWrite12hTokens())
						require.Equal(t, tc.reasoning, usage.CompletionTokenDetails.ReasoningTokens)
						if stream {
							require.False(t, info.StreamResult.Failed)
							require.True(t, info.StreamResult.Diagnostic.UsageFinal)
							require.Equal(t, "upstream", info.StreamResult.UsageSource)
							counts := forkVendorEventCounts(recorder.Body.String())
							require.Zero(t, counts["error"])
							terminal := "DONE"
							if format == types.RelayFormatOpenAIResponses {
								terminal = "response.completed"
							}
							require.Equal(t, 1, counts[terminal])
							require.Equal(t, 1, counts["text_delta"])
						} else {
							require.Nil(t, info.StreamResult)
							require.Equal(t, body, recorder.Body.String())
						}
						if tc.tools != 0 {
							require.NotNil(t, info.ResponsesUsageInfo)
							require.NotNil(t, info.ResponsesUsageInfo.BuiltInTools[forkNormalToolName])
							require.Equal(t, 2, info.ResponsesUsageInfo.BuiltInTools[forkNormalToolName].CallCount, "fragments and terminal snapshots must not duplicate tool calls")
							t.Log("PostText service boundary: two calls at $6/1000, group 1.5, add 9000 quota to the base fee")
						}
						_, err := billingexpr.CompileFromCache(expr)
						require.NoError(t, err)
						params := service.BuildTieredTokenParams(usage, false, billingexpr.UsedVars(expr))
						require.Equal(t, float64(tc.input), params.Len)
						require.Equal(t, float64(200), params.C, "reasoning is already included in total completion")
						if pricing == "tiered_tokens" {
							require.Equal(t, float64(tc.normalizedP), params.P)
							require.Equal(t, float64(tc.cached), params.CR)
							require.Equal(t, float64(tc.shortWrite), params.CC)
							require.Equal(t, float64(tc.longWrite), params.CC12h)
						}
						ok, quota, result := service.TryTieredSettle(info, params)
						require.True(t, ok)
						require.NotNil(t, result, "an expression error must not silently fall back to reservation")
						require.Equal(t, wantQuota, quota)
						require.Equal(t, wantTier, result.MatchedTier)
						if pricing != "tiered_tokens" {
							require.Equal(t, billingexpr.BillingUnitRequest, result.BillingUnit)
							require.NotNil(t, result.FixedPrice)
						} else {
							require.Equal(t, billingexpr.BillingUnitToken, result.BillingUnit)
						}
						require.Nil(t, info.QuotaClamp)
						t.Logf("actual prompt=%d cache=%d write30m=%d write12h=%d output=200 reasoning=%d tier=%s base_quota=%d", tc.input, tc.cached, tc.shortWrite, tc.longWrite, tc.reasoning, result.MatchedTier, quota)
					})
				}
			}
		}
	}
}

func forkNormalTextBody(format types.RelayFormat, stream bool, tc forkNormalUsageCase) string {
	inputDetails := fmt.Sprintf(`{"cached_tokens":%d,"cache_write_tokens":%d,"cache_write_tokens_details":{"ttl_30m_tokens":%d,"ttl_12h_tokens":%d}}`, tc.cached, tc.shortWrite+tc.longWrite, tc.shortWrite, tc.longWrite)
	outputDetails := fmt.Sprintf(`{"reasoning_tokens":%d}`, tc.reasoning)
	if format == types.RelayFormatOpenAI {
		usage := fmt.Sprintf(`{"prompt_tokens":%d,"completion_tokens":200,"total_tokens":%d,"prompt_tokens_details":%s,"completion_tokens_details":%s}`, tc.input, tc.input+200, inputDetails, outputDetails)
		tools, toolFrames := "", ""
		for i := 0; i < tc.tools; i++ {
			if tools != "" {
				tools += ","
			}
			tools += fmt.Sprintf(`{"id":"call_%d","type":"function","function":{"name":%q,"arguments":"{\"value\":1}"}}`, i, forkNormalToolName)
			toolFrames += forkVendorSSE(fmt.Sprintf(`{"id":"chatcmpl_fixture","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":%d,"id":"call_%d","type":"function","function":{"name":%q,"arguments":"{\"value\":"}}]},"finish_reason":null}]}`, i, i, forkNormalToolName))
			toolFrames += forkVendorSSE(fmt.Sprintf(`{"id":"chatcmpl_fixture","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":%d,"function":{"arguments":"1}"}}]},"finish_reason":null}]}`, i))
		}
		finish := "stop"
		if tc.tools > 0 {
			finish = "tool_calls"
		}
		if !stream {
			toolField := ""
			if tools != "" {
				toolField = `,"tool_calls":[` + tools + `]`
			}
			return fmt.Sprintf(`{"id":"chatcmpl_fixture","object":"chat.completion","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"MATRIX_PAYLOAD"%s},"finish_reason":%q}],"usage":%s}`, toolField, finish, usage)
		}
		return forkVendorSSE(forkVendorChatStart) + forkVendorSSE(forkVendorChatText) + toolFrames + forkVendorSSE(fmt.Sprintf(`{"id":"chatcmpl_fixture","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":%q}]}`, finish)) + forkVendorSSE(`{"id":"chatcmpl_fixture","object":"chat.completion.chunk","choices":[],"usage":`+usage+`}`) + "data: [DONE]\n\n"
	}
	usage := fmt.Sprintf(`{"input_tokens":%d,"output_tokens":200,"total_tokens":%d,"input_tokens_details":%s,"output_tokens_details":%s}`, tc.input, tc.input+200, inputDetails, outputDetails)
	output := `{"id":"msg_fixture","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"MATRIX_PAYLOAD","annotations":[]}]}`
	frames := forkVendorSSE(forkVendorResponsesStart) + forkVendorSSE(forkVendorResponsesItem) + forkVendorSSE(forkVendorResponsesPart) + forkVendorSSE(forkVendorResponsesText) + forkVendorSSE(forkVendorResponsesTextDone) + forkVendorSSE(forkVendorResponsesPartDone) + forkVendorSSE(forkVendorResponsesItemDone)
	for i := 0; i < tc.tools; i++ {
		item := fmt.Sprintf(`{"id":"fc_%d","type":"function_call","status":"completed","call_id":"call_%d","name":%q,"arguments":"{\"value\":1}"}`, i, i, forkNormalToolName)
		output += "," + item
		frames += forkVendorSSE(fmt.Sprintf(`{"type":"response.output_item.added","sequence_number":%d,"output_index":%d,"item":{"id":"fc_%d","type":"function_call","status":"in_progress","call_id":"call_%d","name":%q,"arguments":""}}`, 7+i*5, i+1, i, i, forkNormalToolName))
		frames += forkVendorSSE(fmt.Sprintf(`{"type":"response.function_call_arguments.delta","sequence_number":%d,"item_id":"fc_%d","output_index":%d,"delta":"{\"value\":"}`, 8+i*5, i, i+1))
		frames += forkVendorSSE(fmt.Sprintf(`{"type":"response.function_call_arguments.delta","sequence_number":%d,"item_id":"fc_%d","output_index":%d,"delta":"1}"}`, 9+i*5, i, i+1))
		frames += forkVendorSSE(fmt.Sprintf(`{"type":"response.function_call_arguments.done","sequence_number":%d,"item_id":"fc_%d","output_index":%d,"arguments":"{\"value\":1}"}`, 10+i*5, i, i+1))
		frames += forkVendorSSE(fmt.Sprintf(`{"type":"response.output_item.done","sequence_number":%d,"output_index":%d,"item":%s}`, 11+i*5, i+1, item))
	}
	response := fmt.Sprintf(`{"id":"resp_fixture","object":"response","created_at":1700000000,"model":"gpt-4o","status":"completed","error":null,"incomplete_details":null,"output":[%s],"usage":%s}`, output, usage)
	if !stream {
		return response
	}
	return frames + forkVendorSSE(fmt.Sprintf(`{"type":"response.completed","sequence_number":%d,"response":%s}`, 7+tc.tools*5, response))
}

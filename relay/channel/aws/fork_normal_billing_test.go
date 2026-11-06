package aws

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

// Real Bedrock SDK InvokeModel / InvokeModelWithResponseStream handlers consume
// local official-schema fixtures. Prices below are a test contract, not claimed
// vendor prices; expected quotas are hand calculated independently of the engine.
func TestForkAWSNormalBilling(t *testing.T) {
	text := append(forkAWSText(0), forkAWSClose(0))
	thinking := []string{`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me check."}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"c3ludGhldGljLXNpZ25hdHVyZQ=="}}`, forkAWSClose(0)}
	thinking = append(thinking, forkAWSText(1)...)
	thinking = append(thinking, forkAWSClose(1))
	for _, tc := range []struct {
		name, content, stop              string
		blocks                           []string
		input, output, cache, five, hour int
		tokenQuota, baseQuota            int
	}{
		{"text_cache", `[{"type":"text","text":"Hello, 世界 🌍"}]`, "end_turn", text, 1000, 500, 200, 200, 100, 6000, 6090},
		{"thinking_cache", `[{"type":"thinking","thinking":"Let me check.","signature":"c3ludGhldGljLXNpZ25hdHVyZQ=="},{"type":"text","text":"Hello, 世界 🌍"}]`, "end_turn", thinking, 1000, 500, 200, 200, 100, 6000, 6090},
		{"tool_cache", `[{"type":"tool_use","id":"toolu_bedrock_0","name":"get_weather","input":{"location":"東京"}}]`, "tool_use", forkAWSTool(0), 1000, 500, 200, 200, 100, 6000, 6090},
		{"zero_uncached_input", `[{"type":"text","text":"Hello, 世界 🌍"}]`, "end_turn", text, 0, 12, 200, 200, 100, 840, 930},
		{"zero_output", `[]`, "end_turn", nil, 1000, 0, 0, 0, 0, 1500, 1500},
		{"explicit_all_zero", `[]`, "end_turn", nil, 0, 0, 0, 0, 0, 0, 0},
		{"explicit_zero_input_with_output", `[{"type":"text","text":"Hello, 世界 🌍"}]`, "end_turn", text, 0, 12, 0, 0, 0, 90, 90},
		{"missing_input_with_output", `[{"type":"text","text":"Hello, 世界 🌍"}]`, "end_turn", text, 0, 12, 0, 0, 0, 90, 90},
	} {
		for _, stream := range []bool{false, true} {
			for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
				t.Run(fmt.Sprintf("%s/stream_%t/%s", tc.name, stream, format), func(t *testing.T) {
					usageJSON := fmt.Sprintf(`{"input_tokens":%d,"output_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":%d}}`, tc.input, tc.output, tc.cache, tc.five+tc.hour, tc.five, tc.hour)
					if tc.name == "missing_input_with_output" {
						usageJSON = strings.Replace(usageJSON, `"input_tokens":0,`, "", 1)
					}
					wantInput, wantTokenQuota, wantBaseQuota := tc.input, tc.tokenQuota, tc.baseQuota
					// AWS native and converted streams share upstream's finalizer.
					// Commit 1b63c28dcc (#7816) intentionally estimates zero total
					// input, even when explicitly reported. The adapter retains
					// that policy; nonstream confirmed zeros remain unchanged.
					if stream && tc.input == 0 && tc.cache == 0 {
						wantInput, wantTokenQuota, wantBaseQuota = 50, tc.tokenQuota+75, tc.baseQuota+75
					}
					var info *relaycommon.RelayInfo
					var usage *dto.Usage
					var body string
					var apiErr *types.NewAPIError
					if stream {
						initialUsage := strings.Replace(usageJSON, fmt.Sprintf(`"output_tokens":%d`, tc.output), fmt.Sprintf(`"output_tokens":%d`, min(tc.output, 1)), 1)
						events := append([]string{forkAWSStart(initialUsage)}, tc.blocks...)
						events = append(events, fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q,"stop_sequence":null},"usage":{"output_tokens":%d}}`, tc.stop, tc.output), `{"type":"message_stop"}`)
						info, usage, body, apiErr = forkAWSRun(t, forkAWSEncode(t, events), 7, format)
						require.False(t, info.StreamResult.Failed)
						if format == types.RelayFormatClaude {
							require.Equal(t, 1, strings.Count(body, "event: message_stop"))
						} else {
							require.Equal(t, 1, strings.Count(body, "data: [DONE]"))
						}
					} else {
						payload := fmt.Sprintf(`{"id":"msg_bedrock_normal","type":"message","role":"assistant","model":"claude-sonnet-4-5-20250929","content":%s,"stop_reason":%q,"stop_sequence":null,"usage":%s}`, tc.content, tc.stop, usageJSON)
						rec := httptest.NewRecorder()
						c := newAwsTestContext(rec, context.Background())
						info = newAwsTestRelayInfo()
						info.IsStream, info.RelayFormat, info.UpstreamModelName, info.OriginModelName = false, format, forkAWSModel, forkAWSModel
						info.SetEstimatePromptTokens(50)
						client := newAwsTestClient(awsHTTPClientFunc(func(r *http.Request) (*http.Response, error) {
							return &http.Response{StatusCode: 200, Request: r, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(payload))}, nil
						}))
						input := newAwsInvokeModelInput()
						*input.ModelId = forkAWSModel
						input.Body = []byte(`{"anthropic_version":"bedrock-2023-05-31","max_tokens":1024,"messages":[{"role":"user","content":"Hello"}]}`)
						apiErr, usage = awsHandler(c, info, &Adaptor{AwsClient: client, AwsReq: input})
						require.Equal(t, http.StatusOK, rec.Code)
					}
					require.Nil(t, apiErr)
					require.NotNil(t, usage)
					if canonical, ok := usage.BillingUsage.CanonicalUsage(); ok {
						usage = canonical
					}
					require.Equal(t, "anthropic", usage.UsageSemantic)
					require.Equal(t, wantInput, usage.PromptTokens)
					require.Equal(t, tc.output, usage.CompletionTokens)
					require.Equal(t, tc.cache, usage.PromptTokensDetails.CachedTokens)
					require.Equal(t, tc.five, usage.ClaudeCacheCreation5mTokens)
					require.Equal(t, tc.hour, usage.ClaudeCacheCreation1hTokens)
					for _, price := range []struct {
						name, expr string
						quota      int
						unit       billingexpr.BillingUnit
					}{
						{"separate_cache", `tier("tokens", p*2 + c*10 + cr*0.5 + cc*2.5 + cc1h*4)`, wantTokenQuota, billingexpr.BillingUnitToken},
						{"cache_in_base", `tier("tokens", p*2 + c*10)`, wantBaseQuota, billingexpr.BillingUnitToken},
						{"fixed_request", `tier("request", fixed(0.01))`, 7500, billingexpr.BillingUnitRequest},
					} {
						_, err := billingexpr.CompileFromCache(price.expr)
						require.NoError(t, err)
						hash := billingexpr.ExprHashString(price.expr)
						params := service.BuildTieredTokenParams(usage, true, billingexpr.UsedVarsByHash(price.expr, hash))
						require.Equal(t, float64(wantInput+tc.cache+tc.five+tc.hour), params.Len)
						if price.name == "separate_cache" {
							require.Equal(t, float64(wantInput), params.P)
							require.Equal(t, float64(tc.output), params.C)
							require.Equal(t, float64(tc.cache), params.CR)
							require.Equal(t, float64(tc.five), params.CC)
							require.Equal(t, float64(tc.hour), params.CC1h)
						}
						info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprVersion: 1, ExprString: price.expr, ExprHash: hash, QuotaPerUnit: 500000, GroupRatio: 1.5}
						ok, quota, result := service.TryTieredSettle(info, params)
						require.True(t, ok)
						require.NotNil(t, result, price.name)
						require.Equal(t, price.quota, quota, price.name)
						require.Equal(t, price.unit, result.BillingUnit)
						require.Nil(t, result.Clamp)
					}
				})
			}
		}
	}
}

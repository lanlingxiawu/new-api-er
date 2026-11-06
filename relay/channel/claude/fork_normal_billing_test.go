package claude

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

// Prices are an explicit test contract, not a vendor price assertion. The token
// hand calculations use quota = USD * 500000 * 1.5; fixed(0.01) is 7500 quota.
// Synthetic successful responses follow the official Anthropic schemas cited
// by fork_vendor_fault_matrix_test.go and retain the real fixed model ID.
func TestForkClaudeNormalBilling(t *testing.T) {
	for _, tc := range []struct {
		name, blocks, content, stop      string
		input, output, cache, five, hour int
		tokenQuota, baseQuota            int
	}{
		{"text_cache", forkClaudeText(0) + forkClaudeClose(0), `[{"type":"text","text":"Hello, 世界 🌍"}]`, "end_turn", 1000, 500, 200, 200, 100, 6000, 6090},
		{"thinking_cache", forkClaudeThinking(0) + forkClaudeText(1) + forkClaudeClose(1), `[{"type":"thinking","thinking":"Let me check.","signature":"c3ludGhldGljLXNpZ25hdHVyZQ=="},{"type":"text","text":"Hello, 世界 🌍"}]`, "end_turn", 1000, 500, 200, 200, 100, 6000, 6090},
		{"tool_cache", forkClaudeTool(0), `[{"type":"tool_use","id":"toolu_matrix_0","name":"get_weather","input":{"location":"東京"}}]`, "tool_use", 1000, 500, 200, 200, 100, 6000, 6090},
		{"zero_uncached_input", forkClaudeText(0) + forkClaudeClose(0), `[{"type":"text","text":"Hello, 世界 🌍"}]`, "end_turn", 0, 12, 200, 200, 100, 840, 930},
		{"zero_output", "", `[]`, "end_turn", 1000, 0, 0, 0, 0, 1500, 1500},
		{"explicit_all_zero", "", `[]`, "end_turn", 0, 0, 0, 0, 0, 0, 0},
		{"explicit_zero_input_with_output", forkClaudeText(0) + forkClaudeClose(0), `[{"type":"text","text":"Hello, 世界 🌍"}]`, "end_turn", 0, 12, 0, 0, 0, 90, 90},
		{"missing_input_with_output", forkClaudeText(0) + forkClaudeClose(0), `[{"type":"text","text":"Hello, 世界 🌍"}]`, "end_turn", 0, 12, 0, 0, 0, 90, 90},
	} {
		for _, stream := range []bool{false, true} {
			for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {

				t.Run(fmt.Sprintf("%s/stream_%t/%s", tc.name, stream, format), func(t *testing.T) {
					usageJSON := fmt.Sprintf(`{"input_tokens":%d,"output_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":%d}}`, tc.input, tc.output, tc.cache, tc.five+tc.hour, tc.five, tc.hour)
					if tc.name == "missing_input_with_output" {
						usageJSON = strings.Replace(usageJSON, `"input_tokens":0,`, "", 1)
					}
					wantInput, wantTokenQuota, wantBaseQuota := tc.input, tc.tokenQuota, tc.baseQuota
					// Upstream commit 1b63c28dcc (#7816) deliberately treats zero
					// total input (including explicit zero) as uncounted. Converted
					// streams retain its input estimate with either compatibility
					// setting; strict native Claude already retains confirmed zero.
					// Do not turn this comparison into a billing-policy change.
					if stream && format == types.RelayFormatOpenAI && tc.input == 0 && tc.cache == 0 {
						wantInput = 50
						wantTokenQuota += 75
						wantBaseQuota += 75
					}
					body := fmt.Sprintf(`{"id":"msg_normal_billing","type":"message","role":"assistant","model":%q,"content":%s,"stop_reason":%q,"stop_sequence":null,"usage":%s}`, forkClaudeModel, tc.content, tc.stop, usageJSON)
					if stream {
						initialUsage := strings.Replace(usageJSON, fmt.Sprintf(`"output_tokens":%d`, tc.output), fmt.Sprintf(`"output_tokens":%d`, min(tc.output, 1)), 1)
						body = forkClaudeStart(initialUsage) + tc.blocks + forkClaudeFinish(tc.stop, tc.output)
					}
					c, rec, resp, info := strictTestContext(io.NopCloser(strings.NewReader(body)))
					info.IsStream, info.RelayFormat, info.UpstreamModelName, info.OriginModelName = stream, format, forkClaudeModel, forkClaudeModel
					resp.Header.Set("Content-Type", "application/json")
					var usage *dto.Usage
					var apiErr *types.NewAPIError
					if stream {
						resp.Header.Set("Content-Type", "text/event-stream")
						service.BeginStreamAttempt(c, info)
						info.StreamSession.ObserveTransport(resp, nil)
						usage, apiErr = ClaudeStreamHandler(c, resp, info)
						usage = service.FinalizeStreamUsage(c, info, usage)
						require.False(t, info.StreamResult.Failed)
						if format == types.RelayFormatClaude {
							require.Equal(t, 1, strings.Count(rec.Body.String(), "event: message_stop"))
						} else {
							require.Equal(t, 1, strings.Count(rec.Body.String(), "data: [DONE]"))
						}
					} else {
						usage, apiErr = ClaudeHandler(c, resp, info)
					}
					require.Nil(t, apiErr)
					require.Equal(t, http.StatusOK, rec.Code)
					require.NotNil(t, usage)
					// This is the same public snapshot selection used by the service's
					// internal effectiveBillingUsage before its pure pricing entry point.
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

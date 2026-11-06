package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// This audit evaluates real handler-selected usage with pure billing
// expressions. No wallet, database, real provider or real prompt is accessed.
func TestForkUpstreamCacheBillingAudit(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	for _, tc := range []struct {
		name, usage, expr         string
		fullQuota, truncatedQuota int
	}{
		{"creation alias",
			`{"prompt_tokens":1510,"completion_tokens":20,"total_tokens":1530,"prompt_tokens_details":{"cached_tokens":1200,"cache_creation_input_tokens":300}}`,
			`tier("standard", p * 2 + c * 2 + cr * 0.2 + cc * 2.5)`, 525, 450},
		{"12h writes",
			`{"prompt_tokens":12000,"completion_tokens":20,"total_tokens":12020,"prompt_tokens_details":{"cached_tokens":4000,"cache_write_tokens":7000,"cache_write_tokens_details":{"ttl_30m_tokens":3000,"ttl_12h_tokens":4000}}}`,
			`tier("standard", p * 10 + c * 50 + cr * 1 + cc * 12.5 + cc12h * 25)`, 76250, 51250},
		{"cached image breakdown",
			`{"prompt_tokens":1000,"completion_tokens":100,"total_tokens":1100,"prompt_tokens_details":{"image_tokens":600,"cached_tokens":300,"cached_tokens_details":{"text_tokens":100,"image_tokens":200}}}`,
			`tier("standard", p * 5 + cr * 1.25 + img * 8 + img_cr * 2 + c * 30)`, 4113, 4338},
	} {
		for _, truncated := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/complete", true: "/EOF"}[truncated], func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
				info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, FinalRequestRelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "audit-model"}}
				info.PriceData.GroupRatioInfo.GroupRatio = 1
				info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: tc.expr, ExprHash: billingexpr.ExprHashString(tc.expr), GroupRatio: 1, QuotaPerUnit: 500000}
				body := "data: {\"id\":\"audit\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n"
				if !truncated {
					body += "data: {\"id\":\"audit\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"
				}
				body += "data: {\"id\":\"audit\",\"choices\":[],\"usage\":" + tc.usage + "}\n\n"
				if !truncated {
					body += "data: [DONE]\n\n"
				}
				resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
				service.BeginStreamAttempt(c, info)
				info.StreamSession.ObserveTransport(resp, nil)
				info.StreamDiagnostic.Observe(resp)
				info.StreamSession.ObserveHTTP(resp)
				usage, apiErr := OaiStreamHandler(c, info, resp)
				require.Nil(t, apiErr)
				require.NotNil(t, usage)
				beforeCreate := usage.PromptTokensDetails.CacheCreationTokensTotal()
				before12h := usage.PromptTokensDetails.CacheWrite12hTokens()
				usage = service.FinalizeStreamUsage(c, info, usage)
				require.Equal(t, truncated, info.StreamResult.Failed)
				if truncated {
					require.Equal(t, "upstream_incomplete", string(info.StreamStatus.EndReason))
					require.Equal(t, "upstream", info.StreamResult.UsageSource)
				}
				_, compileErr := billingexpr.CompileFromCache(tc.expr)
				require.NoError(t, compileErr)
				params := service.BuildTieredTokenParams(usage, false, billingexpr.UsedVars(tc.expr))
				ok, quota, result := service.TryTieredSettle(info, params)
				require.True(t, ok)
				require.NotNil(t, result)
				want := tc.fullQuota
				require.Equal(t, want, quota)
				require.Contains(t, rec.Body.String(), "hello")
				t.Logf("truncated=%t before_creation=%d after_creation=%d before_12h=%d after_12h=%d img_cr=%v quota=%d reference_quota=%d end=%s", truncated, beforeCreate, usage.PromptTokensDetails.CacheCreationTokensTotal(), before12h, usage.PromptTokensDetails.CacheWrite12hTokens(), params.ImgCR, quota, tc.fullQuota, info.StreamStatus.EndReason)
			})
		}
	}
}

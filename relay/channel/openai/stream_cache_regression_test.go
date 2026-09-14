package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestOpenAIStreamCacheEvidenceFixtures 使用 t 的内存 HTTP 原始响应及真实转换器验证缓存字段在 EOF 异常结算后保留。
func TestOpenAIStreamCacheEvidenceFixtures(t *testing.T) {
	for _, tc := range []struct {
		name     string // 原有兼容渠道及字段组合。
		channel  int    // 本次选择的渠道类型。
		evidence string // 原始累计用量及供应商缓存字段。
	}{
		{"deepseek", constant.ChannelTypeDeepSeek, `{"usage":{"prompt_tokens":1000,"completion_tokens":3,"total_tokens":1003,"prompt_cache_hit_tokens":900}}`},
		{"moonshot", constant.ChannelTypeMoonshot, `{"usage":{"prompt_tokens":1000,"completion_tokens":3,"total_tokens":1003},"choices":[{"index":0,"usage":{"cached_tokens":900},"delta":{}}]}`},
		{"llama", constant.ChannelTypeOpenAI, `{"usage":{"prompt_tokens":1000,"completion_tokens":3,"total_tokens":1003},"timings":{"cache_n":900}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			common.SetContextKey(c, constant.ContextKeyChannelType, tc.channel)
			info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, FinalRequestRelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{ChannelType: tc.channel, UpstreamModelName: "fixture-model"}}
			service.BeginStreamAttempt(c, info)
			body := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\ndata: " + tc.evidence + "\n\n"
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamDiagnostic.Observe(resp)
			info.StreamSession.ObserveHTTP(resp)
			usage, err := OaiStreamHandler(c, info, resp)
			require.Nil(t, err)
			selected := service.FinalizeStreamUsage(c, info, usage)
			require.Equal(t, "upstream", info.StreamResult.UsageSource)
			require.True(t, info.StreamResult.DiagnosticAvailable)
			require.Equal(t, 900, selected.PromptTokensDetails.CachedTokens)
			require.Equal(t, 1003, selected.TotalTokens)
			require.Contains(t, rec.Body.String(), "hello")
			require.Equal(t, 1, strings.Count(rec.Body.String(), "event: error"))
			require.NotContains(t, rec.Body.String(), "[DONE]")
		})
	}
}

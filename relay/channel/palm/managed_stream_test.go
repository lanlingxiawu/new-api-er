package palm

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestUnifiedPaLMJSONToStream 验证完整 JSON 转流路径；仅成功解析时输出成功尾帧，格式截断和原生错误进入统一终止。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestUnifiedPaLMJSONToStream(t *testing.T) {
	for _, tc := range []struct {
		body     string
		complete bool
	}{
		{`{"candidates":[{"author":"assistant","content":"hello"}]}`, true},
		{`{}`, false},
		{`{"candidates":[]}`, false},
		{`{"candidates":[{}]}`, false},
		{`{"candidates":[{"content":"   "}]}`, false},
		{`{"usage":{"output_tokens":20}}`, false},
		{`{"candidates":[`, false},
		{`{"error":{"code":500,"message":"provider-error","status":"INTERNAL"}}`, false},
	} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "palm-2"}}
		service.BeginStreamAttempt(c, info)
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(tc.body))}
		info.StreamSession.ObserveTransport(resp, nil)
		info.StreamDiagnostic.Observe(resp)
		info.StreamSession.ObserveHTTP(resp)
		err, text := palmStreamHandler(c, resp)
		service.FinalizeStreamUsage(c, info, &dto.Usage{PromptTokens: 4, CompletionTokens: 1, TotalTokens: 5})
		require.Equal(t, !tc.complete, info.StreamResult.Failed)
		if tc.complete {
			require.Nil(t, err)
			require.Equal(t, "hello", text)
			require.True(t, info.StreamResult.EffectiveContent)
			require.Contains(t, rec.Body.String(), "[DONE]")
		} else {
			require.NotNil(t, err)
			require.Equal(t, "none", info.StreamResult.UsageSource)
			require.Contains(t, rec.Body.String(), "event: error")
			require.NotContains(t, rec.Body.String(), "[DONE]")
		}
	}
}

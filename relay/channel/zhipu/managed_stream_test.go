package zhipu

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

// TestUnifiedZhipuStream 验证旧智谱纯文本 add 与 finish/meta，不把正文当作坏 JSON。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestUnifiedZhipuStream(t *testing.T) {
	for _, complete := range []bool{true, false} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeZhipu)
		info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "chatglm"}}
		service.BeginStreamAttempt(c, info)
		body := "event: add\ndata: hello\n\n"
		if complete {
			body += "event: finish\nmeta: {\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}}\n\n"
		}
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
		info.StreamSession.ObserveTransport(resp, nil)
		u, err := managedZhipuStream(c, info, resp)
		require.Nil(t, err)
		service.FinalizeStreamUsage(c, info, u)
		require.Equal(t, !complete, info.StreamResult.Failed)
		require.True(t, info.StreamResult.EffectiveContent)
		if complete {
			require.Equal(t, 2, info.StreamFinalUsage.CompletionTokens)
			require.Contains(t, rec.Body.String(), "[DONE]")
		} else {
			require.NotContains(t, rec.Body.String(), "[DONE]")
		}
	}
}

package xai

import (
	"errors"
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

// totalUsageFailedWriter 模拟客户端在首个有效数据写入时断开，不先向客户端交付内容。
type totalUsageFailedWriter struct{ gin.ResponseWriter }

// Write 返回连接错误；p 为待写入数据，本夹具接受字节数恒为零。
func (w *totalUsageFailedWriter) Write(p []byte) (int, error) {
	return 0, errors.New("fixture client closed")
}

// TestManagedProviderTotalUsage 验证原适配器转换后，异常结算仍保留总量推导的输出；t 为测试上下文。
func TestManagedProviderTotalUsage(t *testing.T) {
	for _, clientGone := range []bool{false, true} {
		t.Run(map[bool]string{false: "upstream EOF", true: "client gone"}[clientGone], func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeXai)
			if clientGone {
				c.Writer = &totalUsageFailedWriter{c.Writer}
			}
			info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "fixture"}}
			service.BeginStreamAttempt(c, info)
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + `{"id":"fixture","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}],"usage":{"prompt_tokens":10,"completion_tokens":1,"total_tokens":14}}` + "\n\n"))}
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamDiagnostic.Observe(resp)
			info.StreamSession.ObserveHTTP(resp)
			usage, apiErr := xAIStreamHandler(c, info, resp)
			require.Nil(t, apiErr)
			require.Equal(t, 4, usage.CompletionTokens)
			selected := service.FinalizeStreamUsage(c, info, usage)
			require.Equal(t, 10, selected.PromptTokens)
			require.Equal(t, 4, selected.CompletionTokens)
			require.Equal(t, 14, selected.TotalTokens)
			require.Equal(t, "upstream", info.StreamResult.UsageSource)
			require.Equal(t, !clientGone, info.StreamResult.EffectiveContent)
			require.Equal(t, !clientGone, info.StreamResult.DiagnosticAvailable)
		})
	}
}

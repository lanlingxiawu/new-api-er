package xunfei

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// TestUnifiedXunfeiStream 覆盖讯飞 WS 正常结束、缺少结束及坏 JSON，使用本地上游夹具。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestUnifiedXunfeiStream(t *testing.T) {
	for _, ending := range []string{`{"header":{"code":0},"payload":{"choices":{"status":2,"text":[{"content":"ok"}]},"usage":{"text":{"prompt_tokens":9,"completion_tokens":2}}}}`, "{bad", ""} {
		t.Run(ending, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, http.Header{"X-Upstream": []string{"fixture"}})
				if err != nil {
					return
				}
				defer conn.Close()
				_, _, _ = conn.ReadMessage()
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"header":{"code":0},"payload":{"choices":{"status":1,"text":[{"content":"hi"}]}}}`))
				if ending != "" {
					_ = conn.WriteMessage(websocket.TextMessage, []byte(ending))
				}
			}))
			defer srv.Close()
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "SparkDesk"}}
			service.BeginStreamAttempt(c, info)
			u, _ := managedXunfeiStream(c, dto.GeneralOpenAIRequest{}, "test", "ws"+strings.TrimPrefix(srv.URL, "http"), "app", info)
			service.FinalizeStreamUsage(c, info, u)
			require.True(t, info.StreamResult.EffectiveContent)
			if strings.Contains(ending, `"status":2`) {
				require.False(t, info.StreamResult.Failed)
				require.Contains(t, rec.Body.String(), "[DONE]")
				require.Equal(t, 2, info.StreamFinalUsage.CompletionTokens)
			} else {
				require.True(t, info.StreamResult.Failed)
				require.Contains(t, rec.Body.String(), "event: error")
				require.NotContains(t, rec.Body.String(), "[DONE]")
			}
			require.Equal(t, "fixture", info.StreamResult.Diagnostic.ResponseHeaders.Get("X-Upstream"))
		})
	}
}

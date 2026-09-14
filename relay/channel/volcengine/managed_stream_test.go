package volcengine

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

// TestUnifiedTTSStream 验证裸媒体保持原字节，截断时不插入 SSE，只有实际终帧才算成功。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestUnifiedTTSStream(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(map[bool]string{true: "complete", false: "truncated"}[complete], func(t *testing.T) {
			msg, err := NewMessage(MsgTypeAudioOnlyServer, MsgTypeFlagPositiveSeq)
			require.NoError(t, err)
			msg.Sequence = 1
			if complete {
				msg.MsgTypeFlag = MsgTypeFlagNegativeSeq
				msg.Sequence = -1
			}
			msg.Payload = []byte("audio-data")
			raw, err := msg.Marshal()
			require.NoError(t, err)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				_, _, _ = conn.ReadMessage()
				_ = conn.WriteMessage(websocket.BinaryMessage, raw)
			}))
			defer srv.Close()
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/audio/speech", nil)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "tts", ApiKey: "app|token"}}
			info.SetEstimatePromptTokens(10)
			service.BeginStreamAttempt(c, info)
			u, _ := handleTTSWebSocketResponse(c, "ws"+strings.TrimPrefix(srv.URL, "http"), VolcengineTTSRequest{}, info, "mp3")
			usage, _ := u.(*dto.Usage)
			service.FinalizeStreamUsage(c, info, usage)
			require.Equal(t, !complete, info.StreamResult.Failed)
			require.Equal(t, !complete, info.StreamResult.DiagnosticAvailable)
			require.True(t, info.StreamResult.EffectiveContent)
			require.Equal(t, "audio-data", rec.Body.String())
			require.Equal(t, raw, append(info.StreamResult.Diagnostic.BodyHead, info.StreamResult.Diagnostic.BodyTail...))
		})
	}
}

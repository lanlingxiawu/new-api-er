package channel

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// streamGateWSAdaptor 复用渠道接口，只覆盖本测试实际使用的 URL/头设置。
type streamGateWSAdaptor struct {
	Adaptor
	url string // 本地夹具地址，不使用线上渠道。
}

// GetRequestURL 的 info 沿用接口参数；返回本地地址以测试真正握手而非适配器占位响应。
func (a streamGateWSAdaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return a.url, nil
}

// SetupRequestHeader 的 c/header/info 均只满足接口，不添加测试请求凭据。
func (a streamGateWSAdaptor) SetupRequestHeader(c *gin.Context, header *http.Header, info *relaycommon.RelayInfo) error {
	return nil
}

// TestStreamResponseGateWSActual 覆盖真实拨号 101、握手非 101 与无响应；失败保持原错误，成功开启后续流观察。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestStreamResponseGateWSActual(t *testing.T) {
	for _, status := range []int{0, 101, 200, 401, 503} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if status == 101 {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err == nil {
					_ = conn.Close()
				}
				return
			}
			if status == 0 {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
				return
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte("original handshake error"))
		}))
		t.Cleanup(srv.Close)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest("GET", "/v1/realtime", nil)
		info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIRealtime, ChannelMeta: &relaycommon.ChannelMeta{}}
		service.BeginStreamAttempt(c, info)
		conn, err := DoWssRequest(streamGateWSAdaptor{url: "ws" + strings.TrimPrefix(srv.URL, "http")}, c, info, nil)
		if status == 101 {
			require.NoError(t, err)
			require.True(t, info.StreamSession.Active())
			require.Equal(t, 101, info.StreamDiagnostic.Snapshot().StatusCode)
			require.NoError(t, conn.Close())
		} else {
			require.Error(t, err)
			require.False(t, service.FinalizeStreamFailure(c, info, err))
			require.False(t, info.StreamSession.Active())
			require.Nil(t, info.StreamResult)
			require.Empty(t, rec.Body.String())
			other := map[string]any{}
			service.AppendStreamErrorDiagnostic(c, other, err)
			require.Empty(t, other)
		}
	}
}

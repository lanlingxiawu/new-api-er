package helper

import (
	"errors"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// originFailureWriter 模拟客户端首帧写入失败，不接受任何正文。
type originFailureWriter struct{ gin.ResponseWriter }

// Write 拒绝写入 p 并返回测试连接错误，不触及真实网络。
func (w *originFailureWriter) Write(p []byte) (int, error) {
	return 0, errors.New("fixture client write failed")
}

// TestStreamConversionErrorOrigin 覆盖转换登记、客户端首因、已有上游首因、nil 与旧路径；t 为上下文。
func TestStreamConversionErrorOrigin(t *testing.T) {
	for _, mode := range []string{"marshal", "marshal after complete", "client", "upstream", "nil", "legacy", "disabled", "awaiting response"} {
		t.Run(mode, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/", nil)
			s := relaycommon.NewStreamSession(types.RelayFormatOpenAI)
			result := newStreamResult(relaycommon.NewStreamStatus())
			if mode != "legacy" {
				result.session = s
			}
			if mode == "disabled" {
				s.Disable()
			} else if mode == "awaiting response" {
				s.ResponseGate = &relaycommon.StreamResponseGate{}
			} else if mode == "marshal after complete" {
				s.Complete()
			}
			if mode == "client" {
				c.Writer = &originFailureWriter{c.Writer}
			}
			c.Writer = relaycommon.NewStreamWriter(c.Writer, s, nil)
			SetEventStreamHeaders(c)
			var err error
			if mode == "client" {
				err = ObjectData(c, map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": "hello"}}}})
			} else if mode != "nil" {
				// 函数值不是 JSON 数据，确保错误来自本地序列化而非上游解析。
				err = ObjectData(c, func() {})
			}
			if mode == "upstream" {
				s.Fail("upstream_read_error", errors.New("fixture upstream failed first"))
			}
			result.ConversionError(err)
			snapshot := s.Snapshot()
			require.Equal(t, mode != "nil" && result.session.Active(), result.IsStopped())
			switch mode {
			case "marshal", "marshal after complete":
				require.Equal(t, relaycommon.StreamEndReason("response_conversion_error"), snapshot.Reason)
				require.False(t, snapshot.UpstreamFailure)
				require.False(t, snapshot.Complete)
			case "client":
				require.Error(t, snapshot.ClientErr)
				require.Empty(t, snapshot.Reason)
				require.False(t, snapshot.Effective)
			case "upstream":
				require.Equal(t, relaycommon.StreamEndReason("upstream_read_error"), snapshot.Reason)
			default:
				require.Empty(t, snapshot.Reason)
			}
		})
	}
}

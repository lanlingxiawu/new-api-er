package common

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// framingChunkReader 按 size 限制一次 Read，制造 CRLF、事件名和 JSON 跨网络块；不修改原数据。
type framingChunkReader struct {
	io.Reader     // 内存原始响应。
	size      int // 每次最多返回的字节数。
}

// TestStreamWriterMixedFragments 验证下游逐字节 Write 保留换行原文，CRLF 后半段不丢失；t 为测试上下文。
func TestStreamWriterMixedFragments(t *testing.T) {
	for _, ending := range []string{"\n\r\n", "\r\n\r\n", "\r\r", "\n\r"} {
		raw := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}" + ending
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		s := NewStreamSession(types.RelayFormatOpenAI)
		w := NewStreamWriter(c.Writer, s, nil)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, b := range []byte(raw) {
			_, err := w.Write([]byte{b})
			require.NoError(t, err)
		}
		w.Finish()
		require.Equal(t, raw, rec.Body.String())
		require.True(t, s.Snapshot().Effective)
		require.Empty(t, s.Snapshot().Reason)
	}
}

// Read 将原始字节写入 p，最多 size 字节；错误沿用内存源。
func (r framingChunkReader) Read(p []byte) (int, error) {
	return r.Reader.Read(p[:min(len(p), r.size)])
}

// TestStreamMixedLineEndings 覆盖 LF/CRLF/CR 混用、单字节读取及实际原始错误；t 为测试上下文。
func TestStreamMixedLineEndings(t *testing.T) {
	for i, line := range []string{"\n", "\r\n", "\r"} {
		for j, blank := range []string{"\n", "\r\n", "\r"} {
			// CR 紧接 LF 属于一个 CRLF，不是两个行结束符。
			if line == "\r" && blank == "\n" {
				continue
			}
			t.Run(fmt.Sprintf("%d_%d", i, j), func(t *testing.T) {
				raw := "event: message" + line + "data: {\"choices\":" + line + "data: [{\"index\":0,\"finish_reason\":\"stop\"}]}" + line + blank
				require.Equal(t, len(raw), StreamFrameEnd([]byte(raw)))
				event, payload := StreamFramePayload([]byte(raw))
				require.Equal(t, "message", event)
				require.Equal(t, "{\"choices\":\n[{\"index\":0,\"finish_reason\":\"stop\"}]}", string(payload))
				for _, size := range []int{1, 2, 3, 4096} {
					s := NewStreamSession(types.RelayFormatOpenAI)
					body := raw + "data: [DONE]" + line + blank
					resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(framingChunkReader{strings.NewReader(body), size})}
					s.ObserveHTTP(resp)
					got, err := io.ReadAll(resp.Body)
					require.NoError(t, err, "chunk size %d", size)
					require.Equal(t, body, string(got))
					require.True(t, s.ProtocolComplete())
					require.Empty(t, s.Snapshot().Reason)
				}
			})
		}
	}
	for _, raw := range []string{"data: {}", "data: {}\n", "data: {}\r\n", "data: {}\r"} {
		require.Zero(t, StreamFrameEnd([]byte(raw)), "one line is not a complete event: %q", raw)
	}
	for _, raw := range []string{"event: error\ndata: {\"error\":{\"message\":\"original\"}}\n\r\n", "event: error\rdata: {\"error\":{\"message\":\"original\"}}\r\r"} {
		s := NewStreamSession(types.RelayFormatOpenAI)
		_ = s.ObserveFrame([]byte(raw))
		require.Equal(t, StreamEndReason("upstream_error"), s.Snapshot().Reason)
		require.Equal(t, raw, string(s.Snapshot().ErrorFrame))
	}
}

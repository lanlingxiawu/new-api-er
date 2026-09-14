package common

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestDownstreamCaptureBounds 用边界和二进制数据验证只保留实际字节；t 为测试上下文。
func TestDownstreamCaptureBounds(t *testing.T) {
	for _, size := range []int{0, 1, 1023, 1024, 1025, 2047, 2048, 2049, 8192} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			capture := NewStreamResponseCapture(1)
			data := make([]byte, size)
			for i := range data {
				data[i] = byte(i % 251)
			}
			for start := 0; start < len(data); start += 17 {
				capture.WriteDownstreamPayload(data[start:min(start+17, len(data))])
			}
			body := capture.DownstreamBody()
			require.NotNil(t, body)
			decoded, err := base64.StdEncoding.DecodeString(*body)
			require.NoError(t, err)
			want := data
			if size > 2048 {
				want = append(bytes.Clone(data[:1024]), data[size-1024:]...)
			}
			require.Equal(t, want, decoded)
			capture.WriteDownstreamPayload([]byte("later"))
			require.NotEqual(t, *body, *capture.DownstreamBody(), "快照不得被后续写入修改")
		})
	}
	var disabled *StreamResponseCapture
	disabled.WriteDownstreamPayload([]byte("ignored"))
	require.Nil(t, disabled.DownstreamBody())
}

// downstreamPartialWriter 注入可控的短写结果；n/err 分别是底层接受字节数和原始错误。
type downstreamPartialWriter struct {
	gin.ResponseWriter
	n   int
	err error
}

// Write 仅写入 p 的前 n 字节，返回夹具预设错误以覆盖带数据错误。
func (w downstreamPartialWriter) Write(p []byte) (int, error) {
	_, _ = w.ResponseWriter.Write(p[:w.n])
	return w.n, w.err
}

// TestDownstreamCapturePartialWrite 验证短写、零写、带数据错误均按 n 采集且原样返回；t 为测试上下文。
func TestDownstreamCapturePartialWrite(t *testing.T) {
	for _, n := range []int{0, 2, 4} {
		for _, cause := range []error{nil, io.ErrClosedPipe} {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			capture := NewStreamResponseCapture(1)
			w := NewDownstreamCaptureWriter(downstreamPartialWriter{c.Writer, n, cause}, capture)
			got, err := w.WriteString("body")
			require.Equal(t, n, got)
			require.Equal(t, cause, err)
			require.Equal(t, base64.StdEncoding.EncodeToString(rec.Body.Bytes()), *capture.DownstreamBody())
		}
	}
}

// TestDownstreamCaptureOutputBoundary 过滤帧和暂存帧不入诊断，刷新故障也不抹掉底层已接受字节；t 为测试上下文。
func TestDownstreamCaptureOutputBoundary(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	capture := NewStreamResponseCapture(1)
	s := NewStreamSession(types.RelayFormatOpenAI)
	w := NewStreamWriter(NewDownstreamCaptureWriter(streamFlushFailure{c.Writer}, capture), s, nil)
	w.Header().Set("Content-Type", "text/event-stream")
	_, err := w.WriteString("data: [DONE]\n\n")
	require.NoError(t, err)
	require.Empty(t, *capture.DownstreamBody())
	frame := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"
	_, err = w.WriteString(frame[:10])
	require.NoError(t, err)
	require.Empty(t, *capture.DownstreamBody())
	_, err = w.WriteString(frame[10:])
	require.NoError(t, err)
	require.Error(t, w.FlushError())
	require.False(t, s.Snapshot().Effective)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte(frame)), *capture.DownstreamBody())
	s.Disable()
	_, err = w.WriteString("strict")
	require.NoError(t, err)
	_, err = w.ResponseWriter.Write([]byte("terminal-error"))
	require.NoError(t, err)
	require.Equal(t, base64.StdEncoding.EncodeToString(rec.Body.Bytes()), *capture.DownstreamBody())
}

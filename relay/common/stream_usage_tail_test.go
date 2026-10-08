package common

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestStreamUsageTailClassification relay-control D-2：choices 为空、带 usage 对象的 OpenAI 用量尾帧
// 只属于成功的流，失败后由写入器拦下；只有 Azure 过滤结论的空 choices 帧、usage 为 null 的帧不算。
func TestStreamUsageTailClassification(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		want       streamSuccessScope
	}{
		{"synthetic tail", `{"id":"","object":"chat.completion.chunk","created":0,"model":"gpt-4o","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":0}}`, streamSuccessUsageTail},
		{"azure prompt filter", `{"choices":[],"prompt_filter_results":[{"prompt_index":0}]}`, streamSuccessNone},
		{"null usage", `{"choices":[],"usage":null}`, streamSuccessNone},
		{"usage with content", `{"choices":[{"delta":{"content":"a"}}],"usage":{"prompt_tokens":1}}`, streamSuccessNone},
		{"error with usage", `{"choices":[],"usage":{"prompt_tokens":1},"error":{"message":"x"}}`, streamSuccessNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, classifyStreamSuccessEvent("", []byte(tc.data)))
		})
	}
}

// TestStreamWriterDropsUsageTailOnlyAfterFailure 失败后的用量尾帧不写出；健康流的用量帧照常写出。
func TestStreamWriterDropsUsageTailOnlyAfterFailure(t *testing.T) {
	tail := "data: {\"id\":\"\",\"created\":0,\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":0}}\n\n"
	for _, failed := range []bool{false, true} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		s := NewStreamSession(types.RelayFormatOpenAI)
		w := NewStreamWriter(c.Writer, s, nil)
		w.Header().Set("Content-Type", "text/event-stream")
		_, err := w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		require.NoError(t, err)
		if failed {
			s.EndRead(io.EOF)
		}
		_, err = w.Write([]byte(tail))
		require.NoError(t, err)
		require.NoError(t, w.FlushError())
		require.Equal(t, !failed, strings.Contains(rec.Body.String(), `"choices":[]`))
	}
}

// TestStreamOutputReportCurrency 最近的上游输出计数是否覆盖其后收到的内容：message_start 的初值不算，
// 之后的内容使旧计数过期，同帧携带内容与计数时计数覆盖该帧。
func TestStreamOutputReportCurrency(t *testing.T) {
	s := NewStreamSession(types.RelayFormatOpenAI)
	s.receivedFactory = func() func(string, int) int { return func(text string, _ int) int { return len(text) } }
	require.NoError(t, s.ObserveEvent("", []byte(`{"type":"message_start","message":{"usage":{"input_tokens":5,"output_tokens":1}}}`)))
	require.False(t, s.Snapshot().OutputReportCurrent)
	require.NoError(t, s.ObserveEvent("", []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)))
	require.NoError(t, s.ObserveEvent("", []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`)))
	require.False(t, s.Snapshot().OutputReportCurrent)
	require.NoError(t, s.ObserveEvent("", []byte(`{"type":"content_block_stop","index":0}`)))
	require.NoError(t, s.ObserveEvent("", []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`)))
	require.True(t, s.Snapshot().OutputReportCurrent)

	o := NewStreamSession(types.RelayFormatOpenAI)
	o.receivedFactory = s.receivedFactory
	require.NoError(t, o.ObserveEvent("", []byte(`{"choices":[{"delta":{"content":"a"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)))
	require.True(t, o.Snapshot().OutputReportCurrent, "a chunk's own usage covers its content")
	require.NoError(t, o.ObserveEvent("", []byte(`{"choices":[{"delta":{"content":"more"}}]}`)))
	require.False(t, o.Snapshot().OutputReportCurrent)
}

package common

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestStreamLegacyFunctionDelivery 用 t 验证旧版函数只有完整结束后才计交付，并按候选累计名称和参数。
func TestStreamLegacyFunctionDelivery(t *testing.T) {
	const start = `{"choices":[{"index":0,"delta":{"function_call":{"name":"lookup","arguments":"{\"x\":"}}}]}`
	const end = `{"choices":[{"index":0,"delta":{"function_call":{"arguments":"1}"}},"finish_reason":"function_call"}]}`
	const stop = `{"choices":[{"index":0,"delta":{},"finish_reason":"function_call"}]}`
	for _, tc := range []struct {
		name   string   // 场景名称。
		frames []string // 已成功写出且刷新后的 JSON 事件。
		text   string   // 应交给估算器的完整工具内容；空表示没有有效工具交付。
	}{
		{"single frame", []string{`{"choices":[{"index":0,"delta":{"function_call":{"name":"lookup","arguments":"{\"x\":1}"}},"finish_reason":"function_call"}]}`}, `lookup{"x":1}`},
		{"argument fragments", []string{start, end}, `lookup{"x":1}`},
		{"name fragments", []string{
			`{"choices":[{"index":0,"delta":{"function_call":{"name":"look","arguments":"{\"x\":"}}}]}`,
			`{"choices":[{"index":0,"delta":{"function_call":{"name":"up","arguments":"1}"}}}]}`, stop,
		}, `lookup{"x":1}`},
		{"empty arguments", []string{`{"choices":[{"index":0,"delta":{"function_call":{"name":"lookup","arguments":""}}}]}`, stop}, "lookup{}"},
		{"missing arguments", []string{`{"choices":[{"index":0,"delta":{"function_call":{"name":"lookup"}}}]}`, stop}, "lookup{}"},
		{"missing name", []string{`{"choices":[{"index":0,"delta":{"function_call":{"arguments":"{}"}}}]}`, stop}, ""},
		{"no finish", []string{`{"choices":[{"index":0,"delta":{"function_call":{"name":"lookup","arguments":"{}"}}}]}`}, ""},
		{"incomplete arguments", []string{start, stop}, ""},
		{"array arguments", []string{`{"choices":[{"index":0,"delta":{"function_call":{"name":"lookup","arguments":"[]"}}}]}`, stop}, ""},
		{"nonobject function", []string{`{"choices":[{"index":0,"delta":{"function_call":"lookup"}}]}`, stop}, ""},
		{"duplicate finish", []string{start, end, stop}, `lookup{"x":1}`},
		{"candidate isolation", []string{start, `{"choices":[{"index":1,"delta":{"function_call":{"arguments":"1}"}},"finish_reason":"function_call"}]}`, stop}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStreamSession(types.RelayFormatOpenAI)
			var delivered strings.Builder
			for _, frame := range tc.frames {
				s.CommitDelivery([]byte(frame))
				delivered.WriteString(s.TakeDeliveredText())
			}
			require.Equal(t, tc.text, delivered.String())
			require.Equal(t, tc.text != "", s.Snapshot().Effective)
		})
	}
}

// TestStreamLegacyFunctionNamespaces 用 t 验证不同候选及同候选的新旧工具互不覆盖，现代名称仍为更新语义。
func TestStreamLegacyFunctionNamespaces(t *testing.T) {
	s := NewStreamSession(types.RelayFormatOpenAI)
	s.CommitDelivery([]byte(`{"choices":[{"index":0,"delta":{"function_call":{"name":"legacy","arguments":"{\"a\":1}"},"tool_calls":[{"index":0,"function":{"name":"old","arguments":"{\"b\":"}}]}},{"index":1,"delta":{"function_call":{"name":"other","arguments":"{}"}}}]}`))
	require.False(t, s.Snapshot().Effective)
	require.Len(t, s.tools, 3)
	s.CommitDelivery([]byte(`{"choices":[{"index":1,"delta":{},"finish_reason":"function_call"}]}`))
	require.Equal(t, "other{}", s.TakeDeliveredText())
	require.Len(t, s.tools, 2)
	s.CommitDelivery([]byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"modern","arguments":"2}"}}]},"finish_reason":"tool_calls"}]}`))
	text := s.TakeDeliveredText()
	require.Contains(t, text, `legacy{"a":1}`)
	require.Contains(t, text, `modern{"b":2}`)
	require.Equal(t, len(`legacy{"a":1}modern{"b":2}`), len(text))
	require.Empty(t, s.tools)
	require.Zero(t, s.toolBytes)
}

// TestStreamLegacyFunctionBounds 用 t 验证名称/参数共用数量及字节预算，SDK 新响应清理旧片段。
func TestStreamLegacyFunctionBounds(t *testing.T) {
	t.Run("tool count", func(t *testing.T) {
		s := NewStreamSession(types.RelayFormatOpenAI)
		s.ObserveTransport(&http.Response{StatusCode: http.StatusOK}, nil)
		for i := 0; i < 129; i++ {
			s.CommitDelivery([]byte(fmt.Sprintf(`{"choices":[{"index":%d,"delta":{"function_call":{"name":"f","arguments":"{}"}}}]}`, i)))
			require.Len(t, s.tools, min(i+1, 128))
		}
		require.Equal(t, 128*3, s.toolBytes)
		s.ObserveTransport(&http.Response{StatusCode: http.StatusOK}, nil)
		require.Empty(t, s.tools)
		require.Zero(t, s.toolBytes)
		s.CommitDelivery([]byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"function_call"}]}`))
		require.False(t, s.Snapshot().Effective)
	})
	for _, field := range []string{"name", "arguments"} {
		t.Run(field+" byte budget", func(t *testing.T) {
			s := NewStreamSession(types.RelayFormatOpenAI)
			for _, size := range []int{MaxStreamFrameBytes / 2, MaxStreamFrameBytes/2 - 1, 1} {
				s.CommitDelivery([]byte(fmt.Sprintf(`{"choices":[{"index":0,"delta":{"function_call":{%q:%q}}}]}`, field, strings.Repeat("x", size))))
			}
			require.Equal(t, MaxStreamFrameBytes, s.toolBytes)
			s.CommitDelivery([]byte(fmt.Sprintf(`{"choices":[{"index":0,"delta":{"function_call":{%q:"x"}}}]}`, field)))
			require.Empty(t, s.tools)
			require.Zero(t, s.toolBytes)
			require.False(t, s.Snapshot().Effective)
		})
	}
	t.Run("shared name and arguments budget", func(t *testing.T) {
		s := NewStreamSession(types.RelayFormatOpenAI)
		s.CommitDelivery([]byte(fmt.Sprintf(`{"choices":[{"index":0,"delta":{"function_call":{"name":%q}}}]}`, strings.Repeat("x", MaxStreamFrameBytes/2))))
		s.CommitDelivery([]byte(fmt.Sprintf(`{"choices":[{"index":1,"delta":{"function_call":{"arguments":%q}}}]}`, strings.Repeat("x", MaxStreamFrameBytes/2))))
		require.Equal(t, MaxStreamFrameBytes, s.toolBytes)
		s.CommitDelivery([]byte(`{"choices":[{"index":1,"delta":{"function_call":{"name":"x"}}}]}`))
		require.Len(t, s.tools, 1)
		require.Equal(t, MaxStreamFrameBytes/2, s.toolBytes)
		require.False(t, s.Snapshot().Effective)
	})
}

// TestStreamLegacyFunctionDeliveryGate 用 t 验证原始帧不变，只有成功写入并刷新才确认旧版工具。
func TestStreamLegacyFunctionDeliveryGate(t *testing.T) {
	const frame = "data: {\"choices\":[{\"index\":0,\"delta\":{\"function_call\":{\"name\":\"lookup\",\"arguments\":\"{}\"}},\"finish_reason\":\"function_call\"}]}\n\n"
	for _, failure := range []string{"", "write", "flush"} {
		t.Run(failure, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			s := NewStreamSession(types.RelayFormatOpenAI)
			underlying := c.Writer
			switch failure {
			case "write":
				underlying = streamShortWriter{ResponseWriter: underlying}
			case "flush":
				underlying = streamFlushFailure{underlying}
			}
			w := NewStreamWriter(underlying, s, func(text string) int { return len(text) })
			w.Header().Set("Content-Type", "text/event-stream")
			_, err := w.Write([]byte(frame))
			require.False(t, s.Snapshot().Effective)
			if failure == "write" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, frame, rec.Body.String())
			}
			err = w.FlushError()
			if failure != "" {
				require.Error(t, err)
				require.False(t, s.Snapshot().Effective)
				require.Zero(t, s.Snapshot().EstimatedOutput)
			} else {
				require.NoError(t, err)
				require.True(t, s.Snapshot().Effective)
				require.Equal(t, len("lookup{}"), s.Snapshot().EstimatedOutput)
			}
		})
	}
}

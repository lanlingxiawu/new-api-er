package common

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

// TestStreamGeminiPolicyBoundary 区分合法策略终态、空反馈、缺尾帧及错误；t 提供纯内存协议夹具。
func TestStreamGeminiPolicyBoundary(t *testing.T) {
	for _, tc := range []struct {
		name string // 场景名称，用于定位分支回归。
		body string // 未经转换的上游 JSON。
		done bool   // 是否应视为完整的策略终态。
	}{
		{"blocked", `{"promptFeedback":{"blockReason":"SAFETY"}}`, true},
		{"empty_candidates", `{"candidates":[],"promptFeedback":{"blockReason":"BLOCKLIST"}}`, true},
		{"null_candidates", `{"candidates":null,"promptFeedback":{"blockReason":"SAFETY"}}`, true},
		{"future_reason", `{"promptFeedback":{"blockReason":"FUTURE_REASON"}}`, true},
		{"empty_object", `{}`, false},
		{"usage_only", `{"usageMetadata":{"promptTokenCount":17}}`, false},
		{"empty_feedback", `{"promptFeedback":{}}`, false},
		{"empty_reason", `{"promptFeedback":{"blockReason":"  "}}`, false},
		{"unspecified", `{"promptFeedback":{"blockReason":"BLOCK_REASON_UNSPECIFIED"}}`, false},
		{"padded_unspecified", `{"promptFeedback":{"blockReason":" BLOCK_REASON_UNSPECIFIED "}}`, false},
		{"numeric_reason", `{"promptFeedback":{"blockReason":1}}`, false},
		{"invalid_candidates", `{"candidates":{},"promptFeedback":{"blockReason":"SAFETY"}}`, false},
		{"unfinished_candidate", `{"candidates":[{"index":0}],"promptFeedback":{"blockReason":"SAFETY"}}`, false},
		{"native_error", `{"error":{"message":"original"},"promptFeedback":{"blockReason":"SAFETY"}}`, false},
		{"invalid_usage", `{"usageMetadata":{"promptTokenCount":-1},"promptFeedback":{"blockReason":"SAFETY"}}`, false},
	} {
		for _, whole := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{true: "_json", false: "_sse"}[whole], func(t *testing.T) {
				s := NewStreamSession(types.RelayFormatGemini)
				s.ExpectedChoices = 2 // 合法拦截不要求产生本来请求的候选。
				if whole {
					resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(tc.body))}
					s.ObserveHTTP(resp)
					_, _ = io.ReadAll(resp.Body)
					_ = resp.Body.Close()
				} else {
					_ = s.ObserveEvent("", []byte(tc.body))
					s.EndRead(io.EOF)
				}
				require.Equal(t, tc.done, s.ProtocolComplete())
				require.Equal(t, !tc.done, s.Snapshot().DiagnosticAvailable(false))
			})
		}
	}
	s := NewStreamSession(types.RelayFormatGemini)
	require.NoError(t, s.ObserveEvent("", []byte(`{"candidates":[{"index":0,"content":{"parts":[{"text":"partial"}]}}]}`)))
	require.NoError(t, s.ObserveEvent("", []byte(`{"promptFeedback":{"blockReason":"SAFETY"}}`)))
	s.EndRead(io.EOF)
	require.False(t, s.ProtocolComplete(), "反馈不替代先前候选缺失的结束帧")
	s = NewStreamSession(types.RelayFormatGemini)
	s.Fail("upstream_read_error", errors.New("first error"))
	_ = s.ObserveEvent("", []byte(`{"promptFeedback":{"blockReason":"SAFETY"}}`))
	require.Equal(t, StreamEndReason("upstream_read_error"), s.Snapshot().Reason)
}

// TestStreamCandidateCompletionRecomputed 验证晚出现候选撤销临时完成，逐候选结束而非按当前帧判断。
// 参数 t 为测试上下文；覆盖两种索引协议、逐个收尾及被截断后的 DONE。
func TestStreamCandidateCompletionRecomputed(t *testing.T) {
	for _, gemini := range []bool{false, true} {
		for _, complete := range []bool{false, true} {
			s := NewStreamSession(types.RelayFormatOpenAI)
			first := `{"choices":[{"index":0,"delta":{"content":"one"},"finish_reason":"stop"}]}`
			second := `{"choices":[{"index":1,"delta":{"content":"two"}}]}`
			last := `{"choices":[{"index":1,"delta":{},"finish_reason":"stop"}]}`
			if gemini {
				first = `{"candidates":[{"index":0,"content":{"parts":[{"text":"one"}]},"finishReason":"STOP"}]}`
				second = `{"candidates":[{"index":1,"content":{"parts":[{"text":"two"}]}}]}`
				last = `{"candidates":[{"index":1,"finishReason":"STOP"}]}`
			}
			require.NoError(t, s.ObserveEvent("", []byte(first)))
			require.NoError(t, s.ObserveEvent("", []byte(second)))
			require.False(t, s.Snapshot().Complete)
			if complete {
				require.NoError(t, s.ObserveEvent("", []byte(last)))
			}
			_ = s.ObserveEvent("", []byte("[DONE]"))
			s.EndRead(io.EOF)
			require.Equal(t, complete, s.Snapshot().Complete)
			require.Equal(t, !complete, s.Snapshot().DiagnosticAvailable(false))
		}
	}
}

// TestStreamExpectedChoices 验证声明候选数、逐候选累计、重复结束和异常索引的边界。
// 参数 t 为测试上下文；128 为现有候选资源上限，不通过夹具分配无界缓存。
func TestStreamExpectedChoices(t *testing.T) {
	s := NewStreamSession(types.RelayFormatOpenAI)
	s.ExpectedChoices = 2
	require.NoError(t, s.ObserveEvent("", []byte(`{"candidates":[{"index":0,"finishReason":"STOP"}]}`)))
	require.False(t, s.Snapshot().Complete)
	require.NoError(t, s.ObserveEvent("", []byte(`{"candidates":[{"index":0,"finishReason":"STOP"}]}`)))
	require.False(t, s.Snapshot().Complete)
	require.NoError(t, s.ObserveEvent("", []byte(`{"candidates":[{"index":1,"finishReason":"STOP"}]}`)))
	require.True(t, s.Snapshot().Complete)
	for _, index := range []string{"-1", "1.5", "128", `"0"`} {
		s = NewStreamSession(types.RelayFormatOpenAI)
		_ = s.ObserveEvent("", []byte(`{"choices":[{"index":`+index+`,"finish_reason":"stop"}]}`))
		require.Equal(t, StreamEndReason("upstream_protocol_error"), s.Snapshot().Reason)
	}
}

// streamEndReadFixture 在最后一个非空 Read 同时返回 err，用于检查字节优先于读取终止的顺序。
type streamEndReadFixture struct {
	data string // 仅在一次 Read 中消费的固定响应。
	err  error  // 与最后一批字节一起返回的错误。
}

// Read 将夹具字节写入 p；剩余数据耗尽时与 n 一起返回预设读取结果。
func (r *streamEndReadFixture) Read(p []byte) (int, error) {
	n := copy(p, r.data)
	r.data = r.data[n:]
	if len(r.data) == 0 {
		return n, r.err
	}
	return n, nil
}

// TestStreamObserverReadBoundaries 验证 NDJSON 同批错误、n+EOF、分片及完整 JSON 的读取错误。
// 参数 t 为测试上下文；读取异常不因收到语法完整 JSON 而变成正常成功。
func TestStreamObserverReadBoundaries(t *testing.T) {
	for _, size := range []int{1, 4096} {
		s := NewStreamSession(types.RelayFormatOpenAI)
		body := "{\"done\":false,\"response\":\"FIRST\"}\n{\"error\":\"later\"}\n"
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/x-ndjson"}}, Body: io.NopCloser(&streamEndReadFixture{data: body, err: io.EOF})}
		s.ObserveHTTP(resp)
		buf := make([]byte, size)
		var got strings.Builder
		for {
			n, err := resp.Body.Read(buf)
			got.Write(buf[:n])
			if got.Len() <= strings.IndexByte(body, '\n')+1 {
				require.Empty(t, s.Snapshot().Reason)
			}
			if err != nil {
				break
			}
		}
		require.Equal(t, body, got.String())
		require.Equal(t, StreamEndReason("upstream_error"), s.Snapshot().Reason)
	}
	s := NewStreamSession(types.RelayFormatOpenAI)
	body := `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(&streamEndReadFixture{data: body, err: io.ErrUnexpectedEOF})}
	s.ObserveHTTP(resp)
	_, err := io.ReadAll(resp.Body)
	require.Error(t, err)
	require.Equal(t, StreamEndReason("upstream_read_error"), s.Snapshot().Reason)
}

// TestStreamLegacyScannerReadOrder 验证独立扫描器收到第一帧时尚未观察同批下一帧的错误。
// 参数 t 为测试上下文；大缓冲读入两帧最易暴露预读状态提前的问题。
func TestStreamLegacyScannerReadOrder(t *testing.T) {
	s := NewStreamSession(types.RelayFormatOpenAI)
	body := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"TAIL\"},\"finish_reason\":\"stop\"}]}\n\nevent: error\ndata: {\"error\":{\"message\":\"later\"}}\n\n"
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
	s.ObserveHTTP(resp)
	scanner := bufio.NewScanner(resp.Body)
	require.True(t, scanner.Scan())
	require.Contains(t, scanner.Text(), "TAIL")
	require.Empty(t, s.Snapshot().Reason)
	for scanner.Scan() {
	}
	require.Equal(t, StreamEndReason("upstream_error"), s.Snapshot().Reason)
}

// TestStreamWholeJSONRequiresCompletion 验证语法合法不等于成功，并保留已有标准完整响应。
// 参数 t 为测试上下文；空对象和仅用量属于异常，不为它们添加成功尾帧。
func TestStreamWholeJSONRequiresCompletion(t *testing.T) {
	for _, tc := range []struct {
		body     string
		complete bool
	}{
		{`{}`, false}, {`{"usage":{"output_tokens":9523}}`, false},
		{`{"choices":[]}`, false},
		{`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`, true},
		{`{"output":{"message":{"content":[{"text":"ok"}]}},"stopReason":"end_turn","usage":{"inputTokens":4,"outputTokens":1}}`, true},
	} {
		s := NewStreamSession(types.RelayFormatOpenAI)
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(tc.body))}
		s.ObserveHTTP(resp)
		_, _ = io.ReadAll(resp.Body)
		require.Equal(t, tc.complete, s.Snapshot().Complete, tc.body)
		if !tc.complete {
			require.NotEmpty(t, s.Snapshot().Reason)
		}
	}
}

// TestStreamObserverLargeFrameBoundaries 验证分隔符跨越 4 KiB/8 KiB 读取边界时仍逐字保留完整帧。
// 参数 t 为测试上下文；覆盖 CRLF 分隔符四个字节位于相邻底层 Read 的全部边界位置。
func TestStreamObserverLargeFrameBoundaries(t *testing.T) {
	prefix := `data: {"choices":[{"index":0,"delta":{"content":"`
	suffix := "\"},\"finish_reason\":\"stop\"}]}\r\n\r\n"
	for _, length := range []int{4095, 4096, 4097, 4098, 4099, 8191, 8192, 8193, 8194, 8195} {
		body := prefix + strings.Repeat("x", length-len(prefix)-len(suffix)) + suffix + "data: [DONE]\r\n\r\n"
		s := NewStreamSession(types.RelayFormatOpenAI)
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
		s.ObserveHTTP(resp)
		got, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, body, string(got))
		require.True(t, s.Snapshot().Complete)
	}
}

package common

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

// jsonProbeCheckedReader 在每次底层读取前验证首行游标已追上旧数据，防止重复前缀搜索回归。
type jsonProbeCheckedReader struct {
	reader   io.Reader           // 内存原始响应读取器。
	observed *streamObservedBody // 受测包装，用于检查格式探测游标。
	t        *testing.T          // 当前测试的断言上下文。
	read     int                 // 已返回的字节数，不包含尚未读取的内容。
}

// Read 校验已扫描位置再提供下一片 p；返回底层读取数量及错误，不改变输入片大小。
func (r *jsonProbeCheckedReader) Read(p []byte) (int, error) {
	require.Equal(r.t, r.read, r.observed.jsonProbeScanned, "每次只搜索新增字节")
	require.False(r.t, r.observed.jsonProbeDone, "单行未结束前只推进游标")
	n, err := r.reader.Read(p)
	r.read += n
	return n, err
}

// TestStreamJSONProbeProgress 为性能修复提供确定性断言，不依赖机器计时；t 使用多次 4 KiB 读取。
func TestStreamJSONProbeProgress(t *testing.T) {
	body := `{"text":"` + strings.Repeat("x", 64<<10) + `"}`
	r := &jsonProbeCheckedReader{reader: strings.NewReader(body), t: t}
	s := NewStreamSession(types.RelayFormatOpenAI)
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(r)}
	s.ObserveHTTP(resp)
	r.observed = resp.Body.(*streamObservedBody)
	_, err := io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.True(t, r.observed.jsonProbeDone)
	require.Equal(t, len(body), r.observed.jsonProbeScanned)
}

// TestStreamJSONProbeBoundaries 验证长首行、排版 JSON 和伪装 JSON 媒体类型的 NDJSON；t 仅使用内存读取器。
func TestStreamJSONProbeBoundaries(t *testing.T) {
	for _, size := range []int{4095, 4096, 4097, 1 << 20} {
		for _, newline := range []string{"", "\n"} {
			body := `{"text":"` + strings.Repeat("x", size) + `"}` + newline
			s := NewStreamSession(types.RelayFormatOpenAI)
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
			s.ObserveHTTP(resp)
			got, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, body, string(got))
			require.True(t, s.ProtocolComplete())
		}
	}
	for _, body := range []string{
		"{\n\"text\":\"hello\"\n}",
		"{\"done\":false,\"response\":\"hello\"}\n{\"done\":true}\n",
		"{\"done\":false,\"response\":\"hello\"}\n{\"done\":true}",
	} {
		s := NewStreamSession(types.RelayFormatOpenAI)
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
		s.ObserveHTTP(resp)
		got, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, body, string(got))
		require.True(t, s.ProtocolComplete())
	}
}

// BenchmarkStreamWholeJSONProbe 对比完整单行图片 JSON 的读取/格式探测成本；b 控制迭代，不包含网络和计费。
func BenchmarkStreamWholeJSONProbe(b *testing.B) {
	for _, size := range []int{64 << 10, 1 << 20, 4 << 20} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			body := `{"data":[{"b64_json":"` + strings.Repeat("a", size) + `"}]}`
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s := NewStreamSession(types.RelayFormatOpenAI)
				s.ExpectedImages = 1
				resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
				s.ObserveHTTP(resp)
				_, err := io.Copy(io.Discard, resp.Body)
				require.NoError(b, err)
			}
		})
	}
}

package common

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

// TestStreamImageWholeJSON 校验图片会话的完整响应边界；t 管理断言，夹具不读取外部图片。
func TestStreamImageWholeJSON(t *testing.T) {
	for _, tc := range []struct {
		name, body  string // 原始响应及子用例名。
		want, count int    // 请求张数和通过校验的确认张数；count 为 0 表示响应应失败。
	}{
		{"base64", `{"data":[{"b64_json":"aGk="}]}`, 1, 1},
		{"url", `{"data":[{"url":"https://fixture.invalid/image.png"}]}`, 1, 1},
		{"multiple", `{"data":[{"b64_json":"aGk="},{"url":"https://fixture.invalid/2"}]}`, 2, 2},
		{"extra image", `{"data":[{"b64_json":"aGk="},{"b64_json":"aGk="}]}`, 1, 2},
		{"missing image", `{"data":[{"b64_json":"aGk="}]}`, 2, 0},
		{"empty", `{"data":[]}`, 1, 0},
		{"null", `{"data":null}`, 1, 0},
		{"not array", `{"data":{"b64_json":"aGk="}}`, 1, 0},
		{"empty item", `{"data":[{}]}`, 1, 0},
		{"whitespace", `{"data":[{"url":"  "}]}`, 1, 0},
		{"invalid item", `{"data":[{"b64_json":42}]}`, 1, 0},
		{"invalid second item", `{"data":[{"b64_json":"aGk="},null]}`, 2, 0},
		{"text is not image", `{"text":"ok"}`, 1, 0},
		{"error wins", `{"error":{"message":"fixture"},"data":[{"b64_json":"aGk="}]}`, 1, 0},
		{"invalid usage wins", `{"usage":{"output_tokens":-1},"data":[{"b64_json":"aGk="}]}`, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStreamSession(types.RelayFormatOpenAI)
			s.ExpectedImages = tc.want
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(tc.body))}
			s.ObserveHTTP(resp)
			body, err := io.ReadAll(resp.Body)
			require.Equal(t, tc.body, string(body))
			if tc.count > 0 {
				require.NoError(t, err)
				require.True(t, s.ProtocolComplete())
				require.Equal(t, tc.count, s.Snapshot().Evidence["image_count"])
			} else {
				require.Error(t, err)
				require.False(t, s.ProtocolComplete())
				require.True(t, s.Snapshot().DiagnosticAvailable(false))
			}
		})
	}
	// 普通聊天会话不因存在 data 数组而跳过自身响应结构校验。
	s := NewStreamSession(types.RelayFormatOpenAI)
	s.observeWholeJSON([]byte(`{"data":[{"url":"https://fixture.invalid/image.png"}]}`), false)
	require.False(t, s.ProtocolComplete())
}

// TestStreamImageDoneRequiresCompletedCount 确认 DONE 不替代图片完成事件；t 覆盖空流、预览、少图和足量。
func TestStreamImageDoneRequiresCompletedCount(t *testing.T) {
	for _, tc := range []struct {
		want, completed int  // 期望和已收到的完成图片数。
		preview         bool // 是否先成功交付预览图。
	}{{1, 0, false}, {1, 0, true}, {2, 1, false}, {1, 1, false}, {2, 2, true}} {
		s := NewStreamSession(types.RelayFormatOpenAI)
		s.ExpectedImages = tc.want
		if tc.preview {
			frame := []byte(`{"type":"image_generation.partial_image","b64_json":"aGk="}`)
			require.NoError(t, s.ObserveEvent("", frame))
			s.CommitDelivery(frame)
		}
		for i := 0; i < tc.completed; i++ {
			frame := []byte(`{"type":"image_generation.completed","b64_json":"aGk="}`)
			require.NoError(t, s.ObserveEvent("", frame))
			s.CommitDelivery(frame)
		}
		err := s.ObserveEvent("", []byte("[DONE]"))
		if tc.completed < tc.want {
			require.Error(t, err, "case %+v", tc)
			require.Equal(t, StreamEndReason("upstream_protocol_error"), s.Snapshot().Reason)
		} else {
			require.NoError(t, err)
		}
		s.EndRead(io.EOF)
		require.Equal(t, tc.completed >= tc.want, s.ProtocolComplete())
		require.Equal(t, tc.preview || tc.completed > 0, s.Snapshot().Effective)
		require.Equal(t, tc.completed, s.Snapshot().Evidence["image_count"])
	}
}

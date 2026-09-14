package common

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestStreamMediaTypeNormalization 覆盖类型提取、二进制白名单和参数隔离；t 提供等价类与边界断言。
func TestStreamMediaTypeNormalization(t *testing.T) {
	for _, tc := range []struct {
		header, mediaType string // 原始头及预期基础类型，空头也保留空值。
		binary            bool   // 是否属于既有裸媒体协议。
	}{
		{"", "", false},
		{" ; charset=utf-8", "", false},
		{"audio/mpeg", "audio/mpeg", true},
		{" Video/MP4 ; codecs=avc1 ", "video/mp4", true},
		{"Application/Octet-Stream; charset=binary", "application/octet-stream", true},
		{`application/octet-stream; name="foo;json"`, "application/octet-stream", true},
		{"application/octet-streaming", "application/octet-streaming", false},
		{`application/json; name="audio/mpeg"`, "application/json", false},
		{"Text/Event-Stream; charset=UTF-8", "text/event-stream", false},
	} {
		t.Run(tc.header, func(t *testing.T) {
			require.Equal(t, tc.mediaType, StreamMediaType(tc.header))
			require.Equal(t, tc.binary, IsStreamBinaryContentType(tc.header))
		})
	}
}

// TestStreamMediaTypeObservation 验证协议仅由基础 MIME 类型决定，参数值不劫持分类；t 为本地断言上下文。
func TestStreamMediaTypeObservation(t *testing.T) {
	for _, tc := range []struct {
		contentType, body string // 原始响应头和值，包含可能误导旧分类的参数。
	}{
		{" Application/Octet-Stream ; charset=binary ", "media-bytes"},
		{`audio/mpeg; name="audio.json.eventstream"`, "media-bytes"},
		{`application/json; name="eventstream"`, `{"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}]}`},
		{`Text/Event-Stream; name="application/json"`, "data: {\"choices\":[{\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
	} {
		t.Run(tc.contentType, func(t *testing.T) {
			s := NewStreamSession(types.RelayFormatOpenAI)
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {tc.contentType}}, Body: io.NopCloser(strings.NewReader(tc.body))}
			s.ObserveHTTP(resp)
			got, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, tc.body, string(got))
			require.True(t, s.Snapshot().Complete)
			require.Equal(t, tc.contentType, resp.Header.Get("Content-Type"))
		})
	}
}

// TestStreamMediaTypeWriter 验证大小写及参数不影响 SSE 半帧缓存、JSON 正文和媒体交付；t 为测试上下文。
func TestStreamMediaTypeWriter(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body string // 用例、响应头、实际下游内容。
		isSSE, isMedia          bool   // 期望分帧与有效媒体分类。
	}{
		{"SSE case", "Text/Event-Stream; charset=utf-8", "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n", true, false},
		{"JSON misleading parameter", `Application/JSON; name="text/event-stream"`, `{"choices":[{"message":{"content":"hi"}}]}`, false, false},
		{"media misleading parameter", `application/octet-stream; name="audio.json"`, "media-bytes", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			s := NewStreamSession(types.RelayFormatOpenAI)
			w := NewStreamWriter(c.Writer, s, nil)
			w.Header().Set("Content-Type", tc.contentType)
			_, err := w.Write([]byte(tc.body[:1]))
			require.NoError(t, err)
			if tc.isSSE {
				require.Empty(t, rec.Body.String())
			}
			_, err = w.Write([]byte(tc.body[1:]))
			require.NoError(t, err)
			require.NoError(t, w.FlushError())
			require.Equal(t, tc.body, rec.Body.String())
			require.True(t, s.Snapshot().Effective)
			require.Equal(t, tc.isMedia, s.Snapshot().MediaBytes > 0)
			require.Equal(t, tc.contentType, rec.Header().Get("Content-Type"))
		})
	}
}

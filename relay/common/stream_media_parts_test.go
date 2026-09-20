package common

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestCommitDeliveryCountsInlineMedia 校验原生内联媒体按张计数、正文不进文本估算。
// 背景：inlineData 原先只标记 Effective，纯图片响应在估算路径上按 0 输出结算，属于漏收。
func TestCommitDeliveryCountsInlineMedia(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event string
		text  string
		media int
	}{
		{
			name:  "纯图片",
			event: `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"QUJDRA=="}}]}}]}`,
			media: 1,
		},
		{
			name:  "图文混合只计图，文本照常估算",
			event: `{"candidates":[{"content":{"parts":[{"text":"look"},{"inlineData":{"mimeType":"image/png","data":"QUJDRA=="}}]}}]}`,
			text:  "look",
			media: 1,
		},
		{
			name:  "多张分别计数",
			event: `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"QQ=="}},{"inlineData":{"mimeType":"image/jpeg","data":"Qg=="}}]}}]}`,
			media: 2,
		},
		{
			name:  "空 data 不计数",
			event: `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":""}}]}}]}`,
			media: 0,
		},
		{
			name:  "纯文本不计数",
			event: `{"candidates":[{"content":{"parts":[{"text":"plain answer"}]}}]}`,
			text:  "plain answer",
			media: 0,
		},
		{
			name:  "OpenAI 形状不误计",
			event: `{"choices":[{"delta":{"content":"hello"}}]}`,
			text:  "hello",
			media: 0,
		},
		{
			// /v1/images 的图片走 Evidence["image_count"] 按张计价，不能再按 token 折算一次。
			name:  "图片端点的 b64_json 不计张数",
			event: `{"data":[{"b64_json":"QQ=="},{"b64_json":"Qg=="}]}`,
			media: 0,
		},
		{
			name:  "Claude 文本块不计张数",
			event: `{"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}`,
			text:  "hi",
			media: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStreamSession(types.RelayFormatOpenAI)
			s.CommitDelivery([]byte(tc.event))

			require.Equal(t, tc.text, s.TakeDeliveredText())
			require.Equal(t, tc.media, s.TakeDeliveredMedia())
			require.Zero(t, s.TakeDeliveredMedia(), "取走即清零，不跨批重复计费")
		})
	}
}

// TestCommitDeliveryAccumulatesMediaAcrossEvents 断言未取走的张数跨事件累计。
func TestCommitDeliveryAccumulatesMediaAcrossEvents(t *testing.T) {
	s := NewStreamSession(types.RelayFormatGemini)
	event := `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"QQ=="}}]}}]}`
	s.CommitDelivery([]byte(event))
	s.CommitDelivery([]byte(event))

	require.Equal(t, 2, s.TakeDeliveredMedia())
	require.True(t, s.Snapshot().Effective, "交付过媒体即视为有效内容")
}

// TestStreamWriterPassesMediaToEstimator 验证写入器把内联媒体张数交给估算闭包：
// 只含媒体、没有文本的批次也必须触发估算，否则纯图片交付按 0 计。
func TestStreamWriterPassesMediaToEstimator(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	s := NewStreamSession(types.RelayFormatGemini)

	type batch struct {
		text  string
		media int
	}
	var batches []batch
	w := NewStreamWriter(c.Writer, s, func(text string, media int) int {
		batches = append(batches, batch{text: text, media: media})
		return media * 1400
	})
	w.Header().Set("Content-Type", "text/event-stream")

	_, err := w.Write([]byte(`data: {"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"QQ=="}}]}}]}` + "\n\n"))
	require.NoError(t, err)
	require.Empty(t, batches, "刷新前不得计入")
	require.NoError(t, w.FlushError())

	require.Equal(t, []batch{{text: "", media: 1}}, batches, "纯媒体批次也要交给估算闭包")
	require.Equal(t, 1400, s.Snapshot().EstimatedOutput)

	_, err = w.Write([]byte(`data: {"candidates":[{"content":{"parts":[{"text":"done"}]}}]}` + "\n\n"))
	require.NoError(t, err)
	require.NoError(t, w.FlushError())
	require.Equal(t, batch{text: "done", media: 0}, batches[len(batches)-1], "媒体张数取走后不跨批重复")
	require.Equal(t, 1400, s.Snapshot().EstimatedOutput)
}

// TestReceivedEstimatorCountsMedia 验证接收侧同样按张计量：这是"客户端断开"口径的唯一来源，
// 修复前 inlineData 在这里不产生任何量，纯图片响应按 0 结算。
func TestReceivedEstimatorCountsMedia(t *testing.T) {
	s := NewStreamSession(types.RelayFormatGemini)
	s.ResponseGate = &StreamResponseGate{}
	s.ObserveTransport(&http.Response{StatusCode: 200}, nil)

	var sawText string
	var sawMedia int
	s.SetReceivedEstimator(func() func(string, int) int {
		return func(text string, media int) int {
			sawText += text
			sawMedia += media
			return len(text) + media*1400
		}
	})

	require.NoError(t, s.ObserveEvent("", []byte(`{"candidates":[{"content":{"parts":[{"text":"pic:"},{"inlineData":{"mimeType":"image/png","data":"QQ=="}}]}}]}`)))

	require.Equal(t, "pic:", sawText)
	require.Equal(t, 1, sawMedia)
	require.Equal(t, len("pic:")+1400, s.Snapshot().ReceivedOutput)
}

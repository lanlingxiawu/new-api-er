package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestUnifiedStreamOrderedTail 验证同批帧和 EOF 不吞此前候选尾段，并沿用异常计费证据。
// 参数 t 为测试上下文；只调用实际渠道转换器和结算选择，不执行余额操作。
func TestUnifiedStreamOrderedTail(t *testing.T) {
	for _, suffix := range []string{"", "event: error\ndata: {\"error\":{\"message\":\"later\"}}\n\n", "data: {broken}\n\n"} {
		for _, format := range []bool{false, true} {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			n := 2
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, Request: &dto.GeneralOpenAIRequest{N: &n}, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
			info.ChannelSetting.ForceFormat = format
			service.BeginStreamAttempt(c, info)
			body := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"TAIL\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10}}\n\n" + suffix
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamDiagnostic.Observe(resp)
			info.StreamSession.ObserveHTTP(resp)
			usage, err := OaiStreamHandler(c, info, resp)
			require.Nil(t, err)
			final := service.FinalizeStreamUsage(c, info, usage)
			require.Contains(t, rec.Body.String(), "TAIL")
			require.NotContains(t, rec.Body.String(), "[DONE]")
			require.True(t, info.StreamResult.Failed)
			require.True(t, info.StreamResult.EffectiveContent)
			require.Equal(t, "upstream", info.StreamResult.UsageSource)
			require.Equal(t, 10, final.PromptTokens)
		}
	}
}

// streamFlushRecorder 在真实 Flush 时复制已经写出的响应，供另一协程确认交付先于后续上游事件。
type streamFlushRecorder struct {
	*httptest.ResponseRecorder             // 请求所有者专用的内存响应。
	flushed                    chan string // 有界的成功刷新快照，非阻塞发送。
}

// Flush 先执行底层刷新，再向 flushed 发送快照；快照满时跳过，避免测试观察者影响转发。
func (w *streamFlushRecorder) Flush() {
	w.ResponseRecorder.Flush()
	select {
	case w.flushed <- w.Body.String():
	default:
	}
}

// TestUnifiedStreamFirstFrameDoesNotWait 验证当前帧刷新无需下一帧或 EOF；同时覆盖各下游转换协议。
// 参数 t 为测试上下文；内存管道模拟上游在第一帧后暂停，收到下游数据后才释放下一帧。
func TestUnifiedStreamFirstFrameDoesNotWait(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatGemini} {
		t.Run(string(format), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r, w := io.Pipe()
			defer r.Close()
			defer w.Close()
			rec := &streamFlushRecorder{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan string, 10)}
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: format, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}, ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{}}
			service.BeginStreamAttempt(c, info)
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: r}
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamSession.ObserveHTTP(resp)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				u, _ := OaiStreamHandler(c, info, resp)
				service.FinalizeStreamUsage(c, info, u)
			}()
			// 两段写入同一帧，验证既不等下一事件，也不把缺少空行的半帧当成已交付。
			_, err := io.WriteString(w, "data: {\"id\":\"chatcmpl-latency\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"FIRST\"}}]}\n")
			require.NoError(t, err)
			select {
			case body := <-rec.flushed:
				require.NotContains(t, body, "FIRST")
			default:
			}
			_, err = io.WriteString(w, "\n")
			require.NoError(t, err)
			timer := time.NewTimer(time.Second)
			defer timer.Stop()
			received := false
			for !received {
				select {
				case body := <-rec.flushed:
					received = strings.Contains(body, "FIRST")
				case <-timer.C:
					cancel()
					_ = w.Close()
					<-finished
					require.FailNow(t, "first frame waited for a later upstream event")
				}
			}
			_, err = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			require.NoError(t, err)
			_ = w.Close()
			<-finished
			require.False(t, info.StreamResult.Failed)
			require.Equal(t, 1, strings.Count(rec.Body.String(), "FIRST"))
			if format == types.RelayFormatClaude {
				require.Equal(t, 1, strings.Count(rec.Body.String(), "event: message_stop"), rec.Body.String())
			}
		})
	}
}

// TestUnifiedStreamWholeJSONBeforeDelivery 验证入口流式但适配器按完整 JSON 处理时，空结构在写下游前报错。
// 参数 t 为测试上下文；正常完整响应和入口非流式保持原结果，不将无正文用量当成有效交付。
func TestUnifiedStreamWholeJSONBeforeDelivery(t *testing.T) {
	for _, body := range []string{`{}`, `{"usage":{"prompt_tokens":10,"completion_tokens":5}}`, `{"choices":[{"finish_reason":"stop"}]}`} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
		service.BeginStreamAttempt(c, info)
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
		info.StreamSession.ObserveTransport(resp, nil)
		info.StreamDiagnostic.Observe(resp)
		info.StreamSession.ObserveHTTP(resp)
		usage, err := OpenaiHandler(c, info, resp)
		require.NotNil(t, err, body)
		require.Empty(t, rec.Body.String())
		final := service.FinalizeStreamUsage(c, info, usage)
		require.True(t, info.StreamResult.DiagnosticAvailable)
		require.Equal(t, "none", info.StreamResult.UsageSource)
		require.Zero(t, final.TotalTokens)
		require.Contains(t, rec.Body.String(), "event: error")
	}
}

// TestUnifiedStreamClaudeFirstFinishedChunk 验证首帧同时带正文和结束原因时正文只交付一次，尾部用量可延后到独立帧。
// 参数 t 为测试上下文；包含未预建转换状态的真实入口形态，避免首帧结束触发空指针。
func TestUnifiedStreamClaudeFirstFinishedChunk(t *testing.T) {
	for _, usageFrame := range []string{"", "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}}\n\n"} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
		info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
		service.BeginStreamAttempt(c, info)
		body := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ONLY_TAIL\"},\"finish_reason\":\"stop\"}]}\n\n" + usageFrame + "data: [DONE]\n\n"
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
		info.StreamSession.ObserveTransport(resp, nil)
		info.StreamSession.ObserveHTTP(resp)
		usage, err := OaiStreamHandler(c, info, resp)
		require.Nil(t, err)
		service.FinalizeStreamUsage(c, info, usage)
		require.False(t, info.StreamResult.Failed, rec.Body.String())
		require.Equal(t, 1, strings.Count(rec.Body.String(), "ONLY_TAIL"))
		require.Equal(t, 1, strings.Count(rec.Body.String(), "event: message_stop"))
		if usageFrame != "" {
			require.Contains(t, rec.Body.String(), `"output_tokens":2`)
		}
	}
}

// TestUnifiedStreamErrorRawDespiteInvalidUsage 验证上游原生 error 即使附带非法用量，也保留原始错误帧而非替换成自定义错误。
// 参数 t 为测试上下文；非法 usage 不进入确认用量，无有效内容按零计费选择处理。
func TestUnifiedStreamErrorRawDespiteInvalidUsage(t *testing.T) {
	body := "event: error\r\ndata: {\"error\":{\"message\":\"original\",\"type\":\"server_error\"},\"usage\":\"invalid\"}\r\n\r\n"
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, FinalRequestRelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}}
	service.BeginStreamAttempt(c, info)
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
	info.StreamSession.ObserveTransport(resp, nil)
	info.StreamSession.ObserveHTTP(resp)
	u, _ := OaiStreamHandler(c, info, resp)
	final := service.FinalizeStreamUsage(c, info, u)
	require.Equal(t, body, rec.Body.String())
	require.Zero(t, final.TotalTokens)
	require.Empty(t, info.StreamSession.Snapshot().Evidence)
}

// TestUnifiedStreamClaudePreamble 验证前置 ping/空 choices/usage 不占用转换首帧；t 为内存夹具上下文。
func TestUnifiedStreamClaudePreamble(t *testing.T) {
	for _, prefix := range []string{
		"event: ping\ndata: {\"type\":\"ping\"}\n\n",
		"data: {\"choices\":[]}\n\n",
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":0}}\n\n",
		"data: {\"type\":\"ping\"}\n\ndata: {\"choices\":[]}\n\n",
	} {
		for _, finished := range []bool{false, true} {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
			service.BeginStreamAttempt(c, info)
			body := prefix + "data: {\"id\":\"chat-test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"BODY_ONCE\"}}]}\n\n"
			if finished {
				body += "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
			}
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamSession.ObserveHTTP(resp)
			u, err := OaiStreamHandler(c, info, resp)
			require.Nil(t, err)
			service.FinalizeStreamUsage(c, info, u)
			got := rec.Body.String()
			require.Equal(t, 1, strings.Count(got, "event: message_start"), got)
			require.Less(t, strings.Index(got, "event: message_start"), strings.Index(got, "event: content_block_start"))
			require.Equal(t, 1, strings.Count(got, "BODY_ONCE"))
			require.Equal(t, !finished, info.StreamResult.Failed)
			if finished {
				require.Equal(t, 1, strings.Count(got, "event: message_stop"))
			} else {
				require.NotContains(t, got, "event: message_stop")
				require.Contains(t, got, "event: error")
			}
		}
	}
}

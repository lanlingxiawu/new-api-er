package common

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// StreamWriteTimeout 是 HTTP 流每次正文、保活和终止错误写入的有界期限；不替代请求整体超时。
const StreamWriteTimeout = 30 * time.Second

// StreamWriter 是 Gin 透明包装；只缓存一批有界 SSE 帧，不保留完整下游响应。
type StreamWriter struct {
	gin.ResponseWriter                       // 原始下游写入器，保留 Gin 的状态码、头与连接接口。
	mu                 sync.Mutex            // 串行化帧缓存及刷新，不与上游读取共享此锁。
	session            *StreamSession        // 当前尝试的协议、终止和有效交付状态。
	estimate           func(string, int) int // 增量估算回调，参数为本批成功交付正文与内联媒体张数，返回新增而非累计 token。
	pending            []byte                // 尚未组成完整 SSE 帧的尾部。
	framing            StreamFrameScanner    // 下游 SSE 增量行游标，与当前 pending 同步推进。
	delivered          [][]byte              // 已完整 Write、尚待 Flush 确认的事件数据。
	buffered           int                   // 当前交付批次字节数，受单帧上限约束。
	media              int                   // 当前等待刷新确认的裸媒体字节。
	jsonBody           []byte                // 入口流式而渠道返回完整 JSON 时，最多一帧大小的待确认交付。
	writeErr           error                 // 底层写/刷新错误；后续调用沿用同一错误。
}

// NewStreamWriter 为下游 writer 安装语义观察，session 提供流状态；estimate 按交付正文与媒体张数返回新增 token，可为 nil。
// 返回请求局部包装器，不执行写入；非活动会话的 Write 直接转交底层。
func NewStreamWriter(writer gin.ResponseWriter, session *StreamSession, estimate func(string, int) int) *StreamWriter {
	return &StreamWriter{ResponseWriter: writer, session: session, estimate: estimate}
}

// Unwrap 让 ResponseController/专用处理器找到原有响应写入器，不改变连接管理。
func (w *StreamWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// WriteString 把字符串 s 交给 Write，返回其接受长度和错误，避免 StringWriter 快捷路径绕过计量。
func (w *StreamWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

// Write 组装完整 SSE 帧后写出；候选结束无需等其他候选，Claude delta 须有真实 stop_reason 或全流完成证据。
// 整条响应结束仍要求全局 Complete，首个终止错误之后过滤所有成功尾帧。
// 参数 p 为本次下游响应字节；返回输入消费长度及写入/转换错误，成功消费可能只是缓存或过滤，并不等于已向客户端交付。
func (w *StreamWriter) Write(p []byte) (n int, err error) {
	if !w.session.Active() {
		return w.ResponseWriter.Write(p)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			n = 0
			err = errors.New("downstream write panic")
			w.writeErr = err
			w.session.ClientFailed(err)
		}
	}()
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	// 只按基础 MIME 类型分类，避免参数中的 json/SSE 字样影响缓存和交付证据。
	ct := StreamMediaType(w.Header().Get("Content-Type"))
	if !strings.Contains(ct, "text/event-stream") {
		if len(p) > 0 && IsStreamBinaryContentType(ct) {
			w.session.RecordReceivedMedia(0)
		}
		n, err := w.ResponseWriter.Write(p)
		if err == nil && n != len(p) {
			err = io.ErrShortWrite
		}
		if err != nil {
			w.writeErr = err
			w.session.ClientFailed(err)
		} else {
			w.media += n
			if strings.Contains(ct, "json") && len(w.jsonBody)+n <= MaxStreamFrameBytes {
				w.jsonBody = append(w.jsonBody, p[:n]...)
			}
		}
		return n, err
	}
	if len(w.pending)+len(p) > MaxStreamFrameBytes {
		w.session.Fail("response_conversion_error", errors.New("downstream frame exceeds limit"))
		return 0, errors.New("downstream frame exceeds limit")
	}
	w.pending = append(w.pending, p...)
	for {
		end := w.framing.End(w.pending)
		if end == 0 {
			break
		}
		frame := w.pending[:end]
		event, data := StreamFramePayload(frame)
		if len(bytes.TrimSpace(data)) > 0 && !bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) && !gjson.ValidBytes(data) {
			w.pending = nil
			w.session.Fail("response_conversion_error", errors.New("invalid downstream event JSON"))
			return 0, errors.New("invalid downstream event JSON")
		}
		if scope := classifyStreamSuccessEvent(event, data); scope != streamSuccessNone {
			w.session.mu.Lock()
			blocked := w.session.state.Reason != "" || (scope == streamSuccessResponse && !w.session.state.Complete)
			if scope == streamSuccessClaudeDelta && !w.session.claudeStop && !w.session.state.Complete {
				blocked = true
			}
			w.session.mu.Unlock()
			if blocked {
				w.pending = w.pending[end:]
				continue
			}
		}
		n, err := w.ResponseWriter.Write(frame)
		if err == nil && n != len(frame) {
			err = io.ErrShortWrite
		}
		if err != nil {
			w.writeErr = err
			w.session.ClientFailed(err)
			return 0, err
		}
		if len(data) > 0 {
			w.buffered += len(data)
			if w.buffered > MaxStreamFrameBytes {
				w.session.Fail("response_conversion_error", errors.New("unflushed stream batch exceeds limit"))
				return 0, errors.New("unflushed stream batch exceeds limit")
			}
			w.delivered = append(w.delivered, bytes.Clone(data))
		}
		w.pending = w.pending[end:]
	}
	if len(w.pending) == 0 {
		w.pending = nil
	}
	return len(p), nil
}

// Flush 执行可观察错误的刷新；Gin 接口没有返回值，错误保存在会话供最终结算使用。
func (w *StreamWriter) Flush() { _ = w.FlushError() }

// FlushError 刷新成功后才提交有效交付；优先找到底层 FlushError，避免 Gin 吞掉传输错误。
func (w *StreamWriter) FlushError() (err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("downstream flush panic")
			w.writeErr = err
			w.session.ClientFailed(err)
		}
	}()
	if w.writeErr != nil {
		return w.writeErr
	}
	var writer http.ResponseWriter = w.ResponseWriter
	for i := 0; i < 16; i++ {
		if _, ok := writer.(interface{ FlushError() error }); ok {
			break
		}
		u, ok := writer.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			break
		}
		writer = u.Unwrap()
	}
	err = http.NewResponseController(writer).Flush()
	if err != nil {
		w.writeErr = err
		w.session.ClientFailed(err)
		w.delivered = nil
		w.media = 0
		return err
	}
	if w.session.Active() {
		for _, data := range w.delivered {
			w.session.CommitDelivery(data)
		}
		if w.media > 0 && !strings.Contains(StreamMediaType(w.Header().Get("Content-Type")), "json") {
			w.session.CommitMediaDelivery(w.media)
		}
		if len(w.jsonBody) > 0 && gjson.ValidBytes(w.jsonBody) {
			w.session.CommitDelivery(w.jsonBody)
			w.jsonBody = nil
		}
		text := w.session.TakeDeliveredText()
		mediaParts := w.session.TakeDeliveredMedia()
		if w.estimate != nil && (text != "" || mediaParts > 0) {
			w.session.AddEstimatedOutput(w.estimate(text, mediaParts))
		}
	}
	w.delivered = nil
	w.buffered = 0
	w.media = 0
	return nil
}

// Finish 检查未闭合的转换帧并刷新最后一个媒体批次；在渠道处理器退出后调用。
func (w *StreamWriter) Finish() {
	if w == nil || !w.session.Active() {
		return
	}
	w.mu.Lock()
	incomplete := len(bytes.TrimSpace(w.pending)) > 0
	pending := len(w.delivered) > 0 || w.media > 0
	w.mu.Unlock()
	if incomplete {
		w.session.Fail("response_conversion_error", errors.New("downstream conversion ended with an incomplete frame"))
	}
	if pending {
		_ = w.FlushError()
	}
}

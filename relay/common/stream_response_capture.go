package common

import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"sort"
	"sync"
)

// StreamResponseCaptureKey 在请求上下文中关联当前尝试的响应采集器。
const StreamResponseCaptureKey = "stream_response_capture"

// StreamDiagnosticAttemptKey 保存公开的尝试编号，供详情接口精确选择同一请求的日志。
const StreamDiagnosticAttemptKey = "stream_diagnostic_attempt"

// StreamResponseOnlyKey 标记只记录响应诊断、不记录请求，避免普通错误日志暴露原始原因。
const StreamResponseOnlyKey = "stream_response_only"

// StreamResponseSnapshot 保存 HTTP 客户端读取的实体字节及响应头，而非 SDK 解码结果或 TCP/TLS 报文。
type StreamResponseSnapshot struct {
	StatusCode       int         `json:"status_code"`          // 上游 HTTP 状态码；尚未取得响应时为 0。
	ResponseHeaders  http.Header `json:"response_headers"`     // 保留多值的原始响应头，按名称排序采集，最多约 16 KiB。
	HeadersTruncated bool        `json:"headers_truncated"`    // 存在因字节预算不足而省略的响应头值。
	BodyHead         []byte      `json:"body_head_base64"`     // 首部最多 1024 字节，JSON 序列化时使用 base64 保留任意字节。
	BodyTail         []byte      `json:"body_tail_base64"`     // 首部之后的末尾最多 1024 字节；未超限时与首部拼接为全部已读 body。
	ObservedBytes    int64       `json:"observed_bytes"`       // 实际读取字节数，不代表上游尚未读取的完整响应长度。
	OmittedBytes     int64       `json:"omitted_bytes"`        // 已读但省略的中间字节数，未超 2048 字节时为 0。
	ReadEOF          bool        `json:"read_eof"`             // 底层 Read 是否明确返回 EOF，与协议正常结束分别判断。
	ReadError        string      `json:"read_error,omitempty"` // 有界的读取/传输错误；不包含本地协议解析错误。
}

// StreamResponseCapture 属于一次中转尝试，保留最近四次 SDK 上游响应及单份有界下游正文，不采集请求内容。
type StreamResponseCapture struct {
	ResponseGate *StreamResponseGate   // 当前尝试的实际成功响应门控；非流式旧采集器不设置，保持原行为。
	downstream   *ResponseCapture      // 首次实际写出时分配的下游字节缓冲；受 mu 保护，不在普通 Snapshot 中自动暴露。
	mu           sync.Mutex            // 保护本尝试响应列表、下游缓冲、遗漏计数及终止错误，不跨网络读写持锁。
	attempt      int                   // 中转尝试编号，用于日志详情定位。
	responses    []*streamCapturedBody // 按时间顺序保存的响应读取包装器，最多四个。
	omitted      int                   // 被保留数量上限淘汰的历史响应数。
	err          string                // 明确设置的本次尝试终止错误，优先于最后一次读取错误。
}

// WriteDownstreamPayload 记录实际成功写给下游的 HTTP 字节或 WS 载荷，不采集请求与协议帧头。
// 参数 p：底层 Write 接受的前 n 字节，或成功 WriteMessage 的完整载荷；nil 接收者跳过，调用前网络写入已结束。
func (s *StreamResponseCapture) WriteDownstreamPayload(p []byte) {
	if s == nil || !s.ResponseGate.AllowsStream() || len(p) == 0 {
		return
	}
	s.mu.Lock()
	if s.downstream == nil {
		s.downstream = &ResponseCapture{}
	}
	_, _ = s.downstream.Write(p)
	s.mu.Unlock()
}

// DownstreamBody 复制实际写出正文并编码为单一 base64 字符串；nil 表示采集器不存在或成功响应门控未开放。
// 无参数；空字符串表示零字节，超过 2 KiB 仅拼接首尾各 1 KiB，不插入未写给下游的标记。
func (s *StreamResponseCapture) DownstreamBody() *string {
	if s == nil || !s.ResponseGate.AllowsStream() {
		return nil
	}
	s.mu.Lock()
	if s.downstream == nil {
		s.mu.Unlock()
		empty := ""
		return &empty
	}
	head, tail := s.downstream.Bytes()
	s.mu.Unlock()
	body := base64.StdEncoding.EncodeToString(append(head, tail...))
	return &body
}

// NewStreamResponseCapture 创建属于一次中转尝试的空响应采集器。
// 参数 attempt：日志定位使用的尝试编号；返回独立采集器。门控开放前快照为零值，无响应的旧采集器仍可返回尝试编号。
func NewStreamResponseCapture(attempt int) *StreamResponseCapture {
	return &StreamResponseCapture{attempt: attempt}
}

// streamCapturedBody 在业务正常读取响应时旁路复制有界字节；不额外读空响应体。
type streamCapturedBody struct {
	source    io.ReadCloser          // 实际上游响应体，保留其读取返回值及关闭行为。
	mu        sync.Mutex             // 保护本响应的字节缓冲和读取状态，底层网络 Read 在锁外执行。
	data      ResponseCapture        // 前后各 1 KiB 的响应字节缓冲。
	meta      StreamResponseSnapshot // 状态码、响应头及 EOF/错误等元数据。
	closeOnce sync.Once              // 确保底层 Close 最多执行一次。
	closeErr  error                  // 首次关闭的结果，后续 Close 返回相同结果。
}

// Read 转发底层读取，同时采集成功读到的响应字节并记录 EOF 或读取错误。
// 接收者 b：本响应包装器；参数 p：调用方读取缓冲区，将由底层 Read 填充。
// 原样返回读取字节数和错误；即使同时返回数据和错误，数据部分仍被采集。
func (b *streamCapturedBody) Read(p []byte) (int, error) {
	n, err := b.source.Read(p)
	b.mu.Lock()
	if n > 0 {
		_, _ = b.data.Write(p[:n])
	}
	if errors.Is(err, io.EOF) {
		b.meta.ReadEOF = true
	} else if err != nil {
		b.meta.ReadError = BoundedStreamDiagnosticError(err)
	}
	b.mu.Unlock()
	return n, err
}

// Close 只执行一次底层响应关闭，避免清理与 defer 重复关闭同一资源。
// 接收者 b：响应包装器；无参数；每次均返回首次底层关闭的结果。
func (b *streamCapturedBody) Close() error {
	b.closeOnce.Do(func() { b.closeErr = b.source.Close() })
	return b.closeErr
}

// Observe 为已取得的 HTTP 响应接入有界采集，不主动读取响应内容。
// 接收者 s：本次尝试采集器；参数 resp：上游响应，Body 会被包装；nil 采集器、nil 响应或新流程门控未开放时跳过。
func (s *StreamResponseCapture) Observe(resp *http.Response) {
	s.observe(resp, nil)
}

// observe 登记一次 HTTP 交换并包装响应体；新流程先检查成功响应门控，未收到响应的失败仅保留旧采集器兼容能力。
// 接收者 s：本次尝试采集器；参数 resp：上游响应，可为 nil；err：本次传输错误，可为 nil。
// 无返回值；已包装响应不重复登记，响应头预算 16 KiB，只保留最近四次交换。
func (s *StreamResponseCapture) observe(resp *http.Response, err error) {
	if s == nil || !s.ResponseGate.AllowsStream() || (resp == nil && err == nil) {
		return
	}
	if resp == nil {
		resp = &http.Response{}
	}
	if _, wrapped := resp.Body.(*streamCapturedBody); wrapped {
		return
	}
	b := &streamCapturedBody{source: resp.Body}
	b.meta.ReadError = BoundedStreamDiagnosticError(err)
	b.meta.StatusCode = resp.StatusCode
	b.meta.ResponseHeaders = make(http.Header)
	keys := make([]string, 0, len(resp.Header))
	for k := range resp.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	remaining := 16 << 10
	for _, k := range keys {
		for _, v := range resp.Header[k] {
			size := len(k) + len(v) + 4
			if size > remaining {
				b.meta.HeadersTruncated = true
				continue
			}
			b.meta.ResponseHeaders[k] = append(b.meta.ResponseHeaders[k], v)
			remaining -= size
		}
	}
	if resp.Body != nil {
		resp.Body = b
	}
	s.mu.Lock()
	if len(s.responses) == 4 {
		copy(s.responses, s.responses[1:])
		s.responses = s.responses[:3]
		s.omitted++
	}
	s.responses = append(s.responses, b)
	s.mu.Unlock()
}

// BoundedStreamDiagnosticError 把底层错误转为有界私有文本，防止异常错误串扩大日志负载。
// 参数 err：待记录错误，nil 返回空串；返回错误原文或前后各 4096 字节加省略标记。
func BoundedStreamDiagnosticError(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	if len(text) > 8192 {
		return text[:4096] + "\n...\n" + text[len(text)-4096:]
	}
	return text
}

// SetError 记录本次尝试的明确终止错误，供最终快照优先使用。
// 接收者 s：本次采集器，nil 时跳过；参数 err：底层终止错误，nil 时保留已有值。
func (s *StreamResponseCapture) SetError(err error) {
	if s == nil || !s.ResponseGate.AllowsStream() || err == nil {
		return
	}
	s.mu.Lock()
	s.err = BoundedStreamDiagnosticError(err)
	s.mu.Unlock()
}

// WriteStreamPayload 采集 SDK/WS 上游原始载荷，不重新序列化；参数 p 不包含发送给上游的请求。
// 载荷沿用当前握手/响应的首尾缓冲；门控未开放时跳过，门控已开放但未登记响应元数据时创建零状态码快照。
func (s *StreamResponseCapture) WriteStreamPayload(p []byte) {
	if s == nil || !s.ResponseGate.AllowsStream() || len(p) == 0 {
		return
	}
	s.mu.Lock()
	if len(s.responses) == 0 {
		s.responses = append(s.responses, &streamCapturedBody{})
	}
	b := s.responses[len(s.responses)-1]
	s.mu.Unlock()
	b.mu.Lock()
	_, _ = b.data.Write(p)
	b.mu.Unlock()
}

// Snapshot 复制当前已采集响应与错误，生成与后续读取相互独立的诊断快照。
// 接收者 s：本次采集器；无参数；nil 接收者或新流程门控未开放时返回零值诊断。
// 返回值以最后一次响应为主快照，较早响应置于 PreviousResponses；只做短锁内复制，不为采集继续读取 body。
func (s *StreamResponseCapture) Snapshot() StreamDiagnostic {
	if s == nil || !s.ResponseGate.AllowsStream() {
		return StreamDiagnostic{}
	}
	s.mu.Lock()
	d := StreamDiagnostic{Attempt: s.attempt, Error: s.err, OmittedResponses: s.omitted}
	responses := append([]*streamCapturedBody(nil), s.responses...)
	s.mu.Unlock()
	for i, b := range responses {
		b.mu.Lock()
		part := b.meta
		part.ResponseHeaders = b.meta.ResponseHeaders.Clone()
		part.BodyHead, part.BodyTail = b.data.Bytes()
		part.ObservedBytes = b.data.Observed
		part.OmittedBytes = max(0, b.data.Observed-2048)
		b.mu.Unlock()
		if i == len(responses)-1 {
			d.StreamResponseSnapshot = part
		} else {
			d.PreviousResponses = append(d.PreviousResponses, part)
		}
	}
	if d.Error == "" {
		d.Error = d.ReadError
	}
	return d
}

// StreamDiagnosticHTTPClient 为一次调用包装既有 HTTP 客户端，不修改共享连接池；SDK 解码前即接入响应采集。
type StreamDiagnosticHTTPClient struct {
	// Client 为真实 HTTP 执行器；Do 的参数为待发送请求，返回响应及传输错误。
	Client interface {
		Do(*http.Request) (*http.Response, error)
	}
	Capture *StreamResponseCapture // 本次尝试采集器；nil 时观察逻辑直接跳过。
	Stream  *StreamSession         // 本次流式会话；在采集原始字节之后、SDK/渠道解析之前观察协议。
}

// Do 调用真实 HTTP 执行器，并在调用方/SDK 解析前接入原始响应观察。
// 接收者 c：请求级包装客户端；参数 req：实际发送的上游请求，仅转交，不采集请求内容。
// 原样返回上游响应和传输错误，响应 Body 可能已包装为采集读取器。
func (c StreamDiagnosticHTTPClient) Do(req *http.Request) (*http.Response, error) {
	resp, err := c.Client.Do(req)
	c.Stream.ObserveTransport(resp, err)
	c.Capture.observe(resp, err)
	c.Stream.ObserveHTTP(resp)
	return resp, err
}

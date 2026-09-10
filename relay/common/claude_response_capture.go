package common

import (
	"errors"
	"io"
	"net/http"
	"sort"
	"sync"
)

// ClaudeResponseCaptureKey 在请求上下文中关联当前尝试的响应采集器。
const ClaudeResponseCaptureKey = "claude_response_capture"

// ClaudeDiagnosticAttemptKey 保存公开的尝试编号，供详情接口精确选择同一请求的日志。
const ClaudeDiagnosticAttemptKey = "claude_diagnostic_attempt"

// ClaudeResponseOnlyKey 标记只记录上游响应，避免普通错误日志暴露原始原因。
const ClaudeResponseOnlyKey = "claude_response_only"

// ClaudeResponseSnapshot 保存 HTTP 客户端读取的实体字节及响应头，而非 SDK 解码结果或 TCP/TLS 报文。
type ClaudeResponseSnapshot struct {
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

// ClaudeResponseCapture 属于一次中转尝试，只保留最近四次 SDK HTTP 交换的响应，不采集请求内容。
type ClaudeResponseCapture struct {
	mu        sync.Mutex            // 仅保护本尝试的响应列表、遗漏计数及终止错误，不跨网络读取持锁。
	attempt   int                   // 中转尝试编号，用于日志详情定位。
	responses []*claudeCapturedBody // 按时间顺序保存的响应读取包装器，最多四个。
	omitted   int                   // 被保留数量上限淘汰的历史响应数。
	err       string                // 明确设置的本次尝试终止错误，优先于最后一次读取错误。
}

// NewClaudeResponseCapture 创建属于一次中转尝试的空响应采集器。
// 参数 attempt：日志定位使用的尝试编号；返回独立采集器，尚无 HTTP 响应时快照为空。
func NewClaudeResponseCapture(attempt int) *ClaudeResponseCapture {
	return &ClaudeResponseCapture{attempt: attempt}
}

// claudeCapturedBody 在业务正常读取响应时旁路复制有界字节；不额外读空响应体。
type claudeCapturedBody struct {
	source    io.ReadCloser          // 实际上游响应体，保留其读取返回值及关闭行为。
	mu        sync.Mutex             // 保护本响应的字节缓冲和读取状态，底层网络 Read 在锁外执行。
	data      ResponseCapture        // 前后各 1 KiB 的响应字节缓冲。
	meta      ClaudeResponseSnapshot // 状态码、响应头及 EOF/错误等元数据。
	closeOnce sync.Once              // 确保底层 Close 最多执行一次。
	closeErr  error                  // 首次关闭的结果，后续 Close 返回相同结果。
}

// Read 转发底层读取，同时采集成功读到的响应字节并记录 EOF 或读取错误。
// 接收者 b：本响应包装器；参数 p：调用方读取缓冲区，将由底层 Read 填充。
// 原样返回读取字节数和错误；即使同时返回数据和错误，数据部分仍被采集。
func (b *claudeCapturedBody) Read(p []byte) (int, error) {
	n, err := b.source.Read(p)
	b.mu.Lock()
	if n > 0 {
		_, _ = b.data.Write(p[:n])
	}
	if errors.Is(err, io.EOF) {
		b.meta.ReadEOF = true
	} else if err != nil {
		b.meta.ReadError = BoundedClaudeDiagnosticError(err)
	}
	b.mu.Unlock()
	return n, err
}

// Close 只执行一次底层响应关闭，避免清理与 defer 重复关闭同一资源。
// 接收者 b：响应包装器；无参数；每次均返回首次底层关闭的结果。
func (b *claudeCapturedBody) Close() error {
	b.closeOnce.Do(func() { b.closeErr = b.source.Close() })
	return b.closeErr
}

// Observe 为已取得的 HTTP 响应接入有界采集，不主动读取响应内容。
// 接收者 s：本次尝试采集器；参数 resp：上游响应，Body 会被包装；nil 采集器或 nil 响应时跳过。
func (s *ClaudeResponseCapture) Observe(resp *http.Response) {
	s.observe(resp, nil)
}

// observe 登记一次 HTTP 交换并包装响应体，支持尚未取得响应的传输失败。
// 接收者 s：本次尝试采集器；参数 resp：上游响应，可为 nil；err：本次传输错误，可为 nil。
// 无返回值；已包装响应不重复登记，响应头预算 16 KiB，只保留最近四次交换。
func (s *ClaudeResponseCapture) observe(resp *http.Response, err error) {
	if s == nil || (resp == nil && err == nil) {
		return
	}
	if resp == nil {
		resp = &http.Response{}
	}
	if _, wrapped := resp.Body.(*claudeCapturedBody); wrapped {
		return
	}
	b := &claudeCapturedBody{source: resp.Body}
	b.meta.ReadError = BoundedClaudeDiagnosticError(err)
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

// BoundedClaudeDiagnosticError 把底层错误转为有界私有文本，防止异常错误串扩大日志负载。
// 参数 err：待记录错误，nil 返回空串；返回错误原文或前后各 4096 字节加省略标记。
func BoundedClaudeDiagnosticError(err error) string {
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
func (s *ClaudeResponseCapture) SetError(err error) {
	if s == nil || err == nil {
		return
	}
	s.mu.Lock()
	s.err = BoundedClaudeDiagnosticError(err)
	s.mu.Unlock()
}

// Snapshot 复制当前已采集响应与错误，生成与后续读取相互独立的诊断快照。
// 接收者 s：本次采集器；无参数；nil 接收者返回零值诊断。
// 返回值以最后一次响应为主快照，较早响应置于 PreviousResponses；只做短锁内复制，不为采集继续读取 body。
func (s *ClaudeResponseCapture) Snapshot() ClaudeStreamDiagnostic {
	if s == nil {
		return ClaudeStreamDiagnostic{}
	}
	s.mu.Lock()
	d := ClaudeStreamDiagnostic{Attempt: s.attempt, Error: s.err, OmittedResponses: s.omitted}
	responses := append([]*claudeCapturedBody(nil), s.responses...)
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
			d.ClaudeResponseSnapshot = part
		} else {
			d.PreviousResponses = append(d.PreviousResponses, part)
		}
	}
	if d.Error == "" {
		d.Error = d.ReadError
	}
	return d
}

// ClaudeDiagnosticHTTPClient 为一次调用包装既有 HTTP 客户端，不修改共享连接池；SDK 解码前即接入响应采集。
type ClaudeDiagnosticHTTPClient struct {
	// Client 为真实 HTTP 执行器；Do 的参数为待发送请求，返回响应及传输错误。
	Client interface {
		Do(*http.Request) (*http.Response, error)
	}
	Capture *ClaudeResponseCapture // 本次尝试采集器；nil 时观察逻辑直接跳过。
}

// Do 调用真实 HTTP 执行器，并在调用方/SDK 解析前接入原始响应观察。
// 接收者 c：请求级包装客户端；参数 req：实际发送的上游请求，仅转交，不采集请求内容。
// 原样返回上游响应和传输错误，响应 Body 可能已包装为采集读取器。
func (c ClaudeDiagnosticHTTPClient) Do(req *http.Request) (*http.Response, error) {
	resp, err := c.Client.Do(req)
	c.Capture.observe(resp, err)
	return resp, err
}

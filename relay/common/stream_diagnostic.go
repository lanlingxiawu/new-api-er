package common

import "net/http"

// IsUpstreamStreamFailure 校验由协议读取者明确给出的上游原因；reason 为首个终止原因，不接受泛化 error 状态。
// 返回 true 仅用于诊断资格；不修改补发、计费或客户端取消逻辑。
func IsUpstreamStreamFailure(reason StreamEndReason) bool {
	switch reason {
	case "upstream_incomplete", "upstream_read_error", "upstream_json_error", "upstream_protocol_error", "upstream_error", StreamEndReasonTimeout:
		return true
	default:
		return false
	}
}

// DiagnosticAvailable 判断新流程当前尝试是否有明确上游异常；ownedTimeout 表示服务端管理的请求超时。
// 首因优先：本地异常不被清理 EOF 升级，上游异常不被后续下游关闭抹除；此方法不推断历史日志。
func (s StreamSnapshot) DiagnosticAvailable(ownedTimeout bool) bool {
	if s.Reason != "" {
		return s.UpstreamFailure
	}
	if ownedTimeout && s.UpstreamStarted {
		return true
	}
	// TransportFailed 只在观察交换结果时下游尚未取消才成立；后续客户端关闭不覆盖这个已确认的首因。
	return s.TransportFailed
}

// DiagnosticAvailable 在已有会话锁内查询资格，不为错误日志额外复制整份用量或原始错误帧。
// 参数 ownedTimeout 表示本系统管理的超时；nil/已交给专用处理器的会话不在此生成标记。
func (s *StreamSession) DiagnosticAvailable(ownedTimeout bool) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.disabled && s.ResponseGate.AllowsStream() && s.state.DiagnosticAvailable(ownedTimeout)
}

// ObserveTransport 按真实 HTTP 执行器返回的 resp/err 启用观察，仅 HTTP 200 且无传输错误通过。
// 非 200 或未收到响应沿用原错误、重试、退款，不读取响应 body，也不把映射状态作为成功证据。
func (s *StreamSession) ObserveTransport(resp *http.Response, err error) {
	s.observeTransport(resp, err, http.StatusOK)
}

// ObserveWebSocketHandshake 按实际拨号结果判定 WS 资格；resp/err 必须来自拨号器，只有无错误的 101 通过。
func (s *StreamSession) ObserveWebSocketHandshake(resp *http.Response, err error) {
	s.observeTransport(resp, err, http.StatusSwitchingProtocols)
}

// observeTransport 根据实际响应 resp 和传输错误 err 更新共享门控；successStatus 是该传输类型唯一允许的成功状态码。
// SDK 后续交换重新决定资格，成功重试不继承前次协议终止/用量；不访问 Gin 或跨 I/O 持锁。
func (s *StreamSession) observeTransport(resp *http.Response, err error, successStatus int) {
	if s == nil {
		return
	}
	accepted := err == nil && resp != nil && resp.StatusCode == successStatus
	if s.ResponseGate != nil {
		s.ResponseGate.accepted.Store(accepted)
	}
	if !accepted {
		return
	}
	if !s.Active() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.UpstreamStarted {
		// SDK 重试是新的实际响应，旧响应证据只由有界采集器保留，不参加本次流状态/收费。
		s.state = StreamSnapshot{Evidence: map[string]int{}}
		s.text.Reset()
		s.tools = map[string]*streamTool{}
		s.toolBytes = 0
		s.toolDeliveries = nil // 新成功响应重新计量，旧工具身份不跨 SDK 重试。
		s.choices = map[int]bool{}
		s.claudeStarted = false
		s.claudeStop = false
		s.claudeBlocks = map[int]bool{}
	}
	s.state.UpstreamStarted = true
	s.state.TransportFailed = false
}

// FailRelay 记录旧控制器/处理器收口错误；reason 保留原计费标签，err 保存底层原因。
// 未分类错误只有已有传输失败证据才视作上游异常；缺少结束的收口还要求确实进入上游处理，防止本地失败误标。
func (s *StreamSession) FailRelay(reason StreamEndReason, err error) {
	if !s.Active() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Reason != "" {
		return
	}
	s.failLocked(reason, err)
	if s.state.Reason != "" {
		s.state.UpstreamFailure = s.state.TransportFailed || (reason == "upstream_incomplete" && (s.state.UpstreamStarted || s.state.Accepted))
	}
}

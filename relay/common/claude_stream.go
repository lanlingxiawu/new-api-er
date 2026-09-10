package common

// ClaudeStreamHandledKey 标记严格流式流程已接管终止响应及结算，供控制器跳过重复重试、退款和错误写出。
const ClaudeStreamHandledKey = "claude_stream_handled"

// ResponseCapture 保存响应实体的前 1 KiB 与滚动末尾 1 KiB，超过 2 KiB 时省略中间内容。
// 本结构不自行加锁；由单个读取者持有，或由外层采集器加锁后访问。
type ResponseCapture struct {
	head               [1024]byte // 首部字节缓冲，填满后保持不变。
	tail               [1024]byte // 首部之后的尾部环形缓冲，持续保留最近收到的字节。
	headN, tailN, next int        // headN/tailN 为有效字节数；next 为尾部缓冲下一次写入位置。
	Observed           int64      // 实际经采集器读取的字节总数，包含最终被省略的部分。
}

// Write 保留传入数据的首部和滚动尾部，同时累计观察字节数。
// 接收者 s：请求局部字节缓冲；参数 p：本次实际读到的响应字节，仅复制，不保留调用方切片引用。
// 返回 len(p) 和 nil，表示采集消费了整段输入，即使中间字节因上限被省略。
func (s *ResponseCapture) Write(p []byte) (int, error) {
	n := len(p)
	s.Observed += int64(n)
	if s.headN < len(s.head) {
		k := copy(s.head[s.headN:], p)
		s.headN += k
		p = p[k:]
	}
	if len(p) >= len(s.tail) {
		copy(s.tail[:], p[len(p)-len(s.tail):])
		s.tailN = len(s.tail)
		s.next = 0
		return n, nil
	}
	for len(p) > 0 {
		k := copy(s.tail[s.next:], p)
		s.next = (s.next + k) % len(s.tail)
		s.tailN = min(len(s.tail), s.tailN+k)
		p = p[k:]
	}
	return n, nil
}

// Bytes 复制首尾缓冲，并把尾部环形数据还原为原始字节顺序。
// 接收者 s：字节采集缓冲；无参数；依次返回独立的首部、尾部切片，不暴露内部数组。
func (s *ResponseCapture) Bytes() ([]byte, []byte) {
	head := make([]byte, s.headN)
	copy(head, s.head[:s.headN])
	tail := make([]byte, s.tailN)
	if s.tailN < len(s.tail) {
		copy(tail, s.tail[:s.tailN])
	} else {
		k := copy(tail, s.tail[s.next:])
		copy(tail[k:], s.tail[:s.next])
	}
	return head, tail
}

// ClaudeStreamDiagnostic 汇总一次中转尝试的原始响应、底层错误与用量证据，仅经超级管理员接口读取。
type ClaudeStreamDiagnostic struct {
	ClaudeResponseSnapshot                          // 本次尝试最后一次 HTTP 交换的响应快照。
	Attempt                int                      `json:"attempt"`                      // 中转尝试编号；历史日志未记录时为 0。
	PreviousResponses      []ClaudeResponseSnapshot `json:"previous_responses,omitempty"` // 同一尝试内保留的较早 SDK 响应，按出现顺序排列。
	OmittedResponses       int                      `json:"omitted_responses,omitempty"`  // 超过四份保留上限后被丢弃的响应数量。
	Error                  string                   `json:"error,omitempty"`              // 有界的底层终止原因，不作为普通日志公开错误信息。
	UsageEvidence          map[string]int           `json:"usage_evidence"`               // 上游已确认的累计用量；键缺失与显式 0 不同，token 与工具次数按字段区分。
	UsagePhases            map[string]string        `json:"usage_phases,omitempty"`       // 各用量字段最近出现的事件阶段，如 message_start 或 message_delta。
	EstimatedUsage         map[string]int           `json:"estimated_usage,omitempty"`    // 本地估算的输入/输出 token，独立于原始上游用量证据。
	RejectReason           string                   `json:"reject_reason,omitempty"`      // 上游策略停止原因，只随私有诊断暴露。
	UsageFinal             bool                     `json:"usage_final"`                  // 输出报告之后没有新内容块，且通过 message_stop 完整性校验。
	SettlementError        string                   `json:"settlement_error,omitempty"`   // 资金或令牌结算错误，与上游流式错误分别记录。
}

// ClaudeStreamOutcome 由响应处理阶段生成，再由终止结算阶段更新；入队前序列化，异步日志工作者不再修改它。
type ClaudeStreamOutcome struct {
	Failed              bool                   `json:"failed"`            // 本次流式响应是否异常结束；正常 message_stop 为 false。
	ClientGone          bool                   `json:"client_gone"`       // 下游取消或写出失败；受管超时另按上游异常处理。
	EffectiveContent    bool                   `json:"effective_content"` // 已成功完整写出并刷新非空文本/思考或完整工具调用；不等同于客户端已消费。
	ConfirmedUsage      bool                   `json:"confirmed_usage"`   // 是否存在上游确认字段，显式 0 也属于已有证据。
	UsageSource         string                 `json:"usage_source"`      // upstream/estimated/mixed/none：上游、估算、混合或不计费。
	SettlementState     string                 `json:"settlement_state"`  // pending/settled/released/failed/partial：待结算、完成、释放、失败、部分提交。
	IntendedQuota       int                    `json:"intended_quota"`    // 本次策略期望收取的内部额度，0 表示释放预扣。
	ReservedQuota       int                    `json:"reserved_quota"`    // 结算前实际预扣的内部额度，用于核对差额。
	SettlementAttempted bool                   `json:"-"`                 // 单次结算保护标记，失败后也保持 true，避免重复资金运算。
	Diagnostic          ClaudeStreamDiagnostic `json:"-"`                 // 私有诊断，仅由日志封装逻辑单独保存。
}

// SelectUsageSource 按终止原因、已确认用量与有效交付决定计费来源。
// 接收者 s：当前流式结果；无参数；返回 upstream、estimated 或 none，正常部分估算的 mixed 由后续逻辑标记。
// 上游异常且无有效交付优先不收费；用户断开只看上游证据；上游异常已有有效交付但无证据时允许估算。
func (s *ClaudeStreamOutcome) SelectUsageSource() string {
	if s.Failed && !s.ClientGone && !s.EffectiveContent {
		return "none"
	}
	if s.ConfirmedUsage {
		return "upstream"
	}
	if s.ClientGone {
		return "none"
	}
	if !s.Failed || s.EffectiveContent {
		return "estimated"
	}
	return "none"
}

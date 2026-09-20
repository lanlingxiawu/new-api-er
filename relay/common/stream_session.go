package common

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/constant"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/tidwall/gjson"
)

// MaxStreamFrameBytes 统一限制受管事件、完整 JSON 和工具参数为 200 MiB；缓冲按需增长。
const MaxStreamFrameBytes = 200 << 20

// StreamSessionKey 把尝试会话提供给没有 RelayInfo 参数的旧渠道读取函数。
const StreamSessionKey = "unified_stream_session"

// StreamSnapshot 是一次尝试的脱离锁快照；仅由结算所有者转换为日志及最终费用。
type StreamSnapshot struct {
	UsageFormat         types.RelayFormat // 从实际上游事件识别的用量口径，透传不依赖是否执行请求转换。
	Complete            bool              // 已收到协议明确的正常结束，不把传输 EOF 本身当作 SSE 完成。
	Reason              StreamEndReason   // 首个已记录终止原因，可来自上游或本地处理；客户端写入失败另记录。
	Err                 error             // 底层错误，只允许进入私有诊断。
	Evidence            map[string]int    // 统一字段的上游确认累计用量，保留显式零。
	Effective           bool              // 下游成功写出并刷新了有效内容。
	ClientErr           error             // 下游写入/刷新错误，区别于上游错误。
	ErrorFrame          []byte            // 已收到的原始上游错误 SSE 帧，供同协议直接输出。
	ErrorPayload        []byte            // SDK/WS 原始错误载荷，保留原字节；取得完整原 SSE 帧后释放，不作为日志字段。
	ErrorDelivered      bool              // 错误已成功发给下游，避免本地重复补发。
	HTTPObserved        bool              // 已接入 HTTP 响应观察，SDK 可另外送入解码事件。
	Accepted            bool              // 上游已返回成功 HTTP 状态或有效 SDK/WS 事件。
	EstimatedOutput     int               // 成功交付文本的累计增量估算，不对每个分片独立取整。
	ReceivedResponse    bool              // 已解析上游业务响应，不要求下游写入成功。
	ReceivedOutput      int               // 接收侧文本估算，仅用户断开且无确认用量时采用。
	ReceivedAudioOutput int               // Realtime 已接收音频的专用估算，不按压缩字节计量。
	MediaBytes          int64             // 成功交付的裸媒体字节，仅作为证据，不直接换算成 token。
	UpstreamFailure     bool              // 首个终止原因确认为上游异常；与兼容计费原因字符串分开记录。
	UpstreamStarted     bool              // 已通过真实成功响应门控或绑定响应/连接，用于区分响应前超时和成功响应后的超时。
	TransportFailed     bool              // 历史传输失败证据兼容槽；当前非成功交换不激活新流程，成功交换清零。
}

// StreamSession 保存单次中转尝试的协议和交付状态；短锁不跨网络读取、写出或刷新。
type StreamSession struct {
	ResponseGate       *StreamResponseGate          // 本次实际响应门控，初始化后指针不变；未成功时观察器透明转交。
	OpaqueSSE          bool                         // 仅旧智谱 add/finish 文本事件使用，不把正文强当 JSON。
	ChannelType        int                          // 本次选中的上游渠道，用于原生响应及用量别名识别，初始化后不变。
	RelayMode          int                          // 本次请求模式，配合渠道限定原生语音校验，初始化后不变。
	ExpectedImages     int                          // 图片入口/实际出站期望张数，至少 1；0 为未声明图片约束的其他会话。
	mu                 sync.Mutex                   // 保护会话状态、证据及内容块缓存，不持锁执行网络 I/O。
	format             types.RelayFormat            // 下游协议，错误输出按此格式选择。
	state              StreamSnapshot               // 当前可变状态，外部读取须经 Snapshot 复制。
	disabled           bool                         // 原生 Claude 专用处理器已接管时禁用通用观察。
	text               strings.Builder              // 仅保存一个写出批次的文本，调用者及时取走估算。
	media              int                          // 本批已交付/已接收的内联媒体分片数，按张折算，取走即清零。
	tools              map[string]*streamTool       // 有界的未完成工具调用，完整参数交付后才计入有效内容。
	toolBytes          int                          // 所有未完成工具名称和参数共享单帧预算，不为每个工具单独分配上限。
	toolDeliveries     *streamToolDeliveries        // 懒分配的最近已交付工具身份，防止两类完成事件重复估算。
	choices            map[int]bool                 // 当前上游协议按索引累计的候选结束状态，最多 128 项。
	ExpectedChoices    int                          // 实际出站候选约束；透传沿用入口解析，转换后由最终 JSON 覆盖，0 表示按已出现候选判断。
	claudeStarted      bool                         // Claude 转换路径是否看到了 message_start。
	claudeStop         bool                         // Claude 转换路径是否确认 stop_reason。
	claudeBlocks       map[int]bool                 // 尚未关闭的 Claude 内容块。
	closeBody          io.Closer                    // 当前上游资源，终止时由所有者关闭以停止生成。
	requestContext     context.Context              // 请求取消后不再采用排队事件的用量，不保存 Gin 上下文。
	received           *StreamSession               // 独立接收内容跟踪器，复用有界工具去重，不参与协议与交付状态。
	receivedFactory    func() func(string, int) int // 新实际响应/轮次创建独立增量估算器；参数为文本与媒体张数。
	receivedEstimate   func(string, int) int
	receivedOnly       bool // 内容跟踪器专用：按接收片段计工具文本，不等待完整交付。
	receivedTranscript bool // 接收侧已计入转录 delta，完成快照不再重复估算。
}

// streamTool 缓存一个待完成工具调用的名称与参数；所有字段仅在会话锁内操作。
type streamTool struct {
	name strings.Builder // 独立拥有字节的工具名，支持旧版名称分片且不保留整帧 JSON；空名称不计为有效工具内容。
	args strings.Builder // 分片累积的 JSON 参数，完整且合法后才确认交付。
}

// NewStreamSession 创建请求尝试局部会话；参数 format 决定下游协议，ChannelType/RelayMode 随后由入口初始化。
func NewStreamSession(format types.RelayFormat) *StreamSession {
	return &StreamSession{format: format, state: StreamSnapshot{Evidence: map[string]int{}}, tools: map[string]*streamTool{}, choices: map[int]bool{}, claudeBlocks: map[int]bool{}}
}

// BindContext 绑定请求 ctx 的取消信号，避免取消后继续采用上游排队用量；接收者需非 nil，无返回值。
func (s *StreamSession) BindContext(ctx context.Context) {
	s.mu.Lock()
	s.requestContext = ctx
	s.mu.Unlock()
}

// Disable 将终止权交给已有的专用协议处理器；已安装的透明包装随后保持原字节和错误行为。
func (s *StreamSession) Disable() {
	if s != nil {
		s.mu.Lock()
		s.disabled = true
		s.mu.Unlock()
	}
}

// Active 判断通用会话是否仍拥有本次尝试；nil 会话保持非流式和旧处理器行为。
func (s *StreamSession) Active() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.disabled && s.ResponseGate.AllowsStream()
}

// Snapshot 复制证据和帧，避免异步日志与读取工作者共享可变映射。
func (s *StreamSession) Snapshot() StreamSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.state
	v.Evidence = make(map[string]int, len(s.state.Evidence))
	for k, n := range s.state.Evidence {
		v.Evidence[k] = n
	}
	v.ErrorFrame = bytes.Clone(s.state.ErrorFrame)
	v.ErrorPayload = bytes.Clone(s.state.ErrorPayload)
	return v
}

// failLocked 记录首个终止原因 reason 与底层 err；调用者必须持有锁，已取消或已有客户端/终止错误时不覆盖。
func (s *StreamSession) failLocked(reason StreamEndReason, err error) {
	if s.state.Reason != "" || s.state.ClientErr != nil || (s.requestContext != nil && s.requestContext.Err() != nil) {
		return
	}
	s.state.Reason = reason
	s.state.UpstreamFailure = IsUpstreamStreamFailure(reason)
	s.state.Err = err
	s.state.Complete = false
}

// Fail 记录终止原因 reason 与底层 err；原因是否属上游由白名单判定，不直接写出响应或执行收费。
func (s *StreamSession) Fail(reason StreamEndReason, err error) {
	if !s.Active() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failLocked(reason, err)
}

// ClientFailed 保存首个下游写入/刷新错误 err 并关闭上游；nil 错误或非活动会话跳过，不覆盖已有上游原因。
func (s *StreamSession) ClientFailed(err error) {
	if !s.Active() || err == nil {
		return
	}
	s.mu.Lock()
	if s.state.ClientErr == nil {
		s.state.ClientErr = err
	}
	s.mu.Unlock()
	s.CloseUpstream()
}

// EndRead 处理读取结果 err：未完成时 EOF 记为不完整，其他错误记为读取异常；Complete 已为 true 时忽略所有后续读取错误。
func (s *StreamSession) EndRead(err error) {
	if !s.Active() || err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Complete {
		return
	}
	if errors.Is(err, io.EOF) {
		s.failLocked("upstream_incomplete", errors.New("upstream ended before protocol completion"))
	} else {
		s.failLocked("upstream_read_error", err)
	}
}

// Complete 标记专用解析器确认的结束；无参数和返回值，已有终止原因时不覆盖，调用方负责确认实际协议完成。
func (s *StreamSession) Complete() {
	if !s.Active() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Reason == "" {
		s.state.Complete = true
		s.state.Accepted = true
	}
}

// CloseUpstream 停止上游生成；锁外关闭，不新增诊断读取或 drain。
func (s *StreamSession) CloseUpstream() {
	if s == nil {
		return
	}
	s.mu.Lock()
	body := s.closeBody
	s.closeBody = nil
	s.mu.Unlock()
	if body != nil {
		_ = body.Close()
	}
}

// ObserveEvent 观察解析/改写前的单个 JSON/SDK 事件；event 是 SSE 事件名，data 为原始 data 字节。
// 返回协议错误供独立读取器停止；原 error 在用量校验前独立保留，非法用量不覆盖先前证据，亦不替换原错误。
func (s *StreamSession) ObserveEvent(event string, data []byte) error {
	if !s.Active() {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.requestContext != nil && s.requestContext.Err() != nil {
		return s.requestContext.Err()
	}
	if s.state.Reason != "" {
		return s.state.Err // 首个终止事件之后不再采用排队帧的状态或用量。
	}
	if len(data) > MaxStreamFrameBytes {
		s.failLocked("upstream_protocol_error", errors.New("stream event exceeds limit"))
		return s.state.Err
	}
	rawData := data // SDK/WS 终止输出须保留原始空白；仅解析视图裁去两端空白。
	data = bytes.TrimSpace(data)
	if event == "error" && !gjson.ValidBytes(data) {
		s.state.ErrorPayload = bytes.Clone(rawData)
		s.failLocked("upstream_error", fmt.Errorf("upstream error: %s", BoundedStreamDiagnosticError(errors.New(string(data)))))
		return nil
	}
	if bytes.Equal(data, []byte("[DONE]")) {
		// 通用结束标记不替代已请求的完成图片；预览图不计入 image_count。
		if s.ExpectedImages > 0 && s.state.Evidence["image_count"] < s.ExpectedImages {
			s.failLocked("upstream_protocol_error", errors.New("DONE does not replace missing completed images"))
			return s.state.Err
		}
		if (s.state.UsageFormat == types.RelayFormatClaude || s.state.UsageFormat == types.RelayFormatGemini || len(s.choices) > 0 || s.ExpectedChoices > 0) && !s.state.Complete {
			s.failLocked("upstream_protocol_error", errors.New("DONE does not replace the upstream protocol terminator"))
			return s.state.Err
		}
		if s.state.Reason == "" {
			s.state.Complete = true
		}
		return nil
	}
	if len(data) == 0 {
		return nil
	}
	if !gjson.ValidBytes(data) {
		s.failLocked("upstream_json_error", errors.New("invalid upstream event JSON"))
		return s.state.Err
	}
	v := gjson.ParseBytes(data)
	if !v.IsObject() {
		s.failLocked("upstream_protocol_error", errors.New("upstream event must be an object"))
		return s.state.Err
	}
	s.state.Accepted = true
	kind := v.Get("type").String()
	if kind == "" {
		kind = v.Get("event_type").String()
	}
	if kind == "" {
		kind = v.Get("event").String()
	}
	if kind == "" {
		kind = event
	}
	if s.format == types.RelayFormatOpenAIRealtime && IsRealtimeRecoverableError(v) {
		return nil // 可恢复请求错误不改变轮次证据或首个终止原因。
	}
	realtimeRoundComplete := s.format == types.RelayFormatOpenAIRealtime && isRealtimeRoundCompletion(v)
	responsesLimitComplete := IsResponsesLimitCompletion(v) // 限制终态不是服务故障，仍交给原转换器收尾。
	if kind == "message_start" || kind == "message_delta" || kind == "message_stop" || kind == "message" && v.Get("content").IsArray() {
		s.state.UsageFormat = types.RelayFormatClaude
	}
	if v.Get("usageMetadata").Exists() || v.Get("usage_metadata").Exists() {
		s.state.UsageFormat = types.RelayFormatGemini
	}
	upstreamError := (!realtimeRoundComplete && IsStreamErrorEvent(event, data)) || v.Get("Error.Code").Int() != 0 || v.Get("error_code").Int() != 0 || v.Get("header.code").Int() != 0 || v.Get("base_resp.status_code").Int() != 0
	if upstreamError {
		// 错误正文与用量可信度分开：即使后续 usage 校验提前返回，仍有原始 SDK/WS 错误可发送。
		s.state.ErrorPayload = bytes.Clone(rawData)
	}
	// 一批用量全部校验通过后才更新；累计覆盖，不把重复报告相加。
	updates := map[string]int{}
	sources := []struct {
		path string
		keys []streamUsageField
	}{
		{"usage", streamUsageKeys}, {"response.usage", streamUsageKeys}, {"message.usage", streamUsageKeys},
		{"payload.usage.text", streamUsageKeys}, {"Usage", []streamUsageField{{"PromptTokens", "input_tokens"}, {"CompletionTokens", "output_tokens"}}},
		{"usageMetadata", geminiStreamUsageKeys}, {"usage_metadata", geminiStreamUsageKeys},
		{"response.meta.billed_units", streamUsageKeys}, {"meta.billed_units", streamUsageKeys}, {"usage.billed_units", streamUsageKeys},
		{"x_amzn_bedrock_invocationMetrics", []streamUsageField{{"inputTokenCount", "input_tokens"}, {"outputTokenCount", "output_tokens"}}},
		{"metadata.usage", streamUsageKeys},
	}
	for _, source := range sources {
		// Dify 适配器仅从 message_end 取请求总用量；节点事件和其他渠道的 metadata 不作为该证据。
		// 放在通用来源之后保证此权威位置覆盖别名，沿用整批校验并保留显式零。
		if source.path == "metadata.usage" && (s.ChannelType != constant.ChannelTypeDify || kind != "message_end") {
			continue
		}
		u := v.Get(source.path)
		if !u.Exists() || u.Type == gjson.Null {
			continue
		}
		if !u.IsObject() {
			s.failLocked("upstream_json_error", errors.New("usage must be an object"))
			return s.state.Err
		}
		// OpenAI Chat 以 prompt/completion 为准，Responses/Claude 以 input/output 为准；顺序固定，禁止 map 迭代随机覆盖显式零。
		preferInput := strings.HasPrefix(kind, "response.") || strings.HasPrefix(kind, "image_") || strings.HasPrefix(kind, "speech.") || strings.HasPrefix(kind, "transcript.") || s.state.UsageFormat == types.RelayFormatClaude || source.path == "response.usage" || source.path == "message.usage"
		sourceUpdates := map[string]int{}
		for _, field := range source.keys {
			n := u.Get(field.path)
			if !n.Exists() {
				continue
			}
			if n.Type != gjson.Number || n.Float() != float64(n.Int()) || n.Int() < 0 || n.Int() > 1_000_000_000 {
				s.failLocked("upstream_json_error", fmt.Errorf("invalid usage field %s", field.path))
				return s.state.Err
			}
			if preferInput {
				preferred := field.path
				switch {
				case field.path == "prompt_tokens":
					preferred = "input_tokens"
				case field.path == "completion_tokens":
					preferred = "output_tokens"
				case strings.HasPrefix(field.path, "prompt_tokens_details."):
					preferred = strings.Replace(field.path, "prompt_tokens_details.", "input_tokens_details.", 1)
				case strings.HasPrefix(field.path, "completion_tokens_details."):
					preferred = strings.Replace(field.path, "completion_tokens_details.", "output_tokens_details.", 1)
				}
				if preferred != field.path && u.Get(preferred).Exists() {
					continue
				}
			}
			if _, exists := sourceUpdates[field.canonical]; !exists {
				sourceUpdates[field.canonical] = int(n.Int())
			}
		}
		for key, n := range sourceUpdates {
			updates[key] = n
		}
	}
	// Baidu/xAI 原适配器按顶层 Chat 总量减输入归一化输出；只用同帧完整组合，不跨帧补缺失字段。
	// 与其他证据共享批次校验，保留显式零；Responses、图片及其他渠道不借用此总量语义。
	if (s.ChannelType == constant.ChannelTypeBaidu || s.ChannelType == constant.ChannelTypeXai) && s.ExpectedImages == 0 && !strings.HasPrefix(kind, "response.") {
		total, prompt := v.Get("usage.total_tokens"), v.Get("usage.prompt_tokens")
		if total.Exists() {
			if total.Type != gjson.Number || total.Float() != float64(total.Int()) || total.Int() < 0 || total.Int() > 1_000_000_000 || prompt.Exists() && total.Int() < prompt.Int() {
				s.failLocked("upstream_json_error", errors.New("invalid usage field total_tokens"))
				return s.state.Err
			}
			if prompt.Exists() {
				updates["input_tokens"] = int(prompt.Int())
				updates["output_tokens"] = int(total.Int() - prompt.Int())
				updates["total_tokens"] = int(total.Int())
			}
		}
	}
	for key, dst := range map[string]string{"prompt_eval_count": "input_tokens", "eval_count": "output_tokens"} {
		n := v.Get(key)
		if n.Exists() {
			if n.Type != gjson.Number || n.Float() != float64(n.Int()) || n.Int() < 0 || n.Int() > 1_000_000_000 {
				s.failLocked("upstream_json_error", fmt.Errorf("invalid usage field %s", key))
				return s.state.Err
			}
			updates[dst] = int(n.Int())
		}
	}
	// 标准缓存证据优先（包括显式零）；仅补已有渠道适配器支持的原始别名。
	// 与上面字段共享临时映射，任一别名校验失败时整帧不提交，避免把半帧数据用于收费。
	cachePaths := []string(nil)
	switch s.ChannelType {
	case constant.ChannelTypeDeepSeek:
		cachePaths = []string{"usage.prompt_cache_hit_tokens"}
	case constant.ChannelTypeMoonshot, constant.ChannelTypeZhipu_v4:
		cachePaths = []string{"usage.cached_tokens", "usage.prompt_cache_hit_tokens"}
	case constant.ChannelTypeOpenAI:
		cachePaths = []string{"timings.cache_n"}
	}
	if s.ChannelType == constant.ChannelTypeMoonshot {
		// choices 中是同一请求累计缓存量，取首个报告而非多候选求和。
		for _, choice := range v.Get("choices").Array() {
			n := choice.Get("usage.cached_tokens")
			if !n.Exists() {
				continue
			}
			if n.Type != gjson.Number || n.Float() != float64(n.Int()) || n.Int() < 0 || n.Int() > 1_000_000_000 {
				s.failLocked("upstream_json_error", errors.New("invalid usage field choices.usage.cached_tokens"))
				return s.state.Err
			}
			if _, exists := updates["cached_tokens"]; !exists {
				updates["cached_tokens"] = int(n.Int())
			}
		}
	}
	for _, path := range cachePaths {
		n := v.Get(path)
		if !n.Exists() {
			continue
		}
		if n.Type != gjson.Number || n.Float() != float64(n.Int()) || n.Int() < 0 || n.Int() > 1_000_000_000 {
			s.failLocked("upstream_json_error", fmt.Errorf("invalid usage field %s", path))
			return s.state.Err
		}
		if _, exists := updates["cached_tokens"]; !exists {
			updates["cached_tokens"] = int(n.Int())
		}
	}
	for key, n := range updates {
		s.state.Evidence[key] = n
	}
	if upstreamError {
		s.failLocked("upstream_error", errors.New(BoundedStreamDiagnosticError(errors.New(string(data)))))
		// SDK 没有 SSE 外壳时保留原始 JSON 载荷；HTTP 观察器稍后用真实原帧覆盖。
		return nil
	}
	// Gemini 明确拦截时不产生候选；保留渠道原有反馈和计量，不把合法终态当成 EOF 截断。
	// 若之前已经开始输出候选，反馈不替代那些候选缺失的结束帧。
	if len(s.choices) == 0 && isGeminiPromptBlocked(v) {
		s.state.UsageFormat = types.RelayFormatGemini
		s.state.Complete = true
	}
	switch kind {
	case "message_start":
		if s.claudeStarted || !v.Get("message").IsObject() {
			s.failLocked("upstream_protocol_error", errors.New("invalid message_start"))
			return s.state.Err
		}
		s.claudeStarted = true
	case "content_block_start":
		idx := int(v.Get("index").Int())
		if !s.claudeStarted || len(s.claudeBlocks) > 0 || !v.Get("content_block").IsObject() || v.Get("index").Type != gjson.Number || idx < 0 {
			s.failLocked("upstream_protocol_error", errors.New("invalid content block sequence"))
			return s.state.Err
		}
		s.claudeBlocks[idx] = true
	case "content_block_delta":
		if !s.claudeStarted || v.Get("index").Type != gjson.Number || !s.claudeBlocks[int(v.Get("index").Int())] || !v.Get("delta").IsObject() {
			s.failLocked("upstream_protocol_error", errors.New("content delta outside an open block"))
			return s.state.Err
		}
	case "content_block_stop":
		if !s.claudeBlocks[int(v.Get("index").Int())] || v.Get("index").Type != gjson.Number {
			s.failLocked("upstream_protocol_error", errors.New("stop for unopened content block"))
			return s.state.Err
		}
		delete(s.claudeBlocks, int(v.Get("index").Int()))
	case "message_delta":
		if !s.claudeStarted || len(s.claudeBlocks) > 0 || !v.Get("delta").IsObject() {
			s.failLocked("upstream_protocol_error", errors.New("message delta before blocks completed"))
			return s.state.Err
		}
		s.claudeStop = s.claudeStop || v.Get("delta.stop_reason").String() != ""
	case "message_stop":
		if !s.claudeStarted || !s.claudeStop || len(s.claudeBlocks) > 0 {
			s.failLocked("upstream_protocol_error", errors.New("incomplete Claude message_stop"))
			return s.state.Err
		}
		s.state.Complete = true
	case "response.incomplete":
		if responsesLimitComplete {
			s.state.Complete = true
		}
	case "response.completed", "response.done":
		status := v.Get("response.status").String()
		if !realtimeRoundComplete && !responsesLimitComplete && status != "" && status != "completed" {
			s.failLocked("upstream_error", fmt.Errorf("response ended with status %s", status))
		} else {
			s.state.Complete = true
		}
	case "image_generation.completed", "image_edit.completed":
		s.state.Evidence["image_count"]++
		s.state.Complete = s.state.Evidence["image_count"] >= max(1, s.ExpectedImages)
	case "stream-end", "message_end", "chat_end", "conversation.chat.completed", "done", "speech.audio.done", "transcript.text.done", "finish":
		s.state.Complete = true
	}
	if v.Get("done").Bool() || v.Get("is_end").Bool() || v.Get("is_finished").Bool() || v.Get("payload.choices.status").Int() == 2 {
		s.state.Complete = true
	}
	if cs := v.Get("Choices"); cs.IsArray() {
		s.observeChoicesLocked(cs.Array(), "Index", "FinishReason")
	}
	if kind == "message" && v.Get("stop_reason").String() != "" {
		s.state.Complete = true
	}
	if candidates := v.Get("candidates"); candidates.IsArray() {
		s.observeChoicesLocked(candidates.Array(), "index", "finishReason")
	}
	if choices := v.Get("choices"); choices.IsArray() {
		s.observeChoicesLocked(choices.Array(), "index", "finish_reason")
	}
	if s.state.Reason != "" {
		s.state.Complete = false
		return s.state.Err
	}
	s.observeReceivedLocked(event, data, v)
	return nil
}

// isGeminiPromptBlocked 判断原始对象 v 是否为无候选的明确策略反馈；空值、默认枚举及损坏结构不算终态。
// 仅识别 DTO 已支持的响应字段；不读取内容正文，不把已有 error 或非法 usage 的校验责任移到这里。
func isGeminiPromptBlocked(v gjson.Result) bool {
	reason := v.Get("promptFeedback.blockReason")
	name := strings.TrimSpace(reason.String())
	if reason.Type != gjson.String || name == "" || name == "BLOCK_REASON_UNSPECIFIED" {
		return false
	}
	candidates := v.Get("candidates")
	return !candidates.Exists() || candidates.Type == gjson.Null || (candidates.IsArray() && len(candidates.Array()) == 0)
}

// observeChoicesLocked 按 indexKey 合并候选并重新计算整体完成；finishKey 指定该协议的结束字段。
// 调用者持锁；空 usage 帧不撤销完成，但新出现的未结束候选会撤销临时完成。
func (s *StreamSession) observeChoicesLocked(choices []gjson.Result, indexKey, finishKey string) {
	for i, choice := range choices {
		idx := i
		if value := choice.Get(indexKey); value.Exists() {
			if value.Type != gjson.Number || value.Float() != float64(value.Int()) || value.Int() < 0 || value.Int() >= 128 {
				s.failLocked("upstream_protocol_error", errors.New("invalid stream choice index"))
				return
			}
			idx = int(value.Int())
		}
		if _, exists := s.choices[idx]; !exists && len(s.choices) >= 128 {
			s.failLocked("upstream_protocol_error", errors.New("too many stream choices"))
			return
		}
		s.choices[idx] = s.choices[idx] || choice.Get(finishKey).String() != ""
	}
	if len(s.choices) == 0 {
		return
	}
	all := len(s.choices) >= max(1, s.ExpectedChoices)
	for _, done := range s.choices {
		all = all && done
	}
	s.state.Complete = all && s.state.Reason == ""
}

// streamUsageField 按固定次序指定原始字段和统一证据名，存在多个别名时保留协议优先字段。
type streamUsageField struct {
	path      string // 上游 usage 内的字段路径。
	canonical string // 私有确认用量及收费构造使用的名称。
}

// streamUsageKeys 将各供应商确认字段归一化，保留显式零以及缓存、音频、图片和思考细分。
var streamUsageKeys = []streamUsageField{
	{"prompt_tokens", "input_tokens"}, {"input_tokens", "input_tokens"}, {"input_count", "input_tokens"}, {"inputTokens", "input_tokens"},
	{"completion_tokens", "output_tokens"}, {"output_tokens", "output_tokens"}, {"output_count", "output_tokens"}, {"outputTokens", "output_tokens"},
	{"cache_read_input_tokens", "cache_read_input_tokens"}, {"cache_creation_input_tokens", "cache_creation_input_tokens"},
	{"cache_creation.ephemeral_5m_input_tokens", "cache_creation.ephemeral_5m_input_tokens"}, {"cache_creation.ephemeral_1h_input_tokens", "cache_creation.ephemeral_1h_input_tokens"},
	{"prompt_tokens_details.cached_tokens", "cached_tokens"}, {"input_tokens_details.cached_tokens", "cached_tokens"}, {"input_token_details.cached_tokens", "cached_tokens"},
	{"prompt_tokens_details.cache_write_tokens", "cache_write_tokens"}, {"input_tokens_details.cache_write_tokens", "cache_write_tokens"},
	{"prompt_tokens_details.cached_creation_tokens", "cached_creation_tokens"}, {"input_tokens_details.cached_creation_tokens", "cached_creation_tokens"},
	{"prompt_tokens_details.audio_tokens", "input_audio_tokens"}, {"input_tokens_details.audio_tokens", "input_audio_tokens"}, {"input_token_details.audio_tokens", "input_audio_tokens"},
	{"completion_tokens_details.audio_tokens", "output_audio_tokens"}, {"output_tokens_details.audio_tokens", "output_audio_tokens"}, {"output_token_details.audio_tokens", "output_audio_tokens"},
	{"prompt_tokens_details.text_tokens", "input_text_tokens"}, {"input_tokens_details.text_tokens", "input_text_tokens"}, {"input_token_details.text_tokens", "input_text_tokens"},
	{"completion_tokens_details.text_tokens", "output_text_tokens"}, {"output_tokens_details.text_tokens", "output_text_tokens"}, {"output_token_details.text_tokens", "output_text_tokens"},
	{"prompt_tokens_details.image_tokens", "input_image_tokens"}, {"input_tokens_details.image_tokens", "input_image_tokens"},
	{"completion_tokens_details.image_tokens", "output_image_tokens"}, {"output_tokens_details.image_tokens", "output_image_tokens"},
	{"completion_tokens_details.reasoning_tokens", "reasoning_tokens"}, {"output_tokens_details.reasoning_tokens", "reasoning_tokens"},
	{"server_tool_use.web_search_requests", "server_tool_use.web_search_requests"},
}

// geminiStreamUsageKeys 保留 Gemini 独有统计口径，思考 token 是否相加由后续语义归一化决定。
var geminiStreamUsageKeys = []streamUsageField{{"promptTokenCount", "input_tokens"}, {"candidatesTokenCount", "output_tokens"}, {"cachedContentTokenCount", "cached_tokens"}, {"thoughtsTokenCount", "reasoning_tokens"}}

// IsStreamErrorEvent 识别供应商原始失败事件；显式 null error 不表示失败。
// 参数 event 为 SSE 名称，data 为未经改写的 JSON，返回是否应保留原始终止帧。
// Responses 的已知限制终态不是错误；显式 event:error 仍保持错误优先。
func IsStreamErrorEvent(event string, data []byte) bool {
	v := gjson.ParseBytes(data)
	if event != "error" && IsResponsesLimitCompletion(v) {
		return false
	}
	kind := v.Get("type").String()
	if kind == "" {
		kind = event
	}
	return event == "error" || kind == "error" || kind == "upstream_error" || kind == "response.error" || kind == "response.failed" || kind == "response.incomplete" || kind == "response.cancelled" || kind == "response.canceled" || kind == "conversation.chat.failed" || (v.Get("error").Exists() && v.Get("error").Type != gjson.Null) || (kind == "response.done" && v.Get("response.status").Exists() && v.Get("response.status").String() != "completed")
}

// ClientError 只读取传输错误，避免热路径为每帧复制完整诊断快照。
func (s *StreamSession) ClientError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.ClientErr
}

// ProtocolError 读取首个终止错误（含本地转换错误），不复制私有诊断；无参数，供读者决定是否停止拉取。
func (s *StreamSession) ProtocolError() error { s.mu.Lock(); defer s.mu.Unlock(); return s.state.Err }

// ProtocolComplete 读取全流已确认完成状态，不复制用量或诊断；供每帧转换分支使用。
func (s *StreamSession) ProtocolComplete() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Complete && s.state.Reason == ""
}

// BindUpstream 登记 SDK/WS 资源，供下游写失败时立即关闭；conn 只由请求所有者更换。
func (s *StreamSession) BindUpstream(conn io.Closer) {
	if !s.Active() {
		return
	}
	s.mu.Lock()
	s.closeBody = conn
	s.state.Accepted = true
	s.state.UpstreamStarted = true
	s.mu.Unlock()
}

// ObserveHTTP 在业务读取前安装透明响应观察；resp 必须为 HTTP 200，且真实交换门控已开放。
// 此方法不激活门控，适配器的占位响应及非 200 保留原对象交给旧流程。
func (s *StreamSession) ObserveHTTP(resp *http.Response) {
	if !s.Active() || resp == nil || resp.StatusCode != http.StatusOK || resp.Body == nil {
		return
	}
	if _, ok := resp.Body.(*streamObservedBody); ok {
		return
	}
	s.mu.Lock()
	s.state.HTTPObserved = true
	s.state.UpstreamStarted = true
	s.state.Accepted = true
	s.mu.Unlock()
	mode := "auto"
	ct := StreamMediaType(resp.Header.Get("Content-Type"))
	switch {
	case strings.Contains(ct, "eventstream"):
		mode = "sdk"
	case strings.Contains(ct, "text/event-stream"):
		mode = "sse"
	case strings.Contains(ct, "ndjson") || strings.Contains(ct, "jsonl"):
		mode = "lines"
	case strings.Contains(ct, "json"):
		mode = "json"
	case IsStreamBinaryContentType(ct):
		mode = "binary"
	}
	if s.OpaqueSSE {
		mode = "opaque_sse"
	}
	body := &streamObservedBody{source: resp.Body, session: s, mode: mode}
	resp.Body = body
	s.mu.Lock()
	s.closeBody = body
	s.mu.Unlock()
}

// streamObservedBody 旁路解析成功读到的响应，不额外创建读取者，不为诊断读空上游。
type streamObservedBody struct {
	source           io.ReadCloser      // 下层响应读取器，可已包含原始响应采集包装。
	session          *StreamSession     // 当前尝试的协议观察和终止状态。
	mode             string             // SSE、NDJSON、完整 JSON、SDK、裸媒体或待识别模式。
	pending          []byte             // 尚未解析完的协议尾部，受单帧字节上限约束。
	once             sync.Once          // 保证下层 Close 只执行一次。
	closeErr         error              // 首次 Close 的结果，后续关闭调用复用。
	readBytes        int64              // 裸媒体至少读到内容才接受 EOF 为正常完成。
	parseErr         error              // 先交付本帧原始字节，再停止后续网络读取。
	readErr          error              // 同批帧消费完后才处理的底层读取终止结果。
	ready            []byte             // 已观察的当前帧剩余字节，不包含下一帧。
	scanned          int                // NDJSON 已搜索前缀，避免短读时重复扫描整行。
	framing          StreamFrameScanner // SSE 增量行游标，包含跨 Read 的 CRLF 延续状态。
	readBuffer       []byte             // 首次按帧读取时分配并复用的 4 KiB 缓冲；受管扫描器、SDK 和媒体不分配。
	jsonProbeScanned int                // JSON 首行已搜索的字节数，后续只检查新读入部分。
	jsonProbeDone    bool               // 首行完整或到达 EOF 后不重复探测 JSON/NDJSON 格式。
	imageTask        bool               // Ali 适配器在读取前声明异步任务阶段，允许合法中间态独立结束本次 HTTP 响应。
	intermediateJSON bool               // 本响应已验证为任务中间态；只跳过该响应的正常 EOF，不跳过读取异常。
}

// UnwrapStream 将原始已采集响应体交回专用解析器，避免同一 HTTP 响应登记两次。
func (b *streamObservedBody) UnwrapStream() io.ReadCloser { return b.source }

// UseStreamLineFraming 在开始读取前将响应 resp 的通用观察器设为 NDJSON 行模式；nil 或未包装响应跳过，无返回值。
func UseStreamLineFraming(resp *http.Response) {
	if resp != nil {
		if body, ok := resp.Body.(*streamObservedBody); ok {
			body.mode = "lines"
		}
	}
}

// Read 按单个完整帧推进独立扫描器状态；本帧消费完后才观察下一帧或 EOF。
// 参数 p 为调用方缓冲区；允许一帧分多次复制，SDK/媒体仍直接读取。
func (b *streamObservedBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if !b.session.Active() {
		return b.source.Read(p)
	}
	if b.mode == "sdk" || b.mode == "binary" {
		n, err := b.source.Read(p)
		b.readBytes += int64(n)
		if b.mode == "binary" && n > 0 {
			b.session.mu.Lock()
			b.session.state.ReceivedResponse = true
			b.session.mu.Unlock()
		}
		if b.mode == "binary" && err != nil {
			if errors.Is(err, io.EOF) && b.readBytes > 0 {
				b.session.Complete()
			}
			b.session.EndRead(err)
		}
		return n, err
	}
	for {
		if len(b.ready) > 0 {
			n := copy(p, b.ready)
			b.ready = b.ready[n:]
			return n, nil
		}
		if b.parseErr != nil {
			return 0, b.parseErr
		}
		end := 0
		if b.mode == "auto" {
			t := bytes.TrimSpace(b.pending)
			if bytes.HasPrefix(t, []byte("data:")) || bytes.HasPrefix(t, []byte("event:")) || bytes.HasPrefix(t, []byte(":")) {
				b.mode = "sse"
			} else if len(t) > 0 && t[0] == '{' {
				b.mode = "json"
			}
		}
		if b.mode == "json" && !b.jsonProbeDone {
			first := b.pending
			i := bytes.IndexByte(first[b.jsonProbeScanned:], '\n')
			if i >= 0 {
				first = first[:b.jsonProbeScanned+i]
			}
			b.jsonProbeScanned = len(b.pending)
			b.jsonProbeDone = i >= 0 || b.readErr != nil
			// 首行增量搜索且只探测一次，避免单行图片 JSON 反复扫描 base64 前缀。
			if b.jsonProbeDone && gjson.ValidBytes(first) {
				v := gjson.ParseBytes(first)
				if v.Get("event_type").Exists() || v.Get("done").Exists() {
					b.mode = "lines"
				}
			}
		}
		switch b.mode {
		case "sse", "opaque_sse":
			end = b.framing.End(b.pending)
		case "lines":
			if i := bytes.IndexByte(b.pending[b.scanned:], '\n'); i >= 0 {
				end = b.scanned + i + 1
			}
			b.scanned = len(b.pending)
		}
		if end == 0 && b.readErr != nil {
			end = len(b.pending)
		}
		if end > MaxStreamFrameBytes || (end == 0 && len(b.pending) > MaxStreamFrameBytes) {
			b.session.Fail("upstream_protocol_error", errors.New("upstream frame exceeds limit"))
			b.pending = nil
			b.parseErr = b.session.ProtocolError()
			return 0, b.parseErr
		}
		if end > 0 {
			raw := b.pending[:end]
			switch b.mode {
			case "json":
				_ = b.session.ObserveEvent("", raw)
				if b.readErr != nil && !errors.Is(b.readErr, io.EOF) {
					// 完整 JSON 的传输尚未成功结束；即使已看到 finish 字段也保留底层读取错误。
					b.session.Fail("upstream_read_error", b.readErr)
				} else if b.session.ProtocolError() == nil {
					b.intermediateJSON = b.session.observeWholeJSON(raw, b.imageTask)
				}
			case "lines":
				_ = b.session.ObserveEvent("", raw)
			default:
				_ = b.session.ObserveFrame(raw)
			}
			b.ready = raw
			b.pending = b.pending[end:]
			b.scanned = 0
			b.parseErr = b.session.ProtocolError()
			continue
		}
		if b.readErr != nil {
			if !b.intermediateJSON || !errors.Is(b.readErr, io.EOF) {
				b.session.EndRead(b.readErr)
			}
			return 0, b.readErr
		}
		// 固定小块读取，缓存仅容纳当前帧及此块尚未消费的尾部。
		if b.readBuffer == nil {
			b.readBuffer = make([]byte, 4096)
		}
		n, err := b.source.Read(b.readBuffer)
		b.pending = append(b.pending, b.readBuffer[:n]...)
		b.readErr = err
		if n == 0 && err == nil {
			return 0, nil
		}
	}
}

// ObserveFrame 在写入所有者消费时观察原始 SSE 帧 raw；半帧记为不完整，原始错误保留外壳。
// 返回首个协议错误；智谱非结构化内容跳过 JSON 观察，但完成/error 仍校验。
func (s *StreamSession) ObserveFrame(raw []byte) error {
	if !s.Active() || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if StreamFrameEnd(raw) == 0 {
		s.Fail("upstream_incomplete", errors.New("upstream ended with an incomplete SSE frame"))
		return s.ProtocolError()
	}
	event, data := StreamFramePayload(raw)
	var observeErr error
	if !s.OpaqueSSE || event == "finish" || event == "error" {
		observeErr = s.ObserveEvent(event, data)
	} else if event == "add" && len(data) > 0 {
		s.mu.Lock()
		if s.state.Reason == "" && (s.requestContext == nil || s.requestContext.Err() == nil) {
			s.state.ReceivedResponse = true
			if s.receivedFactory != nil {
				if s.receivedEstimate == nil {
					s.receivedEstimate = s.receivedFactory()
				}
				s.state.ReceivedOutput += s.receivedEstimate(string(data), 0)
			}
		}
		s.mu.Unlock()
	}
	if IsStreamErrorEvent(event, data) {
		// 即使原 error 的 usage 校验失败也保留原帧；仅拒绝其无效用量，不替换上游错误正文。
		s.mu.Lock()
		s.state.ErrorFrame = bytes.Clone(raw)
		s.state.ErrorPayload = nil // 已有完整 SSE 外壳，只保留这一份原错误表示。
		s.mu.Unlock()
	}
	if observeErr != nil {
		return observeErr
	}
	return s.ProtocolError()
}

// observeWholeJSON 校验已知完整响应结构；data 已通过语法/用量检查，验证失败在原适配器写出前终止读取。
// imageTask 仅允许 Ali 显式任务阶段；返回 true 表示合法中间态，本响应 EOF 不代表生成结束。
// 标准响应还需对应内容结构与终态，PaLM 需非空正文；图片按实际渠道校验，Gemini 明确策略反馈例外。
// MiniMax 语音仅在语音合成模式接受原生成功 JSON，音频解码继续由原适配器完成。
func (s *StreamSession) observeWholeJSON(data []byte, imageTask bool) bool {
	v := gjson.ParseBytes(data)
	if s.ExpectedImages > 0 {
		return s.observeImageJSON(v, imageTask)
	}
	if s.ChannelType == constant.ChannelTypeMiniMax && s.RelayMode == relayconstant.RelayModeAudioSpeech {
		// 只放行 MiniMax 语音模式的成功完整 JSON；状态缺省兼容原 URL/hex 响应，显式中间态不算完成。
		// 音频仅检查非空字符串，仍由原适配器解码/跳转；不将字符用量推造成 token。
		audio, code, status := v.Get("data.audio"), v.Get("base_resp.status_code"), v.Get("data.status")
		valid := audio.Type == gjson.String && strings.TrimSpace(audio.Str) != "" && code.Type == gjson.Number && code.Float() == 0
		valid = valid && (!status.Exists() || status.Type == gjson.Number && status.Float() == 2)
		if valid {
			s.Complete()
		} else {
			s.Fail("upstream_protocol_error", errors.New("upstream JSON lacks a complete audio response"))
		}
		return false
	}
	valid := isGeminiPromptBlocked(v) && s.ProtocolComplete()
	if choices := v.Get("choices"); choices.IsArray() && len(choices.Array()) > 0 {
		valid = s.ProtocolComplete()
		for _, choice := range choices.Array() {
			valid = valid && (choice.Get("message").IsObject() || choice.Get("text").Type == gjson.String)
		}
	}
	if v.Get("type").String() == "message" && v.Get("content").IsArray() && v.Get("stop_reason").String() != "" {
		valid = s.ProtocolComplete()
	}
	if cs := v.Get("candidates"); cs.IsArray() && len(cs.Array()) > 0 {
		valid = len(cs.Array()) >= max(1, s.ExpectedChoices)
		for _, c := range cs.Array() {
			content := c.Get("content")
			palm := content.Type == gjson.String && strings.TrimSpace(content.String()) != ""
			gemini := c.Get("finishReason").String() != "" && (content.Get("parts").IsArray() || !content.Exists())
			valid = valid && (palm || gemini)
		}
	}
	if parts := v.Get("output.message.content"); parts.IsArray() && len(parts.Array()) > 0 && v.Get("stopReason").String() != "" {
		valid = true
		for _, part := range parts.Array() {
			valid = valid && part.Get("text").Type == gjson.String && strings.TrimSpace(part.Get("text").String()) != ""
		}
	}
	if v.Get("object").String() == "response" && (v.Get("status").String() == "completed" || IsResponsesLimitCompletion(v)) && v.Get("output").IsArray() {
		valid = true
	}
	if v.Get("text").Type == gjson.String {
		valid = true // 语音转录完整 JSON 的必需结果字段；不适用于 SSE 扫描器。
	}
	if valid {
		s.Complete()
	} else {
		s.Fail("upstream_protocol_error", errors.New("upstream JSON lacks a complete response structure"))
	}
	return false
}

// Close 幂等关闭底层资源，避免读取器清理与终止所有者重复释放。
func (b *streamObservedBody) Close() error {
	b.once.Do(func() { b.closeErr = b.source.Close() })
	return b.closeErr
}

// CommitDelivery 在完整写入且刷新成功后记录语义内容；参数 data 是下游事件 JSON，不是请求数据。
func (s *StreamSession) CommitDelivery(data []byte) {
	if !s.Active() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v := gjson.ParseBytes(data)
	kind := v.Get("type").String()
	if s.format == types.RelayFormatOpenAIRealtime && (IsRealtimeRecoverableError(v) || isRealtimeRoundCompletion(v)) {
		return // 轮次终态/恢复错误不是有效正文，也不占用连接级错误已交付标记。
	}
	if IsStreamErrorEvent("", data) {
		s.state.ErrorDelivered = true
		return
	}
	var text strings.Builder
	for _, choice := range v.Get("choices").Array() {
		for _, key := range []string{"delta.content", "delta.reasoning_content", "delta.reasoning", "message.content", "text"} {
			r := choice.Get(key)
			if r.Type == gjson.String {
				text.WriteString(r.String())
			}
		}
		if choice.Get("delta.audio.data").String() != "" {
			s.state.Effective = true
		}
		if fn := choice.Get("delta.function_call"); fn.IsObject() {
			// 旧版每个候选只有一个调用；独立命名空间避免与新版工具索引 0 混合。
			// 名称/参数只累计已刷新片段，沿用下方候选结束分支确认完整交付，不等待下一帧。
			key := fmt.Sprintf("chat:%d:legacy", choice.Get("index").Int())
			s.toolLocked(key, fn.Get("name").String(), fn.Get("arguments").String(), false, true)
		}
		for _, tc := range choice.Get("delta.tool_calls").Array() {
			key := fmt.Sprintf("chat:%d:%d", choice.Get("index").Int(), tc.Get("index").Int())
			s.toolLocked(key, tc.Get("function.name").String(), tc.Get("function.arguments").String(), false, false)
		}
		if choice.Get("finish_reason").String() != "" {
			for key := range s.tools {
				if strings.HasPrefix(key, fmt.Sprintf("chat:%d:", choice.Get("index").Int())) {
					s.toolLocked(key, "", "", true, false)
				}
			}
		}
	}
	switch kind {
	case "content_block_start":
		block := v.Get("content_block")
		switch block.Get("type").String() {
		case "text":
			text.WriteString(block.Get("text").String())
		case "thinking":
			text.WriteString(block.Get("thinking").String())
		case "tool_use", "server_tool_use":
			args := ""
			if block.Get("input").Raw != "{}" {
				args = block.Get("input").Raw
			}
			s.toolLocked(fmt.Sprintf("claude:%d", v.Get("index").Int()), block.Get("name").String(), args, false, false)
		}
	case "content_block_delta":
		switch v.Get("delta.type").String() {
		case "text_delta":
			text.WriteString(v.Get("delta.text").String())
		case "thinking_delta":
			text.WriteString(v.Get("delta.thinking").String())
		case "input_json_delta":
			s.toolLocked(fmt.Sprintf("claude:%d", v.Get("index").Int()), "", v.Get("delta.partial_json").String(), false, false)
		}
	case "content_block_stop":
		s.toolLocked(fmt.Sprintf("claude:%d", v.Get("index").Int()), "", "", true, false)
	case "response.output_text.delta", "response.text.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta", "response.audio_transcript.delta", "transcript.text.delta":
		text.WriteString(v.Get("delta").String())
		if s.receivedOnly && kind == "transcript.text.delta" {
			s.receivedTranscript = true
		}
	case "transcript.text.done":
		if !s.receivedOnly || !s.receivedTranscript {
			text.WriteString(v.Get("text").String())
		}
	case "response.audio.delta", "response.output_audio.delta", "speech.audio.delta":
		if v.Get("delta").String() != "" || v.Get("audio").String() != "" {
			s.state.Effective = true
		}
	case "response.output_item.done":
		item := v.Get("item")
		if item.Get("type").String() == "function_call" {
			s.commitResponseToolLocked(item.Get("id").String(), item.Get("call_id").String(), item.Get("name").String(), item.Get("arguments").String())
		}
		if item.Get("type").String() == "image_generation_call" && item.Get("result").String() != "" {
			s.state.Effective = true
		}
	case "response.function_call_arguments.done":
		s.commitResponseToolLocked(v.Get("item_id").String(), v.Get("call_id").String(), v.Get("name").String(), v.Get("arguments").String())
	case "response.output_item.added":
		item := v.Get("item")
		if s.receivedOnly && item.Get("type").String() == "function_call" {
			s.toolLocked(streamResponseToolKey(item.Get("id").String(), item.Get("call_id").String()), item.Get("name").String(), item.Get("arguments").String(), false, false)
		}
	case "response.function_call_arguments.delta":
		if s.receivedOnly {
			s.toolLocked(streamResponseToolKey(v.Get("item_id").String(), v.Get("call_id").String()), "", v.Get("delta").String(), false, false)
		}
	case "image_generation.partial_image", "image_generation.completed", "image_edit.partial_image", "image_edit.completed":
		// 完整 JSON 转 SSE 也可交付图片 URL；它与 base64 图片同样属于有效返回。
		url := v.Get("url")
		if v.Get("b64_json").String() != "" || url.Type == gjson.String && strings.TrimSpace(url.Str) != "" {
			s.state.Effective = true
		}
	}
	for _, candidate := range v.Get("candidates").Array() {
		for _, part := range candidate.Get("content.parts").Array() {
			text.WriteString(part.Get("text").String())
			if part.Get("inlineData.data").String() != "" {
				s.state.Effective = true
				// 原生内联媒体不进文本，交由估算器按张折算；不把 base64 字节冒充 token。
				s.media++
			}
			if fn := part.Get("functionCall"); fn.IsObject() && fn.Get("name").String() != "" && fn.Get("args").IsObject() {
				s.state.Effective = true
				text.WriteString(fn.Raw)
			}
		}
	}
	for _, item := range v.Get("data").Array() {
		if item.Get("b64_json").String() != "" || item.Get("url").String() != "" {
			s.state.Effective = true
		}
	}
	if strings.TrimSpace(text.String()) != "" {
		s.state.Effective = true
	}
	s.text.WriteString(text.String())
}

// toolLocked 按 key 合并工具名 name 与参数片段 args；done 表示已交付工具结束，空参数按 {}，合法完整对象才记有效内容。
// appendName 仅为旧版函数名称分片开启；关闭时非空 name 替换旧名称，空名称始终保留已有值。
// 调用者持有会话锁；返回值仅在本次提交完整合法工具时为 true，供完成事件去重；达到数量/字节上限则丢弃候选。
func (s *StreamSession) toolLocked(key, name, args string, done, appendName bool) bool {
	tool := s.tools[key]
	if tool == nil {
		if len(s.tools) >= 128 {
			return false
		}
		tool = &streamTool{}
		s.tools[key] = tool
	}
	nameGrowth := 0
	if name != "" {
		nameGrowth = len(name)
		if !appendName {
			nameGrowth -= tool.name.Len()
		}
	}
	if s.toolBytes+len(args)+nameGrowth > MaxStreamFrameBytes {
		s.toolBytes -= tool.args.Len() + tool.name.Len()
		delete(s.tools, key)
		return false
	}
	if name != "" {
		if s.receivedOnly {
			delta := name
			if !appendName && strings.HasPrefix(name, tool.name.String()) {
				delta = strings.TrimPrefix(name, tool.name.String())
			}
			s.text.WriteString(delta)
		}
		if !appendName {
			tool.name.Reset()
		}
		tool.name.WriteString(name) // 独立保存名称片段，不引用 gjson 整帧；增量追加避免每片重新拼接整个名称。
		s.toolBytes += nameGrowth
	}
	tool.args.WriteString(args)
	if s.receivedOnly {
		s.text.WriteString(args)
	}
	s.toolBytes += len(args)
	committed := false
	if done {
		raw := tool.args.String()
		if raw == "" {
			raw = "{}"
		}
		if tool.name.Len() > 0 && gjson.Valid(raw) && gjson.Parse(raw).IsObject() {
			s.state.Effective = true
			if !s.receivedOnly {
				s.text.WriteString(tool.name.String())
				s.text.WriteString(raw)
			}
			committed = true
		}
		delete(s.tools, key)
		s.toolBytes -= tool.args.Len() + tool.name.Len()
	}
	return committed
}

// TakeDeliveredText 转移本批已交付正文供分批 token 估算；读取后清空，不保存完整生成内容。
func (s *StreamSession) TakeDeliveredText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	text := s.text.String()
	s.text.Reset()
	return text
}

// TakeDeliveredMedia 取走本批内联媒体分片数并清零，与 TakeDeliveredText 成对调用。
// 媒体按张交给估算器折算，不按字节；调用者不取走即在下一批累计。
func (s *StreamSession) TakeDeliveredMedia() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	media := s.media
	s.media = 0
	return media
}

// AddEstimatedOutput 累计本地估算，不混入上游确认映射；参数 n 为本批已交付 token 数。
func (s *StreamSession) AddEstimatedOutput(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.EstimatedOutput += max(0, n)
}

// CommitMediaDelivery 累计已成功交付的媒体字节 n 并标记有效内容；n<=0 或会话非活动时跳过，不把字节冒充 token。
func (s *StreamSession) CommitMediaDelivery(n int) {
	if !s.Active() || n <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Effective = true
	s.state.MediaBytes += int64(n)
}

// MarkErrorDelivered 标记供应商失败事件已经写出成功，避免随后额外生成错误帧。
func (s *StreamSession) MarkErrorDelivered() {
	s.mu.Lock()
	s.state.ErrorDelivered = true
	s.mu.Unlock()
}

// IsStreamSuccessEvent 检查事件名 event 及 JSON data 是否含成功结束标记；任一候选有结束原因也返回 true，不判定全流是否完成。
func IsStreamSuccessEvent(event string, data []byte) bool {
	return classifyStreamSuccessEvent(event, data) != streamSuccessNone
}

// streamSuccessScope 区分候选结束与响应结束，避免以全流完成状态过滤单个候选的正常尾段。
type streamSuccessScope uint8

const (
	streamSuccessNone        streamSuccessScope = iota // 普通内容或错误事件，不属于成功结束。
	streamSuccessCandidate                             // 单个候选完成，其他候选可继续生成。
	streamSuccessClaudeDelta                           // Claude 真正的 stop_reason 帧，随后仍需 message_stop。
	streamSuccessResponse                              // 整条响应的结束标记，须由上游全流完成状态确认。
)

// classifyStreamSuccessEvent 按事件名 event 和 JSON data 返回成功结束的粒度，不修改协议状态或原始字节。
// 错误优先于成功，响应级标记优先于附带的候选字段；无成功标记返回 streamSuccessNone。
func classifyStreamSuccessEvent(event string, data []byte) streamSuccessScope {
	if IsStreamErrorEvent(event, data) {
		return streamSuccessNone
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
		return streamSuccessResponse
	}
	v := gjson.ParseBytes(data)
	kind := v.Get("type").String()
	if kind == "" {
		kind = event
	}
	switch kind {
	case "response.incomplete":
		if IsResponsesLimitCompletion(v) {
			return streamSuccessResponse
		}
	case "message_stop", "response.completed", "response.done", "speech.audio.done", "transcript.text.done":
		return streamSuccessResponse
	case "message_delta":
		if v.Get("delta.stop_reason").String() != "" {
			return streamSuccessClaudeDelta
		}
		return streamSuccessNone
	}
	for _, c := range v.Get("choices").Array() {
		if c.Get("finish_reason").String() != "" {
			return streamSuccessCandidate
		}
	}
	for _, c := range v.Get("candidates").Array() {
		if c.Get("finishReason").String() != "" {
			return streamSuccessCandidate
		}
	}
	return streamSuccessNone
}

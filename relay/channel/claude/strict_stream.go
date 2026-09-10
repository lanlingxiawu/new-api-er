package claude

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// maxClaudeFrameBytes 限制单个 SSE 帧及累计工具参数为 8 MiB，避免异常上游持续占用内存。
const maxClaudeFrameBytes = 8 << 20

// errClaudeIncompleteFrame 表示读到 EOF 时仍残留没有空行结束的非空 SSE 帧。
var errClaudeIncompleteFrame = errors.New("incomplete SSE frame at end of response")

// claudeTransportFailure 用 err 携带尚未取得 HTTP 响应的传输错误，复用统一终止路径；不代表真实上游 body。
type claudeTransportFailure struct{ err error }

// Read 将连接失败以读取错误形式交给严格流式处理器。
// 接收者 r：保存原始传输错误的内部读取器；未命名 []byte 参数是调用方缓冲区，不填充。
// 返回 0 字节和原始错误，不生成任何上游响应内容。
func (r *claudeTransportFailure) Read([]byte) (int, error) { return 0, r.err }

// Close 结束内部传输失败读取器；它不持有连接或文件。
// 接收者 r：内部错误读取器；无参数；始终返回 nil。
func (r *claudeTransportFailure) Close() error { return nil }

// HandleStreamTransportFailure shares the single terminal/billing path even
// when no HTTP response body was obtained. No request data is retained.
// HandleStreamTransportFailure 在没有取得 HTTP 响应时复用严格流式的唯一终止和用量选择流程。
// 参数 c：下游请求上下文；info：当前中转状态；err：上游连接/传输错误。
// 返回用于最终结算的用量及处理结果；只包装内部错误读取器，不采集请求内容。
func HandleStreamTransportFailure(c *gin.Context, info *relaycommon.RelayInfo, err error) (*dto.Usage, *types.NewAPIError) {
	return strictClaudeStream(c, &http.Response{Body: &claudeTransportFailure{err: err}}, info)
}

// claudeFrameRead 是读取工作者交给响应处理者的一帧数据或终止信号。
type claudeFrameRead struct {
	raw []byte // 包含原始分隔符的完整 SSE 帧副本；读取失败时为空。
	err error  // 读取/扫描异常或 EOF；为 nil 时处理 raw。
}

// claudeOpenBlock 保存当前尚未结束的内容块，工具参数在 block_stop 时才按完整 JSON 校验。
type claudeOpenBlock struct {
	kind, id, name, input string          // kind 为块类型；id/name 为工具标识；input 为起始块中的原始参数 JSON。
	arguments             strings.Builder // 逐段累积 partial_json，上限为 maxClaudeFrameBytes。
}

// claudeProtocol 是单个响应拥有的协议状态机，按消息与内容块顺序校验，不产生补齐成功事件。
type claudeProtocol struct {
	started, stop bool                     // started 表示已接收合法 message_start；stop 表示已接收非空 stop_reason。
	stopReason    string                   // 上游已确认的停止原因，后续上游原因应与其一致。
	nextIndex     int                      // 下一内容块应使用的索引，从 0 递增。
	blocks        map[int]*claudeOpenBlock // 当前打开的内容块；正常结束时必须为空。
}

// claudeErrorDetail 提取上游 error.message 供私有诊断使用，缺失时使用通用原因。
// 参数 data：error 事件的原始 JSON 文本；返回有界原因，超过 2048 字节时保留前后各 1024 字节并加省略标记。
func claudeErrorDetail(data string) string {
	message := gjson.Get(data, "error.message").String()
	if message == "" {
		message = "upstream error event"
	}
	if len(message) > 2048 {
		message = message[:1024] + " ... " + message[len(message)-1024:]
	}
	return message
}

// claudeFrameSplitter 以增量游标避免短读时重复扫描整帧；offset 为已扫描字节数，lineStart 为当前行起点。
type claudeFrameSplitter struct{ offset, lineStart int }

// Preserve SSE frames byte-for-byte, including CRLF and unknown SSE fields.
// Split 按空行切出完整 SSE 帧，并保留 CRLF、未知字段等原始字节。
// 接收者 s：增量扫描游标；参数 data：Scanner 当前未消费缓冲；atEOF：底层是否已结束。
// 依次返回消费字节数、完整帧和错误；EOF 时仍有非空残片返回不完整帧错误，普通短读继续等待。
func (s *claudeFrameSplitter) Split(data []byte, atEOF bool) (int, []byte, error) {
	for i := s.offset; i < len(data); i++ {
		if data[i] != '\n' {
			continue
		}
		line := data[s.lineStart:i]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if len(line) == 0 {
			s.offset, s.lineStart = 0, 0
			return i + 1, data[:i+1], nil
		}
		s.lineStart = i + 1
	}
	s.offset = len(data)
	if atEOF && len(bytes.TrimSpace(data)) > 0 {
		return 0, nil, errClaudeIncompleteFrame
	}
	return 0, nil, nil
}

// claudeFramePayload 从原始 SSE 帧提取事件名与 data，多个 data 行以换行连接。
// 参数 raw：含分隔符的完整帧；返回 event、data 两个字符串，不修改 raw，透传仍使用原始字节。
func claudeFramePayload(raw []byte) (string, string) {
	var event string
	var data strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSuffix(line, "\r")
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			value = ""
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event = value
		case "data":
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(value)
		}
	}
	return event, data.String()
}

// 返回值只是候选有效内容；调用方应在对应帧完整写出并刷新成功后才计入已交付内容。
// accept 校验已解析的 Claude 事件并推进消息/内容块状态，提取可供交付计量的候选内容。
// 接收者 s：本响应协议状态；参数 v：已解析的单个事件 JSON。
// 返回文本/思考/完整工具调用候选字符串及协议错误；ping、signature、元数据返回空串；未知扩展事件按兼容路径通过。
func (s *claudeProtocol) accept(v gjson.Result) (string, error) {
	kind := v.Get("type").String()
	switch kind {
	case "ping":
		return "", nil
	case "message_start":
		message := v.Get("message")
		if s.started || !message.IsObject() {
			return "", errors.New("invalid or duplicate message_start")
		}
		for _, key := range []string{"id", "model"} {
			value := message.Get(key)
			if value.Type != gjson.String || strings.TrimSpace(value.String()) == "" {
				return "", fmt.Errorf("invalid message_start message.%s", key)
			}
		}
		if message.Get("type").String() != "message" || message.Get("role").String() != "assistant" {
			return "", errors.New("invalid message_start message type or role")
		}
		// 内容由后续带索引的块事件承载；起始 content 应为空，避免绕过块校验及成功写出统计。
		content := message.Get("content")
		if !content.IsArray() || content.Get("#").Int() != 0 {
			return "", errors.New("message_start content must be an empty array")
		}
		s.started = true
	case "content_block_start":
		index := v.Get("index")
		if !s.started || s.stop || index.Type != gjson.Number || index.Int() != int64(s.nextIndex) || index.Float() != float64(index.Int()) || !v.Get("content_block").IsObject() || len(s.blocks) > 0 {
			return "", errors.New("invalid content_block_start sequence")
		}
		b := v.Get("content_block")
		if b.Get("type").Type != gjson.String || b.Get("type").String() == "" {
			return "", errors.New("missing content block type")
		}
		block := &claudeOpenBlock{kind: b.Get("type").String(), id: b.Get("id").String(), name: b.Get("name").String(), input: b.Get("input").Raw}
		if (block.kind == "tool_use" || block.kind == "server_tool_use") && (block.id == "" || block.name == "") {
			return "", errors.New("missing tool identity")
		}
		s.blocks[s.nextIndex] = block
		s.nextIndex++
		if block.kind == "text" {
			if b.Get("text").Type != gjson.String {
				return "", errors.New("missing text block content")
			}
			return b.Get("text").String(), nil
		}
		if block.kind == "thinking" {
			if b.Get("thinking").Type != gjson.String {
				return "", errors.New("missing thinking block content")
			}
			return b.Get("thinking").String(), nil
		}
	case "content_block_delta", "content_block_stop":
		index := v.Get("index")
		if index.Type != gjson.Number || index.Float() != float64(index.Int()) {
			return "", errors.New("missing content block index")
		}
		b := s.blocks[int(index.Int())]
		if b == nil || s.stop {
			return "", errors.New("content event without matching open block")
		}
		if kind == "content_block_stop" {
			delete(s.blocks, int(index.Int()))
			if b.kind == "tool_use" || b.kind == "server_tool_use" {
				args := b.input
				if b.arguments.Len() > 0 {
					args = b.arguments.String()
				}
				if !gjson.Valid(args) || !gjson.Parse(args).IsObject() {
					return "", errors.New("incomplete tool input JSON")
				}
				if b.id != "" && b.name != "" {
					return b.name + args, nil
				}
			}
			return "", nil
		}
		d := v.Get("delta")
		if !d.IsObject() || d.Get("type").Type != gjson.String || d.Get("type").String() == "" {
			return "", errors.New("missing block delta type")
		}
		switch d.Get("type").String() {
		case "text_delta":
			if b.kind != "text" || d.Get("text").Type != gjson.String {
				return "", errors.New("invalid text delta")
			}
			return d.Get("text").String(), nil
		case "thinking_delta":
			if b.kind != "thinking" || d.Get("thinking").Type != gjson.String {
				return "", errors.New("invalid thinking delta")
			}
			return d.Get("thinking").String(), nil
		case "input_json_delta":
			if (b.kind != "tool_use" && b.kind != "server_tool_use") || d.Get("partial_json").Type != gjson.String {
				return "", errors.New("invalid tool delta")
			}
			fragment := d.Get("partial_json").String()
			if b.arguments.Len()+len(fragment) > maxClaudeFrameBytes {
				return "", errors.New("tool input exceeds stream limit")
			}
			b.arguments.WriteString(fragment)
		case "signature_delta":
			if b.kind != "thinking" || d.Get("signature").Type != gjson.String {
				return "", errors.New("invalid signature delta")
			}
		}
	case "message_delta":
		if !s.started || len(s.blocks) > 0 || !v.Get("delta").IsObject() {
			return "", errors.New("invalid message_delta sequence")
		}
		reason := v.Get("delta.stop_reason")
		if reason.Exists() && reason.Type != gjson.Null && reason.Type != gjson.String {
			return "", errors.New("invalid stop reason")
		}
		if reason.Type == gjson.String && reason.String() != "" {
			if s.stop && reason.String() != s.stopReason {
				return "", errors.New("conflicting stop reasons")
			}
			s.stop = true
			s.stopReason = reason.String()
		}
	case "message_stop":
		if !s.started || !s.stop || len(s.blocks) > 0 {
			return "", errors.New("message_stop without complete message")
		}
	case "":
		return "", errors.New("event type is missing")
	}
	return "", nil
}

// mergeClaudeUsage 校验后合并上游累计用量，已出现字段覆盖旧值而非相加，缺失字段保持原值。
// 参数 evidence：已初始化的用量证据映射，成功时原地更新；usage：本事件用量节点，缺失/null 时跳过。
// 返回校验错误；各计数必须是 0 至 10 亿的整数，显式 0 保留，任一字段无效则本批字段均不写入。
func mergeClaudeUsage(evidence map[string]int, usage gjson.Result) error {
	if !usage.Exists() || usage.Type == gjson.Null {
		return nil
	}
	if !usage.IsObject() {
		return errors.New("usage must be an object")
	}
	updates := make(map[string]int, 8)
	for _, key := range []string{"input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "cache_creation.ephemeral_5m_input_tokens", "cache_creation.ephemeral_1h_input_tokens", "server_tool_use.web_search_requests"} {
		value := usage.Get(key)
		if !value.Exists() {
			continue
		}
		var count int
		if err := common.UnmarshalJsonStr(value.Raw, &count); err != nil || value.Type != gjson.Number || count < 0 || count > 1_000_000_000 {
			return fmt.Errorf("invalid usage field %s", key)
		}
		updates[key] = count
	}
	for key, value := range updates {
		evidence[key] = value
	}
	return nil
}

// confirmedClaudeUsage 将已确认的 Anthropic 用量转换为统一 Usage 和 BillingUsage，保持两套计费口径一致。
// 参数 e：上游累计证据映射，缺失计数字段按 0 构造；返回新的用量对象。
// 缓存写入总量缺失但分时字段存在时，由 5 分钟和 1 小时字段合计，不额外估算正文。
func confirmedClaudeUsage(e map[string]int) *dto.Usage {
	cu := &dto.ClaudeUsage{InputTokens: e["input_tokens"], OutputTokens: e["output_tokens"], CacheReadInputTokens: e["cache_read_input_tokens"], CacheCreationInputTokens: e["cache_creation_input_tokens"]}
	five, fiveOK := e["cache_creation.ephemeral_5m_input_tokens"]
	hour, hourOK := e["cache_creation.ephemeral_1h_input_tokens"]
	if fiveOK || hourOK {
		cu.CacheCreation = &dto.ClaudeCacheCreationUsage{Ephemeral5mInputTokens: five, Ephemeral1hInputTokens: hour}
		cu.ClaudeCacheCreation5mTokens = five
		cu.ClaudeCacheCreation1hTokens = hour
		if _, ok := e["cache_creation_input_tokens"]; !ok {
			cu.CacheCreationInputTokens = five + hour
		}
	}
	usage := &dto.Usage{PromptTokens: cu.InputTokens, CompletionTokens: cu.OutputTokens, TotalTokens: cu.InputTokens + cu.OutputTokens, UsageSemantic: "anthropic", BillingUsage: dto.NewClaudeMessagesBillingUsage(cu), ClaudeCacheCreation5mTokens: five, ClaudeCacheCreation1hTokens: hour}
	usage.PromptTokensDetails.CachedTokens = cu.CacheReadInputTokens
	usage.PromptTokensDetails.CachedCreationTokens = cu.CacheCreationInputTokens
	return usage
}

// writeClaudeFrame 完整写入一帧并刷新下游，识别短写、刷新失败及取消，写入 panic 转为错误。
// 参数 c：下游请求及响应写入器；raw：待原样写出的完整帧；terminal：是否为允许在受管超时后写出的终止错误。
// 返回 err：写出结果；nil 仅表示服务器写入/刷新检查通过，不证明客户端应用已经读取。
func writeClaudeFrame(c *gin.Context, raw []byte, terminal bool) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("downstream write panic: %v", r)
		}
	}()
	if err = c.Request.Context().Err(); err != nil && !(terminal && service.IsRelayRequestTimeout(c)) {
		return err
	}
	helper.ExtendWriteDeadline(c)
	n, err := c.Writer.Write(raw)
	if err != nil {
		return err
	}
	if n != len(raw) {
		return io.ErrShortWrite
	}
	// 优先通过 FlushError 获取刷新错误；只有普通 Flush 的写入器不提供传输确认，采集到字节不等于发送成功。
	var writer http.ResponseWriter = c.Writer
	// Gin 的 Flush 丢弃底层错误；逐层解除透明包装以调用 FlushError，但帧 Write 仍经过原写入器。
	for depth := 0; depth < 16; depth++ {
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
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	if terminal && service.IsRelayRequestTimeout(c) {
		return nil
	}
	return c.Request.Context().Err()
}

// strictClaudeStream 统一负责原生 Claude SSE 读取、协议校验、下游交付、终止分类和用量选择，不补造成功结束事件。
// 参数 c：下游请求上下文；resp：上游响应或内部连接失败包装；info：本请求中转数据，会写入状态、证据和用量来源。
// 返回结算用量及 nil 中转错误：终止异常已经在流内处理，调用者依 outcome 结算，控制器应跳过重复响应/重试。
func strictClaudeStream(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	result := &relaycommon.ClaudeStreamOutcome{SettlementState: "pending"} // 本请求唯一终止结果，随后交给结算阶段更新。
	result.Diagnostic.UsageEvidence = make(map[string]int)
	result.Diagnostic.UsagePhases = make(map[string]string)
	info.ClaudeStream = result
	info.StreamStatus = relaycommon.NewStreamStatus()
	c.Set(relaycommon.ClaudeStreamHandledKey, true)
	helper.SetEventStreamHeaders(c)
	if info.CaptureClaudeResponse() {
		if info.ClaudeDiagnostic == nil {
			info.ClaudeDiagnostic = relaycommon.NewClaudeResponseCapture(1)
		}
		if resp != nil {
			// 内部错误读取器仅复用终止逻辑，不额外登记为一次上游 HTTP 响应。
			if _, synthetic := resp.Body.(*claudeTransportFailure); !synthetic {
				info.ClaudeDiagnostic.Observe(resp)
			}
		}
	}
	frames := make(chan claudeFrameRead, 1)                        // 单帧有界交接，避免慢下游使响应帧无限堆积。
	readerDone := make(chan struct{})                              // 读取工作者退出信号，返回 Gin 上下文前必须完成等待。
	stopReader, cancel := context.WithCancel(context.Background()) // 仅控制读取工作者，不改变下游取消原因。
	if resp == nil || resp.Body == nil {
		close(readerDone)
		frames <- claudeFrameRead{err: errors.New("missing upstream response body")}
		close(frames)
	} else {
		common.RelayCtxGo(stopReader, func() {
			defer close(readerDone)
			defer close(frames)
			defer func() {
				if r := recover(); r != nil {
					select {
					case frames <- claudeFrameRead{err: fmt.Errorf("upstream reader panic: %v", r)}:
					case <-stopReader.Done():
					}
				}
			}()
			scanner := bufio.NewScanner(resp.Body)
			scanner.Buffer(make([]byte, 4096), maxClaudeFrameBytes)
			splitter := &claudeFrameSplitter{}
			scanner.Split(splitter.Split)
			for scanner.Scan() {
				raw := append([]byte(nil), scanner.Bytes()...)
				select {
				case frames <- claudeFrameRead{raw: raw}:
				case <-stopReader.Done():
					return
				}
			}
			err := scanner.Err()
			if err == nil {
				err = io.EOF
			}
			select {
			case frames <- claudeFrameRead{err: err}:
			case <-stopReader.Done():
			}
		})
	}
	// cleanup 取消读取、幂等关闭上游并等待工作者退出；无参数，快照和上下文归还前均可调用。
	bodyClosed := false // 避免显式清理与 defer 重复关闭上游响应体。
	cleanup := func() {
		cancel()
		if !bodyClosed && resp != nil && resp.Body != nil {
			bodyClosed = true
			_ = resp.Body.Close()
		}
		<-readerDone
	}
	defer cleanup()
	var idle *time.Timer
	var idleC <-chan time.Time
	idleDuration := time.Duration(constant.StreamingTimeout) * time.Second // 未由中间件管理超时时，使用既有秒级空闲超时。
	if !service.IsRelayTimeoutManaged(c) && idleDuration > 0 {
		idle = time.NewTimer(idleDuration)
		idleC = idle.C
		defer idle.Stop()
	}
	var ping *time.Ticker
	var pingC <-chan time.Time
	settings := operation_setting.GetGeneralSetting()
	if settings.PingIntervalEnabled && !info.DisablePing {
		interval := time.Duration(settings.PingIntervalSeconds) * time.Second
		if interval <= 0 {
			interval = helper.DefaultPingInterval
		}
		ping = time.NewTicker(interval)
		pingC = ping.C
		defer ping.Stop()
	}
	protocol := claudeProtocol{blocks: make(map[int]*claudeOpenBlock)} // 仅由本响应处理循环推进。
	reason := relaycommon.StreamEndReasonDone                          // 首个确定的终止原因；清理取消不应覆盖已确认的上游错误。
	var endErr error                                                   // 底层终止错误，只写入私有诊断。
	upstreamError := false                                             // 上游已给出 error 时只透传，不另生成本地错误事件。
	var upstreamErrorFrame []byte                                      // 保存上游 error 帧原始字节，供结束读取后原样写出。
	outputUsageCurrent := false                                        // 最近 message_delta 输出报告后尚无新内容块；显式 0 也算报告。
	estimatedOutput := 0                                               // 已成功交付内容的估算输出 token 累计值。
	var textBuffer strings.Builder                                     // 成功写出后的候选文本，达到 8 KiB 时分批估算并清空。
loop:
	for {
		// 已观察到的下游取消优先于队列中的后续帧，避免继续采用取消后的用量。
		if c.Request.Context().Err() != nil {
			endErr = c.Request.Context().Err()
			reason = relaycommon.StreamEndReasonClientGone
			if service.IsRelayRequestTimeout(c) {
				reason = relaycommon.StreamEndReasonTimeout
			}
			break
		}
		select {
		case <-c.Request.Context().Done():
			continue
		case <-idleC:
			reason = relaycommon.StreamEndReasonTimeout
			endErr = context.DeadlineExceeded
			break loop
		case <-pingC:
			if err := writeClaudeFrame(c, []byte(": PING\n\n"), false); err != nil {
				reason = relaycommon.StreamEndReasonClientGone
				endErr = err
				break loop
			}
		case frame, ok := <-frames:
			if !ok {
				reason = "upstream_read_error"
				endErr = errors.New("upstream reader stopped unexpectedly")
				break loop
			}
			if frame.err != nil {
				endErr = frame.err
				reason = "upstream_read_error"
				if errors.Is(frame.err, context.Canceled) && c.Request.Context().Err() != nil {
					reason = relaycommon.StreamEndReasonClientGone
				}
				if errors.Is(frame.err, io.EOF) || errors.Is(frame.err, errClaudeIncompleteFrame) {
					result.Diagnostic.ReadEOF = true
					reason = "upstream_incomplete"
					endErr = errors.New("upstream ended without a complete message_stop")
				}
				break loop
			}
			if idle != nil {
				if !idle.Stop() {
					select {
					case <-idle.C:
					default:
					}
				}
				idle.Reset(idleDuration)
			}
			event, data := claudeFramePayload(frame.raw)
			if event == "error" {
				upstreamError = true
				reason = "upstream_error"
				endErr = errors.New(claudeErrorDetail(data))
				upstreamErrorFrame = frame.raw
				break loop
			}
			if strings.TrimSpace(data) == "" {
				switch event {
				case "message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop", "ping":
					reason = "upstream_protocol_error"
					endErr = errors.New("known SSE event has no JSON data")
					break loop
				}
				if err := writeClaudeFrame(c, frame.raw, false); err != nil {
					reason = relaycommon.StreamEndReasonClientGone
					endErr = err
					break loop
				}
				continue
			}
			var response dto.ClaudeResponse
			if err := common.UnmarshalJsonStr(data, &response); err != nil {
				reason = "upstream_json_error"
				endErr = err
				break loop
			}
			v := gjson.Parse(data)
			if response.Type == "error" {
				upstreamError = true
				reason = "upstream_error"
				endErr = errors.New(claudeErrorDetail(data))
				upstreamErrorFrame = frame.raw
				break loop
			}
			if event != "" && event != response.Type {
				reason = "upstream_protocol_error"
				endErr = errors.New("SSE event and JSON type disagree")
				break loop
			}
			content, err := protocol.accept(v)
			if err != nil {
				reason = "upstream_protocol_error"
				endErr = err
				break loop
			}
			switch response.Type {
			case "content_block_start", "content_block_delta", "content_block_stop":
				// 用量报告只覆盖此前内容；后续内容块使它失去最终报告资格。
				outputUsageCurrent = false
			}
			usageNode := v.Get("usage")
			if response.Type == "message_start" {
				usageNode = v.Get("message.usage")
			}
			if response.Type == "message_start" || response.Type == "message_delta" {
				if err = mergeClaudeUsage(result.Diagnostic.UsageEvidence, usageNode); err != nil {
					reason = "upstream_json_error"
					endErr = err
					break loop
				}
				if response.Type == "message_delta" && usageNode.Get("output_tokens").Exists() {
					// 用量和 stop_reason 可分属不同 delta 且先后不限；显式 0 是报告，message_start 用量不是最终报告。
					outputUsageCurrent = true
				}
				for key := range result.Diagnostic.UsageEvidence {
					if usageNode.Get(key).Exists() {
						result.Diagnostic.UsagePhases[key] = response.Type
					}
				}
			}
			if response.StopReason != "" {
				maybeMarkClaudeRefusal(c, response.StopReason)
			}
			if response.Delta != nil && response.Delta.StopReason != nil {
				maybeMarkClaudeRefusal(c, *response.Delta.StopReason)
			}
			if response.Type == "message_stop" {
				result.Diagnostic.UsageFinal = outputUsageCurrent
			}
			info.ReceivedResponseCount++
			if response.Type != "ping" {
				info.SetFirstResponseTime()
			}
			if response.Type == "message_start" && response.Message != nil && response.Message.Model != "" {
				info.UpstreamModelName = response.Message.Model
			}
			raw := frame.raw
			if response.Type == "message_delta" && !shouldSkipClaudeMessageDeltaUsagePatch(info) {
				patched := patchClaudeMessageDeltaUsageData(data, buildFinalClaudeUsage(confirmedClaudeUsage(result.Diagnostic.UsageEvidence)))
				raw = []byte("event: message_delta\ndata: " + patched + "\n\n")
			}
			if err = writeClaudeFrame(c, raw, false); err != nil {
				reason = relaycommon.StreamEndReasonClientGone
				endErr = err
				break loop
			}
			countClaudeStreamBillableTools(c, info, &response)
			if strings.TrimSpace(content) != "" {
				result.EffectiveContent = true
			}
			if content != "" {
				textBuffer.WriteString(content)
				if textBuffer.Len() >= 8192 {
					estimatedOutput += service.EstimateTokenByModel(info.UpstreamModelName, textBuffer.String())
					textBuffer.Reset()
				}
			}
			if response.Type == "message_stop" {
				break loop
			}
		}
	}
	// 中间件超时按上游超时处理，与用户取消区分；清理资源前先固定异常分类。
	if reason == relaycommon.StreamEndReasonClientGone && service.IsRelayRequestTimeout(c) {
		reason = relaycommon.StreamEndReasonTimeout
	}
	result.Failed = reason != relaycommon.StreamEndReasonDone
	result.ClientGone = reason == relaycommon.StreamEndReasonClientGone
	if endErr != nil {
		result.Diagnostic.Error = relaycommon.BoundedClaudeDiagnosticError(endErr)
	}
	info.StreamStatus.SetEndReason(reason, nil)
	// 先关闭上游再写终止错误，避免下游慢写继续拖延上游生成。
	cleanup()
	if upstreamError {
		service.WriteRelayTerminalError(c, func() { _ = writeClaudeFrame(c, upstreamErrorFrame, true) })
	}
	if result.Failed && !result.ClientGone && !upstreamError && (c.Request.Context().Err() == nil || service.IsRelayRequestTimeout(c)) {
		// 即使内容块尚未关闭，也只发送官方格式 error，不伪造 stop_reason 或成功结束事件。
		payload, _ := common.Marshal(map[string]any{"type": "error", "error": map[string]string{"type": "api_error", "message": i18n.Translate(i18n.LangEn, i18n.MsgClaudeStreamFailed)}})
		service.WriteRelayTerminalError(c, func() { _ = writeClaudeFrame(c, []byte("event: error\ndata: "+string(payload)+"\n\n"), true) })
	}
	if info.ClaudeDiagnostic != nil {
		snapshot := info.ClaudeDiagnostic.Snapshot()
		result.Diagnostic.ClaudeResponseSnapshot = snapshot.ClaudeResponseSnapshot
		result.Diagnostic.Attempt = snapshot.Attempt
		result.Diagnostic.PreviousResponses = snapshot.PreviousResponses
		result.Diagnostic.OmittedResponses = snapshot.OmittedResponses
	}
	result.Diagnostic.RejectReason = common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason)
	result.ConfirmedUsage = len(result.Diagnostic.UsageEvidence) > 0
	result.UsageSource = result.SelectUsageSource()
	usage := &dto.Usage{UsageSemantic: "anthropic"}
	switch result.UsageSource {
	case "upstream":
		usage = confirmedClaudeUsage(result.Diagnostic.UsageEvidence)
		c.Set("claude_web_search_requests", result.Diagnostic.UsageEvidence["server_tool_use.web_search_requests"])
		// 起始用量或之后仍有内容的报告不是最终输出；只补正常结束的估算，异常/用户取消仍按既定已确认用量结算。
		if !result.Failed && !result.Diagnostic.UsageFinal {
			estimatedOutput += service.EstimateTokenByModel(info.UpstreamModelName, textBuffer.String())
			usage.CompletionTokens = max(usage.CompletionTokens, estimatedOutput)
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
			usage.BillingUsage = dto.NewClaudeMessagesBillingUsage(buildFinalClaudeUsage(usage))
			usage.BillingUsage.Estimated = true
			result.UsageSource = "mixed"
			result.Diagnostic.EstimatedUsage = map[string]int{"output_tokens": estimatedOutput}
		}
	case "estimated":
		estimatedOutput += service.EstimateTokenByModel(info.UpstreamModelName, textBuffer.String())
		usage.PromptTokens = info.GetEstimatePromptTokens()
		if storage := info.ClaudeRequestBody; storage != nil {
			var request dto.ClaudeRequest
			if _, err := storage.Seek(0, io.SeekStart); err == nil {
				if err = common.DecodeJson(storage, &request); err == nil {
					meta := request.GetTokenCountMeta()
					usage.PromptTokens = service.EstimateTokenByModel(info.UpstreamModelName, meta.CombineText)
					// 复用既有 Claude 媒体固定估算值，不为估算访问外部资源。
					for _, file := range meta.Files {
						switch file.FileType {
						case types.FileTypeImage:
							usage.PromptTokens += 520
						case types.FileTypeAudio:
							usage.PromptTokens += 256
						case types.FileTypeVideo:
							usage.PromptTokens += 8192
						default:
							usage.PromptTokens += 4096
						}
					}
				}
			}
		}
		usage.CompletionTokens = estimatedOutput
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		usage.BillingUsage = dto.NewClaudeMessagesBillingUsage(buildFinalClaudeUsage(usage))
		usage.BillingUsage.Estimated = true
		result.Diagnostic.EstimatedUsage = map[string]int{"input_tokens": usage.PromptTokens, "output_tokens": usage.CompletionTokens}
		common.SetContextKey(c, constant.ContextKeyLocalCountTokens, true)
	}
	return usage, nil
}

package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// BeginStreamAttempt 为每次渠道尝试安装独立会话；非流式直接返回，不更改原请求响应或计费。
// 参数 c 为请求上下文，info 为重试复用的中转信息；模式/配置首次冻结，证据每次重建。
func BeginStreamAttempt(c *gin.Context, info *relaycommon.RelayInfo) {
	if !info.UseStreamErrors() {
		return
	}
	// HTTP/SDK 错误日志有时只持有标准 context，隐私标记同步传递但不把 Gin 或尝试会话放入链中。
	if !common.IsPrivateStream(c.Request.Context()) {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), common.StreamPrivateContextKey, true))
	}
	if old, ok := c.Writer.(*relaycommon.StreamWriter); ok {
		c.Writer = old.ResponseWriter
	}
	if old, ok := c.Writer.(*relaycommon.DownstreamCaptureWriter); ok {
		c.Writer = old.ResponseWriter
	}
	info.StreamResult = nil
	info.StreamFinalUsage = nil
	info.StreamUsageFormat = ""
	info.StreamDiagnostic = nil
	info.StreamRejectReason = ""
	// 每次渠道尝试重新等待真实成功响应；采集和终止共用门控，SDK 内部重试可重新激活。
	info.StreamResponseGate = &relaycommon.StreamResponseGate{}
	info.StreamSession = relaycommon.NewStreamSession(info.RelayFormat)
	InitStreamReceivedEstimator(info)
	info.StreamSession.RelayMode = info.RelayMode
	info.StreamSession.ResponseGate = info.StreamResponseGate
	info.StreamSession.BindContext(c.Request.Context())
	if imageRequest, ok := info.Request.(*dto.ImageRequest); ok {
		// 图片入口即使省略 n 也要求一张完成图；0 留给非图片会话，避免 DONE 或通用 JSON 误走图片规则。
		info.StreamSession.ExpectedImages = 1
		if imageRequest.N != nil {
			info.StreamSession.ExpectedImages = max(1, int(*imageRequest.N))
		}
	}
	// 入口 DTO 为未改写的透传保留计数；转换路径发送前用最终 JSON 覆盖，包含字段被删除的情况。
	// 不重新读取或记录请求体，每次尝试创建的新会话不会沿用上次渠道覆盖后的值。
	switch request := info.Request.(type) {
	case *dto.GeneralOpenAIRequest:
		if request.N != nil {
			info.StreamSession.ExpectedChoices = max(0, *request.N)
		}
	case *dto.GeminiChatRequest:
		if request.GenerationConfig.CandidateCount != nil {
			info.StreamSession.ExpectedChoices = max(0, *request.GenerationConfig.CandidateCount)
		}
	}
	// 此时 ChannelMeta 可能仍属于上次重试，缓存字段识别以本次分配到的上下文渠道为准。
	info.StreamSession.ChannelType = common.GetContextKeyInt(c, constant.ContextKeyChannelType)
	info.StreamSession.OpaqueSSE = info.StreamSession.ChannelType == constant.ChannelTypeZhipu
	c.Set(relaycommon.StreamSessionKey, info.StreamSession)
	c.Set(common.StreamPrivateContextKey, true)
	info.StreamStatus = relaycommon.NewStreamStatus()
	c.Set(relaycommon.StreamHandledKey, false)
	c.Set(relaycommon.StreamResponseOnlyKey, true)
	c.Set(relaycommon.StreamResponseCaptureKey, (*relaycommon.StreamResponseCapture)(nil))
	common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "")
	// 尝试编号与响应采集开关解耦，避免无 body 的多次失败诊断互相命中。
	attempt := c.GetInt(relaycommon.StreamDiagnosticAttemptKey) + 1
	c.Set(relaycommon.StreamDiagnosticAttemptKey, attempt)
	if info.CaptureStreamResponse() {
		info.StreamDiagnostic = relaycommon.NewStreamResponseCapture(attempt)
		info.StreamDiagnostic.ResponseGate = info.StreamResponseGate
		c.Set(relaycommon.StreamResponseCaptureKey, info.StreamDiagnostic)
		// 放在语义写入器之下：过滤帧与尚未组成完整事件的数据不计入原始下游正文。
		c.Writer = relaycommon.NewDownstreamCaptureWriter(c.Writer, info.StreamDiagnostic)
	}
	var estimator StreamTokenEstimator // 每次尝试重新累计，刷新批次边界不改变最终估算。
	info.StreamWriter = relaycommon.NewStreamWriter(c.Writer, info.StreamSession, func(text string) int { return estimator.Add(info.UpstreamModelName, text) })
	c.Writer = info.StreamWriter
}

// FinalizeStreamUsage 固定本次结果、最多输出一次终止错误并选择用量；不在此函数执行资金操作。
// 参数 c 为请求/超时上下文，info 为本次会话与输出状态，usage 为旧渠道计量结果。
// 非活动会话原样返回 usage；活动会话正常完成沿用原计量，异常返回确认用量、交付估算或零用量，并冻结结果以免重复终止。
func FinalizeStreamUsage(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage) *dto.Usage {
	if !info.StreamSession.Active() {
		return usage
	}
	if info.StreamFinalUsage != nil {
		return info.StreamFinalUsage
	}
	// 渠道解析器可能按上游 Content-Type 临时切换 IsStream；终止后的费用和日志仍标记入口流式请求。
	info.IsStream = true
	info.StreamWriter.Finish()
	snapshot := info.StreamSession.Snapshot()
	info.StreamUsageFormat = snapshot.UsageFormat
	if !snapshot.Complete && snapshot.Reason == "" && snapshot.ClientErr == nil && c.Request.Context().Err() == nil {
		info.StreamSession.FailRelay("upstream_incomplete", errors.New("upstream ended before protocol completion"))
		snapshot = info.StreamSession.Snapshot()
	}
	reason := snapshot.Reason
	// 已确定的上游错误不被随后连接清理覆盖；上下文取消直接导致的读错误按用户断开处理。
	clientGone := (snapshot.ClientErr != nil || c.Request.Context().Err() != nil) && (snapshot.Reason == "" || errors.Is(snapshot.Err, context.Canceled))
	if IsRelayRequestTimeout(c) {
		reason = relaycommon.StreamEndReasonTimeout
		clientGone = false
	} else if clientGone {
		reason = relaycommon.StreamEndReasonClientGone
	}
	confirmed := len(snapshot.Evidence) > 0
	if _, imageCountOnly := snapshot.Evidence["image_count"]; imageCountOnly && len(snapshot.Evidence) == 1 && !info.PriceData.UsePrice {
		// 张数是完成证据，不是 token 用量；上游异常按交付估算，用户断开按接收估算。
		// 只排除单独 image_count，显式 0 的 token 字段仍属于确认用量。
		confirmed = false
	}
	outcome := &relaycommon.StreamOutcome{Failed: reason != "", ClientGone: clientGone, ReceivedResponse: snapshot.ReceivedResponse, EffectiveContent: snapshot.Effective, ConfirmedUsage: confirmed, SettlementState: "pending"}
	// 诊断依据实际错误来源，不借用 Failed/计费标签，也不依赖是否启用了 body 采集。
	outcome.DiagnosticAvailable = snapshot.DiagnosticAvailable(IsRelayRequestTimeout(c))
	outcome.Diagnostic = info.StreamDiagnostic.Snapshot()
	outcome.Diagnostic.Attempt = c.GetInt(relaycommon.StreamDiagnosticAttemptKey)
	outcome.Diagnostic.UsageEvidence = snapshot.Evidence
	outcome.Diagnostic.UsageFinal = snapshot.Complete
	cause := snapshot.Err
	if reason == relaycommon.StreamEndReasonTimeout {
		cause = context.DeadlineExceeded
	} else if clientGone {
		cause = snapshot.ClientErr
		if cause == nil {
			cause = c.Request.Context().Err()
		}
	}
	if cause != nil {
		outcome.Diagnostic.Error = relaycommon.BoundedStreamDiagnosticError(cause)
	}
	outcome.UsageSource = outcome.SelectUsageSource()
	info.StreamResult = outcome
	info.StreamStatus = relaycommon.NewStreamStatus()
	if reason == "" {
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
	} else {
		info.StreamStatus.SetEndReason(reason, nil)
	}
	// Realtime 正常 response.done 仅结束一轮，连接保留给下一轮；异常才关闭。
	if info.RelayFormat != types.RelayFormatOpenAIRealtime || outcome.Failed {
		info.StreamSession.CloseUpstream()
	}
	if outcome.Failed && !outcome.ClientGone && !snapshot.ErrorDelivered && (c.Request.Context().Err() == nil || IsRelayRequestTimeout(c)) {
		WriteStreamTerminalError(c, info, snapshot)
	}
	selected := usage
	if !outcome.Failed && confirmed && snapshot.EstimatedOutput > 0 {
		// 适配器可能已经估算过零输出；先保留原始确认输入/缓存，再按统一内容口径补估。
		confirmedUsage := BuildConfirmedStreamUsage(info, snapshot.Evidence)
		if confirmedUsage.CompletionTokens == 0 {
			selected = confirmedUsage
			if usage != nil && usage.BillingUsage != nil && !usage.BillingUsage.Estimated && effectiveBillingUsage(usage).CompletionTokens == 0 {
				// 原始嵌套计费对象保留供应商输入模态等细分，补估函数只修改输出。
				selected = usage
			}
		}
	}
	if outcome.Failed {
		switch outcome.UsageSource {
		case "none":
			selected = &dto.Usage{}
		case "upstream":
			selected = BuildConfirmedStreamUsage(info, snapshot.Evidence)
		case "estimated":
			common.SetContextKey(c, constant.ContextKeyLocalCountTokens, true)
			selected = &dto.Usage{PromptTokens: info.GetEstimatePromptTokens(), CompletionTokens: snapshot.EstimatedOutput}
			if clientGone {
				selected.CompletionTokens = snapshot.ReceivedOutput
			}
			if (info.RelayMode == relayconstant.RelayModeImagesGenerations || info.RelayMode == relayconstant.RelayModeImagesEdits) && info.PriceData.UsePrice {
				// 只有预览而无完成用量时按一张估算，不把原请求的多张数量全部收费。
				info.PriceData.AddOtherRatio("n", 1)
				selected.PromptTokens = max(1, selected.PromptTokens)
			}
			selected.TotalTokens = selected.PromptTokens + selected.CompletionTokens
			outcome.Diagnostic.EstimatedUsage = map[string]int{"input_tokens": selected.PromptTokens, "output_tokens": selected.CompletionTokens}
		}
	}
	if selected == nil {
		selected = &dto.Usage{}
	}
	if info.RelayFormat != types.RelayFormatOpenAIRealtime {
		output := snapshot.EstimatedOutput
		if clientGone {
			output = snapshot.ReceivedOutput
		}
		selected = SupplementStreamZeroOutput(c, info, selected, output, 0)
	}
	info.StreamFinalUsage = selected
	c.Set(relaycommon.StreamHandledKey, true)
	return selected
}

// BuildConfirmedStreamUsage 按实际上游语义构造收费对象，绝不把本地估算标成上游用量。
// 参数 info 提供上游协议及计价单位，e 为已验证的累计字段；返回新 Usage，保留缓存/音频细分与显式零，不估算缺失字段。
func BuildConfirmedStreamUsage(info *relaycommon.RelayInfo, e map[string]int) *dto.Usage {
	u := &dto.Usage{PromptTokens: e["input_tokens"], CompletionTokens: e["output_tokens"]}
	if count := e["image_count"]; count > 0 && info.PriceData.UsePrice {
		info.PriceData.AddOtherRatio("n", float64(count))
		// 与既有图片按次计价的单位占位保持一致，不把字节换算为输出 token。
		u.PromptTokens = max(1, u.PromptTokens)
	}
	format := info.StreamUsageFormat
	if format == "" {
		format = info.GetFinalRequestRelayFormat()
	}
	if format == types.RelayFormatGemini {
		u.CompletionTokens += e["reasoning_tokens"]
	}
	u.CompletionTokenDetails.ReasoningTokens = e["reasoning_tokens"]
	u.TotalTokens = u.PromptTokens + u.CompletionTokens
	u.PromptTokensDetails.CachedTokens = e["cached_tokens"]
	u.PromptTokensDetails.CacheWriteTokens = e["cache_write_tokens"]
	u.PromptTokensDetails.CachedCreationTokens = e["cached_creation_tokens"]
	u.PromptTokensDetails.AudioTokens = e["input_audio_tokens"]
	u.PromptTokensDetails.ImageTokens = e["input_image_tokens"]
	u.CompletionTokenDetails.AudioTokens = e["output_audio_tokens"]
	u.CompletionTokenDetails.ImageTokens = e["output_image_tokens"]
	u.PromptTokensDetails.TextTokens = e["input_text_tokens"]
	u.CompletionTokenDetails.TextTokens = e["output_text_tokens"]
	if _, ok := e["input_text_tokens"]; !ok {
		u.PromptTokensDetails.TextTokens = max(0, u.PromptTokens-u.PromptTokensDetails.AudioTokens)
	}
	if _, ok := e["output_text_tokens"]; !ok {
		u.CompletionTokenDetails.TextTokens = max(0, u.CompletionTokens-u.CompletionTokenDetails.AudioTokens)
	}
	if format == types.RelayFormatClaude {
		cu := &dto.ClaudeUsage{InputTokens: u.PromptTokens, OutputTokens: u.CompletionTokens, CacheReadInputTokens: e["cache_read_input_tokens"], CacheCreationInputTokens: e["cache_creation_input_tokens"]}
		five, hour := e["cache_creation.ephemeral_5m_input_tokens"], e["cache_creation.ephemeral_1h_input_tokens"]
		if _, ok := e["cache_creation_input_tokens"]; !ok {
			cu.CacheCreationInputTokens = five + hour
		}
		cu.CacheCreation = &dto.ClaudeCacheCreationUsage{Ephemeral5mInputTokens: five, Ephemeral1hInputTokens: hour}
		u.ClaudeCacheCreation5mTokens = five
		u.ClaudeCacheCreation1hTokens = hour
		u.UsageSemantic = "anthropic"
		u.PromptTokensDetails.CachedTokens = cu.CacheReadInputTokens
		u.PromptTokensDetails.CachedCreationTokens = cu.CacheCreationInputTokens
		u.BillingUsage = dto.NewClaudeMessagesBillingUsage(cu)
	}
	return u
}

// WriteStreamTerminalError 按下游协议写终止错误；调用方负责一次性判断，本方法自身不设置去重标记。
// 参数 c 提供写入/超时上下文，info 决定下游协议和成功响应资格，snapshot 保存上游原错误帧/载荷与协议结果。
// 同协议且形状兼容时可原样发送上游错误；本地补发使用公开提示。裸媒体已经写出时直接返回，由调用方关闭流，不插入 SSE。
// SDK 原 JSON 只添加 SSE 外壳（换行规范化并逐行加 data 字段），Realtime 原载荷逐字节写入 WS。
func WriteStreamTerminalError(c *gin.Context, info *relaycommon.RelayInfo, snapshot relaycommon.StreamSnapshot) {
	if !info.StreamResponseGate.AllowsStream() {
		return // 未取得成功上游响应，原控制器负责错误格式与 HTTP 状态。
	}
	if c.Writer.Written() && relaycommon.IsStreamBinaryContentType(c.Writer.Header().Get("Content-Type")) {
		return
	}
	message := i18n.Translate(i18n.LangEn, i18n.MsgClaudeStreamFailed)
	payload := map[string]any{"error": map[string]any{"type": "api_error", "message": message, "code": "upstream_stream_error"}}
	event := "error"
	switch info.RelayFormat {
	case types.RelayFormatOpenAIRealtime:
		payload = map[string]any{"type": "error", "error": map[string]any{"type": "server_error", "code": "upstream_stream_error", "message": message}}
	case types.RelayFormatClaude:
		payload = map[string]any{"type": "error", "error": map[string]string{"type": "api_error", "message": message}}
	case types.RelayFormatOpenAIResponses:
		payload = map[string]any{"type": "error", "code": "upstream_stream_error", "message": message, "param": nil}
	case types.RelayFormatGemini:
		payload = map[string]any{"error": map[string]any{"code": 502, "status": "UNAVAILABLE", "message": message}}
	}
	data, _ := common.Marshal(payload)
	frame := []byte("event: " + event + "\ndata: " + string(data) + "\n\n")
	sdkPayload := false // 原 SDK 载荷在终止写入时用固定缓冲添加外壳，不分配换行放大后的完整 SSE 帧。
	if (len(snapshot.ErrorFrame) > 0 || len(snapshot.ErrorPayload) > 0) && info.GetFinalRequestRelayFormat() == info.RelayFormat {
		// 旧原生适配器未必登记转换链，进一步检查错误形状，避免把讯飞 header.code 等当作 OpenAI error。
		originalEvent, originalData := "error", snapshot.ErrorPayload
		if len(snapshot.ErrorFrame) > 0 {
			originalEvent, originalData = relaycommon.StreamFramePayload(snapshot.ErrorFrame)
		}
		v := gjson.ParseBytes(originalData)
		compatible := originalEvent == "error" && !gjson.ValidBytes(originalData)
		switch info.RelayFormat {
		case types.RelayFormatOpenAIRealtime:
			compatible = gjson.ValidBytes(originalData) && v.IsObject() && relaycommon.IsStreamErrorEvent("", originalData) && !relaycommon.IsRealtimeRecoverableError(v)
		case types.RelayFormatOpenAI:
			compatible = compatible || v.Get("error").IsObject() && v.Get("error.message").Type == gjson.String
		case types.RelayFormatClaude:
			compatible = compatible || v.Get("type").String() == "error" && v.Get("error").IsObject()
		case types.RelayFormatGemini:
			compatible = compatible || v.Get("error").IsObject() && v.Get("error.code").Type == gjson.Number
		case types.RelayFormatOpenAIResponses:
			compatible = compatible || strings.HasPrefix(v.Get("type").String(), "response.") || v.Get("type").String() == "error" && v.Get("message").Type == gjson.String
		}
		if compatible {
			data = originalData // WS 直接使用原始载荷，不再发送此前构造的通用错误 JSON。
			if len(snapshot.ErrorFrame) > 0 {
				frame = snapshot.ErrorFrame
			} else if info.RelayFormat != types.RelayFormatOpenAIRealtime {
				sdkPayload = true
			}
		}
	}
	WriteRelayTerminalError(c, func() {
		if info.RelayFormat == types.RelayFormatOpenAIRealtime && info.ClientWs != nil {
			_ = info.ClientWs.SetWriteDeadline(time.Now().Add(15 * time.Second))
			if err := info.ClientWs.WriteMessage(1, data); err == nil {
				info.StreamDiagnostic.WriteDownstreamPayload(data)
			}
			return
		}
		writer := c.Writer
		if w, ok := writer.(*relaycommon.StreamWriter); ok {
			writer = w.ResponseWriter
		}
		writer.Header().Del("Content-Length")
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set("Cache-Control", "no-cache")
		// 终止错误可能晚于最后正文数分钟；仅续期本次专用写入，不恢复超时后的普通输出。
		_ = http.NewResponseController(writer).SetWriteDeadline(time.Now().Add(relaycommon.StreamWriteTimeout))
		if sdkPayload {
			// 固定 4 KiB 编码缓冲：多行只扩展线上 data 字段，不在内存中聚合放大后的全帧。
			encoded := bufio.NewWriterSize(writer, 4096)
			_, _ = encoded.WriteString("event: error\ndata: ")
			remaining := data
			for len(remaining) > 0 {
				i := bytes.IndexAny(remaining, "\r\n")
				if i < 0 {
					if _, err := encoded.Write(remaining); err != nil {
						return
					}
					break
				}
				if _, err := encoded.Write(remaining[:i]); err != nil {
					return
				}
				end := i + 1
				if remaining[i] == '\r' && end < len(remaining) && remaining[end] == '\n' {
					end++
				}
				if _, err := encoded.WriteString("\ndata: "); err != nil {
					return
				}
				remaining = remaining[end:]
			}
			_, _ = encoded.WriteString("\n\n")
			if err := encoded.Flush(); err != nil {
				return // 编码器保留 Write 的首个错误，终止失败后不再刷新或补写第二次。
			}
		} else if _, err := writer.Write(frame); err != nil {
			return
		}
		_ = http.NewResponseController(writer).Flush()
	})
}

// FinalizeStreamFailure 为尚未进入计费入口的活动流选择用量并结算；真实成功响应门控未开放或专用处理器接管时返回 false。
// 参数 c 提供请求与响应状态，info 保存会话/资金信息，err 为当前中转错误（可为 nil）；true 表示已接管或此前已尝试结算。
func FinalizeStreamFailure(c *gin.Context, info *relaycommon.RelayInfo, err error) bool {
	if !info.StreamSession.Active() {
		return false
	}
	if info.StreamResult != nil && info.StreamResult.SettlementAttempted {
		return true
	}
	if err != nil {
		var apiErr *types.NewAPIError
		if errors.As(err, &apiErr) && !c.Writer.Written() && apiErr.StatusCode >= 400 {
			c.Status(apiErr.StatusCode)
		}
		if c.Request.Context().Err() != nil {
			info.StreamSession.FailRelay("upstream_read_error", c.Request.Context().Err())
		} else {
			info.StreamSession.FailRelay("upstream_read_error", err)
		}
	}
	if err == nil && c.Request.Context().Err() == nil && !info.StreamSession.Snapshot().Complete && info.StreamSession.Snapshot().ClientErr == nil {
		info.StreamSession.FailRelay("upstream_incomplete", errors.New("stream handler returned without protocol completion"))
	}
	usage := FinalizeStreamUsage(c, info, nil)
	if usage.PromptTokensDetails.AudioTokens > 0 || usage.CompletionTokenDetails.AudioTokens > 0 {
		PostAudioConsumeQuota(c, info, usage, "")
	} else {
		PostTextConsumeQuota(c, info, usage, nil)
	}
	return true
}

package openai

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
)

// realtimeStreamAccounting 在单个所有者上累计各轮选择后的用量，已完成轮次不因后续错误被退款。
type realtimeStreamAccounting struct {
	usage     *dto.RealtimeUsage         // 最终结算累计，不做旧的逐轮实际扣款。
	evidence  map[string]int             // 上游各轮确认用量的累计，和估算合计分离。
	estimated map[string]int             // 已收费估算轮次的累计明细，不把它混入确认映射。
	sources   map[string]bool            // 出现的收费来源，最终生成 upstream/estimated/mixed/none。
	effective bool                       // 任一轮成功交付过有效内容。
	last      *relaycommon.StreamOutcome // 最后一次终止快照，承载底层错误和响应诊断。
	seen      map[[32]byte]bool          // 最近 response ID 的定长摘要集，不持有 JSON 子串或任意长原 ID。
	ids       [128][32]byte              // 固定 128 个摘要的淘汰环，轮次之间保留、连接之间隔离。
	next      int                        // 环形写入位置。
}

// newRealtimeStreamAccounting 创建连接局部账本，不创建资金会话或共享缓存。
func newRealtimeStreamAccounting() *realtimeStreamAccounting {
	return &realtimeStreamAccounting{usage: &dto.RealtimeUsage{}, evidence: map[string]int{}, estimated: map[string]int{}, sources: map[string]bool{}, seen: map[[32]byte]bool{}}
}

// remember 判断供应商响应 id 是否未在最近 128 个不同 ID 中出现；空 ID 不去重，重复 ID 不推进淘汰环。
// 仅保留 SHA-256 摘要，使缓存字节数有界并释放解析器返回的 JSON 子串引用。
func (a *realtimeStreamAccounting) remember(id string) bool {
	if id == "" {
		return true
	}
	key := sha256.Sum256([]byte(id))
	if a.seen[key] {
		return false
	}
	delete(a.seen, a.ids[a.next])
	a.ids[a.next] = key
	a.next = (a.next + 1) % len(a.ids)
	a.seen[key] = true
	return true
}

// finishRound 按当前轮次结果选择确认/估算/零用量，累加到账本并补充同一资金会话预留；不执行最终扣款。
// 参数 c 为连接请求上下文，local 提供本轮已转发输入及已交付音频估算，info 的会话提供已交付文本估算；返回追加预留错误。
// 正常后续轮次选用估算时，按 info 中本轮首轮标记和工具配置补入工具定义文本输入；确认用量及异常轮次不追加。
func (a *realtimeStreamAccounting) finishRound(c *gin.Context, info *relaycommon.RelayInfo, local *dto.RealtimeUsage) error {
	snapshot := info.StreamSession.Snapshot()
	info.SetEstimatePromptTokens(local.InputTokens)
	// 输入与输出分开构建：文本只取已交付累计估算，音频只取成功写出的音频计量，避免旧 local 遗漏文本后覆盖。
	output := snapshot.EstimatedOutput + local.OutputTokenDetails.AudioTokens
	original := &dto.Usage{PromptTokens: local.InputTokens, CompletionTokens: output, TotalTokens: local.InputTokens + output}
	original.PromptTokensDetails.TextTokens = local.InputTokenDetails.TextTokens
	original.PromptTokensDetails.AudioTokens = local.InputTokenDetails.AudioTokens
	original.CompletionTokenDetails.TextTokens = snapshot.EstimatedOutput
	original.CompletionTokenDetails.AudioTokens = local.OutputTokenDetails.AudioTokens
	estimated := original // 仅在最终策略明确采用估算时使用，确认零与 none 分支保持原选择。
	if len(snapshot.Evidence) > 0 {
		original = service.BuildConfirmedStreamUsage(info, snapshot.Evidence)
	}
	selected := service.FinalizeStreamUsage(c, info, original)
	if info.StreamResult.UsageSource == "estimated" {
		selected = estimated
		if info.StreamResult.ClientGone {
			selected.CompletionTokenDetails.TextTokens = snapshot.ReceivedOutput
			selected.CompletionTokenDetails.AudioTokens = max(snapshot.ReceivedAudioOutput, local.OutputTokenDetails.AudioTokens)
			selected.CompletionTokens = snapshot.ReceivedOutput + selected.CompletionTokenDetails.AudioTokens
			selected.TotalTokens = selected.PromptTokens + selected.CompletionTokens
		}
		if snapshot.Complete && !info.StreamResult.Failed && !info.IsFirstRequest && len(info.RealtimeTools) > 0 {
			// 复用旧 response.done 的工具计数口径；固定分支仅计算文本，没有音频解码或返回错误的路径。
			toolTokens, _, _ := service.CountTokenRealtime(info, dto.RealtimeEvent{Type: dto.RealtimeEventTypeResponseDone}, info.UpstreamModelName)
			selected.PromptTokens += toolTokens
			selected.PromptTokensDetails.TextTokens += toolTokens
			selected.TotalTokens = selected.PromptTokens + selected.CompletionTokens
		}
		info.SetEstimatePromptTokens(selected.PromptTokens)
		info.StreamFinalUsage = selected
		info.StreamResult.Diagnostic.EstimatedUsage = map[string]int{"input_tokens": selected.PromptTokens, "output_tokens": selected.CompletionTokens, "input_audio_tokens": selected.PromptTokensDetails.AudioTokens, "output_audio_tokens": selected.CompletionTokenDetails.AudioTokens}
	}
	textOutput, audioOutput := snapshot.EstimatedOutput, local.OutputTokenDetails.AudioTokens
	if info.StreamResult.ClientGone {
		textOutput = snapshot.ReceivedOutput
		audioOutput = max(snapshot.ReceivedAudioOutput, audioOutput)
	}
	selected = service.SupplementStreamZeroOutput(c, info, selected, textOutput, audioOutput)
	info.StreamFinalUsage = selected
	a.usage.InputTokens += selected.PromptTokens
	a.usage.OutputTokens += selected.CompletionTokens
	a.usage.TotalTokens = a.usage.InputTokens + a.usage.OutputTokens
	a.usage.InputTokenDetails.TextTokens += selected.PromptTokensDetails.TextTokens
	a.usage.InputTokenDetails.AudioTokens += selected.PromptTokensDetails.AudioTokens
	a.usage.InputTokenDetails.CachedTokens += selected.PromptTokensDetails.CachedTokens
	a.usage.OutputTokenDetails.TextTokens += selected.CompletionTokenDetails.TextTokens
	a.usage.OutputTokenDetails.AudioTokens += selected.CompletionTokenDetails.AudioTokens
	for key, n := range snapshot.Evidence {
		a.evidence[key] += n
	}
	if info.StreamResult.UsageSource != "none" {
		a.sources[info.StreamResult.UsageSource] = true
	}
	a.effective = a.effective || snapshot.Effective
	for key, n := range info.StreamResult.Diagnostic.EstimatedUsage {
		a.estimated[key] += n
	}
	a.last = info.StreamResult
	return service.ReserveRealtimeStreamUsage(info, a.usage)
}

// realtimeStreamRead 传递原始 WS 消息，读取工作者不解析、不计费、不访问可复用 Gin 上下文。
type realtimeStreamRead struct {
	client bool   // true 表示来自下游连接，false 表示来自上游连接。
	data   []byte // 原始消息负载，方向由 client 决定，不包含 WS 帧头。
	err    error  // 本次连接读取错误，交给单一所有者分类和终止。
}

// managedRealtimeHandler 使用两个有界读取者和一个写入/计费所有者管理双向连接。
// 参数 c 为连接请求上下文，info 包含两端 WS 与已通过握手的会话；返回 nil 中转错误和累计选定用量，终止原因保存在 info。
// 结束时关闭连接并等待读取者；对最近 128 个非空 response ID 去重，空 ID 或已淘汰的 ID 不享有永久去重保证。
// 已完成轮次之间发生上游异常时仅补发终止错误，保留原累计账本，不虚构新轮次或再次预留。
// 首轮开始前已透传可恢复请求错误且上游正常关闭时，保留空账本；新的请求或回答会取消此例外。
func managedRealtimeHandler(c *gin.Context, info *relaycommon.RelayInfo) (*types.NewAPIError, *dto.RealtimeUsage) {
	info.IsStream = true
	stop, cancel := context.WithCancel(c.Request.Context())
	reads := make(chan realtimeStreamRead, 2)
	var wg sync.WaitGroup
	for _, peer := range []struct {
		conn   *websocket.Conn
		client bool
	}{{info.ClientWs, true}, {info.TargetWs, false}} {
		conn, client := peer.conn, peer.client
		conn.SetReadLimit(relaycommon.MaxStreamFrameBytes)
		wg.Add(1)
		common.RelayCtxGo(stop, func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					select {
					case reads <- realtimeStreamRead{client: client, err: fmt.Errorf("websocket reader panic: %v", r)}:
					case <-stop.Done():
					}
				}
			}()
			for {
				_, data, err := conn.ReadMessage()
				select {
				case reads <- realtimeStreamRead{client: client, data: data, err: err}:
				case <-stop.Done():
					return
				}
				if err != nil {
					return
				}
			}
		})
	}
	defer func() { cancel(); _ = info.TargetWs.Close(); _ = info.ClientWs.Close(); wg.Wait() }()
	a := newRealtimeStreamAccounting()
	local := &dto.RealtimeUsage{}
	var outputEstimator service.StreamTokenEstimator // 每轮独立；同一文本的 WS 分片方式不改变收费。
	active := false
	roundStarted := false     // 上游已经开始本轮；区别于尚待上游接受的客户端 response.create。
	idleRequestError := false // 尚无回答时已透传可恢复请求错误，允许正常关闭时首轮账本仍为空。
	var idle *time.Timer
	var idleC <-chan time.Time
	duration := time.Duration(constant.StreamingTimeout) * time.Second
	if duration > 0 {
		idle = time.NewTimer(duration)
		idleC = idle.C
		defer idle.Stop()
	}
	clientGone := false
	var endErr error
loop:
	for {
		select {
		case <-stop.Done():
			clientGone = true
			endErr = stop.Err()
			info.StreamSession.ClientFailed(endErr)
			break loop
		case <-idleC:
			endErr = context.DeadlineExceeded
			info.StreamSession.Fail(relaycommon.StreamEndReasonTimeout, endErr)
			break loop
		case frame := <-reads:
			if frame.err != nil {
				endErr = frame.err
				clientGone = frame.client
				if clientGone {
					info.StreamSession.ClientFailed(endErr)
				} else if active {
					info.StreamSession.EndRead(frame.err)
				} else if websocket.IsCloseError(frame.err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					endErr = nil
				} else {
					// 轮次之间的异常断链同样有明确上游来源，不借用最终兜底计费标签。
					info.StreamSession.EndRead(frame.err)
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
				idle.Reset(duration)
			}
			var event dto.RealtimeEvent
			if err := common.Unmarshal(frame.data, &event); err != nil {
				endErr = err
				if frame.client {
					info.StreamSession.ClientFailed(err)
					clientGone = true
				} else {
					info.StreamDiagnostic.WriteStreamPayload(frame.data)
					// 嵌套 usage 类型错误可能先被 DTO 拦下；仍先保存原终止 error，再由观察器拒绝其非法用量。
					if relaycommon.IsStreamErrorEvent("", frame.data) {
						_ = info.StreamSession.ObserveEvent("", frame.data)
					}
					info.StreamSession.Fail("upstream_json_error", err)
				}
				active = true
				break loop
			}
			if frame.client {
				// 部分 session.update 省略/null tools 时保留配置；显式 [] 清空，非空数组替换，其他事件不更新工具。
				if event.Type == dto.RealtimeEventTypeSessionUpdate && event.Session != nil && event.Session.Tools != nil {
					info.RealtimeTools = event.Session.Tools
				}
				_ = info.TargetWs.SetWriteDeadline(time.Now().Add(15 * time.Second))
				if err := info.TargetWs.WriteMessage(websocket.TextMessage, frame.data); err != nil {
					info.StreamSession.EndRead(err)
					endErr = err
					active = true
					break loop
				}
				text, audio, err := service.CountTokenRealtime(info, event, info.UpstreamModelName)
				if err != nil {
					endErr = err
					info.StreamSession.Fail("response_conversion_error", err)
					active = true
					break loop
				}
				local.InputTokens += text + audio
				local.InputTokenDetails.TextTokens += text
				local.InputTokenDetails.AudioTokens += audio
				local.TotalTokens = local.InputTokens + local.OutputTokens
				if event.Type == dto.RealtimeEventTypeResponseCreate {
					active = true
					idleRequestError = false // 新请求重新等待回答；此前请求错误不豁免本次缺失的响应。
				}
				continue
			}
			info.StreamDiagnostic.WriteStreamPayload(frame.data)
			info.SetFirstResponseTime()
			if event.Type == dto.RealtimeEventTypeSessionCreated || event.Type == dto.RealtimeEventTypeSessionUpdated {
				if event.Session != nil {
					info.InputAudioFormat = common.GetStringIfEmpty(event.Session.InputAudioFormat, info.InputAudioFormat)
					info.OutputAudioFormat = common.GetStringIfEmpty(event.Session.OutputAudioFormat, info.OutputAudioFormat)
				}
			}
			value := gjson.ParseBytes(frame.data)
			recoverable := event.Type == "error" && relaycommon.IsRealtimeRecoverableError(value)
			id := value.Get("response.id").String()
			duplicate := event.Type == dto.RealtimeEventTypeResponseDone && !a.remember(id)
			if !duplicate {
				if err := info.StreamSession.ObserveEvent("", frame.data); err != nil {
					endErr = err
					active = true
					break loop
				}
			}
			receivedAudio := 0
			var audioErr error
			if !duplicate && (event.Type == "response.audio.delta" || event.Type == "response.output_audio.delta") {
				receivedAudio, audioErr = service.CountAudioTokenOutput(event.Delta, info.OutputAudioFormat)
				if audioErr == nil {
					info.StreamSession.RecordReceivedMedia(receivedAudio)
				}
			}
			_ = info.ClientWs.SetWriteDeadline(time.Now().Add(15 * time.Second))
			if err := info.ClientWs.WriteMessage(websocket.TextMessage, frame.data); err != nil {
				info.StreamSession.ClientFailed(err)
				endErr = err
				clientGone = true
				active = true
				break loop
			}
			// 去重仅影响用量处理；已实际转发的重复帧也属于下游原始正文。
			info.StreamDiagnostic.WriteDownstreamPayload(frame.data)
			if recoverable {
				// 未开始回答时的请求错误不虚构失败轮次；活动回答的状态与计量保持。
				if !roundStarted {
					active = false
					idleRequestError = true
				}
				continue
			}
			if duplicate {
				continue
			}
			info.StreamSession.CommitDelivery(frame.data)
			text := info.StreamSession.TakeDeliveredText()
			if text != "" {
				info.StreamSession.AddEstimatedOutput(outputEstimator.Add(info.UpstreamModelName, text))
			}
			if strings.HasPrefix(event.Type, "response.") {
				active = true
				roundStarted = true
				idleRequestError = false
			}
			if event.Type == "response.audio.delta" || event.Type == "response.output_audio.delta" {
				// 沿用原音频算法；文本/转写/完整工具参数由交付观察器累计，避免再次逐 delta 相加。
				if audioErr != nil {
					endErr = audioErr
					info.StreamSession.Fail("response_conversion_error", audioErr)
					break loop
				}
				local.OutputTokens += receivedAudio
				local.OutputTokenDetails.AudioTokens += receivedAudio
				local.TotalTokens = local.InputTokens + local.OutputTokens
			}
			if state := info.StreamSession.Snapshot(); state.Reason == "upstream_error" {
				info.StreamSession.MarkErrorDelivered()
				active = true
				endErr = state.Err
				break loop
			}
			if event.Type == dto.RealtimeEventTypeResponseDone {
				if err := a.finishRound(c, info, local); err != nil {
					endErr = err
					info.StreamSession.Fail("settlement_reservation_error", err)
					service.WriteStreamTerminalError(c, info, info.StreamSession.Snapshot())
					active = false
					break loop
				}
				// 先按本轮身份完成估算/确认选量，再进入后续轮次，避免首轮被错误追加工具定义。
				info.IsFirstRequest = false
				info.StreamSession.Disable()
				info.StreamSession = relaycommon.NewStreamSession(types.RelayFormatOpenAIRealtime)
				service.InitStreamReceivedEstimator(info)
				info.StreamSession.ResponseGate = info.StreamResponseGate // 同一 WS 的后续轮次继承已成功的握手资格。
				info.StreamSession.BindContext(c.Request.Context())
				info.StreamSession.BindUpstream(info.TargetWs)
				info.StreamFinalUsage = nil
				info.StreamResult = nil
				local = &dto.RealtimeUsage{}
				outputEstimator = service.StreamTokenEstimator{}
				active = false
				roundStarted = false
			}
		}
	}
	// 首轮请求已被拒绝且上游正常关闭时没有待结算回答；真实断链、超时和空握手仍保留既有失败策略。
	if active || (a.last == nil && !(idleRequestError && endErr == nil)) {
		if !clientGone && endErr != nil && info.StreamSession.Snapshot().Reason == "" {
			info.StreamSession.EndRead(endErr)
		}
		_ = a.finishRound(c, info, local)
	} else if endErr != nil {
		// 已有完成轮次且当前无活动轮次：只处理连接终止，避免调用 finishRound 重复累计或预留。
		snapshot := info.StreamSession.Snapshot()
		ownedTimeout := service.IsRelayRequestTimeout(c)
		if ownedTimeout && snapshot.Reason == "" {
			// 受管超时也会取消 context；轮间按 timeout 展示并允许专用错误写入，而非误标用户主动断开。
			clientGone = false
			endErr = context.DeadlineExceeded
		}
		if !clientGone && !snapshot.ErrorDelivered && snapshot.DiagnosticAvailable(ownedTimeout) && (c.Request.Context().Err() == nil || ownedTimeout) {
			// 来源门槛排除已在循环内补发的预留失败；活动轮次和上游原 error 仍由原 finishRound 分支处理。
			service.WriteStreamTerminalError(c, info, snapshot)
		}
	}
	result := a.last
	if result == nil {
		result = &relaycommon.StreamOutcome{SettlementState: "pending"}
	}
	roundError := result.Diagnostic.Error
	endingReason := info.StreamSession.Snapshot().Reason
	if endingReason == "" && info.StreamStatus != nil {
		endingReason = info.StreamStatus.EndReason
	}
	result.Diagnostic = info.StreamDiagnostic.Snapshot()
	result.Diagnostic.Attempt = c.GetInt(relaycommon.StreamDiagnosticAttemptKey)
	result.DiagnosticAvailable = result.DiagnosticAvailable || info.StreamSession.Snapshot().DiagnosticAvailable(service.IsRelayRequestTimeout(c))
	result.Diagnostic.UsageEvidence = a.evidence
	result.Diagnostic.EstimatedUsage = a.estimated
	result.Diagnostic.Error = relaycommon.BoundedStreamDiagnosticError(endErr)
	if endErr == nil {
		result.Diagnostic.Error = roundError
	}
	result.ClientGone = clientGone
	result.Failed = result.Failed || endErr != nil
	result.EffectiveContent = a.effective
	result.ConfirmedUsage = len(a.evidence) > 0
	result.SettlementAttempted = false
	result.UsageSource = "none"
	for source := range a.sources {
		result.UsageSource = source
	}
	if len(a.sources) > 1 {
		result.UsageSource = "mixed"
	}
	info.StreamResult = result
	info.StreamSession.Disable()
	info.StreamStatus = relaycommon.NewStreamStatus()
	reason := relaycommon.StreamEndReasonDone
	if clientGone {
		reason = relaycommon.StreamEndReasonClientGone
	} else if errors.Is(endErr, context.DeadlineExceeded) {
		reason = relaycommon.StreamEndReasonTimeout
	} else if result.Failed {
		reason = endingReason
		if reason == "" || reason == relaycommon.StreamEndReasonDone {
			reason = "upstream_read_error"
		}
	}
	info.StreamStatus.SetEndReason(reason, nil)
	c.Set(relaycommon.StreamHandledKey, true)
	return nil, a.usage
}

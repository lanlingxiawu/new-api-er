package openai

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// addRealtimeUsage preserves the provider's totals, with the same nonnegative
// integer boundary enforced for both local estimates and confirmed round usage.
// Validate all fields before updating any field; a rejected round cannot corrupt
// the previously accumulated connection total.
func addRealtimeUsage(total, round *dto.RealtimeUsage) error {
	fields := [][2]*int{
		{&total.TotalTokens, &round.TotalTokens}, {&total.InputTokens, &round.InputTokens}, {&total.OutputTokens, &round.OutputTokens},
		{&total.InputTokenDetails.TextTokens, &round.InputTokenDetails.TextTokens}, {&total.InputTokenDetails.AudioTokens, &round.InputTokenDetails.AudioTokens},
		{&total.InputTokenDetails.CachedTokens, &round.InputTokenDetails.CachedTokens},
		{&total.OutputTokenDetails.TextTokens, &round.OutputTokenDetails.TextTokens}, {&total.OutputTokenDetails.AudioTokens, &round.OutputTokenDetails.AudioTokens},
	}
	for _, f := range fields {
		if *f[1] < 0 || *f[0] > math.MaxInt-*f[1] {
			return errors.New("invalid realtime usage range")
		}
	}
	for _, f := range fields {
		*f[0] += *f[1]
	}
	return nil
}

// mainRealtimeHandler uses main's DTO/counting rules under the same single
// connection owner as strict mode. Each round uses main's direct debit, followed
// by main's final settlement. Cumulative reservations would charge less than main.
// It never calls the strict finalizer or manufactures a terminal error event.
func mainRealtimeHandler(c *gin.Context, info *relaycommon.RelayInfo) (*types.NewAPIError, *dto.RealtimeUsage) {
	info.IsStream = true
	stop, reads, cleanup := startRealtimeRelayReaders(c, info)
	defer cleanup()
	usage := &dto.RealtimeUsage{}
	local := &dto.RealtimeUsage{}
	var pending *dto.RealtimeUsage
	var terminal error
	reason := relaycommon.StreamEndReasonHandlerStop
	// Main permits pauses between WebSocket frames. Its SSE scanner timeout
	// must not end a compatibility session before the next billable round.
loop:
	for {
		select {
		case <-stop.Done():
			terminal = stop.Err()
			reason = relaycommon.StreamEndReasonClientGone
			if errors.Is(terminal, context.DeadlineExceeded) || service.IsRelayRequestTimeout(c) {
				reason = relaycommon.StreamEndReasonTimeout
			}
			info.StreamSession.Termination.ObserveContext()
			break loop
		case frame := <-reads:
			if frame.err != nil {
				if !websocket.IsCloseError(frame.err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					terminal = frame.err
					reason = relaycommon.StreamEndReasonScannerErr
					if frame.client {
						reason = relaycommon.StreamEndReasonClientGone
					}
					if frame.readerPanic {
						reason = relaycommon.StreamEndReasonPanic
					}
				}
				if frame.client {
					info.StreamSession.Termination.Observe("downstream", "read", frame.err)
				} else {
					info.StreamSession.Termination.ObserveUpstreamRead(frame.err)
				}
				break loop
			}
			if !frame.client {
				info.StreamDiagnostic.WriteStreamPayload(frame.data)
				info.SetFirstResponseTime()
			}
			var event dto.RealtimeEvent
			if err := common.Unmarshal(frame.data, &event); err != nil {
				terminal = err
				actor := "upstream"
				if frame.client {
					actor = "downstream"
				}
				info.StreamSession.Termination.Observe(actor, "protocol", err)
				break loop
			}
			if frame.client && event.Type == dto.RealtimeEventTypeSessionUpdate && event.Session != nil && event.Session.Tools != nil {
				info.RealtimeTools = event.Session.Tools
			}
			if !frame.client && event.Type == "error" {
				// A recoverable provider error remains a forwarded event. Recording
				// it must not turn it into a strict terminal or change the ledger.
				providerErr := errors.New("upstream websocket error")
				info.StreamDiagnostic.SetError(providerErr)
				info.StreamSession.Termination.Observe("upstream", "provider_error", providerErr)
				info.StreamStatus.RecordError("upstream websocket error")
			}
			if !frame.client && event.Type == dto.RealtimeEventTypeResponseDone {
				if event.Response == nil {
					terminal = errors.New("response.done missing response")
					break loop
				}
				// Main counts every response.done, including repeated IDs.
				// Deduplication belongs exclusively to the strict policy.
				round := event.Response.Usage
				if round == nil {
					text, audio, err := service.CountTokenRealtime(info, event, info.UpstreamModelName)
					if err != nil {
						terminal = err
						break loop
					}
					info.IsFirstRequest = false
					increment := &dto.RealtimeUsage{TotalTokens: text + audio, InputTokens: text + audio}
					increment.InputTokenDetails.TextTokens, increment.InputTokenDetails.AudioTokens = text, audio
					if err := addRealtimeUsage(local, increment); err != nil {
						terminal = err
						break loop
					}
					round = local
				}
				if err := addRealtimeUsage(usage, round); err != nil {
					terminal = err
					break loop
				}
				if err := service.PreWssConsumeQuota(c, info, round); err != nil {
					// Main leaves a failed provider round pending and attempts it
					// again at close; its accumulated counts include both attempts.
					if event.Response.Usage != nil {
						pending = round
					}
					terminal = err
					break loop
				}
				local = &dto.RealtimeUsage{}
			} else if !frame.client && (event.Type == dto.RealtimeEventTypeSessionCreated || event.Type == dto.RealtimeEventTypeSessionUpdated) {
				if event.Session != nil {
					info.InputAudioFormat = common.GetStringIfEmpty(event.Session.InputAudioFormat, info.InputAudioFormat)
					info.OutputAudioFormat = common.GetStringIfEmpty(event.Session.OutputAudioFormat, info.OutputAudioFormat)
				}
			} else {
				text, audio, err := service.CountTokenRealtime(info, event, info.UpstreamModelName)
				if err != nil {
					terminal = err
					break loop
				}
				increment := &dto.RealtimeUsage{TotalTokens: text + audio}
				if frame.client {
					increment.InputTokens = text + audio
					increment.InputTokenDetails.TextTokens, increment.InputTokenDetails.AudioTokens = text, audio
				} else {
					increment.OutputTokens = text + audio
					increment.OutputTokenDetails.TextTokens, increment.OutputTokenDetails.AudioTokens = text, audio
				}
				if err := addRealtimeUsage(local, increment); err != nil {
					terminal = err
					break loop
				}
			}
			target := info.ClientWs
			if frame.client {
				target = info.TargetWs
			}
			if err := helper.WssString(c, target, string(frame.data)); err != nil {
				terminal = err
				actor := "downstream"
				if frame.client {
					actor = "upstream"
				} else {
					reason = relaycommon.StreamEndReasonClientGone
				}
				info.StreamSession.Termination.Observe(actor, "write", err)
				break loop
			}
			if !frame.client {
				info.StreamDiagnostic.WriteDownstreamPayload(frame.data)
			}
		}
	}
	if pending != nil && pending.TotalTokens != 0 {
		if err := addRealtimeUsage(usage, pending); err != nil {
			terminal = errors.Join(terminal, err)
		} else if err := service.PreWssConsumeQuota(c, info, pending); err != nil {
			terminal = errors.Join(terminal, err)
		}
	}
	if local.TotalTokens != 0 {
		if err := addRealtimeUsage(usage, local); err != nil {
			terminal = errors.Join(terminal, err)
		} else if err := service.PreWssConsumeQuota(c, info, local); err != nil {
			terminal = errors.Join(terminal, err)
		}
	}
	if terminal != nil {
		info.StreamDiagnostic.SetError(terminal)
		info.StreamStatus.SetEndReason(reason, terminal)
		logger.LogLegacyStreamError(c, fmt.Sprintf("realtime error: %s", terminal))
	}
	return nil, usage
}

package openai

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestRealtimeMainContextEndReason(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancelled", true: "deadline"}[deadline], func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer ws.Close()
				_, _, _ = ws.ReadMessage()
			}))
			defer upstream.Close()
			type result struct {
				reason relaycommon.StreamEndReason
				usage  *dto.RealtimeUsage
				err    error
			}
			finished := make(chan result, 1)
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				client, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					finished <- result{err: err}
					return
				}
				defer client.Close()
				target, handshake, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(upstream.URL, "http"), nil)
				if err != nil {
					finished <- result{err: err}
					return
				}
				ctx, cancel := context.WithCancel(r.Context())
				cancel()
				if deadline {
					ctx, cancel = context.WithDeadline(r.Context(), time.Now().Add(-time.Second))
					defer cancel()
				}
				c, _ := gin.CreateTestContext(w)
				c.Request = r.WithContext(ctx)
				disabled := false
				info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIRealtime, ClientWs: client, TargetWs: target,
					UserSetting: dto.UserSetting{StreamProtocolValidation: &disabled}, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
				service.BeginStreamAttempt(c, info)
				info.StreamSession.ObserveWebSocketHandshake(handshake, nil)
				_, usage := OpenaiRealtimeHandler(c, info)
				finished <- result{reason: info.StreamStatus.EndReason, usage: usage}
			}))
			defer proxy.Close()
			client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(proxy.URL, "http"), nil)
			require.NoError(t, err)
			defer client.Close()
			select {
			case got := <-finished:
				require.NoError(t, got.err)
				require.Zero(t, got.usage.TotalTokens)
				expected := relaycommon.StreamEndReasonClientGone
				if deadline {
					expected = relaycommon.StreamEndReasonTimeout
				}
				require.Equal(t, expected, got.reason)
			case <-time.After(3 * time.Second):
				require.FailNow(t, "cancelled realtime owner did not stop")
			}
		})
	}
}

func TestRealtimeMainPolicyPreservesWireAndCountsEveryTerminal(t *testing.T) {
	for _, ending := range []string{"normal", "abrupt", "invalid JSON", "provider error"} {
		t.Run(ending, func(t *testing.T) {
			frames := []string{
				`{"type":"response.done","response":{"id":"one","usage":{"total_tokens":12,"input_tokens":10,"output_tokens":2}}}`,
				`{"type":"response.done","response":{"id":"one","usage":{"total_tokens":12,"input_tokens":10,"output_tokens":2}}}`,
				`{"type":"response.done","response":{"id":"two","usage":{"total_tokens":15,"input_tokens":11,"output_tokens":4}}}`,
			}
			if ending == "provider error" {
				frames = append([]string{`{"type":"error","error":{"type":"invalid_request_error","message":"recoverable fixture"}}`}, frames...)
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer ws.Close()
				for _, frame := range frames {
					if ws.WriteMessage(websocket.TextMessage, []byte(frame)) != nil {
						return
					}
				}
				if ending == "normal" || ending == "provider error" {
					_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1000, ""))
				}
				if ending == "invalid JSON" {
					_ = ws.WriteMessage(websocket.TextMessage, []byte("{"))
				}
			}))
			defer upstream.Close()
			type result struct {
				usage     *dto.RealtimeUsage
				outcome   *relaycommon.StreamOutcome
				hasErrors bool
				endReason relaycommon.StreamEndReason
				err       error
			}
			finished := make(chan result, 1)
			billing := &realtimeEndBilling{reserveErr: errors.New("main must not use cumulative reservations")}
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				client, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					finished <- result{err: err}
					return
				}
				defer client.Close()
				target, handshake, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(upstream.URL, "http"), nil)
				if err != nil {
					finished <- result{err: err}
					return
				}
				c, _ := gin.CreateTestContext(w)
				c.Request = r
				disabled := false
				info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIRealtime, ClientWs: client, TargetWs: target, Billing: billing,
					UserSetting: dto.UserSetting{StreamProtocolValidation: &disabled}, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
				info.UsePrice = true // Fixed-price main skips the per-round debit; real wallet cases live in service tests.
				service.BeginStreamAttempt(c, info)
				info.StreamSession.ObserveWebSocketHandshake(handshake, nil)
				_, usage := OpenaiRealtimeHandler(c, info)
				finished <- result{usage: usage, outcome: info.StreamResult, hasErrors: info.StreamStatus.HasErrors() || info.StreamStatus.EndError != nil, endReason: info.StreamStatus.EndReason}
			}))
			defer proxy.Close()
			client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(proxy.URL, "http"), nil)
			require.NoError(t, err)
			defer client.Close()
			require.NoError(t, client.SetReadDeadline(time.Now().Add(3*time.Second)))
			var got []string
			for {
				_, data, err := client.ReadMessage()
				if err != nil {
					break
				}
				got = append(got, string(data))
			}
			require.Equal(t, frames, got)
			select {
			case result := <-finished:
				require.NoError(t, result.err)
				require.Nil(t, result.outcome)
				require.Equal(t, ending != "normal", result.hasErrors)
				expectedReason := relaycommon.StreamEndReasonNone
				switch ending {
				case "abrupt":
					expectedReason = relaycommon.StreamEndReasonScannerErr
				case "invalid JSON":
					expectedReason = relaycommon.StreamEndReasonHandlerStop
				}
				require.Equal(t, expectedReason, result.endReason)
				require.Equal(t, 39, result.usage.TotalTokens, "main accumulates repeated response.done IDs")
				require.Equal(t, 31, result.usage.InputTokens)
				require.Equal(t, 8, result.usage.OutputTokens)
				require.Zero(t, billing.reserves)
				require.Zero(t, billing.settles)
			case <-time.After(3 * time.Second):
				require.FailNow(t, "realtime owner did not stop")
			}
		})
	}
}

func TestRealtimeMainUsageRangeIsAtomic(t *testing.T) {
	for _, round := range []*dto.RealtimeUsage{
		{TotalTokens: -1},
		{TotalTokens: math.MaxInt},
		{TotalTokens: 3, OutputTokenDetails: dto.OutputTokenDetails{AudioTokens: -1}},
	} {
		total := &dto.RealtimeUsage{TotalTokens: 1, InputTokens: 1}
		before := *total
		require.Error(t, addRealtimeUsage(total, round))
		require.Equal(t, before, *total)
	}
	total := &dto.RealtimeUsage{TotalTokens: math.MaxInt - 1}
	require.NoError(t, addRealtimeUsage(total, &dto.RealtimeUsage{TotalTokens: 1}))
	require.Equal(t, math.MaxInt, total.TotalTokens)
}

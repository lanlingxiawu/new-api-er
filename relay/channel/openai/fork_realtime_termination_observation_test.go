package openai

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// A failed write leaves the read side open until the real handler cleans up.
// That makes the operation/direction deterministic instead of racing a reset.
type forkRealtimeObservedConn struct {
	net.Conn
	failWrite atomic.Bool
	panicRead atomic.Bool
}

func (c *forkRealtimeObservedConn) Read(p []byte) (int, error) {
	if c.panicRead.Load() {
		panic("synthetic private read panic")
	}
	return c.Conn.Read(p)
}

func (c *forkRealtimeObservedConn) Write(p []byte) (int, error) {
	if c.failWrite.Load() {
		return 0, io.ErrClosedPipe
	}
	return c.Conn.Write(p)
}

func forkRealtimeObservedPair(t *testing.T) (*websocket.Conn, *websocket.Conn, *forkRealtimeObservedConn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err == nil {
			accepted <- ws
		}
	}))
	t.Cleanup(server.Close)
	var transport *forkRealtimeObservedConn
	dialer := websocket.Dialer{NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		transport = &forkRealtimeObservedConn{Conn: conn}
		return transport, nil
	}}
	managed, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = managed.Close() })
	var peer *websocket.Conn
	select {
	case peer = <-accepted:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "loopback websocket handshake did not complete")
	}
	t.Cleanup(func() { _ = peer.Close() })
	return managed, peer, transport
}

type forkRealtimeObservedTimeout struct {
	expired atomic.Bool
	kind    string
}

func (c *forkRealtimeObservedTimeout) RelayTimeoutKind() string {
	if c.expired.Load() {
		return c.kind
	}
	return ""
}

func (c *forkRealtimeObservedTimeout) WriteTerminalError(write func()) { write() }

// All traffic is local and funding uses the existing no-database test double.
// The assertions describe existing usage/error behavior as well as new evidence.
func TestForkRealtimeTerminationObservation(t *testing.T) {
	oldIdle := constant.StreamingTimeout
	constant.StreamingTimeout = 0
	t.Cleanup(func() { constant.StreamingTimeout = oldIdle })
	for _, tc := range []struct {
		name, side, operation string
		clientGone            bool
	}{
		{"upstream_read", "upstream", "read", false},
		{"downstream_read", "downstream", "read", true},
		{"upstream_write", "upstream", "write", false},
		{"downstream_write", "downstream", "write", true},
		{"downstream_protocol", "downstream", "protocol", true},
		{"upstream_protocol", "upstream", "protocol", false},
		{"downstream_reader_panic", "gateway", "panic", true},
		{"upstream_reader_panic", "gateway", "panic", false},
		{"context_cancel", "downstream", "request_cancel", true},
		{"total_timeout", "gateway", "timeout", true},
		{"response_timeout", "gateway", "timeout", true},
		{"idle_timeout", "gateway", "timeout", false},
		{"round_reset", "downstream", "read", true},
		{"normal_close_after_round", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, clientPeer, clientTransport := forkRealtimeObservedPair(t)
			target, targetPeer, targetTransport := forkRealtimeObservedPair(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/realtime", nil).WithContext(ctx)
			timeout := &forkRealtimeObservedTimeout{kind: tc.name}
			if tc.name == "total_timeout" || tc.name == "response_timeout" {
				common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, timeout)
				common.SetContextKey(c, constant.ContextKeyRelayTotalTimeoutSeconds, 5)
				common.SetContextKey(c, constant.ContextKeyRelayResponseTimeoutSeconds, 3)
			}
			if tc.name == "idle_timeout" {
				constant.StreamingTimeout = 1
				defer func() { constant.StreamingTimeout = 0 }()
			}
			billing := &realtimeEndBilling{reserved: make(chan struct{}, 4)}
			info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIRealtime, ClientWs: client, TargetWs: target, Billing: billing, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
			service.BeginStreamAttempt(c, info)
			info.StreamSession.ObserveWebSocketHandshake(&http.Response{StatusCode: http.StatusSwitchingProtocols}, nil)
			info.StreamSession.BindUpstream(target)
			initialSession := info.StreamSession
			if tc.name == "downstream_reader_panic" {
				clientTransport.panicRead.Store(true)
			}
			if tc.name == "upstream_reader_panic" {
				targetTransport.panicRead.Store(true)
			}
			result := make(chan *dto.RealtimeUsage, 1)
			go func() {
				_, usage := managedRealtimeHandler(c, info)
				result <- usage
			}()
			switch tc.name {
			case "upstream_read":
				require.NoError(t, targetPeer.Close())
			case "downstream_read":
				require.NoError(t, clientPeer.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "private-close-text")))
			case "upstream_write":
				targetTransport.failWrite.Store(true)
				require.NoError(t, clientPeer.WriteMessage(websocket.TextMessage, []byte(`{"type":"session.update","session":{}}`)))
			case "downstream_write":
				clientTransport.failWrite.Store(true)
				require.NoError(t, targetPeer.WriteMessage(websocket.TextMessage, []byte(`{"type":"session.created","session":{}}`)))
			case "downstream_protocol":
				require.NoError(t, clientPeer.WriteMessage(websocket.TextMessage, []byte(`{"private":"invalid`)))
			case "upstream_protocol":
				require.NoError(t, targetPeer.WriteMessage(websocket.TextMessage, []byte(`{"private":"invalid`)))
			case "context_cancel", "total_timeout", "response_timeout":
				timeout.expired.Store(tc.name != "context_cancel")
				cancel()
			case "round_reset", "normal_close_after_round":
				require.NoError(t, targetPeer.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.text.delta","delta":"hi"}`)))
				require.NoError(t, targetPeer.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.done","response":{"id":"round_fixture","status":"completed","usage":{"input_tokens":10,"output_tokens":2}}}`)))
				require.NoError(t, clientPeer.SetReadDeadline(time.Now().Add(2*time.Second)))
				for range 2 {
					_, _, err := clientPeer.ReadMessage()
					require.NoError(t, err)
				}
				select {
				case <-billing.reserved:
				case <-time.After(2 * time.Second):
					require.FailNow(t, "completed round did not reserve")
				}
				peer := clientPeer
				if tc.name == "normal_close_after_round" {
					peer = targetPeer
				}
				require.NoError(t, peer.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "private-close-text")))
			}
			var usage *dto.RealtimeUsage
			select {
			case usage = <-result:
			case <-time.After(4 * time.Second):
				cancel()
				require.FailNow(t, "managed realtime did not terminate")
			}
			require.Zero(t, billing.settles)
			require.Zero(t, billing.refunds)
			require.Equal(t, tc.clientGone, info.StreamResult.ClientGone)
			if tc.name == "round_reset" || tc.name == "normal_close_after_round" {
				require.Equal(t, 10, usage.InputTokens)
				require.Equal(t, 2, usage.OutputTokens)
				require.Equal(t, 1, billing.reserves)
				require.NotSame(t, initialSession, info.StreamSession)
			} else {
				require.Zero(t, usage.TotalTokens)
			}
			encoded, err := common.Marshal(info.StreamDiagnostic.Snapshot())
			require.NoError(t, err)
			termination := gjson.GetBytes(encoded, "termination")
			require.True(t, termination.Exists())
			first := termination.Get("first_observed")
			if tc.name == "normal_close_after_round" {
				require.False(t, first.Exists(), "normal close is transport evidence, not failure")
			} else {
				require.Equal(t, tc.side, first.Get("side").String())
				require.Equal(t, tc.operation, first.Get("operation").String())
			}
			require.Equal(t, "gateway", termination.Get("upstream_close.side").String())
			require.Equal(t, "close", termination.Get("upstream_close.operation").String())
			require.NotContains(t, termination.Raw, "private-close-text")
			require.NotContains(t, termination.Raw, "invalid")
			require.NotContains(t, termination.Raw, "synthetic private")
			if strings.HasSuffix(tc.name, "timeout") {
				require.Equal(t, tc.name, first.Get("timeout_kind").String())
				seconds := int64(1)
				if tc.name == "total_timeout" {
					seconds = 5
				}
				if tc.name == "response_timeout" {
					seconds = 3
				}
				require.Equal(t, seconds, first.Get("timeout_seconds").Int())
			}
			if tc.name == "downstream_read" || tc.name == "round_reset" {
				require.Equal(t, int64(websocket.CloseNormalClosure), first.Get("ws_close_code").Int())
			}
			if tc.name == "upstream_read" || tc.name == "normal_close_after_round" {
				require.True(t, termination.Get("upstream_read").Exists())
				require.False(t, termination.Get("upstream_read_after_close").Bool())
			}
			if tc.name == "upstream_write" || tc.name == "downstream_write" || tc.name == "downstream_protocol" {
				require.True(t, termination.Get("upstream_read").Exists())
				require.True(t, termination.Get("upstream_read_after_close").Bool(), "close-induced read errors must not become an upstream-first event")
			}
			if tc.name == "round_reset" || tc.name == "normal_close_after_round" {
				require.Same(t, initialSession.Termination, info.StreamSession.Termination, "rounds retain the connection observer")
			}
		})
	}
}

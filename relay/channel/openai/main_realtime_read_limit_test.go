package openai

import (
	"bytes"
	"net/http/httptest"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// Send the full payload, rather than just an oversized frame header: the main
// owner must be able to read the later usage frame after a large valid event.
func TestRealtimePolicyReadLimit(t *testing.T) {
	payload := bytes.Repeat([]byte(" "), relaycommon.MaxStreamFrameBytes+1)
	copy(payload, `{"type":"response.created"}`)
	terminal := []byte(`{"type":"response.done","response":{"usage":{"total_tokens":21,"input_tokens":16,"output_tokens":5}}}`)
	for _, strict := range []bool{false, true} {
		name := "main_accepts_large_event_and_later_usage"
		if strict {
			name = "strict_keeps_frame_limit"
		}
		t.Run(name, func(t *testing.T) {
			client, _, _ := forkRealtimeObservedPair(t)
			target, peer, _ := forkRealtimeObservedPair(t)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("GET", "/v1/realtime", nil)
			info := &relaycommon.RelayInfo{ClientWs: client, TargetWs: target, IsStream: true,
				UserSetting: dto.UserSetting{StreamProtocolValidation: &strict}}
			service.BeginStreamAttempt(c, info)
			_, reads, cleanup := startRealtimeRelayReaders(c, info)
			defer cleanup()
			written := make(chan error, 1)
			go func() {
				_ = peer.SetWriteDeadline(time.Now().Add(30 * time.Second))
				err := peer.WriteMessage(websocket.TextMessage, payload)
				if err == nil {
					err = peer.WriteMessage(websocket.TextMessage, terminal)
				}
				written <- err
			}()
			select {
			case frame := <-reads:
				if strict {
					require.ErrorIs(t, frame.err, websocket.ErrReadLimit)
					return
				}
				require.NoError(t, frame.err)
				require.False(t, frame.client)
				require.Len(t, frame.data, len(payload))
			case <-time.After(30 * time.Second):
				require.FailNow(t, "large websocket event was not read")
			}
			select {
			case frame := <-reads:
				require.NoError(t, frame.err)
				require.Equal(t, terminal, frame.data)
			case <-time.After(5 * time.Second):
				require.FailNow(t, "later usage frame was not read")
			}
			require.NoError(t, <-written)
		})
	}
}

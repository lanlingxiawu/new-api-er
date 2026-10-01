package openai

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realtimeErrorDisplayRun relays frames from a local upstream WS to a local
// client through managedRealtimeHandler and returns what the client received
// and the terminal-frame input recorded in the request context, if any.
func realtimeErrorDisplayRun(t *testing.T, frames []string) (received []string, recorded any, hasRecorded bool) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		for _, frame := range frames {
			if err := ws.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
				return
			}
		}
		_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	}))
	defer upstream.Close()
	type result struct {
		value any
		ok    bool
	}
	results := make(chan result, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer client.Close()
		target, handshake, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(upstream.URL, "http"), nil)
		if err != nil {
			return
		}
		defer target.Close()
		c, _ := gin.CreateTestContext(w)
		c.Request = r
		info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIRealtime, ClientWs: client, TargetWs: target, Billing: &realtimeEndBilling{}, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
		service.BeginStreamAttempt(c, info)
		info.StreamSession.ObserveWebSocketHandshake(handshake, nil)
		info.StreamSession.BindUpstream(target)
		managedRealtimeHandler(c, info)
		value, ok := c.Get("relay_error_display_stream_input")
		results <- result{value, ok}
	}))
	defer proxy.Close()
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(proxy.URL, "http"), nil)
	require.NoError(t, err)
	defer client.Close()
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, data, err := client.ReadMessage()
		if err != nil {
			break
		}
		received = append(received, string(data))
	}
	select {
	case got := <-results:
		return received, got.value, got.ok
	case <-time.After(2 * time.Second):
		require.FailNow(t, "handler did not finish")
	}
	return nil, nil, false
}

func withRealtimeErrorDisplay(t *testing.T, setting operation_setting.RelayErrorDisplaySetting) {
	t.Helper()
	require.NoError(t, operation_setting.ValidateRelayErrorDisplaySetting(setting))
	previous := operation_setting.GetRelayErrorDisplaySetting()
	operation_setting.ReplaceRelayErrorDisplaySetting(setting)
	t.Cleanup(func() { operation_setting.ReplaceRelayErrorDisplaySetting(previous) })
}

const (
	realtimeRecoverableUpstreamError = `{"type":"error","event_id":"event_up_1","error":{"type":"invalid_request_error","code":"invalid_value","message":"group vip has no quota left","param":"session.voice","event_id":"client-evt-7"}}`
	realtimeFatalUpstreamError       = `{"type":"error","event_id":"event_up_2","error":{"type":"server_error","code":"upstream_internal","message":"dial tcp 10.0.0.8:443 failed for group vip"}}`
)

// Review item 2: upstream Realtime `error` events were written to the client
// verbatim, and the terminal one was then marked delivered, so the terminal
// error writer (and with it the error display) never ran. Both now go through
// the same decision as stream terminal frames, as valid Realtime error events.
func TestRealtimeErrorEventsFollowErrorDisplay(t *testing.T) {
	withRealtimeErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "Unavailable"})
	received, recorded, ok := realtimeErrorDisplayRun(t, []string{realtimeRecoverableUpstreamError, realtimeFatalUpstreamError})
	require.Len(t, received, 2, "%v", received)
	for _, raw := range received {
		assert.NotContains(t, raw, "group vip")
		assert.NotContains(t, raw, "event_up_")
		assert.NotContains(t, raw, "10.0.0.8")
	}
	var recoverable, fatal map[string]any
	require.NoError(t, common.UnmarshalJsonStr(received[0], &recoverable))
	require.NoError(t, common.UnmarshalJsonStr(received[1], &fatal))
	assert.Equal(t, map[string]any{"type": "error", "error": map[string]any{
		"type": "invalid_request_error", "code": operation_setting.RelayStreamErrorCode, "message": "Unavailable", "event_id": "client-evt-7",
	}}, recoverable, "a recoverable error stays recoverable and keeps the client's own event id")
	assert.Equal(t, map[string]any{"type": "error", "error": map[string]any{
		"type": "server_error", "code": operation_setting.RelayStreamErrorCode, "message": "Unavailable",
	}}, fatal)
	require.True(t, ok, "the terminal error's input is kept for the error log")
	in, isInput := recorded.(operation_setting.RelayErrorInput)
	require.True(t, isInput)
	assert.Equal(t, "upstream_internal", in.ErrorCode)
	assert.Contains(t, in.Message, "failed for group vip", "the recorded input is the terminal error's, not the recoverable one's")
}

// Rules apply to Realtime error events as to any upstream stream error: a keep
// rule sends the event as received, an edit rule rewrites its message.
func TestRealtimeErrorEventsFollowRules(t *testing.T) {
	withRealtimeErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "Unavailable",
		Rules: `[{"source":"upstream","error_codes":["invalid_value"],"action":"keep"},` +
			`{"source":"upstream","keywords":["dial tcp"],"action":"edit","edits":[{"find":" for group vip"}]}]`})
	received, _, _ := realtimeErrorDisplayRun(t, []string{realtimeRecoverableUpstreamError, realtimeFatalUpstreamError})
	require.Len(t, received, 2, "%v", received)
	assert.Equal(t, realtimeRecoverableUpstreamError, received[0], "kept: the event as received")
	assert.Contains(t, received[1], `"message":"dial tcp ***.***.***.***:443 failed"`, "edited, from the masked text")
	assert.NotContains(t, received[1], "event_up_2")
}

// Switched off, every error event goes out byte for byte as the upstream sent
// it, and a recoverable error alone records no terminal input.
func TestRealtimeErrorEventsUnchangedWhenDisplayOff(t *testing.T) {
	withRealtimeErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: false, HideUpstreamErrors: true})
	received, _, _ := realtimeErrorDisplayRun(t, []string{realtimeRecoverableUpstreamError, realtimeFatalUpstreamError})
	assert.Equal(t, []string{realtimeRecoverableUpstreamError, realtimeFatalUpstreamError}, received)

	_, _, ok := realtimeErrorDisplayRun(t, []string{realtimeRecoverableUpstreamError})
	assert.False(t, ok, "a recoverable error does not become the stream's terminal input")
}

// Review follow-up: besides `error` events, the stream ends as an upstream
// error on a failed response.done and on any event carrying an error object.
// Those were written verbatim and marked delivered, so the error display never
// ran for them. They keep their type and shape; only the error text changes.
func TestRealtimeTerminalErrorEventsOfOtherTypesFollowErrorDisplay(t *testing.T) {
	const failedDone = `{"type":"response.done","event_id":"event_up_3","response":{"id":"resp_1","status":"failed","status_details":{"type":"failed","error":{"type":"server_error","code":"upstream_quota","message":"quota exhausted for group vip"}}}}`
	const transcriptionFailed = `{"type":"conversation.item.input_audio_transcription.failed","event_id":"event_up_4","item_id":"item_1","content_index":0,"error":{"type":"transcription_error","code":"audio_unintelligible","message":"backend 10.0.0.9 for group vip failed"}}`
	withRealtimeErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "Unavailable"})
	for _, tc := range []struct {
		name, frame, errorPath, code string
	}{
		{"failed response.done", failedDone, "response.status_details.error", "upstream_quota"},
		{"event with an error object", transcriptionFailed, "error", "audio_unintelligible"},
	} {
		received, recorded, ok := realtimeErrorDisplayRun(t, []string{tc.frame})
		require.Len(t, received, 1, "%s: %v", tc.name, received)
		assert.NotContains(t, received[0], "group vip", tc.name)
		assert.NotContains(t, received[0], "upstream_quota", tc.name)
		var got, want map[string]any
		require.NoError(t, common.UnmarshalJsonStr(received[0], &got))
		require.NoError(t, common.UnmarshalJsonStr(tc.frame, &want))
		assert.Equal(t, want["type"], got["type"], "%s: the event keeps its type", tc.name)
		assert.Equal(t, want["event_id"], got["event_id"], "%s: other fields are kept", tc.name)
		errorObject := got
		for _, key := range strings.Split(tc.errorPath, ".") {
			errorObject, _ = errorObject[key].(map[string]any)
		}
		assert.Equal(t, map[string]any{"type": "server_error", "code": operation_setting.RelayStreamErrorCode, "message": "Unavailable"}, errorObject, tc.name)
		require.True(t, ok, "%s: the terminal input is recorded", tc.name)
		in := recorded.(operation_setting.RelayErrorInput)
		assert.Equal(t, tc.code, in.ErrorCode, tc.name)
		assert.Contains(t, in.Message, "for group vip", tc.name)
	}

	withRealtimeErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: false, HideUpstreamErrors: true})
	for _, frame := range []string{failedDone, transcriptionFailed} {
		received, _, ok := realtimeErrorDisplayRun(t, []string{frame})
		assert.Equal(t, []string{frame}, received, "switched off: byte for byte")
		assert.True(t, ok, "the input is recorded whether or not the feature is on")
	}
}

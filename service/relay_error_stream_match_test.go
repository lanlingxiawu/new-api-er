package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// streamMatchRun is one failed stream run to its settlement log.
type streamMatchRun struct {
	quota int
	// frame is the upstream error event; empty for a stream without one.
	frame string
	// timeoutBeforeFrame cuts the stream by our time limit before its terminal
	// frame; timeoutAfterFrame only after it, before settlement.
	timeoutBeforeFrame, timeoutAfterFrame bool
	// timeoutBeforeTerminal fires our deadline after the upstream error frame
	// was read but before the terminal frame is written.
	timeoutBeforeTerminal bool
	// strict ends the stream the way the Claude strict stream does: it decides
	// its own terminal frame and sets StreamResult without the stream session.
	strict bool
}

// runStreamMatch fails one managed stream, settles it and returns the live
// response body and the stored log.
func runStreamMatch(t *testing.T, tokenID int, requestID string, run streamMatchRun) (string, model.Log) {
	t.Helper()
	previousConsumeLog, previousErrorLog := common.LogConsumeEnabled, constant.ErrorLogEnabled
	common.LogConsumeEnabled, constant.ErrorLogEnabled = true, true
	model.DrainRelayLogsSync(5 * time.Second)
	model.RegisterRelayLogAccountingHandler(func(model.RelayLogAccountingPayload, int) {})
	t.Cleanup(func() {
		model.DrainRelayLogsSync(5 * time.Second)
		require.NoError(t, model.LOG_DB.Where("token_id = ?", tokenID).Delete(&model.Log{}).Error)
		common.LogConsumeEnabled, constant.ErrorLogEnabled = previousConsumeLog, previousErrorLog
		model.RegisterRelayLogAccountingHandler(func(p model.RelayLogAccountingPayload, logID int) {
			costCommissionSnapshot{
				UserID: p.UserID, ChannelID: p.ChannelID, ChannelName: p.ChannelName,
				UsingGroup: p.UsingGroup, OriginModelName: p.OriginModelName,
				GroupRatio: p.GroupRatio, Quota: p.Quota,
				BaseQuota: p.BaseQuota,
			}.record(logID)
		})
	})
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, requestID)
	c.Set("id", 7)
	c.Set("token_name", "stream-match-token")
	c.Set("token_id", tokenID)
	c.Set("original_model", "stream-match-model")
	c.Set("group", "default")
	common.SetContextKey(c, constant.ContextKeyIsStream, true)
	common.SetContextKey(c, constant.ContextKeyRelayTotalTimeoutSeconds, 30)
	if run.timeoutBeforeFrame {
		common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, settlementTimeoutControl{})
	}

	info := &relaycommon.RelayInfo{
		UserId: 7, TokenId: tokenID, Billing: &claudeSettlementFixture{}, UserQuota: 1_000_000_000,
		IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{},
	}
	BeginStreamAttempt(c, info)
	info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
	if run.quota > 0 {
		content := []byte(`{"choices":[{"delta":{"content":"partial"}}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`)
		require.NoError(t, info.StreamSession.ObserveEvent("", content))
		info.StreamSession.CommitDelivery(content)
	}
	if run.strict {
		// relay/channel/claude/strict_stream.go: the upstream error frame read,
		// then (after the deadline, if any) its terminal frame and log message.
		if run.timeoutBeforeTerminal {
			common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, settlementTimeoutControl{})
		}
		frame := []byte("event: error\ndata: " + run.frame + "\n\n")
		_, payload := relaycommon.StreamFramePayload(frame)
		message, forward := PresentStreamTerminalMessage(c, payload, i18n.Translate(i18n.LangEn, i18n.MsgClaudeStreamFailed))
		if forward {
			_, _ = recorder.Write(frame)
		} else {
			_, _ = recorder.WriteString(`event: error` + "\n" + `data: {"type":"error","error":{"type":"api_error","message":"` + message + `"}}` + "\n\n")
		}
		info.StreamResult = &relaycommon.StreamOutcome{Failed: true, ErrorMessage: StreamFailureLogMessage(relaycommon.StreamSnapshot{ErrorFrame: frame})}
	} else {
		if run.frame != "" {
			require.NoError(t, info.StreamSession.ObserveEvent("error", []byte(run.frame)))
		}
		if run.timeoutBeforeTerminal {
			common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, settlementTimeoutControl{})
		}
		FinalizeStreamUsage(c, info, nil)
	}
	if run.timeoutAfterFrame {
		common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, settlementTimeoutControl{})
	}
	FinalizeConsumptionSettlement(c, info, ConsumptionSettlementParams{Quota: run.quota, Content: "note", IsStream: true, Other: model.NewLogOther()})
	model.DrainRelayLogsSync(5 * time.Second)
	var logs []model.Log
	require.NoError(t, model.LOG_DB.Where("token_id = ?", tokenID).Find(&logs).Error)
	require.Len(t, logs, 1)
	return recorder.Body.String(), logs[0]
}

func streamMatchAdminInfo(t *testing.T, log model.Log) map[string]any {
	t.Helper()
	other, err := common.StrToMap(log.Other)
	require.NoError(t, err)
	adminInfo, _ := other["admin_info"].(map[string]any)
	return adminInfo
}

// Regression (review M3): the live terminal frame was matched with the
// upstream frame's own error code while the user's view of the same stream's
// error log used upstream_stream_error, so a rule keyed on either code masked
// one view and not the other. The log now keeps the frame's input (admin-only)
// and both views decide with it.
func TestStreamFailureLogDecidesLikeTerminalFrame(t *testing.T) {
	const frame = `{"error":{"code":"insufficient_quota","message":"quota low for group vip"}}`
	for i, tc := range []struct {
		name  string
		rules []operation_setting.RelayErrorRule
		// live is the text the client's terminal frame carries; user is the
		// user's view of the error log.
		live, user string
	}{
		{
			name: "rule keyed on the upstream frame's code",
			rules: []operation_setting.RelayErrorRule{{Source: operation_setting.RelayErrorSourceUpstream,
				ErrorCodes: []string{"insufficient_quota"}, Action: operation_setting.RelayErrorActionReplace, Message: "Busy"}},
			live: "Busy", user: "Busy (request id: %s)",
		},
		{
			name: "keep keyed on upstream_stream_error does not keep a frame with its own code",
			rules: []operation_setting.RelayErrorRule{{Source: operation_setting.RelayErrorSourceUpstream,
				ErrorCodes: []string{operation_setting.RelayStreamErrorCode}, Action: operation_setting.RelayErrorActionKeep}},
			live: "Unavailable", user: "Unavailable (request id: %s)",
		},
		{
			name: "edit works on the frame's text, not the log's prefix",
			rules: []operation_setting.RelayErrorRule{{Source: operation_setting.RelayErrorSourceUpstream,
				Keywords: []string{"quota"}, Action: operation_setting.RelayErrorActionEdit,
				Edits: []operation_setting.RelayErrorEdit{{Find: " for group vip"}}}},
			live: "quota low", user: "quota low (request id: %s)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true,
				DefaultMessage: "Unavailable", Rules: displayRules(t, tc.rules...)})
			requestID := fmt.Sprintf("match-%d", i)
			body, log := runStreamMatch(t, 91000130+i, requestID, streamMatchRun{frame: frame})
			require.Equal(t, model.LogTypeError, log.Type)
			assert.Contains(t, body, `"message":"`+tc.live+`"`, "the live frame")
			assert.Equal(t, "note; quota low for group vip (request id: "+requestID+")", log.Content, "admins keep the original")
			adminInfo := streamMatchAdminInfo(t, log)
			assert.Equal(t, "insufficient_quota", adminInfo[operation_setting.RelayStreamMatchCodeKey])
			assert.Equal(t, "quota low for group vip", adminInfo[operation_setting.RelayStreamMatchMessageKey])
			assert.Contains(t, log.Other, `"error_code":"`+operation_setting.RelayStreamErrorCode+`"`, "error_code stays the stream-failure marker")
			wantUser := fmt.Sprintf(tc.user, requestID)
			assert.Equal(t, wantUser, model.MaskErrorLogContentForUser(&log, "en"), "the user's log view")
		})
	}
}

// A frame without a code of its own is matched as upstream_stream_error, and
// that is what the log records.
func TestStreamFailureLogRecordsDefaultStreamCode(t *testing.T) {
	_, log := runStreamMatch(t, 91000140, "match-default", streamMatchRun{frame: `{"error":{"message":"overloaded"}}`})
	adminInfo := streamMatchAdminInfo(t, log)
	assert.Equal(t, operation_setting.RelayStreamErrorCode, adminInfo[operation_setting.RelayStreamMatchCodeKey])
	assert.Equal(t, "overloaded", adminInfo[operation_setting.RelayStreamMatchMessageKey])
}

// The frame's own code and text are read from the Chat Completions, Claude and
// Responses shapes; a null, empty or non-scalar code is no code.
func TestStreamTerminalRelayErrorInputShapes(t *testing.T) {
	c := errorDisplayCtx()
	for _, tc := range []struct{ payload, code, message string }{
		{`{"error":{"message":"m","type":"server_error","code":null}}`, operation_setting.RelayStreamErrorCode, "m"},
		{`{"error":{"message":"m","code":""}}`, operation_setting.RelayStreamErrorCode, "m"},
		{`{"error":{"message":"m","code":{"x":1}}}`, operation_setting.RelayStreamErrorCode, "m"},
		{`{"error":{"message":"m","code":"insufficient_quota"}}`, "insufficient_quota", "m"},
		{`{"error":{"message":"m","code":429}}`, "429", "m"},
		{`{"type":"error","code":"rate_limit_exceeded","message":"slow down","param":null}`, "rate_limit_exceeded", "slow down"},
		{`{"type":"response.failed","response":{"error":{"code":"server_error","message":"failed upstream"}}}`, "server_error", "failed upstream"},
		{`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, operation_setting.RelayStreamErrorCode, "Overloaded"},
		{`not json`, operation_setting.RelayStreamErrorCode, "fallback"},
	} {
		in := streamTerminalRelayErrorInput(c, []byte(tc.payload), "fallback")
		assert.True(t, in.Upstream, tc.payload)
		assert.Equal(t, tc.code, in.ErrorCode, tc.payload)
		assert.Equal(t, tc.message, in.Message, tc.payload)
	}
}

// Review finding: when our deadline cut a stream after an upstream error frame
// was read, the frame was decided as our timeout, nothing matched, and the raw
// upstream frame went out although hide_upstream_errors was on.
func TestPresentStreamTerminalMessage_TimeoutNeverForwardsUpstreamFrame(t *testing.T) {
	payload := []byte(`{"error":{"message":"upstream secret","code":"insufficient_quota"}}`)
	c := errorDisplayCtx()
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, settlementTimeoutControl{})

	withRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true})
	message, forward := PresentStreamTerminalMessage(c, payload, "generic")
	assert.False(t, forward)
	assert.Equal(t, "generic", message)

	withRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true,
		Rules: displayRules(t, operation_setting.RelayErrorRule{Source: operation_setting.RelayErrorSourceLocal,
			ErrorCodes: []string{operation_setting.RelayTimeoutErrorCode}, Action: operation_setting.RelayErrorActionReplace, Message: "Timed out"})})
	message, forward = PresentStreamTerminalMessage(c, payload, "generic")
	assert.False(t, forward)
	assert.Equal(t, "Timed out", message)

	// Switched off, the terminal frame is exactly as before.
	withRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: false, HideUpstreamErrors: true})
	message, forward = PresentStreamTerminalMessage(c, payload, "generic")
	assert.True(t, forward)
	assert.Equal(t, "generic", message)

	// Not timed out and not replaced: the upstream frame may still be forwarded.
	withRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: false})
	_, forward = PresentStreamTerminalMessage(errorDisplayCtx(), payload, "generic")
	assert.True(t, forward)
}

// The recorded text is masked like the log content: no credential material or
// upstream address is stored, and the frame is decided with the same text.
func TestStreamFailureLogRecordsMaskedText(t *testing.T) {
	withRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true,
		Rules: displayRules(t, operation_setting.RelayErrorRule{Source: operation_setting.RelayErrorSourceUpstream,
			Action: operation_setting.RelayErrorActionEdit, Edits: []operation_setting.RelayErrorEdit{{Find: "overloaded", Replace: "busy"}}})})
	body, log := runStreamMatch(t, 91000145, "match-mask", streamMatchRun{
		frame: `{"error":{"message":"overloaded api_key:secret-123 at https://up.example.com/v1/x"}}`})
	assert.NotContains(t, log.Other, "secret-123")
	assert.NotContains(t, log.Other, "up.example.com")
	assert.NotContains(t, body, "secret-123", "the live frame is built from the masked text")
	assert.NotContains(t, body, "up.example.com")
	stored, _ := streamMatchAdminInfo(t, log)[operation_setting.RelayStreamMatchMessageKey].(string)
	assert.Contains(t, stored, "overloaded api_key:***")
	assert.Equal(t, "busy"+stored[len("overloaded"):]+" (request id: match-mask)", model.MaskErrorLogContentForUser(&log, "en"))
}

// When our deadline fires after the terminal frame went out as an upstream
// error, the log keeps the frame's classification, so the two views agree.
func TestStreamFailureLogKeepsFrameClassificationWhenDeadlineFiresLater(t *testing.T) {
	_, log := runStreamMatch(t, 91000141, "match-late", streamMatchRun{frame: `{"error":{"message":"overloaded"}}`, timeoutAfterFrame: true})
	other, err := common.StrToMap(log.Other)
	require.NoError(t, err)
	assert.Equal(t, operation_setting.RelayStreamErrorCode, other["error_code"])

	_, log = runStreamMatch(t, 91000142, "match-timeout", streamMatchRun{timeoutBeforeFrame: true})
	other, err = common.StrToMap(log.Other)
	require.NoError(t, err)
	assert.Equal(t, operation_setting.RelayTimeoutErrorCode, other["error_code"])
	adminInfo := streamMatchAdminInfo(t, log)
	assert.Equal(t, operation_setting.RelayTimeoutErrorCode, adminInfo[operation_setting.RelayStreamMatchCodeKey])
}

// Review item 1: the upstream sent an error frame and our deadline fired
// before the terminal frame was written. The frame is decided as our timeout
// (the client gets the generic text), so the user's view of the log decides it
// as a local relay_timeout, which no rule matched: the log's content was the
// upstream error.message and went to the user as is with upstream errors
// hidden. The content is now our timeout text; the upstream text is admin-only.
// The Claude strict stream decides its terminal frame itself, same outcome.
func TestStreamFailureLogTimeoutAfterUpstreamFrameKeepsUpstreamTextAdminOnly(t *testing.T) {
	require.NoError(t, i18n.Init())
	withRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "Unavailable"})
	timeoutText := i18n.Translate(i18n.LangEn, i18n.MsgRelayTimeout, map[string]any{"Seconds": 30})
	for i, strict := range []bool{false, true} {
		requestID := fmt.Sprintf("match-deadline-%d", i)
		body, log := runStreamMatch(t, 91000146+i, requestID, streamMatchRun{
			frame: `{"error":{"message":"upstream secret for group vip"}}`, timeoutBeforeTerminal: true, strict: strict})
		assert.NotContains(t, body, "upstream secret", "strict=%v: the live frame", strict)
		require.Equal(t, model.LogTypeError, log.Type)
		assert.Equal(t, operation_setting.RelayTimeoutErrorCode, streamMatchOther(t, log)["error_code"])
		assert.Equal(t, "note; "+timeoutText+" (request id: "+requestID+")", log.Content, "strict=%v", strict)
		adminInfo := streamMatchAdminInfo(t, log)
		assert.Equal(t, "upstream secret for group vip", adminInfo["stream_error"], "admins keep the upstream text")
		assert.Equal(t, operation_setting.RelayTimeoutErrorCode, adminInfo[operation_setting.RelayStreamMatchCodeKey])
		userView := model.MaskErrorLogContentForUser(&log, "en")
		assert.NotContains(t, userView, "upstream secret", "strict=%v: the user's log view", strict)
		assert.Equal(t, "note; "+timeoutText+" (request id: "+requestID+")", userView)
	}

	// A stream cut by our deadline with no upstream frame gets the same text.
	_, log := runStreamMatch(t, 91000148, "match-deadline-none", streamMatchRun{timeoutBeforeFrame: true})
	assert.Equal(t, "note; "+timeoutText+" (request id: match-deadline-none)", log.Content)
}

// Review item 4: the frame's text was capped before it was masked, so an
// address or key the cap split was no longer recognised and its first part was
// recorded and sent. It is now masked first; the cap drops the split token, and
// a capped text is marked truncated so that, unless a rule replaces it, the
// client gets the capped text rather than the upstream frame.
func TestStreamTerminalRelayErrorInputMasksBeforeCapping(t *testing.T) {
	head := strings.Repeat("x ", (operation_setting.MaxRelayErrorTextLen-12)/2)
	for secret, truncated := range map[string]bool{
		"from 10.20.30.40:443 refused":                    true,
		"key sk-abcdefghijklmnopqrstuvwxyz0123":           true,
		"api_key:sk-abcdefghijklmnopqrstuvwxyz0123456789": false, // masked short enough to fit
	} {
		payload, err := common.Marshal(map[string]any{"error": map[string]any{"message": head + secret}})
		require.NoError(t, err)
		in := streamTerminalRelayErrorInput(errorDisplayCtx(), payload, "fallback")
		assert.Equal(t, truncated, in.Truncated, secret)
		assert.NotContains(t, in.Message, "10.20", secret)
		assert.NotContains(t, in.Message, "sk-", secret)
		assert.LessOrEqual(t, utf8.RuneCountInString(in.Message), operation_setting.MaxRelayErrorTextLen)
	}
	short := streamTerminalRelayErrorInput(errorDisplayCtx(), []byte(`{"error":{"message":"short"}}`), "fallback")
	assert.False(t, short.Truncated)
	single := streamTerminalRelayErrorInput(errorDisplayCtx(), []byte(`{"error":{"message":"`+strings.Repeat("a", operation_setting.MaxRelayErrorTextLen+1)+`"}}`), "fallback")
	assert.True(t, single.Truncated)
	assert.Equal(t, "fallback", single.Message, "nothing survives the cut: the generic text")

	// hide_upstream_errors off and no rule: a capped frame is not forwarded as
	// is; the client gets the capped text, and the log view decides the same.
	withRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: false})
	payload, err := common.Marshal(map[string]any{"error": map[string]any{"message": head + "tail beyond the cap is never sent " + strings.Repeat("y ", 100)}})
	require.NoError(t, err)
	c := errorDisplayCtx()
	message, forward := PresentStreamTerminalMessage(c, payload, "fallback")
	assert.False(t, forward)
	assert.NotContains(t, message, "never sent")
	in, ok := streamTerminalInput(c)
	require.True(t, ok)
	assert.True(t, in.Truncated)
	_, forward = PresentStreamTerminalMessage(errorDisplayCtx(), []byte(`{"error":{"message":"short"}}`), "fallback")
	assert.True(t, forward, "a frame whose whole text was checked may still go out as received")
}

// The recorded input keeps its truncated mark in the error log, so the user's
// view of the log sends the capped text too.
func TestStreamFailureLogRecordsTruncatedInput(t *testing.T) {
	withRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: false})
	head := strings.Repeat("x ", (operation_setting.MaxRelayErrorTextLen-12)/2)
	frame, err := common.Marshal(map[string]any{"error": map[string]any{"message": head + "tail beyond the cap"}})
	require.NoError(t, err)
	_, log := runStreamMatch(t, 91000149, "match-truncated", streamMatchRun{frame: string(frame)})
	assert.Equal(t, true, streamMatchAdminInfo(t, log)[operation_setting.RelayStreamMatchTruncatedKey])
	userView := model.MaskErrorLogContentForUser(&log, "en")
	assert.NotContains(t, userView, "beyond the cap")
	assert.True(t, strings.HasSuffix(userView, "(request id: match-truncated)"))
}

func streamMatchOther(t *testing.T, log model.Log) map[string]any {
	t.Helper()
	other, err := common.StrToMap(log.Other)
	require.NoError(t, err)
	return other
}

// Regression (review M4): a charged stream cut by our own time limit said
// "Upstream stream failed" in its consume log; it now says it hit our limit.
// An upstream failure keeps the upstream note.
func TestChargedStreamFailureNote(t *testing.T) {
	require.NoError(t, i18n.Init())
	_, log := runStreamMatch(t, 91000143, "charged-timeout", streamMatchRun{quota: 42, timeoutBeforeFrame: true})
	require.Equal(t, model.LogTypeConsume, log.Type)
	timeoutText := i18n.Translate(i18n.LangEn, i18n.MsgRelayTimeout, map[string]any{"Seconds": 30})
	assert.Equal(t, "note; "+timeoutText+" (request id: charged-timeout)", log.Content)
	assert.Contains(t, timeoutText, "30")

	_, log = runStreamMatch(t, 91000144, "charged-upstream", streamMatchRun{quota: 42, frame: `{"error":{"message":"overloaded"}}`})
	require.Equal(t, model.LogTypeConsume, log.Type)
	assert.Equal(t, "note; "+i18n.Translate(i18n.LangEn, i18n.MsgClaudeStreamFailed)+" (request id: charged-upstream)", log.Content)
	assert.Equal(t, "overloaded", streamMatchAdminInfo(t, log)["stream_error"])
}

// Review follow-up: the 16KB prefix is cut before masking. When masking
// shortens the text below the 4000-character cap, the cap no longer trims, so
// the split token at the 16KB edge has to be dropped before masking.
func TestStreamTerminalRelayErrorInputDropsTokenSplitAtMatchBound(t *testing.T) {
	unit := "api_key:" + strings.Repeat("s", 60) + " "
	head := strings.Repeat(unit, (operation_setting.MaxRelayErrorMatchLen-40)/len(unit))
	head += strings.Repeat(" ", operation_setting.MaxRelayErrorMatchLen-6-len(head))
	payload, err := common.Marshal(map[string]any{"error": map[string]any{"message": head + "internal-gw.corp.example refused"}})
	require.NoError(t, err)
	in := streamTerminalRelayErrorInput(errorDisplayCtx(), payload, "fallback")
	assert.True(t, in.Truncated)
	assert.Less(t, utf8.RuneCountInString(in.Message), operation_setting.MaxRelayErrorTextLen, "masking shortened it below the cap")
	assert.NotContains(t, in.Message, "intern", "half a host at the 16KB edge is not kept")
	assert.NotContains(t, in.Message, "ssssssss")
}

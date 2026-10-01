package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// streamFailureOtherWithInput is a stream-failure error log as settlement
// writes it now: error_code is the stream-failure marker and admin_info keeps
// the input the terminal frame was decided with.
func streamFailureOtherWithInput(marker, frameCode, frameMessage string) string {
	return `{"error_code":"` + marker + `","admin_info":{"use_channel":["7"],` +
		`"stream_error_code":"` + frameCode + `","stream_error_message":"` + frameMessage + `"},` +
		`"stream_result":{"failed":true,"settlement_state":"failed","usage_source":"none"}}`
}

func streamFailureLogWithInput(marker, frameCode string) *Log {
	return &Log{
		Type: LogTypeError, RequestId: "rid-sm",
		Content: "note; quota low for group vip (request id: rid-sm)",
		Other:   streamFailureOtherWithInput(marker, frameCode, "quota low for group vip"),
	}
}

// Regression (review M3): the user's view of a stream-failure error log is
// decided with the terminal frame's own error code and text, as the frame was;
// the list view reads them before the projection removes admin_info.
func TestFormatUserLogs_StreamFailureUsesTerminalFrameInput(t *testing.T) {
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "Unavailable",
		Rules: `[{"source":"upstream","error_codes":["insufficient_quota"],"action":"replace","message":"Busy"},` +
			`{"source":"upstream","error_codes":["upstream_stream_error"],"action":"keep"},` +
			`{"source":"local","error_codes":["relay_timeout"],"keywords":["quota low"],"action":"replace","message":"Timed out"}]`})

	byCode := streamFailureLogWithInput(operation_setting.RelayStreamErrorCode, "insufficient_quota")
	formatUserLogs([]*Log{byCode}, 0)
	assert.Equal(t, "Busy (request id: rid-sm)", byCode.Content, "a rule keyed on the frame's own code")
	assert.NotContains(t, byCode.Other, "stream_error_code", "the recorded input is admin-only")
	assert.NotContains(t, byCode.Other, "admin_info")
	other, err := common.StrToMap(byCode.Other)
	require.NoError(t, err)
	assert.Equal(t, operation_setting.RelayStreamErrorCode, other["error_code"], "the marker is ours and stays")

	withOtherCode := streamFailureLogWithInput(operation_setting.RelayStreamErrorCode, "server_error")
	formatUserLogs([]*Log{withOtherCode}, 0)
	assert.Equal(t, "Unavailable (request id: rid-sm)", withOtherCode.Content,
		"keep on upstream_stream_error does not keep a frame that had its own code")

	kept := streamFailureLogWithInput(operation_setting.RelayStreamErrorCode, operation_setting.RelayStreamErrorCode)
	formatUserLogs([]*Log{kept}, 0)
	assert.Equal(t, "note; quota low for group vip (request id: rid-sm)", kept.Content, "kept as the frame was")

	timedOut := streamFailureLogWithInput(operation_setting.RelayTimeoutErrorCode, operation_setting.RelayTimeoutErrorCode)
	formatUserLogs([]*Log{timedOut}, 0)
	assert.Equal(t, "Timed out (request id: rid-sm)", timedOut.Content, "our deadline: local, matched with the frame's text")

	// A row written before the input was recorded falls back to the marker
	// and the whole content.
	old := &Log{Type: LogTypeError, RequestId: "rid-old", Content: "quota low (request id: rid-old)",
		Other: `{"error_code":"upstream_stream_error","stream_result":{"failed":true}}`}
	formatUserLogs([]*Log{old}, 0)
	assert.Equal(t, "quota low (request id: rid-old)", old.Content, "kept by the upstream_stream_error rule, as before")

	// The self-service export reads the raw row and decides the same way.
	raw := streamFailureLogWithInput(operation_setting.RelayStreamErrorCode, "insufficient_quota")
	assert.Equal(t, "Busy (request id: rid-sm)", MaskErrorLogContentForUser(raw, "en"))
}

// With the feature off the row is only projected: the recorded input never
// reaches the user either way.
func TestFormatUserLogs_StreamFailureInputHiddenWhenDisabled(t *testing.T) {
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: false, HideUpstreamErrors: true})
	log := streamFailureLogWithInput(operation_setting.RelayStreamErrorCode, "insufficient_quota")
	formatUserLogs([]*Log{log}, 0)
	assert.Equal(t, "note; quota low for group vip (request id: rid-sm)", log.Content)
	assert.NotContains(t, log.Other, "stream_error_code")
	assert.NotContains(t, log.Other, "insufficient_quota")
}

func TestStoredStreamTerminalInput(t *testing.T) {
	assert.Nil(t, storedStreamTerminalInput(nil))
	consume := streamFailureLogWithInput(operation_setting.RelayStreamErrorCode, "x")
	consume.Type = LogTypeConsume
	assert.Nil(t, storedStreamTerminalInput(consume), "only error logs")
	assert.Nil(t, storedStreamTerminalInput(&Log{Type: LogTypeError, Other: `{"error_code":"upstream_stream_error"}`}), "older rows")
	assert.Nil(t, storedStreamTerminalInput(&Log{Type: LogTypeError, Other: `{"stream_error_code":"x"}`}), "only inside admin_info")
	assert.Nil(t, storedStreamTerminalInput(&Log{Type: LogTypeError, Other: `{"admin_info":"stream_error_code"}`}), "admin_info not an object")
	assert.Nil(t, storedStreamTerminalInput(&Log{Type: LogTypeError, Other: `{"admin_info":{"stream_error_code":7,"stream_error_message":"m"}}`}), "code not a string")
	assert.Nil(t, storedStreamTerminalInput(&Log{Type: LogTypeError, Other: `{"admin_info":{"stream_error_code":"c"}}`}), "message missing")
	assert.Nil(t, storedStreamTerminalInput(&Log{Type: LogTypeError, Other: `{"admin_info":{"stream_error_code":"c"`}), "malformed")

	in := storedStreamTerminalInput(&Log{Type: LogTypeError, Other: `{"admin_info":{"stream_error_code":"","stream_error_message":"m"}}`})
	require.NotNil(t, in, "an empty code is the frame's own (empty) code")
	assert.Equal(t, "", in.ErrorCode)
	assert.Equal(t, "m", in.Message)
}

// Regression (review M4): the upstream text of a charged stream failure lives
// only in admin_info.stream_error; admin exports append it to 详情.
func TestAdminLogExportContent(t *testing.T) {
	assert.Equal(t, "", AdminLogExportContent(nil))
	assert.Equal(t, "done", AdminLogExportContent(&Log{Content: "done", Other: `{"admin_info":{"use_channel":["7"]}}`}))
	assert.Equal(t, "note; stream_error: overloaded",
		AdminLogExportContent(&Log{Content: "note", Other: `{"admin_info":{"stream_error":"overloaded"}}`}))
	assert.Equal(t, "stream_error: overloaded",
		AdminLogExportContent(&Log{Other: `{"admin_info":{"stream_error":"overloaded"}}`}))
	assert.Equal(t, "note", AdminLogExportContent(&Log{Content: "note", Other: `{"stream_error":"top level is not admin_info"}`}))
	assert.Equal(t, "note", AdminLogExportContent(&Log{Content: "note", Other: `{"admin_info":{"stream_error":7}}`}))
	assert.Equal(t, "note", AdminLogExportContent(&Log{Content: "note", Other: `{"admin_info":{"stream_error":""}}`}))
	assert.Equal(t, "note", AdminLogExportContent(&Log{Content: "note", Other: `{"admin_info":{"stream_error":"x"`}), "malformed other")

	// The export center's 详情 column (admin-only) does the same, reading other.
	col, ok := LookupLogExportColumn("content")
	require.True(t, ok)
	assert.True(t, col.NeedOther, "other must be selected for the stream error")
	assert.Equal(t, "note; stream_error: overloaded",
		renderOne(t, "content", &Log{Content: "note", Other: `{"admin_info":{"stream_error":"overloaded"}}`}, newTestRowCtx()))
	assert.Equal(t, "note", renderOne(t, "content", &Log{Content: "note"}, newTestRowCtx()))

	// A user's own export never includes it.
	consume := &Log{Type: LogTypeConsume, Content: "note", Other: `{"admin_info":{"stream_error":"overloaded"}}`}
	assert.Equal(t, "note", MaskErrorLogContentForUser(consume, "en"))
}

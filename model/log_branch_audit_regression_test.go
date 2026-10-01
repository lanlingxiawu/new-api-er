// Regression tests for defects in the fork's log views found by the branch
// audit (docs/design/branch-audit-vs-main.md M3, L2, L3). M4 is fixed where the
// consume log is written: service TestStreamFailureMessagesReachSettlementLogs.

package model

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// upstreamStreamFailureText is what service.StreamFailureLogMessage extracts
// from an upstream error frame (error.message) when a managed stream fails.
const upstreamStreamFailureText = "Your credit balance is too low to access the Anthropic API (org vip-azure-3)"

// streamFailureOther is what FinalizeConsumptionSettlement logged for a managed
// stream before it recorded error_code (service/log_info_generate.go
// GenerateTextOtherInfo + AppendStreamLogInfo): no error_type/error_code/
// status_code, and stream_status without error texts because privateErrors is
// set when StreamResult exists. Rows like it are still in the log table.
const streamFailureOther = `{"model_ratio":1,"group_ratio":1,"admin_info":{"use_channel":["7"]},` +
	`"stream_status":{"status":"error","end_reason":"upstream_error","error_count":1},` +
	`"stream_result":{"failed":true,"settlement_state":"failed","usage_source":"none"},"stream_diagnostic_attempt":0}`

// streamFailureOtherWithCode is the same row as written now: error_code says
// how the stream ended.
func streamFailureOtherWithCode(code string) string {
	return `{"model_ratio":1,"error_code":"` + code + `",` +
		`"stream_status":{"status":"error","end_reason":"timeout","error_count":1},` +
		`"stream_result":{"failed":true,"settlement_state":"failed","usage_source":"none"},"stream_diagnostic_attempt":0}`
}

// A managed stream that failed with zero charge is written as an ERROR log
// whose content is MessageWithCurrentRequestId(upstream error.message). The
// live client got the terminal frame decided as an upstream stream error
// (PresentStreamTerminalMessage), i.e. the fallback text. The user's view of
// the row must be decided the same way: before the fix the row had no
// error_type/error_code, was treated as a LOCAL error, and the upstream text
// the frame hid was shown in the usage log and the self-service export.
func TestBranchAuditRegression_ZeroChargeStreamFailureErrorLogShowsUpstreamText(t *testing.T) {
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "Unavailable"})
	previous := userLogLanguage
	userLogLanguage = func(int) string { return "en" }
	t.Cleanup(func() { userLogLanguage = previous })

	for name, other := range map[string]string{
		"row without error_code": streamFailureOther,
		"row with error_code":    streamFailureOtherWithCode(operation_setting.RelayStreamErrorCode),
	} {
		t.Run(name, func(t *testing.T) {
			newRow := func() *Log {
				return &Log{Type: LogTypeError, UserId: 1, RequestId: "rid-sf-1", Other: other,
					Content: common.MessageWithRequestId(upstreamStreamFailureText, "rid-sf-1")}
			}

			listed := newRow()
			formatUserLogs([]*Log{listed}, 0)
			assert.NotContains(t, listed.Content, "credit balance", "usage log list leaks the upstream stream error hidden from the live response")
			assert.Equal(t, "Unavailable (request id: rid-sf-1)", listed.Content)
			listedOther := otherOf(t, listed)
			assert.NotContains(t, listedOther, "error_type", "a stream failure's metadata is ours; nothing is added")
			assert.NotContains(t, listedOther, "status_code")

			exported := MaskErrorLogContentForUser(newRow(), "en")
			assert.Equal(t, "Unavailable (request id: rid-sf-1)", exported, "self-service export is masked too")
		})
	}
}

// Rules see a stream failure as the terminal frame did: an upstream error with
// code upstream_stream_error, or our own timeout (relay_timeout, 504). A rule's
// status override is not shown: the stream's status had already been sent.
func TestStreamFailureErrorLogFollowsTerminalFrameClassification(t *testing.T) {
	rules := `[{"source":"local","error_codes":["relay_timeout"],"status_codes":[504],"action":"replace","message":"Timed out","status_code":503},` +
		`{"source":"upstream","error_codes":["upstream_stream_error"],"keywords":["credit"],"action":"replace","message":"Busy","status_code":503}]`
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: false, Rules: rules})
	lang := func(int) string { return "en" }

	for _, tc := range []struct {
		name, other, want string
	}{
		{"upstream, new row", streamFailureOtherWithCode(operation_setting.RelayStreamErrorCode), "Busy (request id: r)"},
		{"upstream, row without code", streamFailureOther, "Busy (request id: r)"},
		{"our timeout", streamFailureOtherWithCode(operation_setting.RelayTimeoutErrorCode), "Timed out (request id: r)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := &Log{Type: LogTypeError, RequestId: "r", Other: tc.other, Content: upstreamStreamFailureText + " (request id: r)"}
			maskProjectedLogForUser(log, nil, lang)
			assert.Equal(t, tc.want, log.Content)
			assert.NotContains(t, log.Other, "status_code")
			assert.NotContains(t, log.Other, `"error_type"`)
		})
	}

	// A stream the client left, or an error log with its own type, is not a
	// stream failure: it keeps its own classification (local, no rule matches).
	for name, other := range map[string]string{
		"client gone":       `{"stream_result":{"failed":true,"client_gone":true}}`,
		"not failed":        `{"stream_result":{"failed":false,"settlement_state":"partial"}}`,
		"own error type":    `{"error_type":"new_api_error","error_code":"relay_timeout","status_code":504,"stream_result":{"failed":true}}`,
		"other error code":  `{"error_code":"insufficient_user_quota","stream_result":{"failed":true}}`,
		"odd stream_result": `{"stream_result":"failed"}`,
	} {
		t.Run(name, func(t *testing.T) {
			withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "Unavailable"})
			log := &Log{Type: LogTypeError, Other: other, Content: "ours"}
			maskProjectedLogForUser(log, nil, lang)
			assert.Equal(t, "ours", log.Content)
			assert.Equal(t, other, log.Other)
		})
	}
}

// maskErrorLogForUser rewrote error_type/error_code of a masked upstream error
// but left other.status_code. When the matching rule overrides the status (the
// preset "upstream quota" rule answers 503), the live client got 503 while the
// user's own log API still returned the upstream's real status (402 here), the
// very signal the override exists to hide.
func TestBranchAuditRegression_MaskedErrorLogKeepsOverriddenUpstreamStatus(t *testing.T) {
	rules := `[{"source":"upstream","keywords":["balance"],"action":"replace","message":"Busy","status_code":503}]`
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, Rules: rules})
	log := &Log{Type: LogTypeError, RequestId: "rid-st", Content: "insufficient balance (request id: rid-st)",
		Other: `{"error_type":"openai_error","error_code":"insufficient_quota","status_code":402,"big":9007199254740993}`}
	maskProjectedLogForUser(log, nil, func(int) string { return "en" })
	require.Equal(t, "Busy (request id: rid-st)", log.Content)
	var got map[string]json.RawMessage
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &got))
	assert.JSONEq(t, `"upstream_error"`, string(got["error_code"]))
	assert.Equal(t, "503", string(got["status_code"]), "the user sees the status the client got")
	assert.Equal(t, "9007199254740993", string(got["big"]))
}

// The status the client got is shown for a local error too, and nothing is
// added when the row records no status or the rule does not override it.
func TestMaskedErrorLogStatusOverrideBranches(t *testing.T) {
	rules := `[{"source":"local","error_codes":["model_not_found"],"action":"replace","message":"Unavailable model","status_code":404},` +
		`{"source":"upstream","action":"replace","message":"Busy"}]`
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, Rules: rules})
	lang := func(int) string { return "en" }

	local := &Log{Type: LogTypeError, Content: "no channel", Other: `{"error_type":"new_api_error","error_code":"model_not_found","status_code":503}`}
	maskProjectedLogForUser(local, nil, lang)
	assert.Equal(t, "Unavailable model", local.Content)
	assert.JSONEq(t, `{"error_type":"new_api_error","error_code":"model_not_found","status_code":404}`, local.Other)

	noStatus := &Log{Type: LogTypeError, Content: "no channel", Other: `{"error_type":"new_api_error","error_code":"model_not_found"}`}
	maskProjectedLogForUser(noStatus, nil, lang)
	assert.Equal(t, `{"error_type":"new_api_error","error_code":"model_not_found"}`, noStatus.Other, "no status recorded: none is invented")

	upstream := &Log{Type: LogTypeError, Content: "boom", Other: `{"error_type":"openai_error","error_code":"x","status_code":500}`}
	maskProjectedLogForUser(upstream, nil, lang)
	assert.JSONEq(t, `{"error_type":"new_api_error","error_code":"upstream_error","status_code":500}`, upstream.Other, "no override: the status stays")
}

// formatEmployeeLogs (employee view, model/log.go) re-encoded other through
// map[string]interface{} whenever a legacy channel_name was present, so every
// other integer beyond 2^53 was rounded. main's projection (formatLogOtherJSON)
// keeps RawMessage values, and so does this view now.
func TestBranchAuditRegression_EmployeeChannelNameStripLosesIntegerPrecision(t *testing.T) {
	logs := []*Log{
		{Other: `{"channel_name":"Azure-JP-secret","amount":9007199254740993}`},
		{Other: `{"admin_info":{"channel_name":"nested"},"amount":9007199254740993}`},
	}
	formatEmployeeLogs(logs)
	var got map[string]json.RawMessage
	require.NoError(t, common.UnmarshalJsonStr(logs[0].Other, &got))
	assert.NotContains(t, got, "channel_name")
	assert.Equal(t, "9007199254740993", string(got["amount"]))
	assert.Equal(t, `{"admin_info":{"channel_name":"nested"},"amount":9007199254740993}`, logs[1].Other,
		"only a top-level channel_name is removed; otherwise other is passed through as stored")
}

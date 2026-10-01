package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Branch audit (logging area): contracts of the fork's user/employee log views
// and of the read-time error masking that the existing tests leave implicit.

func branchAuditFixedLang(int) string { return "en" }

// The owner view projects first and masks second: whatever the masking rewrites,
// the admin/audit scopes, legacy channel keys and the joined channel name are
// already gone, and display ids replace real ids.
func TestBranchAuditFormatUserLogs_ProjectionThenMasking(t *testing.T) {
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "Unavailable"})
	previous := userLogLanguage
	userLogLanguage = branchAuditFixedLang
	t.Cleanup(func() { userLogLanguage = previous })

	logs := []*Log{
		{Id: 9001, Type: LogTypeError, UserId: 1, RequestId: "rid-a", ChannelId: 7, ChannelName: "Azure-JP-secret",
			Content: "upstream said no (request id: rid-a)",
			Other:   `{"error_type":"openai_error","error_code":"bad_response_status_code","status_code":500,"channel_name":"Azure-JP-secret","reject_reason":"policy","admin_info":{"use_channel":["7"]},"audit_info":{"route":"/x"},"root_info":{"raw":"secret"}}`},
		{Id: 9002, Type: LogTypeConsume, UserId: 1, ChannelId: 8, ChannelName: "Other-secret", Content: "ok", Other: `{"model_ratio":1}`},
	}
	formatUserLogs(logs, 20)

	assert.Equal(t, 21, logs[0].Id, "display ids start after the page offset")
	assert.Equal(t, 22, logs[1].Id)
	assert.Equal(t, "Unavailable (request id: rid-a)", logs[0].Content)
	for _, l := range logs {
		assert.Empty(t, l.ChannelName)
		assert.NotContains(t, l.Other, "secret")
		assert.NotContains(t, l.Other, "admin_info")
		assert.NotContains(t, l.Other, "audit_info")
		assert.NotContains(t, l.Other, "root_info")
		assert.NotContains(t, l.Other, "reject_reason")
	}
	assert.Equal(t, 7, logs[0].ChannelId, "the channel number stays visible, as upstream")
	other := otherOf(t, logs[0])
	assert.Equal(t, "new_api_error", other["error_type"])
	assert.Equal(t, "upstream_error", other["error_code"])
	assert.Equal(t, `{"model_ratio":1}`, logs[1].Other, "a consume log without stream errors is not re-encoded")
}

// A keep rule leaves the error untouched, so other must stay byte-identical (no
// float64 round trip) and the content keeps the upstream text by design.
func TestBranchAuditMaskLogForUser_KeepRuleDoesNotReencode(t *testing.T) {
	rules := `[{"source":"upstream","keywords":["context length"],"action":"keep"}]`
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "Unavailable", Rules: rules})
	other := `{"error_type":"openai_error","error_code":"bad_response_status_code","status_code":400,"big":9007199254740993}`
	log := &Log{Type: LogTypeError, Content: "maximum context length exceeded (request id: r1)", RequestId: "r1", Other: other}
	maskProjectedLogForUser(log, nil, branchAuditFixedLang)
	assert.Equal(t, "maximum context length exceeded (request id: r1)", log.Content)
	assert.Equal(t, other, log.Other)
}

// A local error replaced by a rule gets the rule text, but its own (local) type
// and code are kept and other is not rewritten.
func TestBranchAuditMaskLogForUser_LocalErrorKeepsItsCode(t *testing.T) {
	rules := `[{"source":"local","error_codes":["model_not_found"],"action":"replace","message":"Model unavailable"}]`
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, Rules: rules})
	other := `{"error_type":"new_api_error","error_code":"model_not_found","status_code":503,"big":9007199254740993}`
	log := &Log{Type: LogTypeError, Content: "no channel for model x under group vip (request id: r2)", RequestId: "r2", Other: other}
	maskProjectedLogForUser(log, nil, branchAuditFixedLang)
	assert.Equal(t, "Model unavailable (request id: r2)", log.Content)
	assert.Equal(t, other, log.Other, "a local error's metadata is ours and stays as stored")

	// Without any matching rule a local error is never hidden by the upstream fallback.
	plain := &Log{Type: LogTypeError, Content: "insufficient user quota", Other: `{"error_type":"new_api_error","error_code":"insufficient_user_quota","status_code":403}`}
	maskProjectedLogForUser(plain, nil, branchAuditFixedLang)
	assert.Equal(t, "insufficient user quota", plain.Content)
}

// An error log without (or with unparsable) metadata is matched as a local error
// and must not panic; other is left as it was.
func TestBranchAuditMaskLogForUser_MissingOrBrokenOther(t *testing.T) {
	rules := `[{"source":"local","keywords":["boom"],"action":"replace","message":"Failed"}]`
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, Rules: rules})
	for _, other := range []string{"", "not-json", `{"status_code":"500"}`} {
		log := &Log{Type: LogTypeError, Content: "boom", Other: other}
		require.NotPanics(t, func() { maskProjectedLogForUser(log, nil, branchAuditFixedLang) })
		assert.Equal(t, "Failed", log.Content, "no request id: the rule text alone")
		assert.Equal(t, other, log.Other)
	}
}

// stream_status is rewritten entry by entry: only string texts are matched,
// other shapes are left alone rather than dropped.
func TestBranchAuditMaskStreamStatus_OddShapes(t *testing.T) {
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "Unavailable"})
	cases := map[string]struct {
		other string
		want  string
	}{
		"non-string entries kept": {
			other: `{"stream_status":{"status":"error","errors":["leak",42,null,{"m":"x"}]}}`,
			want:  `{"stream_status":{"errors":["Unavailable",42,null,{"m":"x"}],"status":"error"}}`,
		},
		"errors not an array": {
			other: `{"stream_status":{"status":"error","errors":"leak"}}`,
			want:  `{"stream_status":{"status":"error","errors":"leak"}}`,
		},
		"stream_status not an object": {
			other: `{"stream_status":"error"}`,
			want:  `{"stream_status":"error"}`,
		},
		"end_error not a string": {
			other: `{"stream_status":{"status":"error","end_error":{"msg":"leak"}}}`,
			want:  `{"stream_status":{"status":"error","end_error":{"msg":"leak"}}}`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			log := &Log{Type: LogTypeConsume, Content: "ours", Other: tc.other}
			maskProjectedLogForUser(log, nil, branchAuditFixedLang)
			assert.JSONEq(t, tc.want, log.Other)
			if tc.other == tc.want {
				assert.Equal(t, tc.other, log.Other, "untouched metadata is not re-encoded")
			}
			assert.Equal(t, "ours", log.Content)
		})
	}
}

// The self-service export uses MaskErrorLogContentForUser; it must be a pure
// function of its input and never alter the row the caller still holds.
func TestBranchAuditMaskErrorLogContentForUser_Contract(t *testing.T) {
	assert.Equal(t, "", MaskErrorLogContentForUser(nil, "en"))

	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: false, HideUpstreamErrors: true, DefaultMessage: "Unavailable"})
	errLog := upstreamErrorLog()
	assert.Equal(t, errLog.Content, MaskErrorLogContentForUser(errLog, "en"), "switched off: original text")

	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "Unavailable"})
	consume := &Log{Type: LogTypeConsume, Content: "model ratio 1", Other: `{"error_type":"openai_error"}`}
	assert.Equal(t, "model ratio 1", MaskErrorLogContentForUser(consume, "en"), "only error logs are rewritten")

	errLog = upstreamErrorLog()
	beforeContent, beforeOther := errLog.Content, errLog.Other
	assert.Equal(t, "Unavailable (request id: rid-1)", MaskErrorLogContentForUser(errLog, "en"))
	assert.Equal(t, beforeContent, errLog.Content, "the caller's row is not modified")
	assert.Equal(t, beforeOther, errLog.Other)
}

// Employee view: the channel name and the root-only and audit scopes go. The
// documented contract (docs/design/relay-error-message-masking.md §4.1) keeps
// the channel id, the admin scope (the employee page shows its retry chain;
// stream_error* only repeats the unmasked error text employees read by design)
// and the unmasked error text.
func TestBranchAuditFormatEmployeeLogs_Contract(t *testing.T) {
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "Unavailable"})
	untouched := `{"model_ratio":1,"admin_info":{"use_channel":["7"],"stream_error":"raw upstream"}}`
	logs := []*Log{
		{ChannelId: 7, ChannelName: "Azure-JP-secret", Type: LogTypeError, Content: "upstream text",
			Other: `{"channel_name":"Azure-JP-secret","error_type":"openai_error","admin_info":{"use_channel":["7"],"stream_error_message":"upstream text"},"root_info":{"key":"sk-root"},"audit_info":{"route":"/api/x"},"amount":9007199254740993}`},
		{ChannelId: 8, ChannelName: "x", Other: untouched},
		{ChannelId: 9, Other: `{"channel_name":`},
		{ChannelId: 10, Other: ""},
		{ChannelId: 11, Other: `{"root_info":{"key":"sk-root"}`},
		{ChannelId: 12, Other: `{"audit_info":{"route":"/api/x"}}`},
	}
	formatEmployeeLogs(logs)
	for _, l := range logs {
		assert.Empty(t, l.ChannelName)
	}
	other := otherOf(t, logs[0])
	assert.NotContains(t, other, "channel_name")
	assert.NotContains(t, other, "root_info")
	assert.NotContains(t, other, "audit_info")
	assert.Contains(t, other, "admin_info")
	assert.Contains(t, logs[0].Other, `"amount":9007199254740993`, "other values keep their raw JSON")
	assert.Equal(t, "upstream text", logs[0].Content, "employee view is not masked")
	assert.Equal(t, 7, logs[0].ChannelId)
	assert.Equal(t, untouched, logs[1].Other, "nothing to remove: not re-encoded")
	assert.Equal(t, "{}", logs[2].Other, "unparsable other that names a removed key is not passed through")
	assert.Equal(t, "", logs[3].Other)
	assert.Equal(t, "{}", logs[4].Other, "a truncated root_info never reaches an employee")
	assert.Equal(t, "{}", logs[5].Other)
}

// End to end on the real log DB: the owner list is scoped to the owner, the
// time range is inclusive on both ends, and masking runs on the page.
func TestBranchAuditGetUserLogs_ScopeRangeAndMasking(t *testing.T) {
	requireLogDB(t)
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "Unavailable"})
	previous := userLogLanguage
	userLogLanguage = branchAuditFixedLang
	t.Cleanup(func() { userLogLanguage = previous })

	owner, stranger := nextTestID(), nextTestID()
	logCleanupUser(t, owner)
	logCleanupUser(t, stranger)
	start := common.GetTimestamp() - 1000
	end := start + 100
	rid := uniq("rid")
	for _, at := range []int64{start - 1, start, end, end + 1} {
		at := at
		mkLogRow(t, func(l *Log) {
			l.UserId, l.Type, l.CreatedAt, l.RequestId = owner, LogTypeError, at, rid
			l.Content = "upstream secret text (request id: " + rid + ")"
			l.Other = `{"error_type":"openai_error","error_code":"bad_response_status_code","status_code":500}`
		})
	}
	mkLogRow(t, func(l *Log) { l.UserId, l.Type, l.CreatedAt = stranger, LogTypeError, start+1 })

	logs, total, err := GetUserLogs(owner, LogTypeError, start, end, "", "", 0, 10, 0, "", "", "")
	require.NoError(t, err)
	assert.EqualValues(t, 2, total, "created_at == start and == end are both inside")
	require.Len(t, logs, 2)
	for i, l := range logs {
		assert.Equal(t, owner, l.UserId)
		assert.Equal(t, i+1, l.Id)
		assert.Equal(t, "Unavailable (request id: "+rid+")", l.Content)
	}
	assert.GreaterOrEqual(t, logs[0].CreatedAt, logs[1].CreatedAt, "newest first")
}

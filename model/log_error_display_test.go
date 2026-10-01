package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withLiveRelayErrorDisplay(t *testing.T, setting operation_setting.RelayErrorDisplaySetting) {
	t.Helper()
	previous := operation_setting.GetRelayErrorDisplaySetting()
	operation_setting.ReplaceRelayErrorDisplaySetting(setting)
	t.Cleanup(func() { operation_setting.ReplaceRelayErrorDisplaySetting(previous) })
}

func upstreamErrorLog() *Log {
	return &Log{
		Type:      LogTypeError,
		Content:   "token quota is not enough, token remain quota: $0.000002 (request id: rid-1)",
		RequestId: "rid-1",
		Other:     `{"error_type":"openai_error","error_code":"pre_consume_token_quota_failed","status_code":403,"stream_diagnostic":{"body_head_base64":"eyJ1cHN0cmVhbSI6MX0="},"admin_info":{"use_channel":["7"]}}`,
	}
}

func TestFormatUserLogs_MasksUpstreamErrorWhenEnabled(t *testing.T) {
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "服务暂时不可用"})
	log := upstreamErrorLog()
	// Every list read goes through Find, whose AfterFind hook removes the
	// captured upstream bytes before masking sees the row.
	require.NoError(t, log.AfterFind(nil))
	logs := []*Log{log}
	formatUserLogs(logs, 0)

	assert.Equal(t, "服务暂时不可用 (request id: rid-1)", logs[0].Content)
	other, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	assert.Equal(t, "upstream_error", other["error_code"])
	assert.Equal(t, "new_api_error", other["error_type"])
	assert.NotContains(t, other, "stream_diagnostic", "captured upstream bytes are not shown to users")
	assert.NotContains(t, other, "admin_info")
}

func TestFormatUserLogs_ErrorLogUnchangedWhenDisabled(t *testing.T) {
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: false, HideUpstreamErrors: true})
	log := upstreamErrorLog()
	original := log.Content
	formatUserLogs([]*Log{log}, 0)
	assert.Equal(t, original, log.Content)
	other, err := common.StrToMap(log.Other)
	require.NoError(t, err)
	assert.Equal(t, "pre_consume_token_quota_failed", other["error_code"])
}

func TestFormatUserLogs_ConsumeLogsNeverMasked(t *testing.T) {
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true})
	log := &Log{Type: LogTypeConsume, Content: "模型倍率 1", Other: `{"error_type":"openai_error"}`}
	formatUserLogs([]*Log{log}, 0)
	assert.Equal(t, "模型倍率 1", log.Content)
}

// A stream that failed after output is settled as a consume log; the legacy
// stream path records the upstream's error text in stream_status. With upstream
// errors hidden, the user must not read it there either.
func streamFailedConsumeLog() *Log {
	return &Log{
		Type:    LogTypeConsume,
		Content: "模型倍率 1",
		Other:   `{"stream_status":{"status":"error","end_reason":"upstream_error","end_error":"upstream: No available channel for model x under group ChatGPT_AZ","error_count":1,"errors":["token remain quota: $0.000002"]}}`,
	}
}

func streamStatusOf(t *testing.T, log *Log) map[string]interface{} {
	t.Helper()
	other, err := common.StrToMap(log.Other)
	require.NoError(t, err)
	status, ok := other["stream_status"].(map[string]interface{})
	require.True(t, ok)
	return status
}

func TestFormatUserLogs_MasksStreamStatusErrorsWhenHidden(t *testing.T) {
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "服务暂时不可用"})
	log := streamFailedConsumeLog()
	formatUserLogs([]*Log{log}, 0)

	assert.Equal(t, "模型倍率 1", log.Content, "consume content is ours, never rewritten")
	status := streamStatusOf(t, log)
	assert.Equal(t, "服务暂时不可用", status["end_error"])
	assert.Equal(t, []interface{}{"服务暂时不可用"}, status["errors"])
	assert.Equal(t, "upstream_error", status["end_reason"], "the local classification stays")
	assert.Equal(t, float64(1), status["error_count"])
}

// Rules apply as to any upstream error: keep leaves the text, replace uses the rule.
func TestFormatUserLogs_StreamStatusFollowsRules(t *testing.T) {
	rules := `[{"source":"upstream","keywords":["quota"],"action":"replace","message":"服务繁忙"},{"source":"any","keywords":["no available channel"],"action":"keep"}]`
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: false, Rules: rules})
	log := streamFailedConsumeLog()
	formatUserLogs([]*Log{log}, 0)

	status := streamStatusOf(t, log)
	assert.Equal(t, "upstream: No available channel for model x under group ChatGPT_AZ", status["end_error"])
	assert.Equal(t, []interface{}{"服务繁忙"}, status["errors"])
}

func TestFormatUserLogs_StreamStatusUnchangedWhenDisabled(t *testing.T) {
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: false, HideUpstreamErrors: true})
	log := streamFailedConsumeLog()
	formatUserLogs([]*Log{log}, 0)
	status := streamStatusOf(t, log)
	assert.Equal(t, "upstream: No available channel for model x under group ChatGPT_AZ", status["end_error"])
	assert.Equal(t, []interface{}{"token remain quota: $0.000002"}, status["errors"])
}

// A healthy stream has no error text; nothing is added.
func TestFormatUserLogs_StreamStatusWithoutErrorsUntouched(t *testing.T) {
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true})
	log := &Log{Type: LogTypeConsume, Other: `{"stream_status":{"status":"ok","end_reason":"done"}}`}
	formatUserLogs([]*Log{log}, 0)
	status := streamStatusOf(t, log)
	assert.NotContains(t, status, "end_error")
	assert.NotContains(t, status, "errors")
}

// With no fallback text configured, the built-in one is shown in the user's own
// language; the language is looked up once per page.
func TestFormatUserLogs_BuiltInFallbackInUserLanguage(t *testing.T) {
	require.NoError(t, i18n.Init())
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true})
	lookups := 0
	previous := userLogLanguage
	userLogLanguage = func(userId int) string { lookups++; return "en" }
	t.Cleanup(func() { userLogLanguage = previous })

	logs := []*Log{upstreamErrorLog(), upstreamErrorLog(), streamFailedConsumeLog()}
	formatUserLogs(logs, 0)

	english := i18n.Translate(i18n.LangEn, i18n.MsgRelayErrorDefaultMessage)
	assert.Equal(t, english+" (request id: rid-1)", logs[0].Content)
	assert.Equal(t, english, streamStatusOf(t, logs[2])["end_error"])
	assert.Equal(t, 1, lookups)
}

// Switched off, nothing is checked and no language is looked up.
func TestFormatUserLogs_DisabledSkipsLanguageLookup(t *testing.T) {
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: false, HideUpstreamErrors: true})
	previous := userLogLanguage
	userLogLanguage = func(userId int) string { t.Fatal("no lookup while disabled"); return "" }
	t.Cleanup(func() { userLogLanguage = previous })
	formatUserLogs([]*Log{upstreamErrorLog(), streamFailedConsumeLog()}, 0)
}

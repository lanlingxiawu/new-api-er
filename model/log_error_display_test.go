package model

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordErrorLogCapturesRequestGroupBeforeEnqueue(t *testing.T) {
	resetRelayLogPipelineForTest()
	previousAccepting := relayLogAccepting.Load()
	cfg := operation_setting.GetRelayLogPipelineSetting()
	previousConfig := *cfg
	cfg.Enabled = true
	relayLogAccepting.Store(true)
	t.Cleanup(func() {
		resetRelayLogPipelineForTest()
		relayLogAccepting.Store(previousAccepting)
		*cfg = previousConfig
	})
	// Capture even while masking is disabled, so future rules cover these rows.
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{})
	for _, tc := range []struct {
		name, using, auto, want string
		seeded                  bool
	}{
		{"concrete", "vip", "", "vip", false},
		{"auto", "auto", "vip", "vip", false},
		{"unresolved", "auto", "", "", false},
		{"no validated group", "", "", "", false},
		{"preserve terminal group", "default", "next", "vip", true},
		{"preserve terminal unknown", "vip", "next", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			common.SetContextKey(c, constant.ContextKeyUsingGroup, tc.using)
			common.SetContextKey(c, constant.ContextKeyAutoGroup, tc.auto)
			common.SetContextKey(c, constant.ContextKeyUserGroup, "registered-group")
			var other *LogOther
			if tc.seeded {
				other = NewLogOther()
				other.SetAdmin(operation_setting.RelayErrorGroupKey, tc.want)
			}
			RecordErrorLog(c, 42, 7, "m", "t", "upstream failed", 0, 0, true, "public-group", other)
			common.SetContextKey(c, constant.ContextKeyAutoGroup, "changed-after-enqueue")
			events := takeRelayLogBatch(relayLogKindError)
			require.Len(t, events, 1)
			row := events[0].Log
			assert.Equal(t, "public-group", row.Group)
			admin := parseLogOtherFields(row.Other).object(logOtherAdminInfoKey)
			raw, exists := admin[operation_setting.RelayErrorGroupKey]
			require.True(t, exists)
			value, ok := rawJSONString(raw)
			require.True(t, ok)
			assert.Equal(t, tc.want, value)
			assert.NotContains(t, formatLogOtherJSON(row.Other, logOtherVisibilityUser), operation_setting.RelayErrorGroupKey)
		})
	}
}

func withLiveRelayErrorDisplay(t *testing.T, setting operation_setting.RelayErrorDisplaySetting) {
	t.Helper()
	previous := operation_setting.GetRelayErrorDisplaySetting()
	operation_setting.ReplaceRelayErrorDisplaySetting(setting)
	t.Cleanup(func() { operation_setting.ReplaceRelayErrorDisplaySetting(previous) })
}

func TestUserErrorLogs_RequestGroupSnapshotAndLegacyFallback(t *testing.T) {
	withLiveRelayErrorDisplay(t, operation_setting.RelayErrorDisplaySetting{Enabled: true, Rules: `[
		{"groups":["vip"],"source":"any","action":"replace","message":"VIP busy"},
		{"source":"any","action":"replace","message":"Global busy"}
	]`})
	previous := userLogLanguage
	userLogLanguage = func(int) string { return "en" }
	t.Cleanup(func() { userLogLanguage = previous })
	for _, tc := range []struct{ name, group, admin, want string }{
		{"legacy concrete group", "vip", `{}`, "VIP busy"},
		{"legacy auto is unknown", "auto", `{}`, "Global busy"},
		{"legacy empty is unknown", "", `{}`, "Global busy"},
		{"snapshot resolves auto", "auto", `{"relay_error_group":"vip"}`, "VIP busy"},
		{"snapshot wins over public group", "vip", `{"relay_error_group":"default"}`, "Global busy"},
		{"explicit unknown never falls back", "vip", `{"relay_error_group":""}`, "Global busy"},
		{"corrupt snapshot never falls back", "vip", `{"relay_error_group":5}`, "Global busy"},
		{"null snapshot never falls back", "vip", `{"relay_error_group":null}`, "Global busy"},
		{"corrupt metadata never falls back", "vip", `5`, "Global busy"},
		{"unreadable metadata never falls back", "vip", `{"relay_error_group":`, "Global busy"},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(tc.name+fmt.Sprint(stream), func(t *testing.T) {
				log := &Log{Type: LogTypeError, Group: tc.group, RequestId: "group-log", Content: "original quota"}
				if stream {
					log.Other = `{"error_code":"upstream_stream_error","stream_result":{"failed":true},"admin_info":` + tc.admin + `}`
				} else {
					log.Other = `{"error_type":"openai_error","error_code":"quota","status_code":403,"admin_info":` + tc.admin + `}`
				}
				assert.Equal(t, tc.want+" (request id: group-log)", MaskErrorLogContentForUser(log, "en"))
				assert.Equal(t, "original quota", log.Content, "export does not mutate the original row")
				formatUserLogs([]*Log{log}, 0)
				assert.Equal(t, tc.want+" (request id: group-log)", log.Content)
				assert.Equal(t, tc.group, log.Group)
				assert.NotContains(t, log.Other, "relay_error_group")
				assert.NotContains(t, log.Other, "admin_info")
			})
		}
	}
	for _, group := range []string{"vip", "default", "auto"} {
		log := streamFailedConsumeLog()
		log.Group = group
		formatUserLogs([]*Log{log}, 0)
		want := "Global busy"
		if group == "vip" {
			want = "VIP busy"
		}
		assert.Equal(t, want, streamStatusOf(t, log)["end_error"])
		assert.Equal(t, "模型倍率 1", log.Content)
	}
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

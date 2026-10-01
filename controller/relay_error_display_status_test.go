package controller

import (
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Review item 5: stored rules that no longer validate are skipped on their own
// and reported to admins, each reason naming its rule; a valid configuration
// reports nothing.
func TestGetRelayErrorDisplayStatus(t *testing.T) {
	require.NoError(t, i18n.Init())
	previous := operation_setting.GetRelayErrorDisplaySetting()
	t.Cleanup(func() { operation_setting.ReplaceRelayErrorDisplaySetting(previous) })

	tooComplex := strings.Repeat(`.{1,999}`, 24) + "Q"
	operation_setting.ReplaceRelayErrorDisplaySetting(operation_setting.RelayErrorDisplaySetting{
		Enabled: true, HideUpstreamErrors: true, DefaultMessage: "fallback",
		Rules: `[{"source":"any","action":"keep"},` +
			`{"source":"any","action":"edit","edits":[{"find":"x"},{"find":"` + strings.ReplaceAll(tooComplex, `\`, `\\`) + `","regex":true}]},` +
			`{"source":"nowhere","action":"keep"}]`,
	})
	ctx, out := callJSON(t, GetRelayErrorDisplayStatus, http.MethodGet, "/api/option/relay-error-display/status", "")
	require.True(t, out.Success)
	skipped, ok := out.Data["skipped"].([]any)
	require.True(t, ok, "%v", out.Data)
	require.Len(t, skipped, 2)
	first := skipped[0].(map[string]any)
	assert.EqualValues(t, 2, first["rule"])
	assert.Equal(t, i18n.T(ctx, i18n.MsgSettingRelayErrorEditTooComplex, map[string]any{"Rule": 2, "Step": 2}), first["reason"])
	second := skipped[1].(map[string]any)
	assert.EqualValues(t, 3, second["rule"])
	assert.Equal(t, i18n.T(ctx, i18n.MsgSettingRelayErrorRuleSkipped, map[string]any{"Rule": 3}), second["reason"])
	assert.Contains(t, second["reason"], "3")
	assert.True(t, operation_setting.RelayErrorDisplayEnabled(), "the rest stays in effect")

	// Parts that are not one rule say what happened to them.
	many := make([]string, operation_setting.MaxRelayErrorRules+1)
	for i := range many {
		many[i] = `{"source":"any","action":"keep"}`
	}
	for _, tc := range []struct {
		setting operation_setting.RelayErrorDisplaySetting
		part    string
		reason  string
	}{
		{operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, Rules: "{broken"},
			operation_setting.RelayErrorSkippedRules, i18n.T(ctx, i18n.MsgSettingRelayErrorRulesUnreadable)},
		{operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: strings.Repeat("m", operation_setting.MaxRelayErrorMessageLen+1)},
			operation_setting.RelayErrorSkippedDefaultMessage, i18n.T(ctx, i18n.MsgSettingRelayErrorDefaultMessageSkipped)},
		{operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, Rules: "[" + strings.Join(many, ",") + "]"},
			operation_setting.RelayErrorSkippedRuleLimit, i18n.T(ctx, i18n.MsgSettingRelayErrorRulesOverLimit, map[string]any{"Max": operation_setting.MaxRelayErrorRules})},
	} {
		operation_setting.ReplaceRelayErrorDisplaySetting(tc.setting)
		_, out = callJSON(t, GetRelayErrorDisplayStatus, http.MethodGet, "/api/option/relay-error-display/status", "")
		skipped, _ = out.Data["skipped"].([]any)
		require.Len(t, skipped, 1, tc.part)
		whole := skipped[0].(map[string]any)
		assert.Equal(t, tc.part, whole["part"])
		assert.EqualValues(t, 0, whole["rule"])
		assert.Equal(t, tc.reason, whole["reason"])
		assert.NotEqual(t, i18n.MsgSettingRelayErrorRulesUnreadable, whole["reason"], "translated, not the key")
	}

	operation_setting.ReplaceRelayErrorDisplaySetting(operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true})
	_, out = callJSON(t, GetRelayErrorDisplayStatus, http.MethodGet, "/api/option/relay-error-display/status", "")
	require.True(t, out.Success)
	assert.Empty(t, out.Data["skipped"])
}

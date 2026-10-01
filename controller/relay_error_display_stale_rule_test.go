package controller

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test report 2026-09-29 #10 / relay-control D-1: with an invalid rule in the
// stored setting (skipped by the runtime), PUT /api/option/
// relay_error_display_setting.enabled=false was refused with the stale rule's
// error, so the feature could not be switched off. Both save endpoints now
// check only what the save changes.

const staleRuleControllerRules = `[{"source":"upstream","keywords":["quota"],"action":"replace","message":"Busy"},{"source":"any","action":"edit","edits":[{"find":"a*","regex":true}]}]`

var staleRuleOptionKeys = []string{
	"relay_error_display_setting.enabled",
	"relay_error_display_setting.hide_upstream_errors",
	"relay_error_display_setting.default_message",
	"relay_error_display_setting.rules",
}

// withStaleRelayErrorRuleStored puts the stale rule set live and restores the
// live setting and the option rows afterwards (row-scoped, Rule 15.5).
func withStaleRelayErrorRuleStored(t *testing.T) {
	t.Helper()
	requireDB(t)
	require.NoError(t, i18n.Init())
	previous := operation_setting.GetRelayErrorDisplaySetting()
	var saved []model.Option
	require.NoError(t, model.DB.Where(map[string]any{"key": staleRuleOptionKeys}).Find(&saved).Error)
	t.Cleanup(func() {
		operation_setting.ReplaceRelayErrorDisplaySetting(previous)
		model.DB.Where(map[string]any{"key": staleRuleOptionKeys}).Delete(&model.Option{})
		for _, o := range saved {
			model.DB.Create(&o)
		}
	})
	operation_setting.ReplaceRelayErrorDisplaySetting(operation_setting.RelayErrorDisplaySetting{
		Enabled: true, HideUpstreamErrors: true, Rules: staleRuleControllerRules,
	})
	require.Len(t, operation_setting.RelayErrorDisplaySkipped(), 1, "fixture: rule 2 is skipped at runtime")
}

func staleRuleStoredOption(t *testing.T, key string) string {
	t.Helper()
	var o model.Option
	require.NoError(t, model.DB.Where(map[string]any{"key": key}).First(&o).Error)
	return o.Value
}

func TestUpdateOption_RelayErrorDisplayCanBeSwitchedOffWithStaleRule(t *testing.T) {
	withStaleRelayErrorRuleStored(t)

	_, out := callJSON(t, UpdateOption, http.MethodPut, "/api/option/",
		`{"key":"relay_error_display_setting.enabled","value":"false"}`)
	require.True(t, out.Success, out.Message)
	assert.False(t, operation_setting.GetRelayErrorDisplaySetting().Enabled)
	assert.False(t, operation_setting.RelayErrorDisplayEnabled())
	assert.Equal(t, "false", staleRuleStoredOption(t, "relay_error_display_setting.enabled"))
	assert.Equal(t, staleRuleControllerRules, operation_setting.GetRelayErrorDisplaySetting().Rules, "the stored rules are untouched")

	for _, body := range []string{
		`{"key":"relay_error_display_setting.hide_upstream_errors","value":"false"}`,
		`{"key":"relay_error_display_setting.default_message","value":"Try again later"}`,
		`{"key":"relay_error_display_setting.enabled","value":"true"}`,
	} {
		_, out = callJSON(t, UpdateOption, http.MethodPut, "/api/option/", body)
		assert.True(t, out.Success, "%s: %s", body, out.Message)
	}

	// A rules edit that adds another invalid rule is still refused, naming it.
	before := operation_setting.GetRelayErrorDisplaySetting()
	rules := `[{\"source\":\"upstream\",\"keywords\":[\"quota\"],\"action\":\"replace\",\"message\":\"Busy\"},{\"source\":\"any\",\"action\":\"edit\",\"edits\":[{\"find\":\"a*\",\"regex\":true}]},{\"source\":\"any\",\"action\":\"edit\",\"edits\":[{\"find\":\"b*\",\"regex\":true}]}]`
	ctx, out := callJSON(t, UpdateOption, http.MethodPut, "/api/option/",
		`{"key":"relay_error_display_setting.rules","value":"`+rules+`"}`)
	assert.False(t, out.Success)
	assert.Equal(t, i18n.T(ctx, i18n.MsgSettingRelayErrorEditInvalid, map[string]any{"Rule": 3, "Step": 1}), out.Message)
	assert.Equal(t, before, operation_setting.GetRelayErrorDisplaySetting())
}

func TestUpdateOptionGroup_RelayErrorDisplayCanBeSwitchedOffWithStaleRule(t *testing.T) {
	withStaleRelayErrorRuleStored(t)

	// What the settings page sends when only the switch changed: every field,
	// the rules re-serialized by the browser.
	reserialized := `[{\"action\":\"replace\",\"source\":\"upstream\",\"keywords\":[\"quota\"],\"message\":\"Busy\"},{\"action\":\"edit\",\"source\":\"any\",\"edits\":[{\"find\":\"a*\",\"regex\":true}]}]`
	_, out := callJSON(t, UpdateOptionGroup, http.MethodPut, "/api/option/group",
		`{"module":"relay_error_display_setting","values":{"enabled":"false","hide_upstream_errors":"true","default_message":"","rules":"`+reserialized+`"}}`)
	require.True(t, out.Success, out.Message)
	assert.False(t, operation_setting.GetRelayErrorDisplaySetting().Enabled)
	assert.False(t, operation_setting.RelayErrorDisplayEnabled())
	assert.Equal(t, strconv.FormatBool(false), staleRuleStoredOption(t, "relay_error_display_setting.enabled"))

	// Editing the stale rule without fixing it is refused.
	before := operation_setting.GetRelayErrorDisplaySetting()
	edited := `[{\"source\":\"upstream\",\"keywords\":[\"quota\"],\"action\":\"replace\",\"message\":\"Busy\"},{\"source\":\"any\",\"action\":\"edit\",\"edits\":[{\"find\":\"c*\",\"regex\":true}]}]`
	ctx, out := callJSON(t, UpdateOptionGroup, http.MethodPut, "/api/option/group",
		`{"module":"relay_error_display_setting","values":{"enabled":"true","rules":"`+edited+`"}}`)
	assert.False(t, out.Success)
	assert.Equal(t, i18n.T(ctx, i18n.MsgSettingRelayErrorEditInvalid, map[string]any{"Rule": 2, "Step": 1}), out.Message)
	assert.Equal(t, before, operation_setting.GetRelayErrorDisplaySetting())
}

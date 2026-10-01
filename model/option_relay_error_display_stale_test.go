package model

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test report 2026-09-29 #10 / relay-control D-1: with a stored rule that no
// longer validates (the runtime skips it), every save was checked against the
// whole merged setting, so switching the feature off — or changing
// hide_upstream_errors / the default message — was refused until the stale
// rule was removed. A save is now checked only in what it changes.

const (
	staleValidRule   = `{"source":"upstream","keywords":["quota"],"action":"replace","message":"Busy"}`
	staleInvalidRule = `{"source":"any","action":"edit","edits":[{"find":"a*","regex":true}]}`
	staleStoredRules = `[` + staleValidRule + `,` + staleInvalidRule + `]`
	// The same list as the settings page re-serializes it: other whitespace
	// and key order, same content.
	staleReserialized = `[ {"action":"replace","source":"upstream","keywords":["quota"],"message":"Busy"}, {"edits":[{"regex":true,"find":"a*"}],"action":"edit","source":"any"} ]`
)

func withStaleRelayErrorRule(t *testing.T) operation_setting.RelayErrorDisplaySetting {
	t.Helper()
	previous := operation_setting.GetRelayErrorDisplaySetting()
	t.Cleanup(func() { operation_setting.ReplaceRelayErrorDisplaySetting(previous) })
	live := operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, Rules: staleStoredRules}
	operation_setting.ReplaceRelayErrorDisplaySetting(live)
	skipped := operation_setting.RelayErrorDisplaySkipped()
	require.Len(t, skipped, 1, "fixture: the runtime skips the stale rule")
	require.Equal(t, 2, skipped[0].Rule)
	return live
}

func TestValidateOptionValue_RelayErrorDisplayStaleStoredRule(t *testing.T) {
	withStaleRelayErrorRule(t)

	for _, tc := range []struct{ key, value string }{
		{"relay_error_display_setting.enabled", "false"},
		{"relay_error_display_setting.enabled", "true"},
		{"relay_error_display_setting.hide_upstream_errors", "false"},
		{"relay_error_display_setting.default_message", "Try again later"},
		{"relay_error_display_setting.rules", staleStoredRules},
		{"relay_error_display_setting.rules", staleReserialized},
		// Removing the stale rule, or everything.
		{"relay_error_display_setting.rules", `[` + staleValidRule + `]`},
		{"relay_error_display_setting.rules", ""},
		// Adding a valid rule while the stale one stays.
		{"relay_error_display_setting.rules", `[` + staleValidRule + `,` + staleInvalidRule + `,{"source":"local","action":"keep"}]`},
	} {
		assert.NoError(t, validateOptionValue(tc.key, tc.value), "%s=%q", tc.key, tc.value)
	}

	// New or edited rules are still checked strictly, numbered as the admin sees them.
	for _, tc := range []struct {
		value string
		rule  int
	}{
		{`[` + staleValidRule + `,` + staleInvalidRule + `,{"source":"upstream","action":"replace"}]`, 3},
		{`[` + staleValidRule + `,{"source":"any","action":"edit","edits":[{"find":"b*","regex":true}]}]`, 2},
		{`[{"source":"nowhere","action":"keep"},` + staleInvalidRule + `]`, 1},
	} {
		err := validateOptionValue("relay_error_display_setting.rules", tc.value)
		require.Error(t, err, tc.value)
		assert.ErrorIs(t, err, operation_setting.ErrRelayErrorDisplayInvalid)
		var editErr *operation_setting.RelayErrorEditInvalid
		if errors.As(err, &editErr) {
			assert.Equal(t, tc.rule, editErr.Rule, tc.value)
		} else {
			assert.Contains(t, err.Error(), "rule ", tc.value)
		}
	}
	assert.Error(t, validateOptionValue("relay_error_display_setting.rules", `{`), "an unreadable new list is refused")
}

// A stored default message over the limit (older build / hand-edited row) is
// likewise only checked when the save changes it.
func TestValidateOptionValue_RelayErrorDisplayStaleDefaultMessage(t *testing.T) {
	previous := operation_setting.GetRelayErrorDisplaySetting()
	t.Cleanup(func() { operation_setting.ReplaceRelayErrorDisplaySetting(previous) })
	long := make([]rune, operation_setting.MaxRelayErrorMessageLen+1)
	for i := range long {
		long[i] = 'm'
	}
	operation_setting.ReplaceRelayErrorDisplaySetting(operation_setting.RelayErrorDisplaySetting{Enabled: true, DefaultMessage: string(long)})

	assert.NoError(t, validateOptionValue("relay_error_display_setting.enabled", "false"))
	assert.NoError(t, validateOptionValue("relay_error_display_setting.default_message", "short"))
	assert.Error(t, validateOptionValue("relay_error_display_setting.default_message", string(long)+"x"))
}

func TestRelayErrorDisplayRulesToCheck(t *testing.T) {
	assert.Equal(t, "", relayErrorDisplayRulesToCheck(staleStoredRules, staleStoredRules))
	assert.Equal(t, "", relayErrorDisplayRulesToCheck(staleStoredRules, "  "+staleStoredRules+"\n"))
	assert.Equal(t, "", relayErrorDisplayRulesToCheck(staleStoredRules, staleReserialized), "same content, other encoding")
	assert.Equal(t, "", relayErrorDisplayRulesToCheck("", "[]"))
	assert.Equal(t, "", relayErrorDisplayRulesToCheck("", ""))
	assert.Equal(t, "{", relayErrorDisplayRulesToCheck("", "{"), "unreadable drafts are validated as sent")
	assert.Equal(t, "{", relayErrorDisplayRulesToCheck("[]", "{"))
	// An unreadable stored list keeps nothing: every draft rule is new.
	assert.Equal(t, `[`+staleInvalidRule+`]`, relayErrorDisplayRulesToCheck("{", `[`+staleInvalidRule+`]`))
	// Stored rules become the always-valid placeholder, new ones stay, order kept.
	got := relayErrorDisplayRulesToCheck(staleStoredRules, `[{"source":"local","action":"keep","keywords":["n"]},`+staleInvalidRule+`]`)
	assert.Equal(t, `[{"source":"local","keywords":["n"],"action":"keep"},{"source":"any","action":"keep"}]`, got)
}

// The group save (settings page) sends every field, including the unchanged
// stale rules: switching off must go through and be applied.
func TestSaveConfigGroup_RelayErrorDisplayStaleRuleCanBeSwitchedOff(t *testing.T) {
	withRelayErrorDisplayRestore(t)
	withStaleRelayErrorRule(t)

	applied, err := SaveConfigGroup("relay_error_display_setting", map[string]string{
		"enabled": "false", "hide_upstream_errors": "true", "default_message": "", "rules": staleReserialized,
	})
	require.NoError(t, err)
	assert.True(t, applied)
	live := operation_setting.GetRelayErrorDisplaySetting()
	assert.False(t, live.Enabled)
	assert.False(t, operation_setting.RelayErrorDisplayEnabled(), "the error path sees the feature off")
	var stored Option
	require.NoError(t, DB.Where(commonKeyCol+" = ?", "relay_error_display_setting.enabled").First(&stored).Error)
	assert.Equal(t, "false", stored.Value)

	// Editing the rules in the same save is still strict.
	before := operation_setting.GetRelayErrorDisplaySetting()
	applied, err = SaveConfigGroup("relay_error_display_setting", map[string]string{
		"enabled": "true", "rules": `[` + staleValidRule + `,` + staleInvalidRule + `,{"source":"upstream","action":"replace"}]`,
	})
	assert.False(t, applied)
	assert.ErrorIs(t, err, operation_setting.ErrRelayErrorDisplayInvalid)
	assert.Equal(t, before, operation_setting.GetRelayErrorDisplaySetting(), "nothing is applied")
}

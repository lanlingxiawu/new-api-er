package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var relayErrorDisplayOptionKeys = []string{
	"relay_error_display_setting.enabled",
	"relay_error_display_setting.hide_upstream_errors",
	"relay_error_display_setting.default_message",
	"relay_error_display_setting.rules",
}

// saveRelayErrorDisplayGroup saves through the real settings path and restores
// both the live setting and the option rows afterwards.
func withRelayErrorDisplayRestore(t *testing.T) {
	t.Helper()
	requireDB(t)
	// The process initializes OptionMap at startup; the model test binary does not.
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	common.OptionMapRWMutex.Unlock()
	previous := operation_setting.GetRelayErrorDisplaySetting()
	var saved []Option
	require.NoError(t, DB.Where("`key` IN ?", relayErrorDisplayOptionKeys).Find(&saved).Error)
	t.Cleanup(func() {
		operation_setting.ReplaceRelayErrorDisplaySetting(previous)
		DB.Where("`key` IN ?", relayErrorDisplayOptionKeys).Delete(&Option{})
		for _, o := range saved {
			DB.Create(&o)
		}
	})
}

func TestSaveConfigGroup_RelayErrorDisplayAppliesAndPersists(t *testing.T) {
	withRelayErrorDisplayRestore(t)
	rules := `[{"source":"upstream","keywords":["quota"],"action":"replace","message":"服务繁忙","status_code":503}]`
	applied, err := SaveConfigGroup("relay_error_display_setting", map[string]string{
		"enabled": "true", "hide_upstream_errors": "false", "default_message": "稍后再试", "rules": rules,
	})
	require.NoError(t, err)
	assert.True(t, applied)

	live := operation_setting.GetRelayErrorDisplaySetting()
	assert.True(t, live.Enabled)
	assert.False(t, live.HideUpstreamErrors)
	assert.Equal(t, "稍后再试", live.DefaultMessage)
	assert.Equal(t, rules, live.Rules)
	d := operation_setting.DecideRelayError(operation_setting.RelayErrorInput{Upstream: true, StatusCode: 403, Message: "token quota is not enough"})
	assert.True(t, d.Replace, "the saved rules are live immediately")

	var stored Option
	require.NoError(t, DB.Where("`key` = ?", "relay_error_display_setting.rules").First(&stored).Error)
	assert.Equal(t, rules, stored.Value)
}

func TestSaveConfigGroup_RelayErrorDisplayRejectsInvalidRules(t *testing.T) {
	withRelayErrorDisplayRestore(t)
	before := operation_setting.GetRelayErrorDisplaySetting()
	applied, err := SaveConfigGroup("relay_error_display_setting", map[string]string{
		"enabled": "true", "rules": `[{"source":"upstream","action":"replace"}]`,
	})
	assert.False(t, applied)
	assert.ErrorIs(t, err, operation_setting.ErrRelayErrorDisplayInvalid, "the controller maps this to a specific message")
	assert.Equal(t, before, operation_setting.GetRelayErrorDisplaySetting(), "nothing is applied")
	var count int64
	DB.Model(&Option{}).Where("`key` = ? AND value = ?", "relay_error_display_setting.rules", `[{"source":"upstream","action":"replace"}]`).Count(&count)
	assert.Zero(t, count, "nothing is persisted")
}

func TestValidateRelayErrorDisplayFields(t *testing.T) {
	assert.NoError(t, validateRelayErrorDisplayFields(map[string]string{"enabled": "true", "hide_upstream_errors": "false", "default_message": "x", "rules": "[]"}))
	assert.Error(t, validateRelayErrorDisplayFields(map[string]string{"nope": "1"}))
	assert.Error(t, validateRelayErrorDisplayFields(map[string]string{"enabled": "maybe"}))
	assert.Error(t, validateRelayErrorDisplayFields(map[string]string{"hide_upstream_errors": "2"}))
}

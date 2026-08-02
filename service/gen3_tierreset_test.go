package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// commission_tier_reset_task.go — scheduler check / arm / rearm.
// Background scheduled task, off the relay hot path.
// ===========================================================================

// withTierResetSetting snapshots and restores the whole setting struct so the
// persisted last_reset_at side effect does not leak across tests.
// 必须走 Replace：GetCommissionTierResetSetting 返回的是不可变快照，
// 通过它写字段不会被后续读者看到。返回的取值函数每次都重新 Load，
// 这样用例才能观察到被测代码通过配置接口写回的 LastResetAt。
func withTierResetSetting(t *testing.T, mutate func(cfg *operation_setting.CommissionTierResetSetting)) func() *operation_setting.CommissionTierResetSetting {
	t.Helper()
	orig := *operation_setting.GetCommissionTierResetSetting()
	draft := orig
	mutate(&draft)
	operation_setting.ReplaceCommissionTierResetSetting(draft)
	t.Cleanup(func() { operation_setting.ReplaceCommissionTierResetSetting(orig) })
	return operation_setting.GetCommissionTierResetSetting
}

func TestTierReset_CheckFirstArm(t *testing.T) {
	cfg := withTierResetSetting(t, func(cfg *operation_setting.CommissionTierResetSetting) {
		cfg.Enabled = true
		cfg.LastResetAt = 0 // first enable => arm without back-running
	})
	runCommissionTierResetCheck()
	// After first arm, LastResetAt is set to the most recent schedule boundary.
	assert.Greater(t, cfg().LastResetAt, int64(0))
}

func TestTierReset_CheckDueRunsReset(t *testing.T) {
	cfg := withTierResetSetting(t, func(cfg *operation_setting.CommissionTierResetSetting) {
		cfg.Enabled = true
		cfg.LastResetAt = 1 // far in the past => a due boundary exists
	})
	runCommissionTierResetCheck()
	// With no employees the reset loop processes 0 but still advances LastResetAt.
	assert.Greater(t, cfg().LastResetAt, int64(1))
}

func TestTierReset_CheckNotDue(t *testing.T) {
	// LastResetAt in the future => due <= LastResetAt => early return, unchanged.
	future := time.Now().Add(365 * 24 * time.Hour).Unix()
	cfg := withTierResetSetting(t, func(cfg *operation_setting.CommissionTierResetSetting) {
		cfg.Enabled = true
		cfg.LastResetAt = future
	})
	runCommissionTierResetCheck()
	assert.Equal(t, future, cfg().LastResetAt)
}

func TestTierReset_Rearm(t *testing.T) {
	// Disabled => no-op.
	cfgDisabled := withTierResetSetting(t, func(cfg *operation_setting.CommissionTierResetSetting) {
		cfg.Enabled = false
		cfg.LastResetAt = 5
	})
	RearmCommissionTierResetScheduleIfNeeded()
	assert.EqualValues(t, 5, cfgDisabled().LastResetAt)

	// Enabled + armed + old boundary => moves forward.
	cfg := withTierResetSetting(t, func(cfg *operation_setting.CommissionTierResetSetting) {
		cfg.Enabled = true
		cfg.LastResetAt = 1
	})
	RearmCommissionTierResetScheduleIfNeeded()
	assert.Greater(t, cfg().LastResetAt, int64(1))
}

func TestTierReset_ArmAt(t *testing.T) {
	cfg := withTierResetSetting(t, func(cfg *operation_setting.CommissionTierResetSetting) {
		cfg.LastResetAt = 0
	})
	// at <= 0 => no-op.
	ArmCommissionTierResetAt(0)
	assert.EqualValues(t, 0, cfg().LastResetAt)

	// at > 0 => persisted.
	target := time.Now().Unix()
	ArmCommissionTierResetAt(target)
	assert.EqualValues(t, target, cfg().LastResetAt)
}

var _ = require.NoError

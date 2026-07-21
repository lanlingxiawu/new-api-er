package model

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bsResetBreaker zeroes the package-global circuit-breaker state so each test
// starts from a closed, failure-free breaker.
func bsResetBreaker() {
	businessStatsCircuitBreakerLock.Lock()
	businessStatsCircuitBreakerStateValue = businessStatsCircuitBreakerState{}
	businessStatsCircuitBreakerLock.Unlock()
}

func TestNormalizeBusinessStatsCircuitBreakerSetting(t *testing.T) {
	bsWithCircuitSetting(t, func(s *operation_setting.BusinessStatsCircuitBreakerSetting) {
		s.FailureThreshold = 0
		s.InitialCooldownSeconds = 0
		s.MaxCooldownSeconds = 0
		s.SideEffectDBTimeoutMs = 0
	})
	cfg := normalizeBusinessStatsCircuitBreakerSetting()
	assert.Equal(t, 3, cfg.FailureThreshold)
	assert.EqualValues(t, 60, cfg.InitialCooldownSeconds)
	// MaxCooldownSeconds < Initial -> raised to Initial
	assert.EqualValues(t, 60, cfg.MaxCooldownSeconds)
	assert.Equal(t, 800, cfg.SideEffectDBTimeoutMs)
}

func TestIsBusinessStatsCircuitOpen_Matrix(t *testing.T) {
	bsInitFallback(t)

	t.Run("manual disabled -> open even when enabled", func(t *testing.T) {
		bsResetBreaker()
		bsWithCircuitSetting(t, func(s *operation_setting.BusinessStatsCircuitBreakerSetting) {
			s.Enabled = true
			s.ManualDisabled = true
		})
		assert.True(t, IsBusinessStatsCircuitOpen())
		assert.Equal(t, "business stats manually disabled", BusinessStatsCircuitSkipReason())
	})

	t.Run("disabled feature -> closed", func(t *testing.T) {
		bsResetBreaker()
		bsWithCircuitSetting(t, func(s *operation_setting.BusinessStatsCircuitBreakerSetting) {
			s.Enabled = false
			s.ManualDisabled = false
		})
		assert.False(t, IsBusinessStatsCircuitOpen())
		assert.Equal(t, "", BusinessStatsCircuitSkipReason())
	})

	t.Run("hard disabled -> open", func(t *testing.T) {
		bsResetBreaker()
		bsWithCircuitSetting(t, func(s *operation_setting.BusinessStatsCircuitBreakerSetting) {
			s.Enabled = true
			s.ManualDisabled = false
		})
		businessStatsCircuitBreakerLock.Lock()
		businessStatsCircuitBreakerStateValue.hardDisabled = true
		businessStatsCircuitBreakerLock.Unlock()
		assert.True(t, IsBusinessStatsCircuitOpen())
		assert.Contains(t, BusinessStatsCircuitSkipReason(), "hard disabled")
	})

	t.Run("cooldown window -> open until expiry", func(t *testing.T) {
		bsResetBreaker()
		bsWithCircuitSetting(t, func(s *operation_setting.BusinessStatsCircuitBreakerSetting) {
			s.Enabled = true
			s.ManualDisabled = false
		})
		businessStatsCircuitBreakerLock.Lock()
		businessStatsCircuitBreakerStateValue.disabledUntil = time.Now().Unix() + 100
		businessStatsCircuitBreakerStateValue.lastReason = "boom"
		businessStatsCircuitBreakerLock.Unlock()
		assert.True(t, IsBusinessStatsCircuitOpen())
		assert.Contains(t, BusinessStatsCircuitSkipReason(), "circuit open until")

		// past expiry -> closed
		businessStatsCircuitBreakerLock.Lock()
		businessStatsCircuitBreakerStateValue.disabledUntil = time.Now().Unix() - 1
		businessStatsCircuitBreakerLock.Unlock()
		assert.False(t, IsBusinessStatsCircuitOpen())
	})
}

func TestReportBusinessStatsFailure_ThresholdAndExponentialCooldown(t *testing.T) {
	bsInitFallback(t)
	bsResetBreaker()
	bsWithCircuitSetting(t, func(s *operation_setting.BusinessStatsCircuitBreakerSetting) {
		s.Enabled = true
		s.ManualDisabled = false
		s.FailureThreshold = 3
		s.InitialCooldownSeconds = 10
		s.MaxCooldownSeconds = 25
	})

	// Two failures: below threshold, circuit stays closed.
	ReportBusinessStatsFailure("kind", "f1", nil)
	ReportBusinessStatsFailure("kind", "f2", nil)
	st := GetBusinessStatsCircuitBreakerStatus()
	assert.Equal(t, 2, st.ConsecutiveFailures)
	assert.False(t, st.Open)
	assert.EqualValues(t, 0, st.DisabledUntil)

	// Third failure: threshold reached -> open, cooldown = InitialCooldownSeconds.
	before := time.Now().Unix()
	ReportBusinessStatsFailure("kind", "f3", nil)
	st = GetBusinessStatsCircuitBreakerStatus()
	assert.Equal(t, 3, st.ConsecutiveFailures)
	assert.True(t, st.Open)
	assert.EqualValues(t, 10, st.CooldownSeconds)
	assert.GreaterOrEqual(t, st.DisabledUntil, before+10)

	// Fourth failure: cooldown doubles -> 20.
	ReportBusinessStatsFailure("kind", "f4", nil)
	st = GetBusinessStatsCircuitBreakerStatus()
	assert.EqualValues(t, 20, st.CooldownSeconds)

	// Fifth failure: 40 would exceed Max(25) -> clamped to 25.
	ReportBusinessStatsFailure("kind", "f5", nil)
	st = GetBusinessStatsCircuitBreakerStatus()
	assert.EqualValues(t, 25, st.CooldownSeconds)
}

func TestReportBusinessStatsSuccess_ResetsFailuresNotCooldown(t *testing.T) {
	bsInitFallback(t)
	bsResetBreaker()
	bsWithCircuitSetting(t, func(s *operation_setting.BusinessStatsCircuitBreakerSetting) {
		s.Enabled = true
		s.ManualDisabled = false
		s.FailureThreshold = 2
		s.InitialCooldownSeconds = 10
		s.MaxCooldownSeconds = 100
	})

	ReportBusinessStatsFailure("k", "a", nil)
	ReportBusinessStatsFailure("k", "b", nil) // opens, cooldown=10
	st := GetBusinessStatsCircuitBreakerStatus()
	require.EqualValues(t, 10, st.CooldownSeconds)

	ReportBusinessStatsSuccess()
	st = GetBusinessStatsCircuitBreakerStatus()
	assert.Equal(t, 0, st.ConsecutiveFailures) // failures reset
	assert.EqualValues(t, 10, st.CooldownSeconds) // cooldown NOT reset (penalty persists)

	// Next open cycle doubles from the retained cooldown -> 20.
	ReportBusinessStatsFailure("k", "c", nil)
	ReportBusinessStatsFailure("k", "d", nil)
	st = GetBusinessStatsCircuitBreakerStatus()
	assert.EqualValues(t, 20, st.CooldownSeconds)
}

func TestReportBusinessStatsFailure_DisabledIsNoOp(t *testing.T) {
	bsInitFallback(t)
	bsResetBreaker()
	bsWithCircuitSetting(t, func(s *operation_setting.BusinessStatsCircuitBreakerSetting) {
		s.Enabled = false
	})
	ReportBusinessStatsFailure("k", "x", nil)
	st := GetBusinessStatsCircuitBreakerStatus()
	assert.Equal(t, 0, st.ConsecutiveFailures)
	assert.False(t, st.HardDisabled)
}

func TestReportBusinessStatsFailure_HardDisableWhenFallbackFails(t *testing.T) {
	bsResetBreaker()
	bsWithCircuitSetting(t, func(s *operation_setting.BusinessStatsCircuitBreakerSetting) {
		s.Enabled = true
		s.ManualDisabled = false
		s.FailureThreshold = 3
	})
	// Force fallback writes to fail by nil-ing the queue.
	saved := fallbackQueue
	fallbackQueue = nil
	t.Cleanup(func() { fallbackQueue = saved })

	ReportBusinessStatsFailure("k", "boom", nil)
	st := GetBusinessStatsCircuitBreakerStatus()
	assert.True(t, st.HardDisabled)
	assert.True(t, st.Open)
	assert.True(t, IsBusinessStatsCircuitOpen())

	// Once hard-disabled, further failures are no-ops (early return).
	ReportBusinessStatsFailure("k", "again", nil)
	st = GetBusinessStatsCircuitBreakerStatus()
	assert.True(t, st.HardDisabled)
}

func TestRecordBusinessStatsSkipped_HardDisableWhenFallbackFails(t *testing.T) {
	bsResetBreaker()
	saved := fallbackQueue
	fallbackQueue = nil
	t.Cleanup(func() { fallbackQueue = saved })

	RecordBusinessStatsSkipped("kind", "", nil) // empty reason -> defaulted internally
	st := GetBusinessStatsCircuitBreakerStatus()
	assert.True(t, st.HardDisabled)
}

func TestRecordBusinessStatsSkipped_SucceedsWhenQueueUp(t *testing.T) {
	bsInitFallback(t)
	bsResetBreaker()
	RecordBusinessStatsSkipped("kind", "reason", map[string]any{"a": 1})
	st := GetBusinessStatsCircuitBreakerStatus()
	assert.False(t, st.HardDisabled)
}

func TestBeginBusinessStatsSideEffect(t *testing.T) {
	bsInitFallback(t)

	t.Run("open circuit -> skip, no guard", func(t *testing.T) {
		bsResetBreaker()
		bsWithCircuitSetting(t, func(s *operation_setting.BusinessStatsCircuitBreakerSetting) {
			s.Enabled = true
			s.ManualDisabled = true
		})
		guard, ok := BeginBusinessStatsSideEffect("skip_kind", map[string]any{"x": 1})
		assert.False(t, ok)
		assert.Nil(t, guard)
	})

	t.Run("closed circuit -> guard with timeout context", func(t *testing.T) {
		bsResetBreaker()
		bsWithCircuitSetting(t, func(s *operation_setting.BusinessStatsCircuitBreakerSetting) {
			s.Enabled = true
			s.ManualDisabled = false
		})
		guard, ok := BeginBusinessStatsSideEffect("skip_kind", nil)
		require.True(t, ok)
		require.NotNil(t, guard)
		defer guard.Done()
		_, hasDeadline := guard.Context().Deadline()
		assert.True(t, hasDeadline)
	})
}

func TestSideEffectGuardMethods(t *testing.T) {
	bsInitFallback(t)
	bsResetBreaker()
	bsWithCircuitSetting(t, func(s *operation_setting.BusinessStatsCircuitBreakerSetting) {
		s.Enabled = true
		s.ManualDisabled = false
		s.FailureThreshold = 100 // keep the breaker closed for these assertions
	})

	// nil guard fallbacks
	var nilGuard *BusinessStatsSideEffectGuard
	assert.Equal(t, context.Background(), nilGuard.Context())
	nilGuard.Done() // must not panic

	guard, ok := BeginBusinessStatsSideEffect("k", nil)
	require.True(t, ok)
	defer guard.Done()

	// Fail(nil) is a no-op and returns false.
	assert.False(t, guard.Fail("k", nil, nil))
	assert.Equal(t, 0, GetBusinessStatsCircuitBreakerStatus().ConsecutiveFailures)

	// Fail(err) reports a failure.
	assert.True(t, guard.Fail("k", errors.New("db down"), nil))
	assert.Equal(t, 1, GetBusinessStatsCircuitBreakerStatus().ConsecutiveFailures)

	// FailReason reports too.
	guard.FailReason("k", "explicit reason", nil)
	assert.Equal(t, 2, GetBusinessStatsCircuitBreakerStatus().ConsecutiveFailures)

	// Success resets the counter.
	guard.Success()
	assert.Equal(t, 0, GetBusinessStatsCircuitBreakerStatus().ConsecutiveFailures)
}

func TestBusinessStatsSideEffectContext_HasDeadline(t *testing.T) {
	bsWithCircuitSetting(t, func(s *operation_setting.BusinessStatsCircuitBreakerSetting) {
		s.SideEffectDBTimeoutMs = 500
	})
	ctx, cancel := BusinessStatsSideEffectContext()
	defer cancel()
	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(500*time.Millisecond), deadline, 200*time.Millisecond)
}

func TestGetBusinessStatsCircuitBreakerStatus_Fields(t *testing.T) {
	bsResetBreaker()
	bsWithCircuitSetting(t, func(s *operation_setting.BusinessStatsCircuitBreakerSetting) {
		s.Enabled = true
		s.ManualDisabled = false
	})
	st := GetBusinessStatsCircuitBreakerStatus()
	assert.True(t, st.Enabled)
	assert.False(t, st.ManualDisabled)
	assert.False(t, st.Open)
	assert.False(t, st.HardDisabled)
}

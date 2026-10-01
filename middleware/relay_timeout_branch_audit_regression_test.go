package middleware

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression: the retry time-budget gate must be monotonic. With
// retry_min_budget_seconds=10 it stops a retry when 5s of total budget remain,
// and it must keep stopping once the total timer has FIRED (0s remain) —
// previously the fired controller reported "no deadline", which read as "no
// budget configured" and admitted another attempt on a cancelled context.
//
// Reachable in controller.Relay when the total timer fires after
// normalizeRelayTimeoutError has classified an attempt's retryable error (e.g.
// an upstream 500) but before the next ContinueRelayAttempts.
func TestBranchAuditRegressionBudgetGateAdmitsRetryAfterTotalExpiry(t *testing.T) {
	retryAuditSettings(t, 10, 0)
	c, ctx, control := timeoutTestContext(t, time.Minute, 30*time.Millisecond, false)

	param := &service.RetryParam{Ctx: c, Retry: common.GetPointer(0)}
	require.True(t, service.ContinueRelayAttempts(c, param))
	service.BeginRelayAttempt(c, param)
	param.IncreaseRetry()

	stop, _ := service.RelayRetryBudgetExhausted(c)
	require.True(t, stop, "30ms left is below the 10s minimum: no retry")

	waitForTimeout(t, ctx)
	require.Equal(t, relayTimeoutKindTotal, control.ExpiredKind())

	stop, _ = service.RelayRetryBudgetExhausted(c)
	assert.True(t, stop, "a fully spent budget must still count as exhausted")
	assert.False(t, service.ContinueRelayAttempts(c, param),
		"no attempt may start after the request-wide deadline has passed")
}

// With the minimum-budget gate switched off (0) a retry is admitted while the
// total budget lasts, but never after the total deadline has passed.
func TestBranchAuditBudgetGateWithoutMinimumStopsOnlyAfterTotalExpiry(t *testing.T) {
	retryAuditSettings(t, 0, 0)
	c, ctx, control := timeoutTestContext(t, time.Minute, 30*time.Millisecond, false)

	param := &service.RetryParam{Ctx: c, Retry: common.GetPointer(0)}
	require.True(t, service.ContinueRelayAttempts(c, param))
	service.BeginRelayAttempt(c, param)
	param.IncreaseRetry()

	stop, _ := service.RelayRetryBudgetExhausted(c)
	assert.False(t, stop, "no minimum configured: any remaining budget allows a retry")
	assert.True(t, service.ContinueRelayAttempts(c, param))

	waitForTimeout(t, ctx)
	require.Equal(t, relayTimeoutKindTotal, control.ExpiredKind())
	stop, reason := service.RelayRetryBudgetExhausted(c)
	assert.True(t, stop)
	assert.Contains(t, reason, "deadline has passed")
	assert.False(t, service.ContinueRelayAttempts(c, param))
}

// A closed controller (request finished) reports no deadline at all.
func TestBranchAuditTotalDeadlineAbsentAfterClose(t *testing.T) {
	retryAuditSettings(t, 10, 0)
	c, _, control := timeoutTestContext(t, time.Minute, time.Minute, false)
	_, ok := service.RelayTotalDeadline(c)
	require.True(t, ok)
	control.Close()
	_, ok = service.RelayTotalDeadline(c)
	assert.False(t, ok)
}

// The gate follows the settings snapshot the request started with, and a
// passed deadline stops retries even if the feature is switched off mid-request.
func TestBranchAuditBudgetGateUsesRequestSnapshotAfterGlobalDisable(t *testing.T) {
	retryAuditSettings(t, 0, 0)
	c, ctx, _ := timeoutTestContext(t, time.Minute, 30*time.Millisecond, false)
	snapshot := operation_setting.GetRelayTimeoutSetting()
	snapshot.Enabled = true
	snapshot.RetryMinBudgetSeconds = 10
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutSetting, &snapshot)

	disabled := operation_setting.GetRelayTimeoutSetting()
	disabled.Enabled = false
	operation_setting.ReplaceRelayTimeoutSetting(disabled)

	stop, _ := service.RelayRetryBudgetExhausted(c)
	assert.True(t, stop, "the request's own 10s minimum applies although the live setting is off")

	waitForTimeout(t, ctx)
	stop, reason := service.RelayRetryBudgetExhausted(c)
	assert.True(t, stop)
	assert.Contains(t, reason, "deadline has passed")
}

package middleware

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// retryAuditSettings installs the retry gates for one test and restores
// the previous hot setting afterwards.
func retryAuditSettings(t *testing.T, minBudgetSeconds, maxTotalAttempts int) {
	t.Helper()
	previous := operation_setting.GetRelayTimeoutSetting()
	next := previous
	next.Enabled = true
	next.RetryMinBudgetSeconds = minBudgetSeconds
	next.MaxTotalAttempts = maxTotalAttempts
	operation_setting.ReplaceRelayTimeoutSetting(next)
	t.Cleanup(func() { operation_setting.ReplaceRelayTimeoutSetting(previous) })

	previousRetry := common.RetryTimes
	common.RetryTimes = 5
	t.Cleanup(func() { common.RetryTimes = previousRetry })
	previousGroups := operation_setting.GroupRetryTimes2JSONString()
	require.NoError(t, operation_setting.UpdateGroupRetryTimesByJSONString(`{}`))
	t.Cleanup(func() { _ = operation_setting.UpdateGroupRetryTimesByJSONString(previousGroups) })
}

func retryAuditResponsePending(control *relayTimeoutControl) bool {
	return control.RelayResponsePending()
}

// The budget gate against the real controller, not a stub: an ample total
// budget lets a retry start, a short one stops it, and the first attempt is
// admitted either way.
func TestBranchAuditBudgetGateWithRealController(t *testing.T) {
	retryAuditSettings(t, 10, 0)
	for _, tc := range []struct {
		name  string
		total time.Duration
		cont  bool
	}{
		{"ample total budget", time.Minute, true},
		{"short total budget", 5 * time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := timeoutTestContext(t, time.Minute, tc.total, false)
			param := &service.RetryParam{Ctx: c, Retry: common.GetPointer(0)}
			require.True(t, service.ContinueRelayAttempts(c, param))
			service.BeginRelayAttempt(c, param)
			param.IncreaseRetry()
			assert.Equal(t, tc.cont, service.ContinueRelayAttempts(c, param))
		})
	}
}

// A response window alone (no total timeout) is never a budget, also through
// the real controller whose NextDeadline does report the response window.
func TestBranchAuditResponseOnlyControllerIsNotABudget(t *testing.T) {
	retryAuditSettings(t, 10, 0)
	c, _, control := timeoutTestContext(t, time.Second, 0, false)
	_, hasNext := control.NextDeadline()
	require.True(t, hasNext)
	_, hasTotal := control.RelayTotalDeadline()
	require.False(t, hasTotal)

	param := &service.RetryParam{Ctx: c, Retry: common.GetPointer(0)}
	service.BeginRelayAttempt(c, param)
	param.IncreaseRetry()
	assert.True(t, service.ContinueRelayAttempts(c, param))
}

// BeginRelayAttempt must restart the response window for every attempt after
// the first, including the first attempt on a new auto group — where the
// per-group counter was just reset to 0. Keying the restart on GetRetry()>0
// (the code this replaced) left that attempt on the previous attempt's
// already-stopped timer, i.e. with no response bound at all.
func TestBranchAuditBeginRelayAttemptRestartsWindowAfterGroupSwitch(t *testing.T) {
	retryAuditSettings(t, 0, 0)
	c, _, control := timeoutTestContext(t, time.Minute, 0, false)
	param := &service.RetryParam{Ctx: c, Retry: common.GetPointer(0)}

	require.True(t, service.ContinueRelayAttempts(c, param))
	service.BeginRelayAttempt(c, param)
	require.True(t, retryAuditResponsePending(control), "attempt one runs on the initial window")
	require.True(t, service.MarkRelayResponse(c), "attempt one's first byte stops the window")
	require.False(t, retryAuditResponsePending(control))

	// Routing prepared a group switch: counter reset, next advance skipped.
	param.SetRetry(0)
	param.ResetRetryNextTry()
	param.IncreaseRetry()
	require.Equal(t, 0, param.GetRetry())

	require.True(t, service.ContinueRelayAttempts(c, param))
	service.BeginRelayAttempt(c, param)
	assert.True(t, retryAuditResponsePending(control),
		"the new group's first attempt must get a fresh response window")
	assert.Equal(t, 2, param.TotalAttempts())
}

// The first attempt must not restart the window: it was armed when the request
// started and restarting would silently extend it by the time spent on request
// parsing, pricing and pre-consume.
func TestBranchAuditFirstAttemptDoesNotRestartWindow(t *testing.T) {
	retryAuditSettings(t, 0, 0)
	c, _, control := timeoutTestContext(t, time.Minute, 0, false)
	control.mu.Lock()
	before := control.responseDeadline
	control.mu.Unlock()

	time.Sleep(5 * time.Millisecond)
	param := &service.RetryParam{Ctx: c, Retry: common.GetPointer(0)}
	service.BeginRelayAttempt(c, param)

	control.mu.Lock()
	after := control.responseDeadline
	control.mu.Unlock()
	assert.Equal(t, before, after)
}

// Stream requests keep one first-output window across attempts (design
// per-user-relay-timeout §3.1: the stream response timer runs from relay start
// until the first output written to the client). A retry must not re-arm it.
func TestBranchAuditStreamRetryKeepsFirstOutputWindow(t *testing.T) {
	retryAuditSettings(t, 0, 0)
	c, _, control := timeoutTestContext(t, time.Minute, 0, true)
	control.mu.Lock()
	before := control.responseDeadline
	control.mu.Unlock()

	time.Sleep(5 * time.Millisecond)
	param := &service.RetryParam{Ctx: c, Retry: common.GetPointer(0)}
	service.BeginRelayAttempt(c, param)
	param.IncreaseRetry()
	service.BeginRelayAttempt(c, param)

	control.mu.Lock()
	after := control.responseDeadline
	control.mu.Unlock()
	assert.Equal(t, before, after)
	assert.True(t, retryAuditResponsePending(control))
}

// Close owns cleanup: both timers are stopped, the request context is
// cancelled with a plain Canceled cause (not a timeout), a second Close is a
// no-op, and a timer callback racing Close cannot relabel the request.
func TestBranchAuditCloseStopsTimersAndIsIdempotent(t *testing.T) {
	_, ctx, control := timeoutTestContext(t, time.Minute, time.Minute, false)
	control.Close()
	control.Close()

	select {
	case <-ctx.Done():
	default:
		require.Fail(t, "Close must cancel the request context")
	}
	assert.ErrorIs(t, context.Cause(ctx), context.Canceled)
	assert.False(t, control.responseTimer.Stop(), "response timer must already be stopped")
	assert.False(t, control.totalTimer.Stop(), "total timer must already be stopped")

	control.expireResponse()
	control.expireTotal()
	assert.Equal(t, relayTimeoutKindNone, control.ExpiredKind())
	assert.False(t, control.RestartResponse(false))
	assert.False(t, control.MarkResponse(false))
	_, ok := control.RelayTotalDeadline()
	assert.False(t, ok)
}

// A nil controller (never started) is safe on every entry point the retry
// loop and relay layer reach.
func TestBranchAuditNilControllerIsInert(t *testing.T) {
	var control *relayTimeoutControl
	assert.NotPanics(t, func() {
		control.Close()
		control.HoldRelayResponse()
		assert.False(t, control.ReleaseRelayResponse())
		assert.False(t, control.MarkResponse(false))
		assert.False(t, control.RestartResponse(false))
		assert.False(t, control.RelayResponsePending())
		assert.Equal(t, relayTimeoutKindNone, control.ExpiredKind())
		_, ok := control.RelayTotalDeadline()
		assert.False(t, ok)
		_, ok = control.NextDeadline()
		assert.False(t, ok)
	})
}

// Every public operation of the controller may run on a different goroutine
// (transport trace callback, writer, relay goroutine, timer callbacks). They
// must neither deadlock nor panic when interleaved with expiry and Close.
// Meaningful for data races only under -race.
func TestBranchAuditConcurrentControlOperations(t *testing.T) {
	for round := 0; round < 20; round++ {
		c, ctx, control := timeoutTestContext(t, 2*time.Millisecond, 6*time.Millisecond, false)
		var wg sync.WaitGroup
		for w := 0; w < 6; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for i := 0; i < 50; i++ {
					switch (w + i) % 6 {
					case 0:
						service.MarkRelayResponse(c)
					case 1:
						service.RestartRelayResponseTimeout(c)
					case 2:
						service.HoldRelayResponseTimer(c)
					case 3:
						service.ReleaseRelayResponseTimer(c)
					case 4:
						_, _ = service.RelayTotalDeadline(c)
						_, _ = service.RelayRequestDeadline(c)
					case 5:
						_ = control.RelayResponsePending()
						_ = service.RelayRequestTimeoutKind(c)
					}
				}
			}(w)
		}
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			require.Fail(t, "controller operations deadlocked")
		}
		waitForTimeout(t, ctx)
		control.Close()
		assert.NotEqual(t, relayTimeoutKindNone, control.ExpiredKind(), "the total timer always ends the request")
	}
}

// ContextKeyRelayTimeoutControl holds the controller for the whole request;
// the service budget/restart helpers must accept exactly that value.
func TestBranchAuditServiceSeesControllerInterfaces(t *testing.T) {
	c, _, control := timeoutTestContext(t, time.Minute, time.Minute, false)
	value, ok := common.GetContextKey(c, constant.ContextKeyRelayTimeoutControl)
	require.True(t, ok)
	require.Same(t, control, value)

	_, ok = value.(service.RelayResponseHolder)
	assert.True(t, ok)
	_, ok = value.(service.RelayResponseReleaser)
	assert.True(t, ok)
	_, ok = value.(service.RelayTimeoutObserver)
	assert.True(t, ok)
	_, hasTotal := service.RelayTotalDeadline(c)
	assert.True(t, hasTotal, "RelayTotalDeadline must reach the controller's total deadline")
}

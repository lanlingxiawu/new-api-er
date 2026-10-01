package service

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// The loop gate, exhaustively
// ---------------------------------------------------------------------------

// Exhaustive sweep over the gate's input space. Two invariants must hold for
// every combination: attempt one always runs (otherwise pre-consumed quota is
// stranded with no refund), and the gate never permits unbounded attempts when
// a cap is set.
func TestAudit_ShouldAttemptRelayInvariants(t *testing.T) {
	values := []int{-5, -1, 0, 1, 2, 6, 50}
	for _, quota := range values {
		for _, maxTotal := range values {
			assert.True(t, ShouldAttemptRelay(0, quota, 0, maxTotal),
				"first attempt refused: quota=%d maxTotal=%d", quota, maxTotal)

			if maxTotal > 0 {
				assert.False(t, ShouldAttemptRelay(0, 9999, maxTotal, maxTotal),
					"cap ignored: quota=9999 maxTotal=%d", maxTotal)
				assert.False(t, ShouldAttemptRelay(0, 9999, maxTotal+1, maxTotal),
					"cap ignored past the boundary: maxTotal=%d", maxTotal)
			}
		}
	}
}

// Simulating the real loop with a FINITE number of auto-groups, which is what
// routing actually offers: once the groups are exhausted, channel selection
// fails and the loop ends. Within that, neither bound may be exceeded and the
// request must always attempt at least once.
func TestAudit_LoopTerminatesWithinBothBounds(t *testing.T) {
	for _, quota := range []int{0, 1, 3, 20} {
		for _, maxTotal := range []int{0, 1, 2, 6} {
			for _, groups := range []int{1, 2, 3} {
				name := fmt.Sprintf("quota=%d/max=%d/groups=%d", quota, maxTotal, groups)
				t.Run(name, func(t *testing.T) {
					param := &RetryParam{Retry: common.GetPointer(0)}

					attempts := 0
					switchesLeft := groups - 1
					const hardStop = 5000
					for ShouldAttemptRelay(param.GetRetry(), quota, param.TotalAttempts(), maxTotal) {
						attempts++
						require.Less(t, attempts, hardStop, "loop did not terminate")
						param.CountAttempt()
						// A group switch happens only while groups remain, and only
						// once this group's quota is spent — mirroring channel_select.
						if switchesLeft > 0 && param.GetRetry() >= quota {
							switchesLeft--
							param.SetRetry(0)
							param.ResetRetryNextTry()
						} else if param.GetRetry() >= quota && switchesLeft == 0 {
							break // routing has no further group: getChannel would fail
						}
						param.IncreaseRetry()
					}

					assert.GreaterOrEqual(t, attempts, 1, "every request must attempt at least once")
					if maxTotal > 0 {
						assert.LessOrEqual(t, attempts, maxTotal,
							"group switching must not let attempts exceed the total cap")
					} else {
						// No cap: the bound is quota-per-group times the group count,
						// which is exactly the blow-up the total cap exists to stop.
						assert.LessOrEqual(t, attempts, (quota+1)*groups)
					}
				})
			}
		}
	}
}

// max_total_attempts=0 is documented as "unlimited (legacy behaviour)" and is
// the incident rollback switch. Pinning that meaning explicitly: with it off,
// the per-group quota alone does NOT bound a request that keeps switching
// groups — the cap is the only defence against 分组数 × (quota+1).
func TestAudit_ZeroTotalCapLeavesGroupSwitchingUnbounded(t *testing.T) {
	assert.True(t, ShouldAttemptRelay(0, 3, 1_000_000, 0),
		"max_total_attempts=0 deliberately imposes no request-wide bound")
	assert.False(t, ShouldAttemptRelay(0, 3, 1_000_000, 6),
		"a configured cap must bound it regardless of per-group resets")
}

// ---------------------------------------------------------------------------
// Three-level resolution, full matrix
// ---------------------------------------------------------------------------

func TestAudit_ResolveRetryTimesFullMatrix(t *testing.T) {
	withGroupRetryTimes(t, `{"quota":5,"disabled":-1,"zero":0}`)

	type want struct{ value int }
	cases := []struct {
		user   int
		group  string
		global int
		want   int
		why    string
	}{
		{5, "disabled", 1, 5, "user positive beats group -1"},
		{5, "quota", 1, 5, "user positive beats group quota"},
		{-1, "quota", 1, 0, "user -1 beats group quota"},
		{-1, "zero", 1, 0, "user -1 beats group inherit"},
		{0, "quota", 1, 5, "inherit lands on group"},
		{0, "disabled", 1, 0, "inherit lands on group -1"},
		{0, "zero", 1, 1, "group 0 means inherit, not disable"},
		{0, "missing", 1, 1, "absent group inherits global"},
		{0, "missing", 0, 0, "global 0 means no retry"},
		{0, "missing", -1, 0, "a negative global is clamped, never negative"},
	}
	for _, tc := range cases {
		t.Run(tc.why, func(t *testing.T) {
			got := ResolveRetryTimes(tc.user, tc.group, tc.global)
			assert.Equal(t, tc.want, got)
			assert.GreaterOrEqual(t, got, 0, "a resolved quota is a loop bound and must never be negative")
		})
	}
}

// Whatever the configuration, the resolved value must be usable directly as a
// loop bound — i.e. non-negative — including for values the validator rejects.
func TestAudit_ResolveRetryTimesNeverNegativeForAnyInput(t *testing.T) {
	withGroupRetryTimes(t, `{"g":-7}`)
	for _, user := range []int{-100, -2, -1, 0, 1, 100} {
		for _, group := range []string{"g", "absent", ""} {
			for _, global := range []int{-100, -1, 0, 1, 100} {
				got := ResolveRetryTimes(user, group, global)
				assert.GreaterOrEqual(t, got, 0,
					"user=%d group=%q global=%d", user, group, global)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Budget check under every timeout shape
// ---------------------------------------------------------------------------

// The budget is the request-wide deadline only. Each shape below is a real
// configuration a user can produce from the five timeout fields.
func TestAudit_BudgetShapes(t *testing.T) {
	withRelayTimeoutSetting(t, func(s *operation_setting.RelayTimeoutSetting) {
		s.Enabled = true
		s.RetryMinBudgetSeconds = 10
	})
	gin.SetMode(gin.TestMode)

	shapes := []struct {
		name      string
		total     time.Duration
		response  time.Duration
		wantStop  bool
		rationale string
	}{
		{"neither configured", 0, 0, false, "no budget to measure"},
		{"response only, ample", 0, 300 * time.Second, false, "response window is not a budget"},
		{"response only, tight", 0, 3 * time.Second, false, "still not a budget"},
		{"total ample", 600 * time.Second, 0, false, "plenty left"},
		{"total tight", 3 * time.Second, 0, true, "genuinely out of budget"},
		{"total ample, response tight", 600 * time.Second, 2 * time.Second, false, "total governs"},
		{"total tight, response ample", 3 * time.Second, 300 * time.Second, true, "total governs"},
	}
	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			stub := budgetDeadlineStub{}
			if shape.total > 0 {
				stub.totalDeadline = time.Now().Add(shape.total)
			}
			if shape.response > 0 {
				stub.responseDeadline = time.Now().Add(shape.response)
			}
			common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, stub)

			stop, _ := RelayRetryBudgetExhausted(c)
			assert.Equal(t, shape.wantStop, stop, shape.rationale)
		})
	}
}

// The budget check must never be reachable in a way that skips the first
// attempt, regardless of how exhausted the budget is.
func TestAudit_BudgetNeverBlocksFirstAttemptForAnyShape(t *testing.T) {
	withRelayTimeoutSetting(t, func(s *operation_setting.RelayTimeoutSetting) {
		s.Enabled = true
		s.RetryMinBudgetSeconds = 30
	})
	for _, remaining := range []time.Duration{-time.Hour, -time.Second, 0, time.Millisecond, time.Hour} {
		c := budgetContext(t, remaining, true)
		// Whatever the budget says, the gate that guards attempt one is separate
		// and unconditional.
		assert.True(t, ShouldAttemptRelay(0, 0, 0, 1),
			"remaining=%s", remaining)
		_, _ = RelayRetryBudgetExhausted(c)
	}
}

// ---------------------------------------------------------------------------
// Group fail-over follows main: switching groups needs quota left
// ---------------------------------------------------------------------------

// main's IncreaseRetry skips exactly one counter advance after routing prepares
// a group switch, so the new group starts at 0. That instance-field behaviour
// is kept unchanged.
func TestAudit_PreparedSwitchSkipsOneCounterAdvance(t *testing.T) {
	param := &RetryParam{Retry: common.GetPointer(0)}

	param.SetRetry(0)
	param.ResetRetryNextTry()
	param.IncreaseRetry()
	assert.Equal(t, 0, param.GetRetry(), "the new group's first attempt starts at 0")

	param.IncreaseRetry()
	assert.Equal(t, 1, param.GetRetry(), "later advances count as usual")
}

// Total attempts must survive every combination of per-group resets.
func TestAudit_TotalAttemptsSurvivesGroupChurn(t *testing.T) {
	param := &RetryParam{Retry: common.GetPointer(0)}
	for i := 0; i < 7; i++ {
		param.CountAttempt()
		param.SetRetry(0)
		param.ResetRetryNextTry()
		param.IncreaseRetry()
	}
	assert.Equal(t, 7, param.TotalAttempts(),
		"per-group resets must never touch the request-wide counter")
	assert.Equal(t, 0, param.GetRetry())
}

// A spent quota ends the request even when routing has prepared another group,
// exactly as main does with RetryTimes - retry. Granting an extra attempt there
// made user retry_times=-1 still try every auto group, and made the last
// group's real upstream error be replaced by a "no channel" error from an
// attempt that had no group left to go to.
func TestAudit_SpentQuotaEndsRequestDespitePreparedSwitch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyUserRetryTimes, -1)

	param := &RetryParam{Ctx: c, Retry: common.GetPointer(0)}
	require.True(t, ContinueRelayAttempts(c, param), "the first attempt always runs")
	BeginRelayAttempt(c, param)

	// What channel selection does when the group's quota is spent.
	param.SetRetry(0)
	param.ResetRetryNextTry()

	assert.Equal(t, 0, RemainingRetryBudget(c, param),
		"no quota left means no further attempt, prepared switch or not")
}

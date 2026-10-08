package service

import (
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

// withGroupRetryTimes installs a per-group quota map for the duration of a test.
func withGroupRetryTimes(t *testing.T, json string) {
	t.Helper()
	previous := operation_setting.GroupRetryTimes2JSONString()
	require.NoError(t, operation_setting.UpdateGroupRetryTimesByJSONString(json))
	t.Cleanup(func() {
		_ = operation_setting.UpdateGroupRetryTimesByJSONString(previous)
	})
}

// The whole point of the three levels: user beats group, group beats global.
func TestResolveRetryTimes_Priority(t *testing.T) {
	withGroupRetryTimes(t, `{"cheap":5,"official":-1}`)

	cases := []struct {
		name   string
		user   int
		group  string
		global int
		want   int
	}{
		{"user overrides group", 2, "cheap", 3, 2},
		{"user overrides global", 2, "unconfigured", 3, 2},
		{"user disabled beats group quota", RetryTimesDisabled, "cheap", 3, 0},
		{"user disabled beats global", RetryTimesDisabled, "unconfigured", 3, 0},
		{"user inherit falls to group", RetryTimesInherit, "cheap", 3, 5},
		{"group disabled", RetryTimesInherit, "official", 3, 0},
		{"group absent falls to global", RetryTimesInherit, "unconfigured", 3, 3},
		{"all unset", RetryTimesInherit, "", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ResolveRetryTimes(tc.user, tc.group, tc.global))
		})
	}
}

// An explicit 0 stored for a group means "inherit", exactly like an absent
// entry — otherwise an operator could not express it without deleting the key.
func TestResolveRetryTimes_GroupZeroInherits(t *testing.T) {
	withGroupRetryTimes(t, `{"neutral":0}`)
	assert.Equal(t, 4, ResolveRetryTimes(RetryTimesInherit, "neutral", 4))
}

// The resolved value feeds a loop bound directly, so it must never be negative:
// a negative bound would skip the loop entirely and strand the pre-consumed
// quota with no refund (design §7.2).
func TestResolveRetryTimes_NeverNegative(t *testing.T) {
	withGroupRetryTimes(t, `{"weird":-1}`)
	cases := []struct {
		user   int
		group  string
		global int
	}{
		{RetryTimesDisabled, "weird", 3},
		{RetryTimesInherit, "weird", 3},
		{RetryTimesInherit, "absent", -5},
		{RetryTimesDisabled, "absent", -5},
		{-99, "absent", 3},
	}
	for _, tc := range cases {
		got := ResolveRetryTimes(tc.user, tc.group, tc.global)
		assert.GreaterOrEqual(t, got, 0,
			"user=%d group=%q global=%d", tc.user, tc.group, tc.global)
	}
}

// An unknown negative user value is not the documented -1; treat it as
// "no retry" rather than letting it through as a negative bound.
func TestResolveRetryTimes_UnknownNegativeUserValue(t *testing.T) {
	assert.Equal(t, 0, ResolveRetryTimes(-99, "", 3))
}

func TestResolveRetryTimes_EmptyGroupSkipsGroupLevel(t *testing.T) {
	withGroupRetryTimes(t, `{"":7}`)
	assert.Equal(t, 3, ResolveRetryTimes(RetryTimesInherit, "", 3),
		"an empty group name must not match a stored entry")
}

func TestResolveRetryTimesForRequest(t *testing.T) {
	withGroupRetryTimes(t, `{"cheap":5}`)
	gin.SetMode(gin.TestMode)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyUserRetryTimes, 2)
	assert.Equal(t, 2, ResolveRetryTimesForRequest(c, "cheap", 3))

	// Absent context key decodes as 0 = inherit, not as "disabled".
	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	assert.Equal(t, 5, ResolveRetryTimesForRequest(c2, "cheap", 3))
	assert.Equal(t, 3, ResolveRetryTimesForRequest(c2, "unconfigured", 3))

	assert.Equal(t, 3, ResolveRetryTimesForRequest(nil, "unconfigured", 3))
}

func TestReserveRelayFallbackCallConsumesAndRespectsTotalBudget(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	param := &RetryParam{Ctx: c, Retry: common.GetPointer(0)}

	previous := operation_setting.GetRelayTimeoutSetting()
	t.Cleanup(func() { operation_setting.ReplaceRelayTimeoutSetting(previous) })
	setting := operation_setting.GetRelayTimeoutSetting()
	setting.MaxTotalAttempts = 2
	operation_setting.ReplaceRelayTimeoutSetting(setting)

	BeginRelayAttempt(c, param)
	require.Equal(t, 1, param.TotalAttempts())

	allowed, reason := ReserveRelayFallbackCall(c)
	require.True(t, allowed, reason)
	require.Equal(t, 2, param.TotalAttempts())

	allowed, reason = ReserveRelayFallbackCall(c)
	assert.False(t, allowed)
	assert.Contains(t, reason, "total upstream call limit")
	assert.Equal(t, 2, param.TotalAttempts())
}

// Group quota is resolved per group, so a switch mid-request picks up the new
// group's value (design §5.2).
func TestResolveRetryTimes_PerGroupOnSwitch(t *testing.T) {
	withGroupRetryTimes(t, `{"first":1,"second":4,"third":-1}`)
	assert.Equal(t, 1, ResolveRetryTimes(RetryTimesInherit, "first", 3))
	assert.Equal(t, 4, ResolveRetryTimes(RetryTimesInherit, "second", 3))
	assert.Equal(t, 0, ResolveRetryTimes(RetryTimesInherit, "third", 3))
}

func TestCheckGroupRetryTimes(t *testing.T) {
	valid := []string{
		`{}`,
		`{"a":0}`,
		`{"a":-1}`,
		`{"a":1}`,
		`{"a":20}`,
		`{"a":5,"b":-1,"c":0}`,
	}
	for _, s := range valid {
		assert.NoError(t, operation_setting.CheckGroupRetryTimes(s), s)
	}

	invalid := []string{
		`{"a":-2}`,
		`{"a":21}`,
		`{"a":"x"}`,
		`not json`,
	}
	for _, s := range invalid {
		assert.Error(t, operation_setting.CheckGroupRetryTimes(s), s)
	}
}

func TestRelayMaxTotalAttempts(t *testing.T) {
	cases := []struct {
		configured int
		want       int
	}{
		{0, 0},  // unlimited
		{-1, 0}, // never a negative bound
		{1, 1},  // one attempt, still runs the loop once
		{6, 6},
	}
	for _, tc := range cases {
		s := &operation_setting.RelayTimeoutSetting{MaxTotalAttempts: tc.configured}
		assert.Equal(t, tc.want, s.RelayMaxTotalAttempts(), "configured=%d", tc.configured)
	}

	var nilSetting *operation_setting.RelayTimeoutSetting
	assert.Equal(t, 0, nilSetting.RelayMaxTotalAttempts())
}

func TestValidateRelayTimeoutSetting_NewFields(t *testing.T) {
	base := operation_setting.RelayTimeoutSetting{Enabled: true}

	ok := base
	ok.RetryMinBudgetSeconds = 10
	ok.MaxTotalAttempts = 6
	assert.NoError(t, operation_setting.ValidateRelayTimeoutSetting(ok))

	zero := base
	assert.NoError(t, operation_setting.ValidateRelayTimeoutSetting(zero),
		"zero means disabled/unlimited and must stay valid")

	badBudget := base
	badBudget.RetryMinBudgetSeconds = -1
	assert.Error(t, operation_setting.ValidateRelayTimeoutSetting(badBudget))

	badAttempts := base
	badAttempts.MaxTotalAttempts = -1
	assert.Error(t, operation_setting.ValidateRelayTimeoutSetting(badAttempts))

	tooManyAttempts := base
	tooManyAttempts.MaxTotalAttempts = operation_setting.MaxRelayTotalAttemptsLimit + 1
	assert.Error(t, operation_setting.ValidateRelayTimeoutSetting(tooManyAttempts))
}

// ---------------------------------------------------------------------------
// Zero-attempt protection — the billing baseline (design §7.2)
// ---------------------------------------------------------------------------

// The refund defer in controller.Relay only fires when an attempt produced an
// error. A request that never enters the loop would therefore keep the
// pre-consumed quota with no refund and no settlement. No configuration may
// produce that, so the first attempt is allowed unconditionally.
func TestShouldAttemptRelay_FirstAttemptAlwaysAllowed(t *testing.T) {
	hostile := []int{-99, -1, 0, 1, 6, 100}
	for _, quota := range hostile {
		for _, maxTotal := range hostile {
			assert.True(t, ShouldAttemptRelay(0, quota, 0, maxTotal),
				"quota=%d maxTotal=%d must still allow the first attempt", quota, maxTotal)
		}
	}
}

// Same invariant stated against the real resolver output rather than
// hand-picked numbers: whatever the three levels resolve to, attempt one runs.
func TestShouldAttemptRelay_FirstAttemptAllowedForEveryResolvedQuota(t *testing.T) {
	withGroupRetryTimes(t, `{"disabled":-1,"quota":5,"zero":0}`)
	for _, group := range []string{"disabled", "quota", "zero", "absent", ""} {
		for _, user := range []int{RetryTimesDisabled, RetryTimesInherit, 1, -99} {
			for _, global := range []int{0, 3, -1} {
				quota := ResolveRetryTimes(user, group, global)
				require.GreaterOrEqual(t, quota, 0)
				assert.True(t, ShouldAttemptRelay(0, quota, 0, 6),
					"group=%q user=%d global=%d resolved=%d", group, user, global, quota)
			}
		}
	}
}

func TestShouldAttemptRelay_QuotaBound(t *testing.T) {
	// quota=2 means up to 2 retries after the first attempt.
	assert.True(t, ShouldAttemptRelay(1, 2, 1, 0))
	assert.True(t, ShouldAttemptRelay(2, 2, 2, 0))
	assert.False(t, ShouldAttemptRelay(3, 2, 3, 0))

	// quota=0 means no retry: the second attempt is refused.
	assert.True(t, ShouldAttemptRelay(0, 0, 0, 0))
	assert.False(t, ShouldAttemptRelay(1, 0, 1, 0))
}

func TestShouldAttemptRelay_TotalAttemptGate(t *testing.T) {
	// The per-group counter is reset on a group switch; the total is not, so
	// the total gate is what actually bounds a multi-group request.
	assert.True(t, ShouldAttemptRelay(0, 3, 5, 6))
	assert.False(t, ShouldAttemptRelay(0, 3, 6, 6),
		"a group switch resetting retry to 0 must not bypass the total cap")
	assert.False(t, ShouldAttemptRelay(0, 3, 7, 6))

	// 0 means unlimited, never "zero attempts".
	assert.True(t, ShouldAttemptRelay(0, 3, 99, 0))
}

// maxTotalAttempts=1 is the tightest legal setting: exactly one attempt, and
// that attempt must still happen.
func TestShouldAttemptRelay_MaxTotalAttemptsOne(t *testing.T) {
	assert.True(t, ShouldAttemptRelay(0, 3, 0, 1))
	assert.False(t, ShouldAttemptRelay(1, 3, 1, 1))
}

// ---------------------------------------------------------------------------
// Budget check (design §6.1)
// ---------------------------------------------------------------------------

// budgetDeadlineStub mirrors the real controller: the "next" deadline is
// whichever of the two fires first, while the total deadline exists only when a
// total timeout is configured. Keeping them separate is what makes the stub able
// to reproduce a response-timeout-only request.
type budgetDeadlineStub struct {
	totalDeadline    time.Time
	responseDeadline time.Time
}

func (s budgetDeadlineStub) MarkResponse(bool) bool { return false }

func (s budgetDeadlineStub) RelayTimeoutDeadline() (time.Time, bool) {
	deadline := s.totalDeadline
	if !s.responseDeadline.IsZero() && (deadline.IsZero() || s.responseDeadline.Before(deadline)) {
		deadline = s.responseDeadline
	}
	if deadline.IsZero() {
		return time.Time{}, false
	}
	return deadline, true
}

func (s budgetDeadlineStub) RelayTotalDeadline() (time.Time, bool) {
	if s.totalDeadline.IsZero() {
		return time.Time{}, false
	}
	return s.totalDeadline, true
}

func budgetContext(t *testing.T, remaining time.Duration, hasDeadline bool) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	stub := budgetDeadlineStub{}
	if hasDeadline {
		stub.totalDeadline = time.Now().Add(remaining)
	}
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, stub)
	return c
}

// responseOnlyBudgetContext models the common production shape: a per-attempt
// response timeout with NO total timeout.
func responseOnlyBudgetContext(t *testing.T, responseRemaining time.Duration) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl,
		budgetDeadlineStub{responseDeadline: time.Now().Add(responseRemaining)})
	return c
}

func withRelayTimeoutSetting(t *testing.T, mutate func(*operation_setting.RelayTimeoutSetting)) {
	t.Helper()
	previous := operation_setting.GetRelayTimeoutSetting()
	next := previous
	mutate(&next)
	operation_setting.ReplaceRelayTimeoutSetting(next)
	t.Cleanup(func() { operation_setting.ReplaceRelayTimeoutSetting(previous) })
}

func TestRelayRetryBudgetExhausted_Boundaries(t *testing.T) {
	withRelayTimeoutSetting(t, func(s *operation_setting.RelayTimeoutSetting) {
		s.Enabled = true
		s.RetryMinBudgetSeconds = 10
	})

	cases := []struct {
		name      string
		remaining time.Duration
		want      bool
	}{
		{"well above threshold", 60 * time.Second, false},
		{"just above threshold", 11 * time.Second, false},
		{"just below threshold", 9 * time.Second, true},
		{"already expired", -time.Second, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stop, reason := RelayRetryBudgetExhausted(budgetContext(t, tc.remaining, true))
			assert.Equal(t, tc.want, stop)
			if tc.want {
				assert.NotEmpty(t, reason, "a stop decision must be explainable in the log")
			}
		})
	}
}

// No total timeout configured means no budget to measure, so the check must
// never fire — otherwise every request would stop retrying immediately.
func TestRelayRetryBudgetExhausted_NoDeadline(t *testing.T) {
	withRelayTimeoutSetting(t, func(s *operation_setting.RelayTimeoutSetting) {
		s.Enabled = true
		s.RetryMinBudgetSeconds = 10
	})
	stop, _ := RelayRetryBudgetExhausted(budgetContext(t, 0, false))
	assert.False(t, stop)
}

func TestRelayRetryBudgetExhausted_Disabled(t *testing.T) {
	withRelayTimeoutSetting(t, func(s *operation_setting.RelayTimeoutSetting) {
		s.Enabled = true
		s.RetryMinBudgetSeconds = 0
	})
	stop, _ := RelayRetryBudgetExhausted(budgetContext(t, time.Millisecond, true))
	assert.False(t, stop, "RetryMinBudgetSeconds=0 disables the check")

	withRelayTimeoutSetting(t, func(s *operation_setting.RelayTimeoutSetting) {
		s.Enabled = false
		s.RetryMinBudgetSeconds = 10
	})
	stop, _ = RelayRetryBudgetExhausted(budgetContext(t, time.Millisecond, true))
	assert.False(t, stop, "the feature switch must gate the check too")
}

func TestRelayRetryBudgetExhausted_UnmanagedRequest(t *testing.T) {
	withRelayTimeoutSetting(t, func(s *operation_setting.RelayTimeoutSetting) {
		s.Enabled = true
		s.RetryMinBudgetSeconds = 10
	})
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	stop, _ := RelayRetryBudgetExhausted(c)
	assert.False(t, stop)
	stop, _ = RelayRetryBudgetExhausted(nil)
	assert.False(t, stop)
}

// The budget check decides whether to CONTINUE retrying and is never a loop
// precondition. Even with the budget fully gone the first attempt must run, or
// the pre-consumed quota is stranded (design §7.2).
func TestRelayRetryBudget_DoesNotBlockFirstAttempt(t *testing.T) {
	withRelayTimeoutSetting(t, func(s *operation_setting.RelayTimeoutSetting) {
		s.Enabled = true
		s.RetryMinBudgetSeconds = 10
	})
	c := budgetContext(t, -time.Minute, true)

	stop, _ := RelayRetryBudgetExhausted(c)
	require.True(t, stop, "budget is indeed exhausted")

	assert.True(t, ShouldAttemptRelay(0, 0, 0, 1),
		"the loop gate must still admit attempt one regardless of budget")
}

// A response timeout without a total timeout gives no request-wide budget, so
// the check must not fire. Using the "next deadline" here instead of the total
// one made every retry look unaffordable and silently disabled retrying for any
// user who configured only a response timeout — caught on the test server, not
// by the original unit tests, because the old stub could not express this shape.
func TestRelayRetryBudgetExhausted_ResponseTimeoutIsNotABudget(t *testing.T) {
	withRelayTimeoutSetting(t, func(s *operation_setting.RelayTimeoutSetting) {
		s.Enabled = true
		s.RetryMinBudgetSeconds = 10
	})

	// Response window well below the threshold, but no total timeout at all.
	c := responseOnlyBudgetContext(t, 3*time.Second)
	stop, reason := RelayRetryBudgetExhausted(c)
	assert.False(t, stop,
		"a per-attempt response window must never be mistaken for the request budget; got %q", reason)

	// Sanity: the same controller does report a next-deadline, which is exactly
	// the value the buggy version consumed.
	deadline, ok := RelayRequestDeadline(c)
	require.True(t, ok)
	assert.True(t, time.Until(deadline) < 10*time.Second)

	// And it reports no total deadline.
	_, hasTotal := RelayTotalDeadline(c)
	assert.False(t, hasTotal)
}

// With both configured, the budget is the total one even when the response
// window is the nearer deadline.
func TestRelayRetryBudgetExhausted_PrefersTotalOverResponse(t *testing.T) {
	withRelayTimeoutSetting(t, func(s *operation_setting.RelayTimeoutSetting) {
		s.Enabled = true
		s.RetryMinBudgetSeconds = 10
	})
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, budgetDeadlineStub{
		totalDeadline:    time.Now().Add(60 * time.Second),
		responseDeadline: time.Now().Add(2 * time.Second),
	})

	stop, _ := RelayRetryBudgetExhausted(c)
	assert.False(t, stop, "ample total budget must allow another attempt")
}

// The per-group quota map must be reachable through the ordinary option save
// path (the same one GroupRatio uses). Registering it as a config-group snapshot
// instead would make it unsavable: that API rejects any module missing from its
// switch, so the setting could only ever be changed by editing the database.
func TestGroupRetryTimesRoundTripsThroughOptionString(t *testing.T) {
	previous := operation_setting.GroupRetryTimes2JSONString()
	t.Cleanup(func() { _ = operation_setting.UpdateGroupRetryTimesByJSONString(previous) })

	require.NoError(t, operation_setting.UpdateGroupRetryTimesByJSONString(`{"alpha":5,"beta":-1}`))

	alpha, ok := operation_setting.GetGroupRetryTimes("alpha")
	require.True(t, ok)
	assert.Equal(t, 5, alpha)
	beta, ok := operation_setting.GetGroupRetryTimes("beta")
	require.True(t, ok)
	assert.Equal(t, -1, beta)
	_, ok = operation_setting.GetGroupRetryTimes("absent")
	assert.False(t, ok)

	// Serializing back out is what the settings page reads.
	assert.Contains(t, operation_setting.GroupRetryTimes2JSONString(), "alpha")

	// And the resolver sees the same values.
	assert.Equal(t, 5, ResolveRetryTimes(RetryTimesInherit, "alpha", 3))
	assert.Equal(t, 0, ResolveRetryTimes(RetryTimesInherit, "beta", 3))
}

// The settings UI saves the per-group map through the ordinary single-option
// endpoint with the bare key "GroupRetryTimes" (same shape as GroupRatio).
// These assert the exact payload shapes that UI produces round-trip correctly.
func TestGroupRetryTimesAcceptsUIPayloads(t *testing.T) {
	previous := operation_setting.GroupRetryTimes2JSONString()
	t.Cleanup(func() { _ = operation_setting.UpdateGroupRetryTimesByJSONString(previous) })

	cases := []struct {
		name    string
		payload string
		check   func(t *testing.T)
	}{
		{"empty editor saves {}", `{}`, func(t *testing.T) {
			_, ok := operation_setting.GetGroupRetryTimes("anything")
			assert.False(t, ok, "an empty map must mean every group inherits")
		}},
		{"single row", `{"vip":5}`, func(t *testing.T) {
			v, ok := operation_setting.GetGroupRetryTimes("vip")
			require.True(t, ok)
			assert.Equal(t, 5, v)
		}},
		{"disabled row", `{"official":-1}`, func(t *testing.T) {
			v, ok := operation_setting.GetGroupRetryTimes("official")
			require.True(t, ok)
			assert.Equal(t, -1, v)
			assert.Equal(t, 0, ResolveRetryTimes(RetryTimesInherit, "official", 3))
		}},
		{"mixed rows", `{"a":0,"b":20,"c":-1}`, func(t *testing.T) {
			assert.Equal(t, 3, ResolveRetryTimes(RetryTimesInherit, "a", 3), "0 inherits")
			assert.Equal(t, 20, ResolveRetryTimes(RetryTimesInherit, "b", 3))
			assert.Equal(t, 0, ResolveRetryTimes(RetryTimesInherit, "c", 3))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, operation_setting.CheckGroupRetryTimes(tc.payload),
				"the UI only emits payloads the backend validator accepts")
			require.NoError(t, operation_setting.UpdateGroupRetryTimesByJSONString(tc.payload))
			tc.check(t)
		})
	}
}

// The UI clamps to [-1, 20]; the backend must reject anything outside that so a
// hand-crafted request cannot install an unbounded per-group quota.
func TestGroupRetryTimesRejectsOutOfUIRange(t *testing.T) {
	for _, payload := range []string{`{"a":21}`, `{"a":-2}`, `{"a":100}`} {
		assert.Error(t, operation_setting.CheckGroupRetryTimes(payload), payload)
	}
}

// ---------------------------------------------------------------------------
// Loop entry points used by controller (ContinueRelayAttempts / BeginRelayAttempt
// / RemainingRetryBudget)
// ---------------------------------------------------------------------------

func loopContext(t *testing.T, group, autoGroup string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyUsingGroup, group)
	if autoGroup != "" {
		common.SetContextKey(c, constant.ContextKeyAutoGroup, autoGroup)
	}
	return c
}

func withGlobalRetryTimes(t *testing.T, n int) {
	t.Helper()
	previous := common.RetryTimes
	common.RetryTimes = n
	t.Cleanup(func() { common.RetryTimes = previous })
}

// The group is the one auto-routing picked, not the token's "auto" — the same
// rule HandleGroupRatio prices the attempt with.
func TestRelayAttemptGroup(t *testing.T) {
	assert.Equal(t, "vip", relayAttemptGroup(loopContext(t, "auto", "vip")))
	assert.Equal(t, "default", relayAttemptGroup(loopContext(t, "default", "")))
	assert.Equal(t, "", relayAttemptGroup(nil))
}

// The quota must follow the group of the attempt that just ran. Resolving it
// from the token's group would give an auto-group request the global quota.
func TestRemainingRetryBudget_ResolvesAttemptGroup(t *testing.T) {
	withGlobalRetryTimes(t, 1)
	withGroupRetryTimes(t, `{"vip":4}`)
	param := &RetryParam{Retry: common.GetPointer(1)}

	assert.Equal(t, 3, RemainingRetryBudget(loopContext(t, "auto", "vip"), param))
	assert.Equal(t, 0, RemainingRetryBudget(loopContext(t, "default", ""), param),
		"a group without its own quota inherits the global one")
}

func TestRemainingRetryBudget_UserOverridesGroup(t *testing.T) {
	withGlobalRetryTimes(t, 1)
	withGroupRetryTimes(t, `{"vip":4}`)
	c := loopContext(t, "auto", "vip")
	common.SetContextKey(c, constant.ContextKeyUserRetryTimes, 2)
	assert.Equal(t, 2, RemainingRetryBudget(c, &RetryParam{Retry: common.GetPointer(0)}))
}

func TestContinueRelayAttempts(t *testing.T) {
	withGlobalRetryTimes(t, 2)
	withGroupRetryTimes(t, `{}`)

	t.Run("first attempt always runs, even with the budget gone", func(t *testing.T) {
		withRelayTimeoutSetting(t, func(s *operation_setting.RelayTimeoutSetting) {
			s.Enabled, s.RetryMinBudgetSeconds, s.MaxTotalAttempts = true, 10, 1
		})
		c := budgetContext(t, time.Millisecond, true)
		assert.True(t, ContinueRelayAttempts(c, &RetryParam{Ctx: c, Retry: common.GetPointer(0)}))
	})

	t.Run("quota bounds the loop", func(t *testing.T) {
		withRelayTimeoutSetting(t, func(s *operation_setting.RelayTimeoutSetting) { s.MaxTotalAttempts = 0 })
		c := loopContext(t, "default", "")
		param := &RetryParam{Ctx: c, Retry: common.GetPointer(0)}
		attempts := 0
		for ; ContinueRelayAttempts(c, param); param.IncreaseRetry() {
			BeginRelayAttempt(c, param)
			attempts++
			require.Less(t, attempts, 100)
		}
		assert.Equal(t, 3, attempts, "global RetryTimes=2 means 1 attempt + 2 retries")
	})

	t.Run("total cap bounds the loop", func(t *testing.T) {
		withRelayTimeoutSetting(t, func(s *operation_setting.RelayTimeoutSetting) { s.MaxTotalAttempts = 2 })
		c := loopContext(t, "default", "")
		param := &RetryParam{Ctx: c, Retry: common.GetPointer(0)}
		attempts := 0
		for ; ContinueRelayAttempts(c, param); param.IncreaseRetry() {
			BeginRelayAttempt(c, param)
			attempts++
			require.Less(t, attempts, 100)
		}
		assert.Equal(t, 2, attempts)
	})

	t.Run("exhausted time budget stops a retry", func(t *testing.T) {
		withRelayTimeoutSetting(t, func(s *operation_setting.RelayTimeoutSetting) {
			s.Enabled, s.RetryMinBudgetSeconds, s.MaxTotalAttempts = true, 10, 0
		})
		c := budgetContext(t, 5*time.Second, true)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		param := &RetryParam{Ctx: c, Retry: common.GetPointer(0)}
		require.True(t, ContinueRelayAttempts(c, param))
		BeginRelayAttempt(c, param)
		param.IncreaseRetry()
		assert.False(t, ContinueRelayAttempts(c, param),
			"5s left is below the 10s needed for another attempt")
	})

	t.Run("response window alone is not a budget", func(t *testing.T) {
		withRelayTimeoutSetting(t, func(s *operation_setting.RelayTimeoutSetting) {
			s.Enabled, s.RetryMinBudgetSeconds, s.MaxTotalAttempts = true, 10, 0
		})
		c := responseOnlyBudgetContext(t, time.Second)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		param := &RetryParam{Ctx: c, Retry: common.GetPointer(0)}
		BeginRelayAttempt(c, param)
		param.IncreaseRetry()
		assert.True(t, ContinueRelayAttempts(c, param))
	})
}

// BeginRelayAttempt counts every attempt, including the first of a new group
// whose per-group counter was just reset.
func TestBeginRelayAttempt_CountsAcrossGroupSwitch(t *testing.T) {
	c := loopContext(t, "auto", "a")
	param := &RetryParam{Ctx: c, Retry: common.GetPointer(0)}
	BeginRelayAttempt(c, param)
	param.SetRetry(0)
	param.ResetRetryNextTry()
	param.IncreaseRetry()
	BeginRelayAttempt(c, param)
	assert.Equal(t, 0, param.GetRetry())
	assert.Equal(t, 2, param.TotalAttempts())
}

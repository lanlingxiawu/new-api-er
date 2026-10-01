package service

import (
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// retryAuditRunLoop drives the exact three calls controller.Relay makes
// (ContinueRelayAttempts / BeginRelayAttempt / RemainingRetryBudget) for a
// request whose every attempt fails with a retryable status. controller's
// shouldRetry stops as soon as the remaining budget is not positive, so the
// loop breaks there, like the real one.
func retryAuditRunLoop(t *testing.T, c *gin.Context) (attempts int, param *RetryParam) {
	t.Helper()
	param = &RetryParam{Ctx: c, Retry: common.GetPointer(0)}
	for ; ContinueRelayAttempts(c, param); param.IncreaseRetry() {
		BeginRelayAttempt(c, param)
		attempts++
		require.Less(t, attempts, 1000, "retry loop did not terminate")
		if RemainingRetryBudget(c, param) <= 0 {
			break
		}
	}
	return attempts, param
}

// retryAuditMainLoopAttempts is main's loop: `for retry := 0; retry <= RetryTimes`
// with shouldRetry(RetryTimes - retry) deciding whether to go on.
func retryAuditMainLoopAttempts(retryTimes int) int {
	attempts := 0
	for retry := 0; retry <= retryTimes; retry++ {
		attempts++
		if retryTimes-retry <= 0 {
			break
		}
	}
	return attempts
}

func retryAuditLoopDefaults(t *testing.T) {
	t.Helper()
	withGroupRetryTimes(t, `{}`)
	withRelayTimeoutSetting(t, func(s *operation_setting.RelayTimeoutSetting) {
		s.MaxTotalAttempts = 0
		s.RetryMinBudgetSeconds = 0
	})
}

// main's loop condition `retry <= RetryTimes` makes ZERO attempts for a
// negative global RetryTimes (the option is saved with a bare strconv.Atoi, so
// nothing rejects "-1"). A zero-attempt request leaves newAPIError nil, so the
// refund defer never fires and the pre-consumed quota is stranded. The fork's
// loop must make exactly one attempt instead.
func TestBranchAuditNegativeGlobalRetryTimesAttemptsExactlyOnce(t *testing.T) {
	retryAuditLoopDefaults(t)
	for _, global := range []int{-1, -2, -100, math.MinInt32} {
		t.Run(fmt.Sprint(global), func(t *testing.T) {
			withGlobalRetryTimes(t, global)
			assert.Equal(t, 0, retryAuditMainLoopAttempts(global), "main's behaviour this guards against")

			attempts, param := retryAuditRunLoop(t, loopContext(t, "default", ""))
			assert.Equal(t, 1, attempts)
			assert.Equal(t, 1, param.TotalAttempts())
			assert.Equal(t, 0, RemainingRetryBudget(loopContext(t, "default", ""), &RetryParam{Retry: common.GetPointer(0)}),
				"a negative global quota must never hand shouldRetry a negative or positive budget")
		})
	}
}

// With every override unset the attempt count must equal main's for every
// non-negative global RetryTimes, including the boundaries 0 and 1.
func TestBranchAuditAttemptCountParityWithMain(t *testing.T) {
	retryAuditLoopDefaults(t)
	for _, global := range []int{0, 1, 2, 3, 20, 50} {
		t.Run(fmt.Sprint(global), func(t *testing.T) {
			withGlobalRetryTimes(t, global)
			attempts, _ := retryAuditRunLoop(t, loopContext(t, "default", ""))
			assert.Equal(t, retryAuditMainLoopAttempts(global), attempts)
			assert.Equal(t, global+1, attempts, "RetryTimes counts retries after the first attempt, not attempts")
		})
	}
}

// Every branch of the user > group > global resolution, observed through the
// real loop rather than the resolver alone.
func TestBranchAuditAttemptsPerResolvedQuota(t *testing.T) {
	cases := []struct {
		name     string
		groups   string
		group    string
		user     int
		global   int
		maxTotal int
		want     int
	}{
		{"group absent inherits global", `{"other":9}`, "g", 0, 3, 0, 4},
		{"group -1 means one attempt", `{"g":-1}`, "g", 0, 3, 0, 1},
		{"group 0 inherits global", `{"g":0}`, "g", 0, 3, 0, 4},
		{"group 1 beats global 0", `{"g":1}`, "g", 0, 0, 0, 2},
		{"group at the validator max", `{"g":20}`, "g", 0, 0, 0, 21},
		{"group below -1 treated as no retry", `{"g":-7}`, "g", 0, 3, 0, 1},
		{"user -1 beats a group quota", `{"g":5}`, "g", -1, 3, 0, 1},
		{"user positive beats group -1", `{"g":-1}`, "g", 2, 3, 0, 3},
		{"user below -1 treated as no retry", `{}`, "g", -5, 3, 0, 1},
		{"user at the validator max", `{}`, "g", 20, 1, 0, 21},
		{"empty group name never matches", `{"":9}`, "", 0, 1, 0, 2},
		{"total cap below quota", `{"g":20}`, "g", 0, 0, 5, 5},
		{"total cap of one", `{"g":20}`, "g", 0, 0, 1, 1},
		{"total cap above quota is inert", `{"g":2}`, "g", 0, 0, 100, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withGroupRetryTimes(t, tc.groups)
			withGlobalRetryTimes(t, tc.global)
			withRelayTimeoutSetting(t, func(s *operation_setting.RelayTimeoutSetting) {
				s.MaxTotalAttempts = tc.maxTotal
				s.RetryMinBudgetSeconds = 0
			})
			c := loopContext(t, tc.group, "")
			if tc.user != 0 {
				common.SetContextKey(c, constant.ContextKeyUserRetryTimes, tc.user)
			}
			attempts, param := retryAuditRunLoop(t, c)
			assert.Equal(t, tc.want, attempts)
			assert.Equal(t, attempts, param.TotalAttempts())
		})
	}
}

// The total-attempt cap sits in the "AI request timeout" setting block but is
// documented (design §6.3/§6.4) as unconditional. It must bound the loop even
// with the timeout feature switched off, while the time-budget gate — which
// needs that feature — must not fire.
func TestBranchAuditMaxTotalAttemptsIgnoresTimeoutSwitch(t *testing.T) {
	withGroupRetryTimes(t, `{}`)
	withGlobalRetryTimes(t, 5)
	withRelayTimeoutSetting(t, func(s *operation_setting.RelayTimeoutSetting) {
		s.Enabled = false
		s.MaxTotalAttempts = 2
		s.RetryMinBudgetSeconds = 60
	})
	c := budgetContext(t, time.Second, true)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	attempts, _ := retryAuditRunLoop(t, c)
	assert.Equal(t, 2, attempts, "cap applies and the disabled budget gate does not cut it to 1")
}

// A per-group value can reach the map without the validator: model/option.go
// loads the stored option straight into it at startup and on every option
// sync. The resolver does not clamp, so the total cap is the only runtime bound
// on such a value.
func TestBranchAuditUnvalidatedHugeGroupQuotaIsBoundedOnlyByTotalCap(t *testing.T) {
	withGlobalRetryTimes(t, 0)
	withGroupRetryTimes(t, `{"g":1000}`)
	require.Error(t, operation_setting.CheckGroupRetryTimes(`{"g":1000}`), "the save path rejects it")
	assert.Equal(t, 1000, ResolveRetryTimes(RetryTimesInherit, "g", 0), "the load path does not")

	withRelayTimeoutSetting(t, func(s *operation_setting.RelayTimeoutSetting) {
		s.MaxTotalAttempts = 4
		s.RetryMinBudgetSeconds = 0
	})
	attempts, _ := retryAuditRunLoop(t, loopContext(t, "g", ""))
	assert.Equal(t, 4, attempts)
}

// A malformed stored value must not wipe the quotas already in force: the
// map is replaced only after the new JSON decodes.
func TestBranchAuditMalformedGroupRetryJSONKeepsPreviousQuotas(t *testing.T) {
	withGroupRetryTimes(t, `{"g":5}`)
	for _, bad := range []string{`not json`, `{"g":"x"}`, `{"g":1.5}`, `[1,2]`, ``, `{"g":9223372036854775808}`} {
		t.Run(bad, func(t *testing.T) {
			assert.Error(t, operation_setting.UpdateGroupRetryTimesByJSONString(bad))
			v, ok := operation_setting.GetGroupRetryTimes("g")
			require.True(t, ok)
			assert.Equal(t, 5, v)
			assert.Error(t, operation_setting.CheckGroupRetryTimes(bad), "the save path must reject it too")
		})
	}
}

// JSON null decodes to an empty map; every group then inherits and nothing
// panics on lookup.
func TestBranchAuditNullGroupRetryJSONInheritsEverywhere(t *testing.T) {
	withGroupRetryTimes(t, `{"g":5}`)
	require.NoError(t, operation_setting.CheckGroupRetryTimes(`null`))
	require.NoError(t, operation_setting.UpdateGroupRetryTimesByJSONString(`null`))
	_, ok := operation_setting.GetGroupRetryTimes("g")
	assert.False(t, ok)
	assert.Equal(t, 3, ResolveRetryTimes(RetryTimesInherit, "g", 3))
	assert.NotPanics(t, func() { _ = operation_setting.GetGroupRetryTimesCopy() })
}

func TestBranchAuditCheckGroupRetryTimesBoundaries(t *testing.T) {
	for payload, wantErr := range map[string]bool{
		`{"g":-2}`:                   true,
		`{"g":-1}`:                   false,
		`{"g":0}`:                    false,
		`{"g":1}`:                    false,
		`{"g":20}`:                   false,
		`{"g":21}`:                   true,
		`{"g":2147483648}`:           true,
		`{"g":-2147483649}`:          true,
		`{"a":1,"b":21}`:             true,
		`{"a":-1,"b":20,"c":0}`:      false,
		`{"g":5,"g":25}`:             true, // duplicate keys: the last value is the one stored
		`{"g":null}`:                 false,
		`{"vip group":3,"中文分组":2}`:   false,
		`{"g":-9223372036854775808}`: true,
	} {
		t.Run(payload, func(t *testing.T) {
			err := operation_setting.CheckGroupRetryTimes(payload)
			if wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Per-user override validation, including values far outside the range.
func TestBranchAuditValidateRetryTimesBoundaries(t *testing.T) {
	for _, tc := range []struct {
		value int
		ok    bool
	}{
		{math.MinInt, false},
		{-2, false},
		{RetryTimesDisabled, true},
		{RetryTimesInherit, true},
		{1, true},
		{MaxUserRetryTimes, true},
		{MaxUserRetryTimes + 1, false},
		{math.MaxInt, false},
	} {
		t.Run(fmt.Sprint(tc.value), func(t *testing.T) {
			err := ValidateRetryTimes(tc.value)
			if tc.ok {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

// Millisecond-level boundaries of the remaining-budget gate. The comparison
// is remaining >= min → continue; "exactly equal" cannot be observed on a wall
// clock (time moves between the deadline read and time.Until), so the two
// sides of the boundary are pinned instead.
func TestBranchAuditBudgetGateMillisecondBoundaries(t *testing.T) {
	withGroupRetryTimes(t, `{}`)
	withGlobalRetryTimes(t, 5)
	withRelayTimeoutSetting(t, func(s *operation_setting.RelayTimeoutSetting) {
		s.Enabled = true
		s.RetryMinBudgetSeconds = 10
		s.MaxTotalAttempts = 0
	})
	for _, tc := range []struct {
		remaining time.Duration
		cont      bool
	}{
		{10*time.Second + 500*time.Millisecond, true},
		{10*time.Second - 50*time.Millisecond, false},
		{0, false},
	} {
		t.Run(tc.remaining.String(), func(t *testing.T) {
			c := budgetContext(t, tc.remaining, true)
			common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
			param := &RetryParam{Ctx: c, Retry: common.GetPointer(0)}
			require.True(t, ContinueRelayAttempts(c, param), "the first attempt ignores the budget")
			BeginRelayAttempt(c, param)
			param.IncreaseRetry()
			assert.Equal(t, tc.cont, ContinueRelayAttempts(c, param))
		})
	}
}

// When routing prepares a switch, the next attempt runs on the new group and
// the retry decision after it must use the NEW group's quota: a -1 group gets
// its single attempt and ends the request instead of inheriting the previous
// group's remaining retries.
func TestBranchAuditQuotaFollowsGroupAfterPreparedSwitch(t *testing.T) {
	withGlobalRetryTimes(t, 3)
	withGroupRetryTimes(t, `{"a":1,"b":-1}`)
	withRelayTimeoutSetting(t, func(s *operation_setting.RelayTimeoutSetting) {
		s.MaxTotalAttempts = 0
		s.RetryMinBudgetSeconds = 0
	})
	c := loopContext(t, "auto", "a")
	param := &RetryParam{Ctx: c, Retry: common.GetPointer(0)}

	require.True(t, ContinueRelayAttempts(c, param))
	BeginRelayAttempt(c, param) // attempt 1 on a
	require.Equal(t, 1, RemainingRetryBudget(c, param))
	param.IncreaseRetry()

	require.True(t, ContinueRelayAttempts(c, param))
	BeginRelayAttempt(c, param) // attempt 2 on a; routing spends a and prepares b
	param.SetRetry(0)
	param.ResetRetryNextTry()
	require.Equal(t, 1, RemainingRetryBudget(c, param), "a still has quota for the switch")
	param.IncreaseRetry()

	require.True(t, ContinueRelayAttempts(c, param))
	BeginRelayAttempt(c, param) // attempt 3 lands on b
	common.SetContextKey(c, constant.ContextKeyAutoGroup, "b")
	assert.Equal(t, 0, RemainingRetryBudget(c, param), "b is a -1 group: one attempt, no retry")
	assert.Equal(t, 3, param.TotalAttempts())
}

// The loop reads the hot settings on every call while admins save them. The
// values are published through an atomic snapshot and an RWMap; this is only
// meaningful under -race but must also never produce an out-of-contract count.
func TestBranchAuditConcurrentSettingSwapKeepsLoopBounded(t *testing.T) {
	withGroupRetryTimes(t, `{}`)
	withGlobalRetryTimes(t, 2)
	withRelayTimeoutSetting(t, func(s *operation_setting.RelayTimeoutSetting) { s.MaxTotalAttempts = 0 })
	base := operation_setting.GetRelayTimeoutSetting()

	stop := make(chan struct{})
	var writers sync.WaitGroup
	writers.Add(1)
	go func() {
		defer writers.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			next := base
			next.MaxTotalAttempts = i % 4
			operation_setting.ReplaceRelayTimeoutSetting(next)
			_ = operation_setting.UpdateGroupRetryTimesByJSONString(fmt.Sprintf(`{"default":%d}`, i%3-1))
		}
	}()

	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for i := 0; i < 200; i++ {
				c := loopContext(t, "default", "")
				param := &RetryParam{Ctx: c, Retry: common.GetPointer(0)}
				attempts := 0
				for ; ContinueRelayAttempts(c, param); param.IncreaseRetry() {
					BeginRelayAttempt(c, param)
					attempts++
					if attempts > 50 || RemainingRetryBudget(c, param) <= 0 {
						break
					}
				}
				// quota is at most max(global 2, group 1) = 2 → at most 3 attempts.
				assert.GreaterOrEqual(t, attempts, 1)
				assert.LessOrEqual(t, attempts, 3)
			}
		}()
	}
	readers.Wait()
	close(stop)
	writers.Wait()
}

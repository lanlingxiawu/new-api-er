package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// autoGroupRetryContext wires a two-group auto-routing request.
func autoGroupRetryContext(t *testing.T, modelName string) (*gin.Context, *RetryParam) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip", "default"})
	common.SetContextKey(ctx, constant.ContextKeyTokenCrossGroupRetry, true)

	retry := 0
	return ctx, &RetryParam{
		Ctx:         ctx,
		TokenGroup:  "auto",
		ModelName:   modelName,
		RequestPath: "/v1/chat/completions",
		Retry:       &retry,
	}
}

// Selection prepares the next group as soon as a -1 group has had its single
// attempt. Whether the loop takes that switch is decided by the retry budget
// (quota 0 means it does not, as on main); this test covers selection only.
func TestChannelSelect_GroupQuotaDisabledSwitchesImmediately(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	const modelName = "retry-quota-disabled-model"
	createChannelSelectAutoGroupsChannel(t, db, 2301, "vip", modelName)
	createChannelSelectAutoGroupsChannel(t, db, 2302, "default", modelName)
	model.InitChannelCache()

	// vip disabled -> quota 0 -> priorityRetry(0) >= 0 -> prepare switch.
	withGroupRetryTimes(t, `{"vip":-1}`)

	ctx, param := autoGroupRetryContext(t, modelName)
	first, group, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, "vip", group, "the disabled group is still tried once")

	// The group index was advanced, so the next attempt lands on the next group.
	param.IncreaseRetry()
	second, group2, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, "default", group2,
		"after a -1 group's single attempt, selection points at the next group")
	_ = ctx
}

// A generous group quota keeps the request inside that group rather than
// switching after the global RetryTimes.
func TestChannelSelect_GroupQuotaKeepsRequestInGroup(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	const modelName = "retry-quota-generous-model"
	createChannelSelectAutoGroupsChannel(t, db, 2311, "vip", modelName)
	createChannelSelectAutoGroupsChannel(t, db, 2312, "default", modelName)
	model.InitChannelCache()

	withGroupRetryTimes(t, `{"vip":5}`)

	_, param := autoGroupRetryContext(t, modelName)
	_, group, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	assert.Equal(t, "vip", group)

	// priorityRetry(1) < quota(5): still vip, no switch prepared.
	param.IncreaseRetry()
	_, group2, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	assert.Equal(t, "vip", group2,
		"a group quota above the retry index must not hand off to the next group")
}

// The core of design §6.3: a group switch resets the per-group counter, and the
// total counter must NOT follow it — that reset is what let a request reach
// 分组数 × (RetryTimes+1) attempts.
func TestChannelSelect_GroupSwitchDoesNotResetTotalAttempts(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	const modelName = "retry-total-attempts-model"
	createChannelSelectAutoGroupsChannel(t, db, 2321, "vip", modelName)
	createChannelSelectAutoGroupsChannel(t, db, 2322, "default", modelName)
	model.InitChannelCache()

	withGroupRetryTimes(t, `{"vip":-1}`)

	_, param := autoGroupRetryContext(t, modelName)
	param.CountAttempt()
	_, _, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)

	// The switch path calls SetRetry(0); the per-group counter is reset...
	assert.Equal(t, 0, param.GetRetry())
	// ...while the total attempt counter is untouched.
	assert.Equal(t, 1, param.TotalAttempts(),
		"a group switch must not restart the request-wide attempt count")

	param.CountAttempt()
	assert.Equal(t, 2, param.TotalAttempts())

	// With the total cap at 2, no further attempt is allowed even though the
	// per-group counter was just reset to 0.
	assert.False(t, ShouldAttemptRelay(param.GetRetry(), relayRetryQuota(param.Ctx), param.TotalAttempts(), 2),
		"the total gate must bound a request that keeps switching groups")
}

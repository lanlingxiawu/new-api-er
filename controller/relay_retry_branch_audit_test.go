package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func retryAuditUpdateUserRetryTimes(t *testing.T, user *model.User, extra map[string]any) apiResp {
	t.Helper()
	body := map[string]any{"id": user.Id, "username": user.Username, "group": user.Group}
	for k, v := range extra {
		body[k] = v
	}
	ctx, rec := newCtx(t, http.MethodPut, "/api/user/", body)
	asAdmin(ctx, nextTestID())
	UpdateUser(ctx)
	return decodeResp(t, rec)
}

func retryAuditStoredRetryTimes(t *testing.T, id int) int {
	t.Helper()
	var stored model.User
	require.NoError(t, model.DB.Select("retry_times").First(&stored, id).Error)
	return stored.RetryTimes
}

// The per-user retry override is an optional pointer field: omitted keeps the
// stored value, an explicit 0 resets it to "inherit", and every boundary of
// ValidateRetryTimes is enforced by the admin endpoint without touching the
// stored value on rejection.
func TestBranchAuditUpdateUserRetryTimes(t *testing.T) {
	requireDB(t)

	t.Run("omitted preserves", func(t *testing.T) {
		user := mkUser(t, func(u *model.User) { u.RetryTimes = 7 })
		require.True(t, retryAuditUpdateUserRetryTimes(t, user, nil).Success)
		assert.Equal(t, 7, retryAuditStoredRetryTimes(t, user.Id))
	})

	for _, value := range []int{service.RetryTimesDisabled, service.RetryTimesInherit, 1, service.MaxUserRetryTimes} {
		value := value
		t.Run("accepts "+common.Interface2String(value), func(t *testing.T) {
			user := mkUser(t, func(u *model.User) { u.RetryTimes = 7 })
			require.True(t, retryAuditUpdateUserRetryTimes(t, user, map[string]any{"retry_times": value}).Success)
			assert.Equal(t, value, retryAuditStoredRetryTimes(t, user.Id))
		})
	}

	for _, value := range []int{-2, -100, service.MaxUserRetryTimes + 1, 1_000_000} {
		value := value
		t.Run("rejects "+common.Interface2String(value), func(t *testing.T) {
			user := mkUser(t, func(u *model.User) { u.RetryTimes = 7 })
			assert.False(t, retryAuditUpdateUserRetryTimes(t, user, map[string]any{"retry_times": value}).Success)
			assert.Equal(t, 7, retryAuditStoredRetryTimes(t, user.Id), "a rejected value must not be stored")
		})
	}
}

// A valid per-group map saved through the ordinary option endpoint reaches the
// in-memory map the retry loop reads, and the shape the UI sends (a JSON
// string value) is what gets stored.
func TestBranchAuditUpdateOptionAppliesGroupRetryTimes(t *testing.T) {
	requireDB(t)

	var existing model.Option
	existed := model.DB.Where(&model.Option{Key: "GroupRetryTimes"}).Limit(1).Find(&existing).RowsAffected > 0
	previousMap := operation_setting.GroupRetryTimes2JSONString()
	common.OptionMapRWMutex.RLock()
	previousOption, hadOption := common.OptionMap["GroupRetryTimes"]
	common.OptionMapRWMutex.RUnlock()
	t.Cleanup(func() {
		if existed {
			require.NoError(t, model.UpdateOption("GroupRetryTimes", existing.Value))
		} else {
			model.DB.Where(&model.Option{Key: "GroupRetryTimes"}).Delete(&model.Option{})
		}
		_ = operation_setting.UpdateGroupRetryTimesByJSONString(previousMap)
		common.OptionMapRWMutex.Lock()
		if hadOption {
			common.OptionMap["GroupRetryTimes"] = previousOption
		} else {
			delete(common.OptionMap, "GroupRetryTimes")
		}
		common.OptionMapRWMutex.Unlock()
	})

	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/option/",
		strings.NewReader(`{"key":"GroupRetryTimes","value":"{\"ba_vip\":4,\"ba_official\":-1,\"ba_zero\":0}"}`))
	ctx.Set("role", common.RoleRootUser) // the unscoped write path is root-only
	UpdateOption(ctx)

	var payload struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success, payload.Message)

	vip, ok := operation_setting.GetGroupRetryTimes("ba_vip")
	require.True(t, ok)
	assert.Equal(t, 4, vip)
	assert.Equal(t, 0, service.ResolveRetryTimes(service.RetryTimesInherit, "ba_official", 3))
	assert.Equal(t, 3, service.ResolveRetryTimes(service.RetryTimesInherit, "ba_zero", 3))
}

// A rejected save must leave the quotas in force untouched.
func TestBranchAuditRejectedGroupRetryTimesKeepsLiveMap(t *testing.T) {
	previous := operation_setting.GroupRetryTimes2JSONString()
	require.NoError(t, operation_setting.UpdateGroupRetryTimesByJSONString(`{"ba_keep":3}`))
	t.Cleanup(func() { _ = operation_setting.UpdateGroupRetryTimesByJSONString(previous) })

	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/option/",
		strings.NewReader(`{"key":"GroupRetryTimes","value":"{\"ba_keep\":99}"}`))
	ctx.Set("role", common.RoleRootUser) // the unscoped write path is root-only
	UpdateOption(ctx)
	assert.False(t, decodeResp(t, response).Success)

	v, ok := operation_setting.GetGroupRetryTimes("ba_keep")
	require.True(t, ok)
	assert.Equal(t, 3, v)
}

// shouldRetry sees service.RemainingRetryBudget instead of main's
// RetryTimes-retry. The fork-specific outcomes at the budget boundary: a
// relay-timeout never retries even with budget left, an ordinary retryable
// status stops at budget 0 and continues at 1, and a channel error keeps
// main's behaviour of retrying regardless of the budget (the loop condition,
// not shouldRetry, then bounds it).
func TestBranchAuditShouldRetryAtBudgetBoundary(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	assert.False(t, shouldRetry(c, nil, 5))

	timeoutErr := relayTimeoutAPIError(c)
	assert.False(t, shouldRetry(c, timeoutErr, 5), "an owned timeout carries SkipRetry")

	upstream500 := retryAuditStatusError(http.StatusInternalServerError)
	assert.False(t, shouldRetry(c, upstream500, 0))
	assert.False(t, shouldRetry(c, upstream500, -1), "a negative budget is treated as none")
	assert.Equal(t, operation_setting.ShouldRetryByStatusCode(http.StatusInternalServerError), shouldRetry(c, upstream500, 1))

	channelErr := types.NewError(errors.New("no key"), types.ErrorCodeChannelNoAvailableKey)
	assert.True(t, shouldRetry(c, channelErr, 0), "main retries channel errors regardless of the budget")
}

func retryAuditStatusError(status int) *types.NewAPIError {
	return types.NewOpenAIError(errors.New("upstream failed"), types.ErrorCodeBadResponseStatusCode, status)
}

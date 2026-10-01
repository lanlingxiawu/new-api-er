package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupRetryStatusController(t *testing.T) {
	old := operation_setting.GetGroupRetryStatusSetting()
	t.Cleanup(func() { operation_setting.ReplaceGroupRetryStatusSetting(old) })
	operation_setting.ReplaceGroupRetryStatusSetting(operation_setting.GroupRetryStatusSetting{Enabled: true, Rules: `{"a":"429","b":"500-599","none":""}`})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "auto")
	common.SetContextKey(c, constant.ContextKeyAutoGroup, "a")
	common.SetContextKey(c, constant.ContextKeyAutoGroupIndex, 1) // selector has prepared the next group
	err := types.NewErrorWithStatusCode(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, 500)
	assert.False(t, shouldRetry(c, err, 2))
	assert.False(t, shouldRetryTaskRelay(c, 1, &taskdto.TaskError{StatusCode: 500}, 2))
	common.SetContextKey(c, constant.ContextKeyAutoGroup, "b")
	assert.True(t, shouldRetry(c, err, 2))
	assert.True(t, shouldRetryTaskRelay(c, 1, &taskdto.TaskError{StatusCode: 500}, 2))
	assert.False(t, shouldRetry(c, err, 0))
	assert.False(t, shouldRetryTaskRelay(c, 1, &taskdto.TaskError{StatusCode: 500, LocalError: true}, 2))
	for _, code := range []int{200, 504, 524} {
		err := types.NewErrorWithStatusCode(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, code)
		assert.False(t, shouldRetry(c, err, 2))
		assert.False(t, shouldRetryTaskRelay(c, 1, &taskdto.TaskError{StatusCode: code}, 2))
	}
	assert.False(t, shouldRetry(c, types.NewError(errors.New("stop"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry()), 2))
	c.Set("specific_channel_id", 1)
	assert.False(t, shouldRetry(c, err, 2))
}

func TestGroupRetryStatusUpstreamCallsAndBilling(t *testing.T) {
	old := operation_setting.GetGroupRetryStatusSetting()
	t.Cleanup(func() { operation_setting.ReplaceGroupRetryStatusSetting(old) })
	for _, tc := range []struct {
		rule         string
		hits, status int
	}{
		{"500", 2, 200}, {"429", 1, 500}, {"", 1, 500},
	} {
		t.Run(tc.rule, func(t *testing.T) {
			f := newRetryAuditFixture(t, []int{500}, http.StatusOK)
			raw, e := common.Marshal(map[string]string{"default": tc.rule})
			require.NoError(t, e)
			operation_setting.ReplaceGroupRetryStatusSetting(operation_setting.GroupRetryStatusSetting{Enabled: true, Rules: string(raw)})
			common.RetryTimes = 2
			rec := f.send(t)
			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			hits, _ := f.snapshot()
			assert.Equal(t, tc.hits, hits)
			quota := f.quota
			if tc.status == 200 {
				quota -= retryAuditPrice()
			}
			f.waitForQuotas(t, quota, quota)
		})
	}
}

func TestGroupRetryStatusDisabledAndMissingPreserveBehavior(t *testing.T) {
	old := operation_setting.GetGroupRetryStatusSetting()
	t.Cleanup(func() { operation_setting.ReplaceGroupRetryStatusSetting(old) })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	operation_setting.ReplaceGroupRetryStatusSetting(operation_setting.GroupRetryStatusSetting{Rules: "{}"})
	var normal, task [601]bool
	for code := 99; code <= 600; code++ {
		err := types.NewErrorWithStatusCode(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, code)
		normal[code] = shouldRetry(c, err, 2)
		task[code] = shouldRetryTaskRelay(c, 1, &taskdto.TaskError{StatusCode: code}, 2)
	}
	for _, setting := range []operation_setting.GroupRetryStatusSetting{
		{Enabled: false, Rules: `{"default":""}`},
		{Enabled: true, Rules: `{"other":""}`},
	} {
		operation_setting.ReplaceGroupRetryStatusSetting(setting)
		for code := 99; code <= 600; code++ {
			err := types.NewErrorWithStatusCode(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, code)
			assert.Equal(t, normal[code], shouldRetry(c, err, 2), "normal code %d", code)
			assert.Equal(t, task[code], shouldRetryTaskRelay(c, 1, &taskdto.TaskError{StatusCode: code}, 2), "task code %d", code)
		}
	}
}

package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestGroupRetryStatusUsesActualGroup(t *testing.T) {
	old := operation_setting.GetGroupRetryStatusSetting()
	t.Cleanup(func() { operation_setting.ReplaceGroupRetryStatusSetting(old) })
	operation_setting.ReplaceGroupRetryStatusSetting(operation_setting.GroupRetryStatusSetting{Enabled: true, Rules: `{"user":"429","a":"500","b":"429"}`})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyUserGroup, "user")
	_, found := GroupRetryStatusAllowed(c, 500)
	assert.False(t, found, "never substitute the registered user group")
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "a")
	common.SetContextKey(c, constant.ContextKeyAutoGroup, "b")
	allowed, found := GroupRetryStatusAllowed(c, 500)
	assert.True(t, allowed, "fixed group ignores stale auto selection")
	assert.True(t, found)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "auto")
	allowed, found = GroupRetryStatusAllowed(c, 500)
	assert.False(t, allowed)
	assert.True(t, found)
	_, found = GroupRetryStatusAllowed(nil, 500)
	assert.False(t, found)
}

func BenchmarkGroupRetryStatusAllowed(b *testing.B) {
	old := operation_setting.GetGroupRetryStatusSetting()
	b.Cleanup(func() { operation_setting.ReplaceGroupRetryStatusSetting(old) })
	operation_setting.ReplaceGroupRetryStatusSetting(operation_setting.GroupRetryStatusSetting{Enabled: true, Rules: `{"a":"429,500-599"}`})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "a")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		GroupRetryStatusAllowed(c, 500)
	}
}

package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// GroupRetryStatusAllowed uses the failed attempt's actual group, not the
// auto selector's already-advanced index or the user's registered group.
func GroupRetryStatusAllowed(c *gin.Context, status int) (bool, bool) {
	if c == nil {
		return false, false
	}
	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	if group == "auto" {
		group = common.GetContextKeyString(c, constant.ContextKeyAutoGroup)
	}
	if group == "" || group == "auto" {
		return false, false
	}
	return operation_setting.GroupRetryStatusAllowed(group, status)
}

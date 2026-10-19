package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/pkg/retryguard"
	"github.com/gin-gonic/gin"
	"net/http"
)

func GetGroupRetryGuardStatus(c *gin.Context) {
	scope, exists := c.Get(middleware.SystemSettingsScopeContextKey)
	if scope != "system-tuning.group-retry-times" && !((!exists || scope == "") && c.GetInt("role") == common.RoleRootUser) {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": retryguard.GetStatus()})
}

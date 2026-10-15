package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/settingsaccess"
	"net/http"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func GetRequestPolicy(c *gin.Context) {
	options := make(map[string]string)
	scope := c.GetString(middleware.SystemSettingsScopeContextKey)
	for key, value := range model.CurrentRequestPolicy().Options {
		if scope == "" || settingsaccess.AllowsOption(scope, key) {
			options[key] = value
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"options": options}})
}

func UpdateRequestPolicy(c *gin.Context) {
	var request struct {
		Options map[string]string `json:"options"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	scope := c.GetString(middleware.SystemSettingsScopeContextKey)
	for key := range request.Options {
		if scope != "" && !settingsaccess.AllowsOption(scope, key) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
	}
	if err := model.UpdateRequestPolicyOptions(request.Options); err != nil {
		logger.LogError(c, "failed to update request policy: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	GetRequestPolicy(c)
}

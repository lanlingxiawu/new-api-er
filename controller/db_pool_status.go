package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func GetDBPoolRuntimeStatus(c *gin.Context) {
	stats, err := model.GetDBPoolRuntimeStats()
	if err != nil {
		logger.LogError(c, "failed to read database pool runtime status: "+err.Error())
		common.ApiErrorI18n(c, i18n.MsgRetryLater)
		return
	}
	common.ApiSuccess(c, stats)
}

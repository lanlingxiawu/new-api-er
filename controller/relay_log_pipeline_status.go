package controller

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// AdminGetRelayLogPipelineStatus returns an in-memory-only snapshot.
func AdminGetRelayLogPipelineStatus(c *gin.Context) {
	common.ApiSuccess(c, model.GetRelayLogPipelineStatus())
}

// AdminStartRelayLogFallbackReplay starts one background fallback replay task.
// The request returns before any fallback file is scanned or LOG_DB is used.
func AdminStartRelayLogFallbackReplay(c *gin.Context) {
	started, err := model.StartRelayLogFallbackReplay()
	if err == nil {
		if started {
			logger.LogInfo(c, "relay-log: manual fallback replay started")
		}
		common.ApiSuccess(c, gin.H{"started": started})
		return
	}
	switch {
	case errors.Is(err, model.ErrRelayLogReplayRunning):
		common.ApiErrorI18n(c, i18n.MsgBackfillAlreadyRunning)
	case errors.Is(err, model.ErrRelayLogReplayShuttingDown):
		common.ApiErrorI18n(c, i18n.MsgRetryLater)
	default:
		logger.LogError(c, "relay-log: failed to start manual fallback replay")
		common.ApiErrorI18n(c, i18n.MsgRetryLater)
	}
}

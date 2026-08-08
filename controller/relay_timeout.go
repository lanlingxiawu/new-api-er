package controller

import (
	"context"
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

func normalizeRelayTimeoutError(c *gin.Context, current *types.NewAPIError) *types.NewAPIError {
	if !middleware.IsRelayRequestTimeout(c) {
		return current
	}
	if current == nil && c.Writer.Written() {
		return nil
	}
	return relayTimeoutAPIError(c)
}

func relayTimeoutAPIError(c *gin.Context) *types.NewAPIError {
	message := i18n.T(c, i18n.MsgRelayTimeout, map[string]any{
		"Seconds": middleware.RelayRequestTimeoutSeconds(c),
	})
	return types.NewErrorWithStatusCode(
		errors.New(message),
		types.ErrorCodeRelayTimeout,
		http.StatusGatewayTimeout,
		types.ErrOptionWithSkipRetry(),
	)
}

func handleRelayTimeoutResponse(c *gin.Context, isTimeout bool, write func()) bool {
	if !isTimeout {
		return false
	}
	if !c.Writer.Written() {
		middleware.WriteRelayTimeoutResponse(c, write)
	}
	return true
}

func respondMidjourneyTimeout(c *gin.Context) {
	middleware.WriteRelayTimeoutResponse(c, func() {
		c.JSON(http.StatusGatewayTimeout, gin.H{
			"description": i18n.T(c, i18n.MsgRelayTimeout, map[string]any{
				"Seconds": middleware.RelayRequestTimeoutSeconds(c),
			}),
			"type": "upstream_error",
			"code": 4,
		})
	})
}

func shouldStartMidjourneyTimeout(relayMode int) bool {
	switch relayMode {
	case relayconstant.RelayModeMidjourneyNotify,
		relayconstant.RelayModeMidjourneyTaskFetch,
		relayconstant.RelayModeMidjourneyTaskFetchByCondition,
		relayconstant.RelayModeMidjourneyTaskImageSeed:
		return false
	default:
		return true
	}
}

func normalizeRelayTaskTimeout(c *gin.Context, current *dto.TaskError) *dto.TaskError {
	if !middleware.IsRelayRequestTimeout(c) {
		return current
	}
	if current == nil && c.Writer.Written() {
		return nil
	}
	message := i18n.T(c, i18n.MsgRelayTimeout, map[string]any{
		"Seconds": middleware.RelayRequestTimeoutSeconds(c),
	})
	return &dto.TaskError{
		Code:       string(types.ErrorCodeRelayTimeout),
		Message:    message,
		StatusCode: http.StatusGatewayTimeout,
		LocalError: true,
		Error:      context.DeadlineExceeded,
	}
}

func shouldProcessTaskChannelError(taskErr *dto.TaskError) bool {
	return taskErr != nil && (!taskErr.LocalError || taskErr.Code == string(types.ErrorCodeRelayTimeout))
}

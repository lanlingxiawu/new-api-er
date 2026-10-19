package service

import (
	"errors"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"net/http"
)

const BodyCacheCapacityErrorCode types.ErrorCode = "body_cache_capacity"

func NewBodyCacheCapacityError(c *gin.Context) *types.NewAPIError {
	return types.NewErrorWithStatusCode(errors.New(i18n.T(c, i18n.MsgBodyCacheCapacity)), BodyCacheCapacityErrorCode, http.StatusServiceUnavailable, types.ErrOptionWithSkipRetry(), types.ErrOptionWithSafeMessage())
}

package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/gin-gonic/gin"
)

// Interactive log and audit queries default to one day. Older data remains
// accessible through explicit windows of at most thirty days.
func parseLogQueryTimeRange(c *gin.Context) (int64, int64, bool) {
	start, startErr := parseOptionalInt64Query(c, "start_timestamp")
	end, endErr := parseOptionalInt64Query(c, "end_timestamp")
	now := common.GetTimestamp()
	if end == 0 || end > now {
		end = now
	}
	if start == 0 {
		start = max(int64(1), end-86400)
	}
	if startErr != nil || endErr != nil || start > end || end-start > 30*86400 {
		logger.LogWarn(c, "log query rejected: invalid or excessive time range")
		common.ApiErrorI18n(c, i18n.MsgLogQueryTimeRange)
		return 0, 0, false
	}
	return start, end, true
}

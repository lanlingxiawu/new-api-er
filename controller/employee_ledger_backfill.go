package controller

// employee_ledger_backfill.go — admin endpoints for fallback-log backfill.
//
// Routes (all under the existing adminGroup with AdminAuth middleware):
//   GET  /api/admin/employee/consumption-cost-ledger/fallback/status
//   POST /api/admin/employee/consumption-cost-ledger/fallback/backfill
//   GET  /api/admin/employee/consumption-cost-ledger/fallback/backfill-result
//
// Rule 9:  errors use ApiErrorI18n / ApiErrorMsg.
// Rule 10: logger.LogInfo(c, …) / logger.LogError(c, …) for request-scoped logs.

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// AdminGetFallbackStatus returns the fallback file status for a historical date.
//
// GET /api/admin/employee/consumption-cost-ledger/fallback/status?date=2026-06-25
func AdminGetFallbackStatus(c *gin.Context) {
	cfg := operation_setting.GetBusinessStatsFallbackBackfillSetting()
	if !cfg.Enabled {
		common.ApiSuccess(c, service.FallbackFileStatus{HasFile: false, CanBackfill: false})
		return
	}

	date := strings.TrimSpace(c.Query("date"))
	if !isValidDateParam(date) {
		common.ApiErrorI18n(c, i18n.MsgBackfillDateMustBeHistorical)
		return
	}
	if !service.IsSingleHistoricalDate(date) {
		common.ApiErrorI18n(c, i18n.MsgBackfillDateMustBeHistorical)
		return
	}

	status := service.GetFallbackFileStatus(date)
	logger.LogInfo(c, "fallback_status: date="+date+" has_file="+boolStr(status.HasFile))
	common.ApiSuccess(c, status)
}

// AdminTriggerBackfill starts the backfill task for a historical date.
//
// POST /api/admin/employee/consumption-cost-ledger/fallback/backfill
// Body: {"date":"2026-06-25"}
func AdminTriggerBackfill(c *gin.Context) {
	var req struct {
		Date string `json:"date"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	req.Date = strings.TrimSpace(req.Date)

	err := service.TriggerBackfill(req.Date)
	if err == nil {
		logger.LogInfo(c, "backfill triggered: date="+req.Date)
		common.ApiSuccess(c, gin.H{"date": req.Date})
		return
	}

	switch {
	case service.IsErrBackfillDisabled(err):
		common.ApiErrorI18n(c, i18n.MsgBackfillDisabled)
	case service.IsErrBackfillDateMustBeHistorical(err):
		common.ApiErrorI18n(c, i18n.MsgBackfillDateMustBeHistorical)
	case service.IsErrBackfillAlreadyRunning(err):
		common.ApiErrorI18n(c, i18n.MsgBackfillAlreadyRunning)
	case service.IsErrBackfillFileNotFound(err):
		common.ApiErrorI18n(c, i18n.MsgBackfillFileNotFound)
	default:
		logger.LogError(c, "backfill trigger error: "+err.Error())
		common.ApiErrorMsg(c, "Internal error, please check system logs")
	}
}

// AdminGetBackfillResult returns the most recent backfill task result.
//
// GET /api/admin/employee/consumption-cost-ledger/fallback/backfill-result
func AdminGetBackfillResult(c *gin.Context) {
	result := service.GetBackfillResult()
	common.ApiSuccess(c, result)
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// isValidDateParam checks that date is in "2006-01-02" format (length + digits).
func isValidDateParam(date string) bool {
	if len(date) != 10 {
		return false
	}
	// Quick format check without importing time (full parse happens in service).
	for i, ch := range date {
		if i == 4 || i == 7 {
			if ch != '-' {
				return false
			}
		} else {
			if ch < '0' || ch > '9' {
				return false
			}
		}
	}
	return true
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

package controller

import (
	"fmt"
	"strconv"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

var businessStatsBackfillRunning int32

func AdminBackfillBusinessStats(c *gin.Context) {
	timeRange, err := parseUnixTimeRangeQuery(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	batchDays := 0
	if raw := c.Query("batch_days"); raw != "" {
		batchDays, _ = strconv.Atoi(raw)
	}
	if batchDays > 31 {
		batchDays = 31
	}
	if !atomic.CompareAndSwapInt32(&businessStatsBackfillRunning, 0, 1) {
		common.ApiErrorMsg(c, "business stats backfill already running")
		return
	}
	lockOwner, locked := model.AcquireBusinessStatsBackfillLock()
	if !locked {
		atomic.StoreInt32(&businessStatsBackfillRunning, 0)
		common.ApiErrorMsg(c, "business stats backfill already running")
		return
	}
	if !model.NeedsBusinessStatsBackfill() {
		atomic.StoreInt32(&businessStatsBackfillRunning, 0)
		model.ReleaseBusinessStatsBackfillLock(lockOwner)
		common.ApiSuccess(c, gin.H{"started": false, "completed": true})
		return
	}

	startTime := timeRange.StartTime
	endTime := timeRange.EndTime
	go func() {
		defer atomic.StoreInt32(&businessStatsBackfillRunning, 0)
		defer model.ReleaseBusinessStatsBackfillLock(lockOwner)
		model.FlushBusinessStatBuffers()
		result, err := model.BackfillBusinessDailyStats(startTime, endTime, batchDays)
		if err != nil {
			common.SysError("business_stats_backfill: failed: " + err.Error())
			return
		}
		common.SysLog(fmt.Sprintf(
			"business_stats_backfill: completed start_date=%d end_date=%d days=%d batch_days=%d sleep_ms=%d cost_records=%d commission_records=%d platform_rows=%d employee_rows=%d customer_rows=%d monthly_rows=%d",
			result.StartDate,
			result.EndDate,
			result.Days,
			result.BatchDays,
			result.SleepMs,
			result.CostRecords,
			result.CommissionRecords,
			result.PlatformRows,
			result.EmployeeRows,
			result.CustomerRows,
			result.MonthlyRows,
		))
		// 记录历史回填已追平；NeedsBusinessStatsBackfill 仍会按完整日期校验新增缺口。
		if !model.NeedsBusinessStatsBackfill() {
			_ = model.UpdateOption("BusinessStatsBackfillCompleted", "true")
		}
	}()

	common.ApiSuccess(c, gin.H{"started": true, "batch_days": batchDays})
}

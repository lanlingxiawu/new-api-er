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
<<<<<<< Updated upstream
	if !model.NeedsBusinessStatsBackfill() {
		atomic.StoreInt32(&businessStatsBackfillRunning, 0)
=======
	lockOwner, locked := model.AcquireBusinessStatsBackfillLock()
	if !locked {
		atomic.StoreInt32(&businessStatsBackfillRunning, 0)
		common.ApiErrorMsg(c, "business stats backfill already running")
		return
	}
	if !model.NeedsBusinessStatsBackfill() {
		atomic.StoreInt32(&businessStatsBackfillRunning, 0)
		model.ReleaseBusinessStatsBackfillLock(lockOwner)
>>>>>>> Stashed changes
		common.ApiSuccess(c, gin.H{"started": false, "completed": true})
		return
	}

	startTime := timeRange.StartTime
	endTime := timeRange.EndTime
	go func() {
		defer atomic.StoreInt32(&businessStatsBackfillRunning, 0)
<<<<<<< Updated upstream
=======
		defer model.ReleaseBusinessStatsBackfillLock(lockOwner)
>>>>>>> Stashed changes
		model.FlushBusinessStatBuffers()
		result, err := model.BackfillBusinessDailyStats(startTime, endTime, batchDays)
		if err != nil {
			common.SysError("business_stats_backfill: failed: " + err.Error())
			return
		}
		common.SysLog(fmt.Sprintf(
<<<<<<< Updated upstream
			"business_stats_backfill: completed start_date=%d end_date=%d days=%d batch_days=%d sleep_ms=%d cost_records=%d commission_records=%d platform_rows=%d employee_rows=%d",
=======
			"business_stats_backfill: completed start_date=%d end_date=%d days=%d batch_days=%d sleep_ms=%d cost_records=%d commission_records=%d platform_rows=%d employee_rows=%d customer_rows=%d",
>>>>>>> Stashed changes
			result.StartDate,
			result.EndDate,
			result.Days,
			result.BatchDays,
			result.SleepMs,
			result.CostRecords,
			result.CommissionRecords,
			result.PlatformRows,
			result.EmployeeRows,
<<<<<<< Updated upstream
=======
			result.CustomerRows,
>>>>>>> Stashed changes
		))
		// 回填完成后标记，后续 NeedsBusinessStatsBackfill 直接返回 false
		if !model.NeedsBusinessStatsBackfill() {
			_ = model.UpdateOption("BusinessStatsBackfillCompleted", "true")
		}
	}()

	common.ApiSuccess(c, gin.H{"started": true, "batch_days": batchDays})
}

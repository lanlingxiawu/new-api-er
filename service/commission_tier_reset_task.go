package service

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	tierResetTickInterval = 1 * time.Minute
	tierResetBatchSize    = 300
)

var (
	tierResetOnce    sync.Once
	tierResetRunning atomic.Bool
)

// StartCommissionTierResetTask 启动提成等级与"本期业绩/本期提成"月度自动重置后台任务。
func StartCommissionTierResetTask() {
	tierResetOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			logger.LogInfo(context.Background(), fmt.Sprintf("commission tier reset task started: tick=%s", tierResetTickInterval))
			ticker := time.NewTicker(tierResetTickInterval)
			defer ticker.Stop()

			runCommissionTierResetCheck()
			for range ticker.C {
				runCommissionTierResetCheck()
			}
		})
	})
}

// daysInMonth 返回指定年月的天数。
func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func tierResetLocation(cfg *operation_setting.CommissionTierResetSetting) *time.Location {
	if cfg == nil || cfg.Timezone == "" {
		return nil
	}
	if cfg.Timezone == "Local" {
		return time.Local
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		common.SysError("invalid commission tier reset timezone, fallback to local: " + cfg.Timezone)
		return time.Local
	}
	return loc
}

// scheduledTimeInMonth 计算指定年月的调度时刻：cfg.ResetDay 日 cfg.ResetHour:ResetMinute:ResetSecond；
// 若当月天数小于 ResetDay，自动取该月最后一天。
func scheduledTimeInMonth(year int, month time.Month, cfg *operation_setting.CommissionTierResetSetting, loc *time.Location) time.Time {
	day := cfg.ResetDay
	if maxDay := daysInMonth(year, month); day > maxDay {
		day = maxDay
	}
	if day < 1 {
		day = 1
	}
	return time.Date(year, month, day, cfg.ResetHour, cfg.ResetMinute, cfg.ResetSecond, 0, loc)
}

// lastScheduledTimeAtOrBefore 计算在 now 时刻之前（含等于）最近一次的调度时刻（unix 秒）。
func lastScheduledTimeAtOrBefore(now time.Time, cfg *operation_setting.CommissionTierResetSetting) int64 {
	if loc := tierResetLocation(cfg); loc != nil {
		now = now.In(loc)
	}
	candidate := scheduledTimeInMonth(now.Year(), now.Month(), cfg, now.Location())
	if candidate.After(now) {
		year, month := now.Year(), now.Month()
		if month == time.January {
			year--
			month = time.December
		} else {
			month--
		}
		candidate = scheduledTimeInMonth(year, month, cfg, now.Location())
	}
	return candidate.Unix()
}

// NextScheduledResetAt 计算严格晚于 now 的下一次调度时刻（unix 秒），仅供前端展示，不持久化。
func NextScheduledResetAt(now time.Time) int64 {
	cfg := operation_setting.GetCommissionTierResetSetting()
	if loc := tierResetLocation(cfg); loc != nil {
		now = now.In(loc)
	}
	candidate := scheduledTimeInMonth(now.Year(), now.Month(), cfg, now.Location())
	if !candidate.After(now) {
		year, month := now.Year(), now.Month()
		if month == time.December {
			year++
			month = time.January
		} else {
			month++
		}
		candidate = scheduledTimeInMonth(year, month, cfg, now.Location())
	}
	return candidate.Unix()
}

func runCommissionTierResetCheck() {
	cfg := operation_setting.GetCommissionTierResetSetting()
	if !cfg.Enabled {
		return
	}

	due := lastScheduledTimeAtOrBefore(time.Now(), cfg)

	if cfg.LastResetAt == 0 {
		// 首次启用：armed，不补跑已经过去的调度点；下一次真正触发是下个调度周期。
		persistTierResetLastResetAt(due)
		return
	}
	if due <= cfg.LastResetAt {
		return
	}

	if !tierResetRunning.CompareAndSwap(false, true) {
		return
	}
	defer tierResetRunning.Store(false)

	ctx := context.Background()
	model.FlushBusinessStatBuffers()
	total := 0
	for {
		n, err := model.ResetEmployeeTierLevelsForPeriod(due, tierResetBatchSize, 0)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("commission tier reset task failed: %v", err))
			return
		}
		if n == 0 {
			break
		}
		total += n
		if n < tierResetBatchSize {
			break
		}
	}
	persistTierResetLastResetAt(due)
	if common.DebugEnabled {
		logger.LogDebug(ctx, fmt.Sprintf("commission tier reset: due=%d, processed=%d", due, total))
	}
}

// RunCommissionTierResetNow 立即执行一次提成等级/本期业绩与提成重置。
// resetAt 通常为 time.Now().Unix()；operatedBy 为触发该操作的管理员 user id。
// 成功后会更新 commission_tier_reset_setting.last_reset_at，避免被定时任务重复触发。
func RunCommissionTierResetNow(resetAt int64, operatedBy int) (processed int, err error) {
	if !tierResetRunning.CompareAndSwap(false, true) {
		return 0, fmt.Errorf("commission tier reset is already running")
	}
	defer tierResetRunning.Store(false)

	model.FlushBusinessStatBuffers()
	for {
		n, err := model.ResetEmployeeTierLevelsForPeriod(resetAt, tierResetBatchSize, operatedBy)
		if err != nil {
			return processed, err
		}
		if n == 0 {
			break
		}
		processed += n
		if n < tierResetBatchSize {
			break
		}
	}
	persistTierResetLastResetAt(resetAt)
	return processed, nil
}

// persistTierResetLastResetAt 持久化 commission_tier_reset_setting.last_reset_at
// 并同步更新内存配置（model.UpdateOption 内部会经由 config.UpdateConfigFromMap 完成）。
func persistTierResetLastResetAt(at int64) {
	if err := model.UpdateOption("commission_tier_reset_setting.last_reset_at", strconv.FormatInt(at, 10)); err != nil {
		common.SysError("failed to persist commission_tier_reset_setting.last_reset_at: " + err.Error())
	}
}

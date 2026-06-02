package service

// monitor_service.go — Periodic aggregation of group/model health metrics.
//
// Data source: LOG_DB logs table (type=2 for consume, type=5 for error).
// Two tasks are registered with the TaskScheduler:
//   - MonitorStatusAggregation  (hourly, master-only): computes per-group and
//     per-model metrics from the last hour of logs, writes to *_statuses and
//     *_status_histories tables, then prunes old history rows.
//   - MonitorAlertGeneration (every 5 min, master-only): inspects current
//     group_statuses and model_statuses and creates alert_notifications for
//     any entries that exceed health thresholds (with a 6-hour dedup window).

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm/clause"
)

// ─── thresholds ──────────────────────────────────────────────────────────────

const (
	statusHealthy     = 1
	statusWarning     = 2
	statusUnhealthy   = 3
	statusUnavailable = 4

	// Success-rate thresholds (percent).
	thresholdWarning   = 95.0
	thresholdUnhealthy = 90.0

	// P95 response-time thresholds (ms).
	p95ThresholdWarning = 3000
	p95ThresholdHealthy = 2000

	// Alert dedup window — don't re-alert for the same target within 6 hours.
	alertDedupWindowSeconds = 6 * 3600

	// Keep history for 90 days.
	historyRetentionDays = 90

	// Minimum log count before computing meaningful metrics.
	minSampleSize = 5
)

// ─── row type from log query ──────────────────────────────────────────────────

type logMetricRow struct {
	GroupName    string
	ModelName    string
	TotalCount   int
	SuccessCount int
	SumUseTime   int64
}

// ─── exported task handlers ───────────────────────────────────────────────────

// RunStatusAggregation is the handler for the MonitorStatusAggregation task.
func RunStatusAggregation(ctx context.Context) error {
	now := time.Now()
	hourStart := (now.Unix() / 3600) * 3600
	windowStart := now.Unix() - 3600 // last 1 hour

	var errs []error
	if err := aggregateGroupStatuses(ctx, windowStart, hourStart); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("[monitor] group aggregation error: %v", err))
		errs = append(errs, fmt.Errorf("group aggregation: %w", err))
	}
	if err := aggregateModelStatuses(ctx, windowStart, hourStart); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("[monitor] model aggregation error: %v", err))
		errs = append(errs, fmt.Errorf("model aggregation: %w", err))
	}
	pruneOldHistory(ctx)
	return errors.Join(errs...)
}

// RunAlertGeneration is the handler for the MonitorAlertGeneration task.
func RunAlertGeneration(ctx context.Context) error {
	var errs []error
	if err := checkGroupAlerts(ctx); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("[monitor] group alert check error: %v", err))
		errs = append(errs, fmt.Errorf("group alert check: %w", err))
	}
	if err := checkModelAlerts(ctx); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("[monitor] model alert check error: %v", err))
		errs = append(errs, fmt.Errorf("model alert check: %w", err))
	}
	return errors.Join(errs...)
}

// UpdateGroupModelStatusFromChannelTest is called after each channel auto-test
// run to refresh group/model statuses based on the most recent 24 h of logs.
// This runs on every node (not just master) so is NOT protected by a lock.
func UpdateGroupModelStatusFromChannelTest(ctx context.Context) error {
	windowStart := time.Now().Unix() - 24*3600
	hourStart := (time.Now().Unix() / 3600) * 3600

	if err := aggregateGroupStatuses(ctx, windowStart, hourStart); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("[monitor] realtime group update error: %v", err))
		return err
	}
	if err := aggregateModelStatuses(ctx, windowStart, hourStart); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("[monitor] realtime model update error: %v", err))
		return err
	}
	return nil
}

// ─── group aggregation ────────────────────────────────────────────────────────

func aggregateGroupStatuses(ctx context.Context, windowStart, hourStart int64) error {
	rows, responseTimes, err := queryGroupMetrics(windowStart)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}

	// Count available/disabled channels per group.
	channelCounts, err := countChannelsPerGroup()
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("[monitor] channel count error: %v", err))
	}

	now := time.Now().Unix()

	for _, row := range rows {
		if row.TotalCount < minSampleSize {
			continue
		}

		rt := responseTimes[row.GroupName]
		sort.Ints(rt)

		successRate := float64(row.SuccessCount) / float64(row.TotalCount) * 100
		avgRT := 0
		if row.SuccessCount > 0 {
			avgRT = int(row.SumUseTime * 1000 / int64(row.SuccessCount))
		}
		p95 := percentileInt(rt, 95)
		status := computeStatus(successRate, p95)
		errCount := row.TotalCount - row.SuccessCount

		cc := channelCounts[row.GroupName]

		gs := &model.GroupStatus{
			UserGroup:         row.GroupName,
			Status:            status,
			LastTestTime:      now,
			SuccessRate:       successRate,
			AvgResponseTime:   avgRT,
			P95ResponseTime:   p95,
			ErrorCount:        errCount,
			TotalRequests:     row.TotalCount,
			TotalChannels:     cc.total,
			AvailableChannels: cc.available,
			DisabledChannels:  cc.disabled,
			UpdatedAt:         now,
		}

		// Load existing to preserve CreatedAt / ID.
		existing := findOrNewGroupStatus(row.GroupName, now)
		gs.ID = existing.ID
		gs.CreatedAt = existing.CreatedAt

		if err := model.UpsertGroupStatus(gs); err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("[monitor] upsert group_status %s: %v", row.GroupName, err))
			continue
		}

		// Write hourly history snapshot.
		hist := &model.GroupStatusHistory{
			UserGroup:         row.GroupName,
			SnapshotHour:      hourStart,
			SuccessRate:       successRate,
			AvgResponseTime:   avgRT,
			AvailableChannels: cc.available,
			ErrorCount:        errCount,
			TotalRequests:     row.TotalCount,
			CreatedAt:         now,
		}
		// Only one snapshot per hour per group.
		var existingHist int64
		model.DB.Model(&model.GroupStatusHistory{}).
			Where("user_group = ? AND snapshot_hour = ?", row.GroupName, hourStart).
			Count(&existingHist)
		if existingHist == 0 {
			model.DB.Create(hist)
		}
	}
	return nil
}

// ─── model aggregation ───────────────────────────────────────────────────────

func aggregateModelStatuses(ctx context.Context, windowStart, hourStart int64) error {
	rows, responseTimes, err := queryModelMetrics(windowStart)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}

	channelCounts, err := countChannelsPerModel()
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("[monitor] model channel count error: %v", err))
	}

	now := time.Now().Unix()

	for _, row := range rows {
		if row.TotalCount < minSampleSize {
			continue
		}

		rt := responseTimes[row.ModelName]
		sort.Ints(rt)

		successRate := float64(row.SuccessCount) / float64(row.TotalCount) * 100
		avgRT := 0
		if row.SuccessCount > 0 {
			avgRT = int(row.SumUseTime * 1000 / int64(row.SuccessCount))
		}
		p95 := percentileInt(rt, 95)
		status := computeStatus(successRate, p95)
		errCount := row.TotalCount - row.SuccessCount

		cc := channelCounts[row.ModelName]

		ms := &model.ModelStatus{
			ModelName:         row.ModelName,
			Status:            status,
			LastTestTime:      now,
			SuccessRate:       successRate,
			AvgResponseTime:   avgRT,
			P95ResponseTime:   p95,
			ErrorCount:        errCount,
			TotalRequests:     row.TotalCount,
			TotalChannels:     cc.total,
			AvailableChannels: cc.available,
			UpdatedAt:         now,
		}

		existing := findOrNewModelStatus(row.ModelName, now)
		ms.ID = existing.ID
		ms.CreatedAt = existing.CreatedAt

		if err := model.UpsertModelStatus(ms); err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("[monitor] upsert model_status %s: %v", row.ModelName, err))
			continue
		}

		hist := &model.ModelStatusHistory{
			ModelName:       row.ModelName,
			SnapshotHour:    hourStart,
			SuccessRate:     successRate,
			AvgResponseTime: avgRT,
			ErrorCount:      errCount,
			TotalRequests:   row.TotalCount,
			CreatedAt:       now,
		}
		var existingHist int64
		model.DB.Model(&model.ModelStatusHistory{}).
			Where("model_name = ? AND snapshot_hour = ?", row.ModelName, hourStart).
			Count(&existingHist)
		if existingHist == 0 {
			model.DB.Create(hist)
		}
	}
	return nil
}

// ─── alert generation ────────────────────────────────────────────────────────

func checkGroupAlerts(ctx context.Context) error {
	groups, err := model.GetAllGroupStatuses()
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, g := range groups {
		if g.Status <= statusHealthy {
			continue
		}
		has, _ := model.HasRecentAlert(g.UserGroup, "group_unhealthy", alertDedupWindowSeconds)
		if has {
			continue
		}
		severity := "warning"
		if g.Status >= statusUnavailable {
			severity = "critical"
		}
		alert := &model.AlertNotification{
			UserID:     0,
			TargetType: "group",
			TargetName: g.UserGroup,
			AlertType:  "group_unhealthy",
			Severity:   severity,
			Title:      fmt.Sprintf("分组 '%s' 健康度下降", g.UserGroup),
			Message: fmt.Sprintf(
				"成功率 %.1f%% (阈值: %.0f%%), P95响应时间 %dms, 可用渠道 %d/%d",
				g.SuccessRate, thresholdWarning, g.P95ResponseTime,
				g.AvailableChannels, g.TotalChannels,
			),
			Status:    "unread",
			CreatedAt: now,
		}
		if err := model.DB.Create(alert).Error; err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("[monitor] create group alert for %s: %v", g.UserGroup, err))
		}
	}
	return nil
}

func checkModelAlerts(ctx context.Context) error {
	models, err := model.GetAllModelStatuses()
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, m := range models {
		if m.Status <= statusHealthy {
			continue
		}
		has, _ := model.HasRecentAlert(m.ModelName, "model_unhealthy", alertDedupWindowSeconds)
		if has {
			continue
		}
		severity := "warning"
		if m.Status >= statusUnavailable {
			severity = "critical"
		}
		alert := &model.AlertNotification{
			UserID:     0,
			TargetType: "model",
			TargetName: m.ModelName,
			AlertType:  "model_unhealthy",
			Severity:   severity,
			Title:      fmt.Sprintf("模型 '%s' 健康度下降", m.ModelName),
			Message: fmt.Sprintf(
				"成功率 %.1f%%, P95响应时间 %dms, 可用渠道 %d/%d",
				m.SuccessRate, m.P95ResponseTime, m.AvailableChannels, m.TotalChannels,
			),
			Status:    "unread",
			CreatedAt: now,
		}
		if err := model.DB.Create(alert).Error; err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("[monitor] create model alert for %s: %v", m.ModelName, err))
		}
	}
	return nil
}

// ─── DB query helpers ─────────────────────────────────────────────────────────

// queryGroupMetrics reads per-group success/error counts and per-row response
// times from LOG_DB for the given time window.
func queryGroupMetrics(windowStart int64) ([]logMetricRow, map[string][]int, error) {
	type aggRow struct {
		GroupName    string `gorm:"column:group_name"`
		TotalCount   int    `gorm:"column:total_count"`
		SuccessCount int    `gorm:"column:success_count"`
		SumUseTime   int64  `gorm:"column:sum_use_time"`
	}

	var agg []aggRow

	selectGroupCol := "`group`"
	if common.UsingPostgreSQL {
		selectGroupCol = `"group"`
	}

	err := model.LOG_DB.Table("logs").
		Select(fmt.Sprintf(
			"%s as group_name, COUNT(*) as total_count, "+
				"SUM(CASE WHEN type = 2 THEN 1 ELSE 0 END) as success_count, "+
				"SUM(CASE WHEN type = 2 THEN use_time ELSE 0 END) as sum_use_time",
			selectGroupCol,
		)).
		Where("created_at >= ? AND type IN (2, 5)", windowStart).
		Clauses(clause.GroupBy{
			Columns: []clause.Column{{Name: selectGroupCol, Raw: true}},
			Having:  []clause.Expression{clause.Expr{SQL: "COUNT(*) >= ?", Vars: []interface{}{minSampleSize}}},
		}).
		Scan(&agg).Error
	if err != nil {
		return nil, nil, err
	}

	// Fetch raw response times for percentile computation.
	type rtRow struct {
		GroupName string `gorm:"column:group_name"`
		UseTime   int    `gorm:"column:use_time"`
	}
	var rtRows []rtRow
	err = model.LOG_DB.Table("logs").
		Select(fmt.Sprintf("%s as group_name, use_time", selectGroupCol)).
		Where("created_at >= ? AND type = 2", windowStart).
		Scan(&rtRows).Error
	if err != nil {
		return nil, nil, err
	}

	rtMap := make(map[string][]int, len(agg))
	for _, r := range rtRows {
		rtMap[r.GroupName] = append(rtMap[r.GroupName], r.UseTime*1000)
	}

	rows := make([]logMetricRow, len(agg))
	for i, a := range agg {
		rows[i] = logMetricRow{
			GroupName:    a.GroupName,
			TotalCount:   a.TotalCount,
			SuccessCount: a.SuccessCount,
			SumUseTime:   a.SumUseTime,
		}
	}
	return rows, rtMap, nil
}

func queryModelMetrics(windowStart int64) ([]logMetricRow, map[string][]int, error) {
	type aggRow struct {
		ModelName    string `gorm:"column:model_name"`
		TotalCount   int    `gorm:"column:total_count"`
		SuccessCount int    `gorm:"column:success_count"`
		SumUseTime   int64  `gorm:"column:sum_use_time"`
	}

	var agg []aggRow
	err := model.LOG_DB.Table("logs").
		Select(
			"model_name, COUNT(*) as total_count, "+
				"SUM(CASE WHEN type = 2 THEN 1 ELSE 0 END) as success_count, "+
				"SUM(CASE WHEN type = 2 THEN use_time ELSE 0 END) as sum_use_time",
		).
		Where("created_at >= ? AND type IN (2, 5)", windowStart).
		Group("model_name").
		Having("COUNT(*) >= ?", minSampleSize).
		Scan(&agg).Error
	if err != nil {
		return nil, nil, err
	}

	type rtRow struct {
		ModelName string `gorm:"column:model_name"`
		UseTime   int    `gorm:"column:use_time"`
	}
	var rtRows []rtRow
	err = model.LOG_DB.Table("logs").
		Select("model_name, use_time").
		Where("created_at >= ? AND type = 2", windowStart).
		Scan(&rtRows).Error
	if err != nil {
		return nil, nil, err
	}

	rtMap := make(map[string][]int, len(agg))
	for _, r := range rtRows {
		rtMap[r.ModelName] = append(rtMap[r.ModelName], r.UseTime*1000)
	}

	rows := make([]logMetricRow, len(agg))
	for i, a := range agg {
		rows[i] = logMetricRow{
			ModelName:    a.ModelName,
			TotalCount:   a.TotalCount,
			SuccessCount: a.SuccessCount,
			SumUseTime:   a.SumUseTime,
		}
	}
	return rows, rtMap, nil
}

// ─── channel counts ───────────────────────────────────────────────────────────

type channelCount struct {
	total     int
	available int
	disabled  int
}

func countChannelsPerGroup() (map[string]channelCount, error) {
	// Channel.Group is a comma-separated list of group names, so we fetch
	// all rows and expand them in Go rather than relying on SQL GROUP BY.
	type row struct {
		Group  string `gorm:"column:group_col"`
		Status int    `gorm:"column:status"`
	}

	groupCol := "`group`"
	if common.UsingPostgreSQL {
		groupCol = `"group"`
	}

	var rows []row
	err := model.DB.Table("channels").
		Select(fmt.Sprintf("%s as group_col, status", groupCol)).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	result := make(map[string]channelCount)
	for _, r := range rows {
		// Expand comma-separated group names.
		for _, g := range strings.Split(strings.Trim(r.Group, ","), ",") {
			g = strings.TrimSpace(g)
			if g == "" {
				continue
			}
			cc := result[g]
			cc.total++
			if r.Status == 1 {
				cc.available++
			} else {
				cc.disabled++
			}
			result[g] = cc
		}
	}
	return result, nil
}

func countChannelsPerModel() (map[string]channelCount, error) {
	type row struct {
		Models string `gorm:"column:models"`
		Status int    `gorm:"column:status"`
	}
	var rows []row
	err := model.DB.Table("channels").Select("models, status").Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	result := make(map[string]channelCount)
	for _, r := range rows {
		names := strings.Split(strings.Trim(r.Models, ","), ",")
		for _, name := range names {
			name = strings.TrimSpace(name)
			cc := result[name]
			cc.total++
			if r.Status == 1 {
				cc.available++
			} else {
				cc.disabled++
			}
			result[name] = cc
		}
	}
	return result, nil
}

// ─── housekeeping ─────────────────────────────────────────────────────────────

func pruneOldHistory(ctx context.Context) {
	cutoff := time.Now().Unix() - historyRetentionDays*24*3600
	if err := model.DB.Where("created_at < ?", cutoff).Delete(&model.GroupStatusHistory{}).Error; err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("[monitor] prune group history: %v", err))
	}
	if err := model.DB.Where("created_at < ?", cutoff).Delete(&model.ModelStatusHistory{}).Error; err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("[monitor] prune model history: %v", err))
	}
}

// ─── find-or-new helpers to preserve ID/CreatedAt on upsert ─────────────────

func findOrNewGroupStatus(userGroup string, now int64) model.GroupStatus {
	var existing model.GroupStatus
	model.DB.Where("user_group = ?", userGroup).First(&existing)
	if existing.CreatedAt == 0 {
		existing.CreatedAt = now
	}
	return existing
}

func findOrNewModelStatus(modelName string, now int64) model.ModelStatus {
	var existing model.ModelStatus
	model.DB.Where("model_name = ?", modelName).First(&existing)
	if existing.CreatedAt == 0 {
		existing.CreatedAt = now
	}
	return existing
}

// ─── math helpers ─────────────────────────────────────────────────────────────

func percentileInt(sorted []int, p int) int {
	if len(sorted) == 0 {
		return 0
	}
	idx := len(sorted) * p / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func computeStatus(successRate float64, p95ms int) int {
	if successRate >= 99 && p95ms < p95ThresholdHealthy {
		return statusHealthy
	}
	if successRate >= thresholdWarning && p95ms < p95ThresholdWarning {
		return statusWarning
	}
	if successRate >= thresholdUnhealthy {
		return statusUnhealthy
	}
	return statusUnavailable
}

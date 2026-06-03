package service

import (
	"context"

	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
)

// SeedSchedulerConfigs inserts default rows into scheduler_configs for each
// known task. Safe to call multiple times (FirstOrCreate semantics).
func SeedSchedulerConfigs() {
	defaults := []model.SchedulerConfig{
		// 新的简化监控任务：定时测试分组内模型的可通性
		{
			TaskName:        "GroupModelAvailabilityTest",
			Enabled:         1,
			IntervalSeconds: 1800,  // 每 30 分钟执行一次
			TimeoutSeconds:  300,   // 5 分钟超时
			RequiresMaster:  1,
		},
	}

	for i := range defaults {
		if err := model.UpsertSchedulerConfig(&defaults[i]); err != nil {
			logger.LogWarn(context.Background(),
				"[scheduler] failed to seed config for "+defaults[i].TaskName+": "+err.Error())
		}
	}

	// 删除旧的复杂监控任务（如果存在）
	oldTaskNames := []string{
		"MonitorStatusAggregation",
		"MonitorAlertGeneration",
	}
	for _, taskName := range oldTaskNames {
		if err := model.DB.Where("task_name = ?", taskName).Delete(&model.SchedulerConfig{}).Error; err != nil {
			logger.LogWarn(context.Background(),
				"[scheduler] failed to delete old task "+taskName+": "+err.Error())
		}
	}
}

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
		{
			TaskName:        "MonitorStatusAggregation",
			Enabled:         1,
			IntervalSeconds: 3600,  // every hour
			TimeoutSeconds:  300,   // 5-minute lock TTL
			RequiresMaster:  1,
		},
		{
			TaskName:        "MonitorAlertGeneration",
			Enabled:         1,
			IntervalSeconds: 300,   // every 5 minutes
			TimeoutSeconds:  120,
			RequiresMaster:  1,
		},
	}

	for i := range defaults {
		if err := model.UpsertSchedulerConfig(&defaults[i]); err != nil {
			logger.LogWarn(context.Background(),
				"[scheduler] failed to seed config for "+defaults[i].TaskName+": "+err.Error())
		}
	}
}

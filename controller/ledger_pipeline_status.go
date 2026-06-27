package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

func AdminGetLedgerPipelineStatus(c *gin.Context) {
	cfg := operation_setting.GetLedgerPipelineSetting()
	snapshot := model.GetLedgerPipelineStatusSnapshot()

	flushIntervalSec := cfg.FlushIntervalSec
	if flushIntervalSec <= 0 {
		flushIntervalSec = model.DefaultBusinessStatsFlushInterval
	}

	theoreticalPairRecordsPerSec := 0.0
	theoreticalPairRecordsPerMin := 0.0
	if flushIntervalSec > 0 {
		theoreticalPairRecordsPerSec = float64(cfg.PairedFlushMaxPerCycle) / float64(flushIntervalSec)
		theoreticalPairRecordsPerMin = theoreticalPairRecordsPerSec * 60
	}

	common.ApiSuccess(c, gin.H{
		"cost_backlog":                     snapshot.Cost.Backlog,
		"pair_backlog":                     snapshot.Pair.Backlog,
		"commission_backlog":               snapshot.Commission.Backlog,
		"cost_dropped_total":               snapshot.Cost.Dropped,
		"pair_dropped_total":               snapshot.Pair.Dropped,
		"commission_dropped_total":         snapshot.Commission.Dropped,
		"last_cost_flush_items":            snapshot.Cost.LastFlushItems,
		"last_pair_flush_items":            snapshot.Pair.LastFlushItems,
		"last_commission_flush_items":      snapshot.Commission.LastFlushItems,
		"last_cost_flush_took_ms":          snapshot.Cost.LastFlushTookMs,
		"last_pair_flush_took_ms":          snapshot.Pair.LastFlushTookMs,
		"last_commission_flush_took_ms":    snapshot.Commission.LastFlushTookMs,
		"last_cost_flush_at":               snapshot.Cost.LastFlushAt,
		"last_pair_flush_at":               snapshot.Pair.LastFlushAt,
		"last_commission_flush_at":         snapshot.Commission.LastFlushAt,
		"flush_interval_sec":               flushIntervalSec,
		"paired_flush_max_per_cycle":       cfg.PairedFlushMaxPerCycle,
		"full_drain":                       cfg.FullDrain,
		"theoretical_pair_records_per_sec": theoreticalPairRecordsPerSec,
		"theoretical_pair_rpm":             theoreticalPairRecordsPerMin,
	})
}

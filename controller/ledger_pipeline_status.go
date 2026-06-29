package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

func AdminGetLedgerPipelineStatus(c *gin.Context) {
	cfg := operation_setting.GetLedgerPipelineSetting()
	retryCfg := operation_setting.GetLedgerRetryQueueSetting()
	snapshot := model.GetLedgerPipelineStatusSnapshot()

	flushIntervalSec := cfg.FlushIntervalSec
	if flushIntervalSec <= 0 {
		flushIntervalSec = model.DefaultBusinessStatsFlushInterval
	}

	theoreticalPairRecordsPerSec := 0.0
	theoreticalPairRecordsPerMin := 0.0
	if flushIntervalSec > 0 {
		theoreticalPairRecordsPerSec = float64(cfg.SettlementFlushMaxPerCycle) / float64(flushIntervalSec)
		theoreticalPairRecordsPerMin = theoreticalPairRecordsPerSec * 60
	}

	common.ApiSuccess(c, gin.H{
		// Flat fields — consumed by Classic UI
		"cost_backlog":                  snapshot.Cost.Backlog,
		"pair_backlog":                  snapshot.Pair.Backlog,
		"commission_backlog":            snapshot.Commission.Backlog,
		"cost_retry_backlog":            snapshot.CostRetry.Backlog,
		"pair_retry_backlog":            snapshot.PairRetry.Backlog,
		"cost_dropped_total":            snapshot.Cost.Dropped,
		"pair_dropped_total":            snapshot.Pair.Dropped,
		"commission_dropped_total":      snapshot.Commission.Dropped,
		"last_cost_flush_items":         snapshot.Cost.LastFlushItems,
		"last_pair_flush_items":         snapshot.Pair.LastFlushItems,
		"last_commission_flush_items":   snapshot.Commission.LastFlushItems,
		"last_cost_retry_flush_items":   snapshot.CostRetry.LastFlushItems,
		"last_pair_retry_flush_items":   snapshot.PairRetry.LastFlushItems,
		"last_cost_flush_took_ms":       snapshot.Cost.LastFlushTookMs,
		"last_pair_flush_took_ms":       snapshot.Pair.LastFlushTookMs,
		"last_commission_flush_took_ms": snapshot.Commission.LastFlushTookMs,
		"last_cost_retry_flush_took_ms": snapshot.CostRetry.LastFlushTookMs,
		"last_pair_retry_flush_took_ms": snapshot.PairRetry.LastFlushTookMs,
		"last_cost_flush_at":            snapshot.Cost.LastFlushAt,
		"last_pair_flush_at":            snapshot.Pair.LastFlushAt,
		"last_commission_flush_at":      snapshot.Commission.LastFlushAt,
		// Buffer / retry config — displayed in status cards
		"buf_max_entries":         cfg.GetBufMaxEntries(),
		"retry_queue_max_entries": retryCfg.GetRetryQueueMaxEntries(),
		"stat_upsert_max_retries": retryCfg.GetStatUpsertMaxRetries(),
		// Nested snapshot — consumed by Default UI
		"snapshot": gin.H{
			"cost":       snapshot.Cost,
			"pair":       snapshot.Pair,
			"commission": snapshot.Commission,
			"cost_retry": snapshot.CostRetry,
			"pair_retry": snapshot.PairRetry,
		},
		"flush_interval_sec":               flushIntervalSec,
		"settlement_flush_max_per_cycle":   cfg.SettlementFlushMaxPerCycle,
		"full_drain":                       cfg.FullDrain,
		"theoretical_pair_records_per_sec": theoreticalPairRecordsPerSec,
		"theoretical_pair_rpm":             theoreticalPairRecordsPerMin,
	})
}

package model

import (
	"context"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// Recovery is single-writer and uses the existing request_id index. A process
// can die after inserting a log but before checkpointing its generated ID.
// Recover the exact identity instead of creating a second financial key.
func identifyRecoveredRelayLog(ctx context.Context, row *Log) error {
	if row == nil || row.RequestId == "" {
		return errors.New("recovered accounting requires a request identity")
	}
	// Version 2 replay queries identity before reaching insertRelayLogs.
	originalRequestID, originalModelName := row.RequestId, row.ModelName
	normalizeRelayLogText(row)
	queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var ids []int
	query := LOG_DB.WithContext(queryCtx).Model(&Log{}).
		Where("user_id = ? AND created_at = ? AND type = ? AND channel_id = ?", row.UserId, row.CreatedAt, row.Type, row.ChannelId)
	if LOG_DB.Dialector.Name() != "postgres" && (originalRequestID != row.RequestId || originalModelName != row.ModelName) {
		// Other backends may have committed the raw identity before this fix.
		// Match either representation in the same indexed lookup, failing closed
		// below if both exist. Never send the raw NUL to PostgreSQL.
		query = query.Where("request_id IN ? AND model_name IN ?",
			[]string{row.RequestId, originalRequestID}, []string{row.ModelName, originalModelName})
	} else {
		query = query.Where("request_id = ? AND model_name = ?", row.RequestId, row.ModelName)
	}
	err := query.Limit(2).Pluck("id", &ids).Error
	if err != nil {
		return err
	}
	if len(ids) > 1 {
		return errors.New("ambiguous recovered log identity; manual reconciliation required")
	}
	if len(ids) == 1 {
		row.Id = ids[0]
	}
	return nil
}

// This runs only on the background replay worker. Existing financial writers
// enforce log_id uniqueness and commit detail records and statistics together.
// A file remains pending until that durable commit (or its cleanup receipt).
func resumeRelayLogAccounting(ctx context.Context, record relayLogFallbackRecord) error {
	if !operation_setting.QuotaRecoveryEnabled() {
		return errors.New("financial recovery is disabled; payload retained")
	}
	if record.Accounting == nil || record.Log == nil || record.Log.Id <= 0 {
		return errors.New("invalid pending financial recovery payload")
	}
	if record.Accounting.Quota == 0 {
		return nil
	}
	queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	seen, err := FilterRetainedFinancialDetails(DB.WithContext(queryCtx), CleanupLedger, []int{record.Log.Id})
	if err != nil {
		return err
	}
	if _, exists := seen[record.Log.Id]; exists {
		return nil
	}
	if relayLogAccountingHandler == nil || IsBusinessStatsCircuitOpen() {
		return errors.New("financial recovery unavailable; payload retained")
	}
	payload := *record.Accounting
	payload.SkipUpstream = true
	payload.UpstreamOnly = false
	relayLogAccountingHandler(payload, record.Log.Id)
	FlushBusinessStatBuffers()
	seen, err = FilterRetainedFinancialDetails(DB.WithContext(queryCtx), CleanupLedger, []int{record.Log.Id})
	if err != nil {
		return err
	}
	if _, exists := seen[record.Log.Id]; !exists {
		return errors.New("financial recovery has not committed; payload retained")
	}
	return nil
}

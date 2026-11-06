package model

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestRecoveredAccountingCommitsOnceAndRetainsFailure(t *testing.T) {
	if DB == nil || LOG_DB == nil {
		t.Skip("requires real primary and log databases")
	}
	withRelayLogReplayTestState(t)
	id := "financial-" + strings.ReplaceAll(common.GetUUID(), "-", "")
	row := &Log{RequestId: id, UserId: 123, Type: LogTypeConsume, CreatedAt: time.Now().Unix(), Quota: 150}
	record := relayLogFallbackRecord{Version: 2, Kind: "relay_log_pending_accounting", Log: row,
		Accounting: &RelayLogAccountingPayload{Version: 1, UserID: row.UserId, Quota: 150}}
	data, err := common.Marshal(record)
	require.NoError(t, err)
	path := filepath.Join(relayLogFallbackDir, "relay-log.jsonl.recovery")
	require.NoError(t, os.MkdirAll(relayLogFallbackDir, 0o750))
	require.NoError(t, os.WriteFile(path, append(data, '\n'), 0o640))
	oldHandler := relayLogAccountingHandler
	var calls atomic.Int64
	RegisterRelayLogAccountingHandler(func(payload RelayLogAccountingPayload, logID int) {
		require.True(t, payload.SkipUpstream)
		require.False(t, payload.UpstreamOnly)
		calls.Add(1)
	})
	t.Cleanup(func() {
		RegisterRelayLogAccountingHandler(oldHandler)
		var ids []int
		require.NoError(t, LOG_DB.Model(&Log{}).Where("request_id = ?", id).Pluck("id", &ids).Error)
		if len(ids) > 0 {
			require.NoError(t, DB.Where("log_id IN ?", ids).Delete(&ConsumptionCost{}).Error)
		}
		require.NoError(t, LOG_DB.Where("request_id = ?", id).Delete(&Log{}).Error)
	})
	// A handler returning does not prove its financial write committed.
	result := replayRelayLogFallback(context.Background(), path)
	require.Error(t, result.err)
	require.EqualValues(t, 1, result.failed)
	require.FileExists(t, path)
	RegisterRelayLogAccountingHandler(func(payload RelayLogAccountingPayload, logID int) {
		calls.Add(1)
		require.Positive(t, logID)
		require.NoError(t, DB.Create(&ConsumptionCost{LogId: &logID, UserId: payload.UserID, RevenueQuota: int64(payload.Quota)}).Error)
	})
	result = replayRelayLogFallback(context.Background(), path)
	require.NoError(t, result.err)
	require.EqualValues(t, 1, result.processed)
	// Restore the original file with no generated ID, as after a process crash.
	require.NoError(t, os.WriteFile(path, append(data, '\n'), 0o640))
	result = replayRelayLogFallback(context.Background(), path)
	require.NoError(t, result.err)
	require.EqualValues(t, 2, calls.Load())
	var logs []Log
	require.NoError(t, LOG_DB.Where("request_id = ?", id).Find(&logs).Error)
	require.Len(t, logs, 1)
	var costs []ConsumptionCost
	require.NoError(t, DB.Where("log_id = ?", logs[0].Id).Find(&costs).Error)
	require.Len(t, costs, 1)
	require.EqualValues(t, 150, costs[0].RevenueQuota)
}

func TestFallbackPreservesFinancialWorkUntilLogID(t *testing.T) {
	dir := useRelayLogFallbackDir(t)
	previous := relayLogAccountingHandler
	var calls atomic.Int64
	RegisterRelayLogAccountingHandler(func(payload RelayLogAccountingPayload, _ int) {
		if !payload.UpstreamOnly {
			calls.Add(1)
		}
	})
	t.Cleanup(func() { RegisterRelayLogAccountingHandler(previous) })
	event := &relayLogEvent{Log: &Log{RequestId: "pending-financial-recovery", Type: LogTypeConsume},
		Accounting: &RelayLogAccountingPayload{Version: 1, UserID: 17, Quota: 150}}
	batch := []relayLogFallbackJob{{event: event, executeContinuation: true}}
	relayLogFallbackBusy.Add(1)
	remaining := 1
	writeRelayLogFallbackBatch(batch, &remaining)
	require.Zero(t, calls.Load(), "do not create a cost record without a real log ID")
	data, err := os.ReadFile(filepath.Join(dir, "relay-log.jsonl"))
	require.NoError(t, err)
	var record relayLogFallbackRecord
	require.NoError(t, common.Unmarshal(data, &record))
	require.NotNil(t, record.Accounting)
	require.Equal(t, 150, record.Accounting.Quota)
}

func TestReplayIncludesActiveFallbackWithoutRestart(t *testing.T) {
	if LOG_DB == nil {
		t.Skip("requires a real log database fixture")
	}
	withRelayLogReplayTestState(t)
	id := "recovery-" + strings.ReplaceAll(common.GetUUID(), "-", "")
	row := &Log{RequestId: id, Type: LogTypeError, CreatedAt: time.Now().Unix()}
	data, err := common.Marshal(relayLogFallbackRecord{Version: 1, Kind: "relay_log_only", Log: row})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(relayLogFallbackDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(relayLogFallbackDir, "relay-log.jsonl"), append(data, '\n'), 0o640))
	t.Cleanup(func() { require.NoError(t, LOG_DB.Where("request_id = ?", id).Delete(&Log{}).Error) })
	started, err := StartRelayLogFallbackReplay()
	require.NoError(t, err)
	require.True(t, started)
	require.Eventually(t, func() bool { return !relayLogReplayRunning.Load() }, 10*time.Second, 20*time.Millisecond)
	var count int64
	require.NoError(t, LOG_DB.Model(&Log{}).Where("request_id = ?", id).Count(&count).Error)
	require.EqualValues(t, 1, count)
	started, err = StartRelayLogFallbackReplay()
	require.NoError(t, err)
	require.False(t, started)
}

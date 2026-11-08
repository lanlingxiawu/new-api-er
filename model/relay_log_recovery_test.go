package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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

func TestRecoveredRelayLog_NULIdentity(t *testing.T) {
	for _, backend := range relayLogBackends {
		t.Run(backend, func(t *testing.T) {
			db := useRelayLogBackend(t, backend)
			prefix := relayLogTestPrefix(t, db)
			row := &Log{RequestId: prefix + "\x00", ModelName: "model\x00name", Content: "error\x00",
				CreatedAt: 1793121348, UserId: 42, ChannelId: 5, Type: LogTypeError}
			data, err := common.Marshal(relayLogFallbackRecord{Version: 2, Kind: "relay_log_pending_accounting",
				Log: row, Accounting: &RelayLogAccountingPayload{Version: 1, UserID: 42, Quota: 0}})
			require.NoError(t, err)
			path := filepath.Join(t.TempDir(), "relay-log.jsonl.pending-nul")
			for range 2 {
				// The second replay models a crash before checkpointing the assigned ID.
				require.NoError(t, os.WriteFile(path, append(data, '\n'), 0o640))
				result := replayRelayLogFallback(context.Background(), path)
				require.NoError(t, result.err)
				require.Zero(t, result.failed)
				require.EqualValues(t, 1, result.processed)
				require.NoFileExists(t, path)
			}
			var rows []Log
			require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Where("request_id LIKE ?", prefix+"%").Find(&rows).Error)
			require.Len(t, rows, 1)
			require.Equal(t, prefix+`\u0000`, rows[0].RequestId)
			require.Equal(t, `model\u0000name`, rows[0].ModelName)
			require.Equal(t, `error\u0000`, rows[0].Content)
		})
	}
}

func TestRecoveredRelayLog_LegacyNULIdentityKeepsID(t *testing.T) {
	// These databases accepted raw NUL before normalization was introduced.
	for _, backend := range []string{"sqlite", "mysql"} {
		for _, identity := range []struct{ requestID, modelName string }{
			{"\x00", "model"}, {"request", "model\x00name"}, {"\x00", "model\x00name"},
		} {
			t.Run(fmt.Sprintf("%s/%q/%q", backend, identity.requestID, identity.modelName), func(t *testing.T) {
				db := useRelayLogBackend(t, backend)
				prefix := relayLogTestPrefix(t, db)
				stored := &Log{RequestId: prefix + identity.requestID, ModelName: identity.modelName, CreatedAt: 1793121348, Type: LogTypeConsume}
				require.NoError(t, db.Create(stored).Error)
				for _, knownID := range []bool{false, true} {
					row := *stored
					if !knownID {
						row.Id = 0
						require.NoError(t, identifyRecoveredRelayLog(context.Background(), &row))
						require.Equal(t, stored.Id, row.Id)
					}
					require.NoError(t, insertRelayLogs(db, []*Log{&row}, 500))
					require.Equal(t, stored.Id, row.Id)
				}
				var count int64
				require.NoError(t, db.Model(&Log{}).Where("request_id LIKE ?", prefix+"%").Count(&count).Error)
				require.EqualValues(t, 1, count)
				// If both representations exist, recovery must not choose a financial ID arbitrarily.
				duplicate := *stored
				duplicate.Id = 0
				duplicate.RequestId = strings.ReplaceAll(duplicate.RequestId, "\x00", `\u0000`)
				duplicate.ModelName = strings.ReplaceAll(duplicate.ModelName, "\x00", `\u0000`)
				require.NoError(t, db.Create(&duplicate).Error)
				row := *stored
				row.Id = 0
				require.ErrorContains(t, identifyRecoveredRelayLog(context.Background(), &row), "ambiguous recovered log identity")
				require.Zero(t, row.Id)
			})
		}
	}
}

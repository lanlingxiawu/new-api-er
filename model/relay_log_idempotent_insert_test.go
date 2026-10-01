package model

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 消费日志写入必须对"已经带 id 的行"幂等。
//
// 批量插入超时时，服务端可能已经提交，客户端却只看到 context deadline exceeded；
// 这批事件带着 RETURNING 回填的 id 进重试。重试若直接 INSERT 会撞主键（23505），
// 且每次都撞——同批里从未提交的行也被拖着一起失败，最终整批进兜底文件，logs 表缺行。
// 兜底回放同理：文件里带 id 的记录会让整批回放永远失败。
//
// 三库行为不同（PG/SQLite 用 RETURNING 回填、MySQL 用 LastInsertId），逐库验证。

// useRelayLogBackend 把 LOG_DB 指向指定库并重置管道状态。
func useRelayLogBackend(t *testing.T, backend string) *gorm.DB {
	t.Helper()
	switch backend {
	case "sqlite":
		return useRelayLogPipelineSQLite(t)
	case "mysql":
		requireDB(t)
		if DB.Dialector.Name() != "mysql" {
			t.Skip("main DB is not MySQL")
		}
		return swapRelayLogDB(t, DB)
	case "postgres":
		requireLogDB(t)
		if LOG_DB.Dialector.Name() != "postgres" {
			t.Skip("log DB is not PostgreSQL")
		}
		return swapRelayLogDB(t, LOG_DB)
	}
	t.Fatalf("unknown backend %s", backend)
	return nil
}

func swapRelayLogDB(t *testing.T, db *gorm.DB) *gorm.DB {
	t.Helper()
	previous := LOG_DB
	LOG_DB = db
	resetRelayLogPipelineForTest()
	t.Cleanup(func() {
		LOG_DB = previous
		resetRelayLogPipelineForTest()
	})
	return db
}

var relayLogBackends = []string{"sqlite", "mysql", "postgres"}

// relayLogTestPrefix 生成本用例专属的 request_id 前缀，并按前缀行级清理（不整表清空）。
func relayLogTestPrefix(t *testing.T, db *gorm.DB) string {
	t.Helper()
	prefix := "rlid-" + common.GetRandomString(10) + "-"
	t.Cleanup(func() {
		db.Where("request_id LIKE ?", prefix+"%").Delete(&Log{})
	})
	return prefix
}

// committedLog 模拟"超时但已提交"：行已在库里，内存对象也拿到了 id。
func committedLog(t *testing.T, db *gorm.DB, requestID string) *Log {
	t.Helper()
	log := &Log{RequestId: requestID, Type: LogTypeConsume, Quota: 7}
	require.NoError(t, db.Create(log).Error)
	require.Positive(t, log.Id)
	// 与真实重试一致：失败那次尝试里 GORM 已把 id 与 created_at 回填进内存对象。
	return &Log{Id: log.Id, CreatedAt: log.CreatedAt, RequestId: requestID, Type: LogTypeConsume, Quota: 7}
}

// rolledBackLog 模拟"已分到 id 但事务回滚"：id 取自真实序列，库里却没有这一行。
func rolledBackLog(t *testing.T, db *gorm.DB, requestID string) *Log {
	t.Helper()
	log := &Log{RequestId: requestID + "-probe", Type: LogTypeConsume}
	require.NoError(t, db.Create(log).Error)
	require.NoError(t, db.Unscoped().Delete(&Log{}, log.Id).Error)
	return &Log{Id: log.Id, RequestId: requestID, Type: LogTypeConsume, Quota: 7}
}

func requireOneRowEach(t *testing.T, db *gorm.DB, requestIDs ...string) map[string]int {
	t.Helper()
	ids := make(map[string]int, len(requestIDs))
	for _, rid := range requestIDs {
		var rows []Log
		require.NoError(t, db.Where("request_id = ?", rid).Find(&rows).Error)
		require.Len(t, rows, 1, "request %s must be stored exactly once", rid)
		ids[rid] = rows[0].Id
	}
	return ids
}

func TestPersistRelayLogEvents_RetryAfterAmbiguousCommitIsIdempotent(t *testing.T) {
	for _, backend := range relayLogBackends {
		t.Run(backend, func(t *testing.T) {
			db := useRelayLogBackend(t, backend)
			prefix := relayLogTestPrefix(t, db)

			continued := make(chan [2]int, 8)
			previousHandler := relayLogAccountingHandler
			RegisterRelayLogAccountingHandler(func(p RelayLogAccountingPayload, id int) { continued <- [2]int{p.UserID, id} })
			t.Cleanup(func() { RegisterRelayLogAccountingHandler(previousHandler) })

			// 同一批里三类行混在一起：已提交、已回滚（带 id）、从未分到 id。
			logs := []*Log{
				committedLog(t, db, prefix+"committed-1"),
				rolledBackLog(t, db, prefix+"rolledback-1"),
				{RequestId: prefix + "fresh-1", Type: LogTypeConsume, Quota: 7},
				committedLog(t, db, prefix+"committed-2"),
				{RequestId: prefix + "fresh-2", Type: LogTypeConsume, Quota: 7},
			}
			events := make([]*relayLogEvent, len(logs))
			for i, log := range logs {
				events[i] = &relayLogEvent{Log: log, Accounting: &RelayLogAccountingPayload{Version: 1, UserID: i}}
			}
			// 只有已提交的行保留原 id；回滚的 id 可能已被复用（SQLite 必然复用），重新分配。
			presetIDs := []int{logs[0].Id, 0, 0, logs[3].Id, 0}

			require.True(t, persistRelayLogEvents(events), "a retry after an ambiguous commit must succeed")

			stored := requireOneRowEach(t, db,
				prefix+"committed-1", prefix+"rolledback-1", prefix+"fresh-1", prefix+"committed-2", prefix+"fresh-2")
			for i, log := range logs {
				require.Equal(t, stored[log.RequestId], log.Id, "in-memory id must match the stored row for %s", log.RequestId)
				if presetIDs[i] > 0 {
					require.Equal(t, presetIDs[i], log.Id, "a row that already had an id keeps it")
				}
			}
			// 每条恰好记账一次，且拿到的是它自己那一行的 id。
			for range logs {
				got := <-continued
				require.Equal(t, logs[got[0]].Id, got[1])
			}
			require.Empty(t, continued)
		})
	}
}

// 回滚事务分到的 id 被别的行占用时，本条不能被当成"已入库"跳过，也不能动那一行。
func TestPersistRelayLogEvents_StaleIDTakenByAnotherRowInsertsFresh(t *testing.T) {
	for _, backend := range relayLogBackends {
		t.Run(backend, func(t *testing.T) {
			db := useRelayLogBackend(t, backend)
			prefix := relayLogTestPrefix(t, db)

			other := committedLog(t, db, prefix+"other")
			mine := &Log{Id: other.Id, RequestId: prefix + "mine", Type: LogTypeConsume, Quota: 3}
			require.True(t, persistRelayLogEvents([]*relayLogEvent{{Log: mine}}))

			stored := requireOneRowEach(t, db, prefix+"other", prefix+"mine")
			require.Equal(t, other.Id, stored[prefix+"other"], "the row that owns the id is untouched")
			require.NotEqual(t, other.Id, mine.Id)
			require.Equal(t, stored[prefix+"mine"], mine.Id)
		})
	}
}

// 首次写入（没有任何行带 id）不回查，保持单条批量 INSERT。
func TestInsertRelayLogs_FreshBatchSkipsLookup(t *testing.T) {
	db := useRelayLogBackend(t, "sqlite")
	prefix := relayLogTestPrefix(t, db)
	var queries int
	name := "test:count_relay_log_lookups"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(name, func(*gorm.DB) { queries++ }))
	t.Cleanup(func() { _ = db.Callback().Query().Remove(name) })

	logs := []*Log{{RequestId: prefix + "1"}, {RequestId: prefix + "2"}}
	require.NoError(t, insertRelayLogs(db, logs, 500))
	require.Zero(t, queries)
	require.Positive(t, logs[0].Id)
	require.Positive(t, logs[1].Id)
}

func TestReplayRelayLogFallback_IdempotentForRecordsWithIDs(t *testing.T) {
	for _, backend := range relayLogBackends {
		t.Run(backend, func(t *testing.T) {
			db := useRelayLogBackend(t, backend)
			prefix := relayLogTestPrefix(t, db)

			path := filepath.Join(t.TempDir(), "relay-log.jsonl")
			writeFallbackRecords(t, path,
				committedLog(t, db, prefix+"committed"),
				rolledBackLog(t, db, prefix+"rolledback"),
				&Log{RequestId: prefix + "fresh", Type: LogTypeConsume, Quota: 7},
			)

			result := replayRelayLogFallback(context.Background(), path)
			require.NoError(t, result.err)
			require.Zero(t, result.failed)
			require.EqualValues(t, 3, result.processed)
			require.NoFileExists(t, path)
			requireOneRowEach(t, db, prefix+"committed", prefix+"rolledback", prefix+"fresh")
		})
	}
}

// 回放写入"报错但其实已提交"时，保留下来的记录必须带上已分到的 id，
// 否则下一次回放会把这些行再插一遍。
func TestReplayRelayLogFallback_RetainedRecordsCarryAssignedIDs(t *testing.T) {
	for _, backend := range relayLogBackends {
		t.Run(backend, func(t *testing.T) {
			db := useRelayLogBackend(t, backend)
			prefix := relayLogTestPrefix(t, db)

			path := filepath.Join(t.TempDir(), "relay-log.jsonl")
			writeFallbackRecords(t, path,
				&Log{RequestId: prefix + "a", Type: LogTypeConsume},
				&Log{RequestId: prefix + "b", Type: LogTypeConsume},
			)

			oldWriter := relayLogReplayBatchWriter
			relayLogReplayBatchWriter = func(ctx context.Context, logs []*Log, batchSize int) error {
				if err := insertRelayLogs(LOG_DB.WithContext(ctx), logs, batchSize); err != nil {
					return err
				}
				return errors.New("commit acknowledgement lost")
			}
			first := replayRelayLogFallback(context.Background(), path)
			relayLogReplayBatchWriter = oldWriter
			require.Error(t, first.err)
			require.EqualValues(t, 2, first.failed)
			require.FileExists(t, path)
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			require.NotContains(t, string(raw), `"id":0,`, "retained records must carry the ids they were assigned")

			second := replayRelayLogFallback(context.Background(), path)
			require.NoError(t, second.err)
			require.Zero(t, second.failed)
			require.NoFileExists(t, path)
			requireOneRowEach(t, db, prefix+"a", prefix+"b")
		})
	}
}

func writeFallbackRecords(t *testing.T, path string, logs ...*Log) {
	t.Helper()
	var lines []string
	for _, log := range logs {
		data, err := common.Marshal(relayLogFallbackRecord{Version: 1, Kind: "relay_log_only", RecordedAt: 1, Log: log})
		require.NoError(t, err)
		lines = append(lines, string(data))
	}
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o640))
}

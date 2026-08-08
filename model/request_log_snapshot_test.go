package model

import (
	"context"
	"os"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Redis 快照：进程退出写、启动读完即删。稳态零 Redis 流量。
// ---------------------------------------------------------------------------

func requestLogClearSnapshot(t *testing.T) {
	t.Helper()
	drop := func() {
		if requestLogRedisReady() {
			common.RequestLogRDB.Del(context.Background(), requestLogSnapshotKey)
		}
	}
	drop()
	t.Cleanup(drop)
}

func TestRequestLogSnapshot_RoundTripAndKeyRemoval(t *testing.T) {
	enableRedis(t)
	requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	requestLogClearSnapshot(t)

	for i := 0; i < 3; i++ {
		RecordRequestLog(&RequestLog{
			Username:    "alice",
			CreatedAt:   int64(1000 + i),
			RequestId:   "rid-" + strconv.Itoa(i),
			UseTimeMs:   int64(i),
			RequestBody: "body-" + strconv.Itoa(i),
		})
	}
	SnapshotRequestLogs()

	// 清空内存索引，模拟重启
	reqLogMu.Lock()
	reqLogItems = nil
	reqLogMu.Unlock()
	atomic.StoreInt64(&reqLogSeq, 0)

	RestoreRequestLogs()

	list, total, err := GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
	require.Len(t, list, 3)
	assert.EqualValues(t, 1002, list[0].CreatedAt, "restore must preserve newest-first order")

	// 详情仍可从磁盘读回
	detail, err := GetRequestLogById(list[0].Id)
	require.NoError(t, err)
	assert.Equal(t, "body-2", detail.RequestBody)

	// 新写入的 id 必须接着快照里的最大值，否则会与恢复条目撞 id
	RecordRequestLog(&RequestLog{Username: "alice", CreatedAt: 2000, RequestId: "rid-new"})
	fresh, _, err := GetAllRequestLogs("", "", 0, "rid-new", 0, 0, 0, 0, 10)
	require.NoError(t, err)
	require.Len(t, fresh, 1)
	assert.Greater(t, fresh[0].Id, list[0].Id, "sequence must resume above the restored max id")

	// 快照键读完即删
	exists, err := common.RequestLogRDB.Exists(context.Background(), requestLogSnapshotKey).Result()
	require.NoError(t, err)
	assert.EqualValues(t, 0, exists, "the snapshot key must be removed after restore")
}

// 正文已消失的条目不能进索引，否则详情点开是 404。
func TestRequestLogSnapshot_SkipsEntriesWithMissingBody(t *testing.T) {
	enableRedis(t)
	root := requestLogTestStore(t)
	requestLogWithLimits(t, 100, 1000)
	requestLogClearSnapshot(t)

	RecordRequestLog(&RequestLog{Username: "u", CreatedAt: 1000, RequestId: "gone"})
	SnapshotRequestLogs()

	// 删掉磁盘正文后再恢复
	require.NoError(t, os.RemoveAll(root))
	require.NoError(t, os.MkdirAll(root, 0o755))
	reqLogMu.Lock()
	reqLogItems = nil
	reqLogMu.Unlock()

	RestoreRequestLogs()
	assert.Zero(t, requestLogIndexLen())
}

// 快照条数超过 max 时只恢复最新的 max 条。
func TestRequestLogSnapshot_CapsRestoredCount(t *testing.T) {
	enableRedis(t)
	requestLogTestStore(t)
	requestLogWithLimits(t, 2, 6)
	requestLogClearSnapshot(t)

	for i := 0; i < 6; i++ {
		RecordRequestLog(&RequestLog{Username: "u", CreatedAt: int64(1000 + i), RequestId: "rid-" + strconv.Itoa(i)})
	}
	require.Equal(t, 6, requestLogIndexLen())
	SnapshotRequestLogs()

	reqLogMu.Lock()
	reqLogItems = nil
	reqLogMu.Unlock()

	// 收紧上限后恢复：只保留最新的 3 条
	requestLogWithLimits(t, 1, 3)
	RestoreRequestLogs()
	assert.Equal(t, 3, requestLogIndexLen())

	list, _, err := GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 10)
	require.NoError(t, err)
	require.Len(t, list, 3)
	assert.EqualValues(t, 1005, list[0].CreatedAt, "the newest entries are the ones kept")
}

func TestRequestLogSnapshot_CorruptedSnapshotDiscarded(t *testing.T) {
	enableRedis(t)
	requestLogTestStore(t)
	requestLogClearSnapshot(t)

	require.NoError(t, common.RequestLogRDB.Set(context.Background(), requestLogSnapshotKey, "not json", requestLogSnapshotTTL).Err())
	RestoreRequestLogs()
	assert.Zero(t, requestLogIndexLen())

	exists, err := common.RequestLogRDB.Exists(context.Background(), requestLogSnapshotKey).Result()
	require.NoError(t, err)
	assert.EqualValues(t, 0, exists, "a corrupted snapshot must still be dropped")
}

func TestRequestLogSnapshot_EmptyIndexWritesNothing(t *testing.T) {
	enableRedis(t)
	requestLogTestStore(t)
	requestLogClearSnapshot(t)

	SnapshotRequestLogs()
	exists, err := common.RequestLogRDB.Exists(context.Background(), requestLogSnapshotKey).Result()
	require.NoError(t, err)
	assert.EqualValues(t, 0, exists)
}

// Redis 未启用时，快照与恢复都必须是安静的 no-op（Rule 0 优雅降级）。
func TestRequestLogSnapshot_NoopWithoutRedis(t *testing.T) {
	requestLogTestStore(t)
	prevEnabled, prevRDB := common.RedisEnabled, common.RequestLogRDB
	common.RedisEnabled = false
	common.RequestLogRDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = prevEnabled
		common.RequestLogRDB = prevRDB
	})

	RecordRequestLog(&RequestLog{Username: "u", CreatedAt: 1000, RequestId: "rid"})
	require.NotPanics(t, func() {
		SnapshotRequestLogs()
		RestoreRequestLogs()
		StartLegacyRequestLogCleanup()
		CleanupLegacyRequestLogRedisKeys()
	})
	// 恢复是 no-op，不能把已有索引清空
	assert.Equal(t, 1, requestLogIndexLen())
}

// 没有遗留索引键时不得触发 SCAN：那会在共用主库的默认配置下走一遍全键空间。
func TestCleanupLegacyRequestLogRedisKeys_SkipsWhenNothingToClean(t *testing.T) {
	enableRedis(t)
	ctx := context.Background()
	common.RequestLogRDB.Del(ctx, legacyRequestLogIndexKey, legacyRequestLogSeqKey)

	orphanKey := legacyRequestLogMetaKey + "test-orphan"
	require.NoError(t, common.RequestLogRDB.Set(ctx, orphanKey, "{}", requestLogSnapshotTTL).Err())
	t.Cleanup(func() { common.RequestLogRDB.Del(ctx, orphanKey) })

	CleanupLegacyRequestLogRedisKeys()

	exists, err := common.RequestLogRDB.Exists(ctx, orphanKey).Result()
	require.NoError(t, err)
	assert.EqualValues(t, 1, exists, "without the legacy index key the scan must be skipped entirely")
}

func TestCleanupLegacyRequestLogRedisKeys_RemovesLegacyData(t *testing.T) {
	enableRedis(t)
	ctx := context.Background()

	metaKey := legacyRequestLogMetaKey + "42"
	bodyKey := legacyRequestLogBodyKey + "42"
	require.NoError(t, common.RequestLogRDB.Set(ctx, metaKey, "{}", requestLogSnapshotTTL).Err())
	require.NoError(t, common.RequestLogRDB.Set(ctx, bodyKey, "{}", requestLogSnapshotTTL).Err())
	require.NoError(t, common.RequestLogRDB.LPush(ctx, legacyRequestLogIndexKey, "42").Err())
	require.NoError(t, common.RequestLogRDB.Set(ctx, legacyRequestLogSeqKey, "42", requestLogSnapshotTTL).Err())
	t.Cleanup(func() {
		common.RequestLogRDB.Del(ctx, metaKey, bodyKey, legacyRequestLogIndexKey, legacyRequestLogSeqKey)
	})

	CleanupLegacyRequestLogRedisKeys()

	remaining, err := common.RequestLogRDB.Exists(ctx, metaKey, bodyKey, legacyRequestLogIndexKey, legacyRequestLogSeqKey).Result()
	require.NoError(t, err)
	assert.EqualValues(t, 0, remaining, "all legacy request log keys must be gone")
}

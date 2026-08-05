package model

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRelayLogSQLiteBatchInsertReturnsIDs(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Log{}))
	logs := []*Log{{RequestId: "sqlite-one"}, {RequestId: "sqlite-two"}}
	require.NoError(t, db.CreateInBatches(&logs, 2).Error)
	require.Positive(t, logs[0].Id)
	require.Positive(t, logs[1].Id)
	require.NotEqual(t, logs[0].Id, logs[1].Id)
}

func TestFallbackRetentionCapPreservesExistingFilesAndFailsClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "relay-log.jsonl")
	file, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, file.Truncate(1024*1024))
	require.NoError(t, file.Close())
	require.NoError(t, os.WriteFile(path+".1", []byte("unreplayed\n"), 0o640))

	cfg := operation_setting.GetRelayLogPipelineSetting()
	old := *cfg
	t.Cleanup(func() { *cfg = old })
	cfg.FallbackMaxFileSizeMB = 1
	cfg.FallbackMaxFiles = 1

	require.ErrorIs(t, rotateRelayLogFallback(path), errRelayLogFallbackRetentionFull)
	require.FileExists(t, path)
	require.FileExists(t, path+".1")
}

func TestFallbackRotationCountsReplayArtifactsAsOneLogicalTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "relay-log.jsonl")
	file, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, file.Truncate(1024*1024))
	require.NoError(t, file.Close())
	parent := path + ".1"
	require.NoError(t, os.WriteFile(parent, []byte("pending\n"), 0o640))
	require.NoError(t, os.WriteFile(parent+".replay.tmp", []byte("temporary\n"), 0o640))
	require.NoError(t, os.WriteFile(parent+".replay.source", []byte("source\n"), 0o640))

	cfg := operation_setting.GetRelayLogPipelineSetting()
	old := *cfg
	t.Cleanup(func() { *cfg = old })
	cfg.FallbackMaxFileSizeMB = 1
	cfg.FallbackMaxFiles = 2

	require.NoError(t, rotateRelayLogFallback(path))
	matches, err := filepath.Glob(path + ".*")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(matches), 4)
}

func TestReplayRelayLogFallbackNeverRepeatsAccounting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relay-log.jsonl")
	payload := RelayLogAccountingPayload{Version: 1, UserID: 42, Quota: 99}
	data, err := common.Marshal(relayLogFallbackRecord{
		Version: 1, Kind: "relay_log", Accounting: &payload,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(data, '\n'), 0o640))

	got := make(chan RelayLogAccountingPayload, 1)
	oldHandler := relayLogAccountingHandler
	RegisterRelayLogAccountingHandler(func(payload RelayLogAccountingPayload, _ int) { got <- payload })
	t.Cleanup(func() { RegisterRelayLogAccountingHandler(oldHandler) })

	replayRelayLogFallback(context.Background(), path)
	require.Never(t, func() bool { return len(got) > 0 }, 10*time.Millisecond, time.Millisecond)
	require.NoFileExists(t, path)
}

func TestReplayRelayLogFallbackSupportsRecordLargerThan16MiB(t *testing.T) {
	withRelayLogReplayTestState(t)
	path := writeRelayLogFallbackFile(t, &Log{
		RequestId: "large-record",
		Content:   strings.Repeat("x", 17*1024*1024),
	})
	oldWriter := relayLogReplayBatchWriter
	var written atomic.Int64
	relayLogReplayBatchWriter = func(_ context.Context, logs []*Log, _ int) error {
		require.Len(t, logs, 1)
		require.Len(t, logs[0].Content, 17*1024*1024)
		written.Add(1)
		return nil
	}
	t.Cleanup(func() { relayLogReplayBatchWriter = oldWriter })

	result := replayRelayLogFallback(context.Background(), path)

	require.NoError(t, result.err)
	require.EqualValues(t, 1, result.processed)
	require.EqualValues(t, 1, written.Load())
	require.NoFileExists(t, path)
}

func TestPrepareRelayLogReplayFilesOnlyArchivesActiveFile(t *testing.T) {
	oldDir := relayLogFallbackDir
	relayLogFallbackDir = t.TempDir()
	t.Cleanup(func() { relayLogFallbackDir = oldDir })
	active := filepath.Join(relayLogFallbackDir, "relay-log.jsonl")
	require.NoError(t, os.WriteFile(active, []byte("pending\n"), 0o640))

	prepareRelayLogReplayFiles()

	require.NoFileExists(t, active)
	paths, err := filepath.Glob(active + ".*")
	require.NoError(t, err)
	require.Len(t, paths, 1)
	require.Equal(t, []byte("pending\n"), mustReadFile(t, paths[0]))
}

func TestStartRelayLogFallbackReplayReturnsFalseWhenEmpty(t *testing.T) {
	withRelayLogReplayTestState(t)
	started, err := StartRelayLogFallbackReplay()
	require.NoError(t, err)
	require.False(t, started)
	require.Equal(t, "idle", GetRelayLogPipelineStatus().Replay.State)
}

func TestStartRelayLogFallbackReplayReportsRunningBeforeDirectoryListing(t *testing.T) {
	withRelayLogReplayTestState(t)
	relayLogReplayRunning.Store(true)
	_, err := StartRelayLogFallbackReplay()
	require.ErrorIs(t, err, ErrRelayLogReplayRunning)
}

func TestListRelayLogReplayFilesRecoversSourceOnlyCrashWindow(t *testing.T) {
	withRelayLogReplayTestState(t)
	base := filepath.Join(relayLogFallbackDir, "relay-log.jsonl.1")
	require.NoError(t, os.WriteFile(base+".replay.source", []byte("source\n"), 0o640))

	paths, err := listRelayLogReplayFiles()

	require.NoError(t, err)
	require.Equal(t, []string{base}, paths)
	require.Equal(t, []byte("source\n"), mustReadFile(t, base))
	require.NoFileExists(t, base+".replay.source")
}

func TestListRelayLogReplayFilesRecoversCompletedReplacementCrashWindow(t *testing.T) {
	withRelayLogReplayTestState(t)
	base := filepath.Join(relayLogFallbackDir, "relay-log.jsonl.1")
	require.NoError(t, os.WriteFile(base, []byte("remaining\n"), 0o640))
	require.NoError(t, os.WriteFile(base+".replay.source", []byte("source\n"), 0o640))
	require.NoError(t, os.WriteFile(base+".replay.tmp", []byte("stale\n"), 0o640))

	paths, err := listRelayLogReplayFiles()

	require.NoError(t, err)
	require.Equal(t, []string{base}, paths)
	require.Equal(t, []byte("remaining\n"), mustReadFile(t, base))
	require.NoFileExists(t, base+".replay.source")
	require.NoFileExists(t, base+".replay.tmp")
}

func TestListRelayLogReplayFilesRecoversTmpOnlyCrashWindow(t *testing.T) {
	withRelayLogReplayTestState(t)
	base := filepath.Join(relayLogFallbackDir, "relay-log.jsonl.1")
	require.NoError(t, os.WriteFile(base+".replay.tmp", []byte("remaining\n"), 0o640))

	paths, err := listRelayLogReplayFiles()

	require.NoError(t, err)
	require.Equal(t, []string{base}, paths)
	require.Equal(t, []byte("remaining\n"), mustReadFile(t, base))
	require.NoFileExists(t, base+".replay.tmp")
}

func TestStartRelayLogFallbackReplayIsSingleFlight(t *testing.T) {
	withRelayLogReplayTestState(t)
	path := writeRelayLogFallbackFile(t, &Log{RequestId: "manual-single-flight"})
	entered := make(chan struct{})
	release := make(chan struct{})
	oldWriter := relayLogReplayBatchWriter
	relayLogReplayBatchWriter = func(_ context.Context, _ []*Log, _ int) error {
		close(entered)
		<-release
		return nil
	}
	t.Cleanup(func() { relayLogReplayBatchWriter = oldWriter })

	started, err := StartRelayLogFallbackReplay()
	require.NoError(t, err)
	require.True(t, started)
	<-entered
	_, err = StartRelayLogFallbackReplay()
	require.ErrorIs(t, err, ErrRelayLogReplayRunning)
	close(release)
	require.Eventually(t, func() bool {
		return GetRelayLogPipelineStatus().Replay.State == "succeeded"
	}, time.Second, time.Millisecond)
	require.NoFileExists(t, path)
}

func TestManualRelayLogFallbackReplayRetainsFailedRowsAndNeverRunsAccounting(t *testing.T) {
	withRelayLogReplayTestState(t)
	path := writeRelayLogFallbackFile(t, &Log{RequestId: "manual-failed"})
	var accountingCalls atomic.Int64
	oldHandler := relayLogAccountingHandler
	RegisterRelayLogAccountingHandler(func(RelayLogAccountingPayload, int) {
		accountingCalls.Add(1)
	})
	t.Cleanup(func() { RegisterRelayLogAccountingHandler(oldHandler) })
	oldWriter := relayLogReplayBatchWriter
	relayLogReplayBatchWriter = func(context.Context, []*Log, int) error {
		return errors.New("database details must not reach status")
	}
	t.Cleanup(func() { relayLogReplayBatchWriter = oldWriter })

	started, err := StartRelayLogFallbackReplay()
	require.NoError(t, err)
	require.True(t, started)
	require.Eventually(t, func() bool {
		return GetRelayLogPipelineStatus().Replay.State == "failed"
	}, time.Second, time.Millisecond)
	status := GetRelayLogPipelineStatus().Replay
	require.Zero(t, status.ProcessedTotal)
	require.EqualValues(t, 1, status.FailedTotal)
	require.NotContains(t, status.LastError, "database details")
	require.Zero(t, accountingCalls.Load())
	require.FileExists(t, path)
}

func TestManualRelayLogFallbackReplayConcurrentTriggers(t *testing.T) {
	withRelayLogReplayTestState(t)
	writeRelayLogFallbackFile(t, &Log{RequestId: "manual-concurrent"})
	entered := make(chan struct{})
	release := make(chan struct{})
	oldWriter := relayLogReplayBatchWriter
	relayLogReplayBatchWriter = func(_ context.Context, _ []*Log, _ int) error {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-release
		return nil
	}
	t.Cleanup(func() { relayLogReplayBatchWriter = oldWriter })

	var startedCount atomic.Int64
	var runningCount atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			started, err := StartRelayLogFallbackReplay()
			if started {
				startedCount.Add(1)
			}
			if errors.Is(err, ErrRelayLogReplayRunning) {
				runningCount.Add(1)
			}
		}()
	}
	<-entered
	close(release)
	wg.Wait()
	require.EqualValues(t, 1, startedCount.Load())
	require.EqualValues(t, 7, runningCount.Load())
}

func TestManualRelayLogFallbackReplayCancellationRetainsFile(t *testing.T) {
	withRelayLogReplayTestState(t)
	path := writeRelayLogFallbackFile(t, &Log{RequestId: "manual-cancel"})
	entered := make(chan struct{})
	oldWriter := relayLogReplayBatchWriter
	relayLogReplayBatchWriter = func(ctx context.Context, _ []*Log, _ int) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}
	t.Cleanup(func() { relayLogReplayBatchWriter = oldWriter })

	started, err := StartRelayLogFallbackReplay()
	require.NoError(t, err)
	require.True(t, started)
	<-entered
	stopRelayLogFallbackReplay()
	require.Eventually(t, func() bool {
		return GetRelayLogPipelineStatus().Replay.State == "failed"
	}, time.Second, time.Millisecond)
	require.FileExists(t, path)
	require.EqualValues(t, 1, GetRelayLogPipelineStatus().Replay.FailedTotal)
}

func TestManualRelayLogFallbackReplayWaitsForNormalWriterMutex(t *testing.T) {
	withRelayLogReplayTestState(t)
	writeRelayLogFallbackFile(t, &Log{RequestId: "shared-writer"})
	var entered atomic.Bool
	oldWriter := relayLogReplayBatchWriter
	relayLogReplayBatchWriter = func(context.Context, []*Log, int) error {
		entered.Store(true)
		return nil
	}
	t.Cleanup(func() { relayLogReplayBatchWriter = oldWriter })

	relayLogFlushMu.Lock()
	started, err := StartRelayLogFallbackReplay()
	require.NoError(t, err)
	require.True(t, started)
	require.Never(t, entered.Load, 20*time.Millisecond, time.Millisecond)
	relayLogFlushMu.Unlock()
	require.Eventually(t, entered.Load, time.Second, time.Millisecond)
}

func TestManualRelayLogFallbackReplayPanicReleasesWriterAndRetainsFile(t *testing.T) {
	withRelayLogReplayTestState(t)
	path := writeRelayLogFallbackFile(t, &Log{RequestId: "manual-panic"})
	oldWriter := relayLogReplayBatchWriter
	relayLogReplayBatchWriter = func(context.Context, []*Log, int) error {
		panic("injected writer panic")
	}
	t.Cleanup(func() { relayLogReplayBatchWriter = oldWriter })

	started, err := StartRelayLogFallbackReplay()
	require.NoError(t, err)
	require.True(t, started)
	require.Eventually(t, func() bool {
		return GetRelayLogPipelineStatus().Replay.State == "failed"
	}, time.Second, time.Millisecond)
	require.FileExists(t, path)
	require.True(t, relayLogFlushMu.TryLock(), "writer mutex must be released after panic")
	relayLogFlushMu.Unlock()
	require.NotContains(t, GetRelayLogPipelineStatus().Replay.LastError, "injected")
}

func withRelayLogReplayTestState(t *testing.T) {
	t.Helper()
	oldDir := relayLogFallbackDir
	relayLogFallbackDir = t.TempDir()
	relayLogAccepting.Store(true)
	resetRelayLogReplayForTest()
	t.Cleanup(func() {
		stopRelayLogFallbackReplay()
		relayLogReplayWG.Wait()
		resetRelayLogReplayForTest()
		relayLogFallbackDir = oldDir
	})
}

func writeRelayLogFallbackFile(t *testing.T, log *Log) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(relayLogFallbackDir, 0o750))
	path := filepath.Join(relayLogFallbackDir, "relay-log.jsonl.1")
	data, err := common.Marshal(relayLogFallbackRecord{
		Version: 1, Kind: "relay_log_only", RecordedAt: time.Now().Unix(), Log: log,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(data, '\n'), 0o640))
	return path
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

func TestRelayLogBufferOverflowRejectsNewAndDispatchesContinuation(t *testing.T) {
	oldFallbackDir := relayLogFallbackDir
	relayLogFallbackDir = t.TempDir()
	t.Cleanup(func() { relayLogFallbackDir = oldFallbackDir })
	cfg := operation_setting.GetRelayLogPipelineSetting()
	old := *cfg
	t.Cleanup(func() { *cfg = old; resetRelayLogPipelineForTest() })
	cfg.ConsumeBufMaxEntries = 2
	resetRelayLogPipelineForTest()

	require.True(t, enqueueRelayLog(relayLogKindConsume, &relayLogEvent{Log: &Log{RequestId: "one"}}))
	require.True(t, enqueueRelayLog(relayLogKindConsume, &relayLogEvent{Log: &Log{RequestId: "two"}}))
	continued := make(chan int, 1)
	oldHandler := relayLogAccountingHandler
	RegisterRelayLogAccountingHandler(func(_ RelayLogAccountingPayload, id int) { continued <- id })
	t.Cleanup(func() { RegisterRelayLogAccountingHandler(oldHandler) })
	require.True(t, enqueueRelayLog(relayLogKindConsume, &relayLogEvent{
		Log:        &Log{RequestId: "three"},
		Accounting: &RelayLogAccountingPayload{Version: 1},
	}))

	got := takeRelayLogBatch(relayLogKindConsume)
	require.Len(t, got, 2)
	require.Equal(t, "one", got[0].Log.RequestId)
	require.Equal(t, "two", got[1].Log.RequestId)
	require.EqualValues(t, 1, relayLogDroppedConsume.Load())
	require.Equal(t, 0, <-continued)
	require.Eventually(t, func() bool { return relayLogFallbackTotal.Load() == 1 }, time.Second, time.Millisecond)
}

func TestRelayLogRetryQueueBoundedAndCountsRetries(t *testing.T) {
	oldFallbackDir := relayLogFallbackDir
	relayLogFallbackDir = t.TempDir()
	t.Cleanup(func() { relayLogFallbackDir = oldFallbackDir })
	cfg := operation_setting.GetRelayLogRetrySetting()
	old := *cfg
	t.Cleanup(func() { *cfg = old; resetRelayLogPipelineForTest() })
	cfg.RetryBufMaxEntries = 1
	resetRelayLogPipelineForTest()

	first := &relayLogEvent{Log: &Log{RequestId: "one"}}
	second := &relayLogEvent{Log: &Log{RequestId: "two"}}
	enqueueRelayLogRetry([]*relayLogEvent{first})
	enqueueRelayLogRetry([]*relayLogEvent{second})

	got := takeRelayLogRetryBatch(10)
	require.Len(t, got, 1)
	require.Equal(t, "two", got[0].Log.RequestId)
	require.Equal(t, 1, got[0].RetryCount)
	require.Eventually(t, func() bool { return relayLogFallbackTotal.Load() == 1 }, time.Second, time.Millisecond)
}

// TestRelayLogDisabledStopsRecordingWithoutSyncWrite 钉住 enabled 开关的最终定义。
//
// `enabled=false` = **停止记录新的 relay 日志**，而不是"回退到同步写入"。
// 设计文档早期章节曾写成后者，与实现相反——那个语义会把同步写库放回 relay
// goroutine，正是 Rule 0 禁止、也是这套管道要消除的东西；在 30k RPM 下打开这样一个
// "回滚开关"比不记日志危险得多。
//
// 三条契约：不入 buffer、不写库、记账仍以 logID=0 异步继续。
func TestRelayLogDisabledStopsRecordingWithoutSyncWrite(t *testing.T) {
	db := useRelayLogPipelineSQLite(t)
	cfg := operation_setting.GetRelayLogPipelineSetting()
	old := *cfg
	t.Cleanup(func() { *cfg = old; resetRelayLogPipelineForTest() })
	cfg.Enabled = false
	relayLogAccepting.Store(true)

	var before int64
	require.NoError(t, db.Model(&Log{}).Count(&before).Error)

	continued := make(chan int, 1)
	oldHandler := relayLogAccountingHandler
	RegisterRelayLogAccountingHandler(func(_ RelayLogAccountingPayload, id int) { continued <- id })
	t.Cleanup(func() { RegisterRelayLogAccountingHandler(oldHandler) })

	require.True(t, enqueueAsyncRelayLog(relayLogKindConsume,
		&Log{RequestId: "async-disabled"}, &RelayLogAccountingPayload{Version: 1}, nil))

	// 1. 不入 buffer
	require.Empty(t, takeRelayLogBatch(relayLogKindConsume))
	// 2. 记账仍继续，且用兼容的 logID=0
	require.Equal(t, 0, <-continued)
	// 3. 没有发生任何写库——这是"不回退同步写入"的可执行定义
	var after int64
	require.NoError(t, db.Model(&Log{}).Count(&after).Error)
	require.Equal(t, before, after, "关闭开关后仍然写了日志库，说明退回了同步写入")
}

func TestTakeRelayLogBatchSwapDrainsWithoutLeavingSlice(t *testing.T) {
	resetRelayLogPipelineForTest()
	for i := 0; i < 3; i++ {
		require.True(t, enqueueRelayLog(relayLogKindConsume, &relayLogEvent{Log: &Log{Id: i + 1}}))
	}
	got := takeRelayLogBatch(relayLogKindConsume)
	require.Len(t, got, 3, "swap-and-drain takes ownership in O(1); DB sub-batching happens outside the lock")
	require.Empty(t, relayLogConsumeBuf)
}

func TestRelayLogHotCapacityDecreaseEvictsExistingExcess(t *testing.T) {
	oldFallbackDir := relayLogFallbackDir
	relayLogFallbackDir = t.TempDir()
	t.Cleanup(func() { relayLogFallbackDir = oldFallbackDir })
	cfg := operation_setting.GetRelayLogPipelineSetting()
	old := *cfg
	t.Cleanup(func() { *cfg = old; resetRelayLogPipelineForTest() })
	cfg.ConsumeBufMaxEntries = 3
	resetRelayLogPipelineForTest()
	for i := 0; i < 3; i++ {
		require.True(t, enqueueRelayLog(relayLogKindConsume, &relayLogEvent{Log: &Log{Id: i + 1}}))
	}
	cfg.ConsumeBufMaxEntries = 1
	require.True(t, enqueueRelayLog(relayLogKindConsume, &relayLogEvent{Log: &Log{Id: 4}}))
	require.Len(t, relayLogConsumeBuf, 3, "relay caller only rejects the new item in O(1)")
	appendRelayLogPending(relayLogKindConsume, takeRelayLogBatch(relayLogKindConsume))
	require.Len(t, relayLogPendingConsume, 1, "worker performs hot-shrink eviction")
	require.EqualValues(t, 1, relayLogDroppedConsume.Load())
	require.Eventually(t, func() bool { return relayLogFallbackTotal.Load() >= 3 }, time.Second, time.Millisecond)
}

func TestRelayLogContinuationRunsOnlyOnce(t *testing.T) {
	resetRelayLogPipelineForTest()
	done := make(chan int, 2)
	oldHandler := relayLogAccountingHandler
	RegisterRelayLogAccountingHandler(func(_ RelayLogAccountingPayload, id int) { done <- id })
	t.Cleanup(func() { RegisterRelayLogAccountingHandler(oldHandler) })
	event := &relayLogEvent{Log: &Log{}, Accounting: &RelayLogAccountingPayload{Version: 1}}
	dispatchRelayLogContinuation(event, 7)
	dispatchRelayLogContinuation(event, 9)
	require.Equal(t, 7, <-done)
	require.Never(t, func() bool { return len(done) != 0 }, 10*time.Millisecond, time.Millisecond)
}

func TestDropRelayLogShutdownRemainderClearsQueuesInBulk(t *testing.T) {
	resetRelayLogPipelineForTest()
	t.Cleanup(resetRelayLogPipelineForTest)
	relayLogConsumeBuf = make([]*relayLogEvent, 10_000)
	relayLogErrorBuf = make([]*relayLogEvent, 5_000)
	relayLogRetryBuf = make([]*relayLogEvent, 7_000)
	relayLogPendingConsume = make([]*relayLogEvent, 3_000)
	relayLogPendingError = make([]*relayLogEvent, 2_000)

	dropRelayLogShutdownRemainder()

	require.Nil(t, relayLogConsumeBuf)
	require.Nil(t, relayLogErrorBuf)
	require.Nil(t, relayLogRetryBuf)
	require.Nil(t, relayLogPendingConsume)
	require.Nil(t, relayLogPendingError)
	require.EqualValues(t, 27_000, relayLogFallbackErrors.Load())
}

func useRelayLogPipelineSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Log{}))
	previous := LOG_DB
	LOG_DB = db
	resetRelayLogPipelineForTest()
	t.Cleanup(func() {
		LOG_DB = previous
		resetRelayLogPipelineForTest()
	})
	return db
}

func TestPersistRelayLogEventsBatchAssignsIDsAndContinuesExactlyOnce(t *testing.T) {
	db := useRelayLogPipelineSQLite(t)
	continued := make(chan int, 2)
	previousHandler := relayLogAccountingHandler
	RegisterRelayLogAccountingHandler(func(_ RelayLogAccountingPayload, id int) { continued <- id })
	t.Cleanup(func() { RegisterRelayLogAccountingHandler(previousHandler) })

	events := []*relayLogEvent{
		{Log: &Log{RequestId: "batch-one", Type: LogTypeConsume}, Accounting: &RelayLogAccountingPayload{Version: 1}},
		{Log: &Log{RequestId: "batch-two", Type: LogTypeConsume}, Accounting: &RelayLogAccountingPayload{Version: 1}},
	}
	require.True(t, persistRelayLogEvents(events))

	var logs []Log
	require.NoError(t, db.Order("id ASC").Find(&logs).Error)
	require.Len(t, logs, 2)
	require.Positive(t, logs[0].Id)
	require.Positive(t, logs[1].Id)
	require.NotEqual(t, logs[0].Id, logs[1].Id)
	ids := []int{<-continued, <-continued}
	require.ElementsMatch(t, []int{logs[0].Id, logs[1].Id}, ids)
	require.EqualValues(t, 2, relayLogPersistedTotal.Load())
}

func TestPersistRelayLogEventsFailureQueuesRetryAndOpensCircuit(t *testing.T) {
	db := useRelayLogPipelineSQLite(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	retryCfg := operation_setting.GetRelayLogRetrySetting()
	previousCfg := *retryCfg
	retryCfg.CircuitFailureThreshold = 1
	t.Cleanup(func() { *retryCfg = previousCfg })

	event := &relayLogEvent{Log: &Log{RequestId: "failed-batch"}}
	require.False(t, persistRelayLogEvents([]*relayLogEvent{event}))
	require.True(t, relayLogCircuitOpen())
	retries := takeRelayLogRetryBatch(10)
	require.Len(t, retries, 1)
	require.Same(t, event, retries[0])
	require.Equal(t, 1, retries[0].RetryCount)
}

func TestFlushRelayLogsPersistsConsumeAndErrorBuffers(t *testing.T) {
	db := useRelayLogPipelineSQLite(t)
	cfg := operation_setting.GetRelayLogPipelineSetting()
	previousCfg := *cfg
	cfg.FullDrain = true
	cfg.OuterBatchSize = 2
	cfg.InnerBatchSize = 1
	t.Cleanup(func() { *cfg = previousCfg })

	require.True(t, enqueueRelayLog(relayLogKindConsume, &relayLogEvent{Log: &Log{RequestId: "consume-one"}}))
	require.True(t, enqueueRelayLog(relayLogKindConsume, &relayLogEvent{Log: &Log{RequestId: "consume-two"}}))
	require.True(t, enqueueRelayLog(relayLogKindError, &relayLogEvent{Log: &Log{RequestId: "error-one"}}))
	flushRelayLogs()

	var requestIDs []string
	require.NoError(t, db.Model(&Log{}).Order("request_id ASC").Pluck("request_id", &requestIDs).Error)
	require.Equal(t, []string{"consume-one", "consume-two", "error-one"}, requestIDs)
	require.Zero(t, relayLogBacklog())
}

func TestFlushRelayLogsMakesSameSecondConsumptionVisibleFirst(t *testing.T) {
	db := useRelayLogPipelineSQLite(t)
	cfg := operation_setting.GetRelayLogPipelineSetting()
	previousCfg := *cfg
	cfg.FullDrain = true
	cfg.OuterBatchSize = 100
	cfg.InnerBatchSize = 100
	t.Cleanup(func() { *cfg = previousCfg })

	createdAt := time.Now().Unix()
	require.True(t, enqueueRelayLog(relayLogKindConsume, &relayLogEvent{Log: &Log{
		RequestId: "consume-one", Type: LogTypeConsume, CreatedAt: createdAt,
	}}))
	require.True(t, enqueueRelayLog(relayLogKindConsume, &relayLogEvent{Log: &Log{
		RequestId: "consume-two", Type: LogTypeConsume, CreatedAt: createdAt,
	}}))
	require.True(t, enqueueRelayLog(relayLogKindError, &relayLogEvent{Log: &Log{
		RequestId: "error-one", Type: LogTypeError, CreatedAt: createdAt,
	}}))
	flushRelayLogs()

	var displayed []Log
	require.NoError(t, db.Order("created_at DESC, id DESC").Find(&displayed).Error)
	require.Len(t, displayed, 3)
	require.Equal(t, LogTypeConsume, displayed[0].Type,
		"the unchanged indexed list order must show consumption before the same-second error batch")
	require.Equal(t, LogTypeError, displayed[2].Type)
}

func TestFlushRelayLogRetriesPersistsEligibleAndFallsBackExhausted(t *testing.T) {
	db := useRelayLogPipelineSQLite(t)
	previousDir := relayLogFallbackDir
	relayLogFallbackDir = t.TempDir()
	t.Cleanup(func() { relayLogFallbackDir = previousDir })
	retryCfg := operation_setting.GetRelayLogRetrySetting()
	previousRetryCfg := *retryCfg
	retryCfg.MaxRetries = 2
	t.Cleanup(func() { *retryCfg = previousRetryCfg })

	eligible := &relayLogEvent{Log: &Log{RequestId: "retry-success"}}
	exhausted := &relayLogEvent{Log: &Log{RequestId: "retry-exhausted"}, RetryCount: 2}
	enqueueRelayLogRetry([]*relayLogEvent{eligible, exhausted})
	flushRelayLogRetries()

	var requestIDs []string
	require.NoError(t, db.Model(&Log{}).Pluck("request_id", &requestIDs).Error)
	require.Equal(t, []string{"retry-success"}, requestIDs)
	require.Eventually(t, func() bool { return relayLogFallbackTotal.Load() == 1 }, time.Second, time.Millisecond)
	require.Empty(t, takeRelayLogRetryBatch(10))
}

func TestEnqueueConsumeLogAndDrainRelayLogsSync(t *testing.T) {
	db := useRelayLogPipelineSQLite(t)
	previousLogConsume := common.LogConsumeEnabled
	previousDataExport := common.DataExportEnabled
	common.LogConsumeEnabled = true
	common.DataExportEnabled = false
	relayLogAccepting.Store(true)
	t.Cleanup(func() {
		common.LogConsumeEnabled = previousLogConsume
		common.DataExportEnabled = previousDataExport
	})

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("username", "batch-user")
	require.True(t, EnqueueConsumeLog(c, 42, RecordConsumeLogParams{
		ModelName: "batch-model", Quota: 7, PromptTokens: 2, CompletionTokens: 3,
	}, nil))
	DrainRelayLogsSync(time.Second)

	var log Log
	require.NoError(t, db.Where("user_id = ?", 42).First(&log).Error)
	require.Equal(t, "batch-user", log.Username)
	require.Equal(t, "batch-model", log.ModelName)
	require.Equal(t, 7, log.Quota)
}

func TestRelayLogConcurrentEnqueueBatchDrainHasNoLoss(t *testing.T) {
	db := useRelayLogPipelineSQLite(t)
	cfg := operation_setting.GetRelayLogPipelineSetting()
	previousCfg := *cfg
	cfg.ConsumeBufMaxEntries = 20_000
	cfg.FullDrain = true
	cfg.OuterBatchSize = 2_000
	cfg.InnerBatchSize = 500
	t.Cleanup(func() { *cfg = previousCfg })

	const workers = 10
	const perWorker = 200
	var wg sync.WaitGroup
	for worker := range workers {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for item := range perWorker {
				require.True(t, enqueueRelayLog(relayLogKindConsume, &relayLogEvent{
					Log: &Log{RequestId: fmt.Sprintf("batch-%02d-%03d", worker, item)},
				}))
			}
		}(worker)
	}
	wg.Wait()
	DrainRelayLogsSync(5 * time.Second)

	var count int64
	require.NoError(t, db.Model(&Log{}).Count(&count).Error)
	require.EqualValues(t, workers*perWorker, count)
	require.Zero(t, relayLogDroppedConsume.Load())
	require.Zero(t, relayLogBacklog())
}

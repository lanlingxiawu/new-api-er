package model

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 回归：relay-control D-5。设计文档 relay-log-async-batch.md §5 列出的 last_flush_items、
// last_flush_took_ms、oldest_event_age_ms 在状态接口中缺失。

// ageRelayLogBuffer 把某个入队缓冲中所有事件的入队时间改到 age 之前（测试专用，持锁修改）。
func ageRelayLogBuffer(kind relayLogKind, age time.Duration) {
	mu, buf, _, _ := bufferFor(kind)
	mu.Lock()
	defer mu.Unlock()
	for _, event := range *buf {
		event.EnqueuedAt = time.Now().Add(-age)
	}
}

func TestRelayLogStatusReportsLastFlushAndOldestAge(t *testing.T) {
	useRelayLogPipelineSQLite(t)
	useRelayLogFallbackDir(t)
	cfg := operation_setting.GetRelayLogPipelineSetting()
	old := *cfg
	t.Cleanup(func() { *cfg = old; resetRelayLogPipelineForTest() })
	cfg.FlushMaxPerCycle, cfg.FullDrain = 1, false
	resetRelayLogPipelineForTest()

	status := GetRelayLogPipelineStatus()
	require.Zero(t, status.OldestEventAgeMs, "empty pipeline")
	require.Zero(t, status.Consume.LastFlushItems, "no flush yet")

	for i := 0; i < 2; i++ {
		require.True(t, enqueueRelayLog(relayLogKindConsume, &relayLogEvent{Log: &Log{RequestId: "consume", Type: LogTypeConsume}}))
	}
	require.True(t, enqueueRelayLog(relayLogKindError, &relayLogEvent{Log: &Log{RequestId: "error", Type: LogTypeError}}))
	ageRelayLogBuffer(relayLogKindConsume, 3*time.Second)
	ageRelayLogBuffer(relayLogKindError, time.Second)

	status = GetRelayLogPipelineStatus()
	require.GreaterOrEqual(t, status.OldestEventAgeMs, int64(3000), "the oldest waiting event is the aged consume log")
	require.Less(t, status.OldestEventAgeMs, int64(60000))

	// 每轮只落 1 条：消费日志优先，其余留在 worker 的待写车道，年龄仍要从那里读出。
	flushRelayLogs()
	status = GetRelayLogPipelineStatus()
	require.EqualValues(t, 1, status.Consume.LastFlushItems)
	require.GreaterOrEqual(t, status.Consume.LastFlushTookMs, int64(0))
	require.Zero(t, status.Error.LastFlushItems, "no error log was written yet")
	require.GreaterOrEqual(t, status.OldestEventAgeMs, int64(3000), "the pending lane still holds a consume log enqueued 3s ago")

	flushRelayLogs() // 第二条消费日志
	flushRelayLogs() // 错误日志
	status = GetRelayLogPipelineStatus()
	require.EqualValues(t, 1, status.Consume.LastFlushItems, "an empty or error-only cycle does not overwrite the consume figure")
	require.EqualValues(t, 1, status.Error.LastFlushItems)
	require.Zero(t, status.OldestEventAgeMs, "nothing is waiting any more")
	require.EqualValues(t, 3, status.PersistedTotal)

	flushRelayLogs() // 空轮
	require.EqualValues(t, 1, GetRelayLogPipelineStatus().Consume.LastFlushItems)
}

func TestRelayLogStatusRetryQueueFlushAndAge(t *testing.T) {
	useRelayLogPipelineSQLite(t)
	useRelayLogFallbackDir(t)
	t.Cleanup(resetRelayLogPipelineForTest)
	resetRelayLogPipelineForTest()

	event := &relayLogEvent{Log: &Log{RequestId: "retry", Type: LogTypeConsume}, Kind: relayLogKindConsume, EnqueuedAt: time.Now().Add(-2 * time.Second)}
	enqueueRelayLogRetry([]*relayLogEvent{event})
	require.GreaterOrEqual(t, GetRelayLogPipelineStatus().OldestEventAgeMs, int64(2000), "retry queue waits count too")

	flushRelayLogRetries()
	status := GetRelayLogPipelineStatus()
	require.EqualValues(t, 1, status.Retry.LastFlushItems)
	require.Zero(t, status.OldestEventAgeMs)
}

func TestRelayLogOldestAgeMs(t *testing.T) {
	now := time.Unix(1000, 0)
	require.Zero(t, relayLogOldestAgeMs(now))
	require.Zero(t, relayLogOldestAgeMs(now, 0, 0))
	older := now.Add(-1500 * time.Millisecond).UnixNano()
	newer := now.Add(-200 * time.Millisecond).UnixNano()
	require.EqualValues(t, 1500, relayLogOldestAgeMs(now, 0, newer, older, 0))
	require.Zero(t, relayLogOldestAgeMs(now, now.Add(time.Second).UnixNano()), "clock skew never reports a negative age")
	require.Zero(t, relayLogEventsOldest(nil))
	require.Zero(t, relayLogEventsOldest([]*relayLogEvent{{}}), "an event without an enqueue time is not aged")
}

// 数据库故障期间 oldest_event_age_ms 不得低报：重试队列按失败先后追加（队首不一定最早），
// 正在落库、尚未返回的批次也仍在等待。重试批次连续失败两次后仍报告最早入队的那条。
func TestRelayLogStatusOldestAgeDuringOutage(t *testing.T) {
	db := useRelayLogPipelineSQLite(t)
	useRelayLogFallbackDir(t)
	retryCfg := operation_setting.GetRelayLogRetrySetting()
	oldRetry := *retryCfg
	t.Cleanup(func() { *retryCfg = oldRetry; resetRelayLogPipelineForTest() })
	retryCfg.MaxRetries, retryCfg.CircuitFailureThreshold = 10, 100

	var fail atomic.Bool
	fail.Store(true)
	block := make(chan struct{})
	var blocking atomic.Bool
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:relay_log_outage", func(tx *gorm.DB) {
		if blocking.Load() {
			<-block
		}
		if fail.Load() {
			_ = tx.AddError(errors.New("log database outage"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Create().Remove("test:relay_log_outage") })

	newer := &relayLogEvent{Log: &Log{RequestId: "newer", Type: LogTypeConsume}, Kind: relayLogKindConsume, EnqueuedAt: time.Now().Add(-time.Second)}
	older := &relayLogEvent{Log: &Log{RequestId: "older", Type: LogTypeConsume}, Kind: relayLogKindConsume, EnqueuedAt: time.Now().Add(-5 * time.Second)}
	enqueueRelayLogRetry([]*relayLogEvent{newer})
	enqueueRelayLogRetry([]*relayLogEvent{older}) // 后失败的更早入队事件排在队尾
	require.GreaterOrEqual(t, GetRelayLogPipelineStatus().OldestEventAgeMs, int64(5000))

	for round := 0; round < 2; round++ {
		flushRelayLogRetries()
		status := GetRelayLogPipelineStatus()
		require.Equal(t, 2, status.Retry.Backlog, "both events are queued for retry again")
		require.GreaterOrEqual(t, status.OldestEventAgeMs, int64(5000), "round %d", round)
	}

	// 落库进行中：事件已从重试队列取出，但仍算等待。
	blocking.Store(true)
	done := make(chan struct{})
	go func() { defer close(done); flushRelayLogRetries() }()
	require.Eventually(t, func() bool { return GetRelayLogPipelineStatus().Retry.Backlog == 0 }, time.Second, time.Millisecond)
	require.GreaterOrEqual(t, GetRelayLogPipelineStatus().OldestEventAgeMs, int64(5000), "the in-flight batch is still waiting")
	fail.Store(false)
	close(block)
	<-done
	require.Zero(t, GetRelayLogPipelineStatus().OldestEventAgeMs, "persisted at last")
}

// 一轮刷盘选中后分多个外层批次落库时，尚未轮到的批次同样仍算等待。
func TestRelayLogStatusOldestAgeCoversInFlightFlushCycle(t *testing.T) {
	db := useRelayLogPipelineSQLite(t)
	useRelayLogFallbackDir(t)
	t.Cleanup(resetRelayLogPipelineForTest)
	block := make(chan struct{})
	var blocking atomic.Bool
	blocking.Store(true)
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:relay_log_slow", func(*gorm.DB) {
		if blocking.Load() {
			<-block
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Create().Remove("test:relay_log_slow") })

	require.True(t, enqueueRelayLog(relayLogKindConsume, &relayLogEvent{Log: &Log{RequestId: "slow", Type: LogTypeConsume}}))
	ageRelayLogBuffer(relayLogKindConsume, 2*time.Second)
	done := make(chan struct{})
	go func() { defer close(done); flushRelayLogs() }()
	require.Eventually(t, func() bool { return GetRelayLogPipelineStatus().Consume.Backlog == 0 }, time.Second, time.Millisecond)
	require.GreaterOrEqual(t, GetRelayLogPipelineStatus().OldestEventAgeMs, int64(2000))
	blocking.Store(false)
	close(block)
	<-done
	require.Zero(t, GetRelayLogPipelineStatus().OldestEventAgeMs)
}

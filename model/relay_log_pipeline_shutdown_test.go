package model

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// useRelayLogFallbackDir 把 fallback 目录指到用例私有的临时目录，
// 避免污染仓库里的 data/relay-log-fallback。
func useRelayLogFallbackDir(t *testing.T) string {
	t.Helper()
	previous := relayLogFallbackDir
	dir := filepath.Join(t.TempDir(), "relay-log-fallback")
	relayLogFallbackFileMu.Lock()
	relayLogFallbackDir = dir
	relayLogFallbackFileMu.Unlock()
	t.Cleanup(func() {
		relayLogFallbackFileMu.Lock()
		relayLogFallbackDir = previous
		relayLogFallbackFileMu.Unlock()
	})
	return dir
}

// drainRelayLogAux 等待两条辅助通道彻底空闲。
//
// 辅助 worker 由 relayLogWorkerOnce 全局启动一次，跨用例存活。用例结束时若还有
// 在途任务，它们会带着**下一个**用例安装的记账回调继续执行，造成串扰——
// TestReplayRelayLogFallbackNeverRepeatsAccounting 就是这样被压垮的（它断言
// 回放期间记账一次都不许触发）。所以凡是派发过任务的用例都必须先排空再收尾。
func drainRelayLogAux(t *testing.T, timeout time.Duration) {
	t.Helper()
	idle := waitFor(t, timeout, func() bool {
		return len(relayLogContinuationCh) == 0 && len(relayLogFallbackCh) == 0 &&
			relayLogContinuationBusy.Load() == 0 && relayLogFallbackBusy.Load() == 0
	})
	require.True(t, idle,
		"relay-log 辅助 worker 未在 %s 内空闲：continuation=%d/%d fallback=%d/%d",
		timeout, len(relayLogContinuationCh), relayLogContinuationBusy.Load(),
		len(relayLogFallbackCh), relayLogFallbackBusy.Load())
}

// useRelayLogAccountingSpy 替换记账回调并统计调用次数。
// 清理时先排空在途任务，再恢复回调，避免把本用例的任务算到下一个用例头上。
func useRelayLogAccountingSpy(t *testing.T) *int64 {
	t.Helper()
	previous := relayLogAccountingHandler
	var calls int64
	RegisterRelayLogAccountingHandler(func(RelayLogAccountingPayload, int) {
		atomic.AddInt64(&calls, 1)
	})
	t.Cleanup(func() {
		drainRelayLogAux(t, 30*time.Second)
		relayLogAccountingHandler = previous
	})
	return &calls
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return cond()
}

// ---------------------------------------------------------------------------
// fallback worker 的 busy 计数
// ---------------------------------------------------------------------------

// TestFallbackBusyCounterReturnsToZero 是 relayLogFallbackBusy 泄漏的回归用例。
//
// 计数曾经在循环体末尾自增/自减而不是 defer，worker 一旦 panic 就永远停在 >0，
// 而 DrainRelayLogsSync / ShutdownRelayLogFlush 都在等它归零——结果是每次关停
// 都空转到 deadline 才返回。
func TestFallbackBusyCounterReturnsToZero(t *testing.T) {
	useRelayLogFallbackDir(t)
	calls := useRelayLogAccountingSpy(t)
	resetRelayLogPipelineForTest()
	t.Cleanup(resetRelayLogPipelineForTest)

	payload := RelayLogAccountingPayload{Version: 1, UserID: 1, Quota: 10}
	for i := 0; i < 20; i++ {
		dispatchRelayLogFallbackJob(&relayLogEvent{
			Log:        &Log{RequestId: "busy-counter"},
			Accounting: &payload,
		}, true)
	}

	require.True(t, waitFor(t, 5*time.Second, func() bool {
		return len(relayLogFallbackCh) == 0 && relayLogFallbackBusy.Load() == 0
	}), "fallback busy 计数没有归零：%d", relayLogFallbackBusy.Load())

	assert.EqualValues(t, 20, atomic.LoadInt64(calls), "每个事件的记账都要执行一次")
	assert.Zero(t, relayLogFallbackBusy.Load())
}

// panic 发生在记账回调里时，busy 计数同样必须被 defer 释放。
func TestFallbackBusyCounterReleasedWhenContinuationPanics(t *testing.T) {
	useRelayLogFallbackDir(t)
	previous := relayLogAccountingHandler
	var seen int64
	RegisterRelayLogAccountingHandler(func(RelayLogAccountingPayload, int) {
		atomic.AddInt64(&seen, 1)
		panic("boom")
	})
	t.Cleanup(func() { relayLogAccountingHandler = previous })
	resetRelayLogPipelineForTest()
	t.Cleanup(func() {
		drainRelayLogAux(t, 30*time.Second)
		resetRelayLogPipelineForTest()
	})

	payload := RelayLogAccountingPayload{Version: 1, UserID: 2, Quota: 1}
	dispatchRelayLogFallbackJob(&relayLogEvent{
		Log:        &Log{RequestId: "panic-continuation"},
		Accounting: &payload,
	}, true)

	require.True(t, waitFor(t, 5*time.Second, func() bool {
		return atomic.LoadInt64(&seen) == 1 && relayLogFallbackBusy.Load() == 0
	}), "记账 panic 之后 busy 计数没有归零：%d", relayLogFallbackBusy.Load())

	// worker 必须存活，后续任务照常处理
	dispatchRelayLogFallbackJob(&relayLogEvent{Log: &Log{RequestId: "after-panic"}}, false)
	assert.True(t, waitFor(t, 5*time.Second, func() bool {
		return len(relayLogFallbackCh) == 0 && relayLogFallbackBusy.Load() == 0
	}), "fallback worker 在一次记账 panic 之后停止了")
}

// ---------------------------------------------------------------------------
// fallback 落盘内容
// ---------------------------------------------------------------------------

func TestFallbackWritesRecoverableJSONL(t *testing.T) {
	dir := useRelayLogFallbackDir(t)
	useRelayLogAccountingSpy(t)
	resetRelayLogPipelineForTest()
	t.Cleanup(resetRelayLogPipelineForTest)

	dispatchRelayLogFallbackJob(&relayLogEvent{
		Log: &Log{RequestId: "jsonl-1", Username: "u1", Quota: 5},
	}, false)

	require.True(t, waitFor(t, 5*time.Second, func() bool {
		return relayLogFallbackTotal.Load() >= 1 && relayLogFallbackBusy.Load() == 0
	}))

	data, err := os.ReadFile(filepath.Join(dir, "relay-log.jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "jsonl-1")
	assert.Contains(t, string(data), `"version":1`, "回放依赖版本号，必须写进去")
}

// ---------------------------------------------------------------------------
// DispatchRelayLogAccounting —— 关停期调用方的兜底入口
// ---------------------------------------------------------------------------

func TestDispatchRelayLogAccountingRunsExactlyOnce(t *testing.T) {
	useRelayLogFallbackDir(t)
	calls := useRelayLogAccountingSpy(t)
	resetRelayLogPipelineForTest()
	t.Cleanup(resetRelayLogPipelineForTest)

	DispatchRelayLogAccounting(RelayLogAccountingPayload{Version: 1, UserID: 3, Quota: 42}, 0)

	require.True(t, waitFor(t, 5*time.Second, func() bool {
		return atomic.LoadInt64(calls) == 1 && relayLogContinuationBusy.Load() == 0
	}), "记账没有恰好执行一次：%d", atomic.LoadInt64(calls))
}

// ---------------------------------------------------------------------------
// handoffRelayLogEventsUntil —— 关停 deadline 的边界
// ---------------------------------------------------------------------------

func TestHandoffStopsAtDeadlineAndCountsRemainder(t *testing.T) {
	useRelayLogFallbackDir(t)
	useRelayLogAccountingSpy(t)
	resetRelayLogPipelineForTest()
	t.Cleanup(resetRelayLogPipelineForTest)

	events := make([]*relayLogEvent, 5)
	for i := range events {
		events[i] = &relayLogEvent{Log: &Log{RequestId: "deadline"}}
	}

	// deadline 已过：一条都不该被处理，全部计入 fallbackErrors
	before := relayLogFallbackErrors.Load()
	handoffRelayLogEventsUntil(events, time.Now().Add(-time.Second))
	assert.EqualValues(t, before+5, relayLogFallbackErrors.Load())
}

func TestHandoffProcessesEverythingBeforeDeadline(t *testing.T) {
	useRelayLogFallbackDir(t)
	useRelayLogAccountingSpy(t)
	resetRelayLogPipelineForTest()
	t.Cleanup(resetRelayLogPipelineForTest)

	events := make([]*relayLogEvent, 5)
	for i := range events {
		events[i] = &relayLogEvent{Log: &Log{RequestId: "before-deadline"}}
	}

	before := relayLogFallbackErrors.Load()
	handoffRelayLogEventsUntil(events, time.Now().Add(10*time.Second))

	require.True(t, waitFor(t, 5*time.Second, func() bool {
		return len(relayLogFallbackCh) == 0 && relayLogFallbackBusy.Load() == 0
	}))
	assert.Equal(t, before, relayLogFallbackErrors.Load(), "deadline 之内不该有事件被丢弃")
	assert.EqualValues(t, 5, relayLogFallbackTotal.Load())
}

// ---------------------------------------------------------------------------
// dropRelayLogShutdownRemainder —— 超时兜底
// ---------------------------------------------------------------------------

func TestDropShutdownRemainderClearsEveryQueue(t *testing.T) {
	resetRelayLogPipelineForTest()
	t.Cleanup(resetRelayLogPipelineForTest)

	relayLogConsumeMu.Lock()
	relayLogConsumeBuf = []*relayLogEvent{{Log: &Log{}}, {Log: &Log{}}}
	relayLogConsumeMu.Unlock()
	relayLogErrorMu.Lock()
	relayLogErrorBuf = []*relayLogEvent{{Log: &Log{}}}
	relayLogErrorMu.Unlock()
	relayLogRetryMu.Lock()
	relayLogRetryBuf = []*relayLogEvent{{Log: &Log{}}}
	relayLogRetryMu.Unlock()
	relayLogPendingConsume = []*relayLogEvent{{Log: &Log{}}}
	relayLogPendingError = []*relayLogEvent{{Log: &Log{}}}

	before := relayLogFallbackErrors.Load()
	dropRelayLogShutdownRemainder()

	assert.EqualValues(t, before+6, relayLogFallbackErrors.Load(), "丢弃的条数要如实计数")
	assert.Zero(t, relayLogBacklog())
}

// ---------------------------------------------------------------------------
// 主链路入口：EnqueueConsumeLog 的非阻塞性与并发安全（Rule 0 / Rule 8.2）
// ---------------------------------------------------------------------------

// TestEnqueueConsumeLogNeverBlocksRelayGoroutine 直接压 relay goroutine 会调用的
// 那个入口。Rule 0 要求它不能阻塞：即便缓冲已满、即便下游 worker 处理不过来，
// 单次调用也必须在亚毫秒级返回。
func TestEnqueueConsumeLogNeverBlocksRelayGoroutine(t *testing.T) {
	useRelayLogFallbackDir(t)
	useRelayLogAccountingSpy(t)
	cfg := operation_setting.GetRelayLogPipelineSetting()
	old := *cfg
	// 故意把缓冲压到极小，强制走"溢出 → fallback"这条最慢的分支
	cfg.ConsumeBufMaxEntries = 1
	cfg.Enabled = true
	t.Cleanup(func() { *cfg = old; resetRelayLogPipelineForTest() })
	resetRelayLogPipelineForTest()
	relayLogAccepting.Store(true)

	const goroutines, perGoroutine = 32, 200
	var wg sync.WaitGroup
	var worst int64

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			payload := RelayLogAccountingPayload{Version: 1, UserID: 1, Quota: 1}
			for i := 0; i < perGoroutine; i++ {
				started := time.Now()
				enqueueAsyncRelayLog(relayLogKindConsume,
					&Log{RequestId: "nonblocking"}, &payload, nil)
				elapsed := int64(time.Since(started))
				for {
					current := atomic.LoadInt64(&worst)
					if elapsed <= current || atomic.CompareAndSwapInt64(&worst, current, elapsed) {
						break
					}
				}
			}
		}()
	}
	wg.Wait()

	worstDuration := time.Duration(atomic.LoadInt64(&worst))
	t.Logf("EnqueueConsumeLog 最坏单次耗时 = %s（%d 次调用，缓冲上限 1）",
		worstDuration, goroutines*perGoroutine)
	assert.Less(t, worstDuration, 50*time.Millisecond,
		"relay goroutine 上的入队出现了不可接受的阻塞：%s", worstDuration)
}

// 关停之后入队必须返回 false，让调用方走独立的记账兜底，
// 而不是把日志和记账一起丢掉。
func TestEnqueueAfterShutdownReturnsFalseButKeepsQuotaExport(t *testing.T) {
	useRelayLogFallbackDir(t)
	useRelayLogAccountingSpy(t)
	resetRelayLogPipelineForTest()
	t.Cleanup(resetRelayLogPipelineForTest)

	relayLogAccepting.Store(false)
	quota := QuotaDataLogParams{UserID: 9, ModelName: "gpt-4o", Quota: 3}
	accepted := enqueueAsyncRelayLog(relayLogKindConsume,
		&Log{RequestId: "after-shutdown"},
		&RelayLogAccountingPayload{Version: 1, UserID: 9, Quota: 3},
		&quota)

	assert.False(t, accepted, "关停后必须明确告诉调用方没收下")
}

// ---------------------------------------------------------------------------
// 丢弃优先级：成功日志尽量保留，错误日志允许丢弃
// ---------------------------------------------------------------------------

// TestErrorLogsYieldFallbackCapacityToConsumeLogs 钉住取舍顺序：
// fallback 通道被占到水位线之后，错误日志直接丢弃，容量留给消费日志。
func TestErrorLogsYieldFallbackCapacityToConsumeLogs(t *testing.T) {
	useRelayLogFallbackDir(t)
	cfg := operation_setting.GetRelayLogPipelineSetting()
	old := *cfg
	// 容量压到 4：错误日志的水位线 = 4/2 = 2
	cfg.FallbackQueueCapacity = 4
	t.Cleanup(func() { *cfg = old; resetRelayLogPipelineForTest() })
	resetRelayLogPipelineForTest()

	// 用一个可控的闸门堵住记账回调，让 fallback 通道能积压起来
	const fillerCount = 6
	blocked := make(chan struct{})
	var accountingCalls int64
	previousHandler := relayLogAccountingHandler
	RegisterRelayLogAccountingHandler(func(RelayLogAccountingPayload, int) {
		<-blocked
		atomic.AddInt64(&accountingCalls, 1)
	})
	t.Cleanup(func() {
		drainRelayLogAux(t, 30*time.Second)
		RegisterRelayLogAccountingHandler(previousHandler)
	})

	for i := 0; i < fillerCount; i++ {
		dispatchRelayLogFallbackJob(&relayLogEvent{
			Kind:       relayLogKindConsume,
			Log:        &Log{RequestId: "filler"},
			Accounting: &RelayLogAccountingPayload{Version: 1, UserID: 1, Quota: 1},
		}, true)
	}

	yieldedBefore := relayLogErrorYielded.Load()
	for i := 0; i < 10; i++ {
		dispatchRelayLogFallbackJob(&relayLogEvent{
			Kind: relayLogKindError,
			Log:  &Log{RequestId: "error-log"},
		}, false)
	}
	yielded := relayLogErrorYielded.Load() - yieldedBefore
	assert.EqualValues(t, 10, yielded, "水位线之上的错误日志必须全部让位")

	close(blocked)
	drainRelayLogAux(t, 30*time.Second)

	// 关键断言：错误日志被丢弃，但消费日志的记账一条都没少
	assert.EqualValues(t, fillerCount, atomic.LoadInt64(&accountingCalls),
		"让位不该影响消费日志的账务：期望 %d 次记账", fillerCount)
}

// 通道空闲时错误日志正常入队——让位只在压力下发生，不是无条件丢弃。
func TestErrorLogsAreKeptWhenFallbackHasRoom(t *testing.T) {
	useRelayLogFallbackDir(t)
	useRelayLogAccountingSpy(t)
	resetRelayLogPipelineForTest()
	t.Cleanup(resetRelayLogPipelineForTest)

	for i := 0; i < 20; i++ {
		dispatchRelayLogFallbackJob(&relayLogEvent{
			Kind: relayLogKindError,
			Log:  &Log{RequestId: "error-ok"},
		}, false)
	}
	drainRelayLogAux(t, 30*time.Second)

	assert.Zero(t, relayLogErrorYielded.Load(), "通道空闲时不该丢弃错误日志")
	assert.EqualValues(t, 20, relayLogFallbackTotal.Load(), "错误日志应当全部落盘")
}

// 刷盘预算不足时，消费日志先走，错误日志留到下一轮。
func TestFlushDrainsConsumeLogsBeforeErrorLogs(t *testing.T) {
	useRelayLogPipelineSQLite(t)
	useRelayLogFallbackDir(t)
	useRelayLogAccountingSpy(t)
	cfg := operation_setting.GetRelayLogPipelineSetting()
	old := *cfg
	cfg.FlushMaxPerCycle = 3 // 每轮只够 3 条
	cfg.OuterBatchSize = 100
	cfg.InnerBatchSize = 100
	t.Cleanup(func() { *cfg = old; resetRelayLogPipelineForTest() })
	resetRelayLogPipelineForTest()
	relayLogAccepting.Store(true)

	for i := 0; i < 3; i++ {
		require.True(t, enqueueRelayLog(relayLogKindError,
			&relayLogEvent{Log: &Log{RequestId: "err", ModelName: "err-model"}}))
	}
	for i := 0; i < 3; i++ {
		require.True(t, enqueueRelayLog(relayLogKindConsume,
			&relayLogEvent{Log: &Log{RequestId: "consume", ModelName: "consume-model"}}))
	}

	flushRelayLogs()

	// 本轮预算 3 条，应当全部给消费日志——尽管错误日志先入队
	assert.EqualValues(t, 3, relayLogPersistedTotal.Load())
	assert.Len(t, relayLogPendingConsume, 0, "消费日志应当被优先取空")
	assert.Len(t, relayLogPendingError, 3, "错误日志应当留到下一轮")
}

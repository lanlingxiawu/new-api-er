package model

import (
	"fmt"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 主链路压测
//
// 这里压的是 relay goroutine 真正调用的那个入口（EnqueueConsumeLog），
// 后台刷盘 worker 同时在向日志库写。要证明的是 Rule 0 的两条：
//   1. 入队永不阻塞 relay —— 用分位数而不是平均值衡量，平均值会掩盖长尾；
//   2. 日志库故障/变慢时，relay 侧的耗时不受传染。
//
// 规模默认取能在常规 go test 里跑完的量；压更大的量用
// RELAY_LOG_STRESS_EVENTS=200000 go test -run Stress ./model/
// ---------------------------------------------------------------------------

func stressEventCount(t *testing.T) int {
	t.Helper()
	if raw := os.Getenv("RELAY_LOG_STRESS_EVENTS"); raw != "" {
		n, err := strconv.Atoi(raw)
		require.NoError(t, err)
		return n
	}
	return 50_000
}

type latencyRecorder struct {
	mu      sync.Mutex
	samples []time.Duration
}

func (r *latencyRecorder) add(d time.Duration) {
	r.mu.Lock()
	r.samples = append(r.samples, d)
	r.mu.Unlock()
}

func (r *latencyRecorder) percentile(p float64) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.samples) == 0 {
		return 0
	}
	sort.Slice(r.samples, func(a, b int) bool { return r.samples[a] < r.samples[b] })
	index := int(float64(len(r.samples)-1) * p)
	return r.samples[index]
}

func (r *latencyRecorder) report(t *testing.T, label string, total int, elapsed time.Duration) {
	t.Helper()
	t.Logf("%s：%d 次调用 / %s，吞吐 ≈ %.0f 次每秒", label, total, elapsed.Round(time.Millisecond),
		float64(total)/elapsed.Seconds())
	t.Logf("%s：p50=%s p90=%s p99=%s p999=%s max=%s", label,
		r.percentile(0.50), r.percentile(0.90), r.percentile(0.99),
		r.percentile(0.999), r.percentile(1.0))
}

// useRelayLogStressEnv 准备一次压测所需的全部全局状态，并保证退出时复原。
func useRelayLogStressEnv(t *testing.T, mutate func(*operation_setting.RelayLogPipelineSetting)) {
	t.Helper()
	useRelayLogFallbackDir(t)

	cfg := operation_setting.GetRelayLogPipelineSetting()
	previousCfg := *cfg
	previousLogConsume := common.LogConsumeEnabled
	previousDataExport := common.DataExportEnabled

	cfg.Enabled = true
	if mutate != nil {
		mutate(cfg)
	}
	common.LogConsumeEnabled = true
	common.DataExportEnabled = false

	resetRelayLogPipelineForTest()
	relayLogAccepting.Store(true)

	t.Cleanup(func() {
		drainRelayLogAux(t, 60*time.Second)
		*cfg = previousCfg
		common.LogConsumeEnabled = previousLogConsume
		common.DataExportEnabled = previousDataExport
		resetRelayLogPipelineForTest()
	})
}

// TestStressRelayLogEnqueueUnderConcurrency 是主链路的核心压测：
// 高并发入队 + 后台真实落库，断言入队分位数和"零丢失记账"。
func TestStressRelayLogEnqueueUnderConcurrency(t *testing.T) {
	db := useRelayLogPipelineSQLite(t)
	useRelayLogStressEnv(t, func(cfg *operation_setting.RelayLogPipelineSetting) {
		cfg.ConsumeBufMaxEntries = 20_000
		cfg.OuterBatchSize = 2_000
		cfg.InnerBatchSize = 500
		cfg.FlushMaxPerCycle = 20_000
	})
	accounting := useRelayLogAccountingSpy(t)

	total := stressEventCount(t)
	workers := 64
	perWorker := total / workers
	total = workers * perWorker

	latency := &latencyRecorder{}
	var wg sync.WaitGroup
	started := time.Now()

	// 后台刷盘与入队并行进行，模拟真实运行时的争用
	stopFlush := make(chan struct{})
	var flushWG sync.WaitGroup
	flushWG.Add(1)
	go func() {
		defer flushWG.Done()
		for {
			select {
			case <-stopFlush:
				return
			default:
			}
			relayLogFlushMu.Lock()
			flushRelayLogs()
			relayLogFlushMu.Unlock()
			time.Sleep(time.Millisecond)
		}
	}()

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Set("username", fmt.Sprintf("stress-%d", worker))
			payload := RelayLogAccountingPayload{Version: 1, UserID: worker, Quota: 1}
			for i := 0; i < perWorker; i++ {
				callStart := time.Now()
				EnqueueConsumeLog(ctx, worker, RecordConsumeLogParams{
					ModelName:        "stress-model",
					Quota:            1,
					PromptTokens:     1,
					CompletionTokens: 1,
				}, &payload)
				latency.add(time.Since(callStart))
			}
		}(w)
	}
	wg.Wait()
	elapsed := time.Since(started)
	close(stopFlush)
	flushWG.Wait()

	DrainRelayLogsSync(60 * time.Second)
	drainRelayLogAux(t, 60*time.Second)

	latency.report(t, "EnqueueConsumeLog", total, elapsed)

	// --- Rule 0：入队不能阻塞 relay ---
	p99 := latency.percentile(0.99)
	assert.Less(t, p99, 2*time.Millisecond, "入队 p99 过高，relay goroutine 被拖慢了：%s", p99)

	// --- 日志行的去向必须构成一个精确划分 ---
	//
	// 三个去向互斥且穷尽：落库 / 写进 fallback 文件 / 连 fallback 都没写成（计入
	// fallbackErrors）。注意 droppedConsume 不属于这个划分——被缓冲拒收的事件随后
	// 会进 fallback，两者是重叠的，把它算进来会掩盖真正的静默丢失。
	var persistedRows int64
	require.NoError(t, db.Model(&Log{}).Where("model_name = ?", "stress-model").Count(&persistedRows).Error)
	fallbackWritten := relayLogFallbackTotal.Load()
	fallbackFailed := relayLogFallbackErrors.Load()
	rejectedByBuffer := relayLogDroppedConsume.Load()

	accountedFor := uint64(persistedRows) + fallbackWritten + fallbackFailed
	t.Logf("日志行去向：落库 %d / fallback 落盘 %d / fallback 也失败 %d = %d，总投递 %d",
		persistedRows, fallbackWritten, fallbackFailed, accountedFor, total)
	t.Logf("（其中被缓冲拒收后转投 fallback 的有 %d 条，与上面两项重叠，不计入划分）", rejectedByBuffer)

	assert.GreaterOrEqual(t, accountedFor, uint64(total),
		"有 %d 条日志既没落库、没进 fallback、也没被计为 fallback 失败——那就是静默丢失",
		uint64(total)-accountedFor)

	// --- 记账必须每条恰好一次 ---
	assert.EqualValues(t, total, atomic.LoadInt64(accounting),
		"记账次数与投递数不一致，会直接导致成本/提成账目错乱")
}

// TestStressRelayLogSurvivesLogDatabaseOutage 证明日志库挂掉不会传染给 relay。
// 这是 Rule 0 里"新子系统的失败必须静默"的可执行版本。
func TestStressRelayLogSurvivesLogDatabaseOutage(t *testing.T) {
	db := useRelayLogPipelineSQLite(t)
	useRelayLogStressEnv(t, func(cfg *operation_setting.RelayLogPipelineSetting) {
		cfg.ConsumeBufMaxEntries = 5_000
		cfg.WriteTimeoutSec = 1
	})
	useRelayLogAccountingSpy(t)

	// 关掉底层连接，制造"日志库不可用"
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	latency := &latencyRecorder{}
	const workers, perWorker = 32, 400
	var wg sync.WaitGroup
	started := time.Now()

	stopFlush := make(chan struct{})
	var flushWG sync.WaitGroup
	flushWG.Add(1)
	go func() {
		defer flushWG.Done()
		for {
			select {
			case <-stopFlush:
				return
			default:
			}
			if relayLogFlushMu.TryLock() {
				runRelayLogWorkerCycle("stress-flush", flushRelayLogs)
				relayLogFlushMu.Unlock()
			}
			time.Sleep(time.Millisecond)
		}
	}()

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Set("username", "outage")
			payload := RelayLogAccountingPayload{Version: 1, UserID: worker, Quota: 1}
			for i := 0; i < perWorker; i++ {
				callStart := time.Now()
				EnqueueConsumeLog(ctx, worker, RecordConsumeLogParams{
					ModelName: "outage-model", Quota: 1,
				}, &payload)
				latency.add(time.Since(callStart))
			}
		}(w)
	}
	wg.Wait()
	elapsed := time.Since(started)
	close(stopFlush)
	flushWG.Wait()

	latency.report(t, "日志库不可用时的 EnqueueConsumeLog", workers*perWorker, elapsed)

	p99 := latency.percentile(0.99)
	assert.Less(t, p99, 5*time.Millisecond,
		"日志库故障传染到了 relay 入队路径：p99=%s", p99)

	// 熔断必须已经张开，否则每个刷盘周期都会继续打满一个失败的连接
	assert.True(t, relayLogCircuitOpen() || relayLogCircuitFailures.Load() > 0,
		"日志库持续失败却没有触发熔断计数")
}

// TestStressFallbackLaneOutpacesDatabaseLane 固化一条设计不变式：
// fallback 是落库通路的兜底，它必须比被兜的通路更快。
//
// 最初实现对每条日志各做一次 MkdirAll + Stat + OpenFile + Write + Close，
// 实测只有 11,500 条每秒，而真实 PostgreSQL 的批量落库有 33,000 行每秒——
// 兜底比主通路还慢 3 倍，主通路一顶不住兜底就跟着崩，日志行只能丢。
// 改为按批合并写入后是 50 万条每秒以上。
//
// 阈值取 50,000：既远高于逐条写的 11,500（能拦住退化），
// 又给慢速 CI 磁盘留了 10 倍余量。
func TestStressFallbackLaneOutpacesDatabaseLane(t *testing.T) {
	useRelayLogFallbackDir(t)
	useRelayLogAccountingSpy(t)
	resetRelayLogPipelineForTest()
	t.Cleanup(resetRelayLogPipelineForTest)

	const events = 5_000
	started := time.Now()
	for i := 0; i < events; i++ {
		dispatchRelayLogFallbackJob(&relayLogEvent{
			Log: &Log{RequestId: fmt.Sprintf("lane-%d", i), ModelName: "lane-model"},
		}, false)
	}
	require.True(t, waitFor(t, 120*time.Second, func() bool {
		return len(relayLogFallbackCh) == 0 && relayLogFallbackBusy.Load() == 0
	}), "fallback 通路没有排空")
	elapsed := time.Since(started)

	throughput := float64(events) / elapsed.Seconds()
	t.Logf("fallback 通路吞吐 = %.0f 条每秒（%d 条 / %s）",
		throughput, events, elapsed.Round(time.Millisecond))
	assert.EqualValues(t, events, relayLogFallbackTotal.Load(), "所有条目都必须真的落盘")
	assert.Zero(t, relayLogFallbackErrors.Load())
	assert.Greater(t, throughput, 50_000.0,
		"fallback 通路退化到逐条系统调用了：%.0f 条每秒", throughput)
}

// TestStressRelayLogBufferOverflowStaysNonBlocking 压的是最坏分支：
// 缓冲被打满，每条事件都要走 fallback。这条路径涉及文件写和全局锁，
// 是最容易把阻塞泄漏回 relay 的地方。
func TestStressRelayLogBufferOverflowStaysNonBlocking(t *testing.T) {
	useRelayLogPipelineSQLite(t)
	useRelayLogStressEnv(t, func(cfg *operation_setting.RelayLogPipelineSetting) {
		cfg.ConsumeBufMaxEntries = 10 // 立刻溢出
	})
	useRelayLogAccountingSpy(t)

	latency := &latencyRecorder{}
	const workers, perWorker = 32, 300
	var wg sync.WaitGroup
	started := time.Now()

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Set("username", "overflow")
			payload := RelayLogAccountingPayload{Version: 1, UserID: worker, Quota: 1}
			for i := 0; i < perWorker; i++ {
				callStart := time.Now()
				EnqueueConsumeLog(ctx, worker, RecordConsumeLogParams{
					ModelName: "overflow-model", Quota: 1,
				}, &payload)
				latency.add(time.Since(callStart))
			}
		}(w)
	}
	wg.Wait()
	elapsed := time.Since(started)

	latency.report(t, "缓冲溢出时的 EnqueueConsumeLog", workers*perWorker, elapsed)

	p99 := latency.percentile(0.99)
	assert.Less(t, p99, 5*time.Millisecond, "溢出分支阻塞了 relay：p99=%s", p99)
	assert.Positive(t, relayLogDroppedConsume.Load(), "缓冲上限为 10 时必然发生溢出")
}

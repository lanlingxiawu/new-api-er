package middleware

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 写入侧的有界性回归。
//
// 请求日志是尽力而为的可观测性数据，过载时必须 DROP，绝不能阻塞 relay goroutine
// 或无界增长（早期"每条一个 goroutine"的实现在 c=500 时涨到 17 万 goroutine /
// 1.5 GB 堆）。改为磁盘存储后同一个约束还多一层含义：并发写入不得让文件句柄数
// 与 RPM 同阶——句柄上限等于 worker 数，与队列深度和请求量无关。

// withFullQueue 装上一个已经塞满的队列，让下一次投递必定走丢弃分支。
func withFullQueue(t *testing.T, depth int) *requestLogQueueHandle {
	t.Helper()
	prev := requestLogQueue.Load()
	handle := &requestLogQueueHandle{
		ch:   make(chan requestLogTask, depth),
		done: make(chan struct{}),
	}
	for i := 0; i < depth; i++ {
		handle.ch <- requestLogTask{}
	}
	requestLogQueue.Store(handle)
	requestLogDropped.Store(0)
	t.Cleanup(func() {
		requestLogQueue.Store(prev)
		requestLogDropped.Store(0)
	})
	return handle
}

func TestEnqueueRequestLog_DropsWhenQueueFull(t *testing.T) {
	handle := withFullQueue(t, 1)

	done := make(chan struct{})
	go func() {
		enqueueRequestLog(requestLogTask{entry: &model.RequestLog{}}) // 必须命中 default 分支
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("enqueueRequestLog blocked when the queue was full — it must drop, never block the relay goroutine")
	}
	assert.Equal(t, int64(1), requestLogDropped.Load(), "a full queue must drop the log and increment the counter")
	assert.Equal(t, 1, len(handle.ch), "the queued entry must be untouched by a dropped enqueue")
}

// 未启动 worker（或已关停）时投递必须安静地丢弃，不 panic、不计入过载丢弃。
func TestEnqueueRequestLog_NoQueueIsNoop(t *testing.T) {
	prev := requestLogQueue.Load()
	requestLogQueue.Store(nil)
	requestLogDropped.Store(0)
	t.Cleanup(func() {
		requestLogQueue.Store(prev)
		requestLogDropped.Store(0)
	})

	require.NotPanics(t, func() {
		enqueueRequestLog(requestLogTask{entry: &model.RequestLog{}})
	})
	assert.Zero(t, requestLogDropped.Load())
}

// 队列深度与 worker 数来自 InitRequestLogStore 解析的环境变量。
// 关键点：这些值必须在 godotenv.Load 之后读——包级变量在 main() 之前求值，
// 那时 .env 还没进环境，配置会被静默忽略（REQUEST_LOG_MAX_INFLIGHT 曾经如此）。
func TestStartRequestLogWriters_UsesRuntimeConfig(t *testing.T) {
	t.Setenv("REQUEST_LOG_DIR", t.TempDir())
	t.Setenv("REQUEST_LOG_SWEEP_INTERVAL_SEC", "0")
	t.Setenv("REQUEST_LOG_MAX_INFLIGHT", "7")
	t.Setenv("REQUEST_LOG_WRITERS", "3")
	model.InitRequestLogStore()

	StartRequestLogWriters()
	t.Cleanup(StopRequestLogWriters)

	handle := requestLogQueue.Load()
	require.NotNil(t, handle)
	assert.Equal(t, 7, cap(handle.ch), "queue depth must come from REQUEST_LOG_MAX_INFLIGHT")
	assert.Equal(t, 3, model.RequestLogWriterCount())
}

// 重复启动必须幂等：旧 worker 停掉，新队列生效。
func TestStartRequestLogWriters_Restartable(t *testing.T) {
	t.Setenv("REQUEST_LOG_DIR", t.TempDir())
	t.Setenv("REQUEST_LOG_SWEEP_INTERVAL_SEC", "0")
	model.InitRequestLogStore()

	StartRequestLogWriters()
	first := requestLogQueue.Load()
	StartRequestLogWriters()
	second := requestLogQueue.Load()
	t.Cleanup(StopRequestLogWriters)

	require.NotNil(t, first)
	require.NotNil(t, second)
	assert.NotSame(t, first, second, "restart must install a fresh queue")

	StopRequestLogWriters()
	assert.Nil(t, requestLogQueue.Load())
}

// worker 停掉之后队列里还剩的任务永远不会落盘。它们必须记进丢弃计数，
// 而不是连同 pending 一起被无声吞掉。
func TestStopRequestLogWriters_CountsResidualEntriesAsDropped(t *testing.T) {
	t.Setenv("REQUEST_LOG_DIR", t.TempDir())
	t.Setenv("REQUEST_LOG_SWEEP_INTERVAL_SEC", "0")
	t.Setenv("REQUEST_LOG_MAX_INFLIGHT", "16")
	t.Setenv("REQUEST_LOG_WRITERS", "1")
	model.InitRequestLogStore()
	StartRequestLogWriters()
	t.Cleanup(func() {
		StopRequestLogWriters()
		_, _ = model.ClearAllRequestLogs()
	})

	handle := requestLogQueue.Load()
	require.NotNil(t, handle)
	StopRequestLogWriters()
	require.Zero(t, handle.pending.Load(), "the normal path must leave nothing behind")

	// 模拟关停竞态：enqueueRequestLog 取到 handle 之后，worker 才退出，
	// 于是任务进了队列却再也没人消费。
	before := requestLogDropped.Load()
	handle.pending.Add(1)
	handle.ch <- requestLogTask{entry: &model.RequestLog{Username: "residual", CreatedAt: 1}}
	drainResidualRequestLogTasks(handle)

	assert.Equal(t, before+1, requestLogDropped.Load(), "residual entries must be counted as dropped")
	assert.Zero(t, handle.pending.Load(), "pending must not leak after the workers are gone")
}

// 关停时先排空队列，随后的索引快照才是完整的。
func TestDrainRequestLogQueue(t *testing.T) {
	t.Setenv("REQUEST_LOG_DIR", t.TempDir())
	t.Setenv("REQUEST_LOG_SWEEP_INTERVAL_SEC", "0")
	t.Setenv("REQUEST_LOG_WRITERS", "2")
	model.InitRequestLogStore()
	_, _ = model.ClearAllRequestLogs()
	StartRequestLogWriters()
	t.Cleanup(func() {
		StopRequestLogWriters()
		_, _ = model.ClearAllRequestLogs()
	})

	const entries = 50
	for i := 0; i < entries; i++ {
		enqueueRequestLog(requestLogTask{entry: &model.RequestLog{Username: "drain", CreatedAt: int64(1000 + i)}})
	}
	assert.Zero(t, DrainRequestLogQueue(5*time.Second), "everything queued must be written within the budget")

	_, total, err := model.GetAllRequestLogs("drain", "", 0, "", 0, 0, 0, 0, 1)
	require.NoError(t, err)
	assert.EqualValues(t, entries, total)
}

// 没有队列时排空是 no-op。
func TestDrainRequestLogQueue_NoQueue(t *testing.T) {
	prev := requestLogQueue.Load()
	requestLogQueue.Store(nil)
	t.Cleanup(func() { requestLogQueue.Store(prev) })
	assert.Zero(t, DrainRequestLogQueue(time.Second))
}

// worker 内部出错不得终结 worker：后续条目仍要被处理。
func TestRunRequestLogTask_RecoversPanic(t *testing.T) {
	require.NotPanics(t, func() {
		runRequestLogTask(requestLogTask{entry: nil})
	})
}

// 被丢弃的条目必须把记账退回，否则关停排空会永远等一个不存在的在途条目。
func TestEnqueueRequestLog_DropDoesNotLeakPending(t *testing.T) {
	handle := withFullQueue(t, 1)
	before := handle.pending.Load()

	enqueueRequestLog(requestLogTask{entry: &model.RequestLog{}})

	assert.Equal(t, before, handle.pending.Load(), "a dropped enqueue must not leave pending work behind")
	assert.Zero(t, DrainRequestLogQueue(200*time.Millisecond), "drain must not hang on a dropped entry")
}

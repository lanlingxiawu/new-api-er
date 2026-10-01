package model

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

type relayLogKind uint8

var errRelayLogFallbackRetentionFull = errors.New("relay log fallback retention cap reached")

var (
	ErrRelayLogReplayRunning      = errors.New("relay log fallback replay is already running")
	ErrRelayLogReplayShuttingDown = errors.New("relay log pipeline is shutting down")
)

const (
	relayLogReplayErrorFailed     = "relay_log_replay_failed"
	relayLogReplayErrorShutdown   = "relay_log_replay_shutdown"
	relayLogReplayErrorRecovery   = "relay_log_replay_file_recovery_failed"
	relayLogReplayErrorUnexpected = "relay_log_replay_unexpected"
)

const (
	relayLogKindConsume relayLogKind = iota
	relayLogKindError
)

// relayLogFallbackBatchMax 是 fallback worker 一次合并写入的最大条数。
// 取 512 是因为再往上单批的收益已经很小，而单批过大时一次写失败会同时报废更多
// 日志行；256KB 的写缓冲也刚好覆盖这个量级。
const relayLogFallbackBatchMax = 512

// relayLogErrorFallbackDivisor 限定错误日志最多占用 fallback 通道的 1/N。
//
// 两类日志的价值不对等：消费日志关联计费与对账，丢了就对不上账；错误日志只用于
// 诊断，而且**从不携带记账负载**（RecordErrorLog 传入的 accounting/quotaData 均为
// nil），丢弃它零账务后果。所以压力下先牺牲错误日志，把容量留给消费日志。
//
// 取 1/2 而不是更小：正常运行时错误日志量远小于消费日志，这条水位线根本碰不到；
// 一旦碰到就说明已经在异常状态，此时保住一半容量给消费日志足够。
const relayLogErrorFallbackDivisor = 2

type relayLogEvent struct {
	Log *Log
	// Kind 决定压力下的取舍顺序，见 relayLogErrorFallbackDivisor。
	// 由 enqueueRelayLog 统一写入，因此重试、pending 溢出、关停移交等
	// 后续环节都能沿用同一判定，不必各自再传一次种类。
	Kind             relayLogKind
	RetryCount       int
	EnqueuedAt       time.Time
	Accounting       *RelayLogAccountingPayload
	QuotaData        *QuotaDataLogParams
	continuationOnce sync.Once
}

// RelayLogAccountingPayload is deliberately versioned and primitive-only so
// it can survive process crashes in the fallback JSONL.
type RelayLogAccountingPayload struct {
	Version         int     `json:"version"`
	UserID          int     `json:"user_id"`
	ChannelID       int     `json:"channel_id"`
	ChannelName     string  `json:"channel_name"`
	UsingGroup      string  `json:"using_group"`
	OriginModelName string  `json:"origin_model_name"`
	GroupRatio      float64 `json:"group_ratio"`
	Quota           int     `json:"quota"`
	SurchargeQuota  int64   `json:"surcharge_quota"`
	// BaseQuota 渠道每日上限「上游消耗」口径的基础消耗（分组倍率取 1）。旧记录没有该字段时读出为 0，
	// 由消费方回退为 Quota / GroupRatio。
	BaseQuota int64 `json:"base_quota,omitempty"`
}

var relayLogAccountingHandler func(RelayLogAccountingPayload, int)

func RegisterRelayLogAccountingHandler(handler func(RelayLogAccountingPayload, int)) {
	relayLogAccountingHandler = handler
}

func DispatchRelayLogAccounting(payload RelayLogAccountingPayload, logID int) {
	dispatchRelayLogContinuation(&relayLogEvent{Accounting: &payload}, logID)
}

type relayLogContinuationJob struct {
	event *relayLogEvent
	id    int
}

type relayLogFallbackRecord struct {
	Version    int                        `json:"version"`
	Kind       string                     `json:"kind"`
	RecordedAt int64                      `json:"recorded_at"`
	Log        *Log                       `json:"log,omitempty"`
	Accounting *RelayLogAccountingPayload `json:"accounting,omitempty"`
	QuotaData  *QuotaDataLogParams        `json:"quota_data,omitempty"`
}
type relayLogFallbackJob struct {
	event               *relayLogEvent
	executeContinuation bool
	// continuationOnly 表示日志行已经入库（或本来就没有日志行），这个任务只借用
	// fallback worker 执行记账：不写 JSONL、不计 fallback_total，记账用 logID。
	continuationOnly bool
	logID            int
}

var (
	relayLogConsumeMu      sync.Mutex
	relayLogConsumeBuf     []*relayLogEvent
	relayLogErrorMu        sync.Mutex
	relayLogErrorBuf       []*relayLogEvent
	relayLogRetryMu        sync.Mutex
	relayLogRetryBuf       []*relayLogEvent
	relayLogPendingConsume []*relayLogEvent
	relayLogPendingError   []*relayLogEvent
	relayLogFlushMu        sync.Mutex
	relayLogWakeCh         = make(chan struct{}, 1)
	relayLogStopCh         = make(chan struct{})
	relayLogDoneCh         = make(chan struct{})
	relayLogRetryDoneCh    = make(chan struct{})
	// 物理容量取常量而非配置值：Go channel 不能原地扩缩，按启动配置分配的话，
	// 运行中调大只能等下次启动，而后台已经显示成新值——运维在故障中扩容、以为
	// 立即生效、实际仍按旧容量丢弃。配置值恒不超过这两个常量（见 operation_setting
	// 的 bounded 钳制），因此它纯粹是投递时的软上限，双向调整都即时生效。
	relayLogContinuationCh   = make(chan relayLogContinuationJob, operation_setting.MaxRelayLogContinuationBufMaxEntries)
	relayLogFallbackCh       = make(chan relayLogFallbackJob, operation_setting.MaxRelayLogFallbackQueueCapacity)
	relayLogWorkerOnce       sync.Once
	relayLogAuxStopOnce      sync.Once
	relayLogAuxWG            sync.WaitGroup
	relayLogAuxSendMu        sync.RWMutex
	relayLogReplayWG         sync.WaitGroup
	relayLogReplayRunning    atomic.Bool
	relayLogReplayMu         sync.RWMutex
	relayLogReplayCancel     context.CancelFunc
	relayLogReplayStatus     = RelayLogReplayStatus{State: "idle"}
	relayLogLoopOnce         sync.Once
	relayLogStopOnce         sync.Once
	relayLogAccepting        atomic.Bool
	relayLogDroppedConsume   atomic.Uint64
	relayLogDroppedError     atomic.Uint64
	relayLogDroppedRetry     atomic.Uint64
	relayLogFallbackTotal    atomic.Uint64
	relayLogPersistedTotal   atomic.Uint64
	relayLogDBTimeoutTotal   atomic.Uint64
	relayLogLastSuccessAt    atomic.Int64
	relayLogLastErrorAt      atomic.Int64
	relayLogCircuitFailures  atomic.Int64
	relayLogCircuitOpenUntil atomic.Int64
	relayLogHalfOpenProbe    atomic.Bool
	// relayLogContinuationDrop 只统计真正没执行的记账（两条车道都满、或辅助车道
	// 已关闭）。relayLogContinuationOverflow 统计 continuation 车道满、改交 fallback
	// worker 的次数；其中极少数随后连兜底也失败的，另计入 relayLogContinuationDrop。
	relayLogContinuationDrop     atomic.Uint64
	relayLogContinuationOverflow atomic.Uint64
	// relayLogFallbackErrors 只统计丢失的日志行，不含只借道执行记账的任务。
	relayLogFallbackErrors   atomic.Uint64
	relayLogErrorYielded     atomic.Uint64
	relayLogFallbackAlertAt  atomic.Int64
	relayLogContinuationBusy atomic.Int64
	relayLogFallbackBusy     atomic.Int64
	relayLogAuxClosed        atomic.Bool
	relayLogFallbackDir      = filepath.Join("data", "relay-log-fallback")
	relayLogFallbackFileMu   sync.Mutex
	relayLogReplayActivePath string
	relayLogAlertCh          = make(chan string, 1)
	relayLogAlertSink        = common.SysError
)

// intake 与关停的状态。
//
// relayLogAccepting 只表达生命周期：StartRelayLogFlushLoop 之后为 true，
// ShutdownRelayLogFlush 开始时置 false，之后不再恢复。压力（队列满、兜底目录
// 达到保留上限、worker panic）从不关闭 intake：每条通道本身都有硬上限，溢出只会
// 被计数丢弃；关闭 intake 对有界性毫无帮助，却会在日志库恢复后继续拒收全部日志
// （见设计文档 §19.1）。
var (
	// relayLogBuffersSealed 在关停做最后一次取缓冲前、持有两把缓冲锁时置位。
	// 已经越过 relayLogAccepting 检查的 relay goroutine 在锁内看到它就拒收，
	// 而不是把事件留在再也没人排空的缓冲里，或在溢出时向已关闭的通道发送。
	relayLogBuffersSealed atomic.Bool
	// relayLogIntakeRefused 统计 intake 关闭（启动前/关停中）时被拒收的日志条数，
	// 同时计入对应种类的 dropped。
	relayLogIntakeRefused atomic.Uint64
	// pending 切片只由持有 relayLogFlushMu 的 worker 读写；长度另存原子量，
	// 状态接口与关停循环不持锁读取。
	relayLogPendingConsumeLen atomic.Int64
	relayLogPendingErrorLen   atomic.Int64
	// pending 车道队首事件的入队时间（UnixNano，空车道为 0），与长度同处更新，
	// 供状态接口计算 oldest_event_age_ms 而不触碰 worker 独占的切片。
	relayLogPendingConsumeOldest atomic.Int64
	relayLogPendingErrorOldest   atomic.Int64
	// 正在落库的批次（已离开车道、落库尚未返回）的最早入队时间，空闲为 0。
	relayLogInflightFlushOldest atomic.Int64
	relayLogInflightRetryOldest atomic.Int64
	// 最近一次写出过该类日志的刷盘：本类条数与整轮落库耗时（毫秒）。只由刷盘
	// worker 写入，状态接口读取；relay goroutine 的入队路径不涉及。
	relayLogLastFlushConsumeItems atomic.Int64
	relayLogLastFlushConsumeMs    atomic.Int64
	relayLogLastFlushErrorItems   atomic.Int64
	relayLogLastFlushErrorMs      atomic.Int64
	relayLogLastFlushRetryItems   atomic.Int64
	relayLogLastFlushRetryMs      atomic.Int64
	// relayLogRetentionFullLogged 让"兜底目录已满"的系统日志只在进入该状态时
	// 输出一次，持续满时不按批刷屏；任一批写入成功后复位。
	relayLogRetentionFullLogged atomic.Bool
)

func bufferFor(kind relayLogKind) (*sync.Mutex, *[]*relayLogEvent, int, *atomic.Uint64) {
	cfg := operation_setting.GetRelayLogPipelineSetting()
	if kind == relayLogKindConsume {
		return &relayLogConsumeMu, &relayLogConsumeBuf, cfg.GetConsumeBufMaxEntries(), &relayLogDroppedConsume
	}
	return &relayLogErrorMu, &relayLogErrorBuf, cfg.GetErrorBufMaxEntries(), &relayLogDroppedError
}

func enqueueRelayLog(kind relayLogKind, event *relayLogEvent) bool {
	if event == nil || event.Log == nil {
		return false
	}
	event.EnqueuedAt = time.Now()
	event.Kind = kind
	mu, buf, max, dropped := bufferFor(kind)
	mu.Lock()
	if relayLogBuffersSealed.Load() {
		mu.Unlock()
		countRelayLogIntakeRefused(kind)
		return false
	}
	if len(*buf) >= max {
		mu.Unlock()
		dropped.Add(1)
		// Reject-new is O(1). Accounting and the isolated durable fallback are
		// dispatched only after releasing the relay-facing buffer lock.
		dispatchRelayLogFallbackJob(event, true)
		return true
	}
	*buf = append(*buf, event)
	n := len(*buf)
	mu.Unlock()
	if n >= operation_setting.GetRelayLogPipelineSetting().GetInnerBatchSize() {
		select {
		case relayLogWakeCh <- struct{}{}:
		default:
		}
	}
	return true
}

func countRelayLogIntakeRefused(kind relayLogKind) {
	relayLogIntakeRefused.Add(1)
	if kind == relayLogKindConsume {
		relayLogDroppedConsume.Add(1)
	} else {
		relayLogDroppedError.Add(1)
	}
}

// sealRelayLogBuffers 在持有两把缓冲锁时置位，保证此后抢到锁的入队都能看到它；
// 在此之前已经入列的事件会被随后的最后一次取缓冲带走。
func sealRelayLogBuffers() {
	relayLogConsumeMu.Lock()
	relayLogErrorMu.Lock()
	relayLogBuffersSealed.Store(true)
	relayLogErrorMu.Unlock()
	relayLogConsumeMu.Unlock()
}

// takeRelayLogBatch 整体交换缓冲，一次取走全部事件。
//
// 刻意不接受"最多取 N 条"参数：整体交换是 O(1) 且只在锁内待一瞬，
// 按条数截取则要在锁内做拷贝或移位，而这把锁是 relay goroutine 入队时要抢的。
// 每次落库的分批在锁外由 OuterBatchSize / InnerBatchSize 控制。
func takeRelayLogBatch(kind relayLogKind) []*relayLogEvent {
	mu, buf, _, _ := bufferFor(kind)
	mu.Lock()
	out := *buf
	*buf = nil
	mu.Unlock()
	return out
}

func enqueueRelayLogRetry(events []*relayLogEvent) {
	if len(events) == 0 {
		return
	}
	max := operation_setting.GetRelayLogRetrySetting().GetRetryBufMaxEntries()
	var evicted []*relayLogEvent
	relayLogRetryMu.Lock()
	for _, event := range events {
		event.RetryCount++
		relayLogRetryBuf = append(relayLogRetryBuf, event)
	}
	if overflow := len(relayLogRetryBuf) - max; overflow > 0 {
		evicted = append([]*relayLogEvent(nil), relayLogRetryBuf[:overflow]...)
		relayLogRetryBuf = append(relayLogRetryBuf[:0], relayLogRetryBuf[overflow:]...)
		relayLogDroppedRetry.Add(uint64(overflow))
	}
	relayLogRetryMu.Unlock()
	for _, event := range evicted {
		dispatchRelayLogFallbackJob(event, true)
	}
}
func takeRelayLogRetryBatch(max int) []*relayLogEvent {
	relayLogRetryMu.Lock()
	defer relayLogRetryMu.Unlock()
	if max <= 0 || max > len(relayLogRetryBuf) {
		max = len(relayLogRetryBuf)
	}
	out := append([]*relayLogEvent(nil), relayLogRetryBuf[:max]...)
	relayLogRetryBuf = append(relayLogRetryBuf[:0], relayLogRetryBuf[max:]...)
	return out
}

func dispatchRelayLogContinuation(event *relayLogEvent, id int) {
	if event != nil && (event.Accounting != nil || event.QuotaData != nil) {
		relayLogAuxSendMu.RLock()
		defer relayLogAuxSendMu.RUnlock()
		if relayLogAuxClosed.Load() {
			relayLogContinuationDrop.Add(1)
			common.SysError("relay-log: side effect arrived after pipeline shutdown")
			return
		}
		event.continuationOnce.Do(func() {
			startRelayLogAuxWorkers()
			job := relayLogContinuationJob{event: event, id: id}
			// 每次投递重新读软上限，调大调小都即时生效。它由配置钳制保证不超过
			// 物理容量，所以这里不需要再 clamp。
			softCap := operation_setting.GetRelayLogPipelineSetting().GetContinuationBufMaxEntries()
			// 溢出车道只执行记账：到这里日志行要么已经入库（id 已知），要么根本没有，
			// 不能再写进兜底文件，也不能丢掉已知的 logs.id。
			// 改道不是丢失，只计 continuation_overflowed；fallback 车道也放不下、
			// 最后一条路也满时，由 sendRelayLogContinuationLastResort 计一次丢弃。
			overflow := relayLogFallbackJob{event: event, executeContinuation: true, continuationOnly: true, logID: id}
			if len(relayLogContinuationCh) >= softCap {
				relayLogContinuationOverflow.Add(1)
				dispatchRelayLogFallbackLocked(overflow)
				return
			}
			select {
			case relayLogContinuationCh <- job:
			default:
				// Keep relay non-blocking. The isolated fallback worker is the
				// overflow lane and executes the accounting.
				relayLogContinuationOverflow.Add(1)
				dispatchRelayLogFallbackLocked(overflow)
			}
		})
	}
}

func startRelayLogAuxWorkers() {
	relayLogWorkerOnce.Do(func() {
		for i := 0; i < 2; i++ {
			relayLogAuxWG.Add(1)
			go relayLogContinuationWorker()
		}
		relayLogAuxWG.Add(1)
		go relayLogFallbackWorker()
		relayLogAuxWG.Add(1)
		go relayLogAlertWorker()
	})
}

// 两个辅助 worker 都以"单条/单批 recover"的方式运行：一次 panic 只报废当前任务，
// worker 本身继续消费，不会因为车道少了一个消费者而让通道逐渐堆满。
func relayLogContinuationWorker() {
	defer relayLogAuxWG.Done()
	for job := range relayLogContinuationCh {
		relayLogContinuationBusy.Add(1)
		runRelayLogContinuationJob(job)
	}
}

func runRelayLogContinuationJob(job relayLogContinuationJob) {
	defer relayLogContinuationBusy.Add(-1)
	defer func() {
		if recovered := recover(); recovered != nil {
			common.SysError(fmt.Sprintf("relay-log: continuation panic: %v", recovered))
		}
	}()
	if job.event.QuotaData != nil {
		LogQuotaData(*job.event.QuotaData)
	}
	if job.event.Accounting != nil && relayLogAccountingHandler != nil {
		relayLogAccountingHandler(*job.event.Accounting, job.id)
	}
}

func dispatchRelayLogFallbackJob(event *relayLogEvent, executeContinuation bool) {
	if event == nil {
		return
	}
	relayLogAuxSendMu.RLock()
	defer relayLogAuxSendMu.RUnlock()
	dispatchRelayLogFallbackLocked(relayLogFallbackJob{event: event, executeContinuation: executeContinuation})
}

// dispatchRelayLogFallbackLocked 要求调用方已持有 relayLogAuxSendMu 读锁：
// 关停在写锁下关闭两条辅助通道，持读锁再检查 relayLogAuxClosed 才能保证
// 永远不向已关闭的通道发送（那会在 relay goroutine 上 panic）。
// 读锁不可重入（有写者排队时再次 RLock 会死锁），所以已持锁的
// dispatchRelayLogContinuation 直接调用本函数。
func dispatchRelayLogFallbackLocked(job relayLogFallbackJob) {
	event := job.event
	hasContinuation := job.executeContinuation && (event.Accounting != nil || event.QuotaData != nil)
	// 只有真要落盘的日志行放不进车道才算 fallback 错误；continuationOnly 任务的
	// 日志行已在库里（或没有），放不进来丢的只是记账，由 continuation_dropped 计数。
	carriesLog := !job.continuationOnly && event.Log != nil
	countLogLost := func() {
		if carriesLog {
			relayLogFallbackErrors.Add(1)
		}
	}
	if relayLogAuxClosed.Load() {
		countLogLost()
		if hasContinuation {
			relayLogContinuationDrop.Add(1)
		}
		return
	}
	startRelayLogAuxWorkers()
	// 同 continuation：软上限每次投递重新读取，双向即时生效。
	softCap := operation_setting.GetRelayLogPipelineSetting().GetFallbackQueueCapacity()

	// 错误日志在通道用掉 1/N 之后就不再入队，把余量留给消费日志。
	// 这里直接丢弃而不是走下面的软/硬上限分支：错误日志没有记账负载，
	// 无需再往 continuation 通道兜一次，那样只会挤占记账自己的容量。
	// 万一带了记账负载就不走让位：让位不计 continuation_dropped，会让记账静默丢失。
	if event.Kind == relayLogKindError && !hasContinuation && len(relayLogFallbackCh) >= softCap/relayLogErrorFallbackDivisor {
		relayLogErrorYielded.Add(1)
		queueRelayLogAlert("error logs yielding fallback capacity to consume logs")
		return
	}

	if len(relayLogFallbackCh) >= softCap {
		countLogLost()
		queueRelayLogAlert("soft capacity reached")
		if hasContinuation {
			sendRelayLogContinuationLastResort(job)
		}
		return
	}
	select {
	case relayLogFallbackCh <- job:
	default:
		// A log-only fallback may be dropped under extreme pressure. Accounting
		// gets a second fixed worker lane before it is counted as dropped.
		countLogLost()
		queueRelayLogAlert("hard capacity reached")
		if hasContinuation {
			sendRelayLogContinuationLastResort(job)
		}
	}
}

// sendRelayLogContinuationLastResort 是 fallback 车道放不下时记账的最后一条路。
// 它不看 continuation 软上限，只受物理容量约束，并带着 job.logID——日志已入库的
// 记账仍拿到真实的 logs.id。两条车道都满时只能计数丢弃并告警，这是
// continuation_dropped 在压力下唯一的计数点（另一处是辅助车道已关闭），一次事件
// 只计一次。此时 intake 不关闭——所有通道都有硬上限，关闭 intake 既救不回这笔
// 记账，还会在压力消退后继续拒收日志。
func sendRelayLogContinuationLastResort(job relayLogFallbackJob) {
	select {
	case relayLogContinuationCh <- relayLogContinuationJob{event: job.event, id: job.logID}:
	default:
		relayLogContinuationDrop.Add(1)
		queueRelayLogAlert("accounting queues exhausted; accounting continuation dropped")
	}
}

func queueRelayLogAlert(reason string) {
	select {
	case relayLogAlertCh <- reason:
	default:
	}
}

func relayLogAlertWorker() {
	defer relayLogAuxWG.Done()
	for {
		select {
		case reason := <-relayLogAlertCh:
			now := time.Now().Unix()
			last := relayLogFallbackAlertAt.Load()
			if now-last >= 60 && relayLogFallbackAlertAt.CompareAndSwap(last, now) {
				relayLogAlertSink("relay-log: fallback pressure: " + reason)
			}
		case <-relayLogStopCh:
			return
		}
	}
}

func relayLogFallbackWorker() {
	defer relayLogAuxWG.Done()
	// 批量取走已经排队的任务：fallback 是主通路的兜底，如果它比主通路还慢，
	// 主通路一旦顶不住，兜底会立刻跟着崩，日志行就只能丢。一条一条写时每条要付
	// MkdirAll + Stat + OpenFile + Write + Close 五次系统调用，把这些开销摊到一批
	// 上之后，兜底通路才真正比它要兜的通路快。
	batch := make([]relayLogFallbackJob, 0, relayLogFallbackBatchMax)
	for job := range relayLogFallbackCh {
		relayLogFallbackBusy.Add(1)
		batch = append(batch[:0], job)
	drain:
		for len(batch) < relayLogFallbackBatchMax {
			select {
			case next := <-relayLogFallbackCh:
				relayLogFallbackBusy.Add(1)
				batch = append(batch, next)
			default:
				break drain
			}
		}
		runRelayLogFallbackBatch(batch)
	}
}

// runRelayLogFallbackBatch 把一批的 panic 限制在这一批：worker 继续消费后续任务。
//
// panic 时只把本批中尚未计入 fallback_total / fallback_errors 的日志行记为错误：
// 只借道执行记账的任务没有日志行可丢（记账由 defer 照常执行），已按单条计过
// 错误或已写入成功的行也不能再计一次。
func runRelayLogFallbackBatch(batch []relayLogFallbackJob) {
	unsettled := relayLogFallbackLogRows(batch)
	defer func() {
		if recovered := recover(); recovered != nil {
			if unsettled > 0 {
				relayLogFallbackErrors.Add(uint64(unsettled))
			}
			common.SysError(fmt.Sprintf("relay-log: fallback batch panic: %v", recovered))
		}
	}()
	writeRelayLogFallbackBatch(batch, &unsettled)
}

// relayLogFallbackCarriesLog 判定任务是否带着待落盘的日志行。
// continuationOnly 的日志已在库里（或本来就没有），不属于待落盘的行。
func relayLogFallbackCarriesLog(job relayLogFallbackJob) bool {
	return !job.continuationOnly && job.event != nil && job.event.Log != nil
}

func relayLogFallbackLogRows(batch []relayLogFallbackJob) int {
	rows := 0
	for _, job := range batch {
		if relayLogFallbackCarriesLog(job) {
			rows++
		}
	}
	return rows
}

// writeRelayLogFallbackBatch 是独立函数，好让 busy 计数由 defer 释放：
// 放在循环体末尾自减的话，一次 panic 就让计数永远停在 >0，
// 而 drain/关停流程都在等它归零。
//
// unsettled 是本批尚未计数的日志行数，每计入一次 fallback_total 或
// fallback_errors 就相应扣减，供 runRelayLogFallbackBatch 在 panic 时只补计余数。
func writeRelayLogFallbackBatch(batch []relayLogFallbackJob, unsettled *int) {
	// defer 是 LIFO：先注册的计数归还最后执行，保证记账跑完之前 busy 不会归零，
	// 否则关停流程会在记账还没做完时就认为管道已经空闲。
	defer relayLogFallbackBusy.Add(-int64(len(batch)))
	defer runRelayLogFallbackContinuations(batch)

	// 序列化放在文件锁外，锁内只做 IO
	recordedAt := time.Now().Unix()
	payloads := make([][]byte, 0, len(batch))
	for _, job := range batch {
		// 只落盘待补写的日志行。continuationOnly 的日志已在库里（或本来就没有），
		// 写进来只会占用保留空间、虚增 fallback_total，回放时也只会被跳过。
		if job.continuationOnly || job.event.Log == nil {
			continue
		}
		data, err := common.Marshal(relayLogFallbackRecord{
			Version: 1, Kind: "relay_log_only", RecordedAt: recordedAt, Log: job.event.Log,
		})
		if err != nil {
			relayLogFallbackErrors.Add(1)
			*unsettled--
			common.SysError("relay-log: fallback marshal failed: " + err.Error())
			continue
		}
		payloads = append(payloads, data)
	}

	err := appendRelayLogFallbackLines(payloads)
	*unsettled -= len(payloads)
	if err != nil {
		relayLogFallbackErrors.Add(uint64(len(payloads)))
		if errors.Is(err, errRelayLogFallbackRetentionFull) {
			// 目录满是持续状态，按批输出会刷屏；只在进入该状态时记一次。
			// 告警 worker 另有 60 秒限频。
			if relayLogRetentionFullLogged.CompareAndSwap(false, true) {
				common.SysError("relay-log: fallback retention cap reached; overflow logs are dropped until fallback files are backfilled or removed")
			}
			queueRelayLogAlert("fallback retention cap reached; overflow logs are being dropped")
			return
		}
		common.SysError("relay-log: fallback write failed: " + err.Error())
		return
	}
	if len(payloads) > 0 {
		relayLogRetentionFullLogged.Store(false)
	}
	relayLogFallbackTotal.Add(uint64(len(payloads)))
}

// appendRelayLogFallbackLines 一次打开、一次写入、一次关闭，
// 把文件系统开销从"每条日志"摊薄到"每批"。
func appendRelayLogFallbackLines(payloads [][]byte) error {
	if len(payloads) == 0 {
		return nil
	}
	relayLogFallbackFileMu.Lock()
	defer relayLogFallbackFileMu.Unlock()

	if err := os.MkdirAll(relayLogFallbackDir, 0o750); err != nil {
		return err
	}
	path := filepath.Join(relayLogFallbackDir, "relay-log.jsonl")
	if err := rotateRelayLogFallbackLocked(path); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	writer := bufio.NewWriterSize(file, 256*1024)
	for _, payload := range payloads {
		if _, err = writer.Write(payload); err != nil {
			break
		}
		if err = writer.WriteByte('\n'); err != nil {
			break
		}
	}
	if err == nil {
		err = writer.Flush()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

// 记账与配额导出在文件写之后执行，且每条各自 recover：
// 一条的 panic 不能带走同批其余条目的记账。
func runRelayLogFallbackContinuations(batch []relayLogFallbackJob) {
	for _, job := range batch {
		event := job.event
		if !job.executeContinuation || (event.Accounting == nil && event.QuotaData == nil) {
			continue
		}
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					common.SysError(fmt.Sprintf("relay-log: fallback continuation panic: %v", recovered))
				}
			}()
			if event.QuotaData != nil {
				LogQuotaData(*event.QuotaData)
			}
			if event.Accounting != nil && relayLogAccountingHandler != nil {
				relayLogAccountingHandler(*event.Accounting, job.logID)
			}
		}()
	}
}

func rotateRelayLogFallback(path string) error {
	relayLogFallbackFileMu.Lock()
	defer relayLogFallbackFileMu.Unlock()
	return rotateRelayLogFallbackLocked(path)
}

func rotateRelayLogFallbackLocked(path string) error {
	cfg := operation_setting.GetRelayLogPipelineSetting()
	maxBytes := int64(boundedFallbackFileSizeMB(cfg.FallbackMaxFileSizeMB)) * 1024 * 1024
	info, err := os.Stat(path)
	if os.IsNotExist(err) || (err == nil && info.Size() < maxBytes) {
		return nil
	}
	if err != nil {
		return err
	}
	rotated := fmt.Sprintf("%s.%d", path, time.Now().UnixNano())
	logicalFiles, err := relayLogLogicalFallbackCountLocked(path)
	if err != nil {
		return err
	}
	if logicalFiles >= boundedFallbackMaxFiles(cfg.FallbackMaxFiles) {
		return errRelayLogFallbackRetentionFull
	}
	if err := os.Rename(path, rotated); err != nil {
		return err
	}
	return nil
}

// relayLogFallbackRetentionFull 报告兜底目录此刻是否已满，判定与
// rotateRelayLogFallbackLocked 相同：active 文件已达轮转阈值且逻辑文件数已达
// 保留上限，即下一批兜底写入会被拒绝。只读目录元数据，不读文件内容。
func relayLogFallbackRetentionFull() (bool, error) {
	relayLogFallbackFileMu.Lock()
	defer relayLogFallbackFileMu.Unlock()
	cfg := operation_setting.GetRelayLogPipelineSetting()
	path := filepath.Join(relayLogFallbackDir, "relay-log.jsonl")
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Size() < int64(boundedFallbackFileSizeMB(cfg.FallbackMaxFileSizeMB))*1024*1024 {
		return false, nil
	}
	logicalFiles, err := relayLogLogicalFallbackCountLocked(path)
	if err != nil {
		return false, err
	}
	return logicalFiles >= boundedFallbackMaxFiles(cfg.FallbackMaxFiles), nil
}

func boundedFallbackMaxFiles(value int) int {
	if value <= 0 {
		return 8
	}
	if value > 64 {
		return 64
	}
	return value
}

func relayLogLogicalFallbackCountLocked(path string) (int, error) {
	matches, err := filepath.Glob(path + ".*")
	if err != nil {
		return 0, err
	}
	targets := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		target := strings.TrimSuffix(match, ".replay.tmp")
		target = strings.TrimSuffix(target, ".replay.source")
		targets[target] = struct{}{}
	}
	if strings.HasPrefix(relayLogReplayActivePath, path+".") {
		targets[relayLogReplayActivePath] = struct{}{}
	}
	return len(targets), nil
}

func boundedFallbackFileSizeMB(value int) int {
	if value <= 0 {
		return 256
	}
	if value > 4096 {
		return 4096
	}
	return value
}

func relayLogCircuitOpen() bool { return time.Now().UnixNano() < relayLogCircuitOpenUntil.Load() }
func reportRelayLogWriteFailure() {
	n := relayLogCircuitFailures.Add(1)
	relayLogLastErrorAt.Store(time.Now().Unix())
	cfg := operation_setting.GetRelayLogRetrySetting()
	if n >= int64(cfg.GetCircuitFailureThreshold()) {
		relayLogCircuitOpenUntil.Store(time.Now().Add(cfg.GetCircuitOpenDuration()).UnixNano())
		relayLogCircuitFailures.Store(0)
	}
}

func persistRelayLogEvents(events []*relayLogEvent) bool {
	if len(events) == 0 {
		return true
	}
	if relayLogCircuitOpen() {
		enqueueRelayLogRetry(events)
		return false
	}
	if relayLogCircuitOpenUntil.Load() != 0 && !relayLogHalfOpenProbe.CompareAndSwap(false, true) {
		enqueueRelayLogRetry(events)
		return false
	}
	if relayLogHalfOpenProbe.Load() && len(events) > 1 {
		enqueueRelayLogRetry(events[1:])
		events = events[:1]
	}
	defer relayLogHalfOpenProbe.Store(false)
	logs := make([]*Log, 0, len(events))
	for _, event := range events {
		logs = append(logs, event.Log)
	}
	ctx, cancel := context.WithTimeout(context.Background(), operation_setting.GetRelayLogPipelineSetting().GetWriteTimeout())
	defer cancel()
	err := insertRelayLogs(LOG_DB.WithContext(ctx), logs, operation_setting.GetRelayLogPipelineSetting().GetInnerBatchSize())
	if err != nil {
		if ctx.Err() != nil {
			relayLogDBTimeoutTotal.Add(1)
		}
		reportRelayLogWriteFailure()
		enqueueRelayLogRetry(events)
		common.SysError("relay-log: batch insert failed: " + err.Error())
		return false
	}
	relayLogCircuitFailures.Store(0)
	relayLogCircuitOpenUntil.Store(0)
	relayLogLastSuccessAt.Store(time.Now().Unix())
	relayLogPersistedTotal.Add(uint64(len(events)))
	for i, event := range events {
		logID := logs[i].Id
		if logID <= 0 {
			common.SysError("relay-log: batch insert did not return log id; accounting continues with log_id=0")
			logID = 0
		}
		dispatchRelayLogContinuation(event, logID)
	}
	return true
}

func flushRelayLogs() {
	cfg := operation_setting.GetRelayLogPipelineSetting()
	max := cfg.GetFlushMaxPerCycle()
	if cfg.FullDrain {
		max = int(^uint(0) >> 1)
	}
	appendRelayLogPending(relayLogKindConsume, takeRelayLogBatch(relayLogKindConsume))
	appendRelayLogPending(relayLogKindError, takeRelayLogBatch(relayLogKindError))

	// 消费日志优先用满本轮预算，错误日志只拿剩下的。
	//
	// 原来是两类轮流优先，那是"公平"策略；但两者价值并不对等——消费日志关联计费
	// 与对账，错误日志只用于诊断。正常负载下总量远小于每轮预算，两类都会被完整
	// 刷完，这个顺序看不出差别；只有在积压时才生效，而积压时正是要保消费日志。
	//
	// 错误日志因此可能持续排队直到自己的缓冲满、溢出到 fallback 并被优先丢弃，
	// 这是本策略的既定代价。
	events := takeRelayLogPending(relayLogKindConsume, max)
	if remaining := max - len(events); remaining > 0 {
		events = append(events, takeRelayLogPending(relayLogKindError, remaining)...)
	}
	// 按响应完成顺序落库。
	//
	// 两类日志各有独立缓冲，上面的选批把它们首尾相接，插入序因此天然是"一段消费 +
	// 一段错误"。而 created_at 只有秒级精度，列表排序 (created_at DESC, id DESC)
	// 在同一秒内只能靠 id 分先后，id 就是插入序——于是同一秒内成功与失败各自成块
	// 显示。按 EnqueuedAt 排序后 id 与真实响应顺序单调对应，同秒内自然交错，读路径
	// 不需要新增任何排序表达式，也不动索引。
	//
	// 用排序而不是双路归并：EnqueuedAt 在抢缓冲锁之前打戳（见 enqueueRelayLog），
	// 锁一有争用，先打戳的就可能后入列，所以单条缓冲内部并非严格有序，归并会把这层
	// 倒挂原样带到 id 上。把打戳挪进锁内可以让归并成立，但那是往 relay goroutine
	// 争用的临界区里加活；排序跑在 flush goroutine 上，与主链路无关。
	//
	// 排序只作用于已选出的这批，不影响上面消费优先的预算分配：积压时错误日志照旧
	// 让位，只是不再人为把它们挤成一块。
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].EnqueuedAt.Before(events[j].EnqueuedAt)
	})

	consumeItems, errorItems := 0, 0
	for _, event := range events {
		if event.Kind == relayLogKindConsume {
			consumeItems++
		} else {
			errorItems++
		}
	}
	started := time.Now()
	// 本轮选中的事件已离开待写车道，落库返回前仍算"在等待"；按入队时间排过序，队首最早。
	relayLogInflightFlushOldest.Store(relayLogEventsOldest(events))
	defer relayLogInflightFlushOldest.Store(0) // 落库 panic 时也不留下过期的队首时间
	for outer := cfg.GetOuterBatchSize(); len(events) > 0; {
		n := outer
		if n > len(events) {
			n = len(events)
		}
		persistRelayLogEvents(events[:n])
		events = events[n:]
	}
	recordRelayLogFlush(consumeItems, &relayLogLastFlushConsumeItems, &relayLogLastFlushConsumeMs, started)
	recordRelayLogFlush(errorItems, &relayLogLastFlushErrorItems, &relayLogLastFlushErrorMs, started)
}

// recordRelayLogFlush 记下一轮刷盘写出的某类条数与耗时；空轮不覆盖，状态始终反映最近一次真实刷盘。
// 条数是本轮选中并交给落库的条数，写失败转入重试的也计入（耗时同样包含失败的往返）。
func recordRelayLogFlush(items int, lastItems, lastMs *atomic.Int64, started time.Time) {
	if items == 0 {
		return
	}
	lastItems.Store(int64(items))
	lastMs.Store(time.Since(started).Milliseconds())
}

func appendRelayLogPending(kind relayLogKind, incoming []*relayLogEvent) {
	pending := &relayLogPendingError
	capacity := operation_setting.GetRelayLogPipelineSetting().GetErrorBufMaxEntries()
	if kind == relayLogKindConsume {
		pending = &relayLogPendingConsume
		capacity = operation_setting.GetRelayLogPipelineSetting().GetConsumeBufMaxEntries()
	}
	if len(*pending) > capacity {
		excess := (*pending)[capacity:]
		*pending = (*pending)[:capacity]
		for _, event := range excess {
			dispatchRelayLogFallbackJob(event, true)
		}
	}
	available := capacity - len(*pending)
	if available < 0 {
		available = 0
	}
	keep := len(incoming)
	if keep > available {
		keep = available
	}
	*pending = append(*pending, incoming[:keep]...)
	storeRelayLogPendingLen(kind)
	for _, event := range incoming[keep:] {
		dispatchRelayLogFallbackJob(event, true)
	}
}

func takeRelayLogPending(kind relayLogKind, max int) []*relayLogEvent {
	pending := &relayLogPendingError
	if kind == relayLogKindConsume {
		pending = &relayLogPendingConsume
	}
	if max < 0 || max > len(*pending) {
		max = len(*pending)
	}
	out := (*pending)[:max]
	*pending = (*pending)[max:]
	if len(*pending) == 0 {
		*pending = nil
	}
	storeRelayLogPendingLen(kind)
	return out
}

// storeRelayLogPendingLen 在每次改动 pending 切片后同步其长度，调用方持有
// relayLogFlushMu（关停路径同样先拿到这把锁，拿不到就不碰 pending）。
func storeRelayLogPendingLen(kind relayLogKind) {
	if kind == relayLogKindConsume {
		relayLogPendingConsumeLen.Store(int64(len(relayLogPendingConsume)))
		relayLogPendingConsumeOldest.Store(relayLogEventsOldest(relayLogPendingConsume))
		return
	}
	relayLogPendingErrorLen.Store(int64(len(relayLogPendingError)))
	relayLogPendingErrorOldest.Store(relayLogEventsOldest(relayLogPendingError))
}

// relayLogEventsMinEnqueued 逐条取最早的入队时间（UnixNano，空或都未打戳为 0），用于不按入队顺序排列的重试队列与批次。
func relayLogEventsMinEnqueued(events []*relayLogEvent) int64 {
	oldest := int64(0)
	for _, event := range events {
		if event == nil || event.EnqueuedAt.IsZero() {
			continue
		}
		if at := event.EnqueuedAt.UnixNano(); oldest == 0 || at < oldest {
			oldest = at
		}
	}
	return oldest
}

// relayLogEventsOldest 返回按入队顺序排列的事件队首的入队时间（UnixNano），空为 0。
// 入队在抢缓冲锁前打戳，锁争用时相邻事件可能倒挂几微秒，状态展示不需要更精确。
func relayLogEventsOldest(events []*relayLogEvent) int64 {
	if len(events) == 0 || events[0] == nil || events[0].EnqueuedAt.IsZero() {
		return 0
	}
	return events[0].EnqueuedAt.UnixNano()
}

func clearRelayLogPending() {
	relayLogPendingConsume = nil
	relayLogPendingError = nil
	storeRelayLogPendingLen(relayLogKindConsume)
	storeRelayLogPendingLen(relayLogKindError)
}

func flushRelayLogRetries() {
	cfg := operation_setting.GetRelayLogRetrySetting()
	events := takeRelayLogRetryBatch(operation_setting.GetRelayLogPipelineSetting().GetOuterBatchSize())
	if len(events) == 0 {
		return
	}
	var retryable []*relayLogEvent
	for _, event := range events {
		if event.RetryCount >= cfg.GetMaxRetries() {
			dispatchRelayLogFallbackJob(event, true)
		} else {
			retryable = append(retryable, event)
		}
	}
	started := time.Now()
	// 重试批次按失败先后排列而非入队先后，逐条取最早的入队时间。
	relayLogInflightRetryOldest.Store(relayLogEventsMinEnqueued(retryable))
	defer relayLogInflightRetryOldest.Store(0)
	persistRelayLogEvents(retryable)
	recordRelayLogFlush(len(retryable), &relayLogLastFlushRetryItems, &relayLogLastFlushRetryMs, started)
}

func StartRelayLogFlushLoop() {
	relayLogLoopOnce.Do(func() {
		relayLogAccepting.Store(true)
		prepareRelayLogReplayFiles()
		startRelayLogAuxWorkers()
		go func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					common.SysError(fmt.Sprintf("relay-log: flush loop panic: %v", recovered))
				}
			}()
			defer close(relayLogDoneCh)
			for {
				interval := operation_setting.GetRelayLogPipelineSetting().GetFlushInterval()
				select {
				case <-time.After(interval):
				case <-relayLogWakeCh:
				case <-relayLogStopCh:
					return
				}
				relayLogFlushMu.Lock()
				runRelayLogWorkerCycle("flush", flushRelayLogs)
				relayLogFlushMu.Unlock()
			}
		}()
		go func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					common.SysError(fmt.Sprintf("relay-log: retry loop panic: %v", recovered))
				}
			}()
			defer close(relayLogRetryDoneCh)
			for {
				select {
				case <-time.After(operation_setting.GetRelayLogRetrySetting().GetRetryFlushInterval()):
					if operation_setting.GetRelayLogRetrySetting().AllowConcurrentFlush {
						// Preserve the single-writer invariant while allowing a
						// configured retry cycle to wait off the relay path.
						relayLogFlushMu.Lock()
						runRelayLogWorkerCycle("retry", flushRelayLogRetries)
						relayLogFlushMu.Unlock()
					} else if relayLogFlushMu.TryLock() {
						runRelayLogWorkerCycle("retry", flushRelayLogRetries)
						relayLogFlushMu.Unlock()
					}
				case <-relayLogStopCh:
					return
				}
			}
		}()
		common.SysLog("relay-log: bounded async batch writer started")
	})
}

func prepareRelayLogReplayFiles() {
	relayLogFallbackFileMu.Lock()
	defer relayLogFallbackFileMu.Unlock()
	if err := recoverRelayLogReplayArtifactsLocked(); err != nil {
		common.SysError("relay-log: recover fallback replay artifacts failed: " + err.Error())
	}
	base := filepath.Join(relayLogFallbackDir, "relay-log.jsonl")
	if _, err := os.Stat(base); err == nil {
		if err := os.Rename(base, fmt.Sprintf("%s.startup.%d", base, time.Now().UnixNano())); err != nil {
			common.SysError("relay-log: archive active fallback file failed: " + err.Error())
		}
	}
}

func listRelayLogReplayFiles() ([]string, error) {
	relayLogFallbackFileMu.Lock()
	defer relayLogFallbackFileMu.Unlock()
	if err := recoverRelayLogReplayArtifactsLocked(); err != nil {
		return nil, err
	}
	base := filepath.Join(relayLogFallbackDir, "relay-log.jsonl")
	paths, err := filepath.Glob(base + ".*")
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	result := paths[:0]
	for _, path := range paths {
		if strings.HasSuffix(path, ".replay.tmp") || strings.HasSuffix(path, ".replay.source") {
			continue
		}
		result = append(result, path)
	}
	return result, nil
}

func recoverRelayLogReplayArtifactsLocked() error {
	base := filepath.Join(relayLogFallbackDir, "relay-log.jsonl")
	sources, err := filepath.Glob(base + ".*.replay.source")
	if err != nil {
		return err
	}
	sort.Strings(sources)
	for _, source := range sources {
		target := strings.TrimSuffix(source, ".replay.source")
		if target == relayLogReplayActivePath {
			continue
		}
		if _, statErr := os.Stat(target); statErr == nil {
			if err := os.Remove(source); err != nil {
				return err
			}
		} else if os.IsNotExist(statErr) {
			if err := os.Rename(source, target); err != nil {
				return err
			}
		} else {
			return statErr
		}
	}
	temps, err := filepath.Glob(base + ".*.replay.tmp")
	if err != nil {
		return err
	}
	sort.Strings(temps)
	for _, tmp := range temps {
		target := strings.TrimSuffix(tmp, ".replay.tmp")
		if target == relayLogReplayActivePath {
			continue
		}
		if _, statErr := os.Stat(target); statErr == nil {
			if err := os.Remove(tmp); err != nil {
				return err
			}
		} else if os.IsNotExist(statErr) {
			if err := os.Rename(tmp, target); err != nil {
				return err
			}
		} else {
			return statErr
		}
	}
	return nil
}

type relayLogReplayFileResult struct {
	processed uint64
	failed    uint64
	err       error
}

var relayLogReplayBatchWriter = func(ctx context.Context, logs []*Log, batchSize int) error {
	return insertRelayLogs(LOG_DB.WithContext(ctx), logs, batchSize)
}

func writeRelayLogReplayBatch(ctx context.Context, logs []*Log, batchSize int) error {
	relayLogFlushMu.Lock()
	defer relayLogFlushMu.Unlock()
	return relayLogReplayBatchWriter(ctx, logs, batchSize)
}

func StartRelayLogFallbackReplay() (bool, error) {
	if !relayLogReplayRunning.CompareAndSwap(false, true) {
		return false, ErrRelayLogReplayRunning
	}
	relayLogReplayMu.Lock()
	if !relayLogAccepting.Load() {
		relayLogReplayRunning.Store(false)
		relayLogReplayMu.Unlock()
		return false, ErrRelayLogReplayShuttingDown
	}
	now := time.Now().Unix()
	relayLogReplayStatus = RelayLogReplayStatus{
		State:     "running",
		StartedAt: now,
	}
	paths, err := listRelayLogReplayFiles()
	if err != nil {
		relayLogReplayStatus.State = "failed"
		relayLogReplayStatus.FinishedAt = time.Now().Unix()
		relayLogReplayStatus.LastError = relayLogReplayErrorRecovery
		relayLogReplayRunning.Store(false)
		relayLogReplayMu.Unlock()
		return false, err
	}
	if len(paths) == 0 {
		relayLogReplayStatus = RelayLogReplayStatus{State: "idle"}
		relayLogReplayRunning.Store(false)
		relayLogReplayMu.Unlock()
		return false, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	relayLogReplayCancel = cancel
	relayLogReplayWG.Add(1)
	go runRelayLogFallbackReplay(ctx, paths)
	relayLogReplayMu.Unlock()
	return true, nil
}

func runRelayLogFallbackReplay(ctx context.Context, paths []string) {
	defer relayLogReplayWG.Done()
	defer func() {
		if recovered := recover(); recovered != nil {
			common.SysError(fmt.Sprintf("relay-log: manual fallback replay panic: %v", recovered))
			finishRelayLogReplay("failed", relayLogReplayErrorUnexpected)
		}
	}()

	var processed uint64
	var failed uint64
	var lastError string
	for _, path := range paths {
		if ctx.Err() != nil {
			lastError = relayLogReplayErrorShutdown
			break
		}
		relayLogReplayMu.Lock()
		relayLogReplayStatus.RunningFile = filepath.Base(path)
		relayLogReplayMu.Unlock()
		result := replayRelayLogFallbackActive(ctx, path)
		processed += result.processed
		failed += result.failed
		if result.err != nil {
			common.SysError("relay-log: manual fallback replay failed: " + result.err.Error())
			lastError = relayLogReplayErrorFailed
			if ctx.Err() != nil {
				lastError = relayLogReplayErrorShutdown
			}
		}
		updateRelayLogReplayProgress(processed, failed)
	}

	state := "succeeded"
	if lastError != "" || failed > 0 {
		if processed > 0 {
			state = "partial_failed"
		} else {
			state = "failed"
		}
	}
	finishRelayLogReplay(state, lastError)
}

func replayRelayLogFallbackActive(ctx context.Context, path string) relayLogReplayFileResult {
	relayLogFallbackFileMu.Lock()
	relayLogReplayActivePath = path
	relayLogFallbackFileMu.Unlock()
	defer func() {
		relayLogFallbackFileMu.Lock()
		relayLogReplayActivePath = ""
		relayLogFallbackFileMu.Unlock()
	}()
	return replayRelayLogFallback(ctx, path)
}

func updateRelayLogReplayProgress(processed, failed uint64) {
	relayLogReplayMu.Lock()
	relayLogReplayStatus.ProcessedTotal = processed
	relayLogReplayStatus.FailedTotal = failed
	relayLogReplayMu.Unlock()
}

func finishRelayLogReplay(state, lastError string) {
	relayLogReplayMu.Lock()
	relayLogReplayStatus.State = state
	relayLogReplayStatus.RunningFile = ""
	relayLogReplayStatus.FinishedAt = time.Now().Unix()
	relayLogReplayStatus.LastError = lastError
	relayLogReplayCancel = nil
	relayLogReplayRunning.Store(false)
	relayLogReplayMu.Unlock()
}

func stopRelayLogFallbackReplay() {
	relayLogReplayMu.Lock()
	if relayLogReplayCancel != nil {
		relayLogReplayCancel()
	}
	relayLogReplayMu.Unlock()
}

func replayRelayLogFallback(ctx context.Context, path string) relayLogReplayFileResult {
	result := relayLogReplayFileResult{}
	relayLogFallbackFileMu.Lock()
	file, err := os.Open(path)
	relayLogFallbackFileMu.Unlock()
	if os.IsNotExist(err) {
		return result
	}
	if err != nil {
		result.err = err
		return result
	}
	tmp := path + ".replay.tmp"
	relayLogFallbackFileMu.Lock()
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	relayLogFallbackFileMu.Unlock()
	if err != nil {
		result.err = errors.Join(err, file.Close())
		return result
	}
	closeSource := func() error {
		if file == nil {
			return nil
		}
		err := file.Close()
		file = nil
		return err
	}
	closeOutput := func() error {
		if out == nil {
			return nil
		}
		err := out.Close()
		out = nil
		return err
	}
	defer func() {
		if err := closeSource(); err != nil {
			common.SysError("relay-log: close replay source failed: " + err.Error())
		}
		if err := closeOutput(); err != nil {
			common.SysError("relay-log: close replay temp failed: " + err.Error())
		}
	}()

	writer := bufio.NewWriterSize(out, 64*1024)
	type replayItem struct {
		line   []byte
		record relayLogFallbackRecord
	}
	batchSize := operation_setting.GetRelayLogPipelineSetting().GetInnerBatchSize()
	batch := make([]replayItem, 0, batchSize)
	failedCount := uint64(0)
	// retained 表示临时文件里有内容，源文件因此要被它替换而不是直接删除。
	retained := false
	writeRetained := func(line []byte) error {
		retained = true
		_, err := writer.Write(append(line, '\n'))
		return err
	}
	flushBatch := func() error {
		if len(batch) == 0 {
			return nil
		}
		logs := make([]*Log, 0, len(batch))
		for _, item := range batch {
			logs = append(logs, item.record.Log)
		}
		writeCtx, cancel := context.WithTimeout(ctx, operation_setting.GetRelayLogPipelineSetting().GetWriteTimeout())
		writeErr := writeRelayLogReplayBatch(writeCtx, logs, batchSize)
		cancel()
		if writeErr != nil {
			for _, item := range batch {
				// 失败的写入可能其实已提交：带着这次分到的 id 保留，下次回放才能
				// 按 id 认出已入库的行，而不是再插一遍。
				line := item.line
				if item.record.Log.Id > 0 {
					if data, err := common.Marshal(item.record); err == nil {
						line = data
					}
				}
				if err := writeRetained(line); err != nil {
					return err
				}
				failedCount++
			}
			result.err = writeErr
		} else {
			result.processed += uint64(len(batch))
		}
		batch = batch[:0]
		return nil
	}
	discardTemp := func() error {
		closeErr := closeOutput()
		relayLogFallbackFileMu.Lock()
		removeErr := os.Remove(tmp)
		relayLogFallbackFileMu.Unlock()
		if os.IsNotExist(removeErr) {
			removeErr = nil
		}
		return errors.Join(closeErr, removeErr)
	}

	reader := bufio.NewReaderSize(file, 64*1024)
	cancelled := false
	// partial 是取消时已从 reader 读出、但还没读完整的那条记录的前缀。
	var partial []byte
	for {
		if ctx.Err() != nil {
			cancelled = true
			break
		}
		line, readErr := readRelayLogJSONLRecord(ctx, reader)
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(readErr, ctxErr) {
				cancelled = true
				partial = line
				break
			}
			result.err = errors.Join(result.err, readErr, discardTemp())
			result.failed = failedCount
			return result
		}
		var record relayLogFallbackRecord
		if err := common.Unmarshal(line, &record); err != nil || record.Version != 1 {
			if err := writeRetained(line); err != nil {
				// 临时文件写不进去就无法保留任何东西：放弃临时文件、保留源文件。
				result.err = errors.Join(result.err, err, discardTemp())
				result.failed = failedCount
				return result
			}
			failedCount++
			result.err = errors.New("unsupported fallback record")
			continue
		}
		if record.Log == nil {
			continue
		}
		batch = append(batch, replayItem{line: line, record: record})
		if len(batch) >= batchSize {
			if err := flushBatch(); err != nil {
				result.err = errors.Join(result.err, err, discardTemp())
				result.failed = failedCount
				return result
			}
		}
	}
	if cancelled {
		// 取消（关停）发生在文件中途：之前的批次已经提交，却没有可回写的 id。
		// 若整体保留源文件，下次回放会把它们再插一遍。因此（有批次已提交或已有
		// 保留行时）把尚未写库的部分——
		// 当前未提交的批次、读了一半的记录和未读的剩余字节——原样接在临时文件
		// （失败保留行）之后，再走与失败保留相同的两阶段替换。拷贝量不超过单个
		// 兜底文件的大小；关停只在自己的截止时间内等待回放，超时后进程退出留下的
		// .replay.tmp 在下次启动时被丢弃、源文件保留（至少一次语义，见 §20.4）。
		result.err = errors.Join(result.err, ctx.Err())
		result.failed = failedCount + uint64(len(batch))
		if result.processed == 0 && !retained {
			// 这个文件还没有任何批次提交、也没有要改写的保留行：源文件原样就是
			// 尚未写库的全部内容，不拷贝、不替换，只丢弃空的临时文件。
			result.err = errors.Join(result.err, discardTemp())
			return result
		}
		for _, item := range batch {
			if err := writeRetained(item.line); err != nil {
				result.err = errors.Join(result.err, err, discardTemp())
				return result
			}
		}
		batch = batch[:0]
		if len(partial) > 0 {
			retained = true
			if _, err := writer.Write(partial); err != nil {
				result.err = errors.Join(result.err, err, discardTemp())
				return result
			}
		}
		copied, err := io.Copy(writer, reader)
		if err != nil {
			result.err = errors.Join(result.err, err, discardTemp())
			return result
		}
		if copied > 0 {
			retained = true
		}
	} else {
		if err := flushBatch(); err != nil {
			result.err = errors.Join(result.err, err, discardTemp())
			result.failed = failedCount
			return result
		}
		result.failed = failedCount
	}
	if err := writer.Flush(); err != nil {
		result.err = errors.Join(result.err, err, discardTemp())
		return result
	}
	if err := out.Sync(); err != nil {
		result.err = errors.Join(result.err, err, discardTemp())
		return result
	}
	if err := closeOutput(); err != nil {
		result.err = errors.Join(result.err, err)
		return result
	}
	if err := closeSource(); err != nil {
		result.err = errors.Join(result.err, err)
		return result
	}
	if !retained {
		relayLogFallbackFileMu.Lock()
		removeTmpErr := os.Remove(tmp)
		removeSourceErr := os.Remove(path)
		relayLogFallbackFileMu.Unlock()
		if os.IsNotExist(removeTmpErr) {
			removeTmpErr = nil
		}
		if os.IsNotExist(removeSourceErr) {
			removeSourceErr = nil
		}
		result.err = errors.Join(result.err, removeTmpErr, removeSourceErr)
		return result
	}
	if result.err == nil {
		result.err = errors.New("fallback replay retained failed records")
	}
	source := path + ".replay.source"
	relayLogFallbackFileMu.Lock()
	defer relayLogFallbackFileMu.Unlock()
	if err := os.Rename(path, source); err != nil {
		result.err = errors.Join(result.err, err)
		return result
	}
	if err := os.Rename(tmp, path); err != nil {
		restoreErr := os.Rename(source, path)
		result.err = errors.Join(result.err, err, restoreErr)
		return result
	}
	if err := os.Remove(source); err != nil {
		result.err = errors.Join(result.err, err)
	}
	return result
}

// readRelayLogJSONLRecord 读出一条记录（去掉行尾）。ctx 取消时连同已经读出的
// 半条记录一起返回：这些字节已离开 reader，调用方要保留剩余内容就必须写回它们。
func readRelayLogJSONLRecord(ctx context.Context, reader *bufio.Reader) ([]byte, error) {
	var record []byte
	for {
		if err := ctx.Err(); err != nil {
			return record, err
		}
		fragment, err := reader.ReadSlice('\n')
		record = append(record, fragment...)
		switch {
		case err == nil:
			record = record[:len(record)-1]
			if len(record) > 0 && record[len(record)-1] == '\r' {
				record = record[:len(record)-1]
			}
			return record, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(record) == 0 {
				return nil, io.EOF
			}
			return record, nil
		default:
			return nil, err
		}
	}
}

func runRelayLogWorkerCycle(name string, fn func()) {
	defer func() {
		if recovered := recover(); recovered != nil {
			common.SysError(fmt.Sprintf("relay-log: %s cycle panic: %v", name, recovered))
		}
	}()
	fn()
}

// DrainRelayLogsSync synchronously persists everything the pipeline currently
// holds: buffered events, the retry backlog, and the accounting / quota-export
// continuations that normally run on the background workers. It blocks on the
// log DB and must never be called from the relay goroutine — it exists for
// maintenance callers and for tests that need buffered rows to be observable
// immediately instead of after the next flush cycle.
func DrainRelayLogsSync(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	relayLogFlushMu.Lock()
	runRelayLogWorkerCycle("drain", flushRelayLogs)
	runRelayLogWorkerCycle("drain-retry", flushRelayLogRetries)
	relayLogFlushMu.Unlock()
	for time.Now().Before(deadline) &&
		(len(relayLogContinuationCh) > 0 || len(relayLogFallbackCh) > 0 ||
			relayLogContinuationBusy.Load() > 0 || relayLogFallbackBusy.Load() > 0) {
		time.Sleep(time.Millisecond)
	}
}

func ShutdownRelayLogFlush(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	dbDrainDeadline := time.Now().Add(timeout * 3 / 4)
	relayLogAccepting.Store(false)
	stopRelayLogFallbackReplay()
	relayLogStopOnce.Do(func() { close(relayLogStopCh) })
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	select {
	case <-relayLogDoneCh:
	case <-ctx.Done():
	}
	select {
	case <-relayLogRetryDoneCh:
	case <-ctx.Done():
	}
	for time.Now().Before(dbDrainDeadline) && relayLogBacklog() > 0 {
		if relayLogFlushMu.TryLock() {
			flushRelayLogs()
			flushRelayLogRetries()
			relayLogFlushMu.Unlock()
		} else {
			time.Sleep(time.Millisecond)
		}
	}
	// 下面是最后一次取缓冲。封口之后才入队的事件（越过 intake 检查的在途 relay
	// goroutine）被拒收并计数，不会留在再也没人排空的缓冲里。
	sealRelayLogBuffers()
	// Anything left at the deadline is isolated from both databases.
	if !time.Now().Before(deadline) {
		dropRelayLogShutdownRemainder()
		logRelayLogShutdownComplete()
		return
	}
	for _, kind := range []relayLogKind{relayLogKindConsume, relayLogKindError} {
		events := takeRelayLogBatch(kind)
		handoffRelayLogEventsUntil(events, deadline)
	}
	// pending 只能在持有 relayLogFlushMu 时读写；超时仍未返回的刷盘周期还持有它。
	// retry 缓冲也在这把锁内取：持锁的周期写库失败时会继续往 retry 缓冲里放，
	// 锁外先取会把它随后放入的事件留在无人排空的缓冲里。
	if lockRelayLogFlushUntil(deadline) {
		retries := takeRelayLogRetryBatch(int(^uint(0) >> 1))
		consume := takeRelayLogPending(relayLogKindConsume, -1)
		errs := takeRelayLogPending(relayLogKindError, -1)
		relayLogFlushMu.Unlock()
		handoffRelayLogEventsUntil(retries, deadline)
		handoffRelayLogEventsUntil(consume, deadline)
		handoffRelayLogEventsUntil(errs, deadline)
	} else {
		handoffRelayLogEventsUntil(takeRelayLogRetryBatch(int(^uint(0)>>1)), deadline)
		reportRelayLogPendingHeldByOverrunFlush()
	}
	for time.Now().Before(deadline) &&
		(len(relayLogContinuationCh) > 0 || len(relayLogFallbackCh) > 0 ||
			relayLogContinuationBusy.Load() > 0 || relayLogFallbackBusy.Load() > 0) {
		time.Sleep(time.Millisecond)
	}
	if len(relayLogContinuationCh) == 0 && len(relayLogFallbackCh) == 0 &&
		relayLogContinuationBusy.Load() == 0 && relayLogFallbackBusy.Load() == 0 {
		relayLogAuxStopOnce.Do(func() {
			relayLogAuxSendMu.Lock()
			defer relayLogAuxSendMu.Unlock()
			relayLogAuxClosed.Store(true)
			close(relayLogContinuationCh)
			close(relayLogFallbackCh)
		})
		done := make(chan struct{})
		go func() {
			relayLogAuxWG.Wait()
			close(done)
		}()
		if remaining := time.Until(deadline); remaining > 0 {
			timer := time.NewTimer(remaining)
			select {
			case <-done:
				timer.Stop()
			case <-timer.C:
			}
		}
	}
	replayDone := make(chan struct{})
	go func() {
		relayLogReplayWG.Wait()
		close(replayDone)
	}()
	if remaining := time.Until(deadline); remaining > 0 {
		timer := time.NewTimer(remaining)
		select {
		case <-replayDone:
			timer.Stop()
		case <-timer.C:
		}
	}
	logRelayLogShutdownComplete()
}

func logRelayLogShutdownComplete() {
	if refused := relayLogIntakeRefused.Load(); refused > 0 {
		common.SysLog(fmt.Sprintf("relay-log: shutdown complete; %d relay logs refused while intake was closed", refused))
		return
	}
	common.SysLog("relay-log: shutdown complete")
}

func dropRelayLogShutdownRemainder() {
	var dropped []*relayLogEvent
	relayLogConsumeMu.Lock()
	dropped = append(dropped, relayLogConsumeBuf...)
	relayLogConsumeBuf = nil
	relayLogConsumeMu.Unlock()
	relayLogErrorMu.Lock()
	dropped = append(dropped, relayLogErrorBuf...)
	relayLogErrorBuf = nil
	relayLogErrorMu.Unlock()
	relayLogRetryMu.Lock()
	dropped = append(dropped, relayLogRetryBuf...)
	relayLogRetryBuf = nil
	relayLogRetryMu.Unlock()
	// The deadline has passed: take pending only if no overrunning flush cycle
	// still owns it, never wait for one.
	if relayLogFlushMu.TryLock() {
		dropped = append(dropped, relayLogPendingConsume...)
		dropped = append(dropped, relayLogPendingError...)
		clearRelayLogPending()
		relayLogFlushMu.Unlock()
	} else {
		reportRelayLogPendingHeldByOverrunFlush()
	}
	countRelayLogShutdownDropped(dropped, "remaining")
}

func handoffRelayLogEventsUntil(events []*relayLogEvent, deadline time.Time) {
	for index, event := range events {
		if !time.Now().Before(deadline) {
			countRelayLogShutdownDropped(events[index:], "serialized")
			return
		}
		dispatchRelayLogFallbackJob(event, true)
	}
}

// countRelayLogShutdownDropped records events abandoned at the shutdown
// deadline with the same meaning the counters have everywhere else: a lost log
// row counts under fallback_errors, lost accounting under continuation_dropped.
func countRelayLogShutdownDropped(events []*relayLogEvent, what string) {
	logs, accounting := 0, 0
	for _, event := range events {
		if event == nil {
			continue
		}
		if event.Log != nil {
			logs++
		}
		if event.Accounting != nil || event.QuotaData != nil {
			accounting++
		}
	}
	if logs > 0 {
		relayLogFallbackErrors.Add(uint64(logs))
	}
	if accounting > 0 {
		relayLogContinuationDrop.Add(uint64(accounting))
	}
	if logs > 0 || accounting > 0 {
		common.SysError(fmt.Sprintf("relay-log: shutdown deadline dropped %d %s events (%d log rows, %d accounting continuations)", len(events), what, logs, accounting))
	}
}

// lockRelayLogFlushUntil acquires relayLogFlushMu, giving up at deadline so a
// flush cycle that overran the shutdown timeout cannot hang the process exit.
func lockRelayLogFlushUntil(deadline time.Time) bool {
	for {
		if relayLogFlushMu.TryLock() {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}

// reportRelayLogPendingHeldByOverrunFlush is the known limit of design §19.1:
// a flush cycle still running past the shutdown deadline owns the pending
// lanes, so shutdown leaves them to it and reports their size (read from the
// atomic mirrors, never from the slices) instead of racing it.
func reportRelayLogPendingHeldByOverrunFlush() {
	held := relayLogPendingConsumeLen.Load() + relayLogPendingErrorLen.Load()
	if held > 0 {
		common.SysError(fmt.Sprintf("relay-log: shutdown deadline reached while a flush cycle still owns %d pending events; they are not handed off", held))
	}
}

func relayLogBacklog() int {
	relayLogConsumeMu.Lock()
	n := len(relayLogConsumeBuf)
	relayLogConsumeMu.Unlock()
	relayLogErrorMu.Lock()
	n += len(relayLogErrorBuf)
	relayLogErrorMu.Unlock()
	relayLogRetryMu.Lock()
	n += len(relayLogRetryBuf)
	relayLogRetryMu.Unlock()
	return n + int(relayLogPendingConsumeLen.Load()+relayLogPendingErrorLen.Load())
}

type RelayLogQueueStatus struct {
	Backlog  int    `json:"backlog"`
	Capacity int    `json:"capacity"`
	Dropped  uint64 `json:"dropped"`
	// LastFlushItems / LastFlushTookMs 描述最近一次写出过该类日志的刷盘：本类条数与该轮
	// 落库总耗时（消费与错误日志同轮落库，耗时相同）。进程启动后尚未刷过时为 0。
	LastFlushItems  int64 `json:"last_flush_items"`
	LastFlushTookMs int64 `json:"last_flush_took_ms"`
}
type RelayLogReplayStatus struct {
	State          string `json:"state"`
	PendingFiles   int    `json:"pending_files"`
	RunningFile    string `json:"running_file"`
	ProcessedTotal uint64 `json:"processed_total"`
	FailedTotal    uint64 `json:"failed_total"`
	StartedAt      int64  `json:"started_at"`
	FinishedAt     int64  `json:"finished_at"`
	LastError      string `json:"last_error"`
}
type RelayLogPipelineStatus struct {
	Enabled        bool                `json:"enabled"`
	CircuitState   string              `json:"circuit_state"`
	Consume        RelayLogQueueStatus `json:"consume"`
	Error          RelayLogQueueStatus `json:"error"`
	Retry          RelayLogQueueStatus `json:"retry"`
	PersistedTotal uint64              `json:"persisted_total"`
	FallbackTotal  uint64              `json:"fallback_total"`
	DBTimeoutTotal uint64              `json:"db_timeout_total"`
	// OldestEventAgeMs 是仍在内存中等待落库的日志（入队缓冲、worker 待写车道、重试队列）里
	// 最早入队那条已等待的毫秒数，全部为空时为 0。
	OldestEventAgeMs    int64 `json:"oldest_event_age_ms"`
	LastSuccessAt       int64 `json:"last_success_at"`
	LastErrorAt         int64 `json:"last_error_at"`
	ContinuationBacklog int   `json:"continuation_backlog"`
	// ContinuationDropped 是确实没有执行的记账条数（两条车道都满，或关停后到达）。
	ContinuationDropped uint64 `json:"continuation_dropped"`
	// ContinuationOverflowed 是 continuation 车道放不下、改由 fallback worker（或
	// 最后一条路）执行的记账条数。改道不是丢失，它只说明 continuation 车道在承压。
	ContinuationOverflowed uint64 `json:"continuation_overflowed"`
	FallbackBacklog        int    `json:"fallback_backlog"`
	// FallbackErrors 是既没进日志库、也没写进兜底文件的日志行数。
	FallbackErrors uint64 `json:"fallback_errors"`
	// ErrorLogsYielded 是为了给消费日志腾容量而主动丢弃的错误日志条数。
	// 它不为零就说明管道已经在异常状态，但账务仍然完整。
	ErrorLogsYielded uint64 `json:"error_logs_yielded"`
	// IntakeState 只反映生命周期：accepting（运行中）或 stopped（启动前/关停中）。
	// 压力从不关闭 intake。
	IntakeState string `json:"intake_state"`
	// IntakeRefused 是 intake 关闭时被拒收的日志条数（同时计入各自的 dropped）。
	IntakeRefused uint64 `json:"intake_refused"`
	// FallbackRetentionFull 按目录元数据实时计算：为 true 时下一批兜底写入会被拒绝，
	// 进不了日志库的日志将被丢弃，直到回填或清理出空间；空间恢复后自动变回 false。
	FallbackRetentionFull bool                 `json:"fallback_retention_full"`
	Replay                RelayLogReplayStatus `json:"replay"`
}

// relayLogOldestAgeMs 返回各队首入队时间（UnixNano，0 表示该队列为空）中最早者距 now 的毫秒数；全空为 0。
func relayLogOldestAgeMs(now time.Time, heads ...int64) int64 {
	oldest := int64(0)
	for _, head := range heads {
		if head > 0 && (oldest == 0 || head < oldest) {
			oldest = head
		}
	}
	if oldest == 0 {
		return 0
	}
	return max(0, now.Sub(time.Unix(0, oldest)).Milliseconds())
}

func GetRelayLogPipelineStatus() RelayLogPipelineStatus {
	// 各队列的队首就是该队列最早入队的事件，取长度时顺带读取，不另加锁或遍历。
	relayLogConsumeMu.Lock()
	cn := len(relayLogConsumeBuf)
	consumeOldest := relayLogEventsOldest(relayLogConsumeBuf)
	relayLogConsumeMu.Unlock()
	relayLogErrorMu.Lock()
	en := len(relayLogErrorBuf)
	errorOldest := relayLogEventsOldest(relayLogErrorBuf)
	relayLogErrorMu.Unlock()
	relayLogRetryMu.Lock()
	rn := len(relayLogRetryBuf)
	// 重试队列按失败先后追加，不按入队先后，逐条扫描（有 RetryBufMaxEntries 上界，只在状态接口执行）。
	retryOldest := relayLogEventsMinEnqueued(relayLogRetryBuf)
	relayLogRetryMu.Unlock()
	oldestAgeMs := relayLogOldestAgeMs(time.Now(), consumeOldest, errorOldest, retryOldest,
		relayLogPendingConsumeOldest.Load(), relayLogPendingErrorOldest.Load(),
		relayLogInflightFlushOldest.Load(), relayLogInflightRetryOldest.Load())
	state := "closed"
	if relayLogCircuitOpen() {
		state = "open"
	} else if relayLogCircuitOpenUntil.Load() != 0 {
		state = "half_open"
	}
	cfg := operation_setting.GetRelayLogPipelineSetting()
	relayLogReplayMu.RLock()
	replay := relayLogReplayStatus
	relayLogReplayMu.RUnlock()
	paths, replayListErr := listRelayLogReplayFiles()
	replay.PendingFiles = len(paths)
	if replayListErr != nil && replay.LastError == "" {
		replay.LastError = relayLogReplayErrorRecovery
	}
	retentionFull, retentionErr := relayLogFallbackRetentionFull()
	if retentionErr != nil {
		common.SysError("relay-log: inspect fallback retention failed: " + retentionErr.Error())
	}
	intakeState := "stopped"
	if relayLogAccepting.Load() && !relayLogBuffersSealed.Load() {
		intakeState = "accepting"
	}
	// backlog 含 worker 侧 pending 车道（与关停统计 relayLogBacklog 口径一致），
	// 因此积压时可能超过 capacity，最多为其两倍。
	cn += int(relayLogPendingConsumeLen.Load())
	en += int(relayLogPendingErrorLen.Load())
	return RelayLogPipelineStatus{Enabled: cfg.Enabled, CircuitState: state,
		IntakeState: intakeState, IntakeRefused: relayLogIntakeRefused.Load(), FallbackRetentionFull: retentionFull,
		Consume: RelayLogQueueStatus{Backlog: cn, Capacity: cfg.GetConsumeBufMaxEntries(), Dropped: relayLogDroppedConsume.Load(),
			LastFlushItems: relayLogLastFlushConsumeItems.Load(), LastFlushTookMs: relayLogLastFlushConsumeMs.Load()},
		Error: RelayLogQueueStatus{Backlog: en, Capacity: cfg.GetErrorBufMaxEntries(), Dropped: relayLogDroppedError.Load(),
			LastFlushItems: relayLogLastFlushErrorItems.Load(), LastFlushTookMs: relayLogLastFlushErrorMs.Load()},
		Retry: RelayLogQueueStatus{Backlog: rn, Capacity: operation_setting.GetRelayLogRetrySetting().GetRetryBufMaxEntries(), Dropped: relayLogDroppedRetry.Load(),
			LastFlushItems: relayLogLastFlushRetryItems.Load(), LastFlushTookMs: relayLogLastFlushRetryMs.Load()},
		PersistedTotal: relayLogPersistedTotal.Load(), FallbackTotal: relayLogFallbackTotal.Load(), DBTimeoutTotal: relayLogDBTimeoutTotal.Load(),
		OldestEventAgeMs: oldestAgeMs,
		LastSuccessAt:    relayLogLastSuccessAt.Load(), LastErrorAt: relayLogLastErrorAt.Load(),
		ContinuationBacklog: len(relayLogContinuationCh), ContinuationDropped: relayLogContinuationDrop.Load(),
		ContinuationOverflowed: relayLogContinuationOverflow.Load(), FallbackBacklog: len(relayLogFallbackCh),
		FallbackErrors:   relayLogFallbackErrors.Load(),
		ErrorLogsYielded: relayLogErrorYielded.Load(),
		Replay:           replay}
}

func resetRelayLogPipelineForTest() {
	relayLogConsumeMu.Lock()
	relayLogConsumeBuf = nil
	relayLogConsumeMu.Unlock()
	relayLogErrorMu.Lock()
	relayLogErrorBuf = nil
	relayLogErrorMu.Unlock()
	relayLogRetryMu.Lock()
	relayLogRetryBuf = nil
	relayLogRetryMu.Unlock()
	clearRelayLogPending()
	relayLogBuffersSealed.Store(false)
	relayLogIntakeRefused.Store(0)
	relayLogRetentionFullLogged.Store(false)
	relayLogDroppedConsume.Store(0)
	relayLogDroppedError.Store(0)
	relayLogDroppedRetry.Store(0)
	relayLogFallbackTotal.Store(0)
	relayLogPersistedTotal.Store(0)
	relayLogDBTimeoutTotal.Store(0)
	relayLogLastSuccessAt.Store(0)
	relayLogLastErrorAt.Store(0)
	relayLogCircuitFailures.Store(0)
	relayLogCircuitOpenUntil.Store(0)
	relayLogHalfOpenProbe.Store(false)
	relayLogContinuationDrop.Store(0)
	relayLogContinuationOverflow.Store(0)
	relayLogFallbackErrors.Store(0)
	relayLogErrorYielded.Store(0)
	for _, value := range []*atomic.Int64{&relayLogLastFlushConsumeItems, &relayLogLastFlushConsumeMs,
		&relayLogLastFlushErrorItems, &relayLogLastFlushErrorMs, &relayLogLastFlushRetryItems, &relayLogLastFlushRetryMs,
		&relayLogInflightFlushOldest, &relayLogInflightRetryOldest} {
		value.Store(0)
	}
}

func resetRelayLogReplayForTest() {
	relayLogReplayRunning.Store(false)
	relayLogReplayMu.Lock()
	relayLogReplayCancel = nil
	relayLogReplayStatus = RelayLogReplayStatus{State: "idle"}
	relayLogReplayMu.Unlock()
}

// enqueueAsyncRelayLog 返回 false 只表示 intake 已关闭（启动前/关停中）：
// 调用方自己兜底 accounting；quota 导出属于日志构造的一部分，在这里恰好派发一次。
func enqueueAsyncRelayLog(kind relayLogKind, log *Log, accounting *RelayLogAccountingPayload, quotaData *QuotaDataLogParams) bool {
	if !relayLogAccepting.Load() {
		if log != nil {
			countRelayLogIntakeRefused(kind)
		}
		dispatchRelayLogContinuation(&relayLogEvent{QuotaData: quotaData}, 0)
		return false
	}
	if !operation_setting.GetRelayLogPipelineSetting().Enabled {
		// Disabled means do not record relay logs. Accounting still continues
		// asynchronously with logID=0; callers must never sync-INSERT.
		dispatchRelayLogContinuation(&relayLogEvent{Accounting: accounting, QuotaData: quotaData}, 0)
		return true
	}
	if enqueueRelayLog(kind, &relayLogEvent{Log: log, Accounting: accounting, QuotaData: quotaData}) {
		return true
	}
	// 越过上面的检查后关停封口了缓冲（enqueueRelayLog 已计数），或 log 为 nil。
	dispatchRelayLogContinuation(&relayLogEvent{QuotaData: quotaData}, 0)
	return false
}

// insertRelayLogs 写入一批日志，对"已经带 id 的行"幂等。
//
// 批量插入超时时，服务端可能已经提交、客户端只看到 context deadline exceeded。
// 这批事件带着 RETURNING/LastInsertId 回填的 id 进重试或兜底文件；直接 INSERT
// 会撞主键，而且次次都撞，同批里从未提交的行也被拖着失败，最终整批进兜底文件、
// logs 表缺行（压测复现：1740 条兜底记录里 248 条其实已在库）。
//
// 带 id 的行只可能来自之前某次尝试。先按主键回查：库里那一行与本条的身份字段
// 一致，说明那次尝试其实已提交，跳过并保留 id；否则清掉 id 当新行插入。
//
// 不用 ON CONFLICT DO NOTHING：回滚事务分到的 id 在 SQLite（无独立序列）和
// MySQL 5.7 重启后会被别的行复用，按主键冲突跳过会把本条当成"已存在"悄悄丢掉；
// 另外 GORM 在 DoNothing 模式下回填 RETURNING 会跳过已带 id 的元素，与新行混在
// 同一条语句里时新行的 id 整体错位。回查只发生在重试与回放，首次写入的行都不带 id。
func insertRelayLogs(db *gorm.DB, logs []*Log, batchSize int) error {
	pending := logs
	if hasKnownRelayLogID(logs) {
		stored, err := storedRelayLogIdentities(db, logs, batchSize)
		if err != nil {
			return err
		}
		pending = make([]*Log, 0, len(logs))
		for _, log := range logs {
			if log.Id > 0 {
				if row, ok := stored[log.Id]; ok && row.sameAs(log) {
					continue
				}
				log.Id = 0
			}
			pending = append(pending, log)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	return db.CreateInBatches(&pending, batchSize).Error
}

func hasKnownRelayLogID(logs []*Log) bool {
	for _, log := range logs {
		if log.Id > 0 {
			return true
		}
	}
	return false
}

// relayLogIdentity 是判定"库里这一行就是本条"的字段集合。
type relayLogIdentity struct {
	Id        int
	RequestId string
	CreatedAt int64
	UserId    int
	Type      int
}

func (r relayLogIdentity) sameAs(log *Log) bool {
	return r.RequestId == log.RequestId && r.CreatedAt == log.CreatedAt &&
		r.UserId == log.UserId && r.Type == log.Type
}

func storedRelayLogIdentities(db *gorm.DB, logs []*Log, batchSize int) (map[int]relayLogIdentity, error) {
	ids := make([]int, 0, len(logs))
	for _, log := range logs {
		if log.Id > 0 {
			ids = append(ids, log.Id)
		}
	}
	if batchSize <= 0 {
		batchSize = len(ids)
	}
	stored := make(map[int]relayLogIdentity, len(ids))
	for start := 0; start < len(ids); start += batchSize {
		end := min(start+batchSize, len(ids))
		var rows []relayLogIdentity
		if err := db.Model(&Log{}).Select("id", "request_id", "created_at", "user_id", "type").
			Where("id IN ?", ids[start:end]).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			stored[row.Id] = row
		}
	}
	return stored, nil
}

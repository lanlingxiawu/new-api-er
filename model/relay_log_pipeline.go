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
}

var (
	relayLogConsumeMu        sync.Mutex
	relayLogConsumeBuf       []*relayLogEvent
	relayLogErrorMu          sync.Mutex
	relayLogErrorBuf         []*relayLogEvent
	relayLogRetryMu          sync.Mutex
	relayLogRetryBuf         []*relayLogEvent
	relayLogPendingConsume   []*relayLogEvent
	relayLogPendingError     []*relayLogEvent
	relayLogFlushMu          sync.Mutex
	relayLogWakeCh           = make(chan struct{}, 1)
	relayLogStopCh           = make(chan struct{})
	relayLogDoneCh           = make(chan struct{})
	relayLogRetryDoneCh      = make(chan struct{})
	relayLogContinuationCh   = make(chan relayLogContinuationJob, operation_setting.GetRelayLogPipelineSetting().GetContinuationBufMaxEntries())
	relayLogFallbackCh       = make(chan relayLogFallbackJob, operation_setting.GetRelayLogPipelineSetting().GetFallbackQueueCapacity())
	relayLogWorkerOnce       sync.Once
	relayLogAuxStarted       atomic.Bool
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
	relayLogContinuationDrop atomic.Uint64
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
			// Decreases apply immediately. Increases require restart because a Go
			// channel cannot grow; cap at the physical size so the configured
			// admission limit can never promise capacity the channel does not have.
			softCap := operation_setting.GetRelayLogPipelineSetting().GetContinuationBufMaxEntries()
			if physicalCap := cap(relayLogContinuationCh); softCap > physicalCap {
				softCap = physicalCap
			}
			if len(relayLogContinuationCh) >= softCap {
				relayLogContinuationDrop.Add(1)
				dispatchRelayLogFallbackJob(event, true)
				return
			}
			select {
			case relayLogContinuationCh <- job:
			default:
				// Keep relay non-blocking. The isolated fallback worker is the
				// overflow lane and executes accounting after writing JSONL.
				relayLogContinuationDrop.Add(1)
				dispatchRelayLogFallbackJob(event, true)
			}
		})
	}
}

func startRelayLogAuxWorkers() {
	relayLogWorkerOnce.Do(func() {
		relayLogAuxStarted.Store(true)
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

// ApplyRelayLogAuxQueueCapacities rebuilds the auxiliary queues from the
// persisted startup configuration before any worker can hold a channel
// reference. Go channels cannot be resized safely after workers start.
func ApplyRelayLogAuxQueueCapacities() error {
	if relayLogAuxStarted.Load() {
		return fmt.Errorf("relay log auxiliary workers already started")
	}
	setting := operation_setting.GetRelayLogPipelineSetting()
	relayLogContinuationCh = make(chan relayLogContinuationJob, setting.GetContinuationBufMaxEntries())
	relayLogFallbackCh = make(chan relayLogFallbackJob, setting.GetFallbackQueueCapacity())
	return nil
}

func relayLogContinuationWorker() {
	defer relayLogAuxWG.Done()
	defer func() {
		if recovered := recover(); recovered != nil {
			common.SysError(fmt.Sprintf("relay-log: continuation worker panic: %v", recovered))
			relayLogAccepting.Store(false)
		}
	}()
	for job := range relayLogContinuationCh {
		relayLogContinuationBusy.Add(1)
		func() {
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
		}()
	}
}

func dispatchRelayLogFallbackJob(event *relayLogEvent, executeContinuation bool) {
	if event == nil {
		return
	}
	startRelayLogAuxWorkers()
	// Decreases apply immediately; increases are capped by the startup-applied
	// physical channel size until restart.
	softCap := operation_setting.GetRelayLogPipelineSetting().GetFallbackQueueCapacity()
	if physicalCap := cap(relayLogFallbackCh); softCap > physicalCap {
		softCap = physicalCap
	}

	// 错误日志在通道用掉 1/N 之后就不再入队，把余量留给消费日志。
	// 这里直接丢弃而不是走下面的软/硬上限分支：错误日志没有记账负载，
	// 无需再往 continuation 通道兜一次，那样只会挤占记账自己的容量。
	if event.Kind == relayLogKindError && len(relayLogFallbackCh) >= softCap/relayLogErrorFallbackDivisor {
		relayLogErrorYielded.Add(1)
		queueRelayLogAlert("error logs yielding fallback capacity to consume logs")
		return
	}

	if len(relayLogFallbackCh) >= softCap {
		relayLogFallbackErrors.Add(1)
		queueRelayLogAlert("soft capacity reached")
		if executeContinuation {
			select {
			case relayLogContinuationCh <- relayLogContinuationJob{event: event, id: 0}:
			default:
				relayLogContinuationDrop.Add(1)
				relayLogAccepting.Store(false)
			}
		}
		return
	}
	select {
	case relayLogFallbackCh <- relayLogFallbackJob{event: event, executeContinuation: executeContinuation}:
	default:
		// A log-only fallback may be dropped under extreme pressure. Accounting
		// gets a second fixed worker lane and is never silently discarded here.
		relayLogFallbackErrors.Add(1)
		queueRelayLogAlert("hard capacity reached")
		if executeContinuation {
			select {
			case relayLogContinuationCh <- relayLogContinuationJob{event: event, id: 0}:
			default:
				relayLogContinuationDrop.Add(1)
				relayLogAccepting.Store(false)
				queueRelayLogAlert("accounting queues exhausted; relay log intake stopped")
			}
		}
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
	defer func() {
		if recovered := recover(); recovered != nil {
			common.SysError(fmt.Sprintf("relay-log: fallback worker panic: %v", recovered))
			relayLogAccepting.Store(false)
		}
	}()
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
		writeRelayLogFallbackBatch(batch)
	}
}

// writeRelayLogFallbackBatch 是独立函数，好让 busy 计数由 defer 释放：
// 放在循环体末尾自减的话，worker 一旦 panic 计数就永远停在 >0，
// 而 drain/关停流程都在等它归零。
func writeRelayLogFallbackBatch(batch []relayLogFallbackJob) {
	// defer 是 LIFO：先注册的计数归还最后执行，保证记账跑完之前 busy 不会归零，
	// 否则关停流程会在记账还没做完时就认为管道已经空闲。
	defer relayLogFallbackBusy.Add(-int64(len(batch)))
	defer runRelayLogFallbackContinuations(batch)

	// 序列化放在文件锁外，锁内只做 IO
	recordedAt := time.Now().Unix()
	payloads := make([][]byte, 0, len(batch))
	for _, job := range batch {
		data, err := common.Marshal(relayLogFallbackRecord{
			Version: 1, Kind: "relay_log_only", RecordedAt: recordedAt, Log: job.event.Log,
		})
		if err != nil {
			relayLogFallbackErrors.Add(1)
			common.SysError("relay-log: fallback marshal failed: " + err.Error())
			continue
		}
		payloads = append(payloads, data)
	}

	if err := appendRelayLogFallbackLines(payloads); err != nil {
		relayLogFallbackErrors.Add(uint64(len(payloads)))
		common.SysError("relay-log: fallback write failed: " + err.Error())
		if errors.Is(err, errRelayLogFallbackRetentionFull) {
			relayLogAccepting.Store(false)
			queueRelayLogAlert("retention cap reached; intake stopped")
		}
		return
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
				relayLogAccountingHandler(*event.Accounting, 0)
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
	maxFiles := cfg.FallbackMaxFiles
	if maxFiles <= 0 {
		maxFiles = 8
	}
	if maxFiles > 64 {
		maxFiles = 64
	}
	logicalFiles, err := relayLogLogicalFallbackCountLocked(path)
	if err != nil {
		return err
	}
	if logicalFiles >= maxFiles {
		return errRelayLogFallbackRetentionFull
	}
	if err := os.Rename(path, rotated); err != nil {
		return err
	}
	return nil
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
	err := LOG_DB.WithContext(ctx).CreateInBatches(&logs, operation_setting.GetRelayLogPipelineSetting().GetInnerBatchSize()).Error
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
	for outer := cfg.GetOuterBatchSize(); len(events) > 0; {
		n := outer
		if n > len(events) {
			n = len(events)
		}
		persistRelayLogEvents(events[:n])
		events = events[n:]
	}
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
	return out
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
	persistRelayLogEvents(retryable)
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
	return LOG_DB.WithContext(ctx).CreateInBatches(&logs, batchSize).Error
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
			lastError = relayLogReplayErrorFailed
			common.SysError("relay-log: manual fallback replay failed: " + result.err.Error())
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
		line []byte
		log  *Log
	}
	batchSize := operation_setting.GetRelayLogPipelineSetting().GetInnerBatchSize()
	batch := make([]replayItem, 0, batchSize)
	failedCount := uint64(0)
	writeRetained := func(line []byte) error {
		_, err := writer.Write(append(line, '\n'))
		return err
	}
	flushBatch := func() error {
		if len(batch) == 0 {
			return nil
		}
		logs := make([]*Log, 0, len(batch))
		for _, item := range batch {
			logs = append(logs, item.log)
		}
		writeCtx, cancel := context.WithTimeout(ctx, operation_setting.GetRelayLogPipelineSetting().GetWriteTimeout())
		writeErr := writeRelayLogReplayBatch(writeCtx, logs, batchSize)
		cancel()
		if writeErr != nil {
			for _, item := range batch {
				if err := writeRetained(item.line); err != nil {
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
	for {
		if ctx.Err() != nil {
			result.err = errors.Join(ctx.Err(), discardTemp())
			result.failed = failedCount + uint64(len(batch))
			return result
		}
		line, readErr := readRelayLogJSONLRecord(ctx, reader)
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			result.err = errors.Join(result.err, readErr, discardTemp())
			result.failed = failedCount
			return result
		}
		var record relayLogFallbackRecord
		if err := common.Unmarshal(line, &record); err != nil || record.Version != 1 {
			if err := writeRetained(line); err != nil {
				result.err = errors.Join(result.err, err)
				break
			}
			failedCount++
			result.err = errors.New("unsupported fallback record")
			continue
		}
		if record.Log == nil {
			continue
		}
		batch = append(batch, replayItem{line: line, log: record.Log})
		if len(batch) >= batchSize {
			if err := flushBatch(); err != nil {
				result.err = errors.Join(result.err, err)
				break
			}
		}
	}
	if err := flushBatch(); err != nil {
		result.err = errors.Join(result.err, err, discardTemp())
		result.failed = failedCount
		return result
	}
	if err := writer.Flush(); err != nil {
		result.err = errors.Join(result.err, err, discardTemp())
		result.failed = failedCount
		return result
	}
	if err := out.Sync(); err != nil {
		result.err = errors.Join(result.err, err, discardTemp())
		result.failed = failedCount
		return result
	}
	if err := closeOutput(); err != nil {
		result.err = errors.Join(result.err, err)
		result.failed = failedCount
		return result
	}
	if err := closeSource(); err != nil {
		result.err = errors.Join(result.err, err)
		result.failed = failedCount
		return result
	}
	result.failed = failedCount
	if failedCount == 0 {
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

func readRelayLogJSONLRecord(ctx context.Context, reader *bufio.Reader) ([]byte, error) {
	var record []byte
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
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
	// Anything left at the deadline is isolated from both databases.
	if !time.Now().Before(deadline) {
		dropRelayLogShutdownRemainder()
		common.SysLog("relay-log: shutdown complete")
		return
	}
	for _, kind := range []relayLogKind{relayLogKindConsume, relayLogKindError} {
		events := takeRelayLogBatch(kind)
		handoffRelayLogEventsUntil(events, deadline)
	}
	handoffRelayLogEventsUntil(takeRelayLogRetryBatch(int(^uint(0)>>1)), deadline)
	for _, pending := range []*[]*relayLogEvent{&relayLogPendingConsume, &relayLogPendingError} {
		handoffRelayLogEventsUntil(*pending, deadline)
		*pending = nil
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
	common.SysLog("relay-log: shutdown complete")
}

func dropRelayLogShutdownRemainder() {
	relayLogConsumeMu.Lock()
	count := len(relayLogConsumeBuf)
	relayLogConsumeBuf = nil
	relayLogConsumeMu.Unlock()
	relayLogErrorMu.Lock()
	count += len(relayLogErrorBuf)
	relayLogErrorBuf = nil
	relayLogErrorMu.Unlock()
	relayLogRetryMu.Lock()
	count += len(relayLogRetryBuf)
	relayLogRetryBuf = nil
	relayLogRetryMu.Unlock()
	count += len(relayLogPendingConsume) + len(relayLogPendingError)
	relayLogPendingConsume = nil
	relayLogPendingError = nil
	if count > 0 {
		relayLogFallbackErrors.Add(uint64(count))
		common.SysError(fmt.Sprintf("relay-log: shutdown deadline dropped %d remaining events", count))
	}
}

func handoffRelayLogEventsUntil(events []*relayLogEvent, deadline time.Time) {
	for index, event := range events {
		if !time.Now().Before(deadline) {
			remaining := len(events) - index
			relayLogFallbackErrors.Add(uint64(remaining))
			common.SysError(fmt.Sprintf("relay-log: shutdown deadline dropped %d serialized events", remaining))
			return
		}
		dispatchRelayLogFallbackJob(event, true)
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
	return n + len(relayLogPendingConsume) + len(relayLogPendingError)
}

type RelayLogQueueStatus struct {
	Backlog  int    `json:"backlog"`
	Capacity int    `json:"capacity"`
	Dropped  uint64 `json:"dropped"`
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
	Enabled             bool                `json:"enabled"`
	CircuitState        string              `json:"circuit_state"`
	Consume             RelayLogQueueStatus `json:"consume"`
	Error               RelayLogQueueStatus `json:"error"`
	Retry               RelayLogQueueStatus `json:"retry"`
	PersistedTotal      uint64              `json:"persisted_total"`
	FallbackTotal       uint64              `json:"fallback_total"`
	DBTimeoutTotal      uint64              `json:"db_timeout_total"`
	LastSuccessAt       int64               `json:"last_success_at"`
	LastErrorAt         int64               `json:"last_error_at"`
	ContinuationBacklog int                 `json:"continuation_backlog"`
	ContinuationDropped uint64              `json:"continuation_dropped"`
	FallbackBacklog     int                 `json:"fallback_backlog"`
	FallbackErrors      uint64              `json:"fallback_errors"`
	// ErrorLogsYielded 是为了给消费日志腾容量而主动丢弃的错误日志条数。
	// 它不为零就说明管道已经在异常状态，但账务仍然完整。
	ErrorLogsYielded uint64               `json:"error_logs_yielded"`
	Replay           RelayLogReplayStatus `json:"replay"`
}

func GetRelayLogPipelineStatus() RelayLogPipelineStatus {
	relayLogConsumeMu.Lock()
	cn := len(relayLogConsumeBuf)
	relayLogConsumeMu.Unlock()
	relayLogErrorMu.Lock()
	en := len(relayLogErrorBuf)
	relayLogErrorMu.Unlock()
	relayLogRetryMu.Lock()
	rn := len(relayLogRetryBuf)
	relayLogRetryMu.Unlock()
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
	return RelayLogPipelineStatus{Enabled: cfg.Enabled, CircuitState: state,
		Consume:        RelayLogQueueStatus{cn, cfg.GetConsumeBufMaxEntries(), relayLogDroppedConsume.Load()},
		Error:          RelayLogQueueStatus{en, cfg.GetErrorBufMaxEntries(), relayLogDroppedError.Load()},
		Retry:          RelayLogQueueStatus{rn, operation_setting.GetRelayLogRetrySetting().GetRetryBufMaxEntries(), relayLogDroppedRetry.Load()},
		PersistedTotal: relayLogPersistedTotal.Load(), FallbackTotal: relayLogFallbackTotal.Load(), DBTimeoutTotal: relayLogDBTimeoutTotal.Load(),
		LastSuccessAt: relayLogLastSuccessAt.Load(), LastErrorAt: relayLogLastErrorAt.Load(),
		ContinuationBacklog: len(relayLogContinuationCh), ContinuationDropped: relayLogContinuationDrop.Load(),
		FallbackBacklog: len(relayLogFallbackCh), FallbackErrors: relayLogFallbackErrors.Load(),
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
	relayLogPendingConsume = nil
	relayLogPendingError = nil
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
	relayLogFallbackErrors.Store(0)
	relayLogErrorYielded.Store(0)
}

func resetRelayLogReplayForTest() {
	relayLogReplayRunning.Store(false)
	relayLogReplayMu.Lock()
	relayLogReplayCancel = nil
	relayLogReplayStatus = RelayLogReplayStatus{State: "idle"}
	relayLogReplayMu.Unlock()
}

func enqueueAsyncRelayLog(kind relayLogKind, log *Log, accounting *RelayLogAccountingPayload, quotaData *QuotaDataLogParams) bool {
	if !relayLogAccepting.Load() {
		// Callers own accounting fallback on false; quota export is internal to
		// log construction and must be accounted here exactly once.
		dispatchRelayLogContinuation(&relayLogEvent{QuotaData: quotaData}, 0)
		return false
	}
	if !operation_setting.GetRelayLogPipelineSetting().Enabled {
		// Disabled means do not record relay logs. Accounting still continues
		// asynchronously with logID=0; callers must never sync-INSERT.
		dispatchRelayLogContinuation(&relayLogEvent{Accounting: accounting, QuotaData: quotaData}, 0)
		return true
	}
	return enqueueRelayLog(kind, &relayLogEvent{Log: log, Accounting: accounting, QuotaData: quotaData})
}

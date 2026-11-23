package model

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// logExportScanner 按时间窗口由早到晚扫描日志，逐批回调。
//
// 明细导出与聚合汇总共用它：限速闸门、CPU 水位、超时预算、取消检查、进度推进、
// 窗口宽度自适应全部只有这一份实现。聚合模式另写一套循环会让「窗口边界不重不漏」
// 这类不变式出现第二个必须单独维护的副本——而那正是最容易出错、后果最隐蔽的地方。
type logExportScanner struct {
	reader          LogExportDataReader
	job             *LogExportJob
	fields          []string
	needChannelName bool
	rctx            *rowCtx
	gate            *exportGate

	// Preserve the scanner's active-work budget. The outer job context separately
	// bounds total wall time, including resource and off-peak waits.
	startedAt time.Time
	timeout   time.Duration
	lastSaved time.Time
	// lastValidated 员工任务上次复核权限的时间，见 employeeExportRevalidateInterval。
	lastValidated time.Time
}

// employeeExportRevalidateInterval 是撤权后运行中员工任务最多继续扫描的时长。
// 不逐个时间窗复核：每次复核要 3-4 条主库查询，多客户长时间段的任务里其数量
// 会达到日志查询本身的数倍（空窗口也算）。
var employeeExportRevalidateInterval = time.Second

func newLogExportScanner(job *LogExportJob, fields []string, needChannelName bool, rctx *rowCtx) *logExportScanner {
	return &logExportScanner{
		job:             job,
		fields:          fields,
		needChannelName: needChannelName,
		rctx:            rctx,
		gate:            newExportGate(),
		startedAt:       time.Now(),
		// timeout_sec 在任务启动时读一次并固化，改配置不影响运行中的任务。
		timeout:   time.Duration(operation_setting.GetLogExportSetting().GetTimeoutSec()) * time.Second,
		lastSaved: time.Now(),
	}
}

func (s *logExportScanner) exceededBudget() bool {
	return time.Since(s.startedAt)-time.Duration(s.gate.ThrottledMs())*time.Millisecond > s.timeout
}

// run 扫描整个时间范围，每读到一批就调用 onBatch。
// onBatch 返回错误即中止整次扫描（写入失败、分片数上限等都走这条路）。
func (s *logExportScanner) run(ctx context.Context, onBatch func(logs []*Log) error) error {
	return s.runUsageBatches(ctx, func(batch LogExportBatch) error { return onBatch(batch.Logs) })
}

func (s *logExportScanner) runUsageBatches(ctx context.Context, onBatch func(LogExportBatch) error) error {
	if s.job.SelfExport {
		if !s.job.IsSelfExportFor(s.job.UserID) {
			return ErrEmployeeExportAccess
		}
	}
	if s.job.EmployeeScope == nil {
		return s.runBatches(ctx, onBatch)
	}
	original := s.job.Filters.UserId
	defer func() { s.job.Filters.UserId = original }()
	if err := ValidateEmployeeExportJob(ctx, s.job); err != nil {
		return err
	}
	s.lastValidated = time.Now()
	for _, id := range s.job.EmployeeScope.CustomerIDs {
		s.job.Filters.UserId = id
		if err := s.runBatches(ctx, onBatch); err != nil {
			return err
		}
	}
	return nil
}

func (s *logExportScanner) runBatches(ctx context.Context, onBatch func(LogExportBatch) error) error {
	job := s.job
	s.gate.ResetQueryBaselines()
	rawExport := LogExportHasRawColumn(job.Columns)
	if rawExport {
		if err := job.validateRawLogExport(ctx); err != nil {
			return err
		}
		s.lastValidated = time.Now()
	}
	exportStart := job.Filters.StartTimestamp
	exportEnd := job.Filters.EndTimestamp

	// 由早到晚推进：第一个分片装的是区间最早的数据。日志是异步批量落库的，正序扫描时
	// 尚未落库的近期行还在游标前方，等扫到时已经写入，比逆序更不容易漏掉靠近当前时刻的日志。
	//
	// 窗口是闭区间 [windowStart, windowEnd]，所以下一个窗口必须从 windowEnd+1 起步，
	// 否则边界那一秒会被相邻两个窗口各扫一次——每个窗口的游标是独立重置的，
	// 重复扫到的行会真的写进文件。时间戳是整秒，加 1 既不重叠也不留缝。
	//
	// The configured width is a starting point. Cheap sparse windows can grow;
	// expensive reads can shrink below it. A timeout lowers the maximum width
	// for the remainder of this scan, preventing immediate repeated expansion.
	configuredWindowSec := operation_setting.GetLogExportSetting().GetWindowSec()
	windowSec := configuredWindowSec
	maxWindowSec := int64(operation_setting.MaxLogExportWindowSec)
	windowStart := exportStart
	remainingEnd := exportEnd
	reverse := s.reader != nil && s.reader.Reverse()
	for windowStart <= exportEnd {
		cfgWindowSec := operation_setting.GetLogExportSetting().GetWindowSec()
		// Honor live configuration changes within the learned timeout ceiling.
		if cfgWindowSec != configuredWindowSec {
			windowSec = cfgWindowSec
			configuredWindowSec = cfgWindowSec
		}
		windowSec = min(max(windowSec, operation_setting.MinLogExportWindowSec), maxWindowSec)

		windowEnd := windowStart + windowSec - 1
		if windowEnd > exportEnd {
			windowEnd = exportEnd
		}
		if reverse {
			windowEnd = remainingEnd
			windowStart = max(exportStart, windowEnd-windowSec+1)
		}
		var cursor any
		var cursorPosition int64
		// windowRows/windowBatches 只服务于窗口宽度自适应，不参与计费与进度。
		var windowRows, windowBatches int
		var maxQueryCost time.Duration
		lastBatchSize := 0
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			if isLogExportJobCanceled(job.JobID) {
				return context.Canceled
			}

			batchSize := operation_setting.GetLogExportSetting().GetBatchSize()
			if rawExport {
				batchSize = min(batchSize, logExportRawBatchSize)
			}
			if s.reader != nil {
				batchSize = s.reader.BatchSize(batchSize)
			}
			lastBatchSize = batchSize
			// 开工前只过「系统忙不忙」这道闸；计费放到这一批读完之后，
			// 因为只有查完才知道实际读到了多少行。
			s.gate.Before(ctx)
			if err := ctx.Err(); err != nil {
				return err
			}
			job.ThrottledMs = s.gate.ThrottledMs()
			if s.exceededBudget() {
				return context.DeadlineExceeded
			}

			if job.EmployeeScope != nil && time.Since(s.lastValidated) >= employeeExportRevalidateInterval {
				if err := ValidateEmployeeExportJob(ctx, job); err != nil {
					return err
				}
				s.lastValidated = time.Now()
			}
			if rawExport && time.Since(s.lastValidated) >= time.Second {
				if err := job.validateRawLogExport(ctx); err != nil {
					return err
				}
				s.lastValidated = time.Now()
			}
			queryTimeout := logExportQueryTimeout(operation_setting.GetLogExportSetting().GetBatchQueryTimeoutSec())
			queryCtx, queryCancel := context.WithTimeout(ctx, queryTimeout)
			if s.reader != nil {
				if err := s.reader.Validate(queryCtx, false); err != nil {
					queryCancel()
					return err
				}
			}
			observation := exportQuerySample{windowSec: windowEnd - windowStart + 1, batchSize: batchSize}
			started := time.Now()
			var batch LogExportBatch
			var err error
			if s.reader != nil {
				batch, err = s.reader.Read(queryCtx, windowStart, windowEnd, cursor, batchSize)
			} else {
				position, _ := cursor.(*logExportCursor)
				if rawExport {
					batch, err = scanRawLogExportBatch(queryCtx, job.Filters, windowStart, windowEnd, position, batchSize)
				} else {
					batch.Logs, err = scanLogExportBatch(queryCtx, job.Filters, s.fields, windowStart, windowEnd, position, batchSize)
				}
				batch.Scanned = len(batch.Logs)
				batch.Done = batch.Scanned < batchSize
				batch.Position = windowEnd
				if len(batch.Logs) > 0 {
					next := nextLogExportCursor(batch.Logs)
					batch.Cursor, batch.Position = next, next.CreatedAt
				}
			}
			queryErr := queryCtx.Err()
			queryCancel()
			queryCost := time.Since(started)
			observation.cost = queryCost
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if errors.Is(err, ErrLogExportAccessChanged) || errors.Is(err, ErrLogExportRiskChanged) ||
					(!errors.Is(err, context.DeadlineExceeded) && !errors.Is(queryErr, context.DeadlineExceeded)) {
					return err
				}
				// Discard failed rows. Keep the last successful secondary cursor at
				// the boundary second while shrinking only the unread interval.
				unreadStart, unreadEnd := windowStart, windowEnd
				if cursor != nil {
					if reverse {
						unreadEnd = min(windowEnd, cursorPosition)
					} else {
						unreadStart = max(windowStart, cursorPosition)
					}
				}
				unreadSec := unreadEnd - unreadStart + 1
				if windowEnd-windowStart+1 <= operation_setting.MinLogExportWindowSec {
					return context.DeadlineExceeded
				}
				// A cursor near the end of a wide query may leave a short tail.
				// Try that bounded tail once before applying the hard minimum.
				windowSec = max(unreadSec/2, operation_setting.MinLogExportWindowSec)
				maxWindowSec = min(maxWindowSec, windowSec)
				if reverse {
					windowEnd = unreadEnd
					windowStart = max(unreadStart, windowEnd-windowSec+1)
				} else {
					windowStart = unreadStart
					windowEnd = min(unreadEnd, windowStart+windowSec-1)
				}
				windowRows, windowBatches, maxQueryCost = 0, 0, 0
				common.SysLog(fmt.Sprintf("log export: retrying timed-out read job=%s start=%d end=%d query_ms=%d", job.JobID, windowStart, windowEnd, queryCost.Milliseconds()))
				observation.timedOut = true
				s.gate.Observe(observation)
				s.gate.Charge(ctx, 0, batchSize)
				continue
			}
			maxQueryCost = max(maxQueryCost, queryCost)
			observation.rowsRead = batch.Scanned
			s.gate.Observe(observation)

			if s.needChannelName {
				fillLogExportChannelNames(ctx, batch.Logs, s.rctx.channelNames)
			}

			job.ScannedRows += int64(batch.Scanned)
			if err := onBatch(batch); err != nil {
				return err
			}

			// 为刚处理完的这一批付费。放在处理之后，让休眠成为两次数据库查询之间
			// 真实的间隔；放在 break 之前，保证窗口最后那一批（必然读不满）也计费。
			windowRows += batch.Scanned
			windowBatches++
			s.gate.Charge(ctx, batch.Scanned, batchSize)
			if err := ctx.Err(); err != nil {
				return err
			}
			job.ThrottledMs = s.gate.ThrottledMs()

			cursor = batch.Cursor
			cursorPosition = batch.Position
			// 进度取游标当前所在时刻；没有游标（窗口空/已扫完）才退回窗口末尾。
			// 直接用 windowEnd 会让进度在窗口刚开始时就跳到窗口末尾，虚报进度。
			position := batch.Position
			if batch.Done {
				position = windowEnd
			}
			if reverse {
				if batch.Done {
					position = windowStart
				}
				position = exportEnd - position + exportStart
			}
			job.Progress = logExportProgress(exportStart, exportEnd, position)
			if job.EmployeeScope != nil {
				for index, id := range job.EmployeeScope.CustomerIDs {
					if id == job.Filters.UserId {
						job.Progress = (index*100 + job.Progress) / len(job.EmployeeScope.CustomerIDs)
						break
					}
				}
			}
			if time.Since(s.lastSaved) >= 500*time.Millisecond {
				UpdateLogExportJob(job)
				s.lastSaved = time.Now()
			}
			if batch.Done {
				break
			}
		}

		// 宽度自适应。只在窗口跑满整个宽度时调整——被 exportEnd 截断的最后一个
		// 窗口行数天然偏少，拿它当「稀疏」的证据会得出错误结论。
		if windowEnd < exportEnd || (reverse && windowStart > exportStart) {
			queryTimeout := logExportQueryTimeout(operation_setting.GetLogExportSetting().GetBatchQueryTimeoutSec())
			windowSec = nextLogExportWindowSec(windowSec, cfgWindowSec, windowRows, windowBatches, lastBatchSize, maxQueryCost, queryTimeout)
		}
		if reverse {
			if windowStart == exportStart {
				break
			}
			remainingEnd = windowStart - 1
		} else {
			windowStart = windowEnd + 1
		}
	}
	return nil
}

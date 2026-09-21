package model

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// logExportScanner 按时间窗口由早到晚扫描日志，逐批回调。
//
// 明细导出与聚合汇总共用它：限速闸门、CPU 水位、超时预算、取消检查、进度推进、
// 窗口宽度自适应全部只有这一份实现。聚合模式另写一套循环会让「窗口边界不重不漏」
// 这类不变式出现第二个必须单独维护的副本——而那正是最容易出错、后果最隐蔽的地方。
type logExportScanner struct {
	job             *LogExportJob
	fields          []string
	needChannelName bool
	rctx            *rowCtx
	gate            *exportGate

	// startedAt/timeout 构成超时预算：闸门让出的时间不计入，
	// 否则一开低峰模式，白天创建的任务会先干等几小时再以超时失败。
	startedAt time.Time
	timeout   time.Duration
	lastSaved time.Time
}

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
	worked := time.Since(s.startedAt) - time.Duration(s.job.ThrottledMs)*time.Millisecond
	return worked > s.timeout
}

// run 扫描整个时间范围，每读到一批就调用 onBatch。
// onBatch 返回错误即中止整次扫描（xlsx 行数上限、分片数上限等都走这条路）。
func (s *logExportScanner) run(ctx context.Context, onBatch func(logs []*Log) error) error {
	job := s.job
	exportStart := job.Filters.StartTimestamp
	exportEnd := job.Filters.EndTimestamp

	// 由早到晚推进：第一个分片装的是区间最早的数据。日志是异步批量落库的，正序扫描时
	// 尚未落库的近期行还在游标前方，等扫到时已经写入，比逆序更不容易漏掉靠近当前时刻的日志。
	//
	// 窗口是闭区间 [windowStart, windowEnd]，所以下一个窗口必须从 windowEnd+1 起步，
	// 否则边界那一秒会被相邻两个窗口各扫一次——每个窗口的游标是独立重置的，
	// 重复扫到的行会真的写进文件。时间戳是整秒，加 1 既不重叠也不留缝。
	//
	// 窗口宽度自适应：配置的 window_sec 是**起点与下限**而不是固定值。稀疏时间段里
	// 一个窗口连一批都读不满，固定 1 小时窗口会让 31 天的导出白跑 744 次数据库往返；
	// 读不满就把下个窗口翻倍，读得很满就减半收回。密集数据下宽度收敛回配置值，
	// 「把单次索引区间限死」的原始意图不变。
	windowSec := operation_setting.GetLogExportSetting().GetWindowSec()
	windowStart := exportStart
	for windowStart <= exportEnd {
		cfgWindowSec := operation_setting.GetLogExportSetting().GetWindowSec()
		// 配置被调大时立刻跟上；上限沿用配置层的硬上限，不另设一套。
		windowSec = min(max(windowSec, cfgWindowSec), operation_setting.MaxLogExportWindowSec)

		windowEnd := windowStart + windowSec - 1
		if windowEnd > exportEnd {
			windowEnd = exportEnd
		}
		var cursor *logExportCursor
		// windowRows/windowBatches 只服务于窗口宽度自适应，不参与计费与进度。
		var windowRows, windowBatches int
		lastBatchSize := 0
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			if isLogExportJobCanceled(job.JobID) {
				return context.Canceled
			}

			batchSize := operation_setting.GetLogExportSetting().GetBatchSize()
			lastBatchSize = batchSize
			// 开工前只过「系统忙不忙」这道闸；计费放到这一批读完之后，
			// 因为只有查完才知道实际读到了多少行。
			s.gate.Before(ctx)
			job.ThrottledMs = s.gate.ThrottledMs()
			if s.exceededBudget() {
				return context.DeadlineExceeded
			}

			queryCtx, queryCancel := context.WithTimeout(ctx,
				logExportQueryTimeout(operation_setting.GetLogExportSetting().GetBatchQueryTimeoutSec()))
			started := time.Now()
			logs, err := scanLogExportBatch(queryCtx, job.Filters, s.fields, windowStart, windowEnd, cursor, batchSize)
			queryCancel()
			if err != nil {
				return err
			}
			s.gate.Observe(time.Since(started))

			if s.needChannelName {
				fillLogExportChannelNames(ctx, logs, s.rctx.channelNames)
			}

			job.ScannedRows += int64(len(logs))
			if err := onBatch(logs); err != nil {
				return err
			}

			// 为刚处理完的这一批付费。放在处理之后，让休眠成为两次数据库查询之间
			// 真实的间隔；放在 break 之前，保证窗口最后那一批（必然读不满）也计费。
			windowRows += len(logs)
			windowBatches++
			s.gate.Charge(ctx, len(logs), batchSize)
			job.ThrottledMs = s.gate.ThrottledMs()

			if len(logs) > 0 {
				cursor = nextLogExportCursor(logs)
			}
			// 进度取游标当前所在时刻；没有游标（窗口空/已扫完）才退回窗口末尾。
			// 直接用 windowEnd 会让进度在窗口刚开始时就跳到窗口末尾，虚报进度。
			position := windowEnd
			if cursor != nil && cursor.CreatedAt > 0 {
				position = cursor.CreatedAt
			}
			job.Progress = logExportProgress(exportStart, exportEnd, position)
			if time.Since(s.lastSaved) >= 500*time.Millisecond {
				UpdateLogExportJob(job)
				s.lastSaved = time.Now()
			}
			if len(logs) < batchSize {
				break
			}
		}

		// 宽度自适应。只在窗口跑满整个宽度时调整——被 exportEnd 截断的最后一个
		// 窗口行数天然偏少，拿它当「稀疏」的证据会得出错误结论。
		if windowEnd < exportEnd {
			windowSec = nextLogExportWindowSec(windowSec, cfgWindowSec, windowRows, windowBatches, lastBatchSize)
		}
		windowStart = windowEnd + 1
	}
	return nil
}

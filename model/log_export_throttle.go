package model

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// exportGate 是导出任务的资源闸门。每批查询前调用 Wait，让导出在 CPU 紧张、
// 非低峰时段或超过行速率时主动退让，保证正常服务不受可感知影响。
//
// 三道闸依次为：低峰时段 → CPU 水位 → 令牌桶速率。所有阈值都在使用点现调
// getter，因此配置热更新对运行中的任务下一批即生效。
type exportGate struct {
	// throttledMs 累计因闸门让出的毫秒数，回写进任务状态供运维观察。
	throttledMs int64
	// penalty 由实测查询耗时反馈得出的额外休眠，查询变慢说明 DB 有压力。
	penalty time.Duration
	// baselineCost 首批查询耗时，作为后续对比基线。
	baselineCost time.Duration

	// sleepFn 与 nowFn 供测试注入，生产使用真实时钟。
	sleepFn func(ctx context.Context, d time.Duration)
	nowFn   func() time.Time
	// cpuFn 返回当前 CPU 使用率与该读数是否可用。
	cpuFn func() (float64, bool)
}

const (
	// exportGateMaxSoftFactor 软限区间内额外休眠的最大倍数。
	exportGateMaxSoftFactor = 4
	// exportGateMaxPenaltyFactor penalty 相对基础休眠的最大倍数。
	exportGateMaxPenaltyFactor = 8
)

func newExportGate() *exportGate {
	return &exportGate{
		sleepFn: sleepCtx,
		nowFn:   time.Now,
		cpuFn:   currentCPUUsage,
	}
}

// currentCPUUsage 读取系统 CPU 使用率。
//
// 注意：common.GetSystemStatus() 仅在性能监控开启时才更新，否则恒为零值。
// 零值必须被解释为「无 CPU 信息」而不是「CPU 空闲」——否则监控关闭的部署会
// 直接放开限速，把导出变成压垮服务的元凶。
func currentCPUUsage() (float64, bool) {
	status := common.GetSystemStatus()
	if status.CPUUsage <= 0 {
		return 0, false
	}
	return status.CPUUsage, true
}

func sleepCtx(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// ThrottledMs 返回累计让出的毫秒数。
func (g *exportGate) ThrottledMs() int64 { return g.throttledMs }

// Wait 在取下一批数据前执行三道闸。ctx 取消时立即返回。
func (g *exportGate) Wait(ctx context.Context, rows int) {
	if ctx.Err() != nil {
		return
	}
	g.waitOffpeak(ctx)
	extra := g.waitCPU(ctx)
	g.waitRate(ctx, rows)

	base := time.Duration(operation_setting.GetLogExportSetting().GetBatchSleepMs()) * time.Millisecond
	g.sleepAndCount(ctx, base+extra+g.penalty)
}

// waitOffpeak 在开启低峰模式且当前不在窗口内时等待，任务保持 running 不丢进度。
func (g *exportGate) waitOffpeak(ctx context.Context) {
	for ctx.Err() == nil {
		setting := operation_setting.GetLogExportSetting()
		if !setting.OffpeakOnly || setting.InOffpeakWindow(g.nowFn()) {
			return
		}
		// 低峰窗口以分钟为粒度，用固定的粗间隔轮询即可，不必精确到秒。
		g.sleepAndCount(ctx, exportOffpeakPollInterval)
	}
}

const exportOffpeakPollInterval = 30 * time.Second

// waitCPU 实现软/硬水位：硬限以上暂停，软硬之间线性加大休眠，返回额外休眠时长。
func (g *exportGate) waitCPU(ctx context.Context) time.Duration {
	for ctx.Err() == nil {
		setting := operation_setting.GetLogExportSetting()
		cpu, ok := g.cpuFn()
		if !ok {
			// 无 CPU 读数：退化为固定节流，绝不放开限速。
			return 0
		}
		hard := setting.GetCPUHardLimit()
		if cpu >= hard {
			g.sleepAndCount(ctx, time.Duration(setting.GetCPUCheckIntervalMs())*time.Millisecond)
			continue
		}
		soft := setting.GetCPUSoftLimit()
		if cpu < soft || hard <= soft {
			return 0
		}
		base := time.Duration(setting.GetBatchSleepMs()) * time.Millisecond
		ratio := (cpu - soft) / (hard - soft)
		return time.Duration(float64(base) * ratio * exportGateMaxSoftFactor)
	}
	return 0
}

// waitRate 令牌桶限速：把批行数换算成应当占用的时间，不足则补足。
func (g *exportGate) waitRate(ctx context.Context, rows int) {
	if rows <= 0 {
		return
	}
	maxRows := operation_setting.GetLogExportSetting().GetMaxRowsPerSec()
	if maxRows <= 0 {
		return
	}
	need := time.Duration(float64(rows) / float64(maxRows) * float64(time.Second))
	base := time.Duration(operation_setting.GetLogExportSetting().GetBatchSleepMs()) * time.Millisecond
	// 基础休眠已经占掉一部分配额，只补差额。
	if need > base {
		g.sleepAndCount(ctx, need-base)
	}
}

// Observe 用实测查询耗时反馈调节：查询显著变慢说明 DB 有压力，加大 penalty；
// 恢复正常则逐步衰减。
func (g *exportGate) Observe(cost time.Duration) {
	if cost <= 0 {
		return
	}
	if g.baselineCost == 0 {
		g.baselineCost = cost
		return
	}
	base := time.Duration(operation_setting.GetLogExportSetting().GetBatchSleepMs()) * time.Millisecond
	maxPenalty := base * exportGateMaxPenaltyFactor
	switch {
	case cost > g.baselineCost*2:
		if g.penalty == 0 {
			g.penalty = base
		} else {
			g.penalty *= 2
		}
		if g.penalty > maxPenalty {
			g.penalty = maxPenalty
		}
	case cost < g.baselineCost*6/5:
		g.penalty /= 2
		if g.penalty < time.Millisecond {
			g.penalty = 0
		}
	}
	// 基线取观察到的最小耗时，避免第一批恰好偏慢导致后续永远不触发 penalty。
	if cost < g.baselineCost {
		g.baselineCost = cost
	}
}

func (g *exportGate) sleepAndCount(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	g.sleepFn(ctx, d)
	g.throttledMs += d.Milliseconds()
}

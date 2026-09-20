package model

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// exportGate 是导出任务的资源闸门，按「先判断能不能开工，再为做完的工作付费」
// 分成两个时机：
//
//	Before  取下一批之前：低峰时段、CPU 水位 —— 语义是「系统忙就别开工」
//	Charge  这一批读完之后：基础休眠、令牌桶速率、penalty —— 语义是「为刚做完的工作付费」
//
// 计费必须发生在查询之后，因为只有查完才知道这一批到底读到了多少行。早先的实现
// 在查询前按「请求的批大小」一次性收费，于是每个窗口的最后一批（必然读不满）和
// 每个空窗口都要按满批付钱——这笔与数据量无关的固定税在宽时间范围上会吃掉几乎
// 全部耗时（实测 7 天范围：真实工作 4 ms，闸门休眠 25,200 ms）。
//
// 所有阈值都在使用点现调 getter，因此配置热更新对运行中的任务下一批即生效。
type exportGate struct {
	// throttledMs 累计因闸门让出的毫秒数，回写进任务状态供运维观察。
	throttledMs int64
	// penalty 由实测查询耗时反馈得出的额外休眠，查询变慢说明 DB 有压力。
	penalty time.Duration
	// baselineCost 首批查询耗时，作为后续对比基线。
	baselineCost time.Duration
	// softExtra Before 在 CPU 软限区间内算出的额外休眠，留给 Charge 一并结算。
	softExtra time.Duration
	// debt 尚未兑现的休眠。读到的行数很少时单批应付的休眠可能不足 1 毫秒，
	// 逐次 time.NewTimer 的开销比休眠本身还大，因此累积到阈值再真正睡一次。
	debt time.Duration

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
	// exportGateMinSleep 债务攒到这个量级才真正休眠一次，避免海量微休眠。
	exportGateMinSleep = 5 * time.Millisecond
	// exportGateMinCharge 单批最低计费。读到 0 行时应付休眠为 0，若不给地板，
	// 连续的空窗口会退化成不带任何间隔的连续查询。
	exportGateMinCharge = 2 * time.Millisecond
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

// Before 在取下一批数据之前判断「现在能不能开工」：低峰时段与 CPU 水位。
// 软限区间内算出的额外休眠不在这里兑现，留到 Charge 与本批的计费一起结算。
// ctx 取消时立即返回。
func (g *exportGate) Before(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	g.waitOffpeak(ctx)
	g.softExtra = g.waitCPU(ctx)
}

// Charge 为刚读到的 rowsRead 行付费。batchSize 是本次请求的批大小，
// 用于把基础休眠按产出比例缩放——读满一批仍然睡满 batch_sleep_ms，
// 读到 0 行则只付地板价。
func (g *exportGate) Charge(ctx context.Context, rowsRead, batchSize int) {
	if ctx.Err() != nil {
		return
	}
	setting := operation_setting.GetLogExportSetting()
	base := time.Duration(setting.GetBatchSleepMs()) * time.Millisecond

	factor := 0.0
	if rowsRead > 0 && batchSize > 0 {
		factor = min(float64(rowsRead)/float64(batchSize), 1)
	}
	owed := time.Duration(float64(base) * factor)

	// 令牌桶：按**实际读到的行数**换算应当占用的时间，基础休眠已占掉一部分配额，
	// 只取两者的较大值（等价于「只补差额」）。
	if maxRows := setting.GetMaxRowsPerSec(); maxRows > 0 && rowsRead > 0 {
		owed = max(owed, time.Duration(float64(rowsRead)/float64(maxRows)*float64(time.Second)))
	}

	// 软限额外休眠与 penalty 是「CPU 吃紧 / 数据库变慢」的压力信号，与这一批读到
	// 多少行无关，刻意不随产出缩放——该退让的时候读得少也要退让。
	owed += g.softExtra + g.penalty
	g.softExtra = 0

	g.debt += max(owed, exportGateMinCharge)
	if g.debt < exportGateMinSleep {
		return
	}
	due := g.debt
	g.debt = 0
	g.sleepAndCount(ctx, due)
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

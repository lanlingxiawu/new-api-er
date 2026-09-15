package service

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// 渠道每日金额上限的累计与触发。设计见 docs/design/channel-daily-quota-limit.md 与
// docs/design/channel-limit-upstream-basis-and-timed-recovery.md。
//
// 统计口径只有一种：上游消耗 = 不乘用户分组倍率的基础消耗 × 渠道成本系数（见
// channel_daily_limit_upstream.go）。
//
// 主链路约束（Rule 0）：
//   - relay goroutine 上只在结算时多算一份基础消耗（纯算术，随记账载荷入队）；累计本身发生在
//     异步日志管线上，成本系数也在那里读取。
//   - 触发禁用走本功能独享的有界队列，队列满时丢弃并重试，绝不阻塞提交方。
//
// 两种恢复模式共用这一套累计器：
//   - 按日模式：计数键 = (自然日, 渠道)，次日零点恢复或不恢复；
//   - 限时模式：每笔消耗同时记入 (自然日, 渠道) 与 (本轮开始时刻, 渠道) 两个键——前者只供
//     「今日用量」展示，后者才参与上限判定；触顶禁用，N 分钟后恢复并开启新一轮。

const (
	dailyLimitShardCount = 32
	// dailyLimitDisableQueueSize 有界禁用队列容量，量级对齐「配置了上限的渠道数」。
	dailyLimitDisableQueueSize = 256
	// dailyLimitDisableWorkers 固定 worker 数，与全局 gopool 隔离（后者默认容量为
	// math.MaxInt32，是无界池，不满足 Rule 8.2）。
	dailyLimitDisableWorkers = 2
	// dailyLimitFlushInterval 内存增量落库间隔，同时也是配置快照的跨节点刷新周期。
	dailyLimitFlushInterval = 5 * time.Second
	// dailyLimitFlushMaxBackoff flush 失败时的退避上限。
	dailyLimitFlushMaxBackoff = 60 * time.Second
)

// dailyKey 是累计器的计数键。period=false 时 statDate 是自然日的 StatDate；
// period=true 时 statDate 存的是限时模式某一轮的开始时刻（channels.daily_limit_period_start）。
type dailyKey struct {
	statDate  int64
	channelId int
	period    bool
}

type dailyDelta struct {
	cost int64
}

type dailyTotal struct {
	cost int64
}

type dailyShard struct {
	mu      sync.Mutex
	pending map[dailyKey]*dailyDelta
	total   map[dailyKey]dailyTotal
	tripped map[dailyKey]struct{}
	// rearmed 记录「人工放行」时刻的用量水位，只由 rearmTripped 在**确实清掉了一个
	// tripped 标记**时写入。没有它的话，管理员手动启用一个已达上限的渠道会在下一个
	// flush 周期被 recheckAfterFlush 无条件撤销（当日累计仍然 >= 上限），管理员当天
	// 拿不回这个渠道。有了水位，判定改成「必须在水位之上产生新的消费才重新禁用」。
	//
	// 只在清掉 tripped 时记录，是为了不误伤另一条路径：管理员把上限调低到今日已用
	// 之下时，渠道从未 tripped 过，没有水位，下一个 flush 周期照常立即禁用。
	//
	// 限时模式下手动启用会开启新一轮（新的计数键），水位只会留在已经作废的旧键上。
	rearmed map[dailyKey]dailyTotal
}

type channelLimitConfig struct {
	limit          int64
	recoverMinutes int
	periodStart    int64
}

// timed 报告渠道是否处于限时恢复模式。
func (cfg channelLimitConfig) timed() bool {
	return cfg.recoverMinutes > 0
}

type disableRequest struct {
	channelId int
	// statDate 是触发判定所用计数键的日期或轮次开始时刻，period 区分两者（同 dailyKey）。
	statDate int64
	period   bool
	// today 是触发时刻的自然日 StatDate，写入 daily_limit_disabled_date 供「今日已达上限」筛选。
	// 为 0 时按 statDate 处理（按日模式二者相同）。
	today          int64
	limit          int64
	used           int64
	recoverMinutes int
}

func (req disableRequest) key() dailyKey {
	return dailyKey{statDate: req.statDate, channelId: req.channelId, period: req.period}
}

func (req disableRequest) disabledDate() int64 {
	if req.today > 0 {
		return req.today
	}
	return req.statDate
}

type dailyDayRange struct {
	timezone string
	start    int64
	end      int64
}

var dailyLimit = struct {
	shards [dailyLimitShardCount]*dailyShard
	// configs 是「配置了上限的渠道」快照，由 flusher 周期刷新，累计入口无锁读取。
	configs atomic.Pointer[map[int]channelLimitConfig]
	// dayRange 缓存当前自然日边界，跨界或时区变更才重算。
	dayRange atomic.Pointer[dailyDayRange]

	disableQueue chan disableRequest
	startOnce    sync.Once

	// enabledAt 记录总开关最近一次从关到开的时刻；恢复扫描据此跳过关闭期间遗留的禁用，
	// 避免「一开开关就批量放行」。
	enabledAt atomic.Int64
	// lastEnabled 是三态：unknown / off / on。零值必须是 unknown，
	// 否则进程启动会被误判成一次 off→on 切换，见 trackEnabledTransition。
	lastEnabled atomic.Int32
}{}

const (
	dailyLimitSwitchUnknown int32 = 0
	dailyLimitSwitchOff     int32 = 1
	dailyLimitSwitchOn      int32 = 2
)

func init() {
	for i := range dailyLimit.shards {
		dailyLimit.shards[i] = &dailyShard{
			pending: make(map[dailyKey]*dailyDelta),
			total:   make(map[dailyKey]dailyTotal),
			tripped: make(map[dailyKey]struct{}),
			rearmed: make(map[dailyKey]dailyTotal),
		}
	}
	dailyLimit.disableQueue = make(chan disableRequest, dailyLimitDisableQueueSize)
	empty := map[int]channelLimitConfig{}
	dailyLimit.configs.Store(&empty)
}

func shardFor(channelId int) *dailyShard {
	idx := channelId % dailyLimitShardCount
	if idx < 0 {
		idx += dailyLimitShardCount
	}
	return dailyLimit.shards[idx]
}

// currentStatDate 返回当前时刻在配置时区下的 StatDate，带原子缓存。
func currentStatDate(setting *operation_setting.ChannelDailyLimitSetting) int64 {
	now := time.Now().Unix()
	if cached := dailyLimit.dayRange.Load(); cached != nil {
		if cached.timezone == setting.Timezone && now >= cached.start && now < cached.end {
			return cached.start
		}
	}
	start, end := operation_setting.ChannelDailyLimitDayRange(now, setting.Timezone)
	dailyLimit.dayRange.Store(&dailyDayRange{timezone: setting.Timezone, start: start, end: end})
	return start
}

// lookupLimitConfig 从进程内快照读取渠道的上限配置，纯内存。
func lookupLimitConfig(channelId int) (channelLimitConfig, bool) {
	configs := dailyLimit.configs.Load()
	if configs == nil {
		return channelLimitConfig{}, false
	}
	cfg, ok := (*configs)[channelId]
	return cfg, ok
}

// limitKey 返回参与上限判定的计数键：限时模式看本轮，按日模式看今日。
func limitKey(cfg channelLimitConfig, channelId int, today int64) dailyKey {
	if cfg.timed() {
		return dailyKey{statDate: cfg.periodStart, channelId: channelId, period: true}
	}
	return dailyKey{statDate: today, channelId: channelId}
}

func newDisableRequest(key dailyKey, today int64, cfg channelLimitConfig, used int64) disableRequest {
	return disableRequest{
		channelId:      key.channelId,
		statDate:       key.statDate,
		period:         key.period,
		today:          today,
		limit:          cfg.limit,
		used:           used,
		recoverMinutes: cfg.recoverMinutes,
	}
}

// accumulate 累计一笔上游消耗（quota 单位）并判定是否触顶。纯内存，可被任意 goroutine 调用。
func accumulate(channelId int, cost int64) {
	if channelId <= 0 || cost <= 0 {
		return
	}
	setting := operation_setting.GetChannelDailyLimitSnapshot()
	if setting == nil || !setting.Enabled {
		return
	}
	cfg, ok := lookupLimitConfig(channelId)
	if !ok || cfg.limit <= 0 {
		// 未配置上限的渠道零开销：不占内存、不参与判定。
		return
	}

	today := currentStatDate(setting)
	dayKey := dailyKey{statDate: today, channelId: channelId}
	key := limitKey(cfg, channelId, today)

	shard := shardFor(channelId)
	shard.mu.Lock()
	// 今日用量始终要记（展示用）；限时模式再多记一份本轮用量（判定用）。
	addPendingLocked(shard, dayKey, cost)
	if key != dayKey {
		addPendingLocked(shard, key, cost)
	}
	if _, tripped := shard.tripped[key]; tripped {
		// 已提交禁用，仍然累计（数字要准），但不重复触发。
		shard.mu.Unlock()
		return
	}
	total, reached := limitReachedLocked(shard, key, cfg)
	if reached {
		markTrippedLocked(shard, key)
	}
	shard.mu.Unlock()

	if reached {
		submitDisable(newDisableRequest(key, today, cfg, total))
	}
}

func addPendingLocked(shard *dailyShard, key dailyKey, cost int64) {
	delta := shard.pending[key]
	if delta == nil {
		delta = &dailyDelta{}
		shard.pending[key] = delta
	}
	delta.cost += cost
}

// totalLocked 返回「已落库汇总 + 本地未落库增量」的合计。
func totalLocked(shard *dailyShard, key dailyKey) int64 {
	sum := shard.total[key].cost
	if delta := shard.pending[key]; delta != nil {
		sum += delta.cost
	}
	return sum
}

// currentTotalsLocked 返回「已落库汇总 + 本地未落库增量」的合计快照（用于记放行水位）。
func currentTotalsLocked(shard *dailyShard, key dailyKey) dailyTotal {
	return dailyTotal{cost: totalLocked(shard, key)}
}

// limitReachedLocked 判定当前是否越限，并返回合计。
//
// 除了「合计 >= 上限」，还要求合计**高于人工放行水位**：管理员手动启用一个已被限额
// 禁用的渠道后，当日累计本来就还压在上限之上，只看合计会让 recheckAfterFlush 在下一个
// flush 周期（默认 5 秒）把这次启用无条件撤销掉。有水位之后，语义回到「手动启用后继续
// 消费才会再次禁用」。没有水位的 key（从未被人工放行过）行为完全不变。
func limitReachedLocked(shard *dailyShard, key dailyKey, cfg channelLimitConfig) (int64, bool) {
	total := totalLocked(shard, key)
	if total < cfg.limit {
		return total, false
	}
	if mark, ok := shard.rearmed[key]; ok && total <= mark.cost {
		return total, false
	}
	return total, true
}

// markTrippedLocked 打上 tripped 标记，并丢弃已经用过的放行水位。
// 水位是一次性的：这一轮既然重新触发了禁用，下一次放行会重新记录新的水位。
func markTrippedLocked(shard *dailyShard, key dailyKey) {
	shard.tripped[key] = struct{}{}
	delete(shard.rearmed, key)
}

// submitDisable 投递禁用请求。队列满时直接丢弃并清除 tripped，交由下一个 flush 周期重试——
// 提交方可能是记账管线的 worker，绝不允许阻塞。
func submitDisable(req disableRequest) {
	select {
	case dailyLimit.disableQueue <- req:
	default:
		clearTripped(req.key())
		common.SysError(fmt.Sprintf(
			"channel daily limit: disable queue full, dropped request for channel_id=%d, will retry next flush", req.channelId))
	}
}

// clearTripped 清除标记但**不**记放行水位，交由下一个 flush 周期原样重判。
// 用于「禁用没能落地、还需要重试」的场景：队列满、DB 报错、判定作废。
func clearTripped(key dailyKey) {
	shard := shardFor(key.channelId)
	shard.mu.Lock()
	delete(shard.tripped, key)
	shard.mu.Unlock()
}

// releaseTripped 清除标记并记下当前用量水位，用于「渠道已经不是启用态、无需再禁用」。
//
// 与 clearTripped 的区别很重要：这里如果只是清标记，渠道累计始终压在上限之上，
// recheckAfterFlush 每个 flush 周期都会重新提交一次必定失败的禁用请求；而且管理员之后手动
// 启用该渠道时，rearmTripped 找不到 tripped 标记，也就不会记水位，这次启用又会在下一个
// 周期被立刻撤销。记了水位之后，渠道重新启用后要有新的消费才会再次触发禁用。
func releaseTripped(key dailyKey) {
	shard := shardFor(key.channelId)
	shard.mu.Lock()
	delete(shard.tripped, key)
	shard.rearmed[key] = currentTotalsLocked(shard, key)
	shard.mu.Unlock()
}

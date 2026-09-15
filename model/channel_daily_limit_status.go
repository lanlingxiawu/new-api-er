package model

import (
	"fmt"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

// 每日金额上限的状态转换。设计见 docs/design/channel-daily-quota-limit.md §6.2 / §6.3。
//
// 为什么不复用 UpdateChannelStatus：那个函数是「读整行 → 改字段 → 全量 Save」，并且
//  1. 先写内存缓存再写 DB，DB 失败会留下缓存与 DB 不一致；
//  2. 目标状态相同时直接返回，导致「已被限额禁用的渠道随后因上游报错再次禁用」时
//     标记列不会被清除，次日会把一个真坏的渠道自动恢复；
//  3. 全量 Save 会覆盖并发的管理员操作。
//
// 因此这里一律用条件式列更新：状态转换的前置条件写进 WHERE，由 DB 保证只有一个赢家，
// 且 DB 成功之后才同步内存缓存与 abilities。

// DisableChannelForDailyLimit 因达到每日金额上限而禁用渠道（按日模式）。
//
// 只在渠道当前为「启用」时才生效：这既是并发安全的转换条件，也保证不会覆盖管理员的
// 手动禁用或上游报错禁用。返回 true 表示本次调用赢得了竞争并完成了禁用。
func DisableChannelForDailyLimit(channelId int, statDate int64, reason string) (bool, error) {
	return disableChannelForLimit(channelId, statDate, reason, "daily_limit_recover_minutes = 0")
}

// DisableChannelForTimedLimit 限时恢复模式下因本轮上游消耗达到上限而禁用渠道。
//
// 除「当前为启用」外，还要求渠道仍处于请求所属的那一轮（daily_limit_period_start = periodStart）。
// 这是跨节点正确性的关键：渠道刚被恢复进入新一轮时，快照还停在上一轮的节点会用上一轮
// 已经满额的用量立刻再提交一次禁用，这个条件保证那种「旧轮次触发」必然落空。
func DisableChannelForTimedLimit(channelId int, periodStart int64, statDate int64, reason string) (bool, error) {
	return disableChannelForLimit(channelId, statDate, reason,
		"daily_limit_recover_minutes > 0 AND daily_limit_period_start = ?", periodStart)
}

func disableChannelForLimit(channelId int, statDate int64, reason string, modeWhere string, modeArgs ...any) (bool, error) {
	if channelId <= 0 {
		return false, nil
	}
	now := common.GetTimestamp()

	channel, err := GetChannelById(channelId, false)
	if err != nil {
		return false, err
	}
	// other_info 里的 status_reason / status_time 沿用既有约定，供前端状态列展示。
	info := channel.GetOtherInfo()
	info["status_reason"] = reason
	info["status_time"] = now
	otherInfo, err := common.Marshal(info)
	if err != nil {
		return false, err
	}

	res := DB.Model(&Channel{}).
		Where("id = ? AND status = ?", channelId, common.ChannelStatusEnabled).
		Where(modeWhere, modeArgs...).
		Updates(map[string]any{
			"status":                    common.ChannelStatusAutoDisabled,
			"daily_limit_disabled_at":   now,
			"daily_limit_disabled_date": statDate,
			"other_info":                string(otherInfo),
		})
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		// 渠道已不是启用态（被管理员禁用、被报错禁用，或另一节点抢先）。
		return false, nil
	}

	// DB 成功之后才同步缓存与 abilities，顺序与旧实现相反，保证 DB 失败时无可见副作用。
	CacheUpdateChannelDailyLimitMarks(channelId, now, statDate)
	syncChannelStatusSideEffects(channelId, common.ChannelStatusAutoDisabled)
	return true, nil
}

// RecoverChannelFromDailyLimit 日切时恢复因每日上限被禁用的渠道。
//
// 转换条件同样写进 WHERE：必须仍处于「被本功能禁用」且禁用日早于今日。返回 true 表示
// 本次调用完成了恢复。
func RecoverChannelFromDailyLimit(channelId int, todayStatDate int64) (bool, error) {
	if channelId <= 0 {
		return false, nil
	}
	channel, err := GetChannelById(channelId, false)
	if err != nil {
		return false, err
	}
	info := channel.GetOtherInfo()
	info["status_reason"] = "daily quota limit reset"
	info["status_time"] = common.GetTimestamp()
	otherInfo, err := common.Marshal(info)
	if err != nil {
		return false, err
	}

	res := DB.Model(&Channel{}).
		Where("id = ? AND status = ? AND daily_limit_disabled_at > 0 AND daily_limit_disabled_date < ? AND daily_limit_recover_minutes = 0",
			channelId, common.ChannelStatusAutoDisabled, todayStatDate).
		Updates(map[string]any{
			"status":                    common.ChannelStatusEnabled,
			"daily_limit_disabled_at":   0,
			"daily_limit_disabled_date": 0,
			"other_info":                string(otherInfo),
		})
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		return false, nil
	}
	CacheUpdateChannelDailyLimitMarks(channelId, 0, 0)
	syncChannelStatusSideEffects(channelId, common.ChannelStatusEnabled)
	return true, nil
}

// timedRecoveryDueWhere 是「限时模式、被本功能禁用、且已满恢复间隔」的判定。
// 只用加法与乘法比较，SQLite / MySQL / PostgreSQL 三库通用。
const timedRecoveryDueWhere = "status = ? AND daily_limit_disabled_at > 0 AND daily_limit_recover_minutes > 0 " +
	"AND daily_limit_auto_recover = 1 AND daily_limit_disabled_at + daily_limit_recover_minutes * 60 <= ?"

// RecoverChannelFromTimedLimit 限时恢复模式下，禁用满 N 分钟后恢复渠道并开启新一轮。
//
// 条件式单条 UPDATE：状态、标记与 period_start 一次写入，period_start = now 让用量从 0 起算。
// 返回 true 表示本次调用完成了恢复。
func RecoverChannelFromTimedLimit(channelId int, now int64) (bool, error) {
	if channelId <= 0 {
		return false, nil
	}
	channel, err := GetChannelById(channelId, false)
	if err != nil {
		return false, err
	}
	info := channel.GetOtherInfo()
	info["status_reason"] = "timed quota limit reset"
	info["status_time"] = now
	otherInfo, err := common.Marshal(info)
	if err != nil {
		return false, err
	}

	res := DB.Model(&Channel{}).
		Where("id = ?", channelId).
		Where(timedRecoveryDueWhere, common.ChannelStatusAutoDisabled, now).
		Updates(map[string]any{
			"status":                    common.ChannelStatusEnabled,
			"daily_limit_disabled_at":   0,
			"daily_limit_disabled_date": 0,
			"daily_limit_period_start":  now,
			"other_info":                string(otherInfo),
		})
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		return false, nil
	}
	CacheUpdateChannelDailyLimitMarks(channelId, 0, 0)
	syncChannelStatusSideEffects(channelId, common.ChannelStatusEnabled)
	return true, nil
}

// ListTimedLimitRecoveryCandidates 返回已满恢复间隔、可以限时恢复的渠道 id。
func ListTimedLimitRecoveryCandidates(now int64, enabledAfter int64) ([]int, error) {
	var ids []int
	query := DB.Model(&Channel{}).Where(timedRecoveryDueWhere, common.ChannelStatusAutoDisabled, now)
	if enabledAfter > 0 {
		// 同按日模式：全局开关重新打开后不补做关闭期间遗留的恢复。
		query = query.Where("daily_limit_disabled_at >= ?", enabledAfter)
	}
	err := query.Order("id ASC").Pluck("id", &ids).Error
	return ids, err
}

// clearDailyLimitMarksIfPresent 给 UpdateChannelStatus 用的清除入口：发一条带
// daily_limit_disabled_at > 0 条件的 UPDATE，绝大多数是零行更新。
//
// 只对「可能带标记」的渠道执行（见 dailyLimitMarksMayExist），其余渠道的状态变更不多一次写库。
//
// 同一渠道的并发调用会合并：限额禁用后上游报错的在途失败会同时涌入，而连接池与 relay 共用，
// 同一时刻最多一条 UPDATE 在跑。合并不削弱正确性：调用方只认「在它到达之后才开始」的那一次
// 清除——到达时已经在跑的那一条可能早于另一节点写入标记，搭它的车就漏清了。
func clearDailyLimitMarksIfPresent(channelId int) {
	if channelId <= 0 || !dailyLimitMarksMayExist(channelId) {
		return
	}
	flight := dailyLimitMarkClearFlightFor(channelId)
	flight.mu.Lock()
	defer flight.mu.Unlock()
	need := flight.started + 1
	for flight.done < need {
		if flight.running {
			flight.cond.Wait()
			continue
		}
		flight.started++
		generation := flight.started
		flight.running = true
		flight.mu.Unlock()
		runDailyLimitMarkClear(channelId)
		flight.mu.Lock()
		flight.running = false
		flight.done = generation
		flight.cond.Broadcast()
	}
}

// dailyLimitMarksMayExist 判断渠道是否可能带有限额禁用标记。标记只会由限额禁用写入，而写入时
// 渠道必然配置了上限，所以缓存里「没有上限、也没有标记」的渠道不必写库。
//
// 缓存由全量同步整行加载，上限与标记来自同一行，不会出现「缓存里上限为 0、库里却有标记」
// 的组合；唯一的窗口是上限刚在别的节点设置、本节点尚未同步，此期间渠道恰好触顶并在本节点
// 报错——极窄。缓存未启用或查不到时一律按「可能有」处理。
func dailyLimitMarksMayExist(channelId int) bool {
	if !common.MemoryCacheEnabled {
		return true
	}
	cached, err := CacheGetChannel(channelId)
	if err != nil || cached == nil {
		return true
	}
	return cached.DailyQuotaLimit > 0 || cached.DailyLimitDisabledAt > 0
}

// dailyLimitMarkClearFlight 是单个渠道的清除合并状态。
type dailyLimitMarkClearFlight struct {
	mu      sync.Mutex
	cond    *sync.Cond
	running bool
	started uint64 // 已开始的清除次数
	done    uint64 // 最近一次完成的清除序号；清除是串行的，所以单调递增
}

// dailyLimitMarkClearFlights: channelId -> *dailyLimitMarkClearFlight。条目不回收，数量以渠道数为上限。
var dailyLimitMarkClearFlights sync.Map

func dailyLimitMarkClearFlightFor(channelId int) *dailyLimitMarkClearFlight {
	if existing, ok := dailyLimitMarkClearFlights.Load(channelId); ok {
		return existing.(*dailyLimitMarkClearFlight)
	}
	flight := &dailyLimitMarkClearFlight{}
	flight.cond = sync.NewCond(&flight.mu)
	actual, _ := dailyLimitMarkClearFlights.LoadOrStore(channelId, flight)
	return actual.(*dailyLimitMarkClearFlight)
}

// dailyLimitMarkClearExec 执行一次清除。包级变量只为测试能观察合并行为。
var dailyLimitMarkClearExec = clearDailyLimitMarksLocked

// runDailyLimitMarkClear 在合并锁之外执行一次清除。panic 必须在这里吞掉并记录：否则 running
// 永远不会复位，该渠道之后的所有调用都会卡在 cond.Wait 上。
func runDailyLimitMarkClear(channelId int) {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("panic while clearing daily limit marks: channel_id=%d, error=%v", channelId, r))
		}
	}()
	dailyLimitMarkClearExec(channelId)
}

func clearDailyLimitMarksLocked(channelId int) {
	res := DB.Model(&Channel{}).
		Where("id = ? AND daily_limit_disabled_at > 0", channelId).
		Updates(map[string]any{
			"daily_limit_disabled_at":   0,
			"daily_limit_disabled_date": 0,
		})
	if res.Error != nil {
		common.SysError(fmt.Sprintf("failed to clear daily limit marks: channel_id=%d, error=%v", channelId, res.Error))
		return
	}
	if res.RowsAffected > 0 {
		// 只在真的清掉了标记时才同步缓存：缓存写要拿全局写锁，relay 选渠道读的正是这把锁。
		CacheUpdateChannelDailyLimitMarks(channelId, 0, 0)
	}
}

// syncChannelStatusSideEffects 在 DB 状态变更成功后同步内存缓存与 abilities。
func syncChannelStatusSideEffects(channelId int, status int) {
	if common.MemoryCacheEnabled {
		CacheUpdateChannelStatus(channelId, status)
	}
	if err := UpdateAbilityStatus(channelId, status == common.ChannelStatusEnabled); err != nil {
		common.SysError(fmt.Sprintf("failed to update ability status: channel_id=%d, error=%v", channelId, err))
	}
}

// DailyLimitConfig 是每日上限功能需要的渠道配置快照条目。
type DailyLimitConfig struct {
	ChannelId      int
	LimitQuota     int64
	RecoverMinutes int
	PeriodStart    int64
	Status         int
	DisabledAt     int64
}

// LoadDailyLimitConfigs 读取所有配置了每日上限的渠道。命中 daily_quota_limit 索引，
// 结果量级为数十到数百行，由各节点周期刷新到进程内快照供观察者无锁读取。
//
// 顺带返回 status 与 daily_limit_disabled_at，用于 tripped 标记的重新武装判定
// （管理员手动启用渠道后，需要让当天的限额判定重新生效）。
func LoadDailyLimitConfigs() ([]DailyLimitConfig, error) {
	var rows []struct {
		Id                       int
		DailyQuotaLimit          int64
		DailyLimitRecoverMinutes int
		DailyLimitPeriodStart    int64
		Status                   int
		DailyLimitDisabledAt     int64
	}
	err := DB.Model(&Channel{}).
		Select("id, daily_quota_limit, daily_limit_recover_minutes, daily_limit_period_start, status, daily_limit_disabled_at").
		Where("daily_quota_limit > 0").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	configs := make([]DailyLimitConfig, 0, len(rows))
	for _, row := range rows {
		configs = append(configs, DailyLimitConfig{
			ChannelId:      row.Id,
			LimitQuota:     row.DailyQuotaLimit,
			RecoverMinutes: row.DailyLimitRecoverMinutes,
			PeriodStart:    row.DailyLimitPeriodStart,
			Status:         row.Status,
			DisabledAt:     row.DailyLimitDisabledAt,
		})
	}
	return configs, nil
}

// ListDailyLimitRecoveryCandidates 返回可以次日自动恢复的渠道 id（按日模式）。
//
// auto_recover 的过滤下推到 SQL：关闭了自动恢复的渠道不应每分钟被取回一次并记日志。
// 限时模式的渠道不在此列，它们由 ListTimedLimitRecoveryCandidates 按恢复间隔处理。
func ListDailyLimitRecoveryCandidates(todayStatDate int64, enabledAfter int64) ([]int, error) {
	var ids []int
	query := DB.Model(&Channel{}).
		Where("status = ? AND daily_limit_disabled_at > 0 AND daily_limit_disabled_date < ? AND daily_limit_auto_recover = ? AND daily_limit_recover_minutes = 0",
			common.ChannelStatusAutoDisabled, todayStatDate, 1)
	if enabledAfter > 0 {
		// 全局开关重新打开后不补做关闭期间遗留的自动恢复，避免「一开开关就批量放行」。
		query = query.Where("daily_limit_disabled_at >= ?", enabledAfter)
	}
	err := query.Order("id ASC").Pluck("id", &ids).Error
	return ids, err
}


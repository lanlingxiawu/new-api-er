package model

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ChannelLimitPeriodUsage 记录限时恢复模式下每一轮的上游消耗累计。
// 设计见 docs/design/channel-limit-upstream-basis-and-timed-recovery.md §3.2。
//
// 与 channel_daily_usages 分表：后者的 stat_date 是自然日语义，混入轮次会破坏「今日用量」
// 展示、日切清理与「今日已达上限」筛选。跨节点一致性同样只依赖行上的原子
// `cost_quota + delta` upsert，不与 relay 热路径共享任何资源。
type ChannelLimitPeriodUsage struct {
	Id        int `json:"id"`
	ChannelId int `json:"channel_id" gorm:"uniqueIndex:idx_channel_limit_period,priority:1"`
	// PeriodStart 该轮开始时刻（unix 秒），与 channels.daily_limit_period_start 对应。
	PeriodStart int64 `json:"period_start" gorm:"uniqueIndex:idx_channel_limit_period,priority:2;index:idx_channel_limit_period_start"`
	CostQuota   int64 `json:"cost_quota" gorm:"bigint;default:0"`
	UpdatedAt   int64 `json:"updated_at" gorm:"bigint;default:0"`
}

// ChannelPeriodKey 标识某个渠道的某一轮。
type ChannelPeriodKey struct {
	ChannelId   int
	PeriodStart int64
}

// ChannelPeriodDelta 是一次 flush 中某个 (渠道, 轮次) 的增量。
type ChannelPeriodDelta struct {
	ChannelId   int
	PeriodStart int64
	CostQuota   int64
}

// UpsertChannelLimitPeriodUsage 原子累加一批轮次增量，语义与 UpsertChannelDailyUsage 相同：
// clause.OnConflict 三库通用；逐条写入，返回已处理条数，调用方只回退 deltas[processed:]。
func UpsertChannelLimitPeriodUsage(deltas []ChannelPeriodDelta) (int, error) {
	if len(deltas) == 0 {
		return 0, nil
	}
	now := common.GetTimestamp()
	for i, delta := range deltas {
		if delta.ChannelId <= 0 || delta.CostQuota == 0 {
			continue
		}
		row := ChannelLimitPeriodUsage{
			ChannelId:   delta.ChannelId,
			PeriodStart: delta.PeriodStart,
			CostQuota:   delta.CostQuota,
			UpdatedAt:   now,
		}
		err := DB.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "channel_id"}, {Name: "period_start"}},
			DoUpdates: clause.Assignments(map[string]any{
				"cost_quota": gorm.Expr("channel_limit_period_usages.cost_quota + ?", delta.CostQuota),
				"updated_at": now,
			}),
		}).Create(&row).Error
		if err != nil {
			return i, fmt.Errorf("upsert channel limit period usage (channel_id=%d, period_start=%d): %w", delta.ChannelId, delta.PeriodStart, err)
		}
	}
	return len(deltas), nil
}

// GetChannelLimitPeriodUsages 批量读取若干 (渠道, 轮次) 的累计，不存在的键不出现在结果里。
//
// 不用 `(channel_id, period_start) IN ((?, ?), ...)`：行值比较在老版本 SQLite 上不可用。
// 改为两列各自 IN 取超集、再按键过滤，仍然命中唯一索引。
func GetChannelLimitPeriodUsages(keys []ChannelPeriodKey) (map[ChannelPeriodKey]int64, error) {
	result := make(map[ChannelPeriodKey]int64, len(keys))
	if len(keys) == 0 {
		return result, nil
	}
	wanted := make(map[ChannelPeriodKey]struct{}, len(keys))
	idSet := make(map[int]struct{}, len(keys))
	startSet := make(map[int64]struct{}, len(keys))
	for _, key := range keys {
		wanted[key] = struct{}{}
		idSet[key.ChannelId] = struct{}{}
		startSet[key.PeriodStart] = struct{}{}
	}
	ids := make([]int, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	starts := make([]int64, 0, len(startSet))
	for start := range startSet {
		starts = append(starts, start)
	}
	var rows []ChannelLimitPeriodUsage
	if err := DB.Where("channel_id IN ? AND period_start IN ?", ids, starts).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		key := ChannelPeriodKey{ChannelId: row.ChannelId, PeriodStart: row.PeriodStart}
		if _, ok := wanted[key]; ok {
			result[key] = row.CostQuota
		}
	}
	return result, nil
}

// DeleteChannelLimitPeriodUsageByChannelIds 删除若干渠道的全部轮次行，供渠道删除后收尾调用。
// 调用方必须先丢掉累计器的进程内状态，理由同 DeleteChannelDailyUsageByChannelIds。
func DeleteChannelLimitPeriodUsageByChannelIds(channelIds []int) (int64, error) {
	if len(channelIds) == 0 {
		return 0, nil
	}
	res := DB.Where("channel_id IN ?", channelIds).Delete(&ChannelLimitPeriodUsage{})
	return res.RowsAffected, res.Error
}

// DeleteOrphanChannelLimitPeriodUsage 删除所有指向已不存在渠道的轮次行，由 master 清理任务兜底调用。
func DeleteOrphanChannelLimitPeriodUsage() (int64, error) {
	res := DB.Where("channel_id NOT IN (?)", DB.Model(&Channel{}).Select("id")).
		Delete(&ChannelLimitPeriodUsage{})
	return res.RowsAffected, res.Error
}

// currentPeriodExists 排除「仍是渠道当前一轮」的行：一个很久没触顶的限时渠道，当前一轮可能早于
// 保留期开始，删掉它等于把本轮用量清零、放行一整轮额度。
const currentPeriodExists = "NOT EXISTS (SELECT 1 FROM channels WHERE channels.id = channel_limit_period_usages.channel_id " +
	"AND channels.daily_limit_period_start = channel_limit_period_usages.period_start)"

// CleanupChannelLimitPeriodUsage 删除 period_start 早于 cutoff、且不是渠道当前一轮的历史行。
// keyset 两段式分批删除，理由同 CleanupChannelDailyUsage（PostgreSQL 不支持 DELETE ... LIMIT）。
func CleanupChannelLimitPeriodUsage(cutoff int64) (int64, error) {
	if cutoff <= 0 {
		return 0, nil
	}
	var total int64
	lastId := 0
	for {
		var ids []int
		err := DB.Model(&ChannelLimitPeriodUsage{}).
			Where("period_start < ? AND id > ?", cutoff, lastId).
			Where(currentPeriodExists).
			Order("id ASC").
			Limit(channelDailyUsageCleanupBatch).
			Pluck("id", &ids).Error
		if err != nil {
			return total, err
		}
		if len(ids) == 0 {
			return total, nil
		}
		lastId = ids[len(ids)-1]
		res := DB.Where("id IN ?", ids).Delete(&ChannelLimitPeriodUsage{})
		if res.Error != nil {
			return total, res.Error
		}
		total += res.RowsAffected
		if len(ids) < channelDailyUsageCleanupBatch {
			return total, nil
		}
	}
}

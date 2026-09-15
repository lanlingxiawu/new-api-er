package model

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ChannelDailyUsage 记录每个渠道每日的上游消耗累计，支撑「渠道每日金额上限」功能。
// 设计见 docs/design/channel-limit-upstream-basis-and-timed-recovery.md。
//
// 跨节点一致性依赖行上的原子 `cost_quota + delta` upsert：各节点定期把本地增量刷到同一行，
// 刷完回读汇总再判定是否越限，不需要分布式锁。
type ChannelDailyUsage struct {
	Id int `json:"id"`
	// StatDate 配置时区下当日 00:00 的 unix 秒（与 PlatformChannelDailyStat.StatDate 同语义）。
	StatDate  int64 `json:"stat_date" gorm:"uniqueIndex:idx_channel_daily_usage,priority:1;index:idx_channel_daily_usage_date"`
	ChannelId int   `json:"channel_id" gorm:"uniqueIndex:idx_channel_daily_usage,priority:2"`
	// CostQuota 上游消耗累计：不乘用户分组倍率的基础消耗 × 渠道成本系数。
	CostQuota int64 `json:"cost_quota" gorm:"bigint;default:0"`
	UpdatedAt int64 `json:"updated_at" gorm:"bigint;default:0"`
}

// ChannelDailyUsageView 是渠道列表接口回填的用量视图，不持久化。
type ChannelDailyUsageView struct {
	StatDate int64 `json:"stat_date"`
	// CostQuota 今日上游消耗。
	CostQuota  int64 `json:"cost_quota"`
	LimitQuota int64 `json:"limit_quota"`
	DisabledAt int64 `json:"disabled_at"`
	// RecoverMinutes > 0 表示限时恢复模式，此时上限判定看 PeriodCostQuota（本轮上游消耗）。
	RecoverMinutes  int   `json:"recover_minutes"`
	PeriodStart     int64 `json:"period_start"`
	PeriodCostQuota int64 `json:"period_cost_quota"`
	// RecoverAt 限时模式下已被禁用的渠道预计恢复的时刻；未禁用或按日模式为 0。
	RecoverAt int64 `json:"recover_at"`
}

// ChannelDailyDelta 是一次 flush 中某个 (日期, 渠道) 的增量。
type ChannelDailyDelta struct {
	StatDate  int64
	ChannelId int
	CostQuota int64
}

// UpsertChannelDailyUsage 原子累加一批增量。
//
// 用 clause.OnConflict 表达，GORM 会按方言转写为 MySQL 的 ON DUPLICATE KEY UPDATE 与
// PostgreSQL / SQLite 的 ON CONFLICT DO UPDATE，三库通用（Rule 2）。多个节点并发执行时
// 由 DB 保证 `cost_quota + delta` 的原子性，无需应用层加锁。
//
// 返回已成功处理的条目数。逐条写入没有事务包裹，中途失败时前面的条目**已经落库**，
// 调用方必须只把 deltas[processed:] 放回内存重试——整批回退会让已落库的部分被重复累加，
// 金额越算越多。
func UpsertChannelDailyUsage(deltas []ChannelDailyDelta) (int, error) {
	if len(deltas) == 0 {
		return 0, nil
	}
	now := common.GetTimestamp()
	for i, delta := range deltas {
		if delta.ChannelId <= 0 || delta.StatDate <= 0 {
			continue
		}
		if delta.CostQuota == 0 {
			continue
		}
		row := ChannelDailyUsage{
			StatDate:  delta.StatDate,
			ChannelId: delta.ChannelId,
			CostQuota: delta.CostQuota,
			UpdatedAt: now,
		}
		err := DB.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "stat_date"}, {Name: "channel_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"cost_quota": gorm.Expr("channel_daily_usages.cost_quota + ?", delta.CostQuota),
				"updated_at": now,
			}),
		}).Create(&row).Error
		if err != nil {
			return i, fmt.Errorf("upsert channel daily usage (channel_id=%d, stat_date=%d): %w", delta.ChannelId, delta.StatDate, err)
		}
	}
	return len(deltas), nil
}

// GetChannelDailyUsages 批量读取指定日期下若干渠道的用量行，命中唯一索引。
// 返回 map[channelId]*ChannelDailyUsage，不存在的渠道不会出现在结果里。
func GetChannelDailyUsages(statDate int64, channelIds []int) (map[int]*ChannelDailyUsage, error) {
	result := make(map[int]*ChannelDailyUsage, len(channelIds))
	if statDate <= 0 || len(channelIds) == 0 {
		return result, nil
	}
	var rows []*ChannelDailyUsage
	err := DB.Where("stat_date = ? AND channel_id IN ?", statDate, channelIds).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.ChannelId] = row
	}
	return result, nil
}

// DeleteChannelDailyUsageByChannelIds 删除若干渠道的全部用量行，供渠道删除后收尾调用。
//
// channel_id 不是唯一索引 (stat_date, channel_id) 的前缀，这里走全表扫描；表的量级是
// 「配置了上限的渠道数 × 保留天数」，很小。
func DeleteChannelDailyUsageByChannelIds(channelIds []int) (int64, error) {
	if len(channelIds) == 0 {
		return 0, nil
	}
	res := DB.Where("channel_id IN ?", channelIds).Delete(&ChannelDailyUsage{})
	return res.RowsAffected, res.Error
}

// DeleteOrphanChannelDailyUsage 删除所有指向已不存在渠道的用量行。
//
// 渠道删除后，累计器里残留的增量可能在下一个 flush 周期写回一行孤儿记录（孤儿行无害：
// 列表只关联现存渠道）。由 master 的清理任务周期性调用，保证最终一致。
func DeleteOrphanChannelDailyUsage() (int64, error) {
	res := DB.Where("channel_id NOT IN (?)", DB.Model(&Channel{}).Select("id")).
		Delete(&ChannelDailyUsage{})
	return res.RowsAffected, res.Error
}

// channelDailyUsageCleanupBatch 单批删除的行数上限。
const channelDailyUsageCleanupBatch = 1000

// CleanupChannelDailyUsage 删除 stat_date 早于 cutoff 的历史行，返回删除总数。
//
// 不使用 `DELETE ... LIMIT`：PostgreSQL 不支持该语法。改为 keyset 两段式——先按主键取一批
// id，再按 id 删除，三库通用且不产生长事务。
func CleanupChannelDailyUsage(cutoff int64) (int64, error) {
	if cutoff <= 0 {
		return 0, nil
	}
	var total int64
	lastId := 0
	for {
		var ids []int
		err := DB.Model(&ChannelDailyUsage{}).
			Where("stat_date < ? AND id > ?", cutoff, lastId).
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
		res := DB.Where("id IN ?", ids).Delete(&ChannelDailyUsage{})
		if res.Error != nil {
			return total, res.Error
		}
		total += res.RowsAffected
		if len(ids) < channelDailyUsageCleanupBatch {
			return total, nil
		}
	}
}

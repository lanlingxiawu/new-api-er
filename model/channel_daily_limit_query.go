package model

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/setting/operation_setting"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 渠道列表的「每日上限」筛选与「今日用量」排序。
// 设计见 docs/design/channel-daily-quota-limit.md §9.4 / §9.5。
//
// 为什么用相关子查询而不是 LEFT JOIN：
//   - 搜索路径的 WHERE 里有裸 `id = ?`，JOIN 进 channel_daily_usages（同样有 id 列）会
//     产生列歧义；
//   - JOIN 之后 `Find(&channels)` 的 SELECT * 会带出两张表的同名列，需要额外 Select
//     限定，与既有的 Omit("key") 相互干扰；
//   - Count 也要额外确认不被放大。
//
// 相关子查询没有这些问题：语句形状不变、Count 天然正确，而且 (stat_date, channel_id)
// 唯一索引让它是一次点查。渠道表规模为数百行，代价可忽略。
//
// 跨库约束（Rule 2）：只用 CASE WHEN / COALESCE / NULLIF 与标量子查询，
// SQLite、MySQL 5.7、PostgreSQL 9.6 三库通用，不含任何方言函数。

const (
	ChannelLimitFilterAll        = "all"
	ChannelLimitFilterConfigured = "configured"
	ChannelLimitFilterUnlimited  = "unlimited"
	ChannelLimitFilterReached    = "reached"
	ChannelLimitFilterNear       = "near"
)

// 两个表达式排序键。它们不在 channelSortColumns 白名单里，因为 clause.OrderByColumn
// 只能表达单列，无法表达 CASE / 除法。
const (
	ChannelSortByDailyUsed       = "daily_used"
	ChannelSortByDailyUsageRatio = "daily_usage_ratio"
)

// NormalizeChannelLimitFilter 归一化筛选值，非法值一律视为不过滤。
func NormalizeChannelLimitFilter(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ChannelLimitFilterConfigured:
		return ChannelLimitFilterConfigured
	case ChannelLimitFilterUnlimited:
		return ChannelLimitFilterUnlimited
	case ChannelLimitFilterReached:
		return ChannelLimitFilterReached
	case ChannelLimitFilterNear:
		return ChannelLimitFilterNear
	default:
		return ChannelLimitFilterAll
	}
}

func channelSortNeedsUsage(sortBy string) bool {
	return sortBy == ChannelSortByDailyUsed || sortBy == ChannelSortByDailyUsageRatio
}

// dailyUsageSubquery 是「上限判定所用的用量」标量子查询：按日模式取今日上游消耗，
// 限时恢复模式取本轮上游消耗（channel_limit_period_usages 当前一轮）。
// 没有用量行时子查询返回 NULL，外层 COALESCE 归零。占位符只有一个（今日 stat_date）。
const dailyUsageSubquery = `COALESCE(CASE WHEN channels.daily_limit_recover_minutes > 0 ` +
	`THEN (SELECT clpu.cost_quota FROM channel_limit_period_usages clpu ` +
	`WHERE clpu.channel_id = channels.id AND clpu.period_start = channels.daily_limit_period_start) ` +
	`ELSE (SELECT cdu.cost_quota FROM channel_daily_usages cdu ` +
	`WHERE cdu.channel_id = channels.id AND cdu.stat_date = ?) END, 0)`

// ApplyChannelLimitFilter 追加每日上限筛选条件。
func ApplyChannelLimitFilter(query *gorm.DB, limitFilter string, statDate int64) *gorm.DB {
	switch NormalizeChannelLimitFilter(limitFilter) {
	case ChannelLimitFilterConfigured:
		return query.Where("channels.daily_quota_limit > 0")
	case ChannelLimitFilterUnlimited:
		return query.Where("channels.daily_quota_limit <= 0")
	case ChannelLimitFilterReached:
		// 「已达上限」= 当前正因上限而禁用，不限哪天触发（跨过零点仍在等待恢复的限时渠道、
		// 关闭了自动恢复的渠道都算）。标记只由限额禁用写入，恢复与其它任何状态变更都会清零。
		return query.Where("channels.daily_limit_disabled_at > 0")
	case ChannelLimitFilterNear:
		if statDate <= 0 {
			return query
		}
		return query.Where(
			"channels.daily_quota_limit > 0 AND channels.daily_limit_disabled_at = 0 AND "+
				dailyUsageSubquery+" >= channels.daily_quota_limit * ?",
			statDate, operation_setting.ChannelDailyLimitNearThreshold,
		)
	default:
		return query
	}
}

// applyDailyUsageOrder 追加两级排序。
//
// 第一级把「已设上限」固定排在前、「未设上限」固定排在后，与升降序无关——这样无论用户
// 选升序还是降序，没有使用率概念的渠道都稳定排在末尾。第二级才是用户选择的方向。
//
// 三处跨库要点：
//  1. `* 1.0` 强制浮点，否则 SQLite / MySQL 走整数除法，比率全退化成 0 或 1；
//  2. `NULLIF(limit, 0)` 防除零，PostgreSQL 会直接抛 division by zero；
//  3. 不依赖 NULL 的默认排序位置（PG 是 NULLS LAST，MySQL/SQLite 视 NULL 最小），
//     改由第一级 CASE 决定，三库一致。
func applyDailyUsageOrder(query *gorm.DB, sortBy string, statDate int64, desc bool) (*gorm.DB, bool) {
	if statDate <= 0 {
		return query, false
	}
	var expr string
	switch sortBy {
	case ChannelSortByDailyUsed:
		expr = dailyUsageSubquery
	case ChannelSortByDailyUsageRatio:
		expr = dailyUsageSubquery + " * 1.0 / NULLIF(channels.daily_quota_limit, 0)"
	default:
		return query, false
	}
	direction := " DESC"
	if !desc {
		direction = " ASC"
	}
	// 两点都必须这么写，否则排序会被静默丢弃：
	//  1. 两级排序写在同一个 OrderByColumn 里。GORM 的 clause.OrderBy 合并时只累加
	//     Columns，Expression 字段是后者覆盖前者，分两次 Order 调用会丢掉第一级 CASE。
	//  2. 必须用 clause.OrderByColumn 而不是 clause.OrderBy：gorm v1.25.2 的
	//     DB.Order() 只处理 clause.OrderByColumn 与 string 两种类型，传 clause.OrderBy
	//     会走空 switch 被直接忽略（chainable_api.go:302）。
	//
	// statDate 因此只能内联成整数字面量而不是占位符——它是本进程按配置时区算出的
	// int64，不来自任何外部输入，没有注入面。
	stat := strconv.FormatInt(statDate, 10)
	ordering := "CASE WHEN channels.daily_quota_limit > 0 THEN 0 ELSE 1 END ASC, " +
		strings.ReplaceAll(expr, "?", stat) + direction
	query = query.Order(clause.OrderByColumn{Column: clause.Column{Name: ordering, Raw: true}})
	return query, true
}

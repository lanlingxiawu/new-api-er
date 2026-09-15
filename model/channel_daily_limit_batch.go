package model

import (
	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// 每日金额上限的批量与按 Tag 设置。设计见 docs/design/channel-daily-quota-limit.md §9.6。
//
// 实现说明（与设计的偏差）：设计原本打算把 EditChannelByTag 改造成结构体入参再塞进三个
// 字段。实际实现改为独立函数，原因有二：
//  1. EditChannelByTag 用 Updates(struct) 更新，GORM 会忽略结构体零值——而本功能的
//     `daily_quota_limit = 0` 恰恰是「清除上限」这个有意义的值，必须用 map 更新；
//  2. 独立函数不触碰既有 Tag 编辑的行为与测试，改动面更小。

// DailyLimitEdit 描述一次每日上限的批量修改。
// 每个字段用指针：nil = 本次不修改该字段，与既有 Tag 编辑的 Priority/Weight 语义一致。
type DailyLimitEdit struct {
	QuotaLimit     *int64
	AutoRecover    *int
	RecoverMinutes *int
}

// IsEmpty 判断本次修改是否什么都没带。
func (e DailyLimitEdit) IsEmpty() bool {
	return e.QuotaLimit == nil && e.AutoRecover == nil && e.RecoverMinutes == nil
}

// Validate 复用单渠道保存的同一套校验，保证三条写入路径（单渠道 / 批量 / Tag）一致。
func (e DailyLimitEdit) Validate() error {
	limit := int64(0)
	if e.QuotaLimit != nil {
		limit = *e.QuotaLimit
	}
	minutes := 0
	if e.RecoverMinutes != nil {
		minutes = *e.RecoverMinutes
	}
	return ValidateDailyLimitConfig(limit, minutes)
}

// updates 构造 map 形式的更新集。用 map 而不是结构体，才能表达「显式设为 0」。
func (e DailyLimitEdit) updates(now int64) map[string]any {
	updates := make(map[string]any, 4)
	if e.QuotaLimit != nil {
		updates["daily_quota_limit"] = *e.QuotaLimit
	}
	if e.AutoRecover != nil {
		value := 0
		if *e.AutoRecover == 1 {
			value = 1
		}
		updates["daily_limit_auto_recover"] = value
	}
	recoverMinutes := e.RecoverMinutes
	if recoverMinutes == nil && e.AutoRecover != nil && *e.AutoRecover != 1 {
		// 关闭自动恢复即「不自动恢复」模式，限时间隔要一并归零；否则会留下「限时 + 不恢复」的
		// 矛盾状态——限时恢复要求 auto_recover = 1，渠道将永远停在禁用。
		zero := 0
		recoverMinutes = &zero
	}
	if recoverMinutes != nil {
		minutes := *recoverMinutes
		updates["daily_limit_recover_minutes"] = minutes
		// 恢复间隔变化（含按日 ↔ 限时切换）开启新一轮；表单原样回传同一个值时不重置。
		//
		// 依赖同一条 UPDATE 里 CASE 读到的是**旧**间隔：GORM 按列名排序生成 SET，
		// daily_limit_period_start 排在 daily_limit_recover_minutes 之前，因此即便 MySQL
		// 按从左到右、使用已更新值的顺序求值，CASE 仍读到旧值；PostgreSQL / SQLite 的 SET
		// 本来就全部读旧值。由真实库用例 TestDailyLimitEdit_RecoverMinutesChangeStartsNewPeriod 锁住。
		updates["daily_limit_period_start"] = gorm.Expr(
			"CASE WHEN daily_limit_recover_minutes <> ? THEN ? ELSE daily_limit_period_start END", minutes, now)
		if minutes > 0 {
			// 限时模式隐含自动恢复。
			updates["daily_limit_auto_recover"] = 1
		}
	}
	return updates
}

// applyDailyLimitEdit 在给定作用域上执行一次原子 UPDATE，返回受影响行数。
//
// 只改配置列，不动 status 与禁用标记列：调高上限不会自动启用已被禁用的渠道，这与单渠道
// 编辑的行为一致（若新上限仍低于今日已用，下个 flush 周期会重新判定）。
func applyDailyLimitEdit(scope *gorm.DB, edit DailyLimitEdit) (int64, error) {
	if edit.IsEmpty() {
		return 0, nil
	}
	if err := edit.Validate(); err != nil {
		return 0, err
	}
	res := scope.Updates(edit.updates(common.GetTimestamp()))
	return res.RowsAffected, res.Error
}

// UpdateChannelDailyLimitByIds 批量设置选中渠道的每日上限。
func UpdateChannelDailyLimitByIds(ids []int, edit DailyLimitEdit) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	return applyDailyLimitEdit(DB.Model(&Channel{}).Where("id IN ?", ids), edit)
}

// GetChannelIdsByTag 返回某标签下所有渠道的 ID。
//
// 供按 Tag 改每日上限使用：必须在可能发生的改名**之前**取，之后按 ID 更新，
// 否则把 A 改名成已存在的 B 之后再按 B 定位，会连带改到原本就属于 B 的渠道。
func GetChannelIdsByTag(tag string) ([]int, error) {
	if tag == "" {
		return nil, nil
	}
	var ids []int
	err := DB.Model(&Channel{}).Where("tag = ?", tag).Pluck("id", &ids).Error
	return ids, err
}

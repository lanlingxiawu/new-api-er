package model

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ConsumptionCost 逐笔消费成本台账（覆盖全平台所有消费，不仅员工归属流量）。
// 在每笔消费结算后异步写入，记录当次真实的分组倍率与渠道成本系数算出的精确成本，
// 用于平台级成本/利润的精确统计（区别于按聚合估算）。
//
// 单独建表而非写入 logs：logs 还包含大量非消费日志，且成本统计只需消费数据。
type ConsumptionCost struct {
	Id           int     `json:"id"`
	LogId        int     `json:"log_id" gorm:"uniqueIndex;default:0"` // 关联 logs.id，唯一约束保证幂等
	UserId       int     `json:"user_id" gorm:"index;default:0"`
	ChannelId    int     `json:"channel_id" gorm:"index;default:0"`
	GroupName    string  `json:"group_name" gorm:"column:group_name;type:varchar(64);default:''"`
	ModelName    string  `json:"model_name" gorm:"type:varchar(255);default:''"`
	RevenueQuota int64   `json:"revenue_quota" gorm:"default:0"`
	CostQuota    int64   `json:"cost_quota" gorm:"default:0"`
	GroupRatio   float64 `json:"group_ratio" gorm:"default:1"`
	CostRatio    float64 `json:"cost_ratio" gorm:"default:1"`
	CreatedAt    int64   `json:"created_at" gorm:"autoCreateTime;index"`
}

// CreateConsumptionCost 幂等写入逐笔成本记录（log_id 唯一约束 + ON CONFLICT DO NOTHING）。
func CreateConsumptionCost(rec *ConsumptionCost) error {
	if rec.LogId <= 0 {
		return DB.Create(rec).Error
	}
	return DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "log_id"}},
		DoNothing: true,
	}).Create(rec).Error
}

func applyConsumptionCostTimeRange(tx *gorm.DB, startTime, endTime int64) *gorm.DB {
	if startTime != 0 {
		tx = tx.Where("created_at >= ?", startTime)
	}
	if endTime != 0 {
		tx = tx.Where("created_at <= ?", endTime)
	}
	return tx
}

// ConsumptionCostTotals 时间范围内的精确收入/成本总计。
type ConsumptionCostTotals struct {
	TotalRevenue int64 `json:"total_revenue"`
	TotalCost    int64 `json:"total_cost"`
	RecordCount  int64 `json:"record_count"`
}

// GetConsumptionCostTotals 精确汇总收入与成本。
func GetConsumptionCostTotals(startTime, endTime int64) (ConsumptionCostTotals, error) {
	var t ConsumptionCostTotals
	tx := DB.Model(&ConsumptionCost{}).
		Select("COALESCE(SUM(revenue_quota),0) as total_revenue, " +
			"COALESCE(SUM(cost_quota),0) as total_cost, " +
			"COUNT(*) as record_count")
	tx = applyConsumptionCostTimeRange(tx, startTime, endTime)
	err := tx.Scan(&t).Error
	return t, err
}

// ConsumptionCostChannelStat 按渠道的精确收入/成本汇总。
type ConsumptionCostChannelStat struct {
	ChannelId    int    `json:"channel_id"`
	TotalRevenue int64  `json:"total_revenue"`
	TotalCost    int64  `json:"total_cost"`
	RecordCount  int64  `json:"record_count"`
	ChannelName  string `json:"channel_name" gorm:"-"`
}

// GetConsumptionCostByChannel 按渠道精确汇总。
func GetConsumptionCostByChannel(startTime, endTime int64) ([]*ConsumptionCostChannelStat, error) {
	var items []*ConsumptionCostChannelStat
	tx := DB.Model(&ConsumptionCost{}).
		Select("channel_id, " +
			"COALESCE(SUM(revenue_quota),0) as total_revenue, " +
			"COALESCE(SUM(cost_quota),0) as total_cost, " +
			"COUNT(*) as record_count").
		Group("channel_id")
	tx = applyConsumptionCostTimeRange(tx, startTime, endTime)
	err := tx.Scan(&items).Error
	return items, err
}

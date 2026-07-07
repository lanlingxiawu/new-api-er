package model

import (
	"errors"
	"time"
)

// ConsumptionCost 逐笔消费成本台账（覆盖全平台所有消费，不仅员工归属流量）。
// 在每笔消费结算后异步写入，记录当次真实的分组倍率与渠道成本系数算出的精确成本，
// 用于平台级成本/利润的精确统计（区别于按聚合估算）。
//
// 单独建表而非写入 logs：logs 还包含大量非消费日志，且成本统计只需消费数据。
type ConsumptionCost struct {
	Id           int     `json:"id"`
	LogId        *int    `json:"log_id" gorm:"uniqueIndex"` // 关联 logs.id，唯一约束保证幂等；无日志时为 NULL
	UserId       int     `json:"user_id" gorm:"index;default:0"`
	ChannelId    int     `json:"channel_id" gorm:"index;index:idx_consumption_cost_created_channel,priority:2;default:0"`
	ChannelName  string  `json:"channel_name" gorm:"type:varchar(255);default:''"`
	GroupName    string  `json:"group_name" gorm:"column:group_name;type:varchar(64);default:''"`
	ModelName    string  `json:"model_name" gorm:"type:varchar(255);default:''"`
	RevenueQuota int64   `json:"revenue_quota" gorm:"default:0"`
	CostQuota    int64   `json:"cost_quota" gorm:"default:0"`
	GroupRatio   float64 `json:"group_ratio"`
	CostRatio    float64 `json:"cost_ratio"`
	CreatedAt    int64   `json:"created_at" gorm:"autoCreateTime;index;index:idx_consumption_cost_created_channel,priority:1"`
}

// CreateConsumptionCost 将逐笔成本记录推入内存缓冲区，由后台批量入库。
// 幂等性由 DB 侧 uniqueIndex(log_id) + ON CONFLICT DO NOTHING 保障；
// 本路径不做进程内去重——cost 记录量大且无需跨请求关联，直接依赖 DB 约束即可。
func CreateConsumptionCost(rec *ConsumptionCost) error {
	return CreateConsumptionCostRecord(rec)
}

func CreateConsumptionCostRecord(rec *ConsumptionCost) error {
	if rec.CreatedAt == 0 {
		rec.CreatedAt = time.Now().Unix()
	}
	if rec.LogId == nil || *rec.LogId <= 0 {
		rec.LogId = nil
	}
	// 平台日统计的聚合改到 flushConsumptionCostLedger 入库成功后再做（与成对路径一致）：
	// 这样缓冲超限丢弃或刷盘永久失败的记录不会被计入，避免 platform_channel_daily_stats
	// 超前于真正落库的明细而产生偏差。
	BufferConsumptionCostRecord(rec)
	return nil
}

// CreateConsumptionCostAndCommissionLog 将成本+提成记录成对入缓冲，进程内按 log_id 去重。
// 多实例场景下不同进程可能同时通过去重并各自写入 BufferCommissionAndProfit，
// 导致员工汇总被重复累加；DB 侧 ON CONFLICT 仅保护台账明细行，不保护汇总增量。
// 单实例或低并发重复请求场景下无此风险。
func CreateConsumptionCostAndCommissionLog(cost *ConsumptionCost, log *EmployeeCommissionLog) (inserted bool, err error) {
	now := time.Now().Unix()
	if cost.CreatedAt == 0 {
		cost.CreatedAt = now
	}
	if log.CreatedAt == 0 {
		log.CreatedAt = cost.CreatedAt
	}
	if cost.LogId == nil || *cost.LogId <= 0 {
		cost.LogId = nil
	}
	if log.LogId == nil || *log.LogId <= 0 {
		log.LogId = nil
	}

	inserted = CheckAndBufferCostAndCommission(cost, log)
	return inserted, nil
}

// GetConsumptionCostById 按主键读取单条台账记录（用于定向冲销）。
func GetConsumptionCostById(id int) (*ConsumptionCost, error) {
	if id <= 0 {
		return nil, errors.New("invalid consumption cost id")
	}
	var rec ConsumptionCost
	if err := DB.Where("id = ?", id).First(&rec).Error; err != nil {
		return nil, err
	}
	return &rec, nil
}

// ConsumptionCostTotals 时间范围内的精确收入/成本总计。
type ConsumptionCostTotals struct {
	TotalRevenue int64 `json:"total_revenue"`
	TotalCost    int64 `json:"total_cost"`
	RecordCount  int64 `json:"record_count"`
}

// GetConsumptionCostTotals 精确汇总收入与成本，优先使用日聚合统计。
func GetConsumptionCostTotals(startTime, endTime int64) (ConsumptionCostTotals, error) {
	items, err := GetConsumptionCostByChannel(startTime, endTime)
	if err != nil {
		return ConsumptionCostTotals{}, err
	}
	var t ConsumptionCostTotals
	for _, item := range items {
		t.TotalRevenue += item.TotalRevenue
		t.TotalCost += item.TotalCost
		t.RecordCount += item.RecordCount
	}
	return t, nil
}

// ConsumptionCostChannelStat 按渠道的精确收入/成本汇总。
type ConsumptionCostChannelStat struct {
	ChannelId    int     `json:"channel_id"`
	TotalRevenue int64   `json:"total_revenue"`
	TotalCost    int64   `json:"total_cost"`
	RecordCount  int64   `json:"record_count"`
	CostRatio    float64 `json:"cost_ratio"`
	CostRatioSum float64 `json:"-"`
	// ChannelName 由查询的 channel_name 快照列填充；注意不能加 gorm:"-"，
	// 否则 Scan 时会丢弃 SQL 查出的快照名称，渠道删除后将无法显示名称。
	ChannelName string `json:"channel_name"`
}

// GetConsumptionCostByChannel 按渠道精确汇总，优先使用日聚合统计。
func GetConsumptionCostByChannel(startTime, endTime int64) ([]*ConsumptionCostChannelStat, error) {
	return getConsumptionCostByChannelFromStats(startTime, endTime)
}

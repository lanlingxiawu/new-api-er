package model

import "time"

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

// CreateConsumptionCost 幂等写入逐笔成本记录，并同步累加日聚合统计。
func CreateConsumptionCost(rec *ConsumptionCost) error {
	_, err := CreateConsumptionCostRecord(rec)
	return err
}

func CreateConsumptionCostRecord(rec *ConsumptionCost) (inserted bool, err error) {
	if rec.CreatedAt == 0 {
		rec.CreatedAt = time.Now().Unix()
	}
	if rec.LogId == nil || *rec.LogId <= 0 {
		rec.LogId = nil
	}
	// 明细推入缓冲区，由后台批量入库（ON CONFLICT DO NOTHING 保证幂等）
	BufferConsumptionCostRecord(rec)
	// 日统计增量写入缓冲区（Redis 或内存），由后台定时刷盘
	BufferPlatformDailyStat(rec)
	return true, nil
}

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
	if inserted {
		BufferPlatformDailyStat(cost)
		BufferCommissionDailyStat(log)
	}
	return inserted, nil
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

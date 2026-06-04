package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

func cleanCommissionTables(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM employee_commission_tiers")
		model.DB.Exec("DELETE FROM employee_tier_levels")
		model.DB.Exec("DELETE FROM employee_tier_logs")
		model.DB.Exec("DELETE FROM employee_commission_logs")
		model.DB.Exec("DELETE FROM user_extensions")
		model.DB.Exec("DELETE FROM employee_profiles")
		model.DB.Exec("DELETE FROM channel_cost_configs")
		model.DB.Exec("DELETE FROM users")
		model.InvalidateTierCache()
		model.ResetChannelCostCache()
	})
}

func makeRelayInfo(userId, channelId int) *relaycommon.RelayInfo {
	ri := &relaycommon.RelayInfo{}
	ri.UserId = userId
	ri.ChannelId = channelId
	ri.OriginModelName = "gpt-4o"
	ri.PriceData.GroupRatioInfo.GroupRatio = 1.0
	return ri
}

func seedTestUser(t *testing.T, id int, inviterId int) {
	t.Helper()
	u := &model.User{
		Id:        id,
		Username:  "u" + string(rune('0'+id)),
		Password:  "x",
		Status:    1,
		InviterId: inviterId,
	}
	require.NoError(t, model.DB.Create(u).Error)
}

func seedEmployee(t *testing.T, userId int, rate float64) *model.EmployeeProfile {
	t.Helper()
	emp := &model.EmployeeProfile{
		UserId:         userId,
		CommissionRate: rate,
		Status:         1,
	}
	require.NoError(t, model.CreateEmployee(emp))
	return emp
}

// ---------------------------------------------------------------------------
// 1. 纯函数：calcCostQuota
// ---------------------------------------------------------------------------

func TestCalcCostQuota(t *testing.T) {
	tests := []struct {
		name        string
		revenue     int64
		groupRatio  float64
		costRatio   float64
		expectedMin int64
		expectedMax int64
	}{
		{"正常场景", 1000, 1.0, 0.6, 600, 600},
		{"groupRatio=2（加价一倍）成本减半", 1000, 2.0, 1.0, 500, 500},
		{"groupRatio=0 免费模型", 1000, 0.0, 0.5, 0, 0},
		{"costRatio=0 零成本渠道", 1000, 1.0, 0.0, 0, 0},
		{"负收入（退款）", -500, 1.0, 0.6, -300, -300},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calcCostQuota(tt.revenue, tt.groupRatio, tt.costRatio)
			assert.GreaterOrEqual(t, got, tt.expectedMin)
			assert.LessOrEqual(t, got, tt.expectedMax)
		})
	}
}

// ---------------------------------------------------------------------------
// 2. 纯函数：calcCommissionQuota
// ---------------------------------------------------------------------------

func TestCalcCommissionQuota(t *testing.T) {
	tests := []struct {
		name     string
		profit   int64
		rate     float64
		expected int64
	}{
		{"10% 提成", 1000, 0.1, 100},
		{"5% 提成", 2000, 0.05, 100},
		{"极小利润保底 1", 1, 0.01, 1},
		{"零利润不提成", 0, 0.1, 0},
		{"负利润不提成", -100, 0.1, 0},
		{"零比例不提成", 1000, 0.0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calcCommissionQuota(tt.profit, tt.rate)
			assert.Equal(t, tt.expected, got)
		})
	}
}

// ---------------------------------------------------------------------------
// 3. 纯函数：clampSurcharge
// ---------------------------------------------------------------------------

func TestClampSurcharge(t *testing.T) {
	assert.Equal(t, int64(200), clampSurcharge(200, 1000), "正常范围内不变")
	assert.Equal(t, int64(1000), clampSurcharge(1500, 1000), "超出收入上限时截断")
	assert.Equal(t, int64(0), clampSurcharge(200, 0), "收入为 0 时返回 0")
	assert.Equal(t, int64(0), clampSurcharge(200, -100), "负收入时返回 0")
	assert.Equal(t, int64(0), clampSurcharge(-10, 1000), "负加付项返回 0")
}

// ---------------------------------------------------------------------------
// 4. 端到端：禁用员工不计提成
// ---------------------------------------------------------------------------

func TestTrySettleEmployeeCommission_DisabledEmployee(t *testing.T) {
	cleanCommissionTables(t)

	// customer=101，inviter=102（员工，但已禁用）
	seedTestUser(t, 101, 102)
	seedTestUser(t, 102, 0)
	emp := seedEmployee(t, 102, 0.1)

	// 禁用员工
	model.DB.Model(emp).Update("status", 2)

	ri := makeRelayInfo(101, 1)
	TrySettleEmployeeCommission(ri, 1000, 0, 9001)

	var count int64
	model.DB.Model(&model.EmployeeCommissionLog{}).Count(&count)
	assert.Equal(t, int64(0), count, "禁用员工不应产生提成记录")
}

// ---------------------------------------------------------------------------
// 5. 端到端：无等级时使用 emp.CommissionRate
// ---------------------------------------------------------------------------

func TestTrySettleEmployeeCommission_FallbackRate(t *testing.T) {
	cleanCommissionTables(t)

	seedTestUser(t, 201, 202)
	seedTestUser(t, 202, 0)
	seedEmployee(t, 202, 0.10)

	// 设置渠道成本系数 0.6 → profit = 1000 - 1000/1.0*0.6 = 400
	model.DB.Create(&model.ChannelCostConfig{ChannelId: 10, CostRatio: 0.6})

	ri := makeRelayInfo(201, 10)
	TrySettleEmployeeCommission(ri, 1000, 0, 9002)

	var log model.EmployeeCommissionLog
	require.NoError(t, model.DB.First(&log).Error)
	assert.Equal(t, int64(1000), log.RevenueQuota)
	assert.Equal(t, int64(600), log.CostQuota)
	assert.Equal(t, int64(400), log.ProfitQuota)
	// 提成 = 400 * 0.10 = 40
	assert.Equal(t, int64(40), log.CommissionQuota)
	assert.Equal(t, 0.10, log.CommissionRate)
}

// ---------------------------------------------------------------------------
// 6. 端到端：有等级时使用等级 rate，且结算后自动升级
// ---------------------------------------------------------------------------

// 6. 端到端：员工自定义 rate 优先于等级 rate
// ---------------------------------------------------------------------------

func TestTrySettleEmployeeCommission_CustomRatePriority(t *testing.T) {
	cleanCommissionTables(t)

	seedTestUser(t, 301, 302)
	seedTestUser(t, 302, 0)
	emp := seedEmployee(t, 302, 0.05) // 员工自定义 5%

	// 等级 rate=12%，高于员工自定义 5%，但不应生效
	tier := &model.EmployeeCommissionTier{
		Level: 3, ThresholdUsd: 0.0005, Rate: 0.12,
	}
	require.NoError(t, model.CreateTier(tier))
	model.InvalidateTierCache()
	require.NoError(t, model.SetTierLevel(emp.UserId, tier.Id, "manual", 0, "", 0))

	// profit = 1000 quota * (1 - 0.5) = 500 quota
	model.DB.Create(&model.ChannelCostConfig{ChannelId: 20, CostRatio: 0.5})

	ri := makeRelayInfo(301, 20)
	TrySettleEmployeeCommission(ri, 1000, 0, 9003)

	var log model.EmployeeCommissionLog
	require.NoError(t, model.DB.First(&log).Error)
	assert.Equal(t, int64(500), log.ProfitQuota)
	// 员工自定义 5% 优先，而非等级 12%
	assert.Equal(t, 0.05, log.CommissionRate)
	assert.Equal(t, int64(25), log.CommissionQuota) // 500 * 0.05 = 25
}

// ---------------------------------------------------------------------------
// 6b. 端到端：员工无自定义 rate（=0）时使用等级 rate
// ---------------------------------------------------------------------------

func TestTrySettleEmployeeCommission_TierRateFallback(t *testing.T) {
	cleanCommissionTables(t)

	seedTestUser(t, 303, 304)
	seedTestUser(t, 304, 0)
	emp := seedEmployee(t, 304, 0) // 员工未设置自定义比例

	// 等级 rate=10%，作为兜底
	tier := &model.EmployeeCommissionTier{
		Level: 2, ThresholdUsd: 0.0005, Rate: 0.10,
	}
	require.NoError(t, model.CreateTier(tier))
	model.InvalidateTierCache()
	require.NoError(t, model.SetTierLevel(emp.UserId, tier.Id, "manual", 0, "", 0))

	model.DB.Create(&model.ChannelCostConfig{ChannelId: 21, CostRatio: 0.5})
	// profit = 500 quota

	ri := makeRelayInfo(303, 21)
	TrySettleEmployeeCommission(ri, 1000, 0, 9008)

	var log model.EmployeeCommissionLog
	require.NoError(t, model.DB.First(&log).Error)
	// 员工 rate=0，使用等级 rate 10%
	assert.Equal(t, 0.10, log.CommissionRate)
	assert.Equal(t, int64(50), log.CommissionQuota) // 500 * 0.10 = 50
}

// ---------------------------------------------------------------------------
// 7. 端到端：利润为正触发自动升级
// ---------------------------------------------------------------------------

func TestTrySettleEmployeeCommission_AutoTierUpgrade(t *testing.T) {
	cleanCommissionTables(t)

	seedTestUser(t, 401, 402)
	seedTestUser(t, 402, 0)
	seedEmployee(t, 402, 0.05)

	// 等级：门槛 400（quota 单位），低成本渠道 profit≈500
	// profit = 500 quota = $0.001 USD；门槛 $0.0008，低于利润，应触发升级
	tier := &model.EmployeeCommissionTier{
		Level: 2, ThresholdUsd: 0.0008, Rate: 0.08,
	}
	require.NoError(t, model.CreateTier(tier))
	model.InvalidateTierCache()
	model.DB.Create(&model.ChannelCostConfig{ChannelId: 30, CostRatio: 0.5})

	// 结算前无等级
	lvl, _ := model.GetOrCreateTierLevel(402)
	assert.Equal(t, int64(0), lvl.TierId)

	// 结算 quota=1000，profit=500 > 门槛400，应自动升级
	ri := makeRelayInfo(401, 30)
	TrySettleEmployeeCommission(ri, 1000, 0, 9004)

	// 等级日志应有一条 auto 升级记录
	logs, total, err := model.GetTierLogs(model.TierLogFilter{UserId: 402, Page: 1, PageSize: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "auto", logs[0].Source)
	assert.Equal(t, tier.Id, logs[0].ToTierId)
}

// ---------------------------------------------------------------------------
// 8. 端到端：幂等性（同一 log_id 重复调用不重复计提）
// ---------------------------------------------------------------------------

func TestTrySettleEmployeeCommission_Idempotent(t *testing.T) {
	cleanCommissionTables(t)

	seedTestUser(t, 501, 502)
	seedTestUser(t, 502, 0)
	seedEmployee(t, 502, 0.10)
	model.DB.Create(&model.ChannelCostConfig{ChannelId: 40, CostRatio: 0.5})

	ri := makeRelayInfo(501, 40)

	// 同一 log_id 调用两次
	TrySettleEmployeeCommission(ri, 1000, 0, 9005)
	TrySettleEmployeeCommission(ri, 1000, 0, 9005)

	var count int64
	model.DB.Model(&model.EmployeeCommissionLog{}).Count(&count)
	assert.Equal(t, int64(1), count, "相同 log_id 只应产生一条记录")

	// user_extensions 里提成也只累加一次
	ext, err := model.GetUserExtension(502)
	require.NoError(t, err)
	assert.Equal(t, int64(50), ext.CommissionPendingQuota, "500*0.1=50，只计一次")
}

// ---------------------------------------------------------------------------
// 9. 端到端：零利润（成本 >= 收入）不计提成但写日志
// ---------------------------------------------------------------------------

func TestTrySettleEmployeeCommission_ZeroProfit(t *testing.T) {
	cleanCommissionTables(t)

	seedTestUser(t, 601, 602)
	seedTestUser(t, 602, 0)
	seedEmployee(t, 602, 0.10)
	// costRatio=1.0（成本等于收入），profit=0
	ri := makeRelayInfo(601, 50) // 无特殊成本配置，默认 cost_ratio=1.0

	TrySettleEmployeeCommission(ri, 500, 0, 9006)

	var log model.EmployeeCommissionLog
	require.NoError(t, model.DB.First(&log).Error)
	assert.Equal(t, int64(0), log.CommissionQuota, "零利润不应有提成")
	assert.Equal(t, int64(0), log.ProfitQuota)

	// user_extensions 里提成为 0
	ext, err := model.GetUserExtension(602)
	require.NoError(t, err)
	assert.Equal(t, int64(0), ext.CommissionPendingQuota)
}

// ---------------------------------------------------------------------------
// 10. 端到端：负利润（退款冲销）写日志，提成为负
// ---------------------------------------------------------------------------

func TestTrySettleEmployeeCommission_NegativeQuota(t *testing.T) {
	cleanCommissionTables(t)

	seedTestUser(t, 701, 702)
	seedTestUser(t, 702, 0)
	seedEmployee(t, 702, 0.10)
	model.DB.Create(&model.ChannelCostConfig{ChannelId: 60, CostRatio: 0.5})

	ri := makeRelayInfo(701, 60)
	// 负 quota = 退款 -500，profit = -500 - (-500*0.5) = -250
	TrySettleEmployeeCommission(ri, -500, 0, 9007)

	var log model.EmployeeCommissionLog
	require.NoError(t, model.DB.First(&log).Error)
	assert.Equal(t, int64(-500), log.RevenueQuota)
	assert.Equal(t, int64(-250), log.ProfitQuota)
	assert.Equal(t, int64(0), log.CommissionQuota, "负利润提成为 0")
}

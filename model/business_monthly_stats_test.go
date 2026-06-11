package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

func TestResolveCommissionMonthlyPeriodDefaultChinaTimezone(t *testing.T) {
	cfg := operation_setting.GetCommissionTierResetSetting()
	originalDay := cfg.ResetDay
	originalHour := cfg.ResetHour
	originalMinute := cfg.ResetMinute
	originalSecond := cfg.ResetSecond
	originalTimezone := cfg.Timezone
	defer func() {
		cfg.ResetDay = originalDay
		cfg.ResetHour = originalHour
		cfg.ResetMinute = originalMinute
		cfg.ResetSecond = originalSecond
		cfg.Timezone = originalTimezone
	}()

	cfg.ResetDay = 10
	cfg.ResetHour = 0
	cfg.ResetMinute = 0
	cfg.ResetSecond = 0
	cfg.Timezone = "Asia/Shanghai"

	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}

	beforeReset := time.Date(2026, time.June, 9, 23, 59, 59, 0, loc).Unix()
	beforePeriod := ResolveCommissionMonthlyPeriod(beforeReset)
	if beforePeriod.PeriodKey != "2026-05-10" {
		t.Fatalf("expected previous period key 2026-05-10, got %s", beforePeriod.PeriodKey)
	}
	if beforePeriod.Timezone != "Asia/Shanghai" {
		t.Fatalf("expected Asia/Shanghai timezone, got %s", beforePeriod.Timezone)
	}

	atReset := time.Date(2026, time.June, 10, 0, 0, 0, 0, loc).Unix()
	currentPeriod := ResolveCommissionMonthlyPeriod(atReset)
	if currentPeriod.PeriodKey != "2026-06-10" {
		t.Fatalf("expected current period key 2026-06-10, got %s", currentPeriod.PeriodKey)
	}
	if currentPeriod.PeriodStartAt != atReset {
		t.Fatalf("expected period start %d, got %d", atReset, currentPeriod.PeriodStartAt)
	}
}

func TestBackfillBusinessDailyStatsRebuildsCompletedMonthlyStats(t *testing.T) {
	cfg := operation_setting.GetCommissionTierResetSetting()
	originalDay := cfg.ResetDay
	originalHour := cfg.ResetHour
	originalMinute := cfg.ResetMinute
	originalSecond := cfg.ResetSecond
	originalTimezone := cfg.Timezone
	defer func() {
		cfg.ResetDay = originalDay
		cfg.ResetHour = originalHour
		cfg.ResetMinute = originalMinute
		cfg.ResetSecond = originalSecond
		cfg.Timezone = originalTimezone
	}()

	cfg.ResetDay = 10
	cfg.ResetHour = 0
	cfg.ResetMinute = 0
	cfg.ResetSecond = 0
	cfg.Timezone = "Asia/Shanghai"

	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	employeeUserId := 920001
	customerUserId := 920002
	firstCreatedAt := time.Date(2025, time.May, 12, 9, 0, 0, 0, loc).Unix()
	secondCreatedAt := time.Date(2025, time.June, 9, 20, 0, 0, 0, loc).Unix()
	period := ResolveCommissionMonthlyPeriod(firstCreatedAt)

	cleanup := func() {
		if !allowTestDBCleanup() {
			return
		}
		_ = DB.Where("employee_user_id = ?", employeeUserId).Delete(&EmployeeCommissionLog{}).Error
		_ = DB.Where("employee_user_id = ?", employeeUserId).Delete(&EmployeeCommissionDailyStat{}).Error
		_ = DB.Where("employee_user_id = ?", employeeUserId).Delete(&EmployeeCustomerCommissionDailyStat{}).Error
		_ = DB.Where("employee_user_id = ?", employeeUserId).Delete(&EmployeeCommissionMonthlyStat{}).Error
	}
	cleanup()
	t.Cleanup(cleanup)

	if err := DB.Create([]*EmployeeCommissionLog{
		{
			EmployeeUserId:  employeeUserId,
			CustomerUserId:  customerUserId,
			RevenueQuota:    1000,
			CostQuota:       400,
			ProfitQuota:     600,
			CommissionQuota: 60,
			CreatedAt:       firstCreatedAt,
		},
		{
			EmployeeUserId:  employeeUserId,
			CustomerUserId:  customerUserId,
			RevenueQuota:    2000,
			CostQuota:       800,
			ProfitQuota:     1200,
			CommissionQuota: 120,
			CreatedAt:       secondCreatedAt,
		},
	}).Error; err != nil {
		t.Fatal(err)
	}

	result, err := BackfillBusinessDailyStats(period.PeriodStartAt, period.PeriodEndAt, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.MonthlyRows != 1 {
		t.Fatalf("expected 1 rebuilt monthly row, got %d", result.MonthlyRows)
	}

	var stat EmployeeCommissionMonthlyStat
	if err := DB.Where("period_start_at = ? AND employee_user_id = ?", period.PeriodStartAt, employeeUserId).First(&stat).Error; err != nil {
		t.Fatal(err)
	}
	if stat.CommissionQuota != 180 || stat.ProfitQuota != 1800 || stat.RecordCount != 2 {
		t.Fatalf("unexpected monthly stat: commission=%d profit=%d records=%d", stat.CommissionQuota, stat.ProfitQuota, stat.RecordCount)
	}

	if _, err := BackfillBusinessDailyStats(period.PeriodStartAt, period.PeriodEndAt, 1); err != nil {
		t.Fatal(err)
	}
	stat = EmployeeCommissionMonthlyStat{}
	if err := DB.Where("period_start_at = ? AND employee_user_id = ?", period.PeriodStartAt, employeeUserId).First(&stat).Error; err != nil {
		t.Fatal(err)
	}
	if stat.CommissionQuota != 180 || stat.ProfitQuota != 1800 || stat.RecordCount != 2 {
		t.Fatalf("monthly stat should be idempotent, got commission=%d profit=%d records=%d", stat.CommissionQuota, stat.ProfitQuota, stat.RecordCount)
	}
}

func TestNeedsBusinessStatsBackfillDetectsMissingMonthlyStats(t *testing.T) {
	cfg := operation_setting.GetCommissionTierResetSetting()
	originalDay := cfg.ResetDay
	originalHour := cfg.ResetHour
	originalMinute := cfg.ResetMinute
	originalSecond := cfg.ResetSecond
	originalTimezone := cfg.Timezone
	defer func() {
		cfg.ResetDay = originalDay
		cfg.ResetHour = originalHour
		cfg.ResetMinute = originalMinute
		cfg.ResetSecond = originalSecond
		cfg.Timezone = originalTimezone
	}()

	cfg.ResetDay = 10
	cfg.ResetHour = 0
	cfg.ResetMinute = 0
	cfg.ResetSecond = 0
	cfg.Timezone = "Asia/Shanghai"

	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	employeeUserId := 920011
	customerUserId := 920012
	createdAt := time.Date(2025, time.April, 12, 10, 0, 0, 0, loc).Unix()
	period := ResolveCommissionMonthlyPeriod(createdAt)

	cleanup := func() {
		if !allowTestDBCleanup() {
			return
		}
		_ = DB.Where("employee_user_id = ?", employeeUserId).Delete(&EmployeeCommissionLog{}).Error
		_ = DB.Where("employee_user_id = ?", employeeUserId).Delete(&EmployeeCommissionDailyStat{}).Error
		_ = DB.Where("employee_user_id = ?", employeeUserId).Delete(&EmployeeCustomerCommissionDailyStat{}).Error
		_ = DB.Where("employee_user_id = ?", employeeUserId).Delete(&EmployeeCommissionMonthlyStat{}).Error
	}
	cleanup()
	t.Cleanup(cleanup)

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	previousCompleted := common.OptionMap["BusinessStatsBackfillCompleted"]
	previousCustomer := common.OptionMap["BusinessStatsEmployeeCustomerBackfillCaughtUp"]
	previousMonthly := common.OptionMap["BusinessStatsCommissionMonthlyBackfillCaughtUp"]
	common.OptionMap["BusinessStatsBackfillCompleted"] = "true"
	common.OptionMap["BusinessStatsEmployeeCustomerBackfillCaughtUp"] = "true"
	delete(common.OptionMap, "BusinessStatsCommissionMonthlyBackfillCaughtUp")
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		restoreOptionForTest("BusinessStatsBackfillCompleted", previousCompleted)
		restoreOptionForTest("BusinessStatsEmployeeCustomerBackfillCaughtUp", previousCustomer)
		restoreOptionForTest("BusinessStatsCommissionMonthlyBackfillCaughtUp", previousMonthly)
		common.OptionMapRWMutex.Unlock()
	})

	if err := DB.Create(&EmployeeCommissionLog{
		EmployeeUserId:  employeeUserId,
		CustomerUserId:  customerUserId,
		RevenueQuota:    1000,
		CostQuota:       500,
		ProfitQuota:     500,
		CommissionQuota: 50,
		CreatedAt:       createdAt,
	}).Error; err != nil {
		t.Fatal(err)
	}

	needed, err := needsEmployeeCommissionMonthlyStatsBackfillForPeriods([]CommissionMonthlyPeriod{period})
	if err != nil {
		t.Fatal(err)
	}
	if !needed {
		t.Fatal("expected backfill to be needed when completed monthly stats are missing")
	}

	if _, err := BackfillBusinessDailyStats(period.PeriodStartAt, period.PeriodEndAt, 1); err != nil {
		t.Fatal(err)
	}
	needed, err = needsEmployeeCommissionMonthlyStatsBackfillForPeriods([]CommissionMonthlyPeriod{period})
	if err != nil {
		t.Fatal(err)
	}
	if needed {
		t.Fatal("expected backfill to be caught up after monthly stats are rebuilt")
	}
	_ = UpdateOption("BusinessStatsCommissionMonthlyBackfillCaughtUp", "true")
	common.OptionMapRWMutex.RLock()
	caughtUp := common.OptionMap["BusinessStatsCommissionMonthlyBackfillCaughtUp"]
	common.OptionMapRWMutex.RUnlock()
	if caughtUp != "true" {
		t.Fatalf("expected monthly caught-up flag to be true, got %q", caughtUp)
	}
}

func restoreOptionForTest(key, value string) {
	if value == "" {
		delete(common.OptionMap, key)
		return
	}
	common.OptionMap[key] = value
}

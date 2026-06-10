package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
)

func TestGetCustomerCommissionTotalsUsesDailyStats(t *testing.T) {
	employeeUserId := 910001
	customerUserId := 910002
	statDate := int64(1770076800)

	if err := DB.Where("employee_user_id = ? AND customer_user_id = ?", employeeUserId, customerUserId).Delete(&EmployeeCommissionLog{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := DB.Where("employee_user_id = ? AND customer_user_id = ?", employeeUserId, customerUserId).Delete(&EmployeeCustomerCommissionDailyStat{}).Error; err != nil {
		t.Fatal(err)
	}

	detailAmount := int64(9999)
	statAmount := int64(1234)
	if err := DB.Create(&EmployeeCommissionLog{
		EmployeeUserId:  employeeUserId,
		CustomerUserId:  customerUserId,
		CommissionQuota: detailAmount,
		CreatedAt:       statDate,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := DB.Create(&EmployeeCustomerCommissionDailyStat{
		StatDate:        statDate,
		EmployeeUserId:  employeeUserId,
		CustomerUserId:  customerUserId,
		CommissionQuota: statAmount,
		RecordCount:     1,
		LastCreatedAt:   statDate,
	}).Error; err != nil {
		t.Fatal(err)
	}

	totals, err := GetCustomerCommissionTotals(employeeUserId, []int{customerUserId})
	if err != nil {
		t.Fatal(err)
	}
	if totals[customerUserId] != statAmount {
		t.Fatalf("expected daily stat amount %d, got %d", statAmount, totals[customerUserId])
	}
}

func TestGetCustomerCommissionTotalsFallsBackWhenDailyStatsMissing(t *testing.T) {
	employeeUserId := 910021
	customerUserId := 910022
	statDate := int64(1770249600)

	if err := DB.Where("employee_user_id = ? AND customer_user_id = ?", employeeUserId, customerUserId).Delete(&EmployeeCommissionLog{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := DB.Where("employee_user_id = ? AND customer_user_id = ?", employeeUserId, customerUserId).Delete(&EmployeeCustomerCommissionDailyStat{}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = DB.Where("employee_user_id = ? AND customer_user_id = ?", employeeUserId, customerUserId).Delete(&EmployeeCommissionLog{}).Error
		_ = DB.Where("employee_user_id = ? AND customer_user_id = ?", employeeUserId, customerUserId).Delete(&EmployeeCustomerCommissionDailyStat{}).Error
	})

	if err := DB.Create(&EmployeeCommissionLog{
		EmployeeUserId:  employeeUserId,
		CustomerUserId:  customerUserId,
		CommissionQuota: 4321,
		CreatedAt:       statDate,
	}).Error; err != nil {
		t.Fatal(err)
	}

	totals, err := GetCustomerCommissionTotals(employeeUserId, []int{customerUserId})
	if err != nil {
		t.Fatal(err)
	}
	if totals[customerUserId] != 4321 {
		t.Fatalf("expected ledger fallback amount %d, got %d", 4321, totals[customerUserId])
	}
}

func TestNeedsBusinessStatsBackfillDetectsMissingCustomerDailyStats(t *testing.T) {
	employeeUserId := 910011
	customerUserId := 910012
	statDate := int64(1770163200)

	if err := DB.Where("employee_user_id = ? AND customer_user_id = ?", employeeUserId, customerUserId).Delete(&EmployeeCommissionLog{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := DB.Where("employee_user_id = ? AND customer_user_id = ?", employeeUserId, customerUserId).Delete(&EmployeeCustomerCommissionDailyStat{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := DB.Create(&EmployeeCommissionLog{
		EmployeeUserId:  employeeUserId,
		CustomerUserId:  customerUserId,
		CommissionQuota: 100,
		CreatedAt:       statDate,
	}).Error; err != nil {
		t.Fatal(err)
	}

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	previous := common.OptionMap["BusinessStatsBackfillCompleted"]
	common.OptionMap["BusinessStatsBackfillCompleted"] = "true"
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if previous == "" {
			delete(common.OptionMap, "BusinessStatsBackfillCompleted")
		} else {
			common.OptionMap["BusinessStatsBackfillCompleted"] = previous
		}
		common.OptionMapRWMutex.Unlock()
		_ = DB.Where("employee_user_id = ? AND customer_user_id = ?", employeeUserId, customerUserId).Delete(&EmployeeCommissionLog{}).Error
		_ = DB.Where("employee_user_id = ? AND customer_user_id = ?", employeeUserId, customerUserId).Delete(&EmployeeCustomerCommissionDailyStat{}).Error
	})

	if !NeedsBusinessStatsBackfill() {
		t.Fatal("expected backfill to be needed when customer daily stats are missing")
	}

}

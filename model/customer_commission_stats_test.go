package model

import (
	"testing"
)

func TestGetCustomerCommissionTotalsUsesDailyStats(t *testing.T) {
	employeeUserId := 910001
	customerUserId := 910002
	statDate := int64(1770076800)

	if allowTestDBCleanup() {
		if err := DB.Where("employee_user_id = ? AND customer_user_id = ?", employeeUserId, customerUserId).Delete(&EmployeeCommissionLog{}).Error; err != nil {
			t.Fatal(err)
		}
		if err := DB.Where("employee_user_id = ? AND customer_user_id = ?", employeeUserId, customerUserId).Delete(&EmployeeCustomerCommissionDailyStat{}).Error; err != nil {
			t.Fatal(err)
		}
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

func TestGetCustomerCommissionTotalsDoesNotFallbackWhenDailyStatsMissing(t *testing.T) {
	employeeUserId := 910021
	customerUserId := 910022
	statDate := int64(1770249600)

	if allowTestDBCleanup() {
		if err := DB.Where("employee_user_id = ? AND customer_user_id = ?", employeeUserId, customerUserId).Delete(&EmployeeCommissionLog{}).Error; err != nil {
			t.Fatal(err)
		}
		if err := DB.Where("employee_user_id = ? AND customer_user_id = ?", employeeUserId, customerUserId).Delete(&EmployeeCustomerCommissionDailyStat{}).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		if !allowTestDBCleanup() {
			return
		}
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
	if totals[customerUserId] != 0 {
		t.Fatalf("expected 0 without daily stats, got %d", totals[customerUserId])
	}
}


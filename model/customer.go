package model

import (
	"errors"
	"strconv"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

const (
	CustomerStatusEnabled  = 1
	CustomerStatusDisabled = 2
)

const (
	QuotaAdjustModeAdd      = "add"
	QuotaAdjustModeSubtract = "subtract"
	QuotaAdjustModeOverride = "override"
)

var (
	ErrCustomerQuotaMustBePositive = errors.New("quota must be greater than 0")
	ErrEmployeeQuotaNotEnough      = errors.New("employee quota is not enough")
	ErrCustomerQuotaNotEnough      = errors.New("customer quota is not enough")
	ErrInvalidAdjustMode           = errors.New("invalid adjust mode")
)

// CustomerProfile records the ownership relation between an employee user and
// a customer user. A customer can belong to only one employee.
type CustomerProfile struct {
	Id             int    `json:"id"`
	EmployeeUserId int    `json:"employee_user_id" gorm:"index;not null"`
	CustomerUserId int    `json:"customer_user_id" gorm:"uniqueIndex;not null"`
	Status         int    `json:"status" gorm:"type:int;default:1"`
	Remark         string `json:"remark,omitempty" gorm:"type:varchar(255);default:''"`
	CreatedAt      int64  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt      int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

// CustomerQuotaLog records every quota transfer from an employee to a customer.
type CustomerQuotaLog struct {
	Id                  int    `json:"id"`
	EmployeeUserId      int    `json:"employee_user_id" gorm:"index;not null"`
	CustomerUserId      int    `json:"customer_user_id" gorm:"index;not null"`
	QuotaDelta          int    `json:"quota_delta" gorm:"not null"`
	CustomerBeforeQuota int    `json:"customer_before_quota" gorm:"default:0"`
	CustomerAfterQuota  int    `json:"customer_after_quota" gorm:"default:0"`
	EmployeeBeforeQuota int    `json:"employee_before_quota" gorm:"default:0"`
	EmployeeAfterQuota  int    `json:"employee_after_quota" gorm:"default:0"`
	Remark              string `json:"remark,omitempty" gorm:"type:varchar(255);default:''"`
	CreatedAt           int64  `json:"created_at" gorm:"autoCreateTime;index"`
}

func GetInvitedCustomersByEmployee(employeeUserId, page, pageSize int) ([]*User, int64, error) {
	var customers []*User
	var total int64
	offset := (page - 1) * pageSize

	tx := DB.Model(&User{}).
		Where("inviter_id = ? AND role = ?", employeeUserId, common.RoleCommonUser).
		Where("id NOT IN (?)", DB.Model(&EmployeeProfile{}).Select("user_id"))
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := tx.Omit("password").Order("id DESC").Offset(offset).Limit(pageSize).Find(&customers).Error; err != nil {
		return nil, 0, err
	}
	return customers, total, nil
}

func GetInvitedCustomerByEmployee(employeeUserId, customerUserId int, selectAll bool) (*User, error) {
	var customer User
	tx := DB.Where("id = ? AND inviter_id = ? AND role = ?", customerUserId, employeeUserId, common.RoleCommonUser)
	if !selectAll {
		tx = tx.Omit("password")
	}
	if err := tx.First(&customer).Error; err != nil {
		return nil, err
	}
	return &customer, nil
}

func GetCustomersByEmployee(employeeUserId, page, pageSize int) ([]*CustomerProfile, int64, error) {
	var customers []*CustomerProfile
	var total int64
	offset := (page - 1) * pageSize
	tx := DB.Model(&CustomerProfile{}).Where("employee_user_id = ?", employeeUserId)
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := tx.Order("id DESC").Offset(offset).Limit(pageSize).Find(&customers).Error; err != nil {
		return nil, 0, err
	}
	return customers, total, nil
}

func GetAllCustomers(page, pageSize int, employeeUserId int) ([]*CustomerProfile, int64, error) {
	var customers []*CustomerProfile
	var total int64
	offset := (page - 1) * pageSize
	tx := DB.Model(&CustomerProfile{})
	if employeeUserId != 0 {
		tx = tx.Where("employee_user_id = ?", employeeUserId)
	}
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := tx.Order("id DESC").Offset(offset).Limit(pageSize).Find(&customers).Error; err != nil {
		return nil, 0, err
	}
	return customers, total, nil
}

func GetCustomerUsedQuotaTotalsByEmployees(employeeUserIds []int) (map[int]int64, error) {
	totals := make(map[int]int64, len(employeeUserIds))
	if len(employeeUserIds) == 0 {
		return totals, nil
	}

	type customerUsedQuotaRow struct {
		EmployeeUserId int
		CustomerUserId int
		UsedQuota      int64
	}

	addRows := func(rows []customerUsedQuotaRow, seen map[string]struct{}) {
		for _, row := range rows {
			key := strconv.Itoa(row.EmployeeUserId) + ":" + strconv.Itoa(row.CustomerUserId)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			totals[row.EmployeeUserId] += row.UsedQuota
		}
	}

	seen := make(map[string]struct{})
	var profileRows []customerUsedQuotaRow
	if err := DB.Model(&CustomerProfile{}).
		Select("customer_profiles.employee_user_id, customer_profiles.customer_user_id, users.used_quota").
		Joins("JOIN users ON users.id = customer_profiles.customer_user_id").
		Where("customer_profiles.employee_user_id IN ?", employeeUserIds).
		Scan(&profileRows).Error; err != nil {
		return nil, err
	}
	addRows(profileRows, seen)

	var invitedRows []customerUsedQuotaRow
	if err := DB.Model(&User{}).
		Select("inviter_id as employee_user_id, id as customer_user_id, used_quota").
		Where("inviter_id IN ? AND role = ?", employeeUserIds, common.RoleCommonUser).
		Where("id NOT IN (?)", DB.Model(&EmployeeProfile{}).Select("user_id")).
		Scan(&invitedRows).Error; err != nil {
		return nil, err
	}
	addRows(invitedRows, seen)

	return totals, nil
}

func GetCustomerCountsByEmployees(employeeUserIds []int) (map[int]int, error) {
	counts := make(map[int]int, len(employeeUserIds))
	if len(employeeUserIds) == 0 {
		return counts, nil
	}

	type customerOwnerRow struct {
		EmployeeUserId int
		CustomerUserId int
	}

	addRows := func(rows []customerOwnerRow, seen map[string]struct{}) {
		for _, row := range rows {
			key := strconv.Itoa(row.EmployeeUserId) + ":" + strconv.Itoa(row.CustomerUserId)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			counts[row.EmployeeUserId]++
		}
	}

	seen := make(map[string]struct{})
	var profileRows []customerOwnerRow
	if err := DB.Model(&CustomerProfile{}).
		Select("employee_user_id, customer_user_id").
		Where("employee_user_id IN ?", employeeUserIds).
		Scan(&profileRows).Error; err != nil {
		return nil, err
	}
	addRows(profileRows, seen)

	var invitedRows []customerOwnerRow
	if err := DB.Model(&User{}).
		Select("inviter_id as employee_user_id, id as customer_user_id").
		Where("inviter_id IN ? AND role = ?", employeeUserIds, common.RoleCommonUser).
		Where("id NOT IN (?)", DB.Model(&EmployeeProfile{}).Select("user_id")).
		Scan(&invitedRows).Error; err != nil {
		return nil, err
	}
	addRows(invitedRows, seen)

	return counts, nil
}

func GetCustomerProfileById(id int) (*CustomerProfile, error) {
	var cp CustomerProfile
	if err := DB.Where("id = ?", id).First(&cp).Error; err != nil {
		return nil, err
	}
	return &cp, nil
}

func GetCustomerProfileByCustomerUserId(customerUserId int) (*CustomerProfile, error) {
	var cp CustomerProfile
	if err := DB.Where("customer_user_id = ?", customerUserId).First(&cp).Error; err != nil {
		return nil, err
	}
	return &cp, nil
}

func CreateCustomerProfile(cp *CustomerProfile) error {
	if cp.Status == 0 {
		cp.Status = CustomerStatusEnabled
	}
	return DB.Create(cp).Error
}

func UpdateCustomerProfile(cp *CustomerProfile) error {
	var count int64
	if err := DB.Model(&CustomerProfile{}).Where("id = ?", cp.Id).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return gorm.ErrRecordNotFound
	}
	return DB.Model(&CustomerProfile{}).Where("id = ?", cp.Id).Updates(map[string]interface{}{
		"status": cp.Status,
		"remark": cp.Remark,
	}).Error
}

func DeleteCustomerProfile(id int) error {
	return DB.Delete(&CustomerProfile{}, "id = ?", id).Error
}

// AdjustCustomerQuota atomically adjusts a customer's quota with support for
// three modes:
//   - "add":      deduct `quota` from employee, add to customer
//   - "subtract": deduct `quota` from customer, return to employee
//   - "override": set customer quota to exactly `quota`, difference settled with employee
func AdjustCustomerQuota(employeeUserId, customerUserId, quota int, mode, remark string) (*CustomerQuotaLog, error) {
	if quota < 0 {
		return nil, ErrCustomerQuotaMustBePositive
	}
	if mode != QuotaAdjustModeAdd && mode != QuotaAdjustModeSubtract && mode != QuotaAdjustModeOverride {
		return nil, ErrInvalidAdjustMode
	}

	var logEntry CustomerQuotaLog
	var effectiveDelta int
	err := DB.Transaction(func(tx *gorm.DB) error {
		var empUser User
		if err := tx.Select("id", "quota").Where("id = ?", employeeUserId).First(&empUser).Error; err != nil {
			return err
		}
		var custUser User
		if err := tx.Select("id", "quota").Where("id = ?", customerUserId).First(&custUser).Error; err != nil {
			return err
		}

		empBefore := empUser.Quota
		custBefore := custUser.Quota

		// resolve effective delta: positive = customer gains, negative = customer loses
		switch mode {
		case QuotaAdjustModeAdd:
			effectiveDelta = quota
		case QuotaAdjustModeSubtract:
			effectiveDelta = -quota
		case QuotaAdjustModeOverride:
			effectiveDelta = quota - custBefore
		}

		if effectiveDelta > 0 {
			// employee → customer
			result := tx.Model(&User{}).
				Where("id = ? AND quota >= ?", employeeUserId, effectiveDelta).
				Update("quota", gorm.Expr("quota - ?", effectiveDelta))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return ErrEmployeeQuotaNotEnough
			}
			if err := tx.Model(&User{}).Where("id = ?", customerUserId).
				Update("quota", gorm.Expr("quota + ?", effectiveDelta)).Error; err != nil {
				return err
			}
		} else if effectiveDelta < 0 {
			// customer → employee
			deduct := -effectiveDelta
			result := tx.Model(&User{}).
				Where("id = ? AND quota >= ?", customerUserId, deduct).
				Update("quota", gorm.Expr("quota - ?", deduct))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return ErrCustomerQuotaNotEnough
			}
			if err := tx.Model(&User{}).Where("id = ?", employeeUserId).
				Update("quota", gorm.Expr("quota + ?", deduct)).Error; err != nil {
				return err
			}
		}
		// effectiveDelta == 0: override to same value, no-op for balances

		logEntry = CustomerQuotaLog{
			EmployeeUserId:      employeeUserId,
			CustomerUserId:      customerUserId,
			QuotaDelta:          effectiveDelta,
			CustomerBeforeQuota: custBefore,
			CustomerAfterQuota:  custBefore + effectiveDelta,
			EmployeeBeforeQuota: empBefore,
			EmployeeAfterQuota:  empBefore - effectiveDelta,
			Remark:              remark,
		}
		return tx.Create(&logEntry).Error
	})
	if err != nil {
		return nil, err
	}

	if common.RedisEnabled && effectiveDelta != 0 {
		if effectiveDelta > 0 {
			_ = cacheDecrUserQuota(employeeUserId, int64(effectiveDelta))
			_ = cacheIncrUserQuota(customerUserId, int64(effectiveDelta))
		} else {
			deduct := -effectiveDelta
			_ = cacheIncrUserQuota(employeeUserId, int64(deduct))
			_ = cacheDecrUserQuota(customerUserId, int64(deduct))
		}
	}

	return &logEntry, nil
}

// TransferQuotaToCustomer is kept for backwards-compatibility.
// New callers should use AdjustCustomerQuota with mode="add".
func TransferQuotaToCustomer(employeeUserId, customerUserId, quota int, remark string) (*CustomerQuotaLog, error) {
	return AdjustCustomerQuota(employeeUserId, customerUserId, quota, QuotaAdjustModeAdd, remark)
}

type CustomerQuotaLogFilter struct {
	EmployeeUserId int
	CustomerUserId int
	StartTime      int64
	EndTime        int64
	Page           int
	PageSize       int
}

func GetCustomerQuotaLogs(filter CustomerQuotaLogFilter) ([]*CustomerQuotaLog, int64, error) {
	var logs []*CustomerQuotaLog
	var total int64

	tx := DB.Model(&CustomerQuotaLog{})
	if filter.EmployeeUserId != 0 {
		tx = tx.Where("employee_user_id = ?", filter.EmployeeUserId)
	}
	if filter.CustomerUserId != 0 {
		tx = tx.Where("customer_user_id = ?", filter.CustomerUserId)
	}
	if filter.StartTime != 0 {
		tx = tx.Where("created_at >= ?", filter.StartTime)
	}
	if filter.EndTime != 0 {
		tx = tx.Where("created_at <= ?", filter.EndTime)
	}

	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (filter.Page - 1) * filter.PageSize
	if err := tx.Order("id DESC").Offset(offset).Limit(filter.PageSize).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

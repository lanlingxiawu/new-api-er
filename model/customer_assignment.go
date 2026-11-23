package model

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var (
	ErrInvalidCustomerAssignment  = errors.New("invalid employee or customer for assignment")
	ErrEmployeeAssignmentDisabled = errors.New("employee is disabled")
	ErrCustomerNotAssigned        = errors.New("customer is not assigned to this employee")
)

type CustomerAssignment struct {
	UserID   int
	Username string
	Before   int
	After    int
}

// ChangeCustomerEmployee keeps the invitation and customer relation atomic.
// unassign may remove disabled staff, but can only remove the named relationship.
func ChangeCustomerEmployee(ctx context.Context, employeeID, customerID int, unassign bool) (*CustomerAssignment, error) {
	if employeeID <= 0 || customerID <= 0 {
		return nil, ErrInvalidCustomerAssignment
	}
	var change CustomerAssignment
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var employee EmployeeProfile
		if err := lockForUpdate(tx).First(&employee, employeeID).Error; err != nil {
			return err
		}
		if employee.UserId == customerID {
			return ErrInvalidCustomerAssignment
		}
		// A stable user lock order also covers simultaneous assignments involving
		// the same customer. No network/cache work occurs while holding these locks.
		var users []User
		if err := lockForUpdate(tx).Select("id", "username", "role", "status", "inviter_id").
			Where("id IN ?", []int{employee.UserId, customerID}).Order("id").Find(&users).Error; err != nil {
			return err
		}
		if len(users) != 2 {
			return gorm.ErrRecordNotFound
		}
		staff, customer := users[0], users[1]
		if staff.Id != employee.UserId {
			staff, customer = customer, staff
		}
		if customer.Role != common.RoleCommonUser {
			return ErrInvalidCustomerAssignment
		}
		change = CustomerAssignment{UserID: customer.Id, Username: customer.Username, Before: customer.InviterId, After: employee.UserId}
		if unassign {
			if customer.InviterId != employee.UserId {
				return ErrCustomerNotAssigned
			}
			change.After = 0
			if err := tx.Model(&User{}).Where("id = ?", customerID).Update("inviter_id", 0).Error; err != nil {
				return err
			}
			return tx.Where("customer_user_id = ?", customerID).Delete(&CustomerProfile{}).Error
		}
		if employee.Status != 1 || staff.Status != common.UserStatusEnabled {
			return ErrEmployeeAssignmentDisabled
		}
		if staff.Role != common.RoleCommonUser || customer.Status != common.UserStatusEnabled || staff.InviterId == customerID {
			return ErrInvalidCustomerAssignment
		}
		var customerStaff EmployeeProfile
		err := tx.Select("id").Where("user_id = ?", customerID).Take(&customerStaff).Error
		if err == nil {
			return ErrInvalidCustomerAssignment
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if customer.InviterId != employee.UserId {
			if err := tx.Model(&User{}).Where("id = ?", customerID).Update("inviter_id", employee.UserId).Error; err != nil {
				return err
			}
		}
		var relation CustomerProfile
		err = tx.Where("customer_user_id = ?", customerID).Take(&relation).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(&CustomerProfile{CustomerUserId: customerID, EmployeeUserId: employee.UserId, Status: CustomerStatusEnabled}).Error
		}
		if err != nil {
			return err
		}
		if relation.EmployeeUserId == employee.UserId && relation.Status == CustomerStatusEnabled {
			return nil
		}
		return tx.Model(&relation).Updates(map[string]any{"employee_user_id": employee.UserId, "status": CustomerStatusEnabled}).Error
	})
	if err != nil {
		return nil, err
	}
	InvalidateInviterIdCache(customerID)
	return &change, nil
}

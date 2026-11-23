package model

import (
	"context"
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCustomerAssignmentAtomicAndRepeatable(t *testing.T) {
	db := newAdminTokenModelDB(t)
	require.NoError(t, db.AutoMigrate(&User{}, &EmployeeProfile{}, &CustomerProfile{}))
	users := []User{
		{Username: "assignment-staff-" + common.GetRandomString(8), Role: common.RoleCommonUser, Status: common.UserStatusEnabled},
		{Username: "assignment-customer-" + common.GetRandomString(8), Role: common.RoleCommonUser, Status: common.UserStatusEnabled},
		{Username: "assignment-other-" + common.GetRandomString(8), Role: common.RoleCommonUser, Status: common.UserStatusEnabled},
	}
	require.NoError(t, db.Create(&users).Error)
	employee := EmployeeProfile{UserId: users[0].Id, Status: 1}
	require.NoError(t, db.Create(&employee).Error)
	t.Cleanup(func() {
		db.Where("customer_user_id IN ?", []int{users[1].Id, users[2].Id}).Delete(&CustomerProfile{})
		db.Delete(&employee)
		for _, user := range users {
			InvalidateInviterIdCache(user.Id)
			db.Unscoped().Delete(&user)
		}
	})
	for range 2 {
		change, err := ChangeCustomerEmployee(context.Background(), employee.Id, users[1].Id, false)
		require.NoError(t, err)
		require.Equal(t, users[0].Id, change.After)
		var relation CustomerProfile
		require.NoError(t, db.Where("customer_user_id = ?", users[1].Id).First(&relation).Error)
		require.Equal(t, users[0].Id, relation.EmployeeUserId)
	}
	require.Equal(t, users[0].Id, GetUserInviterId(users[1].Id), "prime the inviter cache")
	// Legacy writes could leave the relation out of sync with users.inviter_id.
	require.NoError(t, db.Model(&CustomerProfile{}).Where("customer_user_id = ?", users[1].Id).Update("employee_user_id", users[2].Id).Error)
	require.NoError(t, db.Model(&employee).Update("status", 2).Error)
	_, err := ChangeCustomerEmployee(context.Background(), employee.Id, users[2].Id, false)
	require.ErrorIs(t, err, ErrEmployeeAssignmentDisabled)
	change, err := ChangeCustomerEmployee(context.Background(), employee.Id, users[1].Id, true)
	require.NoError(t, err, "disabled staff must still be removable")
	require.Equal(t, users[0].Id, change.Before)
	require.Zero(t, GetUserInviterId(users[1].Id), "unassignment invalidates the old cached owner")
	var count int64
	require.NoError(t, db.Model(&CustomerProfile{}).Where("customer_user_id = ?", users[1].Id).Count(&count).Error)
	require.Zero(t, count)
	_, err = ChangeCustomerEmployee(context.Background(), employee.Id, users[1].Id, true)
	require.ErrorIs(t, err, ErrCustomerNotAssigned)

	require.NoError(t, db.Model(&employee).Update("status", 1).Error)
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:assignment-rollback", func(tx *gorm.DB) {
		if tx.Statement.Table == "customer_profiles" {
			tx.AddError(errors.New("injected relation failure"))
		}
	}))
	t.Cleanup(func() { db.Callback().Create().Remove("test:assignment-rollback") })
	_, err = ChangeCustomerEmployee(context.Background(), employee.Id, users[2].Id, false)
	require.ErrorContains(t, err, "injected relation failure")
	var stored User
	require.NoError(t, db.First(&stored, users[2].Id).Error)
	require.Zero(t, stored.InviterId, "failed relation write must roll back users.inviter_id")
}

func TestCustomerAssignmentRejectsInvalidTargets(t *testing.T) {
	db := newAdminTokenModelDB(t)
	require.NoError(t, db.AutoMigrate(&User{}, &EmployeeProfile{}, &CustomerProfile{}))
	staff := User{Username: "assignment-validation-" + common.GetRandomString(8), Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&staff).Error)
	employee := EmployeeProfile{UserId: staff.Id, Status: 1}
	require.NoError(t, db.Create(&employee).Error)
	t.Cleanup(func() { db.Delete(&employee); db.Unscoped().Delete(&staff) })
	_, err := ChangeCustomerEmployee(context.Background(), employee.Id, staff.Id, false)
	require.ErrorIs(t, err, ErrInvalidCustomerAssignment)
	for _, tc := range []struct {
		name                      string
		role, status              int
		employee, mutual, deleted bool
	}{
		{name: "admin", role: common.RoleAdminUser, status: common.UserStatusEnabled},
		{name: "disabled", role: common.RoleCommonUser, status: common.UserStatusDisabled},
		{name: "employee", role: common.RoleCommonUser, status: common.UserStatusEnabled, employee: true},
		{name: "mutual", role: common.RoleCommonUser, status: common.UserStatusEnabled, mutual: true},
		{name: "deleted", role: common.RoleCommonUser, status: common.UserStatusEnabled, deleted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			customer := User{Username: "assignment-" + common.GetRandomString(12), Role: tc.role, Status: tc.status}
			require.NoError(t, db.Create(&customer).Error)
			t.Cleanup(func() { db.Unscoped().Delete(&customer) })
			if tc.employee {
				profile := EmployeeProfile{UserId: customer.Id, Status: 1}
				require.NoError(t, db.Create(&profile).Error)
				t.Cleanup(func() { db.Delete(&profile) })
			}
			if tc.mutual {
				require.NoError(t, db.Model(&staff).Update("inviter_id", customer.Id).Error)
				t.Cleanup(func() { db.Model(&staff).Update("inviter_id", 0) })
			}
			if tc.deleted {
				require.NoError(t, db.Delete(&customer).Error)
			}
			_, err := ChangeCustomerEmployee(context.Background(), employee.Id, customer.Id, false)
			require.Error(t, err)
			var stored User
			require.NoError(t, db.Unscoped().First(&stored, customer.Id).Error)
			require.Zero(t, stored.InviterId)
		})
	}
}

package model

import (
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// reloadQuota reads the current quota for a user id.
func reloadQuota(t *testing.T, userID int) int {
	t.Helper()
	var u User
	require.NoError(t, DB.Select("id", "quota").First(&u, userID).Error)
	return u.Quota
}

func itoaID(id int) string { return strconv.Itoa(id) }

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func custMakeProfile(t *testing.T, empID, custID int, mut func(*CustomerProfile)) *CustomerProfile {
	t.Helper()
	requireDB(t)
	cp := &CustomerProfile{
		EmployeeUserId: empID,
		CustomerUserId: custID,
		Status:         CustomerStatusEnabled,
	}
	if mut != nil {
		mut(cp)
	}
	require.NoError(t, DB.Create(cp).Error)
	deleteByID(t, &CustomerProfile{}, cp.Id)
	return cp
}

// custCleanupLogs removes quota-log rows created for an employee during a test.
func custCleanupLogs(t *testing.T, empID int) {
	t.Cleanup(func() {
		if DB != nil {
			DB.Unscoped().Where("employee_user_id = ?", empID).Delete(&CustomerQuotaLog{})
		}
	})
}

// ---------------------------------------------------------------------------
// Profile CRUD + scoping
// ---------------------------------------------------------------------------

func TestCustomer_ProfileGetByIdAndCustomerId(t *testing.T) {
	emp := mkUser(t, nil)
	cust := mkUser(t, nil)
	cp := custMakeProfile(t, emp.Id, cust.Id, nil)

	got, err := GetCustomerProfileById(cp.Id)
	require.NoError(t, err)
	assert.Equal(t, emp.Id, got.EmployeeUserId)

	got, err = GetCustomerProfileByCustomerUserId(cust.Id)
	require.NoError(t, err)
	assert.Equal(t, cp.Id, got.Id)

	// not found
	_, err = GetCustomerProfileById(900_000_999)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = GetCustomerProfileByCustomerUserId(900_000_998)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestCustomer_GetCustomersByEmployeeScoping(t *testing.T) {
	empA := mkUser(t, nil)
	empB := mkUser(t, nil)
	custA := mkUser(t, nil)
	custB := mkUser(t, nil)
	custMakeProfile(t, empA.Id, custA.Id, nil)
	custMakeProfile(t, empB.Id, custB.Id, nil)

	// scoped to empA -> only custA's profile
	list, total, err := GetCustomersByEmployee(empA.Id, 1, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, list, 1)
	assert.Equal(t, custA.Id, list[0].CustomerUserId)

	// empB does not see empA's customer
	list, total, err = GetCustomersByEmployee(empB.Id, 1, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	assert.Equal(t, custB.Id, list[0].CustomerUserId)

	// GetAllCustomers filtered by employee behaves the same
	list, total, err = GetAllCustomers(1, 10, empA.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	assert.Equal(t, custA.Id, list[0].CustomerUserId)

	// GetAllCustomers with 0 returns a superset that contains both
	all, allTotal, err := GetAllCustomers(1, 1000, 0)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, allTotal, int64(2))
	ids := map[int]bool{}
	for _, c := range all {
		ids[c.CustomerUserId] = true
	}
	assert.True(t, ids[custA.Id])
	assert.True(t, ids[custB.Id])
}

func TestCustomer_CreateUpdateDeleteProfile(t *testing.T) {
	emp := mkUser(t, nil)
	cust := mkUser(t, nil)

	// Create defaults Status to enabled when zero
	cp := &CustomerProfile{EmployeeUserId: emp.Id, CustomerUserId: cust.Id}
	require.NoError(t, CreateCustomerProfile(cp))
	deleteByID(t, &CustomerProfile{}, cp.Id)
	assert.Equal(t, CustomerStatusEnabled, cp.Status)

	// Update existing -> status + remark change
	cp.Status = CustomerStatusDisabled
	cp.Remark = "vip customer"
	require.NoError(t, UpdateCustomerProfile(cp))
	reloaded, err := GetCustomerProfileById(cp.Id)
	require.NoError(t, err)
	assert.Equal(t, CustomerStatusDisabled, reloaded.Status)
	assert.Equal(t, "vip customer", reloaded.Remark)

	// Update non-existent -> ErrRecordNotFound
	err = UpdateCustomerProfile(&CustomerProfile{Id: 900_000_777})
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	// Delete
	require.NoError(t, DeleteCustomerProfile(cp.Id))
	_, err = GetCustomerProfileById(cp.Id)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestCustomer_UpsertProfileEmployee(t *testing.T) {
	empA := mkUser(t, nil)
	empB := mkUser(t, nil)
	cust := mkUser(t, nil)

	// no existing profile -> create
	require.NoError(t, UpsertCustomerProfileEmployee(cust.Id, empA.Id))
	got, err := GetCustomerProfileByCustomerUserId(cust.Id)
	require.NoError(t, err)
	deleteByID(t, &CustomerProfile{}, got.Id)
	assert.Equal(t, empA.Id, got.EmployeeUserId)
	assert.Equal(t, CustomerStatusEnabled, got.Status)

	// existing profile -> reassign employee
	require.NoError(t, UpsertCustomerProfileEmployee(cust.Id, empB.Id))
	got, err = GetCustomerProfileByCustomerUserId(cust.Id)
	require.NoError(t, err)
	assert.Equal(t, empB.Id, got.EmployeeUserId)
}

func TestCustomer_UnassignFromEmployee(t *testing.T) {
	emp := mkUser(t, func(u *User) { /* default */ })
	cust := mkUser(t, func(u *User) { u.InviterId = 0 })
	// bind
	require.NoError(t, DB.Model(&User{}).Where("id = ?", cust.Id).Update("inviter_id", emp.Id).Error)
	cp := custMakeProfile(t, emp.Id, cust.Id, nil)

	// wrong employee -> record not found, nothing changed
	err := UnassignCustomerFromEmployee(cust.Id, emp.Id+424242)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	// correct employee -> inviter cleared + profile removed
	require.NoError(t, UnassignCustomerFromEmployee(cust.Id, emp.Id))
	var reloaded User
	require.NoError(t, DB.Select("id", "inviter_id").First(&reloaded, cust.Id).Error)
	assert.Equal(t, 0, reloaded.InviterId)
	_, err = GetCustomerProfileById(cp.Id)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

// ---------------------------------------------------------------------------
// Invited-customer listing (users.inviter_id based) + scoping/exclusion
// ---------------------------------------------------------------------------

func TestCustomer_InvitedCustomersFilterAndExclusion(t *testing.T) {
	emp := mkUser(t, nil)
	c1 := mkUser(t, func(u *User) {
		u.InviterId = emp.Id
		u.DisplayName = "AlphaCustomer"
	})
	c2 := mkUser(t, func(u *User) {
		u.InviterId = emp.Id
		u.DisplayName = "BetaCustomer"
	})
	// c3 is invited but is itself an active employee -> must be excluded
	c3 := mkUser(t, func(u *User) { u.InviterId = emp.Id })
	ep := &EmployeeProfile{UserId: c3.Id, Status: 1}
	require.NoError(t, DB.Create(ep).Error)
	deleteByID(t, &EmployeeProfile{}, ep.Id)

	// basic listing excludes the employee-customer
	list, total, err := GetInvitedCustomersByEmployee(emp.Id, 1, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	ids := map[int]bool{}
	for _, u := range list {
		ids[u.Id] = true
	}
	assert.True(t, ids[c1.Id])
	assert.True(t, ids[c2.Id])
	assert.False(t, ids[c3.Id], "active employee must be excluded from invited customers")

	// keyword filter on display name
	list, total, err = GetInvitedCustomersByEmployeeWithFilter(InvitedCustomerFilter{
		EmployeeUserId: emp.Id, Keyword: "Alpha", Page: 1, PageSize: 10,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, list, 1)
	assert.Equal(t, c1.Id, list[0].Id)

	// numeric keyword matches by id
	_, total, err = GetInvitedCustomersByEmployeeWithFilter(InvitedCustomerFilter{
		EmployeeUserId: emp.Id, Keyword: itoaID(c2.Id), Page: 1, PageSize: 10,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)

	// specific customer id filter
	_, total, err = GetInvitedCustomersByEmployeeWithFilter(InvitedCustomerFilter{
		EmployeeUserId: emp.Id, CustomerUserId: c1.Id, Page: 1, PageSize: 10,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)

	// status filter (enabled) still returns both real customers
	_, total, err = GetInvitedCustomersByEmployeeWithFilter(InvitedCustomerFilter{
		EmployeeUserId: emp.Id, Status: common.UserStatusEnabled, Page: 1, PageSize: 10,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)

	// single-customer getter is employee-scoped
	got, err := GetInvitedCustomerByEmployee(emp.Id, c1.Id, false)
	require.NoError(t, err)
	assert.Equal(t, c1.Id, got.Id)
	// wrong employee -> not found (no cross-employee leak)
	_, err = GetInvitedCustomerByEmployee(emp.Id+9999, c1.Id, true)
	assert.Error(t, err)
}

func TestCustomer_UsedQuotaTotalsAndCounts(t *testing.T) {
	// empty input short-circuits
	totals, err := GetCustomerUsedQuotaTotalsByEmployees(nil)
	require.NoError(t, err)
	assert.Empty(t, totals)
	counts, err := GetCustomerCountsByEmployees(nil)
	require.NoError(t, err)
	assert.Empty(t, counts)

	emp := mkUser(t, nil)
	cA := mkUser(t, func(u *User) { u.InviterId = emp.Id; u.UsedQuota = 100 })
	cB := mkUser(t, func(u *User) { u.InviterId = emp.Id; u.UsedQuota = 200 })
	// cA also has an explicit profile under emp; must be de-duplicated (counted once)
	custMakeProfile(t, emp.Id, cA.Id, nil)

	totals, err = GetCustomerUsedQuotaTotalsByEmployees([]int{emp.Id})
	require.NoError(t, err)
	assert.EqualValues(t, 300, totals[emp.Id]) // 100 + 200, cA not double-counted

	counts, err = GetCustomerCountsByEmployees([]int{emp.Id})
	require.NoError(t, err)
	assert.Equal(t, 2, counts[emp.Id])

	_ = cB
}

// ---------------------------------------------------------------------------
// Quota adjustment (billing integrity) + logs
// ---------------------------------------------------------------------------

func TestCustomer_AdjustQuotaAdd(t *testing.T) {
	emp := mkUser(t, func(u *User) { u.Quota = 1000 })
	cust := mkUser(t, func(u *User) { u.Quota = 200 })
	custCleanupLogs(t, emp.Id)

	log, err := AdjustCustomerQuota(emp.Id, cust.Id, 300, QuotaAdjustModeAdd, "topup")
	require.NoError(t, err)
	require.NotNil(t, log)
	assert.Equal(t, 300, log.QuotaDelta)
	assert.Equal(t, 200, log.CustomerBeforeQuota)
	assert.Equal(t, 500, log.CustomerAfterQuota)
	assert.Equal(t, 1000, log.EmployeeBeforeQuota)
	assert.Equal(t, 700, log.EmployeeAfterQuota)

	assert.Equal(t, 700, reloadQuota(t, emp.Id))
	assert.Equal(t, 500, reloadQuota(t, cust.Id))

	// TransferQuotaToCustomer is an alias for mode=add
	_, err = TransferQuotaToCustomer(emp.Id, cust.Id, 100, "again")
	require.NoError(t, err)
	assert.Equal(t, 600, reloadQuota(t, emp.Id))
	assert.Equal(t, 600, reloadQuota(t, cust.Id))
}

func TestCustomer_AdjustQuotaSubtract(t *testing.T) {
	emp := mkUser(t, func(u *User) { u.Quota = 1000 })
	cust := mkUser(t, func(u *User) { u.Quota = 200 })
	custCleanupLogs(t, emp.Id)

	log, err := AdjustCustomerQuota(emp.Id, cust.Id, 150, QuotaAdjustModeSubtract, "claw back")
	require.NoError(t, err)
	assert.Equal(t, -150, log.QuotaDelta)
	assert.Equal(t, 50, log.CustomerAfterQuota)
	assert.Equal(t, 1150, log.EmployeeAfterQuota)
	assert.Equal(t, 1150, reloadQuota(t, emp.Id))
	assert.Equal(t, 50, reloadQuota(t, cust.Id))
}

func TestCustomer_AdjustQuotaOverride(t *testing.T) {
	// override up (employee pays the difference)
	emp := mkUser(t, func(u *User) { u.Quota = 1000 })
	cust := mkUser(t, func(u *User) { u.Quota = 200 })
	custCleanupLogs(t, emp.Id)
	log, err := AdjustCustomerQuota(emp.Id, cust.Id, 500, QuotaAdjustModeOverride, "set")
	require.NoError(t, err)
	assert.Equal(t, 300, log.QuotaDelta)
	assert.Equal(t, 500, reloadQuota(t, cust.Id))
	assert.Equal(t, 700, reloadQuota(t, emp.Id))

	// override down (difference returns to employee)
	emp2 := mkUser(t, func(u *User) { u.Quota = 1000 })
	cust2 := mkUser(t, func(u *User) { u.Quota = 200 })
	custCleanupLogs(t, emp2.Id)
	log, err = AdjustCustomerQuota(emp2.Id, cust2.Id, 50, QuotaAdjustModeOverride, "set")
	require.NoError(t, err)
	assert.Equal(t, -150, log.QuotaDelta)
	assert.Equal(t, 50, reloadQuota(t, cust2.Id))
	assert.Equal(t, 1150, reloadQuota(t, emp2.Id))

	// override to the same value -> zero delta no-op, still logs
	emp3 := mkUser(t, func(u *User) { u.Quota = 1000 })
	cust3 := mkUser(t, func(u *User) { u.Quota = 200 })
	custCleanupLogs(t, emp3.Id)
	log, err = AdjustCustomerQuota(emp3.Id, cust3.Id, 200, QuotaAdjustModeOverride, "noop")
	require.NoError(t, err)
	assert.Equal(t, 0, log.QuotaDelta)
	assert.Equal(t, 200, reloadQuota(t, cust3.Id))
	assert.Equal(t, 1000, reloadQuota(t, emp3.Id))
}

func TestCustomer_AdjustQuotaGuardsAndInsufficient(t *testing.T) {
	// negative quota rejected
	_, err := AdjustCustomerQuota(1, 2, -1, QuotaAdjustModeAdd, "")
	assert.ErrorIs(t, err, ErrCustomerQuotaMustBePositive)

	// invalid mode rejected
	_, err = AdjustCustomerQuota(1, 2, 10, "bogus", "")
	assert.ErrorIs(t, err, ErrInvalidAdjustMode)

	// employee has insufficient quota for an add
	emp := mkUser(t, func(u *User) { u.Quota = 100 })
	cust := mkUser(t, func(u *User) { u.Quota = 0 })
	custCleanupLogs(t, emp.Id)
	_, err = AdjustCustomerQuota(emp.Id, cust.Id, 300, QuotaAdjustModeAdd, "")
	assert.ErrorIs(t, err, ErrEmployeeQuotaNotEnough)
	assert.Equal(t, 100, reloadQuota(t, emp.Id)) // unchanged
	assert.Equal(t, 0, reloadQuota(t, cust.Id))

	// customer has insufficient quota for a subtract
	emp2 := mkUser(t, func(u *User) { u.Quota = 1000 })
	cust2 := mkUser(t, func(u *User) { u.Quota = 100 })
	custCleanupLogs(t, emp2.Id)
	_, err = AdjustCustomerQuota(emp2.Id, cust2.Id, 300, QuotaAdjustModeSubtract, "")
	assert.ErrorIs(t, err, ErrCustomerQuotaNotEnough)
	assert.Equal(t, 1000, reloadQuota(t, emp2.Id))
	assert.Equal(t, 100, reloadQuota(t, cust2.Id))

	// quota==0 is accepted (note: error var is named MustBePositive but code only rejects <0)
	emp3 := mkUser(t, func(u *User) { u.Quota = 500 })
	cust3 := mkUser(t, func(u *User) { u.Quota = 500 })
	custCleanupLogs(t, emp3.Id)
	log, err := AdjustCustomerQuota(emp3.Id, cust3.Id, 0, QuotaAdjustModeAdd, "zero")
	require.NoError(t, err)
	assert.Equal(t, 0, log.QuotaDelta)
	assert.Equal(t, 500, reloadQuota(t, emp3.Id))
	assert.Equal(t, 500, reloadQuota(t, cust3.Id))
}

func TestCustomer_AdjustQuotaRedisCacheBranch(t *testing.T) {
	enableRedis(t)
	emp := mkUser(t, func(u *User) { u.Quota = 1000 })
	cust := mkUser(t, func(u *User) { u.Quota = 200 })
	custCleanupLogs(t, emp.Id)

	// effectiveDelta > 0 branch (employee -> customer cache mirror)
	_, err := AdjustCustomerQuota(emp.Id, cust.Id, 300, QuotaAdjustModeAdd, "")
	require.NoError(t, err)
	assert.Equal(t, 700, reloadQuota(t, emp.Id))
	assert.Equal(t, 500, reloadQuota(t, cust.Id))

	// effectiveDelta < 0 branch (customer -> employee cache mirror)
	_, err = AdjustCustomerQuota(emp.Id, cust.Id, 100, QuotaAdjustModeSubtract, "")
	require.NoError(t, err)
	assert.Equal(t, 800, reloadQuota(t, emp.Id))
	assert.Equal(t, 400, reloadQuota(t, cust.Id))
}

func TestCustomer_QuotaLogsFilterAndPaging(t *testing.T) {
	emp := mkUser(t, func(u *User) { u.Quota = 10000 })
	cust := mkUser(t, func(u *User) { u.Quota = 0 })
	custCleanupLogs(t, emp.Id)

	for i := 0; i < 3; i++ {
		_, err := AdjustCustomerQuota(emp.Id, cust.Id, 100, QuotaAdjustModeAdd, "batch")
		require.NoError(t, err)
	}

	now := time.Now().Unix()

	// scoped to employee+customer
	logs, total, err := GetCustomerQuotaLogs(CustomerQuotaLogFilter{
		EmployeeUserId: emp.Id, CustomerUserId: cust.Id, Page: 1, PageSize: 10,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
	assert.Len(t, logs, 3)

	// pagination: page size 2
	logs, _, err = GetCustomerQuotaLogs(CustomerQuotaLogFilter{
		EmployeeUserId: emp.Id, Page: 1, PageSize: 2,
	})
	require.NoError(t, err)
	assert.Len(t, logs, 2)
	// ordered id desc
	assert.Greater(t, logs[0].Id, logs[1].Id)

	// time-range including now
	_, total, err = GetCustomerQuotaLogs(CustomerQuotaLogFilter{
		EmployeeUserId: emp.Id, StartTime: now - 3600, EndTime: now + 3600, Page: 1, PageSize: 10,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)

	// time-range in the future excludes everything
	_, total, err = GetCustomerQuotaLogs(CustomerQuotaLogFilter{
		EmployeeUserId: emp.Id, StartTime: now + 100000, Page: 1, PageSize: 10,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 0, total)
}

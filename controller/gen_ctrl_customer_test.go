package controller

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// customer.go: pagination normalization, binding validation guards, and the
// employee-only gating on employee-facing customer handlers.

func TestNormalizePage(t *testing.T) {
	// Defaults.
	ctx, _ := newCtx(t, "GET", "/x", nil)
	page, size := normalizePage(ctx)
	assert.Equal(t, 1, page)
	assert.Equal(t, 20, size)

	// Explicit valid values.
	ctx2, _ := newCtx(t, "GET", "/x?page=3&page_size=50", nil)
	page, size = normalizePage(ctx2)
	assert.Equal(t, 3, page)
	assert.Equal(t, 50, size)

	// Out-of-range clamp to defaults.
	ctx3, _ := newCtx(t, "GET", "/x?page=0&page_size=0", nil)
	page, size = normalizePage(ctx3)
	assert.Equal(t, 1, page)
	assert.Equal(t, 20, size)

	ctx4, _ := newCtx(t, "GET", "/x?page=-5&page_size=101", nil)
	page, size = normalizePage(ctx4)
	assert.Equal(t, 1, page)
	assert.Equal(t, 20, size, "page_size>100 resets to default")

	// page_size boundary: 100 is allowed.
	ctx5, _ := newCtx(t, "GET", "/x?page_size=100", nil)
	_, size = normalizePage(ctx5)
	assert.Equal(t, 100, size)
}

func TestValidateCustomerBinding_InputGuards(t *testing.T) {
	// Non-positive ids rejected before any DB access.
	assert.ErrorIs(t, validateCustomerBinding(0, 5), errInvalidEmployeeId)
	assert.ErrorIs(t, validateCustomerBinding(5, 0), errInvalidCustomerId)
	assert.ErrorIs(t, validateCustomerBinding(7, 7), errCannotBindSelf)
}

func TestValidateCustomerBinding_NonEmployeeActor(t *testing.T) {
	requireDB(t)
	// A plain user acting as "employee" fails the IsEmployee check.
	actor := mkUser(t, nil)
	target := mkUser(t, nil)
	err := validateCustomerBinding(actor.Id, target.Id)
	assert.ErrorIs(t, err, errInvalidEmployee)
}

func TestEmployeeHandlers_RejectNonEmployee(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)

	handlers := map[string]func(*gin.Context){
		"EmployeeCreateCustomer": EmployeeCreateCustomer,
		"EmployeeListCustomers":  EmployeeListCustomers,
		"EmployeeGetCustomer":    EmployeeGetCustomer,
		"EmployeeUpdateCustomer": EmployeeUpdateCustomer,
		"EmployeeListQuotaLogs":  EmployeeListQuotaLogs,
	}
	for name, h := range handlers {
		ctx, rec := newCtx(t, "GET", "/x", nil)
		asUser(ctx, u.Id)
		h(ctx)
		resp := decodeResp(t, rec)
		assert.Falsef(t, resp.Success, "%s should reject non-employee", name)
	}
}

func TestAdminCreateCustomer_BadJSON(t *testing.T) {
	ctx, rec := newRawCtx(t, "POST", "/api/customer", "{bad")
	asAdmin(ctx, 1)
	AdminCreateCustomer(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

func TestAdminDeleteCustomer_InvalidId(t *testing.T) {
	// id <= 0 short-circuits to errInvalidCustomerId.
	ctx, rec := newCtx(t, "DELETE", "/api/customer/0", nil)
	ctx.Params = []gin.Param{{Key: "id", Value: "0"}}
	asAdmin(ctx, 1)
	AdminDeleteCustomer(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Message, errInvalidCustomerId.Error())
}

func TestErrSentinelsAreDistinct(t *testing.T) {
	// Guard against accidental aliasing that would break errors.Is dispatch.
	assert.False(t, errors.Is(errInvalidEmployee, errInvalidCustomer))
	assert.NotEqual(t, common.RoleAdminUser, common.RoleCommonUser)
}

package controller

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestEmployeeCreateCustomerValidatesNormalizedAccount(t *testing.T) {
	requireDB(t)
	employee, _ := mkEmployee(t, nil)
	for _, input := range []map[string]string{
		{"username": "   ", "password": "valid-password"},
		{"username": strings.Repeat("x", 45), "password": "valid-password"},
		{"username": "invalid-email", "password": "valid-password", "email": "not-an-email"},
	} {
		var before int64
		require.NoError(t, model.DB.Model(&model.User{}).Where("inviter_id = ?", employee.Id).Count(&before).Error)
		ctx, recorder := newCtx(t, "POST", "/api/user/employee/customers", input)
		asUser(ctx, employee.Id)
		EmployeeCreateCustomer(ctx)
		require.False(t, decodeResp(t, recorder).Success)
		var after int64
		require.NoError(t, model.DB.Model(&model.User{}).Where("inviter_id = ?", employee.Id).Count(&after).Error)
		require.Equal(t, before, after)
	}
}

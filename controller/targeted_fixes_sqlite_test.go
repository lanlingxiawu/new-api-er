package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestTargetedFixesSQLite(t *testing.T) {
	database := setupModelListControllerTestDB(t)
	require.NoError(t, database.AutoMigrate(&model.Option{}, &model.UserExtension{}, &model.CustomerProfile{}, &model.EmployeeProfile{}, &model.EmployeeTierLevel{}, &model.EmployeeTierLog{}, &model.EmployeeCommissionTier{}))
	t.Run("customer-validation", TestEmployeeCreateCustomerValidatesNormalizedAccount)
	t.Run("model-detail", TestRetrieveModelMatchesVisibleList)
}

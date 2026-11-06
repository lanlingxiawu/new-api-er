package router

import (
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestModelPricingRoutePermissions(t *testing.T) {
	require.Len(t, modelPricingRoutes, 5)
	issues := false
	for _, route := range modelPricingRoutes {
		if route.method == "GET" {
			require.Equal(t, authz.SystemSettingsView("billing.model-pricing"), route.permission)
		} else {
			require.Equal(t, authz.SystemSettingsEdit("billing.model-pricing"), route.permission)
		}
		if route.method == "GET" && route.path == "/issues" {
			issues = true
		}
	}
	require.True(t, issues, "GET /api/option/model_pricing/issues must stay registered")
}

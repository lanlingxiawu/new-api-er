package router

import (
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestModelPricingRoutePermissions(t *testing.T) {
	require.Len(t, modelPricingRoutes, 4)
	for _, route := range modelPricingRoutes {
		if route.method == "GET" {
			require.Equal(t, authz.SystemSettingsView("billing.model-pricing"), route.permission)
		} else {
			require.Equal(t, authz.SystemSettingsEdit("billing.model-pricing"), route.permission)
		}
	}
}

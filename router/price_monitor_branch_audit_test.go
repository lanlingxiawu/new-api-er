package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/service/authz"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Repricing writes model prices, so it needs the model-pricing settings edit permission, not
// just the monitor menu permission.
func TestPriceMonitorAuditApplyPriceNeedsModelPricingEdit(t *testing.T) {
	assertPriceMonitorRoutePermission(t, http.MethodPost, "/apply_price", authz.SystemSettingsEdit("billing.model-pricing"), controller.ApplyPriceMonitorPrice)
}

// An extra permission keyed by a path that no route uses would be silently skipped, leaving the
// route guarded by the menu permission alone.
func TestPriceMonitorAuditExtraPermissionsMatchRoutes(t *testing.T) {
	paths := make(map[string]struct{}, len(priceMonitorAdminRoutes))
	seen := make(map[string]struct{}, len(priceMonitorAdminRoutes))
	for _, route := range priceMonitorAdminRoutes {
		paths[route.path] = struct{}{}
		key := route.method + " " + route.path
		_, duplicate := seen[key]
		require.False(t, duplicate, "duplicate route %s", key)
		seen[key] = struct{}{}
		require.NotNil(t, route.handler, key)
		require.NotEmpty(t, route.permission, key)
	}
	for path, permissions := range priceMonitorExtraPermissions {
		_, exists := paths[path]
		assert.True(t, exists, "extra permissions for %s match no route", path)
		assert.NotEmpty(t, permissions, path)
	}
}

// Every admin route is registered behind AdminAuth: an anonymous request never reaches a
// handler, while the password-protected public query is the only anonymous entry.
func TestPriceMonitorAuditRegisteredRoutesRejectAnonymous(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	passthrough := func(c *gin.Context) { c.Next() }
	registerPriceMonitorRoutes(engine.Group("/api"), passthrough)

	registered := map[string]struct{}{}
	for _, info := range engine.Routes() {
		registered[info.Method+" "+info.Path] = struct{}{}
	}
	require.Contains(t, registered, http.MethodPost+" /api/price_monitor/public_query")
	for _, route := range priceMonitorAdminRoutes {
		key := route.method + " /api/price_monitor" + route.path
		require.Contains(t, registered, key)
	}
	require.Len(t, registered, len(priceMonitorAdminRoutes)+1, "no unlisted route is registered")

	for _, route := range priceMonitorAdminRoutes {
		path := "/api/price_monitor" + route.path
		if route.path == "/channels/:id/cost_ratio" {
			path = "/api/price_monitor/channels/1/cost_ratio"
		}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(route.method, path, nil))
		assert.NotEqual(t, http.StatusOK, recorder.Code, "%s %s must require authentication", route.method, path)
		assert.NotContains(t, recorder.Body.String(), `"success":true`, "%s %s", route.method, path)
	}
}

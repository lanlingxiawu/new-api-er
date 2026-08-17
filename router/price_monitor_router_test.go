package router

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPriceMonitorAdminRoutesUseUnifiedMenuPermissions(t *testing.T) {
	assertPriceMonitorRoutePermission(t, http.MethodGet, "/status", authz.AdminMenuPriceMonitorView, controller.GetPriceMonitorStatus)
	assertPriceMonitorRoutePermission(t, http.MethodGet, "/results", authz.AdminMenuPriceMonitorView, controller.GetPriceMonitorResults)
	assertPriceMonitorRoutePermission(t, http.MethodGet, "/inconsistencies", authz.AdminMenuPriceMonitorView, controller.GetPriceMonitorInconsistencies)
	assertPriceMonitorRoutePermission(t, http.MethodPut, "/settings", authz.AdminMenuPriceMonitorEdit, controller.UpdatePriceMonitorSettings)
	assertPriceMonitorRoutePermission(t, http.MethodPost, "/run", authz.AdminMenuPriceMonitorEdit, controller.RunPriceMonitor)
}

func assertPriceMonitorRoutePermission(t *testing.T, method string, path string, permission authz.Permission, handler any) {
	t.Helper()
	for _, route := range priceMonitorAdminRoutes {
		if route.method == method && route.path == path {
			assert.Equal(t, permission, route.permission)
			assert.Equal(t, reflect.ValueOf(handler).Pointer(), reflect.ValueOf(route.handler).Pointer())
			return
		}
	}
	require.FailNow(t, "price monitor route not found", "%s %s", method, path)
}

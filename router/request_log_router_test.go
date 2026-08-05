package router

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestLogReadRoutesRelyOnlyOnMenuPermission(t *testing.T) {
	assertRequestLogRoutePermission(t, http.MethodGet, "/", authz.Permission{}, controller.GetAllRequestLogs)
	assertRequestLogRoutePermission(t, http.MethodGet, "/:id", authz.Permission{}, controller.GetRequestLogDetail)
}

func TestRequestLogCleanupRouteRequiresRequestLogEditPermission(t *testing.T) {
	assertRequestLogRoutePermission(t, http.MethodDelete, "/", authz.SystemSettingsEdit("operations.request-log"), controller.DeleteHistoryRequestLogs)
}

func TestRequestLogRoutesRegisterWithoutConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/api")

	require.NotPanics(t, func() {
		registerRequestLogRoutes(api)
	})

	routes := make(map[string]struct{}, len(engine.Routes()))
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}
	// The bulk-clear route stays outside the admin_menu.request_logs-gated
	// group so its pre-existing operations.logs permission is unaffected.
	_, hasClearAll := routes[http.MethodDelete+" /api/request-log/all"]
	assert.True(t, hasClearAll, "DELETE /api/request-log/all must remain registered")
}

func assertRequestLogRoutePermission(t *testing.T, method string, path string, permission authz.Permission, handler any) {
	t.Helper()
	for _, route := range requestLogPermissionRoutes {
		if route.method == method && route.path == path {
			assert.Equal(t, permission, route.permission)
			assert.Equal(t, reflect.ValueOf(handler).Pointer(), reflect.ValueOf(route.handler).Pointer())
			return
		}
	}
	t.Fatalf("route %s %s not found", method, path)
}

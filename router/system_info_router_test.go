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

func TestSystemInfoReadRouteReliesOnlyOnMenuPermission(t *testing.T) {
	assertSystemInfoRoutePermission(t, http.MethodGet, "/instances", authz.Permission{}, controller.ListSystemInstances)
}

func TestSystemInfoDeleteRoutesRequireNodeControlEditPermission(t *testing.T) {
	assertSystemInfoRoutePermission(t, http.MethodDelete, "/stale-instances", authz.SystemSettingsEdit("operations.node-control"), controller.DeleteStaleSystemInstances)
	assertSystemInfoRoutePermission(t, http.MethodDelete, "/instances/:node_name", authz.SystemSettingsEdit("operations.node-control"), controller.DeleteStaleSystemInstance)
}

func TestSystemInfoRoutesRegisterWithoutConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/api")

	require.NotPanics(t, func() {
		registerSystemInfoRoutes(api)
	})

	routes := make(map[string]struct{}, len(engine.Routes()))
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}
	// pprof stays outside the admin_menu catalog and requires root regardless
	// of any administrator's system_info menu grant.
	_, hasPprofStatus := routes[http.MethodGet+" /api/system-info/pprof-status"]
	_, hasPprofProfile := routes[http.MethodGet+" /api/system-info/pprof/*name"]
	assert.True(t, hasPprofStatus, "GET /api/system-info/pprof-status must remain registered")
	assert.True(t, hasPprofProfile, "GET /api/system-info/pprof/*name must remain registered")
}

func assertSystemInfoRoutePermission(t *testing.T, method string, path string, permission authz.Permission, handler any) {
	t.Helper()
	for _, route := range systemInfoPermissionRoutes {
		if route.method == method && route.path == path {
			assert.Equal(t, permission, route.permission)
			assert.Equal(t, reflect.ValueOf(handler).Pointer(), reflect.ValueOf(route.handler).Pointer())
			return
		}
	}
	t.Fatalf("route %s %s not found", method, path)
}

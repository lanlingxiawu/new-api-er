package router

import (
	"net/http"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"

	"github.com/gin-gonic/gin"
)

func registerSystemInfoRoutes(apiRouter *gin.RouterGroup) {
	systemInfoRoute := apiRouter.Group("/system-info")
	systemInfoRoute.Use(middleware.AdminAuth(), middleware.RequirePermission(authz.AdminMenuSystemInfoView))

	for _, route := range systemInfoPermissionRoutes {
		handlers := make([]gin.HandlerFunc, 0, 2)
		if route.permission != (authz.Permission{}) {
			handlers = append(handlers, middleware.RequirePermission(route.permission))
		}
		handlers = append(handlers, route.handler)
		systemInfoRoute.Handle(route.method, route.path, handlers...)
	}

	// 性能剖析：开关状态 + 原始 profile 下载。
	// heap / goroutine dump 会带出内存中的凭据与用户数据，独立于 admin_menu.system_info
	// 权限目录之外，始终只对 root 开放（即使某个管理员已获得 system_info 菜单权限）。
	systemInfoRoute.GET("/pprof-status", middleware.RootAuth(), controller.GetPprofStatus)
	systemInfoRoute.GET("/pprof/*name", middleware.RootAuth(), controller.ServePprofProfile)
}

// systemInfoPermissionRoutes lists every route under /api/system-info beyond
// the group-level admin_menu.system_info:view gate (excluding the root-only
// pprof endpoints registered separately above). A zero-value permission means
// the menu view permission is the only requirement.
var systemInfoPermissionRoutes = []permissionRoute{
	{method: http.MethodGet, path: "/instances", handler: controller.ListSystemInstances},
	{method: http.MethodDelete, path: "/stale-instances", permission: authz.SystemSettingsEdit("operations.node-control"), handler: controller.DeleteStaleSystemInstances},
	{method: http.MethodDelete, path: "/instances/:node_name", permission: authz.SystemSettingsEdit("operations.node-control"), handler: controller.DeleteStaleSystemInstance},
}

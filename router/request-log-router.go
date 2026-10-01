package router

import (
	"net/http"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"

	"github.com/gin-gonic/gin"
)

// registerRequestLogRoutes 按权限声明注册请求日志路由。
// 参数 apiRouter：已建立的 API 路由分组，在该分组下挂载请求日志处理器。
func registerRequestLogRoutes(apiRouter *gin.RouterGroup) {
	requestLogRoute := apiRouter.Group("/request-log")
	requestLogRoute.Use(middleware.AdminAuth(), middleware.RequirePermission(authz.AdminMenuRequestLogsView))

	for _, route := range requestLogPermissionRoutes {
		handlers := make([]gin.HandlerFunc, 0, 2)
		if route.permission != (authz.Permission{}) {
			handlers = append(handlers, middleware.RequirePermission(route.permission))
		}
		handlers = append(handlers, route.handler)
		requestLogRoute.Handle(route.method, route.path, handlers...)
	}

	// 清空全部请求日志早于本次修订就已独立挂在 operations.logs 编辑权限下（日志维护分区），
	// 不嵌进上面按 admin_menu.request_logs 鉴权的分组，避免叠加菜单权限改变既有行为。
	apiRouter.DELETE("/request-log/all", middleware.AdminAuth(), middleware.RequirePermission(authz.SystemSettingsEdit("operations.logs")), controller.ClearAllRequestLogs)
}

// requestLogPermissionRoutes lists every route under /api/request-log beyond
// the group-level admin_menu.request_logs:view gate. A zero-value permission
// means the menu view permission is the only requirement. The full record
// (bodies and headers) needs its own view_detail grant; the handler masks
// credential headers for everyone but root.
var requestLogPermissionRoutes = []permissionRoute{
	{method: http.MethodGet, path: "/", handler: controller.GetAllRequestLogs},
	{method: http.MethodGet, path: "/:id", permission: authz.AdminMenuRequestLogsViewDetail, handler: controller.GetRequestLogDetail},
	{method: http.MethodDelete, path: "/", permission: authz.SystemSettingsEdit("operations.request-log"), handler: controller.DeleteHistoryRequestLogs},
}

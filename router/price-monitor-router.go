package router

import (
	"net/http"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
)

var priceMonitorAdminRoutes = []permissionRoute{
	{method: http.MethodGet, path: "/status", permission: authz.AdminMenuPriceMonitorView, handler: controller.GetPriceMonitorStatus},
	{method: http.MethodGet, path: "/results", permission: authz.AdminMenuPriceMonitorView, handler: controller.GetPriceMonitorResults},
	{method: http.MethodGet, path: "/inconsistencies", permission: authz.AdminMenuPriceMonitorView, handler: controller.GetPriceMonitorInconsistencies},
	{method: http.MethodPut, path: "/settings", permission: authz.AdminMenuPriceMonitorEdit, handler: controller.UpdatePriceMonitorSettings},
	{method: http.MethodPost, path: "/run", permission: authz.AdminMenuPriceMonitorEdit, handler: controller.RunPriceMonitor},
	// 改价与 rest_model_ratio 用同一个权限位：两者都是「修改模型价格」，不该有两套授权语义。
	{method: http.MethodPost, path: "/apply_price", permission: authz.SystemSettingsEdit("billing.model-pricing"), handler: controller.ApplyPriceMonitorPrice},
}

func registerPriceMonitorRoutes(apiRouter *gin.RouterGroup, anonymousRequestBodyLimit gin.HandlerFunc) {
	priceMonitorRoute := apiRouter.Group("/price_monitor")
	priceMonitorRoute.POST("/public_query", middleware.PublicQueryRateLimit(), middleware.DisableCache(), anonymousRequestBodyLimit, controller.PublicPriceMonitorQuery)

	priceMonitorAdminRoute := priceMonitorRoute.Group("")
	priceMonitorAdminRoute.Use(middleware.AdminAuth())
	for _, route := range priceMonitorAdminRoutes {
		handlers := []gin.HandlerFunc{middleware.RequirePermission(route.permission)}
		if route.path == "/status" || route.path == "/inconsistencies" {
			handlers = append(handlers, middleware.DisableCache())
		}
		handlers = append(handlers, route.handler)
		priceMonitorAdminRoute.Handle(route.method, route.path, handlers...)
	}
}

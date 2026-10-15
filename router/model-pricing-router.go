package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
)

var modelPricingRoutes = []permissionRoute{
	{method: "GET", path: "", permission: authz.SystemSettingsView("billing.model-pricing"), handler: controller.GetModelPricingConfig},
	{method: "POST", path: "/preview", permission: authz.SystemSettingsEdit("billing.model-pricing"), handler: controller.PreviewModelPricing},
	{method: "POST", path: "/convert", permission: authz.SystemSettingsEdit("billing.model-pricing"), handler: controller.PreviewModelPricingConversion},
	{method: "PATCH", path: "", permission: authz.SystemSettingsEdit("billing.model-pricing"), handler: controller.UpdateModelPricingConfig},
}

func registerModelPricingRoutes(apiRouter *gin.RouterGroup) {
	group := apiRouter.Group("/option/model_pricing", middleware.AdminAuth())
	for _, route := range modelPricingRoutes {
		group.Handle(route.method, route.path, middleware.RequirePermission(route.permission), route.handler)
	}
}

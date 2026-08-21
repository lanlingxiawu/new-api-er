package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"

	// Import oauth package to register providers via init()
	_ "github.com/QuantumNous/new-api/oauth"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

func SetApiRouter(router *gin.Engine) {
	// Token-validated file download: no gzip re-compression, no AdminAuth headers needed.
	router.GET("/dl/ledger/:token", controller.AdminDownloadLedgerExport)
	// Log export parts: same pattern, plus Range/resumable download via http.ServeContent.
	router.GET("/dl/log-export/:token", controller.DownloadLogExport)
	// Public read-only share page. Its data endpoint is protected by a rotating
	// password and an additional public-query rate limit.
	router.GET("/price_monitor/view", middleware.GlobalWebRateLimit(), controller.PriceMonitorView)

	apiRouter := router.Group("/api")
	apiRouter.Use(middleware.RouteTag("api"))
	apiRouter.Use(gzip.Gzip(gzip.DefaultCompression))
	apiRouter.Use(middleware.BodyStorageCleanup()) // Clean up request body storage
	apiRouter.Use(middleware.GlobalAPIRateLimit())
	anonymousRequestBodyLimit := middleware.AnonymousRequestBodyLimit()
	{
		apiRouter.GET("/setup", controller.GetSetup)
		apiRouter.POST("/setup", anonymousRequestBodyLimit, controller.PostSetup)
		apiRouter.GET("/status", controller.GetStatus)
		apiRouter.GET("/uptime/status", controller.GetUptimeKumaStatus)
		apiRouter.GET("/models", middleware.UserAuth(), controller.DashboardListModels)
		apiRouter.GET("/status/test", middleware.AdminAuth(), controller.TestStatus)
		apiRouter.GET("/notice", controller.GetNotice)
		apiRouter.GET("/user-agreement", controller.GetUserAgreement)
		apiRouter.GET("/privacy-policy", controller.GetPrivacyPolicy)
		apiRouter.GET("/about", controller.GetAbout)
		//apiRouter.GET("/midjourney", controller.GetMidjourney)
		apiRouter.GET("/home_page_content", controller.GetHomePageContent)
		apiRouter.GET("/pricing", middleware.HeaderNavModuleAuth("pricing"), controller.GetPricing)
		perfMetricsRoute := apiRouter.Group("/perf-metrics")
		perfMetricsRoute.Use(middleware.HeaderNavModulePublicOrUserAuth("pricing"))
		{
			perfMetricsRoute.GET("/summary", controller.GetPerfMetricsSummary)
			perfMetricsRoute.GET("", controller.GetPerfMetrics)
		}
		apiRouter.GET("/rankings", middleware.HeaderNavModuleAuth("rankings"), controller.GetRankings)
		apiRouter.GET("/verification", middleware.EmailVerificationRateLimit(), middleware.TurnstileCheck(), controller.SendEmailVerification)
		apiRouter.GET("/reset_password", middleware.CriticalRateLimit(), middleware.TurnstileCheck(), controller.SendPasswordResetEmail)
		apiRouter.POST("/user/reset", middleware.CriticalRateLimit(), anonymousRequestBodyLimit, controller.ResetPassword)
		// OAuth routes - specific routes must come before :provider wildcard
		apiRouter.POST("/oauth/state", middleware.CriticalRateLimit(), middleware.DisableCache(), middleware.TryUserAuth(), anonymousRequestBodyLimit, controller.GenerateOAuthCode)
		apiRouter.POST("/oauth/email/bind", middleware.UserAuth(), middleware.UserCriticalRateLimit(), controller.EmailBind)
		// Non-standard OAuth (WeChat, Telegram) - keep original routes
		apiRouter.GET("/oauth/wechat", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.WeChatAuth)
		apiRouter.POST("/oauth/wechat/bind", middleware.UserAuth(), middleware.UserCriticalRateLimit(), controller.WeChatBind)
		apiRouter.GET("/oauth/telegram/login", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.TelegramLogin)
		apiRouter.POST("/oauth/telegram/bind/start", middleware.UserAuth(), middleware.UserCriticalRateLimit(), middleware.DisableCache(), controller.TelegramBindStart)
		apiRouter.GET("/oauth/telegram/bind/:flow_token", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.TelegramBind)
		// Standard OAuth providers (GitHub, Discord, OIDC, LinuxDO) - unified route
		apiRouter.GET("/oauth/:provider", middleware.CriticalRateLimit(), middleware.DisableCache(), middleware.TryUserAuth(), controller.HandleOAuth)
		apiRouter.GET("/ratio_config", middleware.PublicQueryRateLimit(), controller.GetRatioConfig)

		// 汇率接口（公开，前端 5min 缓存）
		apiRouter.GET("/exchange-rate/usd-cny", controller.GetUSDCNYRate)

		apiRouter.POST("/stripe/webhook", anonymousRequestBodyLimit, controller.StripeWebhook)
		apiRouter.POST("/creem/webhook", anonymousRequestBodyLimit, controller.CreemWebhook)
		apiRouter.POST("/waffo/webhook", anonymousRequestBodyLimit, controller.WaffoWebhook)
		apiRouter.POST("/infini/webhook", anonymousRequestBodyLimit, controller.InfiniWebhook)
		// :env separates test vs prod URLs so the operator can register each
		// in Pancake's matching webhook slot; handler enforces env match.
		apiRouter.POST("/waffo-pancake/webhook/:env", anonymousRequestBodyLimit, controller.WaffoPancakeWebhook)
		apiRouter.POST("/alipay/notify", controller.AlipayNotify)
		apiRouter.GET("/alipay/notify", controller.AlipayNotify)
		apiRouter.POST("/wechat/notify", controller.WechatNotify)

		// Universal secure verification routes
		apiRouter.POST("/verify", middleware.UserAuth(), middleware.UserCriticalRateLimit(), middleware.DisableCache(), controller.UniversalVerify)

		userRoute := apiRouter.Group("/user")
		{
			userRoute.POST("/auth/refresh", middleware.SessionCookieOriginGuard(), middleware.SessionCriticalRateLimit(), middleware.DisableCache(), controller.RefreshAuth)
			userRoute.POST("/auth/logout", middleware.SessionCookieOriginGuard(), middleware.SessionCriticalRateLimit(), middleware.DisableCache(), controller.AuthLogout)
			userRoute.POST("/register", middleware.CriticalRateLimit(), anonymousRequestBodyLimit, middleware.TurnstileCheck(), controller.Register)
			userRoute.POST("/login", middleware.CriticalRateLimit(), middleware.DisableCache(), anonymousRequestBodyLimit, middleware.TurnstileCheck(), controller.Login)
			userRoute.POST("/login/2fa", middleware.CriticalRateLimit(), middleware.DisableCache(), anonymousRequestBodyLimit, controller.Verify2FALogin)
			userRoute.POST("/passkey/login/begin", middleware.CriticalRateLimit(), middleware.DisableCache(), anonymousRequestBodyLimit, controller.PasskeyLoginBegin)
			userRoute.POST("/passkey/login/finish", middleware.CriticalRateLimit(), middleware.DisableCache(), anonymousRequestBodyLimit, controller.PasskeyLoginFinish)
			//userRoute.POST("/tokenlog", middleware.CriticalRateLimit(), controller.TokenLog)
			userRoute.POST("/epay/notify", anonymousRequestBodyLimit, controller.EpayNotify)
			userRoute.GET("/epay/notify", controller.EpayNotify)
			userRoute.GET("/groups", controller.GetUserGroups)

			selfRoute := userRoute.Group("/")
			selfRoute.Use(middleware.UserAuth())
			{
				selfRoute.GET("/sessions", middleware.DisableCache(), controller.GetLoginSessions)
				selfRoute.DELETE("/sessions/:sid", middleware.DisableCache(), controller.DeleteLoginSession)
				selfRoute.POST("/sessions/revoke-others", middleware.DisableCache(), controller.RevokeOtherLoginSessions)
				selfRoute.GET("/self/groups", controller.GetUserGroups)
				selfRoute.GET("/self", controller.GetSelf)
				selfRoute.GET("/models", controller.GetUserModels)
				selfRoute.PUT("/self", middleware.UserCriticalRateLimit(), middleware.DisableCache(), controller.UpdateSelf)
				selfRoute.DELETE("/self", controller.DeleteSelf)
				selfRoute.GET("/token", middleware.DisableCache(), controller.GenerateAccessToken)
				selfRoute.GET("/passkey", controller.PasskeyStatus)
				selfRoute.POST("/passkey/register/begin", middleware.DisableCache(), controller.PasskeyRegisterBegin)
				selfRoute.POST("/passkey/register/finish", middleware.DisableCache(), controller.PasskeyRegisterFinish)
				selfRoute.POST("/passkey/verify/begin", middleware.DisableCache(), controller.PasskeyVerifyBegin)
				selfRoute.POST("/passkey/verify/finish", middleware.DisableCache(), controller.PasskeyVerifyFinish)
				selfRoute.DELETE("/passkey", middleware.DisableCache(), controller.PasskeyDelete)
				selfRoute.GET("/aff", controller.GetAffCode)
				selfRoute.GET("/topup/info", controller.GetTopUpInfo)
				selfRoute.GET("/topup/self", controller.GetUserTopUps)
				selfRoute.GET("/topup/self/export", controller.ExportUserTopUps)
				selfRoute.POST("/topup", middleware.UserCriticalRateLimit(), controller.TopUp)
				selfRoute.POST("/pay", middleware.UserCriticalRateLimit(), controller.RequestEpay)
				selfRoute.POST("/amount", controller.RequestAmount)
				selfRoute.POST("/stripe/pay", middleware.UserCriticalRateLimit(), controller.RequestStripePay)
				selfRoute.POST("/stripe/amount", controller.RequestStripeAmount)
				selfRoute.POST("/creem/pay", middleware.UserCriticalRateLimit(), controller.RequestCreemPay)
				selfRoute.POST("/waffo/amount", controller.RequestWaffoAmount)
				selfRoute.POST("/waffo/pay", middleware.UserCriticalRateLimit(), controller.RequestWaffoPay)
				selfRoute.POST("/waffo-pancake/amount", controller.RequestWaffoPancakeAmount)
				selfRoute.POST("/waffo-pancake/pay", middleware.UserCriticalRateLimit(), controller.RequestWaffoPancakePay)
				selfRoute.POST("/alipay/amount", controller.RequestAlipayAmount)
				selfRoute.POST("/alipay/pay", middleware.UserCriticalRateLimit(), controller.RequestAlipayPay)
				selfRoute.POST("/wechat/amount", controller.RequestWechatAmount)
				selfRoute.POST("/wechat/pay", middleware.UserCriticalRateLimit(), controller.RequestWechatPay)
				selfRoute.GET("/wechat/order", controller.RequestWechatOrderQuery)
				selfRoute.POST("/infini/amount", controller.RequestInfiniAmount)
				selfRoute.POST("/infini/pay", middleware.UserCriticalRateLimit(), controller.RequestInfiniPay)
				selfRoute.POST("/aff_transfer", controller.TransferAffQuota)
				selfRoute.PUT("/setting", controller.UpdateUserSetting)

				// 2FA routes
				selfRoute.GET("/2fa/status", controller.Get2FAStatus)
				selfRoute.POST("/2fa/setup", middleware.DisableCache(), controller.Setup2FA)
				selfRoute.POST("/2fa/enable", middleware.DisableCache(), controller.Enable2FA)
				selfRoute.POST("/2fa/disable", middleware.DisableCache(), controller.Disable2FA)
				selfRoute.POST("/2fa/backup_codes", middleware.DisableCache(), controller.RegenerateBackupCodes)

				// Check-in routes
				selfRoute.GET("/checkin", controller.GetCheckinStatus)
				selfRoute.POST("/checkin", middleware.TurnstileCheck(), controller.DoCheckin)

				// Custom OAuth bindings
				selfRoute.GET("/oauth/bindings", controller.GetUserOAuthBindings)
				selfRoute.DELETE("/oauth/bindings/:provider_id", controller.UnbindCustomOAuth)

				// Employee self-query routes
				selfRoute.GET("/employee/profile", controller.GetMyEmployeeProfile)
				selfRoute.GET("/employee/commission", controller.GetMyCommissionLogs)
				selfRoute.GET("/employee/commission/monthly", controller.GetMyCommissionResetPeriodStats)
				selfRoute.GET("/employee/commission/calendar", controller.GetMyCommissionCalendarStats)
				selfRoute.GET("/employee/commission/summary", controller.GetMyCommissionSummary)
			}

			// Employee customer management (employee self)
			employeeCustomerRoute := userRoute.Group("/employee/customers")
			employeeCustomerRoute.Use(middleware.UserAuth())
			{
				employeeCustomerRoute.GET("", controller.EmployeeListCustomers)
				employeeCustomerRoute.POST("", controller.EmployeeCreateCustomer)
				employeeCustomerRoute.GET("/quota-logs", controller.EmployeeListQuotaLogs)
				employeeCustomerRoute.GET("/:id", controller.EmployeeGetCustomer)
				employeeCustomerRoute.PUT("/:id", controller.EmployeeUpdateCustomer)
				// Employee user-profile editing is temporarily disabled.
				// employeeCustomerRoute.PUT("/:id/user", controller.EmployeeUpdateCustomerUser)
				// Employee quota adjustment is temporarily disabled.
				// employeeCustomerRoute.POST("/:id/quota", controller.EmployeeTransferQuota)
			}

			adminRoute := userRoute.Group("/")
			adminRoute.Use(middleware.AdminAuth(), middleware.RequirePermission(authz.AdminMenuUsersView))
			{
				adminRoute.GET("/", controller.GetAllUsers)
				adminRoute.GET("/topup", controller.GetAllTopUps)
				adminRoute.GET("/topup/export", controller.ExportAllTopUps)
				adminRoute.POST("/topup/platform-status", middleware.UserCriticalRateLimit(), controller.AdminQueryTopUpPlatformStatus)
				adminRoute.POST("/topup/complete", controller.AdminCompleteTopUp)
				adminRoute.GET("/search", controller.SearchUsers)
				adminRoute.GET("/:id/oauth/bindings", controller.GetUserOAuthBindingsByAdmin)
				adminRoute.DELETE("/:id/oauth/bindings/:provider_id", controller.UnbindCustomOAuthByAdmin)
				adminRoute.DELETE("/:id/bindings/:binding_type", controller.AdminClearUserBinding)
				adminRoute.GET("/:id", controller.GetUser)
				adminRoute.POST("/", controller.CreateUser)
				adminRoute.POST("/manage", controller.ManageUser)
				adminRoute.PUT("/", controller.UpdateUser)
				adminRoute.DELETE("/:id", controller.DeleteUser)
				adminRoute.DELETE("/:id/reset_passkey", controller.AdminResetPasskey)

				// Admin 2FA routes
				adminRoute.GET("/2fa/stats", controller.Admin2FAStats)
				adminRoute.DELETE("/:id/2fa", controller.AdminDisable2FA)
			}
		}

		// Subscription billing (plans, purchase, admin management)
		subscriptionRoute := apiRouter.Group("/subscription")
		subscriptionRoute.Use(middleware.UserAuth())
		{
			subscriptionRoute.GET("/plans", controller.GetSubscriptionPlans)
			subscriptionRoute.GET("/self", controller.GetSubscriptionSelf)
			subscriptionRoute.PUT("/self/preference", controller.UpdateSubscriptionPreference)
			subscriptionRoute.POST("/balance/pay", middleware.UserCriticalRateLimit(), controller.SubscriptionRequestBalancePay)
			subscriptionRoute.POST("/epay/pay", middleware.UserCriticalRateLimit(), controller.SubscriptionRequestEpay)
			subscriptionRoute.POST("/stripe/pay", middleware.UserCriticalRateLimit(), controller.SubscriptionRequestStripePay)
			subscriptionRoute.POST("/creem/pay", middleware.UserCriticalRateLimit(), controller.SubscriptionRequestCreemPay)
			subscriptionRoute.POST("/waffo-pancake/pay", middleware.UserCriticalRateLimit(), controller.SubscriptionRequestWaffoPancakePay)
		}
		subscriptionAdminRoute := apiRouter.Group("/subscription/admin")
		subscriptionAdminRoute.Use(middleware.AdminAuth(), middleware.RequirePermission(authz.AdminMenuSubscriptionsView))
		{
			subscriptionAdminRoute.GET("/plans", controller.AdminListSubscriptionPlans)
			subscriptionAdminRoute.POST("/plans", controller.AdminCreateSubscriptionPlan)
			subscriptionAdminRoute.PUT("/plans/:id", controller.AdminUpdateSubscriptionPlan)
			subscriptionAdminRoute.PATCH("/plans/:id", controller.AdminUpdateSubscriptionPlanStatus)
			subscriptionAdminRoute.POST("/bind", controller.AdminBindSubscription)
			subscriptionAdminRoute.POST("/plans/:id/subscriptions/reset", controller.AdminResetPlanSubscriptions)

			// User subscription management (admin)
			subscriptionAdminRoute.GET("/users/:id/subscriptions", controller.AdminListUserSubscriptions)
			subscriptionAdminRoute.POST("/users/:id/subscriptions", controller.AdminCreateUserSubscription)
			subscriptionAdminRoute.POST("/users/:id/subscriptions/reset", controller.AdminResetUserSubscriptionsByPlan)
			subscriptionAdminRoute.POST("/user_subscriptions/:id/invalidate", controller.AdminInvalidateUserSubscription)
			subscriptionAdminRoute.DELETE("/user_subscriptions/:id", controller.AdminDeleteUserSubscription)
		}

		// Employee management (admin)
		employeeAdminRoute := apiRouter.Group("/admin/employee")
		employeeAdminRoute.Use(middleware.AdminAuth())
		{
			employeeAdminRoute.GET("", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminListEmployees)
			employeeAdminRoute.POST("", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminCreateEmployee)
			employeeAdminRoute.PUT("/:id", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminUpdateEmployee)
			employeeAdminRoute.DELETE("/:id", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminDeleteEmployee)
			employeeAdminRoute.GET("/commission", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminListCommissionLogs)
			employeeAdminRoute.GET("/commission/channels", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminListCommissionChannelOptions)
			employeeAdminRoute.GET("/commission/monthly", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminListCommissionResetPeriodStats)
			employeeAdminRoute.GET("/commission/calendar", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminCommissionCalendarStats)
			employeeAdminRoute.GET("/commission/monthly-export", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminCommissionMonthlyExport)
			employeeAdminRoute.GET("/commission/summary", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminCommissionSummary)
			employeeAdminRoute.GET("/overview/channels", middleware.RequirePermission(authz.AdminMenuBusinessOverviewView), controller.AdminChannelProfitPage)
			employeeAdminRoute.GET("/overview", middleware.RequirePermission(authz.AdminMenuBusinessOverviewView), controller.AdminCommissionOverview)
			employeeAdminRoute.GET("/consumption-cost-ledger", middleware.RequirePermission(authz.AdminMenuBusinessOverviewView), controller.AdminListConsumptionCostLedger)
			employeeAdminRoute.GET("/consumption-cost-ledger/stats", middleware.RequirePermission(authz.AdminMenuBusinessOverviewView), controller.AdminGetConsumptionCostLedgerStats)
			employeeAdminRoute.POST("/consumption-cost-ledger/export", middleware.RequirePermission(authz.AdminMenuBusinessOverviewView), controller.AdminCreateLedgerExport)
			employeeAdminRoute.GET("/consumption-cost-ledger/export/:job_id", middleware.RequirePermission(authz.AdminMenuBusinessOverviewView), controller.AdminGetLedgerExport)
			employeeAdminRoute.GET("/consumption-cost-ledger/export/:job_id/download-url", middleware.RequirePermission(authz.AdminMenuBusinessOverviewView), controller.AdminGetLedgerExportDownloadURL)
			// Fallback log backfill
			employeeAdminRoute.GET("/consumption-cost-ledger/fallback/status", middleware.RequirePermission(authz.AdminMenuBusinessOverviewView), controller.AdminGetFallbackStatus)
			employeeAdminRoute.POST("/consumption-cost-ledger/fallback/backfill", middleware.RequirePermission(authz.AdminMenuBusinessOverviewView), controller.AdminTriggerBackfill)
			employeeAdminRoute.GET("/consumption-cost-ledger/fallback/backfill-result", middleware.RequirePermission(authz.AdminMenuBusinessOverviewView), controller.AdminGetBackfillResult)
			// 阶梯提成等级配置
			employeeAdminRoute.GET("/tiers", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminListTiers)
			employeeAdminRoute.POST("/tiers", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminCreateTier)
			employeeAdminRoute.PUT("/tiers/:id", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminUpdateTier)
			employeeAdminRoute.DELETE("/tiers/:id", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminDeleteTier)
			employeeAdminRoute.GET("/tiers/logs", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminListTierLogs)
			employeeAdminRoute.GET("/tiers/reset-config", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminGetTierResetConfig)
			employeeAdminRoute.POST("/tiers/reset-now", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminTriggerTierReset)
			employeeAdminRoute.POST("/tiers/switch-period", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminSwitchCommissionPeriod)
			employeeAdminRoute.POST("/:id/tier", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminSetEmployeeTier)
			employeeAdminRoute.POST("/:id/performance", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminAddEmployeePerformance)
			employeeAdminRoute.POST("/performance/:logId/revert", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminRevertPerformanceAdjustment)
			employeeAdminRoute.GET("/:id/customers", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminListEmployeeCustomers)
			employeeAdminRoute.POST("/:id/assign-customer", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminAssignCustomerToEmployee)
			employeeAdminRoute.DELETE("/:id/customer/:user_id", middleware.RequirePermission(authz.AdminMenuEmployeesView), controller.AdminUnassignCustomerFromEmployee)
		}

		systemAdminRoute := apiRouter.Group("/admin/system")
		systemAdminRoute.Use(middleware.AdminAuth())
		{
			systemAdminRoute.GET("/ledger-pipeline/status", middleware.RequirePermission(authz.SystemSettingsView("system-tuning.ledger-pipeline")), controller.AdminGetLedgerPipelineStatus)
			systemAdminRoute.GET("/relay-log-pipeline/status", middleware.RequirePermission(authz.SystemSettingsView("system-tuning.relay-log-pipeline")), controller.AdminGetRelayLogPipelineStatus)
			systemAdminRoute.POST("/relay-log-pipeline/replay", middleware.RequirePermission(authz.SystemSettingsEdit("system-tuning.relay-log-pipeline")), controller.AdminStartRelayLogFallbackReplay)
		}

		// Customer management (admin)
		customerAdminRoute := apiRouter.Group("/admin/customer")
		customerAdminRoute.Use(middleware.AdminAuth())
		{
			customerAdminRoute.GET("", controller.AdminListCustomers)
			customerAdminRoute.POST("", controller.AdminCreateCustomer)
			customerAdminRoute.GET("/quota-logs", controller.AdminListCustomerQuotaLogs)
			customerAdminRoute.GET("/:id", controller.AdminGetCustomer)
			customerAdminRoute.PUT("/:id", controller.AdminUpdateCustomer)
			customerAdminRoute.PUT("/:id/user", controller.AdminUpdateCustomerUser)
			customerAdminRoute.DELETE("/:id", controller.AdminDeleteCustomer)
		}

		// Channel cost config (admin)
		channelCostRoute := apiRouter.Group("/admin/channel/cost")
		channelCostRoute.Use(middleware.AdminAuth(), middleware.RequirePermission(authz.AdminMenuEmployeesView))
		{
			channelCostRoute.GET("", controller.AdminListChannelCosts)
			channelCostRoute.POST("", controller.AdminUpsertChannelCost)
			channelCostRoute.DELETE("/:channel_id", controller.AdminDeleteChannelCost)
		}

		// Subscription payment callbacks (no auth)
		apiRouter.POST("/subscription/epay/notify", anonymousRequestBodyLimit, controller.SubscriptionEpayNotify)
		apiRouter.GET("/subscription/epay/notify", controller.SubscriptionEpayNotify)
		apiRouter.GET("/subscription/epay/return", controller.SubscriptionEpayReturn)
		apiRouter.POST("/subscription/epay/return", anonymousRequestBodyLimit, controller.SubscriptionEpayReturn)
		// xiugai 添加号池节点功能
		nodePoolRoute := apiRouter.Group("/node-pool")
		nodePoolRoute.Use(middleware.RootAuth())
		{
			nodePoolRoute.GET("/nodes", controller.GetNodePoolNodes)
			nodePoolRoute.GET("/nodes/:node_name/accounts", controller.GetNodePoolAccounts)
			nodePoolRoute.DELETE("/nodes/:node_name", controller.DeleteNodePoolNode)
		}
		// end
		optionRoute := apiRouter.Group("/option")
		optionRoute.Use(middleware.AdminAuth())
		{
			optionRoute.GET("/", middleware.RequireSystemSettingsScope(authz.ActionView), controller.GetOptions)
			optionRoute.PUT("/", middleware.RequireSystemSettingsScope(authz.ActionEdit), controller.UpdateOption)
			optionRoute.PUT("/group", middleware.RequireSystemSettingsScope(authz.ActionEdit), controller.UpdateOptionGroup)
		}
		optionRootRoute := apiRouter.Group("/option")
		optionRootRoute.Use(middleware.AdminAuth())
		{
			optionRootRoute.GET("/db-pool/stats", middleware.RequirePermission(authz.SystemSettingsView("system-tuning.database-pool")), controller.GetDBPoolRuntimeStatus)
			optionRootRoute.GET("/business-stats-circuit-breaker/status", middleware.RequirePermission(authz.SystemSettingsView("system-tuning.settlement-guard")), controller.GetBusinessStatsCircuitBreakerStatus)
			optionRootRoute.POST("/payment_compliance", middleware.RequirePermission(authz.SystemSettingsEdit("billing.quota")), controller.ConfirmPaymentCompliance)
			optionRootRoute.GET("/channel_affinity_cache", middleware.RequirePermission(authz.SystemSettingsView("models.channel-affinity")), controller.GetChannelAffinityCacheStats)
			optionRootRoute.DELETE("/channel_affinity_cache", middleware.RequirePermission(authz.SystemSettingsEdit("models.channel-affinity")), controller.ClearChannelAffinityCache)
			optionRootRoute.POST("/rest_model_ratio", middleware.RequirePermission(authz.SystemSettingsEdit("billing.model-pricing")), controller.ResetModelRatio)
			optionRootRoute.GET("/waffo-pancake/catalog", middleware.RequirePermission(authz.SystemSettingsView("billing.payment")), controller.ListWaffoPancakeCatalog)
			optionRootRoute.POST("/waffo-pancake/pair", middleware.RequirePermission(authz.SystemSettingsEdit("billing.payment")), controller.CreateWaffoPancakePair)
			optionRootRoute.POST("/waffo-pancake/save", middleware.RequirePermission(authz.SystemSettingsEdit("billing.payment")), controller.SaveWaffoPancake)
			optionRootRoute.POST("/waffo-pancake/subscription-product", middleware.RequirePermission(authz.SystemSettingsEdit("billing.payment")), controller.CreateWaffoPancakeSubscriptionProduct)
			optionRootRoute.GET("/waffo-pancake/subscription-product-options", middleware.RequirePermission(authz.SystemSettingsView("billing.payment")), controller.ListWaffoPancakeSubscriptionProductOptions)
		}

		// Custom OAuth provider management.
		customOAuthRoute := apiRouter.Group("/custom-oauth-provider")
		customOAuthRoute.Use(middleware.AdminAuth())
		{
			customOAuthRoute.POST("/discovery", middleware.RequirePermission(authz.SystemSettingsEdit("auth.custom-oauth")), controller.FetchCustomOAuthDiscovery)
			customOAuthRoute.GET("/", middleware.RequirePermission(authz.SystemSettingsView("auth.custom-oauth")), controller.GetCustomOAuthProviders)
			customOAuthRoute.GET("/:id", middleware.RequirePermission(authz.SystemSettingsView("auth.custom-oauth")), controller.GetCustomOAuthProvider)
			customOAuthRoute.POST("/", middleware.RequirePermission(authz.SystemSettingsEdit("auth.custom-oauth")), controller.CreateCustomOAuthProvider)
			customOAuthRoute.PUT("/:id", middleware.RequirePermission(authz.SystemSettingsEdit("auth.custom-oauth")), controller.UpdateCustomOAuthProvider)
			customOAuthRoute.DELETE("/:id", middleware.RequirePermission(authz.SystemSettingsEdit("auth.custom-oauth")), controller.DeleteCustomOAuthProvider)
		}
		performanceRoute := apiRouter.Group("/performance")
		performanceRoute.Use(middleware.AdminAuth())
		{
			performanceRoute.GET("/stats", middleware.RequirePermission(authz.SystemSettingsView("operations.performance")), controller.GetPerformanceStats)
			performanceRoute.DELETE("/disk_cache", middleware.RequirePermission(authz.SystemSettingsEdit("operations.performance")), controller.ClearDiskCache)
			performanceRoute.POST("/reset_stats", middleware.RequirePermission(authz.SystemSettingsEdit("operations.performance")), controller.ResetPerformanceStats)
			performanceRoute.POST("/gc", middleware.RequirePermission(authz.SystemSettingsEdit("operations.performance")), controller.ForceGC)
			performanceRoute.GET("/logs", middleware.RequirePermission(authz.SystemSettingsView("operations.logs")), controller.GetLogFiles)
			performanceRoute.DELETE("/logs", middleware.RequirePermission(authz.SystemSettingsEdit("operations.logs")), controller.CleanupLogFiles)
		}
		ratioSyncRoute := apiRouter.Group("/ratio_sync")
		ratioSyncRoute.Use(middleware.AdminAuth())
		{
			ratioSyncRoute.GET("/channels", middleware.RequirePermission(authz.SystemSettingsView("billing.model-pricing")), controller.GetSyncableChannels)
			ratioSyncRoute.POST("/fetch", middleware.RequirePermission(authz.SystemSettingsEdit("billing.model-pricing")), controller.FetchUpstreamRatios)
		}
		registerPriceMonitorRoutes(apiRouter, anonymousRequestBodyLimit)
		registerChannelRoutes(apiRouter)
		registerAuthzRoutes(apiRouter)
		tokenRoute := apiRouter.Group("/token")
		tokenRoute.Use(middleware.UserAuth())
		{
			tokenRoute.GET("/", controller.GetAllTokens)
			tokenRoute.GET("/search", middleware.SearchRateLimit(), controller.SearchTokens)
			tokenRoute.GET("/auto-groups", controller.GetTokenAutoGroups)
			tokenRoute.GET("/:id", controller.GetToken)
			tokenRoute.POST("/:id/key", middleware.UserCriticalRateLimit(), middleware.DisableCache(), controller.GetTokenKey)
			tokenRoute.POST("/", controller.AddToken)
			tokenRoute.PUT("/", controller.UpdateToken)
			tokenRoute.DELETE("/:id", controller.DeleteToken)
			tokenRoute.POST("/batch", controller.DeleteTokenBatch)
			tokenRoute.POST("/batch/keys", middleware.UserCriticalRateLimit(), middleware.DisableCache(), controller.GetTokenKeysBatch)
		}

		// 分组路由
		groupRoute := apiRouter.Group("/group")
		{
			groupRoute.GET("/:group/models", controller.GetAvailableModelsByGroup) // 获取分组的可用模型列表
		}

		usageRoute := apiRouter.Group("/usage")
		usageRoute.Use(middleware.CORS(), middleware.PublicQueryRateLimit())
		{
			tokenUsageRoute := usageRoute.Group("/token")
			tokenUsageRoute.Use(middleware.TokenAuthReadOnly())
			{
				tokenUsageRoute.GET("/", controller.GetTokenUsage)
			}
		}

		redemptionRoute := apiRouter.Group("/redemption")
		redemptionRoute.Use(middleware.AdminAuth(), middleware.RequirePermission(authz.AdminMenuRedemptionCodesView))
		{
			redemptionRoute.GET("/", controller.GetAllRedemptions)
			redemptionRoute.GET("/search", controller.SearchRedemptions)
			redemptionRoute.GET("/:id", controller.GetRedemption)
			redemptionRoute.POST("/", controller.AddRedemption)
			redemptionRoute.PUT("/", controller.UpdateRedemption)
			redemptionRoute.DELETE("/invalid", controller.DeleteInvalidRedemption)
			redemptionRoute.DELETE("/:id", controller.DeleteRedemption)
		}
		logRoute := apiRouter.Group("/log")
		// 前端请求的是 /api/log（无尾斜杠）。只注册 "/" 会让 Gin 回 301 再重定向到
		// "/api/log/"，每次列表都白付一个 RTT。两条路径注册到同一 handler 消除重定向。
		logRoute.GET("", middleware.AdminAuth(), controller.GetAllLogs)
		logRoute.GET("/", middleware.AdminAuth(), controller.GetAllLogs)
		logRoute.GET("/stat", middleware.AdminAuth(), controller.GetLogsStat)
		logRoute.GET("/self/stat", middleware.UserAuth(), controller.GetLogsSelfStat)
		logRoute.GET("/employee/stat", middleware.UserAuth(), controller.GetEmployeeCustomerLogsStat)
		logRoute.GET("/channel_affinity_usage_cache", middleware.AdminAuth(), middleware.RequirePermission(authz.SystemSettingsView("models.channel-affinity")), controller.GetChannelAffinityUsageCacheStats)
		logRoute.GET("/search", middleware.AdminAuth(), controller.SearchAllLogs)
		logRoute.GET("/export", middleware.AdminAuth(), middleware.LogExportRateLimit(), controller.ExportAllLogs)
		logRoute.GET("/self", middleware.UserAuth(), controller.GetUserLogs)
		logRoute.GET("/self/export", middleware.UserAuth(), middleware.LogExportRateLimit(), controller.ExportUserLogs)
		logRoute.GET("/employee", middleware.UserAuth(), controller.GetEmployeeCustomerLogs)
		logRoute.GET("/self/search", middleware.UserAuth(), middleware.SearchRateLimit(), controller.SearchUserLogs)

		// 后台导出（管理员专属）：任务化、分片、断点续传。
		// 普通用户的自助导出仍走上面的 /log/self/export 同步路径。
		logExportRoute := apiRouter.Group("/log/export")
		logExportRoute.Use(middleware.AdminAuth())
		{
			logExportRoute.GET("/columns", controller.GetLogExportColumns)
			logExportRoute.GET("/estimate", controller.GetLogExportEstimate)
			logExportRoute.GET("/templates", controller.GetLogExportTemplates)
			logExportRoute.POST("/templates", controller.CreateLogExportTemplate)
			logExportRoute.PUT("/templates/:id", controller.UpdateLogExportTemplate)
			logExportRoute.DELETE("/templates/:id", controller.DeleteLogExportTemplate)
			logExportRoute.GET("/jobs", controller.GetLogExportJobs)
			logExportRoute.POST("/jobs", controller.CreateLogExportJob)
			logExportRoute.GET("/jobs/:job_id", controller.GetLogExportJob)
			logExportRoute.DELETE("/jobs/:job_id", controller.DeleteLogExportJob)
			logExportRoute.GET("/jobs/:job_id/download-url", controller.GetLogExportDownloadURL)
		}

		// 请求日志（下游请求体/请求头 与 返回头/返回体）：菜单可见性由
		// admin_menu.request_logs 控制，默认对普通管理员关闭，由 root 按人授予。
		registerRequestLogRoutes(apiRouter)
		systemTaskRoute := apiRouter.Group("/system-task")
		systemTaskRoute.Use(middleware.AdminAuth())
		{
			systemTaskRoute.POST("/log-cleanup", middleware.RequirePermission(authz.SystemSettingsEdit("operations.logs")), controller.CreateLogCleanupSystemTask)
			systemTaskRoute.GET("/list", middleware.RequirePermission(authz.SystemSettingsView("operations.logs")), controller.ListSystemTasks)
			systemTaskRoute.GET("/current", middleware.RequirePermission(authz.SystemSettingsView("operations.logs")), controller.GetCurrentSystemTask)
			systemTaskRoute.GET("/:task_id", middleware.RequirePermission(authz.SystemSettingsView("operations.logs")), controller.GetSystemTask)
		}
		// 系统信息：菜单可见性由 admin_menu.system_info 控制，默认对普通管理员关闭；
		// pprof 剖析数据仍始终只对 root 开放（见 registerSystemInfoRoutes）。
		registerSystemInfoRoutes(apiRouter)

		dataRoute := apiRouter.Group("/data")
		dataRoute.GET("/", middleware.AdminAuth(), controller.GetAllQuotaDates)
		dataRoute.GET("/users", middleware.AdminAuth(), controller.GetQuotaDatesByUser)
		dataRoute.GET("/self", middleware.UserAuth(), controller.GetUserQuotaDates)
		dataRoute.GET("/flow", middleware.AdminAuth(), controller.GetAllFlowQuotaDates)
		dataRoute.GET("/flow/self", middleware.UserAuth(), controller.GetUserFlowQuotaDates)

		logRoute.Use(middleware.CORS(), middleware.PublicQueryRateLimit())
		{
			logRoute.GET("/token", middleware.TokenAuthReadOnly(), controller.GetLogByKey)
		}
		groupAdminRoute := apiRouter.Group("/group")
		groupAdminRoute.Use(middleware.AdminAuth())
		{
			groupAdminRoute.GET("/", controller.GetGroups)
		}

		prefillGroupRoute := apiRouter.Group("/prefill_group")
		prefillGroupRoute.Use(middleware.AdminAuth(), middleware.RequirePermission(authz.AdminMenuModelsView))
		{
			prefillGroupRoute.GET("/", controller.GetPrefillGroups)
			prefillGroupRoute.POST("/", controller.CreatePrefillGroup)
			prefillGroupRoute.PUT("/", controller.UpdatePrefillGroup)
			prefillGroupRoute.DELETE("/:id", controller.DeletePrefillGroup)
		}

		mjRoute := apiRouter.Group("/mj")
		mjRoute.GET("/self", middleware.UserAuth(), controller.GetUserMidjourney)
		mjRoute.GET("/", middleware.AdminAuth(), controller.GetAllMidjourney)

		taskRoute := apiRouter.Group("/task")
		{
			taskRoute.GET("/self", middleware.UserAuth(), controller.GetUserTask)
			taskRoute.GET("/", middleware.AdminAuth(), controller.GetAllTask)
		}

		vendorRoute := apiRouter.Group("/vendors")
		vendorRoute.Use(middleware.AdminAuth(), middleware.RequirePermission(authz.AdminMenuModelsView))
		{
			vendorRoute.GET("/", controller.GetAllVendors)
			vendorRoute.GET("/search", controller.SearchVendors)
			vendorRoute.GET("/:id", controller.GetVendorMeta)
			vendorRoute.POST("/", controller.CreateVendorMeta)
			vendorRoute.PUT("/", controller.UpdateVendorMeta)
			vendorRoute.DELETE("/:id", controller.DeleteVendorMeta)
		}

		modelsRoute := apiRouter.Group("/models")
		modelsRoute.Use(middleware.AdminAuth(), middleware.RequirePermission(authz.AdminMenuModelsView))
		{
			modelsRoute.GET("/sync_upstream/preview", controller.SyncUpstreamPreview)
			modelsRoute.POST("/sync_upstream", controller.SyncUpstreamModels)
			modelsRoute.GET("/missing", controller.GetMissingModels)
			modelsRoute.GET("/", controller.GetAllModelsMeta)
			modelsRoute.GET("/search", controller.SearchModelsMeta)
			modelsRoute.GET("/:id", controller.GetModelMeta)
			modelsRoute.POST("/", controller.CreateModelMeta)
			modelsRoute.PUT("/", controller.UpdateModelMeta)
			modelsRoute.DELETE("/:id", controller.DeleteModelMeta)
		}

		// Deployments (model deployment management)
		deploymentSettingsRoute := apiRouter.Group("/deployments")
		deploymentSettingsRoute.Use(middleware.AdminAuth())
		{
			deploymentSettingsRoute.GET("/settings", middleware.RequirePermission(authz.SystemSettingsView("models.model-deployment")), controller.GetModelDeploymentSettings)
			deploymentSettingsRoute.POST("/settings/test-connection", middleware.RequirePermission(authz.SystemSettingsEdit("models.model-deployment")), controller.TestIoNetConnection)
		}
		deploymentsRoute := apiRouter.Group("/deployments")
		deploymentsRoute.Use(middleware.AdminAuth(), middleware.RequirePermission(authz.AdminMenuModelsView))
		{
			deploymentsRoute.GET("/", controller.GetAllDeployments)
			deploymentsRoute.GET("/search", controller.SearchDeployments)
			deploymentsRoute.POST("/test-connection", controller.TestIoNetConnection)
			deploymentsRoute.GET("/hardware-types", controller.GetHardwareTypes)
			deploymentsRoute.GET("/locations", controller.GetLocations)
			deploymentsRoute.GET("/available-replicas", controller.GetAvailableReplicas)
			deploymentsRoute.POST("/price-estimation", controller.GetPriceEstimation)
			deploymentsRoute.GET("/check-name", controller.CheckClusterNameAvailability)
			deploymentsRoute.POST("/", controller.CreateDeployment)
			deploymentsRoute.GET("/:id", controller.GetDeployment)
			deploymentsRoute.GET("/:id/logs", controller.GetDeploymentLogs)
			deploymentsRoute.GET("/:id/containers", controller.ListDeploymentContainers)
			deploymentsRoute.GET("/:id/containers/:container_id", controller.GetContainerDetails)
			deploymentsRoute.PUT("/:id", controller.UpdateDeployment)
			deploymentsRoute.PUT("/:id/name", controller.UpdateDeploymentName)
			deploymentsRoute.POST("/:id/extend", controller.ExtendDeployment)
			deploymentsRoute.DELETE("/:id", controller.DeleteDeployment)
		}

	}
}

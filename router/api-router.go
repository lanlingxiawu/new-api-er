package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	// Import oauth package to register providers via init()
	_ "github.com/QuantumNous/new-api/oauth"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

func SetApiRouter(router *gin.Engine) {
	// Token-validated file download: no gzip re-compression, no AdminAuth headers needed.
	router.GET("/dl/ledger/:token", controller.AdminDownloadLedgerExport)

	apiRouter := router.Group("/api")
	apiRouter.Use(middleware.RouteTag("api"))
	apiRouter.Use(gzip.Gzip(gzip.DefaultCompression))
	apiRouter.Use(middleware.BodyStorageCleanup()) // Clean up request body storage
	apiRouter.Use(middleware.GlobalAPIRateLimit())
	{
		apiRouter.GET("/setup", controller.GetSetup)
		apiRouter.POST("/setup", controller.PostSetup)
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
		apiRouter.POST("/user/reset", middleware.CriticalRateLimit(), controller.ResetPassword)
		// OAuth routes - specific routes must come before :provider wildcard
		apiRouter.GET("/oauth/state", middleware.CriticalRateLimit(), controller.GenerateOAuthCode)
		apiRouter.POST("/oauth/email/bind", middleware.CriticalRateLimit(), controller.EmailBind)
		// Non-standard OAuth (WeChat, Telegram) - keep original routes
		apiRouter.GET("/oauth/wechat", middleware.CriticalRateLimit(), controller.WeChatAuth)
		apiRouter.POST("/oauth/wechat/bind", middleware.CriticalRateLimit(), controller.WeChatBind)
		apiRouter.GET("/oauth/telegram/login", middleware.CriticalRateLimit(), controller.TelegramLogin)
		apiRouter.GET("/oauth/telegram/bind", middleware.CriticalRateLimit(), controller.TelegramBind)
		// Standard OAuth providers (GitHub, Discord, OIDC, LinuxDO) - unified route
		apiRouter.GET("/oauth/:provider", middleware.CriticalRateLimit(), controller.HandleOAuth)
		apiRouter.GET("/ratio_config", middleware.CriticalRateLimit(), controller.GetRatioConfig)

		// 汇率接口（公开，前端 5min 缓存）
		apiRouter.GET("/exchange-rate/usd-cny", controller.GetUSDCNYRate)

		apiRouter.POST("/stripe/webhook", controller.StripeWebhook)
		apiRouter.POST("/creem/webhook", controller.CreemWebhook)
		apiRouter.POST("/waffo/webhook", controller.WaffoWebhook)
		apiRouter.POST("/infini/webhook", controller.InfiniWebhook)
		// :env separates test vs prod URLs so the operator can register each
		// in Pancake's matching webhook slot; handler enforces env match.
		apiRouter.POST("/waffo-pancake/webhook/:env", controller.WaffoPancakeWebhook)
		apiRouter.POST("/alipay/notify", controller.AlipayNotify)
		apiRouter.GET("/alipay/notify", controller.AlipayNotify)
		apiRouter.POST("/wechat/notify", controller.WechatNotify)

		// Universal secure verification routes
		apiRouter.POST("/verify", middleware.UserAuth(), middleware.CriticalRateLimit(), controller.UniversalVerify)

		userRoute := apiRouter.Group("/user")
		{
			userRoute.POST("/register", middleware.CriticalRateLimit(), middleware.TurnstileCheck(), controller.Register)
			userRoute.POST("/login", middleware.CriticalRateLimit(), middleware.TurnstileCheck(), controller.Login)
			userRoute.POST("/login/2fa", middleware.CriticalRateLimit(), controller.Verify2FALogin)
			userRoute.POST("/passkey/login/begin", middleware.CriticalRateLimit(), controller.PasskeyLoginBegin)
			userRoute.POST("/passkey/login/finish", middleware.CriticalRateLimit(), controller.PasskeyLoginFinish)
			//userRoute.POST("/tokenlog", middleware.CriticalRateLimit(), controller.TokenLog)
			userRoute.GET("/logout", controller.Logout)
			userRoute.POST("/epay/notify", controller.EpayNotify)
			userRoute.GET("/epay/notify", controller.EpayNotify)
			userRoute.GET("/groups", controller.GetUserGroups)

			selfRoute := userRoute.Group("/")
			selfRoute.Use(middleware.UserAuth())
			{
				selfRoute.GET("/self/groups", controller.GetUserGroups)
				selfRoute.GET("/self", controller.GetSelf)
				selfRoute.GET("/models", controller.GetUserModels)
				selfRoute.PUT("/self", controller.UpdateSelf)
				selfRoute.DELETE("/self", controller.DeleteSelf)
				selfRoute.GET("/token", controller.GenerateAccessToken)
				selfRoute.GET("/passkey", controller.PasskeyStatus)
				selfRoute.POST("/passkey/register/begin", controller.PasskeyRegisterBegin)
				selfRoute.POST("/passkey/register/finish", controller.PasskeyRegisterFinish)
				selfRoute.POST("/passkey/verify/begin", controller.PasskeyVerifyBegin)
				selfRoute.POST("/passkey/verify/finish", controller.PasskeyVerifyFinish)
				selfRoute.DELETE("/passkey", controller.PasskeyDelete)
				selfRoute.GET("/aff", controller.GetAffCode)
				selfRoute.GET("/topup/info", controller.GetTopUpInfo)
				selfRoute.GET("/topup/self", controller.GetUserTopUps)
				selfRoute.GET("/topup/self/export", controller.ExportUserTopUps)
				selfRoute.POST("/topup", middleware.CriticalRateLimit(), controller.TopUp)
				selfRoute.POST("/pay", middleware.CriticalRateLimit(), controller.RequestEpay)
				selfRoute.POST("/amount", controller.RequestAmount)
				selfRoute.POST("/stripe/pay", middleware.CriticalRateLimit(), controller.RequestStripePay)
				selfRoute.POST("/stripe/amount", controller.RequestStripeAmount)
				selfRoute.POST("/creem/pay", middleware.CriticalRateLimit(), controller.RequestCreemPay)
				selfRoute.POST("/waffo/amount", controller.RequestWaffoAmount)
				selfRoute.POST("/waffo/pay", middleware.CriticalRateLimit(), controller.RequestWaffoPay)
				selfRoute.POST("/waffo-pancake/amount", controller.RequestWaffoPancakeAmount)
				selfRoute.POST("/waffo-pancake/pay", middleware.CriticalRateLimit(), controller.RequestWaffoPancakePay)
				selfRoute.POST("/alipay/amount", controller.RequestAlipayAmount)
				selfRoute.POST("/alipay/pay", middleware.CriticalRateLimit(), controller.RequestAlipayPay)
				selfRoute.POST("/wechat/amount", controller.RequestWechatAmount)
				selfRoute.POST("/wechat/pay", middleware.CriticalRateLimit(), controller.RequestWechatPay)
				selfRoute.GET("/wechat/order", controller.RequestWechatOrderQuery)
				selfRoute.POST("/infini/amount", controller.RequestInfiniAmount)
				selfRoute.POST("/infini/pay", middleware.CriticalRateLimit(), controller.RequestInfiniPay)
				selfRoute.POST("/aff_transfer", controller.TransferAffQuota)
				selfRoute.PUT("/setting", controller.UpdateUserSetting)

				// 2FA routes
				selfRoute.GET("/2fa/status", controller.Get2FAStatus)
				selfRoute.POST("/2fa/setup", controller.Setup2FA)
				selfRoute.POST("/2fa/enable", controller.Enable2FA)
				selfRoute.POST("/2fa/disable", controller.Disable2FA)
				selfRoute.POST("/2fa/backup_codes", controller.RegenerateBackupCodes)

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
			adminRoute.Use(middleware.AdminAuth())
			{
				adminRoute.GET("/", controller.GetAllUsers)
				adminRoute.GET("/topup", controller.GetAllTopUps)
				adminRoute.GET("/topup/export", controller.ExportAllTopUps)
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
			subscriptionRoute.POST("/balance/pay", middleware.CriticalRateLimit(), controller.SubscriptionRequestBalancePay)
			subscriptionRoute.POST("/epay/pay", middleware.CriticalRateLimit(), controller.SubscriptionRequestEpay)
			subscriptionRoute.POST("/stripe/pay", middleware.CriticalRateLimit(), controller.SubscriptionRequestStripePay)
			subscriptionRoute.POST("/creem/pay", middleware.CriticalRateLimit(), controller.SubscriptionRequestCreemPay)
			subscriptionRoute.POST("/waffo-pancake/pay", middleware.CriticalRateLimit(), controller.SubscriptionRequestWaffoPancakePay)
		}
		subscriptionAdminRoute := apiRouter.Group("/subscription/admin")
		subscriptionAdminRoute.Use(middleware.AdminAuth())
		{
			subscriptionAdminRoute.GET("/plans", controller.AdminListSubscriptionPlans)
			subscriptionAdminRoute.POST("/plans", controller.AdminCreateSubscriptionPlan)
			subscriptionAdminRoute.PUT("/plans/:id", controller.AdminUpdateSubscriptionPlan)
			subscriptionAdminRoute.PATCH("/plans/:id", controller.AdminUpdateSubscriptionPlanStatus)
			subscriptionAdminRoute.POST("/bind", controller.AdminBindSubscription)

			// User subscription management (admin)
			subscriptionAdminRoute.GET("/users/:id/subscriptions", controller.AdminListUserSubscriptions)
			subscriptionAdminRoute.POST("/users/:id/subscriptions", controller.AdminCreateUserSubscription)
			subscriptionAdminRoute.POST("/user_subscriptions/:id/invalidate", controller.AdminInvalidateUserSubscription)
			subscriptionAdminRoute.DELETE("/user_subscriptions/:id", controller.AdminDeleteUserSubscription)
		}

		// Employee management (admin)
		employeeAdminRoute := apiRouter.Group("/admin/employee")
		employeeAdminRoute.Use(middleware.AdminAuth())
		{
			employeeAdminRoute.GET("", controller.AdminListEmployees)
			employeeAdminRoute.POST("", controller.AdminCreateEmployee)
			employeeAdminRoute.PUT("/:id", controller.AdminUpdateEmployee)
			employeeAdminRoute.DELETE("/:id", controller.AdminDeleteEmployee)
			employeeAdminRoute.GET("/commission", controller.AdminListCommissionLogs)
			employeeAdminRoute.GET("/commission/channels", controller.AdminListCommissionChannelOptions)
			employeeAdminRoute.GET("/commission/monthly", controller.AdminListCommissionResetPeriodStats)
			employeeAdminRoute.GET("/commission/calendar", controller.AdminCommissionCalendarStats)
			employeeAdminRoute.GET("/commission/summary", controller.AdminCommissionSummary)
			employeeAdminRoute.GET("/overview/channels", controller.AdminChannelProfitPage)
			employeeAdminRoute.GET("/overview", controller.AdminCommissionOverview)
			employeeAdminRoute.GET("/consumption-cost-ledger", controller.AdminListConsumptionCostLedger)
			employeeAdminRoute.GET("/consumption-cost-ledger/stats", controller.AdminGetConsumptionCostLedgerStats)
			employeeAdminRoute.POST("/consumption-cost-ledger/export", controller.AdminCreateLedgerExport)
			employeeAdminRoute.GET("/consumption-cost-ledger/export/:job_id", controller.AdminGetLedgerExport)
			employeeAdminRoute.GET("/consumption-cost-ledger/export/:job_id/download-url", controller.AdminGetLedgerExportDownloadURL)
			// Fallback log backfill
			employeeAdminRoute.GET("/consumption-cost-ledger/fallback/status", controller.AdminGetFallbackStatus)
			employeeAdminRoute.POST("/consumption-cost-ledger/fallback/backfill", controller.AdminTriggerBackfill)
			employeeAdminRoute.GET("/consumption-cost-ledger/fallback/backfill-result", controller.AdminGetBackfillResult)
			// 阶梯提成等级配置
			employeeAdminRoute.GET("/tiers", controller.AdminListTiers)
			employeeAdminRoute.POST("/tiers", controller.AdminCreateTier)
			employeeAdminRoute.PUT("/tiers/:id", controller.AdminUpdateTier)
			employeeAdminRoute.DELETE("/tiers/:id", controller.AdminDeleteTier)
			employeeAdminRoute.GET("/tiers/logs", controller.AdminListTierLogs)
			employeeAdminRoute.GET("/tiers/reset-config", controller.AdminGetTierResetConfig)
			employeeAdminRoute.POST("/tiers/reset-now", controller.AdminTriggerTierReset)
			employeeAdminRoute.POST("/:id/tier", controller.AdminSetEmployeeTier)
			employeeAdminRoute.GET("/:id/customers", controller.AdminListEmployeeCustomers)
			employeeAdminRoute.POST("/:id/assign-customer", controller.AdminAssignCustomerToEmployee)
			employeeAdminRoute.DELETE("/:id/customer/:user_id", controller.AdminUnassignCustomerFromEmployee)
		}

		systemAdminRoute := apiRouter.Group("/admin/system")
		systemAdminRoute.Use(middleware.AdminAuth())
		{
			systemAdminRoute.GET("/ledger-pipeline/status", controller.AdminGetLedgerPipelineStatus)
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
		channelCostRoute.Use(middleware.AdminAuth())
		{
			channelCostRoute.GET("", controller.AdminListChannelCosts)
			channelCostRoute.POST("", controller.AdminUpsertChannelCost)
			channelCostRoute.DELETE("/:channel_id", controller.AdminDeleteChannelCost)
		}

		// Subscription payment callbacks (no auth)
		apiRouter.POST("/subscription/epay/notify", controller.SubscriptionEpayNotify)
		apiRouter.GET("/subscription/epay/notify", controller.SubscriptionEpayNotify)
		apiRouter.GET("/subscription/epay/return", controller.SubscriptionEpayReturn)
		apiRouter.POST("/subscription/epay/return", controller.SubscriptionEpayReturn)
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
		optionRoute.Use(middleware.RootAuth())
		{
			optionRoute.GET("/", controller.GetOptions)
			optionRoute.PUT("/", controller.UpdateOption)
			optionRoute.GET("/business-stats-circuit-breaker/status", controller.GetBusinessStatsCircuitBreakerStatus)
			optionRoute.POST("/payment_compliance", controller.ConfirmPaymentCompliance)
			optionRoute.GET("/channel_affinity_cache", controller.GetChannelAffinityCacheStats)
			optionRoute.DELETE("/channel_affinity_cache", controller.ClearChannelAffinityCache)
			optionRoute.POST("/rest_model_ratio", controller.ResetModelRatio)
			optionRoute.POST("/migrate_console_setting", controller.MigrateConsoleSetting) // Legacy key for migration checks; remove in a later version.
			optionRoute.POST("/waffo-pancake/catalog", controller.ListWaffoPancakeCatalog)
			optionRoute.POST("/waffo-pancake/pair", controller.CreateWaffoPancakePair)
			optionRoute.POST("/waffo-pancake/save", controller.SaveWaffoPancake)
			optionRoute.POST("/waffo-pancake/subscription-product", controller.CreateWaffoPancakeSubscriptionProduct)
			optionRoute.POST("/waffo-pancake/subscription-product-options", controller.ListWaffoPancakeSubscriptionProductOptions)
		}

		// Custom OAuth provider management (root only)
		customOAuthRoute := apiRouter.Group("/custom-oauth-provider")
		customOAuthRoute.Use(middleware.RootAuth())
		{
			customOAuthRoute.POST("/discovery", controller.FetchCustomOAuthDiscovery)
			customOAuthRoute.GET("/", controller.GetCustomOAuthProviders)
			customOAuthRoute.GET("/:id", controller.GetCustomOAuthProvider)
			customOAuthRoute.POST("/", controller.CreateCustomOAuthProvider)
			customOAuthRoute.PUT("/:id", controller.UpdateCustomOAuthProvider)
			customOAuthRoute.DELETE("/:id", controller.DeleteCustomOAuthProvider)
		}
		performanceRoute := apiRouter.Group("/performance")
		performanceRoute.Use(middleware.RootAuth())
		{
			performanceRoute.GET("/stats", controller.GetPerformanceStats)
			performanceRoute.DELETE("/disk_cache", controller.ClearDiskCache)
			performanceRoute.POST("/reset_stats", controller.ResetPerformanceStats)
			performanceRoute.POST("/gc", controller.ForceGC)
			performanceRoute.GET("/logs", controller.GetLogFiles)
			performanceRoute.DELETE("/logs", controller.CleanupLogFiles)
		}
		ratioSyncRoute := apiRouter.Group("/ratio_sync")
		ratioSyncRoute.Use(middleware.RootAuth())
		{
			ratioSyncRoute.GET("/channels", controller.GetSyncableChannels)
			ratioSyncRoute.POST("/fetch", controller.FetchUpstreamRatios)
		}
		channelRoute := apiRouter.Group("/channel")
		channelRoute.Use(middleware.AdminAuth())
		{
			channelRoute.GET("/", controller.GetAllChannels)
			channelRoute.GET("/search", controller.SearchChannels)
			channelRoute.GET("/models", controller.ChannelListModels)
			channelRoute.GET("/models_enabled", controller.EnabledListModels)
			channelRoute.GET("/:id", controller.GetChannel)
			channelRoute.POST("/:id/key", middleware.RootAuth(), middleware.CriticalRateLimit(), middleware.DisableCache(), middleware.SecureVerificationRequired(), controller.GetChannelKey)
			channelRoute.GET("/test", controller.TestAllChannels)
			channelRoute.GET("/test/:id", controller.TestChannel)
			channelRoute.GET("/update_balance", controller.UpdateAllChannelsBalance)
			channelRoute.GET("/update_balance/:id", controller.UpdateChannelBalance)
			channelRoute.POST("/:id/account-balance", controller.UpdateChannelAccountBalance)
			channelRoute.POST("/", middleware.RootAuth(), controller.AddChannel)
			channelRoute.PUT("/", middleware.RootAuth(), controller.UpdateChannel)
			channelRoute.DELETE("/disabled", middleware.RootAuth(), controller.DeleteDisabledChannel)
			channelRoute.POST("/tag/disabled", middleware.RootAuth(), controller.DisableTagChannels)
			channelRoute.POST("/tag/enabled", middleware.RootAuth(), controller.EnableTagChannels)
			channelRoute.PUT("/tag", middleware.RootAuth(), controller.EditTagChannels)
			channelRoute.DELETE("/:id", middleware.RootAuth(), controller.DeleteChannel)
			channelRoute.POST("/batch", middleware.RootAuth(), controller.DeleteChannelBatch)
			channelRoute.POST("/fix", middleware.RootAuth(), controller.FixChannelsAbilities)
			channelRoute.GET("/fetch_models/:id", controller.FetchUpstreamModels)
			channelRoute.POST("/fetch_models", middleware.RootAuth(), controller.FetchModels)
			channelRoute.POST("/codex/oauth/start", middleware.RootAuth(), controller.StartCodexOAuth)
			channelRoute.POST("/codex/oauth/complete", middleware.RootAuth(), controller.CompleteCodexOAuth)
			channelRoute.POST("/:id/codex/oauth/start", middleware.RootAuth(), controller.StartCodexOAuthForChannel)
			channelRoute.POST("/:id/codex/oauth/complete", middleware.RootAuth(), controller.CompleteCodexOAuthForChannel)
			channelRoute.POST("/:id/codex/refresh", middleware.RootAuth(), controller.RefreshCodexChannelCredential)
			channelRoute.GET("/:id/codex/usage", controller.GetCodexChannelUsage)
			channelRoute.POST("/ollama/pull", middleware.RootAuth(), controller.OllamaPullModel)
			channelRoute.POST("/ollama/pull/stream", middleware.RootAuth(), controller.OllamaPullModelStream)
			channelRoute.DELETE("/ollama/delete", middleware.RootAuth(), controller.OllamaDeleteModel)
			channelRoute.GET("/ollama/version/:id", controller.OllamaVersion)
			channelRoute.POST("/batch/tag", middleware.RootAuth(), controller.BatchSetChannelTag)
			channelRoute.GET("/tag/models", controller.GetTagModels)
			channelRoute.POST("/copy/:id", middleware.RootAuth(), controller.CopyChannel)
			channelRoute.POST("/multi_key/manage", middleware.RootAuth(), controller.ManageMultiKeys)
			channelRoute.POST("/upstream_updates/apply", middleware.RootAuth(), controller.ApplyChannelUpstreamModelUpdates)
			channelRoute.POST("/upstream_updates/apply_all", middleware.RootAuth(), controller.ApplyAllChannelUpstreamModelUpdates)
			channelRoute.POST("/upstream_updates/detect", controller.DetectChannelUpstreamModelUpdates)
			channelRoute.POST("/upstream_updates/detect_all", controller.DetectAllChannelUpstreamModelUpdates)
		}
		tokenRoute := apiRouter.Group("/token")
		tokenRoute.Use(middleware.UserAuth())
		{
			tokenRoute.GET("/", controller.GetAllTokens)
			tokenRoute.GET("/search", middleware.SearchRateLimit(), controller.SearchTokens)
			tokenRoute.GET("/:id", controller.GetToken)
			tokenRoute.POST("/:id/key", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.GetTokenKey)
			tokenRoute.POST("/", controller.AddToken)
			tokenRoute.PUT("/", controller.UpdateToken)
			tokenRoute.DELETE("/:id", controller.DeleteToken)
			tokenRoute.POST("/batch", controller.DeleteTokenBatch)
			tokenRoute.POST("/batch/keys", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.GetTokenKeysBatch)
		}

		// 分组路由
		groupRoute := apiRouter.Group("/group")
		{
			groupRoute.GET("/:group/models", controller.GetAvailableModelsByGroup) // 获取分组的可用模型列表
		}

		usageRoute := apiRouter.Group("/usage")
		usageRoute.Use(middleware.CORS(), middleware.CriticalRateLimit())
		{
			tokenUsageRoute := usageRoute.Group("/token")
			tokenUsageRoute.Use(middleware.TokenAuthReadOnly())
			{
				tokenUsageRoute.GET("/", controller.GetTokenUsage)
			}
		}

		redemptionRoute := apiRouter.Group("/redemption")
		redemptionRoute.Use(middleware.AdminAuth())
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
		logRoute.GET("/", middleware.AdminAuth(), controller.GetAllLogs)
		logRoute.DELETE("/", middleware.AdminAuth(), controller.DeleteHistoryLogs)
		logRoute.GET("/stat", middleware.AdminAuth(), controller.GetLogsStat)
		logRoute.GET("/self/stat", middleware.UserAuth(), controller.GetLogsSelfStat)
		logRoute.GET("/employee/stat", middleware.UserAuth(), controller.GetEmployeeCustomerLogsStat)
		logRoute.GET("/channel_affinity_usage_cache", middleware.AdminAuth(), controller.GetChannelAffinityUsageCacheStats)
		logRoute.GET("/search", middleware.AdminAuth(), controller.SearchAllLogs)
		logRoute.GET("/export", middleware.AdminAuth(), middleware.LogExportRateLimit(), controller.ExportAllLogs)
		logRoute.GET("/self", middleware.UserAuth(), controller.GetUserLogs)
		logRoute.GET("/self/export", middleware.UserAuth(), middleware.LogExportRateLimit(), controller.ExportUserLogs)
		logRoute.GET("/employee", middleware.UserAuth(), controller.GetEmployeeCustomerLogs)
		logRoute.GET("/self/search", middleware.UserAuth(), middleware.SearchRateLimit(), controller.SearchUserLogs)

		// 请求日志（下游请求体/请求头 与 返回头/返回体），仅超级管理员可查看
		requestLogRoute := apiRouter.Group("/request-log")
		requestLogRoute.Use(middleware.RootAuth())
		{
			requestLogRoute.GET("/", controller.GetAllRequestLogs)
			requestLogRoute.GET("/:id", controller.GetRequestLogDetail)
			requestLogRoute.DELETE("/", controller.DeleteHistoryRequestLogs)
			requestLogRoute.DELETE("/all", controller.ClearAllRequestLogs)
		}

		dataRoute := apiRouter.Group("/data")
		dataRoute.GET("/", middleware.AdminAuth(), controller.GetAllQuotaDates)
		dataRoute.GET("/users", middleware.AdminAuth(), controller.GetQuotaDatesByUser)
		dataRoute.GET("/self", middleware.UserAuth(), controller.GetUserQuotaDates)

		logRoute.Use(middleware.CORS(), middleware.CriticalRateLimit())
		{
			logRoute.GET("/token", middleware.TokenAuthReadOnly(), controller.GetLogByKey)
		}
		groupAdminRoute := apiRouter.Group("/group")
		groupAdminRoute.Use(middleware.AdminAuth())
		{
			groupAdminRoute.GET("/", controller.GetGroups)
		}

		prefillGroupRoute := apiRouter.Group("/prefill_group")
		prefillGroupRoute.Use(middleware.AdminAuth())
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
		vendorRoute.Use(middleware.AdminAuth())
		{
			vendorRoute.GET("/", controller.GetAllVendors)
			vendorRoute.GET("/search", controller.SearchVendors)
			vendorRoute.GET("/:id", controller.GetVendorMeta)
			vendorRoute.POST("/", controller.CreateVendorMeta)
			vendorRoute.PUT("/", controller.UpdateVendorMeta)
			vendorRoute.DELETE("/:id", controller.DeleteVendorMeta)
		}

		modelsRoute := apiRouter.Group("/models")
		modelsRoute.Use(middleware.AdminAuth())
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
		deploymentsRoute := apiRouter.Group("/deployments")
		deploymentsRoute.Use(middleware.AdminAuth())
		{
			deploymentsRoute.GET("/settings", controller.GetModelDeploymentSettings)
			deploymentsRoute.POST("/settings/test-connection", controller.TestIoNetConnection)
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

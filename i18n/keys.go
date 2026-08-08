package i18n

// Message keys for i18n translations
// Use these constants instead of hardcoded strings

// Common error messages
const (
	MsgInvalidParams     = "common.invalid_params"
	MsgDatabaseError     = "common.database_error"
	MsgRetryLater        = "common.retry_later"
	MsgGenerateFailed    = "common.generate_failed"
	MsgNotFound          = "common.not_found"
	MsgUnauthorized      = "common.unauthorized"
	MsgForbidden         = "common.forbidden"
	MsgInvalidId         = "common.invalid_id"
	MsgIdEmpty           = "common.id_empty"
	MsgFeatureDisabled   = "common.feature_disabled"
	MsgOperationSuccess  = "common.operation_success"
	MsgOperationFailed   = "common.operation_failed"
	MsgUpdateSuccess     = "common.update_success"
	MsgUpdateFailed      = "common.update_failed"
	MsgCreateSuccess     = "common.create_success"
	MsgCreateFailed      = "common.create_failed"
	MsgDeleteSuccess     = "common.delete_success"
	MsgDeleteFailed      = "common.delete_failed"
	MsgAlreadyExists     = "common.already_exists"
	MsgNameCannotBeEmpty = "common.name_cannot_be_empty"
	MsgBatchTooMany      = "common.batch_too_many"
)

const MsgRelayTimeout = "relay.timeout"

// Auth middleware messages
const (
	MsgAuthNotLoggedIn           = "auth.not_logged_in"
	MsgAuthAccessTokenInvalid    = "auth.access_token_invalid"
	MsgAuthUserInfoInvalid       = "auth.user_info_invalid"
	MsgAuthUserIdNotProvided     = "auth.user_id_not_provided"
	MsgAuthUserIdFormatError     = "auth.user_id_format_error"
	MsgAuthUserIdMismatch        = "auth.user_id_mismatch"
	MsgAuthUserBanned            = "auth.user_banned"
	MsgAuthInsufficientPrivilege = "auth.insufficient_privilege"
)

// Token related messages
const (
	MsgTokenNameTooLong          = "token.name_too_long"
	MsgTokenQuotaNegative        = "token.quota_negative"
	MsgTokenQuotaExceedMax       = "token.quota_exceed_max"
	MsgTokenGenerateFailed       = "token.generate_failed"
	MsgTokenGetInfoFailed        = "token.get_info_failed"
	MsgTokenExpiredCannotEnable  = "token.expired_cannot_enable"
	MsgTokenExhaustedCannotEable = "token.exhausted_cannot_enable"
	MsgTokenInvalid              = "token.invalid"
	MsgTokenNotProvided          = "token.not_provided"
	MsgTokenExpired              = "token.expired"
	MsgTokenExhausted            = "token.exhausted"
	MsgTokenStatusUnavailable    = "token.status_unavailable"
	MsgTokenDbError              = "token.db_error"
	MsgTokenAutoGroupsTooMany    = "token.auto_groups_too_many"
	MsgTokenAutoGroupsDuplicate  = "token.auto_groups_duplicate"
	MsgTokenAutoGroupsInvalid    = "token.auto_groups_invalid"
)

// Redemption related messages
const (
	MsgRedemptionNameLength        = "redemption.name_length"
	MsgRedemptionCountPositive     = "redemption.count_positive"
	MsgRedemptionCountMax          = "redemption.count_max"
	MsgRedemptionCreateFailed      = "redemption.create_failed"
	MsgRedemptionInvalid           = "redemption.invalid"
	MsgRedemptionUsed              = "redemption.used"
	MsgRedemptionExpired           = "redemption.expired"
	MsgRedemptionFailed            = "redemption.failed"
	MsgRedemptionNotProvided       = "redemption.not_provided"
	MsgRedemptionExpireTimeInvalid = "redemption.expire_time_invalid"
)

// User related messages
const (
	MsgUserPasswordLoginDisabled     = "user.password_login_disabled"
	MsgUserRegisterDisabled          = "user.register_disabled"
	MsgUserPasswordRegisterDisabled  = "user.password_register_disabled"
	MsgUserUsernameOrPasswordEmpty   = "user.username_or_password_empty"
	MsgUserUsernameOrPasswordError   = "user.username_or_password_error"
	MsgUserEmailOrPasswordEmpty      = "user.email_or_password_empty"
	MsgUserExists                    = "user.exists"
	MsgUserNotExists                 = "user.not_exists"
	MsgUserDisabled                  = "user.disabled"
	MsgUserSessionSaveFailed         = "user.session_save_failed"
	MsgUserRequire2FA                = "user.require_2fa"
	MsgUserEmailVerificationRequired = "user.email_verification_required"
	MsgUserVerificationCodeError     = "user.verification_code_error"
	MsgUserEmailAlreadyTaken         = "user.email_already_taken"
	MsgUserPasswordUnset             = "user.password_unset"
	MsgUserPasswordResetLinkInvalid  = "user.password_reset_link_invalid"
	MsgUserInputInvalid              = "user.input_invalid"
	MsgUserGroupRatiosInvalid        = "user.group_ratios_invalid"
	MsgUserNoPermissionSameLevel     = "user.no_permission_same_level"
	MsgUserNoPermissionHigherLevel   = "user.no_permission_higher_level"
	MsgUserCannotCreateHigherLevel   = "user.cannot_create_higher_level"
	MsgUserCannotDeleteRootUser      = "user.cannot_delete_root_user"
	MsgUserCannotDisableRootUser     = "user.cannot_disable_root_user"
	MsgUserCannotDemoteRootUser      = "user.cannot_demote_root_user"
	MsgUserAlreadyAdmin              = "user.already_admin"
	MsgUserAlreadyCommon             = "user.already_common"
	MsgUserAdminCannotPromote        = "user.admin_cannot_promote"
	MsgUserOriginalPasswordError     = "user.original_password_error"
	MsgUserInviteQuotaInsufficient   = "user.invite_quota_insufficient"
	MsgUserTransferQuotaMinimum      = "user.transfer_quota_minimum"
	MsgUserTransferSuccess           = "user.transfer_success"
	MsgUserTransferFailed            = "user.transfer_failed"
	MsgUserTopUpProcessing           = "user.topup_processing"
	MsgUserRegisterFailed            = "user.register_failed"
	MsgUserDefaultTokenFailed        = "user.default_token_failed"
	MsgUserAffCodeEmpty              = "user.aff_code_empty"
	MsgUserEmailEmpty                = "user.email_empty"
	MsgUserGitHubIdEmpty             = "user.github_id_empty"
	MsgUserDiscordIdEmpty            = "user.discord_id_empty"
	MsgUserOidcIdEmpty               = "user.oidc_id_empty"
	MsgUserWeChatIdEmpty             = "user.wechat_id_empty"
	MsgUserTelegramIdEmpty           = "user.telegram_id_empty"
	MsgUserTelegramNotBound          = "user.telegram_not_bound"
	MsgUserLinuxDOIdEmpty            = "user.linux_do_id_empty"
	MsgUserQuotaChangeZero           = "user.quota_change_zero"
)

// Quota related messages
const (
	MsgQuotaNegative        = "quota.negative"
	MsgQuotaExceedMax       = "quota.exceed_max"
	MsgQuotaInsufficient    = "quota.insufficient"
	MsgQuotaWarningInvalid  = "quota.warning_invalid"
	MsgQuotaThresholdGtZero = "quota.threshold_gt_zero"
)

// Subscription related messages
const (
	MsgSubscriptionNotEnabled       = "subscription.not_enabled"
	MsgSubscriptionTitleEmpty       = "subscription.title_empty"
	MsgSubscriptionPriceNegative    = "subscription.price_negative"
	MsgSubscriptionPriceMax         = "subscription.price_max"
	MsgSubscriptionPurchaseLimitNeg = "subscription.purchase_limit_negative"
	MsgSubscriptionQuotaNegative    = "subscription.quota_negative"
	MsgSubscriptionGroupNotExists   = "subscription.group_not_exists"
	MsgSubscriptionResetCycleGtZero = "subscription.reset_cycle_gt_zero"
	MsgSubscriptionPurchaseMax      = "subscription.purchase_max"
	MsgSubscriptionInvalidId        = "subscription.invalid_id"
	MsgSubscriptionInvalidUserId    = "subscription.invalid_user_id"
)

// Payment related messages
const (
	MsgPaymentNotConfigured      = "payment.not_configured"
	MsgPaymentMethodNotExists    = "payment.method_not_exists"
	MsgPaymentCallbackError      = "payment.callback_error"
	MsgPaymentCreateFailed       = "payment.create_failed"
	MsgPaymentStartFailed        = "payment.start_failed"
	MsgPaymentAmountTooLow       = "payment.amount_too_low"
	MsgPaymentStripeNotConfig    = "payment.stripe_not_configured"
	MsgPaymentWebhookNotConfig   = "payment.webhook_not_configured"
	MsgPaymentPriceIdNotConfig   = "payment.price_id_not_configured"
	MsgPaymentCreemNotConfig     = "payment.creem_not_configured"
	MsgPaymentComplianceRequired = "payment.compliance_required"
)

// Topup related messages
const (
	MsgTopupNotProvided          = "topup.not_provided"
	MsgTopupOrderNotExists       = "topup.order_not_exists"
	MsgTopupOrderStatus          = "topup.order_status"
	MsgTopupFailed               = "topup.failed"
	MsgTopupInvalidQuota         = "topup.invalid_quota"
	MsgUserBillingExportDisabled = "topup.user_billing_export_disabled"
	MsgTopupExportFilterRequired = "topup.export_filter_required"
	MsgTopupExportFailed         = "topup.export_failed"
	MsgTopupExportRateLimited    = "topup.export_rate_limited"
)

// Channel related messages
const (
	MsgChannelNotExists          = "channel.not_exists"
	MsgChannelIdFormatError      = "channel.id_format_error"
	MsgChannelNoAvailableKey     = "channel.no_available_key"
	MsgChannelGetListFailed      = "channel.get_list_failed"
	MsgChannelGetTagsFailed      = "channel.get_tags_failed"
	MsgChannelGetKeyFailed       = "channel.get_key_failed"
	MsgChannelGetOllamaFailed    = "channel.get_ollama_failed"
	MsgChannelQueryFailed        = "channel.query_failed"
	MsgChannelNoValidUpstream    = "channel.no_valid_upstream"
	MsgChannelUpstreamSaturated  = "channel.upstream_saturated"
	MsgChannelGetAvailableFailed = "channel.get_available_failed"
)

// Model related messages
const (
	MsgModelNameEmpty     = "model.name_empty"
	MsgModelNameExists    = "model.name_exists"
	MsgModelIdMissing     = "model.id_missing"
	MsgModelGetListFailed = "model.get_list_failed"
	MsgModelGetFailed     = "model.get_failed"
	MsgModelResetSuccess  = "model.reset_success"
)

// Vendor related messages
const (
	MsgVendorNameEmpty  = "vendor.name_empty"
	MsgVendorNameExists = "vendor.name_exists"
	MsgVendorIdMissing  = "vendor.id_missing"
)

// Group related messages
const (
	MsgGroupNameTypeEmpty = "group.name_type_empty"
	MsgGroupNameExists    = "group.name_exists"
	MsgGroupIdMissing     = "group.id_missing"
)

// Checkin related messages
const (
	MsgCheckinDisabled     = "checkin.disabled"
	MsgCheckinAlreadyToday = "checkin.already_today"
	MsgCheckinFailed       = "checkin.failed"
	MsgCheckinQuotaFailed  = "checkin.quota_failed"
)

// Passkey related messages
const (
	MsgPasskeyCreateFailed  = "passkey.create_failed"
	MsgPasskeyLoginAbnormal = "passkey.login_abnormal"
	MsgPasskeyUpdateFailed  = "passkey.update_failed"
	MsgPasskeyInvalidUserId = "passkey.invalid_user_id"
	MsgPasskeyVerifyFailed  = "passkey.verify_failed"
)

// 2FA related messages
const (
	MsgTwoFANotEnabled    = "twofa.not_enabled"
	MsgTwoFAUserIdEmpty   = "twofa.user_id_empty"
	MsgTwoFAAlreadyExists = "twofa.already_exists"
	MsgTwoFARecordIdEmpty = "twofa.record_id_empty"
	MsgTwoFACodeInvalid   = "twofa.code_invalid"
	MsgTwoFALoginExpired  = "twofa.login_expired"
)

// Rate limit related messages
const (
	MsgRateLimitReached         = "rate_limit.reached"
	MsgRateLimitTotalReached    = "rate_limit.total_reached"
	MsgRateLimitTooManyRequests = "rate_limit.too_many_requests"
)

// Setting related messages
const (
	MsgSettingInvalidType      = "setting.invalid_type"
	MsgSettingWebhookEmpty     = "setting.webhook_empty"
	MsgSettingWebhookInvalid   = "setting.webhook_invalid"
	MsgSettingEmailInvalid     = "setting.email_invalid"
	MsgSettingBarkUrlEmpty     = "setting.bark_url_empty"
	MsgSettingBarkUrlInvalid   = "setting.bark_url_invalid"
	MsgSettingGotifyUrlEmpty   = "setting.gotify_url_empty"
	MsgSettingGotifyTokenEmpty = "setting.gotify_token_empty"
	MsgSettingGotifyUrlInvalid = "setting.gotify_url_invalid"
	MsgSettingUrlMustHttp      = "setting.url_must_http"
	MsgSettingSaved            = "setting.saved"
)

// Deployment related messages (io.net)
const (
	MsgDeploymentNotEnabled     = "deployment.not_enabled"
	MsgDeploymentIdRequired     = "deployment.id_required"
	MsgDeploymentContainerIdReq = "deployment.container_id_required"
	MsgDeploymentNameEmpty      = "deployment.name_empty"
	MsgDeploymentNameTaken      = "deployment.name_taken"
	MsgDeploymentHardwareIdReq  = "deployment.hardware_id_required"
	MsgDeploymentHardwareInvId  = "deployment.hardware_invalid_id"
	MsgDeploymentApiKeyRequired = "deployment.api_key_required"
	MsgDeploymentInvalidPayload = "deployment.invalid_payload"
	MsgDeploymentNotFound       = "deployment.not_found"
)

// Performance related messages
const (
	MsgPerfDiskCacheCleared = "performance.disk_cache_cleared"
	MsgPerfStatsReset       = "performance.stats_reset"
	MsgPerfGcExecuted       = "performance.gc_executed"
)

// Ability related messages
const (
	MsgAbilityDbCorrupted   = "ability.db_corrupted"
	MsgAbilityRepairRunning = "ability.repair_running"
)

// OAuth related messages
const (
	MsgOAuthInvalidCode     = "oauth.invalid_code"
	MsgOAuthGetUserErr      = "oauth.get_user_error"
	MsgOAuthAccountUsed     = "oauth.account_used"
	MsgOAuthUnknownProvider = "oauth.unknown_provider"
	MsgOAuthStateInvalid    = "oauth.state_invalid"
	MsgOAuthNotEnabled      = "oauth.not_enabled"
	MsgOAuthUserDeleted     = "oauth.user_deleted"
	MsgOAuthUserBanned      = "oauth.user_banned"
	MsgOAuthBindSuccess     = "oauth.bind_success"
	MsgOAuthAlreadyBound    = "oauth.already_bound"
	MsgOAuthConnectFailed   = "oauth.connect_failed"
	MsgOAuthTokenFailed     = "oauth.token_failed"
	MsgOAuthUserInfoEmpty   = "oauth.user_info_empty"
	MsgOAuthTrustLevelLow   = "oauth.trust_level_low"
)

// Model layer error messages (for translation in controller)
const (
	MsgRedeemFailed          = "redeem.failed"
	MsgCreateDefaultTokenErr = "user.create_default_token_error"
	MsgUuidDuplicate         = "common.uuid_duplicate"
	MsgInvalidInput          = "common.invalid_input"
)

// Distributor related messages
const (
	MsgDistributorInvalidRequest          = "distributor.invalid_request"
	MsgDistributorInvalidChannelId        = "distributor.invalid_channel_id"
	MsgDistributorChannelDisabled         = "distributor.channel_disabled"
	MsgDistributorAffinityChannelDisabled = "distributor.affinity_channel_disabled"
	MsgDistributorTokenNoModelAccess      = "distributor.token_no_model_access"
	MsgDistributorTokenModelForbidden     = "distributor.token_model_forbidden"
	MsgDistributorModelNameRequired       = "distributor.model_name_required"
	MsgDistributorInvalidPlayground       = "distributor.invalid_playground_request"
	MsgDistributorGroupAccessDenied       = "distributor.group_access_denied"
	MsgDistributorGetChannelFailed        = "distributor.get_channel_failed"
	MsgDistributorNoAvailableChannel      = "distributor.no_available_channel"
	MsgDistributorInvalidMidjourney       = "distributor.invalid_midjourney_request"
	MsgDistributorInvalidParseModel       = "distributor.invalid_request_parse_model"
)

// TopUp export CSV column headers
const (
	MsgTopUpExportColTradeNo       = "topup_export.col_trade_no"
	MsgTopUpExportColUserId        = "topup_export.col_user_id"
	MsgTopUpExportColPaymentMethod = "topup_export.col_payment_method"
	MsgTopUpExportColAmount        = "topup_export.col_amount"
	MsgTopUpExportColMoney         = "topup_export.col_money"
	MsgTopUpExportColCurrency      = "topup_export.col_currency"
	MsgTopUpExportColStatus        = "topup_export.col_status"
	MsgTopUpExportColCreateTime    = "topup_export.col_create_time"
	MsgTopUpExportColCompleteTime  = "topup_export.col_complete_time"
)

// Custom OAuth provider related messages
const (
	MsgCustomOAuthNotFound          = "custom_oauth.not_found"
	MsgCustomOAuthSlugEmpty         = "custom_oauth.slug_empty"
	MsgCustomOAuthSlugExists        = "custom_oauth.slug_exists"
	MsgCustomOAuthNameEmpty         = "custom_oauth.name_empty"
	MsgCustomOAuthHasBindings       = "custom_oauth.has_bindings"
	MsgCustomOAuthBindingNotFound   = "custom_oauth.binding_not_found"
	MsgCustomOAuthProviderIdInvalid = "custom_oauth.provider_id_field_invalid"
)

// Profiling (pprof) messages
const (
	MsgPprofDisabled        = "pprof.disabled"
	MsgPprofBusy            = "pprof.busy"
	MsgPprofTraceBusy       = "pprof.trace_busy"
	MsgPprofInvalidSeconds  = "pprof.invalid_seconds"
	MsgPprofSecondsTooLarge = "pprof.seconds_too_large"
	MsgPprofInvalidProfile  = "pprof.invalid_profile"
)

// Fallback backfill messages
const (
	MsgBackfillDateMustBeHistorical = "backfill_date_must_be_historical"
	MsgBackfillDisabled             = "backfill_disabled"
	MsgBackfillAlreadyRunning       = "backfill_already_running"
	MsgBackfillFileNotFound         = "backfill_file_not_found"
)

// Usage log export messages
const (
	MsgLogExportDisabled          = "log_export.disabled"
	MsgLogExportUnavailable       = "log_export.unavailable"
	MsgLogExportCooldown          = "log_export.cooldown"
	MsgLogExportBusy              = "log_export.busy"
	MsgLogExportTooManyJobs       = "log_export.too_many_jobs"
	MsgLogExportRangeRequired     = "log_export.range_required"
	MsgLogExportRangeInvalid      = "log_export.range_invalid"
	MsgLogExportRangeTooLong      = "log_export.range_too_long"
	MsgLogExportNoColumns         = "log_export.no_columns"
	MsgLogExportInvalidColumn     = "log_export.invalid_column"
	MsgLogExportTooManyColumns    = "log_export.too_many_columns"
	MsgLogExportDiskFull          = "log_export.disk_full"
	MsgLogExportCreateFailed      = "log_export.create_failed"
	MsgLogExportJobNotFound       = "log_export.job_not_found"
	MsgLogExportJobNotReady       = "log_export.job_not_ready"
	MsgLogExportPartNotFound      = "log_export.part_not_found"
	MsgLogExportTemplateNotFound  = "log_export.template_not_found"
	MsgLogExportTemplateNameEmpty = "log_export.template_name_empty"
	MsgLogExportTemplateNameLong  = "log_export.template_name_long"
	MsgLogExportTemplateExists    = "log_export.template_exists"
	MsgLogExportTemplateLimit     = "log_export.template_limit"
)

// Usage log export: log type cell values
const (
	MsgLogExportTypeUnknown = "log_export.type.unknown"
	MsgLogExportTypeTopup   = "log_export.type.topup"
	MsgLogExportTypeConsume = "log_export.type.consume"
	MsgLogExportTypeManage  = "log_export.type.manage"
	MsgLogExportTypeSystem  = "log_export.type.system"
	MsgLogExportTypeError   = "log_export.type.error"
	MsgLogExportTypeRefund  = "log_export.type.refund"
	MsgLogExportTypeLogin   = "log_export.type.login"
)

// Usage log export: CSV/xlsx column headers.
// Key layout mirrors model.LogExportColumnI18nKey(columnKey).
const (
	MsgLogExportColCreatedAt         = "log_export.col.created_at"
	MsgLogExportColId                = "log_export.col.id"
	MsgLogExportColType              = "log_export.col.type"
	MsgLogExportColUsername          = "log_export.col.username"
	MsgLogExportColUserId            = "log_export.col.user_id"
	MsgLogExportColTokenName         = "log_export.col.token_name"
	MsgLogExportColTokenId           = "log_export.col.token_id"
	MsgLogExportColGroup             = "log_export.col.group"
	MsgLogExportColModelName         = "log_export.col.model_name"
	MsgLogExportColUpstreamModelName = "log_export.col.upstream_model_name"
	MsgLogExportColIsModelMapped     = "log_export.col.is_model_mapped"
	MsgLogExportColIp                = "log_export.col.ip"
	MsgLogExportColRequestId         = "log_export.col.request_id"
	MsgLogExportColUpstreamRequestId = "log_export.col.upstream_request_id"
	MsgLogExportColContent           = "log_export.col.content"

	MsgLogExportColPromptTokens          = "log_export.col.prompt_tokens"
	MsgLogExportColCompletionTokens      = "log_export.col.completion_tokens"
	MsgLogExportColTotalTokens           = "log_export.col.total_tokens"
	MsgLogExportColCacheTokens           = "log_export.col.cache_tokens"
	MsgLogExportColCacheCreationTokens   = "log_export.col.cache_creation_tokens"
	MsgLogExportColCacheCreationTokens5m = "log_export.col.cache_creation_tokens_5m"
	MsgLogExportColCacheCreationTokens1h = "log_export.col.cache_creation_tokens_1h"
	MsgLogExportColTextInput             = "log_export.col.text_input"
	MsgLogExportColTextOutput            = "log_export.col.text_output"
	MsgLogExportColAudioInput            = "log_export.col.audio_input"
	MsgLogExportColAudioOutput           = "log_export.col.audio_output"
	MsgLogExportColImageOutput           = "log_export.col.image_output"
	MsgLogExportColWebSearchCallCount    = "log_export.col.web_search_call_count"
	MsgLogExportColFileSearchCallCount   = "log_export.col.file_search_call_count"

	MsgLogExportColQuota                = "log_export.col.quota"
	MsgLogExportColCostUsd              = "log_export.col.cost_usd"
	MsgLogExportColBillingSource        = "log_export.col.billing_source"
	MsgLogExportColBillingMode          = "log_export.col.billing_mode"
	MsgLogExportColMatchedTier          = "log_export.col.matched_tier"
	MsgLogExportColModelRatio           = "log_export.col.model_ratio"
	MsgLogExportColCompletionRatio      = "log_export.col.completion_ratio"
	MsgLogExportColGroupRatio           = "log_export.col.group_ratio"
	MsgLogExportColUserGroupRatio       = "log_export.col.user_group_ratio"
	MsgLogExportColCacheRatio           = "log_export.col.cache_ratio"
	MsgLogExportColCacheCreationRatio   = "log_export.col.cache_creation_ratio"
	MsgLogExportColCacheCreationRatio5m = "log_export.col.cache_creation_ratio_5m"
	MsgLogExportColCacheCreationRatio1h = "log_export.col.cache_creation_ratio_1h"
	MsgLogExportColAudioRatio           = "log_export.col.audio_ratio"
	MsgLogExportColAudioCompletionRatio = "log_export.col.audio_completion_ratio"
	MsgLogExportColImageRatio           = "log_export.col.image_ratio"
	MsgLogExportColModelPrice           = "log_export.col.model_price"
	MsgLogExportColWebSearchPrice       = "log_export.col.web_search_price"
	MsgLogExportColFileSearchPrice      = "log_export.col.file_search_price"

	MsgLogExportColUseTime         = "log_export.col.use_time"
	MsgLogExportColFrt             = "log_export.col.frt"
	MsgLogExportColTokensPerSec    = "log_export.col.tokens_per_sec"
	MsgLogExportColIsStream        = "log_export.col.is_stream"
	MsgLogExportColStreamStatus    = "log_export.col.stream_status"
	MsgLogExportColReasoningEffort = "log_export.col.reasoning_effort"

	MsgLogExportColLoginMethod = "log_export.col.login_method"
	MsgLogExportColUserAgent   = "log_export.col.user_agent"
	MsgLogExportColRequestPath = "log_export.col.request_path"

	MsgLogExportColChannelId        = "log_export.col.channel_id"
	MsgLogExportColChannelName      = "log_export.col.channel_name"
	MsgLogExportColRetryChain       = "log_export.col.retry_chain"
	MsgLogExportColIsMultiKey       = "log_export.col.is_multi_key"
	MsgLogExportColMultiKeyIndex    = "log_export.col.multi_key_index"
	MsgLogExportColUsageBillingPath = "log_export.col.usage_billing_path"
	MsgLogExportColLocalCountTokens = "log_export.col.local_count_tokens"
	MsgLogExportColQuotaSaturation  = "log_export.col.quota_saturation"
	MsgLogExportColOtherRaw         = "log_export.col.other_raw"

	MsgLogExportColAdminUsername         = "log_export.col.admin_username"
	MsgLogExportColAdminId               = "log_export.col.admin_id"
	MsgLogExportColAdminRole             = "log_export.col.admin_role"
	MsgLogExportColAuthMethod            = "log_export.col.auth_method"
	MsgLogExportColPaymentMethod         = "log_export.col.payment_method"
	MsgLogExportColCallbackPaymentMethod = "log_export.col.callback_payment_method"
	MsgLogExportColCallerIp              = "log_export.col.caller_ip"
	MsgLogExportColServerIp              = "log_export.col.server_ip"
	MsgLogExportColNodeName              = "log_export.col.node_name"
	MsgLogExportColVersion               = "log_export.col.version"
	MsgLogExportColAuditMethod           = "log_export.col.audit_method"
	MsgLogExportColAuditRoute            = "log_export.col.audit_route"
	MsgLogExportColAuditPath             = "log_export.col.audit_path"
	MsgLogExportColAuditStatus           = "log_export.col.audit_status"
	MsgLogExportColAuditSuccess          = "log_export.col.audit_success"
)

// Request log (relay 请求日志)
const (
	MsgRequestLogNotFound          = "request_log.not_found"
	MsgRequestLogTimestampRequired = "request_log.timestamp_required"
)

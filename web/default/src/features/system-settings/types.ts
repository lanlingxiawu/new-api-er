/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
export type SystemOption = {
  key: string
  value: string
}

export type SystemOptionKey = string

export type SystemOptionsResponse = {
  success: boolean
  message: string
  data: SystemOption[]
}

export type UpdateOptionRequest = {
  key: string
  value: string | boolean | number
}

export type UpdateOptionResponse = {
  success: boolean
  message: string
}

export type BusinessStatsCircuitBreakerStatus = {
  enabled: boolean
  manual_disabled: boolean
  open: boolean
  hard_disabled: boolean
  disabled_until: number
  consecutive_failures: number
  cooldown_seconds: number
  last_reason: string
}

export type BusinessStatsCircuitBreakerStatusResponse = {
  success: boolean
  message: string
  data: BusinessStatsCircuitBreakerStatus
}

export type LedgerPipelineQueueStatus = {
  backlog: number
  dropped: number
  last_flush_items: number
  last_flush_took_ms: number
  last_flush_at: number
}

export type LedgerPipelineStatus = {
  snapshot: {
    cost: LedgerPipelineQueueStatus
    pair: LedgerPipelineQueueStatus
    commission: LedgerPipelineQueueStatus
    cost_retry: LedgerPipelineQueueStatus
    pair_retry: LedgerPipelineQueueStatus
  }
  config: {
    flush_interval_sec: number
    settlement_flush_max_per_cycle: number
    full_drain: boolean
  }
  theoretical_pair_records_per_sec: number
  theoretical_pair_rpm: number
}

export type LedgerPipelineStatusResponse = {
  success: boolean
  message: string
  data: LedgerPipelineStatus
}

export type ConfirmPaymentComplianceResponse = {
  success: boolean
  message: string
  data?: {
    confirmed: boolean
    terms_version: string
    confirmed_at: number
    confirmed_by: number
  }
}

export type DeleteLogsResponse = {
  success: boolean
  message: string
  data?: number
}

export type SiteSettings = {
  'theme.frontend': string
  Notice: string
  SystemName: string
  Logo: string
  Footer: string
  About: string
  HomePageContent: string
  ServerAddress: string
  'legal.user_agreement': string
  'legal.privacy_policy': string
  HeaderNavModules: string
  SidebarModulesAdmin: string
}

export type AuthSettings = {
  PasswordLoginEnabled: boolean
  PasswordRegisterEnabled: boolean
  EmailVerificationEnabled: boolean
  RegisterEnabled: boolean
  EmailDomainRestrictionEnabled: boolean
  EmailAliasRestrictionEnabled: boolean
  EmailDomainWhitelist: string
  GitHubOAuthEnabled: boolean
  GitHubClientId: string
  GitHubClientSecret: string
  'discord.enabled': boolean
  'discord.client_id': string
  'discord.client_secret': string
  'oidc.enabled': boolean
  'oidc.client_id': string
  'oidc.client_secret': string
  'oidc.well_known': string
  'oidc.authorization_endpoint': string
  'oidc.token_endpoint': string
  'oidc.user_info_endpoint': string
  TelegramOAuthEnabled: boolean
  TelegramBotToken: string
  TelegramBotName: string
  LinuxDOOAuthEnabled: boolean
  LinuxDOClientId: string
  LinuxDOClientSecret: string
  LinuxDOMinimumTrustLevel: string
  WeChatAuthEnabled: boolean
  WeChatServerAddress: string
  WeChatServerToken: string
  WeChatAccountQRCodeImageURL: string
  TurnstileCheckEnabled: boolean
  TurnstileSiteKey: string
  TurnstileSecretKey: string
  'passkey.enabled': boolean
  'passkey.rp_display_name': string
  'passkey.rp_id': string
  'passkey.origins': string
  'passkey.allow_insecure_origin': boolean
  'passkey.user_verification': 'required' | 'preferred' | 'discouraged'
  'passkey.attachment_preference': '' | 'platform' | 'cross-platform'
}

export type ContentSettings = {
  'console_setting.api_info': string
  'console_setting.announcements': string
  'console_setting.faq': string
  'console_setting.uptime_kuma_groups': string
  'console_setting.api_info_enabled': boolean
  'console_setting.announcements_enabled': boolean
  'console_setting.faq_enabled': boolean
  'console_setting.uptime_kuma_enabled': boolean
  DataExportEnabled: boolean
  DataExportDefaultTime: string
  DataExportInterval: number
  Chats: string
  DrawingEnabled: boolean
  MjNotifyEnabled: boolean
  MjAccountFilterEnabled: boolean
  MjForwardUrlEnabled: boolean
  MjModeClearEnabled: boolean
  MjActionCheckSuccessEnabled: boolean
}

export type ModelSettings = {
  'global.pass_through_request_enabled': boolean
  'global.thinking_model_blacklist': string
  'global.chat_completions_to_responses_policy': string
  'general_setting.ping_interval_enabled': boolean
  'general_setting.ping_interval_seconds': number
  'gemini.safety_settings': string
  'gemini.version_settings': string
  'gemini.supported_imagine_models': string
  'gemini.thinking_adapter_enabled': boolean
  'gemini.thinking_adapter_budget_tokens_percentage': number
  'gemini.function_call_thought_signature_enabled': boolean
  'gemini.remove_function_response_id_enabled': boolean
  'claude.model_headers_settings': string
  'claude.default_max_tokens': string
  'claude.thinking_adapter_enabled': boolean
  'claude.thinking_adapter_budget_tokens_percentage': number
  'grok.violation_deduction_enabled': boolean
  'grok.violation_deduction_amount': number
  ModelPrice: string
  ModelRatio: string
  CacheRatio: string
  CreateCacheRatio: string
  CompletionRatio: string
  ImageRatio: string
  AudioRatio: string
  AudioCompletionRatio: string
  ExposeRatioEnabled: boolean
  'billing_setting.billing_mode': string
  'billing_setting.billing_expr': string
  'tool_price_setting.prices': string
  'thirdpartysd2_pricing.matrix': string
  TopupGroupRatio: string
  GroupRatio: string
  UserUsableGroups: string
  GroupGroupRatio: string
  AutoGroups: string
  DefaultUseAutoGroup: boolean
  'group_ratio_setting.group_special_usable_group': string
  'channel_affinity_setting.enabled': boolean
  'channel_affinity_setting.switch_on_success': boolean
  'channel_affinity_setting.max_entries': number
  'channel_affinity_setting.default_ttl_seconds': number
  'channel_affinity_setting.rules': string
  'model_deployment.ionet.api_key': string
  'model_deployment.ionet.enabled': boolean
}

export type BillingSettings = {
  QuotaForNewUser: number
  PreConsumedQuota: number
  QuotaForInviter: number
  QuotaForInvitee: number
  TopUpLink: string
  'general_setting.docs_link': string
  'quota_setting.enable_free_model_pre_consume': boolean
  QuotaPerUnit: number
  USDExchangeRate: number
  'general_setting.quota_display_type': string
  'general_setting.custom_currency_symbol': string
  'general_setting.custom_currency_exchange_rate': number
  DisplayInCurrencyEnabled: boolean
  DisplayTokenStatEnabled: boolean
  ModelPrice: string
  ModelRatio: string
  CacheRatio: string
  CreateCacheRatio: string
  CompletionRatio: string
  ImageRatio: string
  AudioRatio: string
  AudioCompletionRatio: string
  ExposeRatioEnabled: boolean
  'billing_setting.billing_mode': string
  'billing_setting.billing_expr': string
  'tool_price_setting.prices': string
  'thirdpartysd2_pricing.matrix': string
  TopupGroupRatio: string
  GroupRatio: string
  UserUsableGroups: string
  GroupGroupRatio: string
  AutoGroups: string
  DefaultUseAutoGroup: boolean
  'group_ratio_setting.group_special_usable_group': string
  PayAddress: string
  EpayId: string
  EpayKey: string
  Price: number
  MinTopUp: number
  CustomCallbackAddress: string
  PayMethods: string
  'payment_setting.amount_options': string
  'payment_setting.amount_discount': string
  'payment_setting.compliance_confirmed': boolean
  'payment_setting.compliance_terms_version': string
  'payment_setting.compliance_confirmed_at': number
  'payment_setting.compliance_confirmed_by': number
  'payment_setting.compliance_confirmed_ip': string
  StripeEnabled: boolean
  StripeApiSecret: string
  StripeWebhookSecret: string
  StripePriceId: string
  StripeUnitPrice: number
  StripeUseRealtimeRate: boolean
  StripeMinTopUp: number
  StripePromotionCodesEnabled: boolean
  CreemApiKey: string
  CreemWebhookSecret: string
  CreemTestMode: boolean
  CreemProducts: string
  WaffoEnabled: boolean
  WaffoApiKey: string
  WaffoPrivateKey: string
  WaffoPublicCert: string
  WaffoSandboxPublicCert: string
  WaffoSandboxApiKey: string
  WaffoSandboxPrivateKey: string
  WaffoSandbox: boolean
  WaffoMerchantId: string
  WaffoCurrency: string
  WaffoUnitPrice: number
  WaffoMinTopUp: number
  WaffoNotifyUrl: string
  WaffoReturnUrl: string
  WaffoPayMethods: string
  WaffoPancakeMerchantID: string
  WaffoPancakePrivateKey: string
  WaffoPancakeReturnURL: string
  // Bound by the operator through the catalog flow in the admin Pancake
  // section (saved via /api/option/waffo-pancake/save).
  WaffoPancakeStoreID: string
  WaffoPancakeProductID: string
  AlipayEnabled: boolean
  AlipayAppId: string
  AlipayPrivateKey: string
  AlipayPublicKey: string
  AlipaySandbox: boolean
  AlipayMinTopUp: number
  AlipayNotifyUrl: string
  AlipayReturnUrl: string
  WechatEnabled: boolean
  WechatAppId: string
  WechatMchId: string
  WechatApiV3Key: string
  WechatMchPrivateKey: string
  WechatMchCertSerialNo: string
  WechatMinTopUp: number
  WechatNotifyUrl: string
  InfiniEnabled: boolean
  InfiniApiKey: string
  InfiniApiSecret: string
  InfiniWebhookSecret: string
  InfiniSandbox: boolean
  InfiniNotifyUrl: string
  InfiniReturnUrl: string
  InfiniFailUrl: string
  InfiniUnitPrice: number
  InfiniMinTopUp: number
  InfiniCurrency: string
  // JSON 数组，格式 [{currency,unit_price,min_topup}]，优先于单币种字段
  InfiniCurrencies: string
  // JSON 数组，限定 Infini 结账页支付方式，如 [1,2]
  InfiniPayMethods: string
  'checkin_setting.enabled': boolean
  'checkin_setting.min_quota': number
  'checkin_setting.max_quota': number
}

export type OperationsSettings = {
  // xiugai 添加号池节点功能
  NodeControlServiceUrl: string
  // end
  RetryTimes: number
  DefaultCollapseSidebar: boolean
  DemoSiteEnabled: boolean
  SelfUseModeEnabled: boolean
  ChannelDisableThreshold: string
  QuotaRemindThreshold: string
  AutomaticDisableChannelEnabled: boolean
  AutomaticEnableChannelEnabled: boolean
  AutomaticDisableKeywords: string
  AutomaticDisableStatusCodes: string
  AutomaticRetryStatusCodes: string
  'monitor_setting.auto_test_channel_enabled': boolean
  'monitor_setting.auto_test_channel_minutes': number
  SMTPServer: string
  SMTPPort: string
  SMTPAccount: string
  SMTPFrom: string
  SMTPToken: string
  SMTPSSLEnabled: boolean
  SMTPForceAuthLogin: boolean
  WorkerUrl: string
  WorkerValidKey: string
  WorkerAllowHttpImageRequestEnabled: boolean
  LogConsumeEnabled: boolean
  RequestLogEnabled: boolean
  RequestLogUsername: string
  RequestLogMaxBodyKB: number
  RequestLogMinCount: number
  RequestLogMaxCount: number
  'performance_setting.disk_cache_enabled': boolean
  'performance_setting.disk_cache_threshold_mb': number
  'performance_setting.disk_cache_max_size_mb': number
  'performance_setting.disk_cache_path': string
  'performance_setting.monitor_enabled': boolean
  'performance_setting.monitor_cpu_threshold': number
  'performance_setting.monitor_memory_threshold': number
  'performance_setting.monitor_disk_threshold': number
  'perf_metrics_setting.enabled': boolean
  'perf_metrics_setting.flush_interval': number
  'perf_metrics_setting.bucket_time': 'hour' | 'minute' | '5min'
  'perf_metrics_setting.retention_days': number
}

export type SystemTuningSettings = {
  'business_stats_circuit_breaker_setting.enabled': boolean
  'business_stats_circuit_breaker_setting.manual_disabled': boolean
  'business_stats_circuit_breaker_setting.failure_threshold': number
  'business_stats_circuit_breaker_setting.initial_cooldown_seconds': number
  'business_stats_circuit_breaker_setting.max_cooldown_seconds': number
  'business_stats_circuit_breaker_setting.side_effect_db_timeout_ms': number
  'ledger_pipeline_setting.flush_interval_sec': number
  'ledger_pipeline_setting.outer_batch_size': number
  'ledger_pipeline_setting.cost_outer_batch_size': number
  'ledger_pipeline_setting.inner_batch_size': number
  'ledger_pipeline_setting.settlement_flush_max_per_cycle': number
  'ledger_pipeline_setting.cost_flush_max_per_cycle': number
  'ledger_pipeline_setting.full_drain': boolean
  'ledger_pipeline_setting.buf_max_entries': number
  'ledger_pipeline_setting.dedup_mem_max_entries': number
  'ledger_pipeline_setting.dedup_use_redis': boolean
  'ledger_pipeline_setting.dedup_redis_ttl_sec': number
  'ledger_pipeline_setting.flush_db_timeout_sec': number
  'ledger_retry_setting.retry_flush_interval_sec': number
  'ledger_retry_setting.stat_upsert_max_retries': number
  'ledger_retry_setting.retry_queue_max_entries': number
  'ledger_retry_setting.allow_concurrent_flush': boolean
  'ledger_pipeline_setting.fallback_queue_capacity': number
  'ledger_pipeline_setting.shutdown_timeout_sec': number
  'ledger_pipeline_setting.cache_ttl_secs': number[]
  'ledger_pipeline_setting.cache_ttl_jitter_percent': number
  'payment_setting.user_export_max_rows': number
  'export_setting.user_export_enabled': boolean
  'export_setting.rate_limit_cooldown_sec': number
  'export_setting.hard_ceiling_rows': number
  'ledger_detail_setting.export_user_cooldown_sec': number
  'ledger_detail_setting.export_batch_size': number
  'ledger_detail_setting.export_batch_sleep_ms': number
  'ledger_detail_setting.export_rows_per_file': number
  'ledger_detail_setting.export_max_range_sec': number
  'ledger_detail_setting.export_timeout_sec': number
  'ledger_detail_setting.list_max_range_sec': number
  'ledger_detail_setting.list_default_range_sec': number
  'ledger_detail_setting.list_default_limit': number
  'ledger_detail_setting.list_max_limit': number
  'ledger_detail_setting.list_scan_batch_size': number
  'ledger_detail_setting.list_scan_rows_per_req': number
  'business_stats_fallback_backfill_setting.enabled': boolean
  'business_stats_fallback_backfill_setting.use_separate_fallback_dir': boolean
  'business_stats_fallback_backfill_setting.status_cache_seconds': number
  'business_stats_fallback_backfill_setting.max_read_line_bytes': number
  'business_stats_fallback_backfill_setting.write_batch_size': number
  'business_stats_fallback_backfill_setting.flush_interval_sec': number
  'business_stats_fallback_backfill_setting.batch_sleep_ms': number
}

export type SecuritySettings = {
  ModelRequestRateLimitEnabled: boolean
  ModelRequestRateLimitCount: number
  ModelRequestRateLimitSuccessCount: number
  ModelRequestRateLimitDurationMinutes: number
  ModelRequestRateLimitGroup: string
  CheckSensitiveEnabled: boolean
  CheckSensitiveOnPromptEnabled: boolean
  SensitiveWords: string
  'fetch_setting.enable_ssrf_protection': boolean
  'fetch_setting.allow_private_ip': boolean
  'fetch_setting.domain_filter_mode': boolean
  'fetch_setting.ip_filter_mode': boolean
  'fetch_setting.domain_list': string[]
  'fetch_setting.ip_list': string[]
  'fetch_setting.allowed_ports': number[]
  'fetch_setting.apply_ip_filter_for_domain': boolean
}

export type UpstreamChannel = {
  id: number
  name: string
  base_url: string
  status: number
  type?: number
}

export type RatioType =
  | 'model_ratio'
  | 'completion_ratio'
  | 'cache_ratio'
  | 'create_cache_ratio'
  | 'image_ratio'
  | 'audio_ratio'
  | 'audio_completion_ratio'
  | 'model_price'
  | 'billing_mode'
  | 'billing_expr'

export type RatioDifference = {
  current: number | string | null
  upstreams: Record<string, number | string | 'same'>
  confidence: Record<string, boolean>
}

export type DifferencesMap = Record<
  string,
  Partial<Record<RatioType, RatioDifference>>
>

export type UpstreamChannelsResponse = {
  success: boolean
  message: string
  data: UpstreamChannel[]
}

export type UpstreamConfig = {
  id: number
  name: string
  base_url: string
  endpoint: string
}

export type FetchUpstreamRatiosRequest = {
  upstreams: UpstreamConfig[]
  timeout: number
}

export type TestResult = {
  name: string
  status: 'success' | 'error'
  error?: string
}

export type UpstreamRatiosResponse = {
  success: boolean
  message: string
  data: {
    differences: DifferencesMap
    test_results: TestResult[]
  }
}

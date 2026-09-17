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
import { z } from 'zod'

// ============================================================================
// Channel Schema & Types
// ============================================================================

export const channelInfoSchema = z.object({
  is_multi_key: z.boolean().default(false),
  multi_key_size: z.number().default(0),
  multi_key_status_list: z.record(z.string(), z.number()).optional(),
  multi_key_disabled_reason: z.record(z.string(), z.string()).optional(),
  multi_key_disabled_time: z.record(z.string(), z.number()).optional(),
  multi_key_polling_index: z.number().default(0),
  multi_key_mode: z.enum(['random', 'polling']).default('random'),
})

export type ChannelInfo = z.infer<typeof channelInfoSchema>

export const channelAccountBalanceSchema = z.object({
  group: z.string(),
  quota: z.number(),
  used_quota: z.number(),
  updated_time: z.number(),
})

export type ChannelAccountBalance = z.infer<typeof channelAccountBalanceSchema>

// 渠道用量视图，仅在渠道配置了每日上限时由列表接口回填。
// 口径唯一：上游消耗（不含分组倍率的实际用量 × 成本系数）。
// 设计见 docs/design/channel-limit-upstream-basis-and-timed-recovery.md。
export const channelDailyUsageSchema = z.object({
  stat_date: z.number(),
  // 今日上游消耗（quota 单位）
  cost_quota: z.number().default(0),
  limit_quota: z.number().default(0),
  disabled_at: z.number().default(0),
  // >0 = 限时模式（达到上限 N 分钟后恢复），此时上限按「轮」计算
  recover_minutes: z.number().default(0),
  // 限时模式当前一轮的开始时刻（unix 秒）
  period_start: z.number().default(0),
  // 限时模式当前一轮的上游消耗；仅 recover_minutes > 0 时有意义
  period_cost_quota: z.number().default(0),
  // 限时模式下预计重新启用的时刻（unix 秒）；未禁用或非限时模式为 0
  recover_at: z.number().default(0),
})

/**
 * 每日上限的恢复方式（仅表单层概念，后端存两列）：
 * - manual        → daily_limit_auto_recover = 0, daily_limit_recover_minutes = 0
 * - next_day      → daily_limit_auto_recover = 1, daily_limit_recover_minutes = 0
 * - after_minutes → daily_limit_auto_recover = 1, daily_limit_recover_minutes = N
 */
export const DAILY_LIMIT_RECOVER_MODES = [
  'manual',
  'next_day',
  'after_minutes',
] as const
export type DailyLimitRecoverMode = (typeof DAILY_LIMIT_RECOVER_MODES)[number]

/** 「N 分钟后恢复」的取值范围，与后端校验保持一致（最长 7 天）。 */
export const DAILY_LIMIT_RECOVER_MINUTES_MIN = 1
export const DAILY_LIMIT_RECOVER_MINUTES_MAX = 10080
export const DAILY_LIMIT_RECOVER_MINUTES_DEFAULT = 60

/** 「接近上限」阈值，与后端 ChannelDailyLimitNearThreshold 保持一致。 */
export const DAILY_LIMIT_NEAR_THRESHOLD = 0.8

export const channelSchema = z.object({
  id: z.number(),
  type: z.number(),
  key: z.string(),
  openai_organization: z.string().nullish(),
  test_model: z.string().nullish(),
  status: z.number(), // 1: enabled, 0: manual disabled, 2: auto disabled
  name: z.string(),
  weight: z.number().nullish(),
  created_time: z.number(),
  test_time: z.number(),
  response_time: z.number(), // in milliseconds
  base_url: z.string().nullish(),
  other: z.string().default(''),
  balance: z.number().default(0), // in USD
  balance_updated_time: z.number(),
  models: z.string().default(''),
  group: z.string().default('default'),
  used_quota: z.number().default(0),
  model_mapping: z.string().nullish(),
  status_code_mapping: z.string().nullish(),
  priority: z.number().nullish(),
  auto_ban: z.number().nullish(),
  other_info: z.string().default(''),
  tag: z.string().nullish(),
  setting: z.string().nullish(),
  param_override: z.string().nullish(),
  header_override: z.string().nullish(),
  remark: z.string().default(''),
  cost_ratio: z.number().nullish(),
  max_input_tokens: z.number().default(0),
  channel_info: channelInfoSchema.default({
    is_multi_key: false,
    multi_key_size: 0,
    multi_key_polling_index: 0,
    multi_key_mode: 'random',
  }),
  settings: z.string().default('{}'), // other_settings JSON
  account_balance: channelAccountBalanceSchema.nullish(),
  account_balance_configured: z.boolean().default(false),
  // —— 每日金额上限 ——
  // 配置列（可编辑，需要 ChannelSensitiveWrite）
  daily_quota_limit: z.number().default(0),
  daily_limit_auto_recover: z.number().nullish(),
  // 0 = 按日模式；1–10080 = 达到上限后 N 分钟恢复
  daily_limit_recover_minutes: z.number().default(0),
  // 服务端管理的禁用来源标记（只读）：>0 表示当前是因每日上限被禁用
  daily_limit_disabled_at: z.number().default(0),
  daily_limit_disabled_date: z.number().default(0),
  // 限时模式当前一轮的开始时刻（只读，永远不要提交）
  daily_limit_period_start: z.number().default(0),
  // 今日用量，仅配置了上限的渠道才有
  daily_usage: channelDailyUsageSchema.nullish(),
})

export type Channel = z.infer<typeof channelSchema>

// ============================================================================
// Channel Settings Types
// ============================================================================

export interface ChannelSettings {
  task_plugin_key?: string
  force_format?: boolean
  thinking_to_content?: boolean
  proxy?: string
  pass_through_body_enabled?: boolean
  responses_websocket_enabled?: boolean
  system_prompt?: string
  system_prompt_override?: boolean
  http_protocol?: 'auto' | 'http1' | string
  http2_connection_shards?: number
}

export interface ChannelOtherSettings {
  azure_responses_version?: string
  vertex_key_type?: 'json' | 'api_key'
  openrouter_enterprise?: boolean
  aws_key_type?: 'ak_sk' | 'api_key'
  allow_service_tier?: boolean
  disable_store?: boolean
  allow_safety_identifier?: boolean
  allow_include_obfuscation?: boolean
  allow_inference_geo?: boolean
  allow_speed?: boolean
  claude_beta_query?: boolean
  ollama_openai_chat?: boolean
  disable_task_polling_sleep?: boolean
  upstream_model_update_check_enabled?: boolean
  upstream_model_update_auto_sync_enabled?: boolean
  upstream_model_update_ignored_models?: string[]
  upstream_model_update_last_check_time?: number
  upstream_model_update_last_detected_models?: string[]
  advanced_custom?: AdvancedCustomConfig
}

export interface AdvancedCustomConfig {
  advanced_routes?: AdvancedCustomRoute[]
}

export interface AdvancedCustomRoute {
  incoming_path?: string
  upstream_path?: string
  converter?: AdvancedCustomConverter
  models?: string[]
  auth?: AdvancedCustomRouteAuth
  pass_through_body_enabled?: boolean
}

export interface AdvancedCustomRouteAuth {
  type?: AdvancedCustomAuthType
  name?: string
  value?: string
}

export type AdvancedCustomConverter =
  | 'none'
  | 'anthropic_messages_to_openai_chat_completions'
  | 'openai_chat_completions_to_anthropic_messages'
  | 'openai_chat_completions_to_openai_responses'
  | 'openai_responses_to_openai_chat_completions'
  | 'openai_responses_to_gemini_generate_content'
  | 'gemini_generate_content_to_openai_chat_completions'
  | 'openai_chat_completions_to_gemini_generate_content'

export type AdvancedCustomAuthType = 'none' | 'header' | 'query'

// ============================================================================
// API Response Types
// ============================================================================

export interface GetChannelsResponse {
  success: boolean
  message?: string
  data?: {
    items: Channel[]
    total: number
    page: number
    page_size: number
    type_counts?: Record<string, number>
  }
}

export interface SearchChannelsResponse {
  success: boolean
  message?: string
  data?: {
    items: Channel[]
    total: number
    type_counts?: Record<string, number>
  }
}

export interface GetChannelResponse {
  success: boolean
  message?: string
  data?: Channel
}

export interface ChannelOpsResponse {
  success: boolean
  message?: string
  data?: {
    retry_times: number
  }
}

export interface ChannelTestResponse {
  success: boolean
  message?: string
  error_code?: string
  time?: number
  data?: {
    response_time?: number
    error?: string
  }
}

export interface ChannelBalanceResponse {
  success: boolean
  message?: string
  balance?: number
  currency?: string
  raw_response?: string
}

export interface ChannelAccountBalanceResponse {
  success: boolean
  message?: string
  data?: ChannelAccountBalance
}

export interface FetchModelsResponse {
  success: boolean
  message?: string
  data?: string[]
}

export interface CopyChannelResponse {
  success: boolean
  message?: string
  data?: {
    id: number
  }
}

// ============================================================================
// Multi-Key Management Types
// ============================================================================

export interface KeyStatus {
  index: number
  status: number // 1: enabled, 2: manual disabled, 3: auto disabled
  disabled_time?: number
  reason?: string
  key_preview?: string
}

export type MultiKeyConfirmAction = {
  type:
    | 'enable'
    | 'disable'
    | 'delete'
    | 'enable-all'
    | 'disable-all'
    | 'delete-disabled'
  keyIndex?: number
}

export interface MultiKeyStatusResponse {
  success: boolean
  message?: string
  data?: {
    keys: KeyStatus[]
    total: number
    page: number
    page_size: number
    total_pages: number
    enabled_count: number
    manual_disabled_count: number
    auto_disabled_count: number
  }
}

// ============================================================================
// API Request Parameters
// ============================================================================

export type ChannelSortBy =
  | 'id'
  | 'name'
  | 'priority'
  | 'balance'
  | 'response_time'
  | 'test_time'
  // 今日用量 / 今日使用率。标签模式下不支持（后端会回退默认排序）。
  | 'daily_used'
  | 'daily_usage_ratio'

/** 每日上限筛选。reached = 当前因上限处于禁用状态。 */
export type ChannelLimitFilter =
  | 'all'
  | 'configured'
  | 'unlimited'
  | 'reached'
  | 'near'

export type ChannelSortOrder = 'asc' | 'desc'

export interface GetChannelsParams {
  p?: number
  page_size?: number
  status?: string // 'enabled', 'disabled', or empty for all
  type?: number
  group?: string
  id_sort?: boolean
  tag_mode?: boolean
  sort_by?: ChannelSortBy
  sort_order?: ChannelSortOrder
  limit_filter?: ChannelLimitFilter
}

export interface SearchChannelsParams {
  keyword?: string
  group?: string
  model?: string
  status?: string
  type?: number
  id_sort?: boolean
  tag_mode?: boolean
  sort_by?: ChannelSortBy
  sort_order?: ChannelSortOrder
  limit_filter?: ChannelLimitFilter
  p?: number
  page_size?: number
}

export interface ChannelTestParams {
  test_model?: string
}

export interface CopyChannelParams {
  suffix?: string
  reset_balance?: boolean
}

export interface MultiKeyManageParams {
  channel_id: number
  action:
    | 'get_key_status'
    | 'disable_key'
    | 'enable_key'
    | 'enable_all_keys'
    | 'disable_all_keys'
    | 'delete_key'
    | 'delete_disabled_keys'
  key_index?: number
  page?: number
  page_size?: number
  status?: number // 1=enabled, 2=manual_disabled, 3=auto_disabled
}

export interface BatchDeleteParams {
  ids: number[]
}

export interface BatchSetTagParams {
  ids: number[]
  tag: string | null
}

export interface TagOperationParams {
  tag: string
  new_tag?: string
  priority?: number
  weight?: number
  model_mapping?: string
  models?: string
  groups?: string
  // 每日金额上限：字段缺省 = 本次不修改；要清除上限必须显式传 0。
  daily_quota_limit?: number
}

/** 批量设置选中渠道的每日金额上限。语义同上：缺省 = 不修改。 */
export interface BatchDailyLimitParams {
  ids: number[]
  daily_quota_limit?: number
  daily_limit_auto_recover?: 0 | 1
  daily_limit_recover_minutes?: number
}

// ============================================================================
// Form Data Types
// ============================================================================

export interface ChannelFormData {
  name: string
  type: number
  base_url: string
  key: string
  openai_organization?: string
  models: string
  group: string
  model_mapping?: string
  priority?: number
  weight?: number
  test_model?: string
  auto_ban?: number
  status: number
  status_code_mapping?: string
  tag?: string
  remark?: string
  setting?: string
  param_override?: string
  header_override?: string
  settings?: string
  other?: string
  // Multi-key specific
  multi_key_mode?: 'single' | 'batch' | 'multi_to_single'
  multi_key_type?: 'random' | 'polling'
  batch_add_set_key_prefix_2_name?: boolean
}

// ============================================================================
// Add Channel Request (special structure)
// ============================================================================

export interface AddChannelRequest {
  mode: 'single' | 'batch' | 'multi_to_single'
  multi_key_mode?: 'random' | 'polling'
  batch_add_set_key_prefix_2_name?: boolean
  channel: Partial<Channel>
}

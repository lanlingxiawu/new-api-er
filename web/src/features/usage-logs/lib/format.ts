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
import type { StatusBadgeProps } from '@/components/status-badge'
import {
  BILLING_PRICING_VARS,
  normalizeTierLabel,
  parseTiersFromExpr,
  type ParsedTier,
} from '@/features/pricing/lib/billing-expr'

import type { UsageLog } from '../data/schema'
import type { LogOtherData } from '../types'

export { normalizeTierLabel }

const PARAM_OVERRIDE_ACTION_MAP: Record<string, string> = {
  set: 'Set',
  delete: 'Delete',
  copy: 'Copy',
  move: 'Move',
  append: 'Append',
  prepend: 'Prepend',
  trim_prefix: 'Trim Prefix',
  trim_suffix: 'Trim Suffix',
  ensure_prefix: 'Ensure Prefix',
  ensure_suffix: 'Ensure Suffix',
  trim_space: 'Trim Space',
  to_lower: 'To Lower',
  to_upper: 'To Upper',
  replace: 'Replace',
  regex_replace: 'Regex Replace',
  set_header: 'Set Header',
  delete_header: 'Delete Header',
  copy_header: 'Copy Header',
  move_header: 'Move Header',
  pass_headers: 'Pass Headers',
  sync_fields: 'Sync Fields',
  return_error: 'Return Error',
}

/**
 * Get localized label for a param override action
 */
export function getParamOverrideActionLabel(
  action: string,
  t: (key: string) => string
): string {
  const key = PARAM_OVERRIDE_ACTION_MAP[action.toLowerCase()]
  return key ? t(key) : action
}

/**
 * Parse a param override audit line into action and content
 */
export function parseAuditLine(
  line: string
): { action: string; content: string } | null {
  if (typeof line !== 'string') return null
  const firstSpace = line.indexOf(' ')
  if (firstSpace <= 0) return { action: line, content: line }
  return {
    action: line.slice(0, firstSpace),
    content: line.slice(firstSpace + 1),
  }
}

/**
 * Check if the log is a violation fee log
 */
export function isViolationFeeLog(other: LogOtherData | null): boolean {
  if (!other) return false
  return (
    other.violation_fee === true ||
    Boolean(other.violation_fee_code) ||
    Boolean(other.violation_fee_marker)
  )
}

function isPositiveFiniteNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0
}

function hasLegacySearchSurcharge(
  enabled: boolean | undefined,
  count: number | undefined,
  price: number | undefined
): boolean {
  return (
    enabled === true &&
    isPositiveFiniteNumber(count) &&
    isPositiveFiniteNumber(price)
  )
}

/**
 * Check whether a consume log includes an actual tool-call surcharge.
 * Structured surcharge items cover current logs, while the legacy fields keep
 * historical Web Search, File Search, and Image Generation logs visible.
 */
export function hasToolSurcharge(other: LogOtherData | null): boolean {
  if (!other) return false

  const hasStructuredSurcharge =
    Array.isArray(other.tool_surcharges) &&
    other.tool_surcharges.some(
      (item) =>
        typeof item?.name === 'string' &&
        item.name.trim() !== '' &&
        isPositiveFiniteNumber(item.count) &&
        isPositiveFiniteNumber(item.price)
    )
  if (hasStructuredSurcharge) return true

  if (
    hasLegacySearchSurcharge(
      other.web_search,
      other.web_search_call_count,
      other.web_search_price
    )
  ) {
    return true
  }

  if (
    hasLegacySearchSurcharge(
      other.file_search,
      other.file_search_call_count,
      other.file_search_price
    )
  ) {
    return true
  }

  return (
    other.image_generation_call === true &&
    isPositiveFiniteNumber(other.image_generation_call_price)
  )
}

/**
 * Parse the 'other' field from JSON string or already-decoded API payload.
 */
export function parseLogOther(other: unknown): LogOtherData | null {
  if (!other) return null
  if (typeof other === 'object') return other as LogOtherData
  if (typeof other !== 'string') return null
  try {
    return JSON.parse(other) as LogOtherData
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to parse log other field:', error)
    return null
  }
}

/**
 * Get time color based on duration (in seconds)
 */
export function getTimeColor(
  seconds: number
): 'success' | 'warning' | 'danger' {
  if (seconds < 10) return 'success'
  if (seconds < 30) return 'warning'
  return 'danger'
}

/**
 * Get first-response-token color based on latency (in seconds)
 */
export function getFirstResponseTimeColor(
  seconds: number
): 'success' | 'warning' | 'danger' {
  if (seconds < 5) return 'success'
  if (seconds < 10) return 'warning'
  return 'danger'
}

/**
 * Get throughput color based on generated tokens per second
 */
export function getThroughputColor(
  tokensPerSecond: number
): 'success' | 'warning' | 'danger' {
  if (tokensPerSecond >= 30) return 'success'
  if (tokensPerSecond >= 15) return 'warning'
  return 'danger'
}

/**
 * Get response color using throughput only when enough output tokens exist.
 */
export function getResponseTimeColor(
  seconds: number,
  completionTokens: number
): 'success' | 'warning' | 'danger' {
  if (completionTokens < 100 || seconds <= 0) return getTimeColor(seconds)
  return getThroughputColor(completionTokens / seconds)
}

/**
 * Format model name with mapping indicator
 */
export function formatModelName(log: UsageLog): {
  name: string
  isMapped: boolean
  actualModel?: string
} {
  const other = parseLogOther(log.other)
  const isMapped = !!(
    other?.is_model_mapped &&
    other?.upstream_model_name &&
    other.upstream_model_name !== ''
  )

  return {
    name: log.model_name,
    isMapped,
    actualModel: isMapped ? other.upstream_model_name : undefined,
  }
}

/**
 * Decode a base64-encoded billing expression. Safely returns an empty string
 * when the input is missing or malformed (e.g. legacy logs without expr_b64).
 */
export function decodeBillingExprB64(exprB64: string | undefined): string {
  if (!exprB64) return ''
  try {
    const binaryString =
      typeof window !== 'undefined'
        ? window.atob(exprB64)
        : Buffer.from(exprB64, 'base64').toString('binary')
    const bytes = new Uint8Array(binaryString.length)

    for (let i = 0; i < binaryString.length; i++) {
      bytes[i] = binaryString.charCodeAt(i)
    }

    if (typeof TextDecoder !== 'undefined') {
      return new TextDecoder().decode(bytes)
    }

    return decodeURIComponent(
      Array.prototype.map
        .call(bytes, (byte: number) => `%${  byte.toString(16).padStart(2, '0')}`)
        .join('')
    )
  } catch {
    return ''
  }
}

/**
 * Resolve which parsed tier corresponds to the matched_tier label in a log
 * entry. Missing or unknown labels do not fall back to another tier because
 * that would display guessed unit prices.
 */
export function resolveMatchedTier(
  tiers: ParsedTier[],
  matchedLabel: string | undefined
): ParsedTier | null {
  if (tiers.length === 0) return null
  if (!matchedLabel) return null
  const found = tiers.find((tier) => {
    const l1 = normalizeTierLabel(tier.label)
    const l2 = normalizeTierLabel(matchedLabel)
    return l1 === l2 && l1 !== ''
  })
  return found || null
}

/**
 * Tiered pricing summary derived from an `other` log payload using the
 * billing-expression library. Returns null when the entry is not a tiered
 * billing log or the expression failed to parse.
 */
export interface TieredBillingSummary {
  tiers: ParsedTier[]
  tier: ParsedTier
  priceEntries: Array<{ field: string; shortLabel: string; price: number }>
}

/**
 * Whether the request payload reports any cache-related token usage. Used to
 * suppress cache pricing rows from the tiered breakdown when the request did
 * not exercise the cache path (mirrors the classic frontend behaviour).
 */
export function hasAnyCacheTokens(
  other: LogOtherData | null | undefined
): boolean {
  if (!other) return false
  return (
    (other.cache_tokens || 0) > 0 ||
    (other.cache_creation_tokens || 0) > 0 ||
    (other.cache_creation_tokens_5m || 0) > 0 ||
    (other.cache_creation_tokens_1h || 0) > 0
  )
}

export function getTieredBillingSummary(
  other: LogOtherData | null
): TieredBillingSummary | null {
  if (!other || other.billing_mode !== 'tiered_expr') return null
  const exprStr = decodeBillingExprB64(other.expr_b64)
  if (!exprStr) return null
  const tiers = parseTiersFromExpr(exprStr)
  const tier = resolveMatchedTier(tiers, other.matched_tier)
  if (!tier) return null

  const cacheTokensPresent = hasAnyCacheTokens(other)

  const priceEntries: TieredBillingSummary['priceEntries'] = []
  for (const v of BILLING_PRICING_VARS) {
    if (!v.field) continue
    if (v.group === 'cache' && !cacheTokensPresent) continue
    const raw = tier[v.field as keyof ParsedTier]
    const price = Number(raw)
    if (Number.isFinite(price) && price > 0) {
      priceEntries.push({
        field: v.field,
        shortLabel: v.shortLabel,
        price,
      })
    }
  }
  return { tiers, tier, priceEntries }
}

/**
 * Calculate duration and return formatted result with color variant
 * @param submitTime - Submit timestamp
 * @param finishTime - Finish timestamp
 * @param unit - Unit of the timestamps ('seconds' or 'milliseconds')
 */
export function formatDuration(
  submitTime?: number,
  finishTime?: number,
  unit: 'seconds' | 'milliseconds' = 'milliseconds'
): { durationSec: number; variant: StatusBadgeProps['variant'] } | null {
  if (!submitTime || !finishTime) return null

  const durationSec =
    unit === 'milliseconds'
      ? (finishTime - submitTime) / 1000
      : finishTime - submitTime

  return { durationSec, variant: durationSec > 60 ? 'red' : 'green' }
}

/**
 * Maps a language-independent audit/login operation `action` to an i18n
 * template string (the template itself is the i18n key, with {{placeholders}}).
 *
 * The backend stores only `action` + structured `params` in `other.op`; the UI
 * renders localized content at display time so audit/login logs are fully
 * translatable instead of being frozen to whatever language was written to DB.
 *
 * User-targeted actions have two variants. `AUDIT_TEMPLATES` names the target
 * user through a single pre-composed `{{target}}` placeholder (see
 * `formatAuditTargetUser`), so it renders correctly whether the log carries a
 * username, an ID, or both. `AUDIT_TEMPLATES_NO_TARGET` keeps the wording these
 * actions had before target tracking existed, for rows that carry no target at
 * all. See `renderAuditContent`.
 */
const AUDIT_TEMPLATES: Record<string, string> = {
  login: 'Logged in successfully via {{method}}',
  // User management
  'user.create': 'Created user {{target}} (role {{role}})',
  'user.update': 'Updated user {{target}}',
  'user.delete': 'Deleted user {{target}}',
  'user.manage': 'Performed {{action}} on user {{target}}',
  'user.quota_add': 'Increased quota of user {{target}} by {{quota}}',
  'user.quota_subtract': 'Decreased quota of user {{target}} by {{quota}}',
  'user.quota_override':
    'Overrode quota of user {{target}} from {{from}} to {{to}}',
  'user.binding_clear': 'Cleared {{bindingType}} binding for user {{target}}',
  'user.2fa_disable':
    'Force-disabled two-factor authentication for user {{target}}',
  'user.passkey_register': 'Registered a passkey',
  'user.passkey_delete': 'Deleted a passkey',
  'user.topup_complete': 'Completed top-up order for user {{target}}',
  'user.reset_passkey': 'Reset the passkey of user {{target}}',
  'user.oauth_unbind': 'Removed an OAuth binding for user {{target}}',
  // System settings
  'option.update': 'Updated system setting {{key}}',
  'option.payment_compliance': 'Confirmed payment compliance',
  'option.reset_ratio': 'Reset model ratios',
  'option.clear_affinity_cache': 'Cleared channel affinity cache',
  // Custom OAuth
  'custom_oauth.create': 'Created a custom OAuth provider',
  'custom_oauth.update': 'Updated a custom OAuth provider',
  'custom_oauth.delete': 'Deleted a custom OAuth provider',
  // Performance / cache
  'performance.clear_disk_cache': 'Cleared disk cache',
  'performance.gc': 'Triggered garbage collection',
  'performance.clear_logs': 'Cleared log files',
  // Channel
  'channel.create': 'Created channel {{name}} (type {{type}}, count {{count}})',
  'channel.update': 'Updated channel {{name}} (ID: {{id}})',
  'channel.delete': 'Deleted channel {{name}} (ID: {{id}})',
  'channel.delete_batch': 'Batch deleted {{count}} channels',
  'channel.delete_disabled': 'Deleted all disabled channels ({{count}})',
  'channel.key_view': 'Viewed channel key {{name}} (ID: {{id}})',
  'channel.tag_disable': 'Disabled channels with tag {{tag}}',
  'channel.tag_enable': 'Enabled channels with tag {{tag}}',
  'channel.tag_edit': 'Edited channels with tag {{tag}}',
  'channel.tag_batch_set': 'Batch set tag for {{count}} channels',
  'channel.copy':
    'Copied channel (source ID: {{sourceId}}) to {{name}} (new ID: {{id}})',
  'channel.multi_key_manage':
    'Multi-key management {{action}} on channel (ID: {{id}})',
  'channel.upstream_apply':
    'Applied upstream model changes to channel (ID: {{id}})',
  'channel.upstream_apply_all':
    'Applied upstream model changes to {{count}} channels',
  // Redemption codes
  'redemption.create':
    'Created {{count}} redemption codes named {{name}} ({{quota}} each)',
  'redemption.update': 'Updated a redemption code',
  'redemption.delete': 'Deleted a redemption code',
  'redemption.delete_invalid': 'Deleted invalid redemption codes',
  // Prefill groups
  'prefill_group.create': 'Created a prefill group',
  'prefill_group.update': 'Updated a prefill group',
  'prefill_group.delete': 'Deleted a prefill group',
  // Vendors
  'vendor.create': 'Created a vendor',
  'vendor.update': 'Updated a vendor',
  'vendor.delete': 'Deleted a vendor',
  // Model metadata
  'model.create': 'Created a model',
  'model.update': 'Updated a model',
  'model.delete': 'Deleted a model',
  'model.sync_upstream': 'Synced upstream models',
  // Deployments
  'deployment.create': 'Created a deployment',
  'deployment.update': 'Updated a deployment',
  'deployment.delete': 'Deleted a deployment',
  // Subscriptions
  'subscription.plan_create': 'Created a subscription plan',
  'subscription.plan_update': 'Updated a subscription plan',
  'subscription.bind': 'Bound a subscription',
  'subscription.plan_reset': 'Reset active subscriptions for plan {{plan_id}}',
  'subscription.user_plan_reset':
    'Reset active plan {{plan_id}} subscriptions for user {{target}}',
  // Logs
  'log.clear': 'Cleared historical logs',
  // Usage log export (admin background jobs)
  'log_export.job_create':
    'Started a log export ({{columns}} columns, {{format}}) for {{start}}–{{end}}',
  'log_export.job_delete': 'Canceled or deleted a log export job',
  'log_export.template_create': 'Created a log export template',
  'log_export.template_update': 'Updated a log export template',
  'log_export.template_delete': 'Deleted a log export template',
  // Generic middleware fallback
  generic: '{{method}} {{route}}',
}

/**
 * Wording used for user-targeted actions logged before the backend recorded a
 * target user. Kept verbatim from the pre-change templates so historical rows
 * keep reading the way they always did; only actions that can lack a target
 * need an entry here.
 */
const AUDIT_TEMPLATES_NO_TARGET: Record<string, string> = {
  'user.create': 'Created user {{username}} (role {{role}})',
  'user.update': 'Updated user {{username}} (ID: {{id}})',
  'user.delete': 'Deleted user {{username}} (ID: {{id}})',
  'user.manage': 'Performed {{action}} on user {{username}} (ID: {{id}})',
  'user.quota_add': 'Increased user quota by {{quota}}',
  'user.quota_subtract': 'Decreased user quota by {{quota}}',
  'user.quota_override': 'Overrode user quota from {{from}} to {{to}}',
  'user.binding_clear': 'Cleared {{bindingType}} binding for user {{username}}',
  'user.2fa_disable': 'Force-disabled two-factor authentication for the user',
  'user.topup_complete': 'Completed top-up order for the user',
  'user.reset_passkey': 'Reset the user passkey',
  'user.oauth_unbind': 'Removed an OAuth binding for the user',
  'subscription.user_plan_reset':
    'Reset active plan {{plan_id}} subscriptions for the user',
}

/**
 * The user an admin operation was performed on, resolved from an audit log.
 *
 * Newer logs carry `target_user_id` / `target_username` directly. Older ones
 * only have the per-action `username` / `id` params, and middleware-fallback
 * rows only have the route parameter — we read all three so existing records
 * stay auditable.
 */
export type AuditTargetUser = { id?: number; username?: string }

/**
 * Render a target user for display: `alice (ID: 42)`, or just whichever half
 * the log actually recorded (middleware-fallback rows only carry the ID).
 */
export function formatAuditTargetUser(target: AuditTargetUser): string {
  if (target.username && target.id !== undefined) {
    return `${target.username} (ID: ${target.id})`
  }
  if (target.username) return target.username
  return `ID: ${target.id}`
}

function toPositiveInt(value: unknown): number | undefined {
  let n = Number.NaN
  if (typeof value === 'number') n = value
  else if (typeof value === 'string') n = Number(value)
  return Number.isInteger(n) && n > 0 ? n : undefined
}

function toNonEmptyString(value: unknown): string | undefined {
  return typeof value === 'string' && value !== '' ? value : undefined
}

/**
 * Whether an action operates on a user, and therefore whether its untyped
 * legacy `username` / `id` params refer to a user.
 *
 * This gate matters: resource actions use those same key names for their own
 * subject — `channel.update` stores the *channel* id in `params.id` — so
 * reading them unconditionally would report a channel as the target user.
 */
function isUserTargetedAction(action: string): boolean {
  return action.startsWith('user.') || action === 'subscription.user_plan_reset'
}

export function getAuditTargetUser(
  other: LogOtherData | null | undefined
): AuditTargetUser | null {
  const action = other?.op?.action
  if (!action) return null
  const params = other?.op?.params

  // Written by the backend only for user-targeted operations, so it is
  // trustworthy on its own — including on middleware-fallback rows whose
  // action is `generic`.
  let username = toNonEmptyString(params?.target_username)
  // audit_info.params.user_id 是**用户专属**的参数名（:user_id 只出现在用户路由上），
  // 含义不会随 action 改变，所以不受下面的 action 门槛限制 —— 中间件兜底记录的 action
  // 是 generic，门槛会让这条回退永远读不到，「被操作用户」那一行也就不会显示。
  let id =
    toPositiveInt(params?.target_user_id) ??
    toPositiveInt(other?.audit_info?.params?.user_id)

  // Logs written before target tracking existed only carry the per-action
  // params, and fallback rows only carry the route parameter. `id` 是模糊的
  // （channel.update 的 params.id 是渠道 ID），因此仍需按 action 收口。
  if (isUserTargetedAction(action)) {
    username ??= toNonEmptyString(params?.username)
    id ??= toPositiveInt(params?.id) ?? toPositiveInt(other?.audit_info?.params?.id)
  }

  if (id === undefined && username === undefined) return null
  return { id, username }
}

/**
 * Render the localized content of an audit/login log from its structured
 * `other.op` descriptor. Returns null when the log has no recognized action,
 * letting callers fall back to the raw `content` field.
 */
export function renderAuditContent(
  other: LogOtherData | null | undefined,
  t: (key: string, opts?: Record<string, unknown>) => string
): string | null {
  const op = other?.op
  if (!op?.action) return null

  // Normalize the target user so pre-target logs that carry `username`/`id`
  // still render the newer, more explicit wording.
  const target = getAuditTargetUser(other)
  const params: Record<string, unknown> = { ...op.params }
  if (target) params.target = formatAuditTargetUser(target)

  // Without a target, a `{{target}}` template would render a dangling
  // placeholder — fall back to the wording the action had before targets
  // were recorded.
  const template = target
    ? AUDIT_TEMPLATES[op.action]
    : (AUDIT_TEMPLATES_NO_TARGET[op.action] ?? AUDIT_TEMPLATES[op.action])
  if (!template) return null
  return t(template, params)
}

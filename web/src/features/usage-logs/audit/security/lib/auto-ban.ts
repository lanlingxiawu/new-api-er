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
import type { TFunction } from 'i18next'
import { z } from 'zod'

export const autoBanRuleSchema = z.object({
  id: z.enum([
    'multi_account_key_access',
    'multi_source_key_access',
    'new_source_channel_rewrite',
    'session_reuse',
    'risky_ip_login',
    'risky_ip_sensitive_operation',
    'new_source_sensitive_operation',
  ]),
  selected: z.boolean(),
  clear_api_keys: z.boolean().default(false),
  clear_access_tokens: z.boolean().default(false),
  login_window_seconds: z.number().optional(),
  window_seconds: z.number().optional(),
  login_accounts: z.number().optional(),
  key_accounts: z.number().optional(),
  login_ips: z.number().optional(),
  key_ips: z.number().optional(),
  channels: z.number().optional(),
})
export type AutoBanRule = z.infer<typeof autoBanRuleSchema>
export const defaultAutoBanRules: AutoBanRule[] = [
  {
    id: 'multi_account_key_access',
    selected: true,
    clear_api_keys: false,
    clear_access_tokens: false,
    login_window_seconds: 60,
    window_seconds: 300,
    login_accounts: 5,
    key_accounts: 3,
  },
  {
    id: 'multi_source_key_access',
    selected: true,
    clear_api_keys: false,
    clear_access_tokens: false,
    window_seconds: 300,
    login_ips: 5,
    key_ips: 3,
  },
  {
    id: 'new_source_channel_rewrite',
    selected: true,
    clear_api_keys: false,
    clear_access_tokens: false,
    window_seconds: 300,
    channels: 10,
  },
  {
    id: 'session_reuse',
    selected: true,
    clear_api_keys: false,
    clear_access_tokens: false,
  },
  ...(
    [
      'risky_ip_login',
      'risky_ip_sensitive_operation',
      'new_source_sensitive_operation',
    ] as const
  ).map((id) => ({
    id,
    selected: false,
    clear_api_keys: false,
    clear_access_tokens: false,
  })),
]
export const autoBanThresholds = {
  login_window_seconds: ['Sign-in window (seconds)', 10, 120],
  window_seconds: ['Detection window (seconds)', 60, 600],
  login_accounts: ['Minimum signed-in accounts', 3, 100],
  key_accounts: ['Minimum accounts viewing keys', 2, 100],
  login_ips: ['Minimum sign-in IPs', 3, 100],
  key_ips: ['Minimum IPs viewing keys', 2, 100],
  channels: ['Minimum changed channels', 5, 100],
} as const

export function parseAutoBanRules(value: string): AutoBanRule[] | undefined {
  try {
    const result = z
      .array(autoBanRuleSchema)
      .min(4)
      .max(7)
      .safeParse(JSON.parse(value))
    if (
      !result.success ||
      new Set(result.data.map((rule) => rule.id)).size !== result.data.length ||
      !defaultAutoBanRules
        .slice(0, 4)
        .every((required) =>
          result.data.some((rule) => rule.id === required.id)
        )
    ) {
      return undefined
    }
    return [
      ...result.data,
      ...defaultAutoBanRules
        .slice(4)
        .filter((rule) => !result.data.some((item) => item.id === rule.id))
        .map((rule) => ({ ...rule })),
    ]
  } catch {
    return undefined
  }
}

export function autoBanValidation(
  values: Record<string, string>,
  t: TFunction
): string | undefined {
  const rules = parseAutoBanRules(values.auto_ban_rules_json)
  if (!rules) return t('Invalid rule settings. Check them and try again.')
  if (values.auto_ban_enabled === 'true' && !rules.some(hasAutomaticActions)) {
    return t('Select at least one automatic action.')
  }
  if (
    values.auto_ban_enabled === 'true' &&
    values.capture_enabled !== 'true' &&
    rules.some(
      (rule) => rule.id === 'session_reuse' && hasAutomaticActions(rule)
    )
  ) {
    return t(
      'Enable sign-in security event capture to use the session replay rule.'
    )
  }
  if (
    values.auto_ban_enabled === 'true' &&
    rules.some(
      (rule) => rule.id.startsWith('risky_ip_') && hasAutomaticActions(rule)
    ) &&
    !hasAbuseLookup(values)
  ) {
    return t(
      'Enable IP lookup and configure an abuse data source before using high-risk IP rules.'
    )
  }
  for (const rule of rules) {
    const expected = defaultAutoBanRules.find((item) => item.id === rule.id)
    if (!expected) return t('Invalid rule settings. Check them and try again.')
    for (const [field, [label, min, max]] of Object.entries(
      autoBanThresholds
    )) {
      if (!(field in expected)) continue
      const value = rule[field as keyof typeof autoBanThresholds]
      if (
        value === undefined ||
        !Number.isInteger(value) ||
        value < min ||
        value > max
      ) {
        return t('{{field}} must be an integer from {{min}} to {{max}}.', {
          field: t(label),
          min,
          max,
        })
      }
    }
    if ((rule.key_accounts ?? 0) > (rule.login_accounts ?? 0)) {
      return t('Accounts viewing keys cannot exceed signed-in accounts.')
    }
    if ((rule.key_ips ?? 0) > (rule.login_ips ?? 0)) {
      return t('IPs viewing keys cannot exceed sign-in IPs.')
    }
    if ((rule.login_window_seconds ?? 0) > (rule.window_seconds ?? 0)) {
      return t(
        'The detection window cannot be shorter than the sign-in window.'
      )
    }
  }
}

export function hasAutomaticActions(rule: AutoBanRule): boolean {
  return rule.selected || rule.clear_api_keys || rule.clear_access_tokens
}

export function excludedAccountIDs(value: string): number[] | undefined {
  if (!value.trim()) return []
  const parts = value.split(',').map((part) => part.trim())
  if (
    parts.some(
      (part) => !/^[1-9]\d*$/.test(part) || !Number.isSafeInteger(Number(part))
    )
  ) {
    return undefined
  }
  return [...new Set(parts.map(Number))].sort((a, b) => a - b)
}

export function hasAbuseLookup(values: Record<string, string>): boolean {
  if (values.ip_intelligence_enabled !== 'true') return false
  try {
    const providers: unknown = JSON.parse(
      values.ip_intelligence_providers_json || '[]'
    )
    return (
      Array.isArray(providers) &&
      providers.length === 1 &&
      providers[0]?.enabled === true &&
      providers[0]?.type === 'custom_ipapi' &&
      providers[0]?.capabilities?.includes('abuse') === true
    )
  } catch {
    return false
  }
}

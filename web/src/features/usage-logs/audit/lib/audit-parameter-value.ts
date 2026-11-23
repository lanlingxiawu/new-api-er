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
import i18n, { type TFunction } from 'i18next'

import { getChannelTypeLabel } from '@/features/channels/lib/channel-utils'
import { loginMethodLabel } from '@/features/security/components/login-session-utils'
import { toIntlLocale } from '@/i18n/languages'
import { getServerErrorMessageKey } from '@/lib/server-error-message'

import { responseLabels } from '../security/lib/security-display'
import { auditFieldLabel } from './audit-field-labels'

// These objects contain user-defined names, model identifiers or protocol keys.
// Their keys and values are evidence, not UI labels.
export const auditLiteralMaps = new Set([
  'model_mapping',
  'status_code_mapping',
  'param_override',
  'header_override',
  'group_ratios',
  'group_ratio_overrides',
  'permissions',
  'models',
  'model_limits',
])

export function auditParameterValue(
  key: string,
  value: unknown,
  action: string,
  t: TFunction
): unknown {
  if (
    [
      'observed_at',
      'data_updated_at',
      'expires_at',
      'source_first_seen',
      'baseline_first_seen',
      'baseline_last_seen',
    ].includes(key) &&
    typeof value === 'number'
  ) {
    return new Date(value * 1000).toLocaleString(
      toIntlLocale(i18n.resolvedLanguage || i18n.language)
    )
  }
  if (key.startsWith('is_') && value === null) return t('Unknown')
  if (key === 'accuracy' && typeof value === 'string') {
    const accuracy: Record<string, string> = {
      VERY_HIGH: 'Very high',
      HIGH: 'High',
      MEDIUM: 'Medium',
      LOW: 'Low',
      VERY_LOW: 'Very low',
      UNKNOWN: 'Unknown',
    }
    return t(accuracy[value] ?? value)
  }
  if (
    (key === 'changed_fields' || key === 'submitted_fields') &&
    Array.isArray(value)
  ) {
    return (
      value
        .map((field) =>
          typeof field === 'string' ? auditFieldLabel(field, t) : field
        )
        .join(', ') || t('None')
    )
  }
  if (typeof value !== 'string' && typeof value !== 'number') return value
  if (key === 'outcome' && action === 'audit_security.disable_account') {
    return t(
      responseLabels[String(value)] ??
        'The result is not confirmed. Refresh first.'
    )
  }
  if (['role', 'target_role', 'actor_role', 'admin_role'].includes(key)) {
    const roles: Record<string, string> = {
      '0': t('Guest'),
      '1': t('User'),
      '10': t('Administrator'),
      '100': t('Super administrator'),
      guest: t('Guest'),
      user: t('User'),
      admin: t('Administrator'),
      root: t('Super administrator'),
    }
    return roles[String(value)] ?? value
  }
  if (
    key === 'type' &&
    action.startsWith('channel.') &&
    typeof value === 'number'
  ) {
    const label = getChannelTypeLabel(value)
    return label === 'Unknown' ? value : t(label)
  }
  if (
    key === 'status' ||
    ((key === 'from' || key === 'to') && action.includes('status_update'))
  ) {
    if (action.startsWith('channel.')) {
      const states: Record<string, string> = {
        '1': t('Enabled'),
        '2': t('Disabled'),
        '3': t('Auto Disabled'),
      }
      return states[String(value)] ?? value
    }
    if (action.startsWith('token.')) {
      const states: Record<string, string> = {
        '1': t('Enabled'),
        '2': t('Disabled'),
        '3': t('Expired'),
        '4': t('Exhausted'),
      }
      return states[String(value)] ?? value
    }
    if (action.startsWith('user.')) {
      const states: Record<string, string> = {
        '1': t('Enabled'),
        '2': t('Disabled'),
      }
      return states[String(value)] ?? value
    }
    const states: Record<string, string> = {
      open: t('Open'),
      investigating: t('Investigating'),
      resolved: t('Resolved'),
      false_positive: t('False positive'),
      pending: t('Pending'),
      sent: t('Sent'),
      failed: t('Failed'),
      cancelled: t('Cancelled'),
    }
    return states[String(value)] ?? value
  }
  if (typeof value !== 'string') return value
  if (key === 'non_stream_timeout_billing' && value === 'refund') {
    return t('Refund charges')
  }
  if (key === 'capability') {
    const capabilities: Record<string, string> = {
      geo: t('Geolocation'),
      vpn: t('VPN'),
      proxy: t('Proxy'),
      tor: t('Tor'),
      abuse: t('Abuse intelligence'),
    }
    return capabilities[value] ?? value
  }
  if (key === 'source') {
    const sources: Record<string, string> = {
      dbip_city: 'DB-IP City Lite',
      dbip_asn: 'DB-IP ASN Lite',
      drop_v4: 'Spamhaus DROP IPv4',
      drop_v6: 'Spamhaus DROP IPv6',
      tor: t('Tor exit list'),
      upstream_proxycheck: 'ProxyCheck',
    }
    return sources[value] ?? value
  }
  if (key === 'action') {
    const operations: Record<string, string> = {
      unlock_login: t('Unlock sign-in'),
      promote: t('Promote'),
      demote: t('Demote'),
      enable: t('Enable'),
      disable: t('Disable'),
      delete: t('Delete'),
      disable_key: t('Disable key'),
      enable_key: t('Enable key'),
      enable_all_keys: t('Enable all keys'),
      disable_all_keys: t('Disable all keys'),
      delete_key: t('Delete key'),
      delete_disabled_keys: t('Delete disabled keys'),
      add_quota: t('Adjust user quota'),
    }
    return operations[value] ?? value
  }
  if (key === 'mode' && action.startsWith('user.quota_')) {
    const modes: Record<string, string> = {
      add: t('Increase user quota'),
      subtract: t('Decrease user quota'),
      override: t('Override user quota'),
    }
    return modes[value] ?? value
  }
  if (
    key === 'method' ||
    key === 'verification_method' ||
    key === 'auth_method'
  ) {
    if (value === 'session') return t('Session')
    if (value === 'access_token') return t('Access Token')
    return loginMethodLabel(value, t)
  }
  if (key === 'bindingType' || key === 'binding_type' || key === 'provider') {
    const providers: Record<string, string> = {
      email: t('Email'),
      custom_oauth: t('Custom OAuth'),
      github: 'GitHub',
      discord: 'Discord',
      linuxdo: 'LinuxDO',
      oidc: 'OIDC',
      telegram: 'Telegram',
      wechat: t('WeChat'),
    }
    return providers[value] ?? value
  }
  if (
    (key === 'reason' || key === 'failure_reason') &&
    (action.startsWith('auth.') ||
      action.startsWith('access_token.') ||
      action.startsWith('user.quota_') ||
      action === 'customer.bind_blocked' ||
      key === 'failure_reason')
  ) {
    const reasons: Record<string, string> = {
      invalid_credentials: t('Invalid credentials'),
      invalid_code: t('Invalid code'),
      expired: t('Expired'),
      service_error: t('Service error'),
      permission_denied: t('Access denied.'),
      scope_denied: t('The access token lacks the required permission'),
      route_undeclared: t('Access tokens cannot use this feature'),
      session_required: t('This feature requires signing in to the dashboard'),
      channel_not_accessible: t('Channel access denied'),
      invalid_parameters: t('Invalid adjustment parameters'),
      target_not_found: t('Target user not found'),
      quota_limit_exceeded: t('Wallet quota limit exceeded'),
      database_error: t('Quota update failed'),
      mutual_invitation: t('Mutual invitation is not allowed'),
    }
    return reasons[value] ?? value
  }
  if (key === 'code') {
    const message = getServerErrorMessageKey({ code: value })
    return message ? t(message) : value
  }
  if (key === 'resource_type') {
    const resources: Record<string, string> = {
      channel: t('Channels'),
      user: t('Users'),
      account: t('Account'),
    }
    return resources[value] ?? value
  }
  return value
}

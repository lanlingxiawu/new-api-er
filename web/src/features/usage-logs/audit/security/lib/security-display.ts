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
import i18n from 'i18next'

import type { StatusVariant } from '@/components/status-badge'
import { toIntlLocale } from '@/i18n/languages'

import { alertStatuses } from './alert-form'

export function securityActorLabel(
  actor: { actor_id: number; actor_username?: string },
  fallback: string
) {
  if (actor.actor_id <= 0) return fallback
  if (actor.actor_username) {
    return `${actor.actor_username} (#${actor.actor_id})`
  }
  return `#${actor.actor_id}`
}

export function securityTargetLabel(target: {
  target_user_id?: number
  target_id?: number
  target_username?: string
}): string {
  return securityActorLabel(
    {
      actor_id: target.target_user_id ?? target.target_id ?? 0,
      actor_username: target.target_username,
    },
    '—'
  )
}

export function automaticBanMode(
  enabled: boolean,
  suspended: boolean,
  automatic: boolean
) {
  if (suspended) return 'Monitoring is paused. Automatic response is paused.'
  if (!enabled) return 'Monitoring is off. Automatic response is not running.'
  return automatic ? 'Automatic response enabled' : 'Alerts only'
}

export function securityActionLabel(action: string, source?: string): string {
  if (action === 'disable_account') {
    return source === 'automatic' ? 'Automatic ban' : 'Manual ban'
  }
  const labels: Record<string, string> = {
    stop_automatic_response: 'Stop this response',
    clear_api_keys: 'Automatic API key cleanup',
    clear_access_tokens: 'Automatic access token cleanup',
    ...alertStatuses,
    system_reopen: 'Reopened after a matching event',
    archive: 'Archived',
    archived: 'Archived',
    unarchived: 'Restored',
    investigation: 'Investigation history',
    unarchive: 'Restored',
    assign: 'Assignee',
    conclusion: 'Conclusion',
  }
  return labels[action] ?? action
}

export const responseLabels: Record<string, string> = {
  pending: 'Awaiting processing',
  committed: 'Banning...',
  succeeded: 'Account banned.',
  failed: 'Ban failed',
  skipped: 'Skipped',
  sync_pending: 'Ban status is syncing',
  partial: 'The result is not confirmed. Refresh first.',
  unknown: 'The result is not confirmed. Refresh first.',
  superseded: 'Account status has changed. Use the latest status.',
}

export const severities: Record<number, string> = {
  1: 'Medium',
  2: 'High',
  3: 'Critical',
}
export const severityVariants: Record<number, StatusVariant> = {
  1: 'info',
  2: 'warning',
  3: 'danger',
}
export const resources: Record<string, string> = {
  channel: 'Channels',
  user: 'Users',
  account: 'Account',
}
export const securityConclusions: Record<string, string> = {
  authorized: 'Authorized operation',
  expected: 'Expected behavior',
  malicious: 'Malicious activity',
  false_positive: 'False positive',
  other: 'Other',
}
export function securityEventResultKey(action: string, success: boolean) {
  if (action === 'auth.recovery_requested' && success) return 'Request received'
  return success ? 'Success' : 'Failed'
}

export function securityTime(value?: number | null) {
  return value
    ? new Date(value * 1000).toLocaleString(
        toIntlLocale(i18n.resolvedLanguage || i18n.language)
      )
    : '—'
}

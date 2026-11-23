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
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'

import type { SecurityAccountResponse } from './api'
import {
  responseLabels,
  securityActionLabel,
  securityTime,
} from './lib/security-display'

type AccountResponseView = Pick<
  SecurityAccountResponse,
  'id' | 'outcome' | 'action' | 'affected_count' | 'source' | 'created_at'
> & { reason_code?: string; reason?: string }

const responseReasons: Record<string, string> = {
  not_applicable: 'No automatic action applied',
  review_required: 'The result is not confirmed. Refresh first.',
  stopped: 'Further automatic actions stopped',
  sync_failed: 'The ban may not be fully active. Check the account manually.',
  unknown: 'The result is not confirmed. Refresh first.',
  superseded: 'Account status has changed. Use the latest status.',
  expired: 'The automatic response window has passed. Handle this manually.',
  incomplete_evidence: 'Not enough evidence. Review manually.',
  excluded_account: 'This account is outside the automatic ban scope.',
  disabled: 'Automatic bans or this rule are turned off.',
  already_processed: 'This event has already been handled.',
  already_disabled: 'This account is already banned.',
  protected_account: 'Super administrators cannot be banned.',
  forbidden: 'You cannot ban this account.',
  invalid_target: 'The target account could not be confirmed. Review manually.',
}

const cleanupLabels: Record<string, string> = {
  pending: 'Awaiting processing',
  clearing: 'Clearing...',
  sync_pending: 'Cleanup status is syncing',
  partial: 'Cleanup incomplete. Check the account in user management.',
  failed: 'Cleanup failed. Check the account in user management.',
  skipped: 'Skipped',
  superseded: 'Account changed. Remaining cleanup stopped.',
}
const cleanupReasons: Record<string, string> = {
  ...responseReasons,
  sync_failed: 'Cleanup incomplete. Check the account in user management.',
  superseded: 'Account changed. Remaining cleanup stopped.',
  excluded_account: 'This account is outside the automatic response scope.',
  disabled: 'Automatic response or this action is turned off.',
  protected_account:
    'Super administrators are excluded from automatic response.',
}

export function AccountResponseStatus({
  response,
  showReason = false,
}: {
  response?: Pick<
    AccountResponseView,
    'outcome' | 'reason_code' | 'reason' | 'action' | 'affected_count'
  > & { source?: string }
  showReason?: boolean
}) {
  const { t } = useTranslation()
  const cleanup =
    response?.action === 'clear_api_keys' ||
    response?.action === 'clear_access_tokens'
  let label = response
    ? ((cleanup ? cleanupLabels : responseLabels)[response.outcome] ??
      'The result is not confirmed. Refresh first.')
    : 'No automatic action applied'
  if (
    cleanup &&
    response?.outcome === 'succeeded' &&
    response.affected_count !== undefined
  ) {
    if (response.action === 'clear_api_keys') {
      label =
        response.affected_count === 0
          ? 'This user has no API keys to clear.'
          : 'API keys cleared: {{count}}'
    } else {
      label =
        response.affected_count === 0
          ? 'This user has no access tokens to clear.'
          : 'Access tokens cleared: {{count}}'
    }
  }
  const reasons = cleanup ? cleanupReasons : responseReasons
  const reasonCode = response?.reason ?? response?.reason_code
  const reason =
    reasonCode === 'protected_account' && response?.source === 'automatic'
      ? 'Super administrators are excluded from automatic response.'
      : reasons[reasonCode ?? '']
  let variant: 'danger' | 'warning' | 'neutral' | 'success' = 'neutral'
  if (response?.outcome === 'succeeded') {
    variant = cleanup ? 'success' : 'danger'
  } else if (
    response &&
    ['partial', 'unknown', 'failed'].includes(response.outcome)
  ) {
    variant = 'warning'
  }
  return (
    <div className='space-y-1'>
      <StatusBadge
        copyable={false}
        variant={variant}
        className='h-auto min-h-5 max-w-full whitespace-normal'
      >
        {t(label, { count: response?.affected_count })}
      </StatusBadge>
      {showReason && reason && reason !== label && (
        <p className='text-muted-foreground text-xs'>{t(reason)}</p>
      )}
      {showReason &&
        cleanup &&
        response?.outcome !== 'succeeded' &&
        response?.affected_count !== undefined && (
          <p className='text-muted-foreground text-xs'>
            {t(
              response.action === 'clear_api_keys'
                ? 'API keys cleared: {{count}}'
                : 'Access tokens cleared: {{count}}',
              { count: response.affected_count }
            )}
          </p>
        )}
    </div>
  )
}

export function AccountResponses({
  response,
  responses,
  showReason = false,
}: {
  response?: AccountResponseView
  responses?: AccountResponseView[]
  showReason?: boolean
}) {
  const { t } = useTranslation()
  let items = responses ?? []
  if (!items.length && response) items = [response]
  if (!items.length) return <AccountResponseStatus />
  return (
    <div className='min-w-0 space-y-3'>
      {items.map((item) => (
        <div key={item.id} className='min-w-0 space-y-1'>
          <p className='text-muted-foreground text-xs'>
            {t(
              securityActionLabel(item.action ?? 'disable_account', item.source)
            )}
          </p>
          <AccountResponseStatus response={item} showReason={showReason} />
          {showReason && (
            <p className='text-muted-foreground text-xs'>
              {securityTime(item.created_at)}
            </p>
          )}
        </div>
      ))}
    </div>
  )
}

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
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'

import {
  DetailRow,
  DetailSection,
} from '../../components/dialogs/log-detail-layout'
import { renderAuditContent } from '../../lib/format'
import { auditActionLabel } from '../lib/audit-action-labels'
import {
  securityEventResultKey,
  securityTime,
} from '../security/lib/security-display'
import { securityRules } from '../security/lib/security-rules'
import { SecurityRecordPages } from '../security/security-record-pages'
import { selfSecurityGet, type SelfSecurityDetail } from './api'

const responseActions: Record<string, string> = {
  disable_account: 'Ban account',
  clear_api_keys: 'Clear API keys',
  clear_access_tokens: 'Clear access tokens',
}
const responseOutcomes: Record<string, string> = {
  pending: 'Awaiting processing',
  committed: 'Processing...',
  sync_pending: 'Processing...',
  clearing: 'Processing...',
  succeeded: 'Completed',
  failed: 'Failed',
  skipped: 'Skipped',
}

export function SelfSecurityAlertDialog(props: {
  userID: number
  id: number
  onClose: () => void
}) {
  const { t } = useTranslation()
  const detail = useQuery({
    queryKey: ['self-security', props.userID, 'detail', props.id],
    queryFn: ({ signal }) =>
      selfSecurityGet<SelfSecurityDetail>(`alerts/${props.id}`, signal),
    enabled: props.userID > 0,
    gcTime: 0,
    retry: false,
    meta: { errorToast: false },
  })
  const data = detail.isError ? undefined : detail.data
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Security alert')}
      contentHeight='min(68dvh, 42.5rem)'
      contentClassName='max-h-[94dvh] overflow-y-hidden sm:max-w-2xl'
      footerClassName='flex-row flex-wrap'
      footer={
        <>
          <Button
            variant='outline'
            disabled={detail.isFetching}
            onClick={() => void detail.refetch()}
          >
            {t('Refresh')}
          </Button>
          <Button variant='outline' onClick={props.onClose}>
            {t('Close')}
          </Button>
        </>
      }
    >
      {detail.isPending && <LoadingState />}
      {detail.isError && (
        <ErrorState
          title={t('Failed to load audit records')}
          onRetry={() => void detail.refetch()}
        />
      )}
      {data && (
        <div className='space-y-4'>
          <DetailSection
            label={t('Overview')}
            contentClassName='space-y-2 rounded-none border-0 bg-transparent p-0 max-sm:p-0'
          >
            <DetailRow
              label={t('Matched rule')}
              value={t(securityRules[data.alert.rule] ?? 'Security alert')}
            />
            <DetailRow
              label={t('First event')}
              value={securityTime(data.alert.first_seen)}
            />
            <DetailRow
              label={t('Latest event')}
              value={securityTime(data.alert.last_seen)}
            />
          </DetailSection>
          <DetailSection
            label={t('Event')}
            contentClassName='space-y-2 rounded-none border-0 bg-transparent p-0 max-sm:p-0'
          >
            {!data.events.length && (
              <p className='text-muted-foreground text-xs'>{t('No records')}</p>
            )}
            <SecurityRecordPages items={data.events}>
              {(events) => (
                <div className='divide-y'>
                  {events.map((event, index) => (
                    <div
                      // eslint-disable-next-line react/no-array-index-key -- Read-only redacted snapshots have no public event identifier or per-row state.
                      key={`${event.at}:${event.action}:${index}`}
                      className='flex flex-col gap-1 py-3 text-sm first:pt-0 last:pb-0'
                    >
                      <div className='flex flex-wrap items-center justify-between gap-2'>
                        <p className='font-medium'>
                          {auditActionLabel(event.action, t) ??
                            renderAuditContent(
                              { op: { action: event.action, params: {} } },
                              t
                            ) ??
                            t('Unknown')}
                        </p>
                        <Badge
                          variant={
                            securityEventResultKey(
                              event.action,
                              event.success
                            ) === 'Failed'
                              ? 'destructive'
                              : 'secondary'
                          }
                        >
                          {t(
                            securityEventResultKey(event.action, event.success)
                          )}
                        </Badge>
                      </div>
                      <DetailRow
                        label={t('Event time')}
                        value={securityTime(event.at)}
                      />
                      <DetailRow
                        label='IP'
                        value={event.ip || t('Not recorded')}
                        mono
                      />
                    </div>
                  ))}
                </div>
              )}
            </SecurityRecordPages>
          </DetailSection>
          <DetailSection
            label={t('Response result')}
            contentClassName='space-y-2 rounded-none border-0 bg-transparent p-0 max-sm:p-0'
          >
            {!data.responses.length && (
              <p className='text-muted-foreground text-xs'>{t('No records')}</p>
            )}
            <SecurityRecordPages items={data.responses}>
              {(responses) => (
                <div className='divide-y'>
                  {responses.map((response) => (
                    <div
                      key={response.id}
                      className='flex flex-col gap-1 py-3 text-sm first:pt-0 last:pb-0'
                    >
                      <p className='font-medium'>
                        {t(responseActions[response.action] ?? 'Unknown')}
                      </p>
                      <DetailRow
                        label={t('Status')}
                        value={t(
                          responseOutcomes[response.outcome] ??
                            'The result is not confirmed. Refresh first.'
                        )}
                      />
                      <DetailRow
                        label={t('Event time')}
                        value={securityTime(response.created_at)}
                      />
                      {response.affected_count !== undefined &&
                        response.action === 'clear_api_keys' && (
                          <p className='text-xs'>
                            {t('API keys cleared: {{count}}', {
                              count: response.affected_count,
                            })}
                          </p>
                        )}
                      {response.affected_count !== undefined &&
                        response.action === 'clear_access_tokens' && (
                          <p className='text-xs'>
                            {t('Access tokens cleared: {{count}}', {
                              count: response.affected_count,
                            })}
                          </p>
                        )}
                    </div>
                  ))}
                </div>
              )}
            </SecurityRecordPages>
          </DetailSection>
        </div>
      )}
    </Dialog>
  )
}

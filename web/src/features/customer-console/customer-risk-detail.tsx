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
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { AUDIT_ACTIVITY_VERSION } from '@/features/usage-logs/audit/api'
import { activityLabel } from '@/features/usage-logs/audit/lib/activity'
import {
  AccountResponseStatus,
  AccountResponses,
} from '@/features/usage-logs/audit/security/account-response-status'
import { AlertConfirmButton } from '@/features/usage-logs/audit/security/alert-confirm-button'
import { alertStatuses } from '@/features/usage-logs/audit/security/lib/alert-form'
import {
  securityConclusions,
  securityTime,
  severities,
  severityVariants,
} from '@/features/usage-logs/audit/security/lib/security-display'
import { securityRules } from '@/features/usage-logs/audit/security/lib/security-rules'
import { SecurityRecordPages } from '@/features/usage-logs/audit/security/security-record-pages'
import {
  DetailRow,
  DetailSection,
} from '@/features/usage-logs/components/dialogs/log-detail-layout'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { useAuthStore } from '@/stores/auth-store'

import {
  customerAuditGet,
  isCustomerAccessDenied,
  type CustomerSecurityDetail,
  type CustomerSecurityEvent,
} from './audit-api'

export function CustomerRiskDetail(props: {
  customerID: number
  alertID: number
  accountName?: string
  onClose: () => void
  onAccessDenied: () => void
  onConfirmed?: () => void
  onInvestigate: (event: CustomerSecurityEvent) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const userID = useAuthStore((state) => state.auth.user?.id)
  const query = useQuery({
    queryKey: [
      'customer-audit',
      userID,
      props.customerID,
      'detail',
      props.alertID,
      AUDIT_ACTIVITY_VERSION,
    ],
    queryFn: ({ signal }) =>
      customerAuditGet<CustomerSecurityDetail>(
        props.customerID,
        `security/alerts/${props.alertID}`,
        signal
      ),
    gcTime: 0,
    retry: false,
    refetchOnWindowFocus: false,
    meta: { errorToast: false },
  })
  const denied = isCustomerAccessDenied(query.error)
  const onAccessDenied = props.onAccessDenied
  useEffect(() => {
    if (denied) onAccessDenied()
  }, [denied, onAccessDenied])
  const data = query.isError ? undefined : query.data
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={`${t('Security alert')} #${props.alertID}`}
      description={props.accountName}
      contentClassName='w-[calc(100vw-1.5rem)] max-w-[calc(100vw-1.5rem)] sm:max-w-3xl'
      contentHeight='min(60dvh, 30rem)'
      scrollAreaClassName='[scrollbar-gutter:stable]'
      footer={
        <>
          <Button
            variant='outline'
            disabled={query.isFetching}
            onClick={() => void query.refetch()}
          >
            {t('Refresh')}
          </Button>
          <Button variant='outline' onClick={props.onClose}>
            {t('Close')}
          </Button>
        </>
      }
    >
      {query.isPending && <LoadingState />}
      {query.isError && !denied && (
        <ErrorState
          title={t('Failed to load audit records')}
          onRetry={() => void query.refetch()}
        />
      )}
      {data && (
        <>
          <div className='mb-4 flex flex-wrap items-center justify-between gap-3'>
            <div>
              <p className='font-medium'>
                {t(securityRules[data.alert.rule] ?? data.alert.rule)}
              </p>
              <p className='text-muted-foreground text-sm'>
                <StatusBadge
                  copyable={false}
                  variant={severityVariants[data.alert.severity] ?? 'neutral'}
                >
                  {t(severities[data.alert.severity] ?? 'Unknown')}
                </StatusBadge>{' '}
                · {securityTime(data.alert.last_seen)}
              </p>
            </div>
            <AlertConfirmButton
              alert={data.alert}
              customerID={props.customerID}
              disabled={query.isFetching}
              onAccessDenied={props.onAccessDenied}
              onConfirmed={props.onConfirmed}
            />
          </div>
          <Tabs defaultValue='events'>
            <TabsList>
              <TabsTrigger value='events'>{t('Evidence')}</TabsTrigger>
              <TabsTrigger value='overview'>{t('Overview')}</TabsTrigger>
              <TabsTrigger value='history'>{t('Handling history')}</TabsTrigger>
            </TabsList>
            <TabsContent value='overview' className='space-y-3 pt-3'>
              <DetailSection label={t('Response result')}>
                <AccountResponses
                  response={data.alert.response}
                  responses={data.alert.responses}
                  showReason
                />
              </DetailSection>
              <DetailSection label={t('Overview')}>
                <DetailRow
                  label={t('First event time')}
                  value={securityTime(data.alert.first_seen)}
                />
                <DetailRow
                  label={t('Latest event')}
                  value={securityTime(data.alert.last_seen)}
                />
                <DetailRow
                  label={t('Conclusion')}
                  value={t(
                    securityConclusions[data.alert.conclusion] ?? 'Not recorded'
                  )}
                />
                <DetailRow
                  label={t('Occurrences')}
                  value={formatNumber(data.alert.occurrences, locale)}
                />
              </DetailSection>
            </TabsContent>
            <TabsContent value='events' className='space-y-3 pt-3'>
              {data.events.length === 0 && (
                <EmptyState title={t('No related events')} />
              )}
              <SecurityRecordPages items={data.events}>
                {(events) =>
                  events.map((event) => (
                    <DetailSection
                      key={event.event_id}
                      label={activityLabel(event, t)}
                    >
                      <DetailRow
                        label={t('Time')}
                        value={securityTime(event.at)}
                      />
                      <DetailRow
                        label='IP'
                        value={
                          <span className='inline-flex items-center gap-1'>
                            {event.ip || '—'}
                            {event.ip && <CopyButton value={event.ip} />}
                          </span>
                        }
                      />
                      <DetailRow
                        label={t('Result')}
                        value={
                          <StatusBadge
                            copyable={false}
                            variant={event.success ? 'success' : 'danger'}
                          >
                            {event.success ? t('Success') : t('Failed')}
                          </StatusBadge>
                        }
                      />
                      <div className='flex justify-end gap-2'>
                        <Button
                          variant='link'
                          size='sm'
                          onClick={() => props.onInvestigate(event)}
                        >
                          {t('View related audit')}
                        </Button>
                      </div>
                    </DetailSection>
                  ))
                }
              </SecurityRecordPages>
            </TabsContent>
            <TabsContent value='history' className='space-y-3 pt-3'>
              <p className='text-muted-foreground text-xs'>
                {t('Showing the latest 100 handling records.')}
              </p>
              {data.actions.length === 0 && (
                <EmptyState title={t('No handling history')} />
              )}
              <SecurityRecordPages items={data.actions}>
                {(actions) =>
                  actions.map((action) => (
                    <DetailSection
                      key={action.id}
                      label={securityTime(action.created_at)}
                    >
                      <DetailRow
                        label={t('Source')}
                        value={t(
                          {
                            employee: 'Employee',
                            admin: 'Administrator',
                            system: 'System',
                          }[action.source]
                        )}
                      />
                      <DetailRow
                        label={t('Handling status')}
                        value={`${t(alertStatuses[action.from_status as keyof typeof alertStatuses] ?? 'Not recorded')} → ${t(alertStatuses[action.to_status as keyof typeof alertStatuses] ?? 'Not recorded')}`}
                      />
                      {action.conclusion && (
                        <DetailRow
                          label={t('Conclusion')}
                          value={t(
                            securityConclusions[action.conclusion] ??
                              'Not recorded'
                          )}
                        />
                      )}
                      {action.outcome && (
                        <AccountResponseStatus
                          response={{
                            action: action.action,
                            source: action.source,
                            affected_count: action.affected_count,
                            outcome: action.outcome,
                            reason: action.reason,
                          }}
                          showReason
                        />
                      )}
                      {action.note && (
                        <p className='text-sm break-words whitespace-pre-wrap'>
                          {action.note}
                        </p>
                      )}
                    </DetailSection>
                  ))
                }
              </SecurityRecordPages>
            </TabsContent>
          </Tabs>
        </>
      )}
    </Dialog>
  )
}

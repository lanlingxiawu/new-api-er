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
import { ChevronDown } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { hasPermission } from '@/lib/admin-permissions'
import { useAuthStore } from '@/stores/auth-store'

import {
  DetailRow,
  DetailSection,
} from '../../components/dialogs/log-detail-layout'
import { AuditDetailFields } from '../components/audit-detail-fields'
import { auditFieldLabel } from '../lib/audit-field-labels'
import { AccountResponseSection } from './account-response-section'
import { AccountResponseStatus } from './account-response-status'
import { AlertAssignees } from './alert-assignees'
import { AlertConfirmButton } from './alert-confirm-button'
import { AlertStatusCell } from './alert-status-cell'
import { securityGet, type SecurityDetail, type SecurityEvent } from './api'
import { alertStatuses } from './lib/alert-form'
import { canConfirmRepeatSnapshot } from './lib/repeat-operation'
import {
  securityTime,
  securityConclusions,
  securityActionLabel,
  securityTargetLabel,
  securityActorLabel,
  severities,
  severityVariants,
} from './lib/security-display'
import { securityRules } from './lib/security-rules'
import { SecurityAlertCleanup } from './security-alert-cleanup'
import { SecurityEvidence } from './security-evidence'
import { SecurityRecordPages } from './security-record-pages'
import { SecurityRelations } from './security-relations'
import { SecurityRuleSummary } from './security-rule-summary'

const deliveryLabels: Record<string, string> = {
  account_response: 'Awaiting processing',
  sent: 'Sent',
  failed: 'Failed',
  cancelled: 'Cancelled',
  pending: 'Pending',
  skipped: 'Skipped',
  notification_disabled: 'Notifications disabled',
  cooldown: 'Notification cooldown',
  historical: 'Historical event',
  rate_limited: 'Notification limit reached',
  hourly_limit: 'Notification limit reached',
  delivery_failed: 'Delivery failed',
  no_destination: 'No notification destination',
  destination_removed: 'Destination removed',
  historical_event: 'Historical event',
  disabled: 'Notifications disabled',
  backfill: 'Historical event',
}

type SecurityAlertDialogProps = {
  id: number
  onClose: () => void
  onDeleted?: () => void
  onProcessed?: () => void
  onInvestigate?: (event: SecurityEvent) => void
}

export function SecurityAlertDialog(props: SecurityAlertDialogProps) {
  const [activeId, setActiveId] = useState(props.id)
  const user = useAuthStore((state) => state.auth.user)
  return (
    <SecurityAlertDetail
      key={`${user?.id}:${user?.role}:${JSON.stringify(user?.permissions)}:${activeId}`}
      {...props}
      activeId={activeId}
      onSelect={setActiveId}
    />
  )
}

function SecurityAlertDetail(
  props: SecurityAlertDialogProps & {
    activeId: number
    onSelect: (id: number) => void
  }
) {
  const { t } = useTranslation()
  const activeId = props.activeId
  const setActiveId = props.onSelect
  const user = useAuthStore((state) => state.auth.user)
  const accessScope = JSON.stringify([
    user?.id,
    user?.role,
    user?.permissions,
    activeId,
  ])
  const detail = useQuery({
    queryKey: [
      'audit-security',
      user?.id,
      user?.role,
      user?.permissions,
      'detail',
      activeId,
    ],
    queryFn: ({ signal }) =>
      securityGet<SecurityDetail>(`alerts/${activeId}`, signal),
    gcTime: 0,
    retry: false,
    refetchInterval: 15000,
  })
  const [snapshot, setSnapshot] = useState<{
    scope: string
    data: SecurityDetail
  }>()
  const [loadedAt, setLoadedAt] = useState(0)
  useEffect(() => {
    if ((!snapshot || snapshot.scope !== accessScope) && detail.data) {
      setSnapshot({ scope: accessScope, data: detail.data })
      setLoadedAt(detail.dataUpdatedAt)
    }
  }, [snapshot, accessScope, detail.data, detail.dataUpdatedAt])
  const data = snapshot?.scope === accessScope ? snapshot.data : detail.data
  const changed =
    data &&
    detail.data &&
    (data.alert.version !== detail.data.alert.version ||
      JSON.stringify(data.deliveries) !==
        JSON.stringify(detail.data.deliveries))
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={
        <>
          {t('Security alert')} #{activeId}
        </>
      }
      contentClassName='max-h-[94dvh] overflow-y-hidden sm:max-w-4xl lg:max-w-6xl'
      contentHeight='min(68dvh, 48rem)'
      footerClassName='flex-row flex-wrap items-center'
      footer={
        <>
          {data &&
            !detail.isError &&
            user &&
            hasPermission(user, 'audit_security', 'delete') && (
              <div className='me-auto flex items-center'>
                <SecurityAlertCleanup
                  alert={data.alert}
                  onDeleted={() => {
                    props.onDeleted?.()
                    props.onClose()
                  }}
                />
              </div>
            )}
          <Button
            variant='outline'
            disabled={detail.isFetching}
            onClick={async () => {
              const result = await detail.refetch()
              if (result.data) {
                setSnapshot({ scope: accessScope, data: result.data })
                setLoadedAt(result.dataUpdatedAt)
              }
            }}
          >
            {t('Refresh')}
          </Button>
          {data &&
            !detail.isError &&
            user &&
            hasPermission(user, 'audit_security', 'manage') && (
              <AlertConfirmButton
                alert={data.alert}
                size='default'
                disabled={
                  !!changed &&
                  (!detail.data ||
                    !canConfirmRepeatSnapshot(data.alert, detail.data.alert))
                }
                onConfirmed={() => {
                  props.onProcessed?.()
                  void detail
                    .refetch({ cancelRefetch: false })
                    .then((result) => {
                      if (result.data) {
                        setSnapshot({ scope: accessScope, data: result.data })
                        setLoadedAt(result.dataUpdatedAt)
                      }
                    })
                }}
              />
            )}
          <Button variant='outline' onClick={props.onClose}>
            {t('Close')}
          </Button>
        </>
      }
      bodyClassName='flex h-full min-h-0 flex-col gap-4'
      scrollAreaClassName='overflow-y-hidden'
    >
      {activeId !== props.id && (
        <Button
          variant='outline'
          size='sm'
          onClick={() => setActiveId(props.id)}
        >
          {t('Back')}
        </Button>
      )}
      {detail.isPending && !data && <LoadingState />}
      {detail.isError && (
        <ErrorState
          title={t(
            'Unable to load security monitoring. Check settings and retry.'
          )}
          onRetry={() => void detail.refetch()}
        />
      )}
      {data && !detail.isError && (
        <>
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <div className='flex flex-wrap items-center gap-2'>
              <StatusBadge
                copyable={false}
                variant={severityVariants[data.alert.severity] ?? 'neutral'}
              >
                {t(severities[data.alert.severity] ?? 'Unknown')}
              </StatusBadge>
              <span className='text-sm font-medium'>
                {t(securityRules[data.alert.rule] ?? data.alert.rule)}
              </span>
            </div>
            <p className='text-muted-foreground text-xs'>
              {t('Loaded at')}: {loadedAt ? securityTime(loadedAt / 1000) : '—'}
            </p>
          </div>
          {changed && (
            <Alert>
              <AlertDescription>
                {t(
                  'The alert or delivery records have changed. Refresh to load the latest details.'
                )}
              </AlertDescription>
            </Alert>
          )}
          <Tabs defaultValue='overview' className='min-h-0 flex-1 gap-0'>
            <TabsList
              className='z-10 mb-3 grid w-full shrink-0 grid-cols-2 group-data-horizontal/tabs:h-auto sm:flex sm:flex-nowrap'
              aria-label={t('Alert details')}
            >
              <TabsTrigger value='overview'>{t('Overview')}</TabsTrigger>
              <TabsTrigger value='evidence'>{t('Evidence')}</TabsTrigger>
              <TabsTrigger value='history'>
                {t('Investigation history')}
              </TabsTrigger>
              <TabsTrigger value='notifications'>
                {t('Notifications')}
              </TabsTrigger>
            </TabsList>
            <TabsContent
              keepMounted
              value='overview'
              className='min-h-0 [scrollbar-gutter:stable] space-y-4 overflow-y-auto overscroll-contain'
            >
              <AccountResponseSection
                data={data}
                stale={!!changed || detail.isFetching}
                onRefresh={async () => {
                  const result = await detail.refetch()
                  if (result.data) {
                    setSnapshot({ scope: accessScope, data: result.data })
                    setLoadedAt(result.dataUpdatedAt)
                  }
                }}
              />
              <DetailSection
                size='sm'
                label={t('Overview')}
                contentClassName='grid gap-x-6 gap-y-3 md:grid-cols-2'
              >
                <DetailRow
                  size='sm'
                  label={t('Severity')}
                  value={t(
                    (
                      { 1: 'Medium', 2: 'High', 3: 'Critical' } as Record<
                        number,
                        string
                      >
                    )[data.alert.severity] ?? 'Not recorded'
                  )}
                />
                <DetailRow
                  size='sm'
                  label={t('Handling status')}
                  value={t(
                    alertStatuses[
                      data.alert.status as keyof typeof alertStatuses
                    ] ?? 'Not recorded'
                  )}
                />
                <DetailRow
                  size='sm'
                  label={t('Operator')}
                  value={securityActorLabel(
                    data.alert,
                    t('Unidentified account')
                  )}
                />
                {data.alert.attempted_account && (
                  <DetailRow
                    size='sm'
                    label={auditFieldLabel(
                      'attempted_account',
                      t,
                      data.events.find(
                        (item) =>
                          item.action === 'auth.recovery_requested' ||
                          item.action === 'auth.recovery_rejected'
                      )?.action ?? data.alert.rule
                    )}
                    value={data.alert.attempted_account}
                  />
                )}
                {!!data.alert.target_user_id && (
                  <DetailRow
                    size='sm'
                    label={t('Target account')}
                    value={securityTargetLabel(data.alert)}
                  />
                )}
                <DetailRow
                  size='sm'
                  label={t('Resource')}
                  value={t(
                    (
                      {
                        channel: 'Channels',
                        user: 'Users',
                        account: 'Account',
                      } as Record<string, string>
                    )[data.alert.resource_type] ?? 'Not recorded'
                  )}
                />
                <DetailRow
                  size='sm'
                  label={t('First event')}
                  value={securityTime(data.alert.first_seen)}
                />
                <DetailRow
                  size='sm'
                  label={t('Latest event')}
                  value={securityTime(data.alert.last_seen)}
                />
                <DetailRow
                  size='sm'
                  label={t('Last updated')}
                  value={securityTime(data.alert.updated_at)}
                />
                <DetailRow
                  size='sm'
                  label={t('Closed at')}
                  value={securityTime(data.alert.closed_at)}
                />
                <DetailRow
                  size='sm'
                  label={t('Assignee')}
                  value={
                    <AlertAssignees
                      assignees={data.alert.assignees}
                      legacyID={data.alert.assignee_id}
                    />
                  }
                />
                <DetailRow
                  size='sm'
                  label={t('Conclusion')}
                  value={t(
                    securityConclusions[data.alert.conclusion ?? ''] ??
                      'Not recorded'
                  )}
                />
                {data.alert.conclusion_note && (
                  <DetailRow
                    size='sm'
                    label={t('Investigation note')}
                    value={data.alert.conclusion_note}
                  />
                )}
                <DetailRow
                  size='sm'
                  label={t('Archived at')}
                  value={securityTime(data.alert.archived_at)}
                />
              </DetailSection>
              <SecurityRuleSummary
                alert={data.alert}
                snapshot={data.findings?.[0]}
              />
            </TabsContent>
            <TabsContent
              keepMounted
              value='evidence'
              className='min-h-0 [scrollbar-gutter:stable] space-y-4 overflow-y-auto overscroll-contain'
            >
              <SecurityRuleSummary
                compact
                alert={data.alert}
                snapshot={data.findings?.[0]}
              />
              <SecurityEvidence
                events={data.events}
                ipMarkers={data.ip_markers}
                onSelectAlert={setActiveId}
                onInvestigate={props.onInvestigate}
              />
              <Collapsible>
                <CollapsibleTrigger
                  render={
                    <Button
                      type='button'
                      variant='ghost'
                      size='sm'
                      className='group'
                    />
                  }
                >
                  {t('Rule match snapshots')}
                  <ChevronDown
                    aria-hidden
                    className='size-3.5 group-data-[panel-open]:rotate-180'
                  />
                </CollapsibleTrigger>
                <CollapsibleContent className='pt-2'>
                  <DetailSection size='sm' label={t('Rule match snapshots')}>
                    {!data.findings?.length && (
                      <p className='text-muted-foreground text-xs'>
                        {t('No records')}
                      </p>
                    )}
                    <SecurityRecordPages items={data.findings ?? []}>
                      {(records) =>
                        records.map((finding) => (
                          <details
                            key={finding.id}
                            className='space-y-1 border-b py-2 last:border-b-0'
                          >
                            <summary className='cursor-pointer text-sm'>
                              #{finding.id} · {securityTime(finding.trigger_at)}{' '}
                              · {t('Matching count')}: {finding.matched_count}
                            </summary>
                            <div className='space-y-2 pt-3'>
                              <DetailRow
                                size='sm'
                                label={t('Rule')}
                                value={`${t(securityRules[finding.rule] ?? finding.rule)} #${finding.id}`}
                              />
                              <DetailRow
                                size='sm'
                                label={t('Event time')}
                                value={securityTime(finding.trigger_at)}
                              />
                              <DetailRow
                                size='sm'
                                label={t('Detected at')}
                                value={securityTime(finding.detected_at)}
                              />
                              <DetailRow
                                size='sm'
                                label={t('Source')}
                                value={t(
                                  finding.source === 'repair'
                                    ? 'Historical rescan'
                                    : 'Live detection'
                                )}
                              />
                              {finding.threshold > 0 && (
                                <DetailRow
                                  size='sm'
                                  label={t('Threshold at detection')}
                                  value={finding.threshold}
                                />
                              )}
                              <DetailRow
                                size='sm'
                                label={t('Version')}
                                value={finding.rule_version}
                              />
                              {finding.window_seconds > 0 && (
                                <DetailRow
                                  size='sm'
                                  label={t('Window (seconds)')}
                                  value={finding.window_seconds}
                                />
                              )}
                              <DetailRow
                                size='sm'
                                label={t('Matching count')}
                                value={finding.matched_count}
                              />
                              <AuditDetailFields
                                fields={Object.entries({
                                  actor_id: securityActorLabel(
                                    finding,
                                    t('Unidentified account')
                                  ),
                                  ...(finding.attempted_account && {
                                    attempted_account:
                                      finding.attempted_account,
                                  }),
                                  actor_role: finding.actor_role,
                                  resource_type:
                                    finding.resource_type || t('Not recorded'),
                                  target_id:
                                    finding.target_id || t('Not recorded'),
                                  ip: finding.ip || t('Not recorded'),
                                  request_id:
                                    finding.request_id || t('Not recorded'),
                                  event_id: finding.trigger_event_id,
                                }).map(([key, value]) => ({
                                  key,
                                  label: auditFieldLabel(
                                    key,
                                    t,
                                    key === 'attempted_account'
                                      ? (data.events.find(
                                          (item) =>
                                            item.event_id ===
                                            finding.trigger_event_id
                                        )?.action ?? finding.rule)
                                      : undefined
                                  ),
                                  value,
                                  copyable: [
                                    'ip',
                                    'request_id',
                                    'event_id',
                                  ].includes(key),
                                }))}
                              />
                              {!!finding.target_role &&
                                finding.target_id > 0 && (
                                  <DetailRow
                                    size='sm'
                                    label={t('Target account')}
                                    value={securityTargetLabel(finding)}
                                  />
                                )}
                              <AuditDetailFields
                                fields={Object.entries(
                                  finding.dimensions ?? {}
                                ).map(([key, value]) => ({
                                  key,
                                  label: auditFieldLabel(key, t, finding.rule),
                                  value,
                                  action: finding.rule,
                                }))}
                              />
                              {finding.truncated && (
                                <p className='text-muted-foreground text-xs'>
                                  {t(
                                    'Detection history was truncated. Counts may be incomplete.'
                                  )}
                                </p>
                              )}
                            </div>
                          </details>
                        ))
                      }
                    </SecurityRecordPages>
                    {!!data.findings?.length && (
                      <p className='text-muted-foreground text-xs'>
                        {t('Showing up to 100 most recent match snapshots.')}
                      </p>
                    )}
                  </DetailSection>
                </CollapsibleContent>
              </Collapsible>
              {![
                'multi_account_key_access',
                'multi_source_key_access',
                'new_source_channel_rewrite',
              ].includes(data.alert.rule) && (
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Up to 20 related records are retained. They may include context events and are not a complete list of rule matches.'
                  )}
                </p>
              )}
              <SecurityRelations
                key={activeId}
                id={activeId}
                anchor={data.alert.last_seen}
                onSelect={setActiveId}
              />
              <p className='text-muted-foreground flex flex-wrap gap-2 text-xs'>
                {t('IP data sources')}:{' '}
                <a
                  className='underline'
                  href='https://db-ip.com'
                  target='_blank'
                  rel='noreferrer'
                >
                  IP Geolocation by DB-IP
                </a>
                <a
                  className='underline'
                  href='https://www.spamhaus.org/blocklists/do-not-route-or-peer/'
                  target='_blank'
                  rel='noreferrer'
                >
                  Spamhaus DROP
                </a>
                <a
                  className='underline'
                  href='https://ipinfo.io'
                  target='_blank'
                  rel='noreferrer'
                >
                  IPinfo
                </a>
                <a
                  className='underline'
                  href='https://ipapi.is'
                  target='_blank'
                  rel='noreferrer'
                >
                  ipapi.is
                </a>
                <a
                  className='underline'
                  href='https://proxycheck.io'
                  target='_blank'
                  rel='noreferrer'
                >
                  proxycheck.io
                </a>
                <a
                  className='underline'
                  href='https://check.torproject.org/torbulkexitlist'
                  target='_blank'
                  rel='noreferrer'
                >
                  Tor Project
                </a>
              </p>
            </TabsContent>
            <TabsContent
              keepMounted
              value='history'
              className='min-h-0 [scrollbar-gutter:stable] space-y-4 overflow-y-auto overscroll-contain'
            >
              <DetailSection size='sm' label={t('Investigation history')}>
                {user && hasPermission(user, 'audit_security', 'manage') && (
                  <DetailRow
                    size='sm'
                    label={t('Handling status')}
                    value={
                      <AlertStatusCell
                        alert={data.alert}
                        onProcessed={props.onProcessed}
                      />
                    }
                  />
                )}
                {!data.actions.length && (
                  <p className='text-muted-foreground text-xs'>
                    {t('No records')}
                  </p>
                )}
                <SecurityRecordPages items={data.actions}>
                  {(records) =>
                    records.map((action) => (
                      <div
                        key={action.id}
                        className='space-y-1 border-b py-2 last:border-b-0'
                      >
                        <DetailRow
                          size='sm'
                          label={t('Operator')}
                          value={
                            action.action === 'system_reopen' ||
                            action.source === 'automatic'
                              ? t('System')
                              : securityActorLabel(action, t('Not recorded'))
                          }
                        />
                        <DetailRow
                          size='sm'
                          label={t('Action')}
                          value={t(
                            securityActionLabel(action.action, action.source)
                          )}
                        />
                        <DetailRow
                          size='sm'
                          label={t('Time')}
                          value={securityTime(action.created_at)}
                        />
                        {(action.from_status || action.to_status) && (
                          <DetailRow
                            size='sm'
                            label={t('Handling status')}
                            value={`${t(alertStatuses[action.from_status as keyof typeof alertStatuses] ?? 'Not recorded')} → ${t(alertStatuses[action.to_status as keyof typeof alertStatuses] ?? 'Not recorded')}`}
                          />
                        )}
                        {action.assignee_id !== undefined && (
                          <DetailRow
                            size='sm'
                            label={t('Assignee')}
                            value={
                              <AlertAssignees
                                assignees={action.assignees}
                                legacyID={action.assignee_id}
                              />
                            }
                          />
                        )}
                        {action.event_id && (
                          <DetailRow
                            size='sm'
                            label={t('Event ID')}
                            value={action.event_id}
                          />
                        )}
                        {!!action.detected_at && (
                          <DetailRow
                            size='sm'
                            label={t('Detected at')}
                            value={securityTime(action.detected_at)}
                          />
                        )}
                        {!!action.event_at && (
                          <DetailRow
                            size='sm'
                            label={t('Event time')}
                            value={securityTime(action.event_at)}
                          />
                        )}
                        {action.source && (
                          <DetailRow
                            size='sm'
                            label={t('Source')}
                            value={t(
                              (
                                {
                                  repair: 'Historical rescan',
                                  live: 'Live detection',
                                  automatic: 'Automatic response',
                                  human: 'Manual ban',
                                } as Record<string, string>
                              )[action.source] ?? 'Manual action'
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
                              reason_code: action.reason_code ?? '',
                            }}
                            showReason
                          />
                        )}
                        {action.note && (
                          <DetailRow
                            size='sm'
                            label={t('Investigation note')}
                            value={action.note}
                          />
                        )}
                        {action.conclusion && (
                          <DetailRow
                            size='sm'
                            label={t('Conclusion')}
                            value={t(
                              securityConclusions[action.conclusion] ??
                                action.conclusion
                            )}
                          />
                        )}
                      </div>
                    ))
                  }
                </SecurityRecordPages>
              </DetailSection>
            </TabsContent>
            <TabsContent
              keepMounted
              value='notifications'
              className='min-h-0 [scrollbar-gutter:stable] space-y-4 overflow-y-auto overscroll-contain'
            >
              <DetailSection size='sm' label={t('Notification delivery')}>
                {!data.deliveries.length && (
                  <p className='text-muted-foreground text-xs'>
                    {t('No records')}
                  </p>
                )}
                <SecurityRecordPages items={data.deliveries}>
                  {(records) =>
                    records.map((delivery) => (
                      <div
                        key={delivery.id}
                        className='space-y-1 border-b py-2 last:border-b-0'
                      >
                        <DetailRow
                          size='sm'
                          label={t('Type')}
                          value={t(
                            (
                              {
                                none: 'Notification decision',
                                email: 'Email',
                                webhook: 'Webhook',
                                account: 'Account security notifications',
                              } as Record<string, string>
                            )[delivery.kind] ?? delivery.kind
                          )}
                        />
                        <DetailRow
                          size='sm'
                          label={t('Status')}
                          value={t(
                            deliveryLabels[delivery.status] ?? delivery.status
                          )}
                        />
                        <DetailRow
                          size='sm'
                          label={t('Attempts')}
                          value={delivery.attempts}
                        />
                        <DetailRow
                          size='sm'
                          label={t('Created at')}
                          value={securityTime(delivery.created_at)}
                        />
                        <DetailRow
                          size='sm'
                          label={t('Last updated')}
                          value={securityTime(delivery.updated_at)}
                        />
                        {!!delivery.finding_id && (
                          <DetailRow
                            size='sm'
                            label={t('Rule match snapshots')}
                            value={`#${delivery.finding_id}`}
                          />
                        )}
                        <DetailRow
                          size='sm'
                          label={t('Sent at')}
                          value={securityTime(delivery.sent_at)}
                        />
                        <DetailRow
                          size='sm'
                          label={t('Next attempt')}
                          value={securityTime(delivery.next_attempt_at)}
                        />
                        {(delivery.wait_reason || delivery.error_code) && (
                          <DetailRow
                            size='sm'
                            label={t('Reason')}
                            value={t(
                              deliveryLabels[
                                delivery.wait_reason ||
                                  delivery.error_code ||
                                  ''
                              ] ?? 'Delivery needs attention'
                            )}
                          />
                        )}
                      </div>
                    ))
                  }
                </SecurityRecordPages>
              </DetailSection>
            </TabsContent>
          </Tabs>
        </>
      )}
    </Dialog>
  )
}

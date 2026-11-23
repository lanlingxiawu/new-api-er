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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTablePage, useDataTable } from '@/components/data-table'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Spinner } from '@/components/ui/spinner'
import { hasPermission } from '@/lib/admin-permissions'
import type { SecurityView } from '@/lib/audit-navigation'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { defaultAuditTimeRange } from '../lib/time-range'
import {
  exportAlerts,
  securityGet,
  type SecurityAlert,
  type SecurityEvent,
  type SecurityHealth,
  type SecurityCapacity,
} from './api'
import { SecurityAlertCleanup } from './security-alert-cleanup'
import { useSecurityAlertColumns } from './security-alert-columns'
import { SecurityAlertDialog } from './security-alert-dialog'
import { SecurityFilterBar } from './security-filter-bar'
import { SecurityMonitorHealth } from './security-monitor-health'
import { SettingsDrawer } from './settings-drawer'
import { useAlertBatchConfirmation } from './use-alert-batch-confirmation'

export { SecurityAlertDialog } from './security-alert-dialog'

const EMPTY_ALERTS: SecurityAlert[] = []
const viewDescriptions: Record<string, string> = {
  id: 'All handling statuses in the selected event time range.',
  handled:
    'Processed records include confirmed alerts and false positives. The date range uses the latest event time.',
  recent_hits:
    'At most 20 recently matched alerts. This is not a list of every update.',
  activity:
    'Updates include investigation changes. Refresh if the list changes while paging.',
  archived:
    'All archived alerts, including those outside the selected date range.',
  unresolved:
    'Unprocessed records from all time, including records in progress.',
}

export function SecurityMonitor(props: {
  initialView?: SecurityView
  onViewChange?: (view: SecurityView) => void
  onInvestigate?: (event: SecurityEvent) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const user = useAuthStore((state) => state.auth.user)
  const root = user?.role === ROLE.SUPER_ADMIN
  const canManage = !!user && hasPermission(user, 'audit_security', 'manage')
  const [pageSize, setPageSize] = useState(20)
  const [filters, setFilters] = useState(() => {
    return {
      resource: '',
      status: '',
      rule: '',
      severity: '',
      actor_id: '',
      ip: '',
      assignee_id: '',
      history_scope: '',
      view: props.initialView ?? 'id',
      ...defaultAuditTimeRange(),
    }
  })
  const [customRange, setCustomRange] = useState(false)
  const [textFilters, setTextFilters] = useState({
    actor_id: '',
    ip: '',
    assignee_id: '',
  })
  const [cursors, setCursors] = useState<(number | string)[]>([0])
  const onProcessed = useCallback(() => {
    if (filters.view === 'unresolved') setCursors([0])
  }, [filters.view])
  const batch = useAlertBatchConfirmation(onProcessed)
  const [selected, setSelected] = useState<number | null>(null)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const scope = [
    'audit-security',
    user?.id,
    user?.role,
    user?.permissions,
  ] as const
  const health = useQuery({
    queryKey: [...scope, 'health'],
    queryFn: ({ signal }) => securityGet<SecurityHealth>('health', signal),
    enabled: root,
    refetchInterval: 15000,
    gcTime: 0,
  })
  const allTime = filters.view === 'unresolved' || filters.view === 'archived'
  const invalidRange =
    !allTime &&
    (!filters.start ||
      !filters.end ||
      filters.end < filters.start ||
      filters.end - filters.start > 31 * 86400)
  const params: Record<string, unknown> = { ...filters, limit: pageSize }
  if (allTime) {
    delete params.start
    delete params.end
  }
  if (filters.view === 'recent_hits') params.limit = 20
  else if (filters.view === 'activity') {
    params.cursor = cursors.at(-1) || undefined
  } else params.before = cursors.at(-1)
  const alerts = useQuery({
    queryKey: [...scope, 'alerts', params],
    gcTime: 0,
    retry: false,
    queryFn: ({ signal }) =>
      securityGet<{
        items: SecurityAlert[]
        next_cursor?: number | string
        revision?: number
        capacity?: SecurityCapacity
      }>('alerts', signal, params),
    refetchInterval: filters.view === 'activity' ? false : 60_000,
    enabled: !invalidRange,
  })
  const exportQuery = useMutation({
    mutationFn: () =>
      exportAlerts({
        ...params,
        limit: 100,
        view: filters.view,
        cursor: undefined,
        before: undefined,
      }),
    onError: (error) => handleServerError(error, t('Export failed')),
  })
  const update = (patch: Partial<typeof filters>) => {
    setFilters((current) => ({ ...current, ...patch }))
    setCursors([0])
    if (patch.view && patch.view !== filters.view) {
      props.onViewChange?.(patch.view)
    }
  }
  const refresh = () => {
    void queryClient.invalidateQueries({
      queryKey: [...scope, 'pending-count'],
    })
    const range = defaultAuditTimeRange()
    setCursors([0])
    setFilters((current) => ({
      ...current,
      ...textFilters,
      ...(customRange ? {} : range),
    }))
    if (
      cursors.length === 1 &&
      (customRange ||
        allTime ||
        (filters.start === range.start && filters.end === range.end)) &&
      Object.entries(textFilters).every(
        ([key, value]) => filters[key as keyof typeof textFilters] === value
      )
    ) {
      void alerts.refetch()
    }
    if (root) void health.refetch()
  }

  const rows = alerts.isError
    ? EMPTY_ALERTS
    : (alerts.data?.items ?? EMPTY_ALERTS)
  const pendingCount = rows.filter(
    (alert) => alert.status === 'open' || alert.status === 'investigating'
  ).length
  const columns = useSecurityAlertColumns(
    canManage,
    setSelected,
    batch.isPending,
    onProcessed
  )
  const { table } = useDataTable({
    columns,
    data: rows,
    getRowId: (row) => String(row.id),
    enableRowSelection: false,
    enableSorting: false,
    manualFiltering: true,
    manualPagination: true,
    pageCount: -1,
    pagination: {
      pageIndex: cursors.length - 1,
      pageSize: filters.view === 'recent_hits' ? 20 : pageSize,
    },
    onPaginationChange: (updater) => {
      if (
        alerts.isFetching ||
        batch.isPending ||
        invalidRange ||
        filters.view === 'recent_hits'
      ) {
        return
      }
      const current = { pageIndex: cursors.length - 1, pageSize }
      const next = typeof updater === 'function' ? updater(current) : updater
      if (next.pageSize !== pageSize) {
        setPageSize(next.pageSize)
        setCursors([0])
        return
      }
      const nextCursor = alerts.data?.next_cursor
      if (next.pageIndex < current.pageIndex) {
        setCursors((values) => values.slice(0, next.pageIndex + 1))
      } else if (
        next.pageIndex === current.pageIndex + 1 &&
        nextCursor &&
        !alerts.isError
      ) {
        setCursors((values) => [...values, nextCursor])
      }
    },
  })
  const toolbar = (
    <div className='flex min-w-0 shrink-0 flex-col gap-2'>
      <SecurityFilterBar
        table={table}
        filters={filters}
        textFilters={textFilters}
        customRange={customRange}
        isFetching={alerts.isFetching}
        invalidRange={invalidRange}
        onChange={update}
        onTextChange={(patch) =>
          setTextFilters((current) => ({ ...current, ...patch }))
        }
        onDateChange={(patch) => {
          setCustomRange(true)
          update(patch)
        }}
        onSearch={refresh}
        onReset={() => {
          setCustomRange(false)
          setTextFilters({ ip: '', actor_id: '', assignee_id: '' })
          update({
            resource: '',
            status: '',
            rule: '',
            severity: '',
            history_scope: '',
            ip: '',
            actor_id: '',
            assignee_id: '',
            ...defaultAuditTimeRange(),
          })
        }}
        stats={
          alerts.data?.capacity && (
            <div className='text-muted-foreground flex flex-wrap gap-x-3 gap-y-1 text-xs'>
              <span>
                {t('Unprocessed')}: {alerts.data.capacity.open}
              </span>
              <span>
                {t('Archived')}: {alerts.data.capacity.archived}
              </span>
              <span>
                {t('Unknown closure time')}:{' '}
                {alerts.data.capacity.retention_unknown}
              </span>
              {alerts.data.capacity.truncated && (
                <span>{t('Counts are limited samples')}</span>
              )}
              {alerts.data.capacity.warning && (
                <span className='text-destructive'>
                  {t('Storage needs attention')}
                </span>
              )}
            </div>
          )
        }
        actions={
          <>
            {canManage && (
              <Button
                type='button'
                variant='outline'
                disabled={
                  batch.isPending ||
                  alerts.isFetching ||
                  alerts.isError ||
                  invalidRange ||
                  !pendingCount
                }
                aria-busy={batch.isPending}
                onClick={() => batch.confirm(rows)}
              >
                {batch.isPending
                  ? t('Confirming {{done}}/{{total}}...', batch.progress)
                  : t('Confirm this page ({{count}})', { count: pendingCount })}
              </Button>
            )}
            {user && hasPermission(user, 'audit_security', 'export') && (
              <Button
                type='button'
                variant='outline'
                disabled={
                  exportQuery.isPending ||
                  invalidRange ||
                  filters.view === 'activity' ||
                  filters.view === 'recent_hits'
                }
                aria-busy={exportQuery.isPending}
                title={
                  filters.view === 'activity' || filters.view === 'recent_hits'
                    ? t(
                        'Switch to the alert ID, unresolved or archived view to export.'
                      )
                    : undefined
                }
                onClick={() => exportQuery.mutate()}
              >
                {exportQuery.isPending
                  ? t('Loading...')
                  : t('Export alerts (up to 100)')}
              </Button>
            )}
            {user && hasPermission(user, 'audit_security', 'delete') && (
              <SecurityAlertCleanup
                onDeleted={refresh}
                key={`${user.id}:${user.role}:${JSON.stringify(user.permissions)}`}
              />
            )}
            {root && (
              <Button
                type='button'
                variant='outline'
                onClick={() => setSettingsOpen(true)}
              >
                {t('Monitoring settings')}
              </Button>
            )}
          </>
        }
      />
      {!canManage && (
        <p className='text-muted-foreground text-xs'>
          {t(
            'You can view these records. Processing requires security management permission.'
          )}
        </p>
      )}
      {invalidRange && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t('Choose a complete time range of at most 31 days')}
          </AlertDescription>
        </Alert>
      )}
      {root && (
        <>
          {health.isPending && (
            <LoadingState inline size='sm' message={t('Loading...')} />
          )}
          {health.data && !health.isError && (
            <SecurityMonitorHealth health={health.data} />
          )}
        </>
      )}
      {(alerts.isError || (root && health.isError)) && (
        <Alert variant='destructive'>
          <AlertDescription className='flex flex-wrap items-center justify-between gap-2'>
            <span>
              {t(
                'Unable to load security monitoring. Check settings and retry.'
              )}
            </span>
            <Button
              type='button'
              size='sm'
              variant='outline'
              disabled={
                (alerts.isError && alerts.isFetching) ||
                (root && health.isError && health.isFetching)
              }
              onClick={() => {
                if (alerts.isError && !invalidRange) void alerts.refetch()
                if (root && health.isError) void health.refetch()
              }}
            >
              {((alerts.isError && alerts.isFetching) ||
                (root && health.isError && health.isFetching)) && <Spinner />}
              {t('Retry')}
            </Button>
          </AlertDescription>
        </Alert>
      )}
    </div>
  )
  let emptyTitle = t('No alerts match these filters')
  if (filters.view === 'unresolved') {
    const filtered = Object.entries(filters).some(
      ([key, value]) => !['view', 'start', 'end'].includes(key) && !!value
    )
    emptyTitle =
      filtered || cursors.length > 1
        ? t('No matching unprocessed risk records')
        : t('No unprocessed risk records')
  } else if (filters.view === 'handled') {
    emptyTitle = t('No processed risk records in this event time range')
  }
  return (
    <div
      className='flex h-full min-h-0 min-w-0 flex-col gap-3'
      onKeyDown={(event) => {
        if (
          event.key === 'Enter' &&
          event.target instanceof HTMLInputElement &&
          event.target.getAttribute('role') !== 'combobox' &&
          !invalidRange
        ) {
          refresh()
        }
      }}
    >
      {alerts.isError ? (
        toolbar
      ) : (
        <DataTablePage
          table={table}
          scrollResetKey={JSON.stringify(params)}
          columns={columns}
          isLoading={alerts.isPending && !invalidRange}
          isFetching={alerts.isFetching}
          emptyTitle={emptyTitle}
          showPagination={filters.view !== 'recent_hits'}
          cursorPagination={{
            hasNextPage: !!alerts.data?.next_cursor && !invalidRange,
            isFetching: alerts.isFetching || invalidRange || batch.isPending,
            allowPageSizeChange: true,
            numberedPages: true,
          }}
          className='h-auto min-h-0 min-w-0 flex-1'
          applyHeaderSize
          getColumnClassName={() => 'py-2'}
          tableClassName='[&_[data-slot=table]]:text-[13px] [&_[data-slot=table]_td]:text-[13px] [&_[data-slot=table]_td_*]:text-[13px] [&_[data-slot=table]_th]:text-[13px] [&_[data-slot=table]_th_*]:text-[13px]'
          toolbar={toolbar}
          afterTable={
            <div className='flex shrink-0 flex-wrap items-center justify-between gap-2 py-2'>
              <p className='text-muted-foreground text-xs'>
                {t(viewDescriptions[filters.view])}
              </p>
            </div>
          }
        />
      )}
      {selected !== null && (
        <SecurityAlertDialog
          key={selected}
          id={selected}
          onClose={() => setSelected(null)}
          onDeleted={refresh}
          onProcessed={onProcessed}
          onInvestigate={props.onInvestigate}
        />
      )}
      {root && settingsOpen && (
        <SettingsDrawer onClose={() => setSettingsOpen(false)} />
      )}
    </div>
  )
}

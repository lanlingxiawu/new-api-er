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
import type { ColumnDef } from '@tanstack/react-table'
import { isAxiosError } from 'axios'
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTablePage, useDataTable } from '@/components/data-table'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { hasPermission } from '@/lib/admin-permissions'
import { useAuthStore } from '@/stores/auth-store'

import { publicAuditFilters, type AuditFilters, type AuditRecord } from '../api'
import { AuditExportButton } from '../export/audit-export-panel'
import { defaultAuditTimeRange } from '../lib/time-range'
import { useAuditActivityColumns } from './audit-activity-columns'
import { useAuditLogColumns } from './audit-log-columns'
import { AuditLogFilterBar } from './audit-log-filter-bar'
import { useAuditPage } from './use-audit-page'

const EMPTY_LOGS: AuditRecord[] = []

function defaultAuditFilters(pageSize = 20): AuditFilters {
  const range = defaultAuditTimeRange()
  return {
    p: 1,
    page_size: pageSize,
    start_timestamp: range.start,
    end_timestamp: range.end,
  }
}

export function AuditLogViewer(props: {
  scope: 'all' | 'self'
  // Super-admin "Only Mine" keeps the self filters, but reads that account's
  // management records. Other self viewers stay on the public activity DTO.
  ownDetail?: boolean
  accessOnly?: boolean
  currentTokenRef?: string
  // The token scope the viewer opens with and returns to on reset.
  defaultTokenScope?: 'all' | 'current'
  onAccessDenied?: () => Promise<void>
  initialFilters?: Partial<AuditFilters>
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const ownDetail = props.scope === 'self' && !!props.ownDetail
  const canReadSecurity =
    props.scope === 'all' &&
    !!user &&
    hasPermission(user, 'audit_security', 'read') &&
    !props.accessOnly
  const [filters, setFilters] = useState<AuditFilters>(() => {
    const initial = {
      ...defaultAuditFilters(),
      ...props.initialFilters,
      p: 1,
    }
    return props.scope === 'self' ? publicAuditFilters(initial) : initial
  })
  const [baseline, setBaseline] = useState(filters)
  const [draft, setDraft] = useState(filters)
  const [customRange, setCustomRange] = useState(
    props.initialFilters?.start_timestamp !== undefined ||
      props.initialFilters?.end_timestamp !== undefined
  )
  const [appliedCustomRange, setAppliedCustomRange] = useState(customRange)
  const [cursors, setCursors] = useState<string[]>([''])
  const defaultTokenScope = props.defaultTokenScope ?? 'all'
  const [tokenScope, setTokenScope] = useState<string>(defaultTokenScope)
  const [appliedTokenScope, setAppliedTokenScope] = useState(
    defaultTokenScope as string
  )
  const params = { ...filters }
  if (props.accessOnly) params.category = 'access_token'
  if (appliedTokenScope === 'current') params.token_ref = props.currentTokenRef
  if (appliedTokenScope === 'historical') {
    params.exclude_token_ref = props.currentTokenRef
  }
  const canQuery =
    appliedTokenScope === 'all' ||
    (props.currentTokenRef !== undefined &&
      (appliedTokenScope === 'historical' || !!props.currentTokenRef))
  const invalidRange =
    !draft.start_timestamp ||
    !draft.end_timestamp ||
    draft.start_timestamp > draft.end_timestamp ||
    draft.end_timestamp - draft.start_timestamp > 30 * 86400
  const exportParams = { ...params }
  if (!canReadSecurity) delete params.ip_risk
  const requestParams = { ...params }
  if (ownDetail) {
    delete requestParams.username
    delete requestParams.ip_risk
    requestParams.mine = true
  }
  const riskMode = !!params.ip_risk
  const page = useAuditPage(
    ownDetail ? 'all' : props.scope,
    requestParams,
    cursors.at(-1) ?? '',
    canReadSecurity,
    canQuery
  )
  const query = page.query
  const accessDenied =
    (props.scope === 'all' || ownDetail) &&
    isAxiosError(query.error) &&
    query.error.response?.status === 403
  const onAccessDenied = props.onAccessDenied
  useEffect(() => {
    if (accessDenied) void onAccessDenied?.()
  }, [accessDenied, onAccessDenied])
  const filterIP = useCallback(
    (ip: string) => {
      const next = { ...filters, ip, p: 1 }
      setDraft(next)
      setFilters(next)
      setTokenScope(appliedTokenScope)
      setCustomRange(appliedCustomRange)
      setCursors([''])
    },
    [filters, appliedTokenScope, appliedCustomRange]
  )
  const adminColumns = useAuditLogColumns(props.accessOnly, filterIP)
  const activityColumns = useAuditActivityColumns(
    filterIP,
    props.accessOnly ? undefined : user?.username
  )
  // Super-admin own records use the management renderer. Every other self
  // view stays on the public activity renderer, with no client-side fallback.
  const columns = (
    props.scope === 'all' || ownDetail ? adminColumns : activityColumns
  ) as ColumnDef<AuditRecord>[]
  const { table } = useDataTable({
    columns,
    data: canQuery && !query.isError ? page.rows : EMPTY_LOGS,
    getRowId: (entry) => entry.event_id,
    totalCount: query.isError ? 0 : (query.data?.total ?? 0),
    pagination: { pageIndex: filters.p - 1, pageSize: filters.page_size },
    onPaginationChange: (updater) => {
      if (query.isFetching || query.isError || invalidRange || !canQuery) return
      setFilters((previous) => {
        const current = {
          pageIndex: previous.p - 1,
          pageSize: previous.page_size,
        }
        const next = typeof updater === 'function' ? updater(current) : updater
        return {
          ...previous,
          p: next.pageSize === previous.page_size ? next.pageIndex + 1 : 1,
          page_size: next.pageSize,
        }
      })
    },
    enableRowSelection: false,
    enableSorting: false,
    manualFiltering: true,
    manualPagination: true,
  })
  const update = (patch: Partial<AuditFilters>) => {
    if ('start_timestamp' in patch || 'end_timestamp' in patch) {
      setCustomRange(true)
    }
    setDraft((previous) => ({ ...previous, ...patch, p: 1 }))
  }

  const search = () => {
    if (invalidRange) return
    setCursors([''])
    setAppliedTokenScope(tokenScope)
    const range = defaultAuditFilters(filters.page_size)
    const next = {
      ...draft,
      ...(!customRange && {
        start_timestamp: range.start_timestamp,
        end_timestamp: range.end_timestamp,
      }),
      ip: draft.ip?.trim() || undefined,
      request_id: draft.request_id?.trim() || undefined,
      username: draft.username?.trim() || undefined,
      token_ref: draft.token_ref?.trim() || undefined,
      p: 1,
      page_size: filters.page_size,
    }
    if (!customRange) setBaseline(range)
    setDraft(next)
    setFilters(next)
    setAppliedCustomRange(customRange)
    if (
      cursors.length === 1 &&
      JSON.stringify(next) === JSON.stringify(filters) &&
      tokenScope === appliedTokenScope
    ) {
      void query.refetch()
    }
  }

  return (
    <div
      className='flex h-full min-h-0 flex-col'
      onKeyDown={(event) => {
        if (
          event.key === 'Enter' &&
          event.target instanceof HTMLInputElement &&
          event.target.getAttribute('role') !== 'combobox'
        ) {
          search()
        }
      }}
    >
      <DataTablePage
        table={table}
        scrollResetKey={JSON.stringify([requestParams, cursors.at(-1)])}
        columns={columns}
        isLoading={query.isPending && canQuery && !invalidRange}
        isFetching={query.isFetching}
        emptyTitle={
          query.isError
            ? t('Failed to load audit records')
            : t(
                query.data?.next_cursor
                  ? 'Continue loading to search the remaining records'
                  : 'No records'
              )
        }
        hideMobile={props.accessOnly}
        paginationInFooter={!props.accessOnly}
        showPagination={!riskMode}
        className='h-auto min-h-0 flex-1'
        applyHeaderSize
        getColumnClassName={() => 'py-2'}
        tableClassName='[&_[data-slot=table]]:text-[13px] [&_[data-slot=table]_td]:text-[13px] [&_[data-slot=table]_td_*]:text-[13px] [&_[data-slot=table]_th]:text-[13px] [&_[data-slot=table]_th_*]:text-[13px]'
        toolbar={
          <div className='shrink-0 space-y-2'>
            <AuditLogFilterBar
              actionEnd={
                <AuditExportButton
                  scope={props.scope}
                  params={exportParams}
                  canQuery={canQuery}
                  onAccessDenied={props.onAccessDenied}
                />
              }
              table={table}
              filters={draft}
              baseline={baseline}
              applied={filters}
              canReadSecurity={canReadSecurity}
              onChange={update}
              scope={props.scope}
              viewerRole={user?.role ?? 0}
              accessOnly={props.accessOnly}
              tokenScope={tokenScope}
              defaultTokenScope={defaultTokenScope}
              currentTokenRef={props.currentTokenRef}
              onTokenScopeChange={(value) => {
                setTokenScope(value)
                update({})
              }}
              isFetching={query.isFetching}
              onSearch={search}
              onReset={() => {
                setTokenScope(defaultTokenScope)
                setAppliedTokenScope(defaultTokenScope)
                const next = defaultAuditFilters(filters.page_size)
                setBaseline(next)
                setFilters(next)
                setDraft(next)
                setCustomRange(false)
                setAppliedCustomRange(false)
                setCursors([''])
              }}
            />
            {invalidRange && (
              <Alert variant='destructive'>
                <AlertDescription>
                  {t('Choose a complete time range of at most 30 days')}
                </AlertDescription>
              </Alert>
            )}
            {query.isError && (
              <Alert variant='destructive'>
                <AlertDescription className='flex items-center justify-between gap-2'>
                  <span>
                    {t('Failed to load audit records')}
                    {query.error?.cause ? `: ${query.error.message}` : ''}
                  </span>
                  <Button
                    size='sm'
                    variant='outline'
                    onClick={() => {
                      if (riskMode && cursors.length > 1) {
                        setCursors([''])
                      } else {
                        void query.refetch()
                      }
                    }}
                  >
                    {t('Retry')}
                  </Button>
                </AlertDescription>
              </Alert>
            )}
            {canReadSecurity && page.markers.isError && (
              <p role='status' className='text-muted-foreground text-xs'>
                {t('Risk markers unavailable')}
              </p>
            )}
            {page.coverage &&
              (!page.coverage.health.enabled ||
                page.coverage.health.suspended) && (
                <p role='status' className='text-muted-foreground text-xs'>
                  {t('Detection is paused or disabled')}
                </p>
              )}
          </div>
        }
      />
      {riskMode && (
        <div className='flex items-center justify-end gap-2 py-2'>
          <Button
            variant='outline'
            disabled={cursors.length === 1 || query.isFetching}
            onClick={() => setCursors((previous) => previous.slice(0, -1))}
          >
            {t('Previous page')}
          </Button>
          <Button
            variant='outline'
            disabled={
              !query.data?.next_cursor || query.isFetching || query.isError
            }
            onClick={() =>
              setCursors((previous) => [
                ...previous,
                query.data?.next_cursor ?? '',
              ])
            }
          >
            {t('Continue loading')}
          </Button>
        </div>
      )}
    </div>
  )
}

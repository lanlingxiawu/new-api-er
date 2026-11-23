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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import { useTranslation } from 'react-i18next'

import {
  DataTablePage,
  TruncatedCell,
  useDataTable,
} from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import type { CustomerProfile } from '@/features/customers/types'
import { AUDIT_ACTIVITY_VERSION } from '@/features/usage-logs/audit/api'
import { useAuditActivityColumns } from '@/features/usage-logs/audit/components/audit-activity-columns'
import { defaultAuditTimeRange } from '@/features/usage-logs/audit/lib/time-range'
import { AccountResponses } from '@/features/usage-logs/audit/security/account-response-status'
import { AlertConfirmButton } from '@/features/usage-logs/audit/security/alert-confirm-button'
import { AlertHandlingStatus } from '@/features/usage-logs/audit/security/alert-handling-status'
import {
  securityTime,
  severities,
  severityVariants,
} from '@/features/usage-logs/audit/security/lib/security-display'
import { securityRules } from '@/features/usage-logs/audit/security/lib/security-rules'
import { CompactDateTimeRangePicker } from '@/features/usage-logs/components/compact-date-time-range-picker'
import {
  LogsFilterField,
  LogsFilterInput,
  LogsFilterToolbar,
} from '@/features/usage-logs/components/logs-filter-toolbar'
import type { AuditRiskView } from '@/lib/audit-navigation'
import { useAuthStore } from '@/stores/auth-store'

import {
  customerAuditGet,
  employeeAuditGet,
  isCustomerAccessDenied,
  type CustomerAuditAccount,
  type CustomerAuditActivity,
  type CustomerAuditIdentity,
  type CustomerAuditFilters,
  type CustomerAuditPage,
  type CustomerSecurityAlert,
  type CustomerSecurityEvent,
} from './audit-api'
import { CustomerRiskDetail } from './customer-risk-detail'

function customerAuditRange(
  event?: CustomerSecurityEvent
): CustomerAuditFilters {
  if (event) {
    return {
      start: Math.max(1, event.at - 900),
      end: event.at + 900,
      request_id: event.request_id,
      ip: event.ip,
    }
  }
  return { ...defaultAuditTimeRange(), request_id: '', ip: '' }
}

export function CustomerAuditDialog(props: {
  customer: CustomerProfile
  onClose: () => void
}) {
  const { t } = useTranslation()
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Customer audit and risk')}
      description={`${props.customer.username || props.customer.display_name || t('Customer')} #${props.customer.customer_user_id}`}
      contentClassName='w-[calc(100vw-1.5rem)] max-w-[calc(100vw-1.5rem)] overflow-y-hidden sm:max-w-7xl'
      contentHeight='min(72dvh, 32rem)'
      scrollAreaClassName='flex flex-col overflow-y-hidden'
      bodyClassName='flex h-full min-h-0 flex-col'
      footerClassName='flex-row justify-end sm:py-4'
      footer={
        <Button variant='outline' onClick={props.onClose}>
          {t('Close')}
        </Button>
      }
    >
      <CustomerAuditView customer={props.customer} />
    </Dialog>
  )
}

interface CustomerAuditViewProps {
  tab?: 'audit' | 'risk'
  riskView?: AuditRiskView
  onTabChange?: (tab: 'audit' | 'risk') => void
  onRiskViewChange?: (view: AuditRiskView) => void
  customer?: CustomerAuditAccount
  paginationInFooter?: boolean
  customerFilter?: ReactNode
  initialInvestigation?: CustomerSecurityEvent
  onInvestigateCustomer?: (
    customer: CustomerAuditIdentity,
    event: CustomerSecurityEvent
  ) => void
}

export function CustomerAuditView(props: CustomerAuditViewProps) {
  const user = useAuthStore((state) => state.auth.user)
  return (
    <CustomerAuditContent
      key={`${user?.id}:${user?.role}:${user?.is_employee}:${props.customer?.customer_user_id ?? 'all'}:${props.initialInvestigation?.event_id ?? ''}`}
      {...props}
    />
  )
}

function CustomerAuditContent(props: CustomerAuditViewProps) {
  const { t } = useTranslation()
  const userID = useAuthStore((state) => state.auth.user?.id)
  const queryClient = useQueryClient()
  const customerID = props.customer?.customer_user_id
  const [localTab, setLocalTab] = useState<'audit' | 'risk'>('audit')
  const tab = props.tab ?? localTab
  const setTab = (value: string) => {
    const next = value === 'risk' ? 'risk' : 'audit'
    setLocalTab(next)
    props.onTabChange?.(next)
  }
  const [localRiskView, setLocalRiskView] = useState<AuditRiskView>('id')
  const riskView = props.riskView ?? localRiskView
  const [riskRevision, setRiskRevision] = useState(0)
  const onConfirmed = useCallback(() => {
    if (riskView === 'unresolved') setRiskRevision((value) => value + 1)
  }, [riskView])
  const [forbidden, setForbidden] = useState(false)
  const [selectedAlert, setSelectedAlert] = useState<{
    id: number
    customer: CustomerAuditIdentity
  }>()
  const [investigation, setInvestigation] = useState(props.initialInvestigation)
  const onAccessDenied = useCallback(() => {
    setForbidden(true)
    setSelectedAlert(undefined)
    queryClient.removeQueries({
      queryKey: ['customer-audit', userID],
    })
  }, [queryClient, userID])
  const getCustomer = useCallback(
    (entry: {
      customer?: CustomerAuditIdentity
    }): CustomerAuditIdentity | undefined => {
      if (props.customer) {
        return {
          id: props.customer.customer_user_id,
          username: props.customer.username ?? '',
          display_name: props.customer.display_name ?? '',
        }
      }
      return entry.customer
    },
    [props.customer]
  )
  const getCustomerName = useCallback(
    (entry: { customer?: CustomerAuditIdentity }) => {
      const customer = getCustomer(entry)
      if (!customer) return t('Customer')
      return `${customer.username || customer.display_name || t('Customer')} #${customer.id}`
    },
    [getCustomer, t]
  )
  const auditColumns = useAuditActivityColumns<CustomerAuditActivity>(
    undefined,
    getCustomerName,
    'Customer'
  )
  const riskColumns = useMemo<ColumnDef<CustomerSecurityAlert>[]>(
    () => [
      {
        accessorKey: 'last_seen',
        header: t('Latest event'),
        cell: ({ row }) => securityTime(row.original.last_seen),
        meta: { label: t('Latest event'), mobileTitle: true },
      },
      {
        id: 'customer',
        header: t('Customer'),
        accessorFn: getCustomerName,
        cell: ({ getValue }) => (
          <TruncatedCell>{getValue<string>()}</TruncatedCell>
        ),
        enableHiding: false,
        meta: { label: t('Customer') },
      },
      {
        accessorKey: 'rule',
        header: t('Rule'),
        cell: ({ row }) => {
          const customer = getCustomer(row.original)
          return (
            <Button
              variant='link'
              className='h-auto p-0 text-start whitespace-normal'
              disabled={!customer || customer.id <= 0}
              onClick={() => {
                if (customer && customer.id > 0) {
                  setSelectedAlert({ id: row.original.id, customer })
                }
              }}
            >
              {t(securityRules[row.original.rule] ?? row.original.rule)}
            </Button>
          )
        },
        meta: { label: t('Rule') },
      },
      {
        accessorKey: 'severity',
        header: t('Severity'),
        cell: ({ row }) => (
          <StatusBadge
            copyable={false}
            variant={severityVariants[row.original.severity] ?? 'neutral'}
          >
            {t(severities[row.original.severity] ?? 'Unknown')}
          </StatusBadge>
        ),
        meta: { label: t('Severity') },
      },
      {
        id: 'response',
        header: t('Response result'),
        cell: ({ row }) => (
          <AccountResponses
            response={row.original.response}
            responses={row.original.responses}
          />
        ),
      },
      {
        accessorKey: 'status',
        header: t('Handling status'),
        cell: ({ row }) => <AlertHandlingStatus status={row.original.status} />,
        meta: { label: t('Handling status') },
      },
      {
        id: 'actions',
        header: t('Actions'),
        cell: ({ row }) => {
          const customer = getCustomer(row.original)
          if (
            !customer ||
            customer.id <= 0 ||
            (row.original.status !== 'open' &&
              row.original.status !== 'investigating')
          ) {
            return '—'
          }
          return (
            <AlertConfirmButton
              alert={row.original}
              customerID={customer.id}
              onAccessDenied={onAccessDenied}
              onConfirmed={onConfirmed}
              onViewRecord={() =>
                setSelectedAlert({ id: row.original.id, customer })
              }
            />
          )
        },
        meta: { label: t('Actions') },
      },
    ],
    [t, getCustomer, getCustomerName, onAccessDenied, onConfirmed]
  )
  return (
    <>
      {forbidden ? (
        <>
          {props.customerFilter}
          <ErrorState
            title={t('Customer access has changed')}
            description={t('Return to My Customers and refresh the list.')}
          />
        </>
      ) : (
        <Tabs
          value={tab}
          onValueChange={setTab}
          className='h-full min-h-0 min-w-0'
        >
          <TabsList className='shrink-0'>
            <TabsTrigger value='audit'>{t('Operation logs')}</TabsTrigger>
            <TabsTrigger value='risk'>{t('Risk records')}</TabsTrigger>
          </TabsList>
          <TabsContent value='audit' className='flex min-h-0 min-w-0 flex-col'>
            <CustomerRecordList<CustomerAuditActivity>
              key={investigation?.event_id ?? 'all'}
              customerID={customerID}
              kind='audit'
              columns={auditColumns}
              customerFilter={props.customerFilter}
              paginationInFooter={props.paginationInFooter}
              initialFilter={
                investigation ? customerAuditRange(investigation) : undefined
              }
              onAccessDenied={onAccessDenied}
            />
          </TabsContent>
          <TabsContent value='risk' className='flex min-h-0 min-w-0 flex-col'>
            <CustomerRecordList<CustomerSecurityAlert>
              key={`${riskView}:${riskRevision}`}
              riskView={riskView}
              onRiskViewChange={(view) => {
                setLocalRiskView(view)
                props.onRiskViewChange?.(view)
              }}
              customerID={customerID}
              kind='security/alerts'
              columns={riskColumns}
              customerFilter={props.customerFilter}
              paginationInFooter={props.paginationInFooter}
              onAccessDenied={onAccessDenied}
            />
          </TabsContent>
        </Tabs>
      )}
      {selectedAlert && !forbidden && (
        <CustomerRiskDetail
          customerID={selectedAlert.customer.id}
          alertID={selectedAlert.id}
          accountName={getCustomerName({ customer: selectedAlert.customer })}
          onClose={() => setSelectedAlert(undefined)}
          onAccessDenied={onAccessDenied}
          onConfirmed={onConfirmed}
          onInvestigate={(event) => {
            if (customerID === undefined && props.onInvestigateCustomer) {
              props.onInvestigateCustomer(selectedAlert.customer, event)
              return
            }
            setInvestigation(event)
            setSelectedAlert(undefined)
            setTab('audit')
          }}
        />
      )}
    </>
  )
}

function CustomerRecordList<T>(props: {
  riskView?: AuditRiskView
  onRiskViewChange?: (view: AuditRiskView) => void
  customerID?: number
  kind: 'audit' | 'security/alerts'
  columns: ColumnDef<T>[]
  initialFilter?: CustomerAuditFilters
  customerFilter?: ReactNode
  paginationInFooter?: boolean
  onAccessDenied: () => void
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const queryClient = useQueryClient()
  const pending =
    props.kind === 'security/alerts' && props.riskView === 'unresolved'
  const [filters, setFilters] = useState(
    () => props.initialFilter ?? customerAuditRange()
  )
  const [applied, setApplied] = useState(filters)
  const [customRange, setCustomRange] = useState(!!props.initialFilter)
  const [appliedCustomRange, setAppliedCustomRange] = useState(customRange)
  const [cursors, setCursors] = useState([''])
  const cursor = cursors.at(-1) ?? ''
  const page = useQuery({
    queryKey: [
      'customer-audit',
      user?.id,
      props.customerID ?? 'all',
      user?.role,
      user?.is_employee,
      props.kind,
      props.riskView,
      AUDIT_ACTIVITY_VERSION,
      applied,
      cursor,
    ],
    queryFn: ({ signal }) => {
      const params = pending
        ? { view: 'unresolved', cursor }
        : {
            ...applied,
            cursor,
            ...(props.riskView === 'handled' ? { view: 'handled' } : {}),
          }
      if (props.customerID === undefined) {
        return employeeAuditGet<T>(props.kind, signal, params)
      }
      return customerAuditGet<CustomerAuditPage<T>>(
        props.customerID,
        props.kind,
        signal,
        params
      )
    },
    gcTime: 0,
    retry: false,
    meta: { errorToast: false },
  })
  const accessDenied = isCustomerAccessDenied(page.error)
  const onAccessDenied = props.onAccessDenied
  useEffect(() => {
    if (accessDenied) onAccessDenied()
  }, [accessDenied, onAccessDenied])
  const { table } = useDataTable({
    data: page.isError ? [] : (page.data?.items ?? []),
    columns: props.columns,
    enableSorting: false,
    manualPagination: true,
    manualFiltering: true,
    pageCount: -1,
    pagination: { pageIndex: cursors.length - 1, pageSize: 20 },
    onPaginationChange: (updater) => {
      if (page.isFetching) return
      const current = { pageIndex: cursors.length - 1, pageSize: 20 }
      const next = typeof updater === 'function' ? updater(current) : updater
      const nextCursor = page.data?.next_cursor
      if (next.pageIndex < current.pageIndex) {
        setCursors((values) => values.slice(0, next.pageIndex + 1))
      } else if (
        next.pageIndex === current.pageIndex + 1 &&
        nextCursor &&
        !page.isError
      ) {
        setCursors((values) => [...values, nextCursor])
      }
    },
  })
  const primaryFilters = (
    <>
      {props.customerFilter && (
        <LogsFilterField>{props.customerFilter}</LogsFilterField>
      )}
      {props.kind === 'security/alerts' && (
        <LogsFilterField>
          <Combobox
            aria-label={t('Handling status')}
            value={props.riskView ?? 'id'}
            options={[
              { value: 'unresolved', label: t('Unprocessed') },
              { value: 'handled', label: t('Processed') },
              { value: 'id', label: t('All') },
            ]}
            onValueChange={(value) => {
              if (
                value === 'unresolved' ||
                value === 'handled' ||
                value === 'id'
              ) {
                props.onRiskViewChange?.(value)
              }
            }}
            className='w-full'
          />
        </LogsFilterField>
      )}
      {!pending && (
        <LogsFilterField wide>
          <CompactDateTimeRangePicker
            start={new Date(filters.start * 1000)}
            end={new Date(filters.end * 1000)}
            maxRangeSec={31 * 86400}
            onChange={({ start, end }) => {
              if (start && end) {
                setFilters((current) => ({
                  ...current,
                  start: Math.floor(start.getTime() / 1000),
                  end: Math.floor(end.getTime() / 1000),
                }))
                setCustomRange(true)
              }
            }}
          />
        </LogsFilterField>
      )}
      {props.kind === 'audit' && (
        <>
          <LogsFilterField>
            <LogsFilterInput
              aria-label={t('Request ID')}
              placeholder={t('Request ID')}
              value={filters.request_id}
              onChange={(event) =>
                setFilters((current) => ({
                  ...current,
                  request_id: event.target.value,
                }))
              }
            />
          </LogsFilterField>
          <LogsFilterField>
            <LogsFilterInput
              aria-label='IP'
              placeholder='IP'
              value={filters.ip}
              onChange={(event) =>
                setFilters((current) => ({
                  ...current,
                  ip: event.target.value,
                }))
              }
            />
          </LogsFilterField>
        </>
      )}
    </>
  )
  let description = t('All handling statuses in the selected event time range.')
  let emptyTitle = t('No records in this time range')
  if (pending) {
    description = t(
      'Unprocessed records from all time, including records in progress.'
    )
    emptyTitle =
      props.customerID === undefined && cursors.length === 1
        ? t('No unprocessed risk records')
        : t('No matching unprocessed risk records')
  } else if (props.riskView === 'handled') {
    description = t(
      'Processed records include confirmed alerts and false positives. The date range uses the latest event time.'
    )
    emptyTitle = t('No processed risk records in this event time range')
  }
  if (page.isError) emptyTitle = t('Failed to load audit records')
  return (
    <div className='flex h-full min-h-0 min-w-0 flex-col gap-3'>
      {props.kind === 'security/alerts' && (
        <p className='text-muted-foreground shrink-0 text-xs'>{description}</p>
      )}
      <DataTablePage
        table={table}
        scrollResetKey={JSON.stringify([applied, cursor])}
        columns={props.columns}
        isLoading={page.isPending}
        isFetching={page.isFetching}
        paginationInFooter={props.paginationInFooter ?? false}
        cursorPagination={{
          hasNextPage: !!page.data?.next_cursor && !page.isError,
          isFetching: page.isFetching,
        }}
        className='h-auto min-h-0 min-w-0 flex-1'
        applyHeaderSize
        getColumnClassName={() => 'py-2'}
        tableClassName='flex flex-col [&_.overflow-auto]:[scrollbar-gutter:stable] [&_[data-slot=table]]:text-[13px] [&_[data-slot=table]_td]:text-[13px] [&_[data-slot=table]_td_*]:text-[13px] [&_[data-slot=table]_th]:text-[13px] [&_[data-slot=table]_th_*]:text-[13px]'
        emptyTitle={emptyTitle}
        emptyAction={
          page.isError && !accessDenied ? (
            <Button
              variant='outline'
              size='sm'
              onClick={() => void page.refetch()}
            >
              {t('Retry')}
            </Button>
          ) : undefined
        }
        toolbar={
          <LogsFilterToolbar
            table={table}
            primaryFilters={primaryFilters}
            hasActiveFilters={Boolean(
              customRange || filters.ip || filters.request_id
            )}
            searchLoading={page.isFetching}
            onSearch={() => {
              const range = customerAuditRange()
              const next = {
                ...filters,
                ...(!customRange && { start: range.start, end: range.end }),
                ip: filters.ip.trim(),
                request_id: filters.request_id.trim(),
              }
              setFilters(next)
              setApplied(next)
              setAppliedCustomRange(customRange)
              setCursors([''])
              if (
                cursor === '' &&
                JSON.stringify(next) === JSON.stringify(applied)
              ) {
                void page.refetch()
              }
            }}
            onReset={() => {
              const next = customerAuditRange()
              setFilters(next)
              setApplied(next)
              setCustomRange(false)
              setAppliedCustomRange(false)
              setCursors([''])
            }}
            actionEnd={
              <Button
                variant='outline'
                size='sm'
                disabled={page.isFetching}
                onClick={() => {
                  void queryClient.invalidateQueries({
                    queryKey: ['customer-audit', user?.id, 'pending-count'],
                  })
                  const range = customerAuditRange()
                  const next = appliedCustomRange
                    ? applied
                    : { ...applied, start: range.start, end: range.end }
                  setApplied(next)
                  if (!customRange) {
                    setFilters((current) => ({
                      ...current,
                      start: next.start,
                      end: next.end,
                    }))
                  }
                  setCursors([''])
                  if (
                    cursor === '' &&
                    next.start === applied.start &&
                    next.end === applied.end
                  ) {
                    void page.refetch()
                  }
                }}
              >
                {t('Refresh')}
              </Button>
            }
          />
        }
      />
    </div>
  )
}

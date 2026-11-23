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
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTablePage, useDataTable } from '@/components/data-table'
import { Button } from '@/components/ui/button'
import { CompactDateTimeRangePicker } from '@/features/usage-logs/components/compact-date-time-range-picker'
import {
  LogsFilterField,
  LogsFilterToolbar,
} from '@/features/usage-logs/components/logs-filter-toolbar'

import { defaultAuditTimeRange } from '../lib/time-range'
import {
  selfSecurityGet,
  type SelfSecurityAlert,
  type SelfSecurityPage,
} from './api'
import { useSelfSecurityColumns } from './columns'
import { SelfSecurityAlertDialog } from './detail'

const EMPTY_ALERTS: SelfSecurityAlert[] = []

export function SelfSecurity(props: { userID: number }) {
  const { t } = useTranslation()
  const [filters, setFilters] = useState(defaultAuditTimeRange)
  const [applied, setApplied] = useState(filters)
  const [customRange, setCustomRange] = useState(false)
  const [cursors, setCursors] = useState([''])
  const [selected, setSelected] = useState<number>()
  const cursor = cursors.at(-1) ?? ''
  const page = useQuery({
    queryKey: ['self-security', props.userID, 'alerts', applied, cursor],
    queryFn: ({ signal }) =>
      selfSecurityGet<SelfSecurityPage>('alerts', signal, {
        ...applied,
        cursor,
      }),
    enabled: props.userID > 0,
    gcTime: 0,
    retry: false,
    meta: { errorToast: false },
  })
  const columns = useSelfSecurityColumns(setSelected)
  const { table } = useDataTable({
    columns,
    data: page.isError ? EMPTY_ALERTS : (page.data?.items ?? EMPTY_ALERTS),
    getRowId: (row) => String(row.id),
    enableRowSelection: false,
    enableSorting: false,
    manualFiltering: true,
    manualPagination: true,
  })
  const refresh = () => {
    const next = customRange ? filters : defaultAuditTimeRange()
    setFilters(next)
    setApplied(next)
    setCursors([''])
    setSelected(undefined)
    if (
      cursor === '' &&
      next.start === applied.start &&
      next.end === applied.end
    ) {
      void page.refetch()
    }
  }
  return (
    <div className='flex h-full min-h-0 min-w-0 flex-col gap-3'>
      <DataTablePage
        table={table}
        scrollResetKey={JSON.stringify([applied, cursor])}
        columns={columns}
        isLoading={page.isPending}
        isFetching={page.isFetching}
        showPagination={false}
        className='h-auto min-h-0 min-w-0 flex-1'
        applyHeaderSize
        getColumnClassName={() => 'py-2'}
        emptyTitle={
          page.isError
            ? t('Failed to load audit records')
            : t('No records in this time range')
        }
        emptyAction={
          page.isError ? (
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
            primaryFilters={
              <LogsFilterField wide>
                <CompactDateTimeRangePicker
                  start={new Date(filters.start * 1000)}
                  end={new Date(filters.end * 1000)}
                  maxRangeSec={31 * 86400}
                  onChange={({ start, end }) => {
                    if (start && end) {
                      setFilters({
                        start: Math.floor(start.getTime() / 1000),
                        end: Math.floor(end.getTime() / 1000),
                      })
                      setCustomRange(true)
                    }
                  }}
                />
              </LogsFilterField>
            }
            hasActiveFilters={customRange}
            searchLoading={page.isFetching}
            onSearch={refresh}
            onReset={() => {
              const next = defaultAuditTimeRange()
              setFilters(next)
              setApplied(next)
              setCustomRange(false)
              setCursors([''])
              setSelected(undefined)
            }}
            actionEnd={
              <Button
                variant='outline'
                size='sm'
                disabled={page.isFetching}
                onClick={refresh}
              >
                {t('Refresh')}
              </Button>
            }
          />
        }
      />
      <div className='flex shrink-0 flex-wrap items-center justify-between gap-2'>
        <div className='flex flex-wrap items-center gap-2'>
          <Button
            variant='outline'
            size='sm'
            render={<Link to='/security' />}
            nativeButton={false}
            role='link'
          >
            {t('Security & Access')}
          </Button>
          <Button
            variant='outline'
            size='sm'
            render={<Link to='/keys' />}
            nativeButton={false}
            role='link'
          >
            {t('API Keys')}
          </Button>
        </div>
        <div className='ms-auto flex items-center gap-2'>
          <Button
            variant='outline'
            size='sm'
            disabled={cursors.length <= 1 || page.isFetching}
            onClick={() => {
              setSelected(undefined)
              setCursors((current) => current.slice(0, -1))
            }}
          >
            {t('Previous page')}
          </Button>
          <Button
            variant='outline'
            size='sm'
            disabled={
              !page.data?.next_cursor || page.isFetching || page.isError
            }
            onClick={() => {
              if (page.data?.next_cursor) {
                setSelected(undefined)
                setCursors((current) => [...current, page.data.next_cursor])
              }
            }}
          >
            {t('Next page')}
          </Button>
        </div>
      </div>
      {selected !== undefined && !page.isError && (
        <SelfSecurityAlertDialog
          key={selected}
          userID={props.userID}
          id={selected}
          onClose={() => setSelected(undefined)}
        />
      )}
    </div>
  )
}

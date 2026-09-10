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

import { keepPreviousData, useQuery } from '@tanstack/react-query'
import {
  getCoreRowModel,
  useReactTable,
  type ColumnDef,
  type PaginationState,
} from '@tanstack/react-table'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTablePage } from '@/components/data-table/data-table-page'
import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useAuthStore } from '@/stores/auth-store'

import { getRequestLogs } from './api'
import { RequestLogDetailDialog } from './request-log-detail-dialog'
import type { RequestLogFilters, RequestLogItem } from './types'

function formatTime(ts: number): string {
  if (!ts) return '-'
  return new Date(ts * 1000).toLocaleString()
}

function formatBytes(bytes: number): string {
  if (!bytes || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit += 1
  }
  return `${value.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`
}

function formatDuration(ms: number): string {
  if (!ms || ms <= 0) return '-'
  if (ms < 1000) return `${ms} ms`
  return `${(ms / 1000).toFixed(2)} s`
}

function statusVariant(
  status: number
): 'default' | 'secondary' | 'destructive' {
  if (status >= 200 && status < 300) return 'default'
  if (status >= 400) return 'destructive'
  return 'secondary'
}

/**
 * RequestLogs 展示请求日志列表；原始详情按钮仅对超级管理员启用，服务端另行校验权限。
 * 无参数；返回带筛选、分页和详情弹窗的页面，权限变化会同步重建操作列。
 */
export function RequestLogs() {
  const { t } = useTranslation()
  // 选择器 s 是认证状态快照；仅订阅派生权限值，避免无关账户字段变化触发重绘。
  const isRoot = useAuthStore((s) => (s.auth.user?.role ?? 0) >= 100)
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: 20,
  })
  const [filters, setFilters] = useState<RequestLogFilters>({})
  const [draft, setDraft] = useState<RequestLogFilters>({})
  const [detailId, setDetailId] = useState<number | null>(null)

  const { data, isLoading, isFetching, isError } = useQuery({
    queryKey: [
      'request-logs',
      pagination.pageIndex,
      pagination.pageSize,
      filters,
    ],
    queryFn: () =>
      getRequestLogs(pagination.pageIndex + 1, pagination.pageSize, filters),
    placeholderData: keepPreviousData,
  })

  const items = data?.items ?? []
  const total = data?.total ?? 0
  const pageCount = Math.max(1, Math.ceil(total / pagination.pageSize))

  const columns = useMemo<ColumnDef<RequestLogItem>[]>(
    () => [
      {
        accessorKey: 'created_at',
        header: t('Time'),
        cell: ({ row }) => (
          <span className='text-xs whitespace-nowrap'>
            {formatTime(row.original.created_at)}
          </span>
        ),
      },
      {
        accessorKey: 'username',
        header: t('Username'),
        cell: ({ row }) => row.original.username || '-',
      },
      {
        accessorKey: 'model_name',
        header: t('Model'),
        cell: ({ row }) => row.original.model_name || '-',
      },
      {
        accessorKey: 'channel_id',
        header: t('Channel'),
        cell: ({ row }) => row.original.channel_id || '-',
      },
      {
        accessorKey: 'method',
        header: t('Method'),
      },
      {
        accessorKey: 'url',
        header: t('Path'),
        cell: ({ row }) => (
          <span
            className='block max-w-70 truncate font-mono text-xs'
            title={row.original.url}
          >
            {row.original.url}
          </span>
        ),
      },
      {
        accessorKey: 'status_code',
        header: t('Status'),
        cell: ({ row }) => (
          <span className='whitespace-nowrap'>
            <Badge variant={statusVariant(row.original.status_code)}>
              {row.original.status_code}
            </Badge>
            {row.original.is_stream && (
              <Badge variant='secondary' className='ml-1'>
                {t('Stream')}
              </Badge>
            )}
          </span>
        ),
      },
      {
        accessorKey: 'use_time_ms',
        header: t('Duration'),
        cell: ({ row }) => (
          <span className='text-xs whitespace-nowrap'>
            {formatDuration(row.original.use_time_ms)}
          </span>
        ),
      },
      {
        accessorKey: 'request_body_size',
        header: t('Req Size'),
        cell: ({ row }) => (
          <span className='text-xs whitespace-nowrap'>
            {formatBytes(row.original.request_body_size)}
          </span>
        ),
      },
      {
        accessorKey: 'response_body_size',
        header: t('Resp Size'),
        cell: ({ row }) => (
          <span className='text-xs whitespace-nowrap'>
            {formatBytes(row.original.response_body_size)}
          </span>
        ),
      },
      {
        id: 'actions',
        header: t('Actions'),
        // row 为当前表格行；无参点击回调选中其日志 ID，非 Root 禁用按钮，后端仍保留最终鉴权。
        cell: ({ row }) => (
          <Button
            variant='outline'
            size='sm'
            disabled={!isRoot}
            onClick={() => setDetailId(row.original.id)}
          >
            {t('View')}
          </Button>
        ),
      },
    ],
    [t, isRoot] // 语言或权限变化时刷新列定义，不沿用旧账号的详情按钮状态。
  )

  const table = useReactTable({
    data: items,
    columns,
    state: { pagination },
    manualPagination: true,
    pageCount,
    onPaginationChange: setPagination,
    getCoreRowModel: getCoreRowModel(),
  })

  const applyFilters = () => {
    setPagination((p) => ({ ...p, pageIndex: 0 }))
    setFilters(draft)
  }

  const resetFilters = () => {
    setDraft({})
    setFilters({})
    setPagination((p) => ({ ...p, pageIndex: 0 }))
  }

  const toolbar = (
    <div className='flex flex-wrap items-end gap-2'>
      <div className='space-y-1'>
        <label className='text-muted-foreground text-xs'>{t('Username')}</label>
        <Input
          className='h-9 w-40'
          value={draft.username ?? ''}
          onChange={(e) =>
            setDraft((p) => ({ ...p, username: e.target.value }))
          }
          onKeyDown={(e) => e.key === 'Enter' && applyFilters()}
        />
      </div>
      <div className='space-y-1'>
        <label className='text-muted-foreground text-xs'>{t('Model')}</label>
        <Input
          className='h-9 w-40'
          value={draft.model_name ?? ''}
          onChange={(e) =>
            setDraft((p) => ({ ...p, model_name: e.target.value }))
          }
          onKeyDown={(e) => e.key === 'Enter' && applyFilters()}
        />
      </div>
      <div className='space-y-1'>
        <label className='text-muted-foreground text-xs'>
          {t('Request ID')}
        </label>
        <Input
          className='h-9 w-56'
          value={draft.request_id ?? ''}
          onChange={(e) =>
            setDraft((p) => ({ ...p, request_id: e.target.value }))
          }
          onKeyDown={(e) => e.key === 'Enter' && applyFilters()}
        />
      </div>
      <Button onClick={applyFilters}>{t('Search')}</Button>
      <Button variant='outline' onClick={resetFilters}>
        {t('Reset')}
      </Button>
    </div>
  )

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Request Logs')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <DataTablePage
          table={table}
          columns={columns}
          isLoading={isLoading}
          isFetching={isFetching}
          emptyTitle={
            isError ? t('Failed to load request logs') : t('No Logs Found')
          }
          emptyDescription={
            isError
              ? t(
                  'Please retry. If the problem persists, check the server logs.'
                )
              : t(
                  'No request logs available. Logs will appear here once relay requests are made.'
                )
          }
          toolbar={toolbar}
          hideMobile
          tableHeaderClassName='bg-muted/30 sticky top-0 z-10'
        />

        <RequestLogDetailDialog
          id={detailId}
          open={detailId !== null}
          onOpenChange={(open) => !open && setDetailId(null)}
        />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

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
import {
  useInfiniteQuery,
  useMutation,
  useQueryClient,
} from '@tanstack/react-query'
import { isAxiosError } from 'axios'
import dayjs from 'dayjs'
import { Download, RefreshCw, Trash2, X } from 'lucide-react'
import { useEffect, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Progress } from '@/components/ui/progress'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { cn } from '@/lib/utils'

import { LOG_TYPE_FILTERS } from '../../constants'
import { deleteExportJob, downloadExportPart, getExportJobs } from '../api'
import {
  EXPORT_SOURCE_LABELS,
  type ExportJobPage,
  type ExportJob,
  type ExportJobStatus,
  type ExportJobListFilters,
} from '../types'
import { ExportJobFilters } from './export-job-filters'
import { ExportRemainingTime } from './export-remaining-time'

/** Stable backend error codes → user-facing copy. Raw Go errors never reach the UI. */
const ERROR_MESSAGES: Record<string, string> = {
  access_changed:
    'Export access changed. Review your permissions before exporting again.',
  risk_changed:
    'Risk records changed during export. Please create a new export.',
  disk_full:
    'Not enough server storage for this export. Please free up space or narrow the range.',
  too_many_parts:
    'Too much data for one export. Please narrow the time range and try again.',
  timeout: 'The export timed out. Try a shorter time range.',
  too_many_groups:
    'Too many dimension combinations. Please use fewer dimensions or a shorter time range.',
  internal: 'Export failed. Please try again or contact an administrator.',
  reprice_failed:
    'The export charge could not be calculated. Turn off the group ratio override or narrow the filters and try again.',
}

const STATUS_VARIANTS: Record<
  ExportJobStatus,
  'default' | 'secondary' | 'destructive' | 'outline'
> = {
  pending: 'secondary',
  running: 'secondary',
  ready: 'default',
  failed: 'destructive',
  canceled: 'outline',
}

const SKELETON_ROWS = ['s1', 's2', 's3']

const STATUS_LABELS: Record<ExportJobStatus, string> = {
  pending: 'Queued',
  running: 'Running',
  ready: 'Completed',
  failed: 'Failed',
  canceled: 'Canceled',
}

function formatBytes(bytes: number): string {
  if (!bytes) return '-'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${value.toFixed(value >= 10 || unit === 0 ? 0 : 1)} ${units[unit]}`
}

function formatRange(job: ExportJob): string {
  const { start_timestamp: start, end_timestamp: end } = job.filters
  if (!start || !end) return '-'
  return `${dayjs.unix(start).format('MM-DD HH:mm')} → ${dayjs.unix(end).format('MM-DD HH:mm')}`
}

function filterSummary(job: ExportJob, typeLabel: string): string {
  const parts: string[] = [typeLabel]
  if (job.filters.model_name) parts.push(job.filters.model_name)
  if (job.filters.username) parts.push(job.filters.username)
  if (job.filters.token_name) parts.push(job.filters.token_name)
  if (job.filters.group) parts.push(job.filters.group)
  if (job.filters.channel) parts.push(`#${job.filters.channel}`)
  return parts.join(' · ')
}

export function ExportTaskList(props: {
  dataSource: 'usage' | 'audit' | 'quota'
  scope:
    | 'usage_admin'
    | 'usage_self'
    | 'audit_admin'
    | 'audit_self'
    | 'quota_admin'
  apiBase: string
  userId?: number
  self?: boolean
  responsive?: boolean
  toolbarLeading?: ReactNode
  filters?: ExportJobListFilters
  onApplyFilters?: (filters: ExportJobListFilters) => void
  onAccessDenied?: () => Promise<void>
  actions?: (data: ExportJobPage | undefined, unavailable: boolean) => ReactNode
  summary?: (job: ExportJob) => string
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [deleteTarget, setDeleteTarget] = useState<ExportJob>()
  const [denied, setDenied] = useState(false)
  const filters = props.filters ?? {}
  const queryPrefix = [
    'log-export-jobs',
    props.dataSource,
    props.scope,
    props.userId,
  ]
  const handleError = (err: Error) => {
    if (isAxiosError(err) && err.response?.status === 403) {
      setDenied(true)
      void queryClient.cancelQueries({ queryKey: queryPrefix })
      queryClient.removeQueries({ queryKey: queryPrefix })
      void props.onAccessDenied?.()
    } else toast.error(err.message || t('Operation failed'))
  }
  const {
    data: pages,
    isLoading,
    isFetching,
    refetch,
    error,
    hasNextPage,
    fetchNextPage,
    isFetchingNextPage,
  } = useInfiniteQuery({
    queryKey: [...queryPrefix, filters],
    enabled: !denied,
    retry: (count, err) =>
      !(isAxiosError(err) && err.response?.status === 403) && count < 2,
    initialPageParam: '',
    queryFn: ({ pageParam }) =>
      getExportJobs(false, props.self, filters, pageParam, props.apiBase),
    getNextPageParam: (page) => page.next_cursor || undefined,
    // Poll only while something is actually running, and never in a background
    // tab — an idle export page must not keep hitting the server.
    refetchInterval: (query) =>
      !denied &&
      query.state.data?.pages.some((page) =>
        page.jobs.some(
          (job) => job.status === 'running' || job.status === 'pending'
        )
      )
        ? 3000
        : false,
    refetchIntervalInBackground: false,
  })

  const removeJob = useMutation({
    mutationFn: (jobId: string) =>
      deleteExportJob(jobId, props.self, props.apiBase),
    onSuccess: () => {
      setDeleteTarget(undefined)
      queryClient.invalidateQueries({
        queryKey: queryPrefix,
      })
    },
    onError: handleError,
  })

  const download = useMutation({
    mutationFn: ({ jobId, part }: { jobId: string; part?: number }) =>
      downloadExportPart(jobId, part, props.self, props.apiBase),
    onError: handleError,
  })

  const onAccessDenied = props.onAccessDenied
  const { dataSource, scope, userId } = props
  useEffect(() => {
    if (isAxiosError(error) && error.response?.status === 403) {
      setDenied(true)
      const queryKey = ['log-export-jobs', dataSource, scope, userId]
      void queryClient.cancelQueries({ queryKey })
      queryClient.removeQueries({ queryKey })
      void onAccessDenied?.()
    }
  }, [error, onAccessDenied, queryClient, dataSource, scope, userId])
  useEffect(
    () => () => {
      if (props.dataSource === 'audit' || props.dataSource === 'quota') {
        void queryClient.cancelQueries({
          queryKey: [
            'log-export-jobs',
            props.dataSource,
            props.scope,
            props.userId,
          ],
        })
        queryClient.removeQueries({
          queryKey: [
            'log-export-jobs',
            props.dataSource,
            props.scope,
            props.userId,
          ],
        })
      }
    },
    [queryClient, props.dataSource, props.scope, props.userId]
  )
  const data = pages?.pages[0]
  const jobs = [
    ...new Map(
      (denied ? [] : pages?.pages)
        ?.flatMap((page) => page.jobs)
        .map((job) => [job.job_id, job]) ?? []
    ).values(),
  ]
  let emptyMessage = 'No exports yet'
  if (
    Object.values(filters).some((value) => value !== undefined && value !== '')
  ) {
    emptyMessage = 'No matching exports. Adjust or reset the filters.'
  }
  if (hasNextPage) {
    emptyMessage = 'More tasks remain to be checked. Continue searching.'
  }

  return (
    <div className='flex min-w-0 flex-col gap-4'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        {props.toolbarLeading}
        <div className='ml-auto flex flex-wrap items-center gap-2'>
          {props.actions?.(
            data,
            isLoading || !!error || denied || data?.enabled === false
          )}
          <Button
            type='button'
            variant='outline'
            size='icon-sm'
            aria-label={t('Refresh')}
            disabled={denied || isFetching}
            onClick={() => void refetch()}
          >
            <RefreshCw className={isFetching ? 'animate-spin' : ''} />
          </Button>
        </div>
      </div>

      {data && !data.enabled && (
        <p className='text-muted-foreground text-sm'>
          {t(
            props.self && props.dataSource !== 'audit'
              ? 'Self-service export is currently unavailable. Please contact sales for help downloading your logs.'
              : 'Log export is currently turned off'
          )}
        </p>
      )}

      {props.onApplyFilters && (
        <ExportJobFilters
          key={JSON.stringify(filters)}
          value={filters}
          onApply={props.onApplyFilters}
          self={props.self}
          quotaTasks={props.dataSource === 'quota'}
          jobs={jobs}
          hasMoreJobs={hasNextPage}
          loadingMoreJobs={isFetchingNextPage}
          onLoadMoreJobs={() => void fetchNextPage()}
        />
      )}

      {error && (
        <p className='text-destructive text-sm'>
          {(error as Error).message ||
            t(
              'Background export is unavailable. Please contact an administrator.'
            )}
        </p>
      )}

      <div className='min-w-0 rounded-md border'>
        <Table
          className={cn(props.responsive && 'max-lg:block lg:table-fixed')}
        >
          <TableHeader className={cn(props.responsive && 'max-lg:sr-only')}>
            <TableRow>
              <TableHead className={cn(props.responsive && 'w-44')}>
                {t('Created')}
              </TableHead>
              <TableHead className={cn(props.responsive && 'w-36')}>
                {t('Date Range')}
              </TableHead>
              <TableHead>{t('Filters')}</TableHead>
              <TableHead className={cn(props.responsive && 'w-20')}>
                {t('Format')}
              </TableHead>
              <TableHead
                className={cn('text-right', props.responsive && 'w-24')}
              >
                {t('Rows')}
              </TableHead>
              <TableHead className={props.responsive ? 'w-36' : 'w-[180px]'}>
                {t('Progress')}
              </TableHead>
              <TableHead
                className={cn('text-right', props.responsive && 'w-32')}
              >
                {t('Actions')}
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody
            className={cn(props.responsive && 'max-lg:block [&>tr]:h-auto')}
          >
            {isLoading &&
              SKELETON_ROWS.map((rowKey) => (
                <TableRow key={rowKey}>
                  <TableCell colSpan={7}>
                    <Skeleton className='h-6 w-full' />
                  </TableCell>
                </TableRow>
              ))}

            {!isLoading && !error && jobs.length === 0 && (
              <TableRow>
                <TableCell
                  colSpan={7}
                  className='text-muted-foreground py-10 text-center'
                >
                  {t(emptyMessage)}
                </TableCell>
              </TableRow>
            )}

            {jobs.map((job) => {
              const parts = job.parts ?? []
              const isActive =
                job.status === 'running' || job.status === 'pending'
              return (
                <TableRow
                  key={job.job_id}
                  className={cn(
                    props.responsive &&
                      'max-lg:grid max-lg:grid-cols-2 max-lg:gap-x-4 max-lg:gap-y-3 max-lg:p-4 [&>td]:min-w-0 [&>td]:whitespace-normal max-lg:[&>td]:p-0 lg:[&>td]:py-4'
                  )}
                >
                  <TableCell
                    className={cn(
                      'font-mono text-xs whitespace-nowrap',
                      props.responsive && 'max-lg:order-1'
                    )}
                  >
                    {dayjs.unix(job.created_at).format('MM-DD HH:mm:ss')}
                    {job.username && (
                      <div
                        className='text-muted-foreground break-all'
                        data-table-text={
                          props.responsive ? 'secondary' : undefined
                        }
                      >
                        {job.username}
                      </div>
                    )}
                    {job.source && (
                      <div
                        className='text-muted-foreground'
                        data-table-text={
                          props.responsive ? 'secondary' : undefined
                        }
                      >
                        {t(EXPORT_SOURCE_LABELS[job.source])}
                      </div>
                    )}
                    <div
                      className='text-muted-foreground max-w-48 truncate'
                      title={job.job_id}
                      data-table-text={
                        props.responsive ? 'secondary' : undefined
                      }
                    >
                      {job.job_id}
                    </div>
                  </TableCell>
                  <TableCell
                    className={cn(
                      'text-xs whitespace-nowrap',
                      props.responsive && 'max-lg:order-3 max-lg:col-span-2'
                    )}
                  >
                    {props.responsive && (
                      <div
                        data-table-text='secondary'
                        className='text-muted-foreground mb-1 lg:hidden'
                      >
                        {t('Date Range')}
                      </div>
                    )}
                    {props.responsive &&
                    job.filters.start_timestamp &&
                    job.filters.end_timestamp ? (
                      <div className='flex flex-wrap gap-x-1 lg:flex-col'>
                        <span className='whitespace-nowrap'>
                          {dayjs
                            .unix(job.filters.start_timestamp)
                            .format('MM-DD HH:mm')}
                        </span>
                        <span className='whitespace-nowrap'>
                          {'→ '}
                          {dayjs
                            .unix(job.filters.end_timestamp)
                            .format('MM-DD HH:mm')}
                        </span>
                      </div>
                    ) : (
                      formatRange(job)
                    )}
                  </TableCell>
                  <TableCell
                    className={cn(
                      'max-w-[220px] truncate text-xs',
                      props.responsive &&
                        'max-w-none overflow-visible break-words max-lg:order-4 max-lg:col-span-2'
                    )}
                  >
                    {props.responsive && (
                      <div
                        data-table-text='secondary'
                        className='text-muted-foreground mb-1 lg:hidden'
                      >
                        {t('Filters')}
                      </div>
                    )}
                    {props.summary
                      ? props.summary(job)
                      : filterSummary(
                          job,
                          t(
                            LOG_TYPE_FILTERS.find(
                              (item) =>
                                Number(item.value) === (job.filters.type || 0)
                            )?.label ?? 'Unknown'
                          )
                        )}
                    {job.group_ratio_override != null && (
                      <div className='text-muted-foreground'>
                        {t('Group ratio: {{ratio}}', {
                          ratio: job.group_ratio_override,
                        })}
                      </div>
                    )}
                  </TableCell>
                  <TableCell
                    className={cn(
                      'text-xs',
                      props.responsive && 'max-lg:order-5'
                    )}
                  >
                    {props.responsive && (
                      <div
                        data-table-text='secondary'
                        className='text-muted-foreground mb-1 lg:hidden'
                      >
                        {t('Format')}
                      </div>
                    )}
                    {job.format === 'xlsx' ? 'xlsx' : 'csv.gz'}
                    {job.format_downgraded && (
                      <div className='text-muted-foreground'>
                        {t('Switched from Excel')}
                      </div>
                    )}
                  </TableCell>
                  <TableCell
                    className={cn(
                      'text-right font-mono text-xs tabular-nums',
                      props.responsive && 'break-words max-lg:order-6'
                    )}
                  >
                    {props.responsive && (
                      <div
                        data-table-text='secondary'
                        className='text-muted-foreground mb-1 lg:hidden'
                      >
                        {t('Rows')}
                      </div>
                    )}
                    {job.row_count.toLocaleString()}
                    {/*
                      With an anomaly filter, a job can scan a million rows and
                      write three hundred. Showing only the written count makes
                      a long-running job look broken, so the scanned count sits
                      right underneath it.
                    */}
                    {!props.self &&
                      job.scanned_rows != null &&
                      job.scanned_rows > job.row_count && (
                        <div className='text-muted-foreground'>
                          {t('of {{scanned}} scanned', {
                            scanned: job.scanned_rows.toLocaleString(),
                          })}
                        </div>
                      )}
                    {job.bytes_out > 0 && (
                      <div className='text-muted-foreground'>
                        {formatBytes(job.bytes_out)}
                      </div>
                    )}
                  </TableCell>
                  <TableCell
                    className={cn(
                      props.responsive &&
                        'break-words max-lg:order-2 max-lg:text-right'
                    )}
                  >
                    <div className='flex flex-col gap-1'>
                      <Badge variant={STATUS_VARIANTS[job.status]}>
                        {t(STATUS_LABELS[job.status])}
                      </Badge>
                      {isActive && (
                        <Progress value={job.progress} className='h-1.5' />
                      )}
                      {props.self && <ExportRemainingTime job={job} />}
                      {props.self &&
                        job.status === 'ready' &&
                        job.row_count === 0 && (
                          <p className='text-muted-foreground text-xs'>
                            {t(
                              'No logs matched these filters. Adjust the filters and try again.'
                            )}
                          </p>
                        )}
                      {props.self &&
                        job.status === 'ready' &&
                        job.expires_at != null &&
                        job.expires_at > 0 && (
                          <p className='text-muted-foreground text-xs'>
                            {t('Download available until {{time}}', {
                              time: dayjs
                                .unix(job.expires_at)
                                .format('YYYY-MM-DD HH:mm'),
                            })}
                          </p>
                        )}
                      {job.status === 'failed' && (
                        <p className='text-muted-foreground text-[11px]'>
                          {t(
                            props.self && props.dataSource === 'usage'
                              ? ERROR_MESSAGES.internal
                              : (ERROR_MESSAGES[job.error ?? 'internal'] ??
                                  ERROR_MESSAGES.internal)
                          )}
                        </p>
                      )}
                    </div>
                  </TableCell>
                  <TableCell
                    className={cn(
                      'text-right',
                      props.responsive && 'max-lg:order-7 max-lg:col-span-2'
                    )}
                  >
                    <div className='flex flex-wrap justify-end gap-1'>
                      {job.can_download &&
                        job.status === 'ready' &&
                        parts.length > 0 &&
                        (parts.length === 1 ? (
                          <Button
                            type='button'
                            variant='outline'
                            size='sm'
                            disabled={download.isPending}
                            onClick={() =>
                              download.mutate({
                                jobId: job.job_id,
                                part: parts[0].index,
                              })
                            }
                          >
                            <Download data-icon='inline-start' />
                            {t('Download')}
                          </Button>
                        ) : (
                          <DropdownMenu>
                            <DropdownMenuTrigger
                              render={
                                <Button
                                  type='button'
                                  variant='outline'
                                  size='sm'
                                  disabled={download.isPending}
                                />
                              }
                            >
                              <Download data-icon='inline-start' />
                              {t('Download')}
                            </DropdownMenuTrigger>
                            <DropdownMenuContent align='end' className='w-72'>
                              {parts.length > 1 && (
                                <>
                                  <DropdownMenuItem
                                    onClick={() =>
                                      download.mutate({ jobId: job.job_id })
                                    }
                                  >
                                    {t('All parts ({{count}}) · {{size}}', {
                                      count: parts.length,
                                      size: formatBytes(job.bytes_out),
                                    })}
                                  </DropdownMenuItem>
                                  <DropdownMenuSeparator />
                                </>
                              )}
                              {parts.map((part) => (
                                <DropdownMenuItem
                                  key={part.index}
                                  onClick={() =>
                                    download.mutate({
                                      jobId: job.job_id,
                                      part: part.index,
                                    })
                                  }
                                >
                                  <div className='flex flex-col'>
                                    <span>
                                      {part.kind === 'summary'
                                        ? t('Summary file')
                                        : t('Part {{index}}', {
                                            index: part.index,
                                          })}{' '}
                                      · {part.rows.toLocaleString()} {t('rows')}
                                    </span>
                                    <span className='text-muted-foreground text-[11px]'>
                                      {/* 汇总覆盖整个导出范围，逐行时间跨度对它没有意义；
                                        与明细分片并排显示同一种时间格式只会让人以为
                                        它漏了数据或范围搞错了。 */}
                                      {part.kind === 'summary'
                                        ? t('Aggregated over the whole range')
                                        : `${dayjs
                                            .unix(part.start_time)
                                            .format('MM-DD HH:mm')} – ${dayjs
                                            .unix(part.end_time)
                                            .format('MM-DD HH:mm')}`}
                                      {' · '}
                                      {formatBytes(part.bytes)}
                                    </span>
                                  </div>
                                </DropdownMenuItem>
                              ))}
                            </DropdownMenuContent>
                          </DropdownMenu>
                        ))}
                      {(job.can_cancel || job.can_delete) && (
                        <Button
                          type='button'
                          variant='ghost'
                          size='icon-sm'
                          aria-label={isActive ? t('Cancel') : t('Delete')}
                          disabled={removeJob.isPending}
                          onClick={() => {
                            if (isActive) removeJob.mutate(job.job_id)
                            else setDeleteTarget(job)
                          }}
                        >
                          {isActive ? <X /> : <Trash2 />}
                        </Button>
                      )}
                    </div>
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>

      {hasNextPage && (
        <Button
          variant='outline'
          disabled={isFetching}
          onClick={() => void fetchNextPage()}
        >
          {isFetchingNextPage ? t('Loading...') : t('Continue searching')}
        </Button>
      )}
      <ConfirmDialog
        open={Boolean(deleteTarget)}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(undefined)
        }}
        title={t('Delete export')}
        desc={t(
          'Delete this export task and its files? No one will be able to download it afterward. Original logs will not be deleted.'
        )}
        confirmText={t('Delete')}
        destructive
        isLoading={removeJob.isPending}
        handleConfirm={() => {
          if (deleteTarget) removeJob.mutate(deleteTarget.job_id)
        }}
      />
    </div>
  )
}

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
import { getRouteApi } from '@tanstack/react-router'
import dayjs from 'dayjs'
import { Download, Plus, RefreshCw, Trash2, X } from 'lucide-react'
import { useMemo, useState } from 'react'
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
import { canViewAdminMenu } from '@/lib/admin-permissions'
import { useAuthStore } from '@/stores/auth-store'

import { LOG_TYPE_FILTERS } from '../constants'
import { deleteExportJob, downloadExportPart, getExportJobs } from './api'
import { EmployeeTemplateManager } from './components/employee-template-manager'
import { ExportJobFilters } from './components/export-job-filters'
import { ExportRemainingTime } from './components/export-remaining-time'
import {
  NewExportSheet,
  type NewExportPrefill,
} from './components/new-export-sheet'
import { SelfExportSheet } from './components/self-export-sheet'
import {
  EXPORT_SOURCE_LABELS,
  type AnomalyFilters,
  type ExportJob,
  type ExportJobStatus,
  type ExportJobListFilters,
} from './types'

/** Stable backend error codes → user-facing copy. Raw Go errors never reach the UI. */
const ERROR_MESSAGES: Record<string, string> = {
  disk_full:
    'Not enough server storage for this export. Please free up space or narrow the range.',
  too_many_parts:
    'Too much data for one export. Please narrow the time range and try again.',
  timeout:
    'The export took too long and was stopped. Anomaly filters still scan the whole time range, so try a shorter one.',
  too_many_groups:
    'Too many dimension combinations. Please use fewer dimensions or a shorter time range.',
  internal: 'Export failed. Please try again or contact an administrator.',
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

const route = getRouteApi('/_authenticated/usage-logs/$section')

/** Search params carry epoch milliseconds; tolerate ISO strings too. */
function toDate(value?: number | string): Date | undefined {
  if (!value) return undefined
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? undefined : date
}

export function LogExportCenter(props: { self?: boolean }) {
  const canManageEmployees = useAuthStore((state) =>
    canViewAdminMenu(state.auth.user, 'employees')
  )
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [sheetOpen, setSheetOpen] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<ExportJob>()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const navigate = route.useNavigate()

  // 从使用日志页「高级导出」跳转过来时，URL 上带着当时的筛选条件，
  // 用它预填新建表单，用户不必再填一遍。
  const search = route.useSearch()
  const filters: ExportJobListFilters = search.exportFilters ?? {}
  const applyFilters = (value: ExportJobListFilters) => {
    void navigate({
      search: (previous) => ({
        ...previous,
        exportFilters: Object.keys(value).length ? value : undefined,
      }),
      replace: true,
    })
  }

  // 「查同类」的三项特征都为空时返回 undefined，让下面照常走普通预填。
  const anomalyPrefill = useMemo<AnomalyFilters | undefined>(() => {
    const filters: AnomalyFilters = {}
    if (search.anomalyUsageSource) {
      filters.usage_source = [search.anomalyUsageSource]
    }
    if (search.anomalyEndReason) {
      filters.stream_end_reason = [search.anomalyEndReason]
    }
    if (search.anomalyMinRetry) {
      filters.min_retry_count = search.anomalyMinRetry
    }
    return Object.keys(filters).length ? filters : undefined
  }, [
    search.anomalyUsageSource,
    search.anomalyEndReason,
    search.anomalyMinRetry,
  ])

  const prefill = useMemo<NewExportPrefill>(
    () => ({
      start: toDate(search.startTime),
      end: toDate(search.endTime),
      logType: Array.isArray(search.type) ? search.type[0] : search.type,
      model: search.model || undefined,
      username: search.username || undefined,
      token: search.token || undefined,
      channel: search.channel || undefined,
      group: search.group || undefined,
      // 「查同类」跳转：带着该行可观察的特征进来，直接落进异常排查模板。
      anomaly: anomalyPrefill,
      templateId: anomalyPrefill ? 'builtin:anomaly' : undefined,
    }),
    [search, anomalyPrefill]
  )

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
    queryKey: [
      'log-export-jobs',
      props.self ? 'self' : 'admin',
      userId,
      filters,
    ],
    initialPageParam: '',
    queryFn: ({ pageParam }) =>
      getExportJobs(false, props.self, filters, pageParam),
    getNextPageParam: (page) => page.next_cursor || undefined,
    // Poll only while something is actually running, and never in a background
    // tab — an idle export page must not keep hitting the server.
    refetchInterval: (query) =>
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
    mutationFn: (jobId: string) => deleteExportJob(jobId, props.self),
    onSuccess: () => {
      setDeleteTarget(undefined)
      queryClient.invalidateQueries({
        queryKey: ['log-export-jobs', props.self ? 'self' : 'admin'],
      })
    },
    onError: (err: Error) => toast.error(err.message || t('Operation failed')),
  })

  const download = useMutation({
    mutationFn: ({ jobId, part }: { jobId: string; part?: number }) =>
      downloadExportPart(jobId, part, props.self),
    onError: (err: Error) =>
      toast.error(
        err.message || t('Download link expired. Click download again.')
      ),
  })

  const data = pages?.pages[0]
  const jobs = [
    ...new Map(
      pages?.pages
        .flatMap((page) => page.jobs)
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
        <div className='flex items-center gap-2'>
          <Button
            type='button'
            size='sm'
            onClick={() => setSheetOpen(true)}
            disabled={isLoading || !!error || data?.enabled === false}
          >
            <Plus data-icon='inline-start' />
            {t('New Export')}
          </Button>
          {!props.self && canManageEmployees && <EmployeeTemplateManager />}
          {!props.self && data && !data.enabled && (
            <Badge variant='destructive'>
              {t('Log export is currently turned off')}
            </Badge>
          )}
        </div>
        <Button
          type='button'
          variant='outline'
          size='icon-sm'
          aria-label={t('Refresh')}
          onClick={() => void refetch()}
        >
          <RefreshCw className={isFetching ? 'animate-spin' : ''} />
        </Button>
      </div>

      {props.self && data && !data.enabled && (
        <p className='text-muted-foreground text-sm'>
          {t(
            'Self-service export is currently unavailable. Please contact sales for help downloading your logs.'
          )}
        </p>
      )}

      <ExportJobFilters
        key={JSON.stringify(filters)}
        value={filters}
        onApply={applyFilters}
        self={props.self}
        jobs={jobs}
        hasMoreJobs={hasNextPage}
        loadingMoreJobs={isFetchingNextPage}
        onLoadMoreJobs={() => void fetchNextPage()}
      />

      {error && (
        <p className='text-destructive text-sm'>
          {(error as Error).message ||
            t(
              'Background export is unavailable. Please contact an administrator.'
            )}
        </p>
      )}

      <div className='rounded-md border'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Created')}</TableHead>
              <TableHead>{t('Date Range')}</TableHead>
              <TableHead>{t('Filters')}</TableHead>
              <TableHead>{t('Format')}</TableHead>
              <TableHead className='text-right'>{t('Rows')}</TableHead>
              <TableHead className='w-[180px]'>{t('Progress')}</TableHead>
              <TableHead className='text-right'>{t('Actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
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
                <TableRow key={job.job_id}>
                  <TableCell className='font-mono text-xs whitespace-nowrap'>
                    {dayjs.unix(job.created_at).format('MM-DD HH:mm:ss')}
                    {job.username && (
                      <div className='text-muted-foreground'>
                        {job.username}
                      </div>
                    )}
                    {job.source && (
                      <div className='text-muted-foreground'>
                        {t(EXPORT_SOURCE_LABELS[job.source])}
                      </div>
                    )}
                    <div
                      className='text-muted-foreground max-w-48 truncate'
                      title={job.job_id}
                    >
                      {job.job_id}
                    </div>
                  </TableCell>
                  <TableCell className='text-xs whitespace-nowrap'>
                    {formatRange(job)}
                  </TableCell>
                  <TableCell className='max-w-[220px] truncate text-xs'>
                    {filterSummary(
                      job,
                      t(
                        LOG_TYPE_FILTERS.find(
                          (item) =>
                            Number(item.value) === (job.filters.type || 0)
                        )?.label ?? 'Unknown'
                      )
                    )}
                  </TableCell>
                  <TableCell className='text-xs'>
                    {job.format === 'xlsx' ? 'xlsx' : 'csv.gz'}
                    {job.format_downgraded && (
                      <div className='text-muted-foreground'>
                        {t('Switched from Excel')}
                      </div>
                    )}
                  </TableCell>
                  <TableCell className='text-right font-mono text-xs tabular-nums'>
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
                  <TableCell>
                    <div className='flex flex-col gap-1'>
                      <Badge variant={STATUS_VARIANTS[job.status]}>
                        {t(STATUS_LABELS[job.status])}
                      </Badge>
                      {isActive && (
                        <>
                          <Progress value={job.progress} className='h-1.5' />
                          {!props.self && job.throttled_ms > 3000 && (
                            <p className='text-muted-foreground text-[11px]'>
                              {t(
                                'Yielded {{seconds}}s to keep the service responsive',
                                {
                                  seconds: Math.round(job.throttled_ms / 1000),
                                }
                              )}
                            </p>
                          )}
                        </>
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
                            props.self
                              ? ERROR_MESSAGES.internal
                              : (ERROR_MESSAGES[job.error ?? 'internal'] ??
                                  ERROR_MESSAGES.internal)
                          )}
                        </p>
                      )}
                    </div>
                  </TableCell>
                  <TableCell className='text-right'>
                    <div className='flex justify-end gap-1'>
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

      {props.self ? (
        sheetOpen && (
          <SelfExportSheet
            open={sheetOpen}
            onOpenChange={setSheetOpen}
            maxRangeSec={data?.max_range_sec}
          />
        )
      ) : (
        <NewExportSheet
          open={sheetOpen}
          onOpenChange={setSheetOpen}
          prefill={prefill}
        />
      )}
    </div>
  )
}

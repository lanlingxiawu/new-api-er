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
import { getRouteApi } from '@tanstack/react-router'
import dayjs from 'dayjs'
import { Download, Plus, RefreshCw, Trash2, X } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

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

import { deleteExportJob, downloadExportPart, getExportJobs } from './api'
import { NewExportSheet, type NewExportPrefill } from './components/new-export-sheet'
import type { ExportJob, ExportJobStatus } from './types'

/** Stable backend error codes → user-facing copy. Raw Go errors never reach the UI. */
const ERROR_MESSAGES: Record<string, string> = {
  disk_full:
    'Not enough server storage for this export. Please free up space or narrow the range.',
  too_many_parts:
    'Too much data for one export. Please narrow the time range and try again.',
  timeout: 'The export took too long and was stopped. Try a smaller time range.',
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
  ready: 'Ready',
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

function filterSummary(job: ExportJob, allLabel: string): string {
  const parts: string[] = []
  if (job.filters.model_name) parts.push(job.filters.model_name)
  if (job.filters.username) parts.push(job.filters.username)
  if (job.filters.token_name) parts.push(job.filters.token_name)
  if (job.filters.group) parts.push(job.filters.group)
  if (job.filters.channel) parts.push(`#${job.filters.channel}`)
  return parts.length ? parts.join(' · ') : allLabel
}

const route = getRouteApi('/_authenticated/usage-logs/$section')

/** Search params carry epoch milliseconds; tolerate ISO strings too. */
function toDate(value?: number | string): Date | undefined {
  if (!value) return undefined
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? undefined : date
}

export function LogExportCenter() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [sheetOpen, setSheetOpen] = useState(false)

  // 从使用日志页「高级导出」跳转过来时，URL 上带着当时的筛选条件，
  // 用它预填新建表单，用户不必再填一遍。
  const search = route.useSearch()
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
    }),
    [search]
  )

  const { data, isLoading, isFetching, refetch, error } = useQuery({
    queryKey: ['log-export-jobs'],
    queryFn: () => getExportJobs(),
    // Poll only while something is actually running, and never in a background
    // tab — an idle export page must not keep hitting the server.
    refetchInterval: (query) =>
      query.state.data?.jobs.some(
        (job) => job.status === 'running' || job.status === 'pending'
      )
        ? 3000
        : false,
    refetchIntervalInBackground: false,
  })

  const removeJob = useMutation({
    mutationFn: deleteExportJob,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['log-export-jobs'] })
    },
    onError: (err: Error) => toast.error(err.message || t('Operation failed')),
  })

  const download = useMutation({
    mutationFn: ({ jobId, part }: { jobId: string; part?: number }) =>
      downloadExportPart(jobId, part),
    onError: (err: Error) =>
      toast.error(err.message || t('Download link expired. Click download again.')),
  })

  const jobs = data?.jobs ?? []

  return (
    <div className='space-y-4'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div className='flex items-center gap-2'>
          <Button type='button' onClick={() => setSheetOpen(true)}>
            <Plus />
            {t('New Export')}
          </Button>
          {data && !data.enabled && (
            <Badge variant='destructive'>{t('Log export is currently turned off')}</Badge>
          )}
        </div>
        <Button
          type='button'
          variant='outline'
          size='icon'
          aria-label={t('Refresh')}
          onClick={() => void refetch()}
        >
          <RefreshCw className={isFetching ? 'animate-spin' : ''} />
        </Button>
      </div>

      {error && (
        <p className='text-destructive text-sm'>
          {(error as Error).message ||
            t('Background export is unavailable. You can still use Quick Export for smaller ranges.')}
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

            {!isLoading && jobs.length === 0 && (
              <TableRow>
                <TableCell colSpan={7} className='text-muted-foreground py-10 text-center'>
                  {t('No exports yet. Create one to get started.')}
                </TableCell>
              </TableRow>
            )}

            {jobs.map((job) => {
              const parts = job.parts ?? []
              const isActive = job.status === 'running' || job.status === 'pending'
              return (
                <TableRow key={job.job_id}>
                  <TableCell className='font-mono text-xs whitespace-nowrap'>
                    {dayjs.unix(job.created_at).format('MM-DD HH:mm:ss')}
                    {job.username && (
                      <div className='text-muted-foreground'>{job.username}</div>
                    )}
                  </TableCell>
                  <TableCell className='text-xs whitespace-nowrap'>
                    {formatRange(job)}
                  </TableCell>
                  <TableCell className='max-w-[220px] truncate text-xs'>
                    {filterSummary(job, t('All'))}
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
                    {job.bytes_out > 0 && (
                      <div className='text-muted-foreground'>
                        {formatBytes(job.bytes_out)}
                      </div>
                    )}
                  </TableCell>
                  <TableCell>
                    <div className='space-y-1'>
                      <Badge variant={STATUS_VARIANTS[job.status]}>
                        {t(STATUS_LABELS[job.status])}
                      </Badge>
                      {isActive && (
                        <>
                          <Progress value={job.progress} className='h-1.5' />
                          {job.throttled_ms > 3000 && (
                            <p className='text-muted-foreground text-[11px]'>
                              {t('Yielded {{seconds}}s to keep the service responsive', {
                                seconds: Math.round(job.throttled_ms / 1000),
                              })}
                            </p>
                          )}
                        </>
                      )}
                      {job.status === 'failed' && (
                        <p className='text-muted-foreground text-[11px]'>
                          {t(ERROR_MESSAGES[job.error ?? 'internal'] ?? ERROR_MESSAGES.internal)}
                        </p>
                      )}
                    </div>
                  </TableCell>
                  <TableCell className='text-right'>
                    <div className='flex justify-end gap-1'>
                      {job.status === 'ready' && parts.length > 0 && (
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
                            <Download />
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
                                    {t('Part {{index}}', { index: part.index })} ·{' '}
                                    {part.rows.toLocaleString()} {t('rows')}
                                  </span>
                                  <span className='text-muted-foreground text-[11px]'>
                                    {dayjs.unix(part.start_time).format('MM-DD HH:mm')}
                                    {' – '}
                                    {dayjs.unix(part.end_time).format('MM-DD HH:mm')}
                                    {' · '}
                                    {formatBytes(part.bytes)}
                                  </span>
                                </div>
                              </DropdownMenuItem>
                            ))}
                          </DropdownMenuContent>
                        </DropdownMenu>
                      )}
                      <Button
                        type='button'
                        variant='ghost'
                        size='icon'
                        aria-label={isActive ? t('Cancel') : t('Delete')}
                        disabled={removeJob.isPending}
                        onClick={() => removeJob.mutate(job.job_id)}
                      >
                        {isActive ? <X /> : <Trash2 />}
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>

      <NewExportSheet
        open={sheetOpen}
        onOpenChange={setSheetOpen}
        prefill={prefill}
      />
    </div>
  )
}

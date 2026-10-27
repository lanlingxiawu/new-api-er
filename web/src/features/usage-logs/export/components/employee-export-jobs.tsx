import {
  useInfiniteQuery,
  useMutation,
  useQueryClient,
} from '@tanstack/react-query'
import { Download, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from '@/components/ui/empty'
import { Progress } from '@/components/ui/progress'
import { Skeleton } from '@/components/ui/skeleton'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import {
  EMPLOYEE_EXPORT_BASE as BASE,
  employeeTemplateName,
  exportRequest,
} from '../employee-api'
import {
  EXPORT_SOURCE_LABELS,
  type ExportJob,
  type ExportJobListFilters,
  type ExportJobPage,
} from '../types'
import { ExportJobFilters } from './export-job-filters'

const STATUS_LABELS = {
  pending: 'Queued',
  running: 'Running',
  ready: 'Completed',
  failed: 'Failed',
  canceled: 'Canceled',
}

export function EmployeeExportJobs() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const [downloading, setDownloading] = useState<string>()
  const [filters, setFilters] = useState<ExportJobListFilters>({})
  const [deleteTarget, setDeleteTarget] = useState<ExportJob>()
  const jobs = useInfiniteQuery({
    queryKey: ['employee-export-jobs', userId, filters],
    initialPageParam: '',
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams()
      for (const [key, value] of Object.entries(filters)) {
        if (value !== undefined && value !== '') params.set(key, String(value))
      }
      if (pageParam) params.set('cursor', pageParam)
      return exportRequest<ExportJobPage>(
        'get',
        `${BASE}/jobs?${params}`,
        undefined,
        {
          quiet: true,
        }
      )
    },
    getNextPageParam: (page) => page.next_cursor || undefined,
    refetchInterval: (query) =>
      query.state.data?.pages.some((page) =>
        page.jobs.some((job) => ['pending', 'running'].includes(job.status))
      )
        ? 5000
        : false,
    refetchIntervalInBackground: false,
  })
  const remove = useMutation({
    mutationFn: (id: string) => exportRequest('delete', `${BASE}/jobs/${id}`),
    onSuccess: () => {
      setDeleteTarget(undefined)
      void client.invalidateQueries({
        queryKey: ['employee-export-jobs', userId],
      })
    },
  })
  const download = async (id: string) => {
    setDownloading(id)
    try {
      const data = await exportRequest<{ url: string }>(
        'get',
        `${BASE}/jobs/${id}/download-url`
      )
      const link = document.createElement('a')
      link.href = data.url
      link.rel = 'noopener'
      document.body.appendChild(link)
      link.click()
      link.remove()
    } catch (error) {
      handleServerError(error)
      // A customer's assignment or the employee's permissions may have changed.
      void client.invalidateQueries({
        queryKey: ['employee-export-jobs', userId],
      })
    } finally {
      setDownloading(undefined)
    }
  }
  const rows = [
    ...new Map(
      jobs.data?.pages
        .flatMap((page) => page.jobs)
        .map((job) => [job.job_id, job]) ?? []
    ).values(),
  ]
  const hasFilters = Object.values(filters).some(
    (value) => value !== undefined && value !== ''
  )
  let emptyMessage = 'No exports yet'
  if (hasFilters) {
    emptyMessage = 'No matching exports. Adjust or reset the filters.'
  }
  if (jobs.hasNextPage) {
    emptyMessage = 'More tasks remain to be checked. Continue searching.'
  }
  return (
    <div className='space-y-3'>
      <div className='flex items-center justify-between gap-2'>
        <h3 className='text-sm font-medium'>{t('Export history')}</h3>
        <Button
          type='button'
          variant='ghost'
          size='icon'
          aria-label={t('Refresh')}
          disabled={jobs.isFetching}
          onClick={() => void jobs.refetch()}
        >
          <RefreshCw className={jobs.isFetching ? 'animate-spin' : ''} />
        </Button>
      </div>
      <ExportJobFilters
        key={JSON.stringify(filters)}
        value={filters}
        onApply={setFilters}
        employee
        jobs={rows}
        hasMoreJobs={jobs.hasNextPage}
        loadingMoreJobs={jobs.isFetchingNextPage}
        onLoadMoreJobs={() => void jobs.fetchNextPage()}
      />
      {jobs.isPending && <Skeleton className='h-24 w-full' />}
      {jobs.isError && (
        <div className='space-y-2 rounded-md border p-3'>
          <p className='text-muted-foreground text-sm'>
            {t('Could not load export history. Please try again.')}
          </p>
          <Button variant='outline' onClick={() => void jobs.refetch()}>
            {t('Retry')}
          </Button>
        </div>
      )}
      {jobs.data && !jobs.isError && rows.length === 0 && (
        <Empty className='border'>
          <EmptyHeader>
            <EmptyTitle>{t(emptyMessage)}</EmptyTitle>
            {!jobs.hasNextPage && !hasFilters && (
              <EmptyDescription>
                {t(
                  'Create an export to see its progress and download it here.'
                )}
              </EmptyDescription>
            )}
          </EmptyHeader>
        </Empty>
      )}
      {rows.map((job) => (
        <div key={job.job_id} className='space-y-3 rounded-md border p-3'>
          <div className='flex flex-wrap items-start justify-between gap-2'>
            <div className='min-w-0'>
              <p className='truncate text-sm font-medium'>
                {job.employee_scope
                  ? employeeTemplateName(
                      {
                        name: job.employee_scope.template_name,
                        key: job.employee_scope.template_key,
                      },
                      t
                    )
                  : t(EXPORT_SOURCE_LABELS[job.source ?? 'self'])}
              </p>
              <p className='text-muted-foreground text-xs'>
                {new Date(job.created_at * 1000).toLocaleString()}
                {' · '}
                {job.username}
              </p>
              <p
                className='text-muted-foreground truncate text-xs'
                title={job.job_id}
              >
                {job.job_id}
              </p>
            </div>
            <Badge
              variant={job.status === 'failed' ? 'destructive' : 'secondary'}
            >
              {t(STATUS_LABELS[job.status])}
            </Badge>
          </div>
          <p className='text-muted-foreground text-xs'>
            {t('Date Range')}:{' '}
            {new Date(job.filters.start_timestamp * 1000).toLocaleString()} –{' '}
            {new Date(job.filters.end_timestamp * 1000).toLocaleString()}
          </p>
          {['pending', 'running'].includes(job.status) && (
            <Progress
              value={job.progress}
              aria-label={`${t('Progress')} ${job.progress}%`}
            />
          )}
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <p className='text-muted-foreground text-xs'>
              {job.row_count.toLocaleString()} {t('rows')} ·{' '}
              {job.format === 'xlsx' ? 'XLSX' : 'CSV.GZ'}
            </p>
            <div className='flex gap-2'>
              {job.can_download && job.status === 'ready' && (
                <Button
                  variant='outline'
                  size='sm'
                  disabled={Boolean(downloading)}
                  onClick={() => void download(job.job_id)}
                >
                  <Download />
                  {downloading === job.job_id ? t('Loading...') : t('Download')}
                </Button>
              )}
              {(job.can_cancel || job.can_delete) && (
                <Button
                  variant='outline'
                  size='sm'
                  disabled={remove.isPending}
                  onClick={() => {
                    if (job.can_cancel) remove.mutate(job.job_id)
                    else setDeleteTarget(job)
                  }}
                >
                  {['pending', 'running'].includes(job.status)
                    ? t('Cancel')
                    : t('Delete')}
                </Button>
              )}
            </div>
          </div>
          {job.status === 'failed' && (
            <p className='text-destructive text-sm'>
              {t(
                'Export failed. Check your access and try a smaller time range.'
              )}
            </p>
          )}
        </div>
      ))}
      {jobs.hasNextPage && (
        <Button
          variant='outline'
          disabled={jobs.isFetching}
          onClick={() => void jobs.fetchNextPage()}
        >
          {jobs.isFetchingNextPage ? t('Loading...') : t('Continue searching')}
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
        isLoading={remove.isPending}
        handleConfirm={() => {
          if (deleteTarget) remove.mutate(deleteTarget.job_id)
        }}
      />
    </div>
  )
}

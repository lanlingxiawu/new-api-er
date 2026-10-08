import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Download, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

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
import { useAuthStore } from '@/stores/auth-store'

import {
  EMPLOYEE_EXPORT_BASE as BASE,
  employeeTemplateName,
  exportRequest,
} from '../employee-api'
import type { ExportJob } from '../types'

const STATUS_LABELS = {
  pending: 'Queued',
  running: 'Running',
  ready: 'Ready',
  failed: 'Failed',
  canceled: 'Canceled',
}

export function EmployeeExportJobs() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const [downloading, setDownloading] = useState<string>()
  const jobs = useQuery({
    queryKey: ['employee-export-jobs', userId],
    queryFn: () =>
      exportRequest<{ jobs: ExportJob[] }>('get', `${BASE}/jobs`, undefined, {
        quiet: true,
      }),
    refetchInterval: (query) =>
      query.state.data?.jobs.some((job) =>
        ['pending', 'running'].includes(job.status)
      )
        ? 5000
        : false,
  })
  const remove = useMutation({
    mutationFn: (id: string) => exportRequest('delete', `${BASE}/jobs/${id}`),
    onSuccess: () => {
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
    } catch {
      // The HTTP client has already shown the error.
    } finally {
      setDownloading(undefined)
    }
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
      {jobs.data?.jobs.length === 0 && (
        <Empty className='border'>
          <EmptyHeader>
            <EmptyTitle>{t('No exports yet')}</EmptyTitle>
            <EmptyDescription>
              {t('Create an export to see its progress and download it here.')}
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      )}
      {jobs.data?.jobs.map((job) => (
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
                  : t('Export customer data')}
              </p>
              <p className='text-muted-foreground text-xs'>
                {new Date(job.created_at * 1000).toLocaleString()}
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
              {job.row_count.toLocaleString()} {t('Rows')} ·{' '}
              {job.format === 'xlsx' ? 'Excel' : 'CSV'}
            </p>
            <div className='flex gap-2'>
              {job.status === 'ready' && (
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
              <Button
                variant='outline'
                size='sm'
                disabled={remove.isPending}
                onClick={() => remove.mutate(job.job_id)}
              >
                {['pending', 'running'].includes(job.status)
                  ? t('Cancel')
                  : t('Delete')}
              </Button>
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
    </div>
  )
}

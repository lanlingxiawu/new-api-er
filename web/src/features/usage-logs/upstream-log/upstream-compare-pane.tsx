import { useQuery } from '@tanstack/react-query'
import { ArrowRight, RotateCcw } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'

import { queryUpstreamLog } from '../api'
import { UpstreamLogDetailBody } from './upstream-log-detail-body'

/**
 * Fetches the upstream counterpart of a local log by its local request ID and
 * renders it in the same visual language as the local details, so the two can
 * be read side by side without leaving the dialog.
 */
export function UpstreamComparePane({
  localRequestId,
}: {
  localRequestId: string
}) {
  const { t } = useTranslation()

  const query = useQuery({
    queryKey: ['upstream-log-compare', localRequestId],
    queryFn: async ({ signal }) => {
      const response = await queryUpstreamLog(
        { local_request_id: localRequestId, page: 1, page_size: 5 },
        signal
      )
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Request failed'))
      }
      return response.data
    },
    enabled: localRequestId.length > 0,
    retry: false,
    staleTime: 30_000,
  })

  if (query.isPending) {
    return (
      <div className='space-y-3'>
        <Skeleton className='h-5 w-32' />
        <Skeleton className='h-40 w-full rounded-lg' />
        <Skeleton className='h-28 w-full rounded-lg' />
      </div>
    )
  }

  if (query.isError) {
    return (
      <div className='space-y-3'>
        <p className='text-muted-foreground text-sm'>
          {query.error instanceof Error
            ? query.error.message
            : t('Request failed')}
        </p>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={() => void query.refetch()}
        >
          <RotateCcw />
          {t('Retry')}
        </Button>
      </div>
    )
  }

  const item = query.data.items[0]
  if (!item) {
    return (
      <p className='text-muted-foreground text-sm'>
        {t(
          'The upstream instance has no log for this request. It may have been trimmed by the upstream retention policy.'
        )}
      </p>
    )
  }

  return (
    <div className='space-y-3'>
      <div className='flex flex-wrap items-center gap-2 text-xs'>
        <span className='text-muted-foreground'>
          {query.data.channel.name} (#{query.data.channel.id})
        </span>
        {query.data.source && (
          <span className='text-muted-foreground flex min-w-0 items-center gap-1.5'>
            <span
              className='max-w-32 truncate font-mono'
              title={query.data.source.request_id}
            >
              {query.data.source.request_id}
            </span>
            <ArrowRight className='size-3.5 shrink-0' />
            <span
              className='max-w-32 truncate font-mono'
              title={query.data.source.upstream_request_id}
            >
              {query.data.source.upstream_request_id}
            </span>
          </span>
        )}
        {query.data.source?.key_index_from_log === false && (
          <StatusBadge
            label={t('Channel key not recorded in the local log')}
            variant='amber'
            size='sm'
          />
        )}
      </div>

      <UpstreamLogDetailBody item={item} />

      {query.data.total > 1 && (
        <p className='text-muted-foreground text-xs'>
          {t(
            'The upstream returned {{count}} matching logs; the first one is shown.',
            { count: query.data.total }
          )}
        </p>
      )}
    </div>
  )
}

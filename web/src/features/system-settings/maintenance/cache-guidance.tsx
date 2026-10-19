import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

type Recommendation = {
  value_mib: number | null
  reason: string
  below_usage: boolean
}

export type CacheGuidanceData = {
  resources: { sampled_at: number; memory_scope: string; cache_path: string }
  memory: Recommendation
  disk: Recommendation
  threshold: Recommendation
}

type Props = {
  guidance?: CacheGuidanceData
  receivedAt: number
  loading: boolean
  failed: boolean
  canEdit: boolean
  pathChanged: boolean
  onRefresh: () => void
  onApply: (kind: 'memory' | 'disk' | 'threshold', value: number) => void
}

export function CacheGuidance(props: Props) {
  const { t } = useTranslation()
  const guidance = props.guidance
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [])
  // Use the local request start to include network time without clock skew.
  const stale = !props.receivedAt || now - props.receivedAt >= 30000
  const disabled = stale || props.loading || props.failed || !props.canEdit
  const labels = {
    memory: t('Body memory budget (MiB)'),
    disk: t('Disk cache budget (MiB)'),
    threshold: t('Disk cache threshold (MiB)'),
  }
  return (
    <div className='space-y-3 rounded-lg border p-4 text-xs'>
      <div className='flex items-center justify-between gap-2'>
        <span className='font-medium'>
          {t('Cache configuration suggestions')}
        </span>
        <Button
          type='button'
          variant='outline'
          size='sm'
          disabled={props.loading}
          onClick={props.onRefresh}
        >
          {t('Refresh Stats')}
        </Button>
      </div>
      <p className='text-muted-foreground'>
        {t(
          'Suggestions only fill the form. Review and save to apply; switches stay unchanged.'
        )}
      </p>
      {props.guidance && (
        <p className='text-muted-foreground'>
          {t('Sample time')}:{' '}
          {new Date(props.guidance.resources.sampled_at).toLocaleTimeString()}
        </p>
      )}
      {(stale || props.failed) && (
        <p role='status'>
          {t('Refresh statistics to get current suggestions.')}
        </p>
      )}
      {!guidance && !props.loading && <p>{t('No recommendation')}</p>}
      {props.pathChanged && (
        <p role='status'>
          {t(
            'Save the cache directory and refresh before using disk suggestions.'
          )}
        </p>
      )}
      <div className='grid grid-cols-1 gap-3 md:grid-cols-3'>
        {guidance &&
          (['memory', 'disk', 'threshold'] as const).map((kind) => {
            const recommendation = guidance[kind]
            const value = recommendation.value_mib
            let reason = t('No adjustment suggested for this sample.')
            if (recommendation.reason === 'scope_unknown') {
              reason = t('Memory capacity scope is unknown or limited.')
            }
            if (recommendation.reason === 'low_headroom') {
              reason = t('Insufficient resource headroom.')
            }
            return (
              <div
                key={kind}
                className='flex min-w-0 flex-col items-start gap-3 border-t pt-3'
              >
                <div className='space-y-1'>
                  <p className='text-muted-foreground'>{labels[kind]}</p>
                  <p className='text-sm font-medium tabular-nums'>
                    {value === null ? t('No recommendation') : `${value} MiB`}
                  </p>
                </div>
                {value === null && (
                  <span className='text-muted-foreground'>{reason}</span>
                )}
                {value !== null && (
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    disabled={
                      disabled || (kind !== 'memory' && props.pathChanged)
                    }
                    onClick={() => {
                      if (Date.now() - props.receivedAt < 30000 && !disabled) {
                        props.onApply(kind, value)
                      }
                    }}
                  >
                    {t('Fill suggestion')}
                  </Button>
                )}
                {recommendation.below_usage && (
                  <p className='text-muted-foreground w-full'>
                    {t(
                      'Below current reservations: new requests may be rejected until existing requests finish.'
                    )}
                  </p>
                )}
              </div>
            )
          })}
      </div>
      <details className='text-muted-foreground border-t pt-3'>
        <summary className='cursor-pointer'>
          {t('How suggestions are calculated')}
        </summary>
        <p className='mt-2'>
          {t(
            'Starting values, not capacity guarantees. Memory: min(10% total, reserved + 25% available), rounded down to 16 MiB. Unknown or limited memory scope gets no numeric suggestion.'
          )}
        </p>
        <p className='mt-2'>
          {t(
            'Disk: reserve max(2 GiB, 10% total) free space, then use min(10% total, reserved + 25% remaining free), rounded down to 64 MiB. Insufficient space gets no suggestion.'
          )}
        </p>
        <p className='mt-2'>
          {t(
            'At 80% memory budget usage, a 1 MiB disk threshold may be suggested. Check disk latency under load first; lower thresholds increase disk I/O. Suggestions expire after 30 seconds.'
          )}
        </p>
      </details>
    </div>
  )
}

import { AlertTriangle, CheckCircle2, Loader2, XCircle } from 'lucide-react'
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import dayjs from '@/lib/dayjs'

import {
  getFallbackBackfillResult,
  triggerFallbackBackfill,
  type FallbackBackfillResult,
  type FallbackHint,
} from './api'

const POLL_INTERVAL_MS = 3000

function formatFileSize(bytes?: number): string {
  if (!bytes) return '-'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

function formatUnixTime(unix?: number): string {
  if (!unix) return '-'
  return dayjs.unix(unix).format('YYYY-MM-DD HH:mm:ss')
}

interface FallbackBackfillBannerProps {
  hint: FallbackHint | null | undefined
  onBackfillComplete?: () => void
}

export function FallbackBackfillBanner({
  hint,
  onBackfillComplete,
}: FallbackBackfillBannerProps) {
  const { t } = useTranslation()
  const [triggering, setTriggering] = useState(false)
  const [result, setResult] = useState<FallbackBackfillResult | null>(null)
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const onCompleteRef = useRef(onBackfillComplete)

  useEffect(() => {
    onCompleteRef.current = onBackfillComplete
  })

  const stopPolling = useCallback(() => {
    if (pollRef.current !== null) {
      clearInterval(pollRef.current)
      pollRef.current = null
    }
  }, [])

  const startPolling = useCallback(() => {
    stopPolling()
    pollRef.current = setInterval(async () => {
      try {
        const res = await getFallbackBackfillResult()
        if (!res.success || !res.data) return
        setResult(res.data)
        if (!res.data.running) {
          stopPolling()
          if (res.data.success) {
            onCompleteRef.current?.()
          }
        }
      } catch {
        // Network errors during polling are intentionally silent.
      }
    }, POLL_INTERVAL_MS)
  }, [stopPolling])

  useEffect(() => stopPolling, [stopPolling])

  useEffect(() => {
    getFallbackBackfillResult()
      .then((res) => {
        if (res.success && res.data?.running) {
          setResult(res.data)
          startPolling()
        }
      })
      .catch(() => {})
  }, [startPolling])

  const handleTrigger = useCallback(async () => {
    if (!hint?.date) return
    setTriggering(true)
    try {
      const res = await triggerFallbackBackfill(hint.date)
      if (!res.success) {
        toast.error(res.message ?? t('Request failed'))
        return
      }
      setResult({ running: true, date: hint.date })
      startPolling()
    } catch (err: unknown) {
      const msg =
        (err as { response?: { data?: { message?: string } } })?.response?.data
          ?.message ?? t('Request failed')
      toast.error(msg)
    } finally {
      setTriggering(false)
    }
  }, [hint, startPolling, t])

  if (!hint?.has_file) return null

  const isRunning = result?.running === true
  const isDone = result && !result.running
  const isSuccess = isDone && result.success
  const isFailed = isDone && !result.success

  let toneClassName: string
  if (isSuccess) {
    toneClassName =
      'border-emerald-200 bg-emerald-50 text-emerald-600 dark:border-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-400'
  } else if (isFailed) {
    toneClassName =
      'border-red-200 bg-red-50 text-red-600 dark:border-red-800 dark:bg-red-950/40 dark:text-red-400'
  } else {
    toneClassName =
      'border-amber-200 bg-amber-50 text-amber-600 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-400'
  }

  let statusIcon: ReactNode
  if (isRunning) {
    statusIcon = <Loader2 className='h-4 w-4 animate-spin' />
  } else if (isSuccess) {
    statusIcon = <CheckCircle2 className='h-4 w-4' />
  } else if (isFailed) {
    statusIcon = <XCircle className='h-4 w-4' />
  } else {
    statusIcon = <AlertTriangle className='h-4 w-4' />
  }

  let statusContent: ReactNode
  if (isSuccess) {
    statusContent = (
      <>
        <span className='font-medium'>
          {t('Backfill completed for {{date}}', { date: result?.date })}
        </span>
        <span className='text-xs opacity-80'>
          {t('{{rows}} records written', { rows: result?.success_count ?? 0 })}
        </span>
      </>
    )
  } else if (isFailed) {
    statusContent = (
      <>
        <span className='font-medium'>
          {t('Backfill failed for {{date}}', { date: result?.date })}
        </span>
        {result?.last_error ? (
          <span className='text-xs opacity-80'>{result.last_error}</span>
        ) : null}
      </>
    )
  } else if (isRunning) {
    statusContent = (
      <span className='font-medium'>
        {t('Backfill running for {{date}}...', {
          date: result?.date ?? hint.date,
        })}
      </span>
    )
  } else {
    statusContent = (
      <>
        <span className='font-medium'>
          {t('Fallback log detected for {{date}}', { date: hint.date })}
        </span>
        <span className='text-xs opacity-80'>
          {t('File: {{name}} ({{size}}, updated {{time}})', {
            name: hint.file_name ?? '-',
            size: formatFileSize(hint.file_size),
            time: formatUnixTime(hint.updated_at),
          })}
        </span>
        <span className='text-xs opacity-80'>
          {t(
            'These records were buffered during a circuit-breaker event. Trigger backfill to write them into the ledger.'
          )}
        </span>
      </>
    )
  }

  return (
    <div
      className={[
        'flex items-start gap-3 rounded-lg border px-4 py-3 text-sm',
        toneClassName,
      ].join(' ')}
    >
      <span className='mt-0.5 shrink-0'>{statusIcon}</span>

      <div className='flex min-w-0 flex-1 flex-col gap-0.5'>
        {statusContent}
      </div>

      {!isRunning && !isSuccess ? (
        <Button
          size='sm'
          variant={isFailed ? 'destructive' : 'default'}
          className='shrink-0 self-start'
          disabled={triggering}
          onClick={handleTrigger}
        >
          {triggering ? (
            <Loader2 data-icon='inline-start' className='animate-spin' />
          ) : null}
          {isFailed ? t('Retry backfill') : t('Trigger backfill')}
        </Button>
      ) : null}
    </div>
  )
}

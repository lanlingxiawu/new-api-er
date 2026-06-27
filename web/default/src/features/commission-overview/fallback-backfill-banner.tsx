import { useCallback, useEffect, useRef, useState } from 'react'
import { AlertTriangle, CheckCircle2, Loader2, XCircle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import dayjs from '@/lib/dayjs'
import { Button } from '@/components/ui/button'
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

  return (
    <div
      className={[
        'flex items-start gap-3 rounded-lg border px-4 py-3 text-sm',
        isSuccess
          ? 'border-green-300 bg-green-50 text-green-800 dark:border-green-700 dark:bg-green-950/40 dark:text-green-300'
          : isFailed
            ? 'border-red-300 bg-red-50 text-red-800 dark:border-red-700 dark:bg-red-950/40 dark:text-red-300'
            : 'border-amber-300 bg-amber-50 text-amber-800 dark:border-amber-700 dark:bg-amber-950/40 dark:text-amber-200',
      ].join(' ')}
    >
      <span className='mt-0.5 shrink-0'>
        {isRunning ? (
          <Loader2 className='h-4 w-4 animate-spin' />
        ) : isSuccess ? (
          <CheckCircle2 className='h-4 w-4' />
        ) : isFailed ? (
          <XCircle className='h-4 w-4' />
        ) : (
          <AlertTriangle className='h-4 w-4' />
        )}
      </span>

      <div className='flex min-w-0 flex-1 flex-col gap-0.5'>
        {isSuccess ? (
          <>
            <span className='font-medium'>
              {t('Backfill completed for {{date}}', { date: result?.date })}
            </span>
            <span className='text-xs opacity-80'>
              {t('{{rows}} records written', { rows: result?.success_count ?? 0 })}
            </span>
          </>
        ) : isFailed ? (
          <>
            <span className='font-medium'>
              {t('Backfill failed for {{date}}', { date: result?.date })}
            </span>
            {result?.last_error ? (
              <span className='text-xs opacity-80'>{result.last_error}</span>
            ) : null}
          </>
        ) : isRunning ? (
          <span className='font-medium'>
            {t('Backfill running for {{date}}...', {
              date: result?.date ?? hint.date,
            })}
          </span>
        ) : (
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
        )}
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

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
import { useEffect, useEffectEvent, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { DateTimePicker } from '@/components/datetime-picker'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import { formatTimestampToDate } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { createServerError } from '@/lib/server-error-message'

import {
  getCurrentLogCleanupTask,
  getSystemTask,
  startLogCleanupTask,
} from '../api'
import type { LogCleanupTask } from '../types'

const HOURS_IN_DAY = 24

const getDateHoursAgo = (hours: number) => {
  const date = new Date()
  date.setHours(date.getHours() - hours)
  return date
}

const getDateDaysAgo = (days: number) => getDateHoursAgo(days * HOURS_IN_DAY)

const quickSelectOptions = [
  {
    label: '24 hours ago',
    getValue: () => getDateHoursAgo(24),
  },
  {
    label: '7 days ago',
    getValue: () => getDateDaysAgo(7),
  },
  {
    label: '30 days ago',
    getValue: () => getDateDaysAgo(30),
  },
]

function isActiveLogCleanupTask(task: LogCleanupTask | null) {
  return task?.status === 'pending' || task?.status === 'running'
}

/**
 * The history log module of the log maintenance page. It deletes the logs
 * created before a chosen time as a background task and shows its progress.
 */
export function HistoryLogCleanup({
  scope = 'usage',
  onComplete,
}: {
  scope?: 'usage' | 'audit'
  onComplete?: () => void
}) {
  const { t } = useTranslation()
  const titleId = useId()
  const [purgeDate, setPurgeDate] = useState<Date | undefined>(() =>
    getDateDaysAgo(30)
  )
  const [isStarting, setIsStarting] = useState(false)
  const [task, setTask] = useState<LogCleanupTask | null>(null)
  const [showConfirm, setShowConfirm] = useState(false)
  const [loadingCurrent, setLoadingCurrent] = useState(true)
  const [progressError, setProgressError] = useState(false)
  const [retry, setRetry] = useState(0)
  const notifyComplete = useEffectEvent(() => onComplete?.())
  const audit = scope === 'audit'
  const actionLabel = audit ? t('Clean audit logs') : t('Clean logs')

  useEffect(() => {
    let cancelled = false
    async function fetchCurrent() {
      setLoadingCurrent(true)
      try {
        const res = await getCurrentLogCleanupTask()
        if (cancelled) return
        if (!res.success) throw new Error('cleanup progress unavailable')
        setProgressError(false)
        if (res.data) {
          setTask(res.data)
          if ((res.data.payload?.scope ?? 'usage') === scope) {
            const timestamp =
              scope === 'audit'
                ? res.data.payload?.audit_target_timestamp
                : res.data.payload?.target_timestamp
            if (timestamp) setPurgeDate(new Date(timestamp * 1000))
          }
        } else if (retry > 0) {
          // A completed task may have been removed from history while polling
          // failed. An explicit retry must release the stale busy state.
          setTask((previous) =>
            isActiveLogCleanupTask(previous) ? null : previous
          )
        }
      } catch {
        if (!cancelled) setProgressError(true)
      } finally {
        if (!cancelled) setLoadingCurrent(false)
      }
    }
    void fetchCurrent()
    return () => {
      cancelled = true
    }
  }, [retry, scope])

  const active = isActiveLogCleanupTask(task)
  const taskId = task?.task_id
  const taskIsAudit = task?.payload?.scope === 'audit'
  const taskMatchesScope = (task?.payload?.scope ?? 'usage') === scope
  const taskTimestamp = taskIsAudit
    ? task?.payload?.audit_target_timestamp
    : task?.payload?.target_timestamp
  const otherScopeActive = active && !taskMatchesScope
  const progress = Math.min(100, Math.max(0, task?.state?.progress ?? 0))
  const purgeTimestamp = purgeDate ? Math.floor(purgeDate.getTime() / 1000) : 0
  const invalidDate =
    !Number.isFinite(purgeTimestamp) ||
    purgeTimestamp <= 0 ||
    purgeTimestamp > Date.now() / 1000
  const formattedDate = invalidDate ? '' : formatTimestampToDate(purgeTimestamp)

  useEffect(() => {
    if (!taskId || !active) return
    let cancelled = false
    let inFlight = false
    const interval = window.setInterval(async () => {
      if (inFlight) return
      inFlight = true
      try {
        const res = await getSystemTask(taskId)
        if (cancelled) return
        if (!res.success || !res.data) {
          throw new Error('cleanup progress unavailable')
        }
        setProgressError(false)
        setTask(res.data)
        if (!isActiveLogCleanupTask(res.data)) {
          window.clearInterval(interval)
          notifyComplete()
          if ((res.data.payload?.scope ?? 'usage') !== scope) return
          if (res.data.status === 'succeeded') {
            const count =
              res.data.result?.deleted_count ?? res.data.state?.processed ?? 0
            let message = t('No log entries matched the selected time.')
            if (count > 0) {
              message =
                scope === 'audit'
                  ? t('{{count}} audit log entries removed.', { count })
                  : t('{{count}} log entries removed.', { count })
            }
            toast.success(message)
          } else if (res.data.status === 'failed') {
            toast.error(t('Failed to clean logs'))
          }
        }
      } catch {
        if (!cancelled) setProgressError(true)
      } finally {
        inFlight = false
      }
    }, 1000)
    return () => {
      cancelled = true
      window.clearInterval(interval)
    }
  }, [active, taskId, scope, t])

  const handleCleanLogs = async () => {
    if (isStarting || active) return
    if (invalidDate) {
      toast.error(t('Choose a valid time that is not in the future.'))
      return
    }
    setIsStarting(true)
    try {
      const res = await startLogCleanupTask(purgeTimestamp, scope)
      if (!res.success || !res.data) {
        throw createServerError(res, t('Failed to clean logs'))
      }
      setTask(res.data)
      setShowConfirm(false)
      toast.success(
        audit
          ? t('Audit log cleanup task started.')
          : t('Log cleanup task started.')
      )
    } catch (error) {
      handleServerError(error, t('Failed to clean logs'))
    } finally {
      setIsStarting(false)
    }
  }

  return (
    <div
      role='group'
      aria-labelledby={titleId}
      className='flex min-w-0 flex-col gap-4'
    >
      <div>
        <h4 id={titleId} className='font-medium'>
          {audit ? t('Clean audit logs') : t('Clean history logs')}
        </h4>
      </div>
      <fieldset disabled={active || isStarting}>
        <DateTimePicker value={purgeDate} onChange={setPurgeDate} />
      </fieldset>
      {invalidDate && (
        <p role='alert' className='text-destructive text-sm'>
          {t('Choose a valid time that is not in the future.')}
        </p>
      )}
      {loadingCurrent && (
        <p role='status' className='text-muted-foreground text-sm'>
          {t('Loading...')}
        </p>
      )}
      <div className='flex flex-wrap gap-3'>
        {quickSelectOptions.map((option) => (
          <Button
            key={option.label}
            type='button'
            variant='outline'
            disabled={active || isStarting}
            onClick={() => setPurgeDate(option.getValue())}
          >
            {t(option.label)}
          </Button>
        ))}
        <Button
          type='button'
          variant='destructive'
          onClick={() => setShowConfirm(true)}
          disabled={
            loadingCurrent ||
            progressError ||
            isStarting ||
            active ||
            invalidDate
          }
        >
          {isStarting || (active && taskMatchesScope)
            ? t('Cleaning...')
            : actionLabel}
        </Button>
      </div>
      {otherScopeActive && (
        <p role='status' className='text-muted-foreground text-sm'>
          {t('Another log cleanup is running. Wait for it to finish.')}
        </p>
      )}
      {progressError && (
        <div
          role='alert'
          className='flex items-center justify-between gap-2 text-sm'
        >
          <span>{t('Failed to load cleanup progress. Please retry.')}</span>
          <Button
            variant='outline'
            size='sm'
            onClick={() => setRetry((value) => value + 1)}
          >
            {t('Retry')}
          </Button>
        </div>
      )}
      {task && taskMatchesScope && (
        <div className='rounded-md border p-3'>
          <div className='mb-2 flex items-center justify-between gap-3 text-sm'>
            <span className='font-medium'>
              {taskIsAudit
                ? t('Audit log cleanup progress')
                : t('Log cleanup progress')}
            </span>
            <span className='text-muted-foreground tabular-nums'>
              {progress}%
            </span>
          </div>
          <Progress value={progress} />
          {!!taskTimestamp && (
            <p className='text-muted-foreground mt-2 text-xs'>
              {t('Before {{date}}', {
                date: formatTimestampToDate(taskTimestamp),
              })}
            </p>
          )}
          <div className='text-muted-foreground mt-2 text-xs'>
            {t('{{processed}} of {{total}} log entries processed.', {
              processed: task.state?.processed ?? 0,
              total: task.state?.total ?? 0,
            })}
          </div>
          {task.status === 'failed' && (
            <div className='text-destructive mt-2 text-xs'>
              {t('Failed to clean logs')}
            </div>
          )}
        </div>
      )}
      <ConfirmDialog
        open={showConfirm}
        onOpenChange={setShowConfirm}
        title={audit ? t('Clean audit logs') : t('Confirm log cleanup')}
        desc={
          <div className='space-y-2'>
            <p>
              {audit
                ? t(
                    'This will permanently remove all audit logs before {{date}}, including records outside the current filters. Usage logs and security alerts are retained.',
                    { date: formattedDate }
                  )
                : t(
                    'This will permanently remove all log entries created before {{date}}.',
                    { date: formattedDate }
                  )}
            </p>
            <p>{t('This action cannot be undone.')}</p>
          </div>
        }
        confirmText={isStarting ? t('Cleaning...') : t('Delete logs')}
        handleConfirm={handleCleanLogs}
        isLoading={isStarting}
        disabled={invalidDate || active}
        destructive
      />
    </div>
  )
}

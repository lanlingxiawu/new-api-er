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
import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Loader2, Play, RefreshCcw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { formatTimestampToDate } from '@/lib/format'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { SettingsSection } from '../components/settings-section'

// ─── API ─────────────────────────────────────────────────────────────────────

type SchedulerConfig = {
  id: number
  task_name: string
  enabled: number
  interval_seconds: number
  timeout_seconds: number
  requires_master: number
  last_run_time: number
  last_run_status: string
  last_error: string
  next_run_time: number
}

async function getSchedulerStatus(): Promise<SchedulerConfig[]> {
  const res = await api.get('/api/admin/monitor/scheduler/status')
  return res.data?.data ?? []
}

async function updateSchedulerConfig(
  taskName: string,
  fields: { enabled?: number; interval_seconds?: number }
): Promise<void> {
  await api.put(`/api/admin/monitor/scheduler/config/${taskName}`, fields)
}

async function triggerTask(taskName: string): Promise<void> {
  await api.post(`/api/admin/monitor/scheduler/trigger/${taskName}`)
}

// ─── Task descriptions ────────────────────────────────────────────────────────

const TASK_DESCRIPTIONS: Record<string, string> = {
  GroupModelAvailabilityTest:
    '每30分钟测试各分组内的模型可通性，标记不可用的模型',
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

function formatInterval(seconds: number): string {
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`
  return `${Math.round(seconds / 3600)}h`
}

function StatusBadge({ status }: { status: string }) {
  const { t } = useTranslation()
  if (!status) return <span className='text-muted-foreground text-xs'>—</span>
  const variant =
    status === 'success'
      ? 'default'
      : status === 'failed'
        ? 'destructive'
        : 'secondary'
  return (
    <Badge variant={variant} className='text-xs'>
      {t(status)}
    </Badge>
  )
}

// ─── Row ─────────────────────────────────────────────────────────────────────

function TaskRow({ task }: { task: SchedulerConfig }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [editing, setEditing] = useState(false)
  const [intervalInput, setIntervalInput] = useState(
    String(task.interval_seconds)
  )
  const [running, setRunning] = useState(false)

  const updateMut = useMutation({
    mutationFn: (fields: { enabled?: number; interval_seconds?: number }) =>
      updateSchedulerConfig(task.task_name, fields),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['scheduler-status'] })
      toast.success(t('Saved'))
      setEditing(false)
    },
    onError: () => toast.error(t('Save failed')),
  })

  const handleToggle = (checked: boolean) => {
    updateMut.mutate({ enabled: checked ? 1 : 0 })
  }

  const handleSaveInterval = () => {
    const val = parseInt(intervalInput, 10)
    if (isNaN(val) || val < 10) {
      toast.error(t('Interval must be at least 10 seconds'))
      return
    }
    updateMut.mutate({ interval_seconds: val })
  }

  const handleTrigger = async () => {
    setRunning(true)
    try {
      await triggerTask(task.task_name)
      toast.success(t('Task triggered'))
      setTimeout(() => {
        qc.invalidateQueries({ queryKey: ['scheduler-status'] })
      }, 1500)
    } catch {
      toast.error(t('Failed to trigger task'))
    } finally {
      setRunning(false)
    }
  }

  return (
    <div className='flex flex-col gap-3 rounded-lg border p-4 sm:flex-row sm:items-center sm:gap-4'>
      {/* Enable toggle */}
      <Switch
        checked={task.enabled === 1}
        onCheckedChange={handleToggle}
        disabled={updateMut.isPending}
        className='shrink-0'
      />

      {/* Task info */}
      <div className='min-w-0 flex-1 space-y-1'>
        <div className='flex flex-wrap items-center gap-2'>
          <span className='font-mono text-sm font-medium'>{task.task_name}</span>
          {task.requires_master === 1 && (
            <Badge variant='outline' className='text-xs'>
              {t('Master only')}
            </Badge>
          )}
          <StatusBadge status={task.last_run_status} />
        </div>

        {TASK_DESCRIPTIONS[task.task_name] && (
          <p className='text-muted-foreground text-xs'>
            {TASK_DESCRIPTIONS[task.task_name]}
          </p>
        )}

        <div className='text-muted-foreground flex flex-wrap gap-x-4 gap-y-0.5 text-xs'>
          <span>
            {t('Last run')}:{' '}
            {task.last_run_time
              ? formatTimestampToDate(task.last_run_time)
              : '—'}
          </span>
          <span>
            {t('Next run')}:{' '}
            {task.next_run_time
              ? formatTimestampToDate(task.next_run_time)
              : '—'}
          </span>
          {task.last_error && (
            <span className='text-destructive truncate max-w-xs' title={task.last_error}>
              {t('Error')}: {task.last_error}
            </span>
          )}
        </div>
      </div>

      {/* Interval editor */}
      <div className='flex shrink-0 items-center gap-2'>
        {editing ? (
          <>
            <Input
              className='h-8 w-24 text-sm'
              value={intervalInput}
              onChange={(e) => setIntervalInput(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && handleSaveInterval()}
              placeholder='seconds'
            />
            <Button
              size='sm'
              onClick={handleSaveInterval}
              disabled={updateMut.isPending}
            >
              {updateMut.isPending ? (
                <Loader2 className='size-3.5 animate-spin' />
              ) : (
                t('Save')
              )}
            </Button>
            <Button
              size='sm'
              variant='ghost'
              onClick={() => {
                setEditing(false)
                setIntervalInput(String(task.interval_seconds))
              }}
            >
              {t('Cancel')}
            </Button>
          </>
        ) : (
          <Button
            size='sm'
            variant='outline'
            onClick={() => setEditing(true)}
            className='min-w-[80px]'
          >
            {formatInterval(task.interval_seconds)}
          </Button>
        )}

        {/* Run now */}
        <Button
          size='sm'
          variant='ghost'
          onClick={handleTrigger}
          disabled={running}
          title={t('Run now')}
        >
          {running ? (
            <Loader2 className='size-4 animate-spin' />
          ) : (
            <Play className='size-4' />
          )}
        </Button>
      </div>
    </div>
  )
}

// ─── Section ─────────────────────────────────────────────────────────────────

export function SchedulerSection() {
  const { t } = useTranslation()
  const qc = useQueryClient()

  const { data: tasks = [], isLoading, isError } = useQuery({
    queryKey: ['scheduler-status'],
    queryFn: getSchedulerStatus,
    refetchInterval: 30_000,
  })

  return (
    <SettingsSection title={t('Scheduled Tasks')}>
      <div className='flex items-center justify-between'>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Configure and monitor background tasks. Click the interval button to edit. Changes take effect within 1 minute without restart.'
          )}
        </p>
        <Button
          size='sm'
          variant='ghost'
          onClick={() => qc.invalidateQueries({ queryKey: ['scheduler-status'] })}
        >
          <RefreshCcw className='size-4' />
        </Button>
      </div>

      {isLoading && (
        <div className='flex items-center gap-2 py-6 text-sm'>
          <Loader2 className='size-4 animate-spin' />
          {t('Loading...')}
        </div>
      )}

      {isError && (
        <p className='text-destructive text-sm py-4'>
          {t('Failed to load scheduler status.')}
        </p>
      )}

      {!isLoading && !isError && tasks.length === 0 && (
        <p className='text-muted-foreground py-6 text-center text-sm'>
          {t('No scheduled tasks registered.')}
        </p>
      )}

      <div className='space-y-3'>
        {tasks.map((task) => (
          <TaskRow key={task.task_name} task={task} />
        ))}
      </div>
    </SettingsSection>
  )
}

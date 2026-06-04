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
import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ChevronDown, ChevronRight, Loader2, Play, RefreshCcw } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { formatTimestampToDate } from '@/lib/format'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { SettingsSection } from '../components/settings-section'
import { updateSystemOption } from '../api'
import { useSystemOptions, getOptionValue } from '../hooks/use-system-options'

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

// ─── Model health check config defaults ──────────────────────────────────────

const MODEL_HEALTH_CHECK_DEFAULTS = {
  'monitor_setting.model_health_check_enabled': true,
  'monitor_setting.model_health_check_workers': 3,
  'monitor_setting.model_health_check_interval_ms': 200,
}

// Tasks that have extra config params beyond interval/enabled.
const TASKS_WITH_EXTRA_CONFIG = new Set(['GroupModelAvailabilityTest'])

// ─── Extra config panel ───────────────────────────────────────────────────────

function ModelHealthCheckConfig() {
  const qc = useQueryClient()
  const { data: optionsData } = useSystemOptions()

  const opts = getOptionValue(
    optionsData?.data ?? [],
    MODEL_HEALTH_CHECK_DEFAULTS,
  )

  const [workers, setWorkers] = useState('')
  const [intervalMs, setIntervalMs] = useState('')
  const [savingNums, setSavingNums] = useState(false)

  // 数据加载完成后（或变化后）同步到输入框，避免初次渲染时 optionsData 还未就绪
  useEffect(() => {
    if (!optionsData) return
    setWorkers(String(opts['monitor_setting.model_health_check_workers']))
    setIntervalMs(String(opts['monitor_setting.model_health_check_interval_ms']))
  }, [optionsData])

  const saveOption = async (key: string, value: string | boolean | number) => {
    await updateSystemOption({ key, value })
    qc.invalidateQueries({ queryKey: ['system-options'] })
  }

  const handleToggle = async (checked: boolean) => {
    try {
      await saveOption('monitor_setting.model_health_check_enabled', checked)
      toast.success('已保存')
    } catch {
      toast.error('保存失败')
    }
  }

  const handleSaveNums = async () => {
    const w = parseInt(workers, 10)
    const ms = parseInt(intervalMs, 10)
    if (isNaN(w) || w < 1 || w > 20) {
      toast.error('并发数须在 1 到 20 之间')
      return
    }
    if (isNaN(ms) || ms < 0 || ms > 5000) {
      toast.error('探测间隔须在 0 到 5000 毫秒之间')
      return
    }
    setSavingNums(true)
    try {
      await saveOption('monitor_setting.model_health_check_workers', w)
      await saveOption('monitor_setting.model_health_check_interval_ms', ms)
      toast.success('已保存')
    } catch {
      toast.error('保存失败')
    } finally {
      setSavingNums(false)
    }
  }

  return (
    <div className='mt-3 rounded-md border bg-muted/30 p-3 space-y-3'>
      {/* 启用开关 */}
      <div className='flex items-center justify-between gap-4'>
        <div>
          <Label className='text-sm font-medium'>启用模型健康检测</Label>
          <p className='text-xs text-muted-foreground mt-0.5'>
            关闭后定时任务仍会触发，但跳过所有探测
          </p>
        </div>
        <Switch
          checked={opts['monitor_setting.model_health_check_enabled']}
          onCheckedChange={handleToggle}
        />
      </div>

      {/* 并发数 + 探测间隔 */}
      <div className='grid grid-cols-2 gap-3'>
        <div className='space-y-1'>
          <Label className='text-xs text-muted-foreground'>并发探测数（1–20）</Label>
          <Input
            type='number'
            min={1}
            max={20}
            className='h-8 text-sm'
            value={workers}
            onChange={(e) => setWorkers(e.target.value)}
          />
        </div>
        <div className='space-y-1'>
          <Label className='text-xs text-muted-foreground'>探测间隔（毫秒，0–5000）</Label>
          <Input
            type='number'
            min={0}
            max={5000}
            className='h-8 text-sm'
            value={intervalMs}
            onChange={(e) => setIntervalMs(e.target.value)}
          />
        </div>
      </div>

      <div className='flex justify-end'>
        <Button size='sm' onClick={handleSaveNums} disabled={savingNums}>
          {savingNums ? <Loader2 className='size-3.5 animate-spin mr-1' /> : null}
          应用
        </Button>
      </div>
    </div>
  )
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

function formatInterval(seconds: number): string {
  if (seconds < 60) return `${seconds}秒`
  if (seconds < 3600) return `${Math.round(seconds / 60)}分钟`
  return `${Math.round(seconds / 3600)}小时`
}

const STATUS_LABELS: Record<string, string> = {
  success: '成功',
  failed: '失败',
}

function StatusBadge({ status }: { status: string }) {
  if (!status) return <span className='text-muted-foreground text-xs'>—</span>
  const variant =
    status === 'success'
      ? 'default'
      : status === 'failed'
        ? 'destructive'
        : 'secondary'
  return (
    <Badge variant={variant} className='text-xs'>
      {STATUS_LABELS[status] ?? status}
    </Badge>
  )
}

// ─── Row ─────────────────────────────────────────────────────────────────────

function TaskRow({ task }: { task: SchedulerConfig }) {
  const qc = useQueryClient()
  const [editing, setEditing] = useState(false)
  const [intervalInput, setIntervalInput] = useState(
    String(task.interval_seconds)
  )
  const [running, setRunning] = useState(false)
  const [configExpanded, setConfigExpanded] = useState(false)
  const hasExtraConfig = TASKS_WITH_EXTRA_CONFIG.has(task.task_name)

  const updateMut = useMutation({
    mutationFn: (fields: { enabled?: number; interval_seconds?: number }) =>
      updateSchedulerConfig(task.task_name, fields),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['scheduler-status'] })
      toast.success('已保存')
      setEditing(false)
    },
    onError: () => toast.error('保存失败'),
  })

  const handleToggle = (checked: boolean) => {
    updateMut.mutate({ enabled: checked ? 1 : 0 })
  }

  const handleSaveInterval = () => {
    const val = parseInt(intervalInput, 10)
    if (isNaN(val) || val < 10) {
      toast.error('间隔时间不能少于 10 秒')
      return
    }
    updateMut.mutate({ interval_seconds: val })
  }

  const handleTrigger = async () => {
    setRunning(true)
    try {
      await triggerTask(task.task_name)
      toast.success('任务已触发')
      setTimeout(() => {
        qc.invalidateQueries({ queryKey: ['scheduler-status'] })
      }, 1500)
    } catch {
      toast.error('触发失败')
    } finally {
      setRunning(false)
    }
  }

  return (
    <div className='rounded-lg border p-4'>
      <div className='flex flex-col gap-3 sm:flex-row sm:items-center sm:gap-4'>
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
                仅主节点
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
              上次执行:{' '}
              {task.last_run_time
                ? formatTimestampToDate(task.last_run_time)
                : '—'}
            </span>
            <span>
              下次执行:{' '}
              {task.next_run_time
                ? formatTimestampToDate(task.next_run_time)
                : '—'}
            </span>
            {task.last_error && (
              <span className='text-destructive truncate max-w-xs' title={task.last_error}>
                错误: {task.last_error}
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
                placeholder='秒'
              />
              <Button
                size='sm'
                onClick={handleSaveInterval}
                disabled={updateMut.isPending}
              >
                {updateMut.isPending ? (
                  <Loader2 className='size-3.5 animate-spin' />
                ) : (
                  '保存'
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
                取消
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
            title='立即执行'
          >
            {running ? (
              <Loader2 className='size-4 animate-spin' />
            ) : (
              <Play className='size-4' />
            )}
          </Button>

          {/* Expand config */}
          {hasExtraConfig && (
            <Button
              size='sm'
              variant='ghost'
              onClick={() => setConfigExpanded((v) => !v)}
              title='配置参数'
            >
              {configExpanded ? (
                <ChevronDown className='size-4' />
              ) : (
                <ChevronRight className='size-4' />
              )}
            </Button>
          )}
        </div>
      </div>

      {/* Extra config panel */}
      {hasExtraConfig && configExpanded && <ModelHealthCheckConfig />}
    </div>
  )
}

// ─── Section ─────────────────────────────────────────────────────────────────

export function SchedulerSection() {
  const qc = useQueryClient()

  const { data: tasks = [], isLoading, isError } = useQuery({
    queryKey: ['scheduler-status'],
    queryFn: getSchedulerStatus,
    refetchInterval: 30_000,
  })

  return (
    <SettingsSection title='定时任务'>
      <div className='flex items-center justify-between'>
        <p className='text-muted-foreground text-sm'>
          配置和监控后台定时任务。点击间隔按钮可编辑。修改后 1 分钟内生效，无需重启。
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
          加载中...
        </div>
      )}

      {isError && (
        <p className='text-destructive text-sm py-4'>
          加载定时任务状态失败。
        </p>
      )}

      {!isLoading && !isError && tasks.length === 0 && (
        <p className='text-muted-foreground py-6 text-center text-sm'>
          暂无注册的定时任务。
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

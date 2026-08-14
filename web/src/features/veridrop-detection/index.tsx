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
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  Download,
  FlaskConical,
  ListChecks,
  Play,
  RefreshCw,
  Save,
  Settings2,
  ShieldAlert,
} from 'lucide-react'
import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { listSystemTasks } from '@/features/system-settings/api'
import type {
  SystemTask,
  SystemTaskStatus,
} from '@/features/system-settings/types'
import { toIntlLocale } from '@/i18n/languages'
import { formatTimestampRelative, formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'

import {
  getVeridropOptions,
  listVeridropDetectionTargets,
  listVeridropDetectionResults,
  startEnabledVeridropDetection,
  startManualVeridropDetection,
  updateVeridropOptions,
  VERIDROP_DEFAULT_OPTIONS,
} from './api'
import type {
  VeridropDetectionOptions,
  VeridropDetectionResult,
  VeridropDetectionStatus,
  VeridropDetectionTarget,
  VeridropManualDetectionRequest,
} from './types'

const ACTIVE_POLL_INTERVAL_MS = 8000
const RESULT_LIMIT = 100
const RESULT_STATUSES: VeridropDetectionStatus[] = [
  'queued',
  'running',
  'done',
  'error',
  'timeout',
  'cancelled',
  'skipped',
]
const EMPTY_DETECTION_RESULTS: VeridropDetectionResult[] = []
const DEFAULT_MANUAL_FORM: VeridropManualDetectionRequest = {
  protocol: 'openai',
  base_url: 'https://',
  api_key: '',
  model: '',
  mode: 'quick',
  include_long_context: false,
  include_long_context_extreme: false,
  openai_wire_api: 'chat_completions',
}

type Translator = (key: string) => string
type ResultStatusFilter = 'all' | VeridropDetectionStatus
type ResultTimeFilter = 'all' | '24h' | '7d' | '30d'

type DetectionSectionProps = {
  title: ReactNode
  description?: ReactNode
  icon?: ReactNode
  action?: ReactNode
  children: ReactNode
  className?: string
}

function DetectionSection({
  title,
  description,
  icon,
  action,
  children,
  className,
}: DetectionSectionProps) {
  return (
    <section className={cn('rounded-lg border bg-background p-4', className)}>
      <div className='mb-4 flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between'>
        <div className='min-w-0'>
          <div className='flex items-center gap-2'>
            {icon != null && (
              <span className='text-muted-foreground shrink-0'>{icon}</span>
            )}
            <h3 className='text-base font-semibold'>{title}</h3>
          </div>
          {description != null && (
            <p className='text-muted-foreground mt-1 text-sm'>{description}</p>
          )}
        </div>
        {action != null && (
          <div className='flex shrink-0 flex-wrap items-center gap-2'>
            {action}
          </div>
        )}
      </div>
      {children}
    </section>
  )
}

function getVeridropModeLabel(mode: string | undefined, t: Translator) {
  switch (mode) {
    case 'standard':
      return t('Standard')
    case 'full':
    case 'strict':
      return t('Full')
    case 'quick':
    default:
      return t('Quick')
  }
}

function getVeridropProtocolLabel(protocol: string | undefined, t: Translator) {
  switch (protocol) {
    case 'anthropic':
      return t('Anthropic')
    case 'gemini':
      return t('Gemini')
    case 'openai':
    default:
      return t('OpenAI Compatible')
  }
}

function getResultTimeLabel(timeFilter: ResultTimeFilter, t: Translator) {
  switch (timeFilter) {
    case '24h':
      return t('Last 24 hours')
    case '7d':
      return t('Last 7 days')
    case '30d':
      return t('Last 30 days')
    case 'all':
    default:
      return t('Any time')
  }
}

function getResultSinceSeconds(timeFilter: ResultTimeFilter, nowSeconds: number) {
  if (timeFilter === '24h') return nowSeconds - 24 * 60 * 60
  if (timeFilter === '7d') return nowSeconds - 7 * 24 * 60 * 60
  if (timeFilter === '30d') return nowSeconds - 30 * 24 * 60 * 60
  return 0
}

function escapeCsvCell(value: string | number | null | undefined) {
  const text = value == null ? '' : String(value)
  if (!/[",\n\r]/.test(text)) return text
  return `"${text.replaceAll('"', '""')}"`
}

function downloadTextFile(filename: string, content: string, type: string) {
  const blob = new Blob([content], { type })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.append(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
}

function getResultDisplayMessage(result: VeridropDetectionResult) {
  const raw = result.error || result.run_error || result.summary
  if (raw === '') return '-'
  const messageMatch = raw.match(/"message"\s*:\s*"([^"]+)"/)
  if (messageMatch?.[1] != null && messageMatch[1] !== '') {
    return messageMatch[1]
  }
  const detailMatch = raw.match(/"detail"\s*:\s*"([^"]+)"/)
  if (detailMatch?.[1] != null && detailMatch[1] !== '') {
    return detailMatch[1]
  }
  return raw
}

function buildDetectionReportCsv(results: VeridropDetectionResult[]) {
  const headers = [
    'ID',
    'Channel ID',
    'Channel',
    'Model',
    'Protocol',
    'Mode',
    'Status',
    'Score',
    'Verdict',
    'Summary',
    'Error',
    'Updated At',
  ]
  const rows = results.map((result) => [
    result.id,
    result.channel_id,
    result.channel_name,
    result.model,
    result.protocol,
    result.mode,
    result.status,
    result.score > 0 ? result.score : '',
    result.verdict,
    result.summary,
    result.error || result.run_error,
    formatTimestampToDate(result.updated_at),
  ])

  return [headers, ...rows]
    .map((row) => row.map((value) => escapeCsvCell(value)).join(','))
    .join('\n')
}

const TASK_STATUS_CLASS_NAME: Record<SystemTaskStatus, string> = {
  pending:
    'bg-amber-50 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300',
  running:
    'bg-sky-50 text-sky-700 dark:bg-sky-500/15 dark:text-sky-300',
  succeeded:
    'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300',
  failed: '',
}

const RESULT_STATUS_CLASS_NAME: Record<VeridropDetectionStatus, string> = {
  queued:
    'bg-amber-50 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300',
  running: 'bg-sky-50 text-sky-700 dark:bg-sky-500/15 dark:text-sky-300',
  done: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300',
  error: '',
  timeout: '',
  cancelled: 'bg-muted text-muted-foreground',
  skipped: 'bg-muted text-muted-foreground',
}

function isActiveTask(task: SystemTask) {
  return task.status === 'pending' || task.status === 'running'
}

function isActiveResult(result: VeridropDetectionResult) {
  return result.status === 'queued' || result.status === 'running'
}

function numberInputValue(value: number) {
  return Number.isFinite(value) ? String(value) : ''
}

function clampInteger(value: string, fallback: number, min: number, max: number) {
  const parsed = Number.parseInt(value, 10)
  if (!Number.isFinite(parsed)) return fallback
  return Math.min(max, Math.max(min, parsed))
}

function hasUnsavedVeridropChanges(
  options: VeridropDetectionOptions,
  savedOptions: VeridropDetectionOptions
) {
  return (
    JSON.stringify({ ...options, admin_api_key: '' }) !==
      JSON.stringify({ ...savedOptions, admin_api_key: '' }) ||
    options.admin_api_key.trim() !== ''
  )
}

type SettingFieldProps = {
  label: string
  description?: string
  children: ReactNode
}

function SettingField({ label, description, children }: SettingFieldProps) {
  return (
    <div className='grid gap-2'>
      <Label>{label}</Label>
      {children}
      {description != null && (
        <p className='text-muted-foreground text-xs'>{description}</p>
      )}
    </div>
  )
}

function CompactField({
  label,
  children,
}: Pick<SettingFieldProps, 'label' | 'children'>) {
  return (
    <div className='grid min-w-0 gap-1'>
      <Label className='text-muted-foreground text-xs'>{label}</Label>
      {children}
    </div>
  )
}

type SwitchRowProps = {
  label: string
  description: string
  checked: boolean
  onCheckedChange: (checked: boolean) => void
}

function SwitchRow({
  label,
  description,
  checked,
  onCheckedChange,
}: SwitchRowProps) {
  return (
    <div className='flex items-center justify-between gap-4 rounded-md border px-3 py-3'>
      <div className='min-w-0'>
        <div className='text-sm font-medium'>{label}</div>
        <p className='text-muted-foreground mt-0.5 text-xs'>{description}</p>
      </div>
      <Switch checked={checked} onCheckedChange={onCheckedChange} />
    </div>
  )
}

type DetectionSettingsDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  options: VeridropDetectionOptions
  savedOptions: VeridropDetectionOptions
  onChange: (options: VeridropDetectionOptions) => void
  onSave: () => void
  isSaving: boolean
}

function DetectionSettingsDialog({
  open,
  onOpenChange,
  options,
  savedOptions,
  onChange,
  onSave,
  isSaving,
}: DetectionSettingsDialogProps) {
  const { t } = useTranslation()
  const dirty =
    hasUnsavedVeridropChanges(options, savedOptions)

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('Detection Settings')}
      description={t('Configure the Veridrop monitor backend used by this page.')}
      contentClassName='sm:max-w-4xl'
      contentHeight='min(70vh, 620px)'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            onClick={() => onOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button type='button' onClick={onSave} disabled={!dirty || isSaving}>
            <Save data-icon='inline-start' className='size-4' />
            {isSaving ? t('Saving...') : t('Save')}
          </Button>
        </>
      }
    >
      <div className='grid gap-4 lg:grid-cols-2'>
        <SwitchRow
          label={t('Enable Veridrop Detection')}
          description={t('Allow detection tasks to call the configured Veridrop backend.')}
          checked={options.enabled}
          onCheckedChange={(enabled) => onChange({ ...options, enabled })}
        />
        <SwitchRow
          label={t('Auto-disable Failed Channels')}
          description={t('Disabled by default. When enabled, failed detections can disable the channel.')}
          checked={options.auto_disable_enabled}
          onCheckedChange={(auto_disable_enabled) =>
            onChange({ ...options, auto_disable_enabled })
          }
        />
        <SettingField
          label={t('Veridrop Base URL')}
          description={t('The backend address of veridrop-monitor.')}
        >
          <Input
            value={options.base_url}
            placeholder='https://'
            onChange={(event) =>
              onChange({ ...options, base_url: event.target.value })
            }
          />
        </SettingField>
        <SettingField
          label={t('Admin API Key')}
          description={t('Leave blank to keep the existing secret unchanged.')}
        >
          <Input
            type='password'
            value={options.admin_api_key}
            placeholder='sk-...'
            autoComplete='new-password'
            onChange={(event) =>
              onChange({ ...options, admin_api_key: event.target.value })
            }
          />
        </SettingField>
        <SettingField label={t('Detection Mode')}>
          <Select
            value={options.default_mode}
            onValueChange={(default_mode) =>
              onChange({ ...options, default_mode: default_mode ?? 'quick' })
            }
          >
            <SelectTrigger className='w-full'>
              <SelectValue>
                {getVeridropModeLabel(options.default_mode, t)}
              </SelectValue>
            </SelectTrigger>
            <SelectContent align='start' alignItemWithTrigger={false}>
              <SelectItem value='quick'>{t('Quick')}</SelectItem>
              <SelectItem value='standard'>{t('Standard')}</SelectItem>
              <SelectItem value='full'>{t('Full')}</SelectItem>
            </SelectContent>
          </Select>
        </SettingField>
        <SettingField
          label={t('OpenAI Wire API')}
          description={t('Optional wire protocol passed to OpenAI-compatible checks.')}
        >
          <Input
            value={options.default_openai_wire_api}
            placeholder='chat_completions'
            onChange={(event) =>
              onChange({
                ...options,
                default_openai_wire_api: event.target.value,
              })
            }
          />
        </SettingField>
        <SettingField
          label={t('Auto-disable Threshold')}
          description={t('Failed results at or below this score can disable channels when auto-disable is enabled.')}
        >
          <Input
            type='number'
            min={0}
            max={100}
            value={numberInputValue(options.auto_disable_failed_threshold)}
            onChange={(event) =>
              onChange({
                ...options,
                auto_disable_failed_threshold: clampInteger(
                  event.target.value,
                  VERIDROP_DEFAULT_OPTIONS.auto_disable_failed_threshold,
                  0,
                  100
                ),
              })
            }
          />
        </SettingField>
        <SettingField label={t('Max Concurrent Detections')}>
          <Input
            type='number'
            min={1}
            max={20}
            value={numberInputValue(options.max_concurrent)}
            onChange={(event) =>
              onChange({
                ...options,
                max_concurrent: clampInteger(
                  event.target.value,
                  VERIDROP_DEFAULT_OPTIONS.max_concurrent,
                  1,
                  20
                ),
              })
            }
          />
        </SettingField>
        <SwitchRow
          label={t('Long-context Probe')}
          description={t('Run the longer probe when the monitor backend supports it.')}
          checked={options.include_long_context}
          onCheckedChange={(include_long_context) =>
            onChange({ ...options, include_long_context })
          }
        />
        <SwitchRow
          label={t('Extreme Long-context Probe')}
          description={t('Use only when the upstream and monitor backend can tolerate the cost.')}
          checked={options.include_long_context_extreme}
          onCheckedChange={(include_long_context_extreme) =>
            onChange({ ...options, include_long_context_extreme })
          }
        />
      </div>
    </Dialog>
  )
}

type DetectionRunCardProps = {
  options: VeridropDetectionOptions
  activeTask: SystemTask | null
  targetChannelCount: number
  targetModelCount: number
  skippedChannelCount: number
  targetsLoading: boolean
  targetsError: boolean
  onRun: () => void
  isRunning: boolean
  onRefresh: () => void
  isRefreshing: boolean
  settingsDirty: boolean
}

function DetectionRunCard({
  options,
  activeTask,
  targetChannelCount,
  targetModelCount,
  skippedChannelCount,
  targetsLoading,
  targetsError,
  onRun,
  isRunning,
  onRefresh,
  isRefreshing,
  settingsDirty,
}: DetectionRunCardProps) {
  const { t, i18n } = useTranslation()
  let disabledReason: string | null = null
  if (settingsDirty) {
    disabledReason = t('Save your Veridrop settings before starting detection.')
  } else if (!options.enabled || options.base_url.trim() === '') {
    disabledReason = t(
      'Complete and enable the Veridrop settings before starting detection.'
    )
  } else if (targetsLoading) {
    disabledReason = t('Loading detection coverage...')
  } else if (targetsError) {
    disabledReason = t('Detection coverage is unavailable. Refresh before starting detection.')
  } else if (!targetsLoading && targetModelCount === 0) {
    disabledReason = t('No enabled channel/model targets are ready for detection.')
  }

  return (
    <DetectionSection
      title={t('Batch Detection')}
      description={t('Runs all enabled channels and their enabled models against each channel base URL.')}
      icon={<Play className='size-4' aria-hidden='true' />}
      action={
        <div className='flex w-full flex-wrap items-center gap-2 sm:w-auto'>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={onRefresh}
            disabled={isRefreshing}
          >
            <RefreshCw
              data-icon='inline-start'
              className={cn('size-4', isRefreshing && 'animate-spin')}
            />
            {t('Refresh')}
          </Button>
          <Button
            type='button'
            size='sm'
            onClick={onRun}
            disabled={isRunning || disabledReason != null}
          >
            <Play data-icon='inline-start' className='size-4' />
            {isRunning ? t('Starting...') : t('Run Detection')}
          </Button>
        </div>
      }
    >
      <div className='space-y-4'>
        {disabledReason != null && (
          <div className='bg-muted/40 text-muted-foreground flex items-start gap-2 rounded-md px-3 py-2 text-sm'>
            <AlertTriangle className='mt-0.5 size-4 shrink-0' />
            <span>{disabledReason}</span>
          </div>
        )}
        <div className='grid gap-x-6 gap-y-3 sm:grid-cols-3'>
          <div>
            <div className='text-muted-foreground text-xs'>{t('Target')}</div>
            <div className='mt-1 text-sm font-medium'>
              {t('Channel base URL')}
            </div>
          </div>
          <div>
            <div className='text-muted-foreground text-xs'>{t('Scope')}</div>
            <div className='mt-1 text-sm font-medium'>
              {t('{{channels}} channels / {{models}} models', {
                channels: targetChannelCount,
                models: targetModelCount,
              })}
            </div>
          </div>
          <div>
            <div className='text-muted-foreground text-xs'>{t('Failure Policy')}</div>
            <div className='mt-1 text-sm font-medium'>
              {options.auto_disable_enabled
                ? t('Auto-disable enabled')
                : t('Record only')}
            </div>
          </div>
        </div>
        {skippedChannelCount > 0 && (
          <div className='text-muted-foreground px-1 text-xs'>
            {t('{{count}} channels are not included because they are missing models or use unsupported protocols.', {
              count: skippedChannelCount,
            })}
          </div>
        )}
        {activeTask != null ? (
          <div className='bg-muted/40 flex flex-col gap-3 rounded-md px-3 py-3 sm:flex-row sm:items-center sm:justify-between'>
            <div className='min-w-0'>
              <div className='flex items-center gap-2'>
                <Badge
                  variant={
                    activeTask.status === 'failed' ? 'destructive' : 'outline'
                  }
                  className={TASK_STATUS_CLASS_NAME[activeTask.status]}
                >
                  {t(activeTask.status)}
                </Badge>
                <span className='text-sm font-medium'>
                  {t('Latest detection task')}
                </span>
              </div>
              <p
                className='text-muted-foreground mt-1 truncate text-xs'
                title={formatTimestampToDate(activeTask.updated_at)}
              >
                {t('Updated {{time}}', {
                  time: formatTimestampRelative(
                    activeTask.updated_at,
                    'seconds',
                    toIntlLocale(i18n.language)
                  ),
                })}
              </p>
            </div>
            <div className='text-muted-foreground truncate font-mono text-xs'>
              {activeTask.task_id}
            </div>
          </div>
        ) : (
          <div className='text-muted-foreground bg-muted/30 rounded-md px-4 py-6 text-center text-sm'>
            {t('No Veridrop detection task is currently active.')}
          </div>
        )}
      </div>
    </DetectionSection>
  )
}

type ManualDetectionCardProps = {
  form: VeridropManualDetectionRequest
  options: VeridropDetectionOptions
  settingsDirty: boolean
  onChange: (form: VeridropManualDetectionRequest) => void
  onSubmit: () => void
  isSubmitting: boolean
}

function ManualDetectionCard({
  form,
  options,
  settingsDirty,
  onChange,
  onSubmit,
  isSubmitting,
}: ManualDetectionCardProps) {
  const { t } = useTranslation()
  const canSubmit =
    !settingsDirty &&
    options.enabled &&
    options.base_url.trim() !== '' &&
    form.base_url.trim() !== '' &&
    form.base_url.trim() !== 'https://' &&
    form.api_key.trim() !== '' &&
    form.model.trim() !== '' &&
    form.protocol.trim() !== ''
  let disabledReason: string | null = null
  if (settingsDirty) {
    disabledReason = t('Save your Veridrop settings before starting detection.')
  } else if (!options.enabled || options.base_url.trim() === '') {
    disabledReason = t(
      'Complete and enable the Veridrop settings before starting detection.'
    )
  } else if (!canSubmit) {
    disabledReason = t('Fill in Base URL, API Key, model and protocol before testing.')
  }

  return (
    <DetectionSection
      title={t('Manual Test')}
      description={t('Test a temporary upstream without adding it as a channel.')}
      icon={<FlaskConical className='size-4' aria-hidden='true' />}
      action={
        <Button
          type='button'
          size='sm'
          onClick={onSubmit}
          disabled={!canSubmit || isSubmitting}
          className='w-full sm:w-auto'
        >
          <Play data-icon='inline-start' className='size-4' />
          {isSubmitting ? t('Starting...') : t('Start Test')}
        </Button>
      }
    >
      <div className='grid gap-4 lg:grid-cols-2'>
        <SettingField label={t('Protocol')}>
          <Select
            value={form.protocol}
            onValueChange={(protocol) =>
              onChange({ ...form, protocol: protocol ?? '' })
            }
          >
            <SelectTrigger className='w-full'>
              <SelectValue>
                {getVeridropProtocolLabel(form.protocol, t)}
              </SelectValue>
            </SelectTrigger>
            <SelectContent align='start' alignItemWithTrigger={false}>
              <SelectItem value='openai'>{t('OpenAI Compatible')}</SelectItem>
              <SelectItem value='anthropic'>{t('Anthropic')}</SelectItem>
              <SelectItem value='gemini'>{t('Gemini')}</SelectItem>
            </SelectContent>
          </Select>
        </SettingField>
        <SettingField label={t('Detection Mode')}>
          <Select
            value={form.mode || options.default_mode}
            onValueChange={(mode) => onChange({ ...form, mode: mode ?? '' })}
          >
            <SelectTrigger className='w-full'>
              <SelectValue>
                {getVeridropModeLabel(form.mode || options.default_mode, t)}
              </SelectValue>
            </SelectTrigger>
            <SelectContent align='start' alignItemWithTrigger={false}>
              <SelectItem value='quick'>{t('Quick')}</SelectItem>
              <SelectItem value='standard'>{t('Standard')}</SelectItem>
              <SelectItem value='full'>{t('Full')}</SelectItem>
            </SelectContent>
          </Select>
        </SettingField>
        <SettingField
          label={t('Upstream Base URL')}
          description={t('The temporary upstream address to test.')}
        >
          <Input
            value={form.base_url}
            placeholder='https://api.example.com/v1'
            onChange={(event) =>
              onChange({ ...form, base_url: event.target.value })
            }
          />
        </SettingField>
        <SettingField
          label={t('API Key')}
          description={t('Only used for this test and never stored in system tasks.')}
        >
          <Input
            type='password'
            value={form.api_key}
            placeholder='sk-...'
            autoComplete='new-password'
            onChange={(event) =>
              onChange({ ...form, api_key: event.target.value })
            }
          />
        </SettingField>
        <SettingField label={t('Model')}>
          <Input
            value={form.model}
            placeholder='gpt-5'
            onChange={(event) =>
              onChange({ ...form, model: event.target.value })
            }
          />
        </SettingField>
        <SettingField
          label={t('OpenAI Wire API')}
          description={t('Only used when protocol is openai.')}
        >
          <Input
            value={form.openai_wire_api || ''}
            placeholder='chat_completions'
            disabled={form.protocol !== 'openai'}
            onChange={(event) =>
              onChange({ ...form, openai_wire_api: event.target.value })
            }
          />
        </SettingField>
        <SwitchRow
          label={t('Long-context Probe')}
          description={t('Run the longer probe when the monitor backend supports it.')}
          checked={Boolean(form.include_long_context)}
          onCheckedChange={(include_long_context) =>
            onChange({ ...form, include_long_context })
          }
        />
        <SwitchRow
          label={t('Extreme Long-context Probe')}
          description={t('Use only when the upstream and monitor backend can tolerate the cost.')}
          checked={Boolean(form.include_long_context_extreme)}
          onCheckedChange={(include_long_context_extreme) =>
            onChange({ ...form, include_long_context_extreme })
          }
        />
      </div>
      {disabledReason != null && (
        <div className='bg-muted/40 text-muted-foreground mt-4 flex items-start gap-2 rounded-md px-3 py-2 text-xs'>
          <AlertTriangle className='mt-0.5 size-4 shrink-0' />
          <span>{disabledReason}</span>
        </div>
      )}
    </DetectionSection>
  )
}

type DetectionTargetsCardProps = {
  targets: VeridropDetectionTarget[]
  channelCount: number
  modelCount: number
  skippedChannelCount: number
  loading: boolean
  isError: boolean
  onRetry: () => void
}

function getTargetSkippedLabel(
  reason: string | undefined,
  t: (key: string) => string
) {
  if (reason === 'unsupported channel protocol') {
    return t('Unsupported protocol')
  }
  if (reason === 'channel has no enabled models') {
    return t('No enabled models')
  }
  return reason != null && reason !== '' ? reason : t('Not included')
}

function DetectionTargetsCard({
  targets,
  channelCount,
  modelCount,
  skippedChannelCount,
  loading,
  isError,
  onRetry,
}: DetectionTargetsCardProps) {
  const { t } = useTranslation()
  const visibleTargets = targets.slice(0, 12)
  const hiddenCount = Math.max(0, targets.length - visibleTargets.length)
  let content: ReactNode

  if (loading) {
    content = (
      <div className='space-y-2'>
        {['target-skeleton-1', 'target-skeleton-2', 'target-skeleton-3'].map(
          (key) => (
            <Skeleton key={key} className='h-16 w-full rounded-md' />
          )
        )}
      </div>
    )
  } else if (isError) {
    content = (
      <ErrorState
        title={t('We could not load detection coverage.')}
        description={t('Refresh the page or try again later.')}
        onRetry={onRetry}
        className='min-h-[180px]'
      />
    )
  } else if (targets.length === 0) {
    content = (
      <div className='text-muted-foreground bg-muted/30 rounded-md px-4 py-8 text-center text-sm'>
        {t('No enabled channels with models are ready for detection.')}
      </div>
    )
  } else {
    content = (
      <div className='space-y-2'>
        <div className='text-muted-foreground flex flex-wrap items-center gap-2 text-xs'>
          <span>
            {t('{{channels}} channels / {{models}} models', {
              channels: channelCount,
              models: modelCount,
            })}
          </span>
          {skippedChannelCount > 0 && (
            <span>
              {t('{{count}} skipped', { count: skippedChannelCount })}
            </span>
          )}
        </div>
        <div className='max-h-[300px] overflow-y-auto rounded-md border'>
          <div className='bg-muted/40 text-muted-foreground sticky top-0 z-10 hidden grid-cols-[minmax(180px,1.1fr)_minmax(220px,1.5fr)_120px_minmax(260px,2fr)_64px] gap-3 border-b px-3 py-2 text-xs font-medium xl:grid'>
            <span>{t('Channel')}</span>
            <span>{t('Base URL')}</span>
            <span>{t('Protocol')}</span>
            <span>{t('Models')}</span>
            <span className='text-right'>ID</span>
          </div>
          {visibleTargets.map((target) => {
            const models = Array.isArray(target.models) ? target.models : []
            const skipped =
              target.skipped_reason != null && target.skipped_reason !== ''
            return (
              <div
                key={target.channel_id}
                className='grid gap-2 border-b px-3 py-2 last:border-b-0 xl:grid-cols-[minmax(180px,1.1fr)_minmax(220px,1.5fr)_120px_minmax(260px,2fr)_64px] xl:items-start xl:gap-3'
              >
                <div className='min-w-0'>
                  <div className='flex items-center gap-2'>
                    {skipped ? (
                      <AlertTriangle className='text-muted-foreground size-4 shrink-0' />
                    ) : (
                      <CheckCircle2 className='text-emerald-600 size-4 shrink-0 dark:text-emerald-400' />
                    )}
                    <span className='truncate text-sm font-medium'>
                      {target.channel_name || `#${target.channel_id}`}
                    </span>
                  </div>
                </div>
                <div
                  className='text-muted-foreground min-w-0 truncate font-mono text-[11px]'
                  title={target.base_url}
                >
                  {target.base_url || '-'}
                </div>
                <div>
                  <Badge variant='outline' className='text-[11px]'>
                    {target.protocol || target.channel_type_name}
                  </Badge>
                </div>
                <div className='min-w-0'>
                  {skipped ? (
                    <span className='text-muted-foreground text-xs'>
                      {getTargetSkippedLabel(target.skipped_reason, t)}
                    </span>
                  ) : (
                    <div className='flex flex-wrap gap-1.5'>
                      {models.slice(0, 6).map((model) => (
                        <Badge
                          key={`${target.channel_id}-${model}`}
                          variant='secondary'
                          className='max-w-[220px] truncate font-mono text-[11px]'
                        >
                          {model}
                        </Badge>
                      ))}
                      {models.length > 6 && (
                        <Badge variant='outline' className='text-[11px]'>
                          {t('+{{count}} more', {
                            count: models.length - 6,
                          })}
                        </Badge>
                      )}
                    </div>
                  )}
                </div>
                <div className='text-muted-foreground text-right font-mono text-[11px]'>
                  #{target.channel_id}
                </div>
              </div>
            )
          })}
        </div>
        {hiddenCount > 0 && (
          <div className='text-muted-foreground px-3 py-1 text-center text-xs'>
            {t('{{count}} more channels are hidden in this preview.', {
              count: hiddenCount,
            })}
          </div>
        )}
      </div>
    )
  }

  return (
    <DetectionSection
      title={t('Detection Coverage')}
      description={t('Review the enabled channels and models before starting detection.')}
      icon={<ListChecks className='size-4' aria-hidden='true' />}
      action={
        <div className='flex w-full flex-wrap items-center gap-2 sm:w-auto'>
          <Badge variant='outline'>
            {t('{{count}} channels', { count: channelCount })}
          </Badge>
          <Badge variant='outline'>
            {t('{{count}} models', { count: modelCount })}
          </Badge>
          {skippedChannelCount > 0 && (
            <Badge variant='secondary'>
              {t('{{count}} skipped', { count: skippedChannelCount })}
            </Badge>
          )}
        </div>
      }
    >
      {content}
    </DetectionSection>
  )
}

type DetectionResultsTableProps = {
  results: VeridropDetectionResult[]
  loading: boolean
  emptyMessage?: string
}

function DetectionResultsTable({
  results,
  loading,
  emptyMessage,
}: DetectionResultsTableProps) {
  const { t, i18n } = useTranslation()

  if (loading) {
    const skeletonRows = [
      'detection-result-skeleton-1',
      'detection-result-skeleton-2',
      'detection-result-skeleton-3',
      'detection-result-skeleton-4',
      'detection-result-skeleton-5',
      'detection-result-skeleton-6',
    ]
    return (
      <div className='space-y-2'>
        {skeletonRows.map((key) => (
          <Skeleton key={key} className='h-10 w-full rounded-md' />
        ))}
      </div>
    )
  }

  if (results.length === 0) {
    return (
      <div className='text-muted-foreground bg-muted/30 rounded-md px-4 py-10 text-center text-sm'>
        {emptyMessage ?? t('No detection results yet.')}
      </div>
    )
  }

  return (
    <div className='overflow-hidden rounded-md border'>
      <div className='bg-muted/40 text-muted-foreground hidden grid-cols-[minmax(180px,1.2fr)_minmax(180px,1fr)_150px_minmax(260px,2fr)_140px] gap-3 border-b px-4 py-2 text-xs font-medium xl:grid'>
        <span>{t('Channel')}</span>
        <span>{t('Model')}</span>
        <span>{t('Status')}</span>
        <span>{t('Summary')}</span>
        <span>{t('Updated')}</span>
      </div>
      <div className='divide-y'>
        {results.map((result) => {
          const message = getResultDisplayMessage(result)
          return (
            <div
              key={result.id}
              className='grid gap-3 px-4 py-3 hover:bg-muted/30 xl:grid-cols-[minmax(180px,1.2fr)_minmax(180px,1fr)_150px_minmax(260px,2fr)_140px] xl:items-center'
            >
              <div className='min-w-0'>
                <div className='truncate text-sm font-medium'>
                  {result.channel_name || `#${result.channel_id}`}
                </div>
                <div className='mt-1 flex flex-wrap items-center gap-1.5'>
                  <span className='text-muted-foreground font-mono text-[11px]'>
                    #{result.channel_id}
                  </span>
                  {result.protocol !== '' && (
                    <Badge variant='outline' className='text-[11px]'>
                      {result.protocol}
                    </Badge>
                  )}
                  {result.mode !== '' && (
                    <Badge variant='secondary' className='text-[11px]'>
                      {getVeridropModeLabel(result.mode, t)}
                    </Badge>
                  )}
                </div>
              </div>
              <div className='min-w-0 truncate font-mono text-xs'>
                {result.model || '-'}
              </div>
              <div className='flex flex-wrap items-center gap-2'>
                <Badge
                  variant={
                    result.status === 'error' || result.status === 'timeout'
                      ? 'destructive'
                      : 'outline'
                  }
                  className={RESULT_STATUS_CLASS_NAME[result.status]}
                >
                  {t(result.status)}
                </Badge>
                <span className='text-muted-foreground text-xs tabular-nums'>
                  {result.score > 0 ? result.score : '-'}
                </span>
                {result.verdict !== '' && (
                  <span className='text-muted-foreground max-w-[120px] truncate text-xs'>
                    {result.verdict}
                  </span>
                )}
              </div>
              <div
                className='text-muted-foreground line-clamp-2 min-w-0 text-sm'
                title={result.error || result.run_error || result.summary}
              >
                {message}
              </div>
              <div
                className='text-muted-foreground text-xs whitespace-nowrap'
                title={formatTimestampToDate(result.updated_at)}
              >
                {formatTimestampRelative(
                  result.updated_at,
                  'seconds',
                  toIntlLocale(i18n.language)
                )}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}

export function VeridropDetection() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [draftOptions, setDraftOptions] =
    useState<VeridropDetectionOptions>(VERIDROP_DEFAULT_OPTIONS)
  const [runConfirmOpen, setRunConfirmOpen] = useState(false)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [manualForm, setManualForm] =
    useState<VeridropManualDetectionRequest>(DEFAULT_MANUAL_FORM)
  const [resultStatusFilter, setResultStatusFilter] =
    useState<ResultStatusFilter>('all')
  const [resultKeyword, setResultKeyword] = useState('')
  const [resultTimeFilter, setResultTimeFilter] =
    useState<ResultTimeFilter>('all')

  const optionsQuery = useQuery({
    queryKey: ['veridrop-detection', 'options'],
    queryFn: getVeridropOptions,
    staleTime: 60 * 1000,
  })

  const resultRequest = useMemo(() => {
    const keyword = resultKeyword.trim()
    const updatedAfter = getResultSinceSeconds(
      resultTimeFilter,
      Math.floor(Date.now() / 1000)
    )
    const request: Parameters<typeof listVeridropDetectionResults>[0] = {
      limit: RESULT_LIMIT,
    }
    if (resultStatusFilter !== 'all') request.status = resultStatusFilter
    if (keyword !== '') request.keyword = keyword
    if (updatedAfter > 0) request.updated_after = updatedAfter
    return request
  }, [resultKeyword, resultStatusFilter, resultTimeFilter])

  const resultsQuery = useQuery({
    queryKey: ['veridrop-detection', 'results', resultRequest],
    queryFn: async () => {
      const res = await listVeridropDetectionResults(resultRequest)
      if (!res.success || !Array.isArray(res.data?.items)) {
        throw new Error(res.message || t('We could not load detection results.'))
      }
      return res.data.items
    },
    retry: false,
    refetchInterval: (query) =>
      query.state.data?.some((result) => isActiveResult(result))
        ? ACTIVE_POLL_INTERVAL_MS
        : false,
  })

  const tasksQuery = useQuery({
    queryKey: ['veridrop-detection', 'tasks'],
    queryFn: async () => {
      const res = await listSystemTasks(30)
      if (!res.success || !Array.isArray(res.data)) {
        throw new Error(res.message || t('We could not load system tasks.'))
      }
      return res.data.filter((task) => task.type === 'veridrop_detection')
    },
    retry: false,
    refetchInterval: (query) =>
      query.state.data?.some((task) => isActiveTask(task))
        ? ACTIVE_POLL_INTERVAL_MS
        : false,
  })

  const targetRequest = useMemo(
    () => ({
      mode: draftOptions.default_mode,
      include_long_context: draftOptions.include_long_context,
      include_long_context_extreme: draftOptions.include_long_context_extreme,
      openai_wire_api: draftOptions.default_openai_wire_api,
    }),
    [
      draftOptions.default_mode,
      draftOptions.default_openai_wire_api,
      draftOptions.include_long_context,
      draftOptions.include_long_context_extreme,
    ]
  )

  const targetsQuery = useQuery({
    queryKey: ['veridrop-detection', 'targets', targetRequest],
    queryFn: async () => {
      const res = await listVeridropDetectionTargets(targetRequest)
      if (!res.success || res.data == null) {
        throw new Error(res.message || t('We could not load detection coverage.'))
      }
      return res.data
    },
    retry: false,
    staleTime: 30 * 1000,
  })

  useEffect(() => {
    if (optionsQuery.data != null) {
      setDraftOptions({ ...optionsQuery.data, admin_api_key: '' })
    }
  }, [optionsQuery.data])

  const saveMutation = useMutation({
    mutationFn: () =>
      updateVeridropOptions(
        draftOptions,
        optionsQuery.data ?? VERIDROP_DEFAULT_OPTIONS
      ),
    onSuccess: (count) => {
      toast.success(
        count === 0 ? t('No changes to save') : t('Setting updated successfully')
      )
      setSettingsOpen(false)
      queryClient.invalidateQueries({
        queryKey: ['veridrop-detection', 'options'],
      })
    },
    onError: () => {
      toast.error(t('Settings were not saved. Check the values and try again.'))
    },
  })

  const runMutation = useMutation({
    mutationFn: () =>
      startEnabledVeridropDetection({
        mode: draftOptions.default_mode,
        include_long_context: draftOptions.include_long_context,
        include_long_context_extreme:
          draftOptions.include_long_context_extreme,
        openai_wire_api: draftOptions.default_openai_wire_api,
      }),
    onSuccess: (res) => {
      if (!res.success || res.data == null) {
        toast.error(
          res.message ||
            t('Detection was not started. Check the settings and try again.')
        )
        return
      }
      toast.success(
        res.data.created
          ? t('Detection task started')
          : t('Detection task is already running')
      )
      queryClient.invalidateQueries({ queryKey: ['veridrop-detection'] })
    },
    onError: () => {
      toast.error(t('Detection was not started. Check the settings and try again.'))
    },
  })

  const manualMutation = useMutation({
    mutationFn: () =>
      startManualVeridropDetection({
        ...manualForm,
        mode: manualForm.mode || draftOptions.default_mode,
        openai_wire_api:
          manualForm.openai_wire_api || draftOptions.default_openai_wire_api,
      }),
    onSuccess: (res) => {
      if (!res.success || res.data == null) {
        toast.error(
          res.message ||
            t('Manual test was not started. Check the values and try again.')
        )
        return
      }
      toast.success(t('Manual test started'))
      setManualForm((current) => ({ ...current, api_key: '' }))
      queryClient.invalidateQueries({ queryKey: ['veridrop-detection'] })
    },
    onError: () => {
      toast.error(t('Manual test was not started. Check the values and try again.'))
    },
  })

  const savedOptions = optionsQuery.data ?? VERIDROP_DEFAULT_OPTIONS
  const settingsDirty = hasUnsavedVeridropChanges(draftOptions, savedOptions)
  const tasks = tasksQuery.data ?? []
  const activeTask = tasks.find((task) => isActiveTask(task)) ?? tasks[0] ?? null
  const results = resultsQuery.data ?? EMPTY_DETECTION_RESULTS
  const filteredResults = useMemo(() => {
    const keyword = resultKeyword.trim().toLowerCase()
    const nowSeconds = Math.floor(Date.now() / 1000)
    const sinceSeconds = getResultSinceSeconds(resultTimeFilter, nowSeconds)
    return results.filter((result) => {
      if (
        resultStatusFilter !== 'all' &&
        result.status !== resultStatusFilter
      ) {
        return false
      }
      if (sinceSeconds > 0 && result.updated_at < sinceSeconds) {
        return false
      }
      if (keyword === '') return true
      return [
        result.channel_name,
        result.model,
        result.protocol,
        result.mode,
        result.verdict,
        result.summary,
        result.error,
        result.run_error,
      ].some((value) => value.toLowerCase().includes(keyword))
    })
  }, [resultKeyword, resultStatusFilter, resultTimeFilter, results])
  const targets = targetsQuery.data?.items ?? []
  const targetChannelCount = targetsQuery.data?.channel_count ?? 0
  const targetModelCount = targetsQuery.data?.model_count ?? 0
  const skippedTargetCount = targetsQuery.data?.skipped_channel_count ?? 0
  const activeResultCount = results.filter((result) =>
    isActiveResult(result)
  ).length
  const failureCount = results.filter((result) =>
    ['error', 'timeout'].includes(result.status)
  ).length
  const runConfirmDescription = draftOptions.auto_disable_enabled
    ? t(
        'This will test {{channels}} channels and {{models}} models. Failed channels can be disabled automatically.',
        { channels: targetChannelCount, models: targetModelCount }
      )
    : t(
        'This will test {{channels}} channels and {{models}} models. Results will be recorded only.',
        { channels: targetChannelCount, models: targetModelCount }
      )

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Veridrop Detection')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
        <Button
          type='button'
          size='sm'
          variant='outline'
          onClick={() => {
            if (optionsQuery.isError) void optionsQuery.refetch()
            setSettingsOpen(true)
          }}
          disabled={optionsQuery.isLoading}
        >
          <Settings2 data-icon='inline-start' className='size-4' />
          {t('Detection Settings')}
        </Button>
        <Badge variant='outline' className='gap-1.5'>
          <ShieldAlert className='size-3.5' aria-hidden='true' />
          {draftOptions.auto_disable_enabled
            ? t('Auto-disable enabled')
            : t('Record only')}
        </Badge>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content className='space-y-4'>
        {optionsQuery.isError && (
          <ErrorState
            title={t('We could not load detection settings.')}
            description={t('Refresh the page or check your administrator permissions.')}
            onRetry={() => {
              void optionsQuery.refetch()
            }}
          />
        )}
        {!optionsQuery.isError && optionsQuery.isLoading && (
          <div className='space-y-4'>
            <Skeleton className='h-12 rounded-lg' />
            <Skeleton className='h-[360px] rounded-lg' />
          </div>
        )}
        {!optionsQuery.isError && !optionsQuery.isLoading && (
          <div className='space-y-4'>
            <Tabs defaultValue='batch' className='space-y-4'>
              <TabsList className='grid w-full max-w-3xl grid-cols-3'>
                <TabsTrigger value='batch'>{t('Batch Detection')}</TabsTrigger>
                <TabsTrigger value='manual'>{t('Manual Test')}</TabsTrigger>
                <TabsTrigger value='results'>{t('Detection Results')}</TabsTrigger>
              </TabsList>
              <TabsContent
                value='batch'
                className='mt-0 grid gap-4 xl:grid-cols-[420px_minmax(0,1fr)]'
              >
                <DetectionRunCard
                  options={draftOptions}
                  activeTask={activeTask}
                  targetChannelCount={targetChannelCount}
                  targetModelCount={targetModelCount}
                  skippedChannelCount={skippedTargetCount}
                  targetsLoading={targetsQuery.isLoading}
                  targetsError={targetsQuery.isError}
                  onRun={() => {
                    if (draftOptions.auto_disable_enabled) {
                      setRunConfirmOpen(true)
                      return
                    }
                    runMutation.mutate()
                  }}
                  isRunning={runMutation.isPending}
                  onRefresh={() => {
                    void tasksQuery.refetch()
                    void resultsQuery.refetch()
                    void targetsQuery.refetch()
                  }}
                  isRefreshing={
                    (tasksQuery.isFetching ||
                      resultsQuery.isFetching ||
                      targetsQuery.isFetching) &&
                    !tasksQuery.isLoading &&
                    !resultsQuery.isLoading &&
                    !targetsQuery.isLoading
                  }
                  settingsDirty={settingsDirty}
                />
                <DetectionTargetsCard
                  targets={targets}
                  channelCount={targetChannelCount}
                  modelCount={targetModelCount}
                  skippedChannelCount={skippedTargetCount}
                  loading={targetsQuery.isLoading}
                  isError={targetsQuery.isError}
                  onRetry={() => {
                    void targetsQuery.refetch()
                  }}
                />
              </TabsContent>
              <TabsContent value='manual' className='mt-0'>
                <ManualDetectionCard
                  form={manualForm}
                  options={draftOptions}
                  settingsDirty={settingsDirty}
                  onChange={setManualForm}
                  onSubmit={() => manualMutation.mutate()}
                  isSubmitting={manualMutation.isPending}
                />
              </TabsContent>
              <TabsContent value='results' className='mt-0'>
                <DetectionSection
                  title={t('Detection Results')}
                  description={t('Recent results produced by Veridrop for enabled channels and models.')}
                  icon={<Activity className='size-4' aria-hidden='true' />}
                  action={
                    <div className='flex w-full flex-wrap items-center gap-2 sm:w-auto'>
                      <Badge variant='outline'>
                        {t('{{count}} running', { count: activeResultCount })}
                      </Badge>
                      <Badge
                        variant={failureCount > 0 ? 'destructive' : 'outline'}
                      >
                        {t('{{count}} failed', { count: failureCount })}
                      </Badge>
                      <Button
                        type='button'
                        size='sm'
                        variant='outline'
                        disabled={filteredResults.length === 0}
                        onClick={() => {
                          downloadTextFile(
                            `veridrop-report-${Date.now()}.csv`,
                            buildDetectionReportCsv(filteredResults),
                            'text/csv;charset=utf-8'
                          )
                        }}
                      >
                        <Download data-icon='inline-start' className='size-4' />
                        {t('Download Report')}
                      </Button>
                    </div>
                  }
                >
                  {resultsQuery.isError ? (
                    <ErrorState
                      title={t('We could not load detection results.')}
                      description={t('Refresh the page or try again later.')}
                      onRetry={() => {
                        void resultsQuery.refetch()
                      }}
                      className='min-h-[240px]'
                    />
                  ) : (
                    <div className='space-y-3'>
                      <div className='grid gap-2 lg:grid-cols-[minmax(260px,1fr)_160px_160px_auto]'>
                        <CompactField label={t('Keyword')}>
                          <Input
                            value={resultKeyword}
                            placeholder={t('Search verdict, summary or error')}
                            onChange={(event) =>
                              setResultKeyword(event.target.value)
                            }
                          />
                        </CompactField>
                        <CompactField label={t('Status')}>
                          <Select
                            value={resultStatusFilter}
                            onValueChange={(status) =>
                              setResultStatusFilter(
                                (status ?? 'all') as ResultStatusFilter
                              )
                            }
                          >
                            <SelectTrigger className='w-full'>
                              <SelectValue>
                                {resultStatusFilter === 'all'
                                  ? t('All statuses')
                                  : t(resultStatusFilter)}
                              </SelectValue>
                            </SelectTrigger>
                            <SelectContent
                              align='start'
                              alignItemWithTrigger={false}
                            >
                              <SelectItem value='all'>
                                {t('All statuses')}
                              </SelectItem>
                              {RESULT_STATUSES.map((status) => (
                                <SelectItem key={status} value={status}>
                                  {t(status)}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        </CompactField>
                        <CompactField label={t('Updated')}>
                          <Select
                            value={resultTimeFilter}
                            onValueChange={(timeFilter) =>
                              setResultTimeFilter(
                                (timeFilter ?? 'all') as ResultTimeFilter
                              )
                            }
                          >
                            <SelectTrigger className='w-full'>
                              <SelectValue>
                                {getResultTimeLabel(resultTimeFilter, t)}
                              </SelectValue>
                            </SelectTrigger>
                            <SelectContent
                              align='start'
                              alignItemWithTrigger={false}
                            >
                              <SelectItem value='all'>{t('Any time')}</SelectItem>
                              <SelectItem value='24h'>
                                {t('Last 24 hours')}
                              </SelectItem>
                              <SelectItem value='7d'>
                                {t('Last 7 days')}
                              </SelectItem>
                              <SelectItem value='30d'>
                                {t('Last 30 days')}
                              </SelectItem>
                            </SelectContent>
                          </Select>
                        </CompactField>
                        <div className='flex items-end'>
                          <Button
                            type='button'
                            variant='outline'
                            className='w-full'
                            onClick={() => {
                              setResultKeyword('')
                              setResultStatusFilter('all')
                              setResultTimeFilter('all')
                            }}
                          >
                            {t('Reset Filters')}
                          </Button>
                        </div>
                      </div>
                      <div className='flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between'>
                        <div className='text-muted-foreground text-xs'>
                          {t('Showing {{count}} matching results', {
                            count: filteredResults.length,
                          })}
                        </div>
                      </div>
                      <DetectionResultsTable
                        results={filteredResults}
                        loading={resultsQuery.isLoading}
                        emptyMessage={
                          results.length === 0
                            ? t('No detection results yet.')
                            : t('No results match these filters. Clear filters or broaden the criteria.')
                        }
                      />
                    </div>
                  )}
                </DetectionSection>
              </TabsContent>
            </Tabs>
          </div>
        )}
        </SectionPageLayout.Content>
      </SectionPageLayout>
      <DetectionSettingsDialog
        open={settingsOpen}
        onOpenChange={setSettingsOpen}
        options={draftOptions}
        savedOptions={savedOptions}
        onChange={setDraftOptions}
        onSave={() => saveMutation.mutate()}
        isSaving={saveMutation.isPending}
      />
      <ConfirmDialog
        open={runConfirmOpen}
        onOpenChange={setRunConfirmOpen}
        title={t('Start Veridrop detection?')}
        desc={runConfirmDescription}
        confirmText={runMutation.isPending ? t('Starting...') : t('Start')}
        destructive={draftOptions.auto_disable_enabled}
        isLoading={runMutation.isPending}
        handleConfirm={() => {
          runMutation.mutate()
          setRunConfirmOpen(false)
        }}
      />
    </>
  )
}

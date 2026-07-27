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
import { Loader2, Save } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

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
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'

import {
  LOG_TYPE_ALL_VALUE,
  LOG_TYPE_FILTERS,
  TIME_RANGE_PRESETS,
} from '../../constants'
import { useDebounce } from '@/hooks'

import { CompactDateTimeRangePicker } from '../../components/compact-date-time-range-picker'
import {
  createExportJob,
  createExportTemplate,
  getExportColumns,
  getExportEstimate,
  getExportTemplates,
} from '../api'
import type { ExportFormat, ExportOptions } from '../types'

import { ColumnPicker } from './column-picker'

const CUSTOM_TEMPLATE = '__custom__'

export interface NewExportPrefill {
  start?: Date
  end?: Date
  logType?: string
  model?: string
  username?: string
  token?: string
  channel?: string
  group?: string
}

interface NewExportSheetProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  prefill?: NewExportPrefill
}

function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || ''
  } catch {
    return ''
  }
}

export function NewExportSheet({
  open,
  onOpenChange,
  prefill,
}: NewExportSheetProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const { data: catalog } = useQuery({
    queryKey: ['log-export-columns'],
    queryFn: getExportColumns,
    enabled: open,
    staleTime: 5 * 60 * 1000,
  })
  const { data: templates } = useQuery({
    queryKey: ['log-export-templates'],
    queryFn: getExportTemplates,
    enabled: open,
  })

  const [range, setRange] = useState<{ start?: Date; end?: Date }>({})
  // 记住是通过哪个预设选的，仅用于高亮；手动改日期后清空。
  const [presetDays, setPresetDays] = useState<number | null>(null)
  const [logType, setLogType] = useState<string>(LOG_TYPE_ALL_VALUE)
  const [model, setModel] = useState('')
  const [username, setUsername] = useState('')
  const [token, setToken] = useState('')
  const [channel, setChannel] = useState('')
  const [group, setGroup] = useState('')
  const [templateId, setTemplateId] = useState<string>('')
  const [columns, setColumns] = useState<string[]>([])
  const [format, setFormat] = useState<ExportFormat>('csv_gz')
  const [options, setOptions] = useState<ExportOptions>({
    csv_bom: true,
    header: true,
    timezone: browserTimezone(),
  })
  const [templateName, setTemplateName] = useState('')

  // Seed from the list view's current filters so "advanced export" continues
  // whatever the admin was already looking at.
  useEffect(() => {
    if (!open) return
    setRange({ start: prefill?.start, end: prefill?.end })
    setLogType(prefill?.logType ?? LOG_TYPE_ALL_VALUE)
    setModel(prefill?.model ?? '')
    setUsername(prefill?.username ?? '')
    setToken(prefill?.token ?? '')
    setChannel(prefill?.channel ?? '')
    setGroup(prefill?.group ?? '')
  }, [open, prefill])

  // Default to the "as displayed" template on first load.
  useEffect(() => {
    if (!catalog || templateId) return
    const preset =
      catalog.builtin_templates.find((tpl) => tpl.is_default) ??
      catalog.builtin_templates[0]
    if (preset) {
      setTemplateId(preset.id)
      setColumns(preset.columns)
    }
  }, [catalog, templateId])

  const templateOptions = useMemo(() => {
    const builtin = (catalog?.builtin_templates ?? []).map((tpl) => ({
      value: tpl.id,
      label: t(tpl.name),
    }))
    const custom = (templates ?? []).map((tpl) => ({
      value: String(tpl.id),
      label: tpl.is_shared ? `${tpl.name} (${t('Shared')})` : tpl.name,
    }))
    return [...builtin, ...custom]
  }, [catalog, templates, t])

  // 跨度上限由后端下发，前端不硬编码；用来禁用超限预设并给出提示。
  const maxRangeDays = Math.max(
    1,
    Math.floor((catalog?.max_range_sec ?? 31 * 86400) / 86400)
  )
  const selectedDays =
    range.start && range.end
      ? Math.max(
          1,
          Math.ceil(
            (range.end.getTime() - range.start.getTime()) / (24 * 3600 * 1000)
          )
        )
      : 0
  const rangeTooLong = selectedDays > maxRangeDays
  const activePresetDays = presetDays

  const applyPreset = (days: number) => {
    const end = new Date()
    const start = new Date(end.getTime() - days * 24 * 3600 * 1000)
    setRange({ start, end })
    setPresetDays(days)
  }

  const applyTemplate = (value: string) => {
    setTemplateId(value)
    const builtin = catalog?.builtin_templates.find((tpl) => tpl.id === value)
    if (builtin) {
      setColumns(builtin.columns)
      return
    }
    const custom = templates?.find((tpl) => String(tpl.id) === value)
    if (custom) {
      setColumns(custom.columns)
      setFormat(custom.format)
      if (custom.options) setOptions(custom.options)
    }
  }

  // 行数估算：判断 xlsx 是否可行，并给出「预计多少行 / 多少个分片」。
  //
  // 三重防护，避免把日志大表当成打字回调来扫：
  //   1. 走专用的有界计数接口（数到上限即止），不是列表接口的完整 COUNT；
  //   2. 文本筛选先 debounce，敲字过程中不发请求；
  //   3. 超出跨度上限时根本不查——那种范围后端本来就会拒绝导出。
  const rangeReady = Boolean(range.start && range.end)
  const debouncedModel = useDebounce(model, 500)
  const debouncedUsername = useDebounce(username, 500)
  const debouncedToken = useDebounce(token, 500)
  const debouncedChannel = useDebounce(channel, 500)
  const debouncedGroup = useDebounce(group, 500)

  const { data: estimate } = useQuery({
    queryKey: [
      'log-export-estimate',
      range.start?.getTime(),
      range.end?.getTime(),
      logType,
      debouncedModel,
      debouncedUsername,
      debouncedToken,
      debouncedChannel,
      debouncedGroup,
    ],
    queryFn: () =>
      getExportEstimate({
        start_timestamp: Math.floor((range.start as Date).getTime() / 1000),
        end_timestamp: Math.floor((range.end as Date).getTime() / 1000),
        type: logType === LOG_TYPE_ALL_VALUE ? undefined : Number(logType),
        model_name: debouncedModel || undefined,
        username: debouncedUsername || undefined,
        token_name: debouncedToken || undefined,
        channel: debouncedChannel ? Number(debouncedChannel) : undefined,
        group: debouncedGroup || undefined,
      }),
    enabled: open && rangeReady && !rangeTooLong,
    staleTime: 60 * 1000,
  })

  const xlsxLimit = catalog?.xlsx_max_rows ?? 200000
  const rowsPerPart = catalog?.rows_per_file ?? 1000000
  const estRows = estimate?.rows ?? 0
  // capped 表示真实行数 ≥ estRows（计数被上限截断），这对判断「超没超 xlsx 上限」
  // 已经足够——上限本身就是按 xlsx_max_rows + 1 取的。
  const estCapped = estimate?.capped ?? false
  const xlsxTooLarge = estCapped || estRows > xlsxLimit
  const estParts = estRows > 0 ? Math.max(1, Math.ceil(estRows / rowsPerPart)) : 0
  useEffect(() => {
    if (xlsxTooLarge && format === 'xlsx') setFormat('csv_gz')
  }, [xlsxTooLarge, format])

  const createJob = useMutation({
    mutationFn: async () => {
      if (!range.start || !range.end) {
        throw new Error(t('Please select the time range to export'))
      }
      return createExportJob({
        start_timestamp: Math.floor(range.start.getTime() / 1000),
        end_timestamp: Math.floor(range.end.getTime() / 1000),
        type: logType === LOG_TYPE_ALL_VALUE ? 0 : Number(logType),
        model_name: model || undefined,
        username: username || undefined,
        token_name: token || undefined,
        channel: channel ? Number(channel) : undefined,
        group: group || undefined,
        columns,
        format,
        // 让后端在创建时就能判断 xlsx 是否可行，避免跑一半才降级。
        est_rows: estRows || undefined,
        options,
      })
    },
    onSuccess: () => {
      toast.success(t('Export started'))
      queryClient.invalidateQueries({ queryKey: ['log-export-jobs'] })
      onOpenChange(false)
    },
    onError: (error: Error) => toast.error(error.message || t('Export failed')),
  })

  const saveTemplate = useMutation({
    mutationFn: async () =>
      createExportTemplate({
        name: templateName.trim(),
        columns,
        format,
        options,
      }),
    onSuccess: (tpl) => {
      toast.success(t('Template saved'))
      setTemplateName('')
      setTemplateId(String(tpl.id))
      queryClient.invalidateQueries({ queryKey: ['log-export-templates'] })
    },
    onError: (error: Error) =>
      toast.error(error.message || t('Could not save the template')),
  })

  const canSubmit =
    rangeReady && !rangeTooLong && columns.length > 0 && !createJob.isPending

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side='right'
        className='flex w-full flex-col gap-0 sm:max-w-3xl'
      >
        <SheetHeader>
          <SheetTitle>{t('New Export')}</SheetTitle>
          <SheetDescription>
            {t(
              'Exports run in the background and are throttled to protect live traffic.'
            )}
          </SheetDescription>
        </SheetHeader>

        <div className='flex-1 space-y-6 overflow-y-auto px-4 py-2'>
          {/* 1. Filters */}
          <section className='space-y-3'>
            <h3 className='text-sm font-semibold'>{t('Filters')}</h3>
            <div className='space-y-1'>
              <Label>{t('Date Range')}</Label>
              <div className='flex flex-wrap items-center gap-2'>
                <CompactDateTimeRangePicker
                  start={range.start}
                  end={range.end}
                  onChange={(next) => {
                    setRange(next)
                    setPresetDays(null)
                  }}
                />
                {/* 导出必须选时间范围，且有跨度上限——给几个常用档位，
                    比让用户手点日历快得多。超出上限的档位直接禁用。 */}
                {TIME_RANGE_PRESETS.map((preset) => {
                  const disabled = preset.days > maxRangeDays
                  return (
                    <Button
                      key={preset.days}
                      type='button'
                      size='sm'
                      variant={
                        activePresetDays === preset.days ? 'secondary' : 'ghost'
                      }
                      disabled={disabled}
                      onClick={() => applyPreset(preset.days)}
                    >
                      {t(preset.label)}
                    </Button>
                  )
                })}
              </div>
              {rangeReady ? (
                <p
                  className={
                    rangeTooLong
                      ? 'text-destructive text-xs'
                      : 'text-muted-foreground text-xs'
                  }
                >
                  {rangeTooLong
                    ? t('Please select a range within {{count}} days.', {
                        count: maxRangeDays,
                      })
                    : t('Selected {{days}} days · max {{max}} days', {
                        days: selectedDays,
                        max: maxRangeDays,
                      })}
                </p>
              ) : (
                <p className='text-muted-foreground text-xs'>
                  {t('A time range is required for every export.')}
                </p>
              )}
            </div>
            <div className='grid gap-3 sm:grid-cols-2'>
              <div className='space-y-1'>
                <Label>{t('Type')}</Label>
                <Select
                  value={logType}
                  onValueChange={(v) => setLogType(v ?? LOG_TYPE_ALL_VALUE)}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    {LOG_TYPE_FILTERS.map((item) => (
                      <SelectItem key={item.value} value={item.value}>
                        {t(item.label)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className='space-y-1'>
                <Label>{t('Model')}</Label>
                <Input value={model} onChange={(e) => setModel(e.target.value)} />
              </div>
              <div className='space-y-1'>
                <Label>{t('User')}</Label>
                <Input
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                />
              </div>
              <div className='space-y-1'>
                <Label>{t('Token')}</Label>
                <Input value={token} onChange={(e) => setToken(e.target.value)} />
              </div>
              <div className='space-y-1'>
                <Label>{t('Channel')}</Label>
                <Input
                  value={channel}
                  inputMode='numeric'
                  onChange={(e) => setChannel(e.target.value)}
                />
              </div>
              <div className='space-y-1'>
                <Label>{t('Group')}</Label>
                <Input value={group} onChange={(e) => setGroup(e.target.value)} />
              </div>
            </div>
          </section>

          {/* 2. Columns */}
          <section className='space-y-3'>
            <div className='flex flex-wrap items-end justify-between gap-2'>
              <h3 className='text-sm font-semibold'>{t('Columns')}</h3>
              <div className='w-64 space-y-1'>
                <Label className='text-xs'>{t('Template')}</Label>
                <Select
                  value={templateId}
                  onValueChange={(v) => applyTemplate(v ?? "")}
                >
                  <SelectTrigger>
                    <SelectValue placeholder={t('Template')} />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    {templateOptions.map((item) => (
                      <SelectItem key={item.value} value={item.value}>
                        {item.label}
                      </SelectItem>
                    ))}
                    {templateId === CUSTOM_TEMPLATE && (
                      <SelectItem value={CUSTOM_TEMPLATE}>
                        {t('Custom')}
                      </SelectItem>
                    )}
                  </SelectContent>
                </Select>
              </div>
            </div>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Exported files always contain the real values — the sensitive-data toggle on the list page does not apply here.'
              )}
            </p>
            <ColumnPicker
              columns={catalog?.columns ?? []}
              selected={columns}
              maxColumns={catalog?.max_columns ?? 100}
              onChange={(next) => {
                setColumns(next)
                setTemplateId(CUSTOM_TEMPLATE)
              }}
            />
            <div className='flex items-end gap-2'>
              <div className='flex-1 space-y-1'>
                <Label className='text-xs'>{t('Save as template')}</Label>
                <Input
                  value={templateName}
                  placeholder={t('Template name')}
                  onChange={(e) => setTemplateName(e.target.value)}
                />
              </div>
              <Button
                type='button'
                variant='outline'
                disabled={
                  !templateName.trim() ||
                  columns.length === 0 ||
                  saveTemplate.isPending
                }
                onClick={() => saveTemplate.mutate()}
              >
                {saveTemplate.isPending ? (
                  <Loader2 className='animate-spin' />
                ) : (
                  <Save />
                )}
                {t('Save')}
              </Button>
            </div>
          </section>

          {/* 3. Format */}
          <section className='space-y-3'>
            <h3 className='text-sm font-semibold'>{t('Format')}</h3>
            <div className='grid gap-3 sm:grid-cols-2'>
              <div className='space-y-1'>
                <Label>{t('File format')}</Label>
                <Select
                  value={format}
                  onValueChange={(v) => setFormat((v ?? "csv_gz") as ExportFormat)}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectItem value='csv_gz'>
                      {t('CSV (gzip, recommended)')}
                    </SelectItem>
                    <SelectItem value='xlsx' disabled={xlsxTooLarge}>
                      {t('Excel (.xlsx)')}
                    </SelectItem>
                  </SelectContent>
                </Select>
                {xlsxTooLarge && (
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Excel is unavailable above {{count}} rows; this export will use CSV.',
                      { count: xlsxLimit }
                    )}
                  </p>
                )}
              </div>
              <div className='space-y-1'>
                <Label>{t('Timezone')}</Label>
                <Input
                  value={options.timezone}
                  onChange={(e) =>
                    setOptions({ ...options, timezone: e.target.value })
                  }
                />
              </div>
            </div>
            <div className='flex flex-wrap gap-6'>
              <label className='flex items-center gap-2 text-sm'>
                <Switch
                  checked={options.header}
                  onCheckedChange={(v) => setOptions({ ...options, header: v })}
                />
                {t('Include header row')}
              </label>
              <label className='flex items-center gap-2 text-sm'>
                <Switch
                  checked={options.csv_bom}
                  onCheckedChange={(v) => setOptions({ ...options, csv_bom: v })}
                />
                {t('Add BOM (Excel reads UTF-8 correctly)')}
              </label>
            </div>
          </section>
        </div>

        <SheetFooter className='flex-row items-center justify-end gap-2'>
          {estRows > 0 && (
            <span className='text-muted-foreground mr-auto text-xs'>
              {estCapped
                ? t('Over {{rows}} rows · {{parts}} part(s) or more', {
                    rows: estRows.toLocaleString(),
                    parts: estParts,
                  })
                : t('About {{rows}} rows · {{parts}} part(s)', {
                    rows: estRows.toLocaleString(),
                    parts: estParts,
                  })}
            </span>
          )}
          <Button
            type='button'
            variant='outline'
            onClick={() => onOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='button'
            disabled={!canSubmit}
            onClick={() => createJob.mutate()}
          >
            {createJob.isPending && <Loader2 className='animate-spin' />}
            {t('Start export')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

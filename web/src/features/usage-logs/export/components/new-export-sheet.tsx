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
import type {
  AnomalyFilters,
  ExportFormat,
  ExportMode,
  ExportOptions,
  ExportTemplatePurpose,
  NumericFilters,
  SummaryDimension,
} from '../types'

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
  /** Anomaly conditions carried in by "find similar" on a usage-log row. */
  anomaly?: AnomalyFilters
  /** Template to preselect; "find similar" points it at the anomaly template. */
  templateId?: string
}

interface NewExportSheetProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  prefill?: NewExportPrefill
}

/**
 * Anomaly kind → user-facing copy. The kinds themselves come from the server;
 * this map only translates them. An unknown kind falls back to its raw key,
 * so a newly added server-side kind still shows up and stays usable.
 */
const ANOMALY_LABELS: Record<string, string> = {
  estimated_usage: 'Billed from estimate',
  mixed_usage: 'Partly estimated',
  no_usage: 'Not billed',
  stream_error: 'Stream ended abnormally',
  settlement_unsettled: 'Settlement incomplete',
  retried: 'Retried across channels',
  quota_saturated: 'Quota overflowed and was capped',
}

/** Number input that keeps "empty" distinct from 0 — 0 is a meaningful filter. */
function NumberField({
  label,
  value,
  onChange,
  hint,
}: {
  label: string
  value?: number | null
  onChange: (value: number | null) => void
  /** Shown under the input, e.g. the USD equivalent of a quota amount. */
  hint?: string
}) {
  return (
    <div className='space-y-1'>
      <Label className='text-xs'>{label}</Label>
      <Input
        type='number'
        min={0}
        value={value ?? ''}
        onChange={(e) =>
          onChange(e.target.value === '' ? null : Number(e.target.value))
        }
      />
      {hint && <p className='text-muted-foreground text-xs'>{hint}</p>}
    </div>
  )
}

/** Trims trailing zeros so $0.000002 does not render as $0.00000200. */
function formatUSD(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '0'
  return value.toFixed(8).replace(/0+$/, '').replace(/\.$/, '')
}

const SUMMARY_DIM_LABELS: Record<string, string> = {
  date: 'Date',
  username: 'User',
  group: 'Group',
  model_name: 'Model',
  token_name: 'Token',
  channel: 'Channel',
}

const TEMPLATE_PURPOSE_LABELS: Record<ExportTemplatePurpose, string> = {
  reconciliation: 'Reconciliation',
  analytics: 'Analytics',
  diagnostic: 'Investigation',
  audit: 'Audit',
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
  // 异常与数值条件。异常条件没有索引可走、要逐行判定，所以它们的存在会让
  // 行数估算退化成「上限」而不是预测值（见下方 hasRowFilter）。
  const [anomaly, setAnomaly] = useState<AnomalyFilters>({})
  const [numeric, setNumeric] = useState<NumericFilters>({})
  // 明细 vs 聚合汇总。两者共用全部筛选条件，只是产出形态不同：
  // 明细一行一条日志，汇总按维度归并。
  const [mode, setMode] = useState<ExportMode>('detail')
  const [summaryDims, setSummaryDims] = useState<SummaryDimension[]>(['date'])
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
    setAnomaly(prefill?.anomaly ?? {})
    setNumeric({})
  }, [open, prefill])

  // Default to the "as displayed" template, unless the caller asked for another
  // one ("find similar" points at the anomaly template).
  useEffect(() => {
    if (!catalog || templateId) return
    const preset =
      (prefill?.templateId &&
        catalog.builtin_templates.find((tpl) => tpl.id === prefill.templateId)) ||
      catalog.builtin_templates.find((tpl) => tpl.is_default) ||
      catalog.builtin_templates[0]
    if (preset) {
      setTemplateId(preset.id)
      setColumns(preset.columns)
    }
  }, [catalog, templateId, prefill?.templateId])

  // 模板按用途排序后再平铺：九个模板平铺成一长条时，找「客户对账单」得逐条读，
  // 按「对账 / 统计 / 排查 / 审计」归拢后是扫一眼的事。
  const templateOptions = useMemo(() => {
    const purposeOrder: ExportTemplatePurpose[] = [
      'reconciliation',
      'analytics',
      'diagnostic',
      'audit',
    ]
    const builtin = [...(catalog?.builtin_templates ?? [])]
      .sort(
        (a, b) =>
          purposeOrder.indexOf(a.purpose) - purposeOrder.indexOf(b.purpose)
      )
      .map((tpl) => ({
        value: tpl.id,
        label:
          tpl.audience === 'customer'
            ? `${t(tpl.name)} · ${t('Safe to send to customers')}`
            : `${t(TEMPLATE_PURPOSE_LABELS[tpl.purpose])} · ${t(tpl.name)}`,
      }))
    const custom = (templates ?? []).map((tpl) => ({
      value: String(tpl.id),
      label: tpl.is_shared ? `${tpl.name} (${t('Shared')})` : tpl.name,
    }))
    return [...builtin, ...custom]
  }, [catalog, templates, t])

  const selectedBuiltin = catalog?.builtin_templates.find(
    (tpl) => tpl.id === templateId
  )

  // 触发器要显示译名，就得让 items 覆盖所有可能的 value —— 包括改动列后
  // 切到的「自定义」这个伪选项，漏掉它时触发器会退回显示原始值。
  const templateSelectItems = useMemo(
    () => [...templateOptions, { value: CUSTOM_TEMPLATE, label: t('Custom') }],
    [templateOptions, t]
  )

  const logTypeItems = useMemo(
    () =>
      LOG_TYPE_FILTERS.map((item) => ({
        value: item.value as string,
        label: t(item.label),
      })),
    [t]
  )

  const formatItems = useMemo(
    () => [
      { value: 'csv_gz', label: t('CSV (gzip, recommended)') },
      { value: 'xlsx', label: t('Excel (.xlsx)') },
    ],
    [t]
  )

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

  // 异常条件在扫描管线里逐行判定，无法进入这次有界计数，所以带上它们时
  // 估算得到的是「筛选前的上限」。文案要跟着变，否则管理员会以为估出来的
  // 就是最终行数。
  const hasRowFilter = useMemo(
    () =>
      Boolean(
        anomaly.anomaly_only ||
          anomaly.anomaly_kinds?.length ||
          anomaly.usage_source?.length ||
          anomaly.stream_end_reason?.length ||
          anomaly.settlement_state?.length ||
          anomaly.min_retry_count != null
      ),
    [anomaly]
  )

  // 额度→美元换算率由后端下发，前端不硬编码——它是可配置的系统参数。
  const quotaPerUnit = catalog?.quota_per_unit && catalog.quota_per_unit > 0
    ? catalog.quota_per_unit
    : 500000
  const quotaUnitUSD = formatUSD(1 / quotaPerUnit)

  const hasNumericFilter = useMemo(
    () => Object.values(numeric).some((v) => v != null),
    [numeric]
  )

  // 数值条件跟着输入框逐字变化，和文本筛选一样要防抖，否则每敲一位都查一次库。
  const debouncedNumeric = useDebounce(numeric, 500)

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
      hasRowFilter,
      // 数值条件同样影响估算结果；不进 queryKey 的话改了条件也不会重查。
      debouncedNumeric,
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
        has_row_filter: hasRowFilter || undefined,
        ...debouncedNumeric,
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
  // 聚合模式下估算的是扫描量，不是产出行数——扫 100 万行可能只出 50 行汇总，
  // 拿扫描量否决 xlsx 会把最该用 Excel 的场景挡在门外。
  const xlsxTooLarge =
    mode !== 'summary' && (estCapped || estRows > xlsxLimit)
  const estParts = estRows > 0 ? Math.max(1, Math.ceil(estRows / rowsPerPart)) : 0
  useEffect(() => {
    if (xlsxTooLarge && format === 'xlsx') setFormat('csv_gz')
  }, [xlsxTooLarge, format])

  // 带异常条件时估算只覆盖能下推 SQL 的部分，得到的是筛选前的上限。
  // 照旧说「约 N 行」会让人以为那就是最终行数，实际可能只导出几百行。
  const estimateCopy = useMemo(() => {
    const rows = estRows.toLocaleString()
    if (mode === 'summary') {
      // 汇总模式说「N 行 / M 个分片」会被当成产出规模，实际产出只有几十行。
      return t('Scans about {{rows}} rows', { rows })
    }
    if (estimate?.upper_bound) {
      return t('At most {{rows}} rows before anomaly filtering', { rows })
    }
    if (estCapped) {
      return t('Over {{rows}} rows · {{parts}} part(s) or more', {
        rows,
        parts: estParts,
      })
    }
    return t('About {{rows}} rows · {{parts}} part(s)', { rows, parts: estParts })
  }, [mode, estimate?.upper_bound, estCapped, estRows, estParts, t])

  const createJob = useMutation({
    mutationFn: async () => {
      if (!range.start || !range.end) {
        throw new Error(t('Please select the time range to export'))
      }
      if (mode !== 'detail' && summaryDims.length === 0) {
        throw new Error(t('Please select at least one dimension to group by'))
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
        ...anomaly,
        ...numeric,
        mode,
        // 明细+汇总同样要带维度。这里若只判 'summary'，both 模式就会带着空维度
        // 提交，前端校验明明通过、后端却回「请至少选择一个聚合维度」。
        summary_dims: mode !== 'detail' ? summaryDims : undefined,
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
                {/* items 是触发器上显示译名的依据：Base UI 的 Select.Value 靠它
                    把当前 value 映射成 label，不传就只会显示原始值。 */}
                <Select
                  items={logTypeItems}
                  value={logType}
                  onValueChange={(v) => setLogType(v ?? LOG_TYPE_ALL_VALUE)}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    {logTypeItems.map((item) => (
                      <SelectItem key={item.value} value={item.value}>
                        {item.label}
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

            {/* Anomaly & numeric filters — collapsed by default so the common
                case (just pick a range) stays a two-field form. */}
            <details className='rounded-md border px-3 py-2'>
              <summary className='cursor-pointer text-sm font-medium'>
                {t('Troubleshooting filters')}
                {(hasRowFilter || hasNumericFilter) && (
                  <Badge variant='secondary' className='ml-2'>
                    {t('Active')}
                  </Badge>
                )}
              </summary>

              <div className='mt-3 space-y-3'>
                <div className='space-y-1.5'>
                  <Label className='text-xs'>{t('Anomalies')}</Label>
                  <div className='flex flex-wrap gap-1.5'>
                    <Button
                      type='button'
                      size='sm'
                      variant={anomaly.anomaly_only ? 'default' : 'outline'}
                      onClick={() =>
                        setAnomaly((prev) => ({
                          ...prev,
                          anomaly_only: !prev.anomaly_only,
                          // 「任意异常」与具体种类是互斥的表达，同时勾会让人
                          // 以为是并集，实际后端按 AND 取交集。
                          anomaly_kinds: undefined,
                        }))
                      }
                    >
                      {t('Any anomaly')}
                    </Button>
                    {(catalog?.anomaly_kinds ?? []).map((kind) => {
                      const active = anomaly.anomaly_kinds?.includes(kind)
                      return (
                        <Button
                          key={kind}
                          type='button'
                          size='sm'
                          variant={active ? 'default' : 'outline'}
                          onClick={() =>
                            setAnomaly((prev) => {
                              const current = prev.anomaly_kinds ?? []
                              const next = active
                                ? current.filter((k) => k !== kind)
                                : [...current, kind]
                              return {
                                ...prev,
                                anomaly_only: false,
                                anomaly_kinds: next.length ? next : undefined,
                              }
                            })
                          }
                        >
                          {t(ANOMALY_LABELS[kind] ?? kind)}
                        </Button>
                      )
                    })}
                  </div>
                </div>

                {/* 计费与否是独立条件，必须自己有可见控件。
                    只靠快捷按钮的高亮来表达，用户一滚动就看不出这条筛选还开着。 */}
                <div className='space-y-1.5'>
                  <Label className='text-xs'>{t('Charged')}</Label>
                  <div className='flex flex-wrap gap-1.5'>
                    {(
                      [
                        [null, 'Any'],
                        [true, 'Charged (cost > 0)'],
                        [false, 'Free (cost = 0)'],
                      ] as const
                    ).map(([value, label]) => (
                      <Button
                        key={label}
                        type='button'
                        size='sm'
                        variant={
                          (numeric.charged ?? null) === value
                            ? 'default'
                            : 'outline'
                        }
                        onClick={() =>
                          setNumeric((prev) => ({ ...prev, charged: value }))
                        }
                      >
                        {t(label)}
                      </Button>
                    ))}
                  </div>
                </div>

                {/* 常见问法的快捷入口。它只是把下面两个数值条件填好，
                    刻意**不**做成异常标记：embedding 天生没有输出 token，
                    按次/按张计费的请求同样如此，一刀切会把大量正常账单标成异常。 */}
                <div className='space-y-1.5'>
                  <Label className='text-xs'>{t('Quick filters')}</Label>
                  <Button
                    type='button'
                    size='sm'
                    variant={
                      numeric.charged && numeric.completion_tokens_max === 0
                        ? 'default'
                        : 'outline'
                    }
                    onClick={() =>
                      setNumeric((prev) =>
                        prev.charged && prev.completion_tokens_max === 0
                          ? { ...prev, charged: null, completion_tokens_max: null }
                          : { ...prev, charged: true, completion_tokens_max: 0 }
                      )
                    }
                  >
                    {t('Charged with zero output')}
                  </Button>
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Matches rows with no output tokens that still cost money. Note that embeddings and per-call pricing legitimately produce no output tokens.'
                    )}
                  </p>
                </div>

                <div className='grid gap-3 sm:grid-cols-2'>
                  {/* 单位必须写死在标签里。表格「花费」一栏是美元，而这里过滤的是
                      整数额度列，两者相差 quota_per_unit 倍（本站 50 万）；
                      只写「最低费用」会让人按美元填，差出五个数量级。 */}
                  <NumberField
                    label={t('Min quota units ({{usd}} each)', {
                      usd: quotaUnitUSD,
                    })}
                    value={numeric.quota_min}
                    onChange={(v) =>
                      setNumeric((prev) => ({ ...prev, quota_min: v }))
                    }
                    hint={
                      numeric.quota_min != null && numeric.quota_min > 0
                        ? t('≈ ${{usd}}', {
                            usd: formatUSD(numeric.quota_min / quotaPerUnit),
                          })
                        : undefined
                    }
                  />
                  <NumberField
                    label={t('Min output tokens')}
                    value={numeric.completion_tokens_min}
                    onChange={(v) =>
                      setNumeric((prev) => ({
                        ...prev,
                        completion_tokens_min: v,
                      }))
                    }
                  />
                  <NumberField
                    label={t('Max output tokens')}
                    value={numeric.completion_tokens_max}
                    onChange={(v) =>
                      setNumeric((prev) => ({
                        ...prev,
                        completion_tokens_max: v,
                      }))
                    }
                  />
                  <NumberField
                    label={t('Min duration (s)')}
                    value={numeric.use_time_min}
                    onChange={(v) =>
                      setNumeric((prev) => ({ ...prev, use_time_min: v }))
                    }
                  />
                  <NumberField
                    label={t('Min retries')}
                    value={anomaly.min_retry_count}
                    onChange={(v) =>
                      setAnomaly((prev) => ({ ...prev, min_retry_count: v }))
                    }
                  />
                </div>

                {hasRowFilter && (
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Anomaly filters have no index to use, so the export still scans the whole time range — it takes as long as an unfiltered export, but the file is much smaller.'
                    )}
                  </p>
                )}
              </div>
            </details>

            {/* 产出形态：明细一行一条日志，汇总按维度归并。放在筛选区末尾，
                因为两者共用上面的全部筛选条件。 */}
            <div className='space-y-1.5'>
              <Label className='text-xs'>{t('Output')}</Label>
              <div className='flex flex-wrap gap-1.5'>
                <Button
                  type='button'
                  size='sm'
                  variant={mode === 'detail' ? 'default' : 'outline'}
                  onClick={() => setMode('detail')}
                >
                  {t('One row per log')}
                </Button>
                <Button
                  type='button'
                  size='sm'
                  variant={mode === 'summary' ? 'default' : 'outline'}
                  onClick={() => setMode('summary')}
                >
                  {t('Aggregated summary')}
                </Button>
                <Button
                  type='button'
                  size='sm'
                  variant={mode === 'both' ? 'default' : 'outline'}
                  onClick={() => setMode('both')}
                >
                  {t('Detail + summary')}
                </Button>
              </div>
              {mode === 'both' && (
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Both files come from a single scan and arrive in one archive, so it costs no more than exporting the detail alone.'
                  )}
                </p>
              )}
            </div>
          </section>

          {/* 2. 聚合维度（汇总 / 明细+汇总）与 导出列（明细 / 明细+汇总）*/}
          {mode !== 'detail' && (
            <section className='space-y-3'>
              <h3 className='text-sm font-semibold'>{t('Group by')}</h3>
              <div className='flex flex-wrap gap-1.5'>
                {(catalog?.summary_dimensions ?? []).map((dim) => {
                  const active = summaryDims.includes(dim)
                  return (
                    <Button
                      key={dim}
                      type='button'
                      size='sm'
                      variant={active ? 'default' : 'outline'}
                      onClick={() =>
                        setSummaryDims((prev) =>
                          active
                            ? prev.filter((d) => d !== dim)
                            : [...prev, dim]
                        )
                      }
                    >
                      {t(SUMMARY_DIM_LABELS[dim] ?? dim)}
                    </Button>
                  )
                })}
              </div>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Each extra dimension multiplies the number of result rows. Time granularity is by day.'
                )}
              </p>
            </section>
          )}

          {mode !== 'summary' && (
          <section className='space-y-3'>
            <div className='flex flex-wrap items-end justify-between gap-2'>
              <h3 className='flex items-center gap-2 text-sm font-semibold'>
                {t('Columns')}
                {/* 「可发给客户」是这份文件能不能直接转发出去的唯一判据，
                    放在列区标题旁边，而不是藏在下拉项里。 */}
                {selectedBuiltin?.audience === 'customer' && (
                  <Badge variant='secondary'>
                    {t('Safe to send to customers')}
                  </Badge>
                )}
              </h3>
              <div className='w-64 space-y-1'>
                <Label className='text-xs'>{t('Template')}</Label>
                <Select
                  items={templateSelectItems}
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
          )}

          {/* 3. Format */}
          <section className='space-y-3'>
            <h3 className='text-sm font-semibold'>{t('Format')}</h3>
            <div className='grid gap-3 sm:grid-cols-2'>
              <div className='space-y-1'>
                <Label>{t('File format')}</Label>
                <Select
                  items={formatItems}
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
              {estimateCopy}
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

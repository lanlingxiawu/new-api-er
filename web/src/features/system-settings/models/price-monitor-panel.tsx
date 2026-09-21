/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import type { TFunction } from 'i18next'
import {
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Copy,
  ExternalLink,
  Play,
  Save,
  Search,
  Settings2,
  Share2,
} from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  Combobox,
  ComboboxCollection,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxGroup,
  ComboboxInput,
  ComboboxItem,
  ComboboxList,
} from '@/components/ui/combobox'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from '@/components/ui/input-group'
import { Label } from '@/components/ui/label'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { cn } from '@/lib/utils'

import {
  applyPriceMonitorPrice,
  getPriceMonitorResults,
  getPriceMonitorStatus,
  getUpstreamChannels,
  runPriceMonitor,
  updatePriceMonitorSettings,
} from '../api'
import { useSettingsSaveConfirmation } from '../components/settings-save-confirmation'
import type {
  PriceMonitorApplyPriceItem,
  PriceMonitorApplyPriceResponse,
  PriceMonitorFloorViolation,
  PriceMonitorLossKind,
  PriceMonitorMatrixItem,
  PriceMonitorPriceCell,
  PriceMonitorPriceLane,
  PriceMonitorPriceTier,
  PriceMonitorSourceHeader,
  PriceMonitorStatusResponse,
  UpstreamChannel,
} from '../types'
import { ChannelSelectorDialog } from './channel-selector-dialog'
import { OPENROUTER_CHANNEL_TYPE } from './constants'
import {
  PriceMonitorApplyDialog,
  type PriceMonitorRepairTarget,
} from './price-monitor-apply-dialog'

const PAGE_SIZE = 20
const PLATFORM_KEY = 'platform'
const MEASURED_LOSS: PriceMonitorLossKind = 'measured'
const NO_MODELS: ReadonlySet<string> = new Set()

/**
 * 只有实测成本风险能靠改平台价解决。配置成本风险来自 cost = revenue / g * r：
 * 成本正比于收入，平台价是 profit = P*(g-r) 的公因子，改价不改变毛利率，
 * 只会等比放大绝对亏损额——所以这类行不给改价按钮。
 */
function hasMeasuredLoss(
  item: PriceMonitorMatrixItem,
  headers: PriceMonitorSourceHeader[]
) {
  return headers.some(
    (header) =>
      header.type === 'channel' &&
      (item.prices[header.key]?.loss_kinds ?? []).includes(MEASURED_LOSS)
  )
}

/** 阶梯表达式（或动态价格）无法在这里改价，服务端也会拒绝。 */
function isTieredPlatformPrice(platform: PriceMonitorPriceCell | undefined) {
  return platform?.mode === 'tiered_expr' || Boolean(platform?.dynamic)
}

/**
 * 保本价是整行的联合下限 repair_floor.fields（后端按所有可比渠道算出），
 * 与具体哪个渠道命中无关。
 */
function buildRepairTarget(
  item: PriceMonitorMatrixItem,
  headers: PriceMonitorSourceHeader[]
): PriceMonitorRepairTarget | null {
  const platform = item.prices[PLATFORM_KEY]
  const fields = item.repair_floor?.fields
  if (!item.repair_floor || !fields || Object.keys(fields).length === 0) {
    return null
  }
  if (platform?.mode !== 'per_token' && platform?.mode !== 'per_request') {
    return null
  }
  if (platform.dynamic || !hasMeasuredLoss(item, headers)) return null
  return {
    model: item.model,
    platform,
    floor: { ...item.repair_floor, fields },
  }
}

const primaryComparisonFilters = [
  ['all', 'All differences'],
  ['channel_official', 'Channel vs official'],
  ['channel_models_dev', 'Channel vs models.dev'],
  ['channel_platform', 'Channel vs platform'],
  ['platform_official', 'Platform vs official'],
  ['platform_models_dev', 'Platform vs models.dev'],
  ['above_platform', 'Priced above platform'],
  ['loss_risk', 'Loss risk'],
] as const

const additionalComparisonFilters = [
  ['input', 'Input price differs'],
  ['output', 'Output price differs'],
  ['cache', 'Cache price differs'],
  ['billing', 'Billing differs'],
  ['official_missing', 'Official model missing'],
  ['models_dev_missing', 'models.dev model missing'],
  ['channel_missing', 'Channel price missing'],
  ['source_failed', 'Source check failed'],
] as const

type ComparisonFilter =
  | (typeof primaryComparisonFilters)[number][0]
  | (typeof additionalComparisonFilters)[number][0]

const modelsDevComparisonFilters = new Set<ComparisonFilter>([
  'channel_models_dev',
  'platform_models_dev',
  'models_dev_missing',
])

function SourceColumnPicker({
  headers,
  value,
  onChange,
  t,
}: {
  headers: PriceMonitorSourceHeader[]
  value: string[] | null
  onChange: (value: string[] | null) => void
  t: TFunction
}) {
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const [draftValue, setDraftValue] = useState<string[] | null>(value)
  const channels = useMemo(
    () => headers.filter((header) => header.type === 'channel'),
    [headers]
  )
  const selectedCount = value === null ? channels.length : value.length
  const draftKeys =
    draftValue === null ? channels.map((header) => header.key) : draftValue
  const draftSelectedCount = draftKeys.length
  const normalizedSearch = search.trim().toLowerCase()
  const filteredChannels = normalizedSearch
    ? channels.filter(
        (header) =>
          header.name.toLowerCase().includes(normalizedSearch) ||
          header.api_url?.toLowerCase().includes(normalizedSearch)
      )
    : channels
  const label =
    value === null
      ? t('All channels')
      : t('{{count}} channels selected', { count: selectedCount })

  const handleOpenChange = (nextOpen: boolean) => {
    setOpen(nextOpen)
    setSearch('')
    setDraftValue(value)
  }

  return (
    <Popover open={open} onOpenChange={handleOpenChange}>
      <PopoverTrigger
        render={
          <Button
            variant='outline'
            className='w-full justify-between font-normal'
          >
            <span className='truncate'>{label}</span>
            <ChevronDown className='size-4 shrink-0 opacity-60' />
          </Button>
        }
      />
      <PopoverContent align='start' className='w-96 max-w-[90vw] gap-0 p-0'>
        <div className='p-3'>
          <div className='relative'>
            <Search className='text-muted-foreground pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2' />
            <Input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder={t('Search channels')}
              className='pl-9'
              autoFocus
            />
          </div>
          <div className='mt-2 flex gap-1'>
            <Button
              size='sm'
              variant='ghost'
              onClick={() => setDraftValue(null)}
            >
              {t('All channels')}
            </Button>
            <Button size='sm' variant='ghost' onClick={() => setDraftValue([])}>
              {t('Clear channels')}
            </Button>
          </div>
        </div>
        <Separator />
        <div className='grid max-h-72 gap-1.5 overflow-y-auto overscroll-contain p-2'>
          {filteredChannels.map((header) => {
            const checked = draftKeys.includes(header.key)
            return (
              <label
                key={header.key}
                className={cn(
                  'hover:bg-muted/60 focus-within:border-ring focus-within:ring-ring/30 flex cursor-pointer items-start gap-3 rounded-md border border-transparent px-3 py-2.5 transition-colors focus-within:ring-2',
                  checked &&
                    'border-primary/20 bg-primary/5 hover:bg-primary/10'
                )}
              >
                <Checkbox
                  checked={checked}
                  onCheckedChange={() => {
                    if (draftValue === null) {
                      setDraftValue(
                        channels
                          .filter((channel) => channel.key !== header.key)
                          .map((channel) => channel.key)
                      )
                      return
                    }
                    setDraftValue(
                      checked
                        ? draftValue.filter((key) => key !== header.key)
                        : [...draftValue, header.key]
                    )
                  }}
                />
                <span className='min-w-0 flex-1'>
                  <span className='block truncate text-sm font-medium'>
                    {header.name}
                  </span>
                  <span className='text-muted-foreground block text-xs break-all'>
                    {header.api_url}
                  </span>
                </span>
              </label>
            )
          })}
          {filteredChannels.length === 0 && (
            <p className='text-muted-foreground px-3 py-8 text-center text-sm'>
              {t('No matching channels')}
            </p>
          )}
        </div>
        <Separator />
        <div className='flex items-center justify-between gap-3 p-3'>
          <Badge variant='secondary'>
            {t('Selected {{count}}', { count: draftSelectedCount })}
          </Badge>
          <Button
            size='sm'
            onClick={() => {
              onChange(draftValue)
              setOpen(false)
            }}
          >
            {t('Apply')}
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  )
}

function ModelFilterCombobox({
  models,
  value,
  onValueChange,
  onSubmit,
  t,
}: {
  models: string[]
  value: string
  onValueChange: (value: string) => void
  onSubmit: (value: string) => void
  t: TFunction
}) {
  const selectedValue = models.includes(value) ? value : null

  return (
    <Combobox
      items={models}
      value={selectedValue}
      inputValue={value}
      onInputValueChange={(nextValue) => onValueChange(nextValue)}
      onValueChange={(nextValue) => {
        if (!nextValue) return
        onValueChange(nextValue)
        onSubmit(nextValue)
      }}
      filter={(model, query) =>
        model.toLowerCase().includes(query.trim().toLowerCase())
      }
      autoHighlight
    >
      <ComboboxInput
        id='price-monitor-model-filter'
        className='w-full'
        placeholder={t('Search or select a model')}
        showClear
        onKeyDown={(event) => {
          if (event.key !== 'Enter' || selectedValue || !value.trim()) return
          event.preventDefault()
          onSubmit(value.trim())
        }}
      />
      <ComboboxContent>
        <ComboboxList>
          <ComboboxGroup>
            <ComboboxCollection>
              {(model: string) => (
                <ComboboxItem key={model} value={model} className='py-2.5'>
                  <span className='truncate font-medium'>{model}</span>
                </ComboboxItem>
              )}
            </ComboboxCollection>
          </ComboboxGroup>
        </ComboboxList>
        <ComboboxEmpty>{t('No matching models')}</ComboboxEmpty>
      </ComboboxContent>
    </Combobox>
  )
}

function sourceStickyClass(
  type: PriceMonitorSourceHeader['type'],
  fixedIndex: number | undefined,
  layer: 'header' | 'body'
) {
  if (fixedIndex === undefined) return ''
  const zIndex = layer === 'header' ? 'lg:z-40' : 'lg:z-20'
  let left = 'lg:left-[46rem]'
  if (fixedIndex === 0) {
    left = 'lg:left-56'
  } else if (fixedIndex === 1) {
    left = 'lg:left-[30rem]'
  }
  return cn(
    'lg:sticky',
    left,
    zIndex,
    (type === 'official' || type === 'models_dev') &&
      'border-r-2 lg:shadow-[4px_0_6px_-4px_var(--border)]'
  )
}

type PriceMonitorForm = {
  enabled: boolean
  intervalMinutes: number
  timeoutSeconds: number
  includeModelsDev: boolean
  modelWhitelist: string
  /** 渠道 ID -> 价格接口。配了就只用它，不配的渠道由巡检自动探测。 */
  customEndpoints: Record<string, string>
}

type PriceMonitorPanelProps = {
  canEdit: boolean
  /** 改价按钮的权限：后端 apply_price 要的是 billing.model-pricing 编辑权，
      与巡检页自身的编辑权不同，必须分开传。 */
  canRepairPricing: boolean
}

const DEFAULT_FORM: PriceMonitorForm = {
  enabled: false,
  intervalMinutes: 360,
  timeoutSeconds: 10,
  includeModelsDev: false,
  modelWhitelist: '',
  customEndpoints: {},
}

function priceMonitorFormFromConfig(
  config: PriceMonitorStatusResponse['data']['config']
): PriceMonitorForm {
  return {
    enabled: config.enabled,
    intervalMinutes: config.interval_minutes,
    timeoutSeconds: config.timeout_seconds,
    includeModelsDev: config.include_models_dev,
    modelWhitelist: config.model_whitelist,
    customEndpoints: { ...config.custom_endpoints },
  }
}

function formatPrice(value?: number) {
  if (value === undefined) return '—'
  const maximumFractionDigits =
    Math.abs(value) > 0 && Math.abs(value) < 0.000001 ? 10 : 6
  return `$${value.toLocaleString(undefined, { maximumFractionDigits })}`
}

function PriceLine({
  label,
  value,
  different,
  missingText,
}: {
  label: string
  value?: number
  different?: boolean
  missingText?: string
}) {
  return (
    <div
      className={
        different
          ? 'bg-destructive/10 text-destructive flex items-center justify-between gap-4 rounded px-2 py-1'
          : 'flex items-center justify-between gap-4 px-2 py-1'
      }
    >
      <span className='text-muted-foreground text-xs'>{label}</span>
      <span className='font-semibold whitespace-nowrap'>
        {value === undefined ? (
          <span
            className={
              different
                ? 'text-destructive text-xs font-medium'
                : 'text-muted-foreground text-xs font-normal'
            }
          >
            {missingText ?? '—'}
          </span>
        ) : (
          formatPrice(value)
        )}
      </span>
    </div>
  )
}

function laneLabel(key: PriceMonitorPriceLane['key'], t: TFunction) {
  const labels: Record<string, string> = {
    cache_read: t('Cache read price'),
    cache_write: t('Cache write price'),
    cache_write_1h: t('Cache write price (1 hour)'),
    image_input: t('Image input price'),
    image_output: t('Image output price'),
    audio_input: t('Audio input price'),
    audio_output: t('Audio output price'),
  }
  return labels[key] ?? key
}

function tierLabel(tier: PriceMonitorPriceTier, t: TFunction) {
  if (
    !tier.condition_variable ||
    !tier.condition_operator ||
    tier.condition_value === undefined
  ) {
    return t('All input lengths')
  }
  const variable =
    tier.condition_variable === 'c' ? t('Output length') : t('Input length')
  return `${variable} ${tier.condition_operator} ${tier.condition_value.toLocaleString()} tokens`
}

function PriceCell({
  price,
  sourceType,
}: {
  price?: PriceMonitorPriceCell
  sourceType: PriceMonitorSourceHeader['type']
}) {
  const { t } = useTranslation()
  const lossKinds = price?.loss_kinds ?? []
  if (!price?.highest && lossKinds.length === 0) {
    return <PriceCellContent price={price} sourceType={sourceType} />
  }
  const formatFactor = (value?: number) =>
    value === undefined ? '—' : Number(value.toFixed(4)).toString()
  return (
    <div className='space-y-1.5'>
      {/*
        价格行必须排在最前面。同一模型行里各来源是并排的独立单元格，横向对比只有在
        「输入 / 输出 / 缓存读取」处于同一水平线时才成立。徽标与系数是对价格的标注，
        高度随命中情况变化（0 个徽标 ~ 2 个徽标 + 3 行系数），一旦放在价格上方，
        带徽标的单元格就会把自己的价格行整体下推，出现「左侧输入 ↔ 右侧徽标、
        左侧输出 ↔ 右侧输入」的错位——对比表就失去意义了。
        放到下面之后，各单元格的价格行都从顶部开始，天然平行。
      */}
      <PriceCellContent price={price} sourceType={sourceType} />
      <div className='flex flex-wrap gap-1'>
        {price?.highest && (
          <Badge variant='warning'>{t('Highest price')}</Badge>
        )}
        {lossKinds.includes('measured') && (
          <Badge variant='destructive'>{t('Measured loss')}</Badge>
        )}
        {lossKinds.includes('configured') && (
          <Badge variant='destructive'>{t('Configured loss')}</Badge>
        )}
      </div>
      {lossKinds.length > 0 && (
        <div className='text-muted-foreground space-y-0.5 text-xs'>
          <div className='flex justify-between gap-2'>
            <span>{t('Sell factor')}</span>
            <span>{formatFactor(price?.sell_factor)}</span>
          </div>
          <div className='flex justify-between gap-2'>
            <span>{t('Measured factor')}</span>
            <span>{formatFactor(price?.measured_factor)}</span>
          </div>
          <div className='flex justify-between gap-2'>
            <span>{t('Configured factor')}</span>
            <span>{formatFactor(price?.configured_factor)}</span>
          </div>
        </div>
      )}
    </div>
  )
}

function PriceCellContent({
  price,
  sourceType,
}: {
  price?: PriceMonitorPriceCell
  sourceType: PriceMonitorSourceHeader['type']
}) {
  const { t } = useTranslation()
  if (!price) {
    return (
      <span className='text-muted-foreground text-sm'>
        {sourceType === 'channel' ? t('Not enabled') : t('Not in source')}
      </span>
    )
  }
  if (price.unavailable_reason) {
    let missingMessage = t('Not in channel pricing')
    if (sourceType === 'official') {
      missingMessage = t('Not in official prices')
    } else if (sourceType === 'models_dev') {
      missingMessage = t('Not in models.dev')
    }
    const messages = {
      missing: missingMessage,
      placeholder: t('Placeholder price, not compared'),
      source_failed: t('Source check failed'),
    }
    return (
      <span
        className={
          price.different
            ? 'text-destructive text-sm font-medium'
            : 'text-muted-foreground text-sm'
        }
      >
        {messages[price.unavailable_reason]}
      </span>
    )
  }
  const lanes = (price.lanes ?? []).map((lane) => (
    <PriceLine
      key={lane.key}
      label={laneLabel(lane.key, t)}
      value={lane.price}
      different={lane.different}
    />
  ))
  if (price.mode === 'per_token') {
    return (
      <div className='space-y-0.5'>
        <PriceLine
          label={t('Input')}
          value={price.input}
          different={price.input_different}
        />
        <PriceLine
          label={t('Output')}
          value={price.output}
          different={price.output_different}
          missingText={t('Input price only')}
        />
        {lanes}
      </div>
    )
  }
  if (price.mode === 'per_request') {
    return (
      <PriceLine
        label={t('Fixed price')}
        value={price.price}
        different={price.price_different}
      />
    )
  }
  if (price.tiers?.length) {
    return (
      <div className='space-y-2'>
        {price.tiers.map((tier) => (
          <div
            key={`${tier.range}:${tier.condition_variable ?? ''}:${tier.condition_operator ?? ''}:${tier.condition_value ?? ''}`}
            className={
              price.mode_different
                ? 'bg-destructive/10 rounded p-2'
                : 'rounded border p-2'
            }
          >
            <p className='text-muted-foreground mb-1 text-xs'>
              {tierLabel(tier, t)}
            </p>
            <PriceLine
              label={t('Input')}
              value={tier.input}
              different={price.mode_different}
            />
            <PriceLine
              label={t('Output')}
              value={tier.output}
              different={price.mode_different}
            />
            {(tier.lanes ?? []).map((lane) => (
              <PriceLine
                key={lane.key}
                label={laneLabel(lane.key, t)}
                value={lane.price}
                different={price.mode_different}
              />
            ))}
          </div>
        ))}
      </div>
    )
  }
  return (
    <div
      className={
        price.mode_different
          ? 'bg-destructive/10 text-destructive rounded p-2'
          : 'text-muted-foreground p-2'
      }
    >
      <p className='font-medium'>{t('Dynamic rule pricing')}</p>
      <p className='mt-1 text-xs'>{t('Varies by request or time')}</p>
    </div>
  )
}

function PriceMonitorPagination({
  page,
  pageSize,
  total,
  isFetching,
  onPageChange,
}: {
  page: number
  pageSize: number
  total: number
  isFetching: boolean
  onPageChange: (page: number) => void
}) {
  const { t } = useTranslation()
  const totalPages = Math.max(1, Math.ceil(total / pageSize))
  const start = total > 0 ? (page - 1) * pageSize + 1 : 0
  const end = Math.min(page * pageSize, total)

  return (
    <>
      <span className='text-muted-foreground mr-auto text-sm whitespace-nowrap'>
        {total > 0
          ? `${start}-${end} / ${total}`
          : t('{{total}} items', { total })}
      </span>
      <div className='flex items-center gap-2'>
        <span className='text-muted-foreground hidden text-sm sm:inline'>
          {t('Page {{current}} of {{total}}', {
            current: Math.min(page, totalPages),
            total: totalPages,
          })}
        </span>
        <Button
          variant='outline'
          size='sm'
          disabled={isFetching || page <= 1}
          onClick={() => onPageChange(page - 1)}
        >
          <ChevronLeft data-icon='inline-start' className='size-4' />
          {t('Previous page')}
        </Button>
        <Button
          variant='outline'
          size='sm'
          disabled={isFetching || page >= totalPages}
          onClick={() => onPageChange(page + 1)}
        >
          {t('Next page')}
          <ChevronRight data-icon='inline-end' className='size-4' />
        </Button>
      </div>
    </>
  )
}

function PriceMonitorHorizontalScrollControls({
  containerRef,
  refreshKey,
}: {
  containerRef: { current: HTMLDivElement | null }
  refreshKey: string
}) {
  const { t } = useTranslation()
  const [state, setState] = useState({
    canScrollLeft: false,
    canScrollRight: false,
    progress: 0,
  })

  const getScrollElement = useCallback(
    () =>
      containerRef.current?.querySelector<HTMLElement>(
        '[data-slot="table-container"]'
      ) ?? null,
    [containerRef]
  )

  const syncScrollState = useCallback(() => {
    const scrollElement = getScrollElement()
    if (!scrollElement) {
      setState({ canScrollLeft: false, canScrollRight: false, progress: 0 })
      return
    }

    const maxScrollLeft = Math.max(
      0,
      scrollElement.scrollWidth - scrollElement.clientWidth
    )
    const scrollLeft = Math.max(0, scrollElement.scrollLeft)
    setState({
      canScrollLeft: scrollLeft > 1,
      canScrollRight: scrollLeft < maxScrollLeft - 1,
      progress:
        maxScrollLeft > 0
          ? Math.round(
              (Math.min(scrollLeft, maxScrollLeft) / maxScrollLeft) * 100
            )
          : 0,
    })
  }, [getScrollElement])

  useEffect(() => {
    const scrollElement = getScrollElement()
    syncScrollState()
    if (!scrollElement) return

    scrollElement.addEventListener('scroll', syncScrollState, { passive: true })
    window.addEventListener('resize', syncScrollState)
    const frame = window.requestAnimationFrame(syncScrollState)

    return () => {
      scrollElement.removeEventListener('scroll', syncScrollState)
      window.removeEventListener('resize', syncScrollState)
      window.cancelAnimationFrame(frame)
    }
  }, [getScrollElement, refreshKey, syncScrollState])

  const scrollTable = (direction: -1 | 1) => {
    const scrollElement = getScrollElement()
    if (!scrollElement) return

    scrollElement.scrollBy({
      left: direction * Math.max(360, scrollElement.clientWidth * 0.65),
      behavior: 'smooth',
    })
    window.requestAnimationFrame(syncScrollState)
  }

  return (
    <>
      <div aria-hidden='true' className='bg-border hidden h-6 w-px xl:block' />
      <span className='text-muted-foreground text-sm whitespace-nowrap'>
        {t('Horizontal position')}: {state.progress}%
      </span>
      <div className='flex gap-2'>
        <Button
          type='button'
          variant='outline'
          size='sm'
          aria-label={t('Scroll left')}
          disabled={!state.canScrollLeft}
          onClick={() => scrollTable(-1)}
        >
          <ChevronLeft className='size-4' />
        </Button>
        <Button
          type='button'
          variant='outline'
          size='sm'
          aria-label={t('Scroll right')}
          disabled={!state.canScrollRight}
          onClick={() => scrollTable(1)}
        >
          <ChevronRight className='size-4' />
        </Button>
      </div>
    </>
  )
}

export function PriceMonitorPanel({
  canEdit,
  canRepairPricing,
}: PriceMonitorPanelProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const requestSaveConfirmation = useSettingsSaveConfirmation()
  const [form, setForm] = useState(DEFAULT_FORM)
  const formInitializedRef = useRef(false)
  const priceMatrixRef = useRef<HTMLDivElement | null>(null)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [endpointPickerOpen, setEndpointPickerOpen] = useState(false)
  // 选择渠道与端点复用同步上游倍率的那套弹窗，两处交互保持一致。
  const [endpointDraft, setEndpointDraft] = useState<Record<number, string>>({})
  const [endpointSelection, setEndpointSelection] = useState<number[]>([])

  const upstreamChannelsQuery = useQuery({
    queryKey: ['upstream-channels'],
    queryFn: getUpstreamChannels,
    enabled: endpointPickerOpen,
  })
  // 巡检只认真实渠道：官方与 models.dev 预设地址固定，OpenRouter 用的是
  // /v1/models + 渠道密钥，都不接受人工指定端点。
  const endpointChannels = useMemo<UpstreamChannel[]>(
    () =>
      (upstreamChannelsQuery.data?.data ?? []).filter(
        (channel) => channel.id > 0 && channel.type !== OPENROUTER_CHANNEL_TYPE
      ),
    [upstreamChannelsQuery.data]
  )
  const endpointChannelNames = useMemo(() => {
    const names = new Map<string, string>()
    for (const channel of endpointChannels) {
      names.set(String(channel.id), channel.name)
    }
    return names
  }, [endpointChannels])
  const customEndpointEntries = useMemo(
    () =>
      Object.entries(form.customEndpoints).sort((left, right) =>
        Number(left[0]) - Number(right[0])
      ),
    [form.customEndpoints]
  )

  const [shareOpen, setShareOpen] = useState(false)
  const [page, setPage] = useState(1)
  const [draftModel, setDraftModel] = useState('')
  const [runBaseline, setRunBaseline] = useState<{
    checkedAt: number
    lastAttemptAt: number
  } | null>(null)
  const [filters, setFilters] = useState<{
    model: string
    sourceKeys: string[] | null
    comparison: ComparisonFilter
  }>({ model: '', sourceKeys: null, comparison: 'all' })

  const statusQuery = useQuery({
    queryKey: ['price-monitor-status'],
    queryFn: getPriceMonitorStatus,
    refetchInterval: runBaseline ? 1_000 : 15_000,
  })
  const resultsQuery = useQuery({
    queryKey: ['price-monitor-results', filters, page],
    queryFn: () =>
      getPriceMonitorResults({
        model: filters.model,
        source_keys:
          filters.sourceKeys === null
            ? undefined
            : filters.sourceKeys.join(','),
        comparison: filters.comparison,
        page,
        page_size: PAGE_SIZE,
      }),
    refetchInterval: 30_000,
    placeholderData: keepPreviousData,
  })

  const saveMutation = useMutation({
    mutationFn: async () => {
      const response = await updatePriceMonitorSettings({
        enabled: form.enabled,
        interval_minutes: form.intervalMinutes,
        timeout_seconds: form.timeoutSeconds,
        include_models_dev: form.includeModelsDev,
        model_whitelist: form.modelWhitelist,
        custom_endpoints: form.customEndpoints,
      })
      if (!response.success) throw new Error(response.message)
      return response
    },
    onSuccess: () => {
      toast.success(t('Price monitor settings saved'))
      setSettingsOpen(false)
      if (
        !form.includeModelsDev &&
        modelsDevComparisonFilters.has(filters.comparison)
      ) {
        setPage(1)
        setFilters((current) => ({ ...current, comparison: 'all' }))
      }
      void Promise.all([
        queryClient.invalidateQueries({ queryKey: ['price-monitor-status'] }),
        queryClient.invalidateQueries({ queryKey: ['price-monitor-results'] }),
      ])
    },
    onError: (error: Error) =>
      toast.error(error.message || t('Failed to save price monitor settings')),
  })

  const [selectedModels, setSelectedModels] = useState<Set<string>>(new Set())
  const [repairOpen, setRepairOpen] = useState(false)
  // 当前快照内已改过价的模型。快照在下次巡检前仍显示旧价格，再提交一次会与
  // 刚写入的新价冲突，所以这些行的改价入口先禁用；快照一换（checked_at 变化）自动失效。
  const [appliedModels, setAppliedModels] = useState<{
    checkedAt: number
    models: ReadonlySet<string>
  }>({ checkedAt: 0, models: NO_MODELS })

  // 结果统一在 handleApplyPrice 里处理，这里不挂 onSuccess / onError，避免重复提示。
  const applyPriceMutation = useMutation({ mutationFn: applyPriceMonitorPrice })

  const runMutation = useMutation({
    mutationFn: runPriceMonitor,
    onMutate: () => {
      setRunBaseline({
        checkedAt: statusQuery.data?.data.snapshot.checked_at ?? 0,
        lastAttemptAt: statusQuery.data?.data.last_attempt_at ?? 0,
      })
    },
    onSuccess: (response) => {
      if (!response.success) {
        setRunBaseline(null)
        toast.error(response.message || t('Failed to start price check'))
        return
      }
      toast.success(t('Price check started'))
      queryClient.invalidateQueries({ queryKey: ['price-monitor-status'] })
    },
    onError: (error: Error) => {
      setRunBaseline(null)
      toast.error(error.message || t('Failed to start price check'))
    },
  })

  const status = statusQuery.data?.data
  const snapshot = status?.snapshot
  const results = resultsQuery.data?.data
  const checkedAt = snapshot?.checked_at ?? 0
  const isLossView = filters.comparison === 'loss_risk'
  const repairTargets = useMemo(() => {
    const targets = new Map<string, PriceMonitorRepairTarget>()
    if (!isLossView) return targets
    for (const item of results?.items ?? []) {
      const target = buildRepairTarget(item, results?.source_headers ?? [])
      if (target) targets.set(item.model, target)
    }
    return targets
  }, [isLossView, results?.items, results?.source_headers])
  const appliedInSnapshot =
    appliedModels.checkedAt === checkedAt ? appliedModels.models : NO_MODELS
  const selectableModels = useMemo(
    () =>
      [...repairTargets.keys()].filter(
        (model) => !appliedInSnapshot.has(model)
      ),
    [appliedInSnapshot, repairTargets]
  )
  // 必须 memo：结果每 30 秒轮询一次，内联数组会让弹窗每次都拿到新的 targets。
  const selectedTargets = useMemo(
    () =>
      [...selectedModels].flatMap((model) => {
        const target = repairTargets.get(model)
        return target ? [target] : []
      }),
    [repairTargets, selectedModels]
  )

  const handleApplyPrice = async (
    items: PriceMonitorApplyPriceItem[],
    force: boolean
  ): Promise<PriceMonitorFloorViolation[]> => {
    let response: PriceMonitorApplyPriceResponse
    try {
      response = await applyPriceMutation.mutateAsync({
        checked_at: checkedAt,
        pricing_version: status?.pricing_version ?? 0,
        items,
        force,
      })
    } catch {
      toast.error(t('Failed to update pricing'))
      return []
    }
    if (!response.success) {
      // 低于保本下限：交给弹窗列出违规字段并进入确认步骤，不弹 toast。
      const violations =
        response.error_code === 'PRICE_BELOW_FLOOR'
          ? (response.data?.violations ?? [])
          : []
      if (violations.length === 0) {
        toast.error(response.message || t('Failed to update pricing'))
      }
      return violations
    }
    const appliedCount = (response.data?.results ?? []).reduce(
      (total, result) => total + result.applied.length,
      0
    )
    setAppliedModels((current) => ({
      checkedAt,
      models: new Set([
        ...(current.checkedAt === checkedAt ? current.models : []),
        ...items.map((item) => item.model),
      ]),
    }))
    setRepairOpen(false)
    setSelectedModels(new Set())
    toast.success(
      t('Updated {{count}} pricing fields', { count: appliedCount })
    )
    void Promise.all([
      queryClient.invalidateQueries({ queryKey: ['price-monitor-status'] }),
      queryClient.invalidateQueries({ queryKey: ['price-monitor-results'] }),
      queryClient.invalidateQueries({ queryKey: ['system-options'] }),
    ])
    return []
  }

  const hasModelsDev = status?.config.include_models_dev === true
  const visiblePrimaryComparisonFilters = hasModelsDev
    ? primaryComparisonFilters
    : primaryComparisonFilters.filter(
        ([value]) => !modelsDevComparisonFilters.has(value)
      )
  const visibleAdditionalComparisonFilters = hasModelsDev
    ? additionalComparisonFilters
    : additionalComparisonFilters.filter(
        ([value]) => !modelsDevComparisonFilters.has(value)
      )
  const totalResults = results?.total ?? 0
  const sourceHeadersKey = (results?.source_headers ?? [])
    .map((header) => header.key)
    .join('|')

  useEffect(() => {
    if (!status?.config || formInitializedRef.current) return
    formInitializedRef.current = true
    setForm(priceMonitorFormFromConfig(status.config))
  }, [status?.config])

  useEffect(() => {
    if (!runBaseline || !status || status.running) return
    const snapshotUpdated = (snapshot?.checked_at ?? 0) > runBaseline.checkedAt
    const attemptFinished = status.last_attempt_at > runBaseline.lastAttemptAt
    if (!snapshotUpdated && !attemptFinished) return

    setRunBaseline(null)
    void Promise.all([
      queryClient.invalidateQueries({ queryKey: ['price-monitor-status'] }),
      queryClient.invalidateQueries({ queryKey: ['price-monitor-results'] }),
    ])
  }, [queryClient, runBaseline, snapshot?.checked_at, status])

  const fixedSourcePositions = useMemo(() => {
    const positions = new Map<string, number>()
    let fixedIndex = 0
    for (const header of results?.source_headers ?? []) {
      if (
        header.type !== 'platform' &&
        header.type !== 'official' &&
        header.type !== 'models_dev'
      ) {
        continue
      }
      positions.set(header.key, fixedIndex)
      fixedIndex += 1
    }
    return positions
  }, [results?.source_headers])
  const shareURL = useMemo(
    () =>
      typeof window === 'undefined'
        ? '/price_monitor/view'
        : `${window.location.origin}/price_monitor/view`,
    []
  )

  const copyText = async (text: string, successKey: string) => {
    try {
      await navigator.clipboard.writeText(text)
      toast.success(t(successKey))
    } catch {
      toast.error(t('Copy failed'))
    }
  }

  const applyFilters = (model = draftModel.trim()) => {
    setPage(1)
    setFilters((current) => ({ ...current, model }))
  }

  useEffect(() => {
    setSelectedModels(new Set())
  }, [filters.comparison, filters.model, filters.sourceKeys, page])

  const sourceLabel = (header: PriceMonitorSourceHeader) => {
    if (header.type === 'platform') return t('Platform configuration')
    if (header.type === 'official') return t('Official price')
    if (header.type === 'models_dev') return t('models.dev price')
    return header.name
  }

  const sourceSubtitle = (header: PriceMonitorSourceHeader) => {
    if (header.type === 'platform') return t('Platform baseline')
    if (header.type === 'official') return t('Required comparison')
    if (header.type === 'models_dev') return t('Optional comparison')
    return header.api_url || ''
  }

  // 覆盖率回答「这个来源到底比了多少模型」：取到的价格条数和其中真正同名可比的条数。
  const sourceCoverage = (header: PriceMonitorSourceHeader) => {
    if (header.type === 'platform' || !header.fetched_models) return ''
    return t('Compared {{matched}} of {{fetched}} models', {
      matched: header.matched_models ?? 0,
      fetched: header.fetched_models,
    })
  }

  // 没参与对比的来源不会出现在矩阵列里，只能在这里说明，否则管理员看不到它们被跳过。
  const inactiveSources = (results?.available_source_headers ?? []).filter(
    (header) => header.status && header.status !== 'ok'
  )
  const inactiveSourceReason = (header: PriceMonitorSourceHeader) => {
    if (header.status === 'no_models') {
      return t('No models configured on this channel, nothing to compare')
    }
    if (header.status === 'no_overlap') {
      return t('Model names do not match the platform, nothing to compare')
    }
    if (header.failure_reason === 'empty') {
      return t('The source returned no price data')
    }
    return t('Source check failed, waiting for the next run')
  }

  // 亏损视图里每行的操作列。
  const renderRepairAction = (item: PriceMonitorMatrixItem) => {
    if (repairTargets.has(item.model)) {
      if (appliedInSnapshot.has(item.model)) {
        return (
          <div className='space-y-1'>
            <Button size='sm' variant='outline' disabled>
              {t('Repair pricing')}
            </Button>
            <p className='text-muted-foreground text-xs'>
              {t('Updated — refreshes after the next check')}
            </p>
          </div>
        )
      }
      return (
        <div className='flex items-start gap-2'>
          <Checkbox
            className='mt-0.5'
            checked={selectedModels.has(item.model)}
            onCheckedChange={(checked) =>
              setSelectedModels((current) => {
                const next = new Set(current)
                if (checked) next.add(item.model)
                else next.delete(item.model)
                return next
              })
            }
          />
          <Button
            size='sm'
            variant='outline'
            disabled={!canRepairPricing}
            onClick={() => {
              setSelectedModels(new Set([item.model]))
              setRepairOpen(true)
            }}
          >
            {t('Repair pricing')}
          </Button>
        </div>
      )
    }
    if (
      isTieredPlatformPrice(item.prices[PLATFORM_KEY]) &&
      hasMeasuredLoss(item, results?.source_headers ?? [])
    ) {
      return (
        <p className='text-muted-foreground text-xs'>
          {t('Tiered price. Edit it in model pricing.')}
        </p>
      )
    }
    return (
      <div className='text-muted-foreground space-y-1 text-xs'>
        <p>{t('Raising the price does not change this margin.')}</p>
        <p>{t('Adjust the group ratio or cost ratio.')}</p>
      </div>
    )
  }

  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>{t('Price monitor')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button
            type='button'
            size='sm'
            variant='outline'
            onClick={() => setShareOpen(true)}
          >
            <Share2 data-icon='inline-start' className='size-4' />
            {t('Share page')}
          </Button>
          <Button
            type='button'
            size='sm'
            variant='outline'
            onClick={() => {
              if (status?.config) {
                setForm(priceMonitorFormFromConfig(status.config))
              }
              setSettingsOpen(true)
            }}
          >
            <Settings2 data-icon='inline-start' className='size-4' />
            {t('Price monitor settings')}
          </Button>
          <Button
            type='button'
            size='sm'
            onClick={() => runMutation.mutate()}
            disabled={
              !canEdit ||
              runMutation.isPending ||
              runBaseline !== null ||
              status?.running ||
              !status?.is_master
            }
          >
            <Play data-icon='inline-start' className='size-4' />
            {status?.running || runBaseline
              ? t('Checking prices')
              : t('Check now')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content className='flex flex-col'>
          <div className='flex h-full min-h-0 max-w-full min-w-0 flex-col gap-3'>
            <div
              className={cn(
                'grid min-w-0 gap-3 md:grid-cols-2',
                hasModelsDev
                  ? 'xl:grid-cols-4 2xl:grid-cols-9'
                  : 'xl:grid-cols-4 2xl:grid-cols-7'
              )}
            >
              <div className='rounded-lg border p-3'>
                <p className='text-muted-foreground text-sm'>
                  {t('Last checked')}
                </p>
                <p className='mt-1 font-medium'>
                  {snapshot?.checked_at
                    ? new Date(snapshot.checked_at * 1000).toLocaleString()
                    : t('Not checked yet')}
                </p>
              </div>
              {hasModelsDev && (
                <div className='rounded-lg border p-3'>
                  <p className='text-muted-foreground text-sm'>
                    {t('Platform vs models.dev')}
                  </p>
                  <p className='mt-1 text-xl font-semibold'>
                    {snapshot?.comparison_model_counts?.platform_models_dev ??
                      0}
                  </p>
                </div>
              )}
              <div className='rounded-lg border p-3'>
                <p className='text-muted-foreground text-sm'>
                  {t('Platform vs official models')}
                </p>
                <p className='mt-1 text-xl font-semibold'>
                  {snapshot?.comparison_model_counts?.platform_official ?? 0}
                </p>
              </div>
              <div className='rounded-lg border p-3'>
                <p className='text-muted-foreground text-sm'>
                  {t('Platform vs channel models')}
                </p>
                <p className='mt-1 text-xl font-semibold'>
                  {snapshot?.comparison_model_counts?.platform_channel ?? 0}
                </p>
              </div>
              {hasModelsDev && (
                <div className='rounded-lg border p-3'>
                  <p className='text-muted-foreground text-sm'>
                    {t('Channel vs models.dev')}
                  </p>
                  <p className='mt-1 text-xl font-semibold'>
                    {snapshot?.comparison_model_counts?.channel_models_dev ?? 0}
                  </p>
                </div>
              )}
              <div className='rounded-lg border p-3'>
                <p className='text-muted-foreground text-sm'>
                  {t('Channel vs official models')}
                </p>
                <p className='mt-1 text-xl font-semibold'>
                  {snapshot?.comparison_model_counts?.channel_official ?? 0}
                </p>
              </div>
              <div className='rounded-lg border p-3'>
                <p className='text-muted-foreground text-sm'>
                  {t('Models at a loss')}
                </p>
                <p className='text-destructive mt-1 text-xl font-semibold'>
                  {snapshot?.comparison_model_counts?.loss_risk ?? 0}
                </p>
              </div>
              <div className='rounded-lg border p-3'>
                <p className='text-muted-foreground text-sm'>
                  {t('Models priced above platform')}
                </p>
                <p className='mt-1 text-xl font-semibold'>
                  {snapshot?.comparison_model_counts?.above_platform ?? 0}
                </p>
              </div>
              <div className='rounded-lg border p-3'>
                <p className='text-muted-foreground text-sm'>
                  {t('Pricing sources')}
                </p>
                <p className='mt-1 font-medium'>
                  {snapshot
                    ? `${snapshot.source_ok}/${snapshot.source_total}`
                    : '—'}
                </p>
              </div>
            </div>

            {status?.last_attempt_error && (
              <p className='text-destructive text-sm'>
                {t('Last check failed. See the logs and retry.')}
              </p>
            )}

            <div className='rounded-lg border p-3'>
              <div className='grid gap-3 lg:grid-cols-[minmax(18rem,1.15fr)_minmax(18rem,1fr)_auto] lg:items-end'>
                <div className='grid min-w-0 gap-2'>
                  <Label htmlFor='price-monitor-model-filter'>
                    {t('Model filter')}
                  </Label>
                  <ModelFilterCombobox
                    models={results?.available_models ?? []}
                    value={draftModel}
                    onValueChange={setDraftModel}
                    onSubmit={(model) => applyFilters(model)}
                    t={t}
                  />
                </div>
                <div className='grid min-w-0 gap-2'>
                  <Label>{t('Displayed channels')}</Label>
                  <SourceColumnPicker
                    headers={results?.available_source_headers ?? []}
                    value={filters.sourceKeys}
                    t={t}
                    onChange={(sourceKeys) => {
                      setPage(1)
                      setFilters((current) => ({ ...current, sourceKeys }))
                    }}
                  />
                </div>
                <div className='flex gap-2'>
                  <Button variant='outline' onClick={() => applyFilters()}>
                    {t('Apply filters')}
                  </Button>
                  <Button
                    variant='ghost'
                    onClick={() => {
                      setDraftModel('')
                      setPage(1)
                      setFilters({
                        model: '',
                        sourceKeys: null,
                        comparison: 'all',
                      })
                    }}
                  >
                    {t('Reset')}
                  </Button>
                </div>
              </div>
              <div className='mt-3 flex min-h-8 flex-wrap items-center gap-2'>
                <ToggleGroup
                  value={
                    visiblePrimaryComparisonFilters.some(
                      ([value]) => value === filters.comparison
                    )
                      ? [filters.comparison]
                      : []
                  }
                  onValueChange={(values) => {
                    const value = values.at(-1) as
                      | (typeof visiblePrimaryComparisonFilters)[number][0]
                      | undefined
                    if (!value) return
                    setPage(1)
                    setFilters((current) => ({
                      ...current,
                      comparison: value,
                    }))
                  }}
                  variant='outline'
                  size='sm'
                  spacing={2}
                  className='flex-wrap'
                >
                  {visiblePrimaryComparisonFilters.map(([value, label]) => (
                    <ToggleGroupItem
                      key={value}
                      value={value}
                      className='data-[state=on]:border-primary/30 data-[state=on]:bg-primary/10 data-[state=on]:text-primary aria-pressed:border-primary/30 aria-pressed:bg-primary/10 aria-pressed:text-primary'
                    >
                      {t(label)}
                    </ToggleGroupItem>
                  ))}
                </ToggleGroup>
                <Select
                  items={visibleAdditionalComparisonFilters.map(
                    ([value, label]) => ({
                      value,
                      label: t(label),
                    })
                  )}
                  value={
                    visibleAdditionalComparisonFilters.some(
                      ([value]) => value === filters.comparison
                    )
                      ? filters.comparison
                      : null
                  }
                  onValueChange={(value) => {
                    if (!value) return
                    setPage(1)
                    setFilters((current) => ({
                      ...current,
                      comparison: value as ComparisonFilter,
                    }))
                  }}
                >
                  <SelectTrigger size='sm' className='w-44'>
                    <SelectValue placeholder={t('More filters')} />
                  </SelectTrigger>
                  <SelectContent align='start' alignItemWithTrigger={false}>
                    <SelectGroup>
                      {visibleAdditionalComparisonFilters.map(
                        ([value, label]) => (
                          <SelectItem
                            key={value}
                            value={value}
                            className='py-2'
                          >
                            {t(label)}
                          </SelectItem>
                        )
                      )}
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <p className='text-muted-foreground ml-auto text-sm'>
                  {t('USD per 1M tokens')}
                </p>
              </div>
            </div>

            <div
              ref={priceMatrixRef}
              className='flex min-h-0 max-w-full min-w-0 flex-1 flex-col overflow-hidden rounded-lg border'
              aria-busy={resultsQuery.isFetching}
            >
              <div className='bg-background flex shrink-0 flex-wrap items-center gap-x-3 gap-y-2 border-b p-3'>
                {/* 批量修复放进表格工具栏，而不是单独占一行：切换筛选时页面结构保持一致，不会上下跳动。 */}
                {isLossView && repairTargets.size > 0 && (
                  <div className='flex items-center gap-2'>
                    <Checkbox
                      aria-label={t('Select all')}
                      checked={
                        selectedModels.size > 0 &&
                        selectedModels.size === selectableModels.length
                      }
                      disabled={selectableModels.length === 0}
                      onCheckedChange={(checked) =>
                        setSelectedModels(
                          checked ? new Set(selectableModels) : new Set()
                        )
                      }
                    />
                    <span className='text-sm whitespace-nowrap'>
                      {t('Selected {{count}}', { count: selectedModels.size })}
                    </span>
                    <Button
                      size='sm'
                      title={t('Only measured losses can be repaired.')}
                      disabled={!canRepairPricing || selectedModels.size === 0}
                      onClick={() => setRepairOpen(true)}
                    >
                      {t('Repair selected')}
                    </Button>
                    <div aria-hidden='true' className='bg-border h-6 w-px' />
                  </div>
                )}
                <PriceMonitorPagination
                  page={page}
                  pageSize={PAGE_SIZE}
                  total={totalResults}
                  isFetching={resultsQuery.isFetching}
                  onPageChange={setPage}
                />
                <PriceMonitorHorizontalScrollControls
                  containerRef={priceMatrixRef}
                  refreshKey={sourceHeadersKey}
                />
              </div>
              {inactiveSources.length > 0 && (
                <Collapsible className='mb-2'>
                  <CollapsibleTrigger className='text-muted-foreground hover:text-foreground flex items-center gap-1 text-xs data-[panel-open]:[&_svg]:rotate-90'>
                    <ChevronRight className='size-3.5 shrink-0 transition-transform' />
                    {t(
                      '{{count}} sources did not take part in this comparison',
                      { count: inactiveSources.length }
                    )}
                  </CollapsibleTrigger>
                  <CollapsibleContent>
                    <ul className='mt-1 max-h-40 space-y-0.5 overflow-y-auto overscroll-contain rounded-md border px-3 py-2'>
                      {inactiveSources.map((header) => (
                        <li
                          key={header.key}
                          className='text-muted-foreground text-xs'
                        >
                          <span className='font-medium'>
                            {sourceLabel(header)}
                          </span>
                          {header.api_url ? ` · ${header.api_url}` : ''}
                          {header.endpoint ? ` · ${header.endpoint}` : ''}
                          {` · ${inactiveSourceReason(header)}`}
                        </li>
                      ))}
                    </ul>
                  </CollapsibleContent>
                </Collapsible>
              )}
              <Table
                className='min-w-max'
                containerClassName='isolate min-h-0 flex-1 max-w-full overflow-auto'
              >
                <TableHeader>
                  <TableRow>
                    <TableHead className='bg-muted sticky top-0 left-0 z-50 w-56 max-w-56 min-w-56'>
                      {t('Model')}
                    </TableHead>
                    {isLossView && (
                      <TableHead className='bg-muted sticky top-0 z-30 w-40 max-w-40 min-w-40'>
                        {t('Action')}
                      </TableHead>
                    )}
                    {(results?.source_headers ?? []).map((header) => (
                      <TableHead
                        key={header.key}
                        className={`bg-muted sticky top-0 z-30 w-64 max-w-64 min-w-64 align-top ${sourceStickyClass(header.type, fixedSourcePositions.get(header.key), 'header')}`}
                      >
                        <span className='block font-semibold break-all'>
                          {sourceLabel(header)}
                        </span>
                        <span className='text-muted-foreground mt-0.5 block text-xs font-normal'>
                          {sourceSubtitle(header)}
                        </span>
                        {sourceCoverage(header) && (
                          <span className='text-muted-foreground mt-0.5 block text-xs font-normal'>
                            {sourceCoverage(header)}
                          </span>
                        )}
                      </TableHead>
                    ))}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {(results?.items ?? []).map((item) => {
                    return (
                      <TableRow key={item.model}>
                        <TableCell className='bg-background sticky left-0 z-30 w-56 max-w-56 min-w-56 align-top font-semibold'>
                          {item.model}
                        </TableCell>
                        {isLossView && (
                          <TableCell className='bg-background w-40 max-w-40 min-w-40 align-top'>
                            {renderRepairAction(item)}
                          </TableCell>
                        )}
                        {(results?.source_headers ?? []).map((header) => (
                          <TableCell
                            key={header.key}
                            className={`bg-background w-64 max-w-64 min-w-64 align-top ${sourceStickyClass(header.type, fixedSourcePositions.get(header.key), 'body')}`}
                          >
                            <PriceCell
                              price={item.prices[header.key]}
                              sourceType={header.type}
                            />
                          </TableCell>
                        ))}
                      </TableRow>
                    )
                  })}
                  {!resultsQuery.isLoading &&
                    (results?.items.length ?? 0) === 0 && (
                      <TableRow>
                        <TableCell
                          colSpan={
                            1 +
                            (isLossView ? 1 : 0) +
                            (results?.source_headers.length ?? 0)
                          }
                          className='text-muted-foreground text-center'
                        >
                          {t('No price differences found')}
                        </TableCell>
                      </TableRow>
                    )}
                </TableBody>
              </Table>
            </div>
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <PriceMonitorApplyDialog
        open={repairOpen}
        submitting={applyPriceMutation.isPending}
        targets={selectedTargets}
        onOpenChange={setRepairOpen}
        onSubmit={handleApplyPrice}
      />

      <Dialog
        open={settingsOpen}
        onOpenChange={(open) => {
          if (!open && status?.config) {
            setForm(priceMonitorFormFromConfig(status.config))
          }
          setSettingsOpen(open)
        }}
        title={t('Price monitor settings')}
        contentClassName='sm:max-w-4xl'
        footer={
          <>
            <Button
              type='button'
              variant='outline'
              onClick={() => setSettingsOpen(false)}
            >
              {canEdit ? t('Cancel') : t('Close')}
            </Button>
            <Button
              type='button'
              onClick={() =>
                requestSaveConfirmation(async () => {
                  await saveMutation.mutateAsync()
                })
              }
              disabled={
                !canEdit ||
                saveMutation.isPending ||
                form.intervalMinutes < 5 ||
                form.timeoutSeconds < 1 ||
                form.timeoutSeconds > 120
              }
            >
              <Save data-icon='inline-start' className='size-4' />
              {saveMutation.isPending ? t('Saving...') : t('Save settings')}
            </Button>
          </>
        }
      >
        <fieldset disabled={!canEdit}>
          <FieldGroup className='grid gap-4 md:grid-cols-2'>
            <Field
              data-disabled={!canEdit}
              orientation='horizontal'
              className='rounded-md border px-3 py-3'
            >
              <FieldLabel>{t('Enable price monitor')}</FieldLabel>
              <Switch
                checked={form.enabled}
                onCheckedChange={(enabled) =>
                  setForm((current) => ({ ...current, enabled }))
                }
              />
            </Field>
            <Field
              data-disabled={!canEdit}
              orientation='horizontal'
              className='rounded-md border px-3 py-3'
            >
              <FieldLabel>{t('Include models.dev prices')}</FieldLabel>
              <Switch
                checked={form.includeModelsDev}
                onCheckedChange={(includeModelsDev) =>
                  setForm((current) => ({ ...current, includeModelsDev }))
                }
              />
            </Field>
            <Field data-disabled={!canEdit}>
              <FieldLabel htmlFor='price-monitor-interval'>
                {t('Check interval (minutes)')}
              </FieldLabel>
              <Input
                id='price-monitor-interval'
                type='number'
                min={5}
                value={form.intervalMinutes}
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    intervalMinutes: Number(event.target.value),
                  }))
                }
              />
            </Field>
            <Field data-disabled={!canEdit}>
              <FieldLabel htmlFor='price-monitor-timeout'>
                {t('Source timeout (seconds)')}
              </FieldLabel>
              <Input
                id='price-monitor-timeout'
                type='number'
                min={1}
                max={120}
                value={form.timeoutSeconds}
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    timeoutSeconds: Number(event.target.value),
                  }))
                }
              />
            </Field>
            <Field data-disabled={!canEdit} className='md:col-span-2'>
              <FieldLabel htmlFor='price-monitor-model-whitelist'>
                {t('Excluded model whitelist')}
              </FieldLabel>
              <Textarea
                id='price-monitor-model-whitelist'
                rows={4}
                value={form.modelWhitelist}
                placeholder={t('One per line or comma-separated')}
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    modelWhitelist: event.target.value,
                  }))
                }
              />
              <FieldDescription>
                {t('Listed models are skipped; leave empty to check all.')}
              </FieldDescription>
            </Field>
            <Field data-disabled={!canEdit} className='md:col-span-2'>
              <FieldLabel>{t('Channel price endpoints')}</FieldLabel>
              <div className='space-y-2'>
                {customEndpointEntries.length === 0 && (
                  <p className='text-muted-foreground text-sm'>
                    {t('All channels are probed automatically.')}
                  </p>
                )}
                {customEndpointEntries.map(([channelId, endpoint]) => (
                  <div
                    key={channelId}
                    className='flex min-w-0 items-center gap-2 rounded-md border px-3 py-2'
                  >
                    <span className='min-w-0 flex-1 truncate text-sm font-medium'>
                      {endpointChannelNames.get(channelId) ??
                        t('Channel #{{id}}', { id: channelId })}
                    </span>
                    <span className='text-muted-foreground min-w-0 flex-1 truncate text-xs'>
                      {endpoint}
                    </span>
                    <Button
                      type='button'
                      size='sm'
                      variant='ghost'
                      disabled={!canEdit}
                      onClick={() =>
                        setForm((current) => {
                          const next = { ...current.customEndpoints }
                          delete next[channelId]
                          return { ...current, customEndpoints: next }
                        })
                      }
                    >
                      {t('Remove')}
                    </Button>
                  </div>
                ))}
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  disabled={!canEdit}
                  onClick={() => {
                    const draft: Record<number, string> = {}
                    for (const [channelId, endpoint] of Object.entries(
                      form.customEndpoints
                    )) {
                      draft[Number(channelId)] = endpoint
                    }
                    setEndpointDraft(draft)
                    setEndpointSelection(
                      Object.keys(form.customEndpoints).map(Number)
                    )
                    setEndpointPickerOpen(true)
                  }}
                >
                  {t('Select channels and endpoints')}
                </Button>
              </div>
              <FieldDescription>
                {t(
                  'Configured channels use only that endpoint. Channels left out are probed automatically: /api/pricing first, then /api/ratio_config.'
                )}
              </FieldDescription>
            </Field>
          </FieldGroup>
        </fieldset>
      </Dialog>

      <ChannelSelectorDialog
        open={endpointPickerOpen}
        onOpenChange={setEndpointPickerOpen}
        channels={endpointChannels}
        selectedChannelIds={endpointSelection}
        onSelectedChannelIdsChange={setEndpointSelection}
        channelEndpoints={endpointDraft}
        onChannelEndpointsChange={setEndpointDraft}
        onConfirm={(selectedIds) => {
          const endpoints: Record<string, string> = {}
          for (const channelId of selectedIds) {
            const endpoint = (endpointDraft[channelId] ?? '').trim()
            if (endpoint) endpoints[String(channelId)] = endpoint
          }
          setForm((current) => ({ ...current, customEndpoints: endpoints }))
          setEndpointPickerOpen(false)
        }}
      />

      <Dialog
        open={shareOpen}
        onOpenChange={setShareOpen}
        title={t('Share page')}
        contentClassName='sm:max-w-xl'
        footer={
          <Button
            type='button'
            variant='outline'
            render={<a href={shareURL} target='_blank' rel='noreferrer' />}
          >
            <ExternalLink data-icon='inline-start' className='size-4' />
            {t('Open share page')}
          </Button>
        }
      >
        <FieldGroup>
          <Field>
            <FieldLabel>{t('Share page')}</FieldLabel>
            <InputGroup>
              <InputGroupInput value={shareURL} readOnly />
              <InputGroupAddon align='inline-end'>
                <InputGroupButton
                  size='icon-xs'
                  aria-label={t('Copy')}
                  onClick={() => copyText(shareURL, 'Share link copied')}
                >
                  <Copy className='size-4' />
                </InputGroupButton>
              </InputGroupAddon>
            </InputGroup>
          </Field>
          <Field>
            <FieldLabel>{t('Current access password')}</FieldLabel>
            <InputGroup>
              <InputGroupInput
                value={snapshot?.access_password ?? ''}
                readOnly
              />
              <InputGroupAddon align='inline-end'>
                <InputGroupButton
                  size='icon-xs'
                  aria-label={t('Copy')}
                  disabled={!snapshot?.access_password}
                  onClick={() =>
                    copyText(
                      snapshot?.access_password ?? '',
                      'Access password copied'
                    )
                  }
                >
                  <Copy className='size-4' />
                </InputGroupButton>
              </InputGroupAddon>
            </InputGroup>
          </Field>
        </FieldGroup>
      </Dialog>
    </>
  )
}

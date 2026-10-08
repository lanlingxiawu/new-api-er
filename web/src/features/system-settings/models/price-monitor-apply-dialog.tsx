/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import type { TFunction } from 'i18next'
import { type ReactNode, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
} from '@/components/ui/input-group'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { cn } from '@/lib/utils'

import type {
  PriceMonitorApplyPriceItem,
  PriceMonitorFloorViolation,
  PriceMonitorPriceCell,
  PriceMonitorRepairFloor,
} from '../types'
import { PriceInput } from './model-pricing-inputs'
import { shouldForceApply } from './price-monitor-apply-force'
import { formatPricingNumber } from './pricing-format'

const numericDraftRegex = /^(\d+(\.\d*)?|\.\d*)?$/

export type PriceMonitorRepairTarget = {
  model: string
  /** 平台当前的展示价（计费实际使用的价格）。 */
  platform: PriceMonitorPriceCell
  /**
   * 该行的改价数据：fields/display 是保本下限（没有可比渠道时为空），current 是平台现值
   * （也是改价请求的 expected），official/lowest/highest 是参考价。
   */
  floor: PriceMonitorRepairFloor
}

/** 保本价 / 官方价 / 最高渠道价 / 手动。 */
type RepairMode = 'floor' | 'official' | 'highest' | 'manual'

/** 一行价格。价格一律是展示价：按量为每百万 token，按次为每次。 */
type PriceRow = {
  field: string
  labelKey: string
  current?: number
  /** 保本价：低于它就会在某个渠道上亏。 */
  floor?: number
  official?: number
  lowest?: number
  highest?: number
  /** 补全倍率被系统锁定时的输出行：跟随输入价，不能单独改。 */
  derived?: boolean
}

type RowPrices = Record<string, number | undefined>

const PRICE_EPSILON = 1e-9

// 字段与文案与模型定价页一致（model-pricing-core 的 laneConfigs）。
const FIELD_LABELS: Record<string, string> = {
  model_price: 'Fixed price',
  model_ratio: 'Input price',
  completion_ratio: 'Completion price',
  cache_ratio: 'Cache read price',
  create_cache_ratio: 'Cache write price',
  image_ratio: 'Image input price',
  audio_ratio: 'Audio input price',
  audio_completion_ratio: 'Audio output price',
}

const LANE_ROWS: Array<{ field: string; lane: string }> = [
  { field: 'cache_ratio', lane: 'cache_read' },
  { field: 'create_cache_ratio', lane: 'cache_write' },
  { field: 'image_ratio', lane: 'image_input' },
  { field: 'audio_ratio', lane: 'audio_input' },
  { field: 'audio_completion_ratio', lane: 'audio_output' },
]

function formatPrice(value: number | undefined) {
  return value === undefined ? '—' : `$${formatPricingNumber(value)}`
}

function nearlyEqual(left: number | undefined, right: number | undefined) {
  if (left === undefined || right === undefined) return false
  const scale = Math.max(1, Math.abs(left), Math.abs(right))
  return Math.abs(left - right) <= PRICE_EPSILON * scale
}

function isBelow(value: number | undefined, limit: number | undefined) {
  if (value === undefined || limit === undefined) return false
  return value < limit && !nearlyEqual(value, limit)
}

/** 提交给后端的倍率保留 12 位有效数字，避免 0.03 / 0.3 这类除法留下一长串浮点尾数。 */
function roundRatio(value: number) {
  return Number(value.toPrecision(12))
}

function lockedRatio(target: PriceMonitorRepairTarget) {
  return target.floor.locked_completion_ratio ?? 0
}

function hasEntries(values?: Record<string, number>) {
  return values !== undefined && Object.keys(values).length > 0
}

function buildRows(target: PriceMonitorRepairTarget): PriceRow[] {
  const { platform, floor } = target
  const floorFields = floor.fields ?? {}
  const refs = (field: string) => ({
    // display 里的值只有同时出现在 fields 里才是保本约束。
    floor:
      floorFields[field] !== undefined ? floor.display?.[field] : undefined,
    official: floor.official?.[field],
    lowest: floor.lowest?.[field],
    highest: floor.highest?.[field],
  })
  const referenced = (field: string) =>
    Object.values(refs(field)).some((value) => value !== undefined)

  if (platform.mode === 'per_request') {
    return [
      {
        field: 'model_price',
        labelKey: FIELD_LABELS.model_price,
        current: platform.price,
        ...refs('model_price'),
      },
    ]
  }
  // 输入价总要出现：其余项的倍率都相对输入价换算。
  const rows: PriceRow[] = [
    {
      field: 'model_ratio',
      labelKey: FIELD_LABELS.model_ratio,
      current: platform.input,
      ...refs('model_ratio'),
    },
  ]
  const locked = lockedRatio(target) > 0
  if (platform.output !== undefined || referenced('completion_ratio')) {
    rows.push({
      field: 'completion_ratio',
      labelKey: FIELD_LABELS.completion_ratio,
      current: platform.output,
      ...refs('completion_ratio'),
      derived: locked,
    })
  }
  for (const { field, lane } of LANE_ROWS) {
    const current = platform.lanes?.find((entry) => entry.key === lane)?.price
    if (current === undefined && !referenced(field)) continue
    rows.push({
      field,
      labelKey: FIELD_LABELS[field],
      current,
      ...refs(field),
    })
  }
  return rows
}

function maxDefined(left: number | undefined, right: number | undefined) {
  if (left === undefined) return right
  if (right === undefined) return left
  return Math.max(left, right)
}

/**
 * 「保本价」档：每一项低于保本价时抬到保本价，已经高于的保持现价——只抬不降，不会把利润压到零。
 * 补全倍率锁定时，后端已把输出侧的要求折算进输入价的保本下限。
 */
function floorPrices(rows: PriceRow[]) {
  const prices: RowPrices = {}
  for (const row of rows) {
    prices[row.field] = maxDefined(row.current, row.floor)
  }
  return prices
}

/** 「官方价」档：有官方价的项改成官方价（可升可降），没有的项保持现价。 */
function officialPrices(rows: PriceRow[]) {
  const prices: RowPrices = {}
  for (const row of rows) {
    prices[row.field] = row.official ?? row.current
  }
  return prices
}

/**
 * 「最高价」档：每一项取现价与渠道最高价中较高的一个——只抬价、不降价，没有渠道报价的项保持现价；
 * 锁定模型的输入价还要覆盖「输出最高价 / 锁定倍率」。
 */
function highestPrices(target: PriceMonitorRepairTarget, rows: PriceRow[]) {
  const prices: RowPrices = {}
  for (const row of rows) {
    prices[row.field] = maxDefined(row.current, row.highest)
  }
  const locked = lockedRatio(target)
  const output = target.floor.highest?.completion_ratio
  const input = prices.model_ratio
  if (locked > 0 && output !== undefined && input !== undefined) {
    prices.model_ratio = Math.max(input, output / locked)
  }
  return prices
}

function manualPrices(rows: PriceRow[], drafts: Record<string, string>) {
  const prices: RowPrices = {}
  for (const row of rows) {
    const raw = (drafts[row.field] ?? '').trim()
    const parsed = Number(raw)
    prices[row.field] =
      raw === '' || !Number.isFinite(parsed) ? undefined : parsed
  }
  return prices
}

function modePrices(
  mode: RepairMode,
  target: PriceMonitorRepairTarget,
  rows: PriceRow[],
  drafts: Record<string, string>
) {
  switch (mode) {
    case 'floor':
      return floorPrices(rows)
    case 'official':
      return officialPrices(rows)
    case 'highest':
      return highestPrices(target, rows)
    default:
      return manualPrices(rows, drafts)
  }
}

/** 行的新价格。锁定模型的输出行 = 新输入价 × 锁定倍率。 */
function rowPrice(
  target: PriceMonitorRepairTarget,
  row: PriceRow,
  prices: RowPrices
) {
  if (row.derived) {
    const input = prices.model_ratio
    return input === undefined ? undefined : input * lockedRatio(target)
  }
  return prices[row.field]
}

function rowError(row: PriceRow, prices: RowPrices, t: TFunction) {
  if (row.derived) return undefined
  const price = prices[row.field]
  if (price === undefined || price < 0) return t('Enter a valid price')
  if (row.field === 'model_ratio' && !(price > 0)) {
    return t('The input price must be greater than 0')
  }
  return undefined
}

/**
 * 价格换算成倍率：model_ratio = 输入价 / 2，其余比值字段 = 该项价格 / 输入价，按次为价格本身。
 * 只提交变动的字段；输入价变了时比值字段都要按新输入价重算，才能保持填写的价格不变。
 */
function buildItem(
  target: PriceMonitorRepairTarget,
  rows: PriceRow[],
  prices: RowPrices
): PriceMonitorApplyPriceItem | null {
  const fields: Record<string, number> = {}
  if (target.platform.mode === 'per_request') {
    const price = prices.model_price
    if (price !== undefined && !nearlyEqual(price, rows[0]?.current)) {
      fields.model_price = price
    }
  } else {
    const input = prices.model_ratio
    if (input === undefined || !(input > 0)) return null
    const inputRow = rows.find((row) => row.field === 'model_ratio')
    const inputChanged = !nearlyEqual(input, inputRow?.current)
    if (inputChanged) fields.model_ratio = roundRatio(input / 2)
    for (const row of rows) {
      if (row.field === 'model_ratio' || row.derived) continue
      const price = prices[row.field]
      if (price === undefined) continue
      if (!inputChanged && nearlyEqual(price, row.current)) continue
      fields[row.field] = roundRatio(price / input)
    }
  }
  if (Object.keys(fields).length === 0) return null
  const expected = Object.fromEntries(
    Object.keys(fields).map((field) => [
      field,
      target.floor.current?.[field] ?? null,
    ])
  )
  return { model: target.model, fields, expected }
}

function seedDrafts(targets: PriceMonitorRepairTarget[]) {
  return Object.fromEntries(
    targets.map((target) => [
      target.model,
      Object.fromEntries(
        buildRows(target).map((row) => [
          row.field,
          formatPricingNumber(row.current),
        ])
      ),
    ])
  )
}

function modeAvailable(mode: RepairMode, targets: PriceMonitorRepairTarget[]) {
  switch (mode) {
    case 'floor':
      return targets.some((target) => hasEntries(target.floor.fields))
    case 'official':
      return targets.some((target) => hasEntries(target.floor.official))
    case 'highest':
      return targets.some((target) => hasEntries(target.floor.highest))
    default:
      return true
  }
}

function defaultMode(targets: PriceMonitorRepairTarget[]): RepairMode {
  const preferred: RepairMode[] = ['floor', 'official', 'highest']
  return preferred.find((mode) => modeAvailable(mode, targets)) ?? 'manual'
}

function RatioHint({
  row,
  prices,
  t,
}: {
  row: PriceRow
  prices: RowPrices
  t: TFunction
}) {
  const price = prices[row.field]
  if (price === undefined || row.field === 'model_price') return null
  const input = prices.model_ratio
  let ratio: number | undefined
  if (row.field === 'model_ratio') {
    ratio = price / 2
  } else if (input !== undefined && input > 0) {
    ratio = price / input
  }
  if (ratio === undefined) return null
  return (
    <p className='text-muted-foreground text-xs'>
      {t('Ratio: {{ratio}}', { ratio: formatPricingNumber(roundRatio(ratio)) })}
    </p>
  )
}

function channelRange(row: PriceRow) {
  if (row.lowest === undefined || row.highest === undefined) {
    return formatPrice(row.highest ?? row.lowest)
  }
  if (nearlyEqual(row.lowest, row.highest)) return formatPrice(row.highest)
  return `${formatPrice(row.lowest)} – ${formatPrice(row.highest)}`
}

function ModelPriceCard({
  target,
  rows,
  mode,
  drafts,
  prices,
  flagged,
  included,
  selectable,
  onIncludedChange,
  onDraftChange,
  t,
}: {
  target: PriceMonitorRepairTarget
  rows: PriceRow[]
  mode: RepairMode
  drafts: Record<string, string>
  prices: RowPrices
  flagged: Set<string>
  included: boolean
  selectable: boolean
  onIncludedChange: (included: boolean) => void
  onDraftChange: (field: string, value: string) => void
  t: TFunction
}) {
  const belowCount = rows.filter((row) =>
    isBelow(rowPrice(target, row, prices), row.floor)
  ).length
  const hasFloor = hasEntries(target.floor.fields)
  const perRequest = target.platform.mode === 'per_request'

  let floorBadge: ReactNode = null
  if (belowCount > 0) {
    floorBadge = (
      <Badge variant='warning'>
        {t('{{count}} below break-even', { count: belowCount })}
      </Badge>
    )
  } else if (hasFloor) {
    floorBadge = <Badge variant='outline'>{t('Not below break-even')}</Badge>
  }

  return (
    <section
      className={cn(
        'overflow-hidden rounded-lg border',
        !included && 'opacity-60'
      )}
    >
      <div className='bg-muted/40 flex items-center justify-between gap-3 border-b px-4 py-2.5'>
        <label className='flex min-w-0 items-center gap-2'>
          {selectable && (
            <Checkbox
              checked={included}
              aria-label={t('Include this model')}
              onCheckedChange={(checked) => onIncludedChange(checked === true)}
            />
          )}
          <span className='truncate text-sm font-medium'>{target.model}</span>
        </label>
        {included && floorBadge}
      </div>
      {included && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className='ps-4'>{t('Item')}</TableHead>
              <TableHead className='text-end'>{t('Current price')}</TableHead>
              <TableHead className='text-end'>
                {t('Break-even price')}
              </TableHead>
              <TableHead className='text-end'>{t('Official price')}</TableHead>
              <TableHead className='text-end'>{t('Channel prices')}</TableHead>
              <TableHead
                className={cn('pe-4', mode === 'manual' ? 'w-56' : 'text-end')}
              >
                {t('New price')}
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((row) => {
              const price = rowPrice(target, row, prices)
              const below = isBelow(price, row.floor)
              const error =
                mode === 'manual' ? rowError(row, prices, t) : undefined
              const editable = mode === 'manual' && !row.derived
              const onValueChange = (value: string) => {
                if (numericDraftRegex.test(value)) {
                  onDraftChange(row.field, value)
                }
              }
              let valueCell: ReactNode = (
                <span
                  className={cn(
                    'font-medium tabular-nums',
                    below && 'text-warning'
                  )}
                >
                  {formatPrice(price)}
                </span>
              )
              if (editable && perRequest) {
                valueCell = (
                  <InputGroup>
                    <InputGroupAddon>$</InputGroupAddon>
                    <InputGroupInput
                      inputMode='decimal'
                      aria-invalid={Boolean(error) || below}
                      value={drafts[row.field] ?? ''}
                      onChange={(event) => onValueChange(event.target.value)}
                    />
                    <InputGroupAddon align='inline-end'>
                      {t('per request')}
                    </InputGroupAddon>
                  </InputGroup>
                )
              } else if (editable) {
                valueCell = (
                  <PriceInput
                    value={drafts[row.field] ?? ''}
                    onChange={onValueChange}
                  />
                )
              }
              return (
                <TableRow key={row.field}>
                  <TableCell className='ps-4 align-top'>
                    {t(row.labelKey)}
                  </TableCell>
                  <TableCell className='text-muted-foreground text-end align-top tabular-nums'>
                    {formatPrice(row.current)}
                  </TableCell>
                  <TableCell className='text-end align-top tabular-nums'>
                    {formatPrice(row.floor)}
                  </TableCell>
                  <TableCell className='text-end align-top tabular-nums'>
                    {formatPrice(row.official)}
                  </TableCell>
                  <TableCell className='text-end align-top tabular-nums'>
                    {channelRange(row)}
                  </TableCell>
                  <TableCell
                    className={cn('pe-4 align-top', !editable && 'text-end')}
                  >
                    <div className='space-y-1'>
                      {valueCell}
                      {editable && !error && (
                        <RatioHint row={row} prices={prices} t={t} />
                      )}
                      {row.derived && (
                        <p className='text-muted-foreground text-xs'>
                          {t('Input price × {{ratio}} (locked)', {
                            ratio: formatPricingNumber(lockedRatio(target)),
                          })}
                        </p>
                      )}
                      {error && (
                        <p className='text-destructive text-xs'>{error}</p>
                      )}
                      {!error && below && (
                        <p className='text-warning text-xs'>
                          {t('Below break-even price')}
                        </p>
                      )}
                      {flagged.has(row.field) && (
                        <p className='text-warning text-xs'>
                          {t('Below cost after group discounts')}
                        </p>
                      )}
                    </div>
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      )}
    </section>
  )
}

export function PriceMonitorApplyDialog({
  open,
  targets,
  submitting,
  onOpenChange,
  onSubmit,
}: {
  open: boolean
  targets: PriceMonitorRepairTarget[]
  submitting: boolean
  onOpenChange: (open: boolean) => void
  /** 返回服务端判定低于保本下限的字段；成功或其它失败返回空数组。 */
  onSubmit: (
    items: PriceMonitorApplyPriceItem[],
    force: boolean
  ) => Promise<PriceMonitorFloorViolation[]>
}) {
  const { t } = useTranslation()
  const [mode, setMode] = useState<RepairMode>('floor')
  const [drafts, setDrafts] = useState<Record<string, Record<string, string>>>(
    {}
  )
  const [excluded, setExcluded] = useState<ReadonlySet<string>>(new Set())
  // 有低于保本价（或服务端判定仍亏损）的项时，要求再点一次确认。
  const [confirmed, setConfirmed] = useState(false)
  const [serverViolations, setServerViolations] = useState<
    PriceMonitorFloorViolation[]
  >([])

  // 只在「打开」的那一刻初始化。不能依赖 targets：父组件每次重渲染都可能传入
  // 新数组，那样会把手填的值和二次确认一起清掉。
  const [wasOpen, setWasOpen] = useState(open)
  if (open !== wasOpen) {
    setWasOpen(open)
    if (open) {
      setMode(defaultMode(targets))
      setDrafts(seedDrafts(targets))
      setExcluded(new Set())
      setConfirmed(false)
      setServerViolations([])
    }
  }

  const resetConfirmation = () => {
    setConfirmed(false)
    setServerViolations([])
  }

  const models = useMemo(
    () =>
      targets.map((target) => {
        const rows = buildRows(target)
        const prices = modePrices(
          mode,
          target,
          rows,
          drafts[target.model] ?? {}
        )
        const included = !excluded.has(target.model)
        const errors =
          included && mode === 'manual'
            ? rows.filter((row) => rowError(row, prices, t)).length
            : 0
        const below = included
          ? rows.filter((row) =>
              isBelow(rowPrice(target, row, prices), row.floor)
            ).length
          : 0
        const item =
          included && errors === 0 ? buildItem(target, rows, prices) : null
        return { target, rows, prices, included, errors, below, item }
      }),
    [drafts, excluded, mode, targets, t]
  )

  const items = models.flatMap((entry) => (entry.item ? [entry.item] : []))
  const errorCount = models.reduce((total, entry) => total + entry.errors, 0)
  const belowCount = models.reduce((total, entry) => total + entry.below, 0)
  const fieldCount = items.reduce(
    (total, item) => total + Object.keys(item.fields).length,
    0
  )
  const needsConfirm = belowCount > 0 || serverViolations.length > 0
  const canSubmit = !submitting && errorCount === 0 && items.length > 0

  const flaggedByModel = useMemo(() => {
    const result = new Map<string, Set<string>>()
    for (const violation of serverViolations) {
      const fields = result.get(violation.model) ?? new Set<string>()
      fields.add(violation.field)
      result.set(violation.model, fields)
    }
    return result
  }, [serverViolations])

  const handleApply = async () => {
    if (needsConfirm && !confirmed) {
      setConfirmed(true)
      return
    }
    const violations = await onSubmit(
      items,
      shouldForceApply({ confirmed, serverViolations: serverViolations.length })
    )
    if (violations.length > 0) {
      // 按分组折扣后的保本价复算仍亏损：留在弹窗里标出这些项；按钮变为「仍要应用」，
      // 再点一次即带 force 提交（改动任何价格都会清掉这次判定）。
      setServerViolations(violations)
      setConfirmed(true)
    }
  }

  const applyLabel = (() => {
    if (submitting) return t('Saving')
    if (!needsConfirm) return t('Apply new pricing')
    if (confirmed) return t('Apply anyway')
    return belowCount > 0
      ? t('{{count}} below break-even', { count: belowCount })
      : t('Apply anyway')
  })()

  const summary = (() => {
    // 行内已写明每一项的错误，底部只汇总数量，避免同一句话出现两次。
    if (errorCount > 0) {
      return t('{{count}} invalid price(s)', {
        count: errorCount,
      })
    }
    if (items.length === 0) return t('No changes to save')
    return t('Updates {{models}} model(s), {{fields}} field(s)', {
      models: items.length,
      fields: fieldCount,
    })
  })()

  const modeHints: Record<RepairMode, string> = {
    floor: t(
      'Raises each price below its break-even price to break-even; prices already above it stay.'
    ),
    official: t(
      'Sets each price to the official price; items without one keep their current price.'
    ),
    highest: t('Uses the higher of the current and highest channel price.'),
    manual: t('Enter prices; they are saved as ratios.'),
  }
  const modeLabels: Array<[RepairMode, string]> = [
    ['floor', t('Break-even price')],
    ['official', t('Official price')],
    ['highest', t('Highest price')],
    ['manual', t('Set manually')],
  ]

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('Change platform pricing')}
      description={t(
        'USD per 1M tokens; per-request models in USD per request.'
      )}
      contentClassName='sm:max-w-5xl'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            onClick={() => onOpenChange(false)}
            disabled={submitting}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='button'
            variant={needsConfirm ? 'destructive' : 'default'}
            onClick={() => void handleApply()}
            disabled={!canSubmit}
          >
            {applyLabel}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        <div className='flex flex-wrap items-center gap-x-4 gap-y-2'>
          <ToggleGroup
            value={[mode]}
            onValueChange={(values) => {
              const value = values.at(-1) as RepairMode | undefined
              if (!value) return
              setMode(value)
              resetConfirmation()
            }}
            variant='outline'
            size='sm'
            spacing={2}
          >
            {modeLabels.map(([value, label]) => (
              <ToggleGroupItem
                key={value}
                value={value}
                disabled={!modeAvailable(value, targets)}
              >
                {label}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
          <p className='text-muted-foreground text-sm'>{modeHints[mode]}</p>
        </div>

        {serverViolations.length > 0 && (
          <div className='border-warning/40 bg-warning/10 text-warning space-y-1 rounded-lg border p-3 text-sm'>
            <p>
              {t(
                'Still below cost after group discounts. Adjust them or apply anyway.'
              )}
            </p>
            <ul className='list-disc space-y-0.5 ps-5'>
              {serverViolations.map((violation) => (
                <li key={`${violation.model}-${violation.field}`}>
                  {violation.model} ·{' '}
                  {t(FIELD_LABELS[violation.field] ?? violation.field)}
                </li>
              ))}
            </ul>
          </div>
        )}

        <div className='max-h-[55vh] space-y-3 overflow-y-auto pe-1'>
          {models.map(({ target, rows, prices, included }) => (
            <ModelPriceCard
              key={target.model}
              target={target}
              rows={rows}
              mode={mode}
              drafts={drafts[target.model] ?? {}}
              prices={prices}
              flagged={flaggedByModel.get(target.model) ?? new Set()}
              included={included}
              selectable={targets.length > 1}
              onIncludedChange={(include) => {
                setExcluded((current) => {
                  const next = new Set(current)
                  if (include) next.delete(target.model)
                  else next.add(target.model)
                  return next
                })
                resetConfirmation()
              }}
              onDraftChange={(field, value) => {
                setDrafts((current) => ({
                  ...current,
                  [target.model]: { ...current[target.model], [field]: value },
                }))
                resetConfirmation()
              }}
              t={t}
            />
          ))}
        </div>

        <p
          className={cn(
            'text-sm',
            errorCount > 0 ? 'text-destructive' : 'text-muted-foreground'
          )}
        >
          {summary}
        </p>
      </div>
    </Dialog>
  )
}

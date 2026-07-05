import { useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { CalendarDays, ChevronLeft, ChevronRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { BusinessAmount } from '@/features/business/amount-display'
import { formatBusinessAmount } from '@/features/business/format'
import type {
  ApiResponse,
  CommissionCalendarDayStat,
  CommissionCalendarStats,
} from '../types'

export function currentMonthValue() {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
}

export function shiftMonthValue(value: string, offset: number) {
  const [year, month] = value.split('-').map(Number)
  const date = new Date(
    year || new Date().getFullYear(),
    (month || 1) - 1 + offset,
    1
  )
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}`
}

export function monthValueToRange(value: string) {
  const [year, month] = value.split('-').map(Number)
  const start = new Date(
    year || new Date().getFullYear(),
    (month || 1) - 1,
    1,
    0,
    0,
    0
  ).getTime()
  const end = new Date(
    year || new Date().getFullYear(),
    month || 1,
    0,
    23,
    59,
    59
  ).getTime()
  return {
    start_time: Math.floor(start / 1000),
    end_time: Math.floor(end / 1000),
  }
}

function monthValueToCalendarRange(value: string) {
  const currentMonth = currentMonthValue()
  if (value === currentMonth) {
    const now = Math.floor(Date.now() / 1000)
    return {
      start_time: now,
      end_time: now,
    }
  }

  const [year, month] = value.split('-').map(Number)
  const y = year || new Date().getFullYear()
  const m = (month || 1) - 1
  // 历史月份：start_time 只用于后端 ResolveCommissionMonthlyPeriod 识别周期，
  // end_time 只要大于任意月度周期结束时间即可，让后端以 period.PeriodEndAt 为自然上界，
  // 展示完整周期数据（前端不应截断历史数据）。
  const start = Math.floor(new Date(Date.UTC(y, m, 15, 12, 0, 0)).getTime() / 1000)
  return {
    start_time: start,
    end_time: start + 62 * 86400,
  }
}

function monthLabel(value: string) {
  const [year, month] = value.split('-').map(Number)
  return new Date(Date.UTC(year, month - 1, 1)).toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'long',
    timeZone: 'UTC',
  })
}

function toDateKey(date: Date) {
  return `${date.getUTCFullYear()}-${String(date.getUTCMonth() + 1).padStart(2, '0')}-${String(date.getUTCDate()).padStart(2, '0')}`
}

function datePartsInTimezone(date: Date, timezone?: string) {
  if (!timezone || timezone === 'Local') {
    return {
      year: date.getFullYear(),
      month: date.getMonth() + 1,
      day: date.getDate(),
    }
  }
  try {
    const parts = new Intl.DateTimeFormat('en-CA', {
      timeZone: timezone,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    }).formatToParts(date)
    const get = (type: string) =>
      Number(parts.find((part) => part.type === type)?.value)
    const year = get('year')
    const month = get('month')
    const day = get('day')
    if (year && month && day) return { year, month, day }
  } catch {
    // Fall back to local time when the browser cannot resolve the configured timezone.
  }
  return {
    year: date.getFullYear(),
    month: date.getMonth() + 1,
    day: date.getDate(),
  }
}

function calendarDateFromTimestamp(value: number, timezone?: string) {
  const date = new Date(value * 1000)
  const parts = datePartsInTimezone(date, timezone)
  return new Date(Date.UTC(parts.year, parts.month - 1, parts.day))
}

function formatCalendarDate(date: Date) {
  return date.toLocaleDateString(undefined, { timeZone: 'UTC' })
}

function periodLabel(
  startAt?: number,
  endAt?: number,
  fallback?: string,
  timezone?: string
) {
  if (!startAt || !endAt) return fallback || '-'
  const start = formatCalendarDate(calendarDateFromTimestamp(startAt, timezone))
  const end = formatCalendarDate(calendarDateFromTimestamp(endAt, timezone))
  return `${start} - ${end}`
}

function isToday(date: Date, timezone?: string) {
  const today = calendarDateFromTimestamp(
    Math.floor(Date.now() / 1000),
    timezone
  )
  return toDateKey(date) === toDateKey(today)
}

function isMonthValue(value: string) {
  return /^\d{4}-\d{2}$/.test(value)
}

function MonthValueSelector({
  value,
  onChange,
}: {
  value: string
  onChange: (value: string) => void
}) {
  const { t, i18n } = useTranslation()
  const [selectedYear, selectedMonth] = value.split('-').map(Number)
  const nowYear = new Date().getFullYear()
  const years = useMemo(() => {
    const start = Math.min(nowYear - 5, selectedYear || nowYear)
    const end = Math.max(nowYear + 1, selectedYear || nowYear)
    return Array.from({ length: end - start + 1 }, (_, index) => start + index)
  }, [nowYear, selectedYear])
  const monthFormatter = useMemo(
    () =>
      new Intl.DateTimeFormat(i18n.language || undefined, {
        month: 'short',
        timeZone: 'UTC',
      }),
    [i18n.language]
  )
  const months = useMemo(
    () =>
      Array.from({ length: 12 }, (_, index) => ({
        value: index + 1,
        label: monthFormatter.format(new Date(Date.UTC(2024, index, 1))),
      })),
    [monthFormatter]
  )

  const updateMonth = (year: string | number, month: string | number) => {
    const nextYear = Number(year) || nowYear
    const nextMonth = Number(month) || 1
    onChange(`${nextYear}-${String(nextMonth).padStart(2, '0')}`)
  }

  return (
    <div className='flex items-center gap-1.5' aria-label={t('统计月份')}>
      <Select
        items={years.map((year) => ({
          value: String(year),
          label: String(year),
        }))}
        value={String(selectedYear || nowYear)}
        onValueChange={(year) => updateMonth(year, selectedMonth)}
      >
        <SelectTrigger size='sm' className='w-[92px]'>
          <SelectValue />
        </SelectTrigger>
        <SelectContent alignItemWithTrigger={false}>
          <SelectGroup>
            {years.map((year) => (
              <SelectItem key={year} value={String(year)}>
                {year}
              </SelectItem>
            ))}
          </SelectGroup>
        </SelectContent>
      </Select>
      <Select
        items={months.map((month) => ({
          value: String(month.value),
          label: month.label,
        }))}
        value={String(selectedMonth || 1)}
        onValueChange={(month) => updateMonth(selectedYear, month)}
      >
        <SelectTrigger size='sm' className='w-[96px]'>
          <SelectValue />
        </SelectTrigger>
        <SelectContent alignItemWithTrigger={false}>
          <SelectGroup>
            {months.map((month) => (
              <SelectItem key={month.value} value={String(month.value)}>
                {month.label}
              </SelectItem>
            ))}
          </SelectGroup>
        </SelectContent>
      </Select>
    </div>
  )
}

function buildCalendarCells(
  monthValue: string,
  days: CommissionCalendarDayStat[],
  periodStartAt?: number,
  periodEndAt?: number,
  timezone?: string
) {
  const [year, month] = monthValue.split('-').map(Number)
  const periodStart = periodStartAt
    ? calendarDateFromTimestamp(periodStartAt, timezone)
    : new Date(Date.UTC(year, month - 1, 1))
  const periodEnd = periodEndAt
    ? calendarDateFromTimestamp(periodEndAt, timezone)
    : new Date(Date.UTC(year, month, 0))
  const periodStartKey = toDateKey(periodStart)
  const periodEndKey = toDateKey(periodEnd)
  const gridStart = new Date(periodStart)
  gridStart.setUTCDate(periodStart.getUTCDate() - periodStart.getUTCDay())
  const gridEnd = new Date(periodEnd)
  gridEnd.setUTCDate(gridEnd.getUTCDate() + (6 - gridEnd.getUTCDay()))
  const cellCount = Math.max(
    7,
    Math.round((gridEnd.getTime() - gridStart.getTime()) / 86400000) + 1
  )

  const dayMap = new Map(days.map((day) => [day.date, day]))
  return Array.from({ length: cellCount }, (_, index) => {
    const date = new Date(gridStart)
    date.setUTCDate(gridStart.getUTCDate() + index)
    const key = toDateKey(date)
    return {
      key,
      date,
      inPeriod: key >= periodStartKey && key <= periodEndKey,
      stat: dayMap.get(key),
    }
  })
}

function CalendarAmount({
  label,
  value,
  primary,
}: {
  label: string
  value?: number
  primary?: boolean
}) {
  return (
    <div>
      <div className='text-muted-foreground text-xs'>{label}</div>
      <div
        className={cn(
          'mt-1 font-medium',
          primary ? 'text-sm sm:text-base' : 'text-sm'
        )}
      >
        <BusinessAmount value={value ?? 0} />
      </div>
    </div>
  )
}

function commissionIntensity(amount: number, maxAbsAmount: number) {
  if (!amount || !maxAbsAmount) return 0
  const ratio = Math.abs(amount) / maxAbsAmount
  if (ratio >= 0.66) return 3
  if (ratio >= 0.33) return 2
  return 1
}

export function CommissionFinancialCalendar({
  month,
  onMonthChange,
  days,
  summary,
  periodStartAt,
  periodEndAt,
  periodBoundaryAt,
  timezone,
  isLoading,
  toolbar,
  showSummaryCards = true,
  overrideCommissionRate,
}: {
  month: string
  onMonthChange: (month: string) => void
  days: CommissionCalendarDayStat[]
  summary?: {
    revenue_quota?: number
    profit_quota?: number
    commission_quota?: number
    recalc_commission_quota?: number
    record_count?: number
  }
  periodStartAt?: number
  periodEndAt?: number
  periodBoundaryAt?: number
  timezone?: string
  isLoading?: boolean
  toolbar?: ReactNode
  showSummaryCards?: boolean
  /** 前端直接计算提成时传入当前等级费率，优先级高于后端返回的 recalc_commission_quota */
  overrideCommissionRate?: number
}) {
  const { t } = useTranslation()
  // periodBoundaryAt 是下一周期起点（独占上界，= periodEndAt + 1s）。若直接拿它转成自然日
  // 再做闭区间比较，会把"下一周期第一天"误判为本期（如自然月的 8/1、重置日模式的重置日当天）。
  // 用 periodEndAt（本期最后一秒）做闭区间上界；缺省时用 boundary-1s 回退到本期最后一刻。
  const visualPeriodEndAt =
    periodEndAt ?? (periodBoundaryAt ? periodBoundaryAt - 1 : undefined)
  const cells = useMemo(
    () =>
      buildCalendarCells(
        month,
        days,
        periodStartAt,
        visualPeriodEndAt,
        timezone
      ),
    [days, month, periodStartAt, visualPeriodEndAt, timezone]
  )
  const currentMonth = currentMonthValue()
  const [selectedDate, setSelectedDate] = useState<string>()
  const selectedCell = cells.find((cell) => cell.key === selectedDate)
  const selectedStat = selectedCell?.stat
  const maxAbsRevenue = useMemo(
    () =>
      days.reduce(
        (max, day) => Math.max(max, Math.abs(day.profit_quota || 0)),
        0
      ),
    [days]
  )
  const weekdays = [
    t('Sun'),
    t('Mon'),
    t('Tue'),
    t('Wed'),
    t('Thu'),
    t('Fri'),
    t('Sat'),
  ]

  useEffect(() => {
    const selectedInMonth = cells.some(
      (cell) => cell.key === selectedDate && cell.inPeriod
    )
    if (selectedInMonth) return
    const today = cells.find(
      (cell) => cell.inPeriod && isToday(cell.date, timezone)
    )
    const firstStat = cells.find((cell) => cell.inPeriod && cell.stat)
    const firstDay = cells.find((cell) => cell.inPeriod)
    setSelectedDate((today || firstStat || firstDay)?.key)
  }, [cells, selectedDate])

  const selectMonth = (value: string) => {
    onMonthChange(isMonthValue(value) ? value : currentMonthValue())
  }

  return (
    <div className='space-y-4'>
      <div className='bg-muted/30 flex flex-wrap items-center justify-between gap-3 rounded-md p-3'>
        <div className='flex min-w-0 flex-wrap items-center gap-2'>
          <Button
            type='button'
            variant='outline'
            size='icon'
            onClick={() => onMonthChange(shiftMonthValue(month, -1))}
            aria-label={t('Previous month')}
          >
            <ChevronLeft className='h-4 w-4' />
          </Button>
          <MonthValueSelector value={month} onChange={selectMonth} />
          <Button
            type='button'
            variant='outline'
            size='icon'
            onClick={() => onMonthChange(shiftMonthValue(month, 1))}
            aria-label={t('Next month')}
          >
            <ChevronRight className='h-4 w-4' />
          </Button>
          <Button
            type='button'
            variant={month === currentMonth ? 'secondary' : 'outline'}
            size='sm'
            onClick={() => onMonthChange(currentMonth)}
          >
            {t('Today')}
          </Button>
        </div>
        {toolbar ? (
          <div className='flex min-w-0 flex-1 justify-end'>{toolbar}</div>
        ) : null}
      </div>

      {showSummaryCards ? (
        <div className='grid auto-rows-fr gap-3 md:grid-cols-4'>
          <div className='border-border bg-card flex min-h-[112px] flex-col justify-between rounded-md border p-4 shadow-xs md:col-span-2'>
            <div>
              <div className='text-muted-foreground mb-1 flex items-center gap-2 text-sm'>
                <CalendarDays className='h-4 w-4' />
                {periodLabel(
                  periodStartAt,
                  visualPeriodEndAt,
                  monthLabel(month),
                  timezone
                )}
              </div>
              <div className='text-xl font-semibold sm:text-2xl'>
                {isLoading ? (
                  <Skeleton className='h-8 w-36' />
                ) : (
                  <BusinessAmount value={summary?.profit_quota ?? 0} />
                )}
              </div>
            </div>
            <div className='text-muted-foreground mt-2 text-sm'>
              {t('Current Period Performance')}
            </div>
          </div>
          <div className='border-border bg-card flex min-h-[112px] flex-col rounded-md border p-4 shadow-xs'>
            <div className='text-muted-foreground text-sm'>
              {t('Current Period Commission')}
            </div>
            <div className='mt-1 text-base font-semibold sm:text-lg'>
              {isLoading ? (
                <Skeleton className='h-6 w-28' />
              ) : (
                <BusinessAmount
                  value={
                    overrideCommissionRate != null && overrideCommissionRate > 0
                      ? Math.round((summary?.profit_quota ?? 0) * overrideCommissionRate)
                      : (summary?.recalc_commission_quota ?? summary?.commission_quota ?? 0)
                  }
                />
              )}
            </div>
          </div>
          <div className='border-border bg-card flex min-h-[112px] flex-col rounded-md border p-4 shadow-xs'>
            <div className='text-muted-foreground text-sm'>{t('Records')}</div>
            <div className='mt-1 text-base font-semibold sm:text-lg'>
              {isLoading ? (
                <Skeleton className='h-6 w-16' />
              ) : (
                summary?.record_count || 0
              )}
            </div>
          </div>
        </div>
      ) : null}

      <div className='border-border bg-card overflow-hidden rounded-md border shadow-sm'>
        <div className='bg-muted/50 text-muted-foreground grid grid-cols-7 border-b text-center text-[11px] font-semibold tracking-wider uppercase'>
          {weekdays.map((day) => (
            <div key={day} className='px-2 py-2.5'>
              {day}
            </div>
          ))}
        </div>
        <div className='grid grid-cols-7'>
          {cells.map(({ key, date, inPeriod, stat }) => {
            const profit = stat?.profit_quota ?? 0
            const selected = key === selectedDate
            const intensity = commissionIntensity(profit, maxAbsRevenue)
            const positive = profit >= 0
            return (
              <button
                key={key}
                type='button'
                aria-pressed={selected}
                title={
                  stat
                    ? `${key} ${formatBusinessAmount(profit)} (${stat.record_count || 0})`
                    : key
                }
                onClick={() => setSelectedDate(key)}
                className={cn(
                  'group relative flex min-h-[82px] flex-col border-r border-b p-2.5 text-left transition-all outline-none sm:min-h-[104px] [&:nth-child(7n)]:border-r-0',
                  'border-border/70 focus-visible:ring-ring hover:z-10 hover:-translate-y-px hover:shadow-md focus-visible:ring-2 focus-visible:ring-inset',
                  !inPeriod && 'bg-muted/20 text-muted-foreground opacity-60',
                  inPeriod && !stat && 'bg-card hover:bg-muted/40',
                  inPeriod &&
                    stat &&
                    positive &&
                    intensity === 1 &&
                    'bg-emerald-50/70 hover:bg-emerald-50 dark:bg-emerald-950/15 dark:hover:bg-emerald-950/25',
                  inPeriod &&
                    stat &&
                    positive &&
                    intensity === 2 &&
                    'bg-emerald-100/80 hover:bg-emerald-100 dark:bg-emerald-950/30 dark:hover:bg-emerald-950/40',
                  inPeriod &&
                    stat &&
                    positive &&
                    intensity === 3 &&
                    'bg-emerald-200/80 hover:bg-emerald-200 dark:bg-emerald-900/45 dark:hover:bg-emerald-900/55',
                  inPeriod &&
                    stat &&
                    !positive &&
                    intensity === 1 &&
                    'bg-red-50/70 hover:bg-red-50 dark:bg-red-950/15 dark:hover:bg-red-950/25',
                  inPeriod &&
                    stat &&
                    !positive &&
                    intensity === 2 &&
                    'bg-red-100/80 hover:bg-red-100 dark:bg-red-950/30 dark:hover:bg-red-950/40',
                  inPeriod &&
                    stat &&
                    !positive &&
                    intensity === 3 &&
                    'bg-red-200/80 hover:bg-red-200 dark:bg-red-900/45 dark:hover:bg-red-900/55',
                  selected && 'ring-primary z-20 ring-2 ring-inset'
                )}
              >
                <div className='flex items-center justify-between gap-1'>
                  <span
                    className={cn(
                      'flex size-6 items-center justify-center rounded-full text-xs font-semibold',
                      isToday(date, timezone) &&
                        'bg-primary text-primary-foreground shadow-sm',
                      selected &&
                        !isToday(date, timezone) &&
                        'bg-primary/10 text-primary'
                    )}
                  >
                    {date.getUTCDate()}
                  </span>
                  {stat?.record_count ? (
                    <span className='bg-background/80 text-muted-foreground rounded-full px-1.5 py-0.5 text-[10px] font-medium shadow-xs'>
                      {stat.record_count}
                    </span>
                  ) : null}
                </div>
                {stat ? (
                  <div
                    className={cn(
                      'mt-auto flex min-w-0 flex-wrap items-center gap-1 pt-2 text-xs font-bold sm:text-sm',
                      positive
                        ? 'text-emerald-700 dark:text-emerald-300'
                        : 'text-red-700 dark:text-red-300'
                    )}
                  >
                    <span className='min-w-0 truncate'>
                      {formatBusinessAmount(profit)}
                    </span>
                    {!positive ? (
                      <Badge
                        variant='outline'
                        className='border-red-400/40 bg-red-50/80 px-1 py-0 text-[10px] text-red-700 dark:bg-red-950/40 dark:text-red-300'
                      >
                        {t('Loss')}
                      </Badge>
                    ) : null}
                  </div>
                ) : null}
              </button>
            )
          })}
        </div>
      </div>
    </div>
  )
}

export function CommissionCalendarSection({
  queryKey,
  queryFn,
  toolbar,
  showSummaryCards,
  month: controlledMonth,
  onMonthChange,
  overrideCommissionRate,
}: {
  queryKey: readonly unknown[]
  queryFn: (
    range: ReturnType<typeof monthValueToCalendarRange>
  ) => Promise<ApiResponse<CommissionCalendarStats>>
  toolbar?: ReactNode
  showSummaryCards?: boolean
  month?: string
  onMonthChange?: (month: string) => void
  /** 前端直接计算提成时传入当前等级费率，优先级高于后端返回的 recalc_commission_quota */
  overrideCommissionRate?: number
}) {
  const [innerMonth, setInnerMonth] = useState(currentMonthValue)
  const month = controlledMonth ?? innerMonth
  const setMonth = onMonthChange ?? setInnerMonth
  const range = useMemo(() => monthValueToCalendarRange(month), [month])
  const { data, isLoading, isFetching } = useQuery({
    queryKey: [...queryKey, month],
    queryFn: () => queryFn(range),
  })

  return (
    <CommissionFinancialCalendar
      month={month}
      onMonthChange={setMonth}
      days={data?.data?.days ?? []}
      summary={data?.data?.summary}
      periodStartAt={data?.data?.period_start_at}
      periodEndAt={data?.data?.period_end_at}
      periodBoundaryAt={data?.data?.period_boundary_at}
      timezone={data?.data?.timezone}
      isLoading={isLoading || isFetching}
      toolbar={toolbar}
      showSummaryCards={showSummaryCards}
      overrideCommissionRate={overrideCommissionRate}
    />
  )
}

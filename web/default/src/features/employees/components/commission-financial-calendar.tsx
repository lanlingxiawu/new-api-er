import { useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { CalendarDays, ChevronLeft, ChevronRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { formatBusinessAmount } from '@/features/business/format'
import type {
  ApiResponse,
  CommissionCalendarDayStat,
  CommissionCalendarStats,
  CommissionMonthlyStatItem,
  PagedResponse,
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
  const start = new Date(year, month - 1, 1, 0, 0, 0)
  const end = new Date(year, month, 0, 23, 59, 59)
  return {
    start_time: Math.floor(start.getTime() / 1000),
    end_time: Math.floor(end.getTime() / 1000),
  }
}

function monthLabel(value: string) {
  const [year, month] = value.split('-').map(Number)
  return new Date(year, month - 1, 1).toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'long',
  })
}

function toDateKey(date: Date) {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

function isToday(date: Date) {
  const now = new Date()
  return toDateKey(date) === toDateKey(now)
}

function isMonthValue(value: string) {
  return /^\d{4}-\d{2}$/.test(value)
}

function buildCalendarCells(
  monthValue: string,
  days: CommissionCalendarDayStat[]
) {
  const [year, month] = monthValue.split('-').map(Number)
  const first = new Date(year, month - 1, 1)
  const gridStart = new Date(first)
  gridStart.setDate(first.getDate() - first.getDay())

  const dayMap = new Map(days.map((day) => [day.date, day]))
  return Array.from({ length: 42 }, (_, index) => {
    const date = new Date(gridStart)
    date.setDate(gridStart.getDate() + index)
    const key = toDateKey(date)
    return {
      key,
      date,
      inMonth: date.getMonth() === month - 1,
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
          'mt-1 truncate font-medium',
          primary ? 'text-base' : 'text-sm'
        )}
      >
        {formatBusinessAmount(value ?? 0)}
      </div>
    </div>
  )
}

export function CommissionFinancialCalendar({
  month,
  onMonthChange,
  days,
  summary,
  isLoading,
  toolbar,
  showSummaryCards = true,
  showSelectedDetail = true,
}: {
  month: string
  onMonthChange: (month: string) => void
  days: CommissionCalendarDayStat[]
  summary?: {
    revenue_quota?: number
    profit_quota?: number
    commission_quota?: number
    record_count?: number
  }
  isLoading?: boolean
  toolbar?: ReactNode
  showSummaryCards?: boolean
  showSelectedDetail?: boolean
}) {
  const { t } = useTranslation()
  const cells = useMemo(() => buildCalendarCells(month, days), [month, days])
  const currentMonth = currentMonthValue()
  const [selectedDate, setSelectedDate] = useState<string>()
  const selectedCell = cells.find((cell) => cell.key === selectedDate)
  const selectedStat = selectedCell?.stat
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
      (cell) => cell.key === selectedDate && cell.inMonth
    )
    if (selectedInMonth) return
    const today = cells.find((cell) => cell.inMonth && isToday(cell.date))
    const firstStat = cells.find((cell) => cell.inMonth && cell.stat)
    const firstDay = cells.find((cell) => cell.inMonth)
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
          <Input
            type='month'
            value={month}
            aria-label={t('Month number')}
            title={t('Month number')}
            onChange={(event) => selectMonth(event.target.value)}
            className='w-[150px]'
          />
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
                {monthLabel(month)}
              </div>
              <div className='text-2xl font-semibold'>
                {isLoading ? (
                  <Skeleton className='h-8 w-36' />
                ) : (
                  formatBusinessAmount(summary?.commission_quota ?? 0)
                )}
              </div>
            </div>
            <div className='text-muted-foreground mt-2 text-sm'>
              {t('Monthly Commission')}
            </div>
          </div>
          <div className='border-border bg-card flex min-h-[112px] flex-col rounded-md border p-4 shadow-xs'>
            <div className='text-muted-foreground text-sm'>
              {t('Monthly Performance')}
            </div>
            <div className='mt-1 text-lg font-semibold'>
              {isLoading ? (
                <Skeleton className='h-6 w-28' />
              ) : (
                formatBusinessAmount(summary?.profit_quota ?? 0)
              )}
            </div>
          </div>
          <div className='border-border bg-card flex min-h-[112px] flex-col rounded-md border p-4 shadow-xs'>
            <div className='text-muted-foreground text-sm'>{t('Records')}</div>
            <div className='mt-1 text-lg font-semibold'>
              {isLoading ? (
                <Skeleton className='h-6 w-16' />
              ) : (
                summary?.record_count || 0
              )}
            </div>
          </div>
        </div>
      ) : null}

      <div className='border-border bg-card overflow-hidden rounded-md border shadow-xs'>
        <div className='bg-muted/40 grid grid-cols-7 border-b text-center text-xs font-medium'>
          {weekdays.map((day) => (
            <div key={day} className='text-muted-foreground px-2 py-2'>
              {day}
            </div>
          ))}
        </div>
        <div className='grid grid-cols-7'>
          {cells.map(({ key, date, inMonth, stat }) => {
            const commission = stat?.commission_quota ?? 0
            const selected = key === selectedDate
            return (
              <button
                key={key}
                type='button'
                aria-pressed={selected}
                onClick={() => setSelectedDate(key)}
                className={cn(
                  'border-border/70 min-h-[72px] border-r border-b p-2 text-left transition-colors outline-none last:border-r-0 sm:min-h-[88px]',
                  'hover:bg-muted/50 focus-visible:ring-ring focus-visible:ring-2 focus-visible:ring-inset',
                  !inMonth && 'bg-muted/20 text-muted-foreground',
                  inMonth &&
                    commission > 0 &&
                    'bg-emerald-50/70 dark:bg-emerald-950/20',
                  inMonth &&
                    commission < 0 &&
                    'bg-red-50/70 dark:bg-red-950/20',
                  selected && 'ring-primary/70 ring-2 ring-inset'
                )}
              >
                <div className='flex items-center justify-between gap-1'>
                  <span
                    className={cn(
                      'flex size-5 items-center justify-center rounded-full text-xs font-medium',
                      isToday(date) && 'bg-primary text-primary-foreground'
                    )}
                  >
                    {date.getDate()}
                  </span>
                  {stat?.record_count ? (
                    <span className='text-muted-foreground text-[10px]'>
                      {stat.record_count}
                    </span>
                  ) : null}
                </div>
                {stat ? (
                  <div
                    className={cn(
                      'mt-2 truncate text-xs font-semibold sm:text-sm',
                      commission >= 0
                        ? 'text-emerald-700 dark:text-emerald-300'
                        : 'text-red-700 dark:text-red-300'
                    )}
                  >
                    {formatBusinessAmount(commission)}
                  </div>
                ) : null}
              </button>
            )
          })}
        </div>
      </div>

      {showSelectedDetail ? (
        <div className='border-border bg-card rounded-md border p-4 shadow-xs'>
          <div className='mb-3 flex items-center gap-2 text-sm'>
            <CalendarDays className='text-muted-foreground h-4 w-4' />
            <span className='text-muted-foreground'>
              {selectedCell?.date.toLocaleDateString() || monthLabel(month)}
            </span>
          </div>
          <div className='grid gap-3 sm:grid-cols-3 lg:grid-cols-5'>
            <CalendarAmount
              label={t('Commission')}
              value={selectedStat?.commission_quota}
              primary
            />
            <CalendarAmount
              label={t('Profit')}
              value={selectedStat?.profit_quota}
            />
            <CalendarAmount
              label={t('Revenue')}
              value={selectedStat?.revenue_quota}
            />
            <CalendarAmount
              label={t('Cost')}
              value={selectedStat?.cost_quota}
            />
            <div>
              <div className='text-muted-foreground text-xs'>
                {t('Records')}
              </div>
              <div className='mt-1 text-sm font-medium'>
                {selectedStat?.record_count || 0}
              </div>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  )
}

export function CommissionCalendarSection({
  queryKey,
  queryFn,
  toolbar,
  showSummaryCards,
  showSelectedDetail,
}: {
  queryKey: readonly unknown[]
  queryFn: (
    range: ReturnType<typeof monthValueToRange>
  ) => Promise<ApiResponse<CommissionCalendarStats>>
  toolbar?: ReactNode
  showSummaryCards?: boolean
  showSelectedDetail?: boolean
}) {
  const [month, setMonth] = useState(currentMonthValue)
  const range = useMemo(() => monthValueToRange(month), [month])
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
      isLoading={isLoading || isFetching}
      toolbar={toolbar}
      showSummaryCards={showSummaryCards}
      showSelectedDetail={showSelectedDetail}
    />
  )
}

function unixTimeLabel(value?: number) {
  if (!value) return '-'
  return new Date(value * 1000).toLocaleString()
}

function periodSummary(items: CommissionMonthlyStatItem[]) {
  return items.reduce(
    (summary, item) => ({
      revenue_quota: summary.revenue_quota + (item.revenue_quota || 0),
      cost_quota: summary.cost_quota + (item.cost_quota || 0),
      profit_quota: summary.profit_quota + (item.profit_quota || 0),
      commission_quota:
        summary.commission_quota + (item.commission_quota || 0),
      record_count: summary.record_count + (item.record_count || 0),
    }),
    {
      revenue_quota: 0,
      cost_quota: 0,
      profit_quota: 0,
      commission_quota: 0,
      record_count: 0,
    }
  )
}

export function CommissionMonthlyPeriodSection({
  queryKey,
  queryFn,
  toolbar,
}: {
  queryKey: readonly unknown[]
  queryFn: (
    range: ReturnType<typeof monthValueToRange>
  ) => Promise<PagedResponse<CommissionMonthlyStatItem>>
  toolbar?: ReactNode
}) {
  const { t } = useTranslation()
  const [month, setMonth] = useState(currentMonthValue)
  const range = useMemo(() => monthValueToRange(month), [month])
  const { data, isLoading, isFetching } = useQuery({
    queryKey: [...queryKey, month],
    queryFn: () => queryFn(range),
  })
  const items = data?.data?.items ?? []
  const summary = useMemo(() => periodSummary(items), [items])
  const loading = isLoading || isFetching
  const currentMonth = currentMonthValue()

  return (
    <div className='space-y-4'>
      <div className='bg-muted/30 flex flex-wrap items-center justify-between gap-3 rounded-md p-3'>
        <div className='flex min-w-0 flex-wrap items-center gap-2'>
          <Button
            type='button'
            variant='outline'
            size='icon'
            onClick={() => setMonth(shiftMonthValue(month, -1))}
            aria-label={t('Previous month')}
          >
            <ChevronLeft className='h-4 w-4' />
          </Button>
          <Input
            type='month'
            value={month}
            aria-label={t('Month number')}
            title={t('Month number')}
            onChange={(event) =>
              setMonth(
                isMonthValue(event.target.value)
                  ? event.target.value
                  : currentMonthValue()
              )
            }
            className='w-[150px]'
          />
          <Button
            type='button'
            variant='outline'
            size='icon'
            onClick={() => setMonth(shiftMonthValue(month, 1))}
            aria-label={t('Next month')}
          >
            <ChevronRight className='h-4 w-4' />
          </Button>
          <Button
            type='button'
            variant={month === currentMonth ? 'secondary' : 'outline'}
            size='sm'
            onClick={() => setMonth(currentMonth)}
          >
            {t('Today')}
          </Button>
        </div>
        {toolbar ? (
          <div className='flex min-w-0 flex-1 justify-end'>{toolbar}</div>
        ) : null}
      </div>

      <div className='grid auto-rows-fr gap-3 md:grid-cols-4'>
        <div className='border-border bg-card flex min-h-[112px] flex-col justify-between rounded-md border p-4 shadow-xs md:col-span-2'>
          <div>
            <div className='text-muted-foreground mb-1 flex items-center gap-2 text-sm'>
              <CalendarDays className='h-4 w-4' />
              {monthLabel(month)}
            </div>
            <div className='text-2xl font-semibold'>
              {loading ? (
                <Skeleton className='h-8 w-36' />
              ) : (
                formatBusinessAmount(summary.commission_quota)
              )}
            </div>
          </div>
          <div className='text-muted-foreground mt-2 text-sm'>
            {t('Monthly Commission')}
          </div>
        </div>
        <div className='border-border bg-card flex min-h-[112px] flex-col rounded-md border p-4 shadow-xs'>
          <div className='text-muted-foreground text-sm'>
            {t('Monthly Performance')}
          </div>
          <div className='mt-1 text-lg font-semibold'>
            {loading ? (
              <Skeleton className='h-6 w-28' />
            ) : (
              formatBusinessAmount(summary.profit_quota)
            )}
          </div>
        </div>
        <div className='border-border bg-card flex min-h-[112px] flex-col rounded-md border p-4 shadow-xs'>
          <div className='text-muted-foreground text-sm'>{t('Records')}</div>
          <div className='mt-1 text-lg font-semibold'>
            {loading ? <Skeleton className='h-6 w-16' /> : summary.record_count}
          </div>
        </div>
      </div>

      <div className='grid gap-3 lg:grid-cols-2'>
        {loading
          ? Array.from({ length: 2 }).map((_, index) => (
              <div
                key={index}
                className='border-border bg-card rounded-md border p-4 shadow-xs'
              >
                <Skeleton className='h-5 w-44' />
                <Skeleton className='mt-4 h-8 w-36' />
                <Skeleton className='mt-4 h-4 w-full' />
              </div>
            ))
          : items.map((item) => (
              <div
                key={`${item.period_start_at}-${item.employee_user_id}`}
                className='border-border bg-card rounded-md border p-4 shadow-xs'
              >
                <div className='flex flex-wrap items-start justify-between gap-3'>
                  <div>
                    <div className='font-medium'>
                      {item.period_key || t('Period')}
                    </div>
                    <div className='text-muted-foreground mt-1 text-xs'>
                      {unixTimeLabel(item.period_start_at)} -{' '}
                      {unixTimeLabel(item.period_end_at)}
                    </div>
                  </div>
                  <div className='text-right'>
                    <div className='text-lg font-semibold'>
                      {formatBusinessAmount(item.commission_quota)}
                    </div>
                    <div className='text-muted-foreground text-xs'>
                      {t('Commission')}
                    </div>
                  </div>
                </div>
                <div className='mt-4 grid gap-3 text-sm sm:grid-cols-4'>
                  <CalendarAmount
                    label={t('Profit')}
                    value={item.profit_quota}
                  />
                  <CalendarAmount
                    label={t('Revenue')}
                    value={item.revenue_quota}
                  />
                  <CalendarAmount label={t('Cost')} value={item.cost_quota} />
                  <div>
                    <div className='text-muted-foreground text-xs'>
                      {t('Records')}
                    </div>
                    <div className='mt-1 font-medium'>
                      {item.record_count || 0}
                    </div>
                  </div>
                </div>
                {!queryKey.includes('my-commission-monthly-stats') ? (
                  <div className='text-muted-foreground mt-3 text-xs'>
                    {t('Employee UID')}: {item.employee_user_id}
                  </div>
                ) : null}
              </div>
            ))}
      </div>

      {!loading && items.length === 0 ? (
        <div className='border-border bg-card text-muted-foreground rounded-md border p-6 text-center text-sm shadow-xs'>
          {t('No data')}
        </div>
      ) : null}
    </div>
  )
}

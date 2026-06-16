import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ComponentType,
  type ReactNode,
  type UIEvent,
} from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  TrendingUp,
  TrendingDown,
  DollarSign,
  Wallet,
  PiggyBank,
  BadgeDollarSign,
  Percent,
  ArrowUp,
  ArrowDown,
  ArrowUpDown,
  RefreshCw,
  CalendarDays,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import dayjs from '@/lib/dayjs'
import { getEndOfDay, getStartOfDay } from '@/lib/time'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { SectionPageLayout } from '@/components/layout'
import { BusinessAmount } from '@/features/business/amount-display'
import { formatBusinessUsd } from '@/features/business/format'
import {
  getCommissionOverview,
  type ChannelProfitStat,
  type EmployeeStat,
} from './api'

// Range options
type RangeKey = '1d' | '7d' | '30d' | '90d' | 'all' | 'custom'

interface OverviewRange {
  start?: Date
  end?: Date
}

function createTrailingDayRange(days: number): OverviewRange {
  const end = getEndOfDay()
  const start = new Date(end)
  start.setDate(end.getDate() - (days - 1))
  return { start: getStartOfDay(start), end }
}

function getPresetRange(range: Exclude<RangeKey, 'custom'>): OverviewRange {
  if (range === '1d') return createTrailingDayRange(1)
  if (range === '7d') return createTrailingDayRange(7)
  if (range === '30d') return createTrailingDayRange(30)
  if (range === '90d') return createTrailingDayRange(90)
  return {}
}

function rangesMatch(a: OverviewRange, b: OverviewRange): boolean {
  const startMatches = a.start?.getTime() === b.start?.getTime()
  const endMatches = a.end?.getTime() === b.end?.getTime()
  return startMatches && endMatches
}

function resolveRangeKey(range: OverviewRange): RangeKey {
  if (!range.start && !range.end) return 'all'
  if (rangesMatch(range, getPresetRange('1d'))) return '1d'
  if (rangesMatch(range, getPresetRange('7d'))) return '7d'
  if (rangesMatch(range, getPresetRange('30d'))) return '30d'
  if (rangesMatch(range, getPresetRange('90d'))) return '90d'
  return 'custom'
}

function toUnixTimestamp(date?: Date): number | undefined {
  if (!date) return undefined
  return Math.floor(date.getTime() / 1000)
}

function toDateInputValue(date?: Date): string {
  return date ? dayjs(date).format('YYYY-MM-DD') : ''
}

function fromDateInputValue(value: string, boundary: 'start' | 'end') {
  if (!value) return undefined
  const date = new Date(`${value}T00:00:00`)
  if (Number.isNaN(date.getTime())) return undefined
  return boundary === 'start' ? getStartOfDay(date) : getEndOfDay(date)
}

function sortChannelRows(
  rows: ChannelProfitStat[],
  sort: { key: string; dir: 'asc' | 'desc' } | null
): ChannelProfitStat[] {
  if (!sort) return rows
  const { key, dir } = sort
  const sorted = [...rows].sort((a, b) => {
    const cmp =
      key === 'channel_name'
        ? (a.channel_name ?? '').localeCompare(b.channel_name ?? '')
        : (a[key as keyof ChannelProfitStat] as number) -
          (b[key as keyof ChannelProfitStat] as number)
    return dir === 'asc' ? cmp : -cmp
  })
  return sorted
}

const CLASSIC_TABLE_SCROLL_HEIGHT = 223
const OVERVIEW_TABLE_PAGE_SIZE = 10
const EMPLOYEE_PERFORMANCE_TOP_LIMIT = 10

function ScrollTable({
  children,
  hasMore,
  onLoadMore,
  loading,
}: {
  children: ReactNode
  hasMore?: boolean
  onLoadMore?: () => void
  loading?: boolean
}) {
  const rootRef = useRef<HTMLDivElement | null>(null)
  const sentinelRef = useRef<HTMLDivElement | null>(null)
  const requestLoadMore = useCallback(() => {
    if (!hasMore || loading || !onLoadMore) return
    onLoadMore()
  }, [hasMore, loading, onLoadMore])

  const handleScroll = useCallback(
    (event: UIEvent<HTMLDivElement>) => {
      const target = event.currentTarget
      const distanceToBottom =
        target.scrollHeight - target.scrollTop - target.clientHeight
      if (distanceToBottom <= 24) {
        requestLoadMore()
      }
    },
    [requestLoadMore]
  )

  useEffect(() => {
    const root = rootRef.current
    const sentinel = sentinelRef.current
    if (!root || !sentinel) return

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry?.isIntersecting) {
          requestLoadMore()
        }
      },
      {
        root,
        rootMargin: '48px 0px',
        threshold: 0,
      }
    )

    observer.observe(sentinel)
    return () => observer.disconnect()
  }, [requestLoadMore])

  return (
    <div
      ref={rootRef}
      className='w-full overflow-auto rounded-xl border'
      style={{ height: CLASSIC_TABLE_SCROLL_HEIGHT }}
      onScroll={handleScroll}
    >
      {children}
      <div ref={sentinelRef} className='h-px w-full' aria-hidden='true' />
    </div>
  )
}

// Stat card

function StatCard({
  title,
  value,
  sub,
  icon: Icon,
}: {
  title: string
  value: string
  sub?: string
  icon: ComponentType<{ className?: string }>
}) {
  return (
    <div className='min-w-0 px-3 py-3 sm:px-5 sm:py-4'>
      <div className='flex items-center gap-2'>
        <Icon className='text-muted-foreground/60 size-3.5 shrink-0' />
        <div className='text-muted-foreground truncate text-xs font-medium tracking-wider uppercase'>
          {title}
        </div>
      </div>
      <div className='text-foreground mt-1.5 font-mono text-base font-bold tracking-tight break-all tabular-nums sm:mt-2 sm:text-xl'>
        {value}
      </div>
      {sub ? (
        <div className='text-muted-foreground/60 mt-1 truncate text-xs'>
          {sub}
        </div>
      ) : null}
    </div>
  )
}

function AmountStatCard({
  title,
  value,
  sub,
  icon: Icon,
}: {
  title: string
  value: number | null | undefined
  sub?: string
  icon: ComponentType<{ className?: string }>
}) {
  return (
    <div className='min-w-0 px-3 py-3 sm:px-5 sm:py-4'>
      <div className='flex items-center gap-2'>
        <Icon className='text-muted-foreground/60 size-3.5 shrink-0' />
        <div className='text-muted-foreground truncate text-xs font-medium tracking-wider uppercase'>
          {title}
        </div>
      </div>
      <div className='text-foreground mt-1.5 font-mono text-base font-bold tracking-tight tabular-nums sm:mt-2 sm:text-xl'>
        <BusinessAmount value={value} />
      </div>
      {sub ? (
        <div className='text-muted-foreground/60 mt-1 truncate text-xs'>
          {sub}
        </div>
      ) : null}
    </div>
  )
}

function SortableHead({
  children,
  sortKey,
  currentSort,
  onSort,
}: {
  children: ReactNode
  sortKey: string
  currentSort: { key: string; dir: 'asc' | 'desc' } | null
  onSort: (key: string) => void
}) {
  const isActive = currentSort?.key === sortKey
  return (
    <button
      className='hover:text-foreground flex items-center gap-1 transition-colors'
      onClick={() => onSort(sortKey)}
    >
      {children}
      {isActive && currentSort!.dir === 'desc' ? (
        <ArrowDown className='size-3' />
      ) : isActive && currentSort!.dir === 'asc' ? (
        <ArrowUp className='size-3' />
      ) : (
        <ArrowUpDown className='size-3 opacity-40' />
      )}
    </button>
  )
}

function CompactDateRangePicker({
  start,
  end,
  onChange,
}: {
  start?: Date
  end?: Date
  onChange: (range: OverviewRange) => void
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [draftStart, setDraftStart] = useState(toDateInputValue(start))
  const [draftEnd, setDraftEnd] = useState(toDateInputValue(end))

  const label = useMemo(() => {
    if (!start && !end) return t('Date Range')
    const startText = start ? toDateInputValue(start) : '-'
    const endText = end ? toDateInputValue(end) : '-'
    return `${startText} ~ ${endText}`
  }, [end, start, t])

  const handleOpenChange = (nextOpen: boolean) => {
    if (nextOpen) {
      setDraftStart(toDateInputValue(start))
      setDraftEnd(toDateInputValue(end))
    }
    setOpen(nextOpen)
  }

  const applyDraft = () => {
    onChange({
      start: fromDateInputValue(draftStart, 'start'),
      end: fromDateInputValue(draftEnd, 'end'),
    })
    setOpen(false)
  }

  return (
    <Popover open={open} onOpenChange={handleOpenChange}>
      <PopoverTrigger
        render={
          <Button
            type='button'
            variant='outline'
            className='w-full justify-start gap-2 px-2.5 text-sm leading-5 font-normal tabular-nums'
          />
        }
      >
        <CalendarDays className='text-muted-foreground size-4 shrink-0' />
        <span className='truncate'>{label}</span>
      </PopoverTrigger>
      <PopoverContent
        align='start'
        className='w-[min(420px,calc(100vw-2rem))] p-3'
      >
        <div className='space-y-3'>
          <div className='grid gap-2 sm:grid-cols-[1fr_auto_1fr] sm:items-end'>
            <div className='space-y-1.5'>
              <div className='text-muted-foreground text-xs'>
                {t('Start Date')}
              </div>
              <Input
                type='date'
                value={draftStart}
                onChange={(e) => setDraftStart(e.target.value)}
                className='h-8 text-sm leading-5 tabular-nums'
              />
            </div>
            <span className='text-muted-foreground hidden pb-2 text-xs sm:block'>
              ~
            </span>
            <div className='space-y-1.5'>
              <div className='text-muted-foreground text-xs'>
                {t('End Date')}
              </div>
              <Input
                type='date'
                value={draftEnd}
                onChange={(e) => setDraftEnd(e.target.value)}
                className='h-8 text-sm leading-5 tabular-nums'
              />
            </div>
          </div>

          <div className='flex justify-end'>
            <Button size='sm' className='h-8' onClick={applyDraft}>
              {t('Confirm')}
            </Button>
          </div>
        </div>
      </PopoverContent>
    </Popover>
  )
}

function StatPanel({
  children,
  columnsClassName,
}: {
  children: ReactNode
  columnsClassName: string
}) {
  return (
    <div className='overflow-hidden rounded-lg border'>
      <div className={`divide-border/60 grid divide-x ${columnsClassName}`}>
        {children}
      </div>
    </div>
  )
}

function SectionTitle({
  children,
  description,
}: {
  children: ReactNode
  description?: ReactNode
}) {
  return (
    <div className='space-y-1'>
      <h3 className='text-sm font-semibold'>{children}</h3>
      {description ? (
        <p className='text-muted-foreground text-xs'>{description}</p>
      ) : null}
    </div>
  )
}

function BusinessSection({
  title,
  description,
  children,
}: {
  title: ReactNode
  description?: ReactNode
  children: ReactNode
}) {
  return (
    <div className='border-t pt-4'>
      <div className='mb-3 flex flex-col gap-1'>
        <div className='text-sm font-semibold'>{title}</div>
        {description ? (
          <div className='text-muted-foreground text-xs'>{description}</div>
        ) : null}
      </div>
      {children}
    </div>
  )
}

// Main page

export function CommissionOverview() {
  const { t } = useTranslation()
  const [range, setRange] = useState<RangeKey>('1d')
  const [customRange, setCustomRange] = useState<OverviewRange>(() =>
    getPresetRange('1d')
  )
  const [channelPage, setChannelPage] = useState(1)
  const [employeePage, setEmployeePage] = useState(1)
  const [channelFilter, setChannelFilter] = useState('')
  const [channelSort, setChannelSort] = useState<{
    key: string
    dir: 'asc' | 'desc'
  } | null>(null)
  const [loadedChannelRows, setLoadedChannelRows] = useState<
    ChannelProfitStat[]
  >([])
  const [loadedEmployeeRows, setLoadedEmployeeRows] = useState<EmployeeStat[]>(
    []
  )
  const channelRowsByPageRef = useRef(new Map<string, ChannelProfitStat[]>())
  const employeeRowsByPageRef = useRef(new Map<string, EmployeeStat[]>())

  const selectedRange = useMemo(() => {
    if (range === 'custom') return customRange
    return getPresetRange(range)
  }, [customRange, range])
  const startTime = useMemo(
    () => toUnixTimestamp(selectedRange.start),
    [selectedRange.start]
  )
  const endTime = useMemo(
    () => toUnixTimestamp(selectedRange.end),
    [selectedRange.end]
  )
  const channelMergeScope = useMemo(
    () =>
      [range, startTime ?? '', endTime ?? '', channelFilter.trim()].join('|'),
    [channelFilter, endTime, range, startTime]
  )
  const employeeMergeScope = useMemo(
    () => [range, startTime ?? '', endTime ?? ''].join('|'),
    [endTime, range, startTime]
  )

  const { data, isLoading, isFetching, refetch } = useQuery({
    queryKey: [
      'commission-overview',
      range,
      startTime ?? null,
      endTime ?? null,
      channelPage,
      employeePage,
      channelFilter,
    ],
    queryFn: () =>
      getCommissionOverview({
        start_time: startTime,
        end_time: endTime,
        channel_page: channelPage,
        channel_page_size: OVERVIEW_TABLE_PAGE_SIZE,
        channel_keyword: channelFilter.trim() || undefined,
        employee_page: employeePage,
        employee_page_size: EMPLOYEE_PERFORMANCE_TOP_LIMIT,
      }),
  })

  const d = data?.data
  const platform = d?.platform
  const comm = d?.commission

  const rangeButtons: { key: Exclude<RangeKey, 'custom'>; label: string }[] = [
    { key: '1d', label: t('Last 1 day') },
    { key: '7d', label: t('Last 7 days') },
    { key: '30d', label: t('Last 30 days') },
    { key: '90d', label: t('Last 90 days') },
    { key: 'all', label: t('All time') },
  ]

  const handlePresetChange = (nextRange: Exclude<RangeKey, 'custom'>) => {
    setRange(nextRange)
    setChannelPage(1)
    setEmployeePage(1)
    channelRowsByPageRef.current.clear()
    employeeRowsByPageRef.current.clear()
    setLoadedChannelRows([])
    setLoadedEmployeeRows([])
  }

  const handleCustomRangeChange = (nextRange: OverviewRange) => {
    setCustomRange(nextRange)
    setRange(resolveRangeKey(nextRange))
    setChannelPage(1)
    setEmployeePage(1)
    channelRowsByPageRef.current.clear()
    employeeRowsByPageRef.current.clear()
    setLoadedChannelRows([])
    setLoadedEmployeeRows([])
  }

  const channelPlatformRows = useMemo(
    () => d?.by_channel_platform ?? [],
    [d?.by_channel_platform]
  )
  const employeeRows = useMemo(() => d?.by_employee ?? [], [d?.by_employee])
  const sortedChannelRows = useMemo(
    () => sortChannelRows(loadedChannelRows, channelSort),
    [loadedChannelRows, channelSort]
  )
  const displayedChannelRows = sortedChannelRows
  const displayedEmployeeRows = loadedEmployeeRows
  const channelTotal =
    d?.by_channel_platform_total ?? channelPlatformRows.length
  const hasMoreChannelRows = loadedChannelRows.length < channelTotal

  useEffect(() => {
    setChannelPage(1)
    channelRowsByPageRef.current.clear()
    setLoadedChannelRows([])
  }, [channelFilter])

  useEffect(() => {
    if (!d) return
    const fetchedChannelPage = d.by_channel_platform_page ?? channelPage
    channelRowsByPageRef.current.set(
      `${channelMergeScope}|${fetchedChannelPage}`,
      channelPlatformRows
    )
    const nextRows: ChannelProfitStat[] = []
    for (let page = 1; ; page += 1) {
      const rows = channelRowsByPageRef.current.get(
        `${channelMergeScope}|${page}`
      )
      if (!rows) break
      nextRows.push(...rows)
    }
    setLoadedChannelRows(nextRows)
  }, [channelMergeScope, channelPage, channelPlatformRows, d])

  useEffect(() => {
    if (!d) return
    const fetchedEmployeePage = d.by_employee_page ?? employeePage
    employeeRowsByPageRef.current.set(
      `${employeeMergeScope}|${fetchedEmployeePage}`,
      employeeRows
    )
    const nextRows: EmployeeStat[] = []
    for (let page = 1; ; page += 1) {
      const rows = employeeRowsByPageRef.current.get(
        `${employeeMergeScope}|${page}`
      )
      if (!rows) break
      nextRows.push(...rows)
    }
    setLoadedEmployeeRows(nextRows)
  }, [employeeMergeScope, employeePage, employeeRows, d])

  const loadMoreChannels = useCallback(() => {
    if (isFetching || !hasMoreChannelRows) return
    setChannelPage((page) => page + 1)
  }, [hasMoreChannelRows, isFetching])

  const toggleChannelSort = useCallback((key: string) => {
    setChannelSort((prev) =>
      prev?.key === key
        ? prev.dir === 'desc'
          ? { key, dir: 'asc' }
          : null
        : { key, dir: 'desc' }
    )
  }, [])

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Business Overview')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content className='overflow-hidden'>
        <div className='flex h-full min-h-0 flex-col gap-4 overflow-hidden'>
          {/* Range selector */}
          <div className='flex shrink-0 flex-wrap items-center gap-2'>
            {rangeButtons.map((b) => (
              <Button
                key={b.key}
                size='sm'
                variant={range === b.key ? 'default' : 'outline'}
                onClick={() => handlePresetChange(b.key)}
              >
                {b.label}
              </Button>
            ))}
            <div className='min-w-[220px] flex-1 sm:max-w-[320px]'>
              <CompactDateRangePicker
                start={selectedRange.start}
                end={selectedRange.end}
                onChange={handleCustomRangeChange}
              />
            </div>
            <Button
              size='sm'
              variant='outline'
              onClick={() => refetch()}
              disabled={isFetching}
              title={t('Refresh')}
            >
              <RefreshCw
                className={`size-3.5 ${isFetching ? 'animate-spin' : ''}`}
              />
            </Button>
          </div>

          <div className='min-h-0 flex-1 space-y-6 overflow-y-auto pr-1'>
            {isLoading && (
              <p className='text-muted-foreground'>{t('Loading...')}</p>
            )}

            {!isLoading && d && (
              <>
                {/* Platform consumption (whole platform) */}
                <div className='space-y-2'>
                  <SectionTitle
                    description={t(
                      'Cost is recorded precisely per transaction. Data generated before this feature was enabled has no cost record.'
                    )}
                  >
                    {t('Platform-wide (all users)')}
                  </SectionTitle>
                  <StatPanel columnsClassName='grid-cols-1 sm:grid-cols-3'>
                    <AmountStatCard
                      title={t('Total Consumption')}
                      value={platform?.total_consumption_quota ?? 0}
                      sub={formatBusinessUsd(platform?.total_consumption_usd)}
                      icon={Wallet}
                    />
                    <AmountStatCard
                      title={t('Cost')}
                      value={platform?.est_cost_quota ?? 0}
                      sub={formatBusinessUsd(platform?.est_cost_usd)}
                      icon={Wallet}
                    />
                    <AmountStatCard
                      title={t('Profit')}
                      value={platform?.est_profit_quota ?? 0}
                      sub={formatBusinessUsd(platform?.est_profit_usd)}
                      icon={PiggyBank}
                    />
                    <StatCard
                      title={t('Gross Margin')}
                      value={`${((platform?.est_gross_margin ?? 0) * 100).toFixed(1)}%`}
                      icon={Percent}
                    />
                    <StatCard
                      title={t('Requests')}
                      value={String(platform?.request_count ?? 0)}
                      icon={TrendingUp}
                    />
                    <div className='divide-border/60 grid min-w-0 grid-cols-2 divide-x px-3 py-3 sm:px-5 sm:py-4'>
                      <div className='pr-3'>
                        <div className='flex items-center gap-2'>
                          <TrendingUp className='text-muted-foreground/60 size-3.5 shrink-0' />
                          <div className='text-muted-foreground truncate text-xs font-medium tracking-wider uppercase'>
                            {t('Profitable Channels')}
                          </div>
                        </div>
                        <div className='text-foreground mt-1.5 font-mono text-base font-bold tracking-tight break-all tabular-nums sm:mt-2 sm:text-xl'>
                          {String(platform?.profitable_channel_count ?? 0)}
                        </div>
                      </div>
                      <div className='pl-3'>
                        <div className='flex items-center gap-2'>
                          <TrendingDown className='text-muted-foreground/60 size-3.5 shrink-0' />
                          <div className='text-muted-foreground truncate text-xs font-medium tracking-wider uppercase'>
                            {t('Loss Channels')}
                          </div>
                        </div>
                        <div className='text-foreground mt-1.5 font-mono text-base font-bold tracking-tight break-all tabular-nums sm:mt-2 sm:text-xl'>
                          {String(platform?.loss_channel_count ?? 0)}
                        </div>
                      </div>
                    </div>
                  </StatPanel>
                </div>

                <BusinessSection
                  title={t('Channel Profit (platform-wide)')}
                  description={
                    <>
                      {t(
                        'Cost and profit are estimated from per-group ratios and per-channel cost ratios.'
                      )}
                      {(platform?.loss_channel_count ?? 0) > 0 ? (
                        <span className='ml-2 font-medium text-amber-600 dark:text-amber-400'>
                          {t(
                            'Losses usually come from effective group ratios below channel cost ratios, or refund/reversal records.'
                          )}
                        </span>
                      ) : null}
                    </>
                  }
                >
                  <div className='mb-2 flex items-center gap-2'>
                    <input
                      type='text'
                      value={channelFilter}
                      onChange={(e) => setChannelFilter(e.target.value)}
                      placeholder={t('Filter channels...')}
                      className='border-input placeholder:text-muted-foreground focus:ring-ring h-7 w-44 rounded-md border bg-transparent px-3 text-sm outline-none focus:ring-1'
                    />
                    {channelFilter && (
                      <Button
                        size='sm'
                        variant='ghost'
                        onClick={() => setChannelFilter('')}
                        className='h-7 px-2 text-xs'
                      >
                        {t('Clear')}
                      </Button>
                    )}
                  </div>
                  <ScrollTable
                    hasMore={hasMoreChannelRows}
                    onLoadMore={loadMoreChannels}
                    loading={isFetching}
                  >
                    <Table containerClassName='overflow-visible'>
                      <TableHeader className='bg-background sticky top-0 z-10'>
                        <TableRow>
                          <TableHead>
                            <SortableHead
                              sortKey='channel_name'
                              currentSort={channelSort}
                              onSort={toggleChannelSort}
                            >
                              {t('Channel')}
                            </SortableHead>
                          </TableHead>
                          <TableHead>
                            <SortableHead
                              sortKey='cost_ratio'
                              currentSort={channelSort}
                              onSort={toggleChannelSort}
                            >
                              {t('Average Cost Ratio')}
                            </SortableHead>
                          </TableHead>
                          <TableHead>
                            <SortableHead
                              sortKey='consumption_quota'
                              currentSort={channelSort}
                              onSort={toggleChannelSort}
                            >
                              {t('Total Consumption')}
                            </SortableHead>
                          </TableHead>
                          <TableHead>
                            <SortableHead
                              sortKey='est_cost_quota'
                              currentSort={channelSort}
                              onSort={toggleChannelSort}
                            >
                              {t('Cost')}
                            </SortableHead>
                          </TableHead>
                          <TableHead>
                            <SortableHead
                              sortKey='est_profit_quota'
                              currentSort={channelSort}
                              onSort={toggleChannelSort}
                            >
                              {t('Profit')}
                            </SortableHead>
                          </TableHead>
                          <TableHead>
                            <SortableHead
                              sortKey='est_gross_margin'
                              currentSort={channelSort}
                              onSort={toggleChannelSort}
                            >
                              {t('Gross Margin')}
                            </SortableHead>
                          </TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {displayedChannelRows.length === 0 && (
                          <TableRow>
                            <TableCell
                              colSpan={6}
                              className='text-muted-foreground text-center'
                            >
                              {t('No records')}
                            </TableCell>
                          </TableRow>
                        )}
                        {displayedChannelRows.map((ch) => (
                          <TableRow key={ch.channel_id}>
                            <TableCell>
                              {ch.channel_name ||
                                t('Deleted channel #{{id}}', {
                                  id: ch.channel_id,
                                })}
                            </TableCell>
                            <TableCell>
                              {Number(ch.cost_ratio ?? 0).toFixed(2)}
                            </TableCell>
                            <TableCell>
                              <BusinessAmount value={ch.consumption_quota} />
                            </TableCell>
                            <TableCell>
                              <BusinessAmount value={ch.est_cost_quota} />
                            </TableCell>
                            <TableCell>
                              <BusinessAmount value={ch.est_profit_quota} />
                            </TableCell>
                            <TableCell>
                              {(ch.est_gross_margin * 100).toFixed(1)}%
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </ScrollTable>
                </BusinessSection>

                {/* Commission-attributed financials */}
                <div className='space-y-2'>
                  <SectionTitle>
                    {t('Employee-attributed performance')}
                  </SectionTitle>
                  <StatPanel columnsClassName='grid-cols-1 sm:grid-cols-2 lg:grid-cols-5'>
                    <AmountStatCard
                      title={t('Customer consumption')}
                      value={comm?.total_revenue_quota ?? 0}
                      sub={formatBusinessUsd(comm?.total_revenue_usd)}
                      icon={DollarSign}
                    />
                    <AmountStatCard
                      title={t('Customer cost')}
                      value={comm?.total_cost_quota ?? 0}
                      sub={formatBusinessUsd(comm?.total_cost_usd)}
                      icon={Wallet}
                    />
                    <AmountStatCard
                      title={t('Customer profit')}
                      value={comm?.total_profit_quota ?? 0}
                      sub={formatBusinessUsd(comm?.total_profit_usd)}
                      icon={PiggyBank}
                    />
                    <AmountStatCard
                      title={t('Commission')}
                      value={comm?.total_commission_quota ?? 0}
                      sub={formatBusinessUsd(comm?.total_commission_usd)}
                      icon={BadgeDollarSign}
                    />
                    <StatCard
                      title={t('Gross Margin')}
                      value={`${((comm?.gross_margin ?? 0) * 100).toFixed(1)}%`}
                      sub={`${comm?.record_count ?? 0} ${t('records')}`}
                      icon={Percent}
                    />
                  </StatPanel>
                </div>

                <BusinessSection title={t('Top 10 employee performance')}>
                  <ScrollTable>
                    <Table containerClassName='overflow-visible'>
                      <TableHeader className='bg-background sticky top-0 z-10'>
                        <TableRow>
                          <TableHead>{t('Employee')}</TableHead>
                          <TableHead>{t('Customer consumption')}</TableHead>
                          <TableHead>{t('Customer cost')}</TableHead>
                          <TableHead>{t('Customer profit')}</TableHead>
                          <TableHead>{t('Commission')}</TableHead>
                          <TableHead>{t('Records')}</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {displayedEmployeeRows.length === 0 && (
                          <TableRow>
                            <TableCell
                              colSpan={6}
                              className='text-muted-foreground text-center'
                            >
                              {t('No records')}
                            </TableCell>
                          </TableRow>
                        )}
                        {displayedEmployeeRows.map((e) => (
                          <TableRow key={e.employee_user_id}>
                            <TableCell>
                              {e.display_name ||
                                e.username ||
                                `#${e.employee_user_id}`}
                            </TableCell>
                            <TableCell>
                              <BusinessAmount value={e.total_revenue} />
                            </TableCell>
                            <TableCell>
                              <BusinessAmount value={e.total_cost} />
                            </TableCell>
                            <TableCell>
                              <BusinessAmount value={e.total_profit} />
                            </TableCell>
                            <TableCell>
                              <BusinessAmount
                                value={e.total_commission}
                                positiveClassName='text-green-600'
                              />
                            </TableCell>
                            <TableCell>{e.record_count}</TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </ScrollTable>
                </BusinessSection>
              </>
            )}
          </div>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

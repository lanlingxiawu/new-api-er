import {
  useCallback,
  useEffect,
  useMemo,
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
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { getEndOfDay, getStartOfDay } from '@/lib/time'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { SectionPageLayout } from '@/components/layout'
import {
  formatBusinessAmount,
  formatBusinessUsd,
} from '@/features/business/format'
import { CompactDateTimeRangePicker } from '@/features/usage-logs/components/compact-date-time-range-picker'
import { getCommissionOverview } from './api'

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

const TABLE_INITIAL_ROWS = 5
const TABLE_LOAD_STEP = 5
const CLASSIC_TABLE_SCROLL_HEIGHT = 223

function ScrollTable({
  children,
  hasMore,
  onLoadMore,
}: {
  children: ReactNode
  hasMore?: boolean
  onLoadMore?: () => void
}) {
  const handleScroll = useCallback(
    (event: UIEvent<HTMLDivElement>) => {
      if (!hasMore || !onLoadMore) return
      const target = event.currentTarget
      const distanceToBottom =
        target.scrollHeight - target.scrollTop - target.clientHeight
      if (distanceToBottom <= 24) {
        onLoadMore()
      }
    },
    [hasMore, onLoadMore]
  )

  return (
    <div
      className='w-full overflow-auto rounded-xl border'
      style={{ height: CLASSIC_TABLE_SCROLL_HEIGHT }}
      onScroll={handleScroll}
    >
      {children}
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
      <div className='text-foreground mt-1.5 font-mono text-lg font-bold tracking-tight break-all tabular-nums sm:mt-2 sm:text-2xl'>
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
      className='flex items-center gap-1 hover:text-foreground transition-colors'
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
  const [visibleChannelRows, setVisibleChannelRows] =
    useState(TABLE_INITIAL_ROWS)
  const [visibleEmployeeRows, setVisibleEmployeeRows] =
    useState(TABLE_INITIAL_ROWS)
  const [channelFilter, setChannelFilter] = useState('')
  const [channelSort, setChannelSort] = useState<{
    key: string
    dir: 'asc' | 'desc'
  } | null>(null)

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

  const { data, isLoading, isFetching, refetch } = useQuery({
    queryKey: [
      'commission-overview',
      range,
      startTime ?? null,
      endTime ?? null,
    ],
    queryFn: () =>
      getCommissionOverview({ start_time: startTime, end_time: endTime }),
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
  }

  const handleCustomRangeChange = (nextRange: OverviewRange) => {
    setCustomRange(nextRange)
    setRange(resolveRangeKey(nextRange))
  }

  const channelPlatformRows = useMemo(
    () => d?.by_channel_platform ?? [],
    [d?.by_channel_platform]
  )
  const employeeRows = useMemo(
    () => (d?.by_employee ?? []).slice(0, 10),
    [d?.by_employee]
  )
  const filteredChannelRows = useMemo(() => {
    let rows = channelPlatformRows
    const kw = channelFilter.trim().toLowerCase()
    if (kw) {
      rows = rows.filter(
        (r) =>
          (r.channel_name ?? '').toLowerCase().includes(kw) ||
          String(r.channel_id).includes(kw),
      )
    }
    if (channelSort) {
      const { key, dir } = channelSort
      rows = [...rows].sort((a, b) => {
        const av = (a as Record<string, unknown>)[key]
        const bv = (b as Record<string, unknown>)[key]
        if (typeof av === 'string' || typeof bv === 'string') {
          return dir === 'asc'
            ? String(av ?? '').localeCompare(String(bv ?? ''))
            : String(bv ?? '').localeCompare(String(av ?? ''))
        }
        return dir === 'asc'
          ? Number(av ?? 0) - Number(bv ?? 0)
          : Number(bv ?? 0) - Number(av ?? 0)
      })
    }
    return rows
  }, [channelPlatformRows, channelFilter, channelSort])
  const displayedChannelRows = useMemo(
    () => filteredChannelRows.slice(0, visibleChannelRows),
    [filteredChannelRows, visibleChannelRows]
  )
  const displayedEmployeeRows = useMemo(
    () => employeeRows.slice(0, visibleEmployeeRows),
    [employeeRows, visibleEmployeeRows]
  )
  const hasMoreChannelRows = visibleChannelRows < filteredChannelRows.length
  const hasMoreEmployeeRows = visibleEmployeeRows < employeeRows.length

  useEffect(() => {
    setVisibleChannelRows(TABLE_INITIAL_ROWS)
  }, [filteredChannelRows])

  useEffect(() => {
    setVisibleEmployeeRows(TABLE_INITIAL_ROWS)
  }, [employeeRows])

  const toggleChannelSort = useCallback((key: string) => {
    setChannelSort((prev) =>
      prev?.key === key
        ? prev.dir === 'desc'
          ? { key, dir: 'asc' }
          : null
        : { key, dir: 'desc' },
    )
  }, [])

  const loadMoreChannelRows = useCallback(() => {
    setVisibleChannelRows((current) =>
      Math.min(current + TABLE_LOAD_STEP, filteredChannelRows.length)
    )
  }, [filteredChannelRows.length])

  const loadMoreEmployeeRows = useCallback(() => {
    setVisibleEmployeeRows((current) =>
      Math.min(current + TABLE_LOAD_STEP, employeeRows.length)
    )
  }, [employeeRows.length])

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
            <div className='min-w-[280px] flex-1 sm:max-w-[420px]'>
              <CompactDateTimeRangePicker
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
              <RefreshCw className={`size-3.5 ${isFetching ? 'animate-spin' : ''}`} />
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
                    <StatCard
                      title={t('Total Consumption')}
                      value={formatBusinessAmount(
                        platform?.total_consumption_quota ?? 0
                      )}
                      sub={formatBusinessUsd(platform?.total_consumption_usd)}
                      icon={Wallet}
                    />
                    <StatCard
                      title={t('Cost')}
                      value={formatBusinessAmount(
                        platform?.est_cost_quota ?? 0
                      )}
                      sub={formatBusinessUsd(platform?.est_cost_usd)}
                      icon={Wallet}
                    />
                    <StatCard
                      title={t('Profit')}
                      value={formatBusinessAmount(
                        platform?.est_profit_quota ?? 0
                      )}
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
                    <div className='min-w-0 px-3 py-3 sm:px-5 sm:py-4 grid grid-cols-2 divide-x divide-border/60'>
                      <div className='pr-3'>
                        <div className='flex items-center gap-2'>
                          <TrendingUp className='text-muted-foreground/60 size-3.5 shrink-0' />
                          <div className='text-muted-foreground truncate text-xs font-medium tracking-wider uppercase'>
                            {t('Profitable Channels')}
                          </div>
                        </div>
                        <div className='text-foreground mt-1.5 font-mono text-lg font-bold tracking-tight break-all tabular-nums sm:mt-2 sm:text-2xl'>
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
                        <div className='text-foreground mt-1.5 font-mono text-lg font-bold tracking-tight break-all tabular-nums sm:mt-2 sm:text-2xl'>
                          {String(platform?.loss_channel_count ?? 0)}
                        </div>
                      </div>
                    </div>
                  </StatPanel>
                </div>

                <BusinessSection
                  title={t('Channel Profit (platform-wide)')}
                  description={t(
                    'Cost and profit are estimated from per-group ratios and per-channel cost ratios.'
                  )}
                >
                  <div className='mb-2 flex items-center gap-2'>
                    <input
                      type='text'
                      value={channelFilter}
                      onChange={(e) => setChannelFilter(e.target.value)}
                      placeholder={t('Filter channels...')}
                      className='h-7 w-44 rounded-md border border-input bg-transparent px-3 text-sm outline-none placeholder:text-muted-foreground focus:ring-1 focus:ring-ring'
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
                    onLoadMore={loadMoreChannelRows}
                  >
                    <Table containerClassName='overflow-visible'>
                      <TableHeader className='bg-background sticky top-0 z-10'>
                        <TableRow>
                          <TableHead>
                            <SortableHead sortKey='channel_name' currentSort={channelSort} onSort={toggleChannelSort}>
                              {t('Channel')}
                            </SortableHead>
                          </TableHead>
                          <TableHead>
                            <SortableHead sortKey='cost_ratio' currentSort={channelSort} onSort={toggleChannelSort}>
                              {t('Cost Ratio')}
                            </SortableHead>
                          </TableHead>
                          <TableHead>
                            <SortableHead sortKey='consumption_quota' currentSort={channelSort} onSort={toggleChannelSort}>
                              {t('Total Consumption')}
                            </SortableHead>
                          </TableHead>
                          <TableHead>
                            <SortableHead sortKey='est_cost_quota' currentSort={channelSort} onSort={toggleChannelSort}>
                              {t('Cost')}
                            </SortableHead>
                          </TableHead>
                          <TableHead>
                            <SortableHead sortKey='est_profit_quota' currentSort={channelSort} onSort={toggleChannelSort}>
                              {t('Profit')}
                            </SortableHead>
                          </TableHead>
                          <TableHead>
                            <SortableHead sortKey='est_gross_margin' currentSort={channelSort} onSort={toggleChannelSort}>
                              {t('Gross Margin')}
                            </SortableHead>
                          </TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {filteredChannelRows.length === 0 && (
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
                              {ch.channel_name || `#${ch.channel_id}`}
                            </TableCell>
                            <TableCell>{ch.cost_ratio}</TableCell>
                            <TableCell>
                              {formatBusinessAmount(ch.consumption_quota)}
                            </TableCell>
                            <TableCell>
                              {formatBusinessAmount(ch.est_cost_quota)}
                            </TableCell>
                            <TableCell
                              className={
                                ch.est_profit_quota < 0
                                  ? 'text-destructive'
                                  : ''
                              }
                            >
                              {formatBusinessAmount(ch.est_profit_quota)}
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
                    {t('Employee-attributed traffic')}
                  </SectionTitle>
                  <StatPanel columnsClassName='grid-cols-1 sm:grid-cols-2 lg:grid-cols-5'>
                    <StatCard
                      title={t('Revenue')}
                      value={formatBusinessAmount(
                        comm?.total_revenue_quota ?? 0
                      )}
                      sub={formatBusinessUsd(comm?.total_revenue_usd)}
                      icon={DollarSign}
                    />
                    <StatCard
                      title={t('Cost')}
                      value={formatBusinessAmount(comm?.total_cost_quota ?? 0)}
                      sub={formatBusinessUsd(comm?.total_cost_usd)}
                      icon={Wallet}
                    />
                    <StatCard
                      title={t('Profit')}
                      value={formatBusinessAmount(
                        comm?.total_profit_quota ?? 0
                      )}
                      sub={formatBusinessUsd(comm?.total_profit_usd)}
                      icon={PiggyBank}
                    />
                    <StatCard
                      title={t('Commission')}
                      value={formatBusinessAmount(
                        comm?.total_commission_quota ?? 0
                      )}
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

                <BusinessSection title={t('By Employee')}>
                  <ScrollTable
                    hasMore={hasMoreEmployeeRows}
                    onLoadMore={loadMoreEmployeeRows}
                  >
                    <Table containerClassName='overflow-visible'>
                      <TableHeader className='bg-background sticky top-0 z-10'>
                        <TableRow>
                          <TableHead>{t('Employee')}</TableHead>
                          <TableHead>{t('Revenue')}</TableHead>
                          <TableHead>{t('Cost')}</TableHead>
                          <TableHead>{t('Profit')}</TableHead>
                          <TableHead>{t('Commission')}</TableHead>
                          <TableHead>{t('Records')}</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {employeeRows.length === 0 && (
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
                              {e.username ||
                                e.display_name ||
                                `#${e.employee_user_id}`}
                            </TableCell>
                            <TableCell>
                              {formatBusinessAmount(e.total_revenue)}
                            </TableCell>
                            <TableCell>
                              {formatBusinessAmount(e.total_cost)}
                            </TableCell>
                            <TableCell
                              className={
                                e.total_profit < 0 ? 'text-destructive' : ''
                              }
                            >
                              {formatBusinessAmount(e.total_profit)}
                            </TableCell>
                            <TableCell className='text-green-600'>
                              {formatBusinessAmount(e.total_commission)}
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

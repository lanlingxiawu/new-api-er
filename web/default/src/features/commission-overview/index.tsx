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
  DollarSign,
  Wallet,
  PiggyBank,
  BadgeDollarSign,
  Percent,
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

type RangeKey = '7d' | '30d' | '90d' | 'all' | 'custom'

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
  const [range, setRange] = useState<RangeKey>('7d')
  const [customRange, setCustomRange] = useState<OverviewRange>(() =>
    getPresetRange('7d')
  )
  const [visibleChannelRows, setVisibleChannelRows] =
    useState(TABLE_INITIAL_ROWS)
  const [visibleEmployeeRows, setVisibleEmployeeRows] =
    useState(TABLE_INITIAL_ROWS)

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

  const { data, isLoading } = useQuery({
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
  const displayedChannelRows = useMemo(
    () => channelPlatformRows.slice(0, visibleChannelRows),
    [channelPlatformRows, visibleChannelRows]
  )
  const displayedEmployeeRows = useMemo(
    () => employeeRows.slice(0, visibleEmployeeRows),
    [employeeRows, visibleEmployeeRows]
  )
  const hasMoreChannelRows = visibleChannelRows < channelPlatformRows.length
  const hasMoreEmployeeRows = visibleEmployeeRows < employeeRows.length

  useEffect(() => {
    setVisibleChannelRows(TABLE_INITIAL_ROWS)
    setVisibleEmployeeRows(TABLE_INITIAL_ROWS)
  }, [channelPlatformRows, employeeRows])

  const loadMoreChannelRows = useCallback(() => {
    setVisibleChannelRows((current) =>
      Math.min(current + TABLE_LOAD_STEP, channelPlatformRows.length)
    )
  }, [channelPlatformRows.length])

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
                    <StatCard
                      title={t('Tokens')}
                      value={String(platform?.token_count ?? 0)}
                      icon={DollarSign}
                    />
                  </StatPanel>
                </div>

                <BusinessSection
                  title={t('Channel Profit (platform-wide)')}
                  description={t(
                    'Cost and profit are estimated from per-group ratios and per-channel cost ratios.'
                  )}
                >
                  <ScrollTable
                    hasMore={hasMoreChannelRows}
                    onLoadMore={loadMoreChannelRows}
                  >
                    <Table>
                      <TableHeader className='bg-background sticky top-0 z-10'>
                        <TableRow>
                          <TableHead>{t('Channel')}</TableHead>
                          <TableHead>{t('Cost Ratio')}</TableHead>
                          <TableHead>{t('Total Consumption')}</TableHead>
                          <TableHead>{t('Cost')}</TableHead>
                          <TableHead>{t('Profit')}</TableHead>
                          <TableHead>{t('Gross Margin')}</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {channelPlatformRows.length === 0 && (
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
                    <Table>
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

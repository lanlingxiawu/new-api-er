import { useMemo, useState, type ComponentType } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import {
  TrendingUp,
  DollarSign,
  Wallet,
  PiggyBank,
  BadgeDollarSign,
  Percent,
} from 'lucide-react'
import {
  ResponsiveContainer,
  LineChart,
  Line,
  XAxis,
  YAxis,
  Tooltip,
  CartesianGrid,
  Legend,
} from 'recharts'
import { SectionPageLayout } from '@/components/layout'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { formatQuota } from '@/lib/format'
import { getCommissionOverview } from './api'

// ── Range options ──────────────────────────────────────────────────────────

type RangeKey = '7d' | '30d' | '90d' | 'all'

function rangeToTimes(range: RangeKey): { start?: number; end?: number } {
  if (range === 'all') return {}
  const days = range === '7d' ? 7 : range === '30d' ? 30 : 90
  const now = Math.floor(Date.now() / 1000)
  return { start: now - days * 86400, end: now }
}

// ── Stat card ────────────────────────────────────────────────────────────────

function StatCard({
  title,
  value,
  sub,
  icon: Icon,
  color,
}: {
  title: string
  value: string
  sub?: string
  icon: ComponentType<{ className?: string }>
  color: string
}) {
  return (
    <Card>
      <CardHeader className='flex flex-row items-center justify-between pb-2'>
        <CardTitle className='text-sm font-medium text-muted-foreground'>
          {title}
        </CardTitle>
        <Icon className={`h-4 w-4 ${color}`} />
      </CardHeader>
      <CardContent>
        <div className='text-2xl font-bold'>{value}</div>
        {sub ? <p className='mt-1 text-xs text-muted-foreground'>{sub}</p> : null}
      </CardContent>
    </Card>
  )
}

// ── Main page ─────────────────────────────────────────────────────────────────

export function CommissionOverview() {
  const { t } = useTranslation()
  const [range, setRange] = useState<RangeKey>('30d')

  const times = useMemo(() => rangeToTimes(range), [range])

  const { data, isLoading } = useQuery({
    queryKey: ['commission-overview', range],
    queryFn: () =>
      getCommissionOverview({ start_time: times.start, end_time: times.end }),
  })

  const d = data?.data
  const platform = d?.platform
  const comm = d?.commission

  const rangeButtons: { key: RangeKey; label: string }[] = [
    { key: '7d', label: t('Last 7 days') },
    { key: '30d', label: t('Last 30 days') },
    { key: '90d', label: t('Last 90 days') },
    { key: 'all', label: t('All time') },
  ]

  const chartData = (d?.by_day ?? []).map((row) => ({
    date: row.date.slice(5),
    revenue: row.total_revenue,
    cost: row.total_cost,
    profit: row.total_profit,
  }))

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Business Overview')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='space-y-6'>
          {/* Range selector */}
          <div className='flex flex-wrap gap-2'>
            {rangeButtons.map((b) => (
              <Button
                key={b.key}
                size='sm'
                variant={range === b.key ? 'default' : 'outline'}
                onClick={() => setRange(b.key)}
              >
                {b.label}
              </Button>
            ))}
          </div>

          {isLoading && (
            <p className='text-muted-foreground'>{t('Loading...')}</p>
          )}

          {!isLoading && d && (
            <>
              {/* Platform consumption (whole platform) */}
              <div>
                <h3 className='mb-2 text-sm font-semibold text-muted-foreground'>
                  {t('Platform-wide (all users)')}
                </h3>
                <div className='grid grid-cols-1 gap-4 sm:grid-cols-3'>
                  <StatCard
                    title={t('Total Consumption')}
                    value={formatQuota(platform?.total_consumption_quota ?? 0)}
                    sub={`≈ $${(platform?.total_consumption_usd ?? 0).toFixed(4)}`}
                    icon={Wallet}
                    color='text-blue-600'
                  />
                  <StatCard
                    title={t('Requests')}
                    value={String(platform?.request_count ?? 0)}
                    icon={TrendingUp}
                    color='text-indigo-600'
                  />
                  <StatCard
                    title={t('Tokens')}
                    value={String(platform?.token_count ?? 0)}
                    icon={DollarSign}
                    color='text-cyan-600'
                  />
                </div>
              </div>

              {/* Commission-attributed financials */}
              <div>
                <h3 className='mb-2 text-sm font-semibold text-muted-foreground'>
                  {t('Employee-attributed traffic')}
                </h3>
                <div className='grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-5'>
                  <StatCard
                    title={t('Revenue')}
                    value={formatQuota(comm?.total_revenue_quota ?? 0)}
                    sub={`≈ $${(comm?.total_revenue_usd ?? 0).toFixed(4)}`}
                    icon={DollarSign}
                    color='text-blue-600'
                  />
                  <StatCard
                    title={t('Cost')}
                    value={formatQuota(comm?.total_cost_quota ?? 0)}
                    sub={`≈ $${(comm?.total_cost_usd ?? 0).toFixed(4)}`}
                    icon={Wallet}
                    color='text-orange-600'
                  />
                  <StatCard
                    title={t('Profit')}
                    value={formatQuota(comm?.total_profit_quota ?? 0)}
                    sub={`≈ $${(comm?.total_profit_usd ?? 0).toFixed(4)}`}
                    icon={PiggyBank}
                    color='text-green-600'
                  />
                  <StatCard
                    title={t('Commission')}
                    value={formatQuota(comm?.total_commission_quota ?? 0)}
                    sub={`≈ $${(comm?.total_commission_usd ?? 0).toFixed(4)}`}
                    icon={BadgeDollarSign}
                    color='text-purple-600'
                  />
                  <StatCard
                    title={t('Gross Margin')}
                    value={`${((comm?.gross_margin ?? 0) * 100).toFixed(1)}%`}
                    sub={`${comm?.record_count ?? 0} ${t('records')}`}
                    icon={Percent}
                    color='text-pink-600'
                  />
                </div>
              </div>

              {/* Daily trend */}
              <Card>
                <CardHeader>
                  <CardTitle>{t('Daily Trend')}</CardTitle>
                </CardHeader>
                <CardContent>
                  {chartData.length === 0 ? (
                    <p className='text-sm text-muted-foreground'>
                      {t('No records')}
                    </p>
                  ) : (
                    <div className='h-72 w-full'>
                      <ResponsiveContainer width='100%' height='100%'>
                        <LineChart data={chartData}>
                          <CartesianGrid strokeDasharray='3 3' opacity={0.2} />
                          <XAxis dataKey='date' fontSize={12} />
                          <YAxis fontSize={12} />
                          <Tooltip
                            formatter={(v: number) => formatQuota(v)}
                          />
                          <Legend />
                          <Line
                            type='monotone'
                            dataKey='revenue'
                            name={t('Revenue')}
                            stroke='#2563eb'
                            dot={false}
                          />
                          <Line
                            type='monotone'
                            dataKey='cost'
                            name={t('Cost')}
                            stroke='#ea580c'
                            dot={false}
                          />
                          <Line
                            type='monotone'
                            dataKey='profit'
                            name={t('Profit')}
                            stroke='#16a34a'
                            dot={false}
                          />
                        </LineChart>
                      </ResponsiveContainer>
                    </div>
                  )}
                </CardContent>
              </Card>

              {/* By employee */}
              <Card>
                <CardHeader>
                  <CardTitle>{t('By Employee')}</CardTitle>
                </CardHeader>
                <CardContent>
                  <Table>
                    <TableHeader>
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
                      {(d.by_employee ?? []).length === 0 && (
                        <TableRow>
                          <TableCell
                            colSpan={6}
                            className='text-center text-muted-foreground'
                          >
                            {t('No records')}
                          </TableCell>
                        </TableRow>
                      )}
                      {(d.by_employee ?? []).map((e) => (
                        <TableRow key={e.employee_user_id}>
                          <TableCell>
                            {e.username || e.display_name || `#${e.employee_user_id}`}
                          </TableCell>
                          <TableCell>{formatQuota(e.total_revenue)}</TableCell>
                          <TableCell>{formatQuota(e.total_cost)}</TableCell>
                          <TableCell
                            className={
                              e.total_profit < 0 ? 'text-destructive' : ''
                            }
                          >
                            {formatQuota(e.total_profit)}
                          </TableCell>
                          <TableCell className='text-green-600'>
                            {formatQuota(e.total_commission)}
                          </TableCell>
                          <TableCell>{e.record_count}</TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </CardContent>
              </Card>

              {/* By channel */}
              <Card>
                <CardHeader>
                  <CardTitle>{t('By Channel')}</CardTitle>
                </CardHeader>
                <CardContent>
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>{t('Channel')}</TableHead>
                        <TableHead>{t('Revenue')}</TableHead>
                        <TableHead>{t('Cost')}</TableHead>
                        <TableHead>{t('Profit')}</TableHead>
                        <TableHead>{t('Records')}</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {(d.by_channel ?? []).length === 0 && (
                        <TableRow>
                          <TableCell
                            colSpan={5}
                            className='text-center text-muted-foreground'
                          >
                            {t('No records')}
                          </TableCell>
                        </TableRow>
                      )}
                      {(d.by_channel ?? []).map((ch) => (
                        <TableRow key={ch.channel_id}>
                          <TableCell>
                            {ch.channel_name || `#${ch.channel_id}`}
                          </TableCell>
                          <TableCell>{formatQuota(ch.total_revenue)}</TableCell>
                          <TableCell>{formatQuota(ch.total_cost)}</TableCell>
                          <TableCell
                            className={
                              ch.total_profit < 0 ? 'text-destructive' : ''
                            }
                          >
                            {formatQuota(ch.total_profit)}
                          </TableCell>
                          <TableCell>{ch.record_count}</TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </CardContent>
              </Card>
            </>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

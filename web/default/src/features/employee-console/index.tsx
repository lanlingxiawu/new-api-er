import { useMemo, useState, type ComponentType } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  type ColumnDef,
  type PaginationState,
  getCoreRowModel,
  getPaginationRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { BadgeDollarSign, DollarSign, TrendingUp, Wallet } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { DataTableColumnHeader, DataTablePage } from '@/components/data-table'
import { SectionPageLayout } from '@/components/layout'
import { BusinessAmount } from '@/features/business/amount-display'
import {
  formatBusinessAmount,
  formatBusinessExactUsd,
  formatBusinessTargetAmount,
  formatBusinessUsd,
} from '@/features/business/format'
import { CommissionCalendarSection } from '@/features/employees/components/commission-financial-calendar'
import type { CommissionLog } from '@/features/employees/types'
import { CompactDateTimeRangePicker } from '@/features/usage-logs/components/compact-date-time-range-picker'
import {
  getMyCommissionLogs,
  getMyCommissionCalendarStats,
  getMyCommissionSummary,
  getMyEmployeeProfile,
  type EmployeeExtension,
} from './api'

function formatTs(ts: number) {
  if (!ts) return '-'
  return new Date(ts * 1000).toLocaleString()
}

function formatPercent(value: number | undefined) {
  return `${(Number(value || 0) * 100).toFixed(1)}%`
}

function SummaryCards({
  data,
  commissionRate,
  targetAmount,
}: {
  data: Partial<EmployeeExtension>
  commissionRate: number
  targetAmount: number
}) {
  const { t } = useTranslation()
  const num = (value: number | undefined) => value ?? 0
  const totalConsumptionQuota = num(data.customer_total_consumption_quota)
  const totalConsumptionUsd = data.customer_total_consumption_usd
  const totalProfitQuota = num(
    data.current_performance_quota ??
      data.profit_total_quota ??
      data.total_profit_quota
  )
  const totalProfitUsd = num(
    data.current_performance_usd ?? data.profit_total_usd ?? data.total_profit_usd
  )
  const totalCommissionQuota = num(
    data.current_commission_quota ??
      data.total_commission_quota ??
      data.commission_total_quota
  )
  const totalCommissionUsd =
    data.current_commission_usd ??
    data.total_commission_usd ??
    data.commission_total_usd
  const reachedTarget = totalProfitUsd >= targetAmount
  const targetText = targetAmount
    ? `${t('Performance')}: ${formatBusinessTargetAmount(totalProfitUsd)} / ${formatBusinessTargetAmount(targetAmount)}${reachedTarget ? ` ${t('Reached')}` : ''}`
    : `${t('Performance Target')}: ${t('No limit')}`

  const cards = [
    {
      title: t('Total Consumption'),
      value: formatBusinessAmount(totalConsumptionQuota),
      sub:
        totalConsumptionUsd === undefined
          ? undefined
          : formatBusinessUsd(num(totalConsumptionUsd)),
      icon: DollarSign,
    },
    {
      title: t('Current Performance'),
      value: formatBusinessAmount(totalProfitQuota),
      sub: formatBusinessExactUsd(totalProfitUsd),
      icon: TrendingUp,
    },
    {
      title: t('Commission Amount'),
      value: formatBusinessAmount(totalCommissionQuota),
      sub:
        totalCommissionUsd === undefined
          ? undefined
          : formatBusinessUsd(num(totalCommissionUsd)),
      icon: BadgeDollarSign,
    },
    {
      title: t('Commission Tier'),
      value: formatPercent(commissionRate),
      sub: targetText,
      icon: Wallet,
    },
  ]

  return (
    <div className='overflow-hidden rounded-lg border'>
      <div className='divide-border/60 grid grid-cols-1 divide-x sm:grid-cols-2 lg:grid-cols-4'>
        {cards.map((card) => (
          <SummaryCard
            key={card.title}
            title={card.title}
            value={card.value}
            sub={card.sub}
            icon={card.icon}
          />
        ))}
      </div>
    </div>
  )
}

function SummaryCard({
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

function useMyCommissionColumns() {
  const { t } = useTranslation()

  return useMemo(
    (): ColumnDef<CommissionLog>[] => [
      {
        accessorKey: 'created_at',
        meta: { label: t('Time') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Time')} />
        ),
        cell: ({ row }) => (
          <span className='text-xs'>{formatTs(row.original.created_at)}</span>
        ),
      },
      {
        accessorKey: 'model_name',
        meta: { label: t('Model'), mobileTitle: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Model')} />
        ),
        cell: ({ row }) => (
          <span className='block max-w-[140px] truncate text-xs'>
            {row.original.model_name || '-'}
          </span>
        ),
      },
      {
        accessorKey: 'revenue_quota',
        meta: { label: t('Employee Consumption') },
        header: ({ column }) => (
          <DataTableColumnHeader
            column={column}
            title={t('Employee Consumption')}
          />
        ),
        cell: ({ row }) => <BusinessAmount value={row.original.revenue_quota} />,
      },
      {
        accessorKey: 'profit_quota',
        meta: { label: t('Employee Performance') },
        header: ({ column }) => (
          <DataTableColumnHeader
            column={column}
            title={t('Employee Performance')}
          />
        ),
        cell: ({ row }) => <BusinessAmount value={row.original.profit_quota} />,
      },
      {
        accessorKey: 'commission_quota',
        meta: { label: t('Commission'), mobileBadge: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Commission')} />
        ),
        cell: ({ row }) => (
          <BusinessAmount
            value={row.original.commission_quota}
            className='font-medium'
            positiveClassName='text-green-600'
            showPositiveSign
          />
        ),
      },
      {
        accessorKey: 'commission_rate',
        meta: { label: t('Rate') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Rate')} />
        ),
        cell: ({ row }) =>
          `${(row.original.commission_rate * 100).toFixed(1)}%`,
      },
    ],
    [t]
  )
}

function MonthlyStats() {
  return (
    <CommissionCalendarSection
      queryKey={['my-commission-calendar-stats']}
      queryFn={(range) => getMyCommissionCalendarStats(range)}
      showSelectedDetail={false}
    />
  )
}

function CommissionHistory() {
  const { t } = useTranslation()
  const columns = useMyCommissionColumns()
  const [filterForm, setFilterForm] = useState({
    modelName: '',
    channelId: '',
    lossStatus: 'all' as 'all' | 'loss' | 'normal',
    start: undefined as Date | undefined,
    end: undefined as Date | undefined,
  })
  const [filters, setFilters] = useState<{
    model_name?: string
    channel_id?: number
    loss_status?: 'loss' | 'normal'
    start_time?: number
    end_time?: number
  }>({})
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: 10,
  })

  const { data, isLoading } = useQuery({
    queryKey: ['my-commission-logs', pagination, filters],
    queryFn: () =>
      getMyCommissionLogs({
        page: pagination.pageIndex + 1,
        page_size: pagination.pageSize,
        ...filters,
      }),
  })

  const applyFilters = () => {
    const channelId = Number(filterForm.channelId)
    setFilters({
      ...(filterForm.modelName.trim()
        ? { model_name: filterForm.modelName.trim() }
        : {}),
      ...(Number.isFinite(channelId) && channelId > 0
        ? { channel_id: channelId }
        : {}),
      ...(filterForm.lossStatus !== 'all'
        ? { loss_status: filterForm.lossStatus }
        : {}),
      ...(filterForm.start
        ? { start_time: Math.floor(filterForm.start.getTime() / 1000) }
        : {}),
      ...(filterForm.end
        ? { end_time: Math.floor(filterForm.end.getTime() / 1000) }
        : {}),
    })
    setPagination((current) => ({ ...current, pageIndex: 0 }))
  }

  const resetFilters = () => {
    setFilterForm({
      modelName: '',
      channelId: '',
      lossStatus: 'all',
      start: undefined,
      end: undefined,
    })
    setFilters({})
    setPagination((current) => ({ ...current, pageIndex: 0 }))
  }

  const table = useReactTable({
    data: data?.data?.items ?? [],
    columns,
    rowCount: data?.data?.total ?? 0,
    state: { pagination },
    onPaginationChange: setPagination,
    manualPagination: true,
    getCoreRowModel: getCoreRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
  })

  return (
    <DataTablePage
      table={table}
      columns={columns}
      isLoading={isLoading}
      emptyTitle={t('No commission records yet')}
      paginationInFooter={false}
      toolbar={
        <div className='flex flex-wrap items-center gap-2'>
          <Input
            placeholder={t('Model Name')}
            value={filterForm.modelName}
            onChange={(event) =>
              setFilterForm((form) => ({
                ...form,
                modelName: event.target.value,
              }))
            }
            className='w-[170px]'
          />
          <Input
            type='number'
            min={1}
            placeholder={t('Channel ID')}
            value={filterForm.channelId}
            onChange={(event) =>
              setFilterForm((form) => ({
                ...form,
                channelId: event.target.value,
              }))
            }
            className='w-[110px]'
          />
          <Select
            value={filterForm.lossStatus}
            onValueChange={(value) =>
              setFilterForm((form) => ({
                ...form,
                lossStatus: (value || 'all') as 'all' | 'loss' | 'normal',
              }))
            }
          >
            <SelectTrigger size='sm' className='w-[132px]'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='all'>{t('All profit states')}</SelectItem>
              <SelectItem value='loss'>{t('Loss only')}</SelectItem>
              <SelectItem value='normal'>{t('Non-loss only')}</SelectItem>
            </SelectContent>
          </Select>
          <div className='w-[300px]'>
            <CompactDateTimeRangePicker
              start={filterForm.start}
              end={filterForm.end}
              onChange={({ start, end }) =>
                setFilterForm((form) => ({ ...form, start, end }))
              }
            />
          </div>
          <Button size='sm' onClick={applyFilters}>
            {t('Search')}
          </Button>
          <Button size='sm' variant='outline' onClick={resetFilters}>
            {t('Reset')}
          </Button>
        </div>
      }
      getRowClassName={(row) =>
        row.original.profit_quota < 0 ? 'opacity-60' : undefined
      }
      skeletonKeyPrefix='my-commission-skeleton'
      className='flex h-full min-h-0 flex-col overflow-hidden'
      tableClassName='min-h-0 flex-1 overflow-auto'
    />
  )
}

export function EmployeeConsole() {
  const { t } = useTranslation()

  const { data: profileData, isLoading: profileLoading } = useQuery({
    queryKey: ['my-employee-profile'],
    queryFn: getMyEmployeeProfile,
    retry: false,
  })

  const { data: summaryData } = useQuery({
    queryKey: ['my-commission-summary'],
    queryFn: getMyCommissionSummary,
    enabled: !!profileData?.data,
  })

  if (profileLoading) {
    return (
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('My Commission')}</SectionPageLayout.Title>
        <SectionPageLayout.Content className='overflow-hidden'>
          <p className='text-muted-foreground'>{t('Loading...')}</p>
        </SectionPageLayout.Content>
      </SectionPageLayout>
    )
  }

  if (!profileData?.success || !profileData.data) {
    return (
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('My Commission')}</SectionPageLayout.Title>
        <SectionPageLayout.Content className='overflow-hidden'>
          <p className='text-muted-foreground'>
            {t('You do not have employee status. Contact your administrator.')}
          </p>
        </SectionPageLayout.Content>
      </SectionPageLayout>
    )
  }

  const tierInfo = profileData.data.tier
  const effectiveRate = tierInfo?.tier_rate ?? 0
  const tierGroup: string = tierInfo?.tier_group || ''
  const tierTargetAmount = Number(tierInfo?.tier_threshold_usd || 0)
  const summary = {
    ...profileData.data.extension,
    ...profileData.data.period,
    ...summaryData?.data,
  }
  const revenueUsd =
    summary?.current_performance_usd ?? summary?.profit_total_usd ?? 0

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        <span className='flex items-center gap-2'>
          {t('My Commission')}
          <Badge variant='default' className='text-xs'>
            {(effectiveRate * 100).toFixed(1)}% {t('rate')}
          </Badge>
          {tierTargetAmount ? (
            <Badge
              variant={revenueUsd >= tierTargetAmount ? 'default' : 'outline'}
              className='text-xs'
            >
              {t('Target')}: {formatBusinessTargetAmount(revenueUsd)} /{' '}
              {formatBusinessTargetAmount(tierTargetAmount)}
              {revenueUsd >= tierTargetAmount ? ` ${t('Reached')}` : ''}
            </Badge>
          ) : null}
        </span>
      </SectionPageLayout.Title>
      <SectionPageLayout.Content className='overflow-hidden'>
        <Tabs
          defaultValue='monthly'
          className='flex h-full min-h-0 flex-col gap-4 overflow-hidden'
        >
          <TabsList className='shrink-0'>
            <TabsTrigger value='monthly'>{t('Monthly Stats')}</TabsTrigger>
            <TabsTrigger value='details'>{t('Commission Details')}</TabsTrigger>
          </TabsList>

          <TabsContent value='monthly' className='min-h-0 flex-1 overflow-auto'>
            <MonthlyStats />
          </TabsContent>

          <TabsContent
            value='details'
            className='min-h-0 flex-1 overflow-hidden'
          >
            <div className='flex h-full min-h-0 flex-col gap-4 overflow-hidden'>
              {summary ? (
                <div className='shrink-0'>
                  <SummaryCards
                    data={summary}
                    commissionRate={effectiveRate}
                    targetAmount={tierTargetAmount}
                  />
                </div>
              ) : null}
              <div className='min-h-0 flex-1 overflow-hidden'>
                <CommissionHistory />
              </div>
            </div>
          </TabsContent>
        </Tabs>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

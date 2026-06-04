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
import { DataTableColumnHeader, DataTablePage } from '@/components/data-table'
import { SectionPageLayout } from '@/components/layout'
import {
  formatBusinessAmount,
  formatBusinessExactUsd,
  formatBusinessTargetAmount,
  formatBusinessUsd,
} from '@/features/business/format'
import type { CommissionLog } from '@/features/employees/types'
import {
  getMyCommissionLogs,
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
    data.profit_total_quota ?? data.total_profit_quota
  )
  const totalProfitUsd = num(data.profit_total_usd ?? data.total_profit_usd)
  const totalCommissionQuota = num(
    data.total_commission_quota ?? data.commission_total_quota
  )
  const totalCommissionUsd =
    data.total_commission_usd ?? data.commission_total_usd
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
        accessorKey: 'customer_user_id',
        meta: { label: t('Customer'), mobileTitle: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Customer')} />
        ),
        cell: ({ row }) => (
          <Badge variant='outline'>
            {row.original.customer_user_id_masked ??
              `#${row.original.customer_user_id}`}
          </Badge>
        ),
      },
      {
        accessorKey: 'model_name',
        meta: { label: t('Model') },
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
        meta: { label: t('Revenue') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Revenue')} />
        ),
        cell: ({ row }) => formatBusinessAmount(row.original.revenue_quota),
      },
      {
        accessorKey: 'cost_quota',
        meta: { label: t('Cost') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Cost')} />
        ),
        cell: ({ row }) => (
          <span className='text-muted-foreground'>
            {formatBusinessAmount(row.original.cost_quota)}
          </span>
        ),
      },
      {
        accessorKey: 'profit_quota',
        meta: { label: t('Profit') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Profit')} />
        ),
        cell: ({ row }) => formatBusinessAmount(row.original.profit_quota),
      },
      {
        accessorKey: 'commission_quota',
        meta: { label: t('Commission'), mobileBadge: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Commission')} />
        ),
        cell: ({ row }) => (
          <span
            className={
              row.original.commission_quota < 0
                ? 'text-destructive font-medium'
                : 'font-medium text-green-600'
            }
          >
            {row.original.commission_quota < 0 ? '' : '+'}
            {formatBusinessAmount(row.original.commission_quota)}
          </span>
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

function CommissionHistory() {
  const { t } = useTranslation()
  const columns = useMyCommissionColumns()
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: 20,
  })

  const { data, isLoading } = useQuery({
    queryKey: ['my-commission-logs', pagination],
    queryFn: () =>
      getMyCommissionLogs({
        page: pagination.pageIndex + 1,
        page_size: pagination.pageSize,
      }),
  })

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
      getRowClassName={(row) =>
        row.original.commission_quota < 0 ? 'opacity-60' : undefined
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

  const profile = profileData.data.profile
  const summary = summaryData?.data ?? profileData.data.extension
  const revenueUsd = summary?.profit_total_usd ?? 0

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        <span className='flex items-center gap-2'>
          {t('My Commission')}
          <Badge variant='default' className='text-xs'>
            {(profile.commission_rate * 100).toFixed(1)}% {t('rate')}
          </Badge>
          {profile.target_amount ? (
            <Badge
              variant={
                revenueUsd >= profile.target_amount ? 'default' : 'outline'
              }
              className='text-xs'
            >
              {t('Target')}: {formatBusinessTargetAmount(revenueUsd)} /{' '}
              {formatBusinessTargetAmount(profile.target_amount)}
              {revenueUsd >= profile.target_amount ? ` ${t('Reached')}` : ''}
            </Badge>
          ) : null}
        </span>
      </SectionPageLayout.Title>
      <SectionPageLayout.Content className='overflow-hidden'>
        <div className='flex h-full min-h-0 flex-col gap-4 overflow-hidden'>
          {summary && (
            <SummaryCards
              data={summary}
              commissionRate={profile.commission_rate}
              targetAmount={Number(profile.target_amount || 0)}
            />
          )}
          <h3 className='text-muted-foreground shrink-0 text-sm font-semibold'>
            {t('Commission Details')}
          </h3>
          <div className='min-h-0 flex-1 overflow-hidden'>
            <CommissionHistory />
          </div>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { TrendingUp, Users, DollarSign, Clock } from 'lucide-react'
import { SectionPageLayout } from '@/components/layout'
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
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
import {
  getMyEmployeeProfile,
  getMyCommissionLogs,
  getMyCommissionSummary,
} from './api'
import type { EmployeeExtension } from './api'

// ── Summary Cards ─────────────────────────────────────────────────────────────

function SummaryCards({ data }: { data: EmployeeExtension }) {
  const { t } = useTranslation()

  const cards = [
    {
      title: t('Total Commission'),
      value: formatQuota(data.commission_total_quota),
      sub: `≈ $${data.commission_total_usd.toFixed(4)}`,
      icon: TrendingUp,
      color: 'text-green-600',
    },
    {
      title: t('Pending Settlement'),
      value: formatQuota(data.commission_pending_quota),
      sub: `≈ $${data.commission_pending_usd.toFixed(4)}`,
      icon: Clock,
      color: 'text-yellow-600',
    },
    {
      title: t('Total Customer Revenue'),
      value: formatQuota(data.revenue_total_quota),
      sub: `≈ $${data.revenue_total_usd.toFixed(4)}`,
      icon: DollarSign,
      color: 'text-blue-600',
    },
    {
      title: t('Active Customers'),
      value: String(data.revenue_customer_count),
      sub: t('customers with consumption'),
      icon: Users,
      color: 'text-purple-600',
    },
  ]

  return (
    <div className='grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4'>
      {cards.map((card) => (
        <Card key={card.title}>
          <CardHeader className='flex flex-row items-center justify-between pb-2'>
            <CardTitle className='text-sm font-medium text-muted-foreground'>
              {card.title}
            </CardTitle>
            <card.icon className={`h-4 w-4 ${card.color}`} />
          </CardHeader>
          <CardContent>
            <div className='text-2xl font-bold'>{card.value}</div>
            <p className='mt-1 text-xs text-muted-foreground'>{card.sub}</p>
          </CardContent>
        </Card>
      ))}
    </div>
  )
}

// ── Commission Log Table ──────────────────────────────────────────────────────

function formatTs(ts: number) {
  if (!ts) return '-'
  return new Date(ts * 1000).toLocaleString()
}

function CommissionHistory() {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const pageSize = 20

  const { data, isLoading } = useQuery({
    queryKey: ['my-commission-logs', page],
    queryFn: () => getMyCommissionLogs({ page, page_size: pageSize }),
  })

  const logs = data?.data?.items ?? []
  const total = data?.data?.total ?? 0

  return (
    <div className='space-y-4'>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('Time')}</TableHead>
            <TableHead>{t('Customer')}</TableHead>
            <TableHead>{t('Model')}</TableHead>
            <TableHead>{t('Revenue')}</TableHead>
            <TableHead>{t('Cost')}</TableHead>
            <TableHead>{t('Profit')}</TableHead>
            <TableHead>{t('Commission')}</TableHead>
            <TableHead>{t('Rate')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {isLoading && (
            <TableRow>
              <TableCell colSpan={8} className='text-center text-muted-foreground'>
                {t('Loading...')}
              </TableCell>
            </TableRow>
          )}
          {!isLoading && logs.length === 0 && (
            <TableRow>
              <TableCell colSpan={8} className='text-center text-muted-foreground'>
                {t('No commission records yet')}
              </TableCell>
            </TableRow>
          )}
          {logs.map((log) => (
            <TableRow
              key={log.id}
              className={log.commission_quota < 0 ? 'opacity-60' : ''}
            >
              <TableCell className='text-xs'>{formatTs(log.created_at)}</TableCell>
              <TableCell>
                <Badge variant='outline'>
                  {log.customer_user_id_masked ?? `#${log.customer_user_id}`}
                </Badge>
              </TableCell>
              <TableCell className='max-w-[120px] truncate text-xs'>
                {log.model_name || '-'}
              </TableCell>
              <TableCell>{formatQuota(log.revenue_quota)}</TableCell>
              <TableCell className='text-muted-foreground'>
                {formatQuota(log.cost_quota)}
              </TableCell>
              <TableCell>{formatQuota(log.profit_quota)}</TableCell>
              <TableCell
                className={
                  log.commission_quota < 0
                    ? 'font-medium text-destructive'
                    : 'font-medium text-green-600'
                }
              >
                {log.commission_quota < 0 ? '' : '+'}
                {formatQuota(log.commission_quota)}
              </TableCell>
              <TableCell>{(log.commission_rate * 100).toFixed(1)}%</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>

      <div className='flex items-center justify-between text-sm text-muted-foreground'>
        <span>
          {t('Total')}: {total}
        </span>
        <div className='flex gap-2'>
          <Button
            size='sm'
            variant='outline'
            disabled={page <= 1}
            onClick={() => setPage((p) => p - 1)}
          >
            {t('Previous')}
          </Button>
          <Button
            size='sm'
            variant='outline'
            disabled={page * pageSize >= total}
            onClick={() => setPage((p) => p + 1)}
          >
            {t('Next')}
          </Button>
        </div>
      </div>
    </div>
  )
}

// ── Main Page ─────────────────────────────────────────────────────────────────

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
        <SectionPageLayout.Content>
          <p className='text-muted-foreground'>{t('Loading...')}</p>
        </SectionPageLayout.Content>
      </SectionPageLayout>
    )
  }

  if (!profileData?.success || !profileData.data) {
    return (
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('My Commission')}</SectionPageLayout.Title>
        <SectionPageLayout.Content>
          <p className='text-muted-foreground'>
            {t('You do not have employee status. Contact your administrator.')}
          </p>
        </SectionPageLayout.Content>
      </SectionPageLayout>
    )
  }

  const profile = profileData.data.profile
  const summary = summaryData?.data ?? profileData.data.extension

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        <span className='flex items-center gap-2'>
          {t('My Commission')}
          <Badge variant='default' className='text-xs'>
            {(profile.commission_rate * 100).toFixed(1)}% {t('rate')}
          </Badge>
          {profile.target_quota ? (
            <Badge variant='outline' className='text-xs'>
              {t('Target')}: {formatQuota(profile.target_quota)}
            </Badge>
          ) : null}
        </span>
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='space-y-6'>
          {summary && <SummaryCards data={summary} />}
          <Card>
            <CardHeader>
              <CardTitle>{t('Commission History')}</CardTitle>
            </CardHeader>
            <CardContent>
              <CommissionHistory />
            </CardContent>
          </Card>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

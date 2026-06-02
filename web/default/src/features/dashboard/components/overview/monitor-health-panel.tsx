/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Activity, ArrowRight, Boxes, Layers3, Timer } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  getGroupStatuses,
  getModelStatuses,
  type GroupStatusItem,
  type ModelStatusItem,
  type MonitorStatus,
} from '@/features/monitoring/api'
import { MonitorStatusBadge } from '@/features/monitoring/components/monitor-status-badge'
import { getStatusTargetName } from '@/features/monitoring/status'
import {
  formatLatency,
  formatUptimePct,
} from '@/features/performance-metrics/lib/format'

const TOP_STATUS_LIMIT = 4

function countByStatus<T extends { status: MonitorStatus }>(items: T[]) {
  return items.reduce(
    (acc, item) => {
      acc[item.status] += 1
      return acc
    },
    { 1: 0, 2: 0, 3: 0, 4: 0 } satisfies Record<MonitorStatus, number>
  )
}

function getWorstStatus(
  items: { status: MonitorStatus }[]
): MonitorStatus | null {
  if (items.length === 0) return null
  return items.reduce<MonitorStatus>(
    (worst, item) => Math.max(worst, item.status) as MonitorStatus,
    1
  )
}

function sortByRisk<T extends GroupStatusItem | ModelStatusItem>(items: T[]) {
  return [...items].sort((a, b) => {
    if (a.status !== b.status) return b.status - a.status
    if (a.success_rate !== b.success_rate)
      return a.success_rate - b.success_rate
    return b.total_requests - a.total_requests
  })
}

function StatCell(props: {
  label: string
  value: React.ReactNode
  tone?: 'success' | 'warning' | 'danger' | 'neutral'
}) {
  return (
    <div className='bg-muted/40 rounded-xl px-3 py-2.5'>
      <div className='text-muted-foreground text-[11px] font-medium'>
        {props.label}
      </div>
      <div
        className={cn(
          'mt-1 font-mono text-sm font-semibold tabular-nums',
          props.tone === 'success' && 'text-success',
          props.tone === 'warning' && 'text-warning',
          props.tone === 'danger' && 'text-destructive',
          props.tone === 'neutral' && 'text-muted-foreground'
        )}
      >
        {props.value}
      </div>
    </div>
  )
}

function StatusRow(props: {
  item: GroupStatusItem | ModelStatusItem
  kind: 'group' | 'model'
}) {
  const { t } = useTranslation()
  const name = getStatusTargetName(props.item)

  return (
    <div className='grid grid-cols-[minmax(0,1fr)_auto] gap-2 rounded-lg px-1.5 py-1.5'>
      <div className='min-w-0'>
        <div className='flex min-w-0 items-center gap-1.5'>
          <span className='truncate font-mono text-[11px]'>{name}</span>
          <MonitorStatusBadge status={props.item.status} size='sm' />
        </div>
        <div className='text-muted-foreground mt-0.5 flex flex-wrap gap-x-2 gap-y-0.5 text-[11px]'>
          <span>
            {t('Success rate')} {formatUptimePct(props.item.success_rate)}
          </span>
          <span>
            {t('P95')} {formatLatency(props.item.p95_response_time)}
          </span>
        </div>
      </div>
      <div className='text-right text-[11px]'>
        <div className='text-muted-foreground'>{t('Requests')}</div>
        <div className='font-mono font-semibold tabular-nums'>
          {props.item.total_requests}
        </div>
      </div>
    </div>
  )
}

function StatusList(props: {
  title: string
  icon: React.ComponentType<{ className?: string }>
  items: Array<GroupStatusItem | ModelStatusItem>
  kind: 'group' | 'model'
  loading: boolean
}) {
  const { t } = useTranslation()
  const Icon = props.icon

  return (
    <div className='rounded-xl border p-3'>
      <div className='mb-2 flex items-center gap-2'>
        <Icon className='text-muted-foreground/70 size-3.5' />
        <span className='text-sm font-medium'>{props.title}</span>
      </div>
      {props.loading ? (
        <div className='space-y-1.5'>
          {Array.from({ length: 3 }).map((_, index) => (
            <Skeleton key={index} className='h-9 w-full rounded-lg' />
          ))}
        </div>
      ) : props.items.length > 0 ? (
        <div className='divide-border divide-y'>
          {props.items.map((item) => (
            <StatusRow
              key={`${props.kind}-${getStatusTargetName(item)}`}
              item={item}
              kind={props.kind}
            />
          ))}
        </div>
      ) : (
        <div className='text-muted-foreground rounded-lg border border-dashed p-3 text-xs'>
          {t('No monitor data yet')}
        </div>
      )}
    </div>
  )
}

export function MonitorHealthPanel() {
  const { t } = useTranslation()
  const groupQuery = useQuery({
    queryKey: ['monitor', 'group-statuses'],
    queryFn: getGroupStatuses,
    staleTime: 60 * 1000,
    retry: false,
  })
  const modelQuery = useQuery({
    queryKey: ['monitor', 'model-statuses'],
    queryFn: getModelStatuses,
    staleTime: 60 * 1000,
    retry: false,
  })

  const groups = useMemo(() => groupQuery.data?.data ?? [], [groupQuery.data])
  const models = useMemo(() => modelQuery.data?.data ?? [], [modelQuery.data])
  const loading = groupQuery.isLoading || modelQuery.isLoading
  const groupCounts = useMemo(() => countByStatus(groups), [groups])
  const modelCounts = useMemo(() => countByStatus(models), [models])
  const worstStatus = getWorstStatus([...groups, ...models])
  const criticalCount = groupCounts[4] + modelCounts[4]
  const warningCount =
    groupCounts[2] + groupCounts[3] + modelCounts[2] + modelCounts[3]
  const healthyCount = groupCounts[1] + modelCounts[1]
  const topGroups = useMemo(
    () => sortByRisk(groups).slice(0, TOP_STATUS_LIMIT),
    [groups]
  )
  const topModels = useMemo(
    () => sortByRisk(models).slice(0, TOP_STATUS_LIMIT),
    [models]
  )

  return (
    <section className='bg-card h-full overflow-hidden rounded-2xl border shadow-xs'>
      <div className='flex items-center gap-2 border-b px-4 py-3 sm:px-5'>
        <Activity
          className='text-muted-foreground/60 size-4 shrink-0'
          aria-hidden='true'
        />
        <h3 className='text-sm font-semibold'>{t('Monitor health')}</h3>
        <div className='ml-auto flex items-center gap-2'>
          <MonitorStatusBadge status={worstStatus} size='sm' />
          <span className='text-muted-foreground hidden text-xs sm:inline'>
            {t('Group and model availability')}
          </span>
        </div>
      </div>

      <div className='space-y-3 p-4 sm:p-5'>
        <div className='grid grid-cols-3 gap-2'>
          <StatCell
            label={t('Unavailable')}
            value={loading ? '—' : criticalCount}
            tone={criticalCount > 0 ? 'danger' : 'neutral'}
          />
          <StatCell
            label={t('Needs attention')}
            value={loading ? '—' : warningCount}
            tone={warningCount > 0 ? 'warning' : 'neutral'}
          />
          <StatCell
            label={t('Healthy')}
            value={loading ? '—' : healthyCount}
            tone='success'
          />
        </div>

        <div className='grid gap-3 lg:grid-cols-2'>
          <StatusList
            title={t('Group health')}
            icon={Boxes}
            items={topGroups}
            kind='group'
            loading={loading}
          />
          <StatusList
            title={t('Model health')}
            icon={Layers3}
            items={topModels}
            kind='model'
            loading={loading}
          />
        </div>

        <div className='flex flex-wrap items-center justify-between gap-2 border-t pt-3'>
          <span className='text-muted-foreground inline-flex items-center gap-1.5 text-xs'>
            <Timer className='size-3.5' aria-hidden='true' />
            {t('Updated from scheduler snapshots')}
          </span>
          <div className='flex gap-2'>
            <Button
              variant='outline'
              size='sm'
              className='h-8'
              render={<Link to='/pricing' />}
            >
              {t('Models')}
              <ArrowRight data-icon='inline-end' />
            </Button>
            <Button
              variant='outline'
              size='sm'
              className='h-8'
              render={<Link to='/channels' />}
            >
              {t('Channels')}
              <ArrowRight data-icon='inline-end' />
            </Button>
          </div>
        </div>
      </div>
    </section>
  )
}

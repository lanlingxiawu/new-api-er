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
import { AlertTriangle } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { formatBillingCurrencyFromUSD } from '@/lib/currency'
import {
  formatLogQuota,
  formatTimestampToDate,
  formatUseTime,
} from '@/lib/format'
import { cn } from '@/lib/utils'

import type { LogDetailSection } from '../components/dialogs/log-detail-grid'
import type { UsageLog } from '../data/schema'
import { parseLogOther } from '../lib/format'
import {
  collectLogMetrics,
  findLowerMetrics,
  type LogMetricKey,
} from '../lib/log-metrics'
import { isPerCallBilling } from '../lib/utils'

type ComparisonFact = {
  label: string
  values: (string | undefined)[]
  copy?: boolean
}

const COMPARISON_SIDES = ['local', 'upstream'] as const

const TOKEN_ROWS = [
  ['tokens.input', 'Input Tokens'],
  ['tokens.output', 'Output Tokens'],
  ['tokens.cache_read', 'Cache Read'],
  ['tokens.cache_write', 'Cache Write'],
  ['tokens.cache_write_5m', 'Cache Write (5m)'],
  ['tokens.cache_write_1h', 'Cache Write (1h)'],
  ['tokens.cache_write_12h', 'Cache Write (12h)'],
] as const

const BILLING_ROWS = [
  ['total_cost', 'Total Cost'],
  ['price.input', 'Input'],
  ['price.output', 'Output'],
  ['price.cache_read', 'Cache Read'],
  ['price.cache_write', 'Cache Creation'],
  ['price.cache_write_5m', 'Cache Creation (5m)'],
  ['price.cache_write_1h', 'Cache Creation (1h)'],
  ['price.cache_write_12h', 'Cache Creation (12h)'],
  ['price.per_call', 'Model Price'],
  ['group_ratio', 'Group Ratio'],
] as const

function formatMetric(key: LogMetricKey, value: number): string {
  if (key === 'total_cost') return formatLogQuota(value)
  if (key === 'group_ratio') return `${value.toFixed(4)}x`
  if (key.startsWith('tokens.')) return value.toLocaleString()
  const price = formatBillingCurrencyFromUSD(value, {
    digitsLarge: 4,
    digitsSmall: 6,
    abbreviate: false,
  })
  return key === 'price.per_call' ? price : `${price}/M`
}

/** Align the same facts on one row; missing observations are never converted to zero. */
export function LogComparisonTable({
  local,
  upstream,
  section,
}: {
  local: UsageLog
  upstream: UsageLog
  section?: LogDetailSection
}) {
  const { t } = useTranslation()
  const logs = [local, upstream]
  const others = logs.map((log) => parseLogOther(log.other))
  const metrics = logs.map(collectLogMetrics)
  const lower = findLowerMetrics(metrics[0], metrics[1])
  const billingMode: ComparisonFact[] = [
    {
      label: 'Billing Mode',
      values: others.map((other) => {
        if (!other) return undefined
        if (other.billing_mode === 'tiered_expr') return t('Dynamic Pricing')
        if (isPerCallBilling(other.model_price)) return t('Per-call')
        return t('Per-token')
      }),
    },
  ]
  const identifiers: ComparisonFact[] = [
    {
      label: 'Request ID',
      values: logs.map((log) => log.request_id),
      copy: true,
    },
    {
      label: 'Upstream Request ID',
      values: logs.map((log) => log.upstream_request_id),
      copy: true,
    },
  ]
  const overview: ComparisonFact[] = [
    { label: 'Model', values: logs.map((log) => log.model_name) },
    {
      label: 'Created At',
      values: logs.map((log) =>
        log.created_at > 0 ? formatTimestampToDate(log.created_at) : undefined
      ),
    },
    {
      label: 'Response Time',
      values: logs.map((log) =>
        Number.isFinite(log.use_time) && log.use_time >= 0
          ? formatUseTime(log.use_time)
          : undefined
      ),
    },
    ...(others.some((other) => other?.frt != null)
      ? [
          {
            label: 'First response time',
            values: others.map((other) =>
              other?.frt != null && Number.isFinite(other.frt) && other.frt >= 0
                ? `${other.frt} ms`
                : undefined
            ),
          },
        ]
      : []),
    ...(section === 'diagnostics'
      ? [
          {
            label: 'Channel',
            values: logs.map((log) =>
              log.channel > 0
                ? `${log.channel}${log.channel_name ? ` (${log.channel_name})` : ''}`
                : undefined
            ),
          },
          {
            label: 'Retry Chain',
            values: others.map((other) =>
              other?.admin_info?.use_channel?.join(' → ')
            ),
          },
          { label: 'Token', values: logs.map((log) => log.token_name) },
          {
            label: 'Group',
            values: logs.map((log, index) => log.group || others[index]?.group),
          },
          { label: 'IP Address', values: logs.map((log) => log.ip) },
        ]
      : []),
  ]

  return (
    <div className='min-w-0 overflow-hidden rounded-lg border'>
      <Table
        aria-label={t('Query Upstream')}
        className='table-fixed [&_td]:font-normal'
        withContainer={false}
      >
        <TableHeader className='bg-muted/50 max-sm:block'>
          <TableRow className='max-sm:grid max-sm:grid-cols-2'>
            <TableHead
              scope='col'
              className='w-40 px-3 whitespace-normal max-sm:hidden'
            >
              {t('Field')}
            </TableHead>
            <TableHead scope='col' className='border-l px-3'>
              {t('Local Logs')}
            </TableHead>
            <TableHead scope='col' className='border-l px-3'>
              {t('Upstream Logs')}
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody className='max-sm:block [&>tr]:h-auto'>
          <ComparisonFactRows rows={overview} />
          {section !== 'diagnostics' &&
            [
              { title: 'Token Breakdown', rows: TOKEN_ROWS, facts: undefined },
              {
                title: 'Billing Details',
                rows: BILLING_ROWS,
                facts: billingMode,
              },
            ].map((group) => (
              <ComparisonMetricRows
                key={group.title}
                title={t(group.title)}
                rows={group.rows}
                metrics={metrics}
                lower={lower}
                facts={group.facts}
              />
            ))}
          <ComparisonFactRows rows={identifiers} />
        </TableBody>
      </Table>
    </div>
  )
}

function ComparisonFactRows({ rows }: { rows: ComparisonFact[] }) {
  const { t } = useTranslation()
  return rows
    .filter((row) => row.values.some((value) => value != null && value !== ''))
    .map((row) => (
      <TableRow key={row.label} className='max-sm:grid max-sm:grid-cols-2'>
        <TableHead
          scope='row'
          className='text-muted-foreground max-sm:bg-muted/20 h-auto px-3 py-2 font-normal whitespace-normal max-sm:col-span-2 max-sm:border-b max-sm:py-1.5'
        >
          {t(row.label)}
        </TableHead>
        {COMPARISON_SIDES.map((side, index) => {
          const value = row.values[index]
          return (
            <TableCell
              key={side}
              className='border-l px-3 py-1.5 whitespace-normal max-sm:first-of-type:border-l-0'
            >
              <div className='flex min-w-0 items-center gap-1 max-sm:flex-wrap'>
                <span
                  className={cn(
                    'min-w-0 flex-1 break-all select-text',
                    row.copy && 'max-sm:basis-full',
                    !value && 'text-muted-foreground'
                  )}
                >
                  {value || t('Not recorded')}
                </span>
                {row.copy && value && (
                  <CopyButton
                    value={value}
                    size='icon'
                    variant='ghost'
                    className='max-sm:ml-auto'
                    aria-label={t('Copy {{field}}', {
                      field: `${t(index === 0 ? 'Local Logs' : 'Upstream Logs')} · ${t(row.label)}`,
                    })}
                  />
                )}
              </div>
            </TableCell>
          )
        })}
      </TableRow>
    ))
}

function ComparisonMetricRows({
  title,
  rows,
  metrics,
  lower,
  facts,
}: {
  title: string
  rows: readonly (readonly [LogMetricKey, string])[]
  metrics: ReturnType<typeof collectLogMetrics>[]
  lower: ReturnType<typeof findLowerMetrics>
  facts?: ComparisonFact[]
}) {
  const { t } = useTranslation()
  const visibleRows = rows.filter(([key]) =>
    metrics.some((side) => side[key] != null)
  )
  if (!visibleRows.length) return null
  return (
    <>
      <TableRow className='bg-muted/30 max-sm:grid'>
        <TableHead colSpan={3} className='h-9 px-3'>
          {title}
        </TableHead>
      </TableRow>
      {facts && <ComparisonFactRows rows={facts} />}
      {visibleRows.map(([key, label]) => (
        <TableRow key={key} className='max-sm:grid max-sm:grid-cols-2'>
          <TableHead
            scope='row'
            className='text-muted-foreground max-sm:bg-muted/20 h-auto px-3 py-2 font-normal whitespace-normal max-sm:col-span-2 max-sm:border-b max-sm:py-1.5'
          >
            {t(label)}
          </TableHead>
          {COMPARISON_SIDES.map((side, index) => {
            const value = metrics[index][key]
            const warning =
              index === 0 && lower[key] != null
                ? t('Lower than upstream: {{value}}', {
                    value: formatMetric(key, lower[key]),
                  })
                : undefined
            return (
              <TableCell
                key={side}
                className={cn(
                  'border-l px-3 py-2 whitespace-normal max-sm:first-of-type:border-l-0',
                  warning && 'bg-warning/5 text-warning'
                )}
                title={warning}
              >
                <span className='inline-flex items-center gap-1.5 break-all'>
                  {warning && (
                    <AlertTriangle
                      className='size-3.5 shrink-0'
                      aria-hidden='true'
                    />
                  )}
                  {value == null ? (
                    <span className='text-muted-foreground'>
                      {t('Not recorded')}
                    </span>
                  ) : (
                    formatMetric(key, value)
                  )}
                  {warning && <span className='sr-only'>{warning}</span>}
                </span>
              </TableCell>
            )
          })}
        </TableRow>
      ))}
    </>
  )
}

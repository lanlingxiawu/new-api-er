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
import type { CellContext, ColumnDef } from '@tanstack/react-table'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { TruncatedCell } from '@/components/data-table'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import dayjs from '@/lib/dayjs'

import type { AuditActivity } from '../api'
import { activityChanges, activityLabel, activitySource } from '../lib/activity'
import { AuditActivityDetailsDialog } from './audit-activity-details-dialog'

export function useAuditActivityColumns<
  T extends AuditActivity = AuditActivity,
>(
  onIPFilter?: (ip: string) => void,
  accountName?: string | ((entry: T) => string),
  accountLabel: 'Username' | 'Customer' = 'Username'
): ColumnDef<T>[] {
  const { t } = useTranslation()
  return useMemo<ColumnDef<T>[]>(
    () => [
      {
        accessorKey: 'created_at',
        header: t('Time'),
        size: 180,
        cell: ({ row }) => (
          <span className='font-mono tabular-nums'>
            {dayjs.unix(row.original.created_at).format('YYYY-MM-DD HH:mm:ss')}
          </span>
        ),
        meta: { label: t('Time'), mobileTitle: true },
      },
      ...(accountName
        ? [
            {
              id: 'account',
              header: t(accountLabel),
              size: 100,
              accessorFn: (entry: T) =>
                typeof accountName === 'function'
                  ? accountName(entry)
                  : accountName,
              cell: ({ getValue }: CellContext<T, unknown>) => (
                <TruncatedCell>{getValue<string>()}</TruncatedCell>
              ),
              enableHiding: false,
              meta: { label: t(accountLabel) },
            },
          ]
        : []),
      {
        id: 'event',
        header: t('Event'),
        size: 280,
        accessorFn: (entry) =>
          [
            activityLabel(entry, t),
            entry.target?.name,
            entry.target?.id && t('(ID: {{id}})', { id: entry.target.id }),
            activityChanges(entry, t),
          ]
            .filter(Boolean)
            .join(' · '),
        cell: ({ row }) => {
          const entry = row.original
          const changes = activityChanges(entry, t)
          return (
            <div className='min-w-0 space-y-1'>
              <span>{activityLabel(entry, t)}</span>
              {(entry.target?.name || entry.target?.id) && (
                <div className='text-muted-foreground flex min-w-0 items-baseline gap-1'>
                  {entry.target.name && (
                    <TruncatedCell className='min-w-0'>
                      {entry.target.name}
                    </TruncatedCell>
                  )}
                  {entry.target.id && (
                    <span className='shrink-0 whitespace-nowrap'>
                      {t('(ID: {{id}})', { id: entry.target.id })}
                    </span>
                  )}
                </div>
              )}
              {changes && (
                <TruncatedCell
                  className='text-muted-foreground'
                  contentClassName='line-clamp-2 whitespace-normal break-words'
                  tooltipContent={changes}
                >
                  {changes}
                </TruncatedCell>
              )}
            </div>
          )
        },
        meta: { label: t('Event') },
      },
      {
        accessorKey: 'success',
        header: t('Result'),
        size: 82,
        cell: ({ row }) => (
          <StatusBadge
            label={row.original.success ? t('Success') : t('Failed')}
            variant={row.original.success ? 'success' : 'danger'}
            copyable={false}
          />
        ),
        meta: { label: t('Result'), mobileBadge: true },
      },
      {
        accessorKey: 'ip',
        header: 'IP',
        size: 200,
        cell: ({ row }) => (
          <div className='space-y-1'>
            <span className='inline-flex items-center gap-1'>
              {row.original.ip || '—'}
              {row.original.ip && (
                <CopyButton value={row.original.ip} className='size-5' />
              )}
            </span>
            {onIPFilter && row.original.ip && (
              <Button
                variant='link'
                size='sm'
                className='h-auto p-0 text-xs'
                onClick={() => onIPFilter(row.original.ip)}
              >
                {t('Filter by this IP')}
              </Button>
            )}
          </div>
        ),
        meta: { label: 'IP' },
      },
      {
        accessorKey: 'client',
        header: t('Client'),
        size: 140,
        cell: ({ row }) => t(row.original.client || 'Unknown client'),
        meta: { label: t('Client'), mobileHidden: true },
      },
      {
        accessorKey: 'source',
        header: t('Operation source'),
        size: 120,
        cell: ({ row }) => activitySource(row.original.source, t),
        meta: { label: t('Operation source') },
      },
      {
        id: 'details',
        header: t('Details'),
        size: 70,
        enableHiding: false,
        cell: ({ row }) => (
          <AuditActivityDetailsDialog
            entry={row.original}
            accountName={
              typeof accountName === 'function'
                ? accountName(row.original)
                : accountName
            }
            accountLabel={accountLabel}
          />
        ),
        meta: { label: t('Details') },
      },
    ],
    [onIPFilter, accountName, accountLabel, t]
  )
}

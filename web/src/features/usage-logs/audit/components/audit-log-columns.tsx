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
import type { ColumnDef } from '@tanstack/react-table'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { TruncatedCell } from '@/components/data-table'
import { StatusBadge } from '@/components/status-badge'
import dayjs from '@/lib/dayjs'

import type { AuditLog } from '../api'
import { buildAuditDetails } from '../lib/audit-details'
import { auditActorLabel, identifyAuditClient } from '../lib/source'
import { AuditIPCell } from './audit-ip-cell'
import { AuditLogDetailsDialog } from './audit-log-details-dialog'

export function useAuditLogColumns(
  accessOnly?: boolean,
  onIPFilter?: (ip: string) => void
): ColumnDef<AuditLog>[] {
  const { t } = useTranslation()
  return useMemo(() => {
    const columns: ColumnDef<AuditLog>[] = [
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
    ]
    if (!accessOnly) {
      columns.push(
        {
          accessorKey: 'username',
          header: t('Username'),
          size: 100,
          meta: { label: t('Username') },
        },
        {
          id: 'event',
          header: t('Event'),
          size: 360,
          accessorFn: (entry) => {
            const detail = buildAuditDetails(entry, t)
            return [detail.summary, detail.operation?.description]
              .filter(Boolean)
              .join(' · ')
          },
          cell: ({ row, getValue }) => {
            const operation = buildAuditDetails(row.original, t).operation
            if (!operation) {
              return (
                <TruncatedCell className='max-w-64'>
                  {getValue<string>()}
                </TruncatedCell>
              )
            }
            return (
              <div className='min-w-0 space-y-1'>
                <div className='flex min-w-0 items-baseline gap-1'>
                  <TruncatedCell
                    className='min-w-0 font-medium'
                    tooltipContent={operation.summary}
                  >
                    {operation.headline}
                  </TruncatedCell>
                  {operation.identifier && (
                    <span className='shrink-0 whitespace-nowrap'>
                      {operation.identifier}
                    </span>
                  )}
                </div>
                {operation.description && (
                  <TruncatedCell
                    className='text-muted-foreground'
                    contentClassName='line-clamp-2 whitespace-normal break-words'
                    tooltipContent={operation.description}
                  >
                    {operation.description}
                  </TruncatedCell>
                )}
              </div>
            )
          },
          meta: { label: t('Event') },
        }
      )
    }
    columns.push(
      {
        id: 'actor_identity',
        header: t('Actor identity'),
        size: 150,
        cell: ({ row }) =>
          t(
            auditActorLabel(
              row.original.actor_role,
              row.original.actor_is_employee
            )
          ),
        meta: { label: t('Actor identity') },
      },
      {
        accessorKey: 'auth_method',
        header: t('Operation source'),
        size: 120,
        cell: ({ row }) => {
          if (row.original.auth_method === 'session') return t('Web session')
          if (row.original.auth_method === 'access_token') {
            return t('Access Token')
          }
          return t('Unknown source')
        },
        meta: { label: t('Operation source') },
      },
      {
        accessorKey: 'ip',
        header: 'IP',
        size: 240,
        cell: ({ row }) => (
          <AuditIPCell entry={row.original} onFilter={onIPFilter} />
        ),
        meta: { label: 'IP' },
      },
      {
        accessorKey: 'user_agent',
        header: t('Client'),
        size: 180,
        cell: ({ row }) => (
          <TruncatedCell className='max-w-48'>
            {t(identifyAuditClient(row.original.user_agent))}
          </TruncatedCell>
        ),
        meta: { label: t('Client'), mobileHidden: true },
      },
      {
        accessorKey: 'method',
        header: t('Request method'),
        size: 76,
        cell: ({ row }) => (
          <span className='text-muted-foreground font-mono'>
            {row.original.method || '—'}
          </span>
        ),
        meta: { label: t('Request method') },
      },
      {
        accessorKey: 'route',
        header: t('Route'),
        size: 220,
        cell: ({ row }) => (
          <TruncatedCell className='max-w-60 font-mono'>
            {row.original.route || '—'}
          </TruncatedCell>
        ),
        meta: { label: t('Route') },
      },
      {
        accessorKey: 'status',
        header: 'HTTP',
        size: 60,
        cell: ({ row }) => (
          <span className='font-mono tabular-nums'>
            {row.original.status || '—'}
          </span>
        ),
        meta: { label: 'HTTP' },
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
        id: 'details',
        header: t('Details'),
        size: 70,
        enableHiding: false,
        cell: ({ row }) => <AuditLogDetailsDialog entry={row.original} />,
        meta: { label: t('Details') },
      }
    )
    return columns
  }, [accessOnly, t, onIPFilter])
}

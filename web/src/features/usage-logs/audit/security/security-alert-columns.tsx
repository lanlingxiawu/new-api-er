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

import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'

import { auditFieldLabel } from '../lib/audit-field-labels'
import { AccountResponses } from './account-response-status'
import { AlertAssignees } from './alert-assignees'
import { AlertConfirmButton } from './alert-confirm-button'
import { AlertHandlingStatus } from './alert-handling-status'
import { AlertTriggerCount } from './alert-trigger-count'
import type { SecurityAlert } from './api'
import {
  resources,
  securityTargetLabel,
  securityActorLabel,
  securityTime,
  severities,
  severityVariants,
} from './lib/security-display'
import { securityRules } from './lib/security-rules'

export function useSecurityAlertColumns(
  canManage: boolean,
  onSelect: (id: number) => void,
  confirming = false,
  onConfirmed?: () => void
): ColumnDef<SecurityAlert>[] {
  const { t } = useTranslation()
  return useMemo(
    () => [
      {
        accessorKey: 'severity',
        header: t('Severity'),
        size: 90,
        cell: ({ row }) => (
          <StatusBadge
            copyable={false}
            variant={severityVariants[row.original.severity] ?? 'neutral'}
          >
            {t(severities[row.original.severity] ?? 'Medium')}
          </StatusBadge>
        ),
        meta: { label: t('Severity'), mobileBadge: true },
      },
      {
        accessorKey: 'rule',
        header: t('Matched rule'),
        size: 220,
        cell: ({ row }) => (
          <Button
            variant='link'
            disabled={confirming}
            className='h-auto max-w-full justify-start p-0 text-left whitespace-normal'
            onClick={() => onSelect(row.original.id)}
          >
            {securityRules[row.original.rule]
              ? t(securityRules[row.original.rule])
              : row.original.rule || t('Security alert')}{' '}
            #{row.original.id}
          </Button>
        ),
        meta: { label: t('Matched rule'), mobileTitle: true },
      },
      {
        accessorKey: 'status',
        header: t('Handling status'),
        size: 160,
        cell: ({ row }) => <AlertHandlingStatus status={row.original.status} />,
        meta: { label: t('Handling status') },
      },
      {
        accessorKey: 'actor_id',
        header: t('Operator'),
        size: 180,
        cell: ({ row }) => (
          <div className='min-w-0 space-y-1 wrap-anywhere'>
            <span>
              {securityActorLabel(row.original, t('Unidentified account'))}
            </span>
            {!!row.original.target_user_id && (
              <div className='text-xs'>
                <span className='text-muted-foreground'>
                  {t('Target account')}:{' '}
                </span>
                <span>{securityTargetLabel(row.original)}</span>
              </div>
            )}
            {row.original.attempted_account && (
              <div className='text-xs'>
                <span className='text-muted-foreground'>
                  {auditFieldLabel('attempted_account', t, row.original.rule)}
                  :{' '}
                </span>
                <span>{row.original.attempted_account}</span>
              </div>
            )}
          </div>
        ),
        meta: { label: t('Operator') },
      },
      {
        accessorKey: 'resource_type',
        header: t('Resource'),
        size: 100,
        cell: ({ row }) =>
          t(resources[row.original.resource_type] ?? 'Not recorded'),
        meta: { label: t('Resource') },
      },
      {
        accessorKey: 'occurrences',
        header: t('Alert trigger count'),
        size: 110,
        cell: ({ row }) => <AlertTriggerCount alert={row.original} />,
        meta: { label: t('Alert trigger count') },
      },
      {
        accessorKey: 'matched_count',
        header: t('Peak matching count'),
        size: 120,
        meta: { label: t('Peak matching count') },
      },
      {
        accessorKey: 'first_seen',
        header: t('First event'),
        size: 180,
        cell: ({ row }) => (
          <span className='font-mono tabular-nums'>
            {securityTime(row.original.first_seen)}
          </span>
        ),
        meta: { label: t('First event') },
      },
      {
        accessorKey: 'last_seen',
        header: t('Latest event'),
        size: 180,
        cell: ({ row }) => (
          <span className='font-mono tabular-nums'>
            {securityTime(row.original.last_seen)}
          </span>
        ),
        meta: { label: t('Latest event') },
      },
      {
        accessorKey: 'assignee_id',
        header: t('Assignee'),
        size: 100,
        cell: ({ row }) => (
          <AlertAssignees
            assignees={row.original.assignees}
            legacyID={row.original.assignee_id}
          />
        ),
        meta: { label: t('Assignee') },
      },
      {
        id: 'response',
        header: t('Response result'),
        size: 150,
        cell: ({ row }) => (
          <AccountResponses
            response={row.original.response}
            responses={row.original.responses}
          />
        ),
        meta: { label: t('Response result') },
      },
      ...(canManage
        ? [
            {
              id: 'actions',
              header: t('Actions'),
              size: 100,
              cell: ({ row }: { row: { original: SecurityAlert } }) =>
                row.original.status === 'open' ||
                row.original.status === 'investigating' ? (
                  <AlertConfirmButton
                    alert={row.original}
                    disabled={confirming}
                    onConfirmed={onConfirmed}
                    onViewRecord={() => onSelect(row.original.id)}
                  />
                ) : (
                  '—'
                ),
              meta: { label: t('Actions'), pinned: 'right' as const },
            },
          ]
        : []),
    ],
    [t, canManage, onSelect, confirming, onConfirmed]
  )
}

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

import {
  securityTime,
  severities,
  severityVariants,
} from '../security/lib/security-display'
import { securityRules } from '../security/lib/security-rules'
import type { SelfSecurityAlert } from './api'

export function useSelfSecurityColumns(
  onSelect: (id: number) => void
): ColumnDef<SelfSecurityAlert>[] {
  const { t } = useTranslation()
  return useMemo(
    () => [
      {
        accessorKey: 'rule',
        header: t('Matched rule'),
        size: 250,
        cell: ({ row }) => (
          <Button
            variant='link'
            className='h-auto max-w-full justify-start p-0 text-left whitespace-normal'
            onClick={() => onSelect(row.original.id)}
          >
            {t(securityRules[row.original.rule] ?? 'Security alert')} #
            {row.original.id}
          </Button>
        ),
        meta: { label: t('Matched rule'), mobileTitle: true },
      },
      {
        accessorKey: 'severity',
        header: t('Severity'),
        size: 100,
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
    ],
    [t, onSelect]
  )
}

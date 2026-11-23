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
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { useAuthStore } from '@/stores/auth-store'

import type { IPRiskMarker, RiskCoverage } from '../api'
import { securityGet } from './api'
import { securityRules } from './lib/security-rules'

export function IPRiskReasons({
  eventId,
  onSelect,
}: {
  eventId: string
  onSelect: (id: number) => void
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const [cursors, setCursors] = useState([0])
  const query = useQuery({
    queryKey: [
      'audit-security',
      user?.id,
      user?.role,
      user?.permissions,
      'ip-reasons',
      eventId,
      cursors.at(-1),
    ],
    queryFn: ({ signal }) =>
      securityGet<{
        items: IPRiskMarker['reasons']
        next_cursor: number
        revision: number
        coverage: RiskCoverage
      }>('ip-reasons', signal, {
        event_id: eventId,
        after_id: cursors.at(-1),
        limit: 20,
      }),
    gcTime: 0,
    retry: false,
  })
  return (
    <div className='space-y-1'>
      {query.isPending && <p className='text-xs'>{t('Loading...')}</p>}
      {query.isError && (
        <Button variant='link' size='sm' onClick={() => void query.refetch()}>
          {t('Unable to load IP risk reasons. Retry')}
        </Button>
      )}
      {query.data && !query.isError && (
        <>
          {query.data.items.map((reason) => (
            <Button
              key={`${reason.rule}:${reason.alert_id}`}
              variant='link'
              size='sm'
              className='h-auto p-0 text-xs whitespace-normal'
              onClick={() => onSelect(reason.alert_id)}
            >
              {t(securityRules[reason.rule] ?? 'Security alert')} #
              {reason.alert_id}
            </Button>
          ))}
          {!query.data.items.length && (
            <p className='text-xs'>{t('No records')}</p>
          )}
          {query.data.coverage && !query.data.coverage.historical_complete && (
            <p className='text-muted-foreground text-xs'>
              {t('Related event history is incomplete.')}
            </p>
          )}
          <div className='flex gap-2'>
            <Button
              variant='ghost'
              size='sm'
              disabled={cursors.length === 1 || query.isFetching}
              onClick={() => setCursors((values) => values.slice(0, -1))}
            >
              {t('Previous page')}
            </Button>
            <Button
              variant='ghost'
              size='sm'
              disabled={!query.data.next_cursor || query.isFetching}
              onClick={() =>
                setCursors((values) => [...values, query.data.next_cursor])
              }
            >
              {t('Next page')}
            </Button>
          </div>
        </>
      )}
    </div>
  )
}

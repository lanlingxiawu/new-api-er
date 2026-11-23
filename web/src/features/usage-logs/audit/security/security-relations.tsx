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

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { useAuthStore } from '@/stores/auth-store'

import { DetailSection } from '../../components/dialogs/log-detail-layout'
import { AuditIPAddress } from '../components/audit-ip-cell'
import { auditFieldLabel } from '../lib/audit-field-labels'
import {
  securityGet,
  type SecurityRelation,
  type SecurityCoverage,
} from './api'
import {
  securityTargetLabel,
  securityActorLabel,
  securityTime,
} from './lib/security-display'
import { securityRules } from './lib/security-rules'

export function SecurityRelations({
  id,
  onSelect,
  anchor,
}: {
  id: number
  anchor: number
  onSelect: (id: number) => void
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const [range] = useState(() => ({
    start: Math.max(0, anchor - 15 * 86400),
    end: anchor + 15 * 86400,
  }))
  const [relation, setRelation] = useState('')
  const [cursors, setCursors] = useState<string[]>([''])
  const query = useQuery({
    queryKey: [
      'audit-security',
      user?.id,
      user?.role,
      user?.permissions,
      'relations',
      id,
      relation,
      range,
      cursors.at(-1),
    ],
    queryFn: ({ signal }) =>
      securityGet<{
        items: SecurityRelation[]
        next_cursor?: string
        coverage: SecurityCoverage
      }>(`alerts/${id}/relations`, signal, {
        relation,
        ...range,
        cursor: cursors.at(-1),
        limit: 20,
      }),
    enabled: !!relation,
    gcTime: 0,
    retry: false,
  })
  const groups = new Map<string, SecurityRelation[]>()
  for (const item of query.data?.items ?? []) {
    // Only a recorded event with matching context can share a presentation.
    // A request or IP alone does not establish that two findings are one event.
    const key = item.event_id
      ? JSON.stringify([
          item.event_id,
          item.at,
          item.actor_id,
          item.actor_role,
          item.target_id,
          item.target_role,
          item.resource_type,
          item.ip,
          item.request_id,
          item.attempted_account,
        ])
      : `finding:${item.finding_id}`
    const group = groups.get(key)
    if (group) group.push(item)
    else groups.set(key, [item])
  }
  return (
    <DetailSection label={t('Related alerts')}>
      <div className='flex flex-wrap gap-2'>
        {(
          [
            ['account', 'Account'],
            ['ip', 'IP'],
            ['request', 'Request ID'],
          ] as const
        ).map(([value, label]) => (
          <Button
            key={value}
            size='sm'
            variant={relation === value ? 'secondary' : 'outline'}
            onClick={() => {
              setRelation(value)
              setCursors([''])
            }}
          >
            {t(label)}
          </Button>
        ))}
      </div>
      {relation && query.isPending && <LoadingState inline size='sm' />}
      {query.isError && (
        <ErrorState
          title={t('Unable to load related alerts')}
          onRetry={() => {
            setCursors([''])
            void query.refetch()
          }}
        />
      )}
      {query.data && !query.isError && (
        <>
          {!query.data.items.length && (
            <p className='text-muted-foreground text-xs'>{t('No records')}</p>
          )}
          {query.data.coverage && !query.data.coverage.complete && (
            <p className='text-muted-foreground text-xs'>
              {t('Related event history is incomplete.')}
            </p>
          )}
          {query.data.items.length > 0 && (
            <p className='text-muted-foreground text-xs'>
              {t('Alerts from the same event are grouped within each page.')}
            </p>
          )}
          {[...groups].map(([key, items]) => {
            const item = items[0]
            return (
              <div
                key={key}
                role='group'
                aria-label={t('Related alerts')}
                className='space-y-1 rounded-md border p-3'
              >
                <div className='flex flex-wrap gap-2'>
                  {items.map((finding) => (
                    <Button
                      key={finding.finding_id}
                      size='sm'
                      variant='link'
                      className='h-auto text-left whitespace-normal'
                      onClick={() => onSelect(finding.alert_id)}
                    >
                      {t(securityRules[finding.rule] ?? finding.rule)} #
                      {finding.alert_id}
                    </Button>
                  ))}
                </div>
                <p className='text-muted-foreground text-xs'>
                  {securityTime(item.at)} · {t('Operator ID')}:{' '}
                  {securityActorLabel(item, t('Unidentified account'))} ·{' '}
                  {t('Target ID')}: {item.target_id || '—'}
                </p>
                {item.attempted_account && (
                  <p className='text-xs wrap-anywhere'>
                    {auditFieldLabel(
                      'attempted_account',
                      t,
                      items.length === 1 ? item.rule : undefined
                    )}
                    : {item.attempted_account}
                  </p>
                )}
                {!!item.target_role && item.target_id > 0 && (
                  <p className='text-xs wrap-anywhere'>
                    {t('Target account')}: {securityTargetLabel(item)}
                  </p>
                )}
                <AuditIPAddress ip={item.ip} />
                <p className='text-muted-foreground text-xs break-all'>
                  {t('Request ID')}: {item.request_id || '—'}
                </p>
              </div>
            )
          })}
          <div className='flex justify-end gap-2'>
            <Button
              size='sm'
              variant='outline'
              disabled={cursors.length === 1 || query.isFetching}
              onClick={() => setCursors((values) => values.slice(0, -1))}
            >
              {t('Previous page')}
            </Button>
            <Button
              size='sm'
              variant='outline'
              disabled={!query.data.next_cursor || query.isFetching}
              onClick={() =>
                setCursors((values) => [
                  ...values,
                  query.data.next_cursor ?? '',
                ])
              }
            >
              {t('Next page')}
            </Button>
          </div>
        </>
      )}
    </DetailSection>
  )
}

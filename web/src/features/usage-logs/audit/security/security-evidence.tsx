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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { Button } from '@/components/ui/button'

import type { IPRiskMarker } from '../api'
import { AuditIPCell } from '../components/audit-ip-cell'
import { auditActionLabel } from '../lib/audit-action-labels'
import type { SecurityEvent } from './api'
import {
  securityActorLabel,
  securityEventResultKey,
  securityTime,
} from './lib/security-display'
import { SecurityEventCard } from './security-event-card'

export function SecurityEvidence(props: {
  events: SecurityEvent[]
  ipMarkers?: IPRiskMarker[]
  onSelectAlert?: (id: number) => void
  onInvestigate?: (event: SecurityEvent) => void
}) {
  const { t } = useTranslation()
  const [selected, setSelected] = useState<string>()
  const event =
    props.events.find((item) => item.event_id === selected) ?? props.events[0]
  const markers = new Map(
    props.ipMarkers?.map((marker) => [marker.event_id, marker])
  )
  if (!event) {
    return <p className='text-muted-foreground text-sm'>{t('No records')}</p>
  }
  const resultLabel = (item: SecurityEvent) =>
    t(securityEventResultKey(item.action, item.success))
  const single = props.events.length < 2
  return (
    <>
      <div className={single ? 'space-y-3' : 'hidden space-y-3 md:block'}>
        {!single && (
          <StaticDataTable
            data={props.events}
            getRowKey={(row) => row.event_id}
            getRowClassName={(row) =>
              row.event_id === event.event_id ? 'bg-muted' : undefined
            }
            columns={[
              {
                id: 'time',
                header: t('Event time'),
                cell: (row) => securityTime(row.at),
              },
              {
                id: 'action',
                header: t('Action'),
                cell: (row) => (
                  <Button
                    variant='link'
                    size='sm'
                    className='h-auto p-0 text-left whitespace-normal'
                    aria-pressed={row.event_id === event.event_id}
                    onClick={() => setSelected(row.event_id)}
                  >
                    {auditActionLabel(row.action, t) ?? row.action}
                  </Button>
                ),
              },
              {
                id: 'actor',
                header: t('Operator'),
                cell: (row) =>
                  securityActorLabel(row, t('Unidentified account')),
              },
              {
                id: 'ip',
                header: 'IP',
                cell: (row) => (
                  <AuditIPCell
                    entry={{
                      event_id: row.event_id,
                      ip: row.ip,
                      ip_marker: markers.get(row.event_id),
                    }}
                    onSelectAlert={props.onSelectAlert}
                  />
                ),
              },
              {
                id: 'result',
                header: t('Status'),
                cell: (row) => resultLabel(row),
              },
            ]}
          />
        )}
        <SecurityEventCard
          key={event.event_id}
          event={event}
          ipMarker={markers.get(event.event_id)}
          onSelectAlert={props.onSelectAlert}
          onInvestigate={props.onInvestigate}
        />
      </div>
      {!single && (
        <div className='space-y-2 md:hidden'>
          {props.events.map((item) => (
            <details key={item.event_id} className='rounded-md border p-3'>
              <summary className='cursor-pointer text-sm'>
                {auditActionLabel(item.action, t) ?? item.action} ·{' '}
                {securityTime(item.at)} · {resultLabel(item)}
              </summary>
              <div className='mt-3'>
                <SecurityEventCard
                  event={item}
                  ipMarker={markers.get(item.event_id)}
                  onSelectAlert={props.onSelectAlert}
                  onInvestigate={props.onInvestigate}
                />
              </div>
            </details>
          ))}
        </div>
      )}
    </>
  )
}

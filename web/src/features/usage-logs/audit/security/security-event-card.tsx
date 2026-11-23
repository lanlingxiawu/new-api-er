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
import { ChevronDown } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'

import { DetailRow } from '../../components/dialogs/log-detail-layout'
import { renderAuditContent } from '../../lib/format'
import type { IPRiskMarker } from '../api'
import { AuditDetailFields } from '../components/audit-detail-fields'
import { AuditDetailValue } from '../components/audit-detail-value'
import { AuditIPCell } from '../components/audit-ip-cell'
import { auditActionLabel } from '../lib/audit-action-labels'
import { auditFieldLabel, isAuditDetailObject } from '../lib/audit-details'
import { auditParameterValue } from '../lib/audit-parameter-value'
import type { SecurityEvent } from './api'
import {
  securityTargetLabel,
  securityActorLabel,
  securityEventResultKey,
  securityTime,
} from './lib/security-display'
import { SecurityIPSummary } from './security-ip-summary'

export function SecurityEventCard(props: {
  event: SecurityEvent
  ipMarker?: IPRiskMarker
  onSelectAlert?: (id: number) => void
  onInvestigate?: (event: SecurityEvent) => void
}) {
  const { t } = useTranslation()
  let facts: Record<string, unknown> = {}
  let unavailable = false
  try {
    const parsed: unknown = JSON.parse(props.event.facts || '{}')
    if (isAuditDetailObject(parsed)) facts = parsed
    else unavailable = true
  } catch {
    unavailable = true
  }
  const headline =
    auditActionLabel(props.event.action, t) ??
    renderAuditContent({ op: { action: props.event.action, params: {} } }, t) ??
    props.event.action
  const fields = Object.entries(facts).map(([key, value]) => ({
    key,
    action: props.event.action,
    label: auditFieldLabel(key, t, props.event.action),
    value,
  }))
  const technicalFields = fields.filter((field) =>
    ['client_ref', 'ip_intelligence'].includes(field.key)
  )
  const primaryFields = fields.filter(
    (field) => !['client_ref', 'ip_intelligence'].includes(field.key)
  )
  const intelligence = isAuditDetailObject(facts.ip_intelligence)
    ? facts.ip_intelligence
    : undefined
  return (
    <div className='flex flex-col gap-3 rounded-lg border p-3 text-sm'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <p className='font-medium' title={props.event.action}>
          {headline}
        </p>
        <Badge
          variant={
            securityEventResultKey(props.event.action, props.event.success) ===
            'Failed'
              ? 'destructive'
              : 'secondary'
          }
        >
          {t(securityEventResultKey(props.event.action, props.event.success))}
        </Badge>
      </div>
      <div className='grid gap-x-6 gap-y-2 md:grid-cols-2'>
        <DetailRow
          label={t('Operator')}
          value={securityActorLabel(props.event, t('Unidentified account'))}
        />
        <DetailRow
          label={t('Event time')}
          value={securityTime(props.event.at)}
        />
        <DetailRow
          label='IP'
          value={
            <AuditIPCell
              entry={{
                event_id: props.event.event_id,
                ip: props.event.ip,
                ip_marker: props.ipMarker,
              }}
              onSelectAlert={props.onSelectAlert}
            />
          }
        />
        {props.event.actor_role !== undefined && (
          <DetailRow
            label={t('Role')}
            value={String(
              auditParameterValue(
                'actor_role',
                props.event.actor_role,
                props.event.action,
                t
              )
            )}
          />
        )}
        {!!props.event.resource_type && (
          <DetailRow
            label={t('Resource')}
            value={t(
              (
                {
                  channel: 'Channels',
                  user: 'Users',
                  account: 'Account',
                } as Record<string, string>
              )[props.event.resource_type] ?? props.event.resource_type
            )}
          />
        )}
        {props.event.target_id > 0 && (
          <DetailRow label={t('Target ID')} value={props.event.target_id} />
        )}
        {!!props.event.target_role && props.event.target_id > 0 && (
          <DetailRow
            label={t('Target account')}
            value={securityTargetLabel(props.event)}
          />
        )}
        {props.event.affected_count !== 1 && (
          <DetailRow
            label={t('Affected count')}
            value={props.event.affected_count}
          />
        )}
      </div>
      {props.event.reason && !Object.hasOwn(facts, 'reason') && (
        <DetailRow
          label={t('Failure reason')}
          value={String(
            auditParameterValue(
              'reason',
              props.event.reason,
              props.event.action,
              t
            )
          )}
        />
      )}
      <AuditDetailFields fields={primaryFields} />
      {intelligence && <SecurityIPSummary snapshot={intelligence} />}
      {!intelligence && props.event.ip && (
        <DetailRow label={t('IP intelligence')} value={t('Not recorded')} />
      )}
      {unavailable && (
        <AuditDetailValue label={t('Details')} value={props.event.facts} />
      )}
      {(props.event.request_id || technicalFields.length > 0) && (
        <Collapsible>
          <CollapsibleTrigger
            render={
              <Button
                type='button'
                variant='ghost'
                size='sm'
                className='group'
              />
            }
          >
            {t('Technical details')}
            <ChevronDown
              aria-hidden
              className='size-3.5 group-data-[panel-open]:rotate-180'
            />
          </CollapsibleTrigger>
          <CollapsibleContent className='space-y-2 pt-2'>
            {!!props.event.request_id && (
              <DetailRow
                label={t('Request ID')}
                value={
                  <span className='inline-flex items-start gap-1'>
                    <span className='wrap-anywhere'>
                      {props.event.request_id}
                    </span>
                    <CopyButton
                      value={props.event.request_id}
                      className='size-5 shrink-0'
                    />
                  </span>
                }
              />
            )}
            <AuditDetailFields fields={technicalFields} />
          </CollapsibleContent>
        </Collapsible>
      )}
      {props.onInvestigate && (props.event.request_id || props.event.ip) && (
        <div className='flex justify-end'>
          <Button
            variant='link'
            size='sm'
            onClick={() => props.onInvestigate?.(props.event)}
          >
            {t('Related audit records')}
          </Button>
        </div>
      )}
    </div>
  )
}

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
import type { TFunction } from 'i18next'
import { ChevronDown } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import type { PermissionResourceDef } from '@/lib/admin-permissions'

import { DetailSection } from '../../components/dialogs/log-detail-layout'
import type { AuditLog } from '../api'
import {
  readAuditChanges,
  type AuditChangeValue,
  type AuditFieldChange,
  type AuditTargetChange,
} from '../lib/audit-changes'
import { describeAccessTokenScopes } from '../lib/audit-details'
import { auditFieldLabel } from '../lib/audit-field-labels'
import { auditParameterValue } from '../lib/audit-parameter-value'
import { AuditDetailValue } from './audit-detail-value'

function changeText(
  value: AuditChangeValue,
  field: string,
  action: string,
  t: TFunction,
  scopes?: PermissionResourceDef[]
): string {
  if (value.state === 'redacted') return t('Sensitive value hidden')
  if (value.state === 'absent') return t('Not set')
  if (value.state === 'unavailable') return t('Value not recorded')
  if (value.state === 'truncated') return t('Value exceeds the recording limit')
  if (value.value === null) return t('Null value')
  if (value.value === '') return t('Empty')
  if (typeof value.value === 'boolean') return value.value ? t('Yes') : t('No')
  if (
    field === 'scopes' &&
    Array.isArray(value.value) &&
    value.value.every((scope) => typeof scope === 'string')
  ) {
    const labels = describeAccessTokenScopes(value.value, scopes, t)
    return typeof labels === 'string' ? labels : JSON.stringify(labels, null, 2)
  }
  const formatted = auditParameterValue(field, value.value, action, t)
  if (typeof formatted === 'object') return JSON.stringify(formatted, null, 2)
  return String(formatted)
}

function ChangeRows({
  fields,
  created,
  action,
  scopeResources,
}: {
  fields: AuditFieldChange[]
  created: boolean
  action: string
  scopeResources?: PermissionResourceDef[]
}) {
  const { t } = useTranslation()
  return fields.map((field) => {
    const label = field.path.map((part) => auditFieldLabel(part, t)).join(' / ')
    return (
      <div
        key={JSON.stringify(field.path)}
        className={`grid min-w-0 gap-2 border-t py-3 text-xs ${created ? 'sm:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]' : 'sm:grid-cols-[minmax(0,1fr)_minmax(0,1.5fr)_minmax(0,1.5fr)]'}`}
      >
        <p className='text-muted-foreground min-w-0 font-medium wrap-anywhere'>
          {label}
        </p>
        {(created ? (['new'] as const) : (['old', 'new'] as const)).map(
          (side) => (
            <div key={side} className='min-w-0 space-y-1'>
              {!created && (
                <p className='text-muted-foreground sm:hidden'>
                  {side === 'old' ? t('Before') : t('After')}
                </p>
              )}
              <AuditDetailValue
                label={label}
                value={changeText(
                  field[side],
                  field.path.at(-1) ?? '',
                  action,
                  t,
                  scopeResources
                )}
                copyable={field[side].state === 'value'}
              />
            </div>
          )
        )}
      </div>
    )
  })
}

function TargetChanges({
  target,
  action,
  scopeResources,
}: {
  target: AuditTargetChange
  action: string
  scopeResources?: PermissionResourceDef[]
}) {
  const { t } = useTranslation()
  const created =
    /\.(create|generate)$/.test(action) &&
    target.fields.every(
      (field) => field.old.state === 'absent' || field.old.state === 'redacted'
    )
  const secondary = created
    ? target.fields.filter(
        (field) =>
          field.new.state === 'value' &&
          (field.new.value === '' ||
            field.new.value === 0 ||
            field.new.value === false ||
            field.new.value === null)
      )
    : []
  const primary = target.fields.filter((field) => !secondary.includes(field))
  return (
    <div className='min-w-0 space-y-2'>
      <p className='text-xs font-medium wrap-anywhere'>
        {target.name || auditFieldLabel(target.resource, t)}
        {target.id && ` (ID: ${target.id})`}
      </p>
      {!created && target.consistency === 'observed' && (
        <p className='text-muted-foreground text-xs'>
          {t('Earlier values may be affected by concurrent edits.')}
        </p>
      )}
      <div
        className={`text-muted-foreground hidden gap-2 text-xs font-medium sm:grid ${created ? 'sm:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]' : 'sm:grid-cols-[minmax(0,1fr)_minmax(0,1.5fr)_minmax(0,1.5fr)]'}`}
      >
        <span>{t('Field')}</span>
        {!created && <span>{t('Before')}</span>}
        <span>{created ? t('Content') : t('After')}</span>
      </div>
      <ChangeRows
        fields={primary}
        created={created}
        action={action}
        scopeResources={scopeResources}
      />
      {secondary.length > 0 && (
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
            {t('Other fields ({{count}})', { count: secondary.length })}
            <ChevronDown
              aria-hidden
              className='size-3.5 group-data-[panel-open]:rotate-180'
            />
          </CollapsibleTrigger>
          <CollapsibleContent>
            <ChangeRows
              fields={secondary}
              created={created}
              action={action}
              scopeResources={scopeResources}
            />
          </CollapsibleContent>
        </Collapsible>
      )}
    </div>
  )
}

export function AuditChangesSection({
  entry,
  scopeResources,
}: {
  entry: Pick<AuditLog, 'action' | 'success' | 'other'>
  scopeResources?: PermissionResourceDef[]
}) {
  const { t } = useTranslation()
  const changes = readAuditChanges(entry)
  if (!changes) return null
  return (
    <DetailSection label={t('Changes')}>
      {changes.outcome === 'partial' && (
        <p className='text-muted-foreground text-xs'>
          {t('Some changes were saved before the operation failed.')}
        </p>
      )}
      {changes.outcome === 'unchanged' && (
        <p className='text-muted-foreground text-xs'>{t('No changes')}</p>
      )}
      {changes.outcome === 'unavailable' && (
        <p className='text-muted-foreground text-xs'>
          {t('Field change details were not recorded')}
        </p>
      )}
      {changes.outcome === 'not_committed' && (
        <p className='text-muted-foreground text-xs'>
          {t('No changes were saved')}
        </p>
      )}
      {changes.targets.map((target) => (
        <TargetChanges
          key={JSON.stringify([target.resource, target.id, target.fields])}
          target={target}
          action={entry.action}
          scopeResources={scopeResources}
        />
      ))}
      {changes.truncated && (
        <p className='text-muted-foreground text-xs'>
          {t(
            'Only part of the change details was recorded because the limit was reached.'
          )}
        </p>
      )}
    </DetailSection>
  )
}

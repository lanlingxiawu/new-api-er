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
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldLabel,
} from '@/components/ui/field'
import { Switch } from '@/components/ui/switch'
import type {
  AdminPermissionMatrix,
  PermissionResourceDef,
} from '@/lib/admin-permissions'
import { cn } from '@/lib/utils'

export type PermissionMatrixGroup = {
  key: string
  labelKey: string
  resources: PermissionResourceDef[]
}

type PermissionMatrixProps = {
  value: AdminPermissionMatrix
  onChange: (value: AdminPermissionMatrix) => void
  disabled?: boolean
} & (
  | { resources: PermissionResourceDef[] }
  // Groups render as collapsible sections with a selection count and a
  // select-all action.
  | { groups: PermissionMatrixGroup[] }
)

type PermissionResourceRowProps = {
  resource: PermissionResourceDef
  value: AdminPermissionMatrix
  onChange: (value: AdminPermissionMatrix) => void
  disabled?: boolean
  className: string
}

// PermissionMatrix keeps permission selection and descriptions consistent with administrator editing.
export function PermissionMatrix(props: PermissionMatrixProps) {
  const { t } = useTranslation()
  if (!('groups' in props)) {
    return (
      <div className='divide-y rounded-md border'>
        {props.resources.map((resource) => (
          <PermissionResourceRow
            key={resource.resource}
            resource={resource}
            value={props.value}
            onChange={props.onChange}
            disabled={props.disabled}
            className='px-3'
          />
        ))}
      </div>
    )
  }
  return (
    <div className='divide-y rounded-md border'>
      {props.groups.map((group) => {
        const label = t(group.labelKey)
        const cells = group.resources.flatMap((resource) =>
          resource.actions.map(
            (option) => props.value[resource.resource]?.[option.action] === true
          )
        )
        const selected = cells.filter(Boolean).length
        const allSelected = selected === cells.length
        return (
          <section key={group.key} aria-label={label}>
            <Collapsible defaultOpen>
              <div className='flex items-center gap-2 px-3 py-2'>
                <CollapsibleTrigger className='group/trigger flex flex-1 items-center gap-2 text-left text-sm font-medium'>
                  <ChevronDown
                    className='text-muted-foreground size-4 -rotate-90 transition-transform group-data-[panel-open]/trigger:rotate-0'
                    aria-hidden='true'
                  />
                  {label}
                  <Badge variant='secondary' className='tabular-nums'>
                    {t('Selected {{selected}} / {{total}}', {
                      selected,
                      total: cells.length,
                    })}
                  </Badge>
                </CollapsibleTrigger>
                <Button
                  type='button'
                  variant='ghost'
                  size='sm'
                  className='h-7 text-xs'
                  disabled={props.disabled}
                  onClick={() => {
                    const next = { ...props.value }
                    for (const resource of group.resources) {
                      const actions = { ...next[resource.resource] }
                      for (const option of resource.actions) {
                        actions[option.action] = !allSelected
                      }
                      next[resource.resource] = actions
                    }
                    props.onChange(next)
                  }}
                >
                  {allSelected ? t('Clear') : t('Select all')}
                </Button>
              </div>
              <CollapsibleContent>
                <div className='divide-y border-t'>
                  {group.resources.map((resource) => (
                    <PermissionResourceRow
                      key={resource.resource}
                      resource={resource}
                      value={props.value}
                      onChange={props.onChange}
                      disabled={props.disabled}
                      className='pr-3 pl-9'
                    />
                  ))}
                </div>
              </CollapsibleContent>
            </Collapsible>
          </section>
        )
      })}
    </div>
  )
}

export function PermissionOption(props: {
  label: string
  description: string
  checked: boolean
  onCheckedChange: (checked: boolean) => void
  disabled?: boolean
  control?: 'checkbox' | 'switch'
}) {
  const id = useId()
  const Control = props.control === 'switch' ? Switch : Checkbox
  return (
    <Field
      orientation='horizontal'
      data-disabled={props.disabled}
      className='min-w-0 gap-3'
    >
      <Control
        id={id}
        checked={props.checked}
        disabled={props.disabled}
        onCheckedChange={props.onCheckedChange}
        aria-describedby={`${id}-description`}
      />
      <FieldContent className='min-w-0'>
        <FieldLabel htmlFor={id}>{props.label}</FieldLabel>
        <FieldDescription
          id={`${id}-description`}
          className='text-xs leading-relaxed'
        >
          {props.description}
        </FieldDescription>
      </FieldContent>
    </Field>
  )
}

function PermissionResourceRow(props: PermissionResourceRowProps) {
  const { t } = useTranslation()
  const resource = props.resource
  return (
    <div
      role='group'
      aria-label={t(resource.label_key)}
      className={cn(
        'grid min-w-0 gap-3 py-4 sm:grid-cols-[8rem_minmax(0,1fr)]',
        props.className
      )}
    >
      <span className='text-sm font-medium'>{t(resource.label_key)}</span>
      <div className='grid min-w-0 gap-4'>
        {resource.actions.map((option) => (
          <PermissionOption
            key={option.action}
            label={t(option.label_key)}
            description={t(option.description_key)}
            checked={props.value[resource.resource]?.[option.action] === true}
            disabled={props.disabled}
            onCheckedChange={(checked) =>
              props.onChange({
                ...props.value,
                [resource.resource]: {
                  ...props.value[resource.resource],
                  [option.action]: checked,
                },
              })
            }
          />
        ))}
      </div>
    </div>
  )
}

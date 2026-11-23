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
import { useTranslation } from 'react-i18next'

import { PermissionOption } from '@/components/permission-matrix'
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import { FieldGroup, FieldLegend, FieldSet } from '@/components/ui/field'
import {
  ADMIN_PERMISSION_ACTIONS,
  ADMIN_PERMISSION_RESOURCES,
  normalizeAdminPermissions,
  viewDependentActions,
  type AdminPermissionMatrix,
  type PermissionActionDef,
  type PermissionCatalog,
  type PermissionResourceDef,
} from '@/lib/admin-permissions'

type AdminPermissionsEditorProps = {
  catalog: PermissionCatalog
  value: AdminPermissionMatrix | undefined
  onChange: (value: AdminPermissionMatrix) => void
}

const TOKEN_READ_ACTIONS = new Set([
  'users_list',
  'groups_read',
  'employees_read',
  'users_stats',
])

type PermissionGroup = {
  key: string
  labelKey?: string
  resources: PermissionResourceDef[]
}

function groupResources(resources: PermissionResourceDef[]): PermissionGroup[] {
  const groups = new Map<string, PermissionGroup>()
  for (const resource of resources) {
    const key = resource.group ?? resource.resource
    const group = groups.get(key) ?? {
      key,
      labelKey: resource.group_label_key,
      resources: [],
    }
    group.resources.push(resource)
    groups.set(key, group)
  }
  return [...groups.values()].map((group) => ({
    ...group,
    resources: [...group.resources].sort(
      (left, right) => (left.sort ?? 0) - (right.sort ?? 0)
    ),
  }))
}

export function AdminPermissionsEditor(props: AdminPermissionsEditorProps) {
  const { t } = useTranslation()
  const selected = normalizeAdminPermissions(props.value, props.catalog)
  const menuResources = props.catalog.resources.filter((resource) =>
    resource.resource.startsWith(ADMIN_PERMISSION_RESOURCES.ADMIN_MENU_PREFIX)
  )
  const tokenResources = props.catalog.resources.filter(
    (resource) =>
      resource.resource === ADMIN_PERMISSION_RESOURCES.ADMIN_ACCESS_TOKEN
  )
  const channelResources = props.catalog.resources.filter(
    (resource) =>
      resource.resource === ADMIN_PERMISSION_RESOURCES.CHANNEL ||
      resource.resource === ADMIN_PERMISSION_RESOURCES.TASK_PLUGIN
  )
  const settingsResources = props.catalog.resources.filter((resource) =>
    resource.resource.startsWith(
      ADMIN_PERMISSION_RESOURCES.SYSTEM_SETTINGS_PREFIX
    )
  )
  const otherResources = props.catalog.resources.filter(
    (resource) =>
      !menuResources.includes(resource) &&
      !tokenResources.includes(resource) &&
      !channelResources.includes(resource) &&
      !settingsResources.includes(resource)
  )
  const sections = [
    { id: 'menu', label: t('Menu access'), resources: menuResources },
    {
      id: 'token',
      label: t('Administrator token access'),
      resources: tokenResources,
    },
    {
      id: 'channel',
      label: t('Channel permissions'),
      resources: channelResources,
    },
    {
      id: 'settings',
      label: t('System settings'),
      resources: settingsResources,
    },
    { id: 'other', label: t('Other permissions'), resources: otherResources },
  ].filter((section) => section.resources.length > 0)

  const updatePermission = (
    resource: string,
    action: string,
    checked: boolean
  ) => {
    const next = {
      ...selected,
      [resource]: { ...selected[resource], [action]: checked },
    }
    const dependents = viewDependentActions(resource)
    if (dependents.includes(action) && checked) {
      next[resource][ADMIN_PERMISSION_ACTIONS.VIEW] = true
    }
    if (action === ADMIN_PERMISSION_ACTIONS.VIEW && !checked) {
      for (const dependent of dependents) next[resource][dependent] = false
    }
    props.onChange(next)
  }

  const renderActions = (
    resource: PermissionResourceDef,
    actions: PermissionActionDef[] = resource.actions
  ) => (
    <FieldGroup className='gap-5'>
      {actions.map((option) => (
        <PermissionOption
          key={option.action}
          control={
            resource.resource ===
              ADMIN_PERMISSION_RESOURCES.ADMIN_ACCESS_TOKEN &&
            option.action === 'use'
              ? 'switch'
              : 'checkbox'
          }
          label={t(option.label_key)}
          description={t(option.description_key)}
          checked={selected[resource.resource]?.[option.action] === true}
          onCheckedChange={(checked) =>
            updatePermission(resource.resource, option.action, checked)
          }
        />
      ))}
    </FieldGroup>
  )

  const renderResources = (resources: PermissionResourceDef[]) => (
    <FieldGroup className='gap-0'>
      {resources.map((resource) => (
        <FieldSet
          key={resource.resource}
          className='border-border/60 gap-4 border-b py-5 first:pt-0 last:border-b-0 last:pb-0'
        >
          <FieldLegend variant='label'>{t(resource.label_key)}</FieldLegend>
          {renderActions(resource)}
        </FieldSet>
      ))}
    </FieldGroup>
  )

  return (
    <Accordion
      multiple
      defaultValue={['token']}
      className='border-border/60 rounded-lg border px-4'
    >
      {sections.map((section) => {
        const actions = section.resources.flatMap((resource) =>
          resource.actions.map(
            (action) => selected[resource.resource]?.[action.action] === true
          )
        )
        // Offset the scroll padding from sideDrawerFormClassName so content cannot peek above the header.
        return (
          <AccordionItem
            key={section.id}
            value={section.id}
            className='data-open:[&>h3]:bg-background data-open:[&>h3]:sticky data-open:[&>h3]:-top-4 data-open:[&>h3]:z-10 sm:data-open:[&>h3]:-top-5'
          >
            <AccordionTrigger className='items-center gap-3 py-4'>
              <span className='flex min-w-0 flex-1 flex-wrap items-center justify-between gap-x-3 gap-y-1'>
                <span>{section.label}</span>
                <span
                  className='text-muted-foreground text-xs font-normal'
                  aria-hidden='true'
                >
                  {t('{{enabled}} of {{total}} enabled', {
                    enabled: actions.filter(Boolean).length,
                    total: actions.length,
                  })}
                </span>
              </span>
            </AccordionTrigger>
            <AccordionContent className='pb-5'>
              {section.id === 'token' &&
                section.resources.map((resource) => (
                  <div key={resource.resource} className='flex flex-col gap-6'>
                    <div className='bg-muted/40 rounded-lg p-4'>
                      {renderActions(
                        resource,
                        resource.actions.filter(
                          (action) => action.action === 'use'
                        )
                      )}
                    </div>
                    <p className='text-muted-foreground text-sm'>
                      {t(
                        "Token access is limited to the selected modules and the administrator's menu permissions."
                      )}
                    </p>
                    {[
                      {
                        label: t('Read-only access'),
                        actions: resource.actions.filter((action) =>
                          TOKEN_READ_ACTIONS.has(action.action)
                        ),
                      },
                      {
                        label: t('Changes and operations'),
                        actions: resource.actions.filter(
                          (action) =>
                            action.action !== 'use' &&
                            !TOKEN_READ_ACTIONS.has(action.action)
                        ),
                      },
                    ]
                      .filter((group) => group.actions.length > 0)
                      .map((group) => (
                        <FieldSet
                          key={group.label}
                          className='border-border/60 gap-4 border-t pt-5'
                        >
                          <FieldLegend variant='label'>
                            {group.label}
                          </FieldLegend>
                          {renderActions(resource, group.actions)}
                        </FieldSet>
                      ))}
                    <p className='text-muted-foreground text-xs'>
                      {t(
                        'Token permissions apply immediately. Re-enabling restores valid tokens within their existing scopes. Suspected leaks require revoking the token. Saving administrator permissions may require the administrator to sign in again.'
                      )}
                    </p>
                  </div>
                ))}
              {section.id === 'settings' && (
                <FieldGroup>
                  {groupResources(section.resources).map((group) => (
                    <FieldSet
                      key={group.key}
                      className='border-border/60 gap-4 border-b pb-5 last:border-0 last:pb-0'
                    >
                      <FieldLegend variant='label'>
                        {t(group.labelKey ?? group.key)}
                      </FieldLegend>
                      {renderResources(group.resources)}
                    </FieldSet>
                  ))}
                </FieldGroup>
              )}
              {section.id !== 'token' &&
                section.id !== 'settings' &&
                renderResources(section.resources)}
            </AccordionContent>
          </AccordionItem>
        )
      })}
    </Accordion>
  )
}

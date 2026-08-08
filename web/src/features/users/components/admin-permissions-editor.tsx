import {
  CheckCircle2,
  Circle,
  Menu,
  RadioTower,
  Settings,
  ShieldCheck,
} from 'lucide-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  SideDrawerSection,
  SideDrawerSectionHeader,
} from '@/components/drawer-layout'
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
import {
  ADMIN_PERMISSION_ACTIONS,
  ADMIN_PERMISSION_RESOURCES,
  normalizeAdminPermissions,
  type AdminPermissionMatrix,
  type PermissionCatalog,
  type PermissionResourceDef,
} from '@/lib/admin-permissions'
import { cn } from '@/lib/utils'

type AdminPermissionsEditorProps = {
  catalog: PermissionCatalog
  value: AdminPermissionMatrix | undefined
  onChange: (value: AdminPermissionMatrix) => void
  administratorLabel: string
  embedded?: boolean
}

type PermissionGroup = {
  key: string
  labelKey?: string
  resources: PermissionResourceDef[]
}

const ADMIN_PERMISSION_SECTION_IDS = [
  'admin-permissions-menu',
  'admin-permissions-channel',
  'admin-permissions-settings',
] as const

const SECTION_IDS = ADMIN_PERMISSION_SECTION_IDS

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
  const [activeSection, setActiveSection] = useState<string>(SECTION_IDS[0])
  const [openSettingsGroups, setOpenSettingsGroups] = useState<string[]>([])
  const menuResources = props.catalog.resources.filter((resource) =>
    resource.resource.startsWith(ADMIN_PERMISSION_RESOURCES.ADMIN_MENU_PREFIX)
  )
  const channelResources = props.catalog.resources.filter(
    (resource) => resource.resource === ADMIN_PERMISSION_RESOURCES.CHANNEL
  )
  const settingsGroups = groupResources(
    props.catalog.resources.filter((resource) =>
      resource.resource.startsWith(
        ADMIN_PERMISSION_RESOURCES.SYSTEM_SETTINGS_PREFIX
      )
    )
  )
  const enabledMenuCount = menuResources.filter(
    (resource) =>
      selected[resource.resource]?.[ADMIN_PERMISSION_ACTIONS.VIEW] === true
  ).length
  const sections = useMemo(
    () => [
      {
        id: SECTION_IDS[0],
        label: t('Menu access'),
        description: t('{{enabled}} of {{total}} enabled', {
          enabled: enabledMenuCount,
          total: menuResources.length,
        }),
        icon: <Menu className='size-4' aria-hidden='true' />,
      },
      {
        id: SECTION_IDS[1],
        label: t('Channel permissions'),
        description: t('Channel operations'),
        icon: <RadioTower className='size-4' aria-hidden='true' />,
      },
      {
        id: SECTION_IDS[2],
        label: t('System settings'),
        description: t('{{count}} permission groups', {
          count: settingsGroups.length,
        }),
        icon: <Settings className='size-4' aria-hidden='true' />,
      },
    ],
    [enabledMenuCount, menuResources.length, settingsGroups.length, t]
  )

  const updatePermission = (
    resource: string,
    action: string,
    checked: boolean
  ) => {
    const next = {
      ...selected,
      [resource]: { ...selected[resource], [action]: checked },
    }
    if (
      resource.startsWith(ADMIN_PERMISSION_RESOURCES.SYSTEM_SETTINGS_PREFIX)
    ) {
      if (action === ADMIN_PERMISSION_ACTIONS.EDIT && checked) {
        next[resource][ADMIN_PERMISSION_ACTIONS.VIEW] = true
      }
      if (action === ADMIN_PERMISSION_ACTIONS.VIEW && !checked) {
        next[resource][ADMIN_PERMISSION_ACTIONS.EDIT] = false
      }
    }
    props.onChange(next)
  }

  const scrollTo = useCallback((id: string) => {
    setActiveSection(id)
    document
      .querySelector<HTMLElement>(`#${id}`)
      ?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }, [])

  const updateActiveSection = useCallback(() => {
    const form = document.querySelector<HTMLElement>('#user-form')
    if (!form) return
    const activationY = form.getBoundingClientRect().top + 80
    let nextActive: string = SECTION_IDS[0]
    for (const sectionId of SECTION_IDS) {
      const section = document.querySelector<HTMLElement>(`#${sectionId}`)
      if (!section) continue
      if (section.getBoundingClientRect().top <= activationY) {
        nextActive = sectionId
      } else {
        break
      }
    }
    setActiveSection((current) =>
      current === nextActive ? current : nextActive
    )
  }, [])

  useEffect(() => {
    if (props.embedded) return
    const form = document.querySelector<HTMLElement>('#user-form')
    if (!form) return
    updateActiveSection()
    form.addEventListener('scroll', updateActiveSection, { passive: true })
    window.addEventListener('resize', updateActiveSection)
    return () => {
      form.removeEventListener('scroll', updateActiveSection)
      window.removeEventListener('resize', updateActiveSection)
    }
  }, [props.embedded, updateActiveSection])

  const renderResources = (resources: PermissionResourceDef[]) => (
    <FieldGroup className='gap-0'>
      {resources.map((resource) => (
        <FieldSet
          key={resource.resource}
          className='border-border/60 gap-3 border-b py-4 first:pt-0 last:border-b-0 last:pb-0'
        >
          <FieldLegend variant='label'>{t(resource.label_key)}</FieldLegend>
          <FieldGroup className='gap-3 sm:grid sm:grid-cols-2'>
            {resource.actions.map((option) => {
              const id = `${resource.resource}-${option.action}`
              return (
                <Field key={option.action} orientation='horizontal'>
                  <Checkbox
                    id={id}
                    checked={
                      selected[resource.resource]?.[option.action] === true
                    }
                    onCheckedChange={(checked) =>
                      updatePermission(
                        resource.resource,
                        option.action,
                        checked === true
                      )
                    }
                  />
                  <FieldContent>
                    <FieldLabel htmlFor={id} className='font-medium'>
                      {t(option.label_key)}
                    </FieldLabel>
                    <FieldDescription>
                      {t(option.description_key)}
                    </FieldDescription>
                  </FieldContent>
                </Field>
              )
            })}
          </FieldGroup>
        </FieldSet>
      ))}
    </FieldGroup>
  )

  return (
    <div
      className={cn(
        'gap-5',
        props.embedded
          ? 'flex min-w-0 flex-col'
          : 'grid lg:grid-cols-[13rem_minmax(0,1fr)] lg:items-start'
      )}
    >
      {!props.embedded && (
        <aside className='hidden self-start lg:sticky lg:top-4 lg:z-20 lg:block'>
          <div className='flex max-h-[calc(100dvh-12rem)] flex-col gap-3 overflow-y-auto overscroll-contain pr-1'>
            <div className='border-border/60 bg-muted/20 rounded-lg border p-3'>
              <div className='flex min-w-0 items-center gap-2'>
                <span className='bg-background flex size-8 shrink-0 items-center justify-center rounded-md border'>
                  <ShieldCheck className='size-4' aria-hidden='true' />
                </span>
                <div className='min-w-0'>
                  <p className='truncate text-sm font-medium'>
                    {props.administratorLabel}
                  </p>
                  <p className='text-muted-foreground truncate text-xs'>
                    {t('Administrator permissions')}
                  </p>
                </div>
              </div>
            </div>

            <nav
              className='border-border/60 bg-background rounded-lg border p-1'
              aria-label={t('Administrator permissions')}
            >
              {sections.map((section) => {
                const isActive = activeSection === section.id
                return (
                  <button
                    key={section.id}
                    type='button'
                    className={cn(
                      'hover:bg-muted/60 flex w-full items-start gap-2 rounded-md px-2 py-2 text-left transition-colors',
                      isActive && 'bg-muted/70'
                    )}
                    onClick={() => scrollTo(section.id)}
                    aria-current={isActive ? 'true' : undefined}
                  >
                    <span className='bg-muted text-muted-foreground mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md'>
                      {section.icon}
                    </span>
                    <span className='min-w-0 flex-1'>
                      <span className='block truncate text-sm font-medium'>
                        {section.label}
                      </span>
                      <span className='text-muted-foreground block truncate text-xs'>
                        {section.description}
                      </span>
                    </span>
                    {isActive ? (
                      <CheckCircle2
                        className='text-primary mt-1 size-3.5 shrink-0'
                        aria-hidden='true'
                      />
                    ) : (
                      <Circle
                        className='text-muted-foreground mt-1 size-3.5 shrink-0'
                        aria-hidden='true'
                      />
                    )}
                  </button>
                )
              })}
            </nav>
          </div>
        </aside>
      )}

      <div className='flex min-w-0 flex-col gap-5'>
        <div id={SECTION_IDS[0]} className='scroll-mt-4'>
          <SideDrawerSection>
            <SideDrawerSectionHeader
              title={t('Menu access')}
              description={t(
                'Choose which management menus this administrator can access.'
              )}
              icon={<Menu className='size-4' aria-hidden='true' />}
              iconTone='info'
            />
            {renderResources(menuResources)}
          </SideDrawerSection>
        </div>

        <div id={SECTION_IDS[1]} className='scroll-mt-4'>
          <SideDrawerSection>
            <SideDrawerSectionHeader
              title={t('Channel permissions')}
              description={t(
                'Control the operations available inside channel management.'
              )}
              icon={<RadioTower className='size-4' aria-hidden='true' />}
              iconTone='chart-4'
            />
            {renderResources(channelResources)}
          </SideDrawerSection>
        </div>

        <div id={SECTION_IDS[2]} className='scroll-mt-4'>
          <SideDrawerSection>
            <SideDrawerSectionHeader
              title={t('System settings')}
              description={t(
                'Detailed settings permissions are collapsed by category.'
              )}
              icon={<Settings className='size-4' aria-hidden='true' />}
              iconTone='warning'
            />
            <Accordion
              multiple
              value={openSettingsGroups}
              onValueChange={setOpenSettingsGroups}
              className='border-border/60 rounded-lg border px-3'
            >
              {settingsGroups.map((group) => (
                <AccordionItem key={group.key} value={group.key}>
                  <AccordionTrigger>
                    {t(group.labelKey ?? group.key)}
                  </AccordionTrigger>
                  <AccordionContent>
                    {renderResources(group.resources)}
                  </AccordionContent>
                </AccordionItem>
              ))}
            </Accordion>
          </SideDrawerSection>
        </div>
      </div>
    </div>
  )
}

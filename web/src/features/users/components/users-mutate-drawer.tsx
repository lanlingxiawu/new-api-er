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
import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery } from '@tanstack/react-query'
import {
  CheckCircle2,
  Circle,
  CreditCard,
  Link2,
  Pencil,
  Plus,
  ShieldCheck,
  Timer,
  Trash2,
  UserRound,
  UsersRound,
} from 'lucide-react'
import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ComponentProps,
  type ReactNode,
} from 'react'
import { useForm, useFieldArray, type Control } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  SideDrawerSection,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Textarea } from '@/components/ui/textarea'
import {
  ADMIN_PERMISSION_ACTIONS,
  ADMIN_PERMISSION_RESOURCES,
  EMPTY_PERMISSION_CATALOG,
  hasPermission,
} from '@/lib/admin-permissions'
import { getCurrencyDisplay, getCurrencyLabel } from '@/lib/currency'
import { formatQuota, parseQuotaFromDollars } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import {
  createUser,
  updateUser,
  getUser,
  getGroups,
  getPermissionCatalog,
} from '../api'
import { BINDING_FIELDS, ERROR_MESSAGES, SUCCESS_MESSAGES } from '../constants'
import {
  userFormSchema,
  type UserFormValues,
  USER_FORM_DEFAULT_VALUES,
  transformFormDataToPayload,
  transformUserToFormDefaults,
} from '../lib'
import type { User } from '../types'
import { AdminPermissionsEditor } from './admin-permissions-editor'
import { EmployeeAssignField } from './employee-assign-field'
import { GroupCombobox } from './group-combobox'
import { UserQuotaDialog } from './user-quota-dialog'
import { useUsers } from './users-provider'

type UsersMutateDrawerProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentRow?: User
}

type TimeoutOverrideFieldName =
  | 'stream_response_timeout'
  | 'stream_total_timeout'
  | 'non_stream_response_timeout'
  | 'non_stream_total_timeout'

type TimeoutOverrideFieldProps = {
  control: Control<UserFormValues>
  name: TimeoutOverrideFieldName
  label: ReactNode
  description: ReactNode
}

type TimeoutNumberInputProps = Omit<
  ComponentProps<typeof Input>,
  'value' | 'onChange'
> & {
  value: number | undefined
  onValueChange: (value: number) => void
}

function TimeoutNumberInput({
  value,
  onValueChange,
  onBlur,
  ...props
}: TimeoutNumberInputProps) {
  const [draft, setDraft] = useState(String(value ?? 0))

  useEffect(() => setDraft(String(value ?? 0)), [value])

  return (
    <Input
      {...props}
      type='number'
      value={draft}
      onChange={(event) => {
        const next = event.target.value
        setDraft(next)
        if (/^-?\d+$/.test(next)) {
          onValueChange(Number(next))
        }
      }}
      onBlur={(event) => {
        if (!/^-?\d+$/.test(draft)) {
          setDraft('0')
          onValueChange(0)
        }
        onBlur?.(event)
      }}
    />
  )
}

function TimeoutOverrideField({
  control,
  name,
  label,
  description,
}: TimeoutOverrideFieldProps) {
  return (
    <FormField
      control={control}
      name={name}
      render={({ field }) => (
        <FormItem>
          <FormLabel>{label}</FormLabel>
          <FormControl>
            <TimeoutNumberInput
              min={-1}
              max={604800}
              step={1}
              name={field.name}
              ref={field.ref}
              value={field.value}
              onBlur={field.onBlur}
              onValueChange={field.onChange}
            />
          </FormControl>
          <FormDescription>{description}</FormDescription>
          <FormMessage />
        </FormItem>
      )}
    />
  )
}

const USER_SECTION_IDS = {
  BASIC: 'user-basic-information',
  GROUP_QUOTA: 'user-group-quota',
  TIMEOUT: 'user-request-timeout',
  EMPLOYEE: 'user-assigned-employee',
  BINDINGS: 'user-binding-information',
} as const

const ADMIN_PERMISSION_SECTION_IDS = [
  'admin-permissions-menu',
  'admin-permissions-channel',
  'admin-permissions-settings',
] as const

export function UsersMutateDrawer({
  open,
  onOpenChange,
  currentRow,
}: UsersMutateDrawerProps) {
  const { t } = useTranslation()
  const isUpdate = !!currentRow
  const { triggerRefresh } = useUsers()
  const currentUser = useAuthStore((s) => s.auth.user)
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [quotaDialogOpen, setQuotaDialogOpen] = useState(false)
  const [activeSection, setActiveSection] = useState<string>(
    USER_SECTION_IDS.BASIC
  )

  // Fetch groups
  const { data: groupsData } = useQuery({
    queryKey: ['groups'],
    queryFn: getGroups,
    staleTime: 5 * 60 * 1000,
  })

  const groups = groupsData?.data || []

  // Permission catalog is owned by the backend; fetched once and reused.
  const { data: permissionCatalog = EMPTY_PERMISSION_CATALOG } = useQuery({
    queryKey: ['admin-permission-catalog'],
    queryFn: getPermissionCatalog,
    staleTime: 5 * 60 * 1000,
  })

  const form = useForm<UserFormValues>({
    resolver: zodResolver(userFormSchema),
    defaultValues: USER_FORM_DEFAULT_VALUES,
  })

  const {
    fields: groupRatioFields,
    append: appendGroupRatio,
    remove: removeGroupRatio,
  } = useFieldArray({ control: form.control, name: 'groupRatios' })

  // Load existing data when updating
  useEffect(() => {
    if (open && isUpdate && currentRow) {
      // For update, fetch fresh data
      getUser(currentRow.id)
        .then((result) => {
          if (result.success && result.data) {
            form.reset(transformUserToFormDefaults(result.data))
          }
        })
        .catch(() => toast.error(t(ERROR_MESSAGES.LOAD_FAILED)))
    } else if (open && !isUpdate) {
      // For create, reset to defaults
      form.reset(USER_FORM_DEFAULT_VALUES)
    }
  }, [open, isUpdate, currentRow, form, t])

  const { meta: currencyMeta } = getCurrencyDisplay()
  const currencyLabel = getCurrencyLabel()
  const tokensOnly = currencyMeta.kind === 'tokens'

  const currentQuotaRaw = form.watch('quota_dollars') || 0
  const selectedRole = form.watch('role')
  const canEditAdminPermissions = currentUser?.role === ROLE.SUPER_ADMIN
  const targetIsAdmin = (selectedRole ?? currentRow?.role ?? 0) >= ROLE.ADMIN
  const showAdminPermissions =
    canEditAdminPermissions &&
    targetIsAdmin &&
    permissionCatalog.resources.length > 0
  const showEmployeeAssignment = isUpdate && currentRow?.role === ROLE.USER

  const navigationSections = useMemo(
    () => [
      {
        id: USER_SECTION_IDS.BASIC,
        label: t('Basic Information'),
        icon: <UserRound className='size-4' aria-hidden='true' />,
      },
      ...(isUpdate
        ? [
            {
              id: USER_SECTION_IDS.GROUP_QUOTA,
              label: t('Group & Quota'),
              icon: <CreditCard className='size-4' aria-hidden='true' />,
            },
            {
              id: USER_SECTION_IDS.TIMEOUT,
              label: t('AI Request Timeout'),
              icon: <Timer className='size-4' aria-hidden='true' />,
            },
          ]
        : []),
      ...(showAdminPermissions
        ? [
            {
              id: ADMIN_PERMISSION_SECTION_IDS[0],
              label: t('Menu access'),
              icon: <ShieldCheck className='size-4' aria-hidden='true' />,
            },
            {
              id: ADMIN_PERMISSION_SECTION_IDS[1],
              label: t('Channel permissions'),
              icon: <ShieldCheck className='size-4' aria-hidden='true' />,
            },
            {
              id: ADMIN_PERMISSION_SECTION_IDS[2],
              label: t('System settings'),
              icon: <ShieldCheck className='size-4' aria-hidden='true' />,
            },
          ]
        : []),
      ...(showEmployeeAssignment
        ? [
            {
              id: USER_SECTION_IDS.EMPLOYEE,
              label: t('Assigned Employee'),
              icon: <UsersRound className='size-4' aria-hidden='true' />,
            },
          ]
        : []),
      ...(isUpdate
        ? [
            {
              id: USER_SECTION_IDS.BINDINGS,
              label: t('Binding Information'),
              icon: <Link2 className='size-4' aria-hidden='true' />,
            },
          ]
        : []),
    ],
    [isUpdate, showAdminPermissions, showEmployeeAssignment, t]
  )

  const scrollToSection = useCallback((id: string) => {
    setActiveSection(id)
    document
      .querySelector<HTMLElement>(`#${id}`)
      ?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }, [])

  useEffect(() => {
    if (!open) return
    const form = document.querySelector<HTMLElement>('#user-form')
    if (!form) return
    const updateActiveSection = () => {
      const activationY = form.getBoundingClientRect().top + 80
      let nextActive = navigationSections[0]?.id ?? USER_SECTION_IDS.BASIC
      for (const section of navigationSections) {
        const element = document.querySelector<HTMLElement>(`#${section.id}`)
        if (!element) continue
        if (element.getBoundingClientRect().top <= activationY) {
          nextActive = section.id
        } else {
          break
        }
      }
      setActiveSection((current) =>
        current === nextActive ? current : nextActive
      )
    }
    updateActiveSection()
    form.addEventListener('scroll', updateActiveSection, { passive: true })
    window.addEventListener('resize', updateActiveSection)
    return () => {
      form.removeEventListener('scroll', updateActiveSection)
      window.removeEventListener('resize', updateActiveSection)
    }
  }, [navigationSections, open])

  const onSubmit = async (data: UserFormValues) => {
    if (!isUpdate) {
      const passwordLength = data.password?.length || 0
      if (passwordLength < 8 || passwordLength > 20) {
        form.setError('password', {
          type: 'manual',
          message: t('Password must be between 8 and 20 characters'),
        })
        return
      }
    }

    setIsSubmitting(true)
    try {
      const payload = transformFormDataToPayload(
        data,
        currentRow?.id,
        permissionCatalog
      )
      const result = isUpdate
        ? await updateUser(payload as typeof payload & { id: number })
        : await createUser(payload)

      if (result.success) {
        toast.success(
          isUpdate
            ? t(SUCCESS_MESSAGES.USER_UPDATED)
            : t(SUCCESS_MESSAGES.USER_CREATED)
        )
        onOpenChange(false)
        triggerRefresh()
      } else {
        toast.error(
          result.message ||
            (isUpdate
              ? t(ERROR_MESSAGES.UPDATE_FAILED)
              : t(ERROR_MESSAGES.CREATE_FAILED))
        )
      }
    } catch {
      toast.error(t(ERROR_MESSAGES.UNEXPECTED))
    } finally {
      setIsSubmitting(false)
    }
  }

  const refreshUserData = async () => {
    if (!currentRow) return
    const result = await getUser(currentRow.id)
    if (result.success && result.data) {
      form.reset(transformUserToFormDefaults(result.data))
    }
    triggerRefresh()
  }

  return (
    <>
      <Sheet
        open={open}
        onOpenChange={(v) => {
          onOpenChange(v)
          if (!v) {
            form.reset()
          }
        }}
      >
        <SheetContent className={sideDrawerContentClassName('sm:max-w-5xl')}>
          <SheetHeader className={sideDrawerHeaderClassName()}>
            <SheetTitle>
              {isUpdate ? t('Update') : t('Create')} {t('User')}
            </SheetTitle>
            <SheetDescription>
              {isUpdate
                ? t('Update the user by providing necessary info.')
                : t('Add a new user by providing necessary info.')}
            </SheetDescription>
          </SheetHeader>
          <Form {...form}>
            <form
              id='user-form'
              onSubmit={form.handleSubmit(onSubmit)}
              className={sideDrawerFormClassName()}
            >
              <div className='grid gap-5 lg:grid-cols-[13rem_minmax(0,1fr)] lg:items-start'>
                <aside className='hidden self-start lg:sticky lg:top-4 lg:z-20 lg:block'>
                  <div className='flex max-h-[calc(100dvh-12rem)] flex-col gap-3 overflow-y-auto overscroll-contain pr-1'>
                    <div className='border-border/60 bg-muted/20 rounded-lg border p-3'>
                      <div className='flex min-w-0 items-center gap-2'>
                        <span className='bg-background flex size-8 shrink-0 items-center justify-center rounded-md border'>
                          <UserRound className='size-4' aria-hidden='true' />
                        </span>
                        <div className='min-w-0'>
                          <p className='truncate text-sm font-medium'>
                            {form.watch('display_name') ||
                              form.watch('username') ||
                              t('User')}
                          </p>
                          <p className='text-muted-foreground truncate text-xs'>
                            {t('Quick navigation')}
                          </p>
                        </div>
                      </div>
                    </div>
                    <nav
                      className='border-border/60 bg-background rounded-lg border p-1'
                      aria-label={t('Quick navigation')}
                    >
                      {navigationSections.map((section) => {
                        const isActive = activeSection === section.id
                        return (
                          <button
                            key={section.id}
                            type='button'
                            className={cn(
                              'hover:bg-muted/60 flex w-full items-start gap-2 rounded-md px-2 py-2 text-left transition-colors',
                              isActive && 'bg-muted/70'
                            )}
                            onClick={() => scrollToSection(section.id)}
                            aria-current={isActive ? 'true' : undefined}
                          >
                            <span className='bg-muted text-muted-foreground mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md'>
                              {section.icon}
                            </span>
                            <span className='min-w-0 flex-1 truncate text-sm font-medium'>
                              {section.label}
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
                <div className='flex min-w-0 flex-col gap-6'>
                  {/* Basic Information */}
                  <div id={USER_SECTION_IDS.BASIC} className='scroll-mt-4'>
                    <SideDrawerSection>
                      <h3 className='text-sm font-medium'>
                        {t('Basic Information')}
                      </h3>

                      <FormField
                        control={form.control}
                        name='username'
                        render={({ field }) => (
                          <FormItem>
                            <FormLabel>{t('Username')}</FormLabel>
                            <FormControl>
                              <Input
                                {...field}
                                placeholder={t('Enter username')}
                                disabled={isUpdate}
                              />
                            </FormControl>
                            <FormMessage />
                          </FormItem>
                        )}
                      />

                      {!isUpdate && (
                        <FormField
                          control={form.control}
                          name='role'
                          render={({ field }) => (
                            <FormItem>
                              <FormLabel>{t('Role')}</FormLabel>
                              <Select
                                items={[
                                  { value: '1', label: t('Common User') },
                                  { value: '10', label: t('Admin') },
                                ]}
                                onValueChange={(value) =>
                                  value !== null &&
                                  field.onChange(Number.parseInt(value))
                                }
                                value={String(field.value)}
                              >
                                <FormControl>
                                  <SelectTrigger>
                                    <SelectValue
                                      placeholder={t('Select a role')}
                                    />
                                  </SelectTrigger>
                                </FormControl>
                                <SelectContent alignItemWithTrigger={false}>
                                  <SelectGroup>
                                    <SelectItem value='1'>
                                      {t('Common User')}
                                    </SelectItem>
                                    <SelectItem value='10'>
                                      {t('Admin')}
                                    </SelectItem>
                                  </SelectGroup>
                                </SelectContent>
                              </Select>
                              <FormDescription>
                                {t("Set the user's role (cannot be Root)")}
                              </FormDescription>
                              <FormMessage />
                            </FormItem>
                          )}
                        />
                      )}

                      <FormField
                        control={form.control}
                        name='display_name'
                        render={({ field }) => (
                          <FormItem>
                            <FormLabel>{t('Display Name')}</FormLabel>
                            <FormControl>
                              <Input
                                {...field}
                                placeholder={t('Enter display name')}
                              />
                            </FormControl>
                            <FormDescription>
                              {t('Leave empty to use username')}
                            </FormDescription>
                            <FormMessage />
                          </FormItem>
                        )}
                      />

                      <FormField
                        control={form.control}
                        name='password'
                        render={({ field }) => (
                          <FormItem>
                            <FormLabel>{t('Password')}</FormLabel>
                            <FormControl>
                              <Input
                                {...field}
                                type='password'
                                placeholder={
                                  isUpdate
                                    ? t('Leave empty to keep unchanged')
                                    : t('Enter password (8-20 characters)')
                                }
                              />
                            </FormControl>
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                    </SideDrawerSection>
                  </div>

                  {/* Group & Quota Settings (Update only) */}
                  {isUpdate && (
                    <div
                      id={USER_SECTION_IDS.GROUP_QUOTA}
                      className='scroll-mt-4'
                    >
                      <SideDrawerSection>
                        <h3 className='text-sm font-medium'>
                          {t('Group & Quota')}
                        </h3>

                        <FormField
                          control={form.control}
                          name='group'
                          render={({ field }) => (
                            <FormItem>
                              <FormLabel>{t('Group')}</FormLabel>
                              <GroupCombobox
                                groups={groups}
                                value={field.value}
                                onValueChange={field.onChange}
                                placeholder={t('Select a group')}
                              />
                              <FormMessage />
                            </FormItem>
                          )}
                        />

                        {/* Per-user exclusive group ratios */}
                        <FormItem>
                          <FormLabel>{t('Exclusive Group Ratios')}</FormLabel>
                          <FormDescription>
                            {t(
                              'Override the group ratio for this user on specific groups. Leave empty to use the default group ratio.'
                            )}
                          </FormDescription>
                          <div className='space-y-2'>
                            {groupRatioFields.map((row, index) => (
                              <div
                                key={row.id}
                                className='flex items-center gap-2'
                              >
                                <div className='flex-1'>
                                  <FormField
                                    control={form.control}
                                    name={`groupRatios.${index}.group`}
                                    render={({ field }) => (
                                      <GroupCombobox
                                        groups={groups}
                                        value={field.value}
                                        onValueChange={field.onChange}
                                        placeholder={t('Select a group')}
                                      />
                                    )}
                                  />
                                </div>
                                <FormField
                                  control={form.control}
                                  name={`groupRatios.${index}.ratio`}
                                  render={({ field }) => (
                                    <Input
                                      type='number'
                                      step='0.01'
                                      min='0'
                                      className='w-28'
                                      value={field.value ?? ''}
                                      onChange={(e) =>
                                        field.onChange(
                                          e.target.value === ''
                                            ? 0
                                            : Number(e.target.value)
                                        )
                                      }
                                      placeholder={t('Ratio')}
                                    />
                                  )}
                                />
                                <Button
                                  type='button'
                                  variant='ghost'
                                  size='icon-sm'
                                  onClick={() => removeGroupRatio(index)}
                                  aria-label={t('Remove')}
                                >
                                  <Trash2 className='h-4 w-4' />
                                </Button>
                              </div>
                            ))}
                            <Button
                              type='button'
                              variant='outline'
                              size='sm'
                              onClick={() =>
                                appendGroupRatio({ group: '', ratio: 1 })
                              }
                            >
                              <Plus className='mr-1 h-4 w-4' />
                              {t('Add Group')}
                            </Button>
                          </div>
                        </FormItem>

                        <FormField
                          control={form.control}
                          name='quota_dollars'
                          render={({ field }) => (
                            <FormItem>
                              <FormLabel>
                                {t('Remaining Quota ({{currency}})', {
                                  currency: currencyLabel,
                                })}
                              </FormLabel>
                              <div className='flex gap-2'>
                                <FormControl>
                                  <Input
                                    value={
                                      tokensOnly
                                        ? String(field.value || 0)
                                        : (field.value || 0).toFixed(6)
                                    }
                                    readOnly
                                    className='flex-1'
                                  />
                                </FormControl>
                                <Button
                                  type='button'
                                  variant='outline'
                                  onClick={() => setQuotaDialogOpen(true)}
                                >
                                  <Pencil className='mr-1 h-4 w-4' />
                                  {t('Adjust Quota')}
                                </Button>
                              </div>
                              <FormDescription>
                                {formatQuota(
                                  parseQuotaFromDollars(field.value || 0)
                                )}
                              </FormDescription>
                              <FormMessage />
                            </FormItem>
                          )}
                        />

                        <FormField
                          control={form.control}
                          name='remark'
                          render={({ field }) => (
                            <FormItem>
                              <FormLabel>{t('Remark')}</FormLabel>
                              <FormControl>
                                <Textarea
                                  {...field}
                                  placeholder={t(
                                    'Admin notes (only visible to admins)'
                                  )}
                                  rows={3}
                                />
                              </FormControl>
                              <FormMessage />
                            </FormItem>
                          )}
                        />
                      </SideDrawerSection>
                    </div>
                  )}

                  {isUpdate && (
                    <div id={USER_SECTION_IDS.TIMEOUT} className='scroll-mt-4'>
                      <SideDrawerSection>
                        <h3 className='text-sm font-medium'>
                          {t('AI Request Timeout')}
                        </h3>
                        <p className='text-muted-foreground text-sm'>
                          {t(
                            'Each request applies both response and total timeout limits.'
                          )}{' '}
                          {t(
                            'All four 0 keeps legacy behavior; otherwise, 0 inherits the system default and -1 disables that limit.'
                          )}
                          {' '}
                          {t(
                            '10-minute example: stream response 600, stream total -1; non-stream response -1, non-stream total 600.'
                          )}
                        </p>

                        <div className='space-y-4'>
                          <h4 className='text-sm font-medium'>
                            {t('Streaming requests')}
                          </h4>
                          <div className='grid gap-4 sm:grid-cols-2'>
                            <TimeoutOverrideField
                              control={form.control}
                              name='stream_response_timeout'
                              label={t('Stream response timeout (seconds)')}
                              description={t(
                                'Maximum time without valid stream output. The timer restarts after each valid output.'
                              )}
                            />

                            <TimeoutOverrideField
                              control={form.control}
                              name='stream_total_timeout'
                              label={t('Stream total timeout (seconds)')}
                              description={t(
                                'Maximum total duration of a streaming request. This timer never restarts.'
                              )}
                            />
                          </div>
                        </div>

                        <div className='space-y-4'>
                          <h4 className='text-sm font-medium'>
                            {t('Non-streaming requests')}
                          </h4>
                          <div className='grid gap-4 sm:grid-cols-2'>
                            <TimeoutOverrideField
                              control={form.control}
                              name='non_stream_response_timeout'
                              label={t('Non-stream response timeout (seconds)')}
                              description={t(
                                'Maximum time to wait for the upstream first response.'
                              )}
                            />

                            <TimeoutOverrideField
                              control={form.control}
                              name='non_stream_total_timeout'
                              label={t('Non-stream total timeout (seconds)')}
                              description={t(
                                'Maximum total duration until the complete response finishes.'
                              )}
                            />
                          </div>
                        </div>
                      </SideDrawerSection>
                    </div>
                  )}

                  {canEditAdminPermissions &&
                    targetIsAdmin &&
                    permissionCatalog.resources.length > 0 && (
                      <FormField
                        control={form.control}
                        name='admin_permissions'
                        render={({ field }) => (
                          <FormItem>
                            <AdminPermissionsEditor
                              embedded
                              catalog={permissionCatalog}
                              value={field.value}
                              onChange={field.onChange}
                              administratorLabel={
                                form.getValues('display_name') ||
                                form.getValues('username')
                              }
                            />
                            {currentUser && (
                              <p className='text-muted-foreground text-xs'>
                                {hasPermission(
                                  currentUser,
                                  ADMIN_PERMISSION_RESOURCES.CHANNEL,
                                  ADMIN_PERMISSION_ACTIONS.SENSITIVE_WRITE
                                )
                                  ? t(
                                      'Your account can edit sensitive channel settings.'
                                    )
                                  : t(
                                      'Your account cannot edit sensitive channel settings.'
                                    )}
                              </p>
                            )}
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                    )}

                  {/* Assigned Employee (common users only) */}
                  {showEmployeeAssignment && currentRow && (
                    <div id={USER_SECTION_IDS.EMPLOYEE} className='scroll-mt-4'>
                      <SideDrawerSection>
                        <h3 className='text-sm font-medium'>
                          {t('Assigned Employee')}
                        </h3>
                        <FormItem>
                          <FormLabel>{t('Owning Employee')}</FormLabel>
                          <EmployeeAssignField
                            key={currentRow.id}
                            userId={currentRow.id}
                            inviterId={currentRow.inviter_id}
                            onChanged={triggerRefresh}
                          />
                          <FormDescription>
                            {t(
                              'Assign this user to an owning employee for commission attribution. Changes take effect immediately.'
                            )}
                          </FormDescription>
                        </FormItem>
                      </SideDrawerSection>
                    </div>
                  )}

                  {/* Binding Information (Read-only) */}
                  {isUpdate && (
                    <div id={USER_SECTION_IDS.BINDINGS} className='scroll-mt-4'>
                      <SideDrawerSection>
                        <h3 className='text-sm font-medium'>
                          {t('Binding Information')}
                        </h3>
                        <p className='text-muted-foreground text-xs'>
                          {t(
                            'Third-party account bindings (read-only, managed by user in profile settings)'
                          )}
                        </p>

                        <div className='flex flex-col gap-3'>
                          {BINDING_FIELDS.map(({ key, label }) => (
                            <div key={key}>
                              <Label className='text-muted-foreground text-xs'>
                                {t(label)}
                              </Label>
                              <Input
                                value={
                                  (currentRow?.[key as keyof User] as string) ||
                                  '-'
                                }
                                disabled
                                className='mt-1'
                              />
                            </div>
                          ))}
                        </div>
                      </SideDrawerSection>
                    </div>
                  )}
                </div>
              </div>
            </form>
          </Form>
          <SheetFooter className={sideDrawerFooterClassName()}>
            <SheetClose render={<Button variant='outline' />}>
              {t('Close')}
            </SheetClose>
            <Button form='user-form' type='submit' disabled={isSubmitting}>
              {isSubmitting ? t('Saving...') : t('Save changes')}
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>

      {/* Adjust Quota Dialog */}
      {currentRow && (
        <UserQuotaDialog
          open={quotaDialogOpen}
          onOpenChange={setQuotaDialogOpen}
          userId={currentRow.id}
          currentQuota={parseQuotaFromDollars(currentQuotaRaw || 0)}
          onSuccess={refreshUserData}
        />
      )}
    </>
  )
}

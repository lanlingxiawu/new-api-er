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
import type { TFunction } from 'i18next'
import { Pencil, Plus, Trash2 } from 'lucide-react'
import {
  useEffect,
  useState,
  useRef,
  type ComponentProps,
  type ReactNode,
} from 'react'
import {
  useForm,
  useFieldArray,
  type Control,
  type SubmitErrorHandler,
  type FieldPath,
} from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import {
  SideDrawerSection,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { StreamProtocolValidationField } from '@/features/system-settings/components/stream-protocol-validation-field'
import {
  EMPTY_PERMISSION_CATALOG,
  hasPermission,
  normalizeAdminPermissions,
  type AdminPermissionMatrix,
} from '@/lib/admin-permissions'
import { getCurrencyDisplay, getCurrencyLabel } from '@/lib/currency'
import { formatQuota, parseQuotaFromDollars } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { accountPasswordSchema } from '@/lib/password-policy'
import { ROLE } from '@/lib/roles'
import { AuthOperationError } from '@/lib/secure-verification'
import { requireServerSuccess } from '@/lib/server-error-message'
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
import { UserChannelExclusionsField } from './user-channel-exclusions-field'
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
  | 'retry_times'

type TimeoutOverrideFieldProps = {
  control: Control<UserFormValues>
  name: TimeoutOverrideFieldName
  label: ReactNode
  description: ReactNode
  // Bounds default to the timeout range; retry counts pass their own.
  min?: number
  max?: number
}

// timeoutBillingDescription explains, from the user's side, what each timeout
// billing mode costs when our time limit ends a request.
function timeoutBillingDescription(
  value: string | undefined,
  t: TFunction
): string {
  if (value === 'charge') {
    return t(
      'On timeout, supported requests return partial output and charge for received usage (upstream counts first, otherwise an estimate). Other non-stream requests charge estimated input only. Per-call models are refunded if no output is received.'
    )
  }
  if (value === 'input') {
    return t(
      'On timeout, only input is charged. Non-stream input is estimated unless upstream usage is available; output is not charged. Per-call models are refunded.'
    )
  }
  return t(
    'On timeout the user is not charged. Upstream has usually billed by then, so the platform bears that cost.'
  )
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
  min = -1,
  max = 604800,
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
              min={min}
              max={max}
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
  CHANNEL_EXCLUSIONS: 'user-channel-exclusions',
  TIMEOUT: 'user-request-timeout',
  RETRY: 'user-retry',
  EMPLOYEE: 'user-assigned-employee',
  BINDINGS: 'user-binding-information',
} as const

export function UsersMutateDrawer({
  open,
  onOpenChange,
  currentRow,
}: UsersMutateDrawerProps) {
  const { t } = useTranslation()
  const targetId = currentRow?.id
  const isUpdate = targetId !== undefined
  const { triggerRefresh, requestVerification } = useUsers()
  const currentUser = useAuthStore((s) => s.auth.user)
  const canReadChannels =
    hasPermission(currentUser, 'channel', 'read') &&
    hasPermission(currentUser, 'admin_menu.channels', 'view')
  const [isSubmitting, setIsSubmitting] = useState(false)
  const submitting = useRef(false)
  const [discardOpen, setDiscardOpen] = useState(false)
  const [loadAttempt, setLoadAttempt] = useState(0)
  const [loadFailed, setLoadFailed] = useState(false)
  const [loadedUser, setLoadedUser] = useState<User>()
  const ready = !isUpdate || loadedUser?.id === targetId
  const session = useRef(0)
  const [quotaDialogOpen, setQuotaDialogOpen] = useState(false)
  const [activeTab, setActiveTab] = useState('basic')
  const [pendingErrorFocus, setPendingErrorFocus] =
    useState<FieldPath<UserFormValues> | null>(null)
  const formRef = useRef<HTMLFormElement>(null)
  // Matrix as loaded from the server; an unchanged matrix is not resubmitted so
  // routine edits of an administrator do not require step-up verification.
  const loadedPermissions = useRef<AdminPermissionMatrix | undefined>(undefined)

  // Fetch groups
  const { data: groupsData } = useQuery({
    queryKey: ['groups'],
    queryFn: async () => requireServerSuccess(await getGroups()),
    staleTime: 5 * 60 * 1000,
  })

  const groups = groupsData?.data || []
  // 只有分组列表加载成功时才能判断规则所属分组是否已被删除；加载中或失败时不做标记。
  const groupsLoaded = groupsData?.success === true

  // Permission catalog is owned by the backend; fetched once and reused.
  const { data: permissionCatalog = EMPTY_PERMISSION_CATALOG } = useQuery({
    queryKey: ['admin-permission-catalog'],
    queryFn: async () => requireServerSuccess(await getPermissionCatalog()),
    staleTime: 5 * 60 * 1000,
  })

  const form = useForm<UserFormValues>({
    resolver: zodResolver(userFormSchema),
    defaultValues: USER_FORM_DEFAULT_VALUES,
    shouldFocusError: false,
  })

  const {
    fields: groupRatioFields,
    append: appendGroupRatio,
    remove: removeGroupRatio,
  } = useFieldArray({ control: form.control, name: 'groupRatios' })

  // Only reopening or changing the target starts a new editing session.
  // A list refresh or language change must not replace an unsaved draft.
  useEffect(() => {
    const version = ++session.current
    if (!open) return
    setActiveTab('basic')
    setDiscardOpen(false)
    setQuotaDialogOpen(false)
    setLoadFailed(false)
    setLoadedUser(undefined)
    if (targetId === undefined) {
      loadedPermissions.current = undefined
      form.reset(USER_FORM_DEFAULT_VALUES)
      return
    }
    let active = true
    getUser(targetId)
      .then((response) => {
        if (!active || session.current !== version) return
        const result = requireServerSuccess(response)
        if (!result.data) throw new Error('Missing user data')
        loadedPermissions.current = result.data.admin_permissions
        form.reset(transformUserToFormDefaults(result.data))
        setLoadedUser(result.data)
      })
      .catch(() => {
        if (active && session.current === version) setLoadFailed(true)
      })
    return () => {
      active = false
    }
  }, [open, targetId, loadAttempt, form])

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
  const tabs = [
    { id: 'basic', label: t('Basic Information') },
    ...(isUpdate ? [{ id: 'group-quota', label: t('Group & Quota') }] : []),
    { id: 'requests', label: t('Request settings') },
    ...(showAdminPermissions
      ? [{ id: 'permissions', label: t('Administrator permissions') }]
      : []),
    ...(isUpdate ? [{ id: 'bindings', label: t('Binding Information') }] : []),
  ]
  const visibleTab = tabs.some((tab) => tab.id === activeTab)
    ? activeTab
    : 'basic'

  const onInvalid: SubmitErrorHandler<UserFormValues> = (errors) => {
    const field = Object.keys(errors)[0] as keyof UserFormValues | undefined
    if (!field) return
    if (field === 'admin_permissions') {
      setActiveTab('permissions')
    } else if (['group', 'groupRatios', 'quota_dollars'].includes(field)) {
      setActiveTab('group-quota')
    } else if (
      ['username', 'display_name', 'password', 'role', 'remark'].includes(field)
    ) {
      setActiveTab('basic')
    } else {
      setActiveTab('requests')
    }
    setPendingErrorFocus(field)
    toast.error(t('Please fix the highlighted fields before saving'))
  }

  useEffect(() => {
    if (!pendingErrorFocus) return
    const frame = window.requestAnimationFrame(() => {
      form.setFocus(pendingErrorFocus)
      setPendingErrorFocus(null)
    })
    return () => window.cancelAnimationFrame(frame)
  }, [pendingErrorFocus, visibleTab, form])

  const onSubmit = async (data: UserFormValues) => {
    if (!ready || submitting.current) return
    if (!isUpdate || data.password) {
      if (!accountPasswordSchema.safeParse(data.password ?? '').success) {
        form.setError('password', {
          type: 'manual',
          message: t('Password must contain between 8 and 128 characters.'),
        })
        setActiveTab('basic')
        setPendingErrorFocus('password')
        return
      }
    }

    submitting.current = true
    setIsSubmitting(true)
    try {
      const payload = transformFormDataToPayload(
        data,
        currentRow?.id,
        permissionCatalog
      )
      if (
        isUpdate &&
        payload.admin_permissions &&
        JSON.stringify(payload.admin_permissions) ===
          JSON.stringify(
            normalizeAdminPermissions(
              loadedPermissions.current,
              permissionCatalog
            )
          )
      ) {
        delete payload.admin_permissions
      }
      // Resetting a password, rewriting admin permissions, or creating an
      // administrator changes who can sign in or what they may do.
      let proofToken: string | undefined
      if (isUpdate && currentRow) {
        if (payload.password || payload.admin_permissions) {
          const proof = await requestVerification({
            scope: 'admin.user.update',
            context: { user_id: currentRow.id },
            title: t('Verify to update user credentials'),
            description: t(
              'Confirm your identity before changing the account {{username}}.',
              { username: currentRow.username }
            ),
          })
          if (!proof) return
          proofToken = proof.proof_token
        }
      } else if ((payload.role ?? 0) >= ROLE.ADMIN) {
        const proof = await requestVerification({
          scope: 'admin.user.create',
          context: { role: payload.role ?? ROLE.ADMIN },
          title: t('Verify to create administrator'),
          description: t(
            'Confirm your identity before creating an administrator account.'
          ),
        })
        if (!proof) return
        proofToken = proof.proof_token
      }
      const result = isUpdate
        ? await updateUser(
            payload as typeof payload & { id: number },
            proofToken
          )
        : await createUser(payload, proofToken)

      if (result.success) {
        toast.success(
          isUpdate
            ? t(SUCCESS_MESSAGES.USER_UPDATED)
            : t(SUCCESS_MESSAGES.USER_CREATED)
        )
        // 后端会顺带清掉已删除分组上的专属倍率，告诉管理员具体清了哪些。
        const removedGroups =
          result.data && 'removed_group_ratios' in result.data
            ? (result.data.removed_group_ratios ?? [])
            : []
        if (removedGroups.length > 0) {
          toast.info(
            t('Removed exclusive ratios for deleted groups: {{groups}}', {
              groups: removedGroups.join(', '),
            })
          )
        }
        if (
          result.data &&
          'removed_excluded_channel_ids' in result.data &&
          result.data.removed_excluded_channel_ids?.length
        ) {
          toast.info(
            t('Deleted channels were removed from the exclusion list.')
          )
        }
        onOpenChange(false)
        triggerRefresh()
      } else {
        handleServerError(
          result,
          isUpdate
            ? t(ERROR_MESSAGES.UPDATE_FAILED)
            : t(ERROR_MESSAGES.CREATE_FAILED)
        )
        if ('committed' in result && result.committed === true) {
          if (isUpdate) await refreshUserData()
          else {
            onOpenChange(false)
            triggerRefresh()
          }
        }
      }
    } catch (error) {
      handleServerError(
        AuthOperationError.from(error),
        t(ERROR_MESSAGES.UNEXPECTED)
      )
    } finally {
      submitting.current = false
      setIsSubmitting(false)
    }
  }

  const refreshUserData = async (quotaOnly = false) => {
    if (!currentRow) return
    const version = session.current
    try {
      const result = requireServerSuccess(await getUser(currentRow.id))
      if (result.success && result.data && version === session.current) {
        const defaults = transformUserToFormDefaults(result.data)
        if (quotaOnly) {
          form.resetField('quota_dollars', {
            defaultValue: defaults.quota_dollars,
          })
        } else {
          loadedPermissions.current = result.data.admin_permissions
          form.reset(defaults)
        }
        setLoadedUser(result.data)
      }
      triggerRefresh()
    } catch (error) {
      handleServerError(error, t(ERROR_MESSAGES.LOAD_FAILED))
    }
  }

  return (
    <>
      <Sheet
        open={open}
        onOpenChange={(v) => {
          if (submitting.current) return
          if (!v && form.formState.isDirty) {
            setDiscardOpen(true)
            return
          }
          onOpenChange(v)
        }}
      >
        <SheetContent className={sideDrawerContentClassName('sm:max-w-3xl')}>
          <SheetHeader className={sideDrawerHeaderClassName('pr-12 sm:pr-12')}>
            <SheetTitle>
              {isUpdate ? t('Update User') : t('Create User')}
            </SheetTitle>
            {currentRow && (
              <SheetDescription className='break-words'>
                {[currentRow.display_name, currentRow.username]
                  .filter(
                    (value, index, values) =>
                      value && values.indexOf(value) === index
                  )
                  .join(' · ')}
              </SheetDescription>
            )}
          </SheetHeader>
          <Form {...form}>
            <Tabs
              value={visibleTab}
              onValueChange={(value) => {
                setActiveTab(String(value))
                if (formRef.current) formRef.current.scrollTop = 0
              }}
              className='min-h-0 flex-1 gap-0'
            >
              <div className='shrink-0 overflow-x-auto px-4 pt-4 pb-1 sm:px-6'>
                <TabsList
                  aria-label={isUpdate ? t('Update User') : t('Create User')}
                  className='min-w-full justify-start'
                >
                  {tabs.map((tab) => (
                    <TabsTrigger
                      key={tab.id}
                      value={tab.id}
                      className='gap-2 px-3'
                    >
                      {tab.label}
                    </TabsTrigger>
                  ))}
                </TabsList>
              </div>
              <form
                ref={formRef}
                id='user-form'
                noValidate
                onSubmit={form.handleSubmit(onSubmit, onInvalid)}
                className={sideDrawerFormClassName()}
              >
                {!ready &&
                  (loadFailed ? (
                    <ErrorState
                      title={t(ERROR_MESSAGES.LOAD_FAILED)}
                      onRetry={() => setLoadAttempt((value) => value + 1)}
                    />
                  ) : (
                    <LoadingState />
                  ))}
                <fieldset
                  disabled={!ready || isSubmitting}
                  className={ready ? 'min-w-0' : 'hidden'}
                >
                  <TabsContent value='basic' keepMounted>
                    <div className='flex min-w-0 flex-col gap-6'>
                      {/* Basic Information */}
                      <div id={USER_SECTION_IDS.BASIC} className='scroll-mt-4'>
                        <SideDrawerSection className='grid items-start sm:grid-cols-2'>
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
                                    readOnly={isUpdate}
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
                              <FormItem className='sm:col-span-2'>
                                <FormLabel>{t('Password')}</FormLabel>
                                <FormControl>
                                  <Input
                                    {...field}
                                    type='password'
                                    placeholder={
                                      isUpdate
                                        ? t('Leave empty to keep unchanged')
                                        : t('Enter password (8–128 characters)')
                                    }
                                  />
                                </FormControl>
                                <FormMessage />
                              </FormItem>
                            )}
                          />
                          {isUpdate && (
                            <FormField
                              control={form.control}
                              name='remark'
                              render={({ field }) => (
                                <FormItem className='sm:col-span-2'>
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
                          )}
                        </SideDrawerSection>
                      </div>

                      {/* Assigned Employee (common users only) */}
                      {showEmployeeAssignment && currentRow && (
                        <div
                          id={USER_SECTION_IDS.EMPLOYEE}
                          className='scroll-mt-4'
                        >
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
                    </div>
                  </TabsContent>
                  {isUpdate && (
                    <TabsContent value='group-quota' keepMounted>
                      {/* Group & Quota Settings (Update only) */}
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
                              {groupRatioFields.map((row, index) => {
                                // A rule left over from a deleted group renders as
                                // an empty combobox, because its value is not in
                                // the options. Say so, instead of showing a blank.
                                const ruleGroup = form.watch(
                                  `groupRatios.${index}.group`
                                )
                                const isDeletedGroup =
                                  groupsLoaded && ruleGroup
                                    ? !groups.includes(ruleGroup)
                                    : false
                                return (
                                  <div
                                    key={row.id}
                                    className='flex flex-col gap-1'
                                  >
                                    <div className='flex items-center gap-2'>
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
                                    {isDeletedGroup && (
                                      <p className='text-muted-foreground text-xs'>
                                        {t(
                                          'This group has been deleted. The rule is inactive and will be removed when you save.'
                                        )}
                                      </p>
                                    )}
                                  </div>
                                )
                              })}
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
                        </SideDrawerSection>
                      </div>
                    </TabsContent>
                  )}
                  <TabsContent value='requests' keepMounted>
                    <div className='flex min-w-0 flex-col gap-6'>
                      <SideDrawerSection>
                        <StreamProtocolValidationField
                          control={form.control}
                          name='stream_protocol_validation'
                          scope='user'
                          legacyValue={form.watch('claude_stream_validation')}
                          disabled={
                            isSubmitting ||
                            (isUpdate &&
                              form.watch('claude_stream_validation') ===
                                undefined)
                          }
                        />
                      </SideDrawerSection>
                      {isUpdate && (
                        <div
                          id={USER_SECTION_IDS.TIMEOUT}
                          className='scroll-mt-4'
                        >
                          <SideDrawerSection>
                            <h3 className='text-sm font-medium'>
                              {t('AI Request Timeout')}
                            </h3>
                            <p className='text-muted-foreground text-sm'>
                              {t(
                                '0 inherits the system default, -1 disables the limit.'
                              )}
                            </p>
                            <p className='text-muted-foreground text-sm'>
                              {t(
                                'Task submissions, Midjourney, Coze non-stream requests and Ali image generation and editing are not subject to these timeouts.'
                              )}
                            </p>

                            <div className='space-y-4'>
                              <h4 className='text-sm font-medium'>
                                {t('Streaming requests')}
                              </h4>
                              <FormField
                                control={form.control}
                                name='stream_response_timeout_mode'
                                render={({ field }) => (
                                  <FormItem>
                                    <FormLabel>
                                      {t('Response timeout mode')}
                                    </FormLabel>
                                    <Select
                                      items={[
                                        {
                                          value: 'first_output',
                                          label: t('First output timeout'),
                                        },
                                        {
                                          value: 'idle',
                                          label: t(
                                            'Continuous silence timeout'
                                          ),
                                        },
                                      ]}
                                      value={field.value ?? 'first_output'}
                                      onValueChange={(value) => {
                                        if (value !== null) {
                                          field.onChange(value)
                                        }
                                      }}
                                    >
                                      <FormControl>
                                        <SelectTrigger className='w-full'>
                                          <SelectValue />
                                        </SelectTrigger>
                                      </FormControl>
                                      <SelectContent
                                        alignItemWithTrigger={false}
                                      >
                                        <SelectGroup>
                                          <SelectItem value='first_output'>
                                            {t('First output timeout')}
                                          </SelectItem>
                                          <SelectItem value='idle'>
                                            {t('Continuous silence timeout')}
                                          </SelectItem>
                                        </SelectGroup>
                                      </SelectContent>
                                    </Select>
                                    <FormDescription>
                                      {field.value === 'idle'
                                        ? t(
                                            'Restart this timeout after every valid stream output. The request stops if the stream stays silent longer than this value.'
                                          )
                                        : t(
                                            'Only wait for the first valid stream output. Later silence is not managed by this timeout.'
                                          )}
                                    </FormDescription>
                                    <FormMessage />
                                  </FormItem>
                                )}
                              />
                              <div className='grid items-start gap-4 sm:grid-cols-2'>
                                <TimeoutOverrideField
                                  control={form.control}
                                  name='stream_response_timeout'
                                  label={t('Response timeout (seconds)')}
                                  description={t(
                                    'Used as first-output wait time or continuous silence time, depending on the selected stream response timeout mode.'
                                  )}
                                />
                                <TimeoutOverrideField
                                  control={form.control}
                                  name='stream_total_timeout'
                                  label={t('Total timeout (seconds)')}
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
                              <div className='grid items-start gap-4 sm:grid-cols-2'>
                                <TimeoutOverrideField
                                  control={form.control}
                                  name='non_stream_response_timeout'
                                  label={t(
                                    'Non-stream response timeout (seconds)'
                                  )}
                                  description={t(
                                    'Maximum time to wait for the upstream first response.'
                                  )}
                                />
                                <TimeoutOverrideField
                                  control={form.control}
                                  name='non_stream_total_timeout'
                                  label={t(
                                    'Non-stream total timeout (seconds)'
                                  )}
                                  description={t(
                                    'Maximum total duration until the complete response finishes.'
                                  )}
                                />
                              </div>
                              <div className='grid items-start gap-4 sm:grid-cols-2'>
                                <FormField
                                  control={form.control}
                                  name='non_stream_timeout_billing'
                                  render={({ field }) => (
                                    <FormItem>
                                      <FormLabel>
                                        {t('Timeout billing')}
                                      </FormLabel>
                                      <Select
                                        items={[
                                          {
                                            value: 'refund',
                                            label: t('Refund on timeout'),
                                          },
                                          {
                                            value: 'charge',
                                            label: t(
                                              'Charge usage already produced'
                                            ),
                                          },
                                          {
                                            value: 'input',
                                            label: t('Charge input only'),
                                          },
                                        ]}
                                        value={field.value ?? 'refund'}
                                        onValueChange={(value) => {
                                          if (value !== null) {
                                            field.onChange(value)
                                          }
                                        }}
                                      >
                                        <FormControl>
                                          <SelectTrigger className='w-full'>
                                            <SelectValue />
                                          </SelectTrigger>
                                        </FormControl>
                                        <SelectContent
                                          alignItemWithTrigger={false}
                                        >
                                          <SelectGroup>
                                            <SelectItem value='refund'>
                                              {t('Refund on timeout')}
                                            </SelectItem>
                                            <SelectItem value='charge'>
                                              {t(
                                                'Charge usage already produced'
                                              )}
                                            </SelectItem>
                                            <SelectItem value='input'>
                                              {t('Charge input only')}
                                            </SelectItem>
                                          </SelectGroup>
                                        </SelectContent>
                                      </Select>
                                      <FormDescription>
                                        {timeoutBillingDescription(
                                          field.value,
                                          t
                                        )}
                                      </FormDescription>
                                      <FormMessage />
                                    </FormItem>
                                  )}
                                />
                              </div>
                            </div>
                          </SideDrawerSection>
                        </div>
                      )}

                      {isUpdate && (
                        <div
                          id={USER_SECTION_IDS.RETRY}
                          className='scroll-mt-4'
                        >
                          <SideDrawerSection>
                            <h3 className='text-sm font-medium'>
                              {t('Retry')}
                            </h3>
                            <div className='grid items-start gap-4 sm:grid-cols-2'>
                              <TimeoutOverrideField
                                control={form.control}
                                name='retry_times'
                                min={-1}
                                max={20}
                                label={t('Retry attempts')}
                                description={t(
                                  'How many times a failed request may be retried on another channel. 0 follows the group or system default, -1 never retries.'
                                )}
                              />
                            </div>
                          </SideDrawerSection>
                        </div>
                      )}

                      {isUpdate && canReadChannels && (
                        <div
                          id={USER_SECTION_IDS.CHANNEL_EXCLUSIONS}
                          className='scroll-mt-4'
                        >
                          <SideDrawerSection>
                            <FormField
                              control={form.control}
                              name='excluded_channel_ids'
                              render={({ field }) => (
                                <FormItem>
                                  <FormLabel>
                                    {t('Excluded channels')}
                                  </FormLabel>
                                  <FormControl>
                                    <UserChannelExclusionsField
                                      id='user-excluded-channels'
                                      value={field.value ?? []}
                                      onChange={field.onChange}
                                      disabled={
                                        isSubmitting ||
                                        field.value === undefined
                                      }
                                    />
                                  </FormControl>
                                  <FormDescription>
                                    {t(
                                      'Applies to all tokens and groups for this user. Leave empty to allow all otherwise available channels.'
                                    )}
                                  </FormDescription>
                                  <FormMessage />
                                </FormItem>
                              )}
                            />
                          </SideDrawerSection>
                        </div>
                      )}
                    </div>
                  </TabsContent>
                  {showAdminPermissions && (
                    <TabsContent value='permissions' keepMounted>
                      <FormField
                        control={form.control}
                        name='admin_permissions'
                        render={({ field }) => (
                          <FormItem>
                            <AdminPermissionsEditor
                              catalog={permissionCatalog}
                              value={field.value}
                              onChange={field.onChange}
                            />
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                    </TabsContent>
                  )}
                  {isUpdate && (
                    <TabsContent value='bindings' keepMounted>
                      {/* Binding Information (Read-only) */}
                      <div
                        id={USER_SECTION_IDS.BINDINGS}
                        className='scroll-mt-4'
                      >
                        <SideDrawerSection>
                          <h3 className='text-sm font-medium'>
                            {t('Binding Information')}
                          </h3>
                          <p className='text-muted-foreground text-xs'>
                            {t(
                              'Third-party account bindings (read-only, managed by user in Security & Access)'
                            )}
                          </p>

                          <div className='grid gap-4 sm:grid-cols-2'>
                            {BINDING_FIELDS.map(({ key, label }) => (
                              <div key={key}>
                                <Label className='text-muted-foreground text-xs'>
                                  {t(label)}
                                </Label>
                                <div className='mt-1 flex min-w-0 items-start justify-between gap-2 rounded-lg border p-2 text-sm'>
                                  <span className='min-w-0 break-all'>
                                    {String(
                                      loadedUser?.[key as keyof User] || '-'
                                    )}
                                  </span>
                                  {!!loadedUser?.[key as keyof User] && (
                                    <CopyButton
                                      size='icon-sm'
                                      value={String(
                                        loadedUser[key as keyof User]
                                      )}
                                    />
                                  )}
                                </div>
                              </div>
                            ))}
                          </div>
                        </SideDrawerSection>
                      </div>
                    </TabsContent>
                  )}
                </fieldset>
              </form>
            </Tabs>
          </Form>
          <SheetFooter className={sideDrawerFooterClassName()}>
            {form.formState.isDirty && (
              <span className='text-muted-foreground col-span-2 text-xs sm:mr-auto'>
                {t('Unsaved changes')}
              </span>
            )}
            <SheetClose
              render={<Button variant='outline' disabled={isSubmitting} />}
            >
              {t('Close')}
            </SheetClose>
            <Button
              form='user-form'
              type='submit'
              disabled={isSubmitting || !ready}
            >
              {isSubmitting ? t('Saving...') : t('Save changes')}
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>

      <ConfirmDialog
        open={discardOpen}
        onOpenChange={setDiscardOpen}
        title={t('Unsaved changes')}
        desc={t('Discard your unsaved changes?')}
        confirmText={t('Discard changes')}
        handleConfirm={() => {
          setDiscardOpen(false)
          form.reset()
          onOpenChange(false)
        }}
      />

      {/* Adjust Quota Dialog */}
      {currentRow && (
        <UserQuotaDialog
          open={quotaDialogOpen}
          onOpenChange={setQuotaDialogOpen}
          userId={currentRow.id}
          currentQuota={parseQuotaFromDollars(currentQuotaRaw || 0)}
          onSuccess={() => refreshUserData(true)}
        />
      )}
    </>
  )
}

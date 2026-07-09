import { useState, useEffect, useMemo, useRef, type UIEvent } from 'react'
import { z } from 'zod'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { formatBusinessTargetAmount } from '@/features/business/format'
import { searchUsers } from '@/features/users/api'
import type { User } from '@/features/users/types'
import { createEmployee, getEmployeeTiers, updateEmployee } from '../api'
import {
  compareEmployeeTiersByGroupLevel,
  getEmployeeTierGroupBadgeClass,
  getEmployeeTierGroupDotClass,
  getEmployeeTierGroup,
  getEmployeeTierLevelBadgeClass,
} from '../lib/tiers'
import type { EmployeeProfile, EmployeeTier } from '../types'

const EMPTY_TIERS: EmployeeTier[] = []
const USER_PICKER_PAGE_SIZE = 20
const DEFAULT_TIER_GROUP = '通用'

const createSchema = z.object({
  user_id: z.number({ error: 'Required' }).positive('Must be positive'),
  tier_id: z.number().optional(),
  remark: z.string().max(255).optional(),
})

const updateSchema = z.object({
  tier_id: z.number().optional(),
  status: z.number().min(1).max(2),
  remark: z.string().max(255).optional(),
})

type CreateValues = z.infer<typeof createSchema>
type UpdateValues = z.infer<typeof updateSchema>

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentRow?: EmployeeProfile
  onSuccess?: () => void
}

// Search users server-side and select one instead of typing an ID.
function UserPicker({
  value,
  onSelect,
  onClear,
}: {
  value?: number
  onSelect: (user: User) => void
  onClear: () => void
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [keyword, setKeyword] = useState('')
  const [debounced, setDebounced] = useState('')
  const [selectedLabel, setSelectedLabel] = useState('')
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!value) setSelectedLabel('')
  }, [value])

  useEffect(() => {
    const timer = setTimeout(() => setDebounced(keyword.trim()), 300)
    return () => clearTimeout(timer)
  }, [keyword])

  useEffect(() => {
    if (!open) return
    const handler = (e: MouseEvent) => {
      if (
        containerRef.current &&
        !containerRef.current.contains(e.target as Node)
      ) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [open])

  const { data, fetchNextPage, hasNextPage, isFetching, isFetchingNextPage } =
    useInfiniteQuery({
      queryKey: ['employee-user-search', debounced],
      queryFn: ({ pageParam }) =>
        searchUsers({
          keyword: debounced,
          p: Number(pageParam),
          page_size: USER_PICKER_PAGE_SIZE,
          exclude_employee: true,
          exclude_root: true,
          exclude_assigned_customer: true,
        }),
      initialPageParam: 1,
      getNextPageParam: (lastPage) => {
        const page = lastPage.data?.page ?? 1
        const pageSize = lastPage.data?.page_size ?? USER_PICKER_PAGE_SIZE
        const total = lastPage.data?.total ?? 0
        return page * pageSize < total ? page + 1 : undefined
      },
      enabled: open,
    })
  const users = data?.pages.flatMap((page) => page.data?.items ?? []) ?? []
  const isInitialFetching = isFetching && !data

  const handleListScroll = (event: UIEvent<HTMLUListElement>) => {
    const list = event.currentTarget
    const distanceToBottom =
      list.scrollHeight - list.scrollTop - list.clientHeight
    if (distanceToBottom > 48 || !hasNextPage || isFetchingNextPage) return
    void fetchNextPage()
  }

  const clearSelection = () => {
    setSelectedLabel('')
    setKeyword('')
    setDebounced('')
    onClear()
    setOpen(true)
  }

  return (
    <div ref={containerRef} className='relative'>
      <Input
        type='text'
        autoComplete='off'
        placeholder={t('Search username / display name / email')}
        value={open ? keyword : selectedLabel}
        onChange={(e) => {
          setKeyword(e.target.value)
          if (!open) setOpen(true)
        }}
        onFocus={() => {
          setKeyword('')
          setOpen(true)
        }}
        className={value && !open ? 'pr-9' : undefined}
      />
      {value && !open ? (
        <Button
          type='button'
          size='icon'
          variant='ghost'
          className='absolute top-1/2 right-1 size-7 -translate-y-1/2'
          title={t('Clear selection')}
          aria-label={t('Clear selection')}
          onMouseDown={(event) => event.preventDefault()}
          onClick={clearSelection}
        >
          <X className='size-4' />
        </Button>
      ) : null}
      {open && (
        <div className='bg-popover text-popover-foreground absolute top-full z-[100] mt-1 w-full rounded-md border shadow-md'>
          {isInitialFetching ? (
            <div className='text-muted-foreground px-2 py-6 text-center text-sm'>
              {t('Loading...')}
            </div>
          ) : users.length === 0 ? (
            <div className='text-muted-foreground px-2 py-6 text-center text-sm'>
              {t('No users found')}
            </div>
          ) : (
            <ul
              className='max-h-[240px] overflow-y-auto p-1'
              onScroll={handleListScroll}
            >
              {users.map((u) => (
                <li
                  key={u.id}
                  role='option'
                  aria-selected={value === u.id}
                  className={cn(
                    'hover:bg-accent hover:text-accent-foreground flex cursor-pointer flex-col gap-0.5 rounded-sm px-2 py-1.5 text-sm',
                    value === u.id && 'bg-accent'
                  )}
                  onMouseDown={(e) => {
                    e.preventDefault()
                    setSelectedLabel(
                      `${u.username}${u.display_name ? ` (${u.display_name})` : ''} #${u.id}`
                    )
                    setKeyword('')
                    setDebounced('')
                    onSelect(u)
                    setOpen(false)
                  }}
                >
                  <span className='font-medium'>
                    {u.username}
                    {u.display_name ? ` (${u.display_name})` : ''}
                  </span>
                  <span className='text-muted-foreground text-xs'>
                    #{u.id}
                    {u.email ? ` / ${u.email}` : ''}
                  </span>
                </li>
              ))}
              {isFetchingNextPage ? (
                <li className='text-muted-foreground px-2 py-3 text-center text-sm'>
                  {t('Loading...')}
                </li>
              ) : null}
            </ul>
          )}
        </div>
      )}
    </div>
  )
}

function TierSelectField({
  value,
  onValueChange,
  tiers,
}: {
  value: string
  onValueChange: (tierId: string) => void
  tiers: EmployeeTier[]
}) {
  const { t } = useTranslation()

  const groups = useMemo(() => {
    const map = new Map<string, EmployeeTier[]>()
    tiers.forEach((tier) => {
      const group = getEmployeeTierGroup(tier, DEFAULT_TIER_GROUP)
      if (!map.has(group)) map.set(group, [])
      map.get(group)!.push(tier)
    })
    return Array.from(map.entries())
      .sort((a, b) =>
        a[0].localeCompare(b[0], undefined, {
          numeric: true,
          sensitivity: 'base',
        })
      )
      .map(
        ([group, ts]) =>
          [
            group,
            [...ts].sort(
              (a, b) =>
                Number(a.level || 0) - Number(b.level || 0) ||
                Number(a.threshold_usd || 0) - Number(b.threshold_usd || 0) ||
                Number(a.id || 0) - Number(b.id || 0)
            ),
          ] as [string, EmployeeTier[]]
      )
  }, [tiers])

  const currentTier = tiers.find((t) => String(t.id) === value)

  const [activeGroup, setActiveGroup] = useState<string>(
    () =>
      (currentTier
        ? getEmployeeTierGroup(currentTier, DEFAULT_TIER_GROUP)
        : groups[0]?.[0]) ?? DEFAULT_TIER_GROUP
  )

  useEffect(() => {
    if (currentTier) {
      setActiveGroup(getEmployeeTierGroup(currentTier, DEFAULT_TIER_GROUP))
    }
  }, [value, currentTier])

  useEffect(() => {
    if (currentTier || groups.length === 0) return
    if (!groups.some(([group]) => group === activeGroup)) {
      setActiveGroup(groups[0][0])
    }
  }, [activeGroup, currentTier, groups])

  const tiersForGroup = useMemo(
    () => groups.find(([group]) => group === activeGroup)?.[1] ?? [],
    [groups, activeGroup]
  )

  const selectGroup = (group: string) => {
    setActiveGroup(group)
    const groupTiers = groups.find(([g]) => g === group)?.[1] ?? []
    if (groupTiers[0]) onValueChange(String(groupTiers[0].id))
  }

  if (tiers.length === 0) {
    return (
      <div className='space-y-1'>
        <label className='text-sm font-medium'>{t('Commission Tier')}</label>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Commission rate and target are determined by the tier. Configure tiers in the Commission Tiers tab.'
          )}
        </p>
      </div>
    )
  }

  return (
    <div className='space-y-2'>
      <label className='text-sm font-medium'>{t('Commission Tier')}</label>

      {/* Group tabs */}
      <div className='flex flex-wrap gap-2'>
        {groups.map(([group]) => (
          <button
            key={group}
            type='button'
            onClick={() => selectGroup(group)}
            className={cn(
              'rounded-md border px-4 py-1.5 text-sm font-medium transition-colors',
              activeGroup === group
                ? 'border-primary bg-primary text-primary-foreground'
                : 'border-input bg-background hover:bg-accent hover:text-accent-foreground'
            )}
          >
            <span
              className={cn(
                'mr-2 inline-block size-2 rounded-full align-middle',
                activeGroup === group
                  ? 'bg-primary-foreground'
                  : getEmployeeTierGroupDotClass(group)
              )}
            />
            {group}
          </button>
        ))}
      </div>

      {/* Tier buttons within the selected group */}
      {tiersForGroup.length > 0 && (
        <div className='flex flex-wrap gap-2'>
          {tiersForGroup.map((tier) => (
            <button
              key={tier.id}
              type='button'
              onClick={() => onValueChange(String(tier.id))}
              className={cn(
                'rounded-md border px-4 py-1.5 text-sm transition-colors',
                String(tier.id) === value
                  ? getEmployeeTierLevelBadgeClass(tier.level)
                  : 'border-input bg-background hover:bg-accent hover:text-accent-foreground'
              )}
            >
              {t('Tier {{level}}', { level: tier.level })}
              <span className='text-muted-foreground ml-1.5'>
                {(Number(tier.rate || 0) * 100).toFixed(1)}%
              </span>
            </button>
          ))}
        </div>
      )}

      {/* 当前选中等级信息 */}
      {currentTier && (
        <div className='bg-muted text-muted-foreground rounded-md px-3 py-2 text-xs'>
          <Badge
            variant='outline'
            className={cn(
              'align-middle',
              getEmployeeTierLevelBadgeClass(currentTier.level)
            )}
          >
            {t('Tier {{level}}', { level: currentTier.level })}
          </Badge>
          {' · '}
          <Badge
            variant='outline'
            className={cn(
              'mx-1.5 align-middle',
              getEmployeeTierGroupBadgeClass(
                currentTier.group || DEFAULT_TIER_GROUP
              )
            )}
          >
            {currentTier.group || DEFAULT_TIER_GROUP}
          </Badge>
          {t('Threshold')}: $
          {formatBusinessTargetAmount(currentTier.threshold_usd).replace(
            /^\$/,
            ''
          )}
          {' · '}
          {t('Rate')}: {(Number(currentTier.rate || 0) * 100).toFixed(1)}%
        </div>
      )}

      <p className='text-muted-foreground text-xs'>
        {t(
          'Commission rate and target are determined by the tier. Configure tiers in the Commission Tiers tab.'
        )}
      </p>
    </div>
  )
}

export function EmployeeFormDialog({
  open,
  onOpenChange,
  currentRow,
  onSuccess,
}: Props) {
  const { t } = useTranslation()
  const isUpdate = !!currentRow
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [selectedTierId, setSelectedTierId] = useState('')

  const { data: tiersData } = useQuery({
    queryKey: ['employee-tiers'],
    queryFn: getEmployeeTiers,
    enabled: open,
  })
  const tiers = tiersData?.data ?? EMPTY_TIERS

  const createForm = useForm<CreateValues>({
    resolver: zodResolver(createSchema),
    defaultValues: {},
  })

  const updateForm = useForm<UpdateValues>({
    resolver: zodResolver(updateSchema),
    defaultValues: {
      status: currentRow?.status ?? 1,
      remark: currentRow?.remark ?? '',
    },
  })

  useEffect(() => {
    if (!open) setSelectedTierId('')
  }, [open])

  useEffect(() => {
    if (!open || !currentRow) return
    const tierId = Number(currentRow.current_tier_id ?? 0)
    setSelectedTierId(tierId > 0 ? String(tierId) : '')
  }, [currentRow, open])

  // Auto-select the lowest-level default tier when opening in create mode
  useEffect(() => {
    if (!open || currentRow || tiers.length === 0 || selectedTierId) return
    const sorted = [...tiers].sort((a, b) =>
      compareEmployeeTiersByGroupLevel(a, b, DEFAULT_TIER_GROUP)
    )
    const defaultTier =
      sorted.find((t) => t.group === DEFAULT_TIER_GROUP) ?? sorted[0]
    if (defaultTier) setSelectedTierId(String(defaultTier.id))
  }, [open, currentRow, tiers, selectedTierId])

  useEffect(() => {
    if (!open || !currentRow) return
    updateForm.reset({
      status: currentRow.status ?? 1,
      remark: currentRow.remark ?? '',
    })
  }, [currentRow, open, updateForm])

  const handleTierChange = (tierId: string) => {
    setSelectedTierId(tierId)
  }

  const selectedUserId = createForm.watch('user_id')
  const currentEmployeeName =
    currentRow?.username || (currentRow ? `#${currentRow.user_id}` : '')

  const handleCreate = async (values: CreateValues) => {
    setIsSubmitting(true)
    try {
      const tierId = Number(selectedTierId) || 0
      const res = await createEmployee({
        user_id: values.user_id,
        tier_id: tierId,
        remark: values.remark,
      })
      if (!res.success) throw new Error(res.message ?? 'Failed')
      toast.success(t('Employee created successfully'))
      createForm.reset()
      onOpenChange(false)
      onSuccess?.()
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('Operation failed'))
    } finally {
      setIsSubmitting(false)
    }
  }

  const handleUpdate = async (values: UpdateValues) => {
    if (!currentRow) return
    setIsSubmitting(true)
    try {
      const tierId = Number(selectedTierId) || 0
      const res = await updateEmployee(currentRow.id, {
        tier_id: tierId,
        status: values.status,
        remark: values.remark,
      })
      if (!res.success) throw new Error(res.message ?? 'Failed')
      toast.success(t('Employee updated successfully'))
      onOpenChange(false)
      onSuccess?.()
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('Operation failed'))
    } finally {
      setIsSubmitting(false)
    }
  }

  const tierField = (
    <TierSelectField
      value={selectedTierId}
      onValueChange={handleTierChange}
      tiers={tiers}
    />
  )

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className='h-[86vh] max-h-[640px] overflow-visible sm:max-w-120'
        initialFocus={false}
      >
        <DialogHeader>
          <DialogTitle>
            {isUpdate ? t('Edit Employee') : t('Create Employee')}
          </DialogTitle>
          <DialogDescription>
            {isUpdate
              ? t('Adjust tier, status, and internal remark for this employee.')
              : t(
                  'Pick an existing user, choose a starting tier, then create the employee profile.'
                )}
          </DialogDescription>
        </DialogHeader>

        {!isUpdate ? (
          <Form {...createForm}>
            <form
              onSubmit={createForm.handleSubmit(handleCreate)}
              className='space-y-4'
            >
              <FormField
                control={createForm.control}
                name='user_id'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('User')}</FormLabel>
                    <FormControl>
                      <UserPicker
                        value={field.value}
                        onSelect={(user) => {
                          field.onChange(user.id)
                          createForm.setValue('remark', user.remark ?? '')
                        }}
                        onClear={() => {
                          field.onChange(undefined)
                          createForm.setValue('remark', '')
                        }}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Search and select the user to assign employee status'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              {tierField}
              <FormField
                control={createForm.control}
                name='remark'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Remark')}</FormLabel>
                    <FormControl>
                      <Textarea
                        rows={3}
                        placeholder={t('Optional')}
                        {...field}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <DialogFooter>
                <Button
                  type='button'
                  variant='outline'
                  onClick={() => onOpenChange(false)}
                >
                  {t('Cancel')}
                </Button>
                <Button
                  type='submit'
                  disabled={isSubmitting || !selectedUserId}
                >
                  {isSubmitting ? t('Saving...') : t('Create')}
                </Button>
              </DialogFooter>
            </form>
          </Form>
        ) : (
          <Form {...updateForm}>
            <form
              onSubmit={updateForm.handleSubmit(handleUpdate)}
              className='space-y-4'
            >
              <div className='bg-muted/30 rounded-lg border px-3 py-2'>
                <div className='text-muted-foreground text-xs'>
                  {t('Employee')}
                </div>
                <div className='mt-1 flex min-w-0 items-center gap-2'>
                  <span className='truncate font-medium'>
                    {currentEmployeeName}
                  </span>
                  {currentRow?.display_name ? (
                    <span className='text-muted-foreground truncate text-sm'>
                      {currentRow.display_name}
                    </span>
                  ) : null}
                  <Badge variant='outline' className='ml-auto shrink-0'>
                    #{currentRow?.user_id}
                  </Badge>
                </div>
                <div className='mt-2 flex flex-wrap items-center gap-2 text-xs'>
                  <Badge
                    variant={currentRow?.status === 2 ? 'secondary' : 'default'}
                  >
                    {currentRow?.status === 2 ? t('Disabled') : t('Enabled')}
                  </Badge>
                  <span className='text-muted-foreground'>
                    {t('Customer Count')}: {currentRow?.customer_count ?? 0}
                  </span>
                </div>
              </div>
              {tierField}
              <FormField
                control={updateForm.control}
                name='status'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Status')}</FormLabel>
                    <Select
                      value={String(field.value)}
                      onValueChange={(v) => {
                        if (v != null) field.onChange(Number.parseInt(v, 10))
                      }}
                    >
                      <FormControl>
                        <SelectTrigger>
                          <SelectValue>
                            {field.value === 2 ? t('Disabled') : t('Enabled')}
                          </SelectValue>
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent>
                        <SelectItem value='1'>{t('Enabled')}</SelectItem>
                        <SelectItem value='2'>{t('Disabled')}</SelectItem>
                      </SelectContent>
                    </Select>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={updateForm.control}
                name='remark'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Remark')}</FormLabel>
                    <FormControl>
                      <Textarea
                        rows={3}
                        placeholder={t('Optional')}
                        {...field}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <DialogFooter>
                <Button
                  type='button'
                  variant='outline'
                  onClick={() => onOpenChange(false)}
                >
                  {t('Cancel')}
                </Button>
                <Button type='submit' disabled={isSubmitting}>
                  {isSubmitting ? t('Saving...') : t('Save')}
                </Button>
              </DialogFooter>
            </form>
          </Form>
        )}
      </DialogContent>
    </Dialog>
  )
}

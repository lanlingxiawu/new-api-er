import { useState, useEffect, useRef, type UIEvent } from 'react'
import { z } from 'zod'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
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
import { formatBusinessTargetAmount } from '@/features/business/format'
import { searchUsers } from '@/features/users/api'
import {
  createEmployee,
  getEmployeeTiers,
  setEmployeeTier,
  updateEmployee,
} from '../api'
import type { EmployeeProfile, EmployeeTier } from '../types'

const EMPTY_TIERS: EmployeeTier[] = []
const USER_PICKER_PAGE_SIZE = 20

const createSchema = z.object({
  user_id: z.number({ error: 'Required' }).positive('Must be positive'),
  commission_rate: z.number().min(0, 'Min 0').max(1, 'Max 1 (100%)'),
  target_amount: z.number().min(0).optional(),
  remark: z.string().max(255).optional(),
})

const updateSchema = z.object({
  commission_rate: z.number().min(0, 'Min 0').max(1, 'Max 1 (100%)'),
  target_amount: z.number().min(0),
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

function isSameNumber(left: number | undefined, right: number | undefined) {
  return Math.abs(Number(left ?? 0) - Number(right ?? 0)) < 0.000001
}

function resolveSelectedTierId(
  currentRow: EmployeeProfile | undefined,
  tiers: EmployeeTier[]
) {
  if (!currentRow) return ''

  const currentTierId = Number(currentRow.current_tier_id ?? 0)
  if (currentTierId > 0) return String(currentTierId)

  const matchedTier = tiers.find(
    (tier) =>
      isSameNumber(tier.rate, currentRow.commission_rate) &&
      isSameNumber(tier.threshold_usd, currentRow.target_amount)
  )
  return matchedTier ? String(matchedTier.id) : ''
}

// Search users server-side and select one instead of typing an ID.
function UserPicker({
  value,
  onSelect,
}: {
  value?: number
  onSelect: (id: number) => void
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
      />
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
                    onSelect(u.id)
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
    defaultValues: { commission_rate: 0.1, target_amount: 0 },
  })

  const updateForm = useForm<UpdateValues>({
    resolver: zodResolver(updateSchema),
    defaultValues: {
      commission_rate: currentRow?.commission_rate ?? 0.1,
      target_amount: Number(currentRow?.target_amount ?? 0),
      status: currentRow?.status ?? 1,
      remark: currentRow?.remark ?? '',
    },
  })

  useEffect(() => {
    if (!open) setSelectedTierId('')
  }, [open])

  useEffect(() => {
    if (!open || !currentRow) return
    setSelectedTierId(resolveSelectedTierId(currentRow, tiers))
  }, [currentRow, open, tiers])

  useEffect(() => {
    if (!open || !currentRow) return
    updateForm.reset({
      commission_rate: currentRow.commission_rate ?? 0.1,
      target_amount: Number(currentRow.target_amount ?? 0),
      status: currentRow.status ?? 1,
      remark: currentRow.remark ?? '',
    })
  }, [currentRow, open, updateForm])

  const applyTierPreset = (tierId: string | null) => {
    if (!tierId) {
      setSelectedTierId('')
      return
    }
    setSelectedTierId(tierId)
    const tier = tiers.find((item) => String(item.id) === tierId)
    if (!tier) return
    if (isUpdate) {
      updateForm.setValue('commission_rate', Number(tier.rate ?? 0), {
        shouldDirty: true,
        shouldValidate: true,
      })
      updateForm.setValue('target_amount', Number(tier.threshold_usd ?? 0), {
        shouldDirty: true,
        shouldValidate: true,
      })
    } else {
      createForm.setValue('commission_rate', Number(tier.rate ?? 0), {
        shouldDirty: true,
        shouldValidate: true,
      })
      createForm.setValue('target_amount', Number(tier.threshold_usd ?? 0), {
        shouldDirty: true,
        shouldValidate: true,
      })
    }
  }

  const tierPresetField =
    tiers.length > 0 ? (
      <div className='space-y-2'>
        <label className='text-sm font-medium'>{t('Apply Tier Preset')}</label>
        <Select value={selectedTierId} onValueChange={applyTierPreset}>
          <SelectTrigger className='w-full'>
            <SelectValue
              placeholder={t('Select a tier to fill rate and target')}
            />
          </SelectTrigger>
          <SelectContent
            align='start'
            alignItemWithTrigger={false}
            sideOffset={6}
            className='max-h-60 rounded-xl p-1 shadow-lg'
          >
            {tiers.map((tier) => (
              <SelectItem
                key={tier.id}
                value={String(tier.id)}
                className='min-h-9 px-3 py-2'
              >
                {t(
                  'Tier {{level}} - Threshold ${{threshold}} / Rate {{rate}}',
                  {
                    level: tier.level,
                    threshold: formatBusinessTargetAmount(
                      tier.threshold_usd
                    ).replace(/^\$/, ''),
                    rate: `${(Number(tier.rate || 0) * 100).toFixed(1)}%`,
                  }
                )}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <p className='text-muted-foreground text-xs'>
          {t(
            'After selection, the values below are filled automatically and can still be edited.'
          )}
        </p>
      </div>
    ) : null

  const handleCreate = async (values: CreateValues) => {
    setIsSubmitting(true)
    try {
      const res = await createEmployee({
        user_id: values.user_id,
        commission_rate: values.commission_rate,
        target_amount: values.target_amount ?? 0,
        remark: values.remark,
      })
      if (!res.success) throw new Error(res.message ?? 'Failed')
      const tierId = Number(selectedTierId)
      if (res.data?.id && Number.isFinite(tierId) && tierId > 0) {
        const tierRes = await setEmployeeTier(res.data.id, {
          tier_id: tierId,
          source: 'manual',
        })
        if (!tierRes.success) throw new Error(tierRes.message ?? 'Failed')
      }
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
      const res = await updateEmployee(currentRow.id, {
        commission_rate: values.commission_rate,
        target_amount: values.target_amount,
        status: values.status,
        remark: values.remark,
      })
      if (!res.success) throw new Error(res.message ?? 'Failed')
      const tierId = Number(selectedTierId)
      const currentTierId = Number(currentRow.current_tier_id ?? 0)
      if (Number.isFinite(tierId) && tierId > 0 && tierId !== currentTierId) {
        const tierRes = await setEmployeeTier(currentRow.id, {
          tier_id: tierId,
          source: 'manual',
        })
        if (!tierRes.success) throw new Error(tierRes.message ?? 'Failed')
      }
      toast.success(t('Employee updated successfully'))
      onOpenChange(false)
      onSuccess?.()
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('Operation failed'))
    } finally {
      setIsSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='sm:max-w-[480px]' initialFocus={false}>
        <DialogHeader>
          <DialogTitle>
            {isUpdate ? t('Edit Employee') : t('Create Employee')}
          </DialogTitle>
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
                        onSelect={(id) => field.onChange(id)}
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
              {tierPresetField}
              <FormField
                control={createForm.control}
                name='commission_rate'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Commission Rate')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        step='0.01'
                        min='0'
                        max='1'
                        placeholder='e.g. 0.1'
                        {...field}
                        onChange={(e) =>
                          field.onChange(parseFloat(e.target.value) || 0)
                        }
                      />
                    </FormControl>
                    <FormDescription>
                      {t('0.0 ~ 1.0 (e.g. 0.1 = 10%)')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={createForm.control}
                name='target_amount'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Performance Target (USD)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min='0'
                        step='0.01'
                        placeholder='0 = no limit'
                        {...field}
                        onChange={(e) =>
                          field.onChange(parseFloat(e.target.value) || 0)
                        }
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={createForm.control}
                name='remark'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Remark')}</FormLabel>
                    <FormControl>
                      <Input placeholder={t('Optional')} {...field} />
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
              {tierPresetField}
              <FormField
                control={updateForm.control}
                name='commission_rate'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Commission Rate')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        step='0.01'
                        min='0'
                        max='1'
                        {...field}
                        onChange={(e) =>
                          field.onChange(parseFloat(e.target.value) || 0)
                        }
                      />
                    </FormControl>
                    <FormDescription>
                      {t('0.0 ~ 1.0 (e.g. 0.1 = 10%)')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={updateForm.control}
                name='target_amount'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Performance Target (USD)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min='0'
                        step='0.01'
                        {...field}
                        onChange={(e) =>
                          field.onChange(parseFloat(e.target.value) || 0)
                        }
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
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
                      <Input placeholder={t('Optional')} {...field} />
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

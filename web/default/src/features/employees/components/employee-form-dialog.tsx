import { useState, useEffect, useRef } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { useQuery } from '@tanstack/react-query'
import { cn } from '@/lib/utils'
import { searchUsers } from '@/features/users/api'
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
import { createEmployee, updateEmployee } from '../api'
import type { EmployeeProfile } from '../types'

const createSchema = z.object({
  user_id: z.number({ required_error: 'Required' }).positive('Must be positive'),
  commission_rate: z
    .number()
    .min(0, 'Min 0')
    .max(1, 'Max 1 (100%)'),
  target_quota: z.number().min(0).optional(),
  remark: z.string().max(255).optional(),
})

const updateSchema = z.object({
  commission_rate: z
    .number()
    .min(0, 'Min 0')
    .max(1, 'Max 1 (100%)'),
  target_quota: z.number().min(0),
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

// 从用户列表搜索并选择用户（服务端搜索，避免手填 ID）
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

  const { data, isFetching } = useQuery({
    queryKey: ['employee-user-search', debounced],
    queryFn: () => searchUsers({ keyword: debounced, page_size: 20 }),
    enabled: open,
  })
  const users = data?.data?.items ?? []

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
          {isFetching ? (
            <div className='text-muted-foreground px-2 py-6 text-center text-sm'>
              {t('Loading...')}
            </div>
          ) : users.length === 0 ? (
            <div className='text-muted-foreground px-2 py-6 text-center text-sm'>
              {t('No users found')}
            </div>
          ) : (
            <ul className='max-h-[240px] overflow-y-auto p-1'>
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
                    {u.email ? ` · ${u.email}` : ''}
                  </span>
                </li>
              ))}
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

  const createForm = useForm<CreateValues>({
    resolver: zodResolver(createSchema),
    defaultValues: { commission_rate: 0.1, target_quota: 0 },
  })

  const updateForm = useForm<UpdateValues>({
    resolver: zodResolver(updateSchema),
    defaultValues: {
      commission_rate: currentRow?.commission_rate ?? 0.1,
      target_quota: Number(currentRow?.target_quota ?? 0),
      status: currentRow?.status ?? 1,
      remark: currentRow?.remark ?? '',
    },
  })

  const handleCreate = async (values: CreateValues) => {
    setIsSubmitting(true)
    try {
      const res = await createEmployee({
        user_id: values.user_id,
        commission_rate: values.commission_rate,
        target_quota: values.target_quota ?? 0,
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
      const res = await updateEmployee(currentRow.id, {
        commission_rate: values.commission_rate,
        target_quota: values.target_quota,
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
                      {t('Search and select the user to assign employee status')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
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
                name='target_quota'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Performance Target (Quota)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min='0'
                        placeholder='0 = no limit'
                        {...field}
                        onChange={(e) =>
                          field.onChange(parseInt(e.target.value) || 0)
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
                name='target_quota'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Performance Target (Quota)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min='0'
                        {...field}
                        onChange={(e) =>
                          field.onChange(parseInt(e.target.value) || 0)
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
                      onValueChange={(v) => field.onChange(parseInt(v))}
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

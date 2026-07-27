import { useEffect, useMemo, useRef, useState, type UIEvent } from 'react'
import {
  useInfiniteQuery,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import {
  type ColumnDef,
  type PaginationState,
  type Row,
  type SortingState,
  flexRender,
  getCoreRowModel,
  getPaginationRowModel,
  useReactTable,
} from '@tanstack/react-table'
import {
  ChevronDown,
  CirclePlus,
  Download,
  Info,
  List,
  Pencil,
  PlusIcon,
  RotateCcw,
  Search,
  Trash2,
  UserMinus,
  UserRoundPlus,
  Users,
  X,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { getCurrencyDisplay } from '@/lib/currency'
import { cn } from '@/lib/utils'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ButtonGroup } from '@/components/ui/button-group'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Progress } from '@/components/ui/progress'
import { TableCell, TableRow } from '@/components/ui/table'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import {
  DISABLED_ROW_DESKTOP,
  DISABLED_ROW_MOBILE,
  DataTableColumnHeader,
  DataTablePage,
} from '@/components/data-table'
import { SectionPageLayout } from '@/components/layout'
import { BusinessAmount } from '@/features/business/amount-display'
import {
  formatBusinessAmount,
  formatBusinessTargetAmount,
} from '@/features/business/format'
import { CompactDateTimeRangePicker } from '@/features/usage-logs/components/compact-date-time-range-picker'
import { searchUsers } from '@/features/users/api'
import type { User } from '@/features/users/types'
import {
  assignCustomerToEmployee,
  createEmployeeTier,
  deleteEmployee,
  deleteEmployeeTier,
  getCommissionCalendarStats,
  getCommissionMonthlyExport,
  getEmployeeCustomers,
  getCommissionChannelOptions,
  getCommissionLogs,
  getEmployeeTiers,
  getEmployeeTiersPage,
  getEmployees,
  unassignCustomerFromEmployee,
  updateEmployeeTier,
} from './api'
import {
  CommissionCalendarSection,
  currentMonthValue,
  monthValueToCalendarRange,
} from './components/commission-financial-calendar'
import { EmployeeFormDialog } from './components/employee-form-dialog'
import { PerformanceAdjustDialog } from './components/performance-adjust-dialog'
import { TierResetSettingsCard } from './components/tier-reset-settings-card'
import { UsageLogIdHover } from './components/usage-log-id-hover'
import {
  getEmployeeTierGroupBadgeClass,
  getEmployeeTierGroupDotClass,
  getEmployeeTierLevelBadgeClass,
} from './lib/tiers'
import type {
  CommissionLog,
  EmployeeCustomer,
  EmployeeProfile,
  EmployeeTier,
} from './types'

const ASSIGN_USER_PICKER_PAGE_SIZE = 20
const EMPLOYEE_MONTHLY_SELECTOR_PAGE_SIZE = 20
const EMPLOYEE_CUSTOMERS_PAGE_SIZE = 8
const COMMISSION_CHANNEL_FILTER_PAGE_SIZE = 30
const DEFAULT_TIER_GROUP = '通用'

function getCustomerLabel(
  user: Pick<User, 'username' | 'display_name'> | EmployeeCustomer
) {
  return `${user.username}${user.display_name ? ` (${user.display_name})` : ''}`
}

function getEmployeeMonthlyAccountLabel(employee: EmployeeProfile) {
  return employee.username || `#${employee.user_id}`
}

function getEmployeeMonthlyOptionLabel(employee: EmployeeProfile) {
  const account = getEmployeeMonthlyAccountLabel(employee)
  return employee.remark ? `${employee.remark} / ${account}` : account
}

function AssignUserPicker({
  selectedUsers,
  onToggle,
  employeeUserId,
}: {
  selectedUsers: User[]
  onToggle: (user: User) => void
  employeeUserId?: number
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [keyword, setKeyword] = useState('')
  const [debounced, setDebounced] = useState('')
  const containerRef = useRef<HTMLDivElement>(null)
  const selectedIds = useMemo(
    () => new Set(selectedUsers.map((user) => user.id)),
    [selectedUsers]
  )

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
      queryKey: ['assign-customer-user-search', debounced],
      queryFn: ({ pageParam }) =>
        searchUsers({
          keyword: debounced,
          exclude_employee: true,
          exclude_admin: true,
          p: Number(pageParam),
          page_size: ASSIGN_USER_PICKER_PAGE_SIZE,
        }),
      initialPageParam: 1,
      getNextPageParam: (lastPage) => {
        const page = lastPage.data?.page ?? 1
        const pageSize =
          lastPage.data?.page_size ?? ASSIGN_USER_PICKER_PAGE_SIZE
        const total = lastPage.data?.total ?? 0
        return page * pageSize < total ? page + 1 : undefined
      },
      enabled: open,
    })
  const users = useMemo(() => {
    const all = data?.pages.flatMap((page) => page.data?.items ?? []) ?? []
    return [...all].sort((a, b) => {
      const aSelected = selectedIds.has(a.id)
      const bSelected = selectedIds.has(b.id)
      if (aSelected !== bSelected) return aSelected ? -1 : 1
      if (a.is_assigned_customer === b.is_assigned_customer) return 0
      return a.is_assigned_customer ? 1 : -1
    })
  }, [data, selectedIds])
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
        value={keyword}
        onChange={(e) => {
          setKeyword(e.target.value)
          if (!open) setOpen(true)
        }}
        onFocus={() => setOpen(true)}
        className={keyword ? 'pr-9' : undefined}
      />
      {keyword ? (
        <Button
          type='button'
          size='icon'
          variant='ghost'
          className='absolute top-1/2 right-1 size-7 -translate-y-1/2'
          title={t('Clear selection')}
          aria-label={t('Clear selection')}
          onMouseDown={(event) => event.preventDefault()}
          onClick={() => {
            setKeyword('')
            setDebounced('')
            setOpen(true)
          }}
        >
          <X className='size-4' />
        </Button>
      ) : null}
      {open && (
        <div className='bg-popover text-popover-foreground absolute top-full right-0 left-0 z-[120] mt-2 overflow-hidden rounded-lg border shadow-lg'>
          <ul
            onScroll={handleListScroll}
            className='max-h-56 overflow-y-auto p-1'
          >
            {isInitialFetching ? (
              <li className='text-muted-foreground px-3 py-8 text-center text-sm'>
                {t('Loading...')}
              </li>
            ) : users.length === 0 ? (
              <li className='text-muted-foreground px-3 py-8 text-center text-sm'>
                {t('No users found')}
              </li>
            ) : (
              <>
                {users.map((user) => {
                  const isCurrentEmployeeCustomer =
                    user.is_assigned_customer &&
                    user.assigned_employee_user_id === employeeUserId
                  const isSelected = selectedIds.has(user.id)
                  const canSelect = !isCurrentEmployeeCustomer
                  return (
                    <li
                      key={user.id}
                      role='option'
                      aria-selected={isSelected}
                      className={cn(
                        'hover:bg-accent aria-selected:bg-accent/80 cursor-pointer rounded-md px-3 py-2 text-sm transition-colors',
                        !canSelect && 'cursor-default',
                        user.is_assigned_customer &&
                          'bg-amber-50 hover:bg-amber-100 dark:bg-amber-950/30 dark:hover:bg-amber-950/50'
                      )}
                      onMouseDown={(e) => {
                        e.preventDefault()
                        if (!canSelect) return
                        onToggle(user)
                        setOpen(true)
                      }}
                    >
                      <div className='flex min-w-0 items-center justify-between gap-3'>
                        <span className='truncate font-medium'>
                          {getCustomerLabel(user)}
                        </span>
                        <div className='flex shrink-0 items-center gap-1.5'>
                          {isSelected ? (
                            <span className='border-primary/50 text-primary rounded border px-1 py-0.5 text-[10px] leading-none'>
                              {t('Selected')}
                            </span>
                          ) : null}
                          {user.is_assigned_customer ? (
                            <span className='rounded border border-amber-200 px-1 py-0.5 text-[10px] leading-none text-amber-600 dark:border-amber-800 dark:text-amber-400'>
                              {user.assigned_employee_user_id === employeeUserId
                                ? t('Current employee')
                                : t('Assigned')}
                            </span>
                          ) : null}
                          <span className='text-muted-foreground text-xs'>
                            #{user.id}
                          </span>
                        </div>
                      </div>
                      {user.is_assigned_customer ? (
                        <div className='mt-0.5 truncate text-xs text-amber-600 dark:text-amber-400'>
                          {user.assigned_employee_name
                            ? `${t('Assigned to')}: ${user.assigned_employee_name}`
                            : t('Assigned to another employee')}
                        </div>
                      ) : user.email ? (
                        <div className='text-muted-foreground mt-0.5 truncate text-xs'>
                          {user.email}
                        </div>
                      ) : null}
                    </li>
                  )
                })}
                {isFetchingNextPage && (
                  <li className='text-muted-foreground px-3 py-3 text-center text-sm'>
                    {t('Loading...')}
                  </li>
                )}
              </>
            )}
          </ul>
        </div>
      )}
    </div>
  )
}

function getCustomerUserId(customer: User | EmployeeCustomer) {
  return 'customer_user_id' in customer
    ? customer.customer_user_id
    : customer.id
}

function RemoveCustomersDialog({
  open,
  employee,
  onOpenChange,
  onSuccess,
}: {
  open: boolean
  employee?: EmployeeProfile
  onOpenChange: (open: boolean) => void
  onSuccess: () => void
}) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [keyword, setKeyword] = useState('')
  const [debounced, setDebounced] = useState('')
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: EMPLOYEE_CUSTOMERS_PAGE_SIZE,
  })
  const [customerToRemove, setCustomerToRemove] = useState<
    EmployeeCustomer | undefined
  >()
  const [removing, setRemoving] = useState(false)

  useEffect(() => {
    const timer = setTimeout(() => setDebounced(keyword.trim()), 300)
    return () => clearTimeout(timer)
  }, [keyword])

  useEffect(() => {
    if (!open) {
      setKeyword('')
      setDebounced('')
      setPagination((current) => ({ ...current, pageIndex: 0 }))
      setCustomerToRemove(undefined)
    }
  }, [open])

  useEffect(() => {
    setPagination((current) => ({ ...current, pageIndex: 0 }))
  }, [debounced, employee?.id])

  const { data, isLoading, isFetching, refetch } = useQuery({
    queryKey: [
      'employee-current-customers',
      employee?.id,
      debounced,
      pagination,
    ],
    queryFn: () =>
      getEmployeeCustomers(employee!.id, {
        page: pagination.pageIndex + 1,
        page_size: pagination.pageSize,
        keyword: debounced,
      }),
    enabled: open && Boolean(employee?.id),
  })

  const customers = data?.data?.items ?? []
  const total = data?.data?.total ?? 0
  const pageCount = Math.max(1, Math.ceil(total / pagination.pageSize))
  const pageStart =
    total === 0 ? 0 : pagination.pageIndex * pagination.pageSize + 1
  const pageEnd = Math.min(
    total,
    (pagination.pageIndex + 1) * pagination.pageSize
  )
  const employeeLabel =
    employee?.username ||
    employee?.display_name ||
    (employee?.user_id ? `#${employee.user_id}` : '-')

  const confirmRemoveCustomer = async () => {
    if (!employee || !customerToRemove) return
    const customerUserId = getCustomerUserId(customerToRemove)
    setRemoving(true)
    try {
      const res = await unassignCustomerFromEmployee(
        employee.id,
        customerUserId
      )
      if (!res.success) throw new Error(res.message)
      toast.success(t('Customer removed from employee'))
      setCustomerToRemove(undefined)
      qc.invalidateQueries({ queryKey: ['assign-customer-user-search'] })
      onSuccess()
      if (customers.length === 1 && pagination.pageIndex > 0) {
        setPagination((current) => ({
          ...current,
          pageIndex: current.pageIndex - 1,
        }))
      } else {
        void refetch()
      }
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Operation failed')
      )
    } finally {
      setRemoving(false)
    }
  }

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent
          className='flex h-[86vh] max-h-[720px] flex-col overflow-hidden sm:max-w-[760px]'
          initialFocus={false}
        >
          <DialogHeader className='shrink-0'>
            <DialogTitle>{t('Employee Customer List')}</DialogTitle>
            <DialogDescription>
              {t(
                'View customers assigned to this employee. You can remove a customer from this list when needed.'
              )}
            </DialogDescription>
          </DialogHeader>
          <div className='flex min-h-0 flex-1 flex-col gap-4'>
            <div className='bg-muted/30 rounded-lg border px-3 py-2'>
              <div className='text-muted-foreground text-xs'>
                {t('Employee')}
              </div>
              <div className='mt-1 flex min-w-0 items-center gap-2'>
                <span className='truncate font-medium'>{employeeLabel}</span>
                {employee?.display_name && employee.username ? (
                  <span className='text-muted-foreground truncate text-sm'>
                    {employee.display_name}
                  </span>
                ) : null}
                <Badge variant='outline' className='ml-auto shrink-0'>
                  #{employee?.user_id}
                </Badge>
              </div>
            </div>

            <div className='flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between'>
              <div className='relative min-w-0 flex-1'>
                <Search className='text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2' />
                <Input
                  value={keyword}
                  onChange={(event) => setKeyword(event.target.value)}
                  placeholder={t('Search current customers')}
                  className={cn('pl-8', keyword && 'pr-8')}
                />
                {keyword ? (
                  <Button
                    type='button'
                    size='icon'
                    variant='ghost'
                    className='absolute top-1/2 right-1 size-7 -translate-y-1/2'
                    aria-label={t('Clear')}
                    onClick={() => {
                      setKeyword('')
                      setDebounced('')
                    }}
                  >
                    <X className='size-4' />
                  </Button>
                ) : null}
              </div>
              <div className='flex shrink-0 items-center gap-2'>
                <Badge variant='outline'>
                  {t('{{count}} customers', { count: total })}
                </Badge>
                <Button
                  type='button'
                  size='icon'
                  variant='outline'
                  className='size-9'
                  title={t('Refresh')}
                  aria-label={t('Refresh')}
                  onClick={() => void refetch()}
                >
                  <RotateCcw
                    className={cn('size-4', isFetching && 'animate-spin')}
                  />
                </Button>
              </div>
            </div>

            <div className='flex min-h-0 flex-1 flex-col overflow-hidden rounded-lg border'>
              <div className='bg-muted/30 text-muted-foreground border-border/50 grid grid-cols-[minmax(0,1fr)_96px_96px_96px] gap-4 border-b px-4 py-2 text-xs font-medium max-sm:hidden'>
                <span>{t('Customer')}</span>
                <span className='text-right'>{t('Used quota')}</span>
                <span className='text-right'>{t('Commission')}</span>
                <span className='text-right'>{t('Action')}</span>
              </div>
              <div className='divide-border/40 min-h-0 flex-1 divide-y overflow-y-auto'>
                {isLoading ? (
                  <div className='text-muted-foreground flex h-full min-h-[260px] items-center justify-center text-sm'>
                    {t('Loading...')}
                  </div>
                ) : customers.length === 0 ? (
                  <div className='text-muted-foreground flex h-full min-h-[260px] items-center justify-center px-6 text-center text-sm'>
                    {debounced
                      ? t('No current customers match your search')
                      : t('This employee has no assigned customers')}
                  </div>
                ) : (
                  customers.map((customer) => {
                    const customerUserId = getCustomerUserId(customer)
                    return (
                      <div
                        key={customerUserId}
                        className='hover:bg-muted/20 grid min-h-14 grid-cols-[minmax(0,1fr)_96px_96px_96px] items-center gap-4 px-4 py-2.5 text-sm transition-colors max-sm:grid-cols-1 max-sm:gap-2 max-sm:py-3'
                      >
                        <div className='min-w-0'>
                          <div className='flex min-w-0 items-center gap-2'>
                            <span className='truncate font-medium'>
                              {customer.username || '-'}
                              {customer.remark ? ` (${customer.remark})` : ''}
                            </span>
                            {customer.customer_employee_status === 2 ? (
                              <Badge
                                variant='secondary'
                                className='h-5 shrink-0 rounded-sm px-1.5 text-[10px] font-normal'
                              >
                                {t('Employee status disabled')}
                              </Badge>
                            ) : null}
                          </div>
                          <div className='text-muted-foreground truncate text-xs'>
                            #{customerUserId}
                            {customer.email ? ` / ${customer.email}` : ''}
                          </div>
                        </div>
                        <div className='text-right font-medium tabular-nums max-sm:flex max-sm:justify-between max-sm:text-left'>
                          <span className='text-muted-foreground hidden text-xs max-sm:inline'>
                            {t('Used quota')}
                          </span>
                          {formatBusinessAmount(customer.used_quota ?? 0)}
                        </div>
                        <div className='text-right font-medium tabular-nums max-sm:flex max-sm:justify-between max-sm:text-left'>
                          <span className='text-muted-foreground hidden text-xs max-sm:inline'>
                            {t('Commission')}
                          </span>
                          {formatBusinessAmount(customer.commission_quota ?? 0)}
                        </div>
                        <div className='flex justify-end'>
                          <Button
                            type='button'
                            size='sm'
                            variant='ghost'
                            className='text-destructive hover:bg-destructive/10 hover:text-destructive h-8 px-2'
                            onClick={() => setCustomerToRemove(customer)}
                          >
                            <UserMinus className='size-4' />
                            {t('Remove')}
                          </Button>
                        </div>
                      </div>
                    )
                  })
                )}
              </div>
            </div>

            <div className='flex shrink-0 flex-col gap-2 pt-3 text-sm sm:flex-row sm:items-center sm:justify-between'>
              <span className='text-muted-foreground'>
                {t('Showing {{start}}-{{end}} of {{total}} customers', {
                  start: pageStart,
                  end: pageEnd,
                  total,
                })}
              </span>
              <div className='flex items-center justify-end gap-2'>
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  disabled={pagination.pageIndex === 0 || isFetching}
                  onClick={() =>
                    setPagination((current) => ({
                      ...current,
                      pageIndex: Math.max(0, current.pageIndex - 1),
                    }))
                  }
                >
                  {t('Previous')}
                </Button>
                <Badge variant='secondary'>
                  {t('Page {{current}} of {{total}}', {
                    current: pagination.pageIndex + 1,
                    total: pageCount,
                  })}
                </Badge>
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  disabled={pagination.pageIndex + 1 >= pageCount || isFetching}
                  onClick={() =>
                    setPagination((current) => ({
                      ...current,
                      pageIndex: Math.min(pageCount - 1, current.pageIndex + 1),
                    }))
                  }
                >
                  {t('Next')}
                </Button>
              </div>
            </div>
          </div>
          <DialogFooter className='shrink-0'>
            <Button variant='outline' onClick={() => onOpenChange(false)}>
              {t('Close')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <AlertDialog
        open={Boolean(customerToRemove)}
        onOpenChange={(nextOpen) => {
          if (!nextOpen && !removing) setCustomerToRemove(undefined)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Confirm customer removal')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                'Removing this customer will stop future commission from being counted for this employee.'
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          {customerToRemove ? (
            <div className='bg-muted/30 rounded-lg border px-3 py-2 text-sm'>
              <div className='flex items-center justify-between gap-3'>
                <span className='text-muted-foreground'>{t('Customer')}</span>
                <span className='min-w-0 truncate font-medium'>
                  {getCustomerLabel(customerToRemove)}
                </span>
              </div>
              <div className='mt-1 flex items-center justify-between gap-3'>
                <span className='text-muted-foreground'>{t('User ID')}</span>
                <span className='font-medium'>
                  #{getCustomerUserId(customerToRemove)}
                </span>
              </div>
              <div className='mt-1 flex items-center justify-between gap-3'>
                <span className='text-muted-foreground'>
                  {t('Target employee')}
                </span>
                <span className='min-w-0 truncate font-medium'>
                  {employeeLabel}
                </span>
              </div>
            </div>
          ) : null}
          <AlertDialogFooter>
            <AlertDialogCancel disabled={removing}>
              {t('Cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              variant='destructive'
              onClick={confirmRemoveCustomer}
              disabled={removing}
            >
              {removing ? t('Saving...') : t('Remove customer')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

function AssignCustomerDialog({
  open,
  employee,
  onOpenChange,
  onSuccess,
}: {
  open: boolean
  employee?: EmployeeProfile
  onOpenChange: (open: boolean) => void
  onSuccess: () => void
}) {
  const { t } = useTranslation()
  const [selectedCustomers, setSelectedCustomers] = useState<User[]>([])
  const [confirmReassignOpen, setConfirmReassignOpen] = useState(false)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!open) {
      setSelectedCustomers([])
      setConfirmReassignOpen(false)
    }
  }, [open])

  const employeeLabel =
    employee?.username ||
    employee?.display_name ||
    (employee?.user_id ? `#${employee.user_id}` : '-')
  const selectedCount = selectedCustomers.length
  const reassignmentCustomers = selectedCustomers.filter(
    (customer) =>
      customer.is_assigned_customer &&
      customer.assigned_employee_user_id !== employee?.user_id
  )
  const isReassignment = reassignmentCustomers.length > 0

  const toggleCustomer = (customer: User) => {
    setSelectedCustomers((prev) => {
      if (prev.some((item) => item.id === customer.id)) {
        return prev.filter((item) => item.id !== customer.id)
      }
      return [...prev, customer]
    })
  }

  const removeSelectedCustomer = (customerId: number) => {
    setSelectedCustomers((prev) =>
      prev.filter((customer) => customer.id !== customerId)
    )
  }

  const submit = async () => {
    if (!employee || selectedCount === 0) {
      toast.error(t('Please select at least one customer'))
      return
    }
    if (isReassignment && !confirmReassignOpen) {
      setConfirmReassignOpen(true)
      return
    }
    setSaving(true)
    try {
      for (const customer of selectedCustomers) {
        const res = await assignCustomerToEmployee(employee.id, customer.id)
        if (!res.success) throw new Error(res.message)
      }
      toast.success(
        isReassignment
          ? t('Reassigned {{count}} customer(s) successfully', {
              count: selectedCount,
            })
          : t('Assigned {{count}} customer(s) successfully', {
              count: selectedCount,
            })
      )
      onOpenChange(false)
      onSuccess()
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('Operation failed'))
    } finally {
      setSaving(false)
    }
  }

  const submitLabel = isReassignment
    ? t('Reassign selected customers')
    : t('Assign selected customers')

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent
          className='h-[86vh] max-h-[620px] overflow-visible sm:max-w-[560px]'
          initialFocus={false}
        >
          <DialogHeader>
            <DialogTitle>{t('Assign Customer')}</DialogTitle>
            <DialogDescription>
              {t(
                "Assign a customer to an employee so future consumption is counted toward that employee's commission."
              )}
            </DialogDescription>
          </DialogHeader>
          <div className='space-y-4'>
            <div className='bg-muted/30 rounded-lg border px-3 py-2'>
              <div className='text-muted-foreground text-xs'>
                {t('Employee')}
              </div>
              <div className='mt-1 flex min-w-0 items-center gap-2'>
                <span className='truncate font-medium'>
                  {employee?.username || `#${employee?.user_id}`}
                </span>
                {employee?.display_name ? (
                  <span className='text-muted-foreground truncate text-sm'>
                    {employee.display_name}
                  </span>
                ) : null}
                <Badge variant='outline' className='ml-auto shrink-0'>
                  #{employee?.user_id}
                </Badge>
              </div>
            </div>
            <div className='space-y-2'>
              <label className='text-sm font-medium'>
                {t('Add customers')}
              </label>
              <AssignUserPicker
                selectedUsers={selectedCustomers}
                onToggle={toggleCustomer}
                employeeUserId={employee?.user_id}
              />
            </div>
            {selectedCustomers.length > 0 ? (
              <div
                className={cn(
                  'rounded-lg border px-3 py-2 text-sm',
                  isReassignment
                    ? 'border-amber-200 bg-amber-50 text-amber-600 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-400'
                    : 'bg-muted/30'
                )}
              >
                <div className='grid gap-1.5'>
                  <div className='flex items-center justify-between gap-3'>
                    <span className='text-muted-foreground'>
                      {t('Selected customers')}
                    </span>
                    <Badge variant='outline' className='shrink-0'>
                      {selectedCount}
                    </Badge>
                  </div>
                  <div className='flex items-center justify-between gap-3'>
                    <span className='text-muted-foreground'>
                      {t('Target employee')}
                    </span>
                    <span className='min-w-0 truncate font-medium'>
                      {employeeLabel}
                    </span>
                  </div>
                  {isReassignment ? (
                    <div className='flex items-center justify-between gap-3'>
                      <span className='text-muted-foreground'>
                        {t('Customers to reassign')}
                      </span>
                      <span className='min-w-0 truncate font-medium'>
                        {reassignmentCustomers.length}
                      </span>
                    </div>
                  ) : null}
                </div>
                <div className='mt-3 flex flex-wrap gap-2'>
                  {selectedCustomers.map((customer) => (
                    <Badge
                      key={customer.id}
                      variant='secondary'
                      className='max-w-full gap-1 pr-1'
                    >
                      <span className='max-w-[220px] truncate'>
                        {getCustomerLabel(customer)} #{customer.id}
                      </span>
                      <Button
                        type='button'
                        size='icon'
                        variant='ghost'
                        className='size-5'
                        aria-label={t('Remove')}
                        onClick={() => removeSelectedCustomer(customer.id)}
                      >
                        <X className='size-3' />
                      </Button>
                    </Badge>
                  ))}
                </div>
                {isReassignment ? (
                  <p className='mt-2 text-xs'>
                    {t(
                      'Some selected customers are already assigned to another employee. Reassigning will update future commission ownership.'
                    )}
                  </p>
                ) : null}
              </div>
            ) : null}
            <p className='text-muted-foreground text-xs'>
              {t(
                "This will set the user's inviter to this employee, so their consumption generates commission."
              )}
            </p>
          </div>
          <DialogFooter>
            <Button variant='outline' onClick={() => onOpenChange(false)}>
              {t('Cancel')}
            </Button>
            <Button disabled={saving || selectedCount === 0} onClick={submit}>
              {saving ? t('Saving...') : submitLabel}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <AlertDialog
        open={confirmReassignOpen}
        onOpenChange={setConfirmReassignOpen}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Confirm reassignment')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                'Some selected customers are already assigned to another employee. Reassigning will update future commission ownership.'
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <div className='bg-muted/30 rounded-lg border px-3 py-2 text-sm'>
            <div className='flex items-center justify-between gap-3'>
              <span className='text-muted-foreground'>
                {t('Selected customers')}
              </span>
              <span className='min-w-0 truncate font-medium'>
                {selectedCount}
              </span>
            </div>
            <div className='mt-1 flex items-center justify-between gap-3'>
              <span className='text-muted-foreground'>
                {t('Customers to reassign')}
              </span>
              <span className='min-w-0 truncate font-medium'>
                {reassignmentCustomers.length}
              </span>
            </div>
            <div className='mt-1 flex items-center justify-between gap-3'>
              <span className='text-muted-foreground'>
                {t('Target employee')}
              </span>
              <span className='min-w-0 truncate font-medium'>
                {employeeLabel}
              </span>
            </div>
            <div className='mt-3 flex flex-wrap gap-2'>
              {selectedCustomers.slice(0, 6).map((customer) => (
                <Badge key={customer.id} variant='secondary'>
                  {getCustomerLabel(customer)} #{customer.id}
                </Badge>
              ))}
              {selectedCustomers.length > 6 ? (
                <Badge variant='outline'>+{selectedCustomers.length - 6}</Badge>
              ) : null}
            </div>
          </div>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('Cancel')}</AlertDialogCancel>
            <AlertDialogAction onClick={submit} disabled={saving}>
              {saving ? t('Saving...') : t('Reassign selected customers')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

function formatTs(ts: number) {
  if (!ts) return '-'
  return new Date(ts * 1000).toLocaleString()
}

function formatPercent(value: number | undefined) {
  return `${(Number(value || 0) * 100).toFixed(1)}%`
}

function formatTargetAmount(value: number | undefined) {
  return formatBusinessTargetAmount(value)
}

function AmountText({ value }: { value?: number }) {
  return (
    <BusinessAmount
      value={value}
      positiveClassName='text-emerald-600 dark:text-emerald-400'
    />
  )
}

function getPerformanceTargetUsd(row: EmployeeProfile) {
  const nextTierTargetUsd = Number(row.next_tier_threshold_usd || 0)
  if (Number.isFinite(nextTierTargetUsd) && nextTierTargetUsd > 0) {
    return nextTierTargetUsd
  }
  const profileTargetUsd = Number(row.target_amount || 0)
  return Number.isFinite(profileTargetUsd) && profileTargetUsd > 0
    ? profileTargetUsd
    : 0
}

function PerformanceProgressCell({ row }: { row: EmployeeProfile }) {
  const { t } = useTranslation()
  const currentQuota = Number(row.current_performance_quota || 0)
  const targetUsd = getPerformanceTargetUsd(row)

  if (!Number.isFinite(targetUsd) || targetUsd <= 0) {
    return (
      <div className='min-w-[110px]'>{formatBusinessAmount(currentQuota)}</div>
    )
  }

  const { config } = getCurrencyDisplay()
  const targetQuota = targetUsd * config.quotaPerUnit
  const rawPercent = targetQuota > 0 ? (currentQuota / targetQuota) * 100 : 0
  const progressPercent = Math.max(0, Math.min(100, rawPercent))
  const percentText = `${Number.isFinite(rawPercent) ? rawPercent.toFixed(0) : '0'}%`

  return (
    <div className='min-w-[130px] space-y-1'>
      <div className='flex items-center justify-between gap-2 text-xs'>
        <span className='font-medium tabular-nums'>
          {formatBusinessAmount(currentQuota)}
        </span>
        <span
          className={cn(
            'tabular-nums',
            rawPercent >= 100
              ? 'font-semibold text-emerald-600 dark:text-emerald-400'
              : 'text-muted-foreground'
          )}
        >
          {percentText}
        </span>
      </div>
      <Progress
        value={progressPercent}
        className={cn(
          'h-1.5',
          rawPercent >= 100
            ? '[&_[data-slot=progress-indicator]]:bg-emerald-500'
            : '[&_[data-slot=progress-indicator]]:bg-primary'
        )}
      />
      <div className='text-muted-foreground text-xs'>
        {row.next_tier_level
          ? `${t('Tier {{level}}', { level: row.next_tier_level })}: `
          : `${t('Target')}: `}
        {formatTargetAmount(targetUsd)}
      </div>
    </div>
  )
}

function ColumnHeaderWithHint({
  title,
  hint,
}: {
  title: React.ReactNode
  hint: React.ReactNode
}) {
  return (
    <span className='inline-flex items-center gap-1'>
      {title}
      <TooltipProvider delay={0}>
        <Tooltip>
          <TooltipTrigger
            render={
              <Info className='text-muted-foreground size-3.5 shrink-0 cursor-help' />
            }
          />
          <TooltipContent className='max-w-[260px]'>{hint}</TooltipContent>
        </Tooltip>
      </TooltipProvider>
    </span>
  )
}

function StatusBadge({ status }: { status: number }) {
  const { t } = useTranslation()
  return status === 1 ? (
    <Badge variant='default'>{t('Enabled')}</Badge>
  ) : (
    <Badge variant='secondary'>{t('Disabled')}</Badge>
  )
}

function EmployeeRowActions({
  row,
  onEdit,
  onDelete,
  onAssign,
  onViewCustomers,
  onAddPerformance,
}: {
  row: EmployeeProfile
  onEdit: (row: EmployeeProfile) => void
  onDelete: (row: EmployeeProfile) => void
  onAssign: (row: EmployeeProfile) => void
  onViewCustomers: (row: EmployeeProfile) => void
  onAddPerformance: (row: EmployeeProfile) => void
}) {
  const { t } = useTranslation()

  return (
    <div className='flex flex-nowrap items-center justify-end gap-1'>
      {row.status === 1 ? (
        <Button
          type='button'
          size='icon'
          variant='ghost'
          className='size-8'
          title={t('Adjust Performance')}
          aria-label={t('Adjust Performance')}
          onClick={() => onAddPerformance(row)}
        >
          <CirclePlus className='size-4' />
        </Button>
      ) : null}
      {row.status === 1 ? (
        <Button
          type='button'
          size='icon'
          variant='ghost'
          className='size-8'
          title={t('Assign Customer')}
          aria-label={t('Assign Customer')}
          onClick={() => onAssign(row)}
        >
          <UserRoundPlus className='size-4' />
        </Button>
      ) : null}
      <Button
        type='button'
        size='icon'
        variant='ghost'
        className='size-8'
        title={t('View Customers')}
        aria-label={t('View Customers')}
        onClick={() => onViewCustomers(row)}
      >
        <Users className='size-4' />
      </Button>
      <Button
        type='button'
        size='icon'
        variant='ghost'
        className='size-8'
        title={t('Edit Employee')}
        aria-label={t('Edit Employee')}
        onClick={() => onEdit(row)}
      >
        <Pencil className='size-4' />
      </Button>
      {row.status === 1 ? (
        <Button
          type='button'
          size='icon'
          variant='ghost'
          className='text-destructive hover:text-destructive size-8'
          title={t('Disable Employee')}
          aria-label={t('Disable Employee')}
          onClick={() => onDelete(row)}
        >
          <Trash2 className='size-4' />
        </Button>
      ) : null}
    </div>
  )
}

function useEmployeesColumns({
  onEdit,
  onDelete,
  onAssign,
  onViewCustomers,
  onAddPerformance,
  expandedTotals,
  onToggleTotals,
}: {
  onEdit: (row: EmployeeProfile) => void
  onDelete: (row: EmployeeProfile) => void
  onAssign: (row: EmployeeProfile) => void
  onViewCustomers: (row: EmployeeProfile) => void
  onAddPerformance: (row: EmployeeProfile) => void
  expandedTotals: Record<number, boolean>
  onToggleTotals: (row: EmployeeProfile) => void
}) {
  const { t } = useTranslation()

  return useMemo(
    (): ColumnDef<EmployeeProfile>[] => [
      {
        id: 'expand_totals',
        enableSorting: false,
        meta: { mobileHidden: true },
        header: () => null,
        cell: ({ row }) => {
          const expanded = Boolean(expandedTotals[row.original.id])
          return (
            <Button
              type='button'
              size='icon'
              variant='ghost'
              className='size-8'
              aria-expanded={expanded}
              aria-label={expanded ? t('Hide Totals') : t('Show Totals')}
              title={expanded ? t('Hide Totals') : t('Show Totals')}
              onClick={() => onToggleTotals(row.original)}
            >
              <ChevronDown
                className={cn(
                  'size-4 transition-transform',
                  expanded && 'rotate-180'
                )}
              />
            </Button>
          )
        },
      },
      {
        accessorKey: 'user_id',
        meta: { label: t('Employee ID') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Employee ID')} />
        ),
      },
      {
        accessorKey: 'username',
        meta: { label: t('Employee'), mobileTitle: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Employee')} />
        ),
        cell: ({ row }) => (
          <div className='flex flex-col'>
            <span>{row.original.username || `#${row.original.user_id}`}</span>
            {row.original.display_name ? (
              <span className='text-muted-foreground text-xs'>
                {row.original.display_name}
              </span>
            ) : null}
          </div>
        ),
      },
      {
        accessorKey: 'customer_count',
        meta: { label: t('Customer Count'), mobileBadge: true },
        header: ({ column }) => (
          <DataTableColumnHeader
            column={column}
            title={t('Customer Count')}
            className='whitespace-nowrap'
          />
        ),
        cell: ({ row }) => {
          const count = row.original.customer_count ?? 0
          return (
            <Button
              type='button'
              size='sm'
              variant='ghost'
              className='inline-flex h-7 min-w-12 flex-nowrap items-center justify-center gap-1.5 px-2 whitespace-nowrap tabular-nums'
              title={t('View Customers')}
              aria-label={t('View customers for this employee')}
              onClick={() => onViewCustomers(row.original)}
            >
              <Users className='size-3.5' />
              {count}
            </Button>
          )
        },
      },
      {
        accessorKey: 'period_consumption_quota',
        meta: { label: t('Period Customer Consumption') },
        header: ({ column }) => (
          <DataTableColumnHeader
            column={column}
            title={t('Period Customer Consumption')}
          />
        ),
        cell: ({ row }) =>
          <BusinessAmount value={row.original.period_consumption_quota ?? 0} />,
      },
      {
        accessorKey: 'period_cost_quota',
        meta: { label: t('Period Cost') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Period Cost')} />
        ),
        cell: ({ row }) =>
          <BusinessAmount value={row.original.period_cost_quota ?? 0} />,
      },
      {
        accessorKey: 'period_profit_quota',
        meta: { label: t('Period Profit') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Period Profit')} />
        ),
        cell: ({ row }) => (
          <AmountText value={row.original.period_profit_quota ?? 0} />
        ),
      },
      {
        accessorKey: 'period_commission_quota',
        meta: { label: t('Period Commission') },
        header: ({ column }) => (
          <DataTableColumnHeader
            column={column}
            title={t('Period Commission')}
          />
        ),
        cell: ({ row }) => {
          const profitQuota = row.original.period_profit_quota ?? 0
          const tierRate = row.original.current_tier_rate ?? 0
          const value = tierRate > 0 ? Math.round(profitQuota * tierRate) : (row.original.period_commission_quota ?? 0)
          return (
            <BusinessAmount
              value={value}
              positiveClassName='text-emerald-600 dark:text-emerald-400'
            />
          )
        },
      },
      {
        accessorKey: 'current_tier_level',
        meta: { label: t('Current Tier') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Current Tier')} />
        ),
        cell: ({ row }) =>
          row.original.current_tier_level ? (
            <div className='flex flex-col gap-1'>
              <Badge
                variant='outline'
                className={getEmployeeTierLevelBadgeClass(
                  row.original.current_tier_level
                )}
              >
                {t('Tier {{level}}', {
                  level: row.original.current_tier_level,
                })}
              </Badge>
              <div className='flex flex-wrap items-center gap-1.5'>
                <span className='text-muted-foreground text-xs'>
                  {formatPercent(row.original.current_tier_rate)}
                </span>
                {row.original.current_tier_group ? (
                  <Badge
                    variant='outline'
                    className={getEmployeeTierGroupBadgeClass(
                      row.original.current_tier_group
                    )}
                  >
                    {row.original.current_tier_group}
                  </Badge>
                ) : null}
              </div>
            </div>
          ) : (
            <span className='text-muted-foreground'>-</span>
          ),
      },
      {
        accessorKey: 'current_performance_quota',
        meta: { label: t('Current Performance') },
        header: ({ column }) => (
          <DataTableColumnHeader
            column={column}
            title={
              <ColumnHeaderWithHint
                title={t('Current Performance')}
                hint={t(
                  'Profit accumulated since the last monthly reset, used to determine progress toward the next tier. It resets to zero on the next scheduled reset and does not affect total profit or historical records.'
                )}
              />
            }
          />
        ),
        cell: ({ row }) => <PerformanceProgressCell row={row.original} />,
      },
      {
        accessorKey: 'status',
        meta: { label: t('Status'), mobileBadge: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Status')} />
        ),
        cell: ({ row }) => <StatusBadge status={row.original.status} />,
      },
      {
        accessorKey: 'remark',
        enableSorting: false,
        meta: { label: t('Remark'), mobileHidden: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Remark')} />
        ),
        cell: ({ row }) => (
          <span className='block max-w-[160px] truncate'>
            {row.original.remark || '-'}
          </span>
        ),
      },
      {
        id: 'actions',
        enableSorting: false,
        cell: ({ row }) => (
          <EmployeeRowActions
            row={row.original}
            onAssign={onAssign}
            onDelete={onDelete}
            onEdit={onEdit}
            onViewCustomers={onViewCustomers}
            onAddPerformance={onAddPerformance}
          />
        ),
      },
    ],
    [
      expandedTotals,
      onAssign,
      onDelete,
      onEdit,
      onToggleTotals,
      onViewCustomers,
      onAddPerformance,
      t,
    ]
  )
}

function EmployeeTotalsPanel({ row }: { row: EmployeeProfile }) {
  const { t } = useTranslation()
  const items = [
    {
      label: t('Customer Total Consumption'),
      value: (
        <BusinessAmount value={row.total_consumption_quota ?? 0} />
      ),
    },
    {
      label: t('Total Cost'),
      value: <BusinessAmount value={row.total_cost_quota ?? 0} />,
    },
    {
      label: t('Total Profit'),
      value: <AmountText value={row.total_profit_quota ?? 0} />,
    },
    {
      label: t('Total Commission'),
      value: (
        <BusinessAmount
          value={row.total_commission_quota ?? 0}
          positiveClassName='text-emerald-600 dark:text-emerald-400'
        />
      ),
    },
  ]

  return (
    <div className='bg-muted/30 border-t px-4 py-3'>
      <div className='mb-2 text-xs font-medium text-muted-foreground'>
        {t('Historical Totals')}
      </div>
      <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-4'>
        {items.map((item) => (
          <div key={item.label} className='min-w-0'>
            <div className='text-muted-foreground text-xs'>{item.label}</div>
            <div className='mt-1 text-sm font-medium'>{item.value}</div>
          </div>
        ))}
      </div>
    </div>
  )
}

function EmployeeTableRow({
  row,
  expanded,
  className,
}: {
  row: Row<EmployeeProfile>
  expanded: boolean
  className?: string
}) {
  return (
    <>
      <TableRow
        data-state={row.getIsSelected() && 'selected'}
        className={className}
      >
        {row.getVisibleCells().map((cell) => (
          <TableCell key={cell.id}>
            {flexRender(cell.column.columnDef.cell, cell.getContext())}
          </TableCell>
        ))}
      </TableRow>
      {expanded ? (
        <TableRow>
          <TableCell colSpan={row.getVisibleCells().length} className='p-0'>
            <EmployeeTotalsPanel row={row.original} />
          </TableCell>
        </TableRow>
      ) : null}
    </>
  )
}

function EmployeeMobileList({
  rows,
  expandedTotals,
  onToggleTotals,
  getRowClassName,
}: {
  rows: Row<EmployeeProfile>[]
  expandedTotals: Record<number, boolean>
  onToggleTotals: (row: EmployeeProfile) => void
  getRowClassName?: (row: Row<EmployeeProfile>) => string | undefined
}) {
  const { t } = useTranslation()
  return (
    <div className='divide-y overflow-hidden rounded-lg border'>
      {rows.map((row) => {
        const expanded = Boolean(expandedTotals[row.original.id])
        const cells = row
          .getVisibleCells()
          .filter(
            (cell) =>
              cell.column.id !== 'select' &&
              cell.column.id !== 'expand_totals' &&
              !(cell.column.columnDef.meta as { mobileHidden?: boolean })
                ?.mobileHidden
          )
        const titleCell = cells.find(
          (cell) =>
            (cell.column.columnDef.meta as { mobileTitle?: boolean })
              ?.mobileTitle
        )
        const badgeCell = cells.find(
          (cell) =>
            (cell.column.columnDef.meta as { mobileBadge?: boolean })
              ?.mobileBadge
        )
        const actionsCell = cells.find((cell) => cell.column.id === 'actions')
        const fieldCells = cells.filter(
          (cell) =>
            cell !== titleCell && cell !== badgeCell && cell !== actionsCell
        )

        return (
          <div
            key={row.id}
            className={cn('bg-card px-3 py-2.5', getRowClassName?.(row))}
          >
            <div className='flex items-center justify-between gap-2'>
              {titleCell ? (
                <div className='min-w-0 flex-1 overflow-hidden text-sm font-medium'>
                  {flexRender(
                    titleCell.column.columnDef.cell,
                    titleCell.getContext()
                  )}
                </div>
              ) : null}
              {badgeCell ? (
                <div className='shrink-0'>
                  {flexRender(
                    badgeCell.column.columnDef.cell,
                    badgeCell.getContext()
                  )}
                </div>
              ) : null}
            </div>
            <div className='mt-1.5 grid grid-cols-2 gap-x-3 gap-y-1.5'>
              {fieldCells.map((cell) => (
                <div key={cell.id} className='min-w-0 overflow-hidden'>
                  <div className='text-muted-foreground mb-0.5 text-[10px] leading-none select-none'>
                    {
                      (cell.column.columnDef.meta as { label?: string })
                        ?.label
                    }
                  </div>
                  <div className='min-w-0 overflow-hidden text-xs'>
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </div>
                </div>
              ))}
            </div>
            <div className='mt-2 flex items-center justify-between gap-2'>
              <Button
                type='button'
                size='sm'
                variant='ghost'
                className='h-7 px-2 text-xs'
                aria-expanded={expanded}
                onClick={() => onToggleTotals(row.original)}
              >
                <ChevronDown
                  className={cn(
                    'size-3.5 transition-transform',
                    expanded && 'rotate-180'
                  )}
                />
                {expanded ? t('Hide Totals') : t('Show Totals')}
              </Button>
              {actionsCell ? (
                <div className='-mb-0.5 flex justify-end'>
                  {flexRender(
                    actionsCell.column.columnDef.cell,
                    actionsCell.getContext()
                  )}
                </div>
              ) : null}
            </div>
            {expanded ? <EmployeeTotalsPanel row={row.original} /> : null}
          </div>
        )
      })}
    </div>
  )
}

function useCommissionLogColumns() {
  const { t } = useTranslation()

  return useMemo(
    (): ColumnDef<CommissionLog>[] => [
      {
        accessorKey: 'created_at',
        meta: { label: t('Time') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Time')} />
        ),
        cell: ({ row }) => (
          <span className='text-xs'>{formatTs(row.original.created_at)}</span>
        ),
      },
      {
        accessorKey: 'employee_user_id',
        meta: { label: t('Employee UID') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Employee UID')} />
        ),
      },
      {
        accessorKey: 'customer_user_id',
        meta: { label: t('Customer UID') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Customer UID')} />
        ),
      },
      {
        accessorKey: 'model_name',
        meta: { label: t('Model'), mobileTitle: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Model')} />
        ),
        cell: ({ row }) => (
          <span className='block max-w-[140px] truncate text-xs'>
            {row.original.model_name || '-'}
          </span>
        ),
      },
      {
        accessorKey: 'log_id',
        meta: { label: t('Log ID') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Log ID')} />
        ),
        cell: ({ row }) => <UsageLogIdHover logId={row.original.log_id} />,
      },
      {
        accessorKey: 'channel_id',
        meta: { label: t('Channel') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Channel')} />
        ),
        cell: ({ row }) => (
          <span className='block max-w-[140px] truncate text-xs'>
            {row.original.channel_name || `#${row.original.channel_id}`}
          </span>
        ),
      },
      {
        accessorKey: 'cost_ratio',
        meta: { label: t('Cost Ratio') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Cost Ratio')} />
        ),
        cell: ({ row }) => row.original.cost_ratio.toFixed(4),
      },
      {
        accessorKey: 'group_ratio',
        meta: { label: t('Group Ratio') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Group Ratio')} />
        ),
        cell: ({ row }) => {
          const belowCostRatio =
            row.original.group_ratio < row.original.cost_ratio
          return (
            <div className='flex min-w-0 items-center gap-1.5'>
              <span className='tabular-nums'>
                {row.original.group_ratio.toFixed(4)}
              </span>
              {belowCostRatio ? (
                <Badge variant='destructive'>{t('Below cost ratio')}</Badge>
              ) : null}
            </div>
          )
        },
      },
      {
        accessorKey: 'revenue_quota',
        meta: { label: t('Revenue') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Revenue')} />
        ),
        cell: ({ row }) => <BusinessAmount value={row.original.revenue_quota} />,
      },
      {
        accessorKey: 'cost_quota',
        meta: { label: t('Cost') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Cost')} />
        ),
        cell: ({ row }) => <BusinessAmount value={row.original.cost_quota} />,
      },
      {
        accessorKey: 'profit_quota',
        meta: { label: t('Profit') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Profit')} />
        ),
        cell: ({ row }) => (
          <BusinessAmount
            value={row.original.profit_quota}
            positiveClassName='text-emerald-600 dark:text-emerald-400'
            isReversal={(row.original.revenue_quota ?? 0) < 0}
          />
        ),
      },
      {
        accessorKey: 'commission_quota',
        meta: { label: t('Commission'), mobileBadge: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Commission')} />
        ),
        cell: ({ row }) => (
          <BusinessAmount
            value={row.original.commission_quota}
            positiveClassName='text-emerald-600 dark:text-emerald-400'
            isReversal={(row.original.revenue_quota ?? 0) < 0}
          />
        ),
      },
      {
        accessorKey: 'commission_rate',
        meta: { label: t('Rate') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Rate')} />
        ),
        cell: ({ row }) =>
          `${(row.original.commission_rate * 100).toFixed(1)}%`,
      },
    ],
    [t]
  )
}

function EmployeesTab() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [createOpen, setCreateOpen] = useState(false)
  const [editRow, setEditRow] = useState<EmployeeProfile | undefined>()
  const [deleteRow, setDeleteRow] = useState<EmployeeProfile | undefined>()
  const [assignRow, setAssignRow] = useState<EmployeeProfile | undefined>()
  const [performanceRow, setPerformanceRow] = useState<
    EmployeeProfile | undefined
  >()
  const [customerListRow, setCustomerListRow] = useState<
    EmployeeProfile | undefined
  >()
  const [filterForm, setFilterForm] = useState({
    userId: '',
    keyword: '',
    status: '0',
  })
  const [filters, setFilters] = useState<{
    user_id?: number
    keyword?: string
    status?: number
  }>({})
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: 10,
  })
  const [sorting, setSorting] = useState<SortingState>([])
  const [expandedTotals, setExpandedTotals] = useState<Record<number, boolean>>(
    {}
  )
  const toggleTotals = (row: EmployeeProfile) => {
    setExpandedTotals((current) => ({
      ...current,
      [row.id]: !current[row.id],
    }))
  }
  const columns = useEmployeesColumns({
    onEdit: setEditRow,
    onDelete: setDeleteRow,
    onAssign: setAssignRow,
    onViewCustomers: setCustomerListRow,
    onAddPerformance: setPerformanceRow,
    expandedTotals,
    onToggleTotals: toggleTotals,
  })

  const { data, isLoading, isFetching, refetch } = useQuery({
    queryKey: ['employees', filters, sorting, pagination],
    queryFn: () => {
      const activeSort = sorting[0]
      return getEmployees(pagination.pageIndex + 1, pagination.pageSize, {
        ...filters,
        ...(activeSort
          ? {
              sort_by: activeSort.id,
              sort_order: activeSort.desc ? 'desc' : 'asc',
            }
          : {}),
      })
    },
  })

  const table = useReactTable({
    data: data?.data?.items ?? [],
    columns,
    rowCount: data?.data?.total ?? 0,
    state: { sorting, pagination },
    onPaginationChange: setPagination,
    onSortingChange: setSorting,
    manualPagination: true,
    manualSorting: true,
    getCoreRowModel: getCoreRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
  })

  const handleDelete = async () => {
    if (!deleteRow) return
    try {
      const res = await deleteEmployee(deleteRow.id)
      if (!res.success) throw new Error(res.message)
      toast.success(t('Employee disabled'))
      qc.invalidateQueries({ queryKey: ['employees'] })
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('Operation failed'))
    } finally {
      setDeleteRow(undefined)
    }
  }

  const applyFilters = () => {
    const userId = Number(filterForm.userId)
    const status = Number(filterForm.status)
    setFilters({
      ...(Number.isFinite(userId) && userId > 0 ? { user_id: userId } : {}),
      ...(filterForm.keyword.trim()
        ? { keyword: filterForm.keyword.trim() }
        : {}),
      ...(status > 0 ? { status } : {}),
    })
    setPagination((current) => ({ ...current, pageIndex: 0 }))
  }

  const resetFilters = () => {
    setFilterForm({ userId: '', keyword: '', status: '0' })
    setFilters({})
    setPagination((current) => ({ ...current, pageIndex: 0 }))
  }

  const hasFilterDrafts =
    Boolean(filterForm.userId) ||
    Boolean(filterForm.keyword.trim()) ||
    filterForm.status !== '0'
  const hasActiveFilters =
    Boolean(filters.user_id) ||
    Boolean(filters.keyword) ||
    Boolean(filters.status) ||
    hasFilterDrafts
  const employeeTotal = data?.data?.total ?? 0
  const statusOptions = [
    { label: t('All'), value: '0' },
    { label: t('Enabled'), value: '1' },
    { label: t('Disabled'), value: '2' },
  ]

  return (
    <>
      <DataTablePage
        table={table}
        columns={columns}
        isLoading={isLoading}
        isFetching={isFetching}
        emptyTitle={t('No employees yet')}
        emptyDescription={t(
          'Create an employee to start tracking customers, performance, and commission.'
        )}
        emptyAction={
          <Button size='sm' onClick={() => setCreateOpen(true)}>
            <PlusIcon className='h-4 w-4' />
            {t('Add Employee')}
          </Button>
        }
        toolbar={
          <form
            className='flex w-full flex-col gap-2 lg:flex-row lg:items-center lg:justify-between'
            onSubmit={(event) => {
              event.preventDefault()
              applyFilters()
            }}
          >
            <div className='flex flex-wrap items-center gap-2'>
              <Input
                type='number'
                min={1}
                placeholder={t('User ID')}
                value={filterForm.userId}
                onChange={(event) =>
                  setFilterForm((form) => ({
                    ...form,
                    userId: event.target.value,
                  }))
                }
                className='w-[120px]'
              />
              <Input
                placeholder={t('Search username / display name / email')}
                value={filterForm.keyword}
                onChange={(event) =>
                  setFilterForm((form) => ({
                    ...form,
                    keyword: event.target.value,
                  }))
                }
                className='w-[240px]'
              />
              <ButtonGroup>
                {statusOptions.map((option) => (
                  <Button
                    key={option.value}
                    type='button'
                    size='sm'
                    variant={
                      filterForm.status === option.value ? 'default' : 'outline'
                    }
                    onClick={() =>
                      setFilterForm((form) => ({
                        ...form,
                        status: option.value,
                      }))
                    }
                  >
                    {option.label}
                  </Button>
                ))}
              </ButtonGroup>
              <Button size='sm' type='submit'>
                <Search className='h-4 w-4' />
                {t('Search')}
              </Button>
              <Button
                size='sm'
                type='button'
                variant='outline'
                disabled={!hasActiveFilters}
                onClick={resetFilters}
              >
                <RotateCcw className='h-4 w-4' />
                {t('Reset')}
              </Button>
              <span className='text-muted-foreground text-xs'>
                {t('{{count}} employees found', { count: employeeTotal })}
              </span>
            </div>
            <div className='flex items-center gap-2'>
              <Button
                type='button'
                size='sm'
                variant='outline'
                onClick={() => void refetch()}
                disabled={isFetching}
              >
                <RotateCcw className='mr-1 h-4 w-4' />
                {t('Refresh')}
              </Button>
              <Button
                type='button'
                size='sm'
                onClick={() => setCreateOpen(true)}
              >
                <PlusIcon className='mr-1 h-4 w-4' />
                {t('Add Employee')}
              </Button>
            </div>
          </form>
        }
        getRowClassName={(row, ctx) =>
          row.original.status === 2
            ? ctx.isMobile
              ? DISABLED_ROW_MOBILE
              : DISABLED_ROW_DESKTOP
            : undefined
        }
        renderRow={(row) => (
          <EmployeeTableRow
            key={row.id}
            row={row}
            expanded={Boolean(expandedTotals[row.original.id])}
            className={
              row.original.status === 2 ? DISABLED_ROW_DESKTOP : undefined
            }
          />
        )}
        mobile={
          !isLoading && table.getRowModel().rows.length > 0 ? (
            <EmployeeMobileList
              rows={table.getRowModel().rows}
              expandedTotals={expandedTotals}
              onToggleTotals={toggleTotals}
              getRowClassName={(row) =>
                row.original.status === 2 ? DISABLED_ROW_MOBILE : undefined
              }
            />
          ) : undefined
        }
        skeletonKeyPrefix='employees-skeleton'
        className='flex h-full min-h-0 flex-col overflow-hidden'
        tableClassName='min-h-0 flex-1 overflow-auto'
      />
      <EmployeeFormDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onSuccess={() => qc.invalidateQueries({ queryKey: ['employees'] })}
      />
      <EmployeeFormDialog
        open={!!editRow}
        onOpenChange={(open) => !open && setEditRow(undefined)}
        currentRow={editRow}
        onSuccess={() => qc.invalidateQueries({ queryKey: ['employees'] })}
      />
      <PerformanceAdjustDialog
        open={!!performanceRow}
        onOpenChange={(open) => !open && setPerformanceRow(undefined)}
        employee={performanceRow}
        onSuccess={() => qc.invalidateQueries({ queryKey: ['employees'] })}
      />
      <AlertDialog
        open={!!deleteRow}
        onOpenChange={(open) => !open && setDeleteRow(undefined)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Disable Employee')}</AlertDialogTitle>
            <AlertDialogDescription render={<div />}>
              <div className='space-y-3'>
                <p>
                  {t(
                    'This will disable the employee. Historical commission logs are retained.'
                  )}
                </p>
                {deleteRow ? (
                  <div className='bg-muted/40 text-foreground rounded-lg border px-3 py-2 text-sm'>
                    <div className='flex min-w-0 items-center gap-2'>
                      <span className='truncate font-medium'>
                        {deleteRow.username || `#${deleteRow.user_id}`}
                      </span>
                      {deleteRow.display_name ? (
                        <span className='text-muted-foreground truncate'>
                          {deleteRow.display_name}
                        </span>
                      ) : null}
                      <Badge variant='outline' className='ml-auto shrink-0'>
                        #{deleteRow.user_id}
                      </Badge>
                    </div>
                    <div className='text-muted-foreground mt-1 text-xs'>
                      {t('Customer Count')}: {deleteRow.customer_count ?? 0}
                    </div>
                  </div>
                ) : null}
              </div>
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('Cancel')}</AlertDialogCancel>
            <AlertDialogAction variant='destructive' onClick={handleDelete}>
              {t('Disable Employee')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      <AssignCustomerDialog
        open={!!assignRow}
        employee={assignRow}
        onOpenChange={(open) => !open && setAssignRow(undefined)}
        onSuccess={() => qc.invalidateQueries({ queryKey: ['employees'] })}
      />
      <RemoveCustomersDialog
        open={!!customerListRow}
        employee={customerListRow}
        onOpenChange={(open) => !open && setCustomerListRow(undefined)}
        onSuccess={() => qc.invalidateQueries({ queryKey: ['employees'] })}
      />
    </>
  )
}

function CommissionLogsTab() {
  const { t } = useTranslation()
  const columns = useCommissionLogColumns()
  const [compactMode, setCompactMode] = useState(() => {
    if (typeof window === 'undefined') return false
    return localStorage.getItem('employee-commission-logs-compact') === 'true'
  })
  const [filterForm, setFilterForm] = useState({
    employeeUserId: '',
    customerUserId: '',
    modelName: '',
    channelId: '',
    lossStatus: 'all' as 'all' | 'loss' | 'normal',
    start: undefined as Date | undefined,
    end: undefined as Date | undefined,
  })
  const [filters, setFilters] = useState<{
    employee_user_id?: number
    customer_user_id?: number
    model_name?: string
    channel_id?: number
    loss_status?: 'loss' | 'normal'
    start_time?: number
    end_time?: number
  }>({})
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: 10,
  })

  const { data, isLoading, isFetching } = useQuery({
    queryKey: ['admin-commission-logs', pagination, filters],
    queryFn: () =>
      getCommissionLogs({
        page: pagination.pageIndex + 1,
        page_size: pagination.pageSize,
        ...filters,
      }),
  })

  // 渠道下拉选项（含已删除渠道的历史名称）
  const {
    data: channelOptionsData,
    fetchNextPage: fetchNextChannelOptionsPage,
    hasNextPage: hasNextChannelOptionsPage,
    isFetching: isFetchingChannelOptions,
    isFetchingNextPage: isFetchingNextChannelOptionsPage,
  } = useInfiniteQuery({
    queryKey: ['admin-commission-channel-options'],
    queryFn: ({ pageParam }) =>
      getCommissionChannelOptions({
        page: Number(pageParam),
        page_size: COMMISSION_CHANNEL_FILTER_PAGE_SIZE,
      }),
    initialPageParam: 1,
    getNextPageParam: (lastPage) => {
      const page = lastPage.data?.page ?? 1
      const pageSize =
        lastPage.data?.page_size ?? COMMISSION_CHANNEL_FILTER_PAGE_SIZE
      const total = lastPage.data?.total ?? 0
      return page * pageSize < total ? page + 1 : undefined
    },
    staleTime: 60 * 1000,
  })
  const channelOptions = useMemo(() => {
    const all =
      channelOptionsData?.pages.flatMap((page) => page.data?.items ?? []) ?? []
    const existing = new Set<number>()
    return all.filter((channel) => {
      if (existing.has(channel.channel_id)) return false
      existing.add(channel.channel_id)
      return true
    })
  }, [channelOptionsData])

  const handleChannelOptionsScroll = (event: UIEvent<HTMLDivElement>) => {
    const list = event.currentTarget
    const distanceToBottom =
      list.scrollHeight - list.scrollTop - list.clientHeight
    if (
      distanceToBottom > 48 ||
      !hasNextChannelOptionsPage ||
      isFetchingNextChannelOptionsPage
    ) {
      return
    }
    void fetchNextChannelOptionsPage()
  }

  const applyFilters = () => {
    const employeeUserId = Number(filterForm.employeeUserId)
    const customerUserId = Number(filterForm.customerUserId)
    const channelId = Number(filterForm.channelId)
    setFilters({
      ...(Number.isFinite(employeeUserId) && employeeUserId > 0
        ? { employee_user_id: employeeUserId }
        : {}),
      ...(Number.isFinite(customerUserId) && customerUserId > 0
        ? { customer_user_id: customerUserId }
        : {}),
      ...(filterForm.modelName.trim()
        ? { model_name: filterForm.modelName.trim() }
        : {}),
      ...(Number.isFinite(channelId) && channelId > 0
        ? { channel_id: channelId }
        : {}),
      ...(filterForm.lossStatus !== 'all'
        ? { loss_status: filterForm.lossStatus }
        : {}),
      ...(filterForm.start
        ? { start_time: Math.floor(filterForm.start.getTime() / 1000) }
        : {}),
      ...(filterForm.end
        ? { end_time: Math.floor(filterForm.end.getTime() / 1000) }
        : {}),
    })
    setPagination((current) => ({ ...current, pageIndex: 0 }))
  }

  const resetFilters = () => {
    setFilterForm({
      employeeUserId: '',
      customerUserId: '',
      modelName: '',
      channelId: '',
      lossStatus: 'all',
      start: undefined,
      end: undefined,
    })
    setFilters({})
    setPagination((current) => ({ ...current, pageIndex: 0 }))
  }

  const table = useReactTable({
    data: data?.data?.items ?? [],
    columns,
    rowCount: data?.data?.total ?? 0,
    state: { pagination },
    onPaginationChange: setPagination,
    manualPagination: true,
    getCoreRowModel: getCoreRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
  })
  const lossStatusLabel =
    filterForm.lossStatus === 'loss'
      ? t('Loss only')
      : filterForm.lossStatus === 'normal'
        ? t('Non-loss only')
        : t('All profit states')

  useEffect(() => {
    localStorage.setItem(
      'employee-commission-logs-compact',
      String(compactMode)
    )
  }, [compactMode])

  return (
    <DataTablePage
      table={table}
      columns={columns}
      isLoading={isLoading}
      isFetching={isFetching}
      emptyTitle={t('No records')}
      toolbar={
        <div className='space-y-2'>
          <div className='flex justify-end'>
            <Button
              type='button'
              size='sm'
              variant={compactMode ? 'default' : 'outline'}
              onClick={() => setCompactMode((value) => !value)}
            >
              <List className='h-4 w-4' />
              {t('Compact list')}
            </Button>
          </div>
          <div className='flex flex-wrap items-center gap-2'>
            <Input
              type='number'
              min={1}
              placeholder={t('Employee UID')}
              value={filterForm.employeeUserId}
              onChange={(event) =>
                setFilterForm((form) => ({
                  ...form,
                  employeeUserId: event.target.value,
                }))
              }
              className='w-[112px]'
            />
            <Input
              type='number'
              min={1}
              placeholder={t('Customer UID')}
              value={filterForm.customerUserId}
              onChange={(event) =>
                setFilterForm((form) => ({
                  ...form,
                  customerUserId: event.target.value,
                }))
              }
              className='w-[112px]'
            />
            <Input
              placeholder={t('Model Name')}
              value={filterForm.modelName}
              onChange={(event) =>
                setFilterForm((form) => ({
                  ...form,
                  modelName: event.target.value,
                }))
              }
              className='w-[160px]'
            />
            <Select
              value={filterForm.channelId || 'all'}
              onValueChange={(value) =>
                setFilterForm((form) => ({
                  ...form,
                  channelId: !value || value === 'all' ? '' : value,
                }))
              }
              items={[
                { value: 'all', label: t('All channels') },
                ...channelOptions.map((ch) => ({
                  value: String(ch.channel_id),
                  label: ch.channel_name || `#${ch.channel_id}`,
                })),
              ]}
            >
              <SelectTrigger size='sm' className='w-[200px]'>
                <SelectValue placeholder={t('Channel')} />
              </SelectTrigger>
              <SelectContent onScroll={handleChannelOptionsScroll}>
                <SelectGroup>
                  <SelectItem value='all'>{t('All channels')}</SelectItem>
                  {channelOptions.map((ch) => (
                    <SelectItem key={ch.channel_id} value={String(ch.channel_id)}>
                      <span className='flex items-center gap-1'>
                        <span className='max-w-[180px] truncate'>
                          {ch.channel_name || `#${ch.channel_id}`}
                        </span>
                        {ch.deleted ? (
                          <span className='text-muted-foreground text-xs'>
                            {t('(Deleted)')}
                          </span>
                        ) : null}
                      </span>
                    </SelectItem>
                  ))}
                  {isFetchingNextChannelOptionsPage ? (
                    <div className='text-muted-foreground px-2 py-1.5 text-xs'>
                      {t('Loading...')}
                    </div>
                  ) : null}
                  {!isFetchingChannelOptions && channelOptions.length === 0 ? (
                    <div className='text-muted-foreground px-2 py-1.5 text-xs'>
                      {t('No records')}
                    </div>
                  ) : null}
                </SelectGroup>
              </SelectContent>
            </Select>
            <Select
              value={filterForm.lossStatus}
              onValueChange={(value) =>
                setFilterForm((form) => ({
                  ...form,
                  lossStatus: (value || 'all') as 'all' | 'loss' | 'normal',
                }))
              }
              items={[
                { value: 'all', label: t('All profit states') },
                { value: 'loss', label: t('Loss only') },
                { value: 'normal', label: t('Non-loss only') },
              ]}
            >
              <SelectTrigger size='sm' className='w-[132px]'>
                <SelectValue>{lossStatusLabel}</SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectItem value='all'>{t('All profit states')}</SelectItem>
                  <SelectItem value='loss'>{t('Loss only')}</SelectItem>
                  <SelectItem value='normal'>{t('Non-loss only')}</SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
            <div className='w-[300px]'>
              <CompactDateTimeRangePicker
                start={filterForm.start}
                end={filterForm.end}
                onChange={({ start, end }) =>
                  setFilterForm((form) => ({ ...form, start, end }))
                }
              />
            </div>
            <Button size='sm' onClick={applyFilters}>
              {t('Search')}
            </Button>
            <Button size='sm' variant='outline' onClick={resetFilters}>
              {t('Reset')}
            </Button>
          </div>
        </div>
      }
      getRowClassName={(row) =>
        cn(
          compactMode && '[&_td]:h-8 [&_td]:py-1 [&_td]:text-xs',
          row.original.profit_quota < 0 && 'opacity-60'
        )
      }
      skeletonKeyPrefix='commission-logs-skeleton'
      className='flex h-full min-h-0 flex-col overflow-hidden'
      tableClassName='min-h-0 flex-1 overflow-auto'
    />
  )
}

function EmployeeMonthlySelector({
  value,
  onChange,
}: {
  value?: EmployeeProfile
  onChange: (employee?: EmployeeProfile) => void
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [keyword, setKeyword] = useState('')
  const [debounced, setDebounced] = useState('')
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const timer = setTimeout(() => setDebounced(keyword.trim()), 300)
    return () => clearTimeout(timer)
  }, [keyword])

  useEffect(() => {
    if (!open) return
    const handler = (event: MouseEvent) => {
      if (
        containerRef.current &&
        !containerRef.current.contains(event.target as Node)
      ) {
        setOpen(false)
        setKeyword('')
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [open])

  const { data, fetchNextPage, hasNextPage, isFetching, isFetchingNextPage } =
    useInfiniteQuery({
      queryKey: ['commission-monthly-employee-selector', debounced],
      queryFn: ({ pageParam }) =>
        getEmployees(Number(pageParam), EMPLOYEE_MONTHLY_SELECTOR_PAGE_SIZE, {
          keyword: debounced,
          status: 1,
        }),
      initialPageParam: 1,
      getNextPageParam: (lastPage) => {
        const page = lastPage.data?.page ?? 1
        const pageSize =
          lastPage.data?.page_size ?? EMPLOYEE_MONTHLY_SELECTOR_PAGE_SIZE
        const total = lastPage.data?.total ?? 0
        return page * pageSize < total ? page + 1 : undefined
      },
      enabled: open,
    })

  const employees = useMemo(() => {
    const all = data?.pages.flatMap((page) => page.data?.items ?? []) ?? []
    const existing = new Set<number>()
    return all.filter((employee) => {
      if (existing.has(employee.id)) return false
      existing.add(employee.id)
      return true
    })
  }, [data])
  const isInitialFetching = isFetching && !data
  const allEmployeesLabel = t('All employees')
  const displayValue = open
    ? keyword
    : value
      ? getEmployeeMonthlyOptionLabel(value)
      : ''

  const handleListScroll = (event: UIEvent<HTMLUListElement>) => {
    const list = event.currentTarget
    const distanceToBottom =
      list.scrollHeight - list.scrollTop - list.clientHeight
    if (distanceToBottom > 48 || !hasNextPage || isFetchingNextPage) return
    void fetchNextPage()
  }

  const clearSelection = () => {
    setKeyword('')
    setDebounced('')
    onChange(undefined)
    setOpen(true)
  }

  return (
    <div ref={containerRef} className='relative w-full sm:w-[280px]'>
      <Input
        type='text'
        autoComplete='off'
        placeholder={allEmployeesLabel}
        value={displayValue}
        onChange={(event) => {
          setKeyword(event.target.value)
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
      {open ? (
        <div className='bg-popover text-popover-foreground absolute top-full right-0 left-0 z-[120] mt-2 overflow-hidden rounded-lg border shadow-lg'>
          <button
            type='button'
            className={cn(
              'hover:bg-accent flex w-full items-center justify-between px-3 py-2 text-left text-sm',
              !value && 'bg-accent/70'
            )}
            onMouseDown={(event) => {
              event.preventDefault()
              clearSelection()
              setOpen(false)
            }}
          >
            <span className='font-medium'>{allEmployeesLabel}</span>
            <span className='text-muted-foreground text-xs'>
              {t('All')}
            </span>
          </button>
          <ul
            onScroll={handleListScroll}
            className='max-h-64 overflow-y-auto border-t p-1'
          >
            {isInitialFetching ? (
              <li className='text-muted-foreground px-3 py-8 text-center text-sm'>
                {t('Loading...')}
              </li>
            ) : employees.length === 0 ? (
              <li className='text-muted-foreground px-3 py-8 text-center text-sm'>
                {t('No users found')}
              </li>
            ) : (
              <>
                {employees.map((employee) => {
                  const isSelected = value?.id === employee.id
                  return (
                    <li
                      key={employee.id}
                      role='option'
                      aria-selected={isSelected}
                      className='hover:bg-accent aria-selected:bg-accent/80 cursor-pointer rounded-md px-3 py-2 text-sm transition-colors'
                      onMouseDown={(event) => {
                        event.preventDefault()
                        onChange(employee)
                        setKeyword('')
                        setOpen(false)
                      }}
                    >
                      <div className='flex min-w-0 items-center justify-between gap-3'>
                        <span className='truncate font-medium'>
                          {employee.remark || getEmployeeMonthlyAccountLabel(employee)}
                        </span>
                        {employee.remark ? (
                          <span className='text-muted-foreground shrink-0 truncate text-xs'>
                            {getEmployeeMonthlyAccountLabel(employee)}
                          </span>
                        ) : null}
                      </div>
                    </li>
                  )
                })}
                {isFetchingNextPage ? (
                  <li className='text-muted-foreground px-3 py-3 text-center text-sm'>
                    {t('Loading...')}
                  </li>
                ) : null}
              </>
            )}
          </ul>
        </div>
      ) : null}
    </div>
  )
}

function csvCell(value: string | number | undefined) {
  return `"${String(value ?? '').replace(/"/g, '""')}"`
}

function CommissionMonthlyStatsTab() {
  const { t } = useTranslation()
  const [selectedEmployee, setSelectedEmployee] = useState<EmployeeProfile>()
  const [month, setMonth] = useState(currentMonthValue)
  const [exporting, setExporting] = useState(false)
  const employeeUserId = selectedEmployee?.user_id

  const handleExport = async () => {
    setExporting(true)
    try {
      // 用与日历展示相同的周期范围，保证导出的统计周期与页面所见一致
      // （非自然月结算配置下 monthValueToRange 会解析到不同周期）。
      const res = await getCommissionMonthlyExport(
        monthValueToCalendarRange(month)
      )
      if (!res.success || !res.data) {
        throw new Error(res.message || t('Operation failed'))
      }
      const rows = res.data.rows ?? []
      if (rows.length === 0) {
        toast.info(t('No data to export'))
        return
      }
      const headers = [
        t('Employee'),
        t('Employee ID'),
        t('Remark'),
        t('Period Tier'),
        t('Group'),
        t('Commission ratio'),
        t('Current Period Performance'),
        t('Current Period Commission'),
        t('Records'),
      ]
      const lines = [headers.map(csvCell).join(',')]
      for (const row of rows) {
        const account = row.username || `#${row.employee_user_id}`
        const name = row.display_name
          ? `${account} (${row.display_name})`
          : account
        lines.push(
          [
            name,
            row.employee_user_id,
            row.remark || '',
            row.tier_level
              ? t('Tier {{level}}', { level: row.tier_level })
              : '-',
            row.tier_group || '',
            row.tier_level ? `${(row.tier_rate * 100).toFixed(1)}%` : '',
            formatBusinessAmount(row.profit_quota),
            formatBusinessAmount(row.commission_quota),
            row.record_count,
          ]
            .map(csvCell)
            .join(',')
        )
      }
      const csv = '﻿' + lines.join('\r\n')
      // 前置 UTF-8 BOM，便于 Excel/WPS 正确识别中文。
      const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' })
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = `commission-${res.data.period_key || month}.csv`
      document.body.appendChild(link)
      link.click()
      link.remove()
      URL.revokeObjectURL(url)
      toast.success(t('Export successful'))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Operation failed'))
    } finally {
      setExporting(false)
    }
  }

  return (
    <CommissionCalendarSection
      queryKey={['admin-commission-calendar-stats', employeeUserId]}
      month={month}
      onMonthChange={setMonth}
      queryFn={(range) =>
        getCommissionCalendarStats({
          ...range,
          employee_user_id: employeeUserId,
        })
      }
      toolbar={
        <div className='flex w-full flex-col items-stretch gap-2 sm:w-auto sm:flex-row sm:items-center sm:justify-end'>
          <span className='text-muted-foreground shrink-0 text-xs font-medium sm:text-sm'>
            {t('Employee')}
          </span>
          <EmployeeMonthlySelector
            value={selectedEmployee}
            onChange={setSelectedEmployee}
          />
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={handleExport}
            disabled={exporting}
            title={t('Export current month per-employee data')}
          >
            <Download className='h-4 w-4' />
            {exporting ? t('Exporting...') : t('Export')}
          </Button>
        </div>
      }
    />
  )
}

function TierDialog({
  open,
  currentRow,
  tiers = [],
  onOpenChange,
  onSuccess,
}: {
  open: boolean
  currentRow?: EmployeeTier
  tiers?: EmployeeTier[]
  onOpenChange: (open: boolean) => void
  onSuccess: () => void
}) {
  const { t } = useTranslation()
  const [level, setLevel] = useState('1')
  const [group, setGroup] = useState(DEFAULT_TIER_GROUP)
  const [thresholdUsd, setThresholdUsd] = useState('0')
  const [rate, setRate] = useState('0.1')
  const [saving, setSaving] = useState(false)
  const [addingGroup, setAddingGroup] = useState(false)
  const [newGroupText, setNewGroupText] = useState('')
  const [extraGroups, setExtraGroups] = useState<string[]>([])
  const newGroupInputRef = useRef<HTMLInputElement>(null)
  const parsedLevel = Number(level)
  const parsedThresholdUsd = Number(thresholdUsd)
  const parsedRate = Number(rate)
  const normalizedGroup = group.trim() || DEFAULT_TIER_GROUP
  const rateText = Number.isFinite(parsedRate) ? formatPercent(parsedRate) : '-'
  const thresholdText =
    Number.isFinite(parsedThresholdUsd) && parsedThresholdUsd > 0
      ? formatTargetAmount(parsedThresholdUsd)
      : t('No limit')
  const duplicateTier = tiers.find(
    (tier) =>
      tier.id !== currentRow?.id &&
      tier.level === parsedLevel &&
      (tier.group || DEFAULT_TIER_GROUP) === normalizedGroup
  )

  const baseGroups = useMemo(() => {
    const seen = new Set<string>([DEFAULT_TIER_GROUP])
    tiers.forEach((t) => {
      if (t.group) seen.add(t.group)
    })
    return Array.from(seen)
  }, [tiers])

  const allGroups = useMemo(() => {
    const seen = new Set<string>(baseGroups)
    extraGroups.forEach((g) => seen.add(g))
    if (group && !seen.has(group)) seen.add(group)
    return Array.from(seen)
  }, [baseGroups, extraGroups, group])

  useEffect(() => {
    if (!open) return
    setExtraGroups([])
    setAddingGroup(false)
    setNewGroupText('')
    setLevel(String(currentRow?.level ?? 1))
    setGroup(currentRow?.group || DEFAULT_TIER_GROUP)
    setThresholdUsd(String(currentRow?.threshold_usd ?? 0))
    setRate(String(currentRow?.rate ?? 0.1))
  }, [currentRow, open])

  useEffect(() => {
    if (addingGroup) newGroupInputRef.current?.focus()
  }, [addingGroup])

  const confirmNewGroup = () => {
    const g = newGroupText.trim()
    if (g && !allGroups.includes(g)) setExtraGroups((prev) => [...prev, g])
    if (g) setGroup(g)
    setAddingGroup(false)
    setNewGroupText('')
  }

  const submit = async () => {
    const nextLevel = Number(level)
    if (!Number.isFinite(nextLevel) || nextLevel < 1) {
      toast.error(t('Tier number must be at least 1'))
      return
    }
    const nextThresholdUsd = Number(thresholdUsd)
    if (!Number.isFinite(nextThresholdUsd) || nextThresholdUsd < 0) {
      toast.error(t('Threshold must be zero or greater'))
      return
    }
    const nextRate = Number(rate)
    if (!Number.isFinite(nextRate) || nextRate < 0 || nextRate > 1) {
      toast.error(t('Commission rate must be between 0 and 1'))
      return
    }
    if (duplicateTier) {
      toast.error(t('A tier with the same level and group already exists'))
      return
    }
    const body = {
      level: nextLevel,
      group: normalizedGroup,
      threshold_usd: nextThresholdUsd,
      rate: nextRate,
    }
    setSaving(true)
    try {
      const res = currentRow
        ? await updateEmployeeTier(currentRow.id, body)
        : await createEmployeeTier(body)
      if (!res.success) throw new Error(res.message ?? 'Failed')
      toast.success(
        currentRow
          ? t('Tier updated successfully')
          : t('Tier created successfully')
      )
      onOpenChange(false)
      onSuccess()
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Operation failed')
      )
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className='h-[86vh] max-h-[620px] overflow-y-auto sm:max-w-[420px]'
        initialFocus={false}
      >
        <DialogHeader>
          <DialogTitle>
            {currentRow ? t('Edit Tier') : t('Create Tier')}
          </DialogTitle>
          <DialogDescription>
            {t(
              'Set the promotion threshold and commission rate employees receive after reaching this tier.'
            )}
          </DialogDescription>
        </DialogHeader>
        <div className='space-y-4'>
          <div className='space-y-2'>
            <label className='text-sm font-medium'>{t('Tier Number')}</label>
            <Input
              type='number'
              min={1}
              step={1}
              value={level}
              onChange={(event) => setLevel(event.target.value)}
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'A positive integer. Higher numbers indicate higher tiers, such as 1, 2, 3.'
              )}
            </p>
          </div>
          <div className='space-y-2'>
            <label className='text-sm font-medium'>{t('Group')}</label>
            <div className='flex flex-wrap items-center gap-1.5'>
              {allGroups.map((g) => (
                <button
                  key={g}
                  type='button'
                  onClick={() => setGroup(g)}
                  className={cn(
                    'rounded-full border px-4 py-1.5 text-sm font-medium transition-colors',
                    group === g
                      ? 'border-primary bg-primary text-primary-foreground'
                      : 'border-input bg-background hover:bg-accent hover:text-accent-foreground'
                  )}
                >
                  <span
                    className={cn(
                      'mr-2 inline-block size-2 rounded-full align-middle',
                      group === g
                        ? 'bg-primary-foreground'
                        : getEmployeeTierGroupDotClass(g)
                    )}
                  />
                  {g}
                </button>
              ))}
              {addingGroup ? (
                <input
                  ref={newGroupInputRef}
                  value={newGroupText}
                  onChange={(e) => setNewGroupText(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') {
                      e.preventDefault()
                      confirmNewGroup()
                    }
                    if (e.key === 'Escape') {
                      setAddingGroup(false)
                      setNewGroupText('')
                    }
                  }}
                  onBlur={confirmNewGroup}
                  placeholder={t('Group name')}
                  className='border-input bg-background w-24 rounded-full border px-3 py-1 text-xs outline-none focus:ring-1'
                />
              ) : (
                <button
                  type='button'
                  onClick={() => setAddingGroup(true)}
                  className='border-input bg-background hover:bg-accent hover:text-accent-foreground rounded-full border px-4 py-1.5 text-sm font-medium transition-colors'
                >
                  + {t('New')}
                </button>
              )}
            </div>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Group name within this tier level. Multiple groups can share the same tier level with different rates. Defaults to the general group.'
              )}
            </p>
            {duplicateTier ? (
              <p className='text-destructive text-xs'>
                {t('A tier with the same level and group already exists')}
              </p>
            ) : null}
          </div>
          <div className='space-y-2'>
            <label className='text-sm font-medium'>
              {t('Performance Threshold (USD)')}
            </label>
            <Input
              type='number'
              min={0}
              step={100}
              value={thresholdUsd}
              onChange={(event) => setThresholdUsd(event.target.value)}
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'Employees are upgraded automatically after cumulative profit reaches this amount.'
              )}
            </p>
          </div>
          <div className='space-y-2'>
            <label className='text-sm font-medium'>
              {t('Commission Rate')}
            </label>
            <Input
              type='number'
              min={0}
              max={1}
              step={0.01}
              value={rate}
              onChange={(event) => setRate(event.target.value)}
            />
            <div className='flex flex-wrap gap-1.5'>
              {[0.05, 0.08, 0.1, 0.15, 0.2].map((preset) => (
                <Button
                  key={preset}
                  type='button'
                  size='xs'
                  variant={Number(rate) === preset ? 'default' : 'outline'}
                  onClick={() => setRate(String(preset))}
                >
                  {formatPercent(preset)}
                </Button>
              ))}
            </div>
            <p className='text-muted-foreground text-xs'>
              {t('0.1 means 10%, applied after reaching this tier.')}
            </p>
          </div>
          <div className='bg-muted/30 rounded-lg border px-3 py-2 text-sm'>
            <div className='text-muted-foreground text-xs'>
              {t('Tier preview')}
            </div>
            <div className='mt-1 flex flex-wrap items-center gap-2'>
              <Badge
                variant='outline'
                className={getEmployeeTierLevelBadgeClass(parsedLevel)}
              >
                {t('Tier {{level}}', {
                  level:
                    Number.isFinite(parsedLevel) && parsedLevel > 0
                      ? parsedLevel
                      : '-',
                })}
              </Badge>
              <Badge
                variant='outline'
                className={getEmployeeTierGroupBadgeClass(normalizedGroup)}
              >
                {normalizedGroup}
              </Badge>
              <span className='text-muted-foreground'>
                {t('Threshold')}: {thresholdText}
              </span>
              <span className='text-muted-foreground'>
                {t('Rate')}: {rateText}
              </span>
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant='outline' onClick={() => onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button disabled={saving || Boolean(duplicateTier)} onClick={submit}>
            {saving ? t('Saving...') : t('Save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function TierRowActions({
  row,
  onEdit,
  onDelete,
}: {
  row: EmployeeTier
  onEdit: (row: EmployeeTier) => void
  onDelete: (row: EmployeeTier) => void
}) {
  const { t } = useTranslation()

  return (
    <div className='flex flex-nowrap items-center justify-end gap-1'>
      <Button
        type='button'
        size='icon'
        variant='ghost'
        className='size-8'
        title={t('Edit Tier')}
        aria-label={t('Edit Tier')}
        onClick={() => onEdit(row)}
      >
        <Pencil className='size-4' />
      </Button>
      <Button
        type='button'
        size='icon'
        variant='ghost'
        className='text-destructive hover:text-destructive size-8'
        title={t('Delete Tier')}
        aria-label={t('Delete Tier')}
        onClick={() => onDelete(row)}
      >
        <Trash2 className='size-4' />
      </Button>
    </div>
  )
}

function TiersTab() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [createOpen, setCreateOpen] = useState(false)
  const [editRow, setEditRow] = useState<EmployeeTier | undefined>()
  const [deleteRow, setDeleteRow] = useState<EmployeeTier | undefined>()
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: 10,
  })

  const columns = useMemo(
    (): ColumnDef<EmployeeTier>[] => [
      {
        accessorKey: 'group',
        meta: { label: t('Group'), mobileTitle: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Group')} />
        ),
        cell: ({ row }) => (
          <Badge
            variant='outline'
            className={getEmployeeTierGroupBadgeClass(
              row.original.group || DEFAULT_TIER_GROUP
            )}
          >
            {row.original.group || DEFAULT_TIER_GROUP}
          </Badge>
        ),
      },
      {
        accessorKey: 'level',
        meta: { label: t('Tier') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Tier')} />
        ),
        cell: ({ row }) => (
          <Badge
            variant='outline'
            className={getEmployeeTierLevelBadgeClass(row.original.level)}
          >
            {t('Tier {{level}}', { level: row.original.level })}
          </Badge>
        ),
      },
      {
        accessorKey: 'threshold_usd',
        meta: { label: t('Performance Threshold (USD)') },
        header: ({ column }) => (
          <DataTableColumnHeader
            column={column}
            title={t('Performance Threshold (USD)')}
          />
        ),
        cell: ({ row }) => formatTargetAmount(row.original.threshold_usd),
      },
      {
        accessorKey: 'rate',
        meta: { label: t('Commission Rate'), mobileBadge: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Commission Rate')} />
        ),
        cell: ({ row }) => (
          <Badge variant='secondary'>{formatPercent(row.original.rate)}</Badge>
        ),
      },
      {
        id: 'actions',
        enableSorting: false,
        cell: ({ row }) => (
          <TierRowActions
            row={row.original}
            onDelete={setDeleteRow}
            onEdit={setEditRow}
          />
        ),
      },
    ],
    [t]
  )

  const { data, isLoading, isFetching } = useQuery({
    queryKey: ['employee-tiers', 'page', pagination],
    queryFn: () =>
      getEmployeeTiersPage({
        page: pagination.pageIndex + 1,
        page_size: pagination.pageSize,
      }),
  })

  const { data: allTiersData } = useQuery({
    queryKey: ['employee-tiers', 'all'],
    queryFn: getEmployeeTiers,
  })

  const table = useReactTable({
    data: data?.data?.items ?? [],
    columns,
    rowCount: data?.data?.total ?? 0,
    state: { pagination },
    onPaginationChange: setPagination,
    manualPagination: true,
    getCoreRowModel: getCoreRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
  })

  const refresh = () => qc.invalidateQueries({ queryKey: ['employee-tiers'] })
  const tierTotal = data?.data?.total ?? 0

  const handleDelete = async () => {
    if (!deleteRow) return
    try {
      const res = await deleteEmployeeTier(deleteRow.id)
      if (!res.success) throw new Error(res.message)
      toast.success(t('Tier deleted successfully'))
      refresh()
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Operation failed')
      )
    } finally {
      setDeleteRow(undefined)
    }
  }

  return (
    <div className='flex h-full min-h-0 flex-col gap-4 overflow-hidden'>
      <DataTablePage
        table={table}
        columns={columns}
        isLoading={isLoading}
        isFetching={isFetching}
        emptyTitle={t('No tier configuration yet')}
        emptyDescription={t('Click Add Tier to create one.')}
        emptyAction={
          <Button size='sm' onClick={() => setCreateOpen(true)}>
            <PlusIcon className='h-4 w-4' />
            {t('Add Tier')}
          </Button>
        }
        toolbar={
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <span className='text-muted-foreground text-xs'>
              {t('{{count}} tiers configured', { count: tierTotal })}
            </span>
            <div className='flex items-center gap-2'>
              <TierResetSettingsCard />
              <Button size='sm' onClick={() => setCreateOpen(true)}>
                <PlusIcon className='mr-1 h-4 w-4' />
                {t('Add Tier')}
              </Button>
            </div>
          </div>
        }
        skeletonKeyPrefix='employee-tiers-skeleton'
        className='flex min-h-0 flex-1 flex-col overflow-hidden'
        tableClassName='min-h-0 flex-1 overflow-auto'
      />
      <TierDialog
        open={createOpen}
        tiers={allTiersData?.data ?? []}
        onOpenChange={setCreateOpen}
        onSuccess={refresh}
      />
      <TierDialog
        open={!!editRow}
        currentRow={editRow}
        tiers={allTiersData?.data ?? []}
        onOpenChange={(open) => !open && setEditRow(undefined)}
        onSuccess={refresh}
      />
      <AlertDialog
        open={!!deleteRow}
        onOpenChange={(open) => !open && setDeleteRow(undefined)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Delete Tier')}</AlertDialogTitle>
            <AlertDialogDescription render={<div />}>
              <div className='space-y-3'>
                <p>
                  {t(
                    'Employees already at this tier will not be downgraded automatically, but future upgrade checks will use the new configuration.'
                  )}
                </p>
                {deleteRow ? (
                  <div className='bg-muted/40 text-foreground rounded-lg border px-3 py-2 text-sm'>
                    <div className='flex flex-wrap items-center gap-2'>
                      <Badge
                        variant='outline'
                        className={getEmployeeTierLevelBadgeClass(
                          deleteRow.level
                        )}
                      >
                        {t('Tier {{level}}', { level: deleteRow.level })}
                      </Badge>
                      <Badge
                        variant='outline'
                        className={getEmployeeTierGroupBadgeClass(
                          deleteRow.group || DEFAULT_TIER_GROUP
                        )}
                      >
                        {deleteRow.group || DEFAULT_TIER_GROUP}
                      </Badge>
                    </div>
                    <div className='text-muted-foreground mt-2 grid gap-1 text-xs'>
                      <span>
                        {t('Performance Threshold (USD)')}:{' '}
                        {formatTargetAmount(deleteRow.threshold_usd)}
                      </span>
                      <span>
                        {t('Commission Rate')}: {formatPercent(deleteRow.rate)}
                      </span>
                    </div>
                  </div>
                ) : null}
              </div>
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('Cancel')}</AlertDialogCancel>
            <AlertDialogAction variant='destructive' onClick={handleDelete}>
              {t('Delete Tier')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

export function Employees() {
  const { t } = useTranslation()
  const [activeTab, setActiveTab] = useState('monthly')

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Employee Management')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content className='overflow-hidden'>
        <Tabs
          value={activeTab}
          onValueChange={(value) => value && setActiveTab(value)}
          className='flex h-full min-h-0 flex-col overflow-hidden'
        >
          <TabsList className='shrink-0'>
            <TabsTrigger value='monthly'>{t('Monthly Stats')}</TabsTrigger>
            <TabsTrigger value='employees'>{t('Employees')}</TabsTrigger>
            <TabsTrigger value='tiers'>{t('Commission Tiers')}</TabsTrigger>
            <TabsTrigger value='commission'>{t('Commission Logs')}</TabsTrigger>
          </TabsList>
          <TabsContent value='monthly' className='min-h-0 flex-1 overflow-auto'>
            {activeTab === 'monthly' && <CommissionMonthlyStatsTab />}
          </TabsContent>
          <TabsContent
            value='employees'
            className='min-h-0 flex-1 overflow-hidden'
          >
            {activeTab === 'employees' && <EmployeesTab />}
          </TabsContent>
          <TabsContent value='tiers' className='min-h-0 flex-1 overflow-hidden'>
            {activeTab === 'tiers' && <TiersTab />}
          </TabsContent>
          <TabsContent
            value='commission'
            className='min-h-0 flex-1 overflow-hidden'
          >
            {activeTab === 'commission' && <CommissionLogsTab />}
          </TabsContent>
        </Tabs>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

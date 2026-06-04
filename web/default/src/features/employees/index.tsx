import { useEffect, useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  type ColumnDef,
  type PaginationState,
  type SortingState,
  getCoreRowModel,
  getPaginationRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { Pencil, PlusIcon, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
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
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { DataTableColumnHeader, DataTablePage } from '@/components/data-table'
import { SectionPageLayout } from '@/components/layout'
import {
  formatBusinessAmount,
  formatBusinessTargetAmount,
} from '@/features/business/format'
import { CompactDateTimeRangePicker } from '@/features/usage-logs/components/compact-date-time-range-picker'
import {
  createEmployeeTier,
  deleteEmployee,
  deleteEmployeeTier,
  getCommissionLogs,
  getEmployeeTiers,
  getEmployees,
  updateEmployeeTier,
} from './api'
import { EmployeeFormDialog } from './components/employee-form-dialog'
import type { CommissionLog, EmployeeProfile, EmployeeTier } from './types'

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
  const amount = Number(value || 0)
  const className =
    amount > 0 ? 'text-green-600' : amount < 0 ? 'text-destructive' : undefined
  return <span className={className}>{formatBusinessAmount(amount)}</span>
}

function StatusBadge({ status }: { status: number }) {
  const { t } = useTranslation()
  return status === 1 ? (
    <Badge variant='default'>{t('Enabled')}</Badge>
  ) : (
    <Badge variant='secondary'>{t('Disabled')}</Badge>
  )
}

function useEmployeesColumns({
  onEdit,
  onDelete,
}: {
  onEdit: (row: EmployeeProfile) => void
  onDelete: (row: EmployeeProfile) => void
}) {
  const { t } = useTranslation()

  return useMemo(
    (): ColumnDef<EmployeeProfile>[] => [
      {
        accessorKey: 'id',
        meta: { label: t('ID'), mobileHidden: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('ID')} />
        ),
      },
      {
        accessorKey: 'user_id',
        meta: { label: t('User ID') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('User ID')} />
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
        accessorKey: 'total_consumption_quota',
        meta: { label: t('Customer Total Consumption') },
        header: ({ column }) => (
          <DataTableColumnHeader
            column={column}
            title={t('Customer Total Consumption')}
          />
        ),
        cell: ({ row }) =>
          formatBusinessAmount(row.original.total_consumption_quota ?? 0),
      },
      {
        accessorKey: 'total_cost_quota',
        meta: { label: t('Total Cost') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Total Cost')} />
        ),
        cell: ({ row }) =>
          formatBusinessAmount(row.original.total_cost_quota ?? 0),
      },
      {
        accessorKey: 'total_profit_quota',
        meta: { label: t('Total Profit') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Total Profit')} />
        ),
        cell: ({ row }) => (
          <AmountText value={row.original.total_profit_quota} />
        ),
      },
      {
        accessorKey: 'total_commission_quota',
        meta: { label: t('Total Commission') },
        header: ({ column }) => (
          <DataTableColumnHeader
            column={column}
            title={t('Total Commission')}
          />
        ),
        cell: ({ row }) =>
          formatBusinessAmount(row.original.total_commission_quota ?? 0),
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
              <Badge variant='outline'>
                {t('Tier {{level}}', {
                  level: row.original.current_tier_level,
                })}
              </Badge>
              <span className='text-muted-foreground text-xs'>
                {formatPercent(row.original.current_tier_rate)}
              </span>
            </div>
          ) : (
            <span className='text-muted-foreground'>-</span>
          ),
      },
      {
        accessorKey: 'commission_rate',
        meta: { label: t('Commission Rate') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Commission Rate')} />
        ),
        cell: ({ row }) => formatPercent(row.original.commission_rate),
      },
      {
        accessorKey: 'target_amount',
        meta: { label: t('Performance Target') },
        header: ({ column }) => (
          <DataTableColumnHeader
            column={column}
            title={t('Performance Target')}
          />
        ),
        cell: ({ row }) =>
          row.original.target_amount
            ? formatTargetAmount(row.original.target_amount)
            : t('No limit'),
      },
      {
        accessorKey: 'current_performance_quota',
        meta: { label: t('Current Performance') },
        header: ({ column }) => (
          <DataTableColumnHeader
            column={column}
            title={t('Current Performance')}
          />
        ),
        cell: ({ row }) => (
          <div className='min-w-[150px]'>
            <div>
              {formatBusinessAmount(
                row.original.current_performance_quota ?? 0
              )}
            </div>
            {row.original.target_amount ? (
              <div className='text-muted-foreground text-xs'>
                {t('Target')}: {formatTargetAmount(row.original.target_amount)}
              </div>
            ) : null}
          </div>
        ),
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
          <div className='flex gap-1'>
            <Button
              size='icon'
              variant='ghost'
              onClick={() => onEdit(row.original)}
            >
              <Pencil className='h-4 w-4' />
            </Button>
            <Button
              size='icon'
              variant='ghost'
              onClick={() => onDelete(row.original)}
            >
              <Trash2 className='text-destructive h-4 w-4' />
            </Button>
          </div>
        ),
      },
    ],
    [onDelete, onEdit, t]
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
        accessorKey: 'revenue_quota',
        meta: { label: t('Revenue') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Revenue')} />
        ),
        cell: ({ row }) => formatBusinessAmount(row.original.revenue_quota),
      },
      {
        accessorKey: 'cost_quota',
        meta: { label: t('Cost') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Cost')} />
        ),
        cell: ({ row }) => formatBusinessAmount(row.original.cost_quota),
      },
      {
        accessorKey: 'profit_quota',
        meta: { label: t('Profit') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Profit')} />
        ),
        cell: ({ row }) => formatBusinessAmount(row.original.profit_quota),
      },
      {
        accessorKey: 'commission_quota',
        meta: { label: t('Commission'), mobileBadge: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Commission')} />
        ),
        cell: ({ row }) => (
          <span
            className={
              row.original.commission_quota < 0
                ? 'text-destructive'
                : 'text-green-600'
            }
          >
            {formatBusinessAmount(row.original.commission_quota)}
          </span>
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
    pageSize: 20,
  })
  const [sorting, setSorting] = useState<SortingState>([])
  const columns = useEmployeesColumns({
    onEdit: setEditRow,
    onDelete: setDeleteRow,
  })

  const { data, isLoading } = useQuery({
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

  return (
    <>
      <DataTablePage
        table={table}
        columns={columns}
        isLoading={isLoading}
        emptyTitle={t('No employees yet')}
        toolbar={
          <div className='flex w-full flex-col gap-2 lg:flex-row lg:items-center lg:justify-between'>
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
              <Select
                value={filterForm.status}
                onValueChange={(value) =>
                  setFilterForm((form) => ({ ...form, status: value ?? '0' }))
                }
              >
                <SelectTrigger className='w-[120px]'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value='0'>{t('All')}</SelectItem>
                  <SelectItem value='1'>{t('Enabled')}</SelectItem>
                  <SelectItem value='2'>{t('Disabled')}</SelectItem>
                </SelectContent>
              </Select>
              <Button size='sm' onClick={applyFilters}>
                {t('Search')}
              </Button>
              <Button size='sm' variant='outline' onClick={resetFilters}>
                {t('Reset')}
              </Button>
            </div>
            <Button size='sm' onClick={() => setCreateOpen(true)}>
              <PlusIcon className='mr-1 h-4 w-4' />
              {t('Add Employee')}
            </Button>
          </div>
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
      <AlertDialog
        open={!!deleteRow}
        onOpenChange={(open) => !open && setDeleteRow(undefined)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Disable Employee')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                'This will disable the employee. Historical commission logs are retained.'
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('Cancel')}</AlertDialogCancel>
            <AlertDialogAction onClick={handleDelete}>
              {t('Confirm')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

function CommissionLogsTab() {
  const { t } = useTranslation()
  const columns = useCommissionLogColumns()
  const [filterForm, setFilterForm] = useState({
    employeeUserId: '',
    customerUserId: '',
    modelName: '',
    channelId: '',
    start: undefined as Date | undefined,
    end: undefined as Date | undefined,
  })
  const [filters, setFilters] = useState<{
    employee_user_id?: number
    customer_user_id?: number
    model_name?: string
    channel_id?: number
    start_time?: number
    end_time?: number
  }>({})
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: 20,
  })

  const { data, isLoading } = useQuery({
    queryKey: ['admin-commission-logs', pagination, filters],
    queryFn: () =>
      getCommissionLogs({
        page: pagination.pageIndex + 1,
        page_size: pagination.pageSize,
        ...filters,
      }),
  })

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

  return (
    <DataTablePage
      table={table}
      columns={columns}
      isLoading={isLoading}
      emptyTitle={t('No records')}
      toolbar={
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
            className='w-[120px]'
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
            className='w-[120px]'
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
            className='w-[180px]'
          />
          <Input
            type='number'
            min={1}
            placeholder={t('Channel ID')}
            value={filterForm.channelId}
            onChange={(event) =>
              setFilterForm((form) => ({
                ...form,
                channelId: event.target.value,
              }))
            }
            className='w-[120px]'
          />
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
      }
      getRowClassName={(row) =>
        row.original.commission_quota < 0 ? 'opacity-60' : undefined
      }
      skeletonKeyPrefix='commission-logs-skeleton'
      className='flex h-full min-h-0 flex-col overflow-hidden'
      tableClassName='min-h-0 flex-1 overflow-auto'
    />
  )
}

function TierDialog({
  open,
  currentRow,
  onOpenChange,
  onSuccess,
}: {
  open: boolean
  currentRow?: EmployeeTier
  onOpenChange: (open: boolean) => void
  onSuccess: () => void
}) {
  const { t } = useTranslation()
  const [level, setLevel] = useState('1')
  const [thresholdUsd, setThresholdUsd] = useState('0')
  const [rate, setRate] = useState('0.1')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!open) return
    setLevel(String(currentRow?.level ?? 1))
    setThresholdUsd(String(currentRow?.threshold_usd ?? 0))
    setRate(String(currentRow?.rate ?? 0.1))
  }, [currentRow, open])

  const submit = async () => {
    const nextLevel = Number(level)
    if (!Number.isFinite(nextLevel) || nextLevel < 1) {
      toast.error(t('Tier number must be at least 1'))
      return
    }
    const body = {
      level: nextLevel,
      threshold_usd: Number(thresholdUsd) || 0,
      rate: Number(rate) || 0,
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
      <DialogContent className='sm:max-w-[420px]' initialFocus={false}>
        <DialogHeader>
          <DialogTitle>
            {currentRow ? t('Edit Tier') : t('Create Tier')}
          </DialogTitle>
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
            <p className='text-muted-foreground text-xs'>
              {t('0.1 means 10%, applied after reaching this tier.')}
            </p>
          </div>
        </div>
        <DialogFooter>
          <Button variant='outline' onClick={() => onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button disabled={saving} onClick={submit}>
            {saving ? t('Saving...') : t('Save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function TiersTab() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [createOpen, setCreateOpen] = useState(false)
  const [editRow, setEditRow] = useState<EmployeeTier | undefined>()
  const [deleteRow, setDeleteRow] = useState<EmployeeTier | undefined>()

  const columns = useMemo(
    (): ColumnDef<EmployeeTier>[] => [
      {
        accessorKey: 'level',
        meta: { label: t('Tier'), mobileTitle: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Tier')} />
        ),
        cell: ({ row }) => (
          <Badge variant='outline'>
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
          <div className='flex gap-1'>
            <Button
              size='icon'
              variant='ghost'
              onClick={() => setEditRow(row.original)}
            >
              <Pencil className='h-4 w-4' />
            </Button>
            <Button
              size='icon'
              variant='ghost'
              onClick={() => setDeleteRow(row.original)}
            >
              <Trash2 className='text-destructive h-4 w-4' />
            </Button>
          </div>
        ),
      },
    ],
    [t]
  )

  const { data, isLoading } = useQuery({
    queryKey: ['employee-tiers'],
    queryFn: getEmployeeTiers,
  })

  const table = useReactTable({
    data: data?.data ?? [],
    columns,
    getCoreRowModel: getCoreRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
  })

  const refresh = () => qc.invalidateQueries({ queryKey: ['employee-tiers'] })

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
    <>
      <DataTablePage
        table={table}
        columns={columns}
        isLoading={isLoading}
        emptyTitle={t('No tier configuration yet')}
        emptyDescription={t('Click Add Tier to create one.')}
        toolbar={
          <div className='flex justify-end'>
            <Button size='sm' onClick={() => setCreateOpen(true)}>
              <PlusIcon className='mr-1 h-4 w-4' />
              {t('Add Tier')}
            </Button>
          </div>
        }
        showPagination={false}
        skeletonKeyPrefix='employee-tiers-skeleton'
        className='flex h-full min-h-0 flex-col overflow-hidden'
        tableClassName='min-h-0 flex-1 overflow-auto'
      />
      <TierDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onSuccess={refresh}
      />
      <TierDialog
        open={!!editRow}
        currentRow={editRow}
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
            <AlertDialogDescription>
              {t(
                'Employees already at this tier will not be downgraded automatically, but future upgrade checks will use the new configuration.'
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('Cancel')}</AlertDialogCancel>
            <AlertDialogAction onClick={handleDelete}>
              {t('Confirm')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

export function Employees() {
  const { t } = useTranslation()
  const [activeTab, setActiveTab] = useState('employees')

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Employee Management')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content className='overflow-hidden'>
        <Tabs
          value={activeTab}
          onValueChange={(value) => value && setActiveTab(value)}
          className='h-full min-h-0 overflow-hidden'
        >
          <TabsList>
            <TabsTrigger value='employees'>{t('Employees')}</TabsTrigger>
            <TabsTrigger value='tiers'>{t('Commission Tiers')}</TabsTrigger>
            <TabsTrigger value='commission'>{t('Commission Logs')}</TabsTrigger>
          </TabsList>
          <TabsContent value='employees' className='min-h-0 overflow-hidden'>
            {activeTab === 'employees' && <EmployeesTab />}
          </TabsContent>
          <TabsContent value='tiers' className='min-h-0 overflow-hidden'>
            {activeTab === 'tiers' && <TiersTab />}
          </TabsContent>
          <TabsContent value='commission' className='min-h-0 overflow-hidden'>
            {activeTab === 'commission' && <CommissionLogsTab />}
          </TabsContent>
        </Tabs>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

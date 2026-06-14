import { useEffect, useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  type ColumnDef,
  type PaginationState,
  getCoreRowModel,
  getPaginationRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { Pencil, PlusIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { DataTableColumnHeader, DataTablePage } from '@/components/data-table'
import { SectionPageLayout } from '@/components/layout'
import { formatBusinessAmount } from '@/features/business/format'
import {
  createMyCustomer,
  // getMyCustomerQuotaLogs,
  getMyCustomers,
  // transferQuotaToCustomer,
  updateMyCustomer,
  // updateMyCustomerUser,
} from '@/features/customers/api'
import type { CustomerProfile } from '@/features/customers/types'

function CreateCustomerDialog({
  open,
  onOpenChange,
  onSuccess,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSuccess: () => void
}) {
  const { t } = useTranslation()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [email, setEmail] = useState('')
  const [remark, setRemark] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!open) {
      setUsername('')
      setPassword('')
      setDisplayName('')
      setEmail('')
      setRemark('')
    }
  }, [open])

  const submit = async () => {
    if (!username || !password) {
      toast.error(t('Please enter username and password'))
      return
    }
    setSaving(true)
    try {
      const res = await createMyCustomer({
        username,
        password,
        display_name: displayName,
        email,
        remark,
      })
      if (!res.success) throw new Error(res.message ?? 'Failed')
      toast.success(t('Customer created'))
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
          <DialogTitle>{t('Add Customer')}</DialogTitle>
        </DialogHeader>
        <div className='flex flex-col gap-4'>
          <div className='flex flex-col gap-2'>
            <label className='text-sm font-medium'>{t('Username')}</label>
            <Input
              value={username}
              onChange={(e) => setUsername(e.target.value)}
            />
          </div>
          <div className='flex flex-col gap-2'>
            <label className='text-sm font-medium'>{t('Password')}</label>
            <Input
              type='password'
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </div>
          <div className='flex flex-col gap-2'>
            <label className='text-sm font-medium'>{t('Display Name')}</label>
            <Input
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
            />
          </div>
          <div className='flex flex-col gap-2'>
            <label className='text-sm font-medium'>{t('Email')}</label>
            <Input value={email} onChange={(e) => setEmail(e.target.value)} />
          </div>
          <div className='flex flex-col gap-2'>
            <label className='text-sm font-medium'>{t('Remark')}</label>
            <Input value={remark} onChange={(e) => setRemark(e.target.value)} />
          </div>
        </div>
        <DialogFooter>
          <Button variant='outline' onClick={() => onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button disabled={saving || !username || !password} onClick={submit}>
            {saving ? t('Saving...') : t('Save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function EditCustomerDialog({
  open,
  currentRow,
  onOpenChange,
  onSuccess,
}: {
  open: boolean
  currentRow?: CustomerProfile
  onOpenChange: (open: boolean) => void
  onSuccess: () => void
}) {
  const { t } = useTranslation()
  // Employee user-profile editing is temporarily disabled.
  // const [displayName, setDisplayName] = useState('')
  // const [email, setEmail] = useState('')
  // const [password, setPassword] = useState('')
  const [remark, setRemark] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    // Employee user-profile editing is temporarily disabled.
    // setDisplayName(currentRow?.display_name ?? '')
    // setEmail(currentRow?.email ?? '')
    // setPassword('')
    setRemark(currentRow?.remark ?? '')
  }, [currentRow, open])

  const submit = async () => {
    if (!currentRow) return
    setSaving(true)
    try {
      // Employee user-profile editing is temporarily disabled.
      // const r1 = await updateMyCustomerUser(currentRow.id, {
      //   display_name: displayName,
      //   email,
      //   password: password || undefined,
      //   remark,
      // })
      const r2 = await updateMyCustomer(currentRow.id, { remark })
      // if (!r1.success) throw new Error(r1.message ?? 'Failed')
      if (!r2.success) throw new Error(r2.message ?? 'Failed')
      toast.success(t('Customer updated'))
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
      <DialogContent className='sm:max-w-[500px]'>
        <DialogHeader>
          <DialogTitle>{t('Edit Customer')}</DialogTitle>
          <p className='text-muted-foreground text-sm'>
            {t('Update customer information')}
          </p>
        </DialogHeader>
        <div className='flex flex-col gap-6'>
          {/*
          Employee user-profile editing is temporarily disabled.
          <div className='flex flex-col gap-3'>
            <h3 className='text-sm font-semibold'>{t('Basic Information')}</h3>
            <div className='flex flex-col gap-2'>
              <label className='text-sm font-medium'>{t('Username')}</label>
              <Input value={currentRow?.username ?? `#${currentRow?.customer_user_id}`} disabled className='bg-muted' />
            </div>
            <div className='flex flex-col gap-2'>
              <label className='text-sm font-medium'>{t('Display Name')}</label>
              <Input
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
                placeholder={currentRow?.username}
              />
              <p className='text-muted-foreground text-xs'>{t('Leave blank to use username')}</p>
            </div>
            <div className='flex flex-col gap-2'>
              <label className='text-sm font-medium'>{t('Email')}</label>
              <Input value={email} onChange={(e) => setEmail(e.target.value)} />
            </div>
            <div className='flex flex-col gap-2'>
              <label className='text-sm font-medium'>{t('Password')}</label>
              <Input
                type='password'
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder={t('Leave blank to keep unchanged')}
              />
            </div>
          </div>
          */}

          <div className='flex flex-col gap-3'>
            <div className='flex flex-col gap-2'>
              <label className='text-sm font-medium'>{t('Remark')}</label>
              <Input
                value={remark}
                onChange={(e) => setRemark(e.target.value)}
              />
            </div>
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

/*
Employee quota adjustment is temporarily disabled.
const QUOTA_PER_DOLLAR = 500000

type AdjustMode = 'add' | 'subtract' | 'override'

function quotaToUsd(quota: number) {
  return quota / QUOTA_PER_DOLLAR
}
function usdToQuota(usd: number) {
  return Math.round(usd * QUOTA_PER_DOLLAR)
}

function TransferDialog({
  open,
  currentRow,
  onOpenChange,
  onSuccess,
}: {
  open: boolean
  currentRow?: CustomerProfile
  onOpenChange: (open: boolean) => void
  onSuccess: () => void
}) {
  const { t } = useTranslation()
  const [mode, setMode] = useState<AdjustMode>('add')
  const [usd, setUsd] = useState('')
  const [remark, setRemark] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    setMode('add')
    setUsd('')
    setRemark('')
  }, [currentRow, open])

  const currentQuota = currentRow?.quota ?? 0
  const currentUsd = quotaToUsd(currentQuota)
  const inputUsd = parseFloat(usd) || 0

  const previewUsd = (() => {
    if (mode === 'add') return currentUsd + inputUsd
    if (mode === 'subtract') return currentUsd - inputUsd
    return inputUsd // override
  })()

  const deltaUsd = previewUsd - currentUsd
  const deltaDisplay =
    deltaUsd === 0
      ? '+$0'
      : deltaUsd > 0
        ? `+$${deltaUsd.toFixed(4)}`
        : `-$${Math.abs(deltaUsd).toFixed(4)}`

  const submit = async () => {
    if (!currentRow) return
    setSaving(true)
    try {
      const quota = usdToQuota(inputUsd)
      const res = await transferQuotaToCustomer(currentRow.id, { quota, mode, remark })
      if (!res.success) throw new Error(res.message ?? 'Failed')
      toast.success(t('Adjustment completed'))
      onOpenChange(false)
      onSuccess()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Operation failed'))
    } finally {
      setSaving(false)
    }
  }

  const modeOptions: { value: AdjustMode; label: string }[] = [
    { value: 'add', label: t('Add') },
    { value: 'subtract', label: t('Subtract') },
    { value: 'override', label: t('Override') },
  ]

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='sm:max-w-[420px]'>
        <DialogHeader>
          <DialogTitle>{t('Adjust Quota')}</DialogTitle>
          <p className='text-muted-foreground text-sm'>{t('Select mode and enter amount')}</p>
        </DialogHeader>
        <div className='flex flex-col gap-4'>
          <div className='text-sm'>
            <span className='text-muted-foreground'>{t('Current quota')}: </span>
            <span className='font-medium'>${currentUsd.toFixed(4)}</span>
            <span className='text-muted-foreground mx-1'>{deltaDisplay}</span>
            <span className='font-medium'>= ${previewUsd.toFixed(4)}</span>
          </div>
          <div className='flex flex-col gap-2'>
            <label className='text-sm font-medium'>{t('Mode')}</label>
            <div className='flex gap-2'>
              {modeOptions.map((opt) => (
                <Button
                  key={opt.value}
                  size='sm'
                  variant={mode === opt.value ? 'default' : 'outline'}
                  onClick={() => setMode(opt.value)}
                  type='button'
                >
                  {opt.label}
                </Button>
              ))}
            </div>
          </div>
          <div className='flex flex-col gap-2'>
            <label className='text-sm font-medium'>{t('Amount (USD)')}</label>
            <Input
              type='number'
              min='0'
              step='0.0001'
              placeholder={t('Enter amount (USD)')}
              value={usd}
              onChange={(e) => setUsd(e.target.value)}
            />
          </div>
          <div className='flex flex-col gap-2'>
            <label className='text-sm font-medium'>{t('Remark')}</label>
            <Input
              value={remark}
              onChange={(e) => setRemark(e.target.value)}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant='outline' onClick={() => onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button disabled={saving || inputUsd <= 0} onClick={submit}>
            {saving ? t('Saving...') : t('Confirm')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
*/

function useMyCustomerColumns({
  onEdit,
}: {
  onEdit: (row: CustomerProfile) => void
}) {
  const { t } = useTranslation()

  return useMemo(
    (): ColumnDef<CustomerProfile>[] => [
      {
        accessorFn: (row) => row.username || `#${row.customer_user_id}`,
        id: 'customer',
        meta: { label: t('Customer'), mobileTitle: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Customer')} />
        ),
        cell: ({ row }) => (
          <div className='flex flex-col'>
            <span>
              {row.original.username || `#${row.original.customer_user_id}`}
            </span>
            <span className='text-muted-foreground text-xs'>
              #{row.original.customer_user_id}
              {row.original.email ? ` / ${row.original.email}` : ''}
            </span>
          </div>
        ),
      },
      {
        accessorKey: 'quota',
        meta: { label: t('Balance') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Balance')} />
        ),
        cell: ({ row }) => formatBusinessAmount(row.original.quota),
      },
      {
        accessorKey: 'used_quota',
        meta: { label: t('Used Quota') },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Used Quota')} />
        ),
        cell: ({ row }) => formatBusinessAmount(row.original.used_quota),
      },
      {
        accessorKey: 'commission_quota',
        meta: { label: t('Commission'), mobileBadge: true },
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Commission')} />
        ),
        cell: ({ row }) => (
          <span className='font-medium text-green-600'>
            {row.original.commission_quota
              ? formatBusinessAmount(row.original.commission_quota)
              : '-'}
          </span>
        ),
      },
      {
        accessorKey: 'remark',
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
        cell: ({ row }) => (
          <Button
            size='icon'
            variant='ghost'
            onClick={() => onEdit(row.original)}
          >
            <Pencil />
          </Button>
        ),
      },
    ],
    [onEdit, t]
  )
}

function MyCustomersTab() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [createOpen, setCreateOpen] = useState(false)
  const [editRow, setEditRow] = useState<CustomerProfile | undefined>()
  const [filterForm, setFilterForm] = useState({
    keyword: '',
  })
  const [filters, setFilters] = useState<{
    keyword?: string
  }>({})
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: 10,
  })
  const columns = useMyCustomerColumns({ onEdit: setEditRow })
  // Employee quota adjustment is temporarily disabled.
  // const [transferRow, setTransferRow] = useState<CustomerProfile | undefined>()

  const { data, isLoading } = useQuery({
    queryKey: ['my-customers', pagination, filters],
    queryFn: () =>
      getMyCustomers({
        page: pagination.pageIndex + 1,
        page_size: pagination.pageSize,
        ...filters,
      }),
  })
  const customers = data?.data?.items ?? []
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ['my-customers'] })
    qc.invalidateQueries({ queryKey: ['my-customer-quota-logs'] })
  }

  const table = useReactTable({
    data: customers,
    columns,
    rowCount: data?.data?.total ?? 0,
    state: { pagination },
    onPaginationChange: setPagination,
    manualPagination: true,
    getCoreRowModel: getCoreRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
  })

  const applyFilters = () => {
    setFilters({
      ...(filterForm.keyword.trim()
        ? { keyword: filterForm.keyword.trim() }
        : {}),
    })
    setPagination((current) => ({ ...current, pageIndex: 0 }))
  }

  const resetFilters = () => {
    setFilterForm({ keyword: '' })
    setFilters({})
    setPagination((current) => ({ ...current, pageIndex: 0 }))
  }

  return (
    <>
      <DataTablePage
        table={table}
        columns={columns}
        isLoading={isLoading}
        emptyTitle={t('No customers yet')}
        paginationInFooter={false}
        toolbar={
          <div className='flex flex-wrap items-center justify-end gap-2'>
            <Input
              placeholder={t('Customer ID / Account name / Remark')}
              value={filterForm.keyword}
              onChange={(event) =>
                setFilterForm((form) => ({
                  ...form,
                  keyword: event.target.value,
                }))
              }
              className='w-[220px]'
            />
            <Button size='sm' variant='outline' onClick={applyFilters}>
              {t('Search')}
            </Button>
            <Button size='sm' variant='outline' onClick={resetFilters}>
              {t('Reset')}
            </Button>
            <Button size='sm' onClick={() => setCreateOpen(true)}>
              <PlusIcon data-icon='inline-start' />
              {t('Add Customer')}
            </Button>
          </div>
        }
        skeletonKeyPrefix='my-customers-skeleton'
        className='flex h-full min-h-0 flex-col overflow-hidden'
        tableClassName='min-h-0 flex-1 overflow-auto'
      />
      <CreateCustomerDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onSuccess={refresh}
      />
      {/*
      Employee quota adjustment is temporarily disabled.
      <TransferDialog
        open={!!transferRow}
        currentRow={transferRow}
        onOpenChange={(open) => !open && setTransferRow(undefined)}
        onSuccess={refresh}
      />
      */}
      <EditCustomerDialog
        open={!!editRow}
        currentRow={editRow}
        onOpenChange={(open) => !open && setEditRow(undefined)}
        onSuccess={refresh}
      />
    </>
  )
}

/*
Employee customer tabs and recharge logs are temporarily disabled.
function MyRechargeLogsTab() {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const pageSize = 20
  const { data, isLoading } = useQuery({
    queryKey: ['my-customer-quota-logs', page],
    queryFn: () => getMyCustomerQuotaLogs({ page, page_size: pageSize }),
  })
  const logs = data?.data?.items ?? []
  const total = data?.data?.total ?? 0

  return (
    <div className='flex flex-col gap-4'>
      <LogsTable logs={logs} loading={isLoading} />
      <div className='text-muted-foreground flex items-center justify-between text-sm'>
        <span>
          {t('Total')}: {total}
        </span>
        <div className='flex gap-2'>
          <Button
            size='sm'
            variant='outline'
            disabled={page <= 1}
            onClick={() => setPage((p) => p - 1)}
          >
            {t('Previous')}
          </Button>
          <Button
            size='sm'
            variant='outline'
            disabled={page * pageSize >= total}
            onClick={() => setPage((p) => p + 1)}
          >
            {t('Next')}
          </Button>
        </div>
      </div>
    </div>
  )
}
*/

export function CustomerConsole() {
  const { t } = useTranslation()

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('My Customers')}</SectionPageLayout.Title>
      <SectionPageLayout.Content className='overflow-hidden'>
        <div className='h-full min-h-0 overflow-hidden'>
          <MyCustomersTab />
        </div>
        {/*
        Employee customer tabs are temporarily disabled.
        <Tabs defaultValue='customers'>
          <TabsList>
            <TabsTrigger value='customers'>{t('Customers')}</TabsTrigger>
            <TabsTrigger value='logs'>{t('Recharge Logs')}</TabsTrigger>
          </TabsList>
          <TabsContent value='customers' className='mt-4'>
            <MyCustomersTab />
          </TabsContent>
          <TabsContent value='logs' className='mt-4'>
            <MyRechargeLogsTab />
          </TabsContent>
        </Tabs>
        */}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

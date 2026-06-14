import { useEffect, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Pencil, PlusIcon, Trash2, UserRoundPen } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { formatQuota } from '@/lib/format'
import { cn } from '@/lib/utils'
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { SectionPageLayout } from '@/components/layout'
import { searchUsers } from '@/features/users/api'
import {
  createAdminCustomer,
  deleteAdminCustomer,
  getAdminCustomerQuotaLogs,
  getAdminCustomers,
  updateAdminCustomer,
  updateAdminCustomerUser,
} from './api'
import type { CustomerProfile, CustomerQuotaLog } from './types'

function formatTs(ts?: number) {
  if (!ts) return '-'
  return new Date(ts * 1000).toLocaleString()
}

function StatusBadge({ status }: { status: number }) {
  const { t } = useTranslation()
  return status === 1 ? (
    <Badge>{t('Enabled')}</Badge>
  ) : (
    <Badge variant='secondary'>{t('Disabled')}</Badge>
  )
}

function UserPicker({
  value,
  role,
  onSelect,
}: {
  value?: number
  role?: string
  onSelect: (id: number) => void
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [keyword, setKeyword] = useState('')
  const [debounced, setDebounced] = useState('')
  const [label, setLabel] = useState('')
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const timer = setTimeout(() => setDebounced(keyword.trim()), 300)
    return () => clearTimeout(timer)
  }, [keyword])

  useEffect(() => {
    if (!open) return
    const handler = (event: MouseEvent) => {
      if (!containerRef.current?.contains(event.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [open])

  const { data, isFetching } = useQuery({
    queryKey: ['customer-user-search', role ?? 'all', debounced],
    queryFn: () =>
      searchUsers({ keyword: debounced, role, status: '1', page_size: 20 }),
    enabled: open,
  })
  const users = data?.data?.items ?? []

  return (
    <div ref={containerRef} className='relative'>
      <Input
        autoComplete='off'
        value={open ? keyword : label}
        placeholder={t('Search username / display name / email')}
        onChange={(event) => {
          setKeyword(event.target.value)
          if (!open) setOpen(true)
        }}
        onFocus={() => {
          setKeyword('')
          setOpen(true)
        }}
      />
      {open && (
        <div className='bg-popover text-popover-foreground absolute top-full z-50 mt-1 w-full rounded-md border shadow-md'>
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
              {users.map((user) => (
                <li
                  key={user.id}
                  className={cn(
                    'hover:bg-accent hover:text-accent-foreground flex cursor-pointer flex-col gap-0.5 rounded-sm px-2 py-1.5 text-sm',
                    value === user.id && 'bg-accent'
                  )}
                  onMouseDown={(event) => {
                    event.preventDefault()
                    setLabel(
                      `${user.username}${user.display_name ? ` (${user.display_name})` : ''} #${user.id}`
                    )
                    onSelect(user.id)
                    setOpen(false)
                  }}
                >
                  <span className='font-medium'>
                    {user.username}
                    {user.display_name ? ` (${user.display_name})` : ''}
                  </span>
                  <span className='text-muted-foreground text-xs'>
                    #{user.id}
                    {user.email ? ` / ${user.email}` : ''}
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

function CustomerFormDialog({
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
  const isUpdate = !!currentRow
  const [employeeUserId, setEmployeeUserId] = useState<number | undefined>()
  const [customerUserId, setCustomerUserId] = useState<number | undefined>()
  const [status, setStatus] = useState(1)
  const [remark, setRemark] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    setStatus(currentRow?.status ?? 1)
    setRemark(currentRow?.remark ?? '')
  }, [currentRow, open])

  const submit = async () => {
    setSaving(true)
    try {
      const res = isUpdate
        ? await updateAdminCustomer(currentRow.id, { status, remark })
        : await createAdminCustomer({
            employee_user_id: Number(employeeUserId),
            customer_user_id: Number(customerUserId),
            remark,
          })
      if (!res.success) throw new Error(res.message ?? 'Failed')
      toast.success(isUpdate ? t('Customer updated') : t('Customer created'))
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
      <DialogContent className='sm:max-w-[480px]' initialFocus={false}>
        <DialogHeader>
          <DialogTitle>
            {isUpdate ? t('Edit Customer') : t('Create Customer')}
          </DialogTitle>
        </DialogHeader>
        <div className='flex flex-col gap-4'>
          {!isUpdate && (
            <>
              <div className='flex flex-col gap-2'>
                <label className='text-sm font-medium'>{t('Employee')}</label>
                <UserPicker
                  role='1'
                  value={employeeUserId}
                  onSelect={setEmployeeUserId}
                />
              </div>
              <div className='flex flex-col gap-2'>
                <label className='text-sm font-medium'>{t('Customer')}</label>
                <UserPicker
                  role='1'
                  value={customerUserId}
                  onSelect={setCustomerUserId}
                />
              </div>
            </>
          )}
          {isUpdate && (
            <div className='flex flex-col gap-2'>
              <label className='text-sm font-medium'>{t('Status')}</label>
              <Select
                value={String(status)}
                onValueChange={(v) => setStatus(Number(v))}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value='1'>{t('Enabled')}</SelectItem>
                  <SelectItem value='2'>{t('Disabled')}</SelectItem>
                </SelectContent>
              </Select>
            </div>
          )}
          <div className='flex flex-col gap-2'>
            <label className='text-sm font-medium'>{t('Remark')}</label>
            <Input
              value={remark}
              onChange={(event) => setRemark(event.target.value)}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant='outline' onClick={() => onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button
            disabled={
              saving || (!isUpdate && (!employeeUserId || !customerUserId))
            }
            onClick={submit}
          >
            {saving ? t('Saving...') : t('Save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function CustomerUserDialog({
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
  const [displayName, setDisplayName] = useState('')
  const [email, setEmail] = useState('')
  const [remark, setRemark] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    setDisplayName(currentRow?.display_name ?? '')
    setEmail(currentRow?.email ?? '')
    setRemark(currentRow?.remark ?? '')
  }, [currentRow, open])

  const submit = async () => {
    if (!currentRow) return
    setSaving(true)
    try {
      const res = await updateAdminCustomerUser(currentRow.id, {
        display_name: displayName,
        email,
        remark,
      })
      if (!res.success) throw new Error(res.message ?? 'Failed')
      toast.success(t('Customer user updated'))
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
      <DialogContent className='sm:max-w-[480px]'>
        <DialogHeader>
          <DialogTitle>{t('Edit Customer User')}</DialogTitle>
        </DialogHeader>
        <div className='flex flex-col gap-4'>
          <div className='flex flex-col gap-2'>
            <label className='text-sm font-medium'>{t('Display Name')}</label>
            <Input
              value={displayName}
              onChange={(event) => setDisplayName(event.target.value)}
            />
          </div>
          <div className='flex flex-col gap-2'>
            <label className='text-sm font-medium'>{t('Email')}</label>
            <Input
              value={email}
              onChange={(event) => setEmail(event.target.value)}
            />
          </div>
          <div className='flex flex-col gap-2'>
            <label className='text-sm font-medium'>{t('Remark')}</label>
            <Input
              value={remark}
              onChange={(event) => setRemark(event.target.value)}
            />
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

function CustomersTab() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [page, setPage] = useState(1)
  const [createOpen, setCreateOpen] = useState(false)
  const [editRow, setEditRow] = useState<CustomerProfile | undefined>()
  const [editUserRow, setEditUserRow] = useState<CustomerProfile | undefined>()
  const pageSize = 20

  const { data, isLoading } = useQuery({
    queryKey: ['admin-customers', page],
    queryFn: () => getAdminCustomers({ page, page_size: pageSize }),
  })

  const customers = data?.data?.items ?? []
  const total = data?.data?.total ?? 0

  const refresh = () => qc.invalidateQueries({ queryKey: ['admin-customers'] })

  const remove = async (row: CustomerProfile) => {
    try {
      const res = await deleteAdminCustomer(row.id)
      if (!res.success) throw new Error(res.message)
      toast.success(t('Customer deleted'))
      refresh()
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Operation failed')
      )
    }
  }

  return (
    <div className='flex flex-col gap-4'>
      <div className='flex justify-end'>
        <Button size='sm' onClick={() => setCreateOpen(true)}>
          <PlusIcon data-icon='inline-start' />
          {t('Add Customer')}
        </Button>
      </div>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('ID')}</TableHead>
            <TableHead>{t('Customer')}</TableHead>
            <TableHead>{t('Employee')}</TableHead>
            <TableHead>{t('Balance')}</TableHead>
            <TableHead>{t('Used Quota')}</TableHead>
            <TableHead>{t('Status')}</TableHead>
            <TableHead>{t('Remark')}</TableHead>
            <TableHead>{t('Actions')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {isLoading && (
            <TableRow>
              <TableCell
                colSpan={8}
                className='text-muted-foreground text-center'
              >
                {t('Loading...')}
              </TableCell>
            </TableRow>
          )}
          {!isLoading && customers.length === 0 && (
            <TableRow>
              <TableCell
                colSpan={8}
                className='text-muted-foreground text-center'
              >
                {t('No customers yet')}
              </TableCell>
            </TableRow>
          )}
          {customers.map((row) => (
            <TableRow key={row.id}>
              <TableCell>{row.id}</TableCell>
              <TableCell>
                <div className='flex flex-col'>
                  <span>{row.username || `#${row.customer_user_id}`}</span>
                  <span className='text-muted-foreground text-xs'>
                    #{row.customer_user_id}
                    {row.email ? ` / ${row.email}` : ''}
                  </span>
                </div>
              </TableCell>
              <TableCell>
                {row.employee_username || `#${row.employee_user_id}`}
              </TableCell>
              <TableCell>{formatQuota(row.quota)}</TableCell>
              <TableCell>{formatQuota(row.used_quota)}</TableCell>
              <TableCell>
                <StatusBadge status={row.status} />
              </TableCell>
              <TableCell className='max-w-[140px] truncate'>
                {row.remark || '-'}
              </TableCell>
              <TableCell>
                <div className='flex gap-1'>
                  <Button
                    size='icon'
                    variant='ghost'
                    onClick={() => setEditRow(row)}
                  >
                    <Pencil />
                  </Button>
                  <Button
                    size='icon'
                    variant='ghost'
                    onClick={() => setEditUserRow(row)}
                  >
                    <UserRoundPen />
                  </Button>
                  <Button
                    size='icon'
                    variant='ghost'
                    onClick={() => remove(row)}
                  >
                    <Trash2 className='text-destructive' />
                  </Button>
                </div>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
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
      <CustomerFormDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onSuccess={refresh}
      />
      <CustomerFormDialog
        open={!!editRow}
        currentRow={editRow}
        onOpenChange={(open) => !open && setEditRow(undefined)}
        onSuccess={refresh}
      />
      <CustomerUserDialog
        open={!!editUserRow}
        currentRow={editUserRow}
        onOpenChange={(open) => !open && setEditUserRow(undefined)}
        onSuccess={refresh}
      />
    </div>
  )
}

function LogsTable({
  logs,
  loading,
}: {
  logs: CustomerQuotaLog[]
  loading: boolean
}) {
  const { t } = useTranslation()
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>{t('Time')}</TableHead>
          <TableHead>{t('Employee')}</TableHead>
          <TableHead>{t('Customer')}</TableHead>
          <TableHead>{t('Amount')}</TableHead>
          <TableHead>{t('Employee Balance')}</TableHead>
          <TableHead>{t('Customer Balance')}</TableHead>
          <TableHead>{t('Remark')}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {loading && (
          <TableRow>
            <TableCell
              colSpan={7}
              className='text-muted-foreground text-center'
            >
              {t('Loading...')}
            </TableCell>
          </TableRow>
        )}
        {!loading && logs.length === 0 && (
          <TableRow>
            <TableCell
              colSpan={7}
              className='text-muted-foreground text-center'
            >
              {t('No records')}
            </TableCell>
          </TableRow>
        )}
        {logs.map((log) => (
          <TableRow key={log.id}>
            <TableCell className='text-xs'>
              {formatTs(log.created_at)}
            </TableCell>
            <TableCell>
              {log.employee_username || `#${log.employee_user_id}`}
            </TableCell>
            <TableCell>
              {log.customer_username || `#${log.customer_user_id}`}
            </TableCell>
            <TableCell
              className={`font-medium ${log.quota_delta < 0 ? 'text-red-500' : 'text-green-600'}`}
            >
              {log.quota_delta >= 0 ? '+' : ''}{formatQuota(log.quota_delta)}
            </TableCell>
            <TableCell>
              {formatQuota(log.employee_before_quota)}
              {' -> '}
              {formatQuota(log.employee_after_quota)}
            </TableCell>
            <TableCell>
              {formatQuota(log.customer_before_quota)}
              {' -> '}
              {formatQuota(log.customer_after_quota)}
            </TableCell>
            <TableCell className='max-w-[160px] truncate'>
              {log.remark || '-'}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

function QuotaLogsTab() {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const pageSize = 20
  const { data, isLoading } = useQuery({
    queryKey: ['admin-customer-quota-logs', page],
    queryFn: () => getAdminCustomerQuotaLogs({ page, page_size: pageSize }),
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

export function Customers() {
  const { t } = useTranslation()
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Customer Management')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <Tabs defaultValue='customers'>
          <TabsList>
            <TabsTrigger value='customers'>{t('Customers')}</TabsTrigger>
            <TabsTrigger value='logs'>{t('Recharge Logs')}</TabsTrigger>
          </TabsList>
          <TabsContent value='customers' className='mt-4'>
            <CustomersTab />
          </TabsContent>
          <TabsContent value='logs' className='mt-4'>
            <QuotaLogsTab />
          </TabsContent>
        </Tabs>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

export { LogsTable, StatusBadge }

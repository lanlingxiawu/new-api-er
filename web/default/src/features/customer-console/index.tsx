import { useEffect, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Pencil, Send, UserRoundPen } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { formatQuota } from '@/lib/format'
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
import { LogsTable, StatusBadge } from '@/features/customers'
import {
  getMyCustomerQuotaLogs,
  getMyCustomers,
  transferQuotaToCustomer,
  updateMyCustomer,
  updateMyCustomerUser,
} from '@/features/customers/api'
import type { CustomerProfile } from '@/features/customers/types'

function CustomerProfileDialog({
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
  const [status, setStatus] = useState(1)
  const [remark, setRemark] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    setStatus(currentRow?.status ?? 1)
    setRemark(currentRow?.remark ?? '')
  }, [currentRow, open])

  const submit = async () => {
    if (!currentRow) return
    setSaving(true)
    try {
      const res = await updateMyCustomer(currentRow.id, { status, remark })
      if (!res.success) throw new Error(res.message ?? 'Failed')
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
      <DialogContent className='sm:max-w-[420px]'>
        <DialogHeader>
          <DialogTitle>{t('Edit Customer')}</DialogTitle>
        </DialogHeader>
        <div className='flex flex-col gap-4'>
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
      const res = await updateMyCustomerUser(currentRow.id, {
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
      <DialogContent className='sm:max-w-[420px]'>
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
  const [quota, setQuota] = useState(0)
  const [remark, setRemark] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    setQuota(0)
    setRemark('')
  }, [currentRow, open])

  const submit = async () => {
    if (!currentRow) return
    setSaving(true)
    try {
      const res = await transferQuotaToCustomer(currentRow.id, {
        quota,
        remark,
      })
      if (!res.success) throw new Error(res.message ?? 'Failed')
      toast.success(t('Recharge completed'))
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
      <DialogContent className='sm:max-w-[420px]'>
        <DialogHeader>
          <DialogTitle>{t('Recharge Customer')}</DialogTitle>
        </DialogHeader>
        <div className='flex flex-col gap-4'>
          <div className='rounded-md border p-3 text-sm'>
            {currentRow?.username || `#${currentRow?.customer_user_id}`} /{' '}
            {t('Balance')}: {formatQuota(currentRow?.quota ?? 0)}
          </div>
          <div className='flex flex-col gap-2'>
            <label className='text-sm font-medium'>{t('Amount')}</label>
            <Input
              type='number'
              min='1'
              value={quota || ''}
              onChange={(event) => setQuota(parseInt(event.target.value) || 0)}
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
          <Button disabled={saving || quota <= 0} onClick={submit}>
            {saving ? t('Saving...') : t('Confirm')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function MyCustomersTab() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [page, setPage] = useState(1)
  const [editRow, setEditRow] = useState<CustomerProfile | undefined>()
  const [editUserRow, setEditUserRow] = useState<CustomerProfile | undefined>()
  const [transferRow, setTransferRow] = useState<CustomerProfile | undefined>()
  const pageSize = 20

  const { data, isLoading } = useQuery({
    queryKey: ['my-customers', page],
    queryFn: () => getMyCustomers({ page, page_size: pageSize }),
  })
  const customers = data?.data?.items ?? []
  const total = data?.data?.total ?? 0
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ['my-customers'] })
    qc.invalidateQueries({ queryKey: ['my-customer-quota-logs'] })
  }

  return (
    <div className='flex flex-col gap-4'>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('Customer')}</TableHead>
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
                colSpan={6}
                className='text-muted-foreground text-center'
              >
                {t('Loading...')}
              </TableCell>
            </TableRow>
          )}
          {!isLoading && customers.length === 0 && (
            <TableRow>
              <TableCell
                colSpan={6}
                className='text-muted-foreground text-center'
              >
                {t('No customers yet')}
              </TableCell>
            </TableRow>
          )}
          {customers.map((row) => (
            <TableRow key={row.id}>
              <TableCell>
                <div className='flex flex-col'>
                  <span>{row.username || `#${row.customer_user_id}`}</span>
                  <span className='text-muted-foreground text-xs'>
                    #{row.customer_user_id}
                    {row.email ? ` / ${row.email}` : ''}
                  </span>
                </div>
              </TableCell>
              <TableCell>{formatQuota(row.quota)}</TableCell>
              <TableCell>{formatQuota(row.used_quota)}</TableCell>
              <TableCell>
                <StatusBadge status={row.status} />
              </TableCell>
              <TableCell className='max-w-[160px] truncate'>
                {row.remark || '-'}
              </TableCell>
              <TableCell>
                <div className='flex gap-1'>
                  <Button
                    size='icon'
                    variant='ghost'
                    onClick={() => setTransferRow(row)}
                  >
                    <Send />
                  </Button>
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
      <TransferDialog
        open={!!transferRow}
        currentRow={transferRow}
        onOpenChange={(open) => !open && setTransferRow(undefined)}
        onSuccess={refresh}
      />
      <CustomerProfileDialog
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

export function CustomerConsole() {
  const { t } = useTranslation()

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('My Customers')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
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
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

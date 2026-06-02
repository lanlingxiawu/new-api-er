import { useEffect, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Pencil, PlusIcon, Send, UserRoundPen } from 'lucide-react'
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
  createMyCustomer,
  getMyCustomerQuotaLogs,
  getMyCustomers,
  transferQuotaToCustomer,
  updateMyCustomer,
  updateMyCustomerUser,
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
    setSaving(true)
    try {
      const res = await createMyCustomer({ username, password, display_name: displayName, email, remark })
      if (!res.success) throw new Error(res.message ?? 'Failed')
      toast.success(t('Customer created'))
      onOpenChange(false)
      onSuccess()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Operation failed'))
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
            <Input value={username} onChange={(e) => setUsername(e.target.value)} />
          </div>
          <div className='flex flex-col gap-2'>
            <label className='text-sm font-medium'>{t('Password')}</label>
            <Input type='password' value={password} onChange={(e) => setPassword(e.target.value)} />
          </div>
          <div className='flex flex-col gap-2'>
            <label className='text-sm font-medium'>{t('Display Name')}</label>
            <Input value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
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
          <Button variant='outline' onClick={() => onOpenChange(false)}>{t('Cancel')}</Button>
          <Button disabled={saving || !username || !password} onClick={submit}>
            {saving ? t('Saving...') : t('Save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

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

function MyCustomersTab() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [page, setPage] = useState(1)
  const [createOpen, setCreateOpen] = useState(false)
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
      <div className='flex justify-end'>
        <Button size='sm' onClick={() => setCreateOpen(true)}>
          <PlusIcon data-icon='inline-start' />
          {t('Add Customer')}
        </Button>
      </div>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('Customer')}</TableHead>
            <TableHead>{t('Balance')}</TableHead>
            <TableHead>{t('Used Quota')}</TableHead>
            <TableHead>{t('Commission')}</TableHead>
            <TableHead>{t('Status')}</TableHead>
            <TableHead>{t('Remark')}</TableHead>
            <TableHead>{t('Actions')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {isLoading && (
            <TableRow>
              <TableCell
                colSpan={7}
                className='text-muted-foreground text-center'
              >
                {t('Loading...')}
              </TableCell>
            </TableRow>
          )}
          {!isLoading && customers.length === 0 && (
            <TableRow>
              <TableCell
                colSpan={7}
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
              <TableCell className='font-medium text-green-600'>
                {row.commission_quota ? formatQuota(row.commission_quota) : '-'}
              </TableCell>
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
      <CreateCustomerDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onSuccess={refresh}
      />
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

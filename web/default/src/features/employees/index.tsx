import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { PlusIcon, Pencil, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
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
import { formatQuota } from '@/lib/format'
import { getEmployees, getCommissionLogs, deleteEmployee } from './api'
import type { EmployeeProfile, CommissionLog } from './types'
import { EmployeeFormDialog } from './components/employee-form-dialog'

// ── Helpers ──────────────────────────────────────────────────────────────────

function formatTs(ts: number) {
  if (!ts) return '-'
  return new Date(ts * 1000).toLocaleString()
}

function StatusBadge({ status }: { status: number }) {
  const { t } = useTranslation()
  return status === 1 ? (
    <Badge variant='default'>{t('Enabled')}</Badge>
  ) : (
    <Badge variant='secondary'>{t('Disabled')}</Badge>
  )
}

// ── Employees Tab ─────────────────────────────────────────────────────────────

function EmployeesTab() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [createOpen, setCreateOpen] = useState(false)
  const [editRow, setEditRow] = useState<EmployeeProfile | undefined>()
  const [deleteRow, setDeleteRow] = useState<EmployeeProfile | undefined>()

  const { data, isLoading } = useQuery({
    queryKey: ['employees'],
    queryFn: () => getEmployees(1, 100),
  })

  const employees = data?.data?.items ?? []

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

  return (
    <div className='space-y-4'>
      <div className='flex justify-end'>
        <Button size='sm' onClick={() => setCreateOpen(true)}>
          <PlusIcon className='mr-1 h-4 w-4' />
          {t('Add Employee')}
        </Button>
      </div>

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('ID')}</TableHead>
            <TableHead>{t('User ID')}</TableHead>
            <TableHead>{t('Username')}</TableHead>
            <TableHead>{t('Commission Rate')}</TableHead>
            <TableHead>{t('Target Quota')}</TableHead>
            <TableHead>{t('Status')}</TableHead>
            <TableHead>{t('Remark')}</TableHead>
            <TableHead>{t('Actions')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {isLoading && (
            <TableRow>
              <TableCell colSpan={8} className='text-center text-muted-foreground'>
                {t('Loading...')}
              </TableCell>
            </TableRow>
          )}
          {!isLoading && employees.length === 0 && (
            <TableRow>
              <TableCell colSpan={8} className='text-center text-muted-foreground'>
                {t('No employees yet')}
              </TableCell>
            </TableRow>
          )}
          {employees.map((emp) => (
            <TableRow key={emp.id}>
              <TableCell>{emp.id}</TableCell>
              <TableCell>{emp.user_id}</TableCell>
              <TableCell>{emp.username ?? '-'}</TableCell>
              <TableCell>{(emp.commission_rate * 100).toFixed(1)}%</TableCell>
              <TableCell>
                {emp.target_quota
                  ? formatQuota(emp.target_quota)
                  : t('No limit')}
              </TableCell>
              <TableCell>
                <StatusBadge status={emp.status} />
              </TableCell>
              <TableCell className='max-w-[120px] truncate'>
                {emp.remark || '-'}
              </TableCell>
              <TableCell>
                <div className='flex gap-1'>
                  <Button
                    size='icon'
                    variant='ghost'
                    onClick={() => setEditRow(emp)}
                  >
                    <Pencil className='h-4 w-4' />
                  </Button>
                  <Button
                    size='icon'
                    variant='ghost'
                    onClick={() => setDeleteRow(emp)}
                  >
                    <Trash2 className='h-4 w-4 text-destructive' />
                  </Button>
                </div>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>

      <EmployeeFormDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onSuccess={() => qc.invalidateQueries({ queryKey: ['employees'] })}
      />
      <EmployeeFormDialog
        open={!!editRow}
        onOpenChange={(o) => !o && setEditRow(undefined)}
        currentRow={editRow}
        onSuccess={() => qc.invalidateQueries({ queryKey: ['employees'] })}
      />
      <AlertDialog open={!!deleteRow} onOpenChange={(o) => !o && setDeleteRow(undefined)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Disable Employee')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t('This will disable the employee. Historical commission logs are retained.')}
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
    </div>
  )
}

// ── Commission Logs Tab ───────────────────────────────────────────────────────

function CommissionLogsTab() {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const pageSize = 20

  const { data, isLoading } = useQuery({
    queryKey: ['admin-commission-logs', page],
    queryFn: () => getCommissionLogs({ page, page_size: pageSize }),
  })

  const logs = data?.data?.items ?? []
  const total = data?.data?.total ?? 0

  return (
    <div className='space-y-4'>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('Time')}</TableHead>
            <TableHead>{t('Employee UID')}</TableHead>
            <TableHead>{t('Customer UID')}</TableHead>
            <TableHead>{t('Model')}</TableHead>
            <TableHead>{t('Revenue')}</TableHead>
            <TableHead>{t('Cost')}</TableHead>
            <TableHead>{t('Profit')}</TableHead>
            <TableHead>{t('Commission')}</TableHead>
            <TableHead>{t('Rate')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {isLoading && (
            <TableRow>
              <TableCell colSpan={9} className='text-center text-muted-foreground'>
                {t('Loading...')}
              </TableCell>
            </TableRow>
          )}
          {!isLoading && logs.length === 0 && (
            <TableRow>
              <TableCell colSpan={9} className='text-center text-muted-foreground'>
                {t('No records')}
              </TableCell>
            </TableRow>
          )}
          {logs.map((log) => (
            <TableRow key={log.id} className={log.commission_quota < 0 ? 'opacity-60' : ''}>
              <TableCell className='text-xs'>{formatTs(log.created_at)}</TableCell>
              <TableCell>{log.employee_user_id}</TableCell>
              <TableCell>{log.customer_user_id}</TableCell>
              <TableCell className='max-w-[120px] truncate text-xs'>
                {log.model_name || '-'}
              </TableCell>
              <TableCell>{formatQuota(log.revenue_quota)}</TableCell>
              <TableCell>{formatQuota(log.cost_quota)}</TableCell>
              <TableCell>{formatQuota(log.profit_quota)}</TableCell>
              <TableCell
                className={log.commission_quota < 0 ? 'text-destructive' : 'text-green-600'}
              >
                {formatQuota(log.commission_quota)}
              </TableCell>
              <TableCell>{(log.commission_rate * 100).toFixed(1)}%</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>

      <div className='flex items-center justify-between text-sm text-muted-foreground'>
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

// ── Main Page ─────────────────────────────────────────────────────────────────

export function Employees() {
  const { t } = useTranslation()

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Employee Management')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <Tabs defaultValue='employees'>
          <TabsList>
            <TabsTrigger value='employees'>{t('Employees')}</TabsTrigger>
            <TabsTrigger value='commission'>{t('Commission Logs')}</TabsTrigger>
          </TabsList>
          <TabsContent value='employees' className='mt-4'>
            <EmployeesTab />
          </TabsContent>
          <TabsContent value='commission' className='mt-4'>
            <CommissionLogsTab />
          </TabsContent>
        </Tabs>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
                                                                                                                                                                                                                                                                                                                                                                
import { useEffect, useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Undo2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
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
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { BusinessAmount } from '@/features/business/amount-display'
import {
  addEmployeePerformance,
  getEmployeePerformanceAdjustments,
  revertEmployeePerformance,
} from '../api'
import type { CommissionLog, EmployeeProfile } from '../types'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  employee?: EmployeeProfile
  onSuccess?: () => void
}

// 生成最近 N 个自然月的 YYYY-MM 选项（含当月），用于历史周期补录。
function recentMonths(count: number): string[] {
  const out: string[] = []
  const now = new Date()
  let y = now.getFullYear()
  let m = now.getMonth() // 0-based
  for (let i = 0; i < count; i++) {
    out.push(`${y}-${String(m + 1).padStart(2, '0')}`)
    m -= 1
    if (m < 0) {
      m = 11
      y -= 1
    }
  }
  return out
}

// 把 YYYY-MM 转成该月 15 号本地正午的 Unix 秒（落在该月周期内，交由后端解析归属周期）。
function monthToUnix(month: string): number {
  const [y, m] = month.split('-').map((v) => Number(v))
  if (!y || !m) return 0
  return Math.floor(new Date(y, m - 1, 15, 12, 0, 0).getTime() / 1000)
}

export function PerformanceAdjustDialog({
  open,
  onOpenChange,
  employee,
  onSuccess,
}: Props) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [amount, setAmount] = useState('')
  const [reason, setReason] = useState('')
  const [historical, setHistorical] = useState(false)
  const [month, setMonth] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [revertingId, setRevertingId] = useState<number | null>(null)

  // 历史周期补录仅提供「过去的自然月」——当前月不属于历史，避免勾选补录却落到当前周期并触发等级重估。
  const monthOptions = useMemo(() => recentMonths(13).slice(1), [])

  useEffect(() => {
    if (!open) {
      setAmount('')
      setReason('')
      setHistorical(false)
      setMonth('')
      setSubmitting(false)
      setRevertingId(null)
    }
  }, [open])

  const { data: adjData, refetch: refetchAdjustments } = useQuery({
    queryKey: ['employee-perf-adjustments', employee?.id],
    queryFn: () =>
      getEmployeePerformanceAdjustments(employee!.id, {
        page: 1,
        page_size: 20,
      }),
    enabled: open && !!employee,
  })
  const adjustments: CommissionLog[] = adjData?.data?.items ?? []

  const amountNum = Number(amount)
  const amountValid = amount.trim() !== '' && !Number.isNaN(amountNum) && amountNum !== 0
  const rate = Number(employee?.current_tier_rate ?? 0)
  // 预估提成仅在当前周期可算（用现等级费率）；历史周期费率由后端按当期还原，前端不试算。
  const previewCommissionUsd = !historical ? amountNum * rate : null

  const empName = employee?.username || (employee ? `#${employee.user_id}` : '')

  const handleSubmit = async () => {
    if (!employee || !amountValid) return
    if (historical && !month) {
      toast.error(t('Please choose a period'))
      return
    }
    setSubmitting(true)
    try {
      const periodStartAt = historical && month ? monthToUnix(month) : 0
      const res = await addEmployeePerformance(employee.id, {
        profit_usd: amountNum,
        reason: reason.trim(),
        period_start_at: periodStartAt,
      })
      if (!res.success) throw new Error(res.message ?? 'Failed')
      toast.success(t('Performance adjustment applied'))
      setAmount('')
      setReason('')
      await refetchAdjustments()
      qc.invalidateQueries({ queryKey: ['employees'] })
      onSuccess?.()
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('Operation failed'))
    } finally {
      setSubmitting(false)
    }
  }

  const handleRevert = async (logId: number) => {
    setRevertingId(logId)
    try {
      const res = await revertEmployeePerformance(logId)
      if (!res.success) throw new Error(res.message ?? 'Failed')
      toast.success(t('Adjustment reverted'))
      await refetchAdjustments()
      qc.invalidateQueries({ queryKey: ['employees'] })
      onSuccess?.()
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('Operation failed'))
    } finally {
      setRevertingId(null)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className='max-h-[86vh] overflow-y-auto sm:max-w-120'
        initialFocus={false}
      >
        <DialogHeader>
          <DialogTitle>{t('Adjust Performance')}</DialogTitle>
          <DialogDescription>
            {t(
              'Manually add or deduct performance (profit) for this employee. Commission follows automatically.'
            )}
          </DialogDescription>
        </DialogHeader>

        {/* Employee header */}
        <div className='bg-muted/30 rounded-lg border px-3 py-2'>
          <div className='mt-0.5 flex min-w-0 items-center gap-2'>
            <span className='truncate font-medium'>{empName}</span>
            {employee?.display_name ? (
              <span className='text-muted-foreground truncate text-sm'>
                {employee.display_name}
              </span>
            ) : null}
            <Badge variant='outline' className='ml-auto shrink-0'>
              #{employee?.user_id}
            </Badge>
          </div>
          <div className='text-muted-foreground mt-1 text-xs'>
            {t('Current tier rate')}: {(rate * 100).toFixed(1)}%
          </div>
        </div>

        {/* Amount */}
        <div className='space-y-1.5'>
          <label className='text-sm font-medium'>
            {t('Performance amount (USD)')}
          </label>
          <Input
            type='number'
            step='any'
            inputMode='decimal'
            placeholder='0.00'
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
          />
          <p className='text-muted-foreground text-xs'>
            {t('Negative values deduct performance.')}
            {previewCommissionUsd != null && amountValid
              ? ` ${t('Estimated commission')} ≈ $${previewCommissionUsd.toFixed(4)}`
              : ''}
          </p>
        </div>

        {/* Period */}
        <div className='space-y-1.5'>
          <label className='flex items-center gap-2 text-sm font-medium'>
            <input
              type='checkbox'
              className='size-4'
              checked={historical}
              onChange={(e) => {
                setHistorical(e.target.checked)
                if (!e.target.checked) setMonth('')
              }}
            />
            {t('Backfill to a historical period')}
          </label>
          {historical ? (
            <>
              <select
                aria-label={t('Choose a period')}
                className='border-input bg-background focus-visible:ring-ring h-9 w-full rounded-md border px-3 text-sm focus-visible:ring-1 focus-visible:outline-none'
                value={month}
                onChange={(e) => setMonth(e.target.value)}
              >
                <option value=''>{t('Choose a period')}</option>
                {monthOptions.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </select>
              <p className='text-muted-foreground text-xs'>
                {t(
                  "Historical periods use that period's tier rate and do not change the tier."
                )}
              </p>
            </>
          ) : (
            <p className='text-muted-foreground text-xs'>
              {t('Applied to the current period; may trigger tier re-evaluation.')}
            </p>
          )}
        </div>

        {/* Reason */}
        <div className='space-y-1.5'>
          <label className='text-sm font-medium'>{t('Reason')}</label>
          <Textarea
            rows={2}
            placeholder={t('Optional')}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          />
        </div>

        {/* Existing adjustments */}
        {adjustments.length > 0 ? (
          <div className='space-y-1.5'>
            <label className='text-sm font-medium'>
              {t('Adjustment history')}
            </label>
            <div className='divide-y rounded-lg border'>
              {adjustments.map((adj) => {
                const reverted = adj.settle_status === 2
                return (
                  <div
                    key={adj.id}
                    className='flex items-center gap-2 px-3 py-2 text-sm'
                  >
                    <div className='min-w-0 flex-1'>
                      {/* 手工调整：正=加业绩、负=扣减；不套用亏损/冲销徽标（那是消费流水语义）。 */}
                      <BusinessAmount
                        value={adj.profit_quota}
                        showPositiveSign
                        markNegative={false}
                      />
                      <div className='text-muted-foreground text-xs'>
                        {new Date(adj.created_at * 1000).toLocaleString()}
                        {' · '}
                        {(Number(adj.commission_rate || 0) * 100).toFixed(1)}%
                      </div>
                    </div>
                    {reverted ? (
                      <Badge variant='secondary' className='shrink-0'>
                        {t('Reverted')}
                      </Badge>
                    ) : (
                      <Button
                        type='button'
                        size='sm'
                        variant='ghost'
                        className='shrink-0'
                        disabled={revertingId === adj.id}
                        onClick={() => handleRevert(adj.id)}
                      >
                        <Undo2 className='mr-1 size-3.5' />
                        {t('Revert')}
                      </Button>
                    )}
                  </div>
                )
              })}
            </div>
          </div>
        ) : null}

        <DialogFooter>
          <Button
            type='button'
            variant='outline'
            onClick={() => onOpenChange(false)}
          >
            {t('Close')}
          </Button>
          <Button
            type='button'
            disabled={submitting || !amountValid}
            onClick={handleSubmit}
          >
            {submitting ? t('Saving...') : t('Apply')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

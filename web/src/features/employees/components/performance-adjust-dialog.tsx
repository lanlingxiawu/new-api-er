/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'

import { addEmployeePerformance } from '../api'
import type { EmployeeProfile } from '../types'

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
  const formId = useId()
  const inFlight = useRef(false)
  const [amount, setAmount] = useState('')
  const [reason, setReason] = useState('')
  const [historical, setHistorical] = useState(false)
  const [month, setMonth] = useState('')
  const [submitting, setSubmitting] = useState(false)

  // 历史周期补录仅提供「过去的自然月」——当前月不属于历史，避免勾选补录却落到当前周期并触发等级重估。
  const monthOptions = useMemo(() => recentMonths(13).slice(1), [])

  useEffect(() => {
    if (!open) {
      setAmount('')
      setReason('')
      setHistorical(false)
      setMonth('')
      setSubmitting(false)
    }
  }, [open])

  const amountNum = Number(amount)
  const amountValid =
    amount.trim() !== '' && Number.isFinite(amountNum) && amountNum !== 0
  const rate = Number(employee?.current_tier_rate ?? 0)
  // 预估提成仅在当前周期可算（用现等级费率）；历史周期费率由后端按当期还原，前端不试算。
  const previewCommissionUsd = !historical ? amountNum * rate : null

  const empName = employee?.username || (employee ? `#${employee.user_id}` : '')

  const handleSubmit = async () => {
    if (!employee || !amountValid || inFlight.current) return
    if (historical && !month) {
      toast.error(t('Please choose a period'))
      return
    }
    inFlight.current = true
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
      qc.invalidateQueries({ queryKey: ['employees'] })
      onSuccess?.()
      onOpenChange(false)
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t('Operation failed'))
    } finally {
      inFlight.current = false
      setSubmitting(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!inFlight.current) onOpenChange(next)
      }}
    >
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
          <label htmlFor={`${formId}-amount`} className='text-sm font-medium'>
            {t('Performance amount (USD)')}
          </label>
          <Input
            id={`${formId}-amount`}
            disabled={submitting}
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
            <Checkbox
              disabled={submitting}
              checked={historical}
              onCheckedChange={(checked) => {
                setHistorical(checked)
                if (!checked) setMonth('')
              }}
            />
            {t('Backfill to a historical period')}
          </label>
          {historical ? (
            <>
              <Select
                value={month || null}
                onValueChange={(value) => setMonth(value ?? '')}
                disabled={submitting}
              >
                <SelectTrigger aria-label={t('Choose a period')}>
                  <SelectValue placeholder={t('Choose a period')} />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    {monthOptions.map((value) => (
                      <SelectItem key={value} value={value}>
                        {value}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
              <p className='text-muted-foreground text-xs'>
                {t(
                  "Historical periods use that period's tier rate and do not change the tier."
                )}
              </p>
            </>
          ) : (
            <p className='text-muted-foreground text-xs'>
              {t(
                'Applied to the current period; may trigger tier re-evaluation.'
              )}
            </p>
          )}
        </div>

        {/* Reason */}
        <div className='space-y-1.5'>
          <label htmlFor={`${formId}-reason`} className='text-sm font-medium'>
            {t('Reason')}
          </label>
          <Textarea
            id={`${formId}-reason`}
            disabled={submitting}
            rows={2}
            placeholder={t('Optional')}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          />
        </div>

        <DialogFooter>
          <Button
            type='button'
            variant='outline'
            disabled={submitting}
            onClick={() => onOpenChange(false)}
          >
            {t('Cancel')}
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

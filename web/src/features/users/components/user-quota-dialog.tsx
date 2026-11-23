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
import { useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Field, FieldLabel, FieldLegend, FieldSet } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { getCurrencyDisplay, getCurrencyLabel } from '@/lib/currency'
import { formatQuota, parseQuotaFromDollars } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import { adjustUserQuota } from '../api'
import type { QuotaAdjustMode } from '../types'

interface UserQuotaDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  userId: number
  currentQuota: number
  onSuccess: () => void
}

export function UserQuotaDialog(props: UserQuotaDialogProps) {
  const { t } = useTranslation()
  const id = useId()
  const inFlight = useRef(false)
  const [mode, setMode] = useState<QuotaAdjustMode>('add')
  const [amount, setAmount] = useState('')
  const [loading, setLoading] = useState(false)

  const { meta: currencyMeta } = getCurrencyDisplay()
  const currencyLabel = getCurrencyLabel()
  const tokensOnly = currencyMeta.kind === 'tokens'

  const amountValue = Number.parseFloat(amount) || 0
  const quotaValue = parseQuotaFromDollars(Math.abs(amountValue))

  const getPreviewText = () => {
    const current = props.currentQuota
    const val = quotaValue
    switch (mode) {
      case 'add':
        return `${t('Current quota')}: ${formatQuota(current)}  +${formatQuota(val)} = ${formatQuota(current + val)}`
      case 'subtract':
        return `${t('Current quota')}: ${formatQuota(current)}  -${formatQuota(val)} = ${formatQuota(current - val)}`
      case 'override': {
        const overrideQuota = parseQuotaFromDollars(amountValue)
        return `${t('Current quota')}: ${formatQuota(current)} → ${formatQuota(overrideQuota)}`
      }
      default:
        return ''
    }
  }

  const handleConfirm = async () => {
    if (inFlight.current) return
    if (!amount && mode !== 'override') return
    if (quotaValue <= 0 && mode !== 'override') return

    inFlight.current = true
    setLoading(true)
    try {
      const value =
        mode === 'override' ? parseQuotaFromDollars(amountValue) : quotaValue
      const result = await adjustUserQuota({
        id: props.userId,
        action: 'add_quota',
        mode,
        value: mode === 'override' ? value : Math.abs(value),
      })
      if (result.success) {
        toast.success(t('Quota adjusted successfully'))
        setAmount('')
        setMode('add')
        props.onOpenChange(false)
        props.onSuccess()
      } else {
        handleServerError(result, t('Failed to adjust quota'))
      }
    } catch (e: unknown) {
      handleServerError(e, t('Failed to adjust quota'))
    } finally {
      inFlight.current = false
      setLoading(false)
    }
  }

  const handleCancel = () => {
    if (inFlight.current) return
    setAmount('')
    setMode('add')
    props.onOpenChange(false)
  }

  const placeholder = tokensOnly
    ? t('Enter amount in tokens')
    : t('Enter amount in {{currency}}', { currency: currencyLabel })

  return (
    <Dialog
      open={props.open}
      onOpenChange={(open) => {
        if (!open) handleCancel()
      }}
      title={t('Adjust Quota')}
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button variant='outline' onClick={handleCancel} disabled={loading}>
            {t('Cancel')}
          </Button>
          <Button onClick={handleConfirm} disabled={loading}>
            {loading ? t('Processing...') : t('Confirm')}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        <div className='text-muted-foreground text-sm'>{getPreviewText()}</div>

        <FieldSet disabled={loading}>
          <FieldLegend variant='label'>{t('Mode')}</FieldLegend>
          <RadioGroup
            value={mode}
            onValueChange={(value) => {
              setMode(value as QuotaAdjustMode)
              setAmount('')
            }}
            className='grid-cols-1 sm:grid-cols-3'
          >
            {(['add', 'subtract', 'override'] as const).map((value) => (
              <Field key={value} orientation='horizontal'>
                <RadioGroupItem id={`${id}-${value}`} value={value} />
                <FieldLabel htmlFor={`${id}-${value}`}>
                  {value === 'add' && t('Add')}
                  {value === 'subtract' && t('Subtract')}
                  {value === 'override' && t('Override')}
                </FieldLabel>
              </Field>
            ))}
          </RadioGroup>
        </FieldSet>

        <div className='space-y-2'>
          <Label htmlFor={`${id}-amount`}>
            {t('Amount')} ({currencyLabel})
          </Label>
          <Input
            id={`${id}-amount`}
            disabled={loading}
            type='number'
            step={tokensOnly ? 1 : 0.000001}
            min={mode === 'override' ? undefined : 0}
            placeholder={placeholder}
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.nativeEvent.isComposing) {
                e.preventDefault()
                void handleConfirm()
              }
            }}
          />
        </div>
      </div>
    </Dialog>
  )
}

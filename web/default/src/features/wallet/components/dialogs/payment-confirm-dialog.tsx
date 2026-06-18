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
import { Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { formatLocalCurrencyAmount, formatCurrencyFromUSD } from '@/lib/currency'
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
import { Skeleton } from '@/components/ui/skeleton'
import { DEFAULT_DISCOUNT_RATE, PAYMENT_TYPES } from '../../constants'
import { formatCurrency, getPaymentIcon } from '../../lib'
import { DEFAULT_CURRENCY_CONFIG } from '@/stores/system-config-store'
import { isInfiniPayment } from '../../lib/payment'
import type { PaymentMethod } from '../../types'

interface PaymentConfirmDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onConfirm: () => void
  topupAmount: number
  paymentAmount: number
  paymentMethod: PaymentMethod | undefined
  calculating: boolean
  processing: boolean
  discountRate?: number
  usdExchangeRate?: number
  /** Binance P2P 实时汇率；为 0 时不显示换算行 */
  binanceRate?: number
  /** 系统充值比例 = operation_setting.Price（CNY/额度单位），后台可配置 */
  priceRatio?: number
}

export function PaymentConfirmDialog({
  open,
  onOpenChange,
  onConfirm,
  topupAmount,
  paymentAmount,
  paymentMethod,
  calculating,
  processing,
  discountRate = DEFAULT_DISCOUNT_RATE,
  usdExchangeRate = 1,
  binanceRate = 0,
  priceRatio = 1,
}: PaymentConfirmDialogProps) {
  const { t } = useTranslation()
  const hasDiscount = discountRate > 0 && discountRate < 1 && paymentAmount > 0
  const originalAmount = hasDiscount ? paymentAmount / discountRate : 0
  const discountAmount = hasDiscount ? originalAmount - paymentAmount : 0

  // Infini 专属计算
  const paymentType = paymentMethod?.type ?? ''
  const isInfini = isInfiniPayment(paymentType)
  const isAlipay =
    paymentType === PAYMENT_TYPES.ALIPAY ||
    paymentType === PAYMENT_TYPES.ALIPAY_OFFICIAL
  const formatCnyPaymentAmount = (amount: number) =>
    `${formatCurrency(amount)} ${t('CNY')}`
  const formatPaymentAmount = (amount: number) =>
    isAlipay
      ? formatCnyPaymentAmount(amount)
      : formatLocalCurrencyAmount(amount, {
          digitsLarge: 2,
          digitsSmall: 2,
          abbreviate: false,
        })
  // Binance 实时汇率，优先实时值，回退系统配置
  const effectiveRate = binanceRate > 0 ? binanceRate : usdExchangeRate
  const safePrice = priceRatio > 0 ? priceRatio : 1
  // 实际到账额度单位数（浮点，用于显示）= 实付USD × Binance汇率 / 充值比例(Price)
  // 后端精确公式：topUp.Amount = round(payMoney × binanceRate / Price × QuotaPerUnit)
  // 前端此处展示等价金额，保持和 raw quota 快照的精度模型一致。
  const expectedCreditUnits =
    isInfini && paymentAmount > 0 && effectiveRate > 0
      ? Math.round(
          (paymentAmount * effectiveRate * DEFAULT_CURRENCY_CONFIG.quotaPerUnit) /
            safePrice
        ) / DEFAULT_CURRENCY_CONFIG.quotaPerUnit
      : 0

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent className='max-sm:w-[calc(100vw-1.5rem)] sm:max-w-md'>
        <AlertDialogHeader>
          <AlertDialogTitle className='text-xl font-semibold'>
            {t('Confirm Payment')}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {t('Review your payment details')}
          </AlertDialogDescription>
        </AlertDialogHeader>

        <div className='space-y-3 py-3 sm:space-y-4 sm:py-4'>
          <div className='flex items-center justify-between'>
            <span className='text-muted-foreground text-sm'>
              {t('Topup Amount')}
            </span>
            <span className='text-lg font-semibold'>
              {formatLocalCurrencyAmount(topupAmount * usdExchangeRate, {
                digitsLarge: 2,
                digitsSmall: 2,
                abbreviate: false,
              })}
            </span>
          </div>

          <div className='flex items-center justify-between'>
            <span className='text-muted-foreground text-sm'>
              {t('You Pay')}
            </span>
            {calculating ? (
              <Skeleton className='h-6 w-24' />
            ) : (
              <div className='flex items-baseline gap-2'>
                <span className='text-2xl font-semibold'>
                  {isInfini
                    ? /* Infini 以 USD 收款，formatCurrency 只出数字，再追加货币码 */
                      <>
                        {formatCurrency(paymentAmount)}
                        <span className='text-muted-foreground ml-1 text-base font-normal'>
                          {paymentMethod?.currency ?? 'USD'}
                        </span>
                      </>
                    : /* Payment gateways may use a fixed settlement currency. */
                      formatPaymentAmount(paymentAmount)}
                </span>
                {hasDiscount && (
                  <span className='text-muted-foreground text-sm line-through'>
                    {isInfini
                      ? formatCurrency(originalAmount)
                      : formatPaymentAmount(originalAmount)}
                  </span>
                )}
              </div>
            )}
          </div>

          {hasDiscount && !calculating && (
            <div className='bg-muted/50 rounded-lg p-3'>
              <div className='flex items-center justify-between text-sm'>
                <span className='text-muted-foreground'>{t('You save')}</span>
                <span className='font-semibold text-green-600'>
                  {isInfini
                    ? formatCurrency(discountAmount)
                    : formatPaymentAmount(discountAmount)}
                </span>
              </div>
            </div>
          )}

          {/* Infini：三行信息 —— 实际到账 / 充值比例 / 汇率（与经典 UI 对齐） */}
          {isInfini && !calculating && paymentAmount > 0 && (
            <div className='bg-muted/40 rounded-lg px-3 py-3 space-y-3'>
              {/* 行1：实际到账 ≈ paymentUSD × binanceRate / Price */}
              <div className='flex items-center justify-between'>
                <span className='text-muted-foreground text-sm'>{t('Actual credit')}</span>
                <span className='text-xl font-bold text-green-600 dark:text-green-400'>
                  {expectedCreditUnits > 0
                    ? `≈ ${formatCurrencyFromUSD(expectedCreditUnits, { digitsLarge: 2, digitsSmall: 2, abbreviate: false })}`
                    : '—'}
                </span>
              </div>
              {/* 行2：充值比例 = 1/Price（后台可配置，精确值） */}
              <div className='flex items-center justify-between text-xs text-muted-foreground'>
                <span>{t('Top-up rate')}</span>
                <span>
                  {'1 ¥ = '}
                  {formatCurrencyFromUSD(1 / safePrice, { digitsLarge: 2, digitsSmall: 2, abbreviate: false })}
                </span>
              </div>
              {/* 行3：汇率（Binance 实时） */}
              {effectiveRate > 0 && (
                <div className='flex items-center justify-between text-xs text-muted-foreground'>
                  <span>{t('Real-time exchange rate')}</span>
                  <span className='font-medium'>
                    1 {paymentMethod?.currency ?? 'USD'} ≈ ¥{effectiveRate.toFixed(2)}
                  </span>
                </div>
              )}
            </div>
          )}

          <div className='border-t pt-4'>
            <div className='flex items-center justify-between'>
              <span className='text-muted-foreground text-sm'>
                {t('Payment Method')}
              </span>
              <div className='flex items-center gap-2'>
                {getPaymentIcon(
                  paymentMethod?.type,
                  'h-4 w-4',
                  paymentMethod?.icon,
                  paymentMethod?.name
                )}
                <span className='font-medium'>{paymentMethod?.name}</span>
              </div>
            </div>
          </div>
        </div>

        <AlertDialogFooter className='grid grid-cols-2 gap-2 sm:flex'>
          <AlertDialogCancel disabled={processing}>
            {t('Cancel')}
          </AlertDialogCancel>
          <AlertDialogAction onClick={onConfirm} disabled={processing}>
            {processing && <Loader2 className='mr-2 h-4 w-4 animate-spin' />}
            {t('Confirm Payment')}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

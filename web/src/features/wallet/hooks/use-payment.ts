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
import i18next from 'i18next'
import { useState, useCallback } from 'react'
import { toast } from 'sonner'

import { handleServerError } from '@/lib/handle-server-error'

import {
  calculateAmount,
  calculateStripeAmount,
  calculateWaffoAmount,
  calculateWaffoPancakeAmount,
  calculateAlipayAmount,
  calculateWechatAmount,
  calculateInfiniAmount,
  requestPayment,
  requestStripePayment,
  requestAlipayPayment,
  requestInfiniPayment,
  isApiSuccess,
  type InfiniAmountRequest,
} from '../api'
import {
  isStripePayment,
  isWaffoPayment,
  isWaffoPancakePayment,
  isAlipayOfficialPayment,
  isWechatOfficialPayment,
  isInfiniPayment,
  isSafeHttpCheckoutUrl,
  submitPaymentForm,
} from '../lib'
import type { AmountResponse } from '../types'

// ============================================================================
// Payment Hook
// ============================================================================

type AmountCalculator = (
  request: InfiniAmountRequest
) => Promise<AmountResponse>

export interface PaymentAmountCalculators {
  regular: AmountCalculator
  stripe: AmountCalculator
  waffo: AmountCalculator
  waffoPancake: AmountCalculator
  alipay?: AmountCalculator
  wechat?: AmountCalculator
  infini?: AmountCalculator
}

const defaultPaymentAmountCalculators: Required<PaymentAmountCalculators> = {
  regular: calculateAmount,
  stripe: calculateStripeAmount,
  waffo: calculateWaffoAmount,
  waffoPancake: calculateWaffoPancakeAmount,
  alipay: calculateAlipayAmount,
  wechat: calculateWechatAmount,
  infini: calculateInfiniAmount,
}

export interface PaymentQuote {
  amount: number
  // 后端在报价时锁定的到账折算汇率（元/美金），仅动态汇率支付（Stripe/Infini）返回；其它为 0
  rate: number
}

// 按支付方式选择对应的金额计算接口
export async function requestPaymentQuote(
  topupAmount: number,
  paymentType: string,
  infiniCurrency?: string,
  calculators: PaymentAmountCalculators = defaultPaymentAmountCalculators
): Promise<PaymentQuote> {
  const request: InfiniAmountRequest = { amount: topupAmount }
  let calculator = calculators.regular
  if (isStripePayment(paymentType)) {
    calculator = calculators.stripe
  } else if (isWaffoPayment(paymentType)) {
    calculator = calculators.waffo
  } else if (isWaffoPancakePayment(paymentType)) {
    calculator = calculators.waffoPancake
  } else if (isAlipayOfficialPayment(paymentType)) {
    calculator = calculators.alipay ?? defaultPaymentAmountCalculators.alipay
  } else if (isWechatOfficialPayment(paymentType)) {
    calculator = calculators.wechat ?? defaultPaymentAmountCalculators.wechat
  } else if (isInfiniPayment(paymentType)) {
    calculator = calculators.infini ?? defaultPaymentAmountCalculators.infini
    request.currency = infiniCurrency
  }

  const response = await calculator(request)
  if (!isApiSuccess(response) || !response.data) {
    return { amount: 0, rate: 0 }
  }

  const rate = Number((response as { exchange_rate?: number }).exchange_rate)
  return {
    amount: Number.parseFloat(response.data),
    rate: Number.isFinite(rate) && rate > 0 ? rate : 0,
  }
}

export async function requestPaymentAmount(
  topupAmount: number,
  paymentType: string,
  calculators: PaymentAmountCalculators = defaultPaymentAmountCalculators
): Promise<number> {
  const quote = await requestPaymentQuote(
    topupAmount,
    paymentType,
    undefined,
    calculators
  )
  return quote.amount
}

export function usePayment() {
  const [amount, setAmount] = useState<number>(0)
  // paymentRate：后端在报价时锁定的到账折算汇率（元/美金），仅动态汇率支付（Stripe/Infini）返回。
  // 前端确认弹窗用它展示实际到账，保证与后端到账口径一致、且不随前端实时汇率异步刷新而跳动。
  const [paymentRate, setPaymentRate] = useState<number>(0)
  const [calculating, setCalculating] = useState(false)
  const [processing, setProcessing] = useState(false)

  // Calculate payment amount
  // infiniCurrency: 多币种时由外部（Wallet / Infini 选择器）传入所选币种
  const calculatePaymentAmount = useCallback(
    async (
      topupAmount: number,
      paymentType: string,
      infiniCurrency?: string
    ) => {
      try {
        setCalculating(true)

        // Don't show error for calculation; a failed quote resolves to 0
        const quote = await requestPaymentQuote(
          topupAmount,
          paymentType,
          infiniCurrency
        )
        setAmount(quote.amount)
        setPaymentRate(quote.rate)
        return quote.amount
      } catch {
        setAmount(0)
        setPaymentRate(0)
        return 0
      } finally {
        setCalculating(false)
      }
    },
    []
  )

  // Process payment
  const processPayment = useCallback(
    async (
      topupAmount: number,
      paymentType: string,
      infiniCurrency?: string
    ) => {
      try {
        setProcessing(true)

        const amount = Math.floor(topupAmount)

        // Handle Stripe payment
        if (isStripePayment(paymentType)) {
          const response = await requestStripePayment({
            amount,
            payment_method: 'stripe',
          })

          if (!isApiSuccess(response)) {
            toast.error(response.message || i18next.t('Payment request failed'))
            return false
          }

          if (response.data?.pay_link) {
            window.open(response.data.pay_link, '_blank')
            toast.success(i18next.t('Redirecting to payment page...'))
            return true
          }

          return false
        }

        // Handle official Alipay payment
        if (isAlipayOfficialPayment(paymentType)) {
          const response = await requestAlipayPayment({ amount })

          if (!isApiSuccess(response)) {
            toast.error(response.message || i18next.t('Payment request failed'))
            return false
          }

          const data = response.data
          const paymentUrl =
            data && typeof data === 'object' ? data.payment_url : undefined
          if (paymentUrl) {
            if (!isSafeHttpCheckoutUrl(paymentUrl)) {
              toast.error(i18next.t('Invalid payment redirect URL'))
              return false
            }
            window.open(paymentUrl, '_blank')
            toast.success(i18next.t('Redirecting to payment page...'))
            return true
          }

          return false
        }

        // Handle Infini hosted checkout payment
        if (isInfiniPayment(paymentType)) {
          const response = await requestInfiniPayment({
            amount,
            currency: infiniCurrency,
          })

          if (!isApiSuccess(response)) {
            toast.error(response.message || i18next.t('Payment request failed'))
            return false
          }

          const data = response.data
          const paymentUrl =
            data && typeof data === 'object' ? data.payment_url : undefined
          if (paymentUrl) {
            if (!isSafeHttpCheckoutUrl(paymentUrl)) {
              toast.error(i18next.t('Invalid payment redirect URL'))
              return false
            }
            window.open(paymentUrl, '_blank')
            toast.success(i18next.t('Redirecting to payment page...'))
            return true
          }

          return false
        }

        // Handle generic (epay-style form submission) payment
        // Note: Infini payment is handled above via isInfiniPayment check
        const response = await requestPayment({
          amount,
          payment_method: paymentType,
        })

        if (!isApiSuccess(response)) {
          handleServerError(response, i18next.t('Payment request failed'))
          return false
        }

        if (response.data) {
          const url = (response as unknown as { url?: string }).url
          if (url) {
            submitPaymentForm(url, response.data)
            toast.success(i18next.t('Redirecting to payment page...'))
            return true
          }
        }

        return false
      } catch (error) {
        handleServerError(error, i18next.t('Payment request failed'))
        return false
      } finally {
        setProcessing(false)
      }
    },
    []
  )

  return {
    amount,
    paymentRate,
    calculating,
    processing,
    calculatePaymentAmount,
    processPayment,
    setAmount,
  }
}

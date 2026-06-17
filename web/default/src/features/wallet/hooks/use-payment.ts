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
import { useState, useCallback } from 'react'
import i18next from 'i18next'
import { toast } from 'sonner'
import {
  calculateAmount,
  calculateStripeAmount,
  calculateWaffoPancakeAmount,
  calculateAlipayAmount,
  calculateWechatAmount,
  calculateInfiniAmount,
  requestPayment,
  requestStripePayment,
  requestAlipayPayment,
  requestInfiniPayment,
  isApiSuccess,
} from '../api'
import {
  isStripePayment,
  isWaffoPancakePayment,
  isAlipayOfficialPayment,
  isWechatOfficialPayment,
  isInfiniPayment,
  isSafeHttpCheckoutUrl,
  submitPaymentForm,
} from '../lib'

// ============================================================================
// Payment Hook
// ============================================================================

export function usePayment() {
  const [amount, setAmount] = useState<number>(0)
  const [calculating, setCalculating] = useState(false)
  const [processing, setProcessing] = useState(false)

  // Calculate payment amount
  // infiniCurrency: 多币种时由外部（Wallet / Infini 选择器）传入所选币种
  const calculatePaymentAmount = useCallback(
    async (topupAmount: number, paymentType: string, infiniCurrency?: string) => {
      try {
        setCalculating(true)

        const isStripe = isStripePayment(paymentType)
        const isPancake = isWaffoPancakePayment(paymentType)
        const isAlipayOfficial = isAlipayOfficialPayment(paymentType)
        const isWechatOfficial = isWechatOfficialPayment(paymentType)
        const isInfini = isInfiniPayment(paymentType)
        const response = isStripe
          ? await calculateStripeAmount({ amount: topupAmount })
          : isPancake
            ? await calculateWaffoPancakeAmount({ amount: topupAmount })
            : isAlipayOfficial
              ? await calculateAlipayAmount({ amount: topupAmount })
              : isWechatOfficial
                ? await calculateWechatAmount({ amount: topupAmount })
                : isInfini
                  ? await calculateInfiniAmount({ amount: topupAmount, currency: infiniCurrency })
                  : await calculateAmount({ amount: topupAmount })

        if (isApiSuccess(response) && response.data) {
          const calculatedAmount = parseFloat(response.data)
          setAmount(calculatedAmount)
          return calculatedAmount
        }

        // Don't show error for calculation, just set to 0
        setAmount(0)
        return 0
      } catch (_error) {
        setAmount(0)
        return 0
      } finally {
        setCalculating(false)
      }
    },
    []
  )

  // Process payment
  const processPayment = useCallback(
    async (topupAmount: number, paymentType: string, infiniCurrency?: string) => {
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
            toast.error(
              response.message || i18next.t('Payment request failed')
            )
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
            toast.error(
              response.message || i18next.t('Payment request failed')
            )
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
          const response = await requestInfiniPayment({ amount, currency: infiniCurrency })

          if (!isApiSuccess(response)) {
            toast.error(
              response.message || i18next.t('Payment request failed')
            )
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
          toast.error(response.message || i18next.t('Payment request failed'))
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
      } catch (_error) {
        toast.error(i18next.t('Payment request failed'))
        return false
      } finally {
        setProcessing(false)
      }
    },
    []
  )

  return {
    amount,
    calculating,
    processing,
    calculatePaymentAmount,
    processPayment,
    setAmount,
  }
}

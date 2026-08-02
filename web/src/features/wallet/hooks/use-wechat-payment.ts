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
import { requestWechatPayment, queryWechatOrder, isApiSuccess } from '../api'

function getErrorMessage(message: string | undefined, data: unknown): string {
  if (typeof data === 'string' && data.trim()) {
    return data
  }

  return message || i18next.t('Payment request failed')
}

/**
 * Hook for the official WeChat Pay Native flow.
 *
 * Unlike other payment methods, this does not redirect the browser. Instead
 * it returns a `code_url` for the caller to render as a QR code, and exposes
 * `checkOrderStatus` so the caller can poll until the order is paid.
 */
export function useWechatPayment() {
  const [processing, setProcessing] = useState(false)
  const [codeUrl, setCodeUrl] = useState<string | null>(null)
  const [orderId, setOrderId] = useState<string | null>(null)
  const [status, setStatus] = useState<string | null>(null)

  const processWechatPayment = useCallback(async (topupAmount: number) => {
    setProcessing(true)

    try {
      const response = await requestWechatPayment({
        amount: Math.floor(topupAmount),
      })

      if (isApiSuccess(response) && response.data) {
        const data = response.data
        const url = typeof data === 'object' ? data.code_url : undefined
        const order = typeof data === 'object' ? data.order_id : undefined

        if (url) {
          setCodeUrl(url)
          setOrderId(order ?? null)
          setStatus('pending')
          return true
        }
      }

      toast.error(getErrorMessage(response.message, response.data))
      return false
    } catch {
      toast.error(i18next.t('Payment request failed'))
      return false
    } finally {
      setProcessing(false)
    }
  }, [])

  // Poll order status while the QR code is displayed
  const checkOrderStatus = useCallback(async () => {
    if (!orderId) return null

    try {
      const response = await queryWechatOrder(orderId)
      if (isApiSuccess(response) && response.data?.status) {
        setStatus(response.data.status)
        return response.data.status
      }
    } catch {
      // Ignore transient polling errors and try again on the next tick
    }

    return null
  }, [orderId])

  const reset = useCallback(() => {
    setCodeUrl(null)
    setOrderId(null)
    setStatus(null)
  }, [])

  return {
    processing,
    codeUrl,
    orderId,
    status,
    processWechatPayment,
    checkOrderStatus,
    reset,
  }
}

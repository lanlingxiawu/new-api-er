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
import { api } from '@/lib/api'

import type {
  RedemptionRequest,
  PaymentRequest,
  AmountRequest,
  AffiliateTransferRequest,
  ApiResponse,
  TopupInfoResponse,
  RedemptionResponse,
  AmountResponse,
  PaymentResponse,
  StripePaymentResponse,
  AffiliateCodeResponse,
  AffiliateTransferResponse,
  BillingHistoryResponse,
  BillingHistoryFilters,
  CompleteOrderRequest,
  CreemPaymentRequest,
  CreemPaymentResponse,
  WaffoPaymentRequest,
  WaffoPaymentResponse,
  WaffoPancakePaymentRequest,
  WaffoPancakePaymentResponse,
  AlipayPaymentResponse,
  WechatPaymentResponse,
  WechatOrderQueryResponse,
  PlatformStatusQueryResponse,
} from './types'

// ============================================================================
// Wallet API Functions
// ============================================================================

/**
 * Check if API response is successful
 */
export function isApiSuccess(response: ApiResponse): boolean {
  return response.success === true || response.message === 'success'
}

/**
 * Get topup configuration info
 */
export async function getTopupInfo(): Promise<TopupInfoResponse> {
  const res = await api.get('/api/user/topup/info')
  return res.data
}

/**
 * Redeem a topup code
 */
export async function redeemTopupCode(
  request: RedemptionRequest
): Promise<RedemptionResponse> {
  const res = await api.post('/api/user/topup', request)
  return res.data
}

/**
 * Calculate payment amount for regular payment
 */
export async function calculateAmount(
  request: AmountRequest
): Promise<AmountResponse> {
  const res = await api.post('/api/user/amount', request, {
    skipBusinessError: true,
  } as Record<string, unknown>)
  return res.data
}

/**
 * Calculate payment amount for Stripe payment
 */
export async function calculateStripeAmount(
  request: AmountRequest
): Promise<AmountResponse> {
  const res = await api.post('/api/user/stripe/amount', request, {
    skipBusinessError: true,
  } as Record<string, unknown>)
  return res.data
}

/**
 * Request regular payment
 */
export async function requestPayment(
  request: PaymentRequest
): Promise<PaymentResponse> {
  const res = await api.post('/api/user/pay', request, {
    skipBusinessError: true,
  } as Record<string, unknown>)
  return {
    ...res.data,
    url: res.data.url || (res as unknown as { url?: string }).url,
  }
}

/**
 * Request Stripe payment
 */
export async function requestStripePayment(
  request: PaymentRequest
): Promise<StripePaymentResponse> {
  const res = await api.post('/api/user/stripe/pay', request, {
    skipBusinessError: true,
  } as Record<string, unknown>)
  return res.data
}

/**
 * Request Creem payment
 */
export async function requestCreemPayment(
  request: CreemPaymentRequest
): Promise<CreemPaymentResponse> {
  const res = await api.post('/api/user/creem/pay', request, {
    skipBusinessError: true,
  } as Record<string, unknown>)
  return res.data
}

/**
 * Request Waffo payment
 */
export async function requestWaffoPayment(
  request: WaffoPaymentRequest
): Promise<WaffoPaymentResponse> {
  const res = await api.post('/api/user/waffo/pay', request, {
    skipBusinessError: true,
  } as Record<string, unknown>)
  return res.data
}

/**
 * Calculate payment amount for Waffo Pancake payment
 */
export async function calculateWaffoPancakeAmount(
  request: AmountRequest
): Promise<AmountResponse> {
  const res = await api.post('/api/user/waffo-pancake/amount', request, {
    skipBusinessError: true,
  } as Record<string, unknown>)
  return res.data
}

/**
 * Request Waffo Pancake payment
 */
export async function requestWaffoPancakePayment(
  request: WaffoPancakePaymentRequest
): Promise<WaffoPancakePaymentResponse> {
  const res = await api.post('/api/user/waffo-pancake/pay', request, {
    skipBusinessError: true,
  } as Record<string, unknown>)
  return res.data
}

/**
 * Calculate payment amount for official Alipay payment
 */
export async function calculateAlipayAmount(
  request: AmountRequest
): Promise<AmountResponse> {
  const res = await api.post('/api/user/alipay/amount', request, {
    skipBusinessError: true,
  } as Record<string, unknown>)
  return res.data
}

/**
 * Request official Alipay payment
 */
export async function requestAlipayPayment(
  request: AmountRequest
): Promise<AlipayPaymentResponse> {
  const res = await api.post('/api/user/alipay/pay', request, {
    skipBusinessError: true,
  } as Record<string, unknown>)
  return res.data
}

/**
 * Calculate payment amount for official WeChat Pay payment
 */
export async function calculateWechatAmount(
  request: AmountRequest
): Promise<AmountResponse> {
  const res = await api.post('/api/user/wechat/amount', request, {
    skipBusinessError: true,
  } as Record<string, unknown>)
  return res.data
}

/**
 * Request official WeChat Pay payment (Native QR code)
 */
export async function requestWechatPayment(
  request: AmountRequest
): Promise<WechatPaymentResponse> {
  const res = await api.post('/api/user/wechat/pay', request, {
    skipBusinessError: true,
  } as Record<string, unknown>)
  return res.data
}

/**
 * Query official WeChat Pay order status (used while QR code is displayed)
 */
export async function queryWechatOrder(
  tradeNo: string
): Promise<WechatOrderQueryResponse> {
  const res = await api.get('/api/user/wechat/order', {
    params: { trade_no: tradeNo },
    skipBusinessError: true,
  } as Record<string, unknown>)
  return res.data
}

export interface InfiniAmountRequest {
  amount: number
  currency?: string
}

/**
 * Calculate payment amount for Infini payment
 */
export async function calculateInfiniAmount(
  request: InfiniAmountRequest
): Promise<AmountResponse> {
  const res = await api.post('/api/user/infini/amount', request, {
    skipBusinessError: true,
  } as Record<string, unknown>)
  return res.data
}

/**
 * Request Infini payment (hosted checkout)
 */
export async function requestInfiniPayment(
  request: InfiniAmountRequest
): Promise<AlipayPaymentResponse> {
  const res = await api.post('/api/user/infini/pay', request, {
    skipBusinessError: true,
  } as Record<string, unknown>)
  return res.data
}

export interface ExchangeRateResponse {
  success: boolean
  data?: {
    rate: number
    source: string
    pair: string
  }
}

/**
 * Get real-time USD/CNY reference rate (Binance P2P, server-side Redis 1h cache)
 */
export async function getUSDCNYRate(): Promise<ExchangeRateResponse> {
  const res = await api.get('/api/exchange-rate/usd-cny')
  return res.data
}

/**
 * Get affiliate code
 */
export async function getAffiliateCode(): Promise<AffiliateCodeResponse> {
  const res = await api.get('/api/user/aff')
  return res.data
}

/**
 * Transfer affiliate quota to balance
 */
export async function transferAffiliateQuota(
  request: AffiliateTransferRequest
): Promise<AffiliateTransferResponse> {
  const res = await api.post('/api/user/aff_transfer', request)
  return res.data
}

/**
 * Build common billing query params from filters.
 * `user_id` is only attached for admin requests (server ignores it otherwise,
 * but we avoid sending it to keep user requests clean).
 */
function buildBillingFilterParams(
  filters: BillingHistoryFilters,
  isAdmin: boolean
): URLSearchParams {
  const params = new URLSearchParams()
  if (filters.keyword) params.append('keyword', filters.keyword)
  if (filters.startTime) params.append('start_time', String(filters.startTime))
  if (filters.endTime) params.append('end_time', String(filters.endTime))
  if (filters.status) params.append('status', filters.status)
  if (filters.paymentMethod) {
    params.append('payment_method', filters.paymentMethod)
  }
  if (isAdmin && filters.userId && filters.userId > 0) {
    params.append('user_id', String(filters.userId))
  }
  return params
}

interface BillingHistoryQueryOptions {
  pageSize: number
  filters?: BillingHistoryFilters
  /** 1-based page number for offset pagination (server computes the offset). */
  page?: number
}

/**
 * Get billing history for current user.
 * Uses plain offset pagination (`p`); the top-up tables are small enough that
 * offset paging carries no real cost, and it lets the admin jump to any page with
 * a single request instead of sequentially prefetching every preceding page.
 */
export async function getUserBillingHistory(
  options: BillingHistoryQueryOptions
): Promise<ApiResponse<BillingHistoryResponse>> {
  const { pageSize, filters = {}, page } = options
  const params = buildBillingFilterParams(filters, false)
  params.append('page_size', pageSize.toString())
  if (page !== undefined) params.append('p', page.toString())
  const res = await api.get(`/api/user/topup/self?${params.toString()}`)
  return res.data
}

/**
 * Get billing history for all users (admin only). See getUserBillingHistory for
 * the offset-pagination rationale.
 */
export async function getAllBillingHistory(
  options: BillingHistoryQueryOptions
): Promise<ApiResponse<BillingHistoryResponse>> {
  const { pageSize, filters = {}, page } = options
  const params = buildBillingFilterParams(filters, true)
  params.append('page_size', pageSize.toString())
  if (page !== undefined) params.append('p', page.toString())
  const res = await api.get(`/api/user/topup?${params.toString()}`)
  return res.data
}

/**
 * Result of a billing-history CSV export.
 */
export interface BillingExportResult {
  blob: Blob
  filename: string
  /** Whether the export was truncated to the user row limit */
  truncated: boolean
  /** The row limit that triggered truncation (when truncated) */
  maxRows?: number
}

function parseContentDispositionFilename(
  header: string | undefined,
  fallback: string
): string {
  if (!header) return fallback
  const match = /filename="?([^"]+)"?/i.exec(header)
  return match?.[1] || fallback
}

/**
 * Shared CSV export request. Requests a Blob; if the backend returns a JSON
 * business error (Content-Type application/json) instead of a CSV stream, it is
 * decoded and thrown so the caller can surface a readable message.
 */
async function requestBillingExport(url: string): Promise<BillingExportResult> {
  let res
  try {
    res = await api.get(url, {
      responseType: 'blob',
      skipBusinessError: true,
      // Handle errors here (incl. 429 rate limit) so the global interceptor
      // doesn't also pop a generic toast.
      skipErrorHandler: true,
      disableDuplicate: true,
    } as Record<string, unknown>)
  } catch (err) {
    const status = (err as { response?: { status?: number } })?.response?.status
    if (status === 429) {
      throw new Error('TOPUP_EXPORT_RATE_LIMITED', { cause: err })
    }
    throw err instanceof Error
      ? err
      : new Error('Export failed', { cause: err })
  }

  const headers = (res.headers || {}) as Record<string, string>
  const contentType = String(headers['content-type'] || '')

  if (contentType.includes('application/json')) {
    let message = ''
    try {
      const text = await (res.data as Blob).text()
      message = (JSON.parse(text) as { message?: string })?.message || ''
    } catch {
      /* ignore parse failure, fall back to generic message */
    }
    throw new Error(message || 'Export failed')
  }

  return {
    blob: res.data as Blob,
    filename: parseContentDispositionFilename(
      headers['content-disposition'],
      'topup-export.csv'
    ),
    truncated: String(headers['x-export-truncated'] || '') === 'true',
    maxRows: Number(headers['x-export-max-rows']) || undefined,
  }
}

/**
 * Export current user's billing history as CSV (respects filters).
 */
export async function exportUserBillingHistory(
  filters: BillingHistoryFilters = {}
): Promise<BillingExportResult> {
  const params = buildBillingFilterParams(filters, false)
  const qs = params.toString()
  return requestBillingExport(
    `/api/user/topup/self/export${qs ? `?${qs}` : ''}`
  )
}

/**
 * Export all users' billing history as CSV (admin only, respects filters).
 */
export async function exportAllBillingHistory(
  filters: BillingHistoryFilters = {}
): Promise<BillingExportResult> {
  const params = buildBillingFilterParams(filters, true)
  const qs = params.toString()
  return requestBillingExport(`/api/user/topup/export${qs ? `?${qs}` : ''}`)
}

/**
 * Complete a pending order (admin only)
 */
export async function completeOrder(
  request: CompleteOrderRequest
): Promise<ApiResponse> {
  const res = await api.post('/api/user/topup/complete', request)
  return res.data
}

/**
 * Query and persist upstream payment status for one or more top-up orders.
 * The backend route is administrator-only.
 */
export async function queryTopUpPlatformStatus(
  tradeNos: string[]
): Promise<ApiResponse<PlatformStatusQueryResponse>> {
  const res = await api.post('/api/user/topup/platform-status', {
    trade_nos: tradeNos,
  })
  return res.data
}

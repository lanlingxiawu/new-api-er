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
import type { StatusBadgeProps } from '@/components/status-badge'
import { formatTimestampToDate } from '@/lib/format'

import type { TopupStatus } from '../types'

// ============================================================================
// Billing Utility Functions
// ============================================================================

interface StatusConfig {
  variant: StatusBadgeProps['variant']
  label: string
}

/**
 * Status badge configuration
 */
export const STATUS_CONFIG: Record<TopupStatus, StatusConfig> = {
  success: {
    variant: 'success',
    label: 'Success',
  },
  pending: {
    variant: 'warning',
    label: 'Pending',
  },
  failed: {
    variant: 'danger',
    label: 'Failed',
  },
  expired: {
    variant: 'danger',
    label: 'Expired',
  },
}

/**
 * Get status badge configuration
 */
export function getStatusConfig(status: TopupStatus): StatusConfig {
  return STATUS_CONFIG[status] || STATUS_CONFIG.pending
}

/**
 * Payment method display names. Keys are the raw `payment_method` values stored
 * by the backend (see model/topup.go PaymentMethod* constants).
 */
export const PAYMENT_METHOD_NAMES: Record<string, string> = {
  stripe: 'Stripe',
  creem: 'Creem',
  waffo: 'Waffo',
  waffo_pancake: 'Waffo',
  alipay: 'Alipay',
  alipay_official: 'Alipay',
  wxpay: 'WeChat Pay',
  wechat_official: 'WeChat Pay',
  balance: 'Balance',
  infini: 'Infini',
}

/**
 * Payment methods offered as filter options in the billing history dialog.
 */
export const PAYMENT_METHOD_FILTER_OPTIONS = [
  'stripe',
  'creem',
  'waffo',
  'alipay_official',
  'wechat_official',
  'infini',
] as const

/**
 * Enable flags (subset of TopupInfo) used to decide which payment methods are
 * currently turned on by the admin.
 */
export interface PaymentMethodEnableFlags {
  enable_stripe_topup?: boolean
  enable_creem_topup?: boolean
  enable_waffo_topup?: boolean
  enable_alipay_official_topup?: boolean
  enable_wechat_official_topup?: boolean
  enable_infini_topup?: boolean
}

/**
 * Return the payment-method filter options that are currently enabled, in the
 * canonical order. When the billing history filter is shown, only enabled
 * methods should be selectable.
 */
export function getEnabledPaymentMethods(
  flags?: PaymentMethodEnableFlags | null
): string[] {
  if (!flags) return []
  const mapping: Array<[string, boolean | undefined]> = [
    ['stripe', flags.enable_stripe_topup],
    ['creem', flags.enable_creem_topup],
    ['waffo', flags.enable_waffo_topup],
    ['alipay_official', flags.enable_alipay_official_topup],
    ['wechat_official', flags.enable_wechat_official_topup],
    ['infini', flags.enable_infini_topup],
  ]
  return mapping.filter(([, enabled]) => Boolean(enabled)).map(([method]) => method)
}

/**
 * Get payment method display name
 */
export function getPaymentMethodName(
  method: string,
  t?: (key: string) => string
): string {
  const name = PAYMENT_METHOD_NAMES[method] || method
  return t ? t(name) : name
}

/**
 * Currency symbols for common settlement currencies (Infini multi-currency).
 */
const CURRENCY_SYMBOLS: Record<string, string> = {
  USD: '$',
  CNY: '¥',
  EUR: '€',
  GBP: '£',
  JPY: '¥',
  HKD: 'HK$',
  AUD: 'A$',
  CAD: 'C$',
  SGD: 'S$',
}

/**
 * Format a paid amount with its settlement currency symbol.
 *
 * Used for Infini (and other multi-currency) records whose `money` field is
 * denominated in `payment_currency` (USD / CNY / ...), NOT the system display
 * currency — so a fixed `¥` prefix would be wrong. Falls back to the bare
 * number when no currency is known (legacy orders).
 */
export function formatPaymentAmount(
  money: number | null | undefined,
  currency?: string | null
): string {
  const value = Number(money) || 0
  const code = (currency || '').trim().toUpperCase()
  if (!code) {
    return value.toFixed(2)
  }
  const symbol = CURRENCY_SYMBOLS[code]
  if (symbol) {
    return `${symbol}${value.toFixed(2)}`
  }
  // Unknown currency code: show the amount followed by the ISO code.
  return `${value.toFixed(2)} ${code}`
}

/**
 * Format timestamp to readable date string
 */
export function formatTimestamp(timestamp: number): string {
  return formatTimestampToDate(timestamp)
}

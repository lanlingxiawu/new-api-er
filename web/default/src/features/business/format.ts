import { getCurrencyDisplay } from '@/lib/currency'

const BUSINESS_AMOUNT_DIGITS = 4

function formatNumber(value: number) {
  return new Intl.NumberFormat(undefined).format(value)
}

function formatFixedAmount(value: number, digits = BUSINESS_AMOUNT_DIGITS) {
  const fixed = value.toFixed(digits)
  if (Number.parseFloat(fixed) === 0 && value > 0) {
    const minValue = Math.pow(10, -digits)
    return minValue.toFixed(digits)
  }
  return fixed
}

export function formatBusinessAmount(
  quota: number | null | undefined,
  digits = BUSINESS_AMOUNT_DIGITS
) {
  const amount = Number(quota || 0)
  const { config, meta } = getCurrencyDisplay()

  if (meta.kind === 'tokens') {
    return formatNumber(amount)
  }

  const usd = amount / config.quotaPerUnit
  const value = usd * meta.exchangeRate
  return `${meta.symbol}${formatFixedAmount(value, digits)}`
}

export function formatBusinessFullNumber(value: number | null | undefined) {
  if (value === undefined || value === null || value === 0) return '0'
  const raw = String(value)
  if (!/[eE]/.test(raw)) return raw

  const number = Number(value)
  if (!Number.isFinite(number)) return raw

  const [coefficient, exponentPart] = raw.toLowerCase().split('e')
  const exponent = Number(exponentPart)
  if (!Number.isFinite(exponent)) return raw

  const sign = coefficient.startsWith('-') ? '-' : ''
  const unsigned = sign ? coefficient.slice(1) : coefficient
  const [integer, fraction = ''] = unsigned.split('.')
  const digits = `${integer}${fraction}`
  const decimalIndex = integer.length + exponent

  if (decimalIndex <= 0) {
    return `${sign}0.${'0'.repeat(Math.abs(decimalIndex))}${digits}`
  }
  if (decimalIndex >= digits.length) {
    return `${sign}${digits}${'0'.repeat(decimalIndex - digits.length)}`
  }
  return `${sign}${digits.slice(0, decimalIndex)}.${digits.slice(decimalIndex)}`
}

export function formatBusinessUsd(value: number | null | undefined) {
  return `~ $${formatBusinessFullNumber(value)}`
}

export function formatBusinessExactUsd(value: number | null | undefined) {
  return `$${formatBusinessFullNumber(value)}`
}

export function formatBusinessTargetAmount(value: number | null | undefined) {
  const amount = Number(value || 0)
  return Number.isFinite(amount) ? `$${amount.toFixed(2)}` : '$0.00'
}

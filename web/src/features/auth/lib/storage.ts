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
/**
 * Utilities for managing authentication-related browser storage
 */

// ============================================================================
// LocalStorage Keys
// ============================================================================

const STORAGE_KEYS = {
  USER_ID: 'uid',
  AFFILIATE: 'aff',
  STATUS: 'status',
  PENDING_TWO_FA_FLOW: 'auth:pending-2fa:v1',
} as const

export interface PendingTwoFAFlow {
  flowToken: string
  expiresAt: number
}

// ============================================================================
// User ID Storage
// ============================================================================

/**
 * Save user ID to localStorage
 */
export function saveUserId(userId: number | string): void {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(STORAGE_KEYS.USER_ID, String(userId))
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to save user ID:', error)
  }
}

/**
 * Get user ID from localStorage
 */
export function getUserId(): string | null {
  if (typeof window === 'undefined') return null
  try {
    return window.localStorage.getItem(STORAGE_KEYS.USER_ID)
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to get user ID:', error)
    return null
  }
}

/**
 * Remove user ID from localStorage
 */
export function removeUserId(): void {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.removeItem(STORAGE_KEYS.USER_ID)
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to remove user ID:', error)
  }
}

// ============================================================================
// Pending Two-Factor Login Flow
// ============================================================================

export function savePendingTwoFAFlow(
  flowToken: string,
  expiresAt: number
): boolean {
  if (
    typeof window === 'undefined' ||
    !flowToken.trim() ||
    !Number.isFinite(expiresAt)
  ) {
    return false
  }

  try {
    const flow: PendingTwoFAFlow = {
      flowToken: flowToken.trim(),
      expiresAt,
    }
    window.sessionStorage.setItem(
      STORAGE_KEYS.PENDING_TWO_FA_FLOW,
      JSON.stringify(flow)
    )
    return true
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to save pending 2FA login flow:', error)
    return false
  }
}

export function getPendingTwoFAFlow(): PendingTwoFAFlow | null {
  if (typeof window === 'undefined') return null

  try {
    const raw = window.sessionStorage.getItem(STORAGE_KEYS.PENDING_TWO_FA_FLOW)
    if (!raw) return null

    const value = JSON.parse(raw) as Partial<PendingTwoFAFlow>
    if (
      typeof value.flowToken !== 'string' ||
      !value.flowToken.trim() ||
      typeof value.expiresAt !== 'number' ||
      !Number.isFinite(value.expiresAt)
    ) {
      window.sessionStorage.removeItem(STORAGE_KEYS.PENDING_TWO_FA_FLOW)
      return null
    }

    return {
      flowToken: value.flowToken,
      expiresAt: value.expiresAt,
    }
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to read pending 2FA login flow:', error)
    try {
      window.sessionStorage.removeItem(STORAGE_KEYS.PENDING_TWO_FA_FLOW)
    } catch {
      // Storage is unavailable; there is nothing else to clean up.
    }
    return null
  }
}

export function removePendingTwoFAFlow(): void {
  if (typeof window === 'undefined') return

  try {
    window.sessionStorage.removeItem(STORAGE_KEYS.PENDING_TWO_FA_FLOW)
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to remove pending 2FA login flow:', error)
  }
}

// ============================================================================
// Affiliate Code Storage
// ============================================================================

/**
 * Get affiliate code from localStorage
 */
export function getAffiliateCode(): string {
  if (typeof window === 'undefined') return ''
  try {
    return window.localStorage.getItem(STORAGE_KEYS.AFFILIATE) ?? ''
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to get affiliate code:', error)
    return ''
  }
}

/**
 * Save affiliate code to localStorage
 */
export function saveAffiliateCode(code: string): void {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(STORAGE_KEYS.AFFILIATE, code)
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to save affiliate code:', error)
  }
}

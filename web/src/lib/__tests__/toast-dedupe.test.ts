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
import { describe, expect, it } from 'vitest'

import { TOAST_BURST_WINDOW_MS, burstToastId } from '@/lib/toast-dedupe'

describe('burstToastId', () => {
  it('reuses one id while repeated triggers stay within the window', () => {
    const start = 1_000_000
    const first = burstToastId('burst-chain', start)
    expect(burstToastId('burst-chain', start + TOAST_BURST_WINDOW_MS)).toBe(
      first
    )
    // The window slides from the latest trigger, so a long save chain stays merged.
    expect(
      burstToastId('burst-chain', start + 2 * TOAST_BURST_WINDOW_MS)
    ).toBe(first)
  })

  it('starts a new notification once the gap exceeds the window', () => {
    const start = 2_000_000
    const first = burstToastId('burst-gap', start)
    const second = burstToastId(
      'burst-gap',
      start + TOAST_BURST_WINDOW_MS + 1
    )
    expect(second).not.toBe(first)
  })

  it('keeps different events independent at the same instant', () => {
    const now = 3_000_000
    expect(burstToastId('burst-a', now)).not.toBe(burstToastId('burst-b', now))
  })
})

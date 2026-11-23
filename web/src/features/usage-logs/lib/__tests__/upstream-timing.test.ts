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

import { getExchangeIntervalMs } from '../upstream-timing'

describe('upstream timing intervals', () => {
  it.each([
    [43, 639000, 638957],
    [639000, 655000, 16000],
    [0, 0, 0],
    [undefined, 10, undefined],
    [0, undefined, undefined],
    [100, 99, undefined],
    [-1, 10, undefined],
    [0, -1, undefined],
    [Number.NaN, 10, undefined],
    [0, Infinity, undefined],
    ['0', 10, undefined],
    [null, 10, undefined],
  ])('validates %s → %s', (start, end, expected) => {
    expect(getExchangeIntervalMs(start, end)).toBe(expected)
  })
})

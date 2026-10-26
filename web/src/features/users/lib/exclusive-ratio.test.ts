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
import { expect, it } from 'vitest'

import { hasExclusiveGroupRatios } from './exclusive-ratio'

it.each([
  [undefined, false],
  ['', false],
  [' \t\n', false],
  ['{}', false],
  [' {\n} ', false],
  ['null', false],
  ['{"vip":0}', true],
  ['{"vip":0.5,"other":2}', true],
  [' {"vip":1.2e-3} ', true],
  ['{"vip":null}', true],
  ['{"vip":-1}', true],
  ['{"vip":', false],
  ['[1]', false],
  ['"vip"', false],
  ['{"vip":"1"}', false],
  ['{"vip":{"ratio":1}}', false],
  ['{"vip":true}', false],
  ['{"vip":01}', false],
])('identifies stored exclusive configuration %s as %s', (raw, expected) => {
  expect(hasExclusiveGroupRatios(raw)).toBe(expected)
})

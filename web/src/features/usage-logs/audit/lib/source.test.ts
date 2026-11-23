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

import { identifyAuditClient, auditActorLabel } from './source'

describe('audit source display', () => {
  it.each([
    [
      'Mozilla/5.0 (Windows NT 10.0) Chrome/130 Safari/537 Edg/130',
      'Edge · Windows',
    ],
    ['Mozilla/5.0 (iPhone) CriOS/120 Mobile Safari/604', 'Chrome · iOS'],
    ['Mozilla/5.0 Chrome/130 Electron/32', 'Electron'],
    ['Go-http-client/1.1', 'Go HTTP Client'],
    ['node', 'Node.js'],
    ['<script>unknown</script>', 'Other client'],
    ['', 'Unknown client'],
  ])('identifies %s', (ua, expected) => {
    expect(identifyAuditClient(ua)).toBe(expected)
  })
  it('keeps missing employee snapshots distinct from false', () => {
    expect(auditActorLabel(1, null)).toBe('User (employee status not recorded)')
    expect(auditActorLabel(1, false)).toBe('User')
    expect(auditActorLabel(1, true)).toBe('Employee')
    expect(auditActorLabel(10, true)).toBe('Administrator')
    expect(auditActorLabel(0, null)).toBe('Unknown identity')
  })
})

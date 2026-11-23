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

import type { AuditLog } from '../api'
import { readAuditChanges } from './audit-changes'

const entry = (changes: unknown) => ({
  action: 'channel.update',
  success: true,
  other: { changes },
})

describe('audit field changes', () => {
  it('preserves zero, false, null, empty strings and decimal integer strings', () => {
    const values = [0, false, null, '', '9007199254740993']
    const changes = readAuditChanges(
      entry({
        version: 1,
        outcome: 'committed',
        targets: [
          {
            resource: 'channel',
            id: '1',
            fields: values.map((value, i) => ({
              path: ['priority', String(i)],
              old: { state: 'absent' },
              new: { state: 'value', value },
            })),
          },
        ],
      })
    )
    expect(changes?.targets[0].fields.map((field) => field.new.value)).toEqual(
      values
    )
  })

  it('never renders credentials even if a malformed response includes their values', () => {
    const changes = readAuditChanges(
      entry({
        version: 1,
        outcome: 'committed',
        targets: [
          {
            resource: 'channel',
            id: '1',
            fields: ['key', 'password', 'client_secret', 'header_override'].map(
              (key) => ({
                path: [key],
                old: { state: 'value', value: 'old-secret' },
                new: { state: 'redacted', value: 'new-secret' },
              })
            ),
          },
        ],
      })
    )
    expect(JSON.stringify(changes)).not.toContain('old-secret')
    expect(JSON.stringify(changes)).not.toContain('new-secret')
    expect(
      changes?.targets[0].fields.every(
        (field) => field.old.state === 'redacted'
      )
    ).toBe(true)
  })

  it('adapts only known historical old/new parameters and never guesses missing values', () => {
    const historical: Pick<AuditLog, 'action' | 'other' | 'success'> = {
      action: 'token.status_update',
      success: true,
      other: { op: { params: { id: 7, from: 1, to: 2 } } },
    }
    expect(readAuditChanges(historical)?.targets[0].fields[0]).toMatchObject({
      path: ['status'],
      old: { value: 1 },
      new: { value: 2 },
    })
    expect(
      readAuditChanges({ ...historical, action: 'unknown.update' })
    ).toBeNull()
    expect(readAuditChanges({ ...historical, other: null })).toBeNull()
  })
})

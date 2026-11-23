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

import { transformFormDataToPayload } from '@/features/users/lib/user-form'

import { buildSettingJSON, CHANNEL_FORM_DEFAULT_VALUES } from './channel-form'

describe('Stream protocol validation overrides', () => {
  it('does not silently migrate an unchanged legacy channel or user value', () => {
    expect(
      JSON.parse(
        buildSettingJSON({
          ...CHANNEL_FORM_DEFAULT_VALUES,
          claude_stream_validation: false,
        })
      )
    ).not.toHaveProperty('stream_protocol_validation')
    expect(
      transformFormDataToPayload(
        { username: 'test', claude_stream_validation: false },
        12
      )
    ).not.toHaveProperty('stream_protocol_validation')
  })
  it.each([true, false, null])('preserves the %s channel setting', (value) => {
    const setting = JSON.parse(
      buildSettingJSON({
        ...CHANNEL_FORM_DEFAULT_VALUES,
        stream_protocol_validation: value,
      })
    )
    expect(setting.stream_protocol_validation).toBe(value)
  })
  it.each([true, false, null])(
    'preserves the %s user setting on create and edit',
    (value) => {
      for (const id of [undefined, 12]) {
        expect(
          transformFormDataToPayload(
            { username: 'test', stream_protocol_validation: value },
            id
          ).stream_protocol_validation
        ).toBe(value)
      }
    }
  )
  it('does not erase user overrides when another client omits the field', () => {
    expect(
      transformFormDataToPayload({ username: 'test' }, 12)
    ).not.toHaveProperty('stream_protocol_validation')
  })
})

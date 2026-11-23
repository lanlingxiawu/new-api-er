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
import type { TFunction } from 'i18next'
import { expect, it } from 'vitest'

import {
  autoBanValidation,
  defaultAutoBanRules,
  parseAutoBanRules,
} from './auto-ban'

const t = ((key: string) => key) as TFunction

it('supports all independent action combinations and the capture dependency', () => {
  for (const target of defaultAutoBanRules) {
    for (let mask = 0; mask < 8; mask++) {
      const rules = defaultAutoBanRules.map((rule) => ({
        ...rule,
        selected: rule.id === target.id && !!(mask & 1),
        clear_api_keys: rule.id === target.id && !!(mask & 2),
        clear_access_tokens: rule.id === target.id && !!(mask & 4),
      }))
      const values = {
        auto_ban_enabled: 'true',
        capture_enabled: 'true',
        ip_intelligence_enabled: 'true',
        ip_intelligence_providers_json: JSON.stringify([
          {
            id: 'selfhost',
            type: 'custom_ipapi',
            enabled: true,
            capabilities: ['abuse'],
          },
        ]),
        auto_ban_rules_json: JSON.stringify(rules),
      }
      expect(autoBanValidation(values, t)).toBe(
        mask === 0 ? 'Select at least one automatic action.' : undefined
      )
      if (target.id === 'session_reuse' && mask !== 0) {
        expect(
          autoBanValidation({ ...values, capture_enabled: 'false' }, t)
        ).toBe(
          'Enable sign-in security event capture to use the session replay rule.'
        )
      }
    }
  }
})

it('keeps new rules alert-only when loading old settings and checks IP evidence dependency', () => {
  const oldRules = defaultAutoBanRules.slice(0, 4)
  const parsed = parseAutoBanRules(JSON.stringify(oldRules))
  expect(parsed).toHaveLength(7)
  expect(
    parsed
      ?.slice(4)
      .every(
        (rule) =>
          !rule.selected && !rule.clear_api_keys && !rule.clear_access_tokens
      )
  ).toBe(true)
  const rules = defaultAutoBanRules.map((rule) => ({
    ...rule,
    selected: false,
    clear_api_keys: rule.id === 'risky_ip_sensitive_operation',
  }))
  expect(
    autoBanValidation(
      {
        auto_ban_enabled: 'true',
        capture_enabled: 'true',
        auto_ban_rules_json: JSON.stringify(rules),
      },
      t
    )
  ).toBe(
    'Enable IP lookup and configure an abuse data source before using high-risk IP rules.'
  )
  expect(
    parseAutoBanRules(JSON.stringify([...oldRules.slice(1), oldRules[3]]))
  ).toBeUndefined()
})

it('defaults legacy cleanup fields off and rejects malformed actions', () => {
  const legacy = defaultAutoBanRules.map(
    ({ clear_api_keys: _api, clear_access_tokens: _access, ...rule }) => rule
  )
  const parsed = parseAutoBanRules(JSON.stringify(legacy))
  expect(parsed).toBeDefined()
  if (!parsed) throw new Error('Legacy settings must parse')
  expect(
    parsed.every((rule) => !rule.clear_api_keys && !rule.clear_access_tokens)
  ).toBe(true)
  for (const value of [null, 'true', 1]) {
    expect(
      parseAutoBanRules(
        JSON.stringify([
          { ...legacy[0], clear_api_keys: value },
          ...legacy.slice(1),
        ])
      )
    ).toBeUndefined()
  }
})

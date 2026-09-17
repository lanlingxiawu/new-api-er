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
import assert from 'node:assert/strict'

import { describe, test } from 'vitest'

import type { UsageLog } from '../../data/schema'
import { collectLogMetrics, findLowerMetrics } from '../log-metrics'

function mkLog(
  patch: Partial<UsageLog>,
  other: Record<string, unknown>
): UsageLog {
  return {
    id: 1,
    user_id: 0,
    token_id: 0,
    username: '',
    created_at: 1,
    type: 2,
    content: '',
    request_id: 'r',
    upstream_request_id: '',
    model_name: 'm',
    token_name: '',
    quota: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
    use_time: 0,
    is_stream: false,
    channel: 0,
    channel_name: '',
    group: '',
    ip: '',
    other: JSON.stringify(other),
    ...patch,
  }
}

// base64 of a one-tier expression: input 5, output 25, cache read 0.5, cache write 6.25, 1h 10
const TIERED_EXPR_B64 = Buffer.from(
  'tier("standard", p * 5 + c * 25 + cr * 0.5 + cc * 6.25 + cc1h * 10)'
).toString('base64')

describe('collectLogMetrics', () => {
  test('ratio billing: tokens, base unit prices, effective group ratio and total cost', () => {
    const m = collectLogMetrics(
      mkLog(
        { prompt_tokens: 8, completion_tokens: 182, quota: 3254 },
        {
          model_ratio: 2.5,
          completion_ratio: 5,
          cache_ratio: 0.1,
          cache_creation_ratio: 1.25,
          cache_creation_ratio_5m: 1.25,
          cache_creation_ratio_1h: 2,
          cache_tokens: 37,
          cache_creation_tokens_5m: 304,
          group_ratio: 1,
          user_group_ratio: -1,
        }
      )
    )
    assert.equal(m['tokens.input'], 8)
    assert.equal(m['tokens.output'], 182)
    assert.equal(m['tokens.cache_read'], 37)
    assert.equal(m['tokens.cache_write_5m'], 304)
    assert.equal(m['price.input'], 5)
    assert.equal(m['price.output'], 25)
    assert.equal(m['price.cache_read'], 0.5)
    assert.equal(m['price.cache_write'], 6.25)
    assert.equal(m['price.cache_write_5m'], 6.25)
    assert.equal(m['price.cache_write_1h'], 10)
    assert.equal(m.group_ratio, 1)
    assert.equal(m.total_cost, 3254)
  })

  test('a user exclusive ratio overrides the group ratio, as the detail row shows', () => {
    const m = collectLogMetrics(
      mkLog({}, { group_ratio: 1, user_group_ratio: 0.8 })
    )
    assert.equal(m.group_ratio, 0.8)
  })

  test('tiered billing maps the matched tier prices onto the same keys', () => {
    const m = collectLogMetrics(
      mkLog(
        {},
        {
          billing_mode: 'tiered_expr',
          expr_b64: TIERED_EXPR_B64,
          matched_tier: 'standard',
          cache_tokens: 37,
        }
      )
    )
    assert.equal(m['price.input'], 5)
    assert.equal(m['price.output'], 25)
    assert.equal(m['price.cache_read'], 0.5)
    assert.equal(m['price.cache_write'], 6.25)
    assert.equal(m['price.cache_write_1h'], 10)
  })

  test('per-call billing records the call price instead of per-token prices', () => {
    const m = collectLogMetrics(
      mkLog({}, { model_price: 0.04, model_ratio: 2.5 })
    )
    assert.equal(m['price.per_call'], 0.04)
    assert.equal(m['price.input'], undefined)
  })

  test('unparseable other still yields token counts and total cost', () => {
    const m = collectLogMetrics({
      ...mkLog({ prompt_tokens: 3, quota: 9 }, {}),
      other: '',
    })
    assert.equal(m['tokens.input'], 3)
    assert.equal(m.total_cost, 9)
    assert.equal(m.group_ratio, undefined)
  })
})

describe('findLowerMetrics', () => {
  test('flags only keys where the local value is strictly lower, returning the upstream value', () => {
    const lower = findLowerMetrics(
      {
        'tokens.output': 100,
        'price.input': 5,
        group_ratio: 1,
        total_cost: 10,
      },
      {
        'tokens.output': 182,
        'price.input': 5,
        group_ratio: 0.3,
        total_cost: 30,
      }
    )
    assert.deepEqual(lower, { 'tokens.output': 182, total_cost: 30 })
  })

  test('equal and higher values are never flagged, including float noise', () => {
    const lower = findLowerMetrics(
      { 'price.cache_read': 0.1 + 0.2, total_cost: 31 },
      { 'price.cache_read': 0.3, total_cost: 30 }
    )
    assert.deepEqual(lower, {})
  })

  test('a metric present on only one side is not compared', () => {
    const lower = findLowerMetrics(
      { 'price.cache_write_5m': 1 },
      { 'price.cache_write_1h': 10 }
    )
    assert.deepEqual(lower, {})
  })
})

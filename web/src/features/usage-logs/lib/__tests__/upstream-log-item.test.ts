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
import { describe, test } from 'node:test'

import { upstreamItemToUsageLog } from '../upstream-log-item'

describe('upstreamItemToUsageLog', () => {
  // The upstream pane renders an upstream log with the very same layout as the local
  // log detail, so every field that layout reads has to survive the mapping.
  test('carries every field the shared log detail layout renders', () => {
    const log = upstreamItemToUsageLog({
      id: 9,
      created_at: 1_700_000_000,
      type: 2,
      request_id: 'req-up',
      upstream_request_id: 'req-up-2',
      model_name: 'gemini-2.5-flash',
      token_name: 'zzrecon',
      quota: 20,
      prompt_tokens: 3,
      completion_tokens: 30,
      use_time: 1,
      is_stream: true,
      content: 'note',
      channel: 12,
      channel_name: 'up-ch',
      group: 'vip',
      ip: '1.2.3.4',
      other: {
        request_conversion: ['OpenAI Compatible', 'Google Gemini'],
        billing_mode: 'ratio',
      },
    })

    assert.equal(log.id, 9)
    assert.equal(log.created_at, 1_700_000_000)
    assert.equal(log.type, 2)
    assert.equal(log.request_id, 'req-up')
    assert.equal(log.upstream_request_id, 'req-up-2')
    assert.equal(log.model_name, 'gemini-2.5-flash')
    assert.equal(log.token_name, 'zzrecon')
    assert.equal(log.quota, 20)
    assert.equal(log.prompt_tokens, 3)
    assert.equal(log.completion_tokens, 30)
    assert.equal(log.use_time, 1)
    assert.equal(log.is_stream, true)
    assert.equal(log.content, 'note')
    assert.equal(log.channel, 12)
    assert.equal(log.channel_name, 'up-ch')
    assert.equal(log.group, 'vip')
    assert.equal(log.ip, '1.2.3.4')
    // The table's UsageLog stores `other` as a JSON string; the layout parses it.
    assert.deepEqual(JSON.parse(log.other), {
      request_conversion: ['OpenAI Compatible', 'Google Gemini'],
      billing_mode: 'ratio',
    })
  })

  // Missing upstream values must read as "absent" so the layout hides those rows instead
  // of rendering "undefined": no channel -> 0, no other -> empty string.
  test('missing channel and other map to their empty values', () => {
    const log = upstreamItemToUsageLog({
      id: 1,
      created_at: 1,
      type: 2,
      request_id: 'r',
      model_name: 'm',
      quota: 0,
      prompt_tokens: 0,
      completion_tokens: 0,
      use_time: 0,
      is_stream: false,
    })
    assert.equal(log.channel, 0)
    assert.equal(log.other, '')
    assert.equal(log.upstream_request_id, '')
    assert.equal(log.content, '')
  })
})

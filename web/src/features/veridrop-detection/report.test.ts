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

import {
  buildDetailedDetectionReportHtml,
  getVeridropResultDisplayMessage,
  hasVeridropScoreReport,
  parseVeridropScoreReport,
} from './report'
import type { VeridropDetectionResult } from './types'

const result: VeridropDetectionResult = {
  id: 1,
  channel_id: 2,
  channel_name: 'Example channel',
  channel_type: 1,
  protocol: 'openai',
  model: 'gpt-test',
  mode: 'quick',
  status: 'done',
  outcome: 'passed',
  veridrop_job_id: 'job-1',
  batch_task_id: 'batch-1',
  score: 86,
  verdict: 'passed',
  summary: 'Passed',
  run_error: '',
  error: '',
  result_json: JSON.stringify({
    total_score: 86,
    verdict: 'passed',
    results: [
      {
        name: 'protocol',
        display_name: 'Protocol check',
        status: 'pass',
        score: 86,
        weight: 1,
        details: {
          issues: [{ severity: 'low', message: 'first' }],
          note: '</script><script>boom()</script>',
        },
      },
    ],
  }),
  created_at: 1,
  started_at: 1,
  finished_at: 1,
  updated_at: 1,
}

describe('detailed detection report', () => {
  test('keeps records, checks, and evidence collapsed until requested', () => {
    const html = buildDetailedDetectionReportHtml(
      [result],
      (key) => key,
      () => '2026-08-16 10:00',
      '2026-08-16 10:00',
      [],
      'en'
    )

    assert.match(html, /<details class="record">/)
    assert.match(html, /class="record-quick-meta"/)
    assert.match(html, /<details class="check-card">/)
    assert.match(html, /<details class="evidence" data-evidence-id="0">/)
    assert.match(html, /data-evidence-id="0"/)
    assert.match(html, /data-raw-evidence-id="0"/)
    assert.doesNotMatch(html, /<dl class="evidence-object">/)
    assert.doesNotMatch(html, /<details class="record" open>/)
    assert.doesNotMatch(html, /<details class="check-card" open>/)
    assert.match(html, /Protocol check/)
    assert.match(html, /first/)
    assert.equal((html.match(/<\/script>/g) ?? []).length, 1)
    assert.match(html, /\\u003cscript\\u003eboom\(\)\\u003c\/script\\u003e/)
  })

  test('parses every check and preserves all nested evidence entries', () => {
    const source = JSON.stringify({
      total_score: 120,
      verdict: 'marginal',
      performance: { request_count: 4, total_latency_ms: 1234 },
      results: [
        {
          name: 'one',
          status: 'PASSED',
          score: 110,
          weight: 15,
          details: { issues: [{ code: 'a' }, { code: 'b' }] },
        },
        {
          name: 'two',
          status: 'skipped',
          score: -3,
          weight: 20,
          details: { skip_reason: 'mode-excluded', observations: [1, 2, 3] },
        },
        {
          name: 'three',
          status: 'FAILED',
          score: 40,
          weight: 5,
          details: { critical_issue_count: 1 },
        },
      ],
    })

    const report = parseVeridropScoreReport(source)
    assert.ok(report)
    assert.equal(report.totalScore, 100)
    assert.equal(report.checks.length, 3)
    assert.deepEqual(
      report.checks.map((check) => check.status),
      ['pass', 'skip', 'fail']
    )
    assert.deepEqual(report.checks[0]?.details.issues, [
      { code: 'a' },
      { code: 'b' },
    ])
    assert.deepEqual(report.checks[1]?.details.observations, [1, 2, 3])
    assert.equal(report.checks[1]?.skipReason, 'mode-excluded')
    assert.equal(report.hasCriticalIssues, true)
  })

  test('rejects malformed or empty score reports', () => {
    for (const source of ['', '{', '{}', '{"results":[]}', '{"results":[1]}']) {
      assert.equal(parseVeridropScoreReport(source), null)
      assert.equal(hasVeridropScoreReport(source), false)
    }
  })

  test('extracts JSON messages without breaking escaped quotes', () => {
    const withMessage = {
      ...result,
      error: JSON.stringify({ message: 'invalid "quoted" token' }),
    }
    assert.equal(
      getVeridropResultDisplayMessage(withMessage),
      'invalid "quoted" token'
    )
    assert.equal(
      getVeridropResultDisplayMessage({
        ...result,
        error: JSON.stringify({ detail: 'fallback detail' }),
      }),
      'fallback detail'
    )
  })
})

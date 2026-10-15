import assert from 'node:assert/strict'

import { test } from 'vitest'

import type { MonitorPricingScenario } from '../price-monitor-pricing-api'
import { ruleConditionExamples } from '../price-monitor-rule-scenarios'
import type { RuleDocument, RuleNode } from '../price-monitor-rule-types'

const base: MonitorPricingScenario = {
  id: 'base',
  usage_semantics: 'openai',
  usage: {
    prompt_tokens: 1000,
    completion_tokens: 25,
    prompt_tokens_details: { cached_tokens: 800 },
  },
  request_body: { options: { other: 'kept' } },
  headers: { 'X-Quality': 'old' },
  at: '2026-10-13T00:00:00Z',
}
const literal = (kind: RuleNode['kind'], value?: string): RuleNode => ({
  id: 'literal',
  kind,
  value,
})
const call = (name: string, value: string): RuleNode => ({
  id: 'call',
  kind: 'call',
  name,
  args: [literal('string', value)],
})
const compare = (
  left: RuleNode,
  right: RuleNode,
  operator = '=='
): RuleNode => ({
  id: 'compare',
  kind: 'binary',
  operator,
  args: [left, right],
})
const doc = (root: RuleNode): RuleDocument => ({
  schema_version: 1,
  expression_version: 1,
  source_hash: 'test',
  root,
})

test('length examples use integer boundaries and correct raw usage without changing base', () => {
  const before = structuredClone(base)
  for (const usage_semantics of ['openai', 'claude']) {
    const result = ruleConditionExamples(
      doc(
        compare(
          { id: 'len', kind: 'usage', name: 'len' },
          literal('number', '100'),
          '<='
        )
      ),
      { ...base, usage_semantics }
    )
    assert.deepEqual(
      result.scenarios.map((s) => s.usage.prompt_tokens),
      [99, 100, 101]
    )
    assert.equal(result.skipped, 0)
    assert.ok(
      result.scenarios.every(
        (s) =>
          s.usage_semantics === usage_semantics &&
          (s.usage.prompt_tokens_details as Record<string, unknown>)
            .cached_tokens === 0
      )
    )
    assert.equal(new Set(result.scenarios.map((s) => s.id)).size, 3)
  }
  assert.deepEqual(base, before)
})

test('request samples retain zero false null types and nested siblings', () => {
  for (const [kind, value, expected] of [
    ['number', '0', 0],
    ['boolean', 'false', false],
    ['null', undefined, null],
  ] as const) {
    const result = ruleConditionExamples(
      doc(compare(call('param', 'options.quality'), literal(kind, value))),
      base
    )
    assert.equal(
      result.scenarios[0]?.request_body.options &&
        (result.scenarios[0].request_body.options as Record<string, unknown>)
          .quality,
      expected
    )
    const scenario = result.scenarios[0]
    assert.ok(scenario)
    assert.equal(
      (scenario.request_body.options as Record<string, unknown>).other,
      'kept'
    )
    assert.ok(result.scenarios.length >= 2)
  }
  const result = ruleConditionExamples(
    doc(compare(call('header', 'x-quality'), literal('string', 'high'))),
    base
  )
  assert.equal(result.scenarios[0]?.headers['x-quality'], 'high')
  assert.equal(result.scenarios[0]?.headers['X-Quality'], undefined)
})

test('timezone examples find matching and nonmatching local hours', () => {
  const result = ruleConditionExamples(
    doc(compare(call('hour', 'Asia/Shanghai'), literal('number', '9'))),
    base
  )
  const formatter = new Intl.DateTimeFormat('en-GB', {
    timeZone: 'Asia/Shanghai',
    hour: 'numeric',
    hourCycle: 'h23',
  })
  assert.ok(
    result.scenarios.some((s) => Number(formatter.format(new Date(s.at))) === 9)
  )
  assert.ok(
    result.scenarios.some((s) => Number(formatter.format(new Date(s.at))) !== 9)
  )
})

test('complex paths and condition groups report partial coverage and candidates cap at 31', () => {
  const unknown = doc(
    compare(call('param', 'items.#(price>10).name'), literal('string', 'x'))
  )
  assert.deepEqual(ruleConditionExamples(unknown, base), {
    scenarios: [],
    skipped: 1,
  })
  const conditions = Array.from({ length: 20 }, (_, i) =>
    compare(call('param', `value${i}`), literal('number', String(i)))
  )
  const root = conditions.reduce((left, right) => ({
    id: 'and',
    kind: 'binary',
    operator: 'and',
    args: [left, right],
  }))
  const result = ruleConditionExamples(doc(root), base)
  assert.equal(result.scenarios.length, 31)
  assert.ok(result.skipped > 0)
  assert.equal(new Set(result.scenarios.map((s) => s.id)).size, 31)
  assert.deepEqual(
    ruleConditionExamples(
      doc(
        compare(call('header', 'authorization'), literal('string', 'secret'))
      ),
      base
    ),
    { scenarios: [], skipped: 1 }
  )
})

test('time samples use actual local fields across DST and weekly boundaries', () => {
  for (const [name, value] of [
    ['hour', '2'],
    ['minute', '30'],
    ['weekday', '0'],
    ['day', '10'],
    ['month', '3'],
  ]) {
    assert.ok(name && value)
    const result = ruleConditionExamples(
      doc(compare(call(name, 'America/New_York'), literal('number', value))),
      { ...base, at: '2026-03-08T06:30:00Z' }
    )
    assert.equal(result.skipped, 0)
    assert.ok(result.scenarios.length >= 2)
  }
  assert.equal(
    ruleConditionExamples(
      doc(compare(call('hour', 'not-a-timezone'), literal('number', '9'))),
      base
    ).skipped,
    1
  )
})

test('unsafe object paths, overwritten parent scalars, unsupported semantics and huge thresholds are skipped', () => {
  for (const path of [
    '__proto__.polluted',
    'constructor.value',
    'options.0.value',
  ]) {
    assert.equal(
      ruleConditionExamples(
        doc(compare(call('param', path), literal('string', 'x'))),
        base
      ).skipped,
      1
    )
  }
  assert.equal(
    ruleConditionExamples(
      doc(
        compare(call('param', 'options.other.child'), literal('number', '2'))
      ),
      base
    ).skipped,
    1
  )
  assert.equal(
    ruleConditionExamples(
      doc(
        compare(
          { id: 'len', kind: 'usage', name: 'len' },
          literal('number', '1e15')
        )
      ),
      base
    ).skipped,
    1
  )
  assert.equal(
    ruleConditionExamples(
      doc(
        compare(
          { id: 'len', kind: 'usage', name: 'len' },
          literal('number', '10')
        )
      ),
      { ...base, usage_semantics: 'plugin' }
    ).skipped,
    1
  )
})

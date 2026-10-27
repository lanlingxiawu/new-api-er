/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'

import { expect, test } from 'vitest'

import {
  buildPricingChange,
  boundaryPricingScenarios,
  bulkExpressionPrice,
  expressionPriceFields,
  patchExpressionPrice,
  patchExpressionPrices,
  patchFixedPrice,
  parsePriceDraft,
  pricingDraftFingerprint,
  scaleExpressionPrices,
  legacyTokenPrice,
  patchLegacyTokenPrice,
  scalePricingDrafts,
  applyBatchTokenPrice,
  addPricingTier,
  removePricingTier,
  patchPricingThreshold,
} from '../price-monitor-pricing-draft'

test('12-hour cache prices remain editable in ordinary length tiers', () => {
  const expression =
    'len <= 272000 ? tier("standard", p*1+cc*1.25+cc12h*2.5+c*5) : tier("long", p*2+cc*2.5+cc12h*5+c*7.5)'
  const fields = expressionPriceFields(expression)
  expect(fields).toHaveLength(2)
  expect(fields?.map((field) => field.prices.cc12h)).toEqual(['2.5', '5'])
  const changed = patchExpressionPrice(expression, 1, 'cc12h', '0')
  expect(changed).toBe(expression.replace('cc12h*5', 'cc12h*0'))
  expect(expressionPriceFields(changed)?.[1]?.prices.cc12h).toBe('0')
  const scaled = scaleExpressionPrices(
    [
      {
        model_name: 'cache-model',
        expected_version: 'v1',
        pricing: { 'billing_setting.billing_expr': expression },
      },
    ],
    '100'
  )
  const scaledExpression = scaled[0]?.pricing['billing_setting.billing_expr']
  expect(typeof scaledExpression).toBe('string')
  expect(
    expressionPriceFields(String(scaledExpression))?.map(
      (field) => field.prices.cc12h
    )
  ).toEqual(['5', '10'])
})

test('ordinary expressions expose prices without requiring a tier wrapper', () => {
  for (const expression of [
    'p*2+c*4',
    'v1: (2 * p) + (c * 4)',
    'tier("base", (p*2)+(c*4))',
  ]) {
    const fields = expressionPriceFields(expression)
    assert.equal(fields?.length, 1)
    assert.equal(Number(fields?.[0]?.prices.p), 2)
    assert.equal(Number(fields?.[0]?.prices.c), 4)
    assert.equal(patchExpressionPrice(expression, 0, 'p', '2'), expression)
    assert.equal(
      Number(
        expressionPriceFields(
          patchExpressionPrice(expression, 0, 'c', '5')
        )?.[0]?.prices.c
      ),
      5
    )
  }
  assert.equal(expressionPriceFields('p*1 + p*2 + c*4')?.[0]?.prices.p, '3')
  assert.equal(
    expressionPriceFields(
      patchExpressionPrice('p*1 + p*2 + c*4', 0, 'p', '8')
    )?.[0]?.prices.p,
    '8'
  )
})

test('human tier ranges preserve strict, reversed and nested conditions', () => {
  const expression =
    'v1:len < 100 ? p*1+c*2 : (len >= 200 ? tier("long",p*3+c*6) : p*2+c*4)'
  const fields = expressionPriceFields(expression)
  assert.deepEqual(
    fields?.map(({ min, max }) => [min, max]),
    [
      [0, 99],
      [200, undefined],
      [100, 199],
    ]
  )
  const changed = patchExpressionPrice(expression, 1, 'c', '7')
  assert.equal(changed, expression.replace('c*6', 'c*7'))
  for (const expression of [
    'p*2+c*4+unknown',
    'len<=100 ? p*2 : max(p, c)',
    'v9:p*2',
    'p*2; c*4',
    'tier("x", p*2) + c*4',
    'p * -2',
    '(p*2+c*4)*2',
  ]) {
    assert.equal(expressionPriceFields(expression), null)
  }
})

test('tier creation and edits make contiguous ranges without changing prices', () => {
  const flat = 'v1:tier("base", p*2+c*4+cr*0)'
  const split = addPricingTier(flat, '100')
  assert.ok(split.startsWith('v1:'))
  assert.deepEqual(
    expressionPriceFields(split)?.map(({ min, max, prices }) => [
      min,
      max,
      prices.cr,
    ]),
    [
      [0, 100, '0'],
      [101, undefined, '0'],
    ]
  )
  const shifted = patchPricingThreshold(split, 0, '200')
  assert.deepEqual(
    expressionPriceFields(shifted)?.map(({ min, max }) => [min, max]),
    [
      [0, 200],
      [201, undefined],
    ]
  )
  const expanded = addPricingTier(split, '200')
  assert.throws(() => patchPricingThreshold(expanded, 1, '99'))
  assert.throws(() => addPricingTier(split, '100'))
  assert.throws(() => addPricingTier(flat, ''))
  assert.throws(() => addPricingTier(flat, '1.2'))
  assert.equal(expressionPriceFields(removePricingTier(split, 1))?.length, 1)
  assert.equal(expressionPriceFields(removePricingTier(split, 0))?.length, 1)
  assert.throws(() => removePricingTier(flat, 0))
})

test('fixed request and image prices parse with units, quantities and amounts', () => {
  const request = expressionPriceFields('tier("request", fixed(2))')
  assert.equal(request?.length, 1)
  const field = request?.[0]
  assert.equal(field?.unit, 'request')
  assert.equal(field?.price, '2')
  assert.equal(field?.label, 'request')
  assert.equal(field?.wrapped, true)
  assert.equal(field?.quantity, undefined)
  assert.equal(field?.min, 0)
  assert.equal(field?.max, undefined)
  const tiered = expressionPriceFields(
    'v1:len <= 272000 ? tier("short", fixed(1)) : tier("long", fixed(2))'
  )
  assert.deepEqual(
    tiered?.map((branch) => [
      branch.min,
      branch.max,
      branch.reachable,
      branch.price,
    ]),
    [
      [0, 272000, true, '1'],
      [272001, undefined, true, '2'],
    ]
  )
  for (const expression of [
    'tier("image", fixed(0.05)) * image_count',
    'image_count * tier("image", fixed(0.05))',
  ]) {
    const fields = expressionPriceFields(expression)
    assert.equal(fields?.length, 1)
    assert.equal(fields?.[0]?.unit, 'image')
    assert.equal(fields?.[0]?.quantity, 'image_count')
    assert.equal(fields?.[0]?.price, '0.05')
    assert.equal(fields?.[0]?.label, 'image')
    assert.equal(fields?.[0]?.wrapped, true)
  }
  assert.deepEqual(expressionPriceFields('p * 2 + img_cr * 0.5')?.[0]?.prices, {
    p: '2',
    img_cr: '0.5',
  })
})

test('fixed pricing rejects bare calls, linear mixes and image_count lanes', () => {
  for (const expression of [
    'fixed(2)',
    'fixed(-1)',
    'tier("a", fixed(1 + 1))',
    'p*2 + fixed(1)',
    'tier("a", fixed(2)) + p*2',
    'tier("a", fixed(2)) + tier("b", fixed(3))',
    'image_count * 3',
    'len <= 100 ? tier("a", fixed(1)) : p*2',
  ]) {
    assert.equal(expressionPriceFields(expression), null)
  }
})

test('fixed price edits replace only the amount span and keep the prefix', () => {
  const expression =
    'v1:len <= 272000 ? tier("short", fixed(1)) : tier("long", fixed(2))'
  assert.equal(
    patchFixedPrice(expression, 0, '3.5'),
    'v1:len <= 272000 ? tier("short", fixed(3.5)) : tier("long", fixed(2))'
  )
  assert.equal(patchFixedPrice(expression, 1, '2'), expression)
  assert.equal(
    patchFixedPrice('tier("image", fixed(0.05)) * image_count', 0, '0.1'),
    'tier("image", fixed(0.1)) * image_count'
  )
  assert.throws(() => patchFixedPrice(expression, 0, ''))
  assert.throws(() => patchFixedPrice(expression, 5, '3'))
  assert.throws(() => patchFixedPrice('p * 2 + c * 4', 0, '3'))
})

test('batch fixed prices cover every tier and reject token-only expressions', () => {
  const expression = 'len <= 100 ? tier("a", fixed(1)) : tier("b", fixed(2))'
  const changed = patchExpressionPrices(expression, 'fixed', ['3', '4'])
  assert.deepEqual(
    expressionPriceFields(changed)?.map((field) => field.price),
    ['3', '4']
  )
  assert.throws(() => patchExpressionPrices(expression, 'fixed', ['3', '']))
  assert.throws(() => patchExpressionPrices('p * 2', 'fixed', ['3']))
  const bulk = bulkExpressionPrice(
    [
      {
        model_name: 'm',
        expected_version: 'v',
        pricing: { 'billing_setting.billing_expr': expression },
      },
    ],
    'fixed',
    '0.5'
  )
  assert.deepEqual(
    expressionPriceFields(
      String(bulk[0]?.pricing['billing_setting.billing_expr'])
    )?.map((field) => field.price),
    ['0.5', '0.5']
  )
})

test('percentage changes scale fixed tier amounts and reject unparseable fixed rules', () => {
  const fixed = [
    {
      model_name: 'm',
      expected_version: 'v',
      pricing: {
        'billing_setting.billing_expr':
          'v1:len <= 100 ? tier("a", fixed(1)) : tier("b", fixed(0.05)) * image_count',
      },
    },
  ]
  const scaled = scaleExpressionPrices(fixed, '10')
  assert.equal(
    scaled[0]?.pricing['billing_setting.billing_expr'],
    'v1:len <= 100 ? tier("a", fixed(1.1)) : tier("b", fixed(0.055)) * image_count'
  )
  const complex = [
    {
      model_name: 'm',
      expected_version: 'v',
      pricing: {
        'billing_setting.billing_expr':
          'has(param("x"),"a") ? tier("a", fixed(1)) : tier("b", fixed(2))',
      },
    },
  ]
  assert.throws(() => scaleExpressionPrices(complex, '10'))
})

test('structural tier edits keep fixed amounts and image multipliers intact', () => {
  const split = addPricingTier('v1:tier("request", fixed(2))', '272000')
  assert.ok(split.startsWith('v1:'))
  assert.deepEqual(
    expressionPriceFields(split)?.map((field) => [
      field.min,
      field.max,
      field.unit,
      field.price,
    ]),
    [
      [0, 272000, 'request', '2'],
      [272001, undefined, 'request', '2'],
    ]
  )
  assert.deepEqual(
    expressionPriceFields(patchPricingThreshold(split, 0, '271999'))?.map(
      (field) => [field.min, field.max, field.price]
    ),
    [
      [0, 271999, '2'],
      [272000, undefined, '2'],
    ]
  )
  assert.deepEqual(
    expressionPriceFields(patchPricingThreshold(split, 0, '272001'))?.map(
      (field) => [field.min, field.max, field.price]
    ),
    [
      [0, 272001, '2'],
      [272002, undefined, '2'],
    ]
  )
  assert.deepEqual(
    expressionPriceFields(removePricingTier(split, 1))?.map((field) => [
      field.min,
      field.max,
      field.unit,
      field.price,
    ]),
    [[0, undefined, 'request', '2']]
  )
  const imageSplit = addPricingTier(
    'tier("image", fixed(0.05)) * image_count',
    '50'
  )
  assert.deepEqual(
    expressionPriceFields(imageSplit)?.map((field) => [
      field.min,
      field.max,
      field.unit,
      field.quantity,
      field.price,
    ]),
    [
      [0, 50, 'image', 'image_count', '0.05'],
      [51, undefined, 'image', 'image_count', '0.05'],
    ]
  )
  assert.deepEqual(
    expressionPriceFields(removePricingTier(imageSplit, 0))?.map((field) => [
      field.min,
      field.max,
      field.unit,
      field.quantity,
      field.price,
    ]),
    [[0, undefined, 'image', 'image_count', '0.05']]
  )
})

test('numeric edits retain a parseable rule and zero-percent changes are lossless', () => {
  for (const text of ['+2', '0x10', '0b10']) {
    const next = patchExpressionPrice('p*1+c*3', 0, 'p', text)
    assert.equal(
      Number(expressionPriceFields(next)?.[0]?.prices.p),
      Number(text)
    )
  }
  const changes = [
    {
      model_name: 'm',
      expected_version: 'v',
      pricing: { 'billing_setting.billing_expr': 'p*1.2345678901234567+c*2' },
    },
  ]
  assert.deepEqual(scaleExpressionPrices(changes, '0'), changes)
  assert.match(patchExpressionPrice('p*1+c*3', 0, 'p', '1e20'), /p\*1e\+20/)
})

test('labels, version prefixes and conditions survive price edits, including unicode', () => {
  const original =
    'v1:len <= 20 ? tier("短输入", 2 * p + (4 * c)) : tier(\'long\',p*3+c*6)'
  assert.equal(
    patchExpressionPrice(original, 0, 'c', '5'),
    original.replace('4 * c', '5 * c')
  )
  assert.equal(expressionPriceFields(original)?.[1]?.label, 'long')
  assert.equal(
    expressionPriceFields(addPricingTier('tier("", p*2)', '10'))?.[0]?.wrapped,
    true
  )
})

test('structural changes never discard global token variables or unreachable branches', () => {
  const expression = 'len <=100 ? p*2+c*4 : p*2+c*4+cr*0'
  assert.throws(() => removePricingTier(expression, 1))
  const unreachable = 'len < 0 ? tier("unused", p*1+cr*0) : tier("base", p*2)'
  assert.equal(expressionPriceFields(unreachable)?.[0]?.reachable, false)
  assert.throws(() => addPricingTier(unreachable, '20'))
  assert.equal(
    patchExpressionPrice(unreachable, 1, 'p', '3'),
    unreachable.replace('p*2', 'p*3')
  )
  assert.equal(
    expressionPriceFields(`${'('.repeat(80)}p*2${')'.repeat(80)}`),
    null
  )
  assert.equal(expressionPriceFields('p*1e309'), null)
})

test('blank is missing and explicit zero is free', () => {
  assert.equal(parsePriceDraft(''), undefined)
  assert.equal(parsePriceDraft('  '), undefined)
  assert.equal(parsePriceDraft('0'), 0)
  assert.throws(() => parsePriceDraft('-1'))
  assert.throws(() => parsePriceDraft('Infinity'))
})

test('bulk fields include missing lanes, preserve unrelated drafts and reject unsupported models', () => {
  const change = {
    model_name: 'm',
    expected_version: 'v',
    pricing: {
      'billing_setting.billing_expr':
        'len <= 100 ? tier("short", p * 1 + c * 2) : tier("long", p * 2 + c * 3)',
      CacheRatio: 4,
    },
  }
  const result = bulkExpressionPrice([change], 'cr', '0.5')
  assert.equal(
    expressionPriceFields(
      String(result[0]?.pricing['billing_setting.billing_expr'])
    )?.[1]?.prices.cr,
    '0.5'
  )
  assert.equal(result[0]?.pricing.CacheRatio, 4)
  assert.equal(
    expressionPriceFields(change.pricing['billing_setting.billing_expr'])?.[0]
      ?.prices.cr,
    undefined
  )
  assert.throws(() =>
    bulkExpressionPrice(
      [
        change,
        {
          ...change,
          model_name: 'complex',
          pricing: { 'billing_setting.billing_expr': 'max(p, c)' },
        },
      ],
      'cr',
      '2'
    )
  )
})

test('complete drafts preserve unedited values and use the model version', () => {
  const entry = {
    model_name: 'new',
    version: 'empty',
    configured: {},
    effective: { ModelRatio: 3 },
  }
  assert.deepEqual(buildPricingChange(entry, { ModelRatio: 0 }), {
    model_name: 'new',
    expected_version: 'empty',
    pricing: { ModelRatio: 0 },
  })
  const configured = { ModelRatio: 2, 'billing_setting.billing_expr': 'p * 2' }
  const change = buildPricingChange(
    { ...entry, configured },
    { ...configured, ModelRatio: 4 }
  )
  assert.equal(change.pricing['billing_setting.billing_expr'], 'p * 2')
  assert.equal(configured.ModelRatio, 2)
})

test('expression fields allow missing lanes and retain version', () => {
  const expression = 'v1:tier("base", p * 3 + c * 15)'
  const fields = expressionPriceFields(expression)
  assert.equal(fields?.[0]?.prices.cr, undefined)
  assert.equal(
    patchExpressionPrice(expression, 0, 'p', '4'),
    'v1:tier("base", p * 4 + c * 15)'
  )
  assert.equal(
    patchExpressionPrice(expression, 0, 'cr', '0'),
    'v1:tier("base", p * 3 + c * 15 + cr * 0)'
  )
  assert.throws(() => patchExpressionPrice(expression, 0, 'cr', ''))
})

test('first category additions require explicit prices for all tiers', () => {
  const expr =
    'len <= 100 ? tier("a", p * 3 + c * 15) : tier("b", p * 6 + c * 30)'
  assert.throws(() => patchExpressionPrice(expr, 0, 'cr', '0.3'))
  assert.throws(() => patchExpressionPrices(expr, 'cr', ['0.3', '']))
  const changed = patchExpressionPrices(expr, 'cr', ['0.3', '0.6'])
  assert.deepEqual(
    expressionPriceFields(changed)?.map((tier) => tier.prices.cr),
    ['0.3', '0.6']
  )
  assert.equal(patchExpressionPrice(expr, 0, 'p', '3'), expr)
})

test('structural editing rejects runtime rules, external math and p conditions', () => {
  for (const expr of [
    'tier("a", p * 2) * 2',
    'p < 100 ? tier("a", p * 2) : tier("b", p * 3)',
    'tier("a", p * 2)|||when(header("x") has "fast") * 2',
    'v2:tier("a", p * 2)',
  ]) {
    assert.equal(expressionPriceFields(expr), null)
  }
})

test('percentage changes preserve omitted lanes, explicit zero and versions', () => {
  const original = [
    {
      model_name: 'm',
      expected_version: 'v',
      pricing: {
        'billing_setting.billing_expr': 'v1:tier("a", p * 3 + c * 0)',
      },
    },
  ]
  const changed = scaleExpressionPrices(original, '10')
  const fields = expressionPriceFields(
    String(changed[0]?.pricing['billing_setting.billing_expr'])
  )
  assert.equal(Number(fields?.[0]?.prices.p), 3.3)
  assert.equal(fields?.[0]?.prices.c, '0')
  assert.equal(fields?.[0]?.prices.cr, undefined)
  assert.equal(changed[0]?.expected_version, 'v')
  assert.throws(() => scaleExpressionPrices(original, '-101'))
})

test('complex leaves stay raw and draft changes invalidate risk acknowledgement', () => {
  assert.equal(expressionPriceFields('tier("base", max(p * 2, p * 3))'), null)
  assert.notEqual(
    pricingDraftFingerprint([
      { model_name: 'm', expected_version: 'v', pricing: { ModelRatio: 1 } },
    ]),
    pricingDraftFingerprint([
      { model_name: 'm', expected_version: 'v', pricing: { ModelRatio: 0 } },
    ])
  )
})

test('boundary scenarios freeze time and include cache in both usage semantics', () => {
  const at = '2026-10-13T10:00:00Z'
  const scenarios = boundaryPricingScenarios(
    'len <= 100 ? tier("a", p * 1) : tier("b", p * 2)',
    at
  )
  const openai = scenarios.filter(
    (scenario) => scenario.usage_semantics === 'openai'
  )
  assert.deepEqual(
    openai.map((scenario) => scenario.usage.prompt_tokens),
    [1000, 0, 99, 100, 101]
  )
  assert.ok(scenarios.every((scenario) => scenario.at === at))
  assert.equal(
    scenarios.find((scenario) => scenario.id === 'claude-100')?.usage
      .prompt_tokens,
    20
  )
  assert.equal(
    boundaryPricingScenarios(
      'len < 0 ? tier("a", p * 0) : tier("b", p * 2)',
      at
    ).filter((scenario) => scenario.id === 'openai-0').length,
    1
  )
})

test('boundary samples come from parsed ranges rather than labels or numeric spelling', () => {
  const scenarios = boundaryPricingScenarios(
    'len <= (1e5) ? tier("len <= 999", p*2) : p*3',
    '2026-10-13T10:00:00Z'
  )
  assert.deepEqual(
    scenarios
      .filter((scenario) => scenario.usage_semantics === 'openai')
      .map((scenario) => scenario.usage.prompt_tokens),
    [1000, 0, 99999, 100000, 100001]
  )
  assert.throws(() =>
    boundaryPricingScenarios('len <= 1e10 ? p*2 : p*3', '2026-10-13T10:00:00Z')
  )
})

test('percentage adjustment supports runtime expressions without rewriting request rules', () => {
  const expression =
    'v1:tier("a", max(p * 2, 10))|||when(header("x") has "fast") * 2'
  const change = {
    model_name: 'm',
    expected_version: 'v',
    pricing: { 'billing_setting.billing_expr': expression },
  }
  assert.equal(
    scaleExpressionPrices([change], '10')[0]?.pricing[
      'billing_setting.billing_expr'
    ],
    'v1:(tier("a", max(p * 2, 10))) * 1.1|||when(header("x") has "fast") * 2'
  )
  assert.equal(
    scaleExpressionPrices([change], '0')[0]?.pricing[
      'billing_setting.billing_expr'
    ],
    expression
  )
})

test('legacy USD edits add missing fields without disabling old quick edits', () => {
  const entry = {
    model_name: 'm',
    version: 'v',
    configured: { ModelRatio: 1 },
    effective: { ModelRatio: 1, CompletionRatio: 4 },
    completion_locked: false,
  }
  assert.equal(
    legacyTokenPrice(entry, entry.configured, 'CompletionRatio', 2),
    8
  )
  assert.deepEqual(
    patchLegacyTokenPrice(entry, entry.configured, 'CacheRatio', '0.5', 2),
    { ModelRatio: 1, CacheRatio: 0.25 }
  )
  assert.deepEqual(
    patchLegacyTokenPrice(entry, entry.configured, 'ModelRatio', '4', 2),
    { ModelRatio: 2 }
  )
  assert.throws(() =>
    patchLegacyTokenPrice(entry, entry.configured, 'ModelRatio', '0', 2)
  )
  assert.throws(() =>
    patchLegacyTokenPrice(
      { ...entry, completion_locked: true },
      entry.configured,
      'CompletionRatio',
      '10',
      2
    )
  )
  assert.throws(() =>
    patchLegacyTokenPrice(
      { ...entry, completion_locked: undefined },
      entry.configured,
      'CompletionRatio',
      '10',
      2
    )
  )
  assert.equal(legacyTokenPrice(entry, entry.configured, 'ModelRatio', 4), 4)
  assert.deepEqual(
    patchLegacyTokenPrice(entry, entry.configured, 'ModelRatio', '8', 4),
    { ModelRatio: 2 }
  )
  assert.equal(
    legacyTokenPrice(entry, entry.configured, 'ModelRatio', 0),
    undefined
  )
  assert.throws(() =>
    patchLegacyTokenPrice(entry, entry.configured, 'ModelRatio', '8', 0)
  )
})

test('batch actions cover inherited expressions and legacy modes without populating no-op defaults', () => {
  const entries = [
    {
      model_name: 'builtin',
      version: 'v',
      configured: {},
      effective: {
        'billing_setting.billing_mode': 'tiered_expr',
        'billing_setting.billing_expr': 'tier("a", p * 2)',
      },
    },
    {
      model_name: 'legacy',
      version: 'v',
      configured: { ModelRatio: 1 },
      effective: { ModelRatio: 1 },
      completion_locked: false,
    },
    {
      model_name: 'fixed',
      version: 'v',
      configured: { ModelPrice: 0.01 },
      effective: { ModelPrice: 0.01 },
    },
  ]
  const changes = entries.map((entry) =>
    buildPricingChange(entry, entry.configured)
  )
  assert.deepEqual(scalePricingDrafts(changes, entries, '0'), changes)
  const adjusted = scalePricingDrafts(changes, entries, '10')
  assert.equal(adjusted[1]?.pricing.ModelRatio, 1.1)
  assert.equal(adjusted[2]?.pricing.ModelPrice, 0.011)
  assert.equal(
    expressionPriceFields(
      String(adjusted[0]?.pricing['billing_setting.billing_expr'])
    )?.[0]?.prices.p,
    '2.2'
  )
  const tokenOnly = applyBatchTokenPrice(
    changes.slice(0, 2),
    entries,
    'cr',
    '0.5',
    4
  )
  assert.equal(tokenOnly[1]?.pricing.CacheRatio, 0.125)
  assert.throws(() => applyBatchTokenPrice(changes, entries, 'cr', '0.5', 4))
})

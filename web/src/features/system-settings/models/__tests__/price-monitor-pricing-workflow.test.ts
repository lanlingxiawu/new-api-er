/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'

import { test } from 'vitest'

import {
  basicPricingScenario,
  pricingRiskRequired,
  pricingScenarioForm,
} from '../price-monitor-pricing-workflow'

test('basic scenarios include cache in total context and freeze time', () => {
  const form = {
    input: '1000',
    output: '100',
    cache: '800',
    semantics: 'openai' as const,
    at: '2026-10-13T10:00:00Z',
  }
  assert.deepEqual(basicPricingScenario(form).usage, {
    prompt_tokens: 1000,
    completion_tokens: 100,
    prompt_tokens_details: { cached_tokens: 800 },
  })
  assert.equal(
    basicPricingScenario({ ...form, semantics: 'claude' }).usage.prompt_tokens,
    200
  )
  assert.equal(Date.parse(basicPricingScenario(form).at), Date.parse(form.at))
  assert.throws(() => basicPricingScenario({ ...form, cache: '1001' }))
  assert.throws(() => basicPricingScenario({ ...form, input: '' }))
  assert.throws(() => basicPricingScenario({ ...form, at: 'invalid' }))
})

test('save requires confirmation for known loss and uncovered scope independently', () => {
  const base = {
    model_name: 'm',
    comparison_status: 'equal',
    coverage: { compared: 1, unknown: 0 },
    violations: [],
  }
  assert.deepEqual(
    pricingRiskRequired({ context_version: 'c', models: [base] }),
    { loss: false, unknown: false }
  )
  assert.deepEqual(
    pricingRiskRequired({
      context_version: 'c',
      models: [{ ...base, coverage: { compared: 1, unknown: 1 } }],
    }),
    { loss: false, unknown: true }
  )
  assert.deepEqual(
    pricingRiskRequired({
      context_version: 'c',
      models: [
        { ...base, violations: [{ field: 'input', value: 1, floor: 2 }] },
      ],
    }),
    { loss: true, unknown: false }
  )
})

test('multimodal scenarios keep total input and cache lifetimes explicit', () => {
  const form = {
    input: '1000',
    output: '500',
    cache: '200',
    cacheWrite: '100',
    cacheWrite1h: '50',
    imageInput: '20',
    imageOutput: '30',
    audioInput: '40',
    audioOutput: '60',
    semantics: 'claude' as const,
    at: '2026-10-13T10:00:00Z',
  }
  const scenario = basicPricingScenario(form)
  assert.deepEqual(
    basicPricingScenario(pricingScenarioForm(scenario)),
    scenario
  )
  assert.equal(scenario.usage.prompt_tokens, 650)
  assert.equal(scenario.usage.claude_cache_creation_1_h_tokens, 50)
  assert.equal(scenario.usage.claude_cache_creation_5_m_tokens, 100)
  assert.deepEqual(scenario.usage.completion_tokens_details, {
    image_tokens: 30,
    audio_tokens: 60,
  })
  assert.throws(() => basicPricingScenario({ ...form, cacheWrite: '900' }))
  assert.throws(() => basicPricingScenario({ ...form, audioOutput: '501' }))
  assert.throws(() => basicPricingScenario({ ...form, input: '1000000001' }))
  assert.throws(() => basicPricingScenario({ ...form, semantics: 'openai' }))
})

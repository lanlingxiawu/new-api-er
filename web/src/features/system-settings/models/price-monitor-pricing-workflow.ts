/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import type {
  MonitorPricingReview,
  MonitorPricingScenario,
} from './price-monitor-pricing-api'

export type BasicScenarioForm = {
  input: string
  output: string
  cache: string
  cacheWrite?: string
  cacheWrite1h?: string
  imageInput?: string
  imageOutput?: string
  audioInput?: string
  audioOutput?: string
  semantics: 'openai' | 'claude'
  at: string
}

export function basicPricingScenario(
  form: BasicScenarioForm
): MonitorPricingScenario {
  const numbers = [
    form.input,
    form.output,
    form.cache,
    form.cacheWrite ?? '0',
    form.cacheWrite1h ?? '0',
    form.imageInput ?? '0',
    form.imageOutput ?? '0',
    form.audioInput ?? '0',
    form.audioOutput ?? '0',
  ].map((value) => {
    if (!value.trim()) throw new Error('Enter a valid token count')
    const parsed = Number(value)
    if (!Number.isSafeInteger(parsed) || parsed < 0 || parsed > 1_000_000_000) {
      throw new Error('Enter a valid token count')
    }
    return parsed
  })
  const [
    input = 0,
    output = 0,
    cache = 0,
    cacheWrite = 0,
    cacheWrite1h = 0,
    imageInput = 0,
    imageOutput = 0,
    audioInput = 0,
    audioOutput = 0,
  ] = numbers
  if (cache > input) throw new Error('Cached tokens cannot exceed input tokens')
  if (cacheWrite1h > 0 && form.semantics !== 'claude') {
    throw new Error('Choose Claude usage format for one-hour cache creation')
  }
  if (
    cache + cacheWrite + cacheWrite1h + imageInput + audioInput > input ||
    imageOutput + audioOutput > output
  ) {
    throw new Error(
      'Separate token counts cannot exceed their input or output total'
    )
  }
  if (!Number.isFinite(Date.parse(form.at))) {
    throw new Error('Enter a valid simulation time')
  }
  return {
    id: 'example',
    usage_semantics: form.semantics,
    usage: {
      prompt_tokens:
        form.semantics === 'claude'
          ? input - cache - cacheWrite - cacheWrite1h
          : input,
      completion_tokens: output,
      prompt_tokens_details: {
        cached_tokens: cache,
        ...(cacheWrite + cacheWrite1h
          ? { cached_creation_tokens: cacheWrite + cacheWrite1h }
          : {}),
        ...(imageInput ? { image_tokens: imageInput } : {}),
        ...(audioInput ? { audio_tokens: audioInput } : {}),
      },
      ...(imageOutput || audioOutput
        ? {
            completion_tokens_details: {
              image_tokens: imageOutput,
              audio_tokens: audioOutput,
            },
          }
        : {}),
      ...(cacheWrite ? { claude_cache_creation_5_m_tokens: cacheWrite } : {}),
      ...(cacheWrite1h
        ? { claude_cache_creation_1_h_tokens: cacheWrite1h }
        : {}),
    },
    request_body: {},
    headers: {},
    at: new Date(form.at).toISOString(),
  }
}

export function pricingRiskRequired(review: MonitorPricingReview): {
  loss: boolean
  unknown: boolean
} {
  return {
    loss: review.models.some((model) => model.violations.length > 0),
    unknown: review.models.some(
      (model) =>
        model.coverage.unknown > 0 ||
        ['unknown', 'unsupported', 'partial'].includes(model.comparison_status)
    ),
  }
}

export function pricingScenarioForm(
  scenario: MonitorPricingScenario
): BasicScenarioForm {
  const usage = scenario.usage
  const inputDetails = usage.prompt_tokens_details as
    | Record<string, number>
    | undefined
  const outputDetails = usage.completion_tokens_details as
    | Record<string, number>
    | undefined
  const cache = Number(inputDetails?.cached_tokens ?? 0)
  const claude = scenario.usage_semantics === 'claude'
  const cacheWrite = claude
    ? Number(usage.claude_cache_creation_5_m_tokens ?? 0)
    : Math.max(
        inputDetails?.cached_creation_tokens ?? 0,
        inputDetails?.cache_write_tokens ?? 0
      )
  const cacheWrite1h = claude
    ? Number(usage.claude_cache_creation_1_h_tokens ?? 0)
    : 0
  return {
    input: String(
      Number(usage.prompt_tokens ?? 0) +
        (claude ? cache + cacheWrite + cacheWrite1h : 0)
    ),
    output: String(usage.completion_tokens ?? 0),
    cache: String(cache),
    cacheWrite: String(cacheWrite),
    cacheWrite1h: String(cacheWrite1h),
    imageInput: String(inputDetails?.image_tokens ?? 0),
    imageOutput: String(outputDetails?.image_tokens ?? 0),
    audioInput: String(inputDetails?.audio_tokens ?? 0),
    audioOutput: String(outputDetails?.audio_tokens ?? 0),
    semantics: claude ? 'claude' : 'openai',
    at: scenario.at,
  }
}

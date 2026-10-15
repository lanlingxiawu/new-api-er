/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import type { MonitorPricingScenario } from './price-monitor-pricing-api'
import type { RuleDocument, RuleNode } from './price-monitor-rule-types'

const comparisons = new Set([
  '==',
  '!=',
  '<',
  '<=',
  '>',
  '>=',
  'contains',
  'startsWith',
  'endsWith',
])
const forbiddenPathParts = new Set(['__proto__', 'prototype', 'constructor'])

function ruleLiteral(
  node: RuleNode
): string | number | boolean | null | undefined {
  if (node.kind === 'null') return null
  if (node.kind === 'string') return node.value ?? ''
  if (node.kind === 'boolean') {
    if (node.value === 'true') return true
    if (node.value === 'false') return false
  }
  if (node.kind === 'number' && node.value?.trim()) {
    const value = Number(node.value)
    if (Number.isFinite(value)) return value
  }
  if (
    node.kind === 'unary' &&
    ['+', '-'].includes(node.operator ?? '') &&
    node.args?.[0]?.kind === 'number'
  ) {
    const child = ruleLiteral(node.args[0])
    if (typeof child === 'number') return node.operator === '-' ? -child : child
  }
  return undefined
}

function requestConditionExamples(
  source: RuleNode,
  value: string | number | boolean | null,
  operator: string,
  base: MonitorPricingScenario
): MonitorPricingScenario[] | null {
  const nameNode = source.args?.[0]
  if (nameNode?.kind !== 'string' || !nameNode.value) return null
  const name = nameNode.value
  if (name.length > 256) return null
  if (typeof value === 'number' && Math.abs(value) > Number.MAX_SAFE_INTEGER) {
    return null
  }
  if (
    !['==', '!='].includes(operator) &&
    typeof value !== 'number' &&
    typeof value !== 'string'
  ) {
    return null
  }
  let candidates: (string | number | boolean | null)[]
  if (typeof value === 'number') candidates = [value, value - 1, value + 1]
  else if (typeof value === 'boolean') candidates = [value, !value]
  else if (value === null) candidates = [null, 'example']
  else candidates = [value, value === '' ? 'example' : '']
  if (
    ['contains', 'startsWith', 'endsWith'].includes(operator) &&
    (typeof value !== 'string' || value === '')
  ) {
    return null
  }
  const scenarios: MonitorPricingScenario[] = []
  for (const candidate of candidates) {
    if (typeof candidate === 'number' && !Number.isFinite(candidate)) continue
    const scenario = structuredClone(base)
    if (source.name === 'header') {
      const key = name.trim().toLowerCase()
      if (
        typeof candidate !== 'string' ||
        key === 'authorization' ||
        key === 'cookie' ||
        key.includes('api-key') ||
        !/^[a-z0-9!#$%&'*+.^_`|~-]+$/.test(key) ||
        candidate.length > 4096 ||
        /[\r\n]/.test(candidate)
      ) {
        return null
      }
      for (const existing of Object.keys(scenario.headers)) {
        if (existing.trim().toLowerCase() === key) {
          delete scenario.headers[existing]
        }
      }
      scenario.headers[key] = candidate
    } else {
      // GJSON queries, escaped dots, array indices and modifiers cannot be
      // reliably inverted into one object assignment. Keep these manual.
      if (!/^[a-zA-Z_][\w-]*(?:\.[a-zA-Z_][\w-]*)*$/.test(name)) return null
      const parts = name.split('.')
      if (parts.some((part) => forbiddenPathParts.has(part))) return null
      let object = scenario.request_body
      for (const part of parts.slice(0, -1)) {
        const existing = object[part]
        if (
          existing !== undefined &&
          (existing === null ||
            typeof existing !== 'object' ||
            Array.isArray(existing))
        ) {
          return null
        }
        if (existing === undefined) object[part] = {}
        object = object[part] as Record<string, unknown>
      }
      const last = parts.at(-1)
      if (last) object[last] = candidate
    }
    scenarios.push(scenario)
  }
  return scenarios
}

function timeConditionExamples(
  source: RuleNode,
  limit: number,
  base: MonitorPricingScenario
): MonitorPricingScenario[] | null {
  const timezone = source.args?.[0]
  const time = Date.parse(base.at)
  if (
    timezone?.kind !== 'string' ||
    !timezone.value ||
    !Number.isFinite(time)
  ) {
    return null
  }
  const field = source.name
  const ranges: Record<string, [number, number, number, number]> = {
    hour: [0, 23, 3600000, 48],
    minute: [0, 59, 60000, 120],
    weekday: [0, 6, 86400000, 8],
    day: [1, 31, 86400000, 35],
    month: [1, 12, 86400000, 370],
  }
  const range = field ? ranges[field] : undefined
  if (!range || limit < range[0] || limit > range[1]) return null
  try {
    const formatter = new Intl.DateTimeFormat('en-US', {
      timeZone: timezone.value,
      hour: 'numeric',
      minute: 'numeric',
      weekday: 'short',
      day: 'numeric',
      month: 'numeric',
      hourCycle: 'h23',
    })
    const targets = new Set(
      [Math.ceil(limit) - 1, Math.floor(limit), Math.floor(limit) + 1].filter(
        (value) => value >= range[0] && value <= range[1]
      )
    )
    const scenarios: MonitorPricingScenario[] = []
    const weekdays = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']
    // Bounded search checks the actual IANA local clock, including DST; it
    // never assumes a fixed timezone offset or that a local time exists.
    for (let step = -range[3]; step <= range[3] && targets.size; step++) {
      const at = new Date(time + step * range[2])
      const parts = formatter.formatToParts(at)
      const part = parts.find((entry) => entry.type === field)?.value
      const value =
        field === 'weekday' ? weekdays.indexOf(part ?? '') : Number(part)
      if (!targets.delete(value)) continue
      const scenario = structuredClone(base)
      scenario.at = at.toISOString()
      scenarios.push(scenario)
    }
    return targets.size ? null : scenarios
  } catch {
    return null
  }
}

function atomicConditionExamples(
  node: RuleNode,
  base: MonitorPricingScenario
): MonitorPricingScenario[] | null {
  let [source, literal] = node.args ?? []
  let operator = node.operator ?? ''
  if (!source || !literal) return null
  if (ruleLiteral(source) !== undefined) {
    if (['contains', 'startsWith', 'endsWith'].includes(operator)) return null
    ;[source, literal] = [literal, source]
    operator =
      (
        { '<': '>', '<=': '>=', '>': '<', '>=': '<=' } as Record<string, string>
      )[operator] ?? operator
  }
  const value = ruleLiteral(literal)
  if (value === undefined) return null
  if (
    source.kind === 'usage' &&
    source.name === 'len' &&
    typeof value === 'number'
  ) {
    if (
      !['openai', 'claude'].includes(base.usage_semantics) ||
      value < 0 ||
      value > 1e9
    ) {
      return null
    }
    const sizes = [
      ...new Set([
        Math.ceil(value) - 1,
        Math.floor(value),
        Math.floor(value) + 1,
      ]),
    ].filter((size) => size >= 0 && size <= 1e9)
    return sizes.map((size) => {
      const scenario = structuredClone(base)
      // Text-only input makes raw prompt_tokens equal len under both formats;
      // retaining cache counts would add them again under Claude semantics.
      scenario.usage.prompt_tokens = size
      scenario.usage.prompt_tokens_details = {
        cached_tokens: 0,
        cached_creation_tokens: 0,
        cache_write_tokens: 0,
        image_tokens: 0,
        audio_tokens: 0,
      }
      scenario.usage.claude_cache_creation_5_m_tokens = 0
      scenario.usage.claude_cache_creation_1_h_tokens = 0
      scenario.usage.total_tokens =
        size + Number(scenario.usage.completion_tokens ?? 0)
      return scenario
    })
  }
  if (source.kind !== 'call') return null
  if (source.name === 'header' || source.name === 'param') {
    return requestConditionExamples(source, value, operator, base)
  }
  if (typeof value === 'number') {
    return timeConditionExamples(source, value, base)
  }
  return null
}

// These are isolated condition candidates, not a proof that a complete rule
// matches or that all pricing outcomes have been covered. Compound conditions
// and unsupported inversions contribute to skipped, even when their leaves
// yield useful samples. The caller reserves the 32nd slot for its base sample.
export function ruleConditionExamples(
  document: RuleDocument,
  base: MonitorPricingScenario
): { scenarios: MonitorPricingScenario[]; skipped: number } {
  const scenarios: MonitorPricingScenario[] = []
  let skipped = 0
  let visited = 0
  const pending = [{ node: document.root, depth: 0 }]
  const fingerprints = new Set<string>()
  while (pending.length) {
    const current = pending.pop()
    if (!current) break
    if (++visited > 4096 || current.depth > 64) {
      skipped++
      break
    }
    const { node, depth } = current
    if (node.kind === 'binary' && comparisons.has(node.operator ?? '')) {
      if (scenarios.length >= 31) {
        skipped++
        continue
      }
      const candidates = atomicConditionExamples(node, base)
      if (!candidates?.length) skipped++
      for (const candidate of candidates ?? []) {
        const fingerprint = JSON.stringify({ ...candidate, id: '' })
        if (fingerprints.has(fingerprint)) continue
        if (scenarios.length >= 31) {
          skipped++
          break
        }
        fingerprints.add(fingerprint)
        candidate.id = `${base.id.slice(0, 80)}-rule-${scenarios.length + 1}`
        scenarios.push(candidate)
      }
      continue
    }
    if (
      node.kind === 'binary' &&
      ['and', 'or', '&&', '||'].includes(node.operator ?? '')
    ) {
      skipped++
    }
    if (node.kind === 'call' && node.name === 'has') skipped++
    for (const child of [...(node.args ?? [])].reverse()) {
      pending.push({ node: child, depth: depth + 1 })
    }
  }
  return { scenarios, skipped }
}

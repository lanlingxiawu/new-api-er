/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import {
  parsePricingExpression,
  type PricingExpressionField,
} from './price-monitor-expression-form'
import type { MonitorPricingScenario } from './price-monitor-pricing-api'

export type PricingValues = Record<string, unknown>

export type MonitorPricingEntry = {
  model_name: string
  version: string
  configured: PricingValues
  effective: PricingValues
  completion_locked?: boolean
}

export const LEGACY_TOKEN_FIELDS = [
  ['ModelRatio', 'p'],
  ['CompletionRatio', 'c'],
  ['CacheRatio', 'cr'],
  ['CreateCacheRatio', 'cc'],
  ['ImageRatio', 'img'],
  ['AudioRatio', 'ai'],
  ['AudioCompletionRatio', 'ao'],
] as const

export function legacyTokenPrice(
  entry: MonitorPricingEntry,
  draft: PricingValues,
  key: string,
  tokenPriceFactor: number
): number | undefined {
  if (!Number.isFinite(tokenPriceFactor) || tokenPriceFactor <= 0) {
    return undefined
  }
  const base =
    Number(draft.ModelRatio ?? entry.effective.ModelRatio) * tokenPriceFactor
  if (!Number.isFinite(base)) return undefined
  if (key === 'ModelRatio') return base
  const ratio = Number(draft[key] ?? entry.effective[key])
  return Number.isFinite(ratio) ? base * ratio : undefined
}

export function patchLegacyTokenPrice(
  entry: MonitorPricingEntry,
  draft: PricingValues,
  key: string,
  text: string,
  tokenPriceFactor: number
): PricingValues {
  if (!Number.isFinite(tokenPriceFactor) || tokenPriceFactor <= 0) {
    throw new Error('Failed to load model pricing')
  }
  const value = parsePriceDraft(text)
  if (value === undefined) throw new Error('Enter a valid price')
  if (key === 'ModelRatio') {
    if (value <= 0) throw new Error('The input price must be greater than 0')
    return { ...draft, ModelRatio: value / tokenPriceFactor }
  }
  const base = legacyTokenPrice(entry, draft, 'ModelRatio', tokenPriceFactor)
  if (base === undefined || base <= 0) {
    throw new Error('The input price must be greater than 0')
  }
  if (key === 'CompletionRatio' && entry.completion_locked !== false) {
    throw new Error('Completion price follows the input price for this model')
  }
  return { ...draft, [key]: Number((value / base).toPrecision(15)) }
}

export type MonitorPricingChange = {
  model_name: string
  expected_version: string
  pricing: PricingValues
  reset?: boolean
}

export const TOKEN_PRICE_VARIABLES = [
  'p',
  'c',
  'cr',
  'cc',
  'cc1h',
  'cc12h',
  'img',
  'img_cr',
  'img_o',
  'ai',
  'ao',
] as const
export type TokenPriceVariable = (typeof TOKEN_PRICE_VARIABLES)[number]

export function parsePriceDraft(value: string): number | undefined {
  if (!value.trim()) return undefined
  const parsed = Number(value)
  if (!Number.isFinite(parsed) || parsed < 0) {
    throw new Error('Enter a valid price')
  }
  return parsed
}

export function buildPricingChange(
  entry: MonitorPricingEntry,
  pricing: PricingValues
): MonitorPricingChange {
  return {
    model_name: entry.model_name,
    expected_version: entry.version,
    pricing: structuredClone(pricing),
  }
}

export function pricingDraftFingerprint(
  changes: MonitorPricingChange[]
): string {
  return JSON.stringify(
    changes.map((change) => ({
      ...change,
      pricing: Object.fromEntries(
        Object.entries(change.pricing).sort(([a], [b]) => a.localeCompare(b))
      ),
    }))
  )
}

export type ExpressionPriceField = PricingExpressionField

export function expressionPriceFields(
  expression: string
): ExpressionPriceField[] | null {
  return parsePricingExpression(expression)
}

// Large whole prices must remain floating-point literals for expr's int64 parser.
function pricingCoefficientText(value: number): string {
  return Number.isInteger(value) && !Number.isSafeInteger(value)
    ? value.toExponential()
    : String(value)
}

// Structural changes are explicit actions. Price-only edits use source offsets
// instead, retaining every condition and tier marker byte-for-byte.
function rebuildPricingTiers(
  expression: string,
  fields: ExpressionPriceField[]
): string {
  const original = expressionPriceFields(expression)
  if (!original || original.some((field) => !field.reachable)) {
    throw new Error('Open pricing rule cards to change this price')
  }
  const oldVariables = new Set(
    original.flatMap((field) => Object.keys(field.prices))
  )
  const newVariables = new Set(
    fields.flatMap((field) => Object.keys(field.prices))
  )
  if ([...oldVariables].some((key) => !newVariables.has(key))) {
    throw new Error('Removing this tier would change other token prices')
  }
  const sorted = [...fields].sort((a, b) => a.min - b.min)
  if (
    !sorted.length ||
    sorted[0]?.min !== 0 ||
    sorted.at(-1)?.max !== undefined ||
    sorted.some(
      (field, index) =>
        field.max !== undefined &&
        (field.max < field.min || sorted[index + 1]?.min !== field.max + 1)
    )
  ) {
    throw new Error('Enter increasing tier limits')
  }
  const prefix = expression.match(/^\s*v1\s*:/)?.[0] ?? ''
  const bodies = sorted.map((field) => {
    const body = expression.slice(field.start, field.end)
    // Quantity leaves span the whole tier(...) * image_count leaf already.
    if (field.quantity) return body
    return field.wrapped
      ? `tier(${JSON.stringify(field.label)}, ${body})`
      : `(${body})`
  })
  let result = bodies.at(-1) ?? ''
  for (let index = sorted.length - 2; index >= 0; index--) {
    result = `len <= ${sorted[index]?.max} ? ${bodies[index]} : (${result})`
  }
  return prefix + result
}

export function addPricingTier(expression: string, threshold: string): string {
  const value = parsePriceDraft(threshold)
  if (value === undefined || !Number.isSafeInteger(value) || value > 1e15) {
    throw new Error('Enter a whole-number input limit')
  }
  const fields = expressionPriceFields(expression)
  if (
    !fields ||
    fields.some((field) => !field.reachable) ||
    fields.length >= 128
  ) {
    throw new Error('Open pricing rule cards to change this price')
  }
  const index = fields.findIndex(
    (field) =>
      value >= field.min && (field.max === undefined || value < field.max)
  )
  const current = fields[index]
  if (!current) throw new Error('Enter a new tier limit')
  const next = [...fields]
  next.splice(
    index,
    1,
    { ...current, max: value },
    { ...current, min: value + 1 }
  )
  return rebuildPricingTiers(expression, next)
}

export function patchPricingThreshold(
  expression: string,
  index: number,
  threshold: string
): string {
  const value = parsePriceDraft(threshold)
  if (value === undefined || !Number.isSafeInteger(value) || value > 1e15) {
    throw new Error('Enter a whole-number input limit')
  }
  const fields = expressionPriceFields(expression)
  const current = fields?.[index]
  if (!fields || !current || current.max === undefined) {
    throw new Error('Enter increasing tier limits')
  }
  if (current.max === value) return expression
  const nextMin = current.max + 1
  const next = fields.find((field) => field.min === nextMin)
  if (
    !next ||
    value < current.min ||
    (next.max !== undefined && value >= next.max)
  ) {
    throw new Error('Enter increasing tier limits')
  }
  return rebuildPricingTiers(
    expression,
    fields.map((field) => {
      if (field === current) return { ...field, max: value }
      if (field === next) return { ...field, min: value + 1 }
      return field
    })
  )
}

export function removePricingTier(expression: string, index: number): string {
  const fields = expressionPriceFields(expression)
  const current = fields?.[index]
  if (!fields || !current || fields.length < 2) {
    throw new Error('Keep at least one pricing tier')
  }
  const sorted = [...fields].sort((a, b) => a.min - b.min)
  const position = sorted.indexOf(current)
  const neighbor = sorted[position === 0 ? 1 : position - 1]
  if (!neighbor) throw new Error('Keep at least one pricing tier')
  return rebuildPricingTiers(
    expression,
    fields
      .filter((field) => field !== current)
      .map((field) => {
        if (field !== neighbor) return field
        return position === 0
          ? { ...field, min: 0 }
          : { ...field, max: current.max }
      })
  )
}

export function patchExpressionPrice(
  expression: string,
  tier: number,
  variable: TokenPriceVariable,
  value: string
): string {
  if (parsePriceDraft(value) === undefined) {
    throw new Error('Enter a valid price')
  }
  const fields = expressionPriceFields(expression)
  const field = fields?.[tier]
  if (!field) throw new Error('Open pricing rule cards to change this price')
  if (
    fields &&
    fields.length > 1 &&
    !fields.some((candidate) => candidate.prices[variable] !== undefined)
  ) {
    throw new Error('Enter the new price item for every tier')
  }
  return replaceTierPrice(expression, field, variable, value)
}

function replaceTierPrice(
  expression: string,
  field: ExpressionPriceField,
  variable: TokenPriceVariable,
  value: string
): string {
  // Fixed leaves cannot gain token lanes; the relay rejects the combination.
  if (field.unit !== undefined) {
    throw new Error('Open pricing rule cards to change this price')
  }
  value = pricingCoefficientText(Number(value))
  if (
    field.prices[variable] !== undefined &&
    Number(field.prices[variable]) === Number(value)
  ) {
    return expression
  }
  const coefficients = field.coefficients[variable] ?? []
  const coefficient = coefficients[0]
  if (coefficients.length === 1 && coefficient) {
    return (
      expression.slice(0, coefficient.start) +
      value +
      expression.slice(coefficient.end)
    )
  }
  const body = expression.slice(field.start, field.end)
  const next =
    coefficients.length === 0
      ? `${body.trimEnd()} + ${variable} * ${value}`
      : TOKEN_PRICE_VARIABLES.filter((key) => field.prices[key] !== undefined)
          .map(
            (key) =>
              `${key} * ${key === variable ? value : pricingCoefficientText(Number(field.prices[key]))}`
          )
          .join(' + ')
  return expression.slice(0, field.start) + next + expression.slice(field.end)
}

export function patchFixedPrice(
  expression: string,
  tier: number,
  value: string
): string {
  if (parsePriceDraft(value) === undefined) {
    throw new Error('Enter a valid price')
  }
  const fields = expressionPriceFields(expression)
  const field = fields?.[tier]
  if (!field || field.unit === undefined) {
    throw new Error('Open pricing rule cards to change this price')
  }
  if (field.price !== undefined && Number(field.price) === Number(value)) {
    return expression
  }
  const coefficients = field.coefficients['fixed'] ?? []
  const coefficient = coefficients[0]
  if (coefficients.length !== 1 || !coefficient) {
    throw new Error('Open pricing rule cards to change this price')
  }
  return (
    expression.slice(0, coefficient.start) +
    pricingCoefficientText(Number(value)) +
    expression.slice(coefficient.end)
  )
}

export function patchExpressionPrices(
  expression: string,
  variable: TokenPriceVariable | 'fixed',
  values: string[]
): string {
  const fields = expressionPriceFields(expression)
  if (
    !fields ||
    fields.length !== values.length ||
    values.some((value) => parsePriceDraft(value) === undefined) ||
    (variable === 'fixed' && fields.some((field) => field.price === undefined))
  ) {
    throw new Error('Enter the new price item for every tier')
  }
  let next = expression
  // Replace backwards so original source offsets remain valid.
  for (let index = fields.length - 1; index >= 0; index--) {
    const field = fields[index]
    const value = values[index]
    if (field && value !== undefined) {
      next =
        variable === 'fixed'
          ? patchFixedPrice(next, index, value)
          : replaceTierPrice(next, field, variable, value)
    }
  }
  return next
}

export function bulkExpressionPrice(
  changes: MonitorPricingChange[],
  variable: TokenPriceVariable | 'fixed',
  value: string
): MonitorPricingChange[] {
  if (parsePriceDraft(value) === undefined) {
    throw new Error('Enter a valid price')
  }
  return changes.map((change) => {
    const expression = change.pricing['billing_setting.billing_expr']
    if (typeof expression !== 'string') {
      throw new Error('Open pricing rule cards to change this price')
    }
    const tiers = expressionPriceFields(expression)
    if (!tiers) {
      throw new Error('Open pricing rule cards to change this price')
    }
    const next = patchExpressionPrices(
      expression,
      variable,
      tiers.map(() => value)
    )
    return {
      ...change,
      pricing: { ...change.pricing, 'billing_setting.billing_expr': next },
    }
  })
}

export function scaleExpressionPrices(
  changes: MonitorPricingChange[],
  percent: string
): MonitorPricingChange[] {
  const percentage = Number(percent)
  if (!percent.trim() || !Number.isFinite(percentage) || percentage < -100) {
    throw new Error('Enter a valid percentage')
  }
  if (percentage === 0) return changes
  return changes.map((change) => {
    const expression = change.pricing['billing_setting.billing_expr']
    if (typeof expression !== 'string') {
      throw new Error('Open pricing rule cards to change this price')
    }
    const tiers = expressionPriceFields(expression)
    if (!tiers) {
      if (!expression.trim()) {
        throw new Error('Open pricing rule cards to change this price')
      }
      // Wrapping fixed pricing in (body) * factor produces an expression the
      // relay rejects, so unparseable fixed rules stay card-only.
      if (/\bfixed\s*\(/.test(expression)) {
        throw new Error('Open pricing rule cards to change this price')
      }
      const ruleAt = expression.indexOf('|||')
      let body = ruleAt === -1 ? expression : expression.slice(0, ruleAt)
      const rules = ruleAt === -1 ? '' : expression.slice(ruleAt)
      const version = body.match(/^\s*v\d+\s*:/)?.[0] ?? ''
      if (version) body = body.slice(version.length)
      const next =
        percentage === 0
          ? expression
          : `${version}(${body}) * ${pricingCoefficientText(Number((1 + percentage / 100).toPrecision(15)))}${rules}`
      return {
        ...change,
        pricing: { ...change.pricing, 'billing_setting.billing_expr': next },
      }
    }
    let next = expression
    for (let index = tiers.length - 1; index >= 0; index--) {
      const tier = tiers[index]
      if (!tier) continue
      for (const variable of TOKEN_PRICE_VARIABLES) {
        const value = tier.prices[variable]
        if (value !== undefined) {
          next = patchExpressionPrice(
            next,
            index,
            variable,
            String(
              Number((Number(value) * (1 + percentage / 100)).toPrecision(15))
            )
          )
        }
      }
      if (tier.price !== undefined) {
        next = patchFixedPrice(
          next,
          index,
          String(
            Number(
              (Number(tier.price) * (1 + percentage / 100)).toPrecision(15)
            )
          )
        )
      }
    }
    return {
      ...change,
      pricing: { ...change.pricing, 'billing_setting.billing_expr': next },
    }
  })
}

export function boundaryPricingScenarios(
  expression: string,
  at: string
): MonitorPricingScenario[] {
  const fields = expressionPriceFields(expression)
  if (!fields) {
    throw new Error('Open pricing rule cards to change this price')
  }
  const sizes = new Set([1000, 0])
  for (const field of fields) {
    if (!field.reachable || field.max === undefined) continue
    for (const value of [field.max - 1, field.max, field.max + 1]) {
      if (value > 1_000_000_000) throw new Error('Enter a valid token count')
      sizes.add(Math.max(0, value))
    }
  }
  const scenarios: MonitorPricingScenario[] = []
  for (const size of sizes) {
    for (const semantics of ['openai', 'claude']) {
      const cached = Math.floor(size * 0.8)
      scenarios.push({
        id: `${semantics}-${size}`,
        usage_semantics: semantics,
        usage: {
          prompt_tokens: semantics === 'claude' ? size - cached : size,
          completion_tokens: 100,
          prompt_tokens_details: { cached_tokens: cached },
        },
        request_body: {},
        headers: {},
        at,
      })
    }
  }
  if (scenarios.length > 32) {
    throw new Error('Enter between 1 and 32 pricing scenarios')
  }
  return scenarios
}

export function applyBatchTokenPrice(
  changes: MonitorPricingChange[],
  entries: MonitorPricingEntry[],
  variable: TokenPriceVariable | 'fixed',
  value: string,
  tokenPriceFactor: number
): MonitorPricingChange[] {
  return changes.map((change) => {
    const entry = entries.find(
      (candidate) => candidate.model_name === change.model_name
    )
    if (!entry) throw new Error('Failed to load model pricing')
    const mode =
      change.pricing['billing_setting.billing_mode'] ??
      entry.effective['billing_setting.billing_mode']
    if (mode === 'tiered_expr') {
      const pricing = {
        ...change.pricing,
        'billing_setting.billing_mode': mode,
        'billing_setting.billing_expr':
          change.pricing['billing_setting.billing_expr'] ??
          entry.effective['billing_setting.billing_expr'],
      }
      const adjusted = bulkExpressionPrice(
        [{ ...change, pricing }],
        variable,
        value
      )[0]
      if (!adjusted) {
        throw new Error('Open pricing rule cards to change this price')
      }
      return adjusted
    }
    if (
      change.pricing.ModelPrice !== undefined ||
      entry.effective.ModelPrice !== undefined
    ) {
      throw new Error('Open pricing rule cards to change this price')
    }
    const field = LEGACY_TOKEN_FIELDS.find(
      ([, candidate]) => candidate === variable
    )
    if (!field) {
      throw new Error('Open pricing rule cards to change this price')
    }
    return {
      ...change,
      pricing: patchLegacyTokenPrice(
        entry,
        change.pricing,
        field[0],
        value,
        tokenPriceFactor
      ),
    }
  })
}

export function scalePricingDrafts(
  changes: MonitorPricingChange[],
  entries: MonitorPricingEntry[],
  percent: string
): MonitorPricingChange[] {
  const percentage = Number(percent)
  if (!percent.trim() || !Number.isFinite(percentage) || percentage < -100) {
    throw new Error('Enter a valid percentage')
  }
  if (percentage === 0) return changes
  return changes.map((change) => {
    const entry = entries.find(
      (candidate) => candidate.model_name === change.model_name
    )
    if (!entry) throw new Error('Failed to load model pricing')
    const mode =
      change.pricing['billing_setting.billing_mode'] ??
      entry.effective['billing_setting.billing_mode']
    if (mode === 'tiered_expr') {
      const pricing = {
        ...change.pricing,
        'billing_setting.billing_mode': mode,
        'billing_setting.billing_expr':
          change.pricing['billing_setting.billing_expr'] ??
          entry.effective['billing_setting.billing_expr'],
      }
      const adjusted = scaleExpressionPrices(
        [{ ...change, pricing }],
        percent
      )[0]
      if (!adjusted) {
        throw new Error('Open pricing rule cards to change this price')
      }
      return adjusted
    }
    const key =
      change.pricing.ModelPrice !== undefined ||
      entry.effective.ModelPrice !== undefined
        ? 'ModelPrice'
        : 'ModelRatio'
    const value = Number(change.pricing[key] ?? entry.effective[key])
    const adjusted = Number((value * (1 + percentage / 100)).toPrecision(15))
    if (!Number.isFinite(adjusted) || adjusted < 0) {
      throw new Error('Enter a valid price')
    }
    return { ...change, pricing: { ...change.pricing, [key]: adjusted } }
  })
}

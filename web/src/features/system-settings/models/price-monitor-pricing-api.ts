/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { api } from '@/lib/api'

import type {
  MonitorPricingChange,
  MonitorPricingEntry,
  PricingValues,
} from './price-monitor-pricing-draft'

export type MonitorPricingConfig = {
  entries: MonitorPricingEntry[]
  empty_version: string
  capabilities?: Record<string, boolean>
  token_price_factor: number
}

export type MonitorPricingReview = {
  context_version: string
  models: {
    model_name: string
    comparison_status: string
    coverage: { compared: number; unknown: number }
    violations: {
      field: string
      value: number
      floor: number
      binding?: string
    }[]
    reason?: string
  }[]
}

export type MonitorPricingScenario = {
  id: string
  usage_semantics: string
  usage: Record<string, unknown>
  request_body: Record<string, unknown>
  headers: Record<string, string>
  at: string
}

export type MonitorScenarioResults = {
  results: {
    scenario_id: string
    sides: {
      source_key: string
      status: string
      amount_usd?: number
      matched_rules?: string
      reason?: string
    }[]
    differences?: unknown[]
  }[]
  coverage: string
  checked_at: number
}

export async function getMonitorModelPricing(
  names: string[]
): Promise<MonitorPricingConfig> {
  const params = new URLSearchParams()
  for (const name of names) params.append('model', name)
  const response = await api.get('/api/option/model_pricing', { params })
  if (!response.data.success) throw new Error(response.data.message)
  return response.data.data
}

export async function reviewMonitorPricing(
  checkedAt: number,
  changes: MonitorPricingChange[]
): Promise<MonitorPricingReview> {
  const response = await api.post('/api/price_monitor/preview_price', {
    checked_at: checkedAt,
    changes,
  })
  if (!response.data.success) throw new Error(response.data.message)
  return response.data.data
}

export async function convertMonitorPricing(
  modelName: string,
  pricing: PricingValues
): Promise<{
  effective: PricingValues
  expression?: string
  unsupported_reason?: string
}> {
  const response = await api.post('/api/option/model_pricing/convert', {
    model_name: modelName,
    pricing,
  })
  if (!response.data.success) throw new Error(response.data.message)
  return response.data.data
}

export async function savePricingOptionMaps(request: {
  options: Record<string, string>
  expected_options: Record<string, string>
}): Promise<void> {
  const response = await api.patch('/api/option/model_pricing', request)
  if (!response.data.success) throw new Error(response.data.message)
}

export async function applyMonitorPricing(request: {
  checked_at: number
  context_version: string
  changes: MonitorPricingChange[]
  force_below_floor: boolean
  acknowledge_unknown: boolean
}): Promise<{ updated_models: string[]; refreshed: boolean }> {
  const response = await api.post('/api/price_monitor/apply_price', request)
  if (!response.data.success) throw new Error(response.data.message)
  return response.data.data
}

export async function evaluateMonitorPricing(request: {
  model_name: string
  expected_version: string
  pricing: PricingValues
  source_keys: string[]
  scenarios: MonitorPricingScenario[]
}): Promise<MonitorScenarioResults> {
  const response = await api.post('/api/price_monitor/evaluate', request)
  if (!response.data.success) throw new Error(response.data.message)
  return response.data.data
}

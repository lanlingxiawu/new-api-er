/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { api } from '@/lib/api'

import type { RuleDocument } from './price-monitor-rule-types'

export type RulePreviewResponse = {
  pricing: Record<string, unknown>
  effective: Record<string, unknown>
  rule_document: RuleDocument
}

export async function readRuleDocument(
  modelName: string,
  pricing: Record<string, unknown>
): Promise<RulePreviewResponse> {
  const response = await api.post('/api/option/model_pricing/preview', {
    model_name: modelName,
    pricing,
  })
  if (!response.data.success) throw new Error(response.data.message)
  if (!response.data.data.rule_document) {
    throw new Error(
      'The pricing rules could not be loaded. Reload the model and try again.'
    )
  }
  return response.data.data
}

export async function compileRuleDocument(
  modelName: string,
  pricing: Record<string, unknown>,
  document: RuleDocument
): Promise<RulePreviewResponse> {
  const response = await api.post('/api/option/model_pricing/preview', {
    model_name: modelName,
    pricing,
    rule_document: document,
    source_hash: document.source_hash,
  })
  if (!response.data.success) throw new Error(response.data.message)
  return response.data.data
}

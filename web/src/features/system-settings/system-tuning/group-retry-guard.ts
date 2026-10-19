import { z } from 'zod'

import { api } from '@/lib/api'

export type GuardRule = {
  enabled: boolean
  window_seconds: number
  rpm_threshold: number
  latency_threshold_ms: number
  slow_count_threshold: number
  cooldown_mode: 'fixed' | 'progressive'
  cooldown_seconds: number
  cooldown_multiplier?: number
  max_cooldown_seconds?: number
  reset_after_seconds?: number
}

export type GuardDraft = {
  enabled: boolean
  window: string
  rpm: string
  latency: string
  count: string
  mode: 'fixed' | 'progressive'
  cooldown: string
  multiplier: string
  maximum: string
  reset: string
}

export function guardDraft(rule?: GuardRule): GuardDraft {
  return {
    enabled: rule?.enabled ?? false,
    window: String(rule?.window_seconds ?? 60),
    rpm: String(rule?.rpm_threshold ?? 300),
    latency: String((rule?.latency_threshold_ms ?? 10000) / 1000),
    count: String(rule?.slow_count_threshold ?? 5),
    mode: rule?.cooldown_mode ?? 'progressive',
    cooldown: String(rule?.cooldown_seconds ?? 60),
    multiplier: String(rule?.cooldown_multiplier ?? 2),
    maximum: String(rule?.max_cooldown_seconds ?? 300),
    reset: String(rule?.reset_after_seconds ?? 600),
  }
}

const guardSchema = z
  .object({
    enabled: z.boolean(),
    window_seconds: z.number().int().min(10).max(3600),
    rpm_threshold: z.number().int().min(1).max(10000000),
    latency_threshold_ms: z.number().int().min(1).max(3600000),
    slow_count_threshold: z.number().int().min(1).max(10000000),
    cooldown_mode: z.enum(['fixed', 'progressive']),
    cooldown_seconds: z.number().int().min(1).max(86400),
    cooldown_multiplier: z.number().int().min(2).max(10).optional(),
    max_cooldown_seconds: z.number().int().min(1).max(86400).optional(),
    reset_after_seconds: z.number().int().min(10).max(86400).optional(),
  })
  .refine(
    (rule) =>
      rule.cooldown_mode === 'fixed' ||
      (rule.cooldown_multiplier !== undefined &&
        rule.max_cooldown_seconds !== undefined &&
        rule.max_cooldown_seconds >= rule.cooldown_seconds &&
        rule.reset_after_seconds !== undefined)
  )

export function parseGuardDraft(draft: GuardDraft): GuardRule | null {
  const rule: GuardRule = {
    enabled: draft.enabled,
    window_seconds: Number(draft.window),
    rpm_threshold: Number(draft.rpm),
    latency_threshold_ms: Number(draft.latency) * 1000,
    slow_count_threshold: Number(draft.count),
    cooldown_mode: draft.mode,
    cooldown_seconds: Number(draft.cooldown),
  }
  if (draft.mode === 'progressive') {
    rule.cooldown_multiplier = Number(draft.multiplier)
    rule.max_cooldown_seconds = Number(draft.maximum)
    rule.reset_after_seconds = Number(draft.reset)
  }
  const result = guardSchema.safeParse(rule)
  return result.success ? result.data : null
}

export type GuardStatus = {
  group: string
  window_seconds: number
  ready: boolean
  rpm: number
  slow_count: number
  retry_disabled_until: number
  cooldown_level: number
  current_cooldown_seconds: number
}

export async function getGroupRetryGuardStatus(scope: string) {
  const response = await api.get<{
    success: boolean
    message: string
    data?: {
      available: boolean
      updated_at: number
      dropped_samples: number
      groups: GuardStatus[]
    }
  }>('/api/option/group-retry/status', {
    params: { scope },
    disableDuplicate: true,
  })
  return response.data
}

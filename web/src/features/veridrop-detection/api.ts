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
import { api } from '@/lib/api'
import { updateSystemOption } from '@/features/system-settings/api'
import type { SystemOptionsResponse } from '@/features/system-settings/types'

import type {
  VeridropDetectionOptions,
  VeridropDetectionResultsResponse,
  VeridropDetectionResultsRequest,
  VeridropManualDetectionRequest,
  VeridropManualDetectionResponse,
  VeridropDetectionStartRequest,
  VeridropDetectionTaskResponse,
  VeridropDetectionTargetsResponse,
} from './types'

export const VERIDROP_OPTION_KEYS = {
  enabled: 'veridrop_monitor_setting.enabled',
  base_url: 'veridrop_monitor_setting.base_url',
  admin_api_key: 'veridrop_monitor_setting.admin_api_key',
  default_mode: 'veridrop_monitor_setting.default_mode',
  default_openai_wire_api: 'veridrop_monitor_setting.default_openai_wire_api',
  include_long_context: 'veridrop_monitor_setting.include_long_context',
  include_long_context_extreme:
    'veridrop_monitor_setting.include_long_context_extreme',
  auto_disable_enabled: 'veridrop_monitor_setting.auto_disable_enabled',
  auto_disable_failed_threshold:
    'veridrop_monitor_setting.auto_disable_failed_threshold',
  max_concurrent: 'veridrop_monitor_setting.max_concurrent',
  batch_size: 'veridrop_monitor_setting.batch_size',
  submit_timeout_seconds: 'veridrop_monitor_setting.submit_timeout_seconds',
  poll_interval_seconds: 'veridrop_monitor_setting.poll_interval_seconds',
  job_timeout_seconds: 'veridrop_monitor_setting.job_timeout_seconds',
} as const

export const VERIDROP_DEFAULT_OPTIONS: VeridropDetectionOptions = {
  enabled: false,
  base_url: '',
  admin_api_key: '',
  default_mode: 'quick',
  default_openai_wire_api: 'chat_completions',
  include_long_context: false,
  include_long_context_extreme: false,
  auto_disable_enabled: false,
  auto_disable_failed_threshold: 40,
  max_concurrent: 2,
  batch_size: 100,
  submit_timeout_seconds: 30,
  poll_interval_seconds: 5,
  job_timeout_seconds: 300,
}

const booleanKeys = new Set<keyof VeridropDetectionOptions>([
  'enabled',
  'include_long_context',
  'include_long_context_extreme',
  'auto_disable_enabled',
])

const numberKeys = new Set<keyof VeridropDetectionOptions>([
  'auto_disable_failed_threshold',
  'max_concurrent',
  'batch_size',
  'submit_timeout_seconds',
  'poll_interval_seconds',
  'job_timeout_seconds',
])

function parseOptionValue(
  key: keyof VeridropDetectionOptions,
  value: string
): string | number | boolean {
  if (booleanKeys.has(key)) return value === 'true' || value === '1'
  if (numberKeys.has(key)) {
    const parsed = Number(value)
    return Number.isFinite(parsed) ? parsed : VERIDROP_DEFAULT_OPTIONS[key]
  }
  return value
}

export function normalizeVeridropOptions(
  options: SystemOptionsResponse['data'] | undefined
): VeridropDetectionOptions {
  const result = { ...VERIDROP_DEFAULT_OPTIONS }
  if (!Array.isArray(options)) return result

  for (const option of options) {
    const entry = Object.entries(VERIDROP_OPTION_KEYS).find(
      ([, fullKey]) => fullKey === option.key
    )
    if (!entry) continue
    const localKey = entry[0] as keyof VeridropDetectionOptions
    const writable = result as Record<
      keyof VeridropDetectionOptions,
      string | number | boolean
    >
    writable[localKey] = parseOptionValue(localKey, option.value)
  }

  return result
}

export async function getVeridropOptions() {
  const res = await api.get<SystemOptionsResponse>('/api/option/')
  return normalizeVeridropOptions(res.data.data)
}

export async function updateVeridropOptions(
  values: VeridropDetectionOptions,
  defaults: VeridropDetectionOptions
) {
  const updates = Object.entries(values).filter(([key, value]) => {
    if (key === 'admin_api_key' && String(value).trim() === '') return false
    return value !== defaults[key as keyof VeridropDetectionOptions]
  })

  for (const [key, value] of updates) {
    const fullKey = VERIDROP_OPTION_KEYS[key as keyof VeridropDetectionOptions]
    await updateSystemOption({ key: fullKey, value })
  }

  return updates.length
}

export async function startEnabledVeridropDetection(
  request: VeridropDetectionStartRequest
) {
  const res = await api.post<VeridropDetectionTaskResponse>(
    '/api/channel/veridrop/detect_enabled',
    request,
    { skipBusinessError: true, skipErrorHandler: true }
  )
  return res.data
}

export async function startManualVeridropDetection(
  request: VeridropManualDetectionRequest
) {
  const res = await api.post<VeridropManualDetectionResponse>(
    '/api/channel/veridrop/detect_manual',
    request,
    { skipBusinessError: true, skipErrorHandler: true }
  )
  return res.data
}

export async function listVeridropDetectionResults(
  request: VeridropDetectionResultsRequest = {}
) {
  const res = await api.get<VeridropDetectionResultsResponse>(
    '/api/channel/veridrop/results',
    {
      params: { limit: 100, ...request },
      disableDuplicate: true,
    }
  )
  return res.data
}

export async function listVeridropDetectionTargets(
  request: VeridropDetectionStartRequest
) {
  const res = await api.get<VeridropDetectionTargetsResponse>(
    '/api/channel/veridrop/targets',
    {
      params: request,
      disableDuplicate: true,
    }
  )
  return res.data
}

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
import type { SystemTask } from '@/features/system-settings/types'

export type VeridropDetectionStatus =
  | 'queued'
  | 'running'
  | 'done'
  | 'error'
  | 'timeout'
  | 'cancelled'
  | 'skipped'

export type VeridropDetectionResult = {
  id: number
  channel_id: number
  channel_name: string
  channel_type: number
  protocol: string
  model: string
  mode: string
  status: VeridropDetectionStatus
  veridrop_job_id: string
  score: number
  verdict: string
  summary: string
  run_error: string
  error: string
  result_json: string
  created_at: number
  started_at: number
  finished_at: number
  updated_at: number
}

export type VeridropDetectionTarget = {
  channel_id: number
  channel_name: string
  channel_type: number
  channel_type_name: string
  protocol: string
  base_url: string
  models: string[] | null
  model_count: number
  skipped_reason?: string
}

export type VeridropDetectionTargetsResponse = {
  success: boolean
  message?: string
  data?: {
    items: VeridropDetectionTarget[]
    channel_count: number
    model_count: number
    skipped_channel_count: number
  }
}

export type VeridropDetectionOptions = {
  enabled: boolean
  base_url: string
  admin_api_key: string
  default_mode: string
  default_openai_wire_api: string
  include_long_context: boolean
  include_long_context_extreme: boolean
  auto_disable_enabled: boolean
  auto_disable_failed_threshold: number
  max_concurrent: number
  batch_size: number
  submit_timeout_seconds: number
  poll_interval_seconds: number
  job_timeout_seconds: number
}

export type VeridropDetectionStartRequest = {
  mode?: string
  include_long_context?: boolean
  include_long_context_extreme?: boolean
  openai_wire_api?: string
  max_channels?: number
}

export type VeridropManualDetectionRequest = {
  base_url: string
  api_key: string
  model: string
  protocol: string
  mode?: string
  include_long_context?: boolean
  include_long_context_extreme?: boolean
  openai_wire_api?: string
}

export type VeridropDetectionTaskResponse = {
  success: boolean
  message?: string
  data?: {
    task: SystemTask
    created: boolean
  }
}

export type VeridropManualDetectionResponse = {
  success: boolean
  message?: string
  data?: VeridropDetectionResult
}

export type VeridropDetectionResultsResponse = {
  success: boolean
  message?: string
  data?: {
    items: VeridropDetectionResult[]
    next_before_id: number
  }
}

export type VeridropDetectionResultsRequest = {
  limit?: number
  before_id?: number
  channel_id?: number
  channel_name?: string
  model?: string
  mode?: string
  verdict?: string
  keyword?: string
  status?: VeridropDetectionStatus
  protocol?: string
  min_score?: number
  max_score?: number
  updated_after?: number
  error_only?: boolean
}

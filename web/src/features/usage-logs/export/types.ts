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

export type ExportFormat = 'csv_gz' | 'xlsx'

export type ExportJobStatus =
  | 'pending'
  | 'running'
  | 'ready'
  | 'failed'
  | 'canceled'

/** Column groups, used to fold the picker into sections. */
export type ExportColumnGroup =
  | 'basic'
  | 'tokens'
  | 'billing'
  | 'performance'
  | 'admin'
  | 'audit'

export interface ExportColumn {
  key: string
  /** English source string; feed straight into t(). */
  label: string
  group: ExportColumnGroup
  admin_only: boolean
}

export interface BuiltinExportTemplate {
  id: string
  name: string
  columns: string[]
  is_default: boolean
}

export interface ExportColumnCatalog {
  columns: ExportColumn[]
  builtin_templates: BuiltinExportTemplate[]
  default_template: string
  max_columns: number
  /** Server-side thresholds, so the UI never hardcodes them. */
  xlsx_max_rows: number
  rows_per_file: number
  max_range_sec: number
}

export interface ExportOptions {
  csv_bom: boolean
  timezone: string
  header: boolean
}

export interface ExportFilters {
  type: number
  start_timestamp: number
  end_timestamp: number
  model_name: string
  username: string
  token_name: string
  channel: number
  group: string
  user_id: number
}

export interface ExportPart {
  index: number
  rows: number
  bytes: number
  start_time: number
  end_time: number
}

export interface ExportJob {
  job_id: string
  user_id: number
  username: string
  status: ExportJobStatus
  progress: number
  row_count: number
  bytes_out: number
  format: ExportFormat
  format_downgraded: boolean
  columns: string[]
  filters: ExportFilters
  options: ExportOptions
  parts: ExportPart[] | null
  /** Milliseconds yielded to the resource gate; surfaced so a slow job reads as deliberate, not stuck. */
  throttled_ms: number
  /** Stable error code (disk_full / too_many_parts / timeout / internal), mapped to copy in the UI. */
  error?: string
  created_at: number
  updated_at: number
  finished_at?: number
}

export interface ExportTemplate {
  id: number
  user_id: number
  name: string
  log_category: string
  format: ExportFormat
  is_shared: boolean
  created_at: number
  updated_at: number
  columns: string[]
  options: ExportOptions
}

export interface CreateExportJobRequest {
  start_timestamp: number
  end_timestamp: number
  type?: number
  model_name?: string
  username?: string
  token_name?: string
  channel?: number
  group?: string
  columns: string[]
  format: ExportFormat
  est_rows?: number
  options: ExportOptions
}

export interface SaveExportTemplateRequest {
  name: string
  columns: string[]
  format: ExportFormat
  options: ExportOptions
  is_shared?: boolean
}

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
  | 'diagnostic'
  | 'admin'
  | 'audit'

/**
 * Whether a column/template may end up in a file handed to a customer.
 * Stricter than `admin_only`: that one is about who can read the data at all,
 * this one is about what we are willing to put in a customer's hands.
 */
export type ExportAudience = 'internal' | 'customer'

export type ExportTemplatePurpose =
  | 'reconciliation'
  | 'analytics'
  | 'diagnostic'
  | 'audit'

/** Anomaly kinds are enumerated by the server; never hardcode this list. */
export type AnomalyKind = string

export interface ExportColumn {
  key: string
  /** English source string; feed straight into t(). */
  label: string
  group: ExportColumnGroup
  admin_only: boolean
  audience: ExportAudience
}

export interface BuiltinExportTemplate {
  id: string
  name: string
  columns: string[]
  is_default: boolean
  purpose: ExportTemplatePurpose
  audience: ExportAudience
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
  /**
   * Anomaly kinds come from the server: the rules live in one place on the
   * backend, and a second copy here would silently drift away from it.
   */
  anomaly_kinds: AnomalyKind[]
  /** Max number of values allowed in a single list filter. */
  max_filter_values: number
  /**
   * Quota units per USD. The cost column is shown in USD while quota filters
   * take integer quota units, so the UI has to spell the conversion out —
   * typing a dollar figure into a quota box is off by five orders of magnitude.
   */
  quota_per_unit: number
  /** Dimensions available for the aggregate (summary) export. */
  summary_dimensions: SummaryDimension[]
}

/**
 * Aggregate export dimensions. Time granularity is day only — hourly would
 * multiply the combination count by 24 and routinely blow the group cap.
 */
export type SummaryDimension =
  | 'date'
  | 'username'
  | 'group'
  | 'model_name'
  | 'token_name'
  | 'channel'

export type ExportMode = 'detail' | 'summary' | 'both'

/**
 * Filters that cannot be pushed down to SQL and are evaluated row by row
 * during the export scan. Their presence makes the row estimate an upper
 * bound rather than a prediction.
 */
export interface AnomalyFilters {
  anomaly_only?: boolean
  anomaly_kinds?: AnomalyKind[]
  usage_source?: string[]
  stream_end_reason?: string[]
  settlement_state?: string[]
  min_retry_count?: number | null
}

/** Numeric filters, pushed down to SQL. */
export interface NumericFilters {
  /** true = 产生了费用（quota > 0）。避免让人拿美元数字去填整数额度阈值。 */
  charged?: boolean | null
  quota_min?: number | null
  quota_max?: number | null
  prompt_tokens_min?: number | null
  prompt_tokens_max?: number | null
  completion_tokens_min?: number | null
  /** Needed to express "output was 0" — a lower bound alone cannot say "equals 0". */
  completion_tokens_max?: number | null
  use_time_min?: number | null
  use_time_max?: number | null
  is_stream?: boolean | null
}

export interface ExportOptions {
  csv_bom: boolean
  timezone: string
  header: boolean
}

export interface ExportFilters
  extends AnomalyFilters,
    NumericFilters {
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
  /**
   * Omitted or 'detail' for log rows; 'summary' for the aggregate file that a
   * "detail + summary" export produces alongside them. Without it the list
   * shows two indistinguishable "Part N" entries and the aggregate — far fewer
   * rows, spanning the whole range — reads as if something went wrong.
   */
  kind?: 'detail' | 'summary'
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
  /** Rows actually written to the file. With a row filter this is far below scanned_rows. */
  row_count: number
  /**
   * Rows scanned. Without it, "ran for 8 minutes and produced 340 rows" reads
   * as a malfunction — when that is exactly what an anomaly export looks like.
   */
  scanned_rows?: number
  bytes_out: number
  format: ExportFormat
  format_downgraded: boolean
  columns: string[]
  filters: ExportFilters
  mode?: ExportMode
  summary_dims?: SummaryDimension[]
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

export interface CreateExportJobRequest
  extends AnomalyFilters,
    NumericFilters {
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
  /** Omitted or 'detail' produces one row per log; 'summary' aggregates by dimensions. */
  mode?: ExportMode
  summary_dims?: SummaryDimension[]
}

export interface SaveExportTemplateRequest {
  name: string
  columns: string[]
  format: ExportFormat
  options: ExportOptions
  is_shared?: boolean
}

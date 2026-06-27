import { api } from '@/lib/api'
import { appendUnixTimeRangeParams } from '@/lib/query-params'

export interface PlatformStat {
  total_consumption_quota: number
  total_consumption_usd: number
  request_count: number
  est_cost_quota: number
  est_cost_usd: number
  est_profit_quota: number
  est_profit_usd: number
  est_gross_margin: number
  profitable_channel_count: number
  loss_channel_count: number
}

export interface CommissionTotals {
  total_revenue_quota: number
  total_cost_quota: number
  total_profit_quota: number
  total_commission_quota: number
  record_count: number
  gross_margin: number
  total_revenue_usd: number
  total_cost_usd: number
  total_profit_usd: number
  total_commission_usd: number
}

export interface EmployeeStat {
  employee_user_id: number
  username?: string
  display_name?: string
  total_revenue: number
  total_cost: number
  total_profit: number
  total_commission: number
  record_count: number
}

export interface ChannelProfitStat {
  channel_id: number
  channel_name?: string
  consumption_quota: number
  est_cost_quota: number
  est_profit_quota: number
  est_gross_margin: number
  cost_ratio: number
}

export interface OverviewData {
  platform: PlatformStat
  commission: CommissionTotals
  by_employee: EmployeeStat[]
  by_employee_total?: number
  by_employee_page?: number
  by_employee_page_size?: number
  by_channel_platform: ChannelProfitStat[]
  by_channel_platform_total?: number
  by_channel_platform_page?: number
  by_channel_platform_page_size?: number
}

export interface OverviewResponse {
  success: boolean
  message?: string
  data?: OverviewData
}

export interface ChannelProfitPageResponse {
  success: boolean
  message?: string
  data?: {
    items: ChannelProfitStat[]
    total: number
    page: number
    page_size: number
  }
}

export async function getChannelProfitPage(params?: {
  start_time?: number
  end_time?: number
  channel_page?: number
  channel_page_size?: number
  channel_keyword?: string
  channel_sort_by?: string
  channel_sort_order?: string
}): Promise<ChannelProfitPageResponse> {
  const q = new URLSearchParams()
  appendUnixTimeRangeParams(q, params)
  if (params?.channel_page) q.set('channel_page', String(params.channel_page))
  if (params?.channel_page_size)
    q.set('channel_page_size', String(params.channel_page_size))
  if (params?.channel_keyword) q.set('channel_keyword', params.channel_keyword)
  if (params?.channel_sort_by) q.set('channel_sort_by', params.channel_sort_by)
  if (params?.channel_sort_order)
    q.set('channel_sort_order', params.channel_sort_order)
  const res = await api.get(
    `/api/admin/employee/overview/channels?${q.toString()}`
  )
  return res.data
}

export type LedgerTag = 'reversal' | 'loss' | 'profit' | 'zero_revenue'

export interface LedgerCursor {
  created_at: number
  id: number
}

export interface ConsumptionCostLedgerItem {
  id: number
  log_id?: number | null
  user_id: number
  channel_id: number
  channel_name: string
  group_name: string
  model_name: string
  revenue_quota: number
  cost_quota: number
  profit_quota: number
  gross_margin?: number | null
  group_ratio: number
  cost_ratio: number
  created_at: number
  tags: LedgerTag[]
}

export interface LedgerListParams {
  start_time?: number
  end_time?: number
  limit?: number
  cursor_created_at?: number
  cursor_id?: number
  id?: number
  log_id?: number
  user_id?: number
  channel_id?: number
  model_name?: string
  group_name?: string
  tag?: LedgerTag
}

export interface LedgerListResponse {
  success: boolean
  message?: string
  data?: {
    items: ConsumptionCostLedgerItem[]
    has_more: boolean
    next_cursor?: LedgerCursor | null
  }
}

export interface LedgerStats {
  record_count: number
  total_revenue_quota: number
  total_cost_quota: number
  total_profit_quota: number
  gross_margin?: number | null
}

export interface LedgerStatsResponse {
  success: boolean
  message?: string
  data?: {
    stats?: LedgerStats | null
    stats_status: 'ready' | 'pending' | 'unsupported'
    stats_running_count?: number
    stats_running_limit?: number
  }
}

export async function getCommissionOverview(params?: {
  start_time?: number
  end_time?: number
  channel_page?: number
  channel_page_size?: number
  channel_keyword?: string
  employee_page?: number
  employee_page_size?: number
}): Promise<OverviewResponse> {
  const q = new URLSearchParams()
  appendUnixTimeRangeParams(q, params)
  if (params?.channel_page) q.set('channel_page', String(params.channel_page))
  if (params?.channel_page_size)
    q.set('channel_page_size', String(params.channel_page_size))
  if (params?.channel_keyword) q.set('channel_keyword', params.channel_keyword)
  if (params?.employee_page)
    q.set('employee_page', String(params.employee_page))
  if (params?.employee_page_size)
    q.set('employee_page_size', String(params.employee_page_size))
  const res = await api.get(`/api/admin/employee/overview?${q.toString()}`)
  return res.data
}

function appendLedgerParams(q: URLSearchParams, params?: LedgerListParams) {
  if (!params) return
  Object.entries(params).forEach(([key, value]) => {
    if (value === undefined || value === null || value === '') return
    q.set(key, String(value))
  })
}

export async function getConsumptionCostLedger(
  params?: LedgerListParams
): Promise<LedgerListResponse> {
  const q = new URLSearchParams()
  appendLedgerParams(q, params)
  const res = await api.get(
    `/api/admin/employee/consumption-cost-ledger?${q.toString()}`,
    { skipErrorHandler: true, disableDuplicate: true }
  )
  return res.data
}

export interface LedgerExportJob {
  job_id: string
  status: 'pending' | 'running' | 'ready' | 'failed'
  progress: number
  row_count: number
  error?: string
}

export interface LedgerExportCreateResponse {
  success: boolean
  message?: string
  data?: { job_id: string }
}

export interface LedgerExportStatusResponse {
  success: boolean
  message?: string
  data?: LedgerExportJob
}

export async function createLedgerExport(
  params?: LedgerListParams
): Promise<LedgerExportCreateResponse> {
  const q = new URLSearchParams()
  appendLedgerParams(q, params)
  const res = await api.post(
    `/api/admin/employee/consumption-cost-ledger/export?${q.toString()}`,
    null,
    { skipErrorHandler: true }
  )
  return res.data
}

export async function getLedgerExportStatus(
  jobId: string
): Promise<LedgerExportStatusResponse> {
  const res = await api.get(
    `/api/admin/employee/consumption-cost-ledger/export/${jobId}`,
    { skipErrorHandler: true }
  )
  return res.data
}

export interface LedgerExportDownloadURLResponse {
  success: boolean
  message?: string
  data?: { url: string }
}

export async function getLedgerExportDownloadURL(
  jobId: string
): Promise<LedgerExportDownloadURLResponse> {
  const res = await api.get(
    `/api/admin/employee/consumption-cost-ledger/export/${jobId}/download-url`,
    { skipErrorHandler: true }
  )
  return res.data
}

export async function getConsumptionCostLedgerStats(
  params?: LedgerListParams
): Promise<LedgerStatsResponse> {
  const q = new URLSearchParams()
  appendLedgerParams(q, params)
  const res = await api.get(
    `/api/admin/employee/consumption-cost-ledger/stats?${q.toString()}`,
    { skipErrorHandler: true, disableDuplicate: true }
  )
  return res.data
}

// ─── Fallback backfill ────────────────────────────────────────────────────────

export interface FallbackHint {
  has_file: boolean
  can_backfill: boolean
  date?: string
  file_name?: string
  file_size?: number
  updated_at?: number
}

export interface FallbackBackfillResult {
  running: boolean
  success?: boolean
  date?: string
  started_at?: number
  finished_at?: number
  total_lines?: number
  success_count?: number
  discard_count?: number
  file_deleted?: boolean
  last_error?: string
}

export interface FallbackBackfillResultResponse {
  success: boolean
  message?: string
  data?: FallbackBackfillResult
}

export interface FallbackBackfillTriggerResponse {
  success: boolean
  message?: string
  data?: { date: string }
}

export async function triggerFallbackBackfill(
  date: string
): Promise<FallbackBackfillTriggerResponse> {
  const res = await api.post(
    '/api/admin/employee/consumption-cost-ledger/fallback/backfill',
    { date },
    { skipErrorHandler: true }
  )
  return res.data
}

export async function getFallbackBackfillResult(): Promise<FallbackBackfillResultResponse> {
  const res = await api.get(
    '/api/admin/employee/consumption-cost-ledger/fallback/backfill-result',
    { skipErrorHandler: true }
  )
  return res.data
}

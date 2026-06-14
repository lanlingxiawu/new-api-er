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

export async function getCommissionOverview(params?: {
  start_time?: number
  end_time?: number
  channel_page?: number
  channel_page_size?: number
  channel_keyword?: string
  channel_sort_by?: string
  channel_sort_order?: 'asc' | 'desc'
  employee_page?: number
  employee_page_size?: number
}): Promise<OverviewResponse> {
  const q = new URLSearchParams()
  appendUnixTimeRangeParams(q, params)
  if (params?.channel_page) q.set('channel_page', String(params.channel_page))
  if (params?.channel_page_size)
    q.set('channel_page_size', String(params.channel_page_size))
  if (params?.channel_keyword) q.set('channel_keyword', params.channel_keyword)
  if (params?.channel_sort_by) q.set('channel_sort_by', params.channel_sort_by)
  if (params?.channel_sort_order)
    q.set('channel_sort_order', params.channel_sort_order)
  if (params?.employee_page)
    q.set('employee_page', String(params.employee_page))
  if (params?.employee_page_size)
    q.set('employee_page_size', String(params.employee_page_size))
  const res = await api.get(`/api/admin/employee/overview?${q.toString()}`)
  return res.data
}

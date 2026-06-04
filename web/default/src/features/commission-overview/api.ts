import { api } from '@/lib/api'

export interface PlatformStat {
  total_consumption_quota: number
  total_consumption_usd: number
  request_count: number
  token_count: number
  est_cost_quota: number
  est_cost_usd: number
  est_profit_quota: number
  est_profit_usd: number
  est_gross_margin: number
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
  by_channel_platform: ChannelProfitStat[]
}

export interface OverviewResponse {
  success: boolean
  message?: string
  data?: OverviewData
}

export async function getCommissionOverview(params?: {
  start_time?: number
  end_time?: number
}): Promise<OverviewResponse> {
  const q = new URLSearchParams()
  if (params?.start_time) q.set('start_time', String(params.start_time))
  if (params?.end_time) q.set('end_time', String(params.end_time))
  const res = await api.get(`/api/admin/employee/overview?${q.toString()}`)
  return res.data
}

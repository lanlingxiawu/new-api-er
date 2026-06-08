import { api } from '@/lib/api'
import { appendUnixTimeRangeParams } from '@/lib/query-params'
import type { CommissionLog, EmployeeProfile } from '@/features/employees/types'

export interface EmployeeExtension {
  commission_total_quota: number
  total_commission_quota?: number
  commission_pending_quota: number
  commission_settled_quota: number
  profit_total_quota: number // Performance equals profit after customer cost.
  total_profit_quota?: number
  customer_total_consumption_quota?: number
  revenue_customer_count: number
  commission_total_usd: number
  total_commission_usd?: number
  customer_total_consumption_usd?: number
  commission_pending_usd: number
  profit_total_usd: number // Performance amount in USD.
  total_profit_usd?: number
}

export interface MyProfileResponse {
  success: boolean
  message?: string
  data?: {
    profile: EmployeeProfile
    extension: EmployeeExtension
    tier: {
      tier_id: number
      tier_level: number
      tier_group: string
      tier_rate: number
      tier_threshold_usd: number
    }
  }
}

export interface MyLogsResponse {
  success: boolean
  message?: string
  data?: {
    items: CommissionLog[]
    total: number
    page: number
    page_size: number
  }
}

export interface MySummaryResponse {
  success: boolean
  message?: string
  data?: EmployeeExtension
}

export async function getMyEmployeeProfile(): Promise<MyProfileResponse> {
  const res = await api.get('/api/user/employee/profile')
  return res.data
}

export async function getMyCommissionLogs(params: {
  page?: number
  page_size?: number
  customer_user_id?: number
  model_name?: string
  channel_id?: number
  start_time?: number
  end_time?: number
}): Promise<MyLogsResponse> {
  const q = new URLSearchParams()
  if (params.page) q.set('page', String(params.page))
  if (params.page_size) q.set('page_size', String(params.page_size))
  if (params.customer_user_id)
    q.set('customer_user_id', String(params.customer_user_id))
  if (params.model_name) q.set('model_name', params.model_name)
  if (params.channel_id) q.set('channel_id', String(params.channel_id))
  appendUnixTimeRangeParams(q, params)
  const res = await api.get(`/api/user/employee/commission?${q.toString()}`)
  return res.data
}

export async function getMyCommissionSummary(): Promise<MySummaryResponse> {
  const res = await api.get('/api/user/employee/commission/summary')
  return res.data
}

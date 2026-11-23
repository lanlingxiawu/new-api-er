import { commissionLogQueryParams } from '@/features/employees/lib/commission-log-query'
import type {
  ApiResponse,
  CommissionLogPage,
  CommissionLogPageParams,
  CommissionCalendarStats,
  CommissionResetPeriodStatItem,
  EmployeeProfile,
} from '@/features/employees/types'
import { api } from '@/lib/api'
import { appendUnixTimeRangeParams } from '@/lib/query-params'

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
  baseline_reset_at?: number
  baseline_profit_quota?: number
  baseline_profit_usd?: number
  baseline_commission_quota?: number
  baseline_commission_usd?: number
  current_performance_quota?: number
  current_performance_usd?: number
  current_commission_quota?: number
  current_commission_usd?: number
}

export interface NextTierInfo {
  tier_id: number
  tier_level: number
  tier_rate: number
  tier_threshold_usd: number
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
      next_tier?: NextTierInfo
    }
    period?: {
      baseline_reset_at: number
      baseline_profit_quota: number
      baseline_profit_usd: number
      baseline_commission_quota: number
      baseline_commission_usd: number
      current_performance_quota: number
      current_performance_usd: number
      current_commission_quota: number
      current_commission_usd: number
    }
  }
}

export type MyLogsResponse = ApiResponse<CommissionLogPage>

export interface MySummaryResponse {
  success: boolean
  message?: string
  data?: EmployeeExtension
}

export async function getMyEmployeeProfile(): Promise<MyProfileResponse> {
  const res = await api.get('/api/user/employee/profile')
  return res.data
}

export async function getMyCommissionLogs(
  params: Omit<CommissionLogPageParams, 'employee_user_id'>,
  signal?: AbortSignal
): Promise<MyLogsResponse> {
  const res = await api.get(
    `/api/user/employee/commission?${commissionLogQueryParams(params)}`,
    { signal }
  )
  return res.data
}

export async function getMyCommissionSummary(): Promise<MySummaryResponse> {
  const res = await api.get('/api/user/employee/commission/summary')
  return res.data
}

export async function getMyCommissionCalendarStats(params: {
  start_time: number
  end_time: number
}): Promise<{
  success: boolean
  message?: string
  data?: CommissionCalendarStats
}> {
  const q = new URLSearchParams()
  q.set('start_time', String(params.start_time))
  q.set('end_time', String(params.end_time))
  const res = await api.get(
    `/api/user/employee/commission/calendar?${q.toString()}`
  )
  return res.data
}

export async function getMyCommissionResetPeriodStats(params: {
  page?: number
  page_size?: number
  start_time?: number
  end_time?: number
}): Promise<{
  success: boolean
  message?: string
  data?: {
    items: CommissionResetPeriodStatItem[]
    total: number
    page: number
    page_size: number
  }
}> {
  const q = new URLSearchParams()
  q.set('page', String(params.page ?? 1))
  q.set('page_size', String(params.page_size ?? 100))
  appendUnixTimeRangeParams(q, params)
  const res = await api.get(`/api/user/employee/commission/monthly?${q}`)
  return res.data
}

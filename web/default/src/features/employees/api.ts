import { api } from '@/lib/api'
import { appendUnixTimeRangeParams } from '@/lib/query-params'
import type { UsageLog } from '@/features/usage-logs/data/schema'
import type {
  EmployeeProfile,
  EmployeeCustomer,
  CommissionLog,
  ChannelCostConfig,
  CommissionSummaryItem,
  CommissionCalendarStats,
  CommissionResetPeriodStatItem,
  PagedResponse,
  ApiResponse,
  EmployeeTier,
  TierResetConfig,
  TierResetResult,
} from './types'

// Employee CRUD

export async function getEmployees(
  page = 1,
  pageSize = 20,
  params?: {
    user_id?: number
    keyword?: string
    status?: number
    sort_by?: string
    sort_order?: 'asc' | 'desc'
  }
): Promise<PagedResponse<EmployeeProfile>> {
  const q = new URLSearchParams()
  q.set('page', String(page))
  q.set('page_size', String(pageSize))
  if (params?.user_id) q.set('user_id', String(params.user_id))
  if (params?.keyword) q.set('keyword', params.keyword)
  if (params?.status) q.set('status', String(params.status))
  if (params?.sort_by) q.set('sort_by', params.sort_by)
  if (params?.sort_order) q.set('sort_order', params.sort_order)
  const res = await api.get(`/api/admin/employee?${q.toString()}`)
  return res.data
}

export async function createEmployee(data: {
  user_id: number
  tier_id?: number
  remark?: string
}): Promise<ApiResponse<EmployeeProfile>> {
  const res = await api.post('/api/admin/employee', data)
  return res.data
}

export async function updateEmployee(
  id: number,
  data: {
    tier_id?: number
    status: number
    remark?: string
  }
): Promise<ApiResponse> {
  const res = await api.put(`/api/admin/employee/${id}`, data)
  return res.data
}

export async function setEmployeeTier(
  id: number,
  data: {
    tier_id: number
    source?: 'manual' | 'custom'
    remark?: string
  }
): Promise<ApiResponse> {
  const res = await api.post(`/api/admin/employee/${id}/tier`, data)
  return res.data
}

export async function deleteEmployee(id: number): Promise<ApiResponse> {
  const res = await api.delete(`/api/admin/employee/${id}`)
  return res.data
}

export async function getEmployeeCustomers(
  employeeId: number,
  params?: {
    page?: number
    page_size?: number
    keyword?: string
  }
): Promise<PagedResponse<EmployeeCustomer>> {
  const q = new URLSearchParams()
  if (params?.page) q.set('page', String(params.page))
  if (params?.page_size) q.set('page_size', String(params.page_size))
  if (params?.keyword) q.set('keyword', params.keyword)
  const res = await api.get(
    `/api/admin/employee/${employeeId}/customers?${q.toString()}`
  )
  return res.data
}

// Commission Logs

export async function getCommissionLogs(params: {
  page?: number
  page_size?: number
  employee_user_id?: number
  customer_user_id?: number
  model_name?: string
  channel_id?: number
  loss_status?: 'loss' | 'normal'
  start_time?: number
  end_time?: number
}): Promise<PagedResponse<CommissionLog>> {
  const q = new URLSearchParams()
  if (params.page) q.set('page', String(params.page))
  if (params.page_size) q.set('page_size', String(params.page_size))
  if (params.employee_user_id)
    q.set('employee_user_id', String(params.employee_user_id))
  if (params.customer_user_id)
    q.set('customer_user_id', String(params.customer_user_id))
  if (params.model_name) q.set('model_name', params.model_name)
  if (params.channel_id) q.set('channel_id', String(params.channel_id))
  if (params.loss_status) q.set('loss_status', params.loss_status)
  appendUnixTimeRangeParams(q, params)
  const res = await api.get(`/api/admin/employee/commission?${q.toString()}`)
  return res.data
}

export async function getUsageLogById(
  logId: number
): Promise<ApiResponse<UsageLog | null>> {
  const q = new URLSearchParams()
  q.set('page', '1')
  q.set('page_size', '1')
  q.set('log_id', String(logId))
  const res = await api.get(`/api/log?${q.toString()}`)
  const pageData = res.data?.data
  return {
    success: Boolean(res.data?.success),
    message: res.data?.message,
    data: pageData?.items?.[0] ?? null,
  }
}

export interface CommissionChannelOption {
  channel_id: number
  channel_name: string
  deleted: boolean
}

// 渠道筛选下拉选项（含已删除渠道的历史名称快照）
export async function getCommissionChannelOptions(params?: {
  page?: number
  page_size?: number
}): Promise<PagedResponse<CommissionChannelOption>> {
  const q = new URLSearchParams()
  if (params?.page) q.set('page', String(params.page))
  if (params?.page_size) q.set('page_size', String(params.page_size))
  const res = await api.get(
    `/api/admin/employee/commission/channels?${q.toString()}`
  )
  return res.data
}

export async function getCommissionCalendarStats(params: {
  employee_user_id?: number
  start_time: number
  end_time: number
}): Promise<ApiResponse<CommissionCalendarStats>> {
  const q = new URLSearchParams()
  q.set('start_time', String(params.start_time))
  q.set('end_time', String(params.end_time))
  if (params.employee_user_id)
    q.set('employee_user_id', String(params.employee_user_id))
  const res = await api.get(
    `/api/admin/employee/commission/calendar?${q.toString()}`
  )
  return res.data
}

export async function getCommissionResetPeriodStats(params: {
  page?: number
  page_size?: number
  employee_user_id?: number
  start_time?: number
  end_time?: number
}): Promise<PagedResponse<CommissionResetPeriodStatItem>> {
  const q = new URLSearchParams()
  q.set('page', String(params.page ?? 1))
  q.set('page_size', String(params.page_size ?? 100))
  if (params.employee_user_id)
    q.set('employee_user_id', String(params.employee_user_id))
  appendUnixTimeRangeParams(q, params)
  const res = await api.get(`/api/admin/employee/commission/monthly?${q}`)
  return res.data
}

// Employee Tiers

export async function getEmployeeTiers(): Promise<ApiResponse<EmployeeTier[]>> {
  const res = await api.get('/api/admin/employee/tiers')
  return res.data
}

export async function getEmployeeTiersPage(params: {
  page?: number
  page_size?: number
}): Promise<PagedResponse<EmployeeTier>> {
  const q = new URLSearchParams()
  if (params.page) q.set('page', String(params.page))
  if (params.page_size) q.set('page_size', String(params.page_size))
  const res = await api.get(`/api/admin/employee/tiers?${q.toString()}`)
  return res.data
}

export async function createEmployeeTier(data: {
  level: number
  group?: string
  threshold_usd: number
  rate: number
}): Promise<ApiResponse<EmployeeTier>> {
  const res = await api.post('/api/admin/employee/tiers', data)
  return res.data
}

export async function updateEmployeeTier(
  id: number,
  data: {
    level: number
    group?: string
    threshold_usd: number
    rate: number
  }
): Promise<ApiResponse<EmployeeTier>> {
  const res = await api.put(`/api/admin/employee/tiers/${id}`, data)
  return res.data
}

export async function deleteEmployeeTier(id: number): Promise<ApiResponse> {
  const res = await api.delete(`/api/admin/employee/tiers/${id}`)
  return res.data
}

// Tier / performance period monthly auto-reset

export async function getTierResetConfig(): Promise<
  ApiResponse<TierResetConfig>
> {
  const res = await api.get('/api/admin/employee/tiers/reset-config')
  return res.data
}

export async function triggerTierReset(): Promise<
  ApiResponse<TierResetResult>
> {
  const res = await api.post('/api/admin/employee/tiers/reset-now')
  return res.data
}

export async function getCommissionSummary(params?: {
  start_time?: number
  end_time?: number
}): Promise<ApiResponse<CommissionSummaryItem[]>> {
  const q = new URLSearchParams()
  appendUnixTimeRangeParams(q, params)
  const res = await api.get(
    `/api/admin/employee/commission/summary?${q.toString()}`
  )
  return res.data
}

// Channel Cost Config

export async function getChannelCosts(): Promise<
  ApiResponse<ChannelCostConfig[]>
> {
  const res = await api.get('/api/admin/channel/cost')
  return res.data
}

export async function upsertChannelCost(data: {
  channel_id: number
  cost_ratio: number
  remark?: string
}): Promise<ApiResponse> {
  const res = await api.post('/api/admin/channel/cost', data)
  return res.data
}

export async function deleteChannelCost(
  channelId: number
): Promise<ApiResponse> {
  const res = await api.delete(`/api/admin/channel/cost/${channelId}`)
  return res.data
}

export async function assignCustomerToEmployee(
  employeeId: number,
  userId: number
): Promise<ApiResponse> {
  const res = await api.post(
    `/api/admin/employee/${employeeId}/assign-customer`,
    { user_id: userId }
  )
  return res.data
}

export async function unassignCustomerFromEmployee(
  employeeId: number,
  userId: number
): Promise<ApiResponse> {
  const res = await api.delete(
    `/api/admin/employee/${employeeId}/customer/${userId}`
  )
  return res.data
}

import { api } from '@/lib/api'
import type {
  EmployeeProfile,
  CommissionLog,
  ChannelCostConfig,
  CommissionSummaryItem,
  PagedResponse,
  ApiResponse,
  EmployeeTier,
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
  commission_rate: number
  target_amount?: number
  remark?: string
}): Promise<ApiResponse<EmployeeProfile>> {
  const res = await api.post('/api/admin/employee', data)
  return res.data
}

export async function updateEmployee(
  id: number,
  data: {
    commission_rate: number
    target_amount: number
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

// Commission Logs

export async function getCommissionLogs(params: {
  page?: number
  page_size?: number
  employee_user_id?: number
  customer_user_id?: number
  model_name?: string
  channel_id?: number
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
  if (params.start_time) q.set('start_time', String(params.start_time))
  if (params.end_time) q.set('end_time', String(params.end_time))
  const res = await api.get(`/api/admin/employee/commission?${q.toString()}`)
  return res.data
}

// Employee Tiers

export async function getEmployeeTiers(): Promise<ApiResponse<EmployeeTier[]>> {
  const res = await api.get('/api/admin/employee/tiers')
  return res.data
}

export async function createEmployeeTier(data: {
  level: number
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

export async function getCommissionSummary(params?: {
  start_time?: number
  end_time?: number
}): Promise<ApiResponse<CommissionSummaryItem[]>> {
  const q = new URLSearchParams()
  if (params?.start_time) q.set('start_time', String(params.start_time))
  if (params?.end_time) q.set('end_time', String(params.end_time))
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

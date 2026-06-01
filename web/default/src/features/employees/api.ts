import { api } from '@/lib/api'
import type {
  EmployeeProfile,
  CommissionLog,
  ChannelCostConfig,
  CommissionSummaryItem,
  PagedResponse,
  ApiResponse,
} from './types'

// ── Employee CRUD ────────────────────────────────────────────────────────────

export async function getEmployees(
  page = 1,
  pageSize = 20
): Promise<PagedResponse<EmployeeProfile>> {
  const res = await api.get(
    `/api/admin/employee?page=${page}&page_size=${pageSize}`
  )
  return res.data
}

export async function createEmployee(data: {
  user_id: number
  commission_rate: number
  target_quota?: number
  remark?: string
}): Promise<ApiResponse<EmployeeProfile>> {
  const res = await api.post('/api/admin/employee', data)
  return res.data
}

export async function updateEmployee(
  id: number,
  data: {
    commission_rate: number
    target_quota: number
    status: number
    remark?: string
  }
): Promise<ApiResponse> {
  const res = await api.put(`/api/admin/employee/${id}`, data)
  return res.data
}

export async function deleteEmployee(id: number): Promise<ApiResponse> {
  const res = await api.delete(`/api/admin/employee/${id}`)
  return res.data
}

// ── Commission Logs ──────────────────────────────────────────────────────────

export async function getCommissionLogs(params: {
  page?: number
  page_size?: number
  employee_user_id?: number
  customer_user_id?: number
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
  if (params.start_time) q.set('start_time', String(params.start_time))
  if (params.end_time) q.set('end_time', String(params.end_time))
  const res = await api.get(`/api/admin/employee/commission?${q.toString()}`)
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

// ── Channel Cost Config ──────────────────────────────────────────────────────

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

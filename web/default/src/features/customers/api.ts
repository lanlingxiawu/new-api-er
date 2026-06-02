import { api } from '@/lib/api'
import type {
  ApiResponse,
  CustomerProfile,
  CustomerQuotaLog,
  PagedResponse,
} from './types'

const buildQuery = (params: Record<string, number | undefined>) => {
  const q = new URLSearchParams()
  Object.entries(params).forEach(([key, value]) => {
    if (value) q.set(key, String(value))
  })
  return q.toString()
}

export async function getAdminCustomers(params: {
  page?: number
  page_size?: number
  employee_user_id?: number
}): Promise<PagedResponse<CustomerProfile>> {
  const res = await api.get(`/api/admin/customer?${buildQuery(params)}`)
  return res.data
}

export async function createAdminCustomer(data: {
  employee_user_id: number
  customer_user_id: number
  remark?: string
}): Promise<ApiResponse<CustomerProfile>> {
  const res = await api.post('/api/admin/customer', data)
  return res.data
}

export async function updateAdminCustomer(
  id: number,
  data: { status: number; remark?: string }
): Promise<ApiResponse> {
  const res = await api.put(`/api/admin/customer/${id}`, data)
  return res.data
}

export async function updateAdminCustomerUser(
  id: number,
  data: { display_name?: string; email?: string; remark?: string }
): Promise<ApiResponse> {
  const res = await api.put(`/api/admin/customer/${id}/user`, data)
  return res.data
}

export async function deleteAdminCustomer(id: number): Promise<ApiResponse> {
  const res = await api.delete(`/api/admin/customer/${id}`)
  return res.data
}

export async function getAdminCustomerQuotaLogs(params: {
  page?: number
  page_size?: number
  employee_user_id?: number
  customer_user_id?: number
  start_time?: number
  end_time?: number
}): Promise<PagedResponse<CustomerQuotaLog>> {
  const res = await api.get(
    `/api/admin/customer/quota-logs?${buildQuery(params)}`
  )
  return res.data
}

export async function createMyCustomer(data: {
  username: string
  password: string
  display_name?: string
  email?: string
  remark?: string
}): Promise<ApiResponse<CustomerProfile>> {
  const res = await api.post('/api/user/employee/customers', data)
  return res.data
}

export async function getMyCustomers(params: {
  page?: number
  page_size?: number
}): Promise<PagedResponse<CustomerProfile>> {
  const res = await api.get(
    `/api/user/employee/customers?${buildQuery(params)}`
  )
  return res.data
}

export async function updateMyCustomer(
  id: number,
  data: { status: number; remark?: string }
): Promise<ApiResponse> {
  const res = await api.put(`/api/user/employee/customers/${id}`, data)
  return res.data
}

export async function updateMyCustomerUser(
  id: number,
  data: { display_name?: string; email?: string; remark?: string }
): Promise<ApiResponse> {
  const res = await api.put(`/api/user/employee/customers/${id}/user`, data)
  return res.data
}

export async function transferQuotaToCustomer(
  id: number,
  data: { quota: number; mode?: string; remark?: string }
): Promise<ApiResponse<CustomerQuotaLog>> {
  const res = await api.post(`/api/user/employee/customers/${id}/quota`, data)
  return res.data
}

export async function getMyCustomerQuotaLogs(params: {
  page?: number
  page_size?: number
  customer_user_id?: number
  start_time?: number
  end_time?: number
}): Promise<PagedResponse<CustomerQuotaLog>> {
  const res = await api.get(
    `/api/user/employee/customers/quota-logs?${buildQuery(params)}`
  )
  return res.data
}

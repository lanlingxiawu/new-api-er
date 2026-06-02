export interface ApiResponse<T = unknown> {
  success: boolean
  message?: string
  data?: T
}

export interface PagedResponse<T> {
  success: boolean
  message?: string
  data?: {
    items: T[]
    total: number
    page: number
    page_size: number
  }
}

export interface CustomerProfile {
  id: number
  employee_user_id: number
  customer_user_id: number
  status: number
  remark?: string
  created_at?: number
  updated_at?: number
  username?: string
  display_name?: string
  email?: string
  quota: number
  used_quota: number
  employee_username?: string
  employee_display_name?: string
}

export interface CustomerQuotaLog {
  id: number
  employee_user_id: number
  customer_user_id: number
  quota_delta: number
  customer_before_quota: number
  customer_after_quota: number
  employee_before_quota: number
  employee_after_quota: number
  remark?: string
  created_at: number
  employee_username?: string
  employee_display_name?: string
  customer_username?: string
  customer_display_name?: string
}

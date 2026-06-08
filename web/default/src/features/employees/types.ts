import { z } from 'zod'

export const employeeProfileSchema = z.object({
  id: z.number(),
  user_id: z.number(),
  username: z.string().optional(),
  display_name: z.string().optional(),
  email: z.string().optional(),
  customer_count: z.number().optional(),
  commission_rate: z.number(),
  target_amount: z.number(),
  total_consumption_quota: z.number().optional(),
  total_cost_quota: z.number().optional(),
  total_profit_quota: z.number().optional(),
  total_commission_quota: z.number().optional(),
  current_tier_id: z.number().optional(),
  current_tier_level: z.number().optional(),
  current_tier_group: z.string().optional(),
  current_tier_rate: z.number().optional(),
  current_tier_threshold_usd: z.number().optional(),
  next_tier_id: z.number().optional(),
  next_tier_level: z.number().optional(),
  next_tier_group: z.string().optional(),
  next_tier_rate: z.number().optional(),
  next_tier_threshold_usd: z.number().optional(),
  current_performance_quota: z.number().optional(),
  commission_rules: z.string().optional(),
  status: z.number(),
  remark: z.string().optional(),
  created_at: z.number().optional(),
  updated_at: z.number().optional(),
})
export type EmployeeProfile = z.infer<typeof employeeProfileSchema>

export interface EmployeeCustomer {
  id: number
  employee_user_id: number
  customer_user_id: number
  username: string
  display_name?: string
  email?: string
  quota?: number
  used_quota?: number
  commission_quota?: number
  status?: number
  remark?: string
  created_at?: number
}

export interface EmployeeTier {
  id: number
  level: number
  group: string
  threshold_usd: number
  rate: number
  created_at?: number
  updated_at?: number
}

export const commissionLogSchema = z.object({
  id: z.number(),
  employee_id: z.number(),
  employee_user_id: z.number(),
  customer_user_id: z.number(),
  log_id: z.number().nullable(),
  model_name: z.string(),
  channel_id: z.number(),
  revenue_quota: z.number(),
  cost_quota: z.number(),
  profit_quota: z.number(),
  commission_quota: z.number(),
  commission_rate: z.number(),
  cost_ratio: z.number(),
  group_ratio: z.number(),
  settle_status: z.number(),
  settled_at: z.number(),
  created_at: z.number(),
  // employee self-view only
  customer_user_id_masked: z.string().optional(),
})
export type CommissionLog = z.infer<typeof commissionLogSchema>

export const channelCostConfigSchema = z.object({
  id: z.number(),
  channel_id: z.number(),
  cost_ratio: z.number(),
  remark: z.string().optional(),
  created_at: z.number().optional(),
  updated_at: z.number().optional(),
})
export type ChannelCostConfig = z.infer<typeof channelCostConfigSchema>

export interface CommissionSummaryItem {
  employee_user_id: number
  total_revenue_quota: number
  total_cost_quota: number
  total_profit_quota: number
  total_commission_quota: number
  record_count: number
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

export interface ApiResponse<T = unknown> {
  success: boolean
  message?: string
  data?: T
}

export type EmployeeDialogType = 'create' | 'update' | 'delete'
export type ChannelCostDialogType = 'upsert' | 'delete'

import { z } from 'zod'

export const employeeProfileSchema = z.object({
  id: z.number(),
  user_id: z.number(),
  username: z.string().optional(),
  display_name: z.string().optional(),
  email: z.string().optional(),
  customer_count: z.number().optional(),
  target_amount: z.number(),
  total_consumption_quota: z.number().optional(),
  total_cost_quota: z.number().optional(),
  total_profit_quota: z.number().optional(),
  total_commission_quota: z.number().optional(),
  period_consumption_quota: z.number().optional(),
  period_cost_quota: z.number().optional(),
  period_profit_quota: z.number().optional(),
  period_commission_quota: z.number().optional(),
  period_start_at: z.number().optional(),
  period_end_at: z.number().optional(),
  period_key: z.string().optional(),
  period_timezone: z.string().optional(),
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
  current_performance_usd: z.number().optional(),
  current_commission_quota: z.number().optional(),
  current_commission_usd: z.number().optional(),
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
  customer_employee_status?: number
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
  // admin view only: resolved channel name (supports deleted channels)
  channel_name: z.string().optional(),
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

export interface CommissionCalendarDayStat {
  stat_date: number
  date: string
  revenue_quota: number
  cost_quota: number
  profit_quota: number
  commission_quota: number
  record_count: number
  revenue_usd?: number
  cost_usd?: number
  profit_usd?: number
  commission_usd?: number
}

export interface CommissionCalendarStats {
  days: CommissionCalendarDayStat[]
  period_start_at?: number
  period_end_at?: number
  period_boundary_at?: number
  period_key?: string
  timezone?: string
  summary: {
    revenue_quota: number
    cost_quota: number
    profit_quota: number
    commission_quota: number
    record_count: number
    revenue_usd?: number
    cost_usd?: number
    profit_usd?: number
    commission_usd?: number
  }
}

export interface CommissionResetPeriodStatItem {
  id: number
  reset_started_at: number
  reset_ended_at: number
  period_key: string
  timezone: string
  employee_user_id: number
  revenue_quota: number
  cost_quota: number
  profit_quota: number
  commission_quota: number
  record_count: number
  last_created_at: number
  total_revenue_usd?: number
  total_cost_usd?: number
  total_profit_usd?: number
  total_commission_usd?: number
}

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

export interface TierResetConfig {
  enabled: boolean
  reset_day: number
  reset_hour: number
  reset_minute: number
  reset_second: number
  timezone: string
  last_reset_at: number
  next_reset_at: number
}

export interface TierResetResult {
  processed: number
  reset_at: number
}

export type EmployeeDialogType = 'create' | 'update' | 'delete'
export type ChannelCostDialogType = 'upsert' | 'delete'

/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { t } from 'i18next'

import type { ApiResponse } from '@/features/profile/types'
import { api } from '@/lib/api'
import { createServerError } from '@/lib/server-error-message'

export interface AuditLog {
  event_id: string
  user_id: number
  username: string
  actor_role: number
  actor_is_employee?: boolean | null
  ip_marker?: IPRiskMarker
  created_at: number
  category: string
  action: string
  token_ref: string
  auth_method?: string
  ip: string
  user_agent: string
  method: string
  route: string
  status: number
  success: boolean
  request_id: string
  content: string
  other: Record<string, unknown> | null
}

export const AUDIT_ACTIVITY_VERSION = 1

export interface AuditActivity {
  event_id: string
  created_at: number
  category: string
  action: string
  success: boolean
  ip: string
  client: string
  source: string
  login_method?: string
  request_id: string
  target?: { resource: string; id?: string; name?: string }
  change_summary?: { fields: string[]; total: number; outcome: string }
}

export type AuditRecord = AuditLog | AuditActivity
export interface AuditFilters {
  ip?: string
  actor_type?: string
  auth_method?: string
  route?: string
  route_missing?: boolean
  ip_risk?: string
  p: number
  page_size: number
  start_timestamp?: number
  end_timestamp?: number
  success?: string
  category?: string
  token_ref?: string
  exclude_token_ref?: string
  username?: string
  request_id?: string
  /** Server binds this management query to the signed-in account. */
  mine?: boolean
}

export function publicAuditFilters(params: AuditFilters): AuditFilters {
  const visible = {
    p: params.p,
    page_size: params.page_size,
    start_timestamp: params.start_timestamp,
    end_timestamp: params.end_timestamp,
    success: params.success,
    category: params.category,
    ip: params.ip,
    auth_method: params.auth_method,
    request_id: params.request_id,
    token_ref:
      params.category === 'access_token' ? params.token_ref : undefined,
    exclude_token_ref:
      params.category === 'access_token' ? params.exclude_token_ref : undefined,
  }
  return {
    ...Object.fromEntries(
      Object.entries(visible).filter(([, value]) => value !== undefined)
    ),
    p: params.p,
    page_size: params.page_size,
  }
}
export async function getAuditLogs(
  scope: 'all' | 'self',
  params: AuditFilters,
  signal?: AbortSignal
): Promise<{ items: AuditRecord[]; total: number }> {
  const response = await api.get<
    ApiResponse<{ items: AuditRecord[]; total: number }>
  >(scope === 'all' ? '/api/audit' : '/api/audit/self', {
    params: scope === 'self' ? publicAuditFilters(params) : params,
    signal,
  })
  if (!response.data.success || !response.data.data) {
    throw createServerError(response.data, t('Failed to load audit records'))
  }
  return response.data.data
}

export interface IPRiskMarker {
  event_id: string
  ip: string
  state: string
  level: string
  reasons: { rule: string; alert_id: number; status: string; at: number }[]
  more: boolean
}
export interface RiskCoverage {
  historical_complete: boolean
  backfill_done: boolean
  health: {
    enabled: boolean
    suspended: boolean
    ready: boolean
    capture_enabled: boolean
    history_truncated: boolean
  }
}
export async function getAuditRoutes(
  scope: 'all' | 'self',
  params: Record<string, string | number | undefined>,
  signal?: AbortSignal
) {
  const response = await api.get<
    ApiResponse<{ items: string[]; next_cursor?: string }>
  >(`${scope === 'all' ? '/api/audit' : '/api/audit/self'}/routes`, {
    params,
    signal,
  })
  if (!response.data.success || !response.data.data) {
    throw createServerError(response.data, t('Failed to load routes'))
  }
  return response.data.data
}
export async function getAuditIPRecords(
  filters: AuditFilters,
  cursor?: string,
  signal?: AbortSignal
) {
  const { p: _page, page_size, ...params } = filters
  const response = await api.get<
    ApiResponse<{
      items: AuditLog[]
      next_cursor?: string
      coverage: RiskCoverage
    }>
  >('/api/audit/security/ip-records', {
    params: { ...params, cursor, limit: page_size },
    signal,
  })
  if (!response.data.success || !response.data.data) {
    throw createServerError(response.data, t('Failed to load audit records'))
  }
  return response.data.data
}
export async function getAuditIPMarkers(
  eventIds: string[],
  filters: AuditFilters,
  signal?: AbortSignal
) {
  const response = await api.get<
    ApiResponse<{ items: IPRiskMarker[]; coverage: RiskCoverage }>
  >('/api/audit/security/ip-markers', {
    params: {
      event_ids: eventIds.join(','),
      start_timestamp: filters.start_timestamp,
      end_timestamp: filters.end_timestamp,
    },
    signal,
  })
  if (!response.data.success || !response.data.data) {
    throw createServerError(response.data, t('Risk markers unavailable'))
  }
  return response.data.data
}

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
import { isAxiosError } from 'axios'
import { t } from 'i18next'

import type { CustomerProfile } from '@/features/customers/types'
import type { ApiResponse } from '@/features/profile/types'
import type { AuditActivity } from '@/features/usage-logs/audit/api'
import { api } from '@/lib/api'
import { createServerError } from '@/lib/server-error-message'

export type CustomerAuditAccount = Pick<
  CustomerProfile,
  'customer_user_id' | 'username' | 'display_name'
>

export interface CustomerAuditIdentity {
  id: number
  username: string
  display_name: string
}

export interface CustomerAuditActivity extends AuditActivity {
  customer?: CustomerAuditIdentity
}

export interface CustomerSecurityResponse {
  id: number
  action: string
  source: 'automatic' | 'manual'
  outcome: string
  reason?: string
  created_at: number
  affected_count?: number
}

export interface CustomerSecurityAlert {
  customer?: CustomerAuditIdentity
  response?: CustomerSecurityResponse
  responses?: CustomerSecurityResponse[]
  id: number
  rule: string
  severity: number
  status: string
  first_seen: number
  last_seen: number
  updated_at: number
  occurrences: number
  matched_count: number
  version: number
  conclusion: string
}

export interface CustomerSecurityEvent {
  event_id: string
  action: string
  at: number
  ip: string
  request_id: string
  success: boolean
}

export interface CustomerSecurityDetail {
  alert: CustomerSecurityAlert
  events: CustomerSecurityEvent[]
  actions: {
    id: number
    source: 'employee' | 'admin' | 'system'
    from_status: string
    to_status: string
    conclusion: string
    note?: string
    action?: string
    outcome?: string
    reason?: string
    affected_count?: number
    created_at: number
  }[]
}

export interface CustomerAuditFilters {
  start: number
  end: number
  request_id: string
  ip: string
}

export interface CustomerAuditPage<T> {
  items: T[]
  next_cursor: string
}

export function isCustomerAccessDenied(error: unknown) {
  return isAxiosError(error) && error.response?.status === 403
}

export async function customerAuditGet<T>(
  customerID: number,
  path: string,
  signal?: AbortSignal,
  params?: Record<string, unknown>
): Promise<T> {
  const { data } = await api.get<ApiResponse<T>>(
    `/api/user/employee/customers/${customerID}/${path}`,
    // Query owns signalled requests; a remount must not reuse an aborted one.
    { signal, params, disableDuplicate: !!signal }
  )
  if (!data.success || data.data == null) {
    throw createServerError(data, t('Failed to load audit records'))
  }
  return data.data
}

export async function employeeAuditGet<T>(
  kind: 'audit' | 'security/alerts',
  signal?: AbortSignal,
  params?: Record<string, unknown>
): Promise<CustomerAuditPage<T>> {
  const { data } = await api.get<ApiResponse<CustomerAuditPage<T>>>(
    `/api/user/employee/customers/${kind}`,
    // Match securityGet: cancellation and deduplication belong to Query.
    { signal, params, disableDuplicate: !!signal }
  )
  if (!data.success || data.data == null) {
    throw createServerError(data, t('Failed to load audit records'))
  }
  return data.data
}

export async function saveCustomerAlert(
  customerID: number,
  alertID: number,
  values: {
    version: number
    status: string
    note?: string
    conclusion?: string
  }
) {
  const { data } = await api.post<ApiResponse<CustomerSecurityAlert>>(
    `/api/user/employee/customers/${customerID}/security/alerts/${alertID}/actions`,
    values
  )
  if (!data.success || !data.data) {
    throw createServerError(data, t('Save failed'))
  }
  return data.data
}

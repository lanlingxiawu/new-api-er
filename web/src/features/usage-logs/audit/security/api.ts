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

import type { IPRiskMarker } from '../api'

export interface SecurityAssignee {
  id: number
  username: string
  role: number
  source: 'owner' | 'manual'
}
export interface SecurityAlert {
  target_user_id?: number
  target_role?: number
  target_username?: string
  attempted_account?: string
  actor_username?: string
  assignees?: SecurityAssignee[]
  manual_assignee_ids?: number[]
  response_group_id?: number
  can_stop?: boolean
  id: number
  rule: string
  severity: number
  status: string
  actor_id: number
  actor_role?: number
  response?: SecurityAccountResponse
  responses?: SecurityAccountResponse[]
  resource_type: string
  first_seen: number
  last_seen: number
  occurrences: number
  repeat_until?: number | null
  confirmed_occurrences?: number | null
  matched_count: number
  version: number
  rule_version?: number
  updated_at?: number
  closed_at?: number | null
  assignee_id?: number
  conclusion?: string
  conclusion_note?: string
  archived_at?: number | null
  latest_finding_id?: number
}
export interface SecurityAccountResponse {
  response_group_id?: number
  action?: string
  affected_count?: number
  id: number
  source: string
  outcome: string
  reason_code: string
  target_user_id: number
  created_at: number
}
export interface SecurityEvent {
  target_role?: number
  target_username?: string
  actor_username?: string
  event_id: string
  action: string
  at: number
  actor_id: number
  actor_role?: number
  resource_type?: string
  reason?: string
  target_id: number
  ip: string
  request_id: string
  success: boolean
  affected_count: number
  facts: string
}
export interface SecurityDetail {
  alert: SecurityAlert
  events: SecurityEvent[]
  ip_markers?: IPRiskMarker[]
  findings?: SecurityFinding[]
  actions: {
    actor_username?: string
    id: number
    actor_id: number
    action: string
    note: string
    created_at: number
    source?: string
    from_status?: string
    to_status?: string
    event_id?: string
    event_at?: number
    detected_at?: number
    assignee_id?: number
    assignees?: SecurityAssignee[]
    conclusion?: string
    outcome?: string
    reason_code?: string
    target_user_id?: number
    affected_count?: number
  }[]
  deliveries: {
    id: number
    kind: string
    status: string
    attempts: number
    finding_id?: number
    wait_reason?: string
    error_code?: string
    created_at?: number
    updated_at?: number
    sent_at?: number
    next_attempt_at?: number
  }[]
}
export interface SecurityFinding {
  target_role?: number
  target_username?: string
  attempted_account?: string
  actor_username?: string
  id: number
  alert_id: number
  rule: string
  rule_version: number
  trigger_event_id: string
  trigger_at: number
  detected_at: number
  source: string
  subject_ref: string
  actor_id: number
  actor_role: number
  resource_type: string
  target_id: number
  ip: string
  request_id: string
  severity: number
  threshold: number
  window_seconds: number
  matched_count: number
  dimensions: Record<string, number>
  truncated: boolean
}
export interface SecurityCapacity {
  total: number
  open: number
  archived: number
  retention_unknown: number
  truncated: boolean
  warning: boolean
}
export interface SecurityHealth {
  relay_ip_enabled?: boolean
  relay_ip_queued?: number
  relay_ip_dropped?: number
  relay_ip_missing?: number
  relay_ip_last_observed?: number
  enabled: boolean
  capture_enabled: boolean
  suspended: boolean
  auto_ban_enabled?: boolean
  ready: boolean
  last_scan: number
  cursor_at: number
  dropped: number
  history_truncated: boolean
  error_code: string
  storage_ready?: boolean
  activity_revision?: number
  capacity?: SecurityCapacity
  ip_intelligence_enabled?: boolean
  providers?: SecurityProviderHealth[]
}
export interface SecurityProviderHealth {
  id: string
  name: string
  type: string
  enabled: boolean
  priority: number
  daily_limit: number
  key_configured: boolean
  capabilities: string[]
  used: number
  circuit_until: number
  last_error: string
}
export interface SecurityRelation {
  target_role?: number
  target_username?: string
  attempted_account?: string
  actor_username?: string
  alert_id: number
  rule: string
  rule_version: number
  finding_id: number
  event_id: string
  at: number
  detected_at: number
  source: string
  subject_ref: string
  actor_id: number
  actor_role: number
  resource_type: string
  target_id: number
  ip: string
  request_id: string
  severity: number
  matched_count: number
  relations: string[]
}
export interface SecurityCoverage {
  complete: boolean
  reason: string
}
export async function securityGet<T>(
  path: string,
  signal?: AbortSignal,
  params?: Record<string, unknown>
): Promise<T> {
  const { data } = await api.get<ApiResponse<T>>(
    `/api/audit/security/${path}`,
    // Query already deduplicates reads. Its replacement fetch must not reuse
    // an aborted request still present in the HTTP client's in-flight map.
    { signal, params, disableDuplicate: true }
  )
  if (!data.success || data.data == null) {
    throw createServerError(
      data,
      t('Unable to load security monitoring. Check settings and retry.')
    )
  }
  return data.data
}
export async function saveAlert(
  id: number,
  version: number,
  status: string,
  note: string,
  metadata?: {
    assignee_id?: number
    assignee_ids?: number[]
    conclusion?: string
    observed_occurrences?: number
  }
) {
  const { data } = await api.post<ApiResponse<null>>(
    `/api/audit/security/alerts/${id}/actions`,
    { version, status, note, ...metadata }
  )
  if (!data.success) throw createServerError(data, t('Save failed'))
}

export async function banSecurityAccount(
  id: number,
  version: number,
  requestID: string,
  proof: string
) {
  const { data } = await api.post<ApiResponse<SecurityAccountResponse>>(
    `/api/audit/security/alerts/${id}/responses`,
    { version, action: 'disable_account', request_id: requestID },
    { headers: { 'X-Security-Proof': proof }, singleUseAuthorization: true }
  )
  if (!data.success || !data.data) {
    throw createServerError(
      data,
      t('The result is not confirmed. Refresh first.')
    )
  }
  return data.data
}

export async function archiveAlert(
  id: number,
  version: number,
  archived: boolean
) {
  const { data } = await api.post<ApiResponse<SecurityAlert>>(
    `/api/audit/security/alerts/${id}/archive`,
    { version, archived }
  )
  if (!data.success) throw createServerError(data, t('Save failed'))
}

export async function exportAlerts(params: Record<string, unknown>) {
  const { data } = await api.get<Blob>('/api/audit/security/alerts/export', {
    params: { ...params, limit: 100 },
    responseType: 'blob',
  })
  const result: unknown = JSON.parse(await data.text())
  if (
    result &&
    typeof result === 'object' &&
    'success' in result &&
    result.success === false
  ) {
    throw createServerError(result, t('Export failed'))
  }
  const url = URL.createObjectURL(data)
  const link = document.createElement('a')
  link.href = url
  link.download = `security-alerts-${new Date().toISOString().slice(0, 10)}.json`
  link.click()
  URL.revokeObjectURL(url)
}
export interface AlertCleanupResult {
  queued: number
  has_more: boolean
}

export async function deleteAlert(id: number, version: number) {
  const { data } = await api.delete<ApiResponse<AlertCleanupResult>>(
    `/api/audit/security/alerts/${id}`,
    { data: { version } }
  )
  if (!data.success || !data.data) {
    throw createServerError(
      data,
      t('The alert may have changed. Refresh and try again.')
    )
  }
  return data.data
}

export async function cleanupAlerts(closedBefore: number) {
  const { data } = await api.post<ApiResponse<AlertCleanupResult>>(
    '/api/audit/security/alerts/cleanup',
    { closed_before: closedBefore }
  )
  if (!data.success || !data.data) {
    throw createServerError(data, t('Cleanup failed. Please try again.'))
  }
  return data.data
}
export async function getMonitorSettings(signal?: AbortSignal) {
  const { data } = await api.get<ApiResponse<{ key: string; value: string }[]>>(
    '/api/option/',
    { signal, params: { scope: 'security.audit-monitor' } }
  )
  if (!data.success || !data.data) {
    throw createServerError(data, t('Failed to load settings'))
  }
  return Object.fromEntries(
    data.data.map((item) => [
      item.key.replace('audit_security_setting.', ''),
      item.value,
    ])
  )
}
export async function saveMonitorSettings(
  values: Record<string, string>,
  proofToken: string
) {
  const { data } = await api.put<ApiResponse<null>>(
    '/api/option/group',
    {
      scope: 'security.audit-monitor',
      module: 'audit_security_setting',
      values,
    },
    { headers: { 'X-Security-Proof': proofToken } }
  )
  if (!data.success) throw createServerError(data, t('Save failed'))
}

export async function stopSecurityResponse(
  id: number,
  version: number,
  groupID: number,
  requestID: string,
  proof: string
) {
  const { data } = await api.post<ApiResponse<SecurityAccountResponse>>(
    `/api/audit/security/alerts/${id}/responses`,
    {
      version,
      action: 'stop_automatic_response',
      response_group_id: groupID,
      request_id: requestID,
    },
    { headers: { 'X-Security-Proof': proof }, singleUseAuthorization: true }
  )
  if (!data.success || !data.data) {
    throw createServerError(
      data,
      t('The result is not confirmed. Refresh first.')
    )
  }
  return data.data
}

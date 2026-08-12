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
import { api } from '@/lib/api'

import type {
  BusinessStatsCircuitBreakerStatusResponse,
  ConfirmPaymentComplianceResponse,
  DatabasePoolRuntimeStatusResponse,
  DeleteLogsResponse,
  FetchUpstreamRatiosRequest,
  LedgerPipelineStatusResponse,
  RelayLogPipelineStatusResponse,
  RelayLogReplayStartResponse,
  LogCleanupTask,
  SystemOptionsResponse,
  SystemTaskListResponse,
  SystemTaskResponse,
  UpdateOptionRequest,
  UpdateOptionGroupRequest,
  UpdateOptionGroupResponse,
  UpdateOptionResponse,
  UpstreamChannelsResponse,
  UpstreamRatiosResponse,
  PriceMonitorResultsResponse,
  PriceMonitorStatusResponse,
} from './types'

export async function getScopedSystemOptions(scope: string) {
  const res = await api.get<SystemOptionsResponse>('/api/option/', {
    params: { scope },
  })
  return res.data
}

export async function getPriceMonitorStatus() {
  const res = await api.get<PriceMonitorStatusResponse>(
    '/api/price_monitor/status',
    { disableDuplicate: true }
  )
  return res.data
}

export async function getPriceMonitorResults(params: {
  model?: string
  source_keys?: string
  comparison?: string
  page: number
  page_size: number
}) {
  const res = await api.get<PriceMonitorResultsResponse>(
    '/api/price_monitor/results',
    { params, disableDuplicate: true }
  )
  return res.data
}

export async function runPriceMonitor() {
  const res = await api.post<UpdateOptionResponse>('/api/price_monitor/run', {})
  return res.data
}

export async function updateSystemOption(request: UpdateOptionRequest) {
  const res = await api.put<UpdateOptionResponse>('/api/option/', request)
  return res.data
}

export async function updateSystemOptionGroup(
  request: UpdateOptionGroupRequest
) {
  const res = await api.put<UpdateOptionGroupResponse>(
    '/api/option/group',
    request
  )
  return res.data
}

export async function getDatabasePoolRuntimeStatus() {
  const res = await api.get<DatabasePoolRuntimeStatusResponse>(
    '/api/option/db-pool/stats',
    { disableDuplicate: true }
  )
  return res.data
}

export async function getBusinessStatsCircuitBreakerStatus() {
  const res = await api.get<BusinessStatsCircuitBreakerStatusResponse>(
    '/api/option/business-stats-circuit-breaker/status',
    { disableDuplicate: true }
  )
  return res.data
}

export async function getLedgerPipelineStatus() {
  const res = await api.get<LedgerPipelineStatusResponse>(
    '/api/admin/system/ledger-pipeline/status',
    { disableDuplicate: true }
  )
  return res.data
}

export async function getRelayLogPipelineStatus() {
  const res = await api.get<RelayLogPipelineStatusResponse>(
    '/api/admin/system/relay-log-pipeline/status',
    { disableDuplicate: true }
  )
  return res.data
}

export async function startRelayLogFallbackReplay() {
  const res = await api.post<RelayLogReplayStartResponse>(
    '/api/admin/system/relay-log-pipeline/replay',
    {}
  )
  return res.data
}

export async function confirmPaymentCompliance() {
  const res = await api.post<ConfirmPaymentComplianceResponse>(
    '/api/option/payment_compliance',
    { confirmed: true }
  )
  return res.data
}

export async function startLogCleanupTask(targetTimestamp: number) {
  const res = await api.post<SystemTaskResponse<LogCleanupTask>>(
    '/api/system-task/log-cleanup',
    null,
    {
      params: { target_timestamp: targetTimestamp },
    }
  )
  return res.data
}

export async function getCurrentLogCleanupTask() {
  const res = await api.get<SystemTaskResponse<LogCleanupTask | null>>(
    '/api/system-task/current',
    {
      params: { type: 'log_cleanup' },
    }
  )
  return res.data
}

export async function getSystemTask(taskId: string) {
  const res = await api.get<SystemTaskResponse<LogCleanupTask>>(
    `/api/system-task/${taskId}`
  )
  return res.data
}

export async function listSystemTasks(limit = 20) {
  const res = await api.get<SystemTaskListResponse>('/api/system-task/list', {
    params: { limit },
  })
  return res.data
}

export async function clearAllRequestLogs() {
  const res = await api.delete<DeleteLogsResponse>('/api/request-log/all')
  return res.data
}

export async function resetModelRatios() {
  const res = await api.post<UpdateOptionResponse>(
    '/api/option/rest_model_ratio'
  )
  return res.data
}

export async function getUpstreamChannels() {
  const res = await api.get<UpstreamChannelsResponse>(
    '/api/ratio_sync/channels'
  )
  return res.data
}

export async function fetchUpstreamRatios(request: FetchUpstreamRatiosRequest) {
  const res = await api.post<UpstreamRatiosResponse>(
    '/api/ratio_sync/fetch',
    request
  )
  return res.data
}

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

import { buildQueryParams } from './lib/utils'
import type {
  GetLogsParams,
  GetLogsResponse,
  GetLogStatsParams,
  GetLogStatsResponse,
  GetMidjourneyLogsParams,
  GetTaskLogsParams,
  LogsScope,
  UpstreamLogChannelOption,
  UpstreamLogQueryData,
  UpstreamLogQueryRequest,
  UserInfo,
} from './types'

// ============================================================================
// Generic API Helpers
// ============================================================================

function buildApiPath(endpoint: string, scope: LogsScope): string {
  if (scope === 'admin') return endpoint
  if (scope === 'employee') return `${endpoint}/employee`
  return `${endpoint}/self`
}

async function fetchLogs<T>(
  endpoint: string,
  params: T,
  scope: LogsScope
): Promise<GetLogsResponse> {
  const paramRecord = params as unknown as Record<string, unknown>
  const queryParams = buildQueryParams({
    p: paramRecord.p || 1,
    page_size: paramRecord.page_size || 20,
    ...params,
  })
  const path = buildApiPath(endpoint, scope)
  const res = await api.get(`${path}?${queryParams}`)
  return res.data
}

async function fetchLogStats<T>(
  endpoint: string,
  params: T,
  scope: LogsScope
): Promise<GetLogStatsResponse> {
  const queryParams = buildQueryParams(
    params as unknown as Record<string, unknown>
  )
  const path = buildApiPath(endpoint, scope)
  const res = await api.get(`${path}/stat?${queryParams}`)
  return res.data
}

// ============================================================================
// Common Log APIs
// ============================================================================

export const getAllLogs = (params: GetLogsParams = {}) =>
  fetchLogs('/api/log', params, 'admin')

export const getEmployeeCustomerLogs = (params: GetLogsParams = {}) =>
  fetchLogs('/api/log', params, 'employee')

export const getUserLogs = (
  params: Omit<GetLogsParams, 'username' | 'channel'> = {}
) => fetchLogs('/api/log', params, 'self')

export const getLogStats = (params: GetLogStatsParams = {}) =>
  fetchLogStats('/api/log', params, 'admin')

export const getEmployeeCustomerLogStats = (
  params: GetLogStatsParams = {}
) => fetchLogStats('/api/log', params, 'employee')

export const getUserLogStats = (
  params: Omit<GetLogStatsParams, 'username' | 'channel'> = {}
) => fetchLogStats('/api/log', params, 'self')

export async function getUserInfo(
  userId: number
): Promise<{ success: boolean; message?: string; data?: UserInfo }> {
  const res = await api.get(`/api/user/${userId}`)
  return res.data
}

// ============================================================================
// Upstream Log Query (admin only)
// ============================================================================

/**
 * Query the upstream New API instance's logs for a given channel.
 * Admin-only endpoint: the backend enforces AdminAuth, loads the channel
 * credential server-side, and never returns the channel key to the browser.
 */
export async function queryUpstreamLog(
  body: UpstreamLogQueryRequest,
  signal?: AbortSignal
): Promise<{ success: boolean; message?: string; data?: UpstreamLogQueryData }> {
  const res = await api.post('/api/log/upstream/query', body, { signal })
  return res.data
}

/** List New API channels available as upstream-log query targets (admin only). */
export async function getUpstreamLogChannels(
  keyword = '',
  signal?: AbortSignal
): Promise<{
  success: boolean
  message?: string
  data?: UpstreamLogChannelOption[]
}> {
  const query = keyword ? `?keyword=${encodeURIComponent(keyword)}` : ''
  const res = await api.get(`/api/log/upstream/channels${query}`, { signal })
  return res.data
}

// ============================================================================
// Log Export (xlsx)
// ============================================================================

/** Thrown when the export endpoint returns a business error (HTTP 200 + JSON). */
export class LogExportError extends Error {}

function parseFilename(disposition?: string): string | undefined {
  if (!disposition) return undefined
  const match = /filename="?([^"]+)"?/.exec(disposition)
  return match?.[1]
}

function triggerBlobDownload(blob: Blob, filename: string): void {
  const url = window.URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  window.URL.revokeObjectURL(url)
}

/**
 * Export logs as CSV for the given filters and scope.
 * Uses a blob request so the caller can catch HTTP 429 (rate limit) and
 * surface a friendly message. Validation failures come back as HTTP 200 with
 * a JSON body, which is detected here and rethrown as a LogExportError.
 */
export async function exportLogs(
  params: GetLogsParams,
  scope: Exclude<LogsScope, 'employee'>
): Promise<void> {
  const queryParams = buildQueryParams(
    params as unknown as Record<string, unknown>
  )
  const path = scope === 'admin' ? '/api/log/export' : '/api/log/self/export'
  const res = await api.get(`${path}?${queryParams}`, {
    responseType: 'blob',
    skipErrorHandler: true, // handle 429 ourselves
    skipBusinessError: true,
    disableDuplicate: true,
  })

  const blob = res.data as Blob
  // Backend validation errors are returned as HTTP 200 + JSON, not CSV.
  if (blob.type.includes('application/json')) {
    const text = await blob.text()
    let message = ''
    try {
      message = (JSON.parse(text) as { message?: string }).message ?? ''
    } catch {
      /* empty */
    }
    throw new LogExportError(message)
  }

  const filename =
    parseFilename(res.headers['content-disposition'] as string | undefined) ??
    `logs_${Date.now()}.xlsx`
  triggerBlobDownload(blob, filename)
}

// ============================================================================
// MjProxy (Drawing) Logs API
// ============================================================================

export const getAllMidjourneyLogs = (params: GetMidjourneyLogsParams) =>
  fetchLogs('/api/mj', params, 'admin')

export const getUserMidjourneyLogs = (params: GetMidjourneyLogsParams) =>
  fetchLogs('/api/mj', params, 'self')

// ============================================================================
// Task Logs API
// ============================================================================

export const getAllTaskLogs = (params: GetTaskLogsParams) =>
  fetchLogs('/api/task', params, 'admin')

export const getUserTaskLogs = (params: GetTaskLogsParams) =>
  fetchLogs('/api/task', params, 'self')

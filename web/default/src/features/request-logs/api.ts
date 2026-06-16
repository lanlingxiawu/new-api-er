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
import type { RequestLogFilters, RequestLogItem, RequestLogsPage } from './types'

interface ApiResponse<T> {
  success: boolean
  message?: string
  data?: T
}

export async function getRequestLogs(
  page: number,
  pageSize: number,
  filters: RequestLogFilters
): Promise<RequestLogsPage> {
  const params: Record<string, string | number> = {
    p: page,
    page_size: pageSize,
  }
  if (filters.username) params.username = filters.username
  if (filters.model_name) params.model_name = filters.model_name
  if (filters.request_id) params.request_id = filters.request_id
  if (filters.status_code) params.status_code = filters.status_code
  if (filters.start_timestamp) params.start_timestamp = filters.start_timestamp
  if (filters.end_timestamp) params.end_timestamp = filters.end_timestamp

  const query = new URLSearchParams(
    Object.entries(params).map(([k, v]) => [k, String(v)])
  ).toString()
  const res = await api.get<ApiResponse<RequestLogsPage>>(
    `/api/request-log/?${query}`
  )
  return (
    res.data.data ?? { page, page_size: pageSize, total: 0, items: [] }
  )
}

export async function getRequestLogDetail(
  id: number
): Promise<RequestLogItem | undefined> {
  const res = await api.get<ApiResponse<RequestLogItem>>(
    `/api/request-log/${id}`
  )
  return res.data.data
}

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

export type MonitorStatus = 1 | 2 | 3 | 4

export type ModelStatusItem = {
  id: number
  model_name: string
  status: MonitorStatus
  last_test_time: number
  success_rate: number
  avg_response_time: number
  p95_response_time: number
  error_count: number
  total_requests: number
  total_channels: number
  available_channels: number
  created_at: number
  updated_at: number
}

export type GroupStatusItem = {
  id: number
  user_group: string
  status: MonitorStatus
  last_test_time: number
  success_rate: number
  avg_response_time: number
  p95_response_time: number
  error_count: number
  total_requests: number
  total_channels: number
  available_channels: number
  disabled_channels: number
  created_at: number
  updated_at: number
}

export async function getModelStatuses(): Promise<{
  success: boolean
  data: ModelStatusItem[]
}> {
  const res = await api.get('/api/monitor/model/status')
  return res.data
}

export async function getGroupStatuses(): Promise<{
  success: boolean
  data: GroupStatusItem[]
}> {
  const res = await api.get('/api/monitor/group/status')
  return res.data
}

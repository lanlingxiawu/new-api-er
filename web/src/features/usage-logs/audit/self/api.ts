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

export interface SelfSecurityAlert {
  id: number
  rule: string
  severity: number
  status: string
  first_seen: number
  last_seen: number
}

export interface SelfSecurityPage {
  items: SelfSecurityAlert[]
  next_cursor: string
}

export interface SelfSecurityDetail {
  alert: SelfSecurityAlert
  events: { action: string; at: number; ip: string; success: boolean }[]
  responses: {
    id: number
    action: string
    outcome: string
    created_at: number
    affected_count?: number
  }[]
}

export async function selfSecurityGet<T>(
  path: string,
  signal: AbortSignal,
  params?: Record<string, unknown>
): Promise<T> {
  const { data } = await api.get<ApiResponse<T>>(
    `/api/audit/self/security/${path}`,
    { signal, params }
  )
  if (!data.success || data.data == null) {
    throw createServerError(data, t('Failed to load audit records'))
  }
  return data.data
}

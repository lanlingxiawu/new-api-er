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
import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'

import { useAuthStore } from '@/stores/auth-store'

import {
  AUDIT_ACTIVITY_VERSION,
  getAuditIPMarkers,
  getAuditIPRecords,
  getAuditLogs,
  type AuditFilters,
  type AuditRecord,
  type RiskCoverage,
} from '../api'

export function useAuditPage(
  scope: 'all' | 'self',
  params: AuditFilters,
  cursor: string,
  security: boolean,
  enabled: boolean
) {
  const user = useAuthStore((s) => s.auth.user)
  const key = [
    'audit',
    user?.id,
    scope,
    user?.role,
    user?.permissions,
    AUDIT_ACTIVITY_VERSION,
  ]
  const query = useQuery({
    queryKey: [...key, 'records', params, cursor],
    queryFn: async ({
      signal,
    }): Promise<{
      items: AuditRecord[]
      total: number
      next_cursor?: string
      coverage?: RiskCoverage
    }> => {
      if (params.ip_risk) {
        return {
          ...(await getAuditIPRecords(params, cursor, signal)),
          total: 0,
        }
      }
      return getAuditLogs(scope, params, signal)
    },
    enabled,
    retry: false,
    gcTime: 0,
  })
  const ids =
    enabled && !query.isError
      ? (query.data?.items.map((row) => row.event_id) ?? [])
      : []
  const batch = {
    start_timestamp: params.start_timestamp,
    end_timestamp: params.end_timestamp,
  }
  const markers = useQuery({
    queryKey: [...key, 'markers', ids, batch, security],
    queryFn: ({ signal }) => getAuditIPMarkers(ids, params, signal),
    enabled: ids.length > 0 && security && !params.ip_risk,
    retry: false,
    gcTime: 0,
    meta: { errorToast: false },
  })
  const rows = useMemo(() => {
    const byRisk = new Map(
      markers.isError
        ? []
        : markers.data?.items.map((row) => [row.event_id, row])
    )
    return (
      query.data?.items.map((row) =>
        security && 'other' in row
          ? {
              ...row,
              ip_marker: row.ip_marker ?? byRisk.get(row.event_id),
            }
          : row
      ) ?? []
    )
  }, [query.data, markers.data, markers.isError, security])
  return {
    query,
    markers,
    rows,
    coverage: query.data?.coverage ?? markers.data?.coverage,
  }
}

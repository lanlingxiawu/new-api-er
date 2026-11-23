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
import type { OnChangeFn, PaginationState } from '@tanstack/react-table'
import { useState } from 'react'

import { requireServerSuccess } from '@/lib/server-error-message'

import type {
  ApiResponse,
  CommissionLogCursor,
  CommissionLogFilters,
  CommissionLogPage,
  CommissionLogPageParams,
} from '../types'

export function useCommissionLogPage(
  key: string,
  filters: CommissionLogFilters,
  fetchPage: (
    params: CommissionLogPageParams,
    signal?: AbortSignal
  ) => Promise<ApiResponse<CommissionLogPage>>
) {
  const [state, setState] = useState({
    pageIndex: 0,
    pageSize: 10,
    cursors: [null] as (CommissionLogCursor | null)[],
    revision: 0,
  })
  const cursor = state.cursors[state.pageIndex]
  const query = useQuery({
    queryKey: [key, filters, state.pageSize, cursor, state.revision],
    retry: false,
    queryFn: async ({ signal }) =>
      requireServerSuccess(
        await fetchPage(
          {
            ...filters,
            page_size: state.pageSize,
            cursor,
          },
          signal
        )
      ),
  })
  const onPaginationChange: OnChangeFn<PaginationState> = (updater) => {
    if (query.isFetching) return
    setState((current) => {
      const next = typeof updater === 'function' ? updater(current) : updater
      if (next.pageSize !== current.pageSize) {
        return {
          ...current,
          pageSize: next.pageSize,
          pageIndex: 0,
          cursors: [null],
        }
      }
      if (next.pageIndex < 0) return current
      if (next.pageIndex < current.pageIndex) {
        return { ...current, pageIndex: next.pageIndex }
      }
      const nextCursor = query.data?.data?.next_cursor
      if (
        next.pageIndex === current.pageIndex + 1 &&
        query.data?.data?.has_more &&
        nextCursor &&
        !query.isError
      ) {
        return {
          ...current,
          pageIndex: next.pageIndex,
          cursors: [...current.cursors.slice(0, next.pageIndex), nextCursor],
        }
      }
      return current
    })
  }
  const resetPagination = () =>
    setState((current) => ({
      ...current,
      pageIndex: 0,
      cursors: [null],
      revision: current.revision + 1,
    }))

  return {
    query,
    pagination: { pageIndex: state.pageIndex, pageSize: state.pageSize },
    onPaginationChange,
    resetPagination,
    hasNextPage:
      !!query.data?.data?.has_more &&
      !!query.data?.data?.next_cursor &&
      !query.isError,
  }
}

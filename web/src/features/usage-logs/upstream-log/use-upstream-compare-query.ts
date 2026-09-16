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
import { useTranslation } from 'react-i18next'

import { queryUpstreamLog } from '../api'

/**
 * 按本站请求 ID 反查上游日志。放在详情弹窗层调用：右栏展示结果，左栏同时要用它
 * 标出本站低于上游的数据项。只在用户点击「查询上游」后启用。
 */
export function useUpstreamCompareQuery(
  localRequestId: string,
  enabled: boolean
) {
  const { t } = useTranslation()
  return useQuery({
    queryKey: ['upstream-log-compare', localRequestId],
    queryFn: async ({ signal }) => {
      const response = await queryUpstreamLog(
        { local_request_id: localRequestId, page: 1, page_size: 5 },
        signal
      )
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Request failed'))
      }
      return response.data
    },
    enabled: enabled && localRequestId.length > 0,
    retry: false,
    staleTime: 30_000,
  })
}

export type UpstreamCompareQuery = ReturnType<typeof useUpstreamCompareQuery>

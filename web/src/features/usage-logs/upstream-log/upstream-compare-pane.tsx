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
import { RotateCcw } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'

import { LogDetailBody } from '../components/dialogs/log-detail-body'
import {
  type LogDetailSection,
  compareCellClassName,
  compareCellStyle,
} from '../components/dialogs/log-detail-grid'
import { upstreamItemToUsageLog } from '../lib/upstream-log-item'
import type { UpstreamCompareQuery } from './use-upstream-compare-query'

// 加载、出错、无结果时占据右栏的行数：覆盖全部分区行，避免只撑高左侧概览那一行。
const STATE_ROW_SPAN = 30

/**
 * 对比网格的右栏：上游日志与左侧本站日志按同一张行表排列，同类分区左右对齐。
 * 查询由详情弹窗发起并传入，左栏同时用结果标出本站低于上游的数据项。
 */
export function UpstreamComparePane({
  query,
  section,
  showOverview,
}: {
  query: UpstreamCompareQuery
  section?: LogDetailSection
  showOverview?: boolean
}) {
  const { t } = useTranslation()
  const item = query.data?.items[0]

  // 渠道和请求 ID 映射左侧详情已有（右侧「请求 ID」即左侧「上游请求 ID」），不再重复；
  // 只保留需要用户留意的提示。
  const heading = (
    <div className='space-y-2 max-lg:border-t max-lg:pt-4'>
      <h3 className='border-b pb-2 text-sm font-semibold'>
        {t('Upstream Log Details')}
      </h3>
      {item && query.data?.source?.key_index_from_log === false && (
        <StatusBadge
          label={t('Channel key not recorded in the local log')}
          variant='amber'
          size='sm'
        />
      )}
    </div>
  )

  if (item && query.data) {
    return (
      <LogDetailBody
        log={upstreamItemToUsageLog(item)}
        isAdmin
        section={section}
        showOverview={showOverview}
        // 不挂载流式诊断：它按请求 ID 查本站服务器，而这里的请求 ID 属于上游。
        showStreamDiagnostic={false}
        compareColumn={2}
        heading={heading}
        footer={
          query.data.total > 1 ? (
            <p className='text-muted-foreground text-xs'>
              {t(
                'The upstream returned {{count}} matching logs; the first one is shown.',
                { count: query.data.total }
              )}
            </p>
          ) : undefined
        }
      />
    )
  }

  let state: ReactNode
  if (query.isPending) {
    state = (
      <div className='space-y-3'>
        <Skeleton className='h-5 w-32' />
        <Skeleton className='h-40 w-full rounded-lg' />
        <Skeleton className='h-28 w-full rounded-lg' />
      </div>
    )
  } else if (query.isError) {
    state = (
      <div className='space-y-3'>
        <p className='text-muted-foreground text-sm'>
          {query.error instanceof Error
            ? query.error.message
            : t('Request failed')}
        </p>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={() => void query.refetch()}
        >
          <RotateCcw />
          {t('Retry')}
        </Button>
      </div>
    )
  } else {
    state = (
      <p className='text-muted-foreground text-sm'>
        {query.data?.query.checked_account
          ? t(
              'The upstream instance has no log for this request. It may have been trimmed by the upstream retention policy.'
            )
          : t(
              'This request is not among the most recent logs the channel key can read. Add an upstream account access token to the channel to search the full history.'
            )}
      </p>
    )
  }

  return (
    <>
      <div
        className={compareCellClassName(2)}
        style={compareCellStyle('heading')}
      >
        {heading}
      </div>
      <div
        className={compareCellClassName(2)}
        style={compareCellStyle('overview', STATE_ROW_SPAN)}
      >
        {state}
      </div>
    </>
  )
}

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
import { useNavigate } from '@tanstack/react-router'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { StatusBadge, type StatusBadgeProps } from '@/components/status-badge'
import { cn } from '@/lib/utils'

import type { UsageLog } from '../../data/schema'
import { isTieredBillingLog, parseLogOther } from '../../lib/format'
import { collectLogMetrics, findLowerMetrics } from '../../lib/log-metrics'
import { upstreamItemToUsageLog } from '../../lib/upstream-log-item'
import { getLogTypeConfig } from '../../lib/utils'
import { UpstreamComparePane } from '../../upstream-log/upstream-compare-pane'
import { useUpstreamCompareQuery } from '../../upstream-log/use-upstream-compare-query'
import { LogDetailBody } from './log-detail-body'

interface DetailsDialogProps {
  log: UsageLog // 当前日志，含请求 ID、Unix 秒创建时间和后端已过滤的扩展字段。
  isAdmin: boolean // 控制既有管理员费用字段展示，原始诊断权限由面板与后端独立判断。
  isRoot: boolean // 超级管理员额外可见 root_info 诊断字段。
  open: boolean // 弹窗是否打开，同时控制私有诊断面板的挂载生命周期。
  onOpenChange: (open: boolean) => void // 向父组件报告显隐变化，参数 open 为新的打开状态。
}

/**
 * DetailsDialog 展示单条使用日志的费用、流状态及按需加载的私有诊断。
 *
 * 详情主体在 LogDetailBody 中；上游对比面板复用同一个主体，两侧排版保持一致。
 * @param props 日志及弹窗控制参数，具体作用见 DetailsDialogProps。
 * @returns 日志详情弹窗；诊断面板仅在弹窗打开、诊断可查询且具有请求 ID 时挂载。
 */
export function DetailsDialog(props: DetailsDialogProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [showUpstreamComparison, setShowUpstreamComparison] = useState(false)
  const typeConfig = getLogTypeConfig(props.log.type)
  const upstreamQuery = useUpstreamCompareQuery(
    props.log.request_id,
    showUpstreamComparison
  )
  const upstreamItem = showUpstreamComparison
    ? upstreamQuery.data?.items[0]
    : undefined
  // 本站低于上游的数据项（上游值），左栏据此标出警告；高于或等于不标注。
  const lowerThanUpstream = useMemo(
    () =>
      upstreamItem
        ? findLowerMetrics(
            collectLogMetrics(props.log),
            collectLogMetrics(upstreamItemToUsageLog(upstreamItem))
          )
        : undefined,
    [props.log, upstreamItem]
  )

  // 「查同类」：带着这一行可观察的特征跳到导出中心。
  //
  // 这里只读取字段值（用量来源、流结束原因、重试链长度），它们与导出的筛选项
  // 一一对应；异常「判定规则」只有后端一份，前端不复制。
  // 刻意不带用户名与令牌：要回答的是「这个异常影响了多少人」，
  // 预填用户名会把视野锁死在最先被发现的那一个人身上。
  const findSimilar = useMemo(() => {
    const other = parseLogOther(props.log.other)
    const usageSource = other?.stream_result?.usage_source
    const endReason =
      other?.stream_status?.status === 'error'
        ? other?.stream_status?.end_reason
        : undefined
    const retries = other?.admin_info?.use_channel?.length ?? 0

    const search: Record<string, string | number> = {}
    if (usageSource && usageSource !== 'upstream') {
      search.anomalyUsageSource = usageSource
    }
    if (endReason) search.anomalyEndReason = endReason
    if (retries > 1) search.anomalyMinRetry = 2
    if (!Object.keys(search).length) return undefined

    // 时间窗口取该行前后各 3 天，夹在后端的跨度上限内。
    const created = props.log.created_at * 1000
    const threeDays = 3 * 24 * 3600 * 1000
    return () => {
      void navigate({
        to: '/usage-logs/$section',
        params: { section: 'export' },
        search: {
          ...search,
          model: props.log.model_name || undefined,
          startTime: created - threeDays,
          endTime: created + threeDays,
        },
      })
    }
  }, [props.log, navigate])

  let dialogWidthClassName = 'sm:max-w-lg'
  if (isTieredBillingLog(props.log.type, parseLogOther(props.log.other))) {
    dialogWidthClassName = 'sm:max-w-4xl lg:max-w-5xl'
  }
  if (showUpstreamComparison) {
    dialogWidthClassName = 'sm:max-w-[min(96vw,90rem)]'
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={(open) => {
        if (!open) setShowUpstreamComparison(false)
        props.onOpenChange(open)
      }}
      title={
        <>
          {t('Log Details')}
          <StatusBadge
            label={t(typeConfig.label)}
            variant={typeConfig.color as StatusBadgeProps['variant']}
            size='sm'
            copyable={false}
          />
        </>
      }
      description={t('View the complete details for this log entry')}
      contentClassName={cn(
        'min-w-0 overflow-hidden',
        'max-sm:max-h-[calc(100dvh-1.5rem)] max-sm:w-[calc(100vw-1.5rem)] max-sm:max-w-[calc(100vw-1.5rem)] max-sm:p-4',
        dialogWidthClassName
      )}
      headerClassName='max-sm:gap-1'
      titleClassName='flex items-center gap-2 text-base'
      descriptionClassName='sr-only'
      contentHeight={
        showUpstreamComparison ? 'min(76dvh, 780px)' : 'min(72dvh, 720px)'
      }
      bodyClassName={cn(
        'pr-2 sm:pr-4',
        // 对比时两栏共用一个网格，同类分区在同一行对齐；间距由单元格自身留出，
        // 这样右栏分隔线在行与行之间保持连续。
        showUpstreamComparison && 'grid content-start lg:grid-cols-2'
      )}
    >
      <LogDetailBody
        log={props.log}
        isAdmin={props.isAdmin}
        isRoot={props.isRoot}
        heading={
          showUpstreamComparison ? (
            <h3 className='border-b pb-2 text-sm font-semibold'>
              {t('Log Details')}
            </h3>
          ) : undefined
        }
        onQueryUpstream={
          showUpstreamComparison
            ? undefined
            : () => setShowUpstreamComparison(true)
        }
        onFindSimilar={findSimilar}
        showStreamDiagnostic={props.open}
        compareColumn={showUpstreamComparison ? 1 : undefined}
        lowerThanUpstream={lowerThanUpstream}
      />

      {showUpstreamComparison && <UpstreamComparePane query={upstreamQuery} />}
    </Dialog>
  )
}

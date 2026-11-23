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
import { ArrowLeft, ChevronDown, Search, SearchCheck } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { StatusBadge, type StatusBadgeProps } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Spinner } from '@/components/ui/spinner'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { cn } from '@/lib/utils'

import type { UsageLog } from '../../data/schema'
import { isTieredBillingLog, parseLogOther } from '../../lib/format'
import { collectLogMetrics, findLowerMetrics } from '../../lib/log-metrics'
import { upstreamItemToUsageLog } from '../../lib/upstream-log-item'
import { getLogTypeConfig } from '../../lib/utils'
import { LogComparisonTable } from '../../upstream-log/log-comparison-table'
import { UpstreamComparePane } from '../../upstream-log/upstream-compare-pane'
import { useUpstreamCompareQuery } from '../../upstream-log/use-upstream-compare-query'
import { LogDetailBody } from './log-detail-body'
import type { LogDetailSection } from './log-detail-grid'
import { hasLogDetailSections } from './log-detail-sections'

interface DetailsDialogProps {
  log: UsageLog // 当前日志，含请求 ID、Unix 秒创建时间和后端已过滤的扩展字段。
  isAdmin: boolean // 控制既有管理员费用字段展示，原始诊断权限由面板与后端独立判断。
  canViewUpstreamExchange?: boolean
  canViewPolicyDecisions?: boolean // 员工客户日志可查看策略记录，独立于管理员操作权限。
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
  return (
    <LogDetailsSession
      key={[
        props.log.id,
        props.log.request_id,
        props.log.created_at,
        props.isAdmin,
        props.isRoot,
        props.canViewUpstreamExchange,
        props.canViewPolicyDecisions,
      ].join(':')}
      {...props}
    />
  )
}

function LogDetailsSession(props: DetailsDialogProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [showUpstreamComparison, setShowUpstreamComparison] = useState(false)
  const typeConfig = getLogTypeConfig(props.log.type)
  const [section, setSection] = useState<LogDetailSection>('billing')
  useEffect(() => {
    if (!props.open) {
      setSection('billing')
      setShowUpstreamComparison(false)
    }
  }, [props.open])
  const showSections = hasLogDetailSections(props.log, props)
  const upstreamQuery = useUpstreamCompareQuery(
    props.log.request_id,
    props.open && props.isAdmin && showUpstreamComparison
  )
  const upstreamItem = showUpstreamComparison
    ? upstreamQuery.data?.items[0]
    : undefined
  const upstreamLog = useMemo(
    () => (upstreamItem ? upstreamItemToUsageLog(upstreamItem) : undefined),
    [upstreamItem]
  )
  // 本站低于上游的数据项（上游值），左栏据此标出警告；高于或等于不标注。
  const lowerThanUpstream = useMemo(
    () =>
      upstreamLog
        ? findLowerMetrics(
            collectLogMetrics(props.log),
            collectLogMetrics(upstreamLog)
          )
        : undefined,
    [props.log, upstreamLog]
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

  let dialogWidthClassName = 'sm:max-w-3xl'
  if (isTieredBillingLog(props.log.type, parseLogOther(props.log.other))) {
    dialogWidthClassName = 'sm:max-w-4xl lg:max-w-5xl'
  }
  if (showUpstreamComparison) {
    dialogWidthClassName = 'sm:max-w-[min(96vw,80rem)]'
  }

  const detailContent = (
    <div
      className={cn(
        'min-w-0',
        showUpstreamComparison && 'grid content-start lg:grid-cols-2'
      )}
    >
      <LogDetailBody
        log={props.log}
        showOverview={!upstreamLog || section !== 'diagnostics'}
        section={showSections ? section : undefined}
        canViewUpstreamExchange={props.canViewUpstreamExchange}
        isAdmin={props.isAdmin}
        canViewPolicyDecisions={props.canViewPolicyDecisions}
        isRoot={props.isRoot}
        heading={
          showUpstreamComparison ? (
            <h3 className='border-b pb-2 text-sm font-semibold'>
              {t('Log Details')}
            </h3>
          ) : undefined
        }
        showStreamDiagnostic={
          props.open && (!showSections || section === 'diagnostics')
        }
        compareColumn={showUpstreamComparison ? 1 : undefined}
        lowerThanUpstream={lowerThanUpstream}
      />

      {showUpstreamComparison && (
        <UpstreamComparePane
          query={upstreamQuery}
          section={showSections ? section : undefined}
          showOverview={!upstreamLog || section !== 'diagnostics'}
        />
      )}
    </div>
  )

  const comparisonContent = upstreamLog ? (
    <div className='space-y-4'>
      {upstreamQuery.data?.source?.key_index_from_log === false && (
        <StatusBadge
          label={t('Channel key not recorded in the local log')}
          variant='amber'
          size='sm'
        />
      )}
      {upstreamQuery.data && upstreamQuery.data.total > 1 && (
        <p className='text-muted-foreground text-sm'>
          {t(
            'The upstream returned {{count}} matching logs; the first one is shown.',
            { count: upstreamQuery.data.total }
          )}
        </p>
      )}
      <LogComparisonTable
        local={props.log}
        upstream={upstreamLog}
        section={showSections ? section : undefined}
      />
      {section === 'diagnostics' ? (
        detailContent
      ) : (
        <Collapsible key={props.log.request_id}>
          <CollapsibleTrigger
            render={<Button type='button' variant='ghost' />}
            className='group mb-2'
          >
            {t('Details')}
            <ChevronDown
              className='group-data-[panel-open]:rotate-180'
              aria-hidden='true'
            />
          </CollapsibleTrigger>
          <CollapsibleContent>{detailContent}</CollapsibleContent>
        </Collapsible>
      )}
    </div>
  ) : (
    detailContent
  )

  return (
    <Tabs
      value={section}
      onValueChange={(value) => setSection(value as LogDetailSection)}
      className='contents'
    >
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
        navigation={
          <div className='flex flex-wrap items-center justify-between gap-3'>
            {showSections && (
              <TabsList className='h-9 w-full sm:w-auto'>
                <TabsTrigger value='billing'>
                  {t('Usage & billing')}
                </TabsTrigger>
                <TabsTrigger value='diagnostics'>
                  {t('Request diagnostics')}
                </TabsTrigger>
              </TabsList>
            )}
            {props.isAdmin &&
              (findSimilar ||
                (props.log.request_id && props.log.upstream_request_id)) && (
                <div className='ml-auto flex flex-wrap items-center justify-end gap-2'>
                  {showUpstreamComparison && (
                    <Button
                      type='button'
                      variant='outline'
                      size='lg'
                      onClick={() => setShowUpstreamComparison(false)}
                    >
                      <ArrowLeft aria-hidden='true' />
                      {t('Close comparison')}
                    </Button>
                  )}
                  {!showUpstreamComparison &&
                    props.log.request_id &&
                    props.log.upstream_request_id && (
                      <Button
                        type='button'
                        variant='outline'
                        size='lg'
                        onClick={() => setShowUpstreamComparison(true)}
                      >
                        <Search aria-hidden='true' />
                        {t('Query Upstream')}
                      </Button>
                    )}
                  {showUpstreamComparison && upstreamQuery.isFetching && (
                    <Spinner aria-label={t('Loading...')} />
                  )}
                  {findSimilar && (
                    <Button
                      type='button'
                      variant='outline'
                      size='lg'
                      onClick={findSimilar}
                    >
                      <SearchCheck aria-hidden='true' />
                      {t('Find similar')}
                    </Button>
                  )}
                </div>
              )}
          </div>
        }
        contentClassName={cn(
          'min-w-0 overflow-hidden',
          'max-sm:max-h-[calc(100dvh-1.5rem)] max-sm:w-[calc(100vw-1.5rem)] max-sm:max-w-[calc(100vw-1.5rem)] max-sm:p-4',
          dialogWidthClassName
        )}
        headerClassName='max-sm:gap-1'
        titleClassName='flex items-center gap-2 text-base'
        descriptionClassName='sr-only'
        scrollAreaClassName={
          showUpstreamComparison
            ? 'max-h-[min(76dvh,780px)]'
            : 'max-h-[min(72dvh,720px)]'
        }
        bodyClassName={cn(
          'pr-2 sm:pr-4',
          // 对比时两栏共用一个网格，同类分区在同一行对齐；间距由单元格自身留出，
          // 这样右栏分隔线在行与行之间保持连续。
          'min-w-0'
        )}
      >
        {showSections
          ? (['billing', 'diagnostics'] as const).map((value) => (
              <TabsContent key={value} value={value} className='min-w-0'>
                {section === value && comparisonContent}
              </TabsContent>
            ))
          : comparisonContent}
      </Dialog>
    </Tabs>
  )
}

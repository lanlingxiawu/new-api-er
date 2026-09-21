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
import { createFileRoute, redirect } from '@tanstack/react-router'
import z from 'zod'

import { UsageLogs } from '@/features/usage-logs'
import {
  isUsageLogsSectionId,
  USAGE_LOGS_DEFAULT_SECTION,
} from '@/features/usage-logs/section-registry'

const logTypeValues = ['0', '1', '2', '3', '4', '5', '6', '7'] as const
const logTypeSearchSchema = z
  .preprocess((value) => {
    if (value == null || value === '') return undefined
    return Array.isArray(value) ? value : [value]
  }, z.array(z.enum(logTypeValues)).optional())
  .catch([])

const usageLogsSearchSchema = z.object({
  page: z.number().optional().catch(1),
  pageSize: z.number().optional().catch(undefined),
  type: logTypeSearchSchema.optional(),
  filter: z.string().optional().catch(''),
  model: z.string().optional().catch(''),
  token: z.string().optional().catch(''),
  channel: z.string().optional().catch(''),
  group: z.string().optional().catch(''),
  username: z.string().optional().catch(''),
  customerUserId: z.string().optional().catch(''),
  requestId: z.string().optional().catch(''),
  upstreamRequestId: z.string().optional().catch(''),
  startTime: z.number().optional(),
  endTime: z.number().optional(),
  // 「查同类」带过来的特征。刻意只传该行**直接可观察的字段值**，
  // 它们与导出筛选项一一对应，等同于「按这一行的用量来源筛选」，
  // 不包含任何异常判定规则——规则只有后端 logAnomalyFlags 一份，
  // 前端再写一遍必然漂移。
  anomalyUsageSource: z.string().optional().catch(''),
  anomalyEndReason: z.string().optional().catch(''),
  anomalyMinRetry: z.number().int().positive().optional(),
  localSection: z
    .enum(['common', 'drawing', 'task', 'export'])
    .optional()
    .catch('common'),
  upstreamPage: z.number().optional().catch(1),
  upstreamPageSize: z.number().optional().catch(undefined),
  upstreamType: logTypeSearchSchema.optional(),
  upstreamModel: z.string().optional().catch(''),
  upstreamToken: z.string().optional().catch(''),
  upstreamChannel: z.string().optional().catch(''),
  upstreamGroup: z.string().optional().catch(''),
  upstreamUsername: z.string().optional().catch(''),
  upstreamLocalRequestId: z.string().optional().catch(''),
  upstreamFilterRequestId: z.string().optional().catch(''),
  upstreamStartTime: z.number().optional(),
  upstreamEndTime: z.number().optional(),
  upstreamFilterKeyIndex: z.number().int().nonnegative().optional(),
})

export const Route = createFileRoute('/_authenticated/usage-logs/$section')({
  beforeLoad: ({ params, search }) => {
    if (!isUsageLogsSectionId(params.section)) {
      throw redirect({
        to: '/usage-logs/$section',
        params: { section: USAGE_LOGS_DEFAULT_SECTION },
      })
    }
    // type 仅 common 使用，非 common 时清掉 URL 里的 type
    const hasTypeSearch = Array.isArray(search?.type)
      ? search.type.length > 0
      : search?.type != null && search.type !== ''
    if (
      params.section !== 'common' &&
      params.section !== 'upstream' &&
      hasTypeSearch
    ) {
      throw redirect({
        to: '/usage-logs/$section',
        params: { section: params.section },
        search: { ...search, type: undefined },
        replace: true,
      })
    }
  },
  validateSearch: usageLogsSearchSchema,
  component: UsageLogs,
})

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
import type { CSSProperties } from 'react'

import { cn } from '@/lib/utils'

/**
 * 本站/上游日志对比时两栏共用一个网格：同一类分区放在同一网格行，左右起点对齐，
 * 某一侧没有的分区留空。行号按详情从上到下的分区顺序固定，两栏必须使用同一张表。
 */
export const LOG_DETAIL_ROW = {
  heading: 1,
  overview: 2,
  conversion: 3,
  quotaSaturation: 4,
  rejectReason: 5,
  violation: 6,
  refund: 7,
  taskPlugin: 8,
  rootDiagnostics: 9,
  topupAudit: 10,
  quotaOperation: 11,
  manageOperator: 12,
  manageTarget: 13,
  manageAudit: 14,
  loginAudit: 15,
  audioTokens: 16,
  reasoningEffort: 17,
  systemPrompt: 18,
  modelMapping: 19,
  tokens: 20,
  billing: 21,
  dynamicPricing: 22,
  billingPath: 23,
  streamStatus: 24,
  streamBilling: 25,
  streamDiagnostic: 26,
  subscription: 27,
  paramOverride: 28,
  content: 29,
  footer: 30,
} as const

export type LogDetailRow = keyof typeof LOG_DETAIL_ROW
export type LogDetailCompareColumn = 1 | 2

/** 对比网格中一个单元格的类名：宽屏按行列定位，窄屏按文档顺序上下堆叠。 */
export function compareCellClassName(column: LogDetailCompareColumn): string {
  return cn(
    'min-w-0 pb-2.5 sm:pb-3 lg:[grid-row:var(--log-detail-row)]',
    column === 1
      ? 'lg:col-start-1 lg:pr-4'
      : 'lg:col-start-2 lg:border-l lg:pl-4'
  )
}

/** 单元格所在网格行；span 用于上游加载/错误态占据整列，而不撑高左侧某一行。 */
export function compareCellStyle(
  row: LogDetailRow,
  span?: number
): CSSProperties {
  const start = LOG_DETAIL_ROW[row]
  return {
    '--log-detail-row': span ? `${start} / span ${span}` : String(start),
  } as CSSProperties
}

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
import type { UsageLog } from '../data/schema'
import { getTieredBillingSummary, parseLogOther } from './format'
import { isPerCallBilling } from './utils'

/**
 * 本站与上游日志对比时可比较的数据项。键与详情里的行一一对应：Token 明细的 token 数、
 * 计费详情的单价（分组倍率前的基础价，$/M；按次计费为单次价格）、生效分组倍率、总费用。
 * 响应时间等「越低越好」的项不在此列。
 */
export type LogMetricKey =
  | 'tokens.input'
  | 'tokens.output'
  | 'tokens.cache_read'
  | 'tokens.cache_write'
  | 'tokens.cache_write_5m'
  | 'tokens.cache_write_1h'
  | 'price.input'
  | 'price.output'
  | 'price.cache_read'
  | 'price.cache_write'
  | 'price.cache_write_5m'
  | 'price.cache_write_1h'
  | 'price.per_call'
  | 'group_ratio'
  | 'total_cost'

export type LogMetrics = Partial<Record<LogMetricKey, number>>

// 动态计费命中档位的价格字段 -> 与按倍率计费相同的数据项键，两种计费模式之间也能对比。
export const TIERED_PRICE_METRIC: Record<string, LogMetricKey> = {
  inputPrice: 'price.input',
  outputPrice: 'price.output',
  cacheReadPrice: 'price.cache_read',
  cacheCreatePrice: 'price.cache_write',
  cacheCreate1hPrice: 'price.cache_write_1h',
}

function setFinite(metrics: LogMetrics, key: LogMetricKey, value: unknown) {
  if (typeof value === 'number' && Number.isFinite(value)) metrics[key] = value
}

/**
 * 提取一条日志的可比较数据项，口径与详情展示一致。
 * 不按「该行是否展示」过滤：警告只挂在实际渲染的行上，多提取的项不会凭空出现在界面里。
 */
export function collectLogMetrics(log: UsageLog): LogMetrics {
  const metrics: LogMetrics = {}
  setFinite(metrics, 'tokens.input', log.prompt_tokens)
  setFinite(metrics, 'tokens.output', log.completion_tokens)
  setFinite(metrics, 'total_cost', log.quota)

  const other = parseLogOther(log.other)
  if (!other) return metrics

  setFinite(metrics, 'tokens.cache_read', other.cache_tokens)
  setFinite(metrics, 'tokens.cache_write', other.cache_creation_tokens)
  setFinite(metrics, 'tokens.cache_write_5m', other.cache_creation_tokens_5m)
  setFinite(metrics, 'tokens.cache_write_1h', other.cache_creation_tokens_1h)

  const userGR = other.user_group_ratio
  const isUserGR = userGR != null && Number.isFinite(userGR) && userGR !== -1
  setFinite(metrics, 'group_ratio', isUserGR ? userGR : other.group_ratio)

  if (other.billing_mode === 'tiered_expr') {
    for (const entry of getTieredBillingSummary(other)?.priceEntries ?? []) {
      const key = TIERED_PRICE_METRIC[entry.field]
      if (key) setFinite(metrics, key, entry.price)
    }
    return metrics
  }
  if (isPerCallBilling(other.model_price)) {
    setFinite(metrics, 'price.per_call', other.model_price)
    return metrics
  }
  if (other.model_ratio == null) return metrics
  // 缺少的倍率相乘得到 NaN，由 setFinite 跳过。
  const base = other.model_ratio * 2
  const ratioOf = (ratio: number | undefined) => base * (ratio ?? Number.NaN)
  setFinite(metrics, 'price.input', base)
  setFinite(metrics, 'price.output', ratioOf(other.completion_ratio))
  setFinite(metrics, 'price.cache_read', ratioOf(other.cache_ratio))
  setFinite(metrics, 'price.cache_write', ratioOf(other.cache_creation_ratio))
  setFinite(
    metrics,
    'price.cache_write_5m',
    ratioOf(other.cache_creation_ratio_5m)
  )
  setFinite(
    metrics,
    'price.cache_write_1h',
    ratioOf(other.cache_creation_ratio_1h)
  )
  return metrics
}

/**
 * 找出本站低于上游的数据项，返回这些项的上游值（用于提示）。高于或等于不标注；
 * 只有一侧有的项无从比较。浮点计算的舍入误差不算「低于」。
 */
export function findLowerMetrics(
  local: LogMetrics,
  upstream: LogMetrics
): LogMetrics {
  const lower: LogMetrics = {}
  for (const key of Object.keys(local) as LogMetricKey[]) {
    const l = local[key]
    const u = upstream[key]
    if (l == null || u == null) continue
    const epsilon = 1e-9 * Math.max(1, Math.abs(l), Math.abs(u))
    if (u - l > epsilon) lower[key] = u
  }
  return lower
}

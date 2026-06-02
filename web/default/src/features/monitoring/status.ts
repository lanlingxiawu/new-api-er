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
import type { StatusVariant } from '@/components/status-badge'
import type { GroupStatusItem, ModelStatusItem, MonitorStatus } from './api'

export type AnyMonitorStatusItem = GroupStatusItem | ModelStatusItem

export const MONITOR_STATUS_LABELS: Record<MonitorStatus, string> = {
  1: 'Healthy',
  2: 'Warning',
  3: 'Unhealthy',
  4: 'Unavailable',
}

export const MONITOR_STATUS_VARIANTS: Record<MonitorStatus, StatusVariant> = {
  1: 'success',
  2: 'warning',
  3: 'danger',
  4: 'danger',
}

export function getMonitorStatusLabel(status?: number | null): string {
  if (status === 1 || status === 2 || status === 3 || status === 4) {
    return MONITOR_STATUS_LABELS[status]
  }
  return 'No data'
}

export function getMonitorStatusVariant(status?: number | null): StatusVariant {
  if (status === 1 || status === 2 || status === 3 || status === 4) {
    return MONITOR_STATUS_VARIANTS[status]
  }
  return 'neutral'
}

export function getStatusTargetName(item: AnyMonitorStatusItem): string {
  return 'model_name' in item ? item.model_name : item.user_group
}

export function isMonitorStatusUnavailable(
  item?: Pick<AnyMonitorStatusItem, 'status'> | null
): boolean {
  return item?.status === 4
}

export function buildModelStatusMap(items: ModelStatusItem[] | undefined) {
  const map = new Map<string, ModelStatusItem>()
  for (const item of items ?? []) {
    map.set(item.model_name, item)
  }
  return map
}

export function buildGroupStatusMap(items: GroupStatusItem[] | undefined) {
  const map = new Map<string, GroupStatusItem>()
  for (const item of items ?? []) {
    map.set(item.user_group, item)
  }
  return map
}

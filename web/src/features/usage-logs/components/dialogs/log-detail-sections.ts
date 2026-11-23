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
import type { UsageLog } from '../../data/schema'
import { parseLogOther } from '../../lib/format'

export function hasLogDetailSections(
  log: UsageLog,
  access: {
    isAdmin: boolean
    isRoot?: boolean
    canViewUpstreamExchange?: boolean
    canViewPolicyDecisions?: boolean
  }
): boolean {
  if (log.type !== 2 && log.type !== 6) return false
  if (
    !access.isAdmin &&
    !access.isRoot &&
    !access.canViewUpstreamExchange &&
    !access.canViewPolicyDecisions
  ) {
    return false
  }
  const other = parseLogOther(log.other)
  if (!other) return false
  const exchanges = other.admin_info?.upstream_exchange
  return Boolean(
    ((access.canViewUpstreamExchange ?? access.isAdmin) &&
      Array.isArray(exchanges) &&
      exchanges.some((attempt) => attempt && typeof attempt === 'object')) ||
    ((access.canViewPolicyDecisions ?? access.isAdmin) &&
      other.admin_info?.request_policy?.length) ||
    (access.isAdmin &&
      ((log.type !== 6 &&
        (other.request_path || other.request_conversion?.some(Boolean))) ||
        other.admin_info?.reject_reason ||
        other.reject_reason ||
        other.admin_info?.task_plugin ||
        other.admin_info?.user_channel_routing)) ||
    (access.isRoot &&
      (other.root_info?.task_plugin ||
        other.root_info?.upstream_task_id ||
        other.root_info?.node_name ||
        (other.stream_diagnostic_available === true && log.request_id))) ||
    (other.stream_status && other.stream_status.status !== 'ok') ||
    other.response_model ||
    other.po?.length ||
    other.reasoning_effort ||
    other.is_system_prompt_overwritten ||
    (other.is_model_mapped && other.upstream_model_name)
  )
}

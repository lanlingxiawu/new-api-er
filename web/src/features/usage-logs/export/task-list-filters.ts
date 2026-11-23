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
import type { ExportJobListFilters } from './types'

/**
 * Quota jobs are always administrator detail rows. Source, mode and customer
 * belong to usage exports and would hide every quota job.
 */
export function quotaTaskListFilters(
  filters: ExportJobListFilters
): ExportJobListFilters {
  const next: ExportJobListFilters = {}
  if (filters.scope) next.scope = filters.scope
  if (filters.creator_id) next.creator_id = filters.creator_id
  if (filters.status) next.status = filters.status
  if (filters.created_after) next.created_after = filters.created_after
  if (filters.created_before) next.created_before = filters.created_before
  if (filters.job_id) next.job_id = filters.job_id
  return next
}

/** Keep usage-only keys in the shared URL while the quota page edits the rest. */
export function mergeQuotaTaskListFilters(
  current: ExportJobListFilters,
  applied: ExportJobListFilters
): ExportJobListFilters {
  const next = quotaTaskListFilters(applied)
  if (current.source) next.source = current.source
  if (current.mode) next.mode = current.mode
  if (current.customer_id) next.customer_id = current.customer_id
  return next
}

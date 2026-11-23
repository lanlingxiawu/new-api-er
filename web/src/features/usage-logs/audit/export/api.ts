import { api } from '@/lib/api'

import type { ExportOptions } from '../../export/types'
import { publicAuditFilters, type AuditFilters } from '../api'

export type AuditExportFilters = Omit<
  AuditFilters,
  'p' | 'page_size' | 'success'
> & { success?: boolean }

export function auditExportSnapshot(
  params: AuditFilters,
  scope: 'all' | 'self'
): AuditExportFilters {
  if (scope === 'self' && params.ip_risk) {
    throw new Error('Risk export requires security export permission.')
  }
  if (scope === 'self') params = publicAuditFilters(params)
  return {
    start_timestamp: params.start_timestamp,
    end_timestamp: params.end_timestamp,
    category: params.category,
    success:
      params.success === undefined ? undefined : params.success === 'true',
    username: scope === 'all' ? params.username : undefined,
    request_id: params.request_id,
    token_ref: params.token_ref,
    exclude_token_ref: params.exclude_token_ref,
    ip: params.ip,
    actor_type: params.actor_type,
    auth_method: params.auth_method,
    route: params.route,
    route_missing: params.route_missing,
    ip_risk: params.ip_risk,
  }
}

export function auditExportBase(scope: 'all' | 'self') {
  return scope === 'self' ? '/api/audit/self/export' : '/api/audit/export'
}

export async function createAuditExport(
  scope: 'all' | 'self',
  filters: AuditExportFilters,
  options: ExportOptions
): Promise<string> {
  const { data } = await api.post(`${auditExportBase(scope)}/jobs`, {
    filters,
    options,
  })
  if (!data.success) throw new Error(data.message)
  return data.data.job_id
}

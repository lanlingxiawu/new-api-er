import { api } from '@/lib/api'

import type {
  ExportColumn,
  ExportFormat,
  ExportMode,
  SummaryDimension,
} from './types'

export const EMPLOYEE_EXPORT_BASE = '/api/user/employee/export'
export const EMPLOYEE_TEMPLATE_BASE = '/api/admin/employee-export/templates'

export interface EmployeeExportTemplate {
  id: number
  /** What employees submit: a builtin ID ("builtin:...") or the custom row ID. */
  key: string
  /** Builtin templates reuse the admin reconciliation templates and are read-only. */
  builtin: boolean
  name: string
  enabled: boolean
  version: number
  columns: string[]
  format: ExportFormat
  mode: ExportMode
  summary_dims: SummaryDimension[]
  options: { csv_bom: boolean; header: boolean; timezone: string }
  allowed_filters: string[]
}

export interface EmployeeExportCapabilities {
  enabled: boolean
  templates: EmployeeExportTemplate[]
  columns: { key: string; label: string }[]
  max_range_sec: number
  max_customers: number
}

export interface EmployeeExportCatalog {
  columns: ExportColumn[]
  default_columns: string[]
  max_columns: number
}

export function getEmployeeExportColumns() {
  return exportRequest<EmployeeExportCatalog>(
    'get',
    '/api/admin/employee-export/columns'
  )
}

/**
 * Without `quiet` the HTTP client already toasts failures, so callers must not toast again.
 * With `quiet` nothing is shown and the thrown Error carries the server's localized message.
 */
export async function exportRequest<T>(
  method: 'get' | 'post' | 'put' | 'delete',
  url: string,
  data?: unknown,
  options?: { quiet?: boolean }
): Promise<T> {
  let response
  try {
    response = await api.request<{
      success: boolean
      message: string
      data: T
    }>({
      method,
      url,
      data,
      skipErrorHandler: options?.quiet,
      skipBusinessError: options?.quiet,
    })
  } catch (error) {
    const message = (error as { response?: { data?: { message?: string } } })
      ?.response?.data?.message
    throw message ? new Error(message) : error
  }
  if (!response.data.success) throw new Error(response.data.message)
  return response.data.data
}

export async function getEmployeeTemplates(): Promise<{
  builtin: EmployeeExportTemplate[]
  custom: EmployeeExportTemplate[]
}> {
  const custom: EmployeeExportTemplate[] = []
  let builtin: EmployeeExportTemplate[] = []
  let cursor = 0
  do {
    const page = await exportRequest<{
      items: EmployeeExportTemplate[]
      builtin: EmployeeExportTemplate[]
      next_cursor: number
    }>('get', `${EMPLOYEE_TEMPLATE_BASE}?cursor=${cursor}`)
    custom.push(...page.items)
    builtin = page.builtin
    cursor = page.next_cursor
  } while (cursor > 0)
  return { builtin, custom }
}

/** Builtin names are English source strings; custom names are shown as typed by the administrator. */
export function employeeTemplateName(
  template: { name: string; builtin?: boolean; key?: string },
  t: (key: string) => string
) {
  const builtin = template.builtin ?? template.key?.startsWith('builtin:')
  return builtin ? t(template.name) : template.name
}

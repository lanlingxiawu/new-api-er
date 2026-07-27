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
import { api } from '@/lib/api'

import type {
  ExportColumnCatalog,
  ExportJob,
  ExportTemplate,
  CreateExportJobRequest,
  SaveExportTemplateRequest,
} from './types'

interface Envelope<T> {
  success: boolean
  message: string
  data: T
}

const BASE = '/api/log/export'

export async function getExportColumns(): Promise<ExportColumnCatalog> {
  const res = await api.get(`${BASE}/columns`)
  const body = res.data as Envelope<ExportColumnCatalog>
  if (!body.success) throw new Error(body.message)
  return body.data
}

export async function getExportJobs(
  all = false
): Promise<{ jobs: ExportJob[]; enabled: boolean }> {
  const res = await api.get(`${BASE}/jobs${all ? '?all=true' : ''}`)
  const body = res.data as Envelope<{ jobs: ExportJob[] | null; enabled: boolean }>
  if (!body.success) throw new Error(body.message)
  return { jobs: body.data?.jobs ?? [], enabled: body.data?.enabled ?? true }
}

export async function createExportJob(
  payload: CreateExportJobRequest
): Promise<string> {
  const res = await api.post(`${BASE}/jobs`, payload)
  const body = res.data as Envelope<{ job_id: string }>
  if (!body.success) throw new Error(body.message)
  return body.data.job_id
}

/** Cancels a running job, or deletes a finished one along with its part files. */
export async function deleteExportJob(jobId: string): Promise<void> {
  const res = await api.delete(`${BASE}/jobs/${jobId}`)
  const body = res.data as Envelope<unknown>
  if (!body.success) throw new Error(body.message)
}

/**
 * Downloads one part (or the whole archive when `part` is omitted).
 *
 * The backend issues a short-lived token so the browser can fetch the file
 * directly without auth headers; navigating to the URL keeps the browser's
 * native download UI (progress, pause/resume) instead of buffering the whole
 * file in memory like a blob download would.
 */
export async function downloadExportPart(
  jobId: string,
  part?: number
): Promise<void> {
  const query = part ? `?part=${part}` : '?part=all'
  const res = await api.get(`${BASE}/jobs/${jobId}/download-url${query}`)
  const body = res.data as Envelope<{ url: string }>
  if (!body.success) throw new Error(body.message)

  const link = document.createElement('a')
  link.href = body.data.url
  link.rel = 'noopener'
  document.body.appendChild(link)
  link.click()
  link.remove()
}

/**
 * Bounded row estimate for the current filters.
 *
 * Deliberately NOT the log list endpoint: that one runs a full COUNT over the
 * matched set on every call, and this estimate re-runs whenever the admin edits
 * a filter. The dedicated endpoint stops counting at a cap, so its cost does not
 * grow with the log table.
 */
export async function getExportEstimate(params: {
  start_timestamp: number
  end_timestamp: number
  type?: number
  model_name?: string
  username?: string
  token_name?: string
  channel?: number
  group?: string
}): Promise<{ rows: number; capped: boolean; available: boolean }> {
  const query = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === '') continue
    query.set(key, String(value))
  }
  const res = await api.get(`${BASE}/estimate?${query.toString()}`, {
    // 估算失败只影响提示，不该弹错误提示打断用户填表。
    skipErrorHandler: true,
    skipBusinessError: true,
  })
  const body = res.data as Envelope<{
    rows: number
    capped: boolean
    available: boolean
  }>
  if (!body.success) return { rows: 0, capped: false, available: false }
  return body.data
}

export async function getExportTemplates(): Promise<ExportTemplate[]> {
  const res = await api.get(`${BASE}/templates`)
  const body = res.data as Envelope<ExportTemplate[] | null>
  if (!body.success) throw new Error(body.message)
  return body.data ?? []
}

export async function createExportTemplate(
  payload: SaveExportTemplateRequest
): Promise<ExportTemplate> {
  const res = await api.post(`${BASE}/templates`, payload)
  const body = res.data as Envelope<ExportTemplate>
  if (!body.success) throw new Error(body.message)
  return body.data
}

export async function deleteExportTemplate(id: number): Promise<void> {
  const res = await api.delete(`${BASE}/templates/${id}`)
  const body = res.data as Envelope<unknown>
  if (!body.success) throw new Error(body.message)
}

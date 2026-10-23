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
import { requireServerSuccess } from '@/lib/server-error-message'

export type CleanupKind = 'ledger' | 'commission' | 'error_logs'
export type CleanupRun = {
  id: string
  kind: CleanupKind
  before: number
  matched: number
  count_exact: boolean
  deleted: number
  scanned: number
  status: 'preview' | 'pending' | 'running' | 'succeeded' | 'failed'
  error_key: string
}
type CleanupResponse = { success: boolean; message: string; data: CleanupRun }

export async function previewDataCleanup(kind: CleanupKind, before: number) {
  const response = await api.post<CleanupResponse>(
    '/api/data-cleanup/preview',
    { kind, before }
  )
  return requireServerSuccess(response.data).data
}
export async function startDataCleanup(previewId: string) {
  const response = await api.post<CleanupResponse>('/api/data-cleanup', {
    preview_id: previewId,
  })
  return requireServerSuccess(response.data).data
}
export async function getDataCleanup(kind: CleanupKind, id?: string) {
  const response = await api.get<
    Omit<CleanupResponse, 'data'> & { data: CleanupRun | null }
  >('/api/data-cleanup', { params: { kind, id }, disableDuplicate: true })
  return requireServerSuccess(response.data).data
}

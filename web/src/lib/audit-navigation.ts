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
import { z } from 'zod'

export type AuditScope =
  | 'all'
  | 'self'
  | 'customers'
  | 'security'
  | 'my-security'
export type AuditRiskView = 'id' | 'unresolved' | 'handled'
export type SecurityView =
  | AuditRiskView
  | 'recent_hits'
  | 'activity'
  | 'archived'

export interface AuditSearch {
  scope?: AuditScope
  tab?: 'audit' | 'risk'
  view?: SecurityView
}

export const auditSearchSchema = z.object({
  scope: z
    .enum(['all', 'self', 'customers', 'security', 'my-security'])
    .optional()
    .catch(undefined),
  tab: z.enum(['audit', 'risk']).optional().catch(undefined),
  view: z
    .enum([
      'id',
      'unresolved',
      'handled',
      'recent_hits',
      'activity',
      'archived',
    ])
    .optional()
    .catch(undefined),
})

export function parseAuditSearch(search: Record<string, unknown>): AuditSearch {
  return auditSearchSchema.parse(search)
}

declare module '@tanstack/react-router' {
  interface HistoryState {
    auditPendingVisit?: string
  }
}

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
import { createFileRoute, useLocation } from '@tanstack/react-router'

import { AuditLogs } from '@/features/usage-logs/audit'
import { parseAuditSearch } from '@/lib/audit-navigation'

export const Route = createFileRoute('/_authenticated/usage-logs/audit')({
  validateSearch: parseAuditSearch,
  component: AuditRoute,
})

function AuditRoute() {
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  const pendingVisit = useLocation({
    select: (location) => location.state.auditPendingVisit,
  })
  return (
    <AuditLogs
      search={search}
      pendingVisit={pendingVisit}
      onSearchChange={(next) => void navigate({ search: next, state: true })}
    />
  )
}

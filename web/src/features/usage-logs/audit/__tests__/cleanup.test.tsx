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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { AuditLogViewer } from '../components/audit-log-viewer'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

it.each([
  { scope: 'all' as const, accessOnly: false, edit: true },
  { scope: 'all' as const, accessOnly: false, edit: false },
  { scope: 'all' as const, accessOnly: true, edit: true },
  { scope: 'self' as const, accessOnly: false, edit: true },
])(
  'keeps audit cleanup in log maintenance instead of the audit viewer: %j',
  ({ scope, accessOnly, edit }) => {
    useAuthStore.getState().auth.setUser({
      id: 19,
      username: 'maintainer',
      role: 10,
      permissions: {
        admin_permissions: {
          audit: { read: true },
          'system_settings.operations.logs': { view: true, edit },
        },
      },
    })
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const { unmount } = render(
      <QueryClientProvider client={client}>
        <AuditLogViewer scope={scope} accessOnly={accessOnly} />
      </QueryClientProvider>
    )
    expect(
      screen.queryByRole('button', { name: 'Clean audit logs' })
    ).not.toBeInTheDocument()
    unmount()
    client.clear()
  }
)

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, expect, test, vi } from 'vitest'

import { ROLE } from '@/lib/roles'
import { STATUS_QUERY_KEY } from '@/lib/status-query'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'

import { useSidebarView } from '../use-sidebar-view'

vi.mock('@tanstack/react-router', () => ({
  useLocation: () => '/request-logs',
}))

afterEach(() => {
  useAuthStore.setState(useAuthStore.getInitialState(), true)
  window.localStorage.clear()
})

function sidebarFor(user: AuthUser) {
  useAuthStore.getState().auth.setUser(user)
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  // Legacy presentation flags must not override settings scope grants.
  client.setQueryData(STATUS_QUERY_KEY, {
    SidebarModulesAdmin: JSON.stringify({
      admin: { enabled: false, setting: false },
    }),
  })
  function Wrapper(props: { children: ReactNode }) {
    return (
      <QueryClientProvider client={client}>
        {props.children}
      </QueryClientProvider>
    )
  }
  return renderHook(useSidebarView, { wrapper: Wrapper })
}

test.each([
  ['site.notice', '/system-settings/site/notice'],
  ['billing.model-pricing', '/system-settings/billing/model-pricing'],
  ['models.channel-affinity', '/system-settings/request-policies/routing'],
])(
  'admins granted %s see a settings link to their first allowed section',
  (scope, url) => {
    const { result } = sidebarFor({
      id: 1,
      username: 'admin',
      role: ROLE.ADMIN,
      sidebar_modules: JSON.stringify({ admin: { enabled: false } }),
      permissions: {
        admin_permissions: { [`system_settings.${scope}`]: { view: true } },
      },
    })
    const entries = result.current.navGroups.flatMap((group) => group.items)
    expect(entries.find((item) => item.title === 'System Settings')?.url).toBe(
      url
    )
    expect(entries.map((item) => item.title)).not.toContain('Task Plugins')
    expect(entries.map((item) => item.title)).not.toContain('Node Pool')

    act(() => {
      useAuthStore.getState().auth.setUser({
        id: 1,
        username: 'admin',
        role: ROLE.ADMIN,
        permissions: { admin_permissions: {} },
      })
    })
    expect(
      result.current.navGroups
        .flatMap((group) => group.items)
        .map((item) => item.title)
    ).not.toContain('System Settings')
  }
)

test.each([ROLE.USER, ROLE.ADMIN])(
  'role %s without settings grants has no settings entry',
  (role) => {
    const { result } = sidebarFor({ id: 1, username: 'alice', role })
    expect(
      result.current.navGroups
        .flatMap((group) => group.items)
        .map((item) => item.title)
    ).not.toContain('System Settings')
  }
)

test('root keeps its settings entry without an explicit permission matrix', () => {
  const { result } = sidebarFor({
    id: 1,
    username: 'root',
    role: ROLE.SUPER_ADMIN,
  })
  expect(
    result.current.navGroups
      .flatMap((group) => group.items)
      .find((item) => item.title === 'System Settings')?.url
  ).toBe('/system-settings/site/system-info')
})

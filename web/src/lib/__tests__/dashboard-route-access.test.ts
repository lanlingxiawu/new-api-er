import { afterEach, expect, test, vi } from 'vitest'

import { Route } from '@/routes/_authenticated/dashboard/$section'
import { useAuthStore } from '@/stores/auth-store'

vi.mock('@/features/dashboard', () => ({ Dashboard: () => null }))

afterEach(() => useAuthStore.getState().auth.reset())

test.each([
  { role: 1, employee: false, section: 'users', allowed: false },
  { role: 1, employee: true, section: 'users', allowed: false },
  { role: 10, employee: false, section: 'users', allowed: true },
  { role: 100, employee: false, section: 'users', allowed: true },
  { role: 1, employee: false, section: 'overview', allowed: true },
  { role: 1, employee: false, section: 'models', allowed: true },
  { role: 1, employee: false, section: 'flow', allowed: true },
])(
  'direct $section navigation respects role=$role employee=$employee',
  ({ role, employee, section, allowed }) => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 1, username: 'viewer', role, is_employee: employee })
    const load = () =>
      (
        Route.options.beforeLoad as (input: {
          params: { section: string }
        }) => unknown
      )({ params: { section } })
    if (allowed) expect(load).not.toThrow()
    else expect(load).toThrow()
  }
)

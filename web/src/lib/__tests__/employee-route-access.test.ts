import { expect, test } from 'vitest'

import { Route as CommissionRoute } from '@/routes/_authenticated/commission'
import { Route as CustomerRoute } from '@/routes/_authenticated/customer-console'
import { useAuthStore } from '@/stores/auth-store'

test.each([CommissionRoute, CustomerRoute])(
  'restricts employee pages to active employees',
  (route) => {
    for (const role of [1, 10, 100]) {
      useAuthStore
        .getState()
        .auth.setUser({ id: 1, username: 'viewer', role, is_employee: false })
      expect(() => (route.options.beforeLoad as () => unknown)()).toThrow()
    }
    useAuthStore
      .getState()
      .auth.setUser({ id: 1, username: 'employee', role: 1, is_employee: true })
    expect(() => (route.options.beforeLoad as () => unknown)()).not.toThrow()
  }
)

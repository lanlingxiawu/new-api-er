import { test } from '@playwright/test'
import { smoke } from './lib/smoke'
import {
  DEFAULT_BASE,
  DEFAULT_PUBLIC,
  DEFAULT_AUTHED,
  DEFAULT_ADMIN_ONLY,
  statePath,
  Role,
} from './lib/config'

const UI = 'default'

// GUEST: only public routes (authed routes should redirect to sign-in — tested in rbac)
test.describe('default guest', () => {
  test.use({ storageState: { cookies: [], origins: [] } })
  for (const route of DEFAULT_PUBLIC) {
    test(`guest ${route}`, async ({ page }, info) => {
      await smoke(page, info, UI, 'guest', DEFAULT_BASE, route)
    })
  }
})

// authed roles: all routes
for (const role of ['common', 'admin', 'root'] as Role[]) {
  test.describe(`default ${role}`, () => {
    test.use({ storageState: statePath(UI, role) })
    const routes =
      role === 'common'
        ? [...DEFAULT_PUBLIC, ...DEFAULT_AUTHED.filter((r) => !DEFAULT_ADMIN_ONLY.includes(r))]
        : [...DEFAULT_PUBLIC, ...DEFAULT_AUTHED]
    for (const route of routes) {
      test(`${role} ${route}`, async ({ page }, info) => {
        await smoke(page, info, UI, role, DEFAULT_BASE, route)
      })
    }
  })
}

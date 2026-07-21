import { test } from '@playwright/test'
import { smoke } from './lib/smoke'
import {
  CLASSIC_BASE,
  CLASSIC_PUBLIC,
  CLASSIC_AUTHED,
  CLASSIC_ADMIN_ONLY,
  statePath,
  Role,
} from './lib/config'

const UI = 'classic'

test.describe('classic guest', () => {
  test.use({ storageState: { cookies: [], origins: [] } })
  for (const route of CLASSIC_PUBLIC) {
    test(`guest ${route}`, async ({ page }, info) => {
      await smoke(page, info, UI, 'guest', CLASSIC_BASE, route)
    })
  }
})

for (const role of ['common', 'admin', 'root'] as Role[]) {
  test.describe(`classic ${role}`, () => {
    test.use({ storageState: statePath(UI, role) })
    const routes =
      role === 'common'
        ? [...CLASSIC_PUBLIC, ...CLASSIC_AUTHED.filter((r) => !CLASSIC_ADMIN_ONLY.includes(r))]
        : [...CLASSIC_PUBLIC, ...CLASSIC_AUTHED]
    for (const route of routes) {
      test(`${role} ${route}`, async ({ page }, info) => {
        await smoke(page, info, UI, role, CLASSIC_BASE, route)
      })
    }
  })
}

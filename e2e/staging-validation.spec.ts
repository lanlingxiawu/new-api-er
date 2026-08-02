import { expect, test, type Page } from '@playwright/test'

const base = process.env.E2E_STAGING_BASE_URL
const username = process.env.E2E_STAGING_USERNAME
const password = process.env.E2E_STAGING_PASSWORD

if (!base || !username || !password) {
  throw new Error('E2E_STAGING_BASE_URL, E2E_STAGING_USERNAME and E2E_STAGING_PASSWORD are required')
}

const publicRoutes = [
  '/', '/sign-in', '/sign-up', '/register', '/forgot-password', '/reset', '/otp',
  '/pricing', '/rankings', '/about', '/privacy-policy', '/user-agreement', '/401',
  '/403', '/404', '/500', '/503',
]

const rootRoutes = [
  '/dashboard', '/playground', '/keys', '/usage-logs', '/request-logs', '/profile',
  '/wallet', '/security', '/subscriptions', '/commission', '/commission-overview',
  '/customer-console', '/marketplace', '/console/topup', '/console/log', '/channels',
  '/users', '/models', '/employees', '/customers', '/redemption-codes',
  '/system-settings', '/system-info',
]

async function assertRenderable(page: Page, route: string) {
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  page.on('console', (message) => {
    if (message.type() === 'error') errors.push(message.text())
  })
  const response = await page.goto(base + route, { waitUntil: 'domcontentloaded' })
  expect(response?.status(), route).toBeLessThan(500)
  await page.waitForTimeout(2_000)
  const text = (await page.locator('body').innerText()).trim()
  expect(errors, `${route} emitted browser errors`).toEqual([])
  expect(text.length, `${route} rendered an empty page`).toBeGreaterThan(0)
}

test.describe.serial('staging UI validation', () => {
  test.setTimeout(180_000)

  test('public routes render', async ({ page }) => {
    for (const route of publicRoutes) await assertRenderable(page, route)
  })

  test('invalid login stays on sign-in', async ({ page }) => {
    await page.goto(base + '/sign-in', { waitUntil: 'domcontentloaded' })
    await page.locator('input[name="username"]').fill(username)
    await page.locator('input[name="password"]').fill('WrongPass999!')
    await page.locator('input[name="password"]').press('Enter')
    await page.waitForTimeout(1200)
    expect(page.url()).toContain('/sign-in')
  })

  test('root login and protected routes render', async ({ page }) => {
    await page.goto(base + '/sign-in', { waitUntil: 'domcontentloaded' })
    await page.locator('input[name="username"]').fill(username)
    await page.locator('input[name="password"]').fill(password)
    await page.locator('input[name="password"]').press('Enter')
    await expect(page).not.toHaveURL(/\/sign-in/, { timeout: 10_000 })
    for (const route of rootRoutes) await assertRenderable(page, route)
  })
})

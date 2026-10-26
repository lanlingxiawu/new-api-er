import path from 'node:path'

import { expect, test, type Page } from '@playwright/test'

// Deliberately no page.route / API mocks: requests go through Rsbuild's proxy,
// the real Gin middleware/controller/model, and the isolated SQLite fixture.
async function signIn(page: Page) {
  const baseURL = process.env.E2E_EXCLUSIVE_BASE_URL
  const password = process.env.E2E_EXCLUSIVE_PASSWORD
  if (!baseURL || !password) {
    throw new Error(
      'Run with user-exclusive-ratio.config.ts to initialize the isolated fixture'
    )
  }
  await page.goto(`${baseURL}/sign-in`)
  await page.locator('input[name="username"]').fill('ratio_root')
  await page.locator('input[name="password"]').fill(password)
  await page.locator('input[name="password"]').press('Enter')
  await expect(page).not.toHaveURL(/\/sign-in/)
  await page.goto(`${baseURL}/users`)
  await page
    .getByPlaceholder('Filter by username, name or email...')
    .fill('exr-')
}

async function selectRatio(page: Page, label: string) {
  await page.getByRole('button', { name: /^Exclusive Ratio/ }).click()
  await page.getByRole('option', { name: label, exact: true }).click()
  await page.keyboard.press('Escape')
}

async function expectPage(
  page: Page,
  count: number,
  prefix: string,
  size: number,
  highestIndex: number
) {
  await expect(page.getByText(new RegExp(`^Total:\\s*${count}$`))).toBeVisible()
  const names = page.getByText(new RegExp(`^${prefix}\\d{2}$`), { exact: true })
  await expect(names).toHaveCount(size)
  await expect(names).toHaveText(
    Array.from(
      { length: size },
      (_, index) => prefix + String(highestIndex - index).padStart(2, '0')
    )
  )
  for (const name of await names.all()) await expect(name).toBeVisible()
}

test('desktop: real rows, total, server pagination, combined filter, refresh and clear', async ({
  page,
}) => {
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  await signIn(page)
  // The entire original first page has no ratios. A client-only current-page
  // filter would incorrectly show no users when Configured is selected.
  await expectPage(page, 49, 'exr-plain-', 20, 23)

  const filteredResponse = page.waitForResponse(
    (response) =>
      response.url().includes('/api/user/search?') &&
      new URL(response.url()).searchParams.get('has_exclusive_ratio') === 'true'
  )
  await selectRatio(page, 'Configured')
  const payload = await (await filteredResponse).json()
  expect(payload.success).toBe(true)
  expect(payload.data.total).toBe(25)
  await expectPage(page, 25, 'exr-ratio-', 20, 24)
  await expect(
    page.locator('tbody').getByText('Exclusive Ratio', { exact: true })
  ).toHaveCount(20)

  await page
    .getByRole('button', { name: 'Go to next page', exact: true })
    .click()
  await expectPage(page, 25, 'exr-ratio-', 5, 4)
  await expect(page.getByText('exr-ratio-00', { exact: true })).toBeVisible()
  await expect(
    page.locator('tbody').getByText('Exclusive Ratio', { exact: true })
  ).toHaveCount(5)

  await page
    .locator('button[aria-haspopup="dialog"]')
    .filter({ hasText: 'User Group' })
    .click()
  await page.getByRole('option', { name: 'default', exact: true }).click()
  await page.keyboard.press('Escape')
  await expectPage(page, 23, 'exr-ratio-', 20, 22)
  expect(new URL(page.url()).searchParams.get('page') ?? '1').toBe('1')
  await page.reload()
  await expectPage(page, 23, 'exr-ratio-', 20, 22)
  await expect(
    page.getByRole('button', { name: /^Exclusive Ratio/ })
  ).toContainText('Configured')
  await page.screenshot({
    path: path.resolve('results/user-exclusive-ratio/desktop-configured.png'),
    fullPage: true,
  })

  await selectRatio(page, 'Not configured')
  await expectPage(page, 22, 'exr-plain-', 20, 21)
  await expect(
    page.locator('tbody').getByText('Exclusive Ratio', { exact: true })
  ).toHaveCount(0)
  await page.getByRole('button', { name: /^Exclusive Ratio/ }).click()
  await page.getByRole('option', { name: 'Clear filters', exact: true }).click()
  await page.keyboard.press('Escape')
  await expect(page.getByText(/^Total:\s*45$/)).toBeVisible()
  expect(new URL(page.url()).searchParams.has('has_exclusive_ratio')).toBe(
    false
  )
  expect(errors).toEqual([])
})

test('mobile: filter operates on cards and survives a page reload', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await signIn(page)
  await expectPage(page, 49, 'exr-plain-', 10, 23)
  await selectRatio(page, 'Configured')
  await expectPage(page, 25, 'exr-ratio-', 10, 24)
  await page.reload()
  await expectPage(page, 25, 'exr-ratio-', 10, 24)
  await expect(
    page.getByRole('button', { name: /^Exclusive Ratio/ })
  ).toContainText('Configured')
  await page.screenshot({
    path: path.resolve('results/user-exclusive-ratio/mobile-configured.png'),
    fullPage: true,
  })
  await selectRatio(page, 'Not configured')
  await expectPage(page, 24, 'exr-plain-', 10, 23)
})

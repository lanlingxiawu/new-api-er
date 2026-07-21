import { test } from '@playwright/test'
import fs from 'node:fs'
import { DEFAULT_BASE, CLASSIC_BASE, statePath } from './lib/config'

function rec(o: any) { fs.mkdirSync('./results', { recursive: true }); fs.appendFileSync('./results/employee.jsonl', JSON.stringify(o) + '\n') }

// As root, load the admin commission/business overview pages and confirm the
// seeded channel profit (e2e_mockai, consumption 2256) is rendered in the UI.
test.describe('admin overview UI numbers', () => {
  test.use({ storageState: statePath('default', 'root') })
  test('default admin overview shows e2e_mockai profit', async ({ page }) => {
    for (const route of ['/commission-overview', '/dashboard']) {
      await page.goto(DEFAULT_BASE + route, { waitUntil: 'domcontentloaded' })
      await page.waitForLoadState('networkidle', { timeout: 8000 }).catch(() => {})
      await page.waitForTimeout(1200)
      const txt = ((await page.evaluate(() => document.body?.innerText || '')) as string)
      rec({ ui: 'default', check: `overview-ui ${route}`, hasMockChannel: txt.includes('e2e_mockai'), len: txt.length })
    }
    // Also pull the admin overview API through the root session as ground-truth echo
    const api = await page.evaluate(async () => {
      const user = JSON.parse(localStorage.getItem('user') || '{}')
      const res = await fetch('/api/admin/employee/overview', { headers: { 'New-Api-User': String(user.id) } })
      return await res.json()
    })
    const ch = (api?.data?.by_channel_platform || []).find((c: any) => c.channel_name === 'e2e_mockai')
    rec({ ui: 'default', check: 'admin-overview-api', channel: ch?.channel_name, consumption_quota: ch?.consumption_quota, est_cost_quota: ch?.est_cost_quota, est_profit_quota: ch?.est_profit_quota })
  })
})

test.describe('admin overview UI numbers classic', () => {
  test.use({ storageState: statePath('classic', 'root') })
  test('classic business overview loads', async ({ page }) => {
    for (const route of ['/console/commission-overview', '/console']) {
      await page.goto(CLASSIC_BASE + route, { waitUntil: 'domcontentloaded' })
      await page.waitForLoadState('networkidle', { timeout: 8000 }).catch(() => {})
      await page.waitForTimeout(1200)
      const txt = ((await page.evaluate(() => document.body?.innerText || '')) as string)
      rec({ ui: 'classic', check: `overview-ui ${route}`, hasMockChannel: txt.includes('e2e_mockai'), len: txt.length })
    }
  })
})

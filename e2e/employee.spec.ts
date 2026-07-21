import { test, expect } from '@playwright/test'
import fs from 'node:fs'
import { smoke } from './lib/smoke'
import {
  DEFAULT_BASE,
  CLASSIC_BASE,
  DEFAULT_ADMIN_ONLY,
  CLASSIC_ADMIN_ONLY,
  statePath,
} from './lib/config'

function rec(file: string, obj: any) {
  fs.mkdirSync('./results', { recursive: true })
  fs.appendFileSync(`./results/${file}`, JSON.stringify(obj) + '\n')
}
const FORBIDDEN_RE = /forbidden|not authorized|no permission|无权|禁止|403|access denied|please sign in|sign in|登录/i

const DEFAULT_EMP = ['/dashboard', '/commission', '/commission-overview', '/customer-console', '/customers', '/profile', '/keys', '/wallet']
const CLASSIC_EMP = ['/console', '/console/commission', '/console/commission-overview', '/console/customer-console', '/console/personal', '/console/token', '/console/log']

// ---------- DEFAULT employee ----------
test.describe('employee default', () => {
  test.use({ storageState: statePath('default', 'employee') })

  for (const route of DEFAULT_EMP) {
    test(`emp default smoke ${route}`, async ({ page }, info) => {
      await smoke(page, info, 'default', 'employee', DEFAULT_BASE, route)
    })
  }

  test('emp default blocked from admin pages', async ({ page }) => {
    for (const route of DEFAULT_ADMIN_ONLY) {
      await page.goto(DEFAULT_BASE + route, { waitUntil: 'domcontentloaded' })
      await page.waitForTimeout(900)
      const url = page.url()
      const body = ((await page.evaluate(() => document.body?.innerText || '')) as string).replace(/\s+/g, ' ').trim()
      const stayed = url.includes(route)
      const leak = stayed && body.length > 400 && !FORBIDDEN_RE.test(body)
      rec('rbac.jsonl', { ui: 'default', kind: 'employee-admin', route, finalUrl: url, stayed, leak, snippet: body.slice(0, 120) })
      expect.soft(leak, `default employee LEAK on ${route}`).toBeFalsy()
    }
  })

  test('emp default sees own commission numbers (API via session)', async ({ page }) => {
    await page.goto(DEFAULT_BASE + '/commission', { waitUntil: 'domcontentloaded' })
    const data = await page.evaluate(async () => {
      const user = JSON.parse(localStorage.getItem('user') || '{}')
      const res = await fetch('/api/user/employee/commission/summary', {
        headers: { 'New-Api-User': String(user.id) },
      })
      return await res.json()
    })
    const d = data?.data || {}
    rec('employee.jsonl', {
      ui: 'default', check: 'commission_summary', ok: data?.success === true,
      commission_total_quota: d.commission_total_quota,
      current_commission_quota: d.current_commission_quota,
      current_performance_quota: d.current_performance_quota,
      customer_total_consumption_quota: d.customer_total_consumption_quota,
    })
    // DB ground truth: performance(profit)=1116, accrual total=122, current recompute=112, consumption=2256
    expect.soft(d.current_performance_quota, 'employee performance(profit) quota').toBe(1116)
    expect.soft(d.commission_total_quota, 'employee commission accrual total').toBe(122)
    expect.soft(d.current_commission_quota, 'employee current commission (period recompute)').toBe(112)
    expect.soft(d.customer_total_consumption_quota, 'employee customer consumption').toBe(2256)
  })
})

// ---------- CLASSIC employee ----------
test.describe('employee classic', () => {
  test.use({ storageState: statePath('classic', 'employee') })

  for (const route of CLASSIC_EMP) {
    test(`emp classic smoke ${route}`, async ({ page }, info) => {
      await smoke(page, info, 'classic', 'employee', CLASSIC_BASE, route)
    })
  }

  test('emp classic blocked from admin pages', async ({ page }) => {
    for (const route of CLASSIC_ADMIN_ONLY) {
      await page.goto(CLASSIC_BASE + route, { waitUntil: 'domcontentloaded' })
      await page.waitForTimeout(900)
      const url = page.url()
      const body = ((await page.evaluate(() => document.body?.innerText || '')) as string).replace(/\s+/g, ' ').trim()
      const stayed = url.includes(route)
      const leak = stayed && body.length > 400 && !FORBIDDEN_RE.test(body)
      rec('rbac.jsonl', { ui: 'classic', kind: 'employee-admin', route, finalUrl: url, stayed, leak, snippet: body.slice(0, 120) })
      expect.soft(leak, `classic employee LEAK on ${route}`).toBeFalsy()
    }
  })
})

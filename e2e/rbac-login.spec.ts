import { test, expect, Page } from '@playwright/test'
import fs from 'node:fs'
import {
  DEFAULT_BASE,
  CLASSIC_BASE,
  DEFAULT_ADMIN_ONLY,
  CLASSIC_ADMIN_ONLY,
  CREDS,
  statePath,
} from './lib/config'

function rec(file: string, obj: any) {
  fs.mkdirSync('./results', { recursive: true })
  fs.appendFileSync(`./results/${file}`, JSON.stringify(obj) + '\n')
}

const FORBIDDEN_RE = /forbidden|not authorized|no permission|无权|禁止|403|access denied|please sign in|sign in|log ?in|登录/i

// ---------- RBAC: guest hitting authed pages must go to login ----------
test.describe('rbac guest redirects', () => {
  test.use({ storageState: { cookies: [], origins: [] } })

  test('default guest -> authed pages redirect to sign-in', async ({ page }) => {
    for (const route of ['/channels', '/users', '/system-settings', '/keys', '/dashboard']) {
      await page.goto(DEFAULT_BASE + route, { waitUntil: 'domcontentloaded' })
      await page.waitForTimeout(800)
      const url = page.url()
      const redirected = /sign-in|login|\/(401|403)/.test(url) || url.endsWith('/')
      rec('rbac.jsonl', { ui: 'default', kind: 'guest', route, finalUrl: url, redirected })
      expect.soft(redirected, `default guest ${route} should redirect (got ${url})`).toBeTruthy()
    }
  })

  test('classic guest -> authed pages redirect to login', async ({ page }) => {
    for (const route of ['/console/channel', '/console/user', '/console/setting', '/console/token', '/console']) {
      await page.goto(CLASSIC_BASE + route, { waitUntil: 'domcontentloaded' })
      await page.waitForTimeout(800)
      const url = page.url()
      const redirected = /login|forbidden/.test(url)
      rec('rbac.jsonl', { ui: 'classic', kind: 'guest', route, finalUrl: url, redirected })
      expect.soft(redirected, `classic guest ${route} should redirect (got ${url})`).toBeTruthy()
    }
  })
})

// ---------- RBAC: common user hitting admin-only pages must NOT see admin data ----------
test.describe('rbac common blocked from admin', () => {
  test('default common -> admin pages blocked', async ({ page, browser }) => {
    const ctx = await browser.newContext({ storageState: statePath('default', 'common') })
    const p = await ctx.newPage()
    for (const route of DEFAULT_ADMIN_ONLY) {
      await p.goto(DEFAULT_BASE + route, { waitUntil: 'domcontentloaded' })
      await p.waitForTimeout(900)
      const url = p.url()
      const body = ((await p.evaluate(() => document.body?.innerText || '')) as string).replace(/\s+/g, ' ').trim()
      const stayed = url.includes(route)
      const forbiddenShown = FORBIDDEN_RE.test(body) || /\/(401|403)/.test(url) || !stayed
      // leak = stayed on admin route with substantial content and no forbidden marker
      const leak = stayed && body.length > 400 && !FORBIDDEN_RE.test(body)
      rec('rbac.jsonl', {
        ui: 'default', kind: 'common-admin', route, finalUrl: url, stayed, forbiddenShown, leak, snippet: body.slice(0, 160),
      })
      expect.soft(leak, `default common LEAK on ${route}: ${body.slice(0, 120)}`).toBeFalsy()
    }
    await ctx.close()
  })

  test('classic common -> admin pages blocked', async ({ browser }) => {
    const ctx = await browser.newContext({ storageState: statePath('classic', 'common') })
    const p = await ctx.newPage()
    for (const route of CLASSIC_ADMIN_ONLY) {
      await p.goto(CLASSIC_BASE + route, { waitUntil: 'domcontentloaded' })
      await p.waitForTimeout(900)
      const url = p.url()
      const body = ((await p.evaluate(() => document.body?.innerText || '')) as string).replace(/\s+/g, ' ').trim()
      const stayed = url.includes(route)
      const forbiddenShown = FORBIDDEN_RE.test(body) || url.includes('forbidden') || !stayed
      const leak = stayed && body.length > 400 && !FORBIDDEN_RE.test(body)
      rec('rbac.jsonl', {
        ui: 'classic', kind: 'common-admin', route, finalUrl: url, stayed, forbiddenShown, leak, snippet: body.slice(0, 160),
      })
      expect.soft(leak, `classic common LEAK on ${route}: ${body.slice(0, 120)}`).toBeFalsy()
    }
    await ctx.close()
  })
})

// ---------- LOGIN flows ----------
async function fillLogin(page: Page, username: string, password: string) {
  await page.locator('input[name="username"]').first().fill(username)
  await page.locator('input[name="password"]').first().fill(password)
  // try a submit button, fallback to Enter
  const btn = page.getByRole('button', { name: /sign ?in|log ?in|login|登录|Continue/i }).first()
  if (await btn.count()) {
    await btn.click().catch(() => {})
  } else {
    await page.locator('input[name="password"]').first().press('Enter')
  }
}

test.describe('login flows', () => {
  test.use({ storageState: { cookies: [], origins: [] } })

  test('default sign-in valid + invalid', async ({ page }) => {
    // invalid
    await page.goto(DEFAULT_BASE + '/sign-in', { waitUntil: 'domcontentloaded' })
    await fillLogin(page, CREDS.admin.username, 'WrongPass999')
    await page.waitForTimeout(1500)
    const invalidUrl = page.url()
    const invalidStayed = /sign-in/.test(invalidUrl)
    rec('login.jsonl', { ui: 'default', case: 'invalid', finalUrl: invalidUrl, stayedOnLogin: invalidStayed })
    expect.soft(invalidStayed, `default invalid login should stay on sign-in (got ${invalidUrl})`).toBeTruthy()

    // valid
    await page.goto(DEFAULT_BASE + '/sign-in', { waitUntil: 'domcontentloaded' })
    await fillLogin(page, CREDS.admin.username, CREDS.admin.password)
    await page.waitForTimeout(2000)
    const validUrl = page.url()
    const loggedIn = !/sign-in/.test(validUrl)
    rec('login.jsonl', { ui: 'default', case: 'valid', finalUrl: validUrl, loggedIn })
    expect.soft(loggedIn, `default valid login should leave sign-in (got ${validUrl})`).toBeTruthy()
  })

  test('classic login valid + invalid', async ({ page }) => {
    await page.goto(CLASSIC_BASE + '/login', { waitUntil: 'domcontentloaded' })
    await fillLogin(page, CREDS.admin.username, 'WrongPass999')
    await page.waitForTimeout(1500)
    const invalidUrl = page.url()
    const invalidStayed = /login/.test(invalidUrl)
    rec('login.jsonl', { ui: 'classic', case: 'invalid', finalUrl: invalidUrl, stayedOnLogin: invalidStayed })
    expect.soft(invalidStayed, `classic invalid login should stay on login (got ${invalidUrl})`).toBeTruthy()

    await page.goto(CLASSIC_BASE + '/login', { waitUntil: 'domcontentloaded' })
    await fillLogin(page, CREDS.admin.username, CREDS.admin.password)
    await page.waitForTimeout(2000)
    const validUrl = page.url()
    const loggedIn = !/\/login/.test(validUrl)
    rec('login.jsonl', { ui: 'classic', case: 'valid', finalUrl: validUrl, loggedIn })
    expect.soft(loggedIn, `classic valid login should leave login (got ${validUrl})`).toBeTruthy()
  })
})

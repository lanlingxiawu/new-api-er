import { test, expect, Page } from '@playwright/test'
import fs from 'node:fs'
import { DEFAULT_BASE, CLASSIC_BASE, statePath } from './lib/config'

function rec(obj: any) {
  fs.mkdirSync('./results', { recursive: true })
  fs.appendFileSync('./results/crud.jsonl', JSON.stringify(obj) + '\n')
}

// Stable across Playwright worker recycling via env var (set at launch)
const TAG = process.env.E2E_TAG || 'e2e' + Date.now().toString().slice(-6)

// call the app's own API from page context (session cookie + New-Api-User header)
async function apiCall(page: Page, method: string, path: string, body?: any) {
  return await page.evaluate(
    async ({ method, path, body }) => {
      const user = JSON.parse(localStorage.getItem('user') || '{}')
      const headers: Record<string, string> = { 'Content-Type': 'application/json' }
      if (user?.id) headers['New-Api-User'] = String(user.id)
      const res = await fetch(path, {
        method,
        headers,
        body: body ? JSON.stringify(body) : undefined,
      })
      let j: any = null
      try { j = await res.json() } catch {}
      return { status: res.status, json: j }
    },
    { method, path, body },
  )
}

test.describe('CRUD flows (admin, via default UI origin)', () => {
  test.use({ storageState: statePath('default', 'root') })

  const created: { path: string; id: any }[] = []

  test('create channel/token/user/redemption + verify rows + setting toggle', async ({ page }) => {
    await page.goto(DEFAULT_BASE + '/', { waitUntil: 'domcontentloaded' })

    // ---- Channel ----
    {
      const name = `${TAG}_chan`
      const r = await apiCall(page, 'POST', '/api/channel/', {
        mode: 'single',
        channel: {
          name, type: 1, key: 'sk-e2e-dummy', base_url: 'http://127.0.0.1:18080',
          models: 'gpt-4o-mini', groups: ['default'], group: 'default', other: '',
          model_mapping: '', status_code_mapping: '', setting: '',
        },
      })
      const ok = r.status < 500 && r.json?.success === true
      rec({ ui: 'backend', flow: 'channel.create', ok, detail: `status=${r.status} msg=${r.json?.message || ''} name=${name}` })
      expect.soft(ok, `channel create: ${JSON.stringify(r.json)}`).toBeTruthy()
    }

    // ---- Token ----
    {
      const name = `${TAG}_tok`
      const r = await apiCall(page, 'POST', '/api/token/', {
        name, remain_quota: 500000, expired_time: -1, unlimited_quota: true,
        model_limits_enabled: false, model_limits: '', allow_ips: '', group: '',
      })
      const ok = r.status < 500 && r.json?.success === true
      rec({ ui: 'backend', flow: 'token.create', ok, detail: `status=${r.status} msg=${r.json?.message || ''} name=${name}` })
      expect.soft(ok, `token create: ${JSON.stringify(r.json)}`).toBeTruthy()
    }

    // ---- User ----
    {
      const username = `${TAG}_usr`
      const r = await apiCall(page, 'POST', '/api/user/', {
        username, password: 'Test1234!', display_name: username,
      })
      const ok = r.status < 500 && r.json?.success === true
      rec({ ui: 'backend', flow: 'user.create', ok, detail: `status=${r.status} msg=${r.json?.message || ''} name=${username}` })
      expect.soft(ok, `user create: ${JSON.stringify(r.json)}`).toBeTruthy()
    }

    // ---- Redemption ----
    {
      const name = `${TAG}_rdm`
      const r = await apiCall(page, 'POST', '/api/redemption/', { name, quota: 100000, count: 1 })
      const ok = r.status < 500 && r.json?.success === true
      rec({ ui: 'backend', flow: 'redemption.create', ok, detail: `status=${r.status} msg=${r.json?.message || ''} name=${name}` })
      expect.soft(ok, `redemption create: ${JSON.stringify(r.json)}`).toBeTruthy()
    }

    // ---- Setting toggle (read option list, flip a boolean, restore) ----
    {
      const list = await apiCall(page, 'GET', '/api/option/')
      let ok = false
      let detail = `status=${list.status}`
      const opts: any[] = list.json?.data || []
      const target = opts.find((o) => o.key === 'DataExportEnabled') || opts.find((o) => /Enabled$/.test(o.key) && (o.value === 'true' || o.value === 'false'))
      if (target) {
        const orig = target.value
        const flipped = orig === 'true' ? 'false' : 'true'
        const put = await apiCall(page, 'PUT', '/api/option/', { key: target.key, value: flipped })
        const restore = await apiCall(page, 'PUT', '/api/option/', { key: target.key, value: orig })
        ok = put.json?.success === true && restore.json?.success === true
        detail = `key=${target.key} ${orig}->${flipped}->restore put=${put.json?.success} restore=${restore.json?.success}`
      } else {
        detail += ' no toggleable option found'
      }
      rec({ ui: 'backend', flow: 'setting.toggle', ok, detail })
      expect.soft(ok, `setting toggle: ${detail}`).toBeTruthy()
    }
  })

  // Verify created rows render in the DEFAULT UI list pages
  test('default UI shows created rows', async ({ page }) => {
    const checks: [string, string][] = [
      ['/channels', `${TAG}_chan`],
      ['/keys', `${TAG}_tok`],
      ['/users', `${TAG}_usr`],
      ['/redemption-codes', `${TAG}_rdm`],
    ]
    for (const [route, needle] of checks) {
      await page.goto(DEFAULT_BASE + route, { waitUntil: 'domcontentloaded' })
      await page.waitForLoadState('networkidle', { timeout: 8000 }).catch(() => {})
      await page.waitForTimeout(800)
      const found = await page.getByText(needle, { exact: false }).count().catch(() => 0)
      rec({ ui: 'default', flow: `list.row ${route}`, ok: found > 0, detail: `needle=${needle} found=${found}` })
      expect.soft(found > 0, `default ${route} should show ${needle}`).toBeTruthy()
    }
  })
})

// Verify created rows render in the CLASSIC UI list pages
test.describe('CRUD verify in classic UI', () => {
  test.use({ storageState: statePath('classic', 'root') })
  test('classic UI shows created rows', async ({ page }) => {
    const checks: [string, string][] = [
      ['/console/channel', `${TAG}_chan`],
      ['/console/token', `${TAG}_tok`],
      ['/console/user', `${TAG}_usr`],
      ['/console/redemption', `${TAG}_rdm`],
    ]
    for (const [route, needle] of checks) {
      await page.goto(CLASSIC_BASE + route, { waitUntil: 'domcontentloaded' })
      await page.waitForLoadState('networkidle', { timeout: 8000 }).catch(() => {})
      await page.waitForTimeout(800)
      const found = await page.getByText(needle, { exact: false }).count().catch(() => 0)
      rec({ ui: 'classic', flow: `list.row ${route}`, ok: found > 0, detail: `needle=${needle} found=${found}` })
      expect.soft(found > 0, `classic ${route} should show ${needle}`).toBeTruthy()
    }
  })
})

// Cleanup created rows (best-effort, via search + delete)
test.describe('CRUD cleanup', () => {
  test.use({ storageState: statePath('default', 'root') })
  test('delete e2e-created rows', async ({ page }) => {
    await page.goto(DEFAULT_BASE + '/', { waitUntil: 'domcontentloaded' })
    const results: string[] = []

    async function searchDelete(searchPath: string, delPathFn: (id: any) => string, needle: string, label: string) {
      const r = await apiCall(page, 'GET', searchPath)
      const items: any[] = r.json?.data?.items || r.json?.data?.records || r.json?.data || []
      const arr = Array.isArray(items) ? items : []
      let deleted = 0
      for (const it of arr) {
        const nm = it.name || it.username || ''
        if (typeof nm === 'string' && nm.includes(needle) && it.id != null) {
          const d = await apiCall(page, 'DELETE', delPathFn(it.id))
          if (d.status < 500) deleted++
        }
      }
      results.push(`${label}:${deleted}`)
    }

    await searchDelete(`/api/channel/search?keyword=${TAG}`, (id) => `/api/channel/${id}`, `${TAG}_chan`, 'channel')
    await searchDelete(`/api/token/search?keyword=${TAG}`, (id) => `/api/token/${id}`, `${TAG}_tok`, 'token')
    await searchDelete(`/api/user/search?keyword=${TAG}`, (id) => `/api/user/${id}`, `${TAG}_usr`, 'user')
    await searchDelete(`/api/redemption/search?keyword=${TAG}`, (id) => `/api/redemption/${id}`, `${TAG}_rdm`, 'redemption')

    rec({ ui: 'backend', flow: 'cleanup', ok: true, detail: results.join(' ') })
  })
})

import { Page, TestInfo, expect } from '@playwright/test'
import fs from 'node:fs'

export type SmokeResult = {
  ui: string
  role: string
  route: string
  finalUrl: string
  rendered: boolean
  bodyLen: number
  pageErrors: string[]
  serverErrors: string[] // >=500 responses
  consoleErrors: string[]
  snippet: string
  ok: boolean
}

const RESULT_FILE = './results/smoke.jsonl'

export function record(r: SmokeResult) {
  fs.mkdirSync('./results', { recursive: true })
  fs.appendFileSync(RESULT_FILE, JSON.stringify(r) + '\n')
}

// Navigate + collect diagnostics. Returns the result (also written to jsonl).
export async function smoke(
  page: Page,
  info: TestInfo,
  ui: string,
  role: string,
  base: string,
  route: string,
): Promise<SmokeResult> {
  const pageErrors: string[] = []
  const serverErrors: string[] = []
  const consoleErrors: string[] = []

  const onConsole = (msg: any) => {
    if (msg.type() === 'error') consoleErrors.push(msg.text().slice(0, 300))
  }
  const onPageError = (err: Error) => pageErrors.push((err.message || String(err)).slice(0, 300))
  const onResponse = (resp: any) => {
    try {
      if (resp.status() >= 500) serverErrors.push(`${resp.status()} ${resp.url()}`)
    } catch {}
  }
  page.on('console', onConsole)
  page.on('pageerror', onPageError)
  page.on('response', onResponse)

  let finalUrl = ''
  let bodyLen = 0
  let snippet = ''
  try {
    await page.goto(base + route, { waitUntil: 'domcontentloaded' })
    // let SPA render + async fetches settle
    await page.waitForLoadState('networkidle', { timeout: 8000 }).catch(() => {})
    await page.waitForTimeout(600)
    finalUrl = page.url()
    const txt = (await page.evaluate(() => document.body?.innerText || '')) as string
    bodyLen = txt.trim().length
    snippet = txt.replace(/\s+/g, ' ').trim().slice(0, 160)
  } catch (e: any) {
    pageErrors.push('goto: ' + (e.message || String(e)).slice(0, 200))
  } finally {
    page.off('console', onConsole)
    page.off('pageerror', onPageError)
    page.off('response', onResponse)
  }

  const rendered = bodyLen >= 3
  const ok = rendered && pageErrors.length === 0 && serverErrors.length === 0

  const r: SmokeResult = {
    ui,
    role,
    route,
    finalUrl,
    rendered,
    bodyLen,
    pageErrors,
    serverErrors,
    consoleErrors,
    snippet,
    ok,
  }
  record(r)

  if (!ok) {
    await page
      .screenshot({ path: `results/shots/${ui}-${role}-${route.replace(/[\/]/g, '_') || 'root'}.png` })
      .catch(() => {})
  }

  // soft assertions so the whole matrix runs to completion
  expect.soft(rendered, `[${ui}/${role}] ${route} blank page (len=${bodyLen})`).toBeTruthy()
  expect.soft(pageErrors, `[${ui}/${role}] ${route} JS pageerror: ${pageErrors.join(' | ')}`).toHaveLength(0)
  expect.soft(serverErrors, `[${ui}/${role}] ${route} 5xx: ${serverErrors.join(' | ')}`).toHaveLength(0)

  return r
}

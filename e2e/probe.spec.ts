import { test } from '@playwright/test'
import { DEFAULT_BASE, CLASSIC_BASE, statePath } from './lib/config'

function attach(page: any, tag: string) {
  const failed: string[] = []
  const bad: string[] = []
  page.on('requestfailed', (r: any) => failed.push(`${r.method()} ${r.url()} :: ${r.failure()?.errorText}`))
  page.on('response', (r: any) => { if (r.status() >= 400) bad.push(`${r.status()} ${r.url()}`) })
  return { failed, bad }
}

test.describe('probe default', () => {
  test.use({ storageState: statePath('default', 'root') })
  test('default channels renders probe', async ({ page }) => {
    const { failed, bad } = attach(page, 'default')
    await page.goto(DEFAULT_BASE + '/channels', { waitUntil: 'domcontentloaded' })
    await page.waitForLoadState('networkidle', { timeout: 8000 }).catch(() => {})
    await page.waitForTimeout(2500)
    const txt = ((await page.evaluate(() => document.body?.innerText || '')) as string)
    console.log('DEFAULT /channels len=', txt.length, 'hasProbe=', txt.includes('e2eprobe_chan'), 'url=', page.url())
    console.log('DEFAULT failed:', failed.slice(0, 8).join(' | ') || 'none')
    console.log('DEFAULT 4xx/5xx:', bad.slice(0, 8).join(' | ') || 'none')
  })
})

test.describe('probe classic', () => {
  test.use({ storageState: statePath('classic', 'root') })
  test('classic channel renders probe', async ({ page }) => {
    const { failed, bad } = attach(page, 'classic')
    await page.goto(CLASSIC_BASE + '/console/channel', { waitUntil: 'domcontentloaded' })
    await page.waitForLoadState('networkidle', { timeout: 8000 }).catch(() => {})
    await page.waitForTimeout(2500)
    const txt = ((await page.evaluate(() => document.body?.innerText || '')) as string)
    console.log('CLASSIC /console/channel len=', txt.length, 'hasProbe=', txt.includes('e2eprobe_chan'), 'hasNetErr=', txt.includes('Network Error'))
    console.log('CLASSIC failed:', failed.slice(0, 10).join(' | ') || 'none')
    console.log('CLASSIC 4xx/5xx:', bad.slice(0, 10).join(' | ') || 'none')
  })
})

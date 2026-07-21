import { test as setup } from '@playwright/test'
import fs from 'node:fs'
import { CREDS, DEFAULT_BASE, CLASSIC_BASE, statePath } from './lib/config'

fs.mkdirSync('./.auth', { recursive: true })
fs.mkdirSync('./results', { recursive: true })

const uis = [
  { ui: 'default', base: DEFAULT_BASE },
  { ui: 'classic', base: CLASSIC_BASE },
]

for (const { ui, base } of uis) {
  for (const role of ['root', 'admin', 'common', 'employee'] as const) {
    setup(`auth ${ui}-${role}`, async ({ page }) => {
      const { username, password } = CREDS[role]
      // land on origin so localStorage/cookies belong to it
      await page.goto(base + '/', { waitUntil: 'domcontentloaded' })
      const data = await page.evaluate(
        async ({ username, password }) => {
          let j: any = null
          for (let attempt = 0; attempt < 5; attempt++) {
            const res = await fetch('/api/user/login?turnstile=', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ username, password }),
            })
            const txt = await res.text()
            if (txt) {
              try { j = JSON.parse(txt) } catch { j = null }
            }
            if (j && j.success) break
            await new Promise((r) => setTimeout(r, 700))
          }
          if (!j || !j.success) throw new Error('login failed: ' + JSON.stringify(j))
          // Both UIs read the user object from 'user'; the DEFAULT UI additionally
          // reads the id from a separate 'uid' key for the New-Api-User header.
          localStorage.setItem('user', JSON.stringify(j.data))
          localStorage.setItem('uid', String(j.data.id))
          return j.data
        },
        { username, password },
      )
      if (!data || data.username !== username) throw new Error('unexpected login data')
      await page.context().storageState({ path: statePath(ui, role) })
    })
  }
}

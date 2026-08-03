import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

import en from '@/i18n/locales/en.json'
import fr from '@/i18n/locales/fr.json'
import ja from '@/i18n/locales/ja.json'
import ru from '@/i18n/locales/ru.json'
import vi from '@/i18n/locales/vi.json'
import zhTW from '@/i18n/locales/zh-TW.json'
import zh from '@/i18n/locales/zh.json'

const sessionLimitKey =
  'Too many active login sessions. On a device where you are already signed in, open Login sessions and use “Sign out other sessions” to revoke them. If you cannot access a signed-in device, reset your password to sign out all sessions.'

describe('session limit recovery guidance', () => {
  it('all locales identify the exact recovery pages', () => {
    for (const locale of [en, zh, zhTW, fr, ja, ru, vi]) {
      const message = locale.translation[sessionLimitKey]
      assert.match(message, /\/profile/)
      assert.match(message, /\/forgot-password/)
    }
  })
})

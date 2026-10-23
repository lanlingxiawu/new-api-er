import { defineConfig } from '@playwright/test'
import shared from './playwright.config'

// Uses the installed Chrome and a caller-supplied isolated gateway. No legacy
// frontend/auth setup dependency; staging-validation creates its own session.
export default defineConfig({
  ...shared,
  testMatch: /staging-validation\.spec\.ts/,
  projects: [{ name: 'local-chrome', use: { channel: 'chrome' } }],
})

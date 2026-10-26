import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: '.',
  testMatch: 'user-exclusive-ratio.spec.ts',
  globalSetup: './lib/user-exclusive-ratio.setup.ts',
  workers: 1,
  retries: 0,
  timeout: 90_000,
  expect: { timeout: 15_000 },
  outputDir: 'test-results/user-exclusive-ratio',
  reporter: [
    ['list'],
    [
      'html',
      { outputFolder: 'results/user-exclusive-ratio/report', open: 'never' },
    ],
  ],
  use: {
    channel: 'chrome',
    headless: true,
    locale: 'en-US',
    actionTimeout: 10_000,
    navigationTimeout: 30_000,
    viewport: { width: 1440, height: 900 },
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
})

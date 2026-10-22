import { describe, expect, it } from 'vitest'

import { observeExportProgress } from './remaining-time'

describe('export remaining time', () => {
  it('waits for an observed increase instead of treating queue time as processing', () => {
    const start = observeExportProgress(undefined, 'running', 20, 1000)
    expect(start.seconds).toBeNull()
    expect(observeExportProgress(start, 'running', 20, 6000).seconds).toBeNull()
    expect(observeExportProgress(start, 'running', 40, 11000).seconds).toBe(30)
  })
  it('does not count down while stalled or claim completion during packaging', () => {
    const start = observeExportProgress(undefined, 'running', 20, 1000)
    const next = observeExportProgress(start, 'running', 40, 11000)
    expect(observeExportProgress(next, 'running', 40, 21000).seconds).toBe(60)
    expect(observeExportProgress(next, 'running', 99, 21000).seconds).toBeNull()
  })
  it('resets on regressed progress and never estimates queued or terminal jobs', () => {
    const start = observeExportProgress(undefined, 'running', 20, 1000)
    const next = observeExportProgress(start, 'running', 40, 11000)
    expect(observeExportProgress(next, 'running', 30, 12000).seconds).toBeNull()
    for (const status of ['pending', 'ready', 'failed', 'canceled']) {
      expect(observeExportProgress(next, status, 50, 12000).seconds).toBeNull()
    }
    for (const progress of [0, 100, -1, Number.NaN, Infinity]) {
      expect(
        observeExportProgress(next, 'running', progress, 12000).seconds
      ).toBeNull()
    }
  })
})

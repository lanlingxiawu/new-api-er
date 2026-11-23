import { expect, it } from 'vitest'

import {
  buildQuotaExportRequest,
  quotaExportSummary,
  type QuotaExportDraft,
} from './quota-export'

function draft(overrides: Partial<QuotaExportDraft> = {}): QuotaExportDraft {
  return {
    start: 10,
    end: 20,
    userId: '',
    username: '',
    modelName: '',
    useGroup: '',
    channelId: '',
    tokenId: '',
    nodeName: '',
    header: true,
    csvBom: true,
    timezone: 'UTC',
    root: false,
    ...overrides,
  }
}

it('omits empty integers and keeps a stored zero', () => {
  const body = buildQuotaExportRequest(
    draft({ userId: '  ', channelId: '0', username: '  alice  ' })
  )
  expect(body?.filters).toEqual({
    start_timestamp: 10,
    end_timestamp: 20,
    channel_id: 0,
    username: 'alice',
  })
  expect(body?.filters).not.toHaveProperty('user_id')
  expect(body?.filters).not.toHaveProperty('token_id')
  expect(body?.options).toEqual({
    header: true,
    csv_bom: true,
    timezone: 'UTC',
  })
})

it('rejects fractions, signs, and text longer than 64 characters', () => {
  expect(buildQuotaExportRequest(draft({ userId: '-1' }))).toBeUndefined()
  expect(buildQuotaExportRequest(draft({ channelId: '1.5' }))).toBeUndefined()
  expect(buildQuotaExportRequest(draft({ tokenId: '1e2', root: true }))).toBeUndefined()
  expect(
    buildQuotaExportRequest(draft({ username: '名'.repeat(65) }))
  ).toBeUndefined()
})

it('drops root filters unless the creator is root', () => {
  const hidden = buildQuotaExportRequest(
    draft({ tokenId: '4', nodeName: 'node-a' })
  )
  expect(hidden?.filters).not.toHaveProperty('token_id')
  expect(hidden?.filters).not.toHaveProperty('node_name')

  const visible = buildQuotaExportRequest(
    draft({ tokenId: '4', nodeName: ' node-a ', root: true, header: false })
  )
  expect(visible?.filters.token_id).toBe(4)
  expect(visible?.filters.node_name).toBe('node-a')
  expect(visible?.options.header).toBe(false)
})

it('summarizes zero and root filters without treating zero as unset', () => {
  expect(
    quotaExportSummary({
      user_id: 0,
      channel_id: 0,
      model_name: 'gpt',
      token_id: 3,
      node_name: 'node-a',
    })
  ).toBe('0 · gpt · #0 · 3 · node-a')
  expect(quotaExportSummary(undefined)).toBe('')
  expect(quotaExportSummary({ username: 'alice', user_id: 9 })).toBe('alice')
})

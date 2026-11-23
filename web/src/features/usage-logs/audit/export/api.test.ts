import { describe, expect, it, vi } from 'vitest'

import { auditExportSnapshot, createAuditExport } from './api'

const post = vi.hoisted(() => vi.fn())
vi.mock('@/lib/api', () => ({ api: { post } }))

describe('audit export contract', () => {
  it('uses the public filter allowlist for self exports', () => {
    const filters = auditExportSnapshot(
      {
        p: 1,
        page_size: 20,
        ip: '192.0.2.1',
        auth_method: 'session',
        request_id: 'support',
        actor_type: 'root',
        route: '/internal',
        route_missing: true,
        username: 'root',
        token_ref: 'private',
      },
      'self'
    )
    expect(filters).toMatchObject({
      ip: '192.0.2.1',
      auth_method: 'session',
      request_id: 'support',
    })
    for (const key of [
      'actor_type',
      'route',
      'route_missing',
      'username',
      'token_ref',
    ]) {
      expect(filters[key as keyof typeof filters]).toBeUndefined()
    }
  })
  it('takes applied filters without pagination and preserves false and token exclusions', async () => {
    const filters = auditExportSnapshot(
      {
        p: 8,
        page_size: 50,
        start_timestamp: 1,
        end_timestamp: 2,
        success: 'false',
        category: 'access_token',
        exclude_token_ref: 'fingerprint',
      },
      'self'
    )
    expect(filters).toEqual({
      start_timestamp: 1,
      end_timestamp: 2,
      success: false,
      category: 'access_token',
      exclude_token_ref: 'fingerprint',
    })
    post.mockResolvedValue({
      data: { success: true, data: { job_id: 'job-42' } },
    })
    expect(
      await createAuditExport('self', filters, {
        header: true,
        csv_bom: true,
        timezone: 'UTC',
      })
    ).toBe('job-42')
    expect(post).toHaveBeenCalledWith('/api/audit/self/export/jobs', {
      filters,
      options: { header: true, csv_bom: true, timezone: 'UTC' },
    })
  })

  it('never drops a risk constraint to create a broader export', () => {
    expect(() =>
      auditExportSnapshot({ p: 1, page_size: 20, ip_risk: 'high' }, 'self')
    ).toThrow()
    expect(
      auditExportSnapshot({ p: 1, page_size: 20, ip_risk: 'high' }, 'all')
        .ip_risk
    ).toBe('high')
  })
})

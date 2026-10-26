import { afterEach, expect, it, vi } from 'vitest'

import { getServerErrorStatus } from '@/lib/server-error-message'

import { exportRequest } from './employee-api'

const request = vi.hoisted(() => vi.fn())
vi.mock('@/lib/api', () => ({ api: { request } }))
afterEach(() => request.mockReset())

it('preserves forbidden response metadata for presentation and retry decisions', async () => {
  const error = Object.assign(
    new Error('Request failed with status code 403'),
    {
      response: {
        status: 403,
        data: { success: false, message: 'Access denied' },
      },
    }
  )
  request.mockRejectedValue(error)
  const failure = await exportRequest(
    'get',
    '/api/user/employee/export/jobs'
  ).catch((cause) => cause)
  expect(failure).toBe(error)
  expect(getServerErrorStatus(failure)).toBe(403)
})

it('retains the localized business response when an HTTP 200 request fails', async () => {
  const payload = {
    success: false,
    message: 'Please try again.',
    message_key: 'Export failed',
  }
  request.mockResolvedValue({ data: payload })
  const failure = await exportRequest(
    'delete',
    '/api/user/employee/export/jobs/task'
  ).catch((cause) => cause)
  expect(failure).toBeInstanceOf(Error)
  expect(failure).toMatchObject({ cause: payload })
})

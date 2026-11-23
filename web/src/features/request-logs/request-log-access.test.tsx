import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { getRequestLogs, getRequestLogDetail } from './api'
import { RequestLogs } from './index'
import { RequestLogDetailDialog } from './request-log-detail-dialog'
import type { RequestLogItem } from './types'

vi.mock('./api', () => ({
  getRequestLogs: vi.fn(),
  getRequestLogDetail: vi.fn(),
}))

const client = new QueryClient({
  defaultOptions: { queries: { retry: false } },
})
const capture: RequestLogItem = {
  id: 1,
  created_at: 1,
  user_id: 1,
  username: 'viewer',
  token_name: '',
  model_name: 'test-model',
  channel_id: 1,
  method: 'POST',
  url: '/v1/chat/completions',
  status_code: 200,
  ip: '',
  request_id: 'request-one',
  use_time_ms: 1,
  is_stream: false,
  request_body_size: 1,
  response_body_size: 1,
  response_body: 'captured-response',
}
beforeEach(() => {
  vi.mocked(getRequestLogs).mockResolvedValue({
    page: 1,
    page_size: 20,
    total: 1,
    items: [capture],
  })
  vi.mocked(getRequestLogDetail).mockReset().mockResolvedValue(capture)
})
afterEach(() => {
  client.clear()
  useAuthStore.getState().auth.reset()
})

test('unmounts an open capture when detail permission is revoked', async () => {
  const user = {
    id: 1,
    username: 'viewer',
    role: 10,
    permissions: {
      admin_permissions: {
        'admin_menu.request_logs': { view: true, view_detail: true },
      },
    },
  }
  useAuthStore.getState().auth.setUser(user)
  render(
    <QueryClientProvider client={client}>
      <RequestLogs />
    </QueryClientProvider>
  )
  fireEvent.click(await screen.findByRole('button', { name: 'View' }))
  expect(await screen.findByText('captured-response')).toBeVisible()
  expect(screen.getByRole('dialog')).toBeVisible()
  act(() =>
    useAuthStore.getState().auth.setUser({
      ...user,
      permissions: {
        admin_permissions: {
          'admin_menu.request_logs': { view: true, view_detail: false },
        },
      },
    })
  )
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'View' })).toBeDisabled()
})

test('re-fetches role-filtered captures without displaying the previous root response', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'viewer', role: 100 })
  let resolveCapture: (value: RequestLogItem) => void = () => undefined
  vi.mocked(getRequestLogDetail)
    .mockResolvedValueOnce({
      ...capture,
      response_headers: 'root-visible-headers',
    })
    .mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveCapture = resolve
        })
    )
  render(
    <QueryClientProvider client={client}>
      <RequestLogDetailDialog id={1} open onOpenChange={() => undefined} />
    </QueryClientProvider>
  )
  expect(await screen.findByText('root-visible-headers')).toBeVisible()
  act(() =>
    useAuthStore.getState().auth.setUser({
      id: 1,
      username: 'viewer',
      role: 10,
      permissions: {
        admin_permissions: {
          'admin_menu.request_logs': { view: true, view_detail: true },
        },
      },
    })
  )
  expect(screen.queryByText('root-visible-headers')).not.toBeInTheDocument()
  expect(getRequestLogDetail).toHaveBeenCalledTimes(2)
  await act(async () =>
    resolveCapture({ ...capture, response_headers: 'admin-visible-headers' })
  )
  expect(await screen.findByText('admin-visible-headers')).toBeVisible()
})

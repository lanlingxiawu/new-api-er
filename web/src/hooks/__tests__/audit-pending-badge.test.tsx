/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import {
  QueryClient,
  QueryClientProvider,
  focusManager,
} from '@tanstack/react-query'
import { act, cleanup, renderHook } from '@testing-library/react'
import { AxiosError, type AxiosResponse } from 'axios'
import { StrictMode, type ReactNode } from 'react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { createAppQueryClient } from '@/lib/query-client'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'

import {
  auditPendingScope,
  useAuditPendingBadge,
} from '../use-audit-pending-badge'

const employee: AuthUser = {
  id: 1,
  username: 'employee',
  role: 1,
  is_employee: true,
}
const clients: QueryClient[] = []

function view(strict = false, suppliedClient?: QueryClient) {
  const client =
    suppliedClient ??
    new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
  clients.push(client)
  function Wrapper({ children }: { children: ReactNode }) {
    return (
      <QueryClientProvider client={client}>
        {strict ? <StrictMode>{children}</StrictMode> : children}
      </QueryClientProvider>
    )
  }
  return {
    ...renderHook(() => [useAuditPendingBadge(), useAuditPendingBadge()], {
      wrapper: Wrapper,
    }),
    client,
  }
}

beforeEach(() => {
  vi.useFakeTimers()
  focusManager.setFocused(true)
  useAuthStore.setState((state) => ({
    auth: {
      ...state.auth,
      user: employee,
      session: {
        sid: 'badge-session',
        current: true,
        login_method: 'password',
        ip: '',
        user_agent: '',
        created_at: 1,
        last_active_at: 1,
        expires_at: 9999999999,
      },
    },
  }))
})

it('loads through the real HTTP wrapper after a Strict Mode cancellation', async () => {
  const original = api.defaults.adapter
  api.defaults.adapter = async (config) => ({
    config,
    status: 200,
    statusText: 'OK',
    headers: {},
    data: { success: true, data: { count: 3 } },
  })
  try {
    const { result } = view(true)
    await act(() => vi.advanceTimersByTimeAsync(10))
    expect(result.current[0]?.value).toBe('3')
  } finally {
    api.defaults.adapter = original
  }
})

afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  useAuthStore.setState(useAuthStore.getInitialState(), true)
  focusManager.setFocused(undefined)
  vi.restoreAllMocks()
  vi.useRealTimers()
})

it('prioritizes authorized admins and keeps employee fallback separate', () => {
  expect(auditPendingScope(null)).toBeUndefined()
  expect(auditPendingScope({ ...employee, is_employee: false })).toBeUndefined()
  expect(auditPendingScope({ ...employee, role: 10 })).toBe('customers')
  expect(auditPendingScope({ ...employee, role: 100 })).toBe('security')
  expect(
    auditPendingScope({
      ...employee,
      role: 10,
      permissions: {
        admin_permissions: {
          audit: { read: true },
          audit_security: { read: true },
        },
      },
    })
  ).toBe('security')
})

it.each([
  [0, undefined],
  [1, '1'],
  [99, '99'],
  [100, '99+'],
] as const)(
  'renders capped count %s and deduplicates observers',
  async (count, value) => {
    const get = vi
      .spyOn(api, 'get')
      .mockResolvedValue({ data: { success: true, data: { count } } })
    const { result } = view()
    await act(() => vi.advanceTimersByTimeAsync(10))
    expect(get).toHaveBeenCalledTimes(1)
    expect(result.current[0]?.value).toBe(value)
    expect(result.current[1]?.value).toBe(value)
    if (count) {
      expect(result.current[0]?.search).toEqual({
        scope: 'customers',
        tab: 'risk',
        view: 'unresolved',
      })
    }
  }
)

it('polls each minute, pauses in the background, and refreshes stale data on focus', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data: { count: 2 } } })
  view()
  await act(() => vi.advanceTimersByTimeAsync(59_900))
  expect(get).toHaveBeenCalledTimes(1)
  await act(() => vi.advanceTimersByTimeAsync(200))
  expect(get).toHaveBeenCalledTimes(2)
  act(() => focusManager.setFocused(false))
  await act(() => vi.advanceTimersByTimeAsync(120_000))
  expect(get).toHaveBeenCalledTimes(2)
  await act(async () => {
    focusManager.setFocused(true)
    await vi.advanceTimersByTimeAsync(10)
  })
  expect(get).toHaveBeenCalledTimes(3)
})

it('hides stale counts on errors and stops polling after access is denied', async () => {
  const denied = new AxiosError('forbidden', undefined, undefined, undefined, {
    status: 403,
  } as AxiosResponse)
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValueOnce({ data: { success: true, data: { count: 2 } } })
    .mockRejectedValue(denied)
  const { result, client } = view()
  await act(() => vi.advanceTimersByTimeAsync(10))
  expect(result.current[0]?.value).toBe('2')
  await act(() => vi.advanceTimersByTimeAsync(60_000))
  expect(result.current[0]).toBeUndefined()
  await act(async () => {
    await client.invalidateQueries({ queryKey: ['customer-audit'] })
    await vi.advanceTimersByTimeAsync(120_000)
  })
  expect(get).toHaveBeenCalledTimes(2)
})

it('invalidates immediately after processing and aborts on identity change', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValueOnce({ data: { success: true, data: { count: 1 } } })
    .mockResolvedValue({ data: { success: true, data: { count: 0 } } })
  const { result, client } = view()
  await act(() => vi.advanceTimersByTimeAsync(10))
  await act(async () => {
    await client.invalidateQueries({ queryKey: ['customer-audit', 1] })
    await vi.advanceTimersByTimeAsync(10)
  })
  expect(get).toHaveBeenCalledTimes(2)
  expect(result.current[0]).toBeUndefined()
  let signal: AbortSignal | undefined
  get.mockImplementation((_url, config) => {
    signal = config?.signal as AbortSignal
    return new Promise(() => {})
  })
  act(() => {
    void client.invalidateQueries({ queryKey: ['customer-audit', 1] })
  })
  await act(() => vi.advanceTimersByTimeAsync(10))
  act(() => useAuthStore.getState().auth.reset())
  expect(signal?.aborted).toBe(true)
  expect(result.current[0]).toBeUndefined()
})

it('keeps a failed background count on the current page and recovers at the next minute', async () => {
  const failed = new AxiosError(
    'unavailable',
    undefined,
    undefined,
    undefined,
    {
      status: 500,
    } as AxiosResponse
  )
  vi.spyOn(api, 'get')
    .mockResolvedValueOnce({ data: { success: true, data: { count: 3 } } })
    .mockRejectedValueOnce(failed)
    .mockResolvedValue({ data: { success: true, data: { count: 1 } } })
  const redirect = vi.fn()
  const client = createAppQueryClient(redirect)
  const { result } = view(false, client)
  await act(() => vi.advanceTimersByTimeAsync(10))
  expect(result.current[0]?.value).toBe('3')
  await act(() => vi.advanceTimersByTimeAsync(60_000))
  expect(result.current[0]).toBeUndefined()
  expect(redirect).not.toHaveBeenCalled()
  await act(() => vi.advanceTimersByTimeAsync(60_000))
  expect(result.current[0]?.value).toBe('1')
  // Foreground queries retain the application's existing error navigation.
  await expect(
    client.fetchQuery({
      queryKey: ['foreground-error'],
      queryFn: () => Promise.reject(failed),
      retry: false,
      meta: { errorToast: false },
    })
  ).rejects.toBe(failed)
  expect(redirect).toHaveBeenCalledOnce()
})

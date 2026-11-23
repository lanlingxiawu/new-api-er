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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { forwardRef, type ComponentProps } from 'react'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { SelfSecurity } from '.'
import { AuditLogs } from '..'
import type { SelfSecurityAlert } from './api'

// Navigation is verified in the browser; keep the real button/link composition.
vi.mock('@tanstack/react-router', async (original) => ({
  ...(await original<typeof import('@tanstack/react-router')>()),
  Link: forwardRef<HTMLAnchorElement, ComponentProps<'a'> & { to: string }>(
    (props, ref) => <a {...props} href={props.to} ref={ref} />
  ),
}))

const alert: SelfSecurityAlert = {
  id: 71,
  rule: 'new_login_source',
  severity: 2,
  status: 'open',
  first_seen: 1794960000,
  last_seen: 1794960010,
}

function renderSelf() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const view = render(
    <QueryClientProvider client={client}>
      <SelfSecurity userID={19} />
    </QueryClientProvider>
  )
  return { client, ...view }
}

afterEach(() => {
  cleanup()
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

it('uses only the personal API, follows cursors, and resets pagination on refresh', async () => {
  vi.spyOn(Date, 'now').mockReturnValue(
    new Date('2026-11-23T13:39:46').getTime()
  )
  const get = vi.spyOn(api, 'get').mockImplementation(async (_url, config) => ({
    data: {
      success: true,
      data: (config?.params as { cursor?: string })?.cursor
        ? { items: [{ ...alert, id: 72 }], next_cursor: '' }
        : { items: [alert], next_cursor: 'own-page-2' },
    },
  }))
  const { client } = renderSelf()
  await screen.findByRole('button', { name: /#71/ })
  expect(
    screen.queryByRole('columnheader', { name: 'Handling status' })
  ).not.toBeInTheDocument()
  expect(screen.queryByText('Not handled')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Previous page' })).toBeDisabled()
  expect(
    screen.getByRole('link', { name: 'Security & Access' })
  ).toHaveAttribute('href', '/security')
  expect(screen.getByRole('link', { name: 'API Keys' })).toHaveAttribute(
    'href',
    '/keys'
  )
  await userEvent.click(screen.getByRole('button', { name: 'Next page' }))
  await screen.findByRole('button', { name: /#72/ })
  expect(screen.queryByRole('button', { name: /#71/ })).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Next page' })).toBeDisabled()
  await userEvent.click(screen.getByRole('button', { name: 'Previous page' }))
  await screen.findByRole('button', { name: /#71/ })
  await userEvent.click(screen.getByRole('button', { name: 'Next page' }))
  await screen.findByRole('button', { name: /#72/ })
  await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  await screen.findByRole('button', { name: /#71/ })
  expect(screen.getByRole('button', { name: 'Previous page' })).toBeDisabled()
  for (const [url, config] of get.mock.calls) {
    expect(url).toBe('/api/audit/self/security/alerts')
    expect(config?.signal).toBeInstanceOf(AbortSignal)
    expect(config?.params).not.toHaveProperty('user_id')
    expect(config?.params).toMatchObject({
      start: new Date('2026-11-22T00:00:00').getTime() / 1000,
      end: new Date('2026-11-23T23:59:59').getTime() / 1000,
    })
  }
  client.clear()
})

it('includes later records on refresh, advances full days, and preserves custom times until reset', async () => {
  const clock = vi
    .spyOn(Date, 'now')
    .mockReturnValue(new Date('2026-11-23T13:39:46').getTime())
  const later = {
    ...alert,
    last_seen: new Date('2026-11-23T14:00:00').getTime() / 1000,
  }
  const get = vi.spyOn(api, 'get').mockImplementation(async (_url, config) => {
    const range = config?.params as { start: number; end: number }
    const visible =
      later.last_seen <= Date.now() / 1000 &&
      later.last_seen >= range.start &&
      later.last_seen <= range.end
    return {
      data: {
        success: true,
        data: { items: visible ? [later] : [], next_cursor: '' },
      },
    }
  })
  const { client } = renderSelf()
  await screen.findByText('No records in this time range')
  expect(
    screen.getByRole('button', {
      name: '2026-11-22 00:00:00 ~ 2026-11-23 23:59:59',
    })
  ).toBeVisible()
  clock.mockReturnValue(new Date('2026-11-23T14:01:00').getTime())
  await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  await screen.findByRole('button', { name: /#71/ })

  clock.mockReturnValue(new Date('2026-11-24T00:01:00').getTime())
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(get.mock.lastCall?.[1]?.params).toMatchObject({
      start: new Date('2026-11-23T00:00:00').getTime() / 1000,
      end: new Date('2026-11-24T23:59:59').getTime() / 1000,
      cursor: '',
    })
  )
  await userEvent.click(screen.getByRole('button', { name: / ~ / }))
  fireEvent.change(screen.getByLabelText('Start Time'), {
    target: { value: '2026-11-20T10:15:30' },
  })
  fireEvent.change(screen.getByLabelText('End Time'), {
    target: { value: '2026-11-20T11:45:15' },
  })
  await userEvent.click(screen.getByRole('button', { name: 'Confirm' }))
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(get.mock.lastCall?.[1]?.params).toMatchObject({
      start: new Date('2026-11-20T10:15:30').getTime() / 1000,
      end: new Date('2026-11-20T11:45:15').getTime() / 1000,
    })
  )
  get.mockClear()
  clock.mockReturnValue(new Date('2026-11-25T09:00:00').getTime())
  await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  await waitFor(() =>
    expect(get.mock.lastCall?.[1]?.params).toMatchObject({
      start: new Date('2026-11-20T10:15:30').getTime() / 1000,
      end: new Date('2026-11-20T11:45:15').getTime() / 1000,
    })
  )
  await userEvent.click(screen.getByRole('button', { name: 'Reset' }))
  await waitFor(() =>
    expect(get.mock.lastCall?.[1]?.params).toMatchObject({
      start: new Date('2026-11-24T00:00:00').getTime() / 1000,
      end: new Date('2026-11-25T23:59:59').getTime() / 1000,
      cursor: '',
    })
  )
  client.clear()
})

it('shows read-only evidence and outcomes, clears stale details after failure, and retries', async () => {
  let failDetail = false
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url.endsWith('/71')) {
      if (failDetail) {
        return {
          data: { success: false, message: 'private backend internals' },
        }
      }
      return {
        data: {
          success: true,
          data: {
            alert,
            events: [
              {
                action: 'login',
                at: alert.last_seen,
                ip: '192.0.2.71',
                success: true,
              },
            ],
            responses: [
              {
                id: 9,
                action: 'clear_api_keys',
                outcome: 'succeeded',
                created_at: alert.last_seen,
                affected_count: 3,
              },
            ],
          },
        },
      }
    }
    return {
      data: { success: true, data: { items: [alert], next_cursor: '' } },
    }
  })
  const post = vi.spyOn(api, 'post')
  const put = vi.spyOn(api, 'put')
  const remove = vi.spyOn(api, 'delete')
  const { client } = renderSelf()
  await userEvent.click(await screen.findByRole('button', { name: /#71/ }))
  const dialog = await screen.findByRole('dialog', { name: 'Security alert' })
  expect(await within(dialog).findByText('192.0.2.71')).toBeVisible()
  expect(within(dialog).queryByText('Handling status')).not.toBeInTheDocument()
  expect(within(dialog).queryByText('Not handled')).not.toBeInTheDocument()
  expect(within(dialog).getByText('Completed')).toBeVisible()
  expect(within(dialog).getByText('API keys cleared: 3')).toBeVisible()
  expect(
    within(dialog).queryByRole('button', {
      name: /Save|Delete|False positive|Ban|Clear API/,
    })
  ).not.toBeInTheDocument()
  failDetail = true
  await userEvent.click(within(dialog).getByRole('button', { name: 'Refresh' }))
  await within(dialog).findByText('Failed to load audit records')
  expect(within(dialog).queryByText('192.0.2.71')).not.toBeInTheDocument()
  expect(
    within(dialog).queryByText('private backend internals')
  ).not.toBeInTheDocument()
  failDetail = false
  await userEvent.click(within(dialog).getByRole('button', { name: 'Retry' }))
  await within(dialog).findByText('192.0.2.71')
  await userEvent.keyboard('{Escape}')
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  expect(
    get.mock.calls.every(([url]) => url.startsWith('/api/audit/self/security/'))
  ).toBe(true)
  expect(post).not.toHaveBeenCalled()
  expect(put).not.toHaveBeenCalled()
  expect(remove).not.toHaveBeenCalled()
  client.clear()
})

it('hides old rows and an open dialog when the list loses access', async () => {
  let denied = false
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (denied) throw new Error('forbidden')
    return {
      data: {
        success: true,
        data: url.endsWith('/71')
          ? { alert, events: [], responses: [] }
          : { items: [alert], next_cursor: 'cursor' },
      },
    }
  })
  const { client } = renderSelf()
  await userEvent.click(await screen.findByRole('button', { name: /#71/ }))
  await screen.findByRole('dialog')
  denied = true
  await act(() =>
    client.invalidateQueries({ queryKey: ['self-security', 19, 'alerts'] })
  )
  await screen.findByText('Failed to load audit records')
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /#71/ })).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Next page' })).toBeDisabled()
  client.clear()
})

it.each([
  { name: 'user', role: 1, is_employee: false, all: false, security: false },
  { name: 'employee', role: 1, is_employee: true, all: false, security: false },
  {
    name: 'admin without audit',
    role: 10,
    is_employee: false,
    all: false,
    security: false,
  },
  {
    name: 'audit reader',
    role: 10,
    is_employee: false,
    all: true,
    security: false,
  },
  {
    name: 'security reader',
    role: 10,
    is_employee: false,
    all: true,
    security: true,
  },
  { name: 'root', role: 100, is_employee: false, all: true, security: true },
])(
  'exposes personal security without granting management access for $name',
  async (role) => {
    useAuthStore.getState().auth.setUser({
      id: 19,
      username: 'fixture',
      role: role.role,
      is_employee: role.is_employee,
      permissions: {
        admin_permissions: {
          audit: { read: role.all },
          audit_security: { read: role.security },
        },
      },
    })
    const get = vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [], total: 0, next_cursor: '' } },
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <AuditLogs />
      </QueryClientProvider>
    )
    expect(!!screen.queryByRole('tab', { name: 'All' })).toBe(role.all)
    expect(!!screen.queryByRole('tab', { name: 'Security monitoring' })).toBe(
      role.security
    )
    await userEvent.click(screen.getByRole('tab', { name: 'My security' }))
    expect(
      await screen.findByText('No records in this time range')
    ).toBeVisible()
    expect(get).toHaveBeenCalledWith(
      '/api/audit/self/security/alerts',
      expect.anything()
    )
    expect(
      get.mock.calls.some(([url]) => url.startsWith('/api/audit/security'))
    ).toBe(false)
    client.clear()
  }
)

it('resets details, cursor, and cached personal data when the signed-in account changes', async () => {
  useAuthStore.getState().auth.setUser({ id: 19, username: 'first', role: 1 })
  const get = vi.spyOn(api, 'get').mockImplementation(async (url, config) => {
    if (url.endsWith('/71')) {
      return {
        data: { success: true, data: { alert, events: [], responses: [] } },
      }
    }
    const id = useAuthStore.getState().auth.user?.id === 19 ? 71 : 91
    return {
      data: {
        success: true,
        data: {
          items: url.includes('/security/') ? [{ ...alert, id }] : [],
          total: 0,
          next_cursor: (config?.params as { cursor?: string })?.cursor
            ? ''
            : 'next',
        },
      },
    }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <AuditLogs />
    </QueryClientProvider>
  )
  await userEvent.click(screen.getByRole('tab', { name: 'My security' }))
  await screen.findByRole('button', { name: /#71/ })
  await userEvent.click(screen.getByRole('button', { name: 'Next page' }))
  await userEvent.click(await screen.findByRole('button', { name: /#71/ }))
  await screen.findByRole('dialog')
  act(() =>
    useAuthStore
      .getState()
      .auth.setUser({ id: 20, username: 'second', role: 1 })
  )
  await screen.findByRole('button', { name: /#91/ })
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /#71/ })).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Previous page' })).toBeDisabled()
  expect(get.mock.lastCall?.[1]?.params).toMatchObject({ cursor: '' })
  await waitFor(() =>
    expect(
      client.getQueriesData({ queryKey: ['self-security', 19] })
    ).toHaveLength(0)
  )
  client.clear()
})

it('aborts an unfinished personal request when leaving the view', async () => {
  let signal: AbortSignal | undefined
  vi.spyOn(api, 'get').mockImplementation((_url, config) => {
    signal = config?.signal as AbortSignal
    return new Promise(() => {})
  })
  const { unmount, client } = renderSelf()
  await waitFor(() => expect(signal).toBeInstanceOf(AbortSignal))
  unmount()
  expect(signal?.aborted).toBe(true)
  client.clear()
})

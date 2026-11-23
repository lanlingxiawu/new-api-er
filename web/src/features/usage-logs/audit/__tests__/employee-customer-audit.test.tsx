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
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router'
import {
  act,
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, type AxiosResponse } from 'axios'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { NavGroup } from '@/components/layout/components/nav-group'
import { SidebarProvider } from '@/components/ui/sidebar'
import { useAuditPendingBadge } from '@/hooks/use-audit-pending-badge'
import { useSidebarData } from '@/hooks/use-sidebar-data'
import { api } from '@/lib/api'
import { Route as AuditPageRoute } from '@/routes/_authenticated/usage-logs/audit'
import { useAuthStore } from '@/stores/auth-store'

import { AuditLogs } from '..'
import { securityRules } from '../security/lib/security-rules'

const employee = { id: 11, username: 'employee', role: 1, is_employee: true }
const customers = [
  { id: 901, customer_user_id: 31, username: 'customer-a' },
  { id: 902, customer_user_id: 32, username: 'customer-b' },
]
const activity = {
  event_id: 'customer-event',
  created_at: 200,
  category: 'operation',
  action: 'token.create',
  success: true,
  ip: '192.0.2.3',
  client: 'Edge',
  source: 'session',
  request_id: 'customer-request',
  target: { resource: 'token', id: '81', name: 'Customer key' },
}
const clients: QueryClient[] = []

function view(props: Parameters<typeof AuditLogs>[0] = {}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <AuditLogs {...props} />
    </QueryClientProvider>
  )
  return client
}

async function navigateFromBadge(readCount = () => 2) {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  useAuthStore.setState((state) => ({
    auth: {
      ...state.auth,
      session: {
        sid: 'navigation-test',
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
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url.endsWith('/pending-count')) {
      return { data: { success: true, data: { count: readCount() } } }
    }
    return url.endsWith('/security/alerts')
      ? { data: { success: true, data: { items: [], next_cursor: '' } } }
      : auditResponse(url)
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  clients.push(client)
  const root = createRootRoute({
    component: function AuditLayout() {
      const badge = useAuditPendingBadge()
      const { navGroups } = useSidebarData({ auditPending: badge })
      const items = navGroups
        .flatMap((group) => group.items)
        .filter((item) => item.url === '/usage-logs/audit')
      return (
        <SidebarProvider>
          <NavGroup title='General' items={items} />
          <Outlet />
        </SidebarProvider>
      )
    },
  })
  const authenticated = createRoute({
    getParentRoute: () => root,
    id: '_authenticated',
  })
  const audit = AuditPageRoute.update({
    getParentRoute: () => authenticated,
    id: '/usage-logs/audit',
    path: '/usage-logs/audit',
  } as never)
  const router = createRouter({
    routeTree: root.addChildren([authenticated.addChildren([audit])]),
    history: createMemoryHistory({
      initialEntries: [
        '/usage-logs/audit?scope=customers&tab=risk&view=unresolved',
      ],
    }),
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  await userEvent.click(
    await screen.findByRole('link', { name: /^Audit Logs:/ })
  )
  await screen.findByText('No unprocessed risk records')
  return router
}

it('refreshes the count immediately on each badge navigation', async () => {
  const count = vi.fn().mockReturnValueOnce(2).mockReturnValue(1)
  await navigateFromBadge(count)
  expect(
    await screen.findByRole('link', {
      name: 'Audit Logs: Unprocessed customer risk records: 1',
    })
  ).toBeVisible()
  expect(count).toHaveBeenCalledTimes(2)
  count.mockReturnValue(0)
  await userEvent.click(screen.getByRole('link', { name: /^Audit Logs:/ }))
  await waitFor(() =>
    expect(
      screen.queryByRole('link', { name: /^Audit Logs:/ })
    ).not.toBeInTheDocument()
  )
  expect(count).toHaveBeenCalledTimes(3)
  expect(screen.getByText('No unprocessed risk records')).toBeVisible()
})

it('retains the selected customer across routed status changes after entering through the badge', async () => {
  await navigateFromBadge()
  await chooseCustomer()
  await userEvent.click(
    screen.getByRole('combobox', { name: 'Handling status' })
  )
  await userEvent.click(
    await screen.findByRole('option', { name: 'Processed' })
  )
  await screen.findByText('No processed risk records in this event time range')
  expect(screen.getByRole('combobox', { name: 'Customer' })).toHaveValue(
    'customer-a #31'
  )
  await userEvent.click(
    screen.getByRole('combobox', { name: 'Handling status' })
  )
  await userEvent.click(
    await screen.findByRole('option', { name: 'Unprocessed' })
  )
  expect(
    await screen.findByText('No matching unprocessed risk records')
  ).toBeVisible()
  expect(screen.getByRole('combobox', { name: 'Customer' })).toHaveValue(
    'customer-a #31'
  )
})

it('opens and resets the badge destination when randomUUID is unavailable', async () => {
  vi.stubGlobal('crypto', {
    getRandomValues: crypto.getRandomValues.bind(crypto),
  })
  const router = await navigateFromBadge()
  const firstVisit = router.state.location.state.auditPendingVisit
  await chooseCustomer()
  await userEvent.click(screen.getByRole('link', { name: /^Audit Logs:/ }))
  await waitFor(() => {
    expect(screen.getByRole('combobox', { name: 'Customer' })).toHaveValue(
      'All customers'
    )
    expect(router.state.location.state.auditPendingVisit).not.toEqual(
      firstVisit
    )
  })
})

it('opens the pending deep link directly and preserves the risk view when selecting a customer', async () => {
  vi.mocked(api.get).mockImplementation(async (url) =>
    url.endsWith('/security/alerts')
      ? { data: { success: true, data: { items: [], next_cursor: '' } } }
      : auditResponse(url)
  )
  view({ search: { scope: 'customers', tab: 'risk', view: 'unresolved' } })
  expect(await screen.findByText('No unprocessed risk records')).toBeVisible()
  expect(screen.getByRole('tab', { name: 'Risk records' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  expect(api.get).toHaveBeenCalledWith(
    '/api/user/employee/customers/security/alerts',
    expect.objectContaining({ params: { view: 'unresolved', cursor: '' } })
  )
  expect(
    vi
      .mocked(api.get)
      .mock.calls.some(
        ([url]) => url === '/api/audit/self' || url.endsWith('/customers/audit')
      )
  ).toBe(false)
  await chooseCustomer()
  expect(
    await screen.findByText('No matching unprocessed risk records')
  ).toBeVisible()
  expect(api.get).toHaveBeenCalledWith(
    '/api/user/employee/customers/31/security/alerts',
    expect.objectContaining({ params: { view: 'unresolved', cursor: '' } })
  )
  expect(screen.getByRole('tab', { name: 'Risk records' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
})

async function chooseCustomer(name = 'customer-a #31') {
  await userEvent.click(screen.getByRole('combobox', { name: 'Customer' }))
  await userEvent.click(await screen.findByRole('option', { name }))
}

async function auditResponse(url: string) {
  if (url.startsWith('/api/user/employee/customers?')) {
    return {
      data: {
        success: true,
        data: { items: customers, total: 2, page: 1, page_size: 20 },
      },
    }
  }
  if (url === '/api/audit/self') {
    return {
      data: {
        success: true,
        data: {
          items: [
            {
              ...activity,
              target: { ...activity.target, name: 'Employee key' },
            },
          ],
          total: 1,
        },
      },
    }
  }
  if (/\/customers\/(31|32)\/audit$/.test(url)) {
    return {
      data: { success: true, data: { items: [activity], next_cursor: '' } },
    }
  }
  if (url === '/api/user/employee/customers/audit') {
    return {
      data: {
        success: true,
        data: {
          items: customers.map((customer, index) => ({
            ...activity,
            event_id: `aggregate-${index}`,
            customer: {
              id: customer.customer_user_id,
              username: customer.username,
              display_name: '',
            },
          })),
          next_cursor: '',
        },
      },
    }
  }
  throw new Error(`Unexpected endpoint: ${url}`)
}

beforeEach(() => {
  vi.stubGlobal('localStorage', {
    getItem: () => null,
    setItem: () => undefined,
    removeItem: () => undefined,
  })
  useAuthStore.getState().auth.setUser(employee)
  vi.spyOn(api, 'get').mockImplementation(auditResponse)
})

afterEach(() => {
  cleanup()
  for (const client of clients.splice(0)) client.clear()
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

it('loads all customers immediately, labels each record, and supports selecting and clearing a customer', async () => {
  view()
  expect(await screen.findByText('Employee key')).toBeVisible()
  expect(screen.getByRole('columnheader', { name: 'Username' })).toBeVisible()
  expect(screen.getByRole('cell', { name: 'employee' })).toBeVisible()
  expect(screen.queryByRole('tab', { name: 'All' })).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('tab', { name: 'My Customers' }))
  expect(
    await screen.findByRole('cell', { name: 'customer-a #31' })
  ).toBeVisible()
  expect(screen.getByRole('cell', { name: 'customer-b #32' })).toBeVisible()
  expect(screen.getByRole('combobox', { name: 'Customer' })).toHaveValue(
    'All customers'
  )
  expect(
    vi
      .mocked(api.get)
      .mock.calls.some(([url]) =>
        url.startsWith('/api/user/employee/customers?')
      )
  ).toBe(false)
  await userEvent.click(screen.getAllByRole('button', { name: 'Details' })[1])
  expect(
    within(
      await screen.findByRole('dialog', { name: 'Log Details' })
    ).getByText('customer-b #32')
  ).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: 'Close' }))
  await chooseCustomer()
  expect(
    await screen.findByRole('cell', { name: 'customer-a #31' })
  ).toBeVisible()
  expect(screen.getByRole('columnheader', { name: 'Customer' })).toBeVisible()
  expect(screen.queryByText('Employee key')).not.toBeInTheDocument()
  expect(api.get).toHaveBeenCalledWith(
    '/api/user/employee/customers/31/audit',
    expect.objectContaining({ signal: expect.any(AbortSignal) })
  )
  expect(
    screen.queryByRole('combobox', { name: 'Actor identity' })
  ).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Details' }))
  const detail = await screen.findByRole('dialog', { name: 'Log Details' })
  expect(within(detail).getByText('customer-a #31')).toBeVisible()
  expect(within(detail).getByText('Customer key')).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: 'Close' }))
  await userEvent.click(screen.getByRole('button', { name: 'Clear selection' }))
  expect(
    await screen.findByRole('cell', { name: 'customer-b #32' })
  ).toBeVisible()
  expect(screen.getByRole('cell', { name: 'customer-a #31' })).toBeVisible()
})

it.each(['Operation logs', 'Risk records'])(
  'uses the page footer to navigate three customer %s pages and reset filters',
  async (tab) => {
    const endpoint = tab === 'Operation logs' ? 'audit' : 'security/alerts'
    vi.mocked(api.get).mockImplementation(async (url, config) => {
      if (url === `/api/user/employee/customers/${endpoint}`) {
        const cursor =
          (config?.params as { cursor?: string } | undefined)?.cursor ?? ''
        const index = ['', 'page-2', 'page-3'].indexOf(cursor)
        return {
          data: {
            success: true,
            data: {
              items: [],
              next_cursor: index < 2 ? `page-${index + 2}` : '',
            },
          },
        }
      }
      return auditResponse(url)
    })
    view()
    await userEvent.click(screen.getByRole('tab', { name: 'My Customers' }))
    if (tab === 'Risk records') {
      await userEvent.click(screen.getByRole('tab', { name: tab }))
    }
    const pagination = await screen.findByRole('navigation', { name: 'Page' })
    expect(
      pagination.closest('[data-scroll-restoration-id="section-page-content"]')
    ).toBeNull()
    expect(within(pagination).getByText('Page 1')).toBeVisible()
    expect(within(pagination).queryByText('Total:')).not.toBeInTheDocument()
    for (const page of [2, 3]) {
      await waitFor(() =>
        expect(
          within(pagination).getByRole('button', { name: 'Go to next page' })
        ).toBeEnabled()
      )
      await userEvent.click(
        within(pagination).getByRole('button', { name: 'Go to next page' })
      )
      await waitFor(() =>
        expect(api.get).toHaveBeenLastCalledWith(
          `/api/user/employee/customers/${endpoint}`,
          expect.objectContaining({
            params: expect.objectContaining({ cursor: `page-${page}` }),
          })
        )
      )
      expect(within(pagination).getByText(`Page ${page}`)).toBeVisible()
    }
    await waitFor(() =>
      expect(
        within(pagination).getByRole('button', { name: 'Go to previous page' })
      ).toBeEnabled()
    )
    expect(
      within(pagination).getByRole('button', { name: 'Go to next page' })
    ).toBeDisabled()
    await userEvent.click(
      within(pagination).getByRole('button', { name: 'Go to previous page' })
    )
    expect(within(pagination).getByText('Page 2')).toBeVisible()
    await waitFor(() =>
      expect(
        within(pagination).getByRole('button', { name: 'Go to first page' })
      ).toBeEnabled()
    )
    await userEvent.click(
      within(pagination).getByRole('button', { name: 'Go to first page' })
    )
    expect(within(pagination).getByText('Page 1')).toBeVisible()
    await waitFor(() =>
      expect(
        within(pagination).getByRole('button', { name: 'Go to next page' })
      ).toBeEnabled()
    )
    await userEvent.click(
      within(pagination).getByRole('button', { name: 'Go to next page' })
    )
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Search' })).toBeEnabled()
    )
    await userEvent.click(screen.getByRole('button', { name: 'Search' }))
    expect(within(pagination).getByText('Page 1')).toBeVisible()
    await waitFor(() =>
      expect(api.get).toHaveBeenLastCalledWith(
        `/api/user/employee/customers/${endpoint}`,
        expect.objectContaining({
          params: expect.objectContaining({ cursor: '' }),
        })
      )
    )
    expect(
      within(pagination).getByRole('button', { name: 'Go to previous page' })
    ).toBeDisabled()
  }
)

it('searches and paginates only the current employee customer candidates', async () => {
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (!url.startsWith('/api/user/employee/customers?')) {
      return auditResponse(url)
    }
    const params = new URL(url, 'http://localhost').searchParams
    const page = Number(params.get('page'))
    const keyword = params.get('keyword')
    return {
      data: {
        success: true,
        data: {
          items: keyword ? [customers[1]] : [customers[page === 1 ? 0 : 1]],
          total: keyword ? 1 : 21,
          page,
          page_size: 20,
        },
      },
    }
  })
  view()
  await userEvent.click(screen.getByRole('tab', { name: 'My Customers' }))
  await userEvent.click(screen.getByRole('combobox', { name: 'Customer' }))
  expect(
    await screen.findByRole('option', { name: 'customer-a #31' })
  ).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: 'Load more' }))
  expect(
    await screen.findByRole('option', { name: 'customer-b #32' })
  ).toBeVisible()
  expect(api.get).toHaveBeenCalledWith(
    '/api/user/employee/customers?page=2&page_size=20',
    { signal: expect.any(AbortSignal) }
  )
  await userEvent.type(
    screen.getByRole('combobox', { name: 'Customer' }),
    'customer-b'
  )
  await waitFor(() =>
    expect(api.get).toHaveBeenCalledWith(
      '/api/user/employee/customers?page=1&page_size=20&keyword=customer-b',
      { signal: expect.any(AbortSignal) }
    )
  )
  expect(
    screen.queryByRole('option', { name: 'customer-a #31' })
  ).not.toBeInTheDocument()
  await userEvent.click(
    await screen.findByRole('option', { name: 'customer-b #32' })
  )
  expect(
    await screen.findByRole('cell', { name: 'customer-b #32' })
  ).toBeVisible()
})

it('clears the previous customer while loading another and cancels requests when employee access is revoked', async () => {
  let pendingSignal: AbortSignal | undefined
  vi.mocked(api.get).mockImplementation((url, config) => {
    if (url === '/api/user/employee/customers/32/audit') {
      pendingSignal = config?.signal as AbortSignal
      return new Promise(() => {})
    }
    return auditResponse(url)
  })
  view()
  await userEvent.click(screen.getByRole('tab', { name: 'My Customers' }))
  await chooseCustomer()
  expect(await screen.findByText('192.0.2.3')).toBeVisible()
  await chooseCustomer('customer-b #32')
  await waitFor(() => expect(pendingSignal).toBeDefined())
  expect(screen.queryByText('192.0.2.3')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('cell', { name: 'customer-a #31' })
  ).not.toBeInTheDocument()
  act(() =>
    useAuthStore.getState().auth.setUser({ ...employee, is_employee: false })
  )
  expect(
    screen.queryByRole('tab', { name: 'My Customers' })
  ).not.toBeInTheDocument()
  expect(screen.getByRole('tab', { name: 'Only Mine' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  await waitFor(() => expect(pendingSignal?.aborted).toBe(true))
})

it('removes customer data after a 403 and keeps the customer filter available', async () => {
  const client = view()
  await userEvent.click(screen.getByRole('tab', { name: 'My Customers' }))
  await chooseCustomer()
  expect(await screen.findByText('192.0.2.3')).toBeVisible()
  vi.mocked(api.get).mockImplementation((url) => {
    if (url === '/api/user/employee/customers/31/audit') {
      return Promise.reject(
        new AxiosError('Forbidden', 'ERR_BAD_REQUEST', undefined, undefined, {
          status: 403,
        } as AxiosResponse)
      )
    }
    return auditResponse(url)
  })
  await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  expect(await screen.findByText('Customer access has changed')).toBeVisible()
  expect(screen.queryByText('192.0.2.3')).not.toBeInTheDocument()
  expect(
    client.getQueriesData({ queryKey: ['customer-audit', employee.id, 31] })
  ).toEqual([])
  await chooseCustomer('customer-b #32')
  expect(
    await screen.findByRole('cell', { name: 'customer-b #32' })
  ).toBeVisible()
  expect(
    screen.queryByText('Customer access has changed')
  ).not.toBeInTheDocument()
})

it('does not reuse customer selection or candidates after switching employee accounts', async () => {
  view()
  await userEvent.click(screen.getByRole('tab', { name: 'My Customers' }))
  await chooseCustomer()
  expect(await screen.findByText('Customer key')).toBeVisible()
  vi.mocked(api.get).mockImplementation((url) =>
    url.startsWith('/api/user/employee/customers?') ||
    url === '/api/user/employee/customers/audit'
      ? new Promise(() => {})
      : auditResponse(url)
  )
  act(() =>
    useAuthStore
      .getState()
      .auth.setUser({ ...employee, id: 12, username: 'employee-2' })
  )
  expect(screen.getByRole('combobox', { name: 'Customer' })).toHaveValue(
    'All customers'
  )
  expect(screen.queryByText('Customer key')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('combobox', { name: 'Customer' }))
  expect(
    screen.queryByRole('option', { name: 'customer-a #31' })
  ).not.toBeInTheDocument()
})

it('shows customer candidate failures with retry and never interprets them as an empty successful list', async () => {
  vi.mocked(api.get).mockImplementation((url) =>
    url.startsWith('/api/user/employee/customers?')
      ? Promise.resolve({ data: { success: false, message: 'Unavailable' } })
      : auditResponse(url)
  )
  view()
  await userEvent.click(screen.getByRole('tab', { name: 'My Customers' }))
  await userEvent.click(screen.getByRole('combobox', { name: 'Customer' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Failed to load customers'
  )
  expect(screen.queryByRole('option')).not.toBeInTheDocument()
  vi.mocked(api.get).mockImplementation(auditResponse)
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(
    await screen.findByRole('option', { name: 'customer-a #31' })
  ).toBeVisible()
})

it('keeps aggregate risk actions and related audit scoped to the row customer', async () => {
  const alerts = customers.map((customer, index) => ({
    id: 71 + index,
    rule: 'session_reuse',
    severity: 3,
    status: 'open',
    first_seen: 100,
    last_seen: 200,
    updated_at: 200,
    occurrences: 1,
    matched_count: 1,
    version: 4,
    conclusion: '',
    customer: {
      id: customer.customer_user_id,
      username: customer.username,
      display_name: '',
    },
  }))
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url === '/api/user/employee/customers/security/alerts') {
      return {
        data: { success: true, data: { items: alerts, next_cursor: '' } },
      }
    }
    if (url === '/api/user/employee/customers/32/security/alerts/72') {
      return {
        data: {
          success: true,
          data: {
            alert: alerts[1],
            events: [
              {
                event_id: 'event-b',
                at: 200,
                action: 'login',
                ip: '192.0.2.3',
                request_id: 'customer-request',
                success: true,
              },
            ],
            actions: [],
          },
        },
      }
    }
    return auditResponse(url)
  })
  vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { ...alerts[1], version: 5 } },
  })
  view()
  await userEvent.click(screen.getByRole('tab', { name: 'My Customers' }))
  await userEvent.click(screen.getByRole('tab', { name: 'Risk records' }))
  const customerRow = await screen.findByRole('row', { name: /customer-b #32/ })
  const listCalls = vi
    .mocked(api.get)
    .mock.calls.filter(([url]) =>
      url.endsWith('/customers/security/alerts')
    ).length
  await userEvent.click(
    within(customerRow).getByRole('button', { name: 'Confirm' })
  )
  await waitFor(() =>
    expect(api.post).toHaveBeenCalledWith(
      '/api/user/employee/customers/32/security/alerts/72/actions',
      {
        version: 4,
        status: 'resolved',
      }
    )
  )
  await waitFor(() =>
    expect(
      vi
        .mocked(api.get)
        .mock.calls.filter(([url]) =>
          url.endsWith('/customers/security/alerts')
        ).length
    ).toBeGreaterThan(listCalls)
  )
  await userEvent.click(
    within(screen.getByRole('row', { name: /customer-b #32/ })).getByRole(
      'button',
      { name: securityRules.session_reuse }
    )
  )
  const detail = await screen.findByRole('dialog', {
    name: 'Security alert #72',
  })
  expect(within(detail).getByText('customer-b #32')).toBeVisible()
  await userEvent.click(within(detail).getByRole('tab', { name: 'Evidence' }))
  await userEvent.click(
    await within(detail).findByRole('button', { name: 'View related audit' })
  )
  await waitFor(() =>
    expect(api.get).toHaveBeenCalledWith(
      '/api/user/employee/customers/32/audit',
      expect.objectContaining({
        params: expect.objectContaining({
          request_id: 'customer-request',
          ip: '192.0.2.3',
          start: 1,
          end: 1100,
        }),
      })
    )
  )
  expect(screen.getByRole('combobox', { name: 'Customer' })).toHaveValue(
    'customer-b #32'
  )
  expect(
    vi
      .mocked(api.get)
      .mock.calls.some(([url]) => url.startsWith('/api/audit/security'))
  ).toBe(false)
})

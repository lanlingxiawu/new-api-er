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
import type { MouseEvent } from 'react'
import { toast } from 'sonner'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import type { CustomerProfile } from '@/features/customers/types'
import { securityRules } from '@/features/usage-logs/audit/security/lib/security-rules'

import { CustomerAuditDialog } from './customer-audit-dialog'

const requests = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('@/lib/api', () => ({ api: requests }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
    i18n: { language: 'en', resolvedLanguage: 'en' },
  }),
}))
vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('@/stores/auth-store', () => ({
  useAuthStore: (select: (state: unknown) => unknown) =>
    select({ auth: { user: { id: 1, role: 1, is_employee: true } } }),
}))

const customer = {
  id: 999,
  customer_user_id: 3,
  username: 'customer-a',
} as CustomerProfile
const alert = {
  id: 7,
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
}
const audit = {
  event_id: 'own-event',
  user_id: 3,
  username: 'customer-a',
  actor_role: 1,
  actor_is_employee: false,
  ip: '192.0.2.3',
  created_at: 200,
  category: 'login',
  action: 'login',
  token_ref: '',
  auth_method: 'session',
  user_agent: '',
  method: 'POST',
  route: '/api/user/login',
  status: 200,
  success: true,
  request_id: 'customer-request',
  content: '',
  other: {},
}
const clients: QueryClient[] = []

function view(target = customer) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  clients.push(client)
  const wrap = (next: CustomerProfile) => (
    <QueryClientProvider client={client}>
      <CustomerAuditDialog customer={next} onClose={vi.fn()} />
    </QueryClientProvider>
  )
  return { ...render(wrap(target)), wrap, client }
}

beforeEach(() => {
  requests.get.mockImplementation(async (url: string) => {
    if (url.endsWith('/audit')) {
      return {
        data: { success: true, data: { items: [audit], next_cursor: '' } },
      }
    }
    if (url.endsWith('/security/alerts')) {
      return {
        data: { success: true, data: { items: [alert], next_cursor: '' } },
      }
    }
    if (url.endsWith('/security/alerts/7')) {
      return {
        data: {
          success: true,
          data: {
            alert,
            events: [
              {
                event_id: 'own-event',
                at: 200,
                action: 'login',
                ip: '192.0.2.3',
                request_id: 'customer-request',
                success: true,
              },
            ],
            actions: [
              {
                id: 1,
                source: 'employee',
                from_status: 'open',
                to_status: 'investigating',
                conclusion: '',
                note: 'Customer confirmed this sign-in.',
                created_at: 200,
              },
            ],
          },
        },
      }
    }
    throw new Error(`Unexpected endpoint: ${url}`)
  })
  requests.post.mockResolvedValue({
    data: { success: true, data: { ...alert, version: 5 } },
  })
})

afterEach(() => {
  cleanup()
  for (const client of clients.splice(0)) client.clear()
  vi.resetAllMocks()
  vi.restoreAllMocks()
})

it('looks up a copied request ID within the current customer and resets the cursor', async () => {
  requests.get.mockResolvedValue({
    data: { success: true, data: { items: [], next_cursor: 'customer-next' } },
  })
  view()
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Go to next page' })
    ).toBeEnabled()
  )
  await userEvent.click(screen.getByRole('button', { name: 'Go to next page' }))
  await waitFor(() =>
    expect(requests.get.mock.lastCall?.[1]?.params.cursor).toBe('customer-next')
  )
  fireEvent.change(screen.getByLabelText('Request ID'), {
    target: { value: '  customer-request  ' },
  })
  fireEvent.change(screen.getByLabelText('IP'), {
    target: { value: '  192.0.2.3  ' },
  })
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(requests.get).toHaveBeenLastCalledWith(
      '/api/user/employee/customers/3/audit',
      expect.objectContaining({
        params: expect.objectContaining({
          cursor: '',
          request_id: 'customer-request',
          ip: '192.0.2.3',
        }),
      })
    )
  )
  expect(screen.getByLabelText('Request ID')).toHaveValue('customer-request')
})

it.each(['Operation logs', 'Risk records'])(
  'refreshes complete local days and resets pagination for %s',
  async (tab) => {
    const clock = vi
      .spyOn(Date, 'now')
      .mockReturnValue(new Date('2026-11-23T13:39:46').getTime())
    requests.get.mockResolvedValue({
      data: {
        success: true,
        data: { items: [], next_cursor: 'next-customer' },
      },
    })
    view()
    if (tab === 'Risk records') {
      await userEvent.click(screen.getByRole('tab', { name: tab }))
    }
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Go to next page' })
      ).toBeEnabled()
    )
    expect(requests.get.mock.lastCall?.[1]?.params).toMatchObject({
      start: new Date('2026-11-22T00:00:00').getTime() / 1000,
      end: new Date('2026-11-23T23:59:59').getTime() / 1000,
    })
    await userEvent.click(
      screen.getByRole('button', { name: 'Go to next page' })
    )
    await waitFor(() =>
      expect(requests.get).toHaveBeenLastCalledWith(
        expect.any(String),
        expect.objectContaining({
          params: expect.objectContaining({ cursor: 'next-customer' }),
        })
      )
    )
    clock.mockReturnValue(new Date('2026-11-24T00:01:00').getTime())
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Refresh' })).toBeEnabled()
    )
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() =>
      expect(requests.get).toHaveBeenLastCalledWith(
        expect.any(String),
        expect.objectContaining({
          params: expect.objectContaining({
            start: new Date('2026-11-23T00:00:00').getTime() / 1000,
            end: new Date('2026-11-24T23:59:59').getTime() / 1000,
            cursor: '',
          }),
        })
      )
    )
    expect(
      screen.getByRole('button', { name: 'Go to previous page' })
    ).toBeDisabled()
    requests.get.mockClear()
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(requests.get).toHaveBeenCalled())
  }
)

it('keeps a manually applied time window and leaves draft filters unapplied on refresh', async () => {
  const clock = vi.spyOn(Date, 'now').mockReturnValue(1795000000000)
  view()
  await screen.findByText('192.0.2.3')
  await userEvent.click(screen.getByRole('button', { name: / ~ / }))
  fireEvent.change(screen.getByLabelText('Start Time'), {
    target: { value: '2026-11-10T10:00:00' },
  })
  fireEvent.change(screen.getByLabelText('End Time'), {
    target: { value: '2026-11-10T11:00:00' },
  })
  await userEvent.click(screen.getByRole('button', { name: 'Confirm' }))
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  const start = new Date('2026-11-10T10:00:00').getTime() / 1000
  const end = new Date('2026-11-10T11:00:00').getTime() / 1000
  await waitFor(() =>
    expect(requests.get).toHaveBeenLastCalledWith(
      '/api/user/employee/customers/3/audit',
      expect.objectContaining({
        params: expect.objectContaining({ start, end }),
      })
    )
  )
  fireEvent.change(screen.getByLabelText('IP'), {
    target: { value: '192.0.2.99' },
  })
  clock.mockReturnValue(1795000060000)
  await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  await waitFor(() =>
    expect(requests.get).toHaveBeenLastCalledWith(
      '/api/user/employee/customers/3/audit',
      expect.objectContaining({
        params: expect.objectContaining({ start, end, ip: '', cursor: '' }),
      })
    )
  )
  expect(screen.getByLabelText('IP')).toHaveValue('192.0.2.99')
})

it('confirms with one click, prevents duplicate writes, and preserves existing conclusions', async () => {
  let finish!: () => void
  requests.post.mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = () =>
          resolve({
            data: {
              success: true,
              data: { ...alert, status: 'resolved', version: 5 },
            },
          })
      })
  )
  view()
  expect(await screen.findByText('192.0.2.3')).toBeVisible()
  expect(requests.get).toHaveBeenCalledWith(
    '/api/user/employee/customers/3/audit',
    expect.objectContaining({ params: expect.objectContaining({ cursor: '' }) })
  )
  await userEvent.click(screen.getByRole('tab', { name: 'Risk records' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Confirm' }))
  const saving = screen.getByRole('button', { name: 'Saving...' })
  expect(saving).toBeDisabled()
  await userEvent.click(saving)
  expect(screen.queryByText('Assignee')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Archive' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Delete alert' })
  ).not.toBeInTheDocument()
  expect(screen.queryByLabelText('Investigation note')).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Save' })).not.toBeInTheDocument()
  await waitFor(() =>
    expect(requests.post).toHaveBeenCalledExactlyOnceWith(
      '/api/user/employee/customers/3/security/alerts/7/actions',
      {
        version: 4,
        status: 'resolved',
      }
    )
  )
  await act(async () => finish())
  expect(await screen.findByText('Confirmed')).toBeVisible()
  expect(
    requests.get.mock.calls.every(([url]) =>
      url.startsWith('/api/user/employee/customers/3/')
    )
  ).toBe(true)
})

it('refreshes the customer version after a conflict without retrying the write', async () => {
  view()
  await userEvent.click(screen.getByRole('tab', { name: 'Risk records' }))
  const confirm = await screen.findByRole('button', { name: 'Confirm' })
  requests.get.mockResolvedValue({
    data: {
      success: true,
      data: { items: [{ ...alert, version: 5 }], next_cursor: '' },
    },
  })
  requests.post.mockResolvedValueOnce({
    data: {
      success: false,
      message: 'The alert may have changed. Refresh and try again.',
    },
  })
  await userEvent.click(confirm)
  await waitFor(() => expect(toast.error).toHaveBeenCalled())
  expect(requests.post).toHaveBeenCalledTimes(1)
  expect(screen.queryByText('Confirmed')).not.toBeInTheDocument()
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeEnabled()
  )
  await userEvent.click(screen.getByRole('button', { name: 'Confirm' }))
  await waitFor(() =>
    expect(requests.post).toHaveBeenLastCalledWith(
      '/api/user/employee/customers/3/security/alerts/7/actions',
      { version: 5, status: 'resolved' }
    )
  )
  expect(requests.post).toHaveBeenCalledTimes(2)
})

it.each(['list', 'detail'])(
  'clears customer records when confirmation from the %s loses permission',
  async (entry) => {
    const { client } = view()
    await userEvent.click(screen.getByRole('tab', { name: 'Risk records' }))
    let confirm = await screen.findByRole('button', { name: 'Confirm' })
    if (entry === 'detail') {
      await userEvent.click(
        screen.getByRole('button', { name: securityRules.session_reuse })
      )
      confirm = await within(
        await screen.findByRole('dialog', { name: 'Security alert #7' })
      ).findByRole('button', { name: 'Confirm' })
    }
    requests.post.mockRejectedValue({
      isAxiosError: true,
      response: { status: 403 },
    })
    await userEvent.click(confirm)
    expect(await screen.findByText('Customer access has changed')).toBeVisible()
    expect(screen.queryByText('192.0.2.3')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('dialog', { name: 'Security alert #7' })
    ).not.toBeInTheDocument()
    expect(client.getQueriesData({ queryKey: ['customer-audit', 1] })).toEqual(
      []
    )
    expect(requests.post).toHaveBeenCalledTimes(1)
  }
)

it('clears cached customer records when ownership has been revoked', async () => {
  const { client } = view()
  expect(await screen.findByText('192.0.2.3')).toBeVisible()
  requests.get.mockRejectedValue({
    isAxiosError: true,
    response: { status: 403 },
  })
  await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  expect(await screen.findByText('Customer access has changed')).toBeVisible()
  expect(screen.queryByText('192.0.2.3')).not.toBeInTheDocument()
  expect(client.getQueriesData({ queryKey: ['customer-audit', 1, 3] })).toEqual(
    []
  )
})

it('does not display the previous customer while another customer is loading', async () => {
  const result = view()
  expect(await screen.findByText('192.0.2.3')).toBeVisible()
  requests.get.mockImplementation(() => new Promise(() => {}))
  result.rerender(
    result.wrap({ ...customer, customer_user_id: 4, username: 'customer-b' })
  )
  expect(screen.queryByText('192.0.2.3')).not.toBeInTheDocument()
  await waitFor(() =>
    expect(requests.get).toHaveBeenCalledWith(
      '/api/user/employee/customers/4/audit',
      expect.anything()
    )
  )
})

it('shows handling history and links related audit within the same customer', async () => {
  view()
  await screen.findByText('192.0.2.3')
  await userEvent.click(screen.getByRole('tab', { name: 'Risk records' }))
  await userEvent.click(
    await screen.findByRole('button', { name: securityRules.session_reuse })
  )
  const detail = await screen.findByRole('dialog', {
    name: 'Security alert #7',
  })
  expect(within(detail).getByRole('tab', { name: 'Evidence' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  expect(await within(detail).findByText('Login')).toBeVisible()
  expect(within(detail).getByText('192.0.2.3')).toBeVisible()
  expect(within(detail).getByText('Success')).toBeVisible()
  expect(within(detail).queryByText('Request ID')).not.toBeInTheDocument()
  expect(within(detail).queryByText('customer-request')).not.toBeInTheDocument()
  await userEvent.click(
    within(detail).getByRole('tab', { name: 'Handling history' })
  )
  expect(
    await within(detail).findByText('Customer confirmed this sign-in.')
  ).toBeVisible()
  await userEvent.click(within(detail).getByRole('tab', { name: 'Evidence' }))
  await userEvent.click(
    within(detail).getByRole('button', { name: 'View related audit' })
  )
  await waitFor(() =>
    expect(requests.get).toHaveBeenCalledWith(
      '/api/user/employee/customers/3/audit',
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
  await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  await waitFor(() =>
    expect(requests.get).toHaveBeenLastCalledWith(
      '/api/user/employee/customers/3/audit',
      expect.objectContaining({
        params: expect.objectContaining({
          start: 1,
          end: 1100,
          request_id: 'customer-request',
        }),
      })
    )
  )
})

it('refreshes the list and handling history after confirmation in the detail', async () => {
  view()
  await userEvent.click(screen.getByRole('tab', { name: 'Risk records' }))
  await userEvent.click(
    await screen.findByRole('button', { name: securityRules.session_reuse })
  )
  const detail = await screen.findByRole('dialog', {
    name: 'Security alert #7',
  })
  const confirm = await within(detail).findByRole('button', { name: 'Confirm' })
  requests.get.mockClear()
  requests.get.mockImplementation(async (url: string) => ({
    data: {
      success: true,
      data: url.endsWith('/security/alerts')
        ? {
            items: [{ ...alert, status: 'resolved', version: 5 }],
            next_cursor: '',
          }
        : {
            alert: { ...alert, status: 'resolved', version: 5 },
            events: [],
            actions: [
              {
                id: 2,
                source: 'employee',
                from_status: 'open',
                to_status: 'resolved',
                conclusion: '',
                created_at: 300,
              },
            ],
          },
    },
  }))
  await userEvent.click(confirm)
  expect(await within(detail).findByText('Confirmed')).toBeVisible()
  await waitFor(() => {
    expect(requests.get).toHaveBeenCalledWith(
      '/api/user/employee/customers/3/security/alerts',
      expect.anything()
    )
    expect(requests.get).toHaveBeenCalledWith(
      '/api/user/employee/customers/3/security/alerts/7',
      expect.anything()
    )
  })
  await userEvent.click(
    within(detail).getByRole('tab', { name: 'Handling history' })
  )
  expect(
    await within(detail).findByText('Not handled → Confirmed')
  ).toBeVisible()
  await userEvent.click(
    within(detail).getAllByRole('button', { name: 'Close' })[0]
  )
  expect(screen.getByText('Processed · Confirmed')).toBeVisible()
  expect(requests.post).toHaveBeenCalledExactlyOnceWith(
    '/api/user/employee/customers/3/security/alerts/7/actions',
    { version: 4, status: 'resolved' }
  )
})

it('keeps the handling status separate from actions for every risk state', async () => {
  requests.get.mockResolvedValue({
    data: {
      success: true,
      data: {
        items: ['open', 'investigating', 'resolved', 'false_positive'].map(
          (status, index) => ({ ...alert, id: 10 + index, status })
        ),
        next_cursor: '',
      },
    },
  })
  view()
  await userEvent.click(screen.getByRole('tab', { name: 'Risk records' }))
  const table = await screen.findByRole('table')
  for (const label of [
    'Unprocessed',
    'In progress',
    'Processed · Confirmed',
    'Processed · False positive',
  ]) {
    expect(await within(table).findByText(label)).toBeVisible()
  }
  expect(
    within(table).getByRole('columnheader', { name: 'Handling status' })
  ).toBeVisible()
  expect(
    within(table).getByRole('columnheader', { name: 'Actions' })
  ).toBeVisible()
  expect(
    within(table).getAllByRole('button', { name: 'Confirm' })
  ).toHaveLength(2)
})

it('shows all-time pending records, resets after confirmation, and offers the processed record', async () => {
  let processed = false
  requests.get.mockImplementation(async (url: string) => ({
    data: {
      success: true,
      data: url.endsWith('/security/alerts/7')
        ? { alert: { ...alert, status: 'resolved' }, events: [], actions: [] }
        : {
            items: processed ? [] : [alert],
            next_cursor: processed ? '' : 'next-pending',
          },
    },
  }))
  requests.post.mockImplementation(async () => {
    processed = true
    return {
      data: {
        success: true,
        data: { ...alert, status: 'resolved', version: 5 },
      },
    }
  })
  const { client } = view()
  const invalidate = vi.spyOn(client, 'invalidateQueries')
  await userEvent.click(screen.getByRole('tab', { name: 'Risk records' }))
  await userEvent.click(screen.getByRole('button', { name: 'Handling status' }))
  await userEvent.click(
    await screen.findByRole('option', { name: 'Unprocessed' })
  )
  await waitFor(() =>
    expect(requests.get.mock.lastCall?.[1]?.params).toEqual({
      view: 'unresolved',
      cursor: '',
    })
  )
  expect(
    screen.getByText(
      'Unprocessed records from all time, including records in progress.'
    )
  ).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: 'Go to next page' }))
  await waitFor(() =>
    expect(requests.get.mock.lastCall?.[1]?.params.cursor).toBe('next-pending')
  )
  await userEvent.click(await screen.findByRole('button', { name: 'Confirm' }))
  expect(
    await screen.findByText('No matching unprocessed risk records')
  ).toBeVisible()
  expect(requests.get.mock.lastCall?.[1]?.params).toEqual({
    view: 'unresolved',
    cursor: '',
  })
  expect(
    screen.getByRole('button', { name: 'Go to previous page' })
  ).toBeDisabled()
  expect(invalidate).toHaveBeenCalledWith({ queryKey: ['customer-audit', 1] })
  expect(toast.success).toHaveBeenCalledWith(
    'Risk record processed',
    expect.objectContaining({
      action: { label: 'View record', onClick: expect.any(Function) },
    })
  )
  const action = vi.mocked(toast.success).mock.lastCall?.[1]?.action
  if (action && typeof action === 'object' && 'onClick' in action) {
    act(() => action.onClick({} as MouseEvent<HTMLButtonElement>))
  }
  expect(
    await screen.findByRole('dialog', { name: 'Security alert #7' })
  ).toBeVisible()
})

it('requests handled records within the event time range and clears the old cursor', async () => {
  view()
  await userEvent.click(screen.getByRole('tab', { name: 'Risk records' }))
  await userEvent.click(screen.getByRole('button', { name: 'Handling status' }))
  await userEvent.click(
    await screen.findByRole('option', { name: 'Processed' })
  )
  await waitFor(() =>
    expect(requests.get.mock.lastCall?.[1]?.params).toMatchObject({
      view: 'handled',
      cursor: '',
      start: expect.any(Number),
      end: expect.any(Number),
    })
  )
  expect(
    screen.getByText(
      'Processed records include confirmed alerts and false positives. The date range uses the latest event time.'
    )
  ).toBeVisible()
})

it.each([0, 20])(
  'shows %i compact evidence records with empty and page states',
  async (count) => {
    view()
    await userEvent.click(screen.getByRole('tab', { name: 'Risk records' }))
    const open = await screen.findByRole('button', {
      name: securityRules.session_reuse,
    })
    requests.get.mockResolvedValue({
      data: {
        success: true,
        data: {
          alert,
          events: Array.from({ length: count }, (_, index) => ({
            event_id: `evidence-${index}`,
            at: 200 + index,
            action:
              index === 0 ? 'unknown.internal-action' : 'auth.login_failed',
            ip: `192.0.2.${index + 10}`,
            request_id: `private-request-${index}`,
            success: false,
            facts: '{"private":"internal-evidence"}',
          })),
          actions: [],
        },
      },
    })
    await userEvent.click(open)
    const detail = await screen.findByRole('dialog', {
      name: 'Security alert #7',
    })
    if (count === 0) {
      expect(await within(detail).findByText('No related events')).toBeVisible()
      return
    }
    expect(await within(detail).findByText('Operation audit')).toBeVisible()
    expect(within(detail).getAllByText('Login failed')).toHaveLength(9)
    expect(within(detail).getAllByText('Failed')).toHaveLength(10)
    expect(within(detail).queryByText('192.0.2.20')).not.toBeInTheDocument()
    expect(
      within(detail).queryByText(/private-request/)
    ).not.toBeInTheDocument()
    expect(
      within(detail).queryByText(/internal-action/)
    ).not.toBeInTheDocument()
    expect(
      within(detail).queryByText(/internal-evidence/)
    ).not.toBeInTheDocument()
    await userEvent.click(
      within(detail).getByRole('button', { name: 'Next page' })
    )
    expect(within(detail).getByText('192.0.2.20')).toBeVisible()
    expect(within(detail).getAllByText('Login failed')).toHaveLength(10)
    expect(within(detail).queryByText('192.0.2.10')).not.toBeInTheDocument()
    await userEvent.click(
      within(detail).getByRole('button', { name: 'Previous page' })
    )
    expect(within(detail).getByText('192.0.2.10')).toBeVisible()
  }
)

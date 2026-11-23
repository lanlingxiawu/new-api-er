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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { PropsWithChildren } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { EmployeeConsole } from '@/features/employee-console'

import { Employees } from './index'

const mocks = vi.hoisted(() => ({ get: vi.fn(), logs: vi.fn() }))
vi.mock('@/lib/api', () => ({ api: { get: mocks.get } }))
vi.mock('@/components/layout', () => {
  const Container = (props: PropsWithChildren) => <div>{props.children}</div>
  return {
    SectionPageLayout: Object.assign(Container, {
      Title: Container,
      Content: Container,
    }),
  }
})
vi.mock('@/components/layout/components/page-footer', () => ({
  PageFooterPortal: (props: PropsWithChildren) => <div>{props.children}</div>,
}))
vi.mock('./components/commission-financial-calendar', () => ({
  CommissionCalendarSection: () => null,
}))

const clients: QueryClient[] = []
beforeEach(() => {
  mocks.logs.mockReset()
  mocks.get.mockReset()
  mocks.get.mockImplementation(
    async (url: string, config?: { signal?: AbortSignal }) => {
      if (url.includes('/commission?')) {
        return {
          data: await mocks.logs(
            new URL(url, 'http://localhost').searchParams,
            config
          ),
        }
      }
      if (url.includes('/commission/channels')) {
        return { data: { success: true, data: { items: [], total: 0 } } }
      }
      if (url.endsWith('/profile')) {
        return {
          data: {
            success: true,
            data: { profile: {}, extension: {}, tier: {} },
          },
        }
      }
      if (url.endsWith('/summary')) return { data: { success: true, data: {} } }
      throw new Error(`Unexpected request: ${url}`)
    }
  )
})
afterEach(() => {
  for (const client of clients) client.clear()
  clients.length = 0
})

function commissionRow(id: number) {
  return {
    id,
    employee_id: 1,
    employee_user_id: 101,
    customer_user_id: 201,
    channel_id: 0,
    log_id: null,
    model_name: `commission-model-${id}`,
    created_at: Math.floor(Date.now() / 1000) - 10,
    revenue_quota: 100,
    cost_quota: 10,
    profit_quota: 90,
    commission_quota: 9,
    commission_rate: 0.1,
    cost_ratio: 1,
    group_ratio: 1,
    settle_status: 0,
    settled_at: 0,
  }
}

async function openCommissionLogs(admin: boolean) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      {admin ? <Employees /> : <EmployeeConsole />}
    </QueryClientProvider>
  )
  const tab = await screen.findByRole('tab', {
    name: admin ? 'Commission Logs' : 'Commission Details',
  })
  await userEvent.click(tab)
}

describe.each([true, false])('commission list (admin=%s)', (admin) => {
  it.each(['business', 'network'])(
    'shows %s failure and recovers on retry',
    async (kind) => {
      if (kind === 'business') {
        mocks.logs.mockResolvedValueOnce({
          success: false,
          message: 'Temporarily unavailable',
        })
      } else {
        mocks.logs.mockRejectedValueOnce(new Error('Network unavailable'))
      }
      mocks.logs.mockResolvedValue({
        success: true,
        data: { items: [commissionRow(1)], has_more: false, next_cursor: null },
      })
      await openCommissionLogs(admin)
      expect(
        await screen.findByText('Failed to load commission records')
      ).toBeVisible()
      expect(screen.queryByText('No records')).not.toBeInTheDocument()
      expect(
        screen.queryByText('No commission records yet')
      ).not.toBeInTheDocument()
      expect(screen.queryByText('Total:')).not.toBeInTheDocument()
      await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
      expect(await screen.findByText('commission-model-1')).toBeVisible()
      expect(
        screen.queryByText('Failed to load commission records')
      ).not.toBeInTheDocument()
      const [query, config] = mocks.logs.mock.calls[0]
      expect(query.get('pagination')).toBe('cursor')
      expect(query.has('page')).toBe(false)
      expect(
        Number(query.get('end_time')) - Number(query.get('start_time'))
      ).toBe(86400)
      expect(config.signal).toBeInstanceOf(AbortSignal)
    }
  )

  it('keeps cursor boundaries and resets them when searching, resetting, or changing page size', async () => {
    const cursor = { created_at: commissionRow(2).created_at, id: 2 }
    mocks.logs.mockImplementation(async (query: URLSearchParams) => ({
      success: true,
      data: query.has('cursor_id')
        ? { items: [commissionRow(1)], has_more: false, next_cursor: null }
        : {
            items: [commissionRow(3), commissionRow(2)],
            has_more: true,
            next_cursor: cursor,
          },
    }))
    await openCommissionLogs(admin)
    expect(await screen.findByText('commission-model-3')).toBeVisible()
    expect(screen.queryByText('Total:')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Go to last page' })
    ).not.toBeInTheDocument()
    const firstQuery = mocks.logs.mock.calls[0][0] as URLSearchParams
    await userEvent.click(
      screen.getByRole('button', { name: 'Go to next page' })
    )
    expect(await screen.findByText('commission-model-1')).toBeVisible()
    const secondQuery = mocks.logs.mock.calls.at(-1)?.[0] as URLSearchParams
    expect(secondQuery.get('cursor_id')).toBe('2')
    expect(secondQuery.get('cursor_created_at')).toBe(String(cursor.created_at))
    expect(secondQuery.get('start_time')).toBe(firstQuery.get('start_time'))
    expect(secondQuery.get('end_time')).toBe(firstQuery.get('end_time'))
    expect(
      screen.getByRole('button', { name: 'Go to next page' })
    ).toBeDisabled()
    await userEvent.type(screen.getByPlaceholderText('Model Name'), 'filtered')
    await userEvent.click(screen.getByRole('button', { name: 'Search' }))
    expect(await screen.findByText('commission-model-3')).toBeVisible()
    expect(mocks.logs.mock.calls.at(-1)?.[0].has('cursor_id')).toBe(false)
    expect(mocks.logs.mock.calls.at(-1)?.[0].get('model_name')).toBe('filtered')
    const beforeSearch = mocks.logs.mock.calls.length
    await userEvent.click(screen.getByRole('button', { name: 'Search' }))
    await waitFor(() =>
      expect(mocks.logs.mock.calls.length).toBeGreaterThan(beforeSearch)
    )
    await userEvent.click(screen.getByRole('button', { name: 'Reset' }))
    await waitFor(() =>
      expect(mocks.logs.mock.calls.at(-1)?.[0].has('model_name')).toBe(false)
    )
    await userEvent.click(
      screen.getByRole('combobox', { name: 'Rows per page' })
    )
    await userEvent.click(screen.getByRole('option', { name: '20' }))
    await waitFor(() =>
      expect(mocks.logs.mock.calls.at(-1)?.[0].get('page_size')).toBe('20')
    )
    expect(mocks.logs.mock.calls.at(-1)?.[0].has('cursor_id')).toBe(false)
  })

  it('only displays an empty result after a successful request', async () => {
    mocks.logs.mockResolvedValue({
      success: true,
      data: { items: [], has_more: false, next_cursor: null },
    })
    await openCommissionLogs(admin)
    expect(
      await screen.findByText(
        admin ? 'No records' : 'No commission records yet'
      )
    ).toBeVisible()
    expect(
      screen.queryByText('Failed to load commission records')
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Go to next page' })
    ).toBeDisabled()
  })

  it('retains the cursor after a failed next page and can return to the first page', async () => {
    const cursor = { created_at: commissionRow(2).created_at, id: 2 }
    const firstPage = {
      success: true,
      data: {
        items: [commissionRow(3), commissionRow(2)],
        has_more: true,
        next_cursor: cursor,
      },
    }
    mocks.logs
      .mockResolvedValueOnce(firstPage)
      .mockResolvedValueOnce({ success: false, message: 'Query timed out' })
      .mockResolvedValueOnce({
        success: true,
        data: { items: [commissionRow(1)], has_more: false, next_cursor: null },
      })
      .mockResolvedValue(firstPage)
    await openCommissionLogs(admin)
    expect(await screen.findByText('commission-model-3')).toBeVisible()
    await userEvent.click(
      screen.getByRole('button', { name: 'Go to next page' })
    )
    expect(
      await screen.findByText('Failed to load commission records')
    ).toBeVisible()
    expect(screen.queryByText('commission-model-3')).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('commission-model-1')).toBeVisible()
    expect(mocks.logs.mock.calls[1][0].toString()).toBe(
      mocks.logs.mock.calls[2][0].toString()
    )
    await userEvent.click(
      screen.getByRole('button', { name: 'Go to previous page' })
    )
    expect(await screen.findByText('commission-model-3')).toBeVisible()
    await waitFor(() =>
      expect(mocks.logs.mock.calls.at(-1)?.[0].has('cursor_id')).toBe(false)
    )
  })
})

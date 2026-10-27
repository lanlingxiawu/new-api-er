import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { EmployeeCustomerExport } from './employee-customer-export'

const request = vi.hoisted(() => vi.fn())
vi.mock('../employee-api', async (original) => ({
  ...(await original<typeof import('../employee-api')>()),
  exportRequest: request,
}))
vi.mock('@/stores/auth-store', () => ({
  useAuthStore: (selector: (state: unknown) => unknown) =>
    selector({ auth: { user: { id: 10 } } }),
}))

beforeEach(() => {
  request.mockReset()
  request.mockImplementation(async (_method: string, url: string) => {
    if (url.endsWith('/capabilities')) {
      return {
        enabled: true,
        max_range_sec: 86400 * 31,
        max_customers: 20,
        columns: [],
        anomaly_kinds: [],
        templates: ['invoice', 'custom'].map((key) => ({
          key,
          name: key,
          mode: 'detail',
          columns: [],
          allowed_filters: ['model_name'],
        })),
      }
    }
    if (url.includes('/options?')) return { items: [], next_cursor: '' }
    if (url.endsWith('/estimate')) {
      return { rows: 2, capped: false, available: true }
    }
    return { jobs: [] }
  })
})
afterEach(cleanup)

it.each([
  ['3', 'Manage'],
  ['7', 'Login'],
])(
  'marks retired type %s while allowing historical exports',
  async (logType, label) => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const user = userEvent.setup()
    render(
      <QueryClientProvider client={client}>
        <EmployeeCustomerExport prefill={{ customerId: 42, logType }} />
      </QueryClientProvider>
    )
    await user.click(
      await screen.findByRole('button', { name: 'Export customer data' })
    )
    const type = screen.getByRole('combobox', { name: 'Type' })
    expect(type).toHaveTextContent(`${label}Deprecated`)
    expect(type).toHaveAccessibleDescription(
      'Only used to find historical logs. New records are available in Audit Logs.'
    )
    await user.click(type)
    for (const retired of ['Manage', 'Login']) {
      expect(
        within(
          screen.getByRole('option', { name: new RegExp(`^${retired}`) })
        ).getByText('Deprecated')
      ).toBeVisible()
    }
    for (const current of [
      'All Types',
      'Top-up',
      'Consume',
      'System',
      'Error',
      'Refund',
    ]) {
      expect(
        within(screen.getByRole('option', { name: current })).queryByText(
          'Deprecated'
        )
      ).not.toBeInTheDocument()
    }
    await user.click(
      screen.getByRole('option', { name: new RegExp(`^${label}`) })
    )
    await waitFor(() =>
      expect(request).toHaveBeenCalledWith(
        'post',
        expect.stringContaining('/estimate'),
        expect.objectContaining({ type: Number(logType) }),
        { quiet: true }
      )
    )
    await user.click(screen.getByRole('button', { name: 'Start Export' }))
    await waitFor(() =>
      expect(request).toHaveBeenCalledWith(
        'post',
        '/api/user/employee/export/jobs',
        expect.objectContaining({ type: Number(logType) })
      )
    )
    client.clear()
  }
)

it.each([undefined, '5'])(
  'uses the selected type for estimates, options and creation (prefill=%s)',
  async (logType) => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const user = userEvent.setup()
    render(
      <QueryClientProvider client={client}>
        <EmployeeCustomerExport prefill={{ customerId: 42, logType }} />
      </QueryClientProvider>
    )
    await user.click(
      await screen.findByRole('button', { name: 'Export customer data' })
    )
    const type = screen.getByRole('combobox', { name: 'Type' })
    expect(type).toHaveTextContent(logType ? 'Error' : 'All Types')
    await waitFor(() =>
      expect(request).toHaveBeenCalledWith(
        'post',
        expect.stringContaining('/estimate'),
        expect.objectContaining({
          type: Number(logType ?? 0),
          customer_ids: [42],
        }),
        { quiet: true }
      )
    )
    await user.click(type)
    await user.click(await screen.findByRole('option', { name: 'Consume' }))
    await waitFor(() =>
      expect(request).toHaveBeenCalledWith(
        'post',
        expect.stringContaining('/estimate'),
        expect.objectContaining({ type: 2 }),
        { quiet: true }
      )
    )
    await waitFor(() =>
      expect(
        request.mock.calls.some(
          (call) => call[1].includes('/options?') && call[1].includes('type=2')
        )
      ).toBe(true)
    )
    await user.click(type)
    await user.click(await screen.findByRole('option', { name: 'Error' }))
    await user.click(screen.getByRole('combobox', { name: 'Template' }))
    await user.click(await screen.findByRole('option', { name: 'custom' }))
    expect(type).toHaveTextContent('Error')
    await waitFor(() =>
      expect(request).toHaveBeenCalledWith(
        'post',
        expect.stringContaining('/estimate'),
        expect.objectContaining({ type: 5, template_id: 'custom' }),
        { quiet: true }
      )
    )
    const estimate = [...request.mock.calls]
      .reverse()
      .find((call) => call[1].endsWith('/estimate'))?.[2]
    await user.click(screen.getByRole('button', { name: 'Start Export' }))
    await waitFor(() =>
      expect(request).toHaveBeenCalledWith(
        'post',
        '/api/user/employee/export/jobs',
        estimate
      )
    )
    client.clear()
  }
)

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

import { EmployeeExportJobs } from './employee-export-jobs'

const mocks = vi.hoisted(() => ({ request: vi.fn(), reportError: vi.fn() }))
vi.mock('@/lib/handle-server-error', () => ({
  handleServerError: mocks.reportError,
}))
vi.mock('../employee-api', () => ({
  EMPLOYEE_EXPORT_BASE: '/api/user/employee/export',
  employeeTemplateName: () => 'Template',
  exportRequest: mocks.request,
}))
vi.mock('./export-filter-select', () => ({ ExportFilterSelect: () => null }))
afterEach(cleanup)
beforeEach(() => {
  mocks.request.mockReset()
  mocks.reportError.mockReset()
})

it('distinguishes an empty history from an empty filtered result', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  mocks.request.mockResolvedValue({ jobs: [] })
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={client}>
      <EmployeeExportJobs />
    </QueryClientProvider>
  )
  expect(await screen.findByText('No exports yet')).toBeVisible()
  await user.type(screen.getByLabelText('Creator username'), 'Alice')
  await user.click(screen.getByRole('button', { name: 'Search' }))
  expect(
    await screen.findByText('No matching exports. Adjust or reset the filters.')
  ).toBeVisible()
  expect(
    screen.queryByText(
      'Create an export to see its progress and download it here.'
    )
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Reset' }))
  expect(await screen.findByText('No exports yet')).toBeVisible()
  client.clear()
})

it('reports a refused download and reloads permissions on a stale customer task', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const denied = new Error('Your export access has changed.')
  const job = {
    job_id: 'customer-task',
    username: 'Alice',
    source: 'self',
    created_at: 100,
    status: 'ready',
    row_count: 3,
    format: 'csv_gz',
    filters: {},
    can_download: true,
    can_delete: true,
    can_cancel: false,
  }
  mocks.request
    .mockResolvedValueOnce({ jobs: [job] })
    .mockRejectedValueOnce(denied)
    .mockResolvedValue({
      jobs: [{ ...job, can_download: false, can_delete: false }],
    })
  render(
    <QueryClientProvider client={client}>
      <EmployeeExportJobs />
    </QueryClientProvider>
  )
  await userEvent.click(await screen.findByRole('button', { name: 'Download' }))
  await waitFor(() => expect(mocks.reportError).toHaveBeenCalledWith(denied))
  await waitFor(() =>
    expect(
      screen.queryByRole('button', { name: 'Download' })
    ).not.toBeInTheDocument()
  )
  expect(screen.getByText('customer-task')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Delete' })
  ).not.toBeInTheDocument()
  client.clear()
})

it('continues past an empty filtered page and respects operation capabilities', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const user = userEvent.setup()
  mocks.request
    .mockResolvedValueOnce({ jobs: [], next_cursor: 'next', has_more: true })
    .mockResolvedValueOnce({
      jobs: [
        {
          job_id: 'customer-task',
          username: 'Customer Alice',
          source: 'self',
          created_at: 100,
          status: 'ready',
          row_count: 10,
          format: 'csv_gz',
          filters: {},
          can_download: false,
          can_cancel: false,
          can_delete: false,
        },
      ],
      has_more: false,
    })
  render(
    <QueryClientProvider client={client}>
      <EmployeeExportJobs />
    </QueryClientProvider>
  )
  await user.click(
    await screen.findByRole('button', { name: 'Continue searching' })
  )
  expect(await screen.findByText('customer-task')).toBeVisible()
  expect(mocks.request.mock.calls[1][1]).toContain('cursor=next')
  expect(
    screen.queryByRole('button', { name: 'Download' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Delete' })
  ).not.toBeInTheDocument()
  client.clear()
})

it('requires confirmation before deleting a customer export', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const user = userEvent.setup()
  const page = {
    jobs: [
      {
        job_id: 'customer-task',
        username: 'Alice',
        source: 'self',
        created_at: 100,
        status: 'ready',
        row_count: 10,
        format: 'csv_gz',
        filters: {},
        can_download: true,
        can_cancel: false,
        can_delete: true,
      },
    ],
  }
  mocks.request.mockResolvedValue(page)
  render(
    <QueryClientProvider client={client}>
      <EmployeeExportJobs />
    </QueryClientProvider>
  )
  await user.click(await screen.findByRole('button', { name: 'Delete' }))
  expect(mocks.request.mock.calls.some(([method]) => method === 'delete')).toBe(
    false
  )
  expect(screen.getByRole('alertdialog')).toBeVisible()
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Delete',
    })
  )
  await waitFor(() =>
    expect(mocks.request).toHaveBeenCalledWith(
      'delete',
      '/api/user/employee/export/jobs/customer-task'
    )
  )
  client.clear()
})

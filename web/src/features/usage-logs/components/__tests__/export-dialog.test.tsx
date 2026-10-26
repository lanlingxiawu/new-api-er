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
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { LogExportCenter } from '../../export'
import { SelfExportSheet } from '../../export/components/self-export-sheet'

const mocks = vi.hoisted(() => ({
  create: vi.fn(),
  jobs: vi.fn(),
  search: {
    startTime: 1700000000000,
    endTime: 1700003600000,
    model: 'gpt-test',
    username: 'other-user',
    channel: '9',
    type: ['2'],
    token: 'my-token',
  },
}))
vi.mock('@tanstack/react-router', () => ({
  getRouteApi: () => ({
    useSearch: () => mocks.search,
    useNavigate: () => vi.fn(),
  }),
}))
vi.mock('../../export/api', () => ({
  createSelfExportJob: mocks.create,
  getExportJobs: mocks.jobs,
  deleteExportJob: vi.fn(),
  downloadExportPart: vi.fn(),
}))
vi.mock('../../export/components/new-export-sheet', () => ({
  NewExportSheet: () => <div data-testid='admin-export-form' />,
}))
vi.mock('../../export/components/employee-template-manager', () => ({
  EmployeeTemplateManager: () => null,
}))
beforeEach(() => {
  mocks.create.mockReset()
  mocks.create.mockResolvedValue('job-1')
  mocks.jobs.mockReset()
  mocks.jobs.mockResolvedValue({ jobs: [], enabled: true, max_range_sec: 60 })
})
afterEach(cleanup)

it.each([true, false])(
  'shows server diagnostics only in the administrator view (self=%s)',
  async (self) => {
    mocks.jobs.mockResolvedValue({
      enabled: true,
      jobs: [
        {
          job_id: 'running-job',
          status: 'running',
          progress: 20,
          row_count: 100,
          scanned_rows: 200,
          bytes_out: 0,
          format: 'csv_gz',
          filters: {},
          parts: [],
          throttled_ms: 9000,
          created_at: 1700000000,
          updated_at: 1700000010,
        },
        {
          job_id: 'failed-job',
          status: 'failed',
          progress: 10,
          row_count: 0,
          bytes_out: 0,
          format: 'csv_gz',
          filters: {},
          parts: [],
          error: 'disk_full',
          throttled_ms: 0,
          created_at: 1700000000,
          updated_at: 1700000010,
        },
      ],
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <LogExportCenter self={self} />
      </QueryClientProvider>
    )
    await screen.findByTitle('running-job')
    if (self) {
      expect(
        await screen.findByText('Estimating remaining time...')
      ).toBeVisible()
      expect(
        screen.queryByText(/keep the service responsive/)
      ).not.toBeInTheDocument()
      expect(screen.queryByText(/server storage/)).not.toBeInTheDocument()
      expect(screen.queryByText(/scanned/)).not.toBeInTheDocument()
      expect(
        screen.getByText(
          'Export failed. Please try again or contact an administrator.'
        )
      ).toBeVisible()
    } else {
      expect(screen.getByText(/keep the service responsive/)).toBeVisible()
      expect(screen.getByText(/server storage/)).toBeVisible()
    }
    client.clear()
  }
)
it('creates a task with only self filters and closes after submission', async () => {
  const closed = vi.fn()
  const client = new QueryClient({
    defaultOptions: { mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SelfExportSheet open onOpenChange={closed} />
    </QueryClientProvider>
  )
  await userEvent.click(screen.getByRole('button', { name: 'Export' }))
  await waitFor(() => expect(mocks.create).toHaveBeenCalledTimes(1))
  const body = mocks.create.mock.calls[0][0]
  expect(body.filters).toEqual({
    start_timestamp: 1700000000,
    end_timestamp: 1700003600,
    model_name: 'gpt-test',
    token_name: 'my-token',
    type: 2,
    group: undefined,
  })
  expect(body.options).toMatchObject({ header: true, csv_bom: true })
  expect(body.filters).not.toHaveProperty('username')
  expect(body.filters).not.toHaveProperty('channel')
  await waitFor(() => expect(closed).toHaveBeenCalledWith(false))
})
it('blocks ranges above the server limit', () => {
  const client = new QueryClient()
  render(
    <QueryClientProvider client={client}>
      <SelfExportSheet open onOpenChange={vi.fn()} maxRangeSec={60} />
    </QueryClientProvider>
  )
  expect(screen.getByRole('button', { name: 'Export' })).toBeDisabled()
  expect(mocks.create).not.toHaveBeenCalled()
})

it('loads self tasks and applies the server time limit in the export center', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <LogExportCenter self />
    </QueryClientProvider>
  )
  await waitFor(() =>
    expect(mocks.jobs).toHaveBeenCalledWith(false, true, {}, '')
  )
  const button = screen.getByRole('button', { name: 'New Export' })
  await waitFor(() => expect(button).toBeEnabled())
  await userEvent.click(button)
  expect(screen.getByRole('button', { name: 'Export' })).toBeDisabled()
  expect(screen.queryByTestId('admin-export-form')).not.toBeInTheDocument()
})

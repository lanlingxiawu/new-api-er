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

import { SelfExportSheet } from '../../export/components/self-export-sheet'
import { LogExportCenter } from '../../export'

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
  getRouteApi: () => ({ useSearch: () => mocks.search }),
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
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <LogExportCenter self />
    </QueryClientProvider>
  )
  await waitFor(() => expect(mocks.jobs).toHaveBeenCalledWith(false, true))
  const button = screen.getByRole('button', { name: 'New Export' })
  await waitFor(() => expect(button).toBeEnabled())
  await userEvent.click(button)
  expect(screen.getByRole('button', { name: 'Export' })).toBeDisabled()
  expect(screen.queryByTestId('admin-export-form')).not.toBeInTheDocument()
})

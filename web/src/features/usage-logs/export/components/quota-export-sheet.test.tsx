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

import { useAuthStore } from '@/stores/auth-store'

import { LogExportCenter } from '..'

const mocks = vi.hoisted(() => ({
  create: vi.fn(),
  jobs: vi.fn(),
  navigate: vi.fn(),
  search: {} as {
    exportFilters?: Record<string, unknown>
    exportSource?: 'usage' | 'quota'
  },
  listeners: new Set<() => void>(),
}))

vi.mock('@tanstack/react-router', async () => {
  const { useSyncExternalStore } = await import('react')
  return {
    getRouteApi: () => ({
      useSearch: () =>
        useSyncExternalStore(
          (listener) => {
            mocks.listeners.add(listener)
            return () => {
              mocks.listeners.delete(listener)
            }
          },
          () => mocks.search
        ),
      useNavigate: () => mocks.navigate,
    }),
  }
})
vi.mock('../api', () => ({
  createQuotaExportJob: mocks.create,
  getExportJobs: mocks.jobs,
  deleteExportJob: vi.fn(),
  downloadExportPart: vi.fn(),
}))
vi.mock('./new-export-sheet', () => ({
  NewExportSheet: () => <div data-testid='usage-export-form' />,
}))
vi.mock('./employee-template-manager', () => ({
  EmployeeTemplateManager: () => <div data-testid='employee-templates' />,
}))

const MAX_RANGE = 31 * 86400

function admin(role: number) {
  useAuthStore.getState().auth.setUser({
    id: 7,
    username: 'admin',
    role,
    permissions: {
      admin_permissions: { 'admin_menu.employees': { view: true } },
    },
  })
}

function renderCenter(self = false) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <LogExportCenter self={self} />
    </QueryClientProvider>
  )
  return client
}

beforeEach(() => {
  mocks.create.mockReset()
  mocks.create.mockResolvedValue('job-1')
  mocks.navigate.mockReset()
  mocks.search = {}
  mocks.navigate.mockImplementation(({ search }) => {
    mocks.search = typeof search === 'function' ? search(mocks.search) : search
    mocks.listeners.forEach((listener) => listener())
    return Promise.resolve()
  })
  mocks.jobs.mockReset()
  mocks.jobs.mockResolvedValue({
    enabled: true,
    max_range_sec: MAX_RANGE,
    jobs: [
      {
        job_id: 'quota-job',
        status: 'ready',
        progress: 100,
        row_count: 2,
        bytes_out: 10,
        format: 'csv_gz',
        filters: { start_timestamp: 1700000000, end_timestamp: 1700086400 },
        quota_filters: { username: 'alice', channel_id: 0 },
        parts: [{ part: 1 }],
        created_at: 1700000000,
        updated_at: 1700000010,
      },
    ],
  })
  admin(10)
})

afterEach(() => {
  cleanup()
  useAuthStore.getState().auth.reset()
})

it('keeps quota export off the self-service page', async () => {
  mocks.search.exportSource = 'quota'
  renderCenter(true)
  await screen.findByRole('button', { name: 'New Export' })
  expect(
    screen.queryByRole('button', { name: 'Quota Data' })
  ).not.toBeInTheDocument()
  expect(screen.queryByTestId('employee-templates')).not.toBeInTheDocument()
  expect(mocks.jobs).toHaveBeenCalledWith(
    false,
    true,
    {},
    '',
    '/api/log/self/export'
  )
})

it('restores the export data source from the route search', async () => {
  mocks.search.exportSource = 'quota'
  renderCenter()
  await waitFor(() =>
    expect(mocks.jobs).toHaveBeenCalledWith(
      false,
      false,
      {},
      '',
      '/api/data/quota/export'
    )
  )
  expect(screen.getByRole('button', { name: 'Quota Data' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  expect(screen.queryByTestId('employee-templates')).not.toBeInTheDocument()
})

it('opens the quota form for an administrator and sends a stored channel zero', async () => {
  const user = userEvent.setup()
  renderCenter()
  expect(await screen.findByTestId('employee-templates')).toBeVisible()
  expect(screen.queryByText('alice · #0')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Quota Data' }))
  await waitFor(() =>
    expect(mocks.jobs).toHaveBeenCalledWith(
      false,
      false,
      {},
      '',
      '/api/data/quota/export'
    )
  )
  expect(screen.queryByTestId('employee-templates')).not.toBeInTheDocument()
  expect(await screen.findByText('alice · #0')).toBeVisible()

  await user.click(screen.getByRole('button', { name: 'New Export' }))
  const dialog = screen.getByRole('dialog', { name: 'Quota Data' })
  expect(dialog).toBeVisible()
  expect(screen.getByLabelText('User ID')).toBeVisible()
  expect(screen.getByLabelText('Channel ID')).toHaveAttribute(
    'inputmode',
    'numeric'
  )
  expect(screen.getByLabelText('Username')).toHaveAttribute('inputmode', 'text')
  expect(screen.queryByLabelText('Token ID')).not.toBeInTheDocument()
  expect(screen.queryByLabelText('Node')).not.toBeInTheDocument()
  expect(screen.queryByText('Invalid parameters')).not.toBeInTheDocument()

  await user.type(screen.getByLabelText('User ID'), '1.5')
  expect(screen.getByRole('alert')).toHaveTextContent('Invalid parameters')
  expect(screen.getByRole('button', { name: 'Export' })).toBeDisabled()
  await user.clear(screen.getByLabelText('User ID'))
  await user.type(screen.getByLabelText('Channel ID'), '0')
  await user.click(screen.getByRole('button', { name: 'Export' }))
  await waitFor(() => expect(mocks.create).toHaveBeenCalledTimes(1))
  const payload = mocks.create.mock.calls[0][0]
  expect(payload.filters.channel_id).toBe(0)
  expect(payload.filters).not.toHaveProperty('user_id')
  expect(payload.filters).not.toHaveProperty('username')
  expect(payload.filters).not.toHaveProperty('token_id')
  expect(payload.filters).not.toHaveProperty('node_name')
  expect(payload.options.header).toBe(true)
  expect(payload.options.csv_bom).toBe(true)
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )

  admin(100)
  await user.click(screen.getByRole('button', { name: 'New Export' }))
  expect(screen.getByLabelText('Token ID')).toBeVisible()
  expect(screen.getByLabelText('Node')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  await user.click(screen.getByRole('button', { name: 'Usage Logs' }))
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(screen.getByTestId('employee-templates')).toBeVisible()
  expect(screen.getByTestId('usage-export-form')).toBeInTheDocument()
})

it('keeps usage-only task filters off the quota list', async () => {
  mocks.search.exportFilters = {
    mode: 'summary',
    source: 'employee',
    customer_id: 4,
    status: 'ready',
  }
  mocks.jobs.mockResolvedValue({
    enabled: false,
    max_range_sec: MAX_RANGE,
    jobs: [],
  })
  const user = userEvent.setup()
  renderCenter()
  expect(
    await screen.findByText('Log export is currently turned off')
  ).toBeVisible()
  expect(screen.getByRole('button', { name: 'New Export' })).toBeDisabled()
  expect(mocks.jobs).toHaveBeenCalledWith(
    false,
    false,
    expect.objectContaining({
      mode: 'summary',
      source: 'employee',
      customer_id: 4,
      status: 'ready',
    }),
    '',
    '/api/log/export'
  )

  await user.click(screen.getByRole('button', { name: 'Quota Data' }))
  await waitFor(() =>
    expect(mocks.jobs).toHaveBeenCalledWith(
      false,
      false,
      { status: 'ready' },
      '',
      '/api/data/quota/export'
    )
  )
  expect(screen.queryByText('Customer')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'More filters' }))
  expect(
    screen.queryByRole('combobox', { name: 'Export source' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('combobox', { name: 'Export mode' })
  ).not.toBeInTheDocument()
  expect(screen.getByRole('combobox', { name: 'Export scope' })).toBeVisible()
  expect(screen.queryByText(/created by this customer/)).not.toBeInTheDocument()

  await user.click(screen.getByRole('button', { name: 'Reset' }))
  const update = mocks.navigate.mock.calls.at(-1)?.[0].search as (previous: {
    exportFilters?: Record<string, unknown>
  }) => { exportFilters?: Record<string, unknown> }
  expect(
    update({ exportFilters: mocks.search.exportFilters }).exportFilters
  ).toEqual({
    mode: 'summary',
    source: 'employee',
    customer_id: 4,
  })
})

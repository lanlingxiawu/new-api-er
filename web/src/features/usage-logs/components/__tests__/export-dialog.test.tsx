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
  download: vi.fn(),
  models: vi.fn(),
  groups: vi.fn(),
  tokens: vi.fn(),
  searchTokens: vi.fn(),
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
vi.mock('@/lib/api', () => ({
  getUserModels: mocks.models,
  getUserGroups: mocks.groups,
}))
vi.mock('@/features/keys/api', () => ({
  getApiKeys: mocks.tokens,
  searchApiKeys: mocks.searchTokens,
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
  downloadExportPart: mocks.download,
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
  mocks.download.mockReset()
  mocks.models
    .mockReset()
    .mockResolvedValue({ success: true, data: ['gpt-test', 'another-model'] })
  mocks.groups
    .mockReset()
    .mockResolvedValue({ success: true, data: { default: {}, vip: {} } })
  mocks.tokens.mockReset().mockResolvedValue({
    success: true,
    data: {
      items: [{ name: 'my-token' }, { name: 'another-token' }],
      total: 2,
    },
  })
  mocks.searchTokens
    .mockReset()
    .mockResolvedValue({ success: true, data: { items: [], total: 0 } })
})
afterEach(cleanup)

it('marks historical types in self exports and retains the selected type', async () => {
  const client = new QueryClient()
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={client}>
      <SelfExportSheet open onOpenChange={vi.fn()} />
    </QueryClientProvider>
  )
  const type = screen.getByRole('combobox', { name: 'Type' })
  await user.click(type)
  await user.click(screen.getByRole('option', { name: /^Login/ }))
  expect(type).toHaveTextContent('LoginDeprecated')
  await user.click(screen.getByRole('button', { name: 'Export' }))
  await waitFor(() => expect(mocks.create).toHaveBeenCalledTimes(1))
  expect(mocks.create.mock.calls[0][0].filters.type).toBe(7)
  client.clear()
})

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
  expect(screen.getByLabelText('Model')).toHaveValue('gpt-test')
  expect(screen.getByLabelText('Token')).toHaveValue('my-token')
  expect(screen.getByRole('combobox', { name: 'Type' })).toHaveTextContent(
    'Consume'
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

it('lets users clear inherited filters before exporting', async () => {
  const client = new QueryClient()
  render(
    <QueryClientProvider client={client}>
      <SelfExportSheet open onOpenChange={vi.fn()} />
    </QueryClientProvider>
  )
  await userEvent.click(screen.getByRole('button', { name: 'Clear filters' }))
  await userEvent.click(screen.getByRole('button', { name: 'Export' }))
  await waitFor(() => expect(mocks.create).toHaveBeenCalledTimes(1))
  expect(mocks.create.mock.calls[0][0].filters).toMatchObject({
    type: 0,
    model_name: undefined,
    token_name: undefined,
    group: undefined,
  })
  client.clear()
})

it('lets users edit inherited filters before exporting', async () => {
  const client = new QueryClient()
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={client}>
      <SelfExportSheet open onOpenChange={vi.fn()} />
    </QueryClientProvider>
  )
  await user.click(screen.getByRole('combobox', { name: 'Type' }))
  await user.click(screen.getByRole('option', { name: 'Error' }))
  await user.clear(screen.getByLabelText('Model'))
  await user.type(screen.getByLabelText('Model'), 'changed-model')
  await user.type(screen.getByLabelText('Group'), 'vip')
  await user.click(screen.getByRole('button', { name: 'Export' }))
  await waitFor(() => expect(mocks.create).toHaveBeenCalledTimes(1))
  expect(mocks.create.mock.calls[0][0].filters).toMatchObject({
    type: 5,
    model_name: 'changed-model',
    token_name: 'my-token',
    group: 'vip',
  })
  client.clear()
})

it('uses searchable suggestions for all self filters and submits selected values', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={client}>
      <SelfExportSheet open onOpenChange={vi.fn()} />
    </QueryClientProvider>
  )
  for (const [label, value] of [
    ['Model', 'another-model'],
    ['Token', 'another-token'],
    ['Group', 'vip'],
  ]) {
    await user.click(screen.getByRole('combobox', { name: label }))
    await user.click(await screen.findByRole('option', { name: value }))
  }
  await user.click(screen.getByRole('button', { name: 'Export' }))
  await waitFor(() => expect(mocks.create).toHaveBeenCalledTimes(1))
  expect(mocks.create.mock.calls[0][0].filters).toMatchObject({
    model_name: 'another-model',
    token_name: 'another-token',
    group: 'vip',
  })
  client.clear()
})

it('keeps historical values usable when suggestions fail and allows retry', async () => {
  mocks.models.mockRejectedValueOnce(new Error('internal details'))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={client}>
      <SelfExportSheet open onOpenChange={vi.fn()} />
    </QueryClientProvider>
  )
  expect(await screen.findByRole('alert')).toHaveTextContent('Failed to load')
  expect(screen.queryByText('internal details')).not.toBeInTheDocument()
  expect(screen.getByRole('combobox', { name: 'Model' })).toHaveValue(
    'gpt-test'
  )
  await user.click(screen.getByRole('button', { name: 'Retry' }))
  await waitFor(() =>
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  )
  await user.clear(screen.getByRole('combobox', { name: 'Model' }))
  await user.type(
    screen.getByRole('combobox', { name: 'Model' }),
    'retired-model'
  )
  await user.click(screen.getByRole('button', { name: 'Export' }))
  await waitFor(() => expect(mocks.create).toHaveBeenCalledTimes(1))
  expect(mocks.create.mock.calls[0][0].filters.model_name).toBe('retired-model')
  client.clear()
})

it('paginates own token suggestions, deduplicates names and searches remotely', async () => {
  mocks.tokens.mockImplementation(({ p }) =>
    Promise.resolve({
      success: true,
      data: {
        items:
          p === 1
            ? [{ name: 'my-token' }]
            : [{ name: 'my-token' }, { name: 'page-two-token' }],
        total: 51,
      },
    })
  )
  mocks.searchTokens.mockResolvedValue({
    success: true,
    data: { items: [{ name: 'remote-token' }], total: 1 },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={client}>
      <SelfExportSheet open onOpenChange={vi.fn()} />
    </QueryClientProvider>
  )
  const token = screen.getByRole('combobox', { name: 'Token' })
  await user.click(token)
  await user.click(await screen.findByRole('button', { name: 'Load more' }))
  expect(
    await screen.findByRole('option', { name: 'page-two-token' })
  ).toBeVisible()
  expect(screen.getAllByRole('option', { name: 'my-token' })).toHaveLength(1)
  expect(mocks.tokens).toHaveBeenCalledWith({ p: 2, size: 50 })
  await user.clear(token)
  await user.type(token, 'remote')
  await user.click(await screen.findByRole('option', { name: 'remote-token' }))
  expect(mocks.searchTokens).toHaveBeenCalledWith({
    keyword: 'remote',
    p: 1,
    size: 50,
  })
  expect(token).toHaveValue('remote-token')
  client.clear()
})

it.each([1, 2])(
  'shows type, expiry and empty results and downloads %i files appropriately',
  async (partCount) => {
    const client = new QueryClient()
    mocks.jobs.mockResolvedValue({
      enabled: true,
      jobs: [
        {
          job_id: 'ready-job',
          status: 'ready',
          row_count: 0,
          bytes_out: 100,
          format: 'csv_gz',
          filters: { type: 5 },
          created_at: 1700000000,
          updated_at: 1700000010,
          expires_at: 1900000000,
          can_download: true,
          parts: Array.from({ length: partCount }, (_, i) => ({
            index: i + 1,
            rows: 0,
            bytes: 100,
            start_time: 1700000000,
            end_time: 1700000000,
          })),
        },
      ],
    })
    render(
      <QueryClientProvider client={client}>
        <LogExportCenter self />
      </QueryClientProvider>
    )
    expect(await screen.findByText('Error')).toBeVisible()
    expect(screen.getByText(/Download available until/)).toBeVisible()
    expect(
      screen.getByText(
        'No logs matched these filters. Adjust the filters and try again.'
      )
    ).toBeVisible()
    await userEvent.click(screen.getByRole('button', { name: 'Download' }))
    if (partCount === 1) {
      await waitFor(() =>
        expect(mocks.download).toHaveBeenCalledWith('ready-job', 1, true)
      )
      expect(screen.queryByRole('menu')).not.toBeInTheDocument()
    } else {
      expect(mocks.download).not.toHaveBeenCalled()
      await userEvent.click(screen.getByRole('menuitem', { name: /All parts/ }))
      await waitFor(() =>
        expect(mocks.download).toHaveBeenCalledWith(
          'ready-job',
          undefined,
          true
        )
      )
    }
    client.clear()
  }
)
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

it('restores page filters when reopening a canceled self export', async () => {
  const client = new QueryClient()
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={client}>
      <LogExportCenter self />
    </QueryClientProvider>
  )
  const button = screen.getByRole('button', { name: 'New Export' })
  await waitFor(() => expect(button).toBeEnabled())
  await user.click(button)
  await user.click(screen.getByRole('button', { name: 'Clear filters' }))
  expect(screen.getByLabelText('Model')).toHaveValue('')
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  await user.click(button)
  expect(screen.getByLabelText('Model')).toHaveValue('gpt-test')
  expect(screen.getByRole('combobox', { name: 'Type' })).toHaveTextContent(
    'Consume'
  )
  client.clear()
})

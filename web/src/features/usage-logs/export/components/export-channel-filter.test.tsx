import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState, type ReactNode } from 'react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { ExportFilterSelect } from './export-filter-select'
import { NewExportSheet } from './new-export-sheet'

const mocks = vi.hoisted(() => ({
  request: vi.fn(),
  create: vi.fn(),
  estimate: vi.fn(),
}))
vi.mock('../employee-api', () => ({ exportRequest: mocks.request }))
vi.mock('@/stores/auth-store', () => ({
  useAuthStore: (
    selector: (state: {
      auth: { user: { id: number; role: number } }
    }) => unknown
  ) => selector({ auth: { user: { id: 1, role: 10 } } }),
}))
vi.mock('../api', () => ({
  createExportJob: mocks.create,
  getExportEstimate: mocks.estimate,
  getExportTemplates: async () => [],
  createExportTemplate: vi.fn(),
  getExportColumns: async () => ({
    columns: [
      { key: 'quota', label: 'Quota', group: 'billing', admin_only: false },
    ],
    builtin_templates: [
      { id: 'default', name: 'Default', columns: ['quota'], is_default: true },
    ],
    max_columns: 100,
    max_range_sec: 2678400,
    quota_per_unit: 500000,
    summary_dimensions: ['date'],
    anomaly_kinds: [],
  }),
}))

const clients: QueryClient[] = []

function renderWithQuery(children: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  return render(
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function ChannelFilter(props: { initialValue?: string }) {
  const [value, setValue] = useState(props.initialValue ?? '')
  return (
    <ExportFilterSelect
      field='channel'
      label='Channel'
      value={value}
      onChange={setValue}
      showId
    />
  )
}

beforeEach(() => {
  mocks.request.mockReset().mockImplementation((_method, url: string) => {
    const params = new URL(url, 'http://localhost').searchParams
    if (params.get('field') !== 'channel') {
      return Promise.resolve({ items: [], next_cursor: '' })
    }
    const keyword = params.get('keyword')?.trim() ?? ''
    if (keyword === 'Far channel' || keyword === '530') {
      return Promise.resolve({
        items: [{ value: '530', label: 'Far channel' }],
        next_cursor: '',
      })
    }
    if (keyword === '#999') {
      return Promise.resolve({
        items: [{ value: '999', label: '999' }],
        next_cursor: '',
      })
    }
    return Promise.resolve({
      items: [{ value: '275', label: 'First channel' }],
      next_cursor: '',
    })
  })
  mocks.create.mockReset().mockResolvedValue('job')
  mocks.estimate.mockReset().mockResolvedValue({ rows: 2, capped: false })
})

afterEach(() => {
  cleanup()
  for (const client of clients) client.clear()
  clients.length = 0
})

it('resolves a selected channel outside the first page without requiring log dates', async () => {
  renderWithQuery(<ChannelFilter initialValue='530' />)
  await waitFor(() =>
    expect(screen.getByRole('combobox', { name: 'Channel' })).toHaveValue(
      'Far channel · #530'
    )
  )
})

it('accepts a historical ID returned by the server and lets it be cleared', async () => {
  const user = userEvent.setup()
  renderWithQuery(<ChannelFilter />)
  const input = screen.getByRole('combobox', { name: 'Channel' })
  await user.click(input)
  await user.type(input, '#999')
  await user.click(await screen.findByRole('option', { name: '999' }))
  expect(input).toHaveValue('999')
  await user.click(screen.getByRole('button', { name: 'Clear' }))
  expect(input).toHaveValue('')
})

it('searches by channel name and uses its numeric ID for estimation and export', async () => {
  const user = userEvent.setup()
  renderWithQuery(
    <NewExportSheet
      open
      onOpenChange={vi.fn()}
      prefill={{ start: new Date('2026-11-01'), end: new Date('2026-11-02') }}
    />
  )
  const input = await screen.findByRole('combobox', { name: 'Channel' })
  await user.click(input)
  await user.type(input, 'Far channel')
  await user.click(
    await screen.findByRole('option', { name: 'Far channel · #530' })
  )
  expect(input).toHaveValue('Far channel · #530')
  await waitFor(() =>
    expect(mocks.estimate).toHaveBeenLastCalledWith(
      expect.objectContaining({ channel: 530 })
    )
  )
  await user.click(screen.getByRole('button', { name: 'Start export' }))
  await waitFor(() =>
    expect(mocks.create).toHaveBeenCalledWith(
      expect.objectContaining({ channel: 530 })
    )
  )
})

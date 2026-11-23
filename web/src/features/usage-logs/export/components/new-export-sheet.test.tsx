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

import { NewExportSheet } from './new-export-sheet'

const mocks = vi.hoisted(() => ({
  create: vi.fn(),
  estimate: vi.fn(),
  saveTemplate: vi.fn(),
  getTemplates: vi.fn(),
  columns: ['quota'],
  maxColumns: 100,
  templates: [] as {
    id: number
    name: string
    columns: string[]
    is_shared: boolean
  }[],
  role: 10,
}))
vi.mock('@/stores/auth-store', () => ({
  useAuthStore: (
    selector: (state: { auth: { user: { role: number } } }) => unknown
  ) => selector({ auth: { user: { role: mocks.role } } }),
}))
vi.mock('../api', () => ({
  createExportJob: mocks.create,
  getExportEstimate: mocks.estimate,
  getExportTemplates: mocks.getTemplates,
  createExportTemplate: mocks.saveTemplate,
  getExportColumns: async () => ({
    columns: [
      {
        key: 'quota',
        label: 'Quota',
        group: 'billing',
        admin_only: false,
        audience: 'customer',
      },
      {
        key: 'other_raw',
        label: 'Raw Other JSON',
        group: 'diagnostic',
        admin_only: true,
        audience: 'internal',
      },
      {
        key: 'log_raw',
        label: 'Raw Log JSON',
        group: 'diagnostic',
        admin_only: true,
        audience: 'internal',
      },
      {
        key: 'request_id',
        label: 'Request ID',
        group: 'basic',
        admin_only: false,
        audience: 'customer',
      },
    ],
    builtin_templates: [
      {
        id: 'default',
        name: 'Default',
        columns: mocks.columns,
        is_default: true,
        purpose: 'reconciliation',
        audience: 'customer',
      },
    ],
    max_columns: mocks.maxColumns,
    max_range_sec: 2678400,
    quota_per_unit: 500000,
    summary_dimensions: ['date'],
    anomaly_kinds: [],
  }),
}))
vi.mock('./export-filter-select', () => ({ ExportFilterSelect: () => null }))

beforeEach(() => {
  mocks.role = 10
  mocks.columns = ['quota']
  mocks.maxColumns = 100
  mocks.templates = []
  mocks.getTemplates.mockReset().mockImplementation(async () => mocks.templates)
  mocks.saveTemplate
    .mockReset()
    .mockImplementation(async (input: { name: string; columns: string[] }) => {
      const template = { id: 1, ...input, is_shared: false }
      mocks.templates = [template]
      return template
    })
  mocks.create.mockReset().mockResolvedValue('job')
  mocks.estimate.mockReset().mockResolvedValue({ rows: 2, capped: false })
})
afterEach(cleanup)

it('keeps selected columns while the saved template is awaiting refetch', async () => {
  mocks.getTemplates
    .mockResolvedValueOnce([])
    .mockImplementation(() => new Promise(() => {}))
  mocks.saveTemplate.mockImplementation(async (input) => ({
    id: 2,
    ...input,
    is_shared: false,
  }))
  const user = userEvent.setup()
  showSheet()
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Start export' })).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: 'Request ID' }))
  await user.type(
    screen.getByPlaceholderText('Template name'),
    'Selected columns'
  )
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(mocks.saveTemplate).toHaveBeenCalledOnce())
  await user.click(screen.getByRole('button', { name: 'Start export' }))
  await waitFor(() => expect(mocks.create).toHaveBeenCalledOnce())
  expect(mocks.create.mock.calls[0][0].columns).toEqual(['quota', 'request_id'])
})

function showSheet() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const view = render(
    <QueryClientProvider client={client}>
      <NewExportSheet
        open
        onOpenChange={() => {}}
        prefill={{ start: new Date('2026-11-01'), end: new Date('2026-11-02') }}
      />
    </QueryClientProvider>
  )
  return view
}

it('submits an explicit zero override without changing filters or requesting another estimate', async () => {
  const user = userEvent.setup()
  showSheet()
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Start export' })).toBeEnabled()
  )
  await user.click(screen.getByRole('switch', { name: 'Override group ratio' }))
  const input = screen.getByRole('textbox', { name: 'New group ratio' })
  await user.clear(input)
  await user.type(input, '0')
  const estimates = mocks.estimate.mock.calls.length
  await user.click(screen.getByRole('button', { name: 'Start export' }))
  await waitFor(() => expect(mocks.create).toHaveBeenCalledOnce())
  expect(mocks.create.mock.calls[0][0]).toMatchObject({
    group_ratio_override: '0',
    columns: ['quota'],
  })
  expect(mocks.estimate.mock.calls).toHaveLength(estimates)
})

it('validates the ratio and omits it after the override is turned off', async () => {
  const user = userEvent.setup()
  showSheet()
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Start export' })).toBeEnabled()
  )
  const toggle = screen.getByRole('switch', { name: 'Override group ratio' })
  await user.click(toggle)
  const input = screen.getByRole('textbox', { name: 'New group ratio' })
  await user.clear(input)
  await user.type(input, '10001')
  expect(screen.getByRole('button', { name: 'Start export' })).toBeDisabled()
  await user.click(toggle)
  await user.click(screen.getByRole('button', { name: 'Start export' }))
  await waitFor(() => expect(mocks.create).toHaveBeenCalledOnce())
  expect(mocks.create.mock.calls[0][0].group_ratio_override).toBeUndefined()
})

it('does not expose group ratio override to a non-administrator', async () => {
  mocks.role = 1
  showSheet()
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Start export' })).toBeEnabled()
  )
  expect(
    screen.queryByRole('switch', { name: 'Override group ratio' })
  ).not.toBeInTheDocument()
})

it('preserves the hidden raw column when editing and saving a template during override', async () => {
  mocks.role = 100
  mocks.columns = ['quota', 'other_raw']
  const user = userEvent.setup()
  showSheet()
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Start export' })).toBeEnabled()
  )
  const toggle = screen.getByRole('switch', { name: 'Override group ratio' })
  await user.click(toggle)
  expect(screen.queryByText('Raw Other JSON')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Request ID' }))
  await user.type(screen.getByPlaceholderText('Template name'), 'With raw data')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(mocks.saveTemplate).toHaveBeenCalledOnce())
  expect(mocks.saveTemplate.mock.calls[0][0].columns).toEqual([
    'quota',
    'other_raw',
    'request_id',
  ])
  await user.click(screen.getByRole('button', { name: 'Start export' }))
  await waitFor(() => expect(mocks.create).toHaveBeenCalledOnce())
  expect(mocks.create.mock.calls[0][0].columns).toEqual(['quota', 'request_id'])
  await user.click(toggle)
  expect(screen.getByText('Raw Other JSON')).toBeInTheDocument()
})

it('allows summary without detail columns while detail and both still require them', async () => {
  mocks.role = 100
  mocks.columns = ['other_raw']
  const user = userEvent.setup()
  showSheet()
  const submit = screen.getByRole('button', { name: 'Start export' })
  await waitFor(() => expect(submit).toBeEnabled())
  await user.click(screen.getByRole('switch', { name: 'Override group ratio' }))
  expect(submit).toBeDisabled()
  await user.click(screen.getByRole('radio', { name: 'Detail + summary' }))
  expect(submit).toBeDisabled()
  await user.click(screen.getByRole('radio', { name: 'Aggregated summary' }))
  expect(submit).toBeEnabled()
  await user.click(submit)
  await waitFor(() => expect(mocks.create).toHaveBeenCalledOnce())
  expect(mocks.create.mock.calls[0][0]).toMatchObject({
    mode: 'summary',
    columns: [],
    group_ratio_override: '1',
  })
})

it.each([
  { action: 'Move down', expected: ['request_id', 'other_raw', 'quota'] },
  { action: 'Remove', expected: ['request_id', 'other_raw'] },
  { action: 'Clear', expected: ['other_raw'] },
])(
  'preserves the hidden selection after $action',
  async ({ action, expected }) => {
    mocks.role = 100
    mocks.columns = ['quota', 'other_raw', 'request_id']
    const user = userEvent.setup()
    showSheet()
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Start export' })).toBeEnabled()
    )
    const toggle = screen.getByRole('switch', { name: 'Override group ratio' })
    await user.click(toggle)
    await user.click(screen.getAllByRole('button', { name: action })[0])
    await user.click(toggle)
    await user.click(screen.getByRole('button', { name: 'Start export' }))
    await waitFor(() => expect(mocks.create).toHaveBeenCalledOnce())
    expect(mocks.create.mock.calls[0][0].columns).toEqual(expected)
  }
)

it('counts the hidden selection toward the template column limit', async () => {
  mocks.role = 100
  mocks.columns = ['quota', 'other_raw']
  mocks.maxColumns = 2
  const user = userEvent.setup()
  showSheet()
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Start export' })).toBeEnabled()
  )
  await user.click(screen.getByRole('switch', { name: 'Override group ratio' }))
  expect(screen.getByRole('button', { name: 'Request ID' })).toBeDisabled()
})

it('exports stored raw fields and blocks summary or repricing without dropping the selection', async () => {
  mocks.role = 100
  mocks.columns = ['quota', 'log_raw']
  const user = userEvent.setup()
  showSheet()
  const submit = screen.getByRole('button', { name: 'Start export' })
  await waitFor(() => expect(submit).toBeEnabled())
  expect(
    screen.getByText(
      'Includes stored log fields and diagnostics. Existing body omissions are preserved.'
    )
  ).toBeInTheDocument()
  await user.click(screen.getByRole('radio', { name: 'Aggregated summary' }))
  expect(submit).toBeDisabled()
  expect(screen.getByRole('alert')).toHaveTextContent('include details')
  await user.click(screen.getByRole('radio', { name: 'Detail + summary' }))
  expect(submit).toBeEnabled()
  const toggle = screen.getByRole('switch', { name: 'Override group ratio' })
  await user.click(toggle)
  expect(submit).toBeDisabled()
  expect(screen.getByRole('alert')).toHaveTextContent(
    'turn off the group ratio override'
  )
  await user.click(toggle)
  await user.click(submit)
  await waitFor(() => expect(mocks.create).toHaveBeenCalledOnce())
  expect(mocks.create.mock.calls[0][0]).toMatchObject({
    mode: 'both',
    columns: ['quota', 'log_raw'],
    summary_dims: ['date'],
  })
})

it('blocks a raw selection cached before losing root access', async () => {
  mocks.role = 10
  mocks.columns = ['quota', 'log_raw']
  showSheet()
  await waitFor(() =>
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Complete raw fields require root access.'
    )
  )
  expect(screen.getByRole('button', { name: 'Start export' })).toBeDisabled()
  expect(mocks.create).not.toHaveBeenCalled()
})

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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { DataCleanupControl } from '../data-cleanup'

const api = vi.hoisted(() => ({
  preview: vi.fn(),
  start: vi.fn(),
  get: vi.fn(),
}))
vi.mock('../data-cleanup-api', () => ({
  previewDataCleanup: api.preview,
  startDataCleanup: api.start,
  getDataCleanup: api.get,
}))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, values?: Record<string, unknown>) =>
      key.replaceAll(/{{(\w+)}}/g, (_, key: string) =>
        String(values?.[key] ?? '')
      ),
  }),
}))
vi.mock('@/components/datetime-picker', () => ({
  DateTimePicker: (props: { value?: Date; onChange: (date: Date) => void }) => (
    <input
      aria-label='Cleanup date'
      type='datetime-local'
      value={props.value?.toISOString().slice(0, 16)}
      onChange={(event) => props.onChange(new Date(`${event.target.value}Z`))}
    />
  ),
}))
vi.mock('@/components/confirm-dialog', () => ({
  ConfirmDialog: (props: {
    open: boolean
    title: string
    desc: string
    handleConfirm: () => void
    isLoading: boolean
  }) =>
    props.open && (
      <div role='dialog'>
        {props.title}
        {props.desc}
        <button
          type='button'
          disabled={props.isLoading}
          onClick={props.handleConfirm}
        >
          Confirm deletion
        </button>
      </div>
    ),
}))
vi.mock('sonner', () => ({ toast: { success: vi.fn() } }))

function renderCleanup() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <DataCleanupControl kind='error_logs' />
    </QueryClientProvider>
  )
}

describe('date-based cleanup', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.get.mockResolvedValue(null)
    api.preview.mockResolvedValue({
      id: 'preview',
      kind: 'error_logs',
      before: 1704067200,
      matched: 12,
      count_exact: true,
      status: 'preview',
    })
    api.start.mockResolvedValue({
      id: 'preview',
      kind: 'error_logs',
      before: 1704067200,
      status: 'pending',
      deleted: 0,
      scanned: 0,
    })
  })
  it('previews the selected date and only deletes after explicit confirmation', async () => {
    renderCleanup()
    await waitFor(() =>
      expect(
        (
          screen.getByRole('button', {
            name: 'Preview cleanup',
          }) as HTMLButtonElement
        ).disabled
      ).toBe(false)
    )
    fireEvent.change(screen.getByLabelText('Cleanup date'), {
      target: { value: '2024-01-01T00:00' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Preview cleanup' }))
    await waitFor(() =>
      expect(api.preview).toHaveBeenCalledWith('error_logs', 1704067200)
    )
    expect(api.start).not.toHaveBeenCalled()
    fireEvent.click(
      await screen.findByRole('button', { name: 'Confirm deletion' })
    )
    await waitFor(() => expect(api.start.mock.calls[0]?.[0]).toBe('preview'))
  })
  it('restores an active task and prevents another preview', async () => {
    api.get.mockResolvedValue({
      id: 'active',
      kind: 'error_logs',
      status: 'running',
      deleted: 42,
      scanned: 42,
      before: 1704067200,
    })
    renderCleanup()
    await screen.findByText('Cleanup is running in the background.')
    expect(
      (
        screen.getByRole('button', {
          name: 'Preview cleanup',
        }) as HTMLButtonElement
      ).disabled
    ).toBe(true)
  })
})

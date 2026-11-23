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
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { DataCleanupControl } from '../data-cleanup'

const api = vi.hoisted(() => ({
  preview: vi.fn(),
  start: vi.fn(),
  direct: vi.fn(),
  get: vi.fn(),
}))
vi.mock('../data-cleanup-api', () => ({
  previewDataCleanup: api.preview,
  startDataCleanup: api.start,
  startErrorLogCleanup: api.direct,
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

function renderCleanup(
  kind: 'error_logs' | 'ledger' | 'commission' = 'error_logs'
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <DataCleanupControl kind={kind} />
    </QueryClientProvider>
  )
}

describe('date-based cleanup', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })
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
    api.direct.mockResolvedValue({
      id: 'direct',
      kind: 'error_logs',
      before: 1704067200,
      status: 'pending',
      deleted: 0,
      scanned: 0,
    })
  })
  it('confirms the selected date without preview and submits once after confirmation', async () => {
    renderCleanup()
    await waitFor(() =>
      expect(
        (
          screen.getByRole('button', {
            name: 'Clean error usage logs',
          }) as HTMLButtonElement
        ).disabled
      ).toBe(false)
    )
    fireEvent.change(screen.getByLabelText('Cleanup date'), {
      target: { value: '2024-01-01T00:00' },
    })
    fireEvent.click(
      screen.getByRole('button', { name: 'Clean error usage logs' })
    )
    expect(api.preview).not.toHaveBeenCalled()
    expect(api.direct).not.toHaveBeenCalled()
    expect(api.start).not.toHaveBeenCalled()
    fireEvent.click(
      await screen.findByRole('button', { name: 'Confirm deletion' })
    )
    await waitFor(() => expect(api.direct).toHaveBeenCalledWith(1704067200))
    expect(api.direct).toHaveBeenCalledTimes(1)
    expect(api.preview).not.toHaveBeenCalled()
    expect(api.start).not.toHaveBeenCalled()
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
          name: 'Clean error usage logs',
        }) as HTMLButtonElement
      ).disabled
    ).toBe(true)
  })
  it('distinguishes queued work from an expired worker lease', async () => {
    api.get
      .mockResolvedValueOnce({
        id: 'active',
        kind: 'error_logs',
        status: 'pending',
        deleted: 0,
        scanned: 0,
        before: 1704067200,
      })
      .mockResolvedValue({
        id: 'active',
        kind: 'error_logs',
        status: 'running',
        deleted: 0,
        scanned: 0,
        before: 1704067200,
        progress: {
          phase: 'scanning',
          lease_valid: false,
          checkpoint_at: 0,
          heartbeat_at: 0,
          cursor_id: 0,
          snapshot_max_id: 1000000001,
        },
      })
    renderCleanup()
    await screen.findByText('Cleanup is waiting for a worker.')
    expect(
      screen.queryByText('Cleanup is running in the background.')
    ).toBeNull()
    await screen.findByText(
      'Cleanup has lost its heartbeat. Waiting for task recovery.',
      {},
      { timeout: 4000 }
    )
    expect(
      screen.queryByRole('button', { name: 'Continue cleanup' })
    ).toBeNull()
  })
  it('labels cached progress after polling fails and recovers on retry', async () => {
    const run = {
      id: 'active',
      kind: 'error_logs',
      status: 'running',
      deleted: 0,
      scanned: 0,
      before: 1704067200,
    }
    api.get.mockResolvedValueOnce(run).mockRejectedValue(new Error('offline'))
    renderCleanup()
    await screen.findByText('Cleanup is running in the background.')
    await screen.findByText(
      'Cleanup status could not be refreshed. Retry to see the latest progress.',
      {},
      { timeout: 4000 }
    )
    expect(
      screen.queryByText('Cleanup is running in the background.')
    ).toBeNull()
    expect(
      screen.getByRole('button', { name: 'Clean error usage logs' })
    ).toBeDisabled()
    api.get.mockResolvedValue({
      ...run,
      status: 'succeeded',
      deleted: 2000,
      scanned: 3000,
    })
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await screen.findByText('Cleanup completed.')
    await screen.findByText('At least 2000 records deleted.')
  })
  it('keeps the confirmed cutoff while the date input changes', async () => {
    renderCleanup()
    const button = screen.getByRole('button', {
      name: 'Clean error usage logs',
    })
    await waitFor(() =>
      expect((button as HTMLButtonElement).disabled).toBe(false)
    )
    fireEvent.change(screen.getByLabelText('Cleanup date'), {
      target: { value: '2024-01-01T00:00' },
    })
    fireEvent.click(button)
    await screen.findByRole('dialog')
    fireEvent.change(screen.getByLabelText('Cleanup date'), {
      target: { value: '2025-01-01T00:00' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Confirm deletion' }))
    await waitFor(() => expect(api.direct).toHaveBeenCalledWith(1704067200))
    expect(screen.queryByText('0 records match this cleanup.')).toBeNull()
  })
  it('explains an initial status failure and waits for a successful retry', async () => {
    api.get.mockRejectedValueOnce(new Error('status unavailable'))
    renderCleanup()
    await screen.findByText(
      'Cleanup status could not be refreshed. Retry to see the latest progress.'
    )
    expect(
      screen.getByRole('button', { name: 'Clean error usage logs' })
    ).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Clean error usage logs' })
      ).not.toBeDisabled()
    )
  })
  it('continues a stopped task only after confirmation, using its original run', async () => {
    api.get.mockResolvedValue({
      id: 'stopped',
      kind: 'error_logs',
      before: 1704067200,
      matched: 0,
      count_exact: false,
      status: 'failed',
      scanned: 1000,
      deleted: 2,
      can_resume: true,
    })
    renderCleanup()
    await screen.findByText('1000 records scanned.')
    fireEvent.click(
      await screen.findByRole('button', { name: 'Continue cleanup' })
    )
    await screen.findByText(
      'Continue from saved progress. The original cutoff date and cleanup range are kept.'
    )
    expect(api.start).not.toHaveBeenCalled()
    expect(api.preview).not.toHaveBeenCalled()
    expect(api.direct).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Confirm deletion' }))
    await waitFor(() => expect(api.start).toHaveBeenCalledWith('stopped', true))
  })
  it('does not offer continuation without server eligibility', async () => {
    api.get.mockResolvedValue({
      id: 'stopped',
      kind: 'error_logs',
      before: 1704067200,
      status: 'failed',
      scanned: 1000,
      deleted: 2,
      can_resume: false,
    })
    renderCleanup()
    await screen.findByText('1000 records scanned.')
    expect(
      screen.queryByRole('button', { name: 'Continue cleanup' })
    ).toBeNull()
    await screen.findByText(
      'Cleanup stopped. Completed deletions are kept. You can start a new cleanup.'
    )
  })
  it('disables duplicate confirmation while the direct request is pending', async () => {
    let finish: (value: unknown) => void = () => {}
    api.direct.mockReturnValueOnce(
      new Promise((resolve) => {
        finish = resolve
      })
    )
    renderCleanup()
    const button = screen.getByRole('button', {
      name: 'Clean error usage logs',
    })
    await waitFor(() =>
      expect((button as HTMLButtonElement).disabled).toBe(false)
    )
    fireEvent.click(button)
    const confirm = await screen.findByRole('button', {
      name: 'Confirm deletion',
    })
    fireEvent.click(confirm)
    await waitFor(() =>
      expect((confirm as HTMLButtonElement).disabled).toBe(true)
    )
    fireEvent.click(confirm)
    expect(api.direct).toHaveBeenCalledTimes(1)
    finish({
      id: 'direct',
      status: 'pending',
      before: 1704067200,
      deleted: 0,
      scanned: 0,
    })
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })
  it('keeps the confirmation open after a failed direct request for retry', async () => {
    api.direct.mockRejectedValueOnce(new Error('temporary connection failure'))
    renderCleanup()
    const button = screen.getByRole('button', {
      name: 'Clean error usage logs',
    })
    await waitFor(() =>
      expect((button as HTMLButtonElement).disabled).toBe(false)
    )
    fireEvent.click(button)
    const confirm = await screen.findByRole('button', {
      name: 'Confirm deletion',
    })
    fireEvent.click(confirm)
    await waitFor(() => expect(api.direct).toHaveBeenCalledTimes(1))
    await waitFor(() =>
      expect((confirm as HTMLButtonElement).disabled).toBe(false)
    )
    expect(screen.queryByRole('dialog')).not.toBeNull()
    fireEvent.click(confirm)
    await waitFor(() => expect(api.direct).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })
  it.each(['ledger', 'commission'] as const)(
    'previews %s cleanup at three days before requiring confirmation',
    async (kind) => {
      vi.spyOn(Date, 'now').mockReturnValue(
        new Date('2026-11-21T12:00:00Z').getTime()
      )
      renderCleanup(kind)
      expect(screen.getByLabelText('Cleanup date')).toHaveValue(
        '2026-11-18T12:00'
      )
      const button = screen.getByRole('button', { name: 'Preview cleanup' })
      await waitFor(() =>
        expect((button as HTMLButtonElement).disabled).toBe(false)
      )
      fireEvent.click(button)
      await screen.findByText('12 records match this cleanup.')
      expect(api.preview).toHaveBeenCalledWith(
        kind,
        new Date('2026-11-18T12:00:00Z').getTime() / 1000
      )
      expect(api.direct).not.toHaveBeenCalled()
      expect(api.start).not.toHaveBeenCalled()
      fireEvent.click(screen.getByRole('button', { name: 'Confirm deletion' }))
      await waitFor(() =>
        expect(api.start).toHaveBeenCalledWith('preview', false)
      )
    }
  )
})

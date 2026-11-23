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
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { downloadProfile, getPprofStatus } from '../api'
import { ProfilingControls } from './profiling-controls'

vi.mock('../api', () => ({ getPprofStatus: vi.fn(), downloadProfile: vi.fn() }))
vi.mock('@/features/system-settings/hooks/use-update-option', () => ({
  useUpdateOption: () => ({ isPending: false, mutateAsync: vi.fn() }),
}))
vi.mock(
  '@/features/system-settings/components/settings-save-confirmation',
  () => ({ useSettingsSaveConfirmation: () => vi.fn() })
)

let client: QueryClient
beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  vi.mocked(getPprofStatus).mockResolvedValue({
    success: true,
    data: {
      enabled: true,
      profiles: ['profile', 'heap', 'goroutine', 'trace'],
    },
  })
})
afterEach(() => {
  cleanup()
  client.clear()
  vi.resetAllMocks()
})
function renderControls() {
  render(
    <QueryClientProvider client={client}>
      <ProfilingControls />
    </QueryClientProvider>
  )
}

it('shows a retry state instead of an off switch when status cannot be read', async () => {
  vi.mocked(getPprofStatus).mockRejectedValueOnce(new Error('offline'))
  renderControls()
  expect(
    await screen.findByText('Failed to load profiling status')
  ).toBeVisible()
  expect(screen.queryByRole('switch')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByRole('switch')).toBeChecked()
})

it('validates integer sampling time and prevents duplicate downloads', async () => {
  let finish!: () => void
  vi.mocked(downloadProfile).mockImplementation(
    () =>
      new Promise<void>((resolve) => {
        finish = resolve
      })
  )
  renderControls()
  const seconds = await screen.findByLabelText('Sampling seconds')
  for (const value of ['', '0', '1.5', '121']) {
    fireEvent.change(seconds, { target: { value } })
    await userEvent.click(screen.getByRole('button', { name: 'Download' }))
  }
  expect(downloadProfile).not.toHaveBeenCalled()
  fireEvent.change(seconds, { target: { value: '30' } })
  await userEvent.click(screen.getByRole('button', { name: 'Download' }))
  const pending = screen.getByRole('button', { name: /Preparing download/ })
  expect(pending).toBeDisabled()
  expect(screen.getByRole('switch')).toBeDisabled()
  fireEvent.click(pending)
  expect(downloadProfile).toHaveBeenCalledExactlyOnceWith('profile', {
    seconds: 30,
  })
  await act(async () => finish())
})

it('offers goroutine text as a profile and hides sampling duration for snapshots', async () => {
  renderControls()
  await userEvent.click(
    await screen.findByRole('combobox', { name: 'Profile type' })
  )
  await userEvent.click(screen.getByRole('option', { name: /Goroutine text/ }))
  expect(screen.queryByLabelText('Sampling seconds')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Download' }))
  expect(downloadProfile).toHaveBeenCalledExactlyOnceWith('goroutine', {
    debug: 2,
  })
})

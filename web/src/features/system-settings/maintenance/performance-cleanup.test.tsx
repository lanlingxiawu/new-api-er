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
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { PerformanceSection } from './performance-section'

const requests = vi.hoisted(() => ({
  get: vi.fn(),
  delete: vi.fn(),
  post: vi.fn(),
}))
const access = vi.hoisted(() => ({ canEdit: true }))
vi.mock('@/lib/api', () => ({ api: requests }))
vi.mock('../components/settings-page-access-context', () => ({
  useSettingsPageAccess: () => ({ scope: 'test', canEdit: access.canEdit }),
}))
vi.mock('../components/settings-save-confirmation', () => ({
  useSettingsSaveConfirmation: () => vi.fn(),
}))
vi.mock('../components/settings-page-context', () => ({
  SettingsPageFormActions: () => null,
  useSuppressSettingsSectionHeader: () => false,
}))
vi.mock('@/features/profiling/components/profiling-controls', () => ({
  ProfilingControls: () => <p>Profiling fixture</p>,
}))
afterEach(() => {
  cleanup()
  access.canEdit = true
  useAuthStore.getState().auth.reset()
})

const defaults = {
  'performance_setting.body_governance_enabled': false,
  'performance_setting.body_memory_budget_mb': 0,
  'performance_setting.disk_cache_enabled': false,
  'performance_setting.disk_cache_threshold_mb': 10,
  'performance_setting.disk_cache_max_size_mb': 100,
  'performance_setting.disk_cache_path': '',
  'performance_setting.monitor_enabled': false,
  'performance_setting.monitor_cpu_threshold': 0,
  'performance_setting.monitor_memory_threshold': 0,
  'performance_setting.monitor_disk_threshold': 0,
}

it.each([true, false])(
  'closes cleanup only on business success=%s',
  async (success) => {
    requests.get.mockResolvedValue({ data: { success: true, data: {} } })
    requests.delete
      .mockReset()
      .mockResolvedValue({ data: { success, message: 'Cleanup failed' } })
    const user = userEvent.setup()
    render(
      <QueryClientProvider client={new QueryClient()}>
        <PerformanceSection defaultValues={defaults} />
      </QueryClientProvider>
    )
    await user.click(
      screen.getByRole('tab', { name: 'Diagnostics & maintenance' })
    )
    await user.click(
      screen.getByRole('button', { name: 'Clean up inactive cache' })
    )
    expect(screen.getByRole('alertdialog')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /^Confirm$/ }))
    await waitFor(() => expect(requests.delete).toHaveBeenCalledOnce())
    await waitFor(() => {
      if (success) {
        expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
      } else {
        expect(screen.getByRole('alertdialog')).toBeInTheDocument()
      }
    })
  }
)

function renderPerformance() {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <PerformanceSection defaultValues={defaults} />
    </QueryClientProvider>
  )
}

it('keeps a config draft across tabs and loads root profiling only when requested', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  requests.get.mockResolvedValue({ data: { success: true, data: {} } })
  renderPerformance()
  expect(screen.queryByText('Profiling fixture')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('tab', { name: 'Cache & protection' }))
  fireEvent.change(screen.getByLabelText('Body memory budget (MiB)'), {
    target: { value: '128' },
  })
  await userEvent.click(
    screen.getByRole('tab', { name: 'Diagnostics & maintenance' })
  )
  expect(screen.getByText('Profiling fixture')).toBeVisible()
  await userEvent.click(screen.getByRole('tab', { name: 'Cache & protection' }))
  expect(screen.getByLabelText('Body memory budget (MiB)')).toHaveValue(128)
  expect(screen.getByText('Unsaved changes')).toBeVisible()
  expect(requests.post).not.toHaveBeenCalled()
})

it('allows read-only refresh and navigation while blocking every maintenance control', async () => {
  access.canEdit = false
  requests.get.mockResolvedValue({ data: { success: true, data: {} } })
  renderPerformance()
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Refresh Stats' })).toBeEnabled()
  )
  await userEvent.click(
    screen.getByRole('tab', { name: 'Diagnostics & maintenance' })
  )
  for (const name of [
    'Run GC',
    'Reset cache hit counters',
    'Clean up inactive cache',
  ]) {
    expect(screen.getByRole('button', { name })).toBeDisabled()
  }
  expect(screen.queryByText('Profiling fixture')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('tab', { name: 'Cache & protection' }))
  expect(screen.getByLabelText('Body memory budget (MiB)')).toBeDisabled()
  expect(screen.getByLabelText('Enable Disk Cache')).toBeDisabled()
})

it('preserves the previous snapshot after a failed refresh', async () => {
  requests.get.mockResolvedValueOnce({
    data: {
      success: true,
      data: {
        memory_stats: {
          alloc: 1048576,
          total_alloc: 1048576,
          sys: 1048576,
          num_gc: 1,
          num_goroutine: 1,
        },
      },
    },
  })
  renderPerformance()
  await screen.findAllByText('1 MiB')
  requests.get.mockRejectedValueOnce(new Error('offline'))
  await userEvent.click(screen.getByRole('button', { name: 'Refresh Stats' }))
  expect(
    await screen.findByText('Refresh failed. Showing the last available data.')
  ).toBeVisible()
  expect(screen.getAllByText('1 MiB').length).toBeGreaterThan(0)
})

it('confirms only counter reset and blocks duplicate requests while it runs', async () => {
  requests.get.mockResolvedValue({ data: { success: true, data: {} } })
  let finish!: (value: unknown) => void
  requests.post.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finish = resolve
      })
  )
  renderPerformance()
  await userEvent.click(
    screen.getByRole('tab', { name: 'Diagnostics & maintenance' })
  )
  await userEvent.click(
    screen.getByRole('button', { name: 'Reset cache hit counters' })
  )
  expect(requests.post).not.toHaveBeenCalled()
  const confirm = screen.getByRole('button', { name: 'Reset' })
  await userEvent.click(confirm)
  expect(confirm).toBeDisabled()
  fireEvent.click(confirm)
  expect(requests.post).toHaveBeenCalledExactlyOnceWith(
    '/api/performance/reset_stats'
  )
  await act(async () => finish({ data: { success: true } }))
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
})

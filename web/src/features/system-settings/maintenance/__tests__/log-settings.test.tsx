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
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { SettingsPage } from '../../components/settings-page'
import { HistoryLogCleanup } from '../history-log-cleanup'
import { LogSettingsSection } from '../log-settings-section'

vi.mock('@tanstack/react-router', () => ({
  useParams: () => ({ section: 'logs' }),
}))

async function renderSettings() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SettingsPage
        routePath='/test'
        group='operations'
        defaultSection='logs'
        defaultSettings={{
          LogConsumeEnabled: true,
          auditRetentionDays: 180,
          'log_setting.user_export_enabled': true,
          'log_setting.max_range_days': 30,
        }}
        getSectionMeta={() => ({ titleKey: 'Log maintenance' })}
        getSectionContent={(_, settings) => (
          <LogSettingsSection defaultValues={settings} />
        )}
      />
    </QueryClientProvider>
  )
  return screen.findByRole('spinbutton', {
    name: 'Longest log time range (days)',
  })
}

beforeEach(() => {
  useAuthStore.getState().auth.setUser({
    id: 19,
    username: 'log-maintainer',
    role: 10,
    permissions: {
      admin_permissions: {
        'system_settings.operations.logs': { view: true, edit: true },
      },
    },
  })
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data:
        url === '/api/performance/logs'
          ? { enabled: true, log_dir: '/var/log/new-api', file_count: 3 }
          : null,
    },
  }))
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
})

afterEach(() => useAuthStore.getState().auth.reset())

test('error cleanup resource settings are root-only and save under their own keys', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  await renderSettings()
  const limit = screen.getByRole('spinbutton', {
    name: 'Error log batch limit',
  })
  expect(limit).toHaveValue(5000)
  expect(
    screen.getByRole('spinbutton', {
      name: 'Error log batch timeout (seconds)',
    })
  ).toHaveValue(5)
  fireEvent.change(limit, { target: { value: '10000' } })
  fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() =>
    expect(api.put).toHaveBeenCalledWith('/api/option/', {
      key: 'data_cleanup_setting.error_log_batch_size',
      value: 10000,
      scope: 'operations.logs',
    })
  )
})

test('ordinary log administrators do not get error cleanup resource controls', async () => {
  await renderSettings()
  expect(
    screen.queryByRole('spinbutton', { name: 'Error log batch limit' })
  ).toBeNull()
  expect(
    screen.queryByRole('switch', {
      name: 'Automatically continue error log cleanup',
    })
  ).toBeNull()
})

test('log maintenance audit cleanup confirms its global scope, uses the existing task API, and refreshes on completion', async () => {
  const user = userEvent.setup()
  const invalidate = vi.spyOn(QueryClient.prototype, 'invalidateQueries')
  const task = {
    task_id: 'audit-cleanup',
    status: 'running',
    payload: {
      scope: 'audit',
      target_timestamp: 0,
      audit_target_timestamp: 1234,
    },
    state: { total: 10, processed: 5, progress: 50, remaining: 5 },
  }
  vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: task },
  })
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url === '/api/system-task/audit-cleanup') {
      return {
        data: {
          success: true,
          data: {
            ...task,
            status: 'succeeded',
            state: { total: 10, processed: 10, progress: 100, remaining: 0 },
            result: { deleted_count: 10 },
          },
        },
      }
    }
    return {
      data: {
        success: true,
        data:
          url === '/api/performance/logs'
            ? { enabled: true, log_dir: '/var/log/new-api', file_count: 3 }
            : null,
      },
    }
  })
  await renderSettings()
  const audit = screen.getByRole('group', { name: 'Clean audit logs' })
  const clean = within(audit).getByRole('button', { name: 'Clean audit logs' })
  await waitFor(() => expect(clean).toBeEnabled())
  await user.click(clean)
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  const confirmation = await screen.findByRole('alertdialog', {
    name: 'Clean audit logs',
  })
  expect(
    within(confirmation).getByText(
      /including records outside the current filters/
    )
  ).toBeVisible()
  await user.click(within(confirmation).getByRole('button', { name: 'Cancel' }))
  expect(api.post).not.toHaveBeenCalled()
  await user.click(clean)
  await user.click(
    within(await screen.findByRole('alertdialog')).getByRole('button', {
      name: 'Delete logs',
    })
  )
  await waitFor(() =>
    expect(api.post).toHaveBeenCalledWith(
      '/api/system-task/log-cleanup',
      null,
      {
        params: {
          scope: 'audit',
          target_timestamp: 0,
          audit_target_timestamp: expect.any(Number),
        },
      }
    )
  )
  await waitFor(
    () => expect(invalidate).toHaveBeenCalledWith({ queryKey: ['audit'] }),
    { timeout: 2500 }
  )
  expect(
    within(audit).getByText('10 of 10 log entries processed.')
  ).toBeVisible()
})

test.each([true, false])(
  'audit and history cleanup share page edit access: %s',
  async (canEdit) => {
    useAuthStore.getState().auth.setUser({
      id: 19,
      username: 'log-maintainer',
      role: 10,
      permissions: {
        admin_permissions: {
          'system_settings.operations.logs': { view: true, edit: canEdit },
        },
      },
    })
    await renderSettings()
    const history = screen.getByRole('button', { name: 'Clean logs' })
    const audit = screen.getByRole('button', { name: 'Clean audit logs' })
    if (canEdit) {
      await waitFor(() => expect(history).toBeEnabled())
      await waitFor(() => expect(audit).toBeEnabled())
    } else {
      expect(history).toBeDisabled()
      expect(audit).toBeDisabled()
      await userEvent.click(audit)
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    }
  }
)

test('revoking log edit permission closes an open cleanup confirmation', async () => {
  const user = userEvent.setup()
  const post = vi.spyOn(api, 'post')
  await renderSettings()
  const clean = screen.getByRole('button', { name: 'Clean audit logs' })
  await waitFor(() => expect(clean).toBeEnabled())
  await user.click(clean)
  expect(await screen.findByRole('alertdialog')).toBeVisible()
  act(() => {
    useAuthStore.getState().auth.setUser({
      id: 19,
      username: 'log-maintainer',
      role: 10,
      permissions: {
        admin_permissions: {
          'system_settings.operations.logs': { view: true, edit: false },
        },
      },
    })
  })
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(
    screen.getByRole('button', { name: 'Clean audit logs' })
  ).toBeDisabled()
  expect(post).not.toHaveBeenCalled()
})

test.each([undefined, 'usage', 'audit'] as const)(
  'cleanup progress stays in its own section through completion (scope=%s)',
  async (scope) => {
    const auditTask = scope === 'audit'
    const task = {
      task_id: 'current-cleanup',
      status: 'running',
      payload: {
        scope,
        target_timestamp: auditTask ? 0 : 1234,
        audit_target_timestamp: auditTask ? 1234 : undefined,
      },
      state: { total: 10, processed: 2, progress: 20, remaining: 8 },
    }
    vi.mocked(api.get).mockImplementation(async (url) => {
      if (url === '/api/system-task/current') {
        return { data: { success: true, data: task } }
      }
      if (url === '/api/system-task/current-cleanup') {
        return {
          data: {
            success: true,
            data: {
              ...task,
              status: 'succeeded',
              state: { total: 10, processed: 10, progress: 100, remaining: 0 },
              result: { deleted_count: 10 },
            },
          },
        }
      }
      return { data: { success: true, data: null } }
    })
    await renderSettings()
    const current = screen.getByRole('group', {
      name: auditTask ? 'Clean audit logs' : 'Clean history logs',
    })
    const other = screen.getByRole('group', {
      name: auditTask ? 'Clean history logs' : 'Clean audit logs',
    })
    expect(
      await within(current).findByText('2 of 10 log entries processed.')
    ).toBeVisible()
    expect(within(current).getByRole('progressbar')).toBeVisible()
    expect(within(other).queryByRole('progressbar')).not.toBeInTheDocument()
    expect(
      within(other).queryByText(/log cleanup progress/i)
    ).not.toBeInTheDocument()
    const otherAction = within(other).getByRole('button', {
      name: auditTask ? 'Clean logs' : 'Clean audit logs',
    })
    expect(otherAction).toBeDisabled()
    expect(
      within(other).getByText(
        'Another log cleanup is running. Wait for it to finish.'
      )
    ).toBeVisible()
    expect(
      await within(current).findByText(
        '10 of 10 log entries processed.',
        {},
        { timeout: 2500 }
      )
    ).toBeVisible()
    await waitFor(() => expect(otherAction).toBeEnabled())
    expect(within(other).queryByRole('progressbar')).not.toBeInTheDocument()
    expect(
      within(other).queryByText(/log entries processed/)
    ).not.toBeInTheDocument()
    expect(screen.getAllByRole('progressbar')).toHaveLength(1)
  }
)

test('a failed cleanup progress request can be retried without starting a deletion', async () => {
  vi.mocked(api.get).mockRejectedValueOnce(
    new Error('private database failure')
  )
  render(<HistoryLogCleanup scope='audit' />)
  expect(
    await screen.findByText('Failed to load cleanup progress. Please retry.')
  ).toBeVisible()
  expect(screen.queryByText('private database failure')).not.toBeInTheDocument()
  expect(
    screen.getByRole('button', { name: 'Clean audit logs' })
  ).toBeDisabled()
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Clean audit logs' })
    ).toBeEnabled()
  )
})

test('the page shows log maintenance modules in order with inline audit controls', async () => {
  await renderSettings()

  expect(
    screen.getAllByRole('heading', { level: 4 }).map((h) => h.textContent)
  ).toEqual([
    'Log recording',
    'Log search and export',
    'Clean history logs',
    'Clean audit logs',
    'Server Log Management',
  ])
  const recording = screen.getByRole('group', { name: 'Log recording' })
  expect(within(recording).getAllByRole('switch')).toHaveLength(1)
  expect(
    within(recording).getByRole('switch', { name: 'Record quota usage' })
  ).toBeVisible()
  const search = screen.getByRole('group', { name: 'Log search and export' })
  expect(
    within(search).getByRole('switch', { name: 'Allow users to export logs' })
  ).toBeVisible()
  expect(
    within(search).getByRole('spinbutton', {
      name: 'Longest log time range (days)',
    })
  ).toBeVisible()
  const cleanup = screen.getByRole('group', { name: 'Clean history logs' })
  expect(
    within(cleanup).getByRole('button', { name: 'Clean logs' })
  ).toBeVisible()
  const audit = screen.getByRole('group', { name: 'Clean audit logs' })
  expect(
    within(audit).getByRole('button', { name: '7 days ago' })
  ).toBeVisible()
  expect(
    within(audit).getByRole('button', { name: 'Clean audit logs' })
  ).toBeVisible()
  const server = screen.getByRole('group', { name: 'Server Log Management' })
  expect(
    await within(server).findByRole('button', { name: 'Clean Up Log Files' })
  ).toBeVisible()
})

test('confirming a history cleanup starts the task and shows its progress in that module', async () => {
  const user = userEvent.setup()
  vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        task_id: 'systask_1',
        status: 'running',
        state: { total: 40, processed: 10, progress: 25, remaining: 30 },
      },
    },
  })
  await renderSettings()
  const cleanup = screen.getByRole('group', { name: 'Clean history logs' })

  await user.click(within(cleanup).getByRole('button', { name: 'Clean logs' }))
  await user.click(
    within(await screen.findByRole('alertdialog')).getByRole('button', {
      name: 'Delete logs',
    })
  )

  await waitFor(() =>
    expect(api.post).toHaveBeenCalledWith(
      '/api/system-task/log-cleanup',
      null,
      { params: { target_timestamp: expect.any(Number) } }
    )
  )
  expect(
    await within(cleanup).findByText('10 of 40 log entries processed.')
  ).toBeVisible()
  expect(
    within(cleanup).getByRole('button', { name: 'Cleaning...' })
  ).toBeDisabled()
})

test('saving sends only the log export settings that changed', async () => {
  const user = userEvent.setup()
  const days = await renderSettings()
  expect(days).toHaveValue(30)

  await user.click(
    screen.getByRole('switch', { name: 'Allow users to export logs' })
  )
  fireEvent.change(days, { target: { value: '90' } })
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(2))
  expect(api.put).toHaveBeenCalledWith('/api/option/', {
    scope: 'operations.logs',
    key: 'log_setting.user_export_enabled',
    value: false,
  })
  expect(api.put).toHaveBeenCalledWith('/api/option/', {
    scope: 'operations.logs',
    key: 'log_setting.max_range_days',
    value: 90,
  })
})

test('a time range under one day shows a field error and is not saved', async () => {
  const user = userEvent.setup()
  const days = await renderSettings()

  fireEvent.change(days, { target: { value: '0' } })
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  await waitFor(() => expect(days).toHaveAttribute('aria-invalid', 'true'))
  expect(api.put).not.toHaveBeenCalled()
})

test('keeps the unsaved range after a partial failure and retries only that field', async () => {
  const user = userEvent.setup()
  const days = await renderSettings()
  vi.mocked(api.put)
    .mockResolvedValueOnce({ data: { success: true } })
    .mockResolvedValueOnce({ data: { success: false, message: 'Save failed' } })
  await user.click(
    screen.getByRole('switch', { name: 'Allow users to export logs' })
  )
  fireEvent.change(days, { target: { value: '60' } })
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(2))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save Changes' })).toBeEnabled()
  )
  expect(days).toHaveValue(60)
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(3))
  expect(vi.mocked(api.put).mock.calls[2][1]).toMatchObject({
    key: 'log_setting.max_range_days',
    value: 60,
  })
})

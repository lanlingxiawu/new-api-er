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
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import * as api from '../../api'
import type { User } from '../../types'
import { UsersMutateDrawer } from '../users-mutate-drawer'

vi.mock('../../api', async (original) => ({
  ...(await original<typeof import('../../api')>()),
  getUser: vi.fn(),
  getGroups: vi.fn(),
  getPermissionCatalog: vi.fn(),
  adjustUserQuota: vi.fn(),
  updateUser: vi.fn(),
}))
vi.mock('../users-provider', () => ({
  useUsers: () => ({ triggerRefresh: vi.fn(), requestVerification: vi.fn() }),
}))
vi.mock('../employee-assign-field', () => ({ EmployeeAssignField: () => null }))
vi.mock('../user-channel-exclusions-field', () => ({
  UserChannelExclusionsField: () => null,
}))

const target: User = {
  id: 2,
  username: 'customer',
  display_name: 'Original name',
  role: 1,
  status: 1,
  quota: 500000,
  used_quota: 0,
  request_count: 0,
  group: 'default',
}
let client: QueryClient
beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  useAuthStore.getState().auth.setUser({ id: 1, username: 'admin', role: 10 })
  useSystemConfigStore
    .getState()
    .setConfig({ currency: { ...DEFAULT_CURRENCY_CONFIG } })
  vi.mocked(api.getGroups).mockResolvedValue({
    success: true,
    data: ['default'],
  })
  vi.mocked(api.getPermissionCatalog).mockResolvedValue({
    resources: [],
    roles: [],
  })
  vi.mocked(api.getUser).mockResolvedValue({ success: true, data: target })
})
afterEach(() => {
  cleanup()
  client.clear()
  useAuthStore.getState().auth.reset()
  vi.resetAllMocks()
})

function drawer(row = target, onOpenChange = vi.fn()) {
  return (
    <QueryClientProvider client={client}>
      <UsersMutateDrawer open currentRow={row} onOpenChange={onOpenChange} />
    </QueryClientProvider>
  )
}

it('keeps the draft when a refreshed list provides a new object for the same user', async () => {
  const view = render(drawer())
  const input = await screen.findByDisplayValue('Original name')
  fireEvent.change(input, { target: { value: 'Unsaved name' } })
  view.rerender(drawer({ ...target, display_name: 'List refresh' }))
  expect(screen.getByDisplayValue('Unsaved name')).toBeVisible()
  expect(api.getUser).toHaveBeenCalledTimes(1)
})

it('ignores a late response for a previously selected user', async () => {
  let resolveFirst!: (result: Awaited<ReturnType<typeof api.getUser>>) => void
  vi.mocked(api.getUser).mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        resolveFirst = resolve
      })
  )
  const view = render(drawer())
  expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled()
  const next = { ...target, id: 3, username: 'next', display_name: 'Next user' }
  vi.mocked(api.getUser).mockResolvedValue({ success: true, data: next })
  view.rerender(drawer(next))
  await screen.findByDisplayValue('Next user')
  await act(async () => resolveFirst({ success: true, data: target }))
  expect(screen.getByDisplayValue('Next user')).toBeVisible()
  expect(screen.queryByDisplayValue('Original name')).not.toBeInTheDocument()
})

it('offers retry and blocks saving after a failed load', async () => {
  vi.mocked(api.getUser).mockRejectedValueOnce(new Error('offline'))
  render(drawer())
  await userEvent.click(await screen.findByRole('button', { name: 'Retry' }))
  await screen.findByDisplayValue('Original name')
  expect(api.getUser).toHaveBeenCalledTimes(2)
  expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled()
})

it('refreshes only the quota after an adjustment and keeps the unsaved name', async () => {
  render(drawer())
  fireEvent.change(await screen.findByDisplayValue('Original name'), {
    target: { value: 'Unsaved name' },
  })
  await userEvent.click(screen.getByRole('tab', { name: 'Group & Quota' }))
  await userEvent.click(screen.getByRole('button', { name: 'Adjust Quota' }))
  const quotaDialog = within(
    screen.getByRole('dialog', { name: 'Adjust Quota' })
  )
  expect(quotaDialog.getByRole('radio', { name: 'Add' })).toBeChecked()
  fireEvent.change(quotaDialog.getByRole('spinbutton'), {
    target: { value: '2' },
  })
  vi.mocked(api.adjustUserQuota).mockResolvedValue({ success: true })
  vi.mocked(api.getUser).mockResolvedValue({
    success: true,
    data: { ...target, quota: 1500000 },
  })
  await userEvent.click(quotaDialog.getByRole('button', { name: 'Confirm' }))
  await waitFor(() => expect(api.getUser).toHaveBeenCalledTimes(2))
  await userEvent.click(screen.getByRole('tab', { name: 'Basic Information' }))
  expect(screen.getByDisplayValue('Unsaved name')).toBeVisible()
  expect(screen.getByText('Unsaved changes')).toBeVisible()
  expect(api.updateUser).not.toHaveBeenCalled()
})

it('asks before discarding a draft and preserves it when cancelling', async () => {
  const onOpenChange = vi.fn()
  render(drawer(target, onOpenChange))
  fireEvent.change(await screen.findByDisplayValue('Original name'), {
    target: { value: 'Unsaved name' },
  })
  await userEvent.keyboard('{Escape}')
  expect(screen.getByRole('alertdialog')).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(screen.getByDisplayValue('Unsaved name')).toBeVisible()
  expect(onOpenChange).not.toHaveBeenCalled()
  await userEvent.keyboard('{Escape}')
  await userEvent.click(screen.getByRole('button', { name: 'Discard changes' }))
  expect(onOpenChange).toHaveBeenCalledWith(false)
})

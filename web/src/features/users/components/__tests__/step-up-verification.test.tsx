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
import type { Row } from '@tanstack/react-table'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useEffect } from 'react'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import type { User } from '../../types'
import { DataTableRowActions } from '../data-table-row-actions'
import { UsersClearAccessTokensDialog } from '../users-clear-access-tokens-dialog'
import { UsersClearAPIKeysDialog } from '../users-clear-api-keys-dialog'
import { UsersDeleteDialog } from '../users-delete-dialog'
import { UsersProvider, useUsers } from '../users-provider'

const target: User = {
  id: 2,
  username: 'managed-user',
  display_name: 'Managed user',
  role: 1,
  status: 1,
  quota: 0,
  used_quota: 0,
  request_count: 0,
  group: 'default',
}

function mockVerification(scope: string, proofToken: string) {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/verify/methods') {
      return {
        data: {
          success: true,
          data: {
            scope,
            methods: [{ method: '2fa', available: true }],
            oauth_providers: [],
            password_encryption_enabled: false,
          },
        },
      }
    }
    return { data: { success: true, data: [] } }
  })
  return {
    data: {
      success: true,
      data: {
        proof_token: proofToken,
        method: '2fa',
        scope,
        expires_at: Math.floor(Date.now() / 1000) + 60,
      },
    },
  }
}

function OpenDeleteDialog() {
  const users = useUsers()
  useEffect(() => {
    users.setCurrentRow(target)
    users.setOpen('delete')
    // The harness only seeds provider state once on mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  return null
}

function OpenClearAPIKeysDialog() {
  const users = useUsers()
  useEffect(() => {
    users.setCurrentRow(target)
    users.setOpen('clear-api-keys')
    // Seed the target once, just like opening its row menu.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  return <UsersClearAPIKeysDialog />
}

function OpenClearAccessTokensDialog() {
  const users = useUsers()
  useEffect(() => {
    users.setCurrentRow(target)
    users.setOpen('clear-access-tokens')
    // Seed the target once, just like opening its row menu.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  return <UsersClearAccessTokensDialog />
}

it.each([0, 21])(
  'clears access tokens with a target-bound proof and reports %s revoked',
  async (count) => {
    const proof = mockVerification(
      'admin.user.access_tokens.delete_all',
      'access-clear-proof'
    )
    const post = vi.spyOn(api, 'post').mockResolvedValue(proof)
    const del = vi.spyOn(api, 'delete').mockResolvedValue({
      data: { success: true, data: { revoked_count: count } },
    })
    renderInProvider(<OpenClearAccessTokensDialog />)
    expect(api.get).not.toHaveBeenCalled()
    await userEvent.click(
      await screen.findByRole('button', { name: 'Clear access tokens' })
    )
    await screen.findByLabelText('Authenticator code or backup code')
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(del).not.toHaveBeenCalled()
    await completeTwoFactorVerification()
    await waitFor(() =>
      expect(del).toHaveBeenCalledWith(
        '/api/user/2/access_tokens',
        expect.objectContaining({
          headers: { 'X-Security-Proof': 'access-clear-proof' },
          singleUseAuthorization: true,
        })
      )
    )
    expect(post).toHaveBeenCalledWith(
      '/api/verify',
      expect.objectContaining({
        scope: 'admin.user.access_tokens.delete_all',
        context: { user_id: 2 },
      }),
      expect.anything()
    )
    expect(
      vi
        .mocked(api.get)
        .mock.calls.every(([url]) => url === '/api/verify/methods')
    ).toBe(true)
    await waitFor(() =>
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    )
  }
)

it('cancelling access token verification keeps credentials untouched', async () => {
  mockVerification('admin.user.access_tokens.delete_all', 'access-clear-proof')
  const del = vi.spyOn(api, 'delete')
  renderInProvider(<OpenClearAccessTokensDialog />)
  await userEvent.click(
    await screen.findByRole('button', { name: 'Clear access tokens' })
  )
  await screen.findByLabelText('Authenticator code or backup code')
  await userEvent.keyboard('{Escape}')
  await screen.findByRole('alertdialog')
  expect(del).not.toHaveBeenCalled()
})

it('retries failed access token clearing only after new verification', async () => {
  const proof = mockVerification(
    'admin.user.access_tokens.delete_all',
    'access-first-proof'
  )
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValueOnce(proof)
    .mockResolvedValueOnce({
      data: {
        ...proof.data,
        data: { ...proof.data.data, proof_token: 'access-retry-proof' },
      },
    })
  const del = vi
    .spyOn(api, 'delete')
    .mockResolvedValueOnce({
      data: { success: false, message: 'Please retry.' },
    })
    .mockResolvedValueOnce({
      data: { success: true, data: { revoked_count: 0 } },
    })
  renderInProvider(<OpenClearAccessTokensDialog />)
  await userEvent.click(
    await screen.findByRole('button', { name: 'Clear access tokens' })
  )
  await completeTwoFactorVerification()
  await waitFor(() => expect(del).toHaveBeenCalledTimes(1))
  await userEvent.click(
    await screen.findByRole('button', { name: 'Clear access tokens' })
  )
  await completeTwoFactorVerification()
  await waitFor(() => expect(del).toHaveBeenCalledTimes(2))
  expect(post).toHaveBeenCalledTimes(2)
  expect(del.mock.calls[1][1]).toMatchObject({
    headers: { 'X-Security-Proof': 'access-retry-proof' },
  })
})

it('prevents duplicate access token clearing while pending', async () => {
  const proof = mockVerification(
    'admin.user.access_tokens.delete_all',
    'access-clear-proof'
  )
  vi.spyOn(api, 'post').mockResolvedValue(proof)
  let finish: (value: unknown) => void = () => {}
  const del = vi.spyOn(api, 'delete').mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve
      })
  )
  renderInProvider(<OpenClearAccessTokensDialog />)
  await userEvent.dblClick(
    await screen.findByRole('button', { name: 'Clear access tokens' })
  )
  await completeTwoFactorVerification()
  const button = await screen.findByRole('button', { name: 'Clearing...' })
  expect(button).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()
  await userEvent.click(button)
  expect(del).toHaveBeenCalledTimes(1)
  finish({ data: { success: true, data: { revoked_count: 1 } } })
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
})

it('clears all API keys with a target-bound proof without fetching a key list', async () => {
  const proof = mockVerification(
    'admin.user.api_keys.delete_all',
    'clear-proof'
  )
  const post = vi.spyOn(api, 'post').mockResolvedValue(proof)
  const del = vi.spyOn(api, 'delete').mockResolvedValue({
    data: { success: true, data: { deleted_count: 101, completed: true } },
  })
  renderInProvider(<OpenClearAPIKeysDialog />)
  expect(api.get).not.toHaveBeenCalled()
  await userEvent.click(
    await screen.findByRole('button', { name: 'Clear API keys' })
  )
  await screen.findByLabelText('Authenticator code or backup code')
  expect(del).not.toHaveBeenCalled()
  await completeTwoFactorVerification()
  await waitFor(() =>
    expect(del).toHaveBeenCalledWith(
      '/api/user/2/tokens',
      expect.objectContaining({
        headers: { 'X-Security-Proof': 'clear-proof' },
        singleUseAuthorization: true,
      })
    )
  )
  expect(post).toHaveBeenCalledWith(
    '/api/verify',
    expect.objectContaining({
      scope: 'admin.user.api_keys.delete_all',
      context: { user_id: 2 },
    }),
    expect.anything()
  )
  expect(
    vi
      .mocked(api.get)
      .mock.calls.every(([url]) => url === '/api/verify/methods')
  ).toBe(true)
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
})

it('cancelling API key verification does not clear keys', async () => {
  mockVerification('admin.user.api_keys.delete_all', 'clear-proof')
  const del = vi.spyOn(api, 'delete')
  renderInProvider(<OpenClearAPIKeysDialog />)
  await userEvent.click(
    await screen.findByRole('button', { name: 'Clear API keys' })
  )
  await screen.findByLabelText('Authenticator code or backup code')
  await userEvent.keyboard('{Escape}')
  await screen.findByRole('alertdialog')
  expect(del).not.toHaveBeenCalled()
})

it('keeps a failed clear operation open and gets a fresh proof before retrying', async () => {
  const proof = mockVerification(
    'admin.user.api_keys.delete_all',
    'first-proof'
  )
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValueOnce(proof)
    .mockResolvedValueOnce({
      data: {
        ...proof.data,
        data: { ...proof.data.data, proof_token: 'retry-proof' },
      },
    })
  const del = vi
    .spyOn(api, 'delete')
    .mockResolvedValueOnce({
      data: {
        success: false,
        message: 'Please retry.',
        data: { deleted_count: 100, completed: false },
      },
    })
    .mockResolvedValueOnce({
      data: { success: true, data: { deleted_count: 1, completed: true } },
    })
  renderInProvider(<OpenClearAPIKeysDialog />)
  await userEvent.click(
    await screen.findByRole('button', { name: 'Clear API keys' })
  )
  await completeTwoFactorVerification()
  await waitFor(() => expect(del).toHaveBeenCalledTimes(1))
  await userEvent.click(
    await screen.findByRole('button', { name: 'Clear API keys' })
  )
  await completeTwoFactorVerification()
  await waitFor(() => expect(del).toHaveBeenCalledTimes(2))
  expect(post).toHaveBeenCalledTimes(2)
  expect(del.mock.calls[1][1]).toMatchObject({
    headers: { 'X-Security-Proof': 'retry-proof' },
  })
})

function renderInProvider(children: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <UsersProvider>{children}</UsersProvider>
    </QueryClientProvider>
  )
}

async function completeTwoFactorVerification() {
  await userEvent.type(
    await screen.findByLabelText('Authenticator code or backup code'),
    '123456'
  )
  await userEvent.click(screen.getByRole('button', { name: 'Verify' }))
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.setUser(null)
})

it.each([1, 10, 100])(
  'only offers clearing for lower roles (operator role %s)',
  async (role) => {
    useAuthStore.getState().auth.setUser({ id: 1, username: 'operator', role })
    renderInProvider(
      <DataTableRowActions
        row={{ original: { ...target, role: 10 } } as Row<User>}
      />
    )
    await userEvent.click(screen.getByRole('button', { name: 'Open menu' }))
    if (role === 100) {
      expect(
        await screen.findByRole('menuitem', { name: 'Clear access tokens' })
      ).toBeInTheDocument()
      expect(
        await screen.findByRole('menuitem', { name: 'Clear API keys' })
      ).toBeInTheDocument()
    } else {
      expect(
        screen.queryByRole('menuitem', { name: 'Clear access tokens' })
      ).not.toBeInTheDocument()
      expect(
        screen.queryByRole('menuitem', { name: 'Clear API keys' })
      ).not.toBeInTheDocument()
    }
  }
)

it('disables duplicate clearing submissions while the request is pending', async () => {
  const proof = mockVerification(
    'admin.user.api_keys.delete_all',
    'clear-proof'
  )
  vi.spyOn(api, 'post').mockResolvedValue(proof)
  let finish: (value: unknown) => void = () => {}
  const del = vi.spyOn(api, 'delete').mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve
      })
  )
  renderInProvider(<OpenClearAPIKeysDialog />)
  await userEvent.dblClick(
    await screen.findByRole('button', { name: 'Clear API keys' })
  )
  await completeTwoFactorVerification()
  const button = await screen.findByRole('button', { name: 'Clearing...' })
  expect(button).toBeDisabled()
  await userEvent.click(button)
  expect(del).toHaveBeenCalledTimes(1)
  finish({
    data: { success: true, data: { deleted_count: 0, completed: true } },
  })
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
})

it('deleting a user sends the request only with a single-use proof for that user', async () => {
  const proof = mockVerification('admin.user.delete', 'delete-proof')
  const post = vi.spyOn(api, 'post').mockResolvedValue(proof)
  const del = vi
    .spyOn(api, 'delete')
    .mockResolvedValue({ data: { success: true } })
  renderInProvider(
    <>
      <OpenDeleteDialog />
      <UsersDeleteDialog />
    </>
  )
  await userEvent.click(await screen.findByRole('button', { name: 'Delete' }))
  await screen.findByLabelText('Authenticator code or backup code')
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(del).not.toHaveBeenCalled()
  await completeTwoFactorVerification()
  await waitFor(() =>
    expect(del).toHaveBeenCalledWith(
      '/api/user/2/',
      expect.objectContaining({
        headers: { 'X-Security-Proof': 'delete-proof' },
        singleUseAuthorization: true,
      })
    )
  )
  expect(post).toHaveBeenCalledWith(
    '/api/verify',
    expect.objectContaining({
      scope: 'admin.user.delete',
      context: { user_id: 2 },
    }),
    expect.anything()
  )
})

it('cancelling verification leaves the user untouched and returns to the confirmation', async () => {
  mockVerification('admin.user.delete', 'delete-proof')
  const del = vi.spyOn(api, 'delete')
  renderInProvider(
    <>
      <OpenDeleteDialog />
      <UsersDeleteDialog />
    </>
  )
  await userEvent.click(await screen.findByRole('button', { name: 'Delete' }))
  await screen.findByLabelText('Authenticator code or backup code')
  await userEvent.keyboard('{Escape}')
  await screen.findByRole('alertdialog')
  expect(del).not.toHaveBeenCalled()
})

it('unlocking sign-in requires a proof bound to the user and unlock action', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'operator', role: 10 })
  const proof = mockVerification('admin.user.manage', 'unlock-proof')
  const post = vi.spyOn(api, 'post').mockImplementation(async (url) => {
    if (url === '/api/verify') return proof
    if (url === '/api/user/manage') return { data: { success: true } }
    throw new Error(`Unexpected POST ${url}`)
  })
  renderInProvider(
    <DataTableRowActions
      row={
        {
          original: {
            ...target,
            login_protection: {
              locked: true,
              failed_attempts: 5,
              locked_at: 1,
            },
          },
        } as Row<User>
      }
    />
  )
  await userEvent.click(screen.getByRole('button', { name: 'Open menu' }))
  await userEvent.click(
    await screen.findByRole('menuitem', { name: 'Unlock sign-in' })
  )
  expect(post).not.toHaveBeenCalledWith(
    '/api/user/manage',
    expect.anything(),
    expect.anything()
  )
  await completeTwoFactorVerification()
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/user/manage',
      { id: 2, action: 'unlock_login' },
      expect.objectContaining({
        headers: { 'X-Security-Proof': 'unlock-proof' },
        singleUseAuthorization: true,
      })
    )
  )
  expect(post).toHaveBeenCalledWith(
    '/api/verify',
    expect.objectContaining({
      scope: 'admin.user.manage',
      context: { user_id: 2, action: 'unlock_login' },
    }),
    expect.anything()
  )
})

it('disabling a user from the row menu binds the proof to the user and action', async () => {
  const proof = mockVerification('admin.user.manage', 'manage-proof')
  const post = vi.spyOn(api, 'post').mockImplementation(async (url) => {
    if (url === '/api/verify') return proof
    if (url === '/api/user/manage') return { data: { success: true } }
    throw new Error(`Unexpected POST ${url}`)
  })
  renderInProvider(
    <DataTableRowActions row={{ original: target } as Row<User>} />
  )
  await userEvent.click(screen.getByRole('button', { name: 'Open menu' }))
  await userEvent.click(
    await screen.findByRole('menuitem', { name: 'Disable' })
  )
  expect(post).not.toHaveBeenCalledWith(
    '/api/user/manage',
    expect.anything(),
    expect.anything()
  )
  await completeTwoFactorVerification()
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/user/manage',
      { id: 2, action: 'disable' },
      expect.objectContaining({
        headers: { 'X-Security-Proof': 'manage-proof' },
        singleUseAuthorization: true,
      })
    )
  )
  expect(post).toHaveBeenCalledWith(
    '/api/verify',
    expect.objectContaining({
      scope: 'admin.user.manage',
      context: { user_id: 2, action: 'disable' },
    }),
    expect.anything()
  )
})

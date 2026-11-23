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
import { afterEach, expect, it, vi } from 'vitest'

import { registerFormSchema } from '@/features/auth/constants'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { userFormSchema } from '../../lib/user-form'
import type { User } from '../../types'
import { UsersMutateDrawer } from '../users-mutate-drawer'
import { UsersProvider } from '../users-provider'

const target: User = {
  id: 2,
  username: 'managed-admin',
  display_name: 'Managed admin',
  role: 10,
  status: 1,
  quota: 0,
  used_quota: 0,
  request_count: 0,
  group: 'default',
}
const label = "View other accounts' audit logs"
const description =
  'View audit records from user and admin roles. Root records are always excluded.'

function renderPermissions(
  viewerRole: number,
  allowed?: boolean,
  timeoutBilling: 'refund' | 'charge' | 'input' = 'refund',
  tokenPermissions = false
) {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'operator', role: viewerRole })
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/channel/exclusion-options') {
      return {
        data: {
          success: true,
          data: { items: [], next_after_id: 0, has_more: false },
        },
      }
    }
    if (url === '/api/authz/catalog') {
      return {
        data: {
          success: true,
          data: {
            resources: [
              ...(tokenPermissions
                ? [
                    {
                      resource: 'admin_access_token',
                      label_key: 'Administrator token access',
                      actions: [
                        {
                          action: 'use',
                          label_key: 'Allow management token access',
                        },
                        {
                          action: 'users_list',
                          label_key: 'Allow exact user lookup',
                        },
                        {
                          action: 'users_quota',
                          label_key: 'Allow balance changes',
                        },
                        {
                          action: 'users_group_ratios',
                          label_key: 'Allow exclusive ratio changes',
                        },
                        {
                          action: 'users_employee',
                          label_key: 'Allow employee assignment',
                        },
                        {
                          action: 'groups_read',
                          label_key: 'Allow group names',
                        },
                        {
                          action: 'employees_read',
                          label_key: 'Allow employee selection',
                        },
                        {
                          action: 'users_stats',
                          label_key: 'Allow usage statistics',
                        },
                        {
                          action: 'topups_reconcile',
                          label_key: 'Allow verified top-up reconciliation',
                        },
                        {
                          action: 'channels_owned',
                          label_key: 'Allow access to own channels',
                        },
                      ],
                    },
                  ]
                : []),
              {
                resource: 'audit',
                label_key: 'Audit Logs',
                actions: [
                  {
                    action: 'read',
                    label_key: label,
                    description_key: description,
                  },
                ],
              },
            ],
            roles: [{ key: 'admin', grants: { audit: { read: false } } }],
          },
        },
      }
    }
    if (url === '/api/group/') {
      return { data: { success: true, data: ['default'] } }
    }
    if (url === '/api/verify/methods') {
      return {
        data: {
          success: true,
          data: {
            scope: 'admin.user.update',
            methods: [{ method: '2fa', available: true }],
            oauth_providers: [],
            password_encryption_enabled: false,
          },
        },
      }
    }
    return {
      data: {
        success: true,
        data: {
          ...target,
          non_stream_timeout_billing: timeoutBilling,
          admin_permissions:
            allowed === undefined ? {} : { audit: { read: allowed } },
        },
      },
    }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <UsersProvider>
        <UsersMutateDrawer
          open
          onOpenChange={() => undefined}
          currentRow={{ ...target, non_stream_timeout_billing: timeoutBilling }}
        />
      </UsersProvider>
    </QueryClientProvider>
  )
  return get
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

it('groups request settings in one tab and preserves edits without submitting', async () => {
  const put = vi.spyOn(api, 'put')
  renderPermissions(100)
  const name = await screen.findByDisplayValue('Managed admin')
  await userEvent.clear(name)
  await userEvent.type(name, 'Edited admin')
  await userEvent.click(screen.getByRole('tab', { name: 'Request settings' }))
  expect(screen.getByRole('tab', { name: 'Request settings' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  expect(screen.getByLabelText('Retry attempts')).toBeVisible()
  expect(screen.getByText('AI Request Timeout')).toBeVisible()
  expect(screen.getByText('Excluded channels')).toBeVisible()
  expect(screen.getByText('Stream protocol strict validation')).toBeVisible()
  expect(name).not.toBeVisible()
  await userEvent.click(screen.getByRole('tab', { name: 'Basic Information' }))
  expect(screen.getByDisplayValue('Edited admin')).toBeVisible()
  expect(screen.getByLabelText('Remark')).toBeVisible()
  expect(put).not.toHaveBeenCalled()
})

it('opens the tab containing an invalid field before focusing it', async () => {
  const put = vi.spyOn(api, 'put')
  renderPermissions(100)
  await screen.findByDisplayValue('Managed admin')
  await userEvent.click(screen.getByRole('tab', { name: 'Request settings' }))
  const retries = screen.getByLabelText('Retry attempts')
  await userEvent.clear(retries)
  await userEvent.type(retries, '21')
  await userEvent.click(screen.getByRole('tab', { name: 'Basic Information' }))
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(retries).toHaveFocus())
  expect(screen.getByRole('tab', { name: 'Request settings' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  expect(retries).toHaveAttribute('aria-invalid', 'true')
  expect(put).not.toHaveBeenCalled()
})

it('hides the channel exclusion navigation when channel access is unavailable', async () => {
  renderPermissions(1)
  await screen.findByRole('tab', { name: 'Group & Quota' })
  expect(
    screen.queryByRole('button', { name: 'Excluded channels' })
  ).not.toBeInTheDocument()
  expect(
    document.querySelector('#user-channel-exclusions')
  ).not.toBeInTheDocument()
})

it.each([
  {
    mode: 'charge' as const,
    description:
      'On timeout, supported requests return partial output and charge for received usage (upstream counts first, otherwise an estimate). Other non-stream requests charge estimated input only. Per-call models are refunded if no output is received.',
  },
  {
    mode: 'input' as const,
    description:
      'On timeout, only input is charged. Non-stream input is estimated unless upstream usage is available; output is not charged. Per-call models are refunded.',
  },
])(
  'explains $mode billing and preserves the saved selection',
  async (testCase) => {
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    renderPermissions(100, true, testCase.mode)
    await userEvent.click(screen.getByRole('tab', { name: 'Request settings' }))
    expect(await screen.findByText(testCase.description)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(
        '/api/user/',
        expect.objectContaining({ non_stream_timeout_billing: testCase.mode }),
        {}
      )
    )
  }
)

it.each(['username', 'display_name'] as const)(
  '用户名称长度按 Unicode 字符校验：%s',
  (field) => {
    for (const character of ['a', '名', '😀']) {
      for (const length of [20, 21, 31, 32, 33]) {
        const result = userFormSchema.safeParse({
          username: 'managed-user',
          [field]: character.repeat(length),
        })
        expect(result.success).toBe(length <= 32)
        if (!result.success) {
          expect(result.error.issues[0]).toMatchObject({
            code: 'too_big',
            maximum: 32,
          })
        }
      }
    }
  }
)

it('注册与管理员编辑采用相同的用户名长度和去空格规则', () => {
  for (const character of ['a', '名', '😀']) {
    for (const length of [20, 21, 31, 32, 33]) {
      const username = ` ${character.repeat(length)} `
      const result = registerFormSchema.safeParse({
        username,
        password: 'valid-password',
        confirmPassword: 'valid-password',
      })
      expect(result.success).toBe(length <= 32)
      if (result.success) expect(result.data.username).toBe(username.trim())
    }
  }
  expect(userFormSchema.safeParse({ username: '   ' }).success).toBe(false)
  expect(
    userFormSchema.safeParse({ username: 'valid', remark: '注'.repeat(256) })
      .success
  ).toBe(false)
})

it('超长显示名阻止保存，修正后可保存且保留超时和重试配置', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  renderPermissions(100, true)
  const displayName = await screen.findByDisplayValue('Managed admin')
  await userEvent.clear(displayName)
  await userEvent.type(displayName, 'x'.repeat(33))
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() =>
    expect(displayName).toHaveAttribute('aria-invalid', 'true')
  )
  expect(put).not.toHaveBeenCalled()
  await userEvent.clear(displayName)
  await userEvent.type(displayName, 'Corrected admin')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(
      '/api/user/',
      expect.objectContaining({
        display_name: 'Corrected admin',
        stream_response_timeout: 0,
        stream_total_timeout: 0,
        non_stream_response_timeout: 0,
        non_stream_total_timeout: 0,
        stream_response_timeout_mode: 'first_output',
        non_stream_timeout_billing: 'refund',
        retry_times: 0,
      }),
      {}
    )
  )
})

it.each([undefined, true])(
  'root can save an audit grant or revocation after step-up verification (previous=%s)',
  async (allowed) => {
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    vi.spyOn(api, 'post').mockResolvedValue({
      data: {
        success: true,
        data: {
          proof_token: 'update-proof',
          method: '2fa',
          scope: 'admin.user.update',
          expires_at: Math.floor(Date.now() / 1000) + 60,
        },
      },
    })
    renderPermissions(100, allowed)
    await screen.findByDisplayValue('Managed admin')
    await userEvent.click(
      screen.getByRole('tab', { name: 'Administrator permissions' })
    )
    await userEvent.click(
      screen.getByRole('button', { name: /Other permissions/ })
    )
    const toggle = await screen.findByRole('checkbox', { name: label })
    await waitFor(() =>
      expect(toggle).toHaveAttribute('aria-checked', String(!!allowed))
    )
    expect(toggle).toHaveAccessibleDescription(description)
    await userEvent.click(toggle)
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    // The permission matrix changed, so the save waits for verification.
    expect(put).not.toHaveBeenCalled()
    await userEvent.type(
      await screen.findByLabelText('Authenticator code or backup code'),
      '123456'
    )
    await userEvent.click(screen.getByRole('button', { name: 'Verify' }))
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(
        '/api/user/',
        expect.objectContaining({
          id: 2,
          admin_permissions: { audit: { read: !allowed } },
        }),
        expect.objectContaining({
          headers: { 'X-Security-Proof': 'update-proof' },
          singleUseAuthorization: true,
        })
      )
    )
  }
)

it('root saving an administrator without changing permissions or password does not verify', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  const get = renderPermissions(100, true)
  const displayName = await screen.findByDisplayValue('Managed admin')
  await userEvent.clear(displayName)
  await userEvent.type(displayName, 'Renamed admin')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(
      '/api/user/',
      expect.objectContaining({ id: 2, display_name: 'Renamed admin' }),
      {}
    )
  )
  expect(put.mock.calls[0][1]).not.toHaveProperty('admin_permissions')
  expect(get).not.toHaveBeenCalledWith('/api/verify/methods', expect.anything())
})

it('reloads saved values when a committed change reports a synchronization failure', async () => {
  vi.spyOn(api, 'put').mockResolvedValue({
    data: {
      success: false,
      committed: true,
      message: 'The changes were saved, but synchronization failed.',
    },
  })
  const get = renderPermissions(100, false)
  const displayName = await screen.findByDisplayValue('Managed admin')
  await userEvent.clear(displayName)
  await userEvent.type(displayName, 'Changed admin')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() =>
    expect(
      get.mock.calls.filter(([url]) => url === '/api/user/2')
    ).toHaveLength(2)
  )
  await waitFor(() =>
    expect(screen.getByDisplayValue('Managed admin')).toBeVisible()
  )
})

it('admin cannot edit the audit permission even when the catalog is available', async () => {
  renderPermissions(10)
  await screen.findByDisplayValue('Managed admin')
  expect(
    screen.queryByRole('tab', { name: 'Administrator permissions' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('checkbox', { name: label })
  ).not.toBeInTheDocument()
})

it('root can select token modules independently and preserve them when disabling the master switch', async () => {
  const put = vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: true },
  })
  vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        proof_token: 'update-proof',
        method: '2fa',
        scope: 'admin.user.update',
        expires_at: Math.floor(Date.now() / 1000) + 60,
      },
    },
  })
  renderPermissions(100, false, 'refund', true)
  await screen.findByDisplayValue('Managed admin')
  await userEvent.click(
    screen.getByRole('tab', { name: 'Administrator permissions' })
  )
  const master = await screen.findByRole('switch', {
    name: 'Allow management token access',
  })
  const users = screen.getByRole('checkbox', {
    name: 'Allow exact user lookup',
  })
  const channels = screen.getByRole('checkbox', {
    name: 'Allow access to own channels',
  })
  await userEvent.click(master)
  await userEvent.click(users)
  for (const name of [
    'Allow balance changes',
    'Allow exclusive ratio changes',
    'Allow employee assignment',
    'Allow group names',
    'Allow employee selection',
    'Allow usage statistics',
    'Allow verified top-up reconciliation',
  ]) {
    const capability = screen.getByRole('checkbox', { name })
    expect(capability).toHaveAttribute('aria-checked', 'false')
    await userEvent.click(capability)
    expect(capability).toHaveAttribute('aria-checked', 'true')
  }
  expect(channels).toHaveAttribute('aria-checked', 'false')
  await userEvent.click(master)
  expect(master).toHaveAttribute('aria-checked', 'false')
  expect(users).toHaveAttribute('aria-checked', 'true')
  expect(channels).toHaveAttribute('aria-checked', 'false')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(put).not.toHaveBeenCalled()
  await userEvent.type(
    await screen.findByLabelText('Authenticator code or backup code'),
    '123456'
  )
  await userEvent.click(screen.getByRole('button', { name: 'Verify' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(
      '/api/user/',
      expect.objectContaining({
        admin_permissions: expect.objectContaining({
          admin_access_token: expect.objectContaining({
            use: false,
            groups_read: true,
            employees_read: true,
            users_stats: true,
          }),
        }),
      }),
      expect.objectContaining({
        headers: { 'X-Security-Proof': 'update-proof' },
      })
    )
  )
})

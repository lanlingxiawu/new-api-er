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
import { AxiosError, type AxiosResponse } from 'axios'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import fr from '@/i18n/locales/fr.json'
import ja from '@/i18n/locales/ja.json'
import ru from '@/i18n/locales/ru.json'
import viLocale from '@/i18n/locales/vi.json'
import zhTW from '@/i18n/locales/zh-TW.json'
import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { AuditLogs } from '..'
import { AuditLogViewer } from '../components/audit-log-viewer'
import { AuditExportButton } from '../export/audit-export-panel'

it('keeps explicit export entries disabled for an administrator with read access only', () => {
  useAuthStore.getState().auth.setUser({
    id: 19,
    username: 'reader',
    role: 10,
    permissions: { admin_permissions: { audit: { read: true } } },
  })
  render(
    <AuditExportButton scope='all' params={{ p: 1, page_size: 20 }} canQuery />
  )
  expect(screen.getByRole('button', { name: 'Export' })).toBeDisabled()
  expect(
    screen.queryByRole('button', { name: 'Export history' })
  ).not.toBeInTheDocument()
  expect(
    screen.getByText('Audit export permission has not been granted.')
  ).toBeVisible()
})

it('opens existing export tasks even when the current audit query is invalid', async () => {
  useAuthStore.getState().auth.setUser({ id: 19, username: 'self', role: 1 })
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: { jobs: [], enabled: true, max_range_sec: 86400 },
    },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <AuditExportButton
        scope='self'
        params={{ p: 1, page_size: 20 }}
        canQuery={false}
      />
    </QueryClientProvider>
  )
  expect(screen.getByRole('button', { name: 'Export' })).toBeEnabled()
  await userEvent.click(screen.getByRole('button', { name: 'Export' }))
  expect(
    await screen.findByRole('tab', { name: 'Export history' })
  ).toHaveAttribute('aria-selected', 'true')
  client.clear()
})

it.each([true, false])(
  'refreshes export permissions without revoking audit read unnecessarily (read=%s)',
  async (read) => {
    const admin = {
      id: 19,
      username: 'admin',
      role: 10,
      permissions: {
        admin_permissions: { audit: { read: true, export: true } },
      },
    }
    useAuthStore.getState().auth.setUser(admin)
    const denied = vi.fn().mockResolvedValue(undefined)
    const forbidden = new AxiosError(
      'Forbidden',
      'ERR_BAD_REQUEST',
      undefined,
      undefined,
      { status: 403 } as AxiosResponse
    )
    vi.spyOn(api, 'get').mockImplementation(async (url) => {
      if (url === '/api/user/self') {
        return {
          data: {
            success: true,
            data: {
              ...admin,
              permissions: {
                admin_permissions: { audit: { read, export: false } },
              },
            },
          },
        }
      }
      throw forbidden
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <AuditExportButton
          scope='all'
          params={{ p: 1, page_size: 20 }}
          canQuery
          onAccessDenied={denied}
        />
      </QueryClientProvider>
    )
    await userEvent.click(screen.getByRole('button', { name: 'Export' }))
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    expect(
      useAuthStore.getState().auth.user?.permissions?.admin_permissions?.audit
        .read
    ).toBe(read)
    expect(denied).toHaveBeenCalledTimes(read ? 0 : 1)
    expect(
      client.getQueriesData({
        queryKey: ['log-export-jobs', 'audit', 'audit_admin', 19],
      })
    ).toHaveLength(0)
    client.clear()
  }
)

it.each([
  [
    'generic',
    {
      action: 'add_quota',
      target_user_id: 11,
      mode: 'unsupported',
      requested_quota: 500000,
      failure_reason: 'invalid_parameters',
    },
    'Adjust user quota',
    'Requested quota: $1 · Invalid adjustment parameters',
  ],
  [
    'user.quota_add',
    {
      target_user_id: 11,
      target_username: 'quota-owner',
      requested_quota: 500000,
      quota: 500000,
      from: 500000,
      to: 1000000,
    },
    'Increase quota for user “quota-owner”',
    'Requested quota: $1 · $1 → $2',
  ],
  [
    'user.quota_subtract',
    {
      target_user_id: 11,
      target_username: 'quota-owner',
      requested_quota: 500000,
      quota: 500000,
      from: 1000000,
      to: 500000,
    },
    'Decrease quota for user “quota-owner”',
    'Requested quota: $1 · $2 → $1',
  ],
  [
    'user.quota_override',
    {
      target_user_id: 11,
      target_username: 'quota-owner',
      requested_quota: 0,
      from: 500000,
      to: 0,
    },
    'Override quota for user “quota-owner”',
    'Requested quota: $0 · $1 → $0',
  ],
  ['token.create', { id: 11, name: '1' }, 'Create API token “1”', ''],
  [
    'token.update',
    {
      id: 11,
      name: 'production',
      changed_fields: ['remain_quota', 'expired_time'],
    },
    'Update API token “production”',
    'Changed fields: Remaining quota, Expiration Time',
  ],
  [
    'token.status_update',
    { id: 11, name: 'production', from: 1, to: 2 },
    'Update API token “production”',
    'Enabled → Disabled',
  ],
  [
    'token.delete',
    { id: 11, name: 'production' },
    'Delete API token “production”',
    '',
  ],
  [
    'token.key_view',
    { id: 11, name: 'production' },
    'View key for API token “production”',
    '',
  ],
  [
    'token.delete_batch',
    { total: 4, count: 1, requested_ids: [11, 11, 12, 99] },
    'Batch delete API tokens',
    'Requested: 4 · Deleted: 1',
  ],
  [
    'token.key_view_batch',
    { total: 4, count: 0, returned_ids: [] },
    'View API token keys in batch',
    'Requested: 4 · Returned: 0',
  ],
])(
  'shows the management target and business outcome for %s directly in the event cell',
  async (action, params, headline, outcome) => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          total: 1,
          items: [
            {
              event_id: 'token-event',
              created_at: 1788600600,
              username: 'root',
              actor_role: 100,
              category: 'security',
              action,
              success: action !== 'generic',
              status: 200,
              other: { op: { action, params } },
            },
          ],
        },
      },
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <AuditLogViewer scope='all' />
      </QueryClientProvider>
    )
    const cell = await screen.findByRole('cell', { name: new RegExp(headline) })
    expect(cell).toHaveTextContent(headline)
    if ('id' in params || 'target_user_id' in params) {
      expect(cell).toHaveTextContent('(ID: 11)')
    }
    if (outcome) expect(cell).toHaveTextContent(outcome)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  }
)

it.each([false, true])(
  'keeps the ID outside long-name truncation and shows complete details (mobile=%s)',
  async (mobile) => {
    const matchMedia = window.matchMedia.bind(window)
    vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
      ...matchMedia(query),
      matches: mobile && query === '(max-width: 640px)',
    }))
    const name = 'production-europe-primary-customer-routing-token'
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          total: 1,
          items: [
            {
              event_id: 'long-token-name',
              created_at: 1788600600,
              username: 'root',
              actor_role: 100,
              category: 'security',
              action: 'token.status_update',
              success: true,
              status: 200,
              other: {
                op: {
                  action: 'token.status_update',
                  params: { id: 11, name, from: 1, to: 2 },
                },
              },
            },
          ],
        },
      },
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <AuditLogViewer scope='all' />
      </QueryClientProvider>
    )
    const id = await screen.findByText('(ID: 11)')
    expect(id).toBeVisible()
    expect(id).toHaveClass('shrink-0')
    expect(id.closest('.truncate')).toBeNull()
    expect(screen.getByText('Enabled → Disabled')).toBeVisible()
    if (mobile) expect(screen.queryByRole('table')).not.toBeInTheDocument()
    else expect(screen.getByRole('table')).toBeVisible()
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Details' }))
    const dialog = await screen.findByRole('dialog', { name: 'Log Details' })
    expect(
      within(dialog).getByText(`Update API token “${name}” (ID: 11)`)
    ).toBeVisible()
    expect(
      within(dialog).getByText('Token Name').parentElement
    ).toHaveTextContent(name)
  }
)

it('uses the shared log toolbar and opens details in a keyboard-accessible dialog without expanding the row', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        total: 1,
        items: [
          {
            event_id: 'request-1',
            created_at: 1788600600,
            username: 'alice',
            category: 'access_token',
            action: 'access_token.request',
            content: '',
            other: {},
            request_id: 'audit-request-42',
            token_ref: 'a'.repeat(64),
            user_agent: 'Browser client with a long version description',
            client: 'Other client',
            source: 'session',
            method: 'GET',
            route: '/api/user/self',
            ip: '127.0.0.1',
            status: 200,
            success: true,
          },
        ],
      },
    },
  })
  renderViewer()
  expect(
    screen.getByRole('button', { name: /\d{4}-\d{2}-\d{2}.*~/ })
  ).toBeVisible()
  expect(screen.getByRole('button', { name: 'Search' })).toBeVisible()
  expect(
    await screen.findByRole('button', { name: 'Go to next page' })
  ).toBeDisabled()
  const trigger = await screen.findByRole('button', { name: 'Details' })
  const row = screen.getByRole('row', { name: /127.0.0.1/ })
  const clientCell = within(row).getByRole('cell', {
    name: 'Other client',
  })
  expect(clientCell).toBeVisible()
  expect(row.querySelector('details')).not.toBeInTheDocument()
  trigger.focus()
  await userEvent.keyboard('{Enter}')
  const dialog = await screen.findByRole('dialog', { name: 'Log Details' })
  expect(within(dialog).getByText('audit-request-42')).toBeVisible()
  expect(within(dialog).getByText('Other client')).toBeVisible()
  expect(dialog).not.toHaveTextContent(
    'Browser client with a long version description'
  )
  await userEvent.keyboard('{Escape}')
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  await waitFor(() => expect(trigger).toHaveFocus())
})

it.each([
  [
    'en',
    en,
    'Updated channel status (ID: 42)',
    'Batch updated channel status (1/2 changed)',
  ],
  ['zh', zh, '更新渠道状态（ID: 42）', '批量更新渠道状态（1/2 个已变更）'],
  ['zh-TW', zhTW, '更新渠道狀態（ID: 42）', '批次更新渠道狀態（1/2 個已變更）'],
  [
    'fr',
    fr,
    'Statut du canal mis à jour (ID : 42)',
    'Statuts des canaux mis à jour par lot (1/2 modifiés)',
  ],
  [
    'ja',
    ja,
    'チャネルの状態を更新（ID: 42）',
    'チャネルの状態を一括更新（1/2 件変更）',
  ],
  [
    'ru',
    ru,
    'Обновлён статус канала (ID: 42)',
    'Массовое обновление статусов каналов (изменено: 1/2)',
  ],
  [
    'vi',
    viLocale,
    'Đã cập nhật trạng thái kênh (ID: 42)',
    'Đã cập nhật trạng thái kênh hàng loạt (1/2 đã thay đổi)',
  ],
] as const)(
  'renders existing channel status audit records in %s',
  async (locale, resources, single, batch) => {
    const i18n = createInstance()
    await i18n.init({
      lng: locale,
      fallbackLng: false,
      resources: { [locale]: resources },
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          total: 2,
          items: [
            {
              event_id: 'single-status',
              created_at: 0,
              username: 'alice',
              action: 'channel.status_update',
              content: 'channel.status_update',
              other: {
                op: {
                  action: 'channel.status_update',
                  params: { id: 42, status: 2, changed: true },
                },
              },
            },
            {
              event_id: 'batch-status',
              created_at: 0,
              username: 'alice',
              action: 'channel.status_update_batch',
              content: 'channel.status_update_batch',
              other: {
                op: {
                  action: 'channel.status_update_batch',
                  params: { count: 1, total: 2, status: 1 },
                },
              },
            },
          ],
        },
      },
    })
    render(
      <I18nextProvider i18n={i18n}>
        <QueryClientProvider client={client}>
          <AuditLogViewer scope='all' />
        </QueryClientProvider>
      </I18nextProvider>
    )
    expect(await screen.findByRole('cell', { name: single })).toBeVisible()
    expect(screen.getByRole('cell', { name: batch })).toBeVisible()
    expect(
      screen.queryByRole('cell', { name: 'channel.status_update' })
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('cell', { name: 'channel.status_update_batch' })
    ).not.toBeInTheDocument()
  }
)

beforeEach(() => {
  vi.stubGlobal('localStorage', {
    getItem: () => null,
    setItem: () => undefined,
    removeItem: () => undefined,
  })
})

it.each([10, 100])(
  'only offers audit identities within current role %s',
  async (role) => {
    useAuthStore.getState().auth.setUser({
      id: 19,
      username: 'admin',
      role,
      permissions: { admin_permissions: { audit: { read: true } } },
    })
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <AuditLogs />
      </QueryClientProvider>
    )
    await userEvent.click(screen.getByRole('button', { name: 'Expand' }))
    await userEvent.click(
      screen.getByRole('combobox', { name: 'Actor identity' })
    )
    for (const label of [
      'Administrator',
      'Employee',
      'User',
      'User (employee status not recorded)',
    ]) {
      expect(screen.getByRole('option', { name: label })).toBeVisible()
    }
    for (const label of ['Super administrator', 'Unknown identity']) {
      if (role === 100) {
        expect(screen.getByRole('option', { name: label })).toBeVisible()
      } else {
        expect(
          screen.queryByRole('option', { name: label })
        ).not.toBeInTheDocument()
      }
    }
    client.clear()
  }
)

it('clears higher-role filters and records when the current account is demoted', async () => {
  const admin = {
    id: 19,
    username: 'admin',
    role: 100,
    permissions: { admin_permissions: { audit: { read: true } } },
  }
  useAuthStore.getState().auth.setUser(admin)
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: [], total: 0 } },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <AuditLogs />
    </QueryClientProvider>
  )
  await userEvent.click(screen.getByRole('button', { name: 'Expand' }))
  await userEvent.click(
    screen.getByRole('combobox', { name: 'Actor identity' })
  )
  await userEvent.click(
    screen.getByRole('option', { name: 'Super administrator' })
  )
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith(
      '/api/audit',
      expect.objectContaining({
        params: expect.objectContaining({ actor_type: 'root' }),
      })
    )
  )
  get.mockClear()
  act(() => useAuthStore.getState().auth.setUser({ ...admin, role: 10 }))
  await waitFor(() =>
    expect(get.mock.calls.some(([url]) => url === '/api/audit')).toBe(true)
  )
  for (const [, config] of get.mock.calls) {
    expect(config?.params).not.toHaveProperty('actor_type', 'root')
  }
  await userEvent.click(screen.getByRole('button', { name: 'Expand' }))
  expect(screen.getByRole('combobox', { name: 'Actor identity' })).toHaveValue(
    'All identities'
  )
  client.clear()
})

it.each([false, true])(
  'limits self activity fields and filters for employees=%s',
  async (employee) => {
    useAuthStore.getState().auth.setUser({
      id: 19,
      username: 'self',
      role: 1,
      is_employee: employee,
    })
    const get = vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          items: [
            {
              event_id: 'activity',
              created_at: 1788600600,
              category: 'operation',
              action: 'token.create',
              success: true,
              ip: '192.0.2.1',
              client: 'Edge · Windows',
              source: 'session',
              request_id: 'support',
              target: { resource: 'token', id: '86', name: 'My key' },
            },
          ],
          total: 1,
        },
      },
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <AuditLogViewer
          scope='self'
          initialFilters={{
            actor_type: 'root',
            route: '/internal',
            token_ref: 'private',
          }}
        />
      </QueryClientProvider>
    )
    expect(await screen.findByText('My key')).toBeVisible()
    await userEvent.click(screen.getByRole('button', { name: 'Expand' }))
    for (const label of [
      'Actor identity',
      'Route',
      'Token identifier',
      'Username',
    ]) {
      expect(
        screen.queryByRole('combobox', { name: label })
      ).not.toBeInTheDocument()
      expect(
        screen.queryByRole('textbox', { name: label })
      ).not.toBeInTheDocument()
    }
    expect(screen.getByRole('textbox', { name: 'Request ID' })).toBeVisible()
    expect(screen.getByRole('textbox', { name: 'IP' })).toBeVisible()
    fireEvent.change(screen.getByRole('textbox', { name: 'IP' }), {
      target: { value: '192.0.2.5' },
    })
    fireEvent.change(screen.getByRole('textbox', { name: 'Request ID' }), {
      target: { value: 'request-filter' },
    })
    await userEvent.click(screen.getByRole('button', { name: 'Search' }))
    await waitFor(() =>
      expect(get).toHaveBeenLastCalledWith(
        '/api/audit/self',
        expect.objectContaining({
          params: expect.objectContaining({
            ip: '192.0.2.5',
            request_id: 'request-filter',
          }),
        })
      )
    )
    expect(
      screen.queryByRole('columnheader', { name: 'HTTP' })
    ).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Details' }))
    expect(screen.getByRole('dialog')).not.toHaveTextContent(
      /Actor identity|HTTP|Route|Other fields/
    )
    expect(
      get.mock.calls.some(([url]) => url === '/api/audit/self/routes')
    ).toBe(false)
    const query = get.mock.calls.find(([url]) => url === '/api/audit/self')?.[1]
      ?.params
    expect(query).not.toHaveProperty('actor_type')
    expect(query).not.toHaveProperty('route')
    expect(query).not.toHaveProperty('token_ref')
    client.clear()
  }
)
afterEach(() => {
  cleanup()
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})
function renderViewer(scope: 'all' | 'self' = 'self') {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <AuditLogViewer
        scope={scope}
        accessOnly
        currentTokenRef={'a'.repeat(64)}
      />
    </QueryClientProvider>
  )
}

it('allows exact request lookup in access-token history and trims copied identifiers', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: [], total: 45 } },
  })
  renderViewer()
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Go to next page' })
    ).toBeEnabled()
  )
  await userEvent.click(screen.getByRole('button', { name: 'Go to next page' }))
  await userEvent.click(screen.getByRole('button', { name: 'Expand' }))
  fireEvent.change(screen.getByRole('textbox', { name: 'Request ID' }), {
    target: { value: '  support-request  ' },
  })
  fireEvent.change(screen.getByRole('textbox', { name: 'IP' }), {
    target: { value: '  192.0.2.8  ' },
  })
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/audit/self', {
      signal: expect.any(AbortSignal),
      params: expect.objectContaining({
        p: 1,
        category: 'access_token',
        request_id: 'support-request',
        ip: '192.0.2.8',
      }),
    })
  )
  expect(screen.getByRole('textbox', { name: 'Request ID' })).toHaveValue(
    'support-request'
  )
})

it('keeps displayed filters aligned with the applied query after filtering by a row IP', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        items: [
          {
            event_id: 'ip-row',
            created_at: 1788600600,
            category: 'access_token',
            action: 'access_token.request',
            success: true,
            ip: '192.0.2.1',
            client: '',
            source: 'access_token',
            request_id: 'request',
          },
        ],
        total: 1,
      },
    },
  })
  renderViewer()
  await userEvent.click(await screen.findByRole('combobox', { name: 'Result' }))
  await userEvent.click(screen.getByRole('option', { name: 'Failed' }))
  await userEvent.click(screen.getByRole('combobox', { name: 'Token scope' }))
  await userEvent.click(screen.getByRole('option', { name: 'Current token' }))
  await userEvent.click(
    screen.getByRole('button', { name: 'Filter by this IP' })
  )
  await waitFor(() =>
    expect(get.mock.lastCall?.[1]?.params).toMatchObject({ ip: '192.0.2.1' })
  )
  expect(get.mock.lastCall?.[1]?.params).not.toHaveProperty('success')
  expect(get.mock.lastCall?.[1]?.params).not.toHaveProperty('token_ref')
  expect(screen.getByRole('combobox', { name: 'Result' })).toHaveValue(
    'All results'
  )
  expect(screen.getByRole('combobox', { name: 'Token scope' })).toHaveValue(
    'All tokens'
  )
})

it.each([false, true])(
  'searches complete local days unless an explicit historical range is pinned (%s)',
  async (pinned) => {
    const now = vi
      .spyOn(Date, 'now')
      .mockReturnValue(new Date('2026-11-23T13:39:46').getTime())
    const get = vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const range = { start_timestamp: 1795000000, end_timestamp: 1795086400 }
    render(
      <QueryClientProvider client={client}>
        <AuditLogViewer
          scope='self'
          initialFilters={pinned ? range : undefined}
        />
      </QueryClientProvider>
    )
    await waitFor(() => expect(get).toHaveBeenCalled())
    expect(get.mock.lastCall?.[1]?.params).toMatchObject(
      pinned
        ? range
        : {
            start_timestamp: new Date('2026-11-22T00:00:00').getTime() / 1000,
            end_timestamp: new Date('2026-11-23T23:59:59').getTime() / 1000,
          }
    )
    get.mockClear()
    await userEvent.click(screen.getByRole('button', { name: 'Search' }))
    await waitFor(() => expect(get).toHaveBeenCalled())
    now.mockReturnValue(new Date('2026-11-24T00:01:00').getTime())
    await userEvent.click(screen.getByRole('button', { name: 'Search' }))
    await waitFor(() =>
      expect(get.mock.lastCall?.[1]?.params).toMatchObject(
        pinned
          ? range
          : {
              start_timestamp: new Date('2026-11-23T00:00:00').getTime() / 1000,
              end_timestamp: new Date('2026-11-24T23:59:59').getTime() / 1000,
            }
      )
    )
    client.clear()
  }
)

it('exports applied token filters and opens isolated audit tasks without usage routes', async () => {
  useAuthStore.getState().auth.setUser({ id: 19, username: 'user', role: 1 })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data:
        url === '/api/audit/self/export/jobs'
          ? { jobs: [], enabled: true, max_range_sec: 62 * 86400 }
          : { items: [], total: 0 },
    },
  }))
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { job_id: 'audit-created' } },
  })
  const remove = vi.spyOn(api, 'delete')
  render(
    <QueryClientProvider client={client}>
      <AuditLogViewer
        scope='self'
        accessOnly
        currentTokenRef={'a'.repeat(64)}
        defaultTokenScope='current'
        initialFilters={{ success: 'false' }}
      />
    </QueryClientProvider>
  )
  await userEvent.click(screen.getByRole('button', { name: 'Export' }))
  const dialog = await screen.findByRole('dialog', {
    name: 'Export audit logs',
  })
  const submit = within(dialog).getByRole('button', { name: 'Start export' })
  await waitFor(() => expect(submit).toBeEnabled())
  await userEvent.click(submit)
  await waitFor(() => expect(post).toHaveBeenCalledOnce())
  expect(post).toHaveBeenCalledWith('/api/audit/self/export/jobs', {
    filters: expect.objectContaining({
      category: 'access_token',
      token_ref: 'a'.repeat(64),
      success: false,
    }),
    options: expect.objectContaining({ header: true, csv_bom: true }),
  })
  const payload = post.mock.calls[0][1] as { filters: Record<string, unknown> }
  expect(payload.filters).not.toHaveProperty('p')
  expect(payload.filters).not.toHaveProperty('page_size')
  expect(
    get.mock.calls.some(([url]) => String(url).startsWith('/api/log/'))
  ).toBe(false)
  expect(
    client
      .getQueryCache()
      .getAll()
      .filter((query) => query.queryKey[0] === 'log-export-jobs')
      .every(
        (query) =>
          query.queryKey[1] === 'audit' &&
          query.queryKey[2] === 'audit_self' &&
          query.queryKey[3] === 19
      )
  ).toBe(true)
  expect(remove).not.toHaveBeenCalled()
})

it.each([true, false])(
  'keeps the risk filter when export is denied and security read is %s',
  async (read) => {
    useAuthStore.getState().auth.setUser({
      id: 19,
      username: 'admin',
      role: 10,
      permissions: {
        admin_permissions: {
          audit: { read: true, export: true },
          audit_security: { read, export: false },
        },
      },
    })
    vi.spyOn(api, 'get').mockImplementation(async (url) => ({
      data: {
        success: true,
        data:
          url === '/api/audit/export/jobs'
            ? {
                jobs: [],
                enabled: true,
                max_range_sec: 86400,
                risk_export_enabled: false,
              }
            : { items: [], total: 0 },
      },
    }))
    const post = vi.spyOn(api, 'post')
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <AuditLogViewer scope='all' initialFilters={{ ip_risk: 'high' }} />
      </QueryClientProvider>
    )
    await userEvent.click(screen.getByRole('button', { name: 'Export' }))
    const dialog = await screen.findByRole('dialog', {
      name: 'Export audit logs',
    })
    expect(
      await within(dialog).findByText(
        'Risk export requires security export permission.'
      )
    ).toBeVisible()
    expect(
      within(dialog).getByRole('button', { name: 'Start export' })
    ).toBeDisabled()
    expect(post).not.toHaveBeenCalled()
  }
)

it('closes the export panel and clears its task cache when the account changes', async () => {
  useAuthStore.getState().auth.setUser({ id: 19, username: 'first', role: 1 })
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: { jobs: [], items: [], enabled: true, max_range_sec: 86400 },
    },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <AuditLogViewer scope='self' />
    </QueryClientProvider>
  )
  await userEvent.click(screen.getByRole('button', { name: 'Export' }))
  await screen.findByRole('dialog', { name: 'Export audit logs' })
  act(() =>
    useAuthStore
      .getState()
      .auth.setUser({ id: 20, username: 'second', role: 1 })
  )
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  expect(
    client.getQueriesData({
      queryKey: ['log-export-jobs', 'audit', 'audit_self', 19],
    })
  ).toHaveLength(0)
})

it('restarts an expired risk cursor on retry without changing the applied filters', async () => {
  useAuthStore.getState().auth.setUser({ id: 19, username: 'root', role: 100 })
  const get = vi.spyOn(api, 'get').mockImplementation(async (url, config) => {
    const params = config?.params as { cursor?: string } | undefined
    if (url === '/api/audit/security/ip-records' && params?.cursor) {
      return {
        data: {
          success: false,
          message: 'Risk results have changed. Refresh and try again.',
        },
      }
    }
    return {
      data: {
        success: true,
        data: { items: [], next_cursor: 'expired-cursor' },
      },
    }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  render(
    <QueryClientProvider client={client}>
      <AuditLogViewer
        scope='all'
        initialFilters={{ ip_risk: 'high', ip: '192.0.2.1' }}
      />
    </QueryClientProvider>
  )
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Continue loading' })
    ).toBeEnabled()
  )
  await userEvent.click(
    screen.getByRole('button', { name: 'Continue loading' })
  )
  await userEvent.click(await screen.findByRole('button', { name: 'Retry' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/audit/security/ip-records', {
      params: expect.objectContaining({
        cursor: '',
        ip_risk: 'high',
        ip: '192.0.2.1',
      }),
      signal: expect.any(AbortSignal),
    })
  )
  expect(
    screen.queryByRole('button', { name: 'Retry' })
  ).not.toBeInTheDocument()
  client.clear()
})

it('continues sparse risk results on the server and restarts the cursor on search', async () => {
  useAuthStore.getState().auth.setUser({ id: 19, username: 'root', role: 100 })
  const get = vi.spyOn(api, 'get').mockImplementation(async (url, config) => {
    const params = config?.params as { cursor?: string } | undefined
    if (url === '/api/audit/security/ip-records') {
      return {
        data: {
          success: true,
          data: {
            items: [],
            next_cursor: params?.cursor ? undefined : 'next-event',
          },
        },
      }
    }
    return { data: { success: true, data: { items: [], total: 0 } } }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <AuditLogViewer scope='all' />
    </QueryClientProvider>
  )
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Expand' }))
  fireEvent.change(screen.getByLabelText('IP'), {
    target: { value: '2001:db8::1' },
  })
  await user.click(screen.getByRole('combobox', { name: 'IP risk' }))
  await user.click(await screen.findByRole('option', { name: 'High risk' }))
  expect(get).not.toHaveBeenCalledWith(
    '/api/audit/security/ip-records',
    expect.anything()
  )
  await user.click(screen.getByRole('button', { name: 'Search' }))
  expect(
    await screen.findByText('Continue loading to search the remaining records')
  ).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Go to next page' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Continue loading' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/audit/security/ip-records', {
      params: expect.objectContaining({
        ip: '2001:db8::1',
        ip_risk: 'high',
        cursor: 'next-event',
      }),
      signal: expect.any(AbortSignal),
    })
  )
  expect(await screen.findByText('No records')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/audit/security/ip-records', {
      params: expect.objectContaining({ cursor: '' }),
      signal: expect.any(AbortSignal),
    })
  )
})

it('loads management route options only when opened and applies an exact route on search', async () => {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data:
        url === '/api/audit/routes'
          ? { items: ['/api/user/self'] }
          : { items: [], total: 0 },
    },
  }))
  renderViewer('all')
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Expand' }))
  expect(get).not.toHaveBeenCalledWith('/api/audit/routes', expect.anything())
  await user.click(screen.getByRole('combobox', { name: 'Route' }))
  await user.click(
    await screen.findByRole('option', { name: '/api/user/self' })
  )
  expect(
    get.mock.calls.find(([url]) => url === '/api/audit')?.[1]?.params
  ).not.toHaveProperty('route')
  await user.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/audit', {
      params: expect.objectContaining({ route: '/api/user/self' }),
      signal: expect.any(AbortSignal),
    })
  )
})

it('filters own access history by result, generation and time and resets pagination', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: [], total: 45 } },
  })
  renderViewer()
  const user = userEvent.setup()
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Go to next page' })
    ).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: 'Go to next page' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/audit/self', {
      signal: expect.any(AbortSignal),
      params: expect.objectContaining({ p: 2, category: 'access_token' }),
    })
  )
  await user.click(screen.getByRole('combobox', { name: 'Result' }))
  await user.click(await screen.findByRole('option', { name: 'Failed' }))
  await user.click(screen.getByRole('combobox', { name: 'Token scope' }))
  await user.click(
    await screen.findByRole('option', { name: 'Historical tokens' })
  )
  await user.click(screen.getByRole('button', { name: /\d{4}-\d{2}-\d{2}.*~/ }))
  fireEvent.change(screen.getByLabelText('Start Time'), {
    target: { value: '2026-09-01T12:00' },
  })
  fireEvent.change(screen.getByLabelText('End Time'), {
    target: { value: '2026-09-02T12:00' },
  })
  await user.click(screen.getByRole('button', { name: 'Confirm' }))
  await user.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/audit/self', {
      signal: expect.any(AbortSignal),
      params: expect.objectContaining({
        p: 1,
        success: 'false',
        exclude_token_ref: 'a'.repeat(64),
        start_timestamp: expect.any(Number),
      }),
    })
  )
  await user.click(screen.getByRole('combobox', { name: 'Token scope' }))
  await user.click(await screen.findByRole('option', { name: 'Current token' }))
  await user.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/audit/self', {
      signal: expect.any(AbortSignal),
      params: expect.objectContaining({ token_ref: 'a'.repeat(64) }),
    })
  )
  expect(screen.queryByLabelText('Username')).not.toBeInTheDocument()
  expect(screen.getByRole('table').style.minWidth).toMatch(/max\(100%, \d+px\)/)
})

it('opens on the current token when asked and resets back to it', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: [], total: 0 } },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <AuditLogViewer
        scope='self'
        accessOnly
        currentTokenRef={'a'.repeat(64)}
        defaultTokenScope='current'
      />
    </QueryClientProvider>
  )
  const user = userEvent.setup()
  await waitFor(() => expect(get).toHaveBeenCalled())
  expect(get.mock.calls[0]).toEqual([
    '/api/audit/self',
    {
      signal: expect.any(AbortSignal),
      params: expect.objectContaining({
        category: 'access_token',
        token_ref: 'a'.repeat(64),
      }),
    },
  ])
  const reset = screen.getByRole('button', { name: 'Reset' })
  expect(reset).toBeDisabled()
  await user.click(screen.getByRole('combobox', { name: 'Token scope' }))
  await user.click(await screen.findByRole('option', { name: 'All tokens' }))
  await user.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(get.mock.lastCall?.[1]?.params).not.toHaveProperty('token_ref')
  )
  await user.click(screen.getByRole('combobox', { name: 'Result' }))
  await user.click(await screen.findByRole('option', { name: 'Failed' }))
  await user.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/audit/self', {
      signal: expect.any(AbortSignal),
      params: expect.objectContaining({ success: 'false' }),
    })
  )
  await user.click(reset)
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/audit/self', {
      signal: expect.any(AbortSignal),
      params: expect.objectContaining({ token_ref: 'a'.repeat(64) }),
    })
  )
  expect(get.mock.lastCall?.[1]?.params).not.toHaveProperty('success')
  expect(screen.getByRole('combobox', { name: 'Token scope' })).toHaveValue(
    'Current token'
  )
  expect(reset).toBeDisabled()
})

it('a failed history query exposes retry and no empty history claim', async () => {
  vi.spyOn(api, 'get')
    .mockRejectedValueOnce(new Error('offline'))
    .mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
  renderViewer()
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Failed to load audit records'
  )
  expect(screen.queryByText('No records')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('No records')).toBeVisible()
})

it('administrator scope uses the admin endpoint and exposes the username filter', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: [], total: 0 } },
  })
  renderViewer('all')
  await userEvent.click(screen.getByRole('button', { name: 'Expand' }))
  fireEvent.change(screen.getByLabelText('Username'), {
    target: { value: 'alice' },
  })
  expect(get.mock.lastCall?.[1]?.params).not.toHaveProperty('username')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/audit', {
      signal: expect.any(AbortSignal),
      params: expect.objectContaining({ username: 'alice' }),
    })
  )
})

it('changing rows per page resets pagination and sends the selected page size', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: [], total: 80 } },
  })
  renderViewer()
  const user = userEvent.setup()
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Go to next page' })
    ).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: 'Go to next page' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/audit/self', {
      signal: expect.any(AbortSignal),
      params: expect.objectContaining({ p: 2 }),
    })
  )
  await user.click(screen.getByRole('combobox', { name: 'Rows per page' }))
  await user.click(await screen.findByRole('option', { name: '50' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/audit/self', {
      signal: expect.any(AbortSignal),
      params: expect.objectContaining({ p: 1, page_size: 50 }),
    })
  )
})

it.each([10, 100])(
  'role %i defaults to all audit records and can switch both ways',
  async (role) => {
    useAuthStore.getState().auth.setUser({
      id: 1,
      username: 'admin',
      role,
      permissions: { admin_permissions: { audit: { read: role === 10 } } },
    })
    const get = vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <AuditLogs />
      </QueryClientProvider>
    )
    expect(screen.getByRole('tab', { name: 'All' })).toHaveAttribute(
      'aria-selected',
      'true'
    )
    await waitFor(() =>
      expect(get).toHaveBeenCalledWith('/api/audit', expect.anything())
    )
    expect(get).not.toHaveBeenCalledWith('/api/audit/self', expect.anything())
    await userEvent.click(screen.getByRole('tab', { name: 'Only Mine' }))
    if (role === 100) {
      await waitFor(() =>
        expect(get).toHaveBeenLastCalledWith(
          '/api/audit',
          expect.objectContaining({
            params: expect.objectContaining({ mine: true }),
          })
        )
      )
      expect(get.mock.lastCall?.[1]?.params).not.toHaveProperty('username')
      expect(get).not.toHaveBeenCalledWith('/api/audit/self', expect.anything())
    } else {
      await waitFor(() =>
        expect(get).toHaveBeenLastCalledWith(
          '/api/audit/self',
          expect.anything()
        )
      )
      expect(get.mock.lastCall?.[1]?.params).not.toHaveProperty('mine')
    }
    expect(screen.getByRole('tab', { name: 'Only Mine' })).toHaveAttribute(
      'aria-selected',
      'true'
    )
    await userEvent.click(screen.getByRole('tab', { name: 'All' }))
    await waitFor(() =>
      expect(get).toHaveBeenLastCalledWith('/api/audit', expect.anything())
    )
    expect(get.mock.lastCall?.[1]?.params).not.toHaveProperty('mine')
    expect(screen.getByRole('tab', { name: 'All' })).toHaveAttribute(
      'aria-selected',
      'true'
    )
  }
)

it('shows channel status details when a super admin opens their own operation', async () => {
  useAuthStore.getState().auth.setUser({
    id: 7,
    username: 'P90f18MT',
    role: 100,
  })
  vi.spyOn(api, 'get').mockImplementation(async (url, config) => {
    const params = config?.params as { mine?: boolean } | undefined
    if (url === '/api/audit' && params?.mine === true) {
      return {
        data: {
          success: true,
          data: {
            total: 1,
            items: [
              {
                event_id: 'channel-status',
                user_id: 7,
                username: 'P90f18MT',
                actor_role: 100,
                created_at: 1788600600,
                category: 'operation',
                action: 'channel.status_update',
                auth_method: 'session',
                ip: '154.92.130.108',
                user_agent:
                  'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Version/18.0 Mobile/15E148 Safari/604.1',
                method: 'PUT',
                route: '/api/channel/:id/status',
                status: 200,
                success: true,
                request_id: '202611221316395487018688268d9d6AAvPzFDc',
                content: 'channel.status_update',
                other: {
                  op: {
                    action: 'channel.status_update',
                    params: { id: 42, status: 1, changed: true },
                  },
                  changes: {
                    version: 1,
                    outcome: 'committed',
                    targets: [
                      {
                        resource: 'channel',
                        id: '42',
                        name: 'sentinel-channel',
                        operation: 'update',
                        visibility: 'admin',
                        consistency: 'observed',
                        fields: [
                          {
                            path: ['status'],
                            old: {
                              state: 'value',
                              value: 2,
                              value_type: 'integer',
                            },
                            new: {
                              state: 'value',
                              value: 1,
                              value_type: 'integer',
                            },
                          },
                        ],
                      },
                    ],
                  },
                },
              },
            ],
          },
        },
      }
    }
    return { data: { success: true, data: { items: [], total: 0 } } }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <AuditLogs />
    </QueryClientProvider>
  )
  await userEvent.click(screen.getByRole('tab', { name: 'Only Mine' }))
  expect(
    await screen.findByRole('cell', {
      name: /Updated channel status \(ID: 42\)/,
    })
  ).toBeVisible()
  expect(screen.getByRole('columnheader', { name: 'HTTP' })).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: 'Details' }))
  const dialog = await screen.findByRole('dialog', { name: 'Log Details' })
  expect(dialog).toHaveTextContent('Updated channel status (ID: 42)')
  expect(dialog).toHaveTextContent('sentinel-channel (ID: 42)')
  expect(dialog).toHaveTextContent('Enabled')
  expect(dialog).toHaveTextContent('Disabled')
  expect(dialog).toHaveTextContent('PUT')
  expect(dialog).toHaveTextContent('/api/channel/:id/status')
})

it('keeps a super admin access-token history on the public activity contract', async () => {
  useAuthStore.getState().auth.setUser({
    id: 7,
    username: 'P90f18MT',
    role: 100,
  })
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: [], total: 0 } },
  })
  renderViewer()
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith('/api/audit/self', expect.anything())
  )
  expect(get).not.toHaveBeenCalledWith(
    '/api/audit',
    expect.objectContaining({
      params: expect.objectContaining({ mine: true }),
    })
  )
})

it.each([1, 10])(
  'role %i without an audit grant has personal tabs and cannot access management scopes',
  async (role) => {
    useAuthStore.getState().auth.setUser({ id: 2, username: 'alice', role })
    const get = vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <AuditLogs />
      </QueryClientProvider>
    )
    await waitFor(() =>
      expect(get).toHaveBeenCalledWith('/api/audit/self', expect.anything())
    )
    expect(get).not.toHaveBeenCalledWith('/api/audit', expect.anything())
    expect(screen.getByRole('tab', { name: 'Only Mine' })).toHaveAttribute(
      'aria-selected',
      'true'
    )
    expect(screen.getByRole('tab', { name: 'My security' })).toBeVisible()
    expect(screen.queryByRole('tab', { name: 'All' })).not.toBeInTheDocument()
    expect(
      screen.queryByRole('tab', { name: 'Security monitoring' })
    ).not.toBeInTheDocument()
  }
)

it('clears global records and open details on revocation, falls back to self, and refreshes permissions', async () => {
  const admin = {
    id: 1,
    username: 'admin',
    role: 10,
    permissions: { admin_permissions: { audit: { read: true } } },
  }
  useAuthStore.getState().auth.setUser(admin)
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const forbidden = new AxiosError(
    'Forbidden',
    'ERR_BAD_REQUEST',
    undefined,
    undefined,
    { status: 403 } as AxiosResponse
  )
  let revoked = false
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/user/self') {
      return {
        data: {
          success: true,
          data: {
            ...admin,
            permissions: { admin_permissions: { audit: { read: false } } },
          },
        },
      }
    }
    if (url === '/api/audit' && revoked) throw forbidden
    return {
      data: {
        success: true,
        data: {
          total: url === '/api/audit' ? 1 : 0,
          items:
            url === '/api/audit'
              ? [
                  {
                    event_id: 'private-event',
                    user_id: 2,
                    username: 'other-account',
                    actor_role: 10,
                    created_at: 1788600600,
                    category: 'operation',
                    action: 'channel.update',
                    content: 'Private operation',
                    other: {},
                    request_id: 'private-request',
                    token_ref: '',
                    user_agent: '',
                    method: 'PUT',
                    route: '/api/channel/',
                    ip: '',
                    status: 200,
                    success: true,
                  },
                ]
              : [],
        },
      },
    }
  })
  client.setQueryData(['audit', 99, 'all'], { items: ['separate-session'] })
  render(
    <QueryClientProvider client={client}>
      <AuditLogs />
    </QueryClientProvider>
  )
  await screen.findByText('other-account')
  await userEvent.click(screen.getByRole('button', { name: 'Details' }))
  expect(
    await screen.findByRole('dialog', { name: 'Log Details' })
  ).toBeVisible()
  revoked = true
  await client.invalidateQueries({ queryKey: ['audit', 1, 'all'] })
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith('/api/audit/self', expect.anything())
  )
  await waitFor(() => expect(get).toHaveBeenCalledWith('/api/user/self'))
  expect(screen.queryByText('other-account')).not.toBeInTheDocument()
  expect(screen.queryByRole('tab', { name: 'All' })).not.toBeInTheDocument()
  expect(client.getQueriesData({ queryKey: ['audit', 1, 'all'] })).toHaveLength(
    0
  )
  expect(client.getQueryData(['audit', 99, 'all'])).toEqual({
    items: ['separate-session'],
  })
  expect(
    useAuthStore.getState().auth.user?.permissions?.admin_permissions?.audit
      .read
  ).toBe(false)
})

it('mobile access history keeps pagination visible and puts result filters in a drawer', async () => {
  const pointerCapture = Object.getOwnPropertyDescriptor(
    HTMLElement.prototype,
    'setPointerCapture'
  )
  // JSDOM does not implement the pointer-capture API used by the mobile drawer.
  Object.defineProperty(HTMLElement.prototype, 'setPointerCapture', {
    configurable: true,
    value: () => undefined,
  })
  try {
    const matchMedia = window.matchMedia.bind(window)
    vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
      ...matchMedia(query),
      matches: query === '(max-width: 640px)',
    }))
    const get = vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
    renderViewer()
    const user = userEvent.setup()
    expect(
      screen.getByRole('button', { name: 'Go to next page' })
    ).toBeVisible()
    expect(
      screen.queryByRole('combobox', { name: 'Result' })
    ).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Filter' }))
    const drawer = await screen.findByRole('dialog', { name: 'Filter' })
    await user.click(within(drawer).getByRole('combobox', { name: 'Result' }))
    await user.click(await screen.findByRole('option', { name: 'Failed' }))
    expect(get.mock.lastCall?.[1]?.params).not.toHaveProperty('success')
    await user.click(within(drawer).getByRole('button', { name: 'Search' }))
    await waitFor(() =>
      expect(get).toHaveBeenLastCalledWith('/api/audit/self', {
        signal: expect.any(AbortSignal),
        params: expect.objectContaining({ success: 'false', p: 1 }),
      })
    )
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    expect(
      screen.getByRole('button', { name: 'Go to next page' })
    ).toBeVisible()
  } finally {
    if (pointerCapture) {
      Object.defineProperty(
        HTMLElement.prototype,
        'setPointerCapture',
        pointerCapture
      )
    } else {
      Reflect.deleteProperty(HTMLElement.prototype, 'setPointerCapture')
    }
  }
})

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
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import {
  checkVerificationMethods,
  verify,
} from '@/features/auth/secure-verification/api'

import { getMonitorSettings, saveMonitorSettings } from './api'
import { defaultAutoBanRules } from './lib/auto-ban'
import { SettingsDrawer } from './settings-drawer'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))
vi.mock('@/stores/auth-store', () => ({
  useAuthStore: (select: (value: unknown) => unknown) =>
    select({ auth: { user: { id: 1, role: 100 } } }),
}))
vi.mock('./api', () => ({
  getMonitorSettings: vi.fn(),
  saveMonitorSettings: vi.fn(),
}))
vi.mock('@/features/auth/secure-verification/api', () => ({
  checkVerificationMethods: vi.fn(),
  verify: vi.fn(),
}))

beforeEach(() => {
  vi.mocked(getMonitorSettings).mockResolvedValue({
    ip_intelligence_timeout_seconds: '5',
  })
  vi.mocked(checkVerificationMethods).mockResolvedValue({
    scope: 'audit.security.settings',
    methods: [{ method: 'password', available: true }],
    oauth_providers: [],
    password_encryption_enabled: false,
  })
  vi.mocked(verify).mockResolvedValue({
    method: 'password',
    proof_token: 'test-proof',
    scope: 'audit.security.settings',
    expires_at: 9999999999,
  })
  vi.mocked(saveMonitorSettings).mockResolvedValue(undefined)
})
afterEach(() => {
  cleanup()
  vi.resetAllMocks()
})

function renderDrawer() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const onClose = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <SettingsDrawer onClose={onClose} />
    </QueryClientProvider>
  )
  return onClose
}

it('opens on the right and validates the lookup timeout before verification', async () => {
  renderDrawer()
  const drawer = screen.getByRole('dialog', { name: 'Monitoring settings' })
  expect(drawer).toHaveAttribute('data-side', 'right')
  await userEvent.click(await screen.findByRole('tab', { name: 'IP lookup' }))
  await userEvent.click(
    await screen.findByRole('button', { name: 'More settings' })
  )
  const input = await screen.findByLabelText('IP lookup timeout (seconds)')
  expect(input).toHaveValue(5)
  expect(input).toHaveAttribute('aria-describedby')
  for (const value of ['0', '9', '1.5', '']) {
    fireEvent.change(input, { target: { value } })
    await userEvent.click(within(drawer).getByRole('button', { name: 'Save' }))
    expect(input).toBeInvalid()
    expect(checkVerificationMethods).not.toHaveBeenCalled()
    expect(saveMonitorSettings).not.toHaveBeenCalled()
  }
})

it.each([1, 8])(
  'saves lookup timeout boundary %s with proof bound to the reviewed values',
  async (value) => {
    const onClose = renderDrawer()
    await userEvent.click(await screen.findByRole('tab', { name: 'IP lookup' }))
    await userEvent.click(
      await screen.findByRole('button', { name: 'More settings' })
    )
    fireEvent.change(
      await screen.findByLabelText('IP lookup timeout (seconds)'),
      { target: { value: String(value) } }
    )
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    const verification = await screen.findByRole('dialog', {
      name: 'Security verification',
    })
    expect(saveMonitorSettings).not.toHaveBeenCalled()
    await userEvent.type(
      await within(verification).findByLabelText('Password', {
        selector: 'input',
      }),
      'fixture-password'
    )
    await userEvent.click(
      within(verification).getByRole('button', { name: 'Verify' })
    )
    await waitFor(() =>
      expect(saveMonitorSettings).toHaveBeenCalledWith(
        expect.objectContaining({
          ip_intelligence_timeout_seconds: String(value),
        }),
        'test-proof'
      )
    )
    expect(verify).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({
        scope: 'audit.security.settings',
        context: {
          values: expect.objectContaining({
            ip_intelligence_timeout_seconds: String(value),
          }),
        },
      }),
      false,
      expect.objectContaining({ aborted: false }),
      expect.any(Function)
    )
    await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
  }
)

it('retains the draft when verification is cancelled and when saving fails', async () => {
  const onClose = renderDrawer()
  await userEvent.click(await screen.findByRole('tab', { name: 'IP lookup' }))
  await userEvent.click(
    await screen.findByRole('button', { name: 'More settings' })
  )
  const input = await screen.findByLabelText('IP lookup timeout (seconds)')
  fireEvent.change(input, { target: { value: '4' } })
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  let verification = await screen.findByRole('dialog', {
    name: 'Security verification',
  })
  await userEvent.click(
    within(verification).getByRole('button', { name: 'Cancel' })
  )
  await waitFor(() =>
    expect(
      screen.queryByRole('dialog', { name: 'Security verification' })
    ).not.toBeInTheDocument()
  )
  expect(screen.getByLabelText('IP lookup timeout (seconds)')).toHaveValue(4)
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save' })).toHaveFocus()
  )
  expect(saveMonitorSettings).not.toHaveBeenCalled()
  expect(onClose).not.toHaveBeenCalled()
  vi.mocked(saveMonitorSettings).mockRejectedValueOnce(
    new Error('fixture failure')
  )
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  verification = await screen.findByRole('dialog', {
    name: 'Security verification',
  })
  await userEvent.type(
    await within(verification).findByLabelText('Password', {
      selector: 'input',
    }),
    'fixture-password'
  )
  await userEvent.click(
    within(verification).getByRole('button', { name: 'Verify' })
  )
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled()
  )
  expect(screen.getByLabelText('IP lookup timeout (seconds)')).toHaveValue(4)
  expect(onClose).not.toHaveBeenCalled()
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(onClose).toHaveBeenCalledOnce()
})

it('keeps automatic bans off by default and omits fixed travel thresholds from the submitted snapshot', async () => {
  renderDrawer()
  await userEvent.click(
    await screen.findByRole('tab', { name: 'Automatic response' })
  )
  expect(
    screen.getByRole('switch', { name: 'Automatic response' })
  ).not.toBeChecked()
  expect(
    screen.getByRole('switch', { name: 'Include super administrators' })
  ).not.toBeChecked()
  expect(
    screen.queryByText('Travel speed threshold (km/h)')
  ).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  const dialog = await screen.findByRole('dialog', {
    name: 'Security verification',
  })
  await userEvent.type(
    await within(dialog).findByLabelText('Password', { selector: 'input' }),
    'fixture-password'
  )
  await userEvent.click(within(dialog).getByRole('button', { name: 'Verify' }))
  await waitFor(() => expect(saveMonitorSettings).toHaveBeenCalledOnce())
  const [values] = vi.mocked(saveMonitorSettings).mock.calls[0]
  expect(values.auto_ban_enabled).toBe('false')
  expect(values.auto_ban_include_staff).toBe('false')
  expect(values.auto_ban_exclude_root).toBe('true')
  expect(values.auto_ban_excluded_user_ids_json).toBe('[]')
  expect(
    Object.keys(values).some((key) => key.startsWith('impossible_travel'))
  ).toBe(false)
  expect(values).not.toHaveProperty('_excludedIDs')
  expect(values).not.toHaveProperty('ip_intelligence_cache_ttl_minutes')
  expect(
    screen.queryByLabelText('IP cache lifetime (minutes)')
  ).not.toBeInTheDocument()
})

const ipProvider = {
  id: 'settings-fixture',
  name: 'IP lookup',
  type: 'custom_ipapi',
  endpoint: 'https://provider.example',
  enabled: true,
  priority: 100,
  api_key: '',
  daily_limit: 1000,
  capabilities: ['geo', 'vpn', 'proxy', 'tor', 'abuse'],
  allowed_private_hosts: ['first.example'],
}

it('preserves unfinished host and limit drafts across tabs and binds normalized values to proof', async () => {
  vi.mocked(getMonitorSettings).mockResolvedValue({
    ip_intelligence_providers_json: JSON.stringify([ipProvider]),
  })
  renderDrawer()
  const user = userEvent.setup()
  await user.click(await screen.findByRole('tab', { name: 'IP lookup' }))
  await user.click(screen.getAllByRole('button', { name: 'More settings' })[0])
  await user.type(
    screen.getByLabelText('Allowed private network hosts'),
    ', second.example'
  )
  expect(screen.getByLabelText('Allowed private network hosts')).toHaveValue(
    'first.example, second.example'
  )
  await user.clear(screen.getByLabelText('Daily lookup limit'))
  expect(screen.getByLabelText('Daily lookup limit')).toHaveValue(null)
  await user.click(screen.getByRole('button', { name: 'Save' }))
  expect(checkVerificationMethods).not.toHaveBeenCalled()
  await user.click(screen.getByRole('tab', { name: 'Monitoring' }))
  await user.click(screen.getByRole('tab', { name: 'IP lookup' }))
  expect(screen.getByLabelText('Daily lookup limit')).toHaveValue(null)
  await user.type(screen.getByLabelText('Daily lookup limit'), '500')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  const dialog = await screen.findByRole('dialog', {
    name: 'Security verification',
  })
  await user.type(
    await within(dialog).findByLabelText('Password', { selector: 'input' }),
    'fixture-password'
  )
  await user.click(within(dialog).getByRole('button', { name: 'Verify' }))
  await waitFor(() => expect(saveMonitorSettings).toHaveBeenCalledOnce())
  const [values] = vi.mocked(saveMonitorSettings).mock.calls[0]
  expect(JSON.parse(values.ip_intelligence_providers_json)).toEqual([
    {
      ...ipProvider,
      daily_limit: 500,
      allowed_private_hosts: ['first.example', 'second.example'],
    },
  ])
  expect(Object.keys(values).some((key) => key.startsWith('_'))).toBe(false)
  expect(verify).toHaveBeenCalledWith(
    expect.anything(),
    expect.objectContaining({ context: { values } }),
    false,
    expect.anything(),
    expect.any(Function)
  )
})

it.each([
  'http://provider.example',
  'https://sample:sample@provider.example',
  'https://@provider.example',
  'https://provider.example/?q=sample',
  'https://provider.example/#sample',
])(
  'rejects unsupported IP service URL %s before verification',
  async (endpoint) => {
    vi.mocked(getMonitorSettings).mockResolvedValue({
      ip_intelligence_providers_json: JSON.stringify([
        { ...ipProvider, endpoint },
      ]),
    })
    renderDrawer()
    await userEvent.click(await screen.findByRole('tab', { name: 'IP lookup' }))
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(checkVerificationMethods).not.toHaveBeenCalled()
    expect(saveMonitorSettings).not.toHaveBeenCalled()
    expect(
      screen.getByText(
        'Use an HTTPS URL without credentials, query parameters or a fragment.'
      )
    ).toBeVisible()
  }
)

it.each(['true', 'false'])(
  'maps stored root exclusion %s to inclusion and preserves it through saving and reopening',
  async (saved) => {
    vi.mocked(getMonitorSettings).mockResolvedValue({
      auto_ban_exclude_root: saved,
      auto_ban_include_staff: 'false',
    })
    const onClose = renderDrawer()
    await userEvent.click(
      await screen.findByRole('tab', { name: 'Automatic response' })
    )
    const includeRoot = screen.getByRole('switch', {
      name: 'Include super administrators',
    })
    expect(includeRoot).toHaveAttribute(
      'aria-checked',
      String(saved === 'false')
    )
    await userEvent.click(includeRoot)
    expect(includeRoot).toHaveAttribute('aria-checked', saved)
    expect(
      screen.getByRole('switch', {
        name: 'Include administrators and employees',
      })
    ).not.toBeChecked()
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    const dialog = await screen.findByRole('dialog', {
      name: 'Security verification',
    })
    await userEvent.type(
      await within(dialog).findByLabelText('Password', { selector: 'input' }),
      'fixture-password'
    )
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Verify' })
    )
    const expectedValues = expect.objectContaining({
      auto_ban_exclude_root: String(saved !== 'true'),
      auto_ban_include_staff: 'false',
    })
    await waitFor(() =>
      expect(saveMonitorSettings).toHaveBeenCalledWith(
        expectedValues,
        'test-proof'
      )
    )
    expect(verify).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ context: { values: expectedValues } }),
      false,
      expect.anything(),
      expect.any(Function)
    )
    await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
    const [values] = vi.mocked(saveMonitorSettings).mock.calls[0]
    cleanup()
    vi.mocked(getMonitorSettings).mockResolvedValue(values)
    renderDrawer()
    await userEvent.click(
      await screen.findByRole('tab', { name: 'Automatic response' })
    )
    expect(
      screen.getByRole('switch', { name: 'Include super administrators' })
    ).toHaveAttribute('aria-checked', saved)
  }
)

it('reveals invalid rule settings from another tab before requesting verification', async () => {
  vi.mocked(getMonitorSettings).mockResolvedValue({
    auto_ban_rules_json: JSON.stringify(
      defaultAutoBanRules.map((rule) =>
        rule.id === 'multi_account_key_access'
          ? { ...rule, login_accounts: 5, key_accounts: 6 }
          : rule
      )
    ),
  })
  renderDrawer()
  await screen.findByRole('switch', { name: 'Enable monitoring' })
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(
      screen.getByRole('tab', { name: 'Automatic response' })
    ).toHaveAttribute('aria-selected', 'true')
  )
  expect(screen.getByRole('alert')).toBeVisible()
  expect(
    await screen.findByLabelText('Minimum accounts viewing keys')
  ).toHaveValue(6)
  expect(checkVerificationMethods).not.toHaveBeenCalled()
  expect(saveMonitorSettings).not.toHaveBeenCalled()
})

it('preserves account exclusions across tab changes and validates them before saving', async () => {
  renderDrawer()
  await userEvent.click(
    await screen.findByRole('tab', { name: 'Automatic response' })
  )
  fireEvent.change(screen.getByLabelText('Excluded account IDs'), {
    target: { value: '12, -3' },
  })
  await userEvent.click(screen.getByRole('tab', { name: 'Notifications' }))
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(
      screen.getByRole('tab', { name: 'Automatic response' })
    ).toHaveAttribute('aria-selected', 'true')
  )
  expect(screen.getByLabelText('Excluded account IDs')).toHaveValue('12, -3')
  expect(screen.getByRole('alert')).toHaveTextContent(
    'Account IDs must be positive integers separated by commas.'
  )
  expect(checkVerificationMethods).not.toHaveBeenCalled()
})

it('requires capture for session replay and saves the dependency with the proof', async () => {
  renderDrawer()
  await userEvent.click(
    await screen.findByRole('tab', { name: 'Automatic response' })
  )
  await userEvent.click(
    screen.getByRole('switch', { name: 'Automatic response' })
  )
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  expect(checkVerificationMethods).not.toHaveBeenCalled()
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Enable sign-in security event capture to use the session replay rule.'
  )
  await userEvent.click(
    screen.getByRole('switch', { name: 'Capture sign-in security events' })
  )
  await waitFor(() =>
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  )
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  const dialog = await screen.findByRole('dialog', {
    name: 'Security verification',
  })
  await userEvent.type(
    await within(dialog).findByLabelText('Password', { selector: 'input' }),
    'fixture-password'
  )
  await userEvent.click(within(dialog).getByRole('button', { name: 'Verify' }))
  await waitFor(() =>
    expect(saveMonitorSettings).toHaveBeenCalledWith(
      expect.objectContaining({
        auto_ban_enabled: 'true',
        capture_enabled: 'true',
      }),
      'test-proof'
    )
  )
})

it('saves cleanup without banning and preserves its selections across tabs', async () => {
  renderDrawer()
  await userEvent.click(
    await screen.findByRole('tab', { name: 'Automatic response' })
  )
  for (const checkbox of screen.getAllByRole('checkbox', {
    name: 'Clear API keys',
  })) {
    expect(checkbox).not.toBeChecked()
  }
  for (const checkbox of screen.getAllByRole('checkbox', {
    name: 'Clear access tokens',
  })) {
    expect(checkbox).not.toBeChecked()
  }
  for (const checkbox of screen.getAllByRole('checkbox', {
    name: 'Ban account',
  })) {
    if (checkbox.getAttribute('aria-checked') === 'true') {
      await userEvent.click(checkbox)
    }
  }
  await userEvent.click(
    screen.getAllByRole('checkbox', { name: 'Clear API keys' })[0]
  )
  await userEvent.click(
    screen.getAllByRole('checkbox', { name: 'Clear access tokens' })[0]
  )
  await userEvent.click(
    screen.getByRole('switch', { name: 'Automatic response' })
  )
  await userEvent.click(screen.getByRole('tab', { name: 'Notifications' }))
  await userEvent.click(screen.getByRole('tab', { name: 'Automatic response' }))
  expect(
    screen.getAllByRole('checkbox', { name: 'Clear API keys' })[0]
  ).toBeChecked()
  expect(
    screen.getAllByRole('checkbox', { name: 'Clear access tokens' })[0]
  ).toBeChecked()
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  const dialog = await screen.findByRole('dialog', {
    name: 'Security verification',
  })
  await userEvent.type(
    await within(dialog).findByLabelText('Password', { selector: 'input' }),
    'fixture-password'
  )
  await userEvent.click(within(dialog).getByRole('button', { name: 'Verify' }))
  await waitFor(() => expect(saveMonitorSettings).toHaveBeenCalledOnce())
  const [values] = vi.mocked(saveMonitorSettings).mock.calls[0]
  const rules = JSON.parse(values.auto_ban_rules_json)
  expect(rules[0]).toMatchObject({
    selected: false,
    clear_api_keys: true,
    clear_access_tokens: true,
  })
  expect(
    rules
      .slice(1)
      .every(
        (rule: Record<string, unknown>) =>
          !rule.selected && !rule.clear_api_keys && !rule.clear_access_tokens
      )
  ).toBe(true)
  expect(values.capture_enabled).toBe('false')
})

it('reloads cleanup-only settings without enabling a ban', async () => {
  vi.mocked(getMonitorSettings).mockResolvedValue({
    auto_ban_rules_json: JSON.stringify(
      defaultAutoBanRules.map((rule) => ({
        ...rule,
        selected: false,
        clear_api_keys: true,
        clear_access_tokens: false,
      }))
    ),
  })
  renderDrawer()
  await userEvent.click(
    await screen.findByRole('tab', { name: 'Automatic response' })
  )
  for (const checkbox of screen.getAllByRole('checkbox', {
    name: 'Ban account',
  })) {
    expect(checkbox).not.toBeChecked()
  }
  for (const checkbox of screen.getAllByRole('checkbox', {
    name: 'Clear API keys',
  })) {
    expect(checkbox).toBeChecked()
  }
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(saveMonitorSettings).not.toHaveBeenCalled()
})

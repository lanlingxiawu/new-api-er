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
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { toast } from 'sonner'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { createServerError } from '@/lib/server-error-message'

import { AccountResponseSection } from './account-response-section'
import {
  banSecurityAccount,
  stopSecurityResponse,
  type SecurityDetail,
} from './api'

const fixture = vi.hoisted(() => ({
  user: { id: 1, role: 100 },
  verify: vi.fn(),
}))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))
vi.mock('@/stores/auth-store', () => ({
  useAuthStore: (select: (value: unknown) => unknown) =>
    select({ auth: { user: fixture.user } }),
}))
vi.mock('./api', () => ({
  banSecurityAccount: vi.fn(),
  stopSecurityResponse: vi.fn(),
}))
vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('@/features/auth/secure-verification', () => ({
  useSecureVerification: () => ({
    requestVerification: fixture.verify,
    dialogProps: {},
  }),
  SecureVerificationDialog: () => null,
}))

const data: SecurityDetail = {
  alert: {
    id: 7,
    actor_id: 2,
    actor_role: 1,
    rule: 'multi_source_key_access',
    severity: 3,
    status: 'open',
    resource_type: 'account',
    first_seen: 1,
    last_seen: 2,
    occurrences: 1,
    matched_count: 3,
    version: 4,
  },
  actions: [],
  events: [],
  deliveries: [],
  findings: [],
}
beforeEach(() => {
  fixture.user = { id: 1, role: 100 }
  fixture.verify.mockResolvedValue({ proof_token: 'fixture-proof' })
})
afterEach(() => {
  cleanup()
  vi.resetAllMocks()
})

function renderResponse(detail = data) {
  const refresh = vi.fn().mockResolvedValue(undefined)
  const client = new QueryClient({
    defaultOptions: { mutations: { retry: false } },
  })
  const rendered = render(
    <QueryClientProvider client={client}>
      <AccountResponseSection data={detail} stale={false} onRefresh={refresh} />
    </QueryClientProvider>
  )
  return Object.assign(refresh, { rendered, client })
}

it('explains that a super administrator was excluded by the automatic policy', () => {
  renderResponse({
    ...data,
    alert: {
      ...data.alert,
      actor_role: 100,
      response: {
        id: 20,
        action: 'disable_account',
        source: 'automatic',
        outcome: 'skipped',
        reason_code: 'protected_account',
        target_user_id: data.alert.actor_id,
        created_at: 3,
      },
    },
  })
  expect(
    screen.getByText(
      'Super administrators are excluded from automatic response.'
    )
  ).toBeInTheDocument()
  expect(
    screen.queryByText('Super administrators cannot be banned.')
  ).not.toBeInTheDocument()
})

it('shows cleanup separately and still allows a manual ban', () => {
  renderResponse({
    ...data,
    alert: {
      ...data.alert,
      responses: [
        {
          id: 20,
          action: 'clear_api_keys',
          source: 'automatic',
          outcome: 'succeeded',
          reason_code: '',
          target_user_id: 2,
          created_at: 10,
          affected_count: 4,
        },
      ],
    },
  })
  expect(screen.getByText('Automatic API key cleanup')).toBeVisible()
  expect(screen.getByText('API keys cleared: {{count}}')).toBeVisible()
  expect(screen.queryByText('Account banned.')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Ban account' })).toBeEnabled()
})

it('does not submit when confirmation or verification is cancelled', async () => {
  renderResponse()
  await userEvent.click(screen.getByRole('button', { name: 'Ban account' }))
  const dialog = await screen.findByRole('alertdialog', {
    name: 'Ban this account?',
  })
  await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
  expect(fixture.verify).not.toHaveBeenCalled()
  fixture.verify.mockResolvedValueOnce(null)
  await userEvent.click(screen.getByRole('button', { name: 'Ban account' }))
  await userEvent.click(screen.getByRole('button', { name: 'Confirm ban' }))
  await waitFor(() => expect(fixture.verify).toHaveBeenCalledOnce())
  expect(banSecurityAccount).not.toHaveBeenCalled()
  expect(screen.getByRole('button', { name: 'Ban account' })).toBeEnabled()
})

it('binds proof to the target and refreshes the returned result', async () => {
  vi.mocked(banSecurityAccount).mockResolvedValue({
    id: 9,
    source: 'human',
    outcome: 'succeeded',
    reason_code: '',
    target_user_id: 2,
    created_at: 10,
  })
  const refresh = renderResponse()
  await userEvent.click(screen.getByRole('button', { name: 'Ban account' }))
  await userEvent.click(screen.getByRole('button', { name: 'Confirm ban' }))
  await waitFor(() => expect(banSecurityAccount).toHaveBeenCalledOnce())
  expect(fixture.verify).toHaveBeenCalledWith({
    scope: 'admin.user.manage',
    context: { user_id: 2, action: 'disable' },
  })
  expect(banSecurityAccount).toHaveBeenCalledWith(
    7,
    4,
    expect.stringMatching(/^[\da-f-]{36}$/),
    'fixture-proof'
  )
  await waitFor(() => expect(refresh).toHaveBeenCalledOnce())
})

it('keeps an ambiguous request disabled and never retries it automatically', async () => {
  vi.mocked(banSecurityAccount).mockRejectedValue(
    new Error('fixture transport outage')
  )
  const refresh = renderResponse()
  await userEvent.click(screen.getByRole('button', { name: 'Ban account' }))
  await userEvent.click(screen.getByRole('button', { name: 'Confirm ban' }))
  await waitFor(() => expect(refresh).toHaveBeenCalledOnce())
  expect(screen.getByRole('alert')).toHaveTextContent(
    'The result is not confirmed. Refresh first.'
  )
  expect(screen.getByRole('button', { name: 'Ban account' })).toBeDisabled()
  expect(banSecurityAccount).toHaveBeenCalledOnce()
})

it.each([1, 10])(
  'does not expose account banning to role %s without both permissions',
  (role) => {
    fixture.user = { id: 1, role }
    renderResponse()
    expect(
      screen.queryByRole('button', { name: 'Ban account' })
    ).not.toBeInTheDocument()
  }
)

it('never offers a ban for a super administrator', () => {
  renderResponse({ ...data, alert: { ...data.alert, actor_role: 100 } })
  expect(
    screen.queryByRole('button', { name: 'Ban account' })
  ).not.toBeInTheDocument()
})

it('binds stopping to the response group, current alert version and a fresh proof', async () => {
  vi.mocked(stopSecurityResponse).mockResolvedValue({
    id: 21,
    action: 'stop_automatic_response',
    source: 'human',
    outcome: 'succeeded',
    reason_code: 'stopped',
    target_user_id: 2,
    created_at: 10,
  })
  const refresh = renderResponse({
    ...data,
    alert: { ...data.alert, can_stop: true, response_group_id: 12 },
  })
  await userEvent.click(
    screen.getByRole('button', { name: 'Stop this response' })
  )
  const dialog = await screen.findByRole('alertdialog', {
    name: 'Stop this response',
  })
  await userEvent.click(
    within(dialog).getByRole('button', { name: 'Stop this response' })
  )
  await waitFor(() => expect(stopSecurityResponse).toHaveBeenCalledOnce())
  expect(fixture.verify).toHaveBeenCalledWith({
    scope: 'audit.security.response.stop',
    context: {
      alert_id: 7,
      version: 4,
      response_group_id: 12,
      action: 'stop_automatic_response',
    },
  })
  expect(stopSecurityResponse).toHaveBeenCalledWith(
    7,
    4,
    12,
    expect.any(String),
    'fixture-proof'
  )
  expect(banSecurityAccount).not.toHaveBeenCalled()
  await waitFor(() => expect(refresh).toHaveBeenCalledOnce())
})

it('does not expose stopping to ordinary investigators', () => {
  fixture.user = { id: 4, role: 10 }
  renderResponse({
    ...data,
    alert: { ...data.alert, can_stop: true, response_group_id: 12 },
  })
  expect(
    screen.queryByRole('button', { name: 'Stop this response' })
  ).not.toBeInTheDocument()
})

it.each([
  { success: false, code: 'AUDIT_SECURITY_RESPONSE_REJECTED' },
  { success: false, code: 'SECURITY_PROOF_EXPIRED' },
  { response: { status: 403 } },
])(
  'preserves a confirmed rejection and allows a newly verified retry: %j',
  async (rejection) => {
    const message =
      'code' in rejection && rejection.code === 'SECURITY_PROOF_EXPIRED'
        ? 'Security verification has expired. Please verify again.'
        : 'The alert has changed. Refresh and try again.'
    vi.mocked(banSecurityAccount).mockRejectedValue(
      createServerError({ ...rejection, message })
    )
    const refresh = renderResponse()
    await userEvent.click(screen.getByRole('button', { name: 'Ban account' }))
    await userEvent.click(screen.getByRole('button', { name: 'Confirm ban' }))
    await waitFor(() => expect(refresh).toHaveBeenCalledOnce())
    expect(toast.error).toHaveBeenCalledWith(message)
    refresh.rendered.rerender(
      <QueryClientProvider client={refresh.client}>
        <AccountResponseSection
          data={{ ...data, alert: { ...data.alert, version: 5 } }}
          stale={false}
          onRefresh={refresh}
        />
      </QueryClientProvider>
    )
    expect(screen.getByRole('button', { name: 'Ban account' })).toBeEnabled()
    fixture.verify.mockResolvedValueOnce(null)
    await userEvent.click(screen.getByRole('button', { name: 'Ban account' }))
    await userEvent.click(screen.getByRole('button', { name: 'Confirm ban' }))
    await waitFor(() => expect(fixture.verify).toHaveBeenCalledTimes(2))
    expect(banSecurityAccount).toHaveBeenCalledOnce()
  }
)

it('keeps an unclassified server failure uncertain even after refreshing', async () => {
  vi.mocked(banSecurityAccount).mockRejectedValue(
    createServerError({ success: false, message: 'Service unavailable' })
  )
  const refresh = renderResponse()
  await userEvent.click(screen.getByRole('button', { name: 'Ban account' }))
  await userEvent.click(screen.getByRole('button', { name: 'Confirm ban' }))
  await waitFor(() => expect(refresh).toHaveBeenCalledOnce())
  refresh.rendered.rerender(
    <QueryClientProvider client={refresh.client}>
      <AccountResponseSection
        data={{ ...data, alert: { ...data.alert, version: 5 } }}
        stale={false}
        onRefresh={refresh}
      />
    </QueryClientProvider>
  )
  expect(screen.getByRole('button', { name: 'Ban account' })).toBeDisabled()
  expect(banSecurityAccount).toHaveBeenCalledOnce()
})

it('allows retry when verification fails before the ban request', async () => {
  fixture.verify.mockRejectedValueOnce(
    new Error('Verification failed. Please try again.')
  )
  const refresh = renderResponse()
  await userEvent.click(screen.getByRole('button', { name: 'Ban account' }))
  await userEvent.click(screen.getByRole('button', { name: 'Confirm ban' }))
  await waitFor(() => expect(refresh).toHaveBeenCalledOnce())
  expect(banSecurityAccount).not.toHaveBeenCalled()
  expect(screen.getByRole('button', { name: 'Ban account' })).toBeEnabled()
})

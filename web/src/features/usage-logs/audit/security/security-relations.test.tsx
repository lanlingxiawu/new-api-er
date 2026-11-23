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
import { afterEach, expect, it, vi } from 'vitest'

import { securityGet } from './api'
import { IPRiskReasons } from './ip-risk-reasons'
import { SecurityRelations } from './security-relations'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))
vi.mock('@/stores/auth-store', () => ({
  useAuthStore: (selector: (state: unknown) => unknown) =>
    selector({ auth: { user: { id: 1, role: 100, permissions: [] } } }),
}))
vi.mock('../components/audit-ip-cell', () => ({
  AuditIPAddress: ({ ip }: { ip: string }) => <span>{ip}</span>,
}))
vi.mock('./api', () => ({ securityGet: vi.fn() }))
afterEach(() => {
  cleanup()
  vi.resetAllMocks()
})

it('uses relation event fields and keeps the query window across cursor pages', async () => {
  const user = userEvent.setup()
  const select = vi.fn()
  vi.mocked(securityGet).mockResolvedValue({
    items: [
      {
        alert_id: 17,
        finding_id: 31,
        event_id: 'event-31',
        rule: 'login_failures',
        at: 1730000000,
        actor_id: 2,
        target_id: 0,
        ip: '8.8.8.8',
        request_id: 'req-31',
      },
    ],
    next_cursor: 'relation-next',
    coverage: { complete: false, reason: 'history_limited' },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SecurityRelations id={7} anchor={1730000000} onSelect={select} />
    </QueryClientProvider>
  )
  expect(securityGet).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Account' }))
  await user.click(
    await screen.findByRole('button', { name: 'Repeated sign-in failures #17' })
  )
  expect(select).toHaveBeenCalledWith(17)
  expect(screen.getByText('8.8.8.8')).toBeVisible()
  expect(screen.getByText('Related event history is incomplete.')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Next page' }))
  await waitFor(() =>
    expect(securityGet).toHaveBeenLastCalledWith(
      'alerts/7/relations',
      expect.any(AbortSignal),
      expect.objectContaining({ cursor: 'relation-next' })
    )
  )
  const calls = vi.mocked(securityGet).mock.calls
  expect(calls.at(-1)?.[2]?.start).toBe(1730000000 - 15 * 86400)
  expect(calls.at(-1)?.[2]?.end).toBe(calls[0]?.[2]?.end)
  client.clear()
})

it('loads all IP reason pages using the exact event and cursor', async () => {
  const user = userEvent.setup()
  const select = vi.fn()
  vi.mocked(securityGet).mockResolvedValue({
    items: [
      {
        rule: 'anonymous_network',
        alert_id: 29,
        status: 'open',
        at: 1730000000,
      },
    ],
    next_cursor: 29,
    revision: 1,
    coverage: { historical_complete: false, backfill_done: false, health: {} },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <IPRiskReasons eventId='event-ip' onSelect={select} />
    </QueryClientProvider>
  )
  await user.click(
    await screen.findByRole('button', {
      name: 'Anonymous network activity #29',
    })
  )
  expect(select).toHaveBeenCalledWith(29)
  await user.click(screen.getByRole('button', { name: 'Next page' }))
  await waitFor(() =>
    expect(securityGet).toHaveBeenLastCalledWith(
      'ip-reasons',
      expect.any(AbortSignal),
      { event_id: 'event-ip', after_id: 29, limit: 20 }
    )
  )
  client.clear()
})

it('groups rules for one recorded event while preserving distinct and unidentified events', async () => {
  const user = userEvent.setup()
  const select = vi.fn()
  const event = {
    event_id: 'shared-event',
    at: 1730000000,
    actor_id: 2,
    actor_role: 1,
    target_id: 0,
    ip: '8.8.8.8',
    request_id: 'same-request',
    attempted_account: 'submitted@example.test',
  }
  vi.mocked(securityGet).mockResolvedValue({
    items: [
      { ...event, alert_id: 17, finding_id: 31, rule: 'ip_abuse' },
      { ...event, alert_id: 18, finding_id: 32, rule: 'recovery_requests' },
      { ...event, alert_id: 19, finding_id: 33, rule: 'ip_abuse', actor_id: 3 },
      {
        ...event,
        alert_id: 20,
        finding_id: 34,
        rule: 'ip_abuse',
        event_id: '',
      },
      {
        ...event,
        alert_id: 21,
        finding_id: 35,
        rule: 'ip_abuse',
        event_id: '',
      },
    ],
    coverage: { complete: false },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SecurityRelations id={7} anchor={1730000000} onSelect={select} />
    </QueryClientProvider>
  )
  await user.click(screen.getByRole('button', { name: 'Request ID' }))
  const groups = await screen.findAllByRole('group', { name: 'Related alerts' })
  expect(groups).toHaveLength(4)
  expect(within(groups[0]).getAllByRole('button')).toHaveLength(2)
  expect(within(groups[0]).getAllByText('8.8.8.8')).toHaveLength(1)
  expect(groups[0]).toHaveTextContent('Submitted account')
  expect(groups[0]).not.toHaveTextContent('Attempted sign-in account')
  await user.click(within(groups[0]).getByRole('button', { name: /#18$/ }))
  expect(select).toHaveBeenCalledWith(18)
  expect(
    screen.getByText('Alerts from the same event are grouped within each page.')
  ).toBeVisible()
  client.clear()
})

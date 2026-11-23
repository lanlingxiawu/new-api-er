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
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, expect, it, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'

import type { SecurityEvent } from './api'
import { SecurityEventCard } from './security-event-card'

vi.mock('../components/audit-ip-cell', () => ({
  AuditIPCell: ({ entry }: { entry: { ip: string } }) => (
    <span>{entry.ip}</span>
  ),
}))

afterEach(cleanup)

it('labels an attempted account as evidence without changing the actor', async () => {
  const i18n = createInstance()
  await i18n.init({ lng: 'zh', resources: { zh } })
  render(
    <I18nextProvider i18n={i18n}>
      <SecurityEventCard
        event={{
          ...event,
          actor_id: 0,
          action: 'auth.login_failed',
          target_id: 31,
          target_role: 1,
          target_username: 'target-customer',
          success: false,
          facts: JSON.stringify({
            attempted_account: '<img src=x onerror=alert(1)>',
          }),
        }}
      />
    </I18nextProvider>
  )
  expect(screen.getByText('尝试登录账号')).toBeVisible()
  expect(screen.getByText('<img src=x onerror=alert(1)>')).toBeVisible()
  expect(screen.getByText('未识别账号')).toBeVisible()
  expect(screen.getByText('目标账户')).toBeVisible()
  expect(screen.getByText('target-customer (#31)')).toBeVisible()
  expect(document.querySelector('img')).toBeNull()
})

it('labels a password recovery address without treating it as the operator', async () => {
  const i18n = createInstance()
  await i18n.init({ lng: 'zh', resources: { zh } })
  render(
    <I18nextProvider i18n={i18n}>
      <SecurityEventCard
        event={{
          ...event,
          actor_id: 0,
          action: 'auth.recovery_requested',
          success: true,
          facts: JSON.stringify({
            attempted_account: 'recovery@example.com',
          }),
        }}
      />
    </I18nextProvider>
  )
  expect(screen.getByText('找回账号')).toBeVisible()
  expect(screen.getByText('recovery@example.com')).toBeVisible()
  expect(screen.getByText('未识别账号')).toBeVisible()
  expect(screen.getByText('已接收')).toBeVisible()
  expect(screen.queryByText('成功')).toBeNull()
  expect(screen.queryByText('尝试登录账号')).toBeNull()
})

it.each([
  { actor_id: 31, actor_username: 'alice', expected: 'alice (#31)' },
  { actor_id: 31, actor_username: undefined, expected: '#31' },
  {
    actor_id: 0,
    actor_username: 'unverified',
    expected: 'Unidentified account',
  },
])(
  'shows the actor name with a stable ID or the identity fallback: $expected',
  async (actor) => {
    const i18n = createInstance()
    await i18n.init({ lng: 'en', resources: { en } })
    render(
      <I18nextProvider i18n={i18n}>
        <SecurityEventCard event={{ ...event, ...actor }} />
      </I18nextProvider>
    )
    expect(screen.getByText(actor.expected)).toBeVisible()
    expect(screen.queryByText('unverified')).not.toBeInTheDocument()
  }
)

it('shows result, role, resource, zero impact and related-audit navigation', async () => {
  const i18n = createInstance()
  await i18n.init({ lng: 'en', resources: { en } })
  const onInvestigate = vi.fn()
  render(
    <I18nextProvider i18n={i18n}>
      <SecurityEventCard
        event={{
          ...event,
          actor_role: 10,
          resource_type: 'channel',
          success: false,
          affected_count: 0,
        }}
        onInvestigate={onInvestigate}
      />
    </I18nextProvider>
  )
  expect(screen.getByText('Failed')).toBeVisible()
  expect(screen.getByText('Administrator')).toBeVisible()
  expect(screen.getByText('Channels')).toBeVisible()
  expect(screen.getByText('0')).toBeVisible()
  screen.getByRole('button', { name: 'Related audit records' }).click()
  expect(onInvestigate).toHaveBeenCalledWith(
    expect.objectContaining({ request_id: 'request-test', at: 100 })
  )
})

const event: SecurityEvent = {
  event_id: 'test',
  action: 'channel.create',
  actor_id: 1,
  target_id: 0,
  ip: '127.0.0.1',
  request_id: 'request-test',
  at: 100,
  success: true,
  affected_count: 1,
  facts: '{"count":1}',
}

it('renders operation and evidence labels in the selected language', async () => {
  const i18n = createInstance()
  await i18n.init({
    lng: 'zh',
    resources: { zh, en },
    interpolation: { escapeValue: false },
  })
  const view = render(
    <I18nextProvider i18n={i18n}>
      <SecurityEventCard event={event} />
    </I18nextProvider>
  )
  expect(screen.getByText('创建渠道')).toBeVisible()
  expect(screen.getByText('数量')).toBeVisible()
  expect(screen.getByText('数量').parentElement).toHaveTextContent('1')
  expect(screen.queryByText('channel.create')).not.toBeInTheDocument()
  expect(screen.queryByText('{"count":1}')).not.toBeInTheDocument()
  await i18n.changeLanguage('en')
  view.rerender(
    <I18nextProvider i18n={i18n}>
      <SecurityEventCard event={event} />
    </I18nextProvider>
  )
  expect(screen.getByText('Create Channel')).toBeVisible()
  expect(screen.getByText('Count')).toBeVisible()
})

it('translates field names and reasons while preserving false and zero evidence', async () => {
  const i18n = createInstance()
  await i18n.init({
    lng: 'zh',
    resources: { zh },
    interpolation: { escapeValue: false },
  })
  render(
    <I18nextProvider i18n={i18n}>
      <SecurityEventCard
        event={{
          ...event,
          action: 'auth.login_failed',
          facts:
            '{"changed_fields":["key","base_url"],"reason":"invalid_credentials","changed":false,"count":0}',
        }}
      />
    </I18nextProvider>
  )
  expect(screen.getByText('登录失败')).toBeVisible()
  expect(screen.getByText('凭据无效')).toBeVisible()
  expect(
    screen.getByText(`${zh.translation.Key}, ${zh.translation['Base URL']}`)
  ).toBeVisible()
  expect(screen.getByText('否')).toBeVisible()
  expect(screen.getByText('0')).toBeVisible()
})

it('retains unknown operations and malformed evidence for investigation', async () => {
  const i18n = createInstance()
  await i18n.init({ lng: 'zh', resources: { zh } })
  render(
    <I18nextProvider i18n={i18n}>
      <SecurityEventCard
        event={{ ...event, action: 'future.operation', facts: '{incomplete' }}
      />
    </I18nextProvider>
  )
  expect(screen.getByText('future.operation')).toBeVisible()
  expect(screen.getByText('{incomplete')).toBeVisible()
})

it('shows localized dataset evidence and unknown VPN without inventing a clean verdict', async () => {
  const i18n = createInstance()
  await i18n.init({ lng: 'zh', resources: { zh } })
  render(
    <I18nextProvider i18n={i18n}>
      <SecurityEventCard
        event={{
          ...event,
          action: 'login',
          facts: JSON.stringify({
            ip_intelligence: {
              asn_number: 15169,
              asn_organization: 'Example network',
              is_vpn: null,
              evidence: [
                {
                  source: 'drop_v4',
                  capability: 'abuse',
                  matched_network: '8.8.8.0/24',
                  reference: 'SBL123',
                  data_updated_at: 1794801404,
                },
              ],
            },
          }),
        }}
      />
    </I18nextProvider>
  )
  expect(screen.getByText(/8\.8\.8\.0\/24/)).toBeVisible()
  expect(screen.getByText(/SBL123/)).toBeVisible()
  expect(screen.getByText(/Spamhaus DROP IPv4/)).toBeVisible()
  expect(screen.queryByText('数据更新时间')).not.toBeInTheDocument()
  expect(screen.queryByText('1794801404')).not.toBeInTheDocument()
  expect(
    screen.getByText(new RegExp(`${zh.translation.Unknown}:`))
  ).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: '技术详情' }))
  expect(screen.getByText('数据更新时间')).toBeVisible()
  expect(screen.getByText('命中网段')).toBeVisible()
})

it('prioritizes location and signals while keeping source IDs and coordinates behind details', async () => {
  const i18n = createInstance()
  await i18n.init({ lng: 'en', resources: { en } })
  render(
    <I18nextProvider i18n={i18n}>
      <SecurityEventCard
        event={{
          ...event,
          facts: JSON.stringify({
            client_ref: 'client-fingerprint',
            ip_intelligence: {
              providers: ['provider-uuid'],
              field_providers: { geo: 'provider-uuid', tor: 'provider-uuid' },
              country: 'United States',
              country_code: 'US',
              state: 'Arizona',
              city: 'Phoenix',
              latitude: 0,
              longitude: -112,
              asn_number: 53755,
              asn_organization: 'Example network',
              is_vpn: true,
              is_proxy: false,
              is_tor: false,
              is_abuser: true,
            },
          }),
        }}
      />
    </I18nextProvider>
  )
  expect(screen.getByText('United States · Arizona · Phoenix')).toBeVisible()
  expect(screen.getByText('AS53755 · Example network')).toBeVisible()
  expect(screen.getByText('VPN')).toHaveClass('text-info')
  expect(screen.getByText('Abuse intelligence')).toHaveClass('text-destructive')
  expect(screen.queryByText('provider-uuid')).not.toBeInTheDocument()
  expect(screen.queryByText('client-fingerprint')).not.toBeInTheDocument()
  expect(screen.queryByText('request-test')).not.toBeInTheDocument()
  expect(screen.queryByText('Latitude')).not.toBeInTheDocument()
  await userEvent.click(
    screen.getByRole('button', { name: 'Technical details' })
  )
  expect(screen.getByText('client-fingerprint')).toBeVisible()
  expect(screen.getByText('request-test')).toBeVisible()
  expect(screen.getAllByText('provider-uuid')).toHaveLength(3)
  expect(screen.getByText('Latitude').parentElement).toHaveTextContent('0')
  expect(screen.getByText('Proxy').parentElement).toHaveTextContent('No')
})

it('groups missing and null IP flags as unknown without a clean verdict', async () => {
  const i18n = createInstance()
  await i18n.init({ lng: 'en', resources: { en } })
  render(
    <I18nextProvider i18n={i18n}>
      <SecurityEventCard
        event={{
          ...event,
          facts: JSON.stringify({
            ip_intelligence: { is_vpn: null, is_tor: false },
          }),
        }}
      />
    </I18nextProvider>
  )
  expect(
    screen.getByText('Unknown: VPN, Proxy, Abuse intelligence')
  ).toBeVisible()
  expect(screen.queryByText('None')).not.toBeInTheDocument()
  expect(screen.queryByText('Safe')).not.toBeInTheDocument()
})

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import type { IPRiskMarker } from '../api'
import type { SecurityEvent } from './api'
import { SecurityEventCard } from './security-event-card'
import { SecurityEvidence } from './security-evidence'

const event: SecurityEvent = {
  event_id: 'failed-login',
  action: 'auth.login_failed',
  actor_id: 0,
  target_id: 0,
  ip: '204.1.132.33',
  request_id: 'request-21',
  at: 1795251306,
  success: false,
  affected_count: 1,
  facts: JSON.stringify({
    attempted_account: 'root',
    reason: 'invalid_credentials',
  }),
}
const marker: IPRiskMarker = {
  event_id: event.event_id,
  ip: event.ip,
  state: 'active',
  level: 'attention',
  reasons: [
    { rule: 'login_failures', alert_id: 21, status: 'open', at: event.at },
  ],
  more: false,
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

it('marks a failed-login IP and opens its reason in the current alert view', async () => {
  const fetch = vi.fn()
  vi.stubGlobal('fetch', fetch)
  const onSelectAlert = vi.fn()
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SecurityEventCard
        event={event}
        ipMarker={marker}
        onSelectAlert={onSelectAlert}
      />
    </QueryClientProvider>
  )
  expect(screen.getByText('Needs attention').parentElement).toHaveClass(
    'text-warning'
  )
  expect(screen.getByText('IP intelligence').parentElement).toHaveTextContent(
    'Not recorded'
  )
  expect(screen.getByText('Unidentified account')).toBeVisible()
  expect(screen.getByText('root')).toBeVisible()
  expect(screen.queryByText('High risk')).not.toBeInTheDocument()
  expect(screen.queryByText('Safe')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Needs attention' }))
  await userEvent.click(
    screen.getByRole('button', { name: 'Repeated login failures #21' })
  )
  expect(onSelectAlert).toHaveBeenCalledWith(21)
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(fetch).not.toHaveBeenCalled()
  client.clear()
})

it.each([
  {
    state: 'active',
    level: 'high',
    label: 'High risk',
    color: 'text-destructive',
  },
  {
    state: 'resolved',
    level: '',
    label: 'Resolved',
    color: 'text-muted-foreground',
  },
])(
  'uses the same $state IP marker in evidence rows and cards',
  ({ state, level, label, color }) => {
    const client = new QueryClient()
    render(
      <QueryClientProvider client={client}>
        <SecurityEvidence
          events={[event, { ...event, event_id: 'other-failed-login' }]}
          ipMarkers={[{ ...marker, state, level }]}
        />
      </QueryClientProvider>
    )
    expect(screen.getByRole('columnheader', { name: 'IP' })).toBeInTheDocument()
    expect(
      screen.queryByRole('columnheader', { name: 'Target ID' })
    ).not.toBeInTheDocument()
    expect(screen.getAllByText(label)).toHaveLength(3)
    for (const item of screen.getAllByText(label)) {
      expect(item.parentElement).toHaveClass(color)
    }
    client.clear()
  }
)

it('shows a single event once, without a second evidence table', () => {
  const client = new QueryClient()
  render(
    <QueryClientProvider client={client}>
      <SecurityEvidence events={[event]} ipMarkers={[marker]} />
    </QueryClientProvider>
  )
  expect(
    screen.queryByRole('columnheader', { name: 'IP' })
  ).not.toBeInTheDocument()
  expect(screen.getAllByText('204.1.132.33')).toHaveLength(1)
  expect(screen.getAllByText('Needs attention')).toHaveLength(1)
  client.clear()
})

it.each([{ event_id: 'another-event' }, { ip: '1.1.1.1' }, { state: 'none' }])(
  'does not apply a mismatched or inactive marker: %j',
  (override) => {
    const client = new QueryClient()
    render(
      <QueryClientProvider client={client}>
        <SecurityEventCard
          event={event}
          ipMarker={{ ...marker, ...override }}
        />
      </QueryClientProvider>
    )
    expect(screen.queryByText('Needs attention')).not.toBeInTheDocument()
    expect(screen.queryByText('Safe')).not.toBeInTheDocument()
    client.clear()
  }
)

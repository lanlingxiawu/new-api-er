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
import { fireEvent, render, screen, within } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import { queryUpstreamLog } from '../../api'
import type { UsageLog } from '../../data/schema'
import type { LogOtherData, UpstreamLogQueryData } from '../../types'
import { LogComparisonTable } from '../../upstream-log/log-comparison-table'
import { DetailsDialog } from '../dialogs/details-dialog'

vi.mock('../../api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api')>()),
  queryUpstreamLog: vi.fn(),
}))

function log(
  other: LogOtherData = {},
  overrides: Partial<UsageLog> = {}
): UsageLog {
  return {
    id: 1,
    user_id: 1,
    created_at: 1,
    type: 2,
    content: '',
    username: 'user',
    token_name: 'token',
    model_name: 'model',
    quota: 5000,
    prompt_tokens: 0,
    completion_tokens: 20,
    use_time: 0,
    is_stream: true,
    channel: 1,
    channel_name: 'upstream',
    token_id: 1,
    group: 'default',
    ip: '',
    other: JSON.stringify(other),
    request_id: 'local-request',
    upstream_request_id: 'upstream-request',
    ...overrides,
  }
}

const i18n = createInstance()
const previousConfig = useSystemConfigStore.getState().config
let client: QueryClient
beforeEach(async () => {
  await i18n.init({
    lng: 'en',
    resources: { en },
    interpolation: { escapeValue: false },
  })
  useSystemConfigStore
    .getState()
    .setConfig({ currency: { ...DEFAULT_CURRENCY_CONFIG } })
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  client.setQueryData(['status'], {}, { updatedAt: Date.now() + 60_000 })
  client.setQueryData(
    ['pricing'],
    { data: [], vendors: [] },
    { updatedAt: Date.now() + 60_000 }
  )
  vi.mocked(queryUpstreamLog).mockReset()
})
afterEach(() => {
  client.clear()
  useSystemConfigStore.getState().setConfig(previousConfig)
})

test('keeps corresponding values on one row, including zero, missing data and timing', () => {
  render(
    <I18nextProvider i18n={i18n}>
      <LogComparisonTable
        local={log({ frt: 0 }, { quota: 0 })}
        upstream={log(
          { cache_tokens: 12 },
          {
            request_id: 'upstream-request',
            upstream_request_id: '',
            prompt_tokens: 9,
            completion_tokens: 10,
            use_time: 2,
          }
        )}
      />
    </I18nextProvider>
  )
  const input = within(
    screen.getByRole('row', { name: /Input Tokens/ })
  ).getAllByRole('cell')
  expect(input[0]).toHaveTextContent(/^0Lower than upstream: 9$/)
  expect(input[1]).toHaveTextContent(/^9$/)
  const cache = within(
    screen.getByRole('row', { name: /^Cache Read/ })
  ).getAllByRole('cell')
  expect(cache[0]).toHaveTextContent('Not recorded')
  expect(cache[1]).toHaveTextContent('12')
  expect(cache[0]).not.toHaveAttribute('title')
  const output = within(
    screen.getByRole('row', { name: /Output Tokens/ })
  ).getAllByRole('cell')
  expect(output[0]).not.toHaveAttribute('title')
  const timing = within(
    screen.getByRole('row', { name: /^Response Time/ })
  ).getAllByRole('cell')
  expect(timing[0]).toHaveTextContent('0.0s')
  expect(timing[1]).toHaveTextContent('2.0s')
  expect(timing[0]).not.toHaveAttribute('title')
  const first = within(
    screen.getByRole('row', { name: /^First response time/ })
  ).getAllByRole('cell')
  expect(first[0]).toHaveTextContent('0 ms')
  expect(first[1]).toHaveTextContent('Not recorded')
})

test('uses the existing billing units and preserves currency precision in comparisons', () => {
  render(
    <I18nextProvider i18n={i18n}>
      <LogComparisonTable
        local={log({ model_ratio: 0.2, completion_ratio: 4, group_ratio: 1 })}
        upstream={log(
          { model_ratio: 0.4, completion_ratio: 4, group_ratio: 1 },
          { quota: 6000 }
        )}
      />
    </I18nextProvider>
  )
  const cost = within(
    screen.getByRole('row', { name: /^Total Cost/ })
  ).getAllByRole('cell')
  expect(cost[0]).toHaveTextContent('$0.01')
  expect(cost[1]).toHaveTextContent('$0.012')
  expect(cost[0]).toHaveAttribute('title', 'Lower than upstream: $0.012')
  const input = within(
    screen.getByRole('row', { name: /^Input \$/ })
  ).getAllByRole('cell')
  expect(input[0]).toHaveTextContent('$0.4/M')
  expect(input[1]).toHaveTextContent('$0.8/M')
  expect(screen.getByRole('row', { name: /^Group Ratio/ })).toHaveTextContent(
    '1.0000x'
  )
})

test('queries only on demand, preserves the local log after failure, and reuses results across tabs', async () => {
  const current = log({ request_path: '/v1/chat/completions' })
  const data: UpstreamLogQueryData = {
    channel: {
      id: 1,
      name: 'upstream',
      type: 1,
      key_index: 0,
      is_multi_key: false,
    },
    query: {
      scope: 'exact',
      checked_account: true,
      filters: {},
      page: 1,
      page_size: 5,
    },
    total: 1,
    elapsed_ms: 5,
    items: [
      {
        ...current,
        channel_name: current.channel_name ?? undefined,
        request_id: 'upstream-request',
        upstream_request_id: '',
        other: {},
      },
    ],
  }
  vi.mocked(queryUpstreamLog)
    .mockRejectedValueOnce(new Error('Unable to load upstream'))
    .mockResolvedValueOnce({ success: true, data })
  render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <DetailsDialog
          log={current}
          isAdmin
          isRoot={false}
          open
          onOpenChange={() => undefined}
        />
      </QueryClientProvider>
    </I18nextProvider>
  )
  expect(queryUpstreamLog).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: 'Query Upstream' }))
  expect(await screen.findByText('Unable to load upstream')).toBeVisible()
  expect(screen.getByText('local-request')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(
    await screen.findByRole('table', { name: 'Query Upstream' })
  ).toBeVisible()
  expect(queryUpstreamLog).toHaveBeenLastCalledWith(
    { local_request_id: 'local-request', page: 1, page_size: 5 },
    expect.any(AbortSignal)
  )
  fireEvent.click(screen.getByRole('button', { name: 'Details' }))
  expect(
    await screen.findByRole('heading', { name: 'Upstream Log Details' })
  ).toBeVisible()
  fireEvent.click(screen.getByRole('tab', { name: 'Request diagnostics' }))
  expect(screen.getByRole('table', { name: 'Query Upstream' })).toBeVisible()
  expect(queryUpstreamLog).toHaveBeenCalledTimes(2)
  fireEvent.click(screen.getByRole('button', { name: 'Close comparison' }))
  expect(
    screen.queryByRole('table', { name: 'Query Upstream' })
  ).not.toBeInTheDocument()
  expect(
    screen.getByRole('tab', { name: 'Request diagnostics' })
  ).toHaveAttribute('aria-selected', 'true')
  expect(screen.getByRole('button', { name: 'Query Upstream' })).toBeVisible()
})

test('clears comparison and diagnostic navigation when the viewer loses admin access', async () => {
  const current = log({ reasoning_effort: 'high' })
  vi.mocked(queryUpstreamLog).mockResolvedValue({
    success: true,
    data: { total: 0, items: [] } as unknown as UpstreamLogQueryData,
  })
  function Viewer(props: { isAdmin: boolean }) {
    return (
      <I18nextProvider i18n={i18n}>
        <QueryClientProvider client={client}>
          <DetailsDialog
            log={current}
            isAdmin={props.isAdmin}
            isRoot={false}
            open
            onOpenChange={() => undefined}
          />
        </QueryClientProvider>
      </I18nextProvider>
    )
  }
  const view = render(<Viewer isAdmin />)
  fireEvent.click(screen.getByRole('tab', { name: 'Request diagnostics' }))
  fireEvent.click(screen.getByRole('button', { name: 'Query Upstream' }))
  expect(
    await screen.findByRole('heading', { name: 'Upstream Log Details' })
  ).toBeVisible()
  view.rerender(<Viewer isAdmin={false} />)
  expect(screen.queryByRole('tab')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('heading', { name: 'Upstream Log Details' })
  ).not.toBeInTheDocument()
  expect(screen.getByText('Reasoning Effort')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Query Upstream' })
  ).not.toBeInTheDocument()
})

test('starts a different open log on billing without querying its upstream automatically', () => {
  const current = log({ reasoning_effort: 'high' })
  function Viewer(props: { current: UsageLog }) {
    return (
      <I18nextProvider i18n={i18n}>
        <QueryClientProvider client={client}>
          <DetailsDialog
            log={props.current}
            isAdmin
            isRoot={false}
            open
            onOpenChange={() => undefined}
          />
        </QueryClientProvider>
      </I18nextProvider>
    )
  }
  const view = render(<Viewer current={current} />)
  fireEvent.click(screen.getByRole('tab', { name: 'Request diagnostics' }))
  view.rerender(
    <Viewer current={{ ...current, id: 2, request_id: 'next-request' }} />
  )
  expect(screen.getByRole('tab', { name: 'Usage & billing' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  expect(queryUpstreamLog).not.toHaveBeenCalled()
})

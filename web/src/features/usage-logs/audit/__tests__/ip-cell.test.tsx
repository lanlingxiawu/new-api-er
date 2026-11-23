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

import type { AuditLog } from '../api'
import { AuditIPCell } from '../components/audit-ip-cell'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function renderIPCell(ip = '8.8.8.8') {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <AuditIPCell entry={{ ip } as AuditLog} />
    </QueryClientProvider>
  )
  return client
}

it('only fetches on hover and reuses the result when reopened', async () => {
  const fetch = vi.fn().mockResolvedValue(
    new Response(
      JSON.stringify({
        countryName: 'United States',
        countryCode: 'US',
        regionName: 'California',
        cityName: 'Mountain View',
      })
    )
  )
  vi.stubGlobal('fetch', fetch)
  renderIPCell()
  const user = userEvent.setup()
  const trigger = screen.getByRole('button', { name: '8.8.8.8' })
  expect(fetch).not.toHaveBeenCalled()
  expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
  await user.hover(trigger)
  await waitFor(() =>
    expect(screen.getByRole('tooltip')).toHaveTextContent(
      'United States · California · Mountain View'
    )
  )
  await user.unhover(trigger)
  await waitFor(() =>
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
  )
  await user.hover(trigger)
  expect(await screen.findByRole('tooltip')).toHaveTextContent('Mountain View')
  expect(fetch).toHaveBeenCalledTimes(1)
})

it.each(['keyboard', 'click'])(
  'opens local address information using %s without a network call',
  async (method) => {
    const fetch = vi.fn()
    vi.stubGlobal('fetch', fetch)
    renderIPCell('127.0.0.1')
    const user = userEvent.setup()
    if (method === 'keyboard') await user.tab()
    else await user.click(screen.getByRole('button', { name: '127.0.0.1' }))
    expect(await screen.findByRole('tooltip')).toHaveTextContent(
      'Loopback address'
    )
    expect(fetch).not.toHaveBeenCalled()
  }
)

it('keeps the IP visible after a lookup fails and retries on the next hover', async () => {
  const fetch = vi
    .fn()
    .mockRejectedValueOnce(new Error('offline'))
    .mockResolvedValue(
      new Response(
        JSON.stringify({
          countryName: 'United States',
          countryCode: 'US',
          regionName: null,
          cityName: null,
        })
      )
    )
  vi.stubGlobal('fetch', fetch)
  renderIPCell()
  const user = userEvent.setup()
  const trigger = screen.getByRole('button', { name: '8.8.8.8' })
  await user.hover(trigger)
  await waitFor(() =>
    expect(screen.getByRole('tooltip')).toHaveTextContent(
      'IP locations unavailable'
    )
  )
  expect(trigger).toBeVisible()
  await user.unhover(trigger)
  await waitFor(() =>
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
  )
  await user.hover(trigger)
  await waitFor(() =>
    expect(screen.getByRole('tooltip')).toHaveTextContent('United States')
  )
  expect(fetch).toHaveBeenCalledTimes(2)
})

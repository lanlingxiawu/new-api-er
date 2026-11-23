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
import { QueryClient, QueryObserver } from '@tanstack/react-query'
import {
  CanceledError,
  type AxiosAdapter,
  type InternalAxiosRequestConfig,
} from 'axios'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { saveAlert, securityGet } from './api'

const originalAdapter = api.defaults.adapter

afterEach(() => {
  api.defaults.adapter = originalAdapter
})

it.each([
  {
    path: 'alerts',
    before: { items: [{ id: 7, version: 3, status: 'open' }] },
    after: { items: [{ id: 7, version: 4, status: 'resolved' }] },
  },
  {
    path: 'alerts/7',
    before: { alert: { id: 7, version: 3, status: 'open' } },
    after: { alert: { id: 7, version: 4, status: 'resolved' } },
  },
  {
    path: 'health',
    before: { enabled: true, ready: true, last_scan: 1 },
    after: { enabled: true, ready: true, last_scan: 2 },
  },
])(
  'refreshes $path after confirmation even when a background request is still running',
  async ({ path, before, after }) => {
    let saved = false
    const reads: InternalAxiosRequestConfig[] = []
    const adapter = vi.fn<AxiosAdapter>((config) => {
      if (config.method === 'post') {
        expect(config.url).toBe('/api/audit/security/alerts/7/actions')
        expect(JSON.parse(config.data)).toEqual({
          version: 3,
          status: 'resolved',
          note: '',
        })
        saved = true
      } else {
        reads.push(config)
        if (!saved) {
          return new Promise((_resolve, reject) => {
            config.signal?.addEventListener?.(
              'abort',
              () => reject(new CanceledError()),
              { once: true }
            )
          })
        }
      }
      return Promise.resolve({
        config,
        status: 200,
        statusText: 'OK',
        headers: {},
        data: { success: true, data: after },
      })
    })
    api.defaults.adapter = adapter
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const observer = new QueryObserver(client, {
      queryKey: ['audit-security', path],
      queryFn: ({ signal }) => securityGet<typeof before>(path, signal),
      initialData: before,
      staleTime: Infinity,
    })
    const unsubscribe = observer.subscribe(vi.fn())
    try {
      const background = observer.refetch()
      await vi.waitFor(() => expect(reads).toHaveLength(1))
      await saveAlert(7, 3, 'resolved', '')
      await client.invalidateQueries({ queryKey: ['audit-security'] })
      await background

      expect(reads[0].signal?.aborted).toBe(true)
      expect(observer.getCurrentResult()).toMatchObject({
        status: 'success',
        data: after,
        error: null,
      })
      expect(reads).toHaveLength(2)
    } finally {
      unsubscribe()
      client.clear()
    }
  }
)

it('shares concurrent monitoring reads through the query cache', async () => {
  const page = { items: [{ id: 7, version: 3, status: 'open' }] }
  const adapter = vi.fn<AxiosAdapter>(async (config) => ({
    config,
    status: 200,
    statusText: 'OK',
    headers: {},
    data: { success: true, data: page },
  }))
  api.defaults.adapter = adapter
  const client = new QueryClient()
  const options = {
    queryKey: ['audit-security', 'alerts'],
    queryFn: ({ signal }: { signal: AbortSignal }) =>
      securityGet('alerts', signal),
  }
  try {
    const results = await Promise.all([
      client.fetchQuery(options),
      client.fetchQuery(options),
    ])
    expect(results).toEqual([page, page])
    expect(adapter).toHaveBeenCalledTimes(1)
  } finally {
    client.clear()
  }
})

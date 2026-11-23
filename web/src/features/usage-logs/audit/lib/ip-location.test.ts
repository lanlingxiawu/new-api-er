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
import { afterEach, expect, it, vi } from 'vitest'

import { getIPLocation } from './ip-location'

afterEach(() => vi.unstubAllGlobals())

it.each([
  ['10.0.0.1', 'private'],
  ['172.16.0.1', 'private'],
  ['192.168.0.1', 'private'],
  ['127.0.0.1', 'loopback'],
  ['::1', 'loopback'],
  ['fc00::1', 'private'],
  ['fe80::1', 'link_local'],
  ['169.254.1.1', 'link_local'],
  ['::ffff:192.168.1.1', 'private'],
  ['::ffff:c0a8:101', 'private'],
  ['2001:db8::1', 'reserved'],
  ['203.0.113.1', 'reserved'],
  ['100.64.0.1', 'reserved'],
  ['224.0.0.1', 'reserved'],
  ['::', 'reserved'],
  ['', undefined],
  ['bad ip', undefined],
  ['8.8.8.8/24', undefined],
  ['fe80::1%eth0', undefined],
])(
  'does not send non-public address %s to the provider',
  async (ip, networkType) => {
    const fetch = vi.fn()
    vi.stubGlobal('fetch', fetch)
    expect(await getIPLocation(ip)).toMatchObject({ network_type: networkType })
    expect(fetch).not.toHaveBeenCalled()
  }
)

it.each(['8.8.8.8', '2001:4860:4860::8888'])(
  'looks up public IP %s without dashboard credentials',
  async (ip) => {
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
    expect(await getIPLocation(ip)).toMatchObject({
      country: 'United States',
      region: 'California',
      city: 'Mountain View',
    })
    expect(fetch).toHaveBeenCalledWith(
      `https://free.freeipapi.com/api/v1/json/${encodeURIComponent(ip)}`,
      {
        credentials: 'omit',
        referrerPolicy: 'no-referrer',
        signal: expect.any(AbortSignal),
      }
    )
  }
)

it.each([
  new Response('', { status: 429 }),
  new Response('not json'),
  new Response('{"message":"unavailable"}'),
])('rejects unavailable or invalid provider responses', async (response) => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response))
  await expect(getIPLocation('8.8.8.8')).rejects.toThrow()
})

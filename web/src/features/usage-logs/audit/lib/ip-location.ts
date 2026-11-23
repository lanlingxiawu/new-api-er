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
import { z } from 'zod'

export interface IPLocation {
  status: 'located' | 'unknown' | 'private'
  network_type?: 'private' | 'loopback' | 'link_local' | 'reserved'
  country_code?: string
  country?: string
  region?: string
  city?: string
}

const locationResponse = z.object({
  countryName: z.string().nullable(),
  countryCode: z.string().nullable(),
  regionName: z.string().nullable(),
  cityName: z.string().nullable(),
})

export async function getIPLocation(
  ip: string,
  signal?: AbortSignal
): Promise<IPLocation> {
  let address = ip
  let network: IPLocation['network_type']
  if (z.ipv6().safeParse(ip).success && !ip.includes('%')) {
    address = new URL(`http://[${ip}]`).hostname.slice(1, -1)
    if (address.startsWith('::ffff:')) {
      address = address
        .slice(7)
        .split(':')
        .flatMap((part) => {
          const value = Number.parseInt(part, 16)
          return [value >> 8, value & 255]
        })
        .join('.')
    } else if (address === '::1') {
      network = 'loopback'
    } else if (/^f[cd]/.test(address)) {
      network = 'private'
    } else if (/^fe[89ab]/.test(address)) {
      network = 'link_local'
    } else if (!/^[23]/.test(address) || address.startsWith('2001:db8:')) {
      network = 'reserved'
    }
  } else if (!z.ipv4().safeParse(ip).success) {
    return { status: 'unknown', network_type: undefined }
  }
  if (address.includes('.')) {
    const [first, second, third] = address.split('.').map(Number)
    if (first === 127) {
      network = 'loopback'
    } else if (
      first === 10 ||
      (first === 172 && second >= 16 && second <= 31) ||
      (first === 192 && second === 168)
    ) {
      network = 'private'
    } else if (first === 169 && second === 254) {
      network = 'link_local'
    } else if (
      first === 0 ||
      first >= 224 ||
      (first === 100 && second >= 64 && second <= 127) ||
      (first === 192 && second === 0) ||
      (first === 198 &&
        (second === 18 || second === 19 || (second === 51 && third === 100))) ||
      (first === 203 && second === 0 && third === 113)
    ) {
      network = 'reserved'
    }
  }
  if (network) return { status: 'private', network_type: network }

  // This external request must never use the authenticated dashboard API client.
  const timeout = AbortSignal.timeout(5000)
  const response = await fetch(
    `https://free.freeipapi.com/api/v1/json/${encodeURIComponent(ip)}`,
    {
      credentials: 'omit',
      referrerPolicy: 'no-referrer',
      signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
    }
  )
  if (!response.ok) throw new Error('IP location lookup unavailable')
  const data = locationResponse.parse(await response.json())
  return {
    status:
      data.countryName || data.regionName || data.cityName
        ? 'located'
        : 'unknown',
    country: data.countryName || undefined,
    country_code: data.countryCode || undefined,
    region: data.regionName || undefined,
    city: data.cityName || undefined,
  }
}

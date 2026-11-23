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
export function validProviderSettings(value: string): boolean {
  try {
    const providers: unknown = JSON.parse(value || '[]')
    return (
      Array.isArray(providers) &&
      providers.length <= 1 &&
      providers.every(
        (item) =>
          item &&
          typeof item.id === 'string' &&
          item.type === 'custom_ipapi' &&
          Array.isArray(item.capabilities)
      )
    )
  } catch {
    return false
  }
}

export function parsePrivateProviderHosts(value: string): string[] | undefined {
  const hosts = value
    .split(',')
    .map((host) => host.trim().toLowerCase())
    .filter(Boolean)
  for (const host of hosts) {
    if (host.length > 253) return undefined
    if (host.includes(':')) {
      try {
        new URL(`https://[${host}]/`)
      } catch {
        return undefined
      }
    } else if (
      !/^[a-z0-9.-]+$/.test(host) ||
      host.startsWith('.') ||
      host.endsWith('.') ||
      host.includes('..')
    ) {
      return undefined
    }
  }
  return [...new Set(hosts)]
}

export function providerSettingsWithDraft(
  value: string,
  hosts: string,
  dailyLimit: string
): string {
  if (!validProviderSettings(value)) return value
  const providers = JSON.parse(value || '[]') as Record<string, unknown>[]
  if (providers.length === 0) return '[]'
  return JSON.stringify([
    {
      ...providers[0],
      allowed_private_hosts: parsePrivateProviderHosts(hosts),
      daily_limit: Number(dailyLimit),
    },
  ])
}

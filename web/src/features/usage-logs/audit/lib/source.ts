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
export function auditActorLabel(
  role: number,
  employee?: boolean | null
): string {
  if (role === 100) return 'Super administrator'
  if (role === 10) return 'Administrator'
  if (role !== 1) return 'Unknown identity'
  if (employee === true) return 'Employee'
  if (employee === false) return 'User'
  return 'User (employee status not recorded)'
}

export function identifyAuditClient(raw: string): string {
  const ua = (raw || '').slice(0, 2048)
  if (!ua) return 'Unknown client'
  for (const [pattern, name] of [
    [/Electron\//i, 'Electron'],
    [/PostmanRuntime\//i, 'Postman'],
    [/curl\//i, 'curl'],
    [/python-requests\//i, 'Python requests'],
    [/python-httpx\//i, 'Python httpx'],
    [/\bnode(?:\.js)?\b/i, 'Node.js'],
    [/Go-http-client\//i, 'Go HTTP Client'],
  ] as const) {
    if (pattern.test(ua)) return name
  }
  let browser = ''
  for (const [pattern, name] of [
    [/Edg(?:e|A|iOS)?\//, 'Edge'],
    [/OPR\/|Opera\//, 'Opera'],
    [/Firefox\/|FxiOS\//, 'Firefox'],
    [/Chrome\/|CriOS\//, 'Chrome'],
    [/Safari\//, 'Safari'],
  ] as const) {
    if (pattern.test(ua)) {
      browser = name
      break
    }
  }
  if (!browser) return 'Other client'
  let system = ''
  for (const [pattern, name] of [
    [/iPhone|iPad|iPod/, 'iOS'],
    [/Android/, 'Android'],
    [/Windows/, 'Windows'],
    [/Macintosh|Mac OS X/, 'macOS'],
    [/Linux/, 'Linux'],
  ] as const) {
    if (pattern.test(ua)) {
      system = name
      break
    }
  }
  return system ? `${browser} · ${system}` : browser
}

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
function getIconMimeType(url: string) {
  try {
    const pathname = new URL(url, window.location.href).pathname.toLowerCase()
    if (pathname.endsWith('.svg')) return 'image/svg+xml'
    if (pathname.endsWith('.png')) return 'image/png'
  } catch {
    // Ignore malformed URLs
  }
  return ''
}

export function applyFaviconToDom(url: string) {
  if (typeof document === 'undefined' || !url) return
  try {
    const next = new URL(url, window.location.href).href
    const existing =
      document.querySelectorAll<HTMLLinkElement>('link[rel~="icon"]')
    if (existing.length === 1 && existing[0].href === next) return
    const link = document.createElement('link')
    link.rel = 'icon'
    link.href = next
    const mimeType = getIconMimeType(next)
    if (mimeType) {
      link.type = mimeType
      if (mimeType === 'image/svg+xml') {
        link.sizes = 'any'
      }
    }
    existing.forEach((l) => l.remove())
    document.head.appendChild(link)
  } catch {
    // Ignore malformed URLs
  }
}

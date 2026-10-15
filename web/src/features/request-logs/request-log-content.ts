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
import type { RequestLogItem } from './types'

export const SMALL_LOG_CONTENT_LIMIT = 64 * 1024
export const LOG_ROWS_PER_PAGE = 10000

export function formatRequestLogContent(raw = ''): string {
  if (raw.length > SMALL_LOG_CONTENT_LIMIT) return raw
  const trimmed = raw.trim()
  if (
    (trimmed.startsWith('{') && trimmed.endsWith('}')) ||
    (trimmed.startsWith('[') && trimmed.endsWith(']'))
  ) {
    try {
      return JSON.stringify(JSON.parse(trimmed), null, 2)
    } catch {
      return raw
    }
  }
  return raw
}

function nextDisplayRowEnd(content: string, start: number): number {
  const limit = Math.min(start + 256, content.length)
  for (let i = start; i < limit; i++) {
    const code = content.charCodeAt(i)
    if (code === 10) return i + 1
    if (code === 13) return i + (content.charCodeAt(i + 1) === 10 ? 2 : 1)
  }
  const last = content.charCodeAt(limit - 1)
  const next = content.charCodeAt(limit)
  return last >= 0xd800 && last <= 0xdbff && next >= 0xdc00 && next <= 0xdfff
    ? limit - 1
    : limit
}

// Two passes allocate only the exact offset buffer, not an array of substrings.
export function indexRequestLogRows(content: string): Uint32Array {
  const trailingEmpty = !content || /[\r\n]$/.test(content)
  let count = trailingEmpty ? 2 : 1
  for (let end = 0; end < content.length; count++) {
    end = nextDisplayRowEnd(content, end)
  }
  const offsets = new Uint32Array(count)
  let row = 1
  for (let end = 0; end < content.length; ) {
    end = nextDisplayRowEnd(content, end)
    offsets[row++] = end
  }
  if (trailingEmpty) offsets[row] = content.length
  return offsets
}

export function createRequestLogBlob(data: RequestLogItem): Blob {
  return new Blob([JSON.stringify(data, null, 2)], {
    type: 'application/json;charset=utf-8',
  })
}

export type RequestLogJob =
  | { type: 'index'; content: string }
  | { type: 'download'; data: RequestLogItem }

export type RequestLogJobResult = Uint32Array | Blob

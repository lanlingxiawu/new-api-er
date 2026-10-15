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
import {
  createRequestLogBlob,
  indexRequestLogRows,
  type RequestLogJob,
} from './request-log-content'

const scope = self as unknown as {
  postMessage: (value: unknown, transfer: Transferable[]) => void
}

self.addEventListener('message', ({ data }: MessageEvent<RequestLogJob>) => {
  if (data.type === 'index') {
    const offsets = indexRequestLogRows(data.content)
    scope.postMessage(offsets, [offsets.buffer as ArrayBuffer])
  } else {
    scope.postMessage(createRequestLogBlob(data.data), [])
  }
})

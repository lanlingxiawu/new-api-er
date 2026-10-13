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
import type { RequestLogJob, RequestLogJobResult } from './request-log-content'

export function runRequestLogJob(
  job: RequestLogJob,
  signal: AbortSignal
): Promise<RequestLogJobResult> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) {
      reject(new DOMException('Aborted', 'AbortError'))
      return
    }
    const worker = new Worker(
      new URL('./request-log.worker.ts', import.meta.url),
      {
        type: 'module',
      }
    )
    let finished = false
    const finish = (error?: Error, result?: RequestLogJobResult) => {
      if (finished) return
      finished = true
      clearTimeout(timeout)
      signal.removeEventListener('abort', abort)
      worker.terminate()
      if (error) reject(error)
      else if (result) resolve(result)
    }
    const abort = () => finish(new DOMException('Aborted', 'AbortError'))
    const timeout = setTimeout(
      () => finish(new Error('Worker timed out')),
      30000
    )
    signal.addEventListener('abort', abort, { once: true })
    worker.addEventListener(
      'message',
      ({ data }: MessageEvent<RequestLogJobResult>) => {
        const valid =
          job.type === 'index'
            ? data instanceof Uint32Array
            : data instanceof Blob
        if (valid) finish(undefined, data)
        else finish(new Error('Invalid worker result'))
      }
    )
    worker.addEventListener('error', () => finish(new Error('Worker failed')))
    worker.addEventListener('messageerror', () =>
      finish(new Error('Worker message failed'))
    )
    try {
      worker.postMessage(job)
    } catch (error) {
      finish(error instanceof Error ? error : new Error('Worker failed'))
    }
  })
}

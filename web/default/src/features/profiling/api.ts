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
import { api } from '@/lib/api'

import type { PprofStatus } from './types'

export async function getPprofStatus() {
  const res = await api.get<{ success: boolean; data?: PprofStatus }>(
    '/api/system-info/pprof-status'
  )
  return res.data
}

/**
 * 下载必须走 api 实例，不能用 window.open：后端鉴权要求 New-Api-User 请求头
 * （middleware/auth.go 的 authHelper），浏览器直接打开链接带不了自定义头，一律 401。
 * 业务错误是 HTTP 200 + JSON 体，按 Content-Type 判出来抛异常让调用方弹提示，
 * 而不是把错误信息当成 profile 文件存下来。
 */
type DownloadOptions = {
  seconds?: number
  // debug=2 让 goroutine 输出人类可读的全栈文本（而非 go tool pprof 用的 protobuf）
  debug?: number
}

function fileExtension(name: string, debug?: number) {
  if (debug) return 'txt'
  if (name === 'trace') return 'trace'
  return 'pprof'
}

export async function downloadProfile(name: string, opts: DownloadOptions = {}) {
  const params = new URLSearchParams()
  if (opts.seconds) params.set('seconds', String(opts.seconds))
  if (opts.debug) params.set('debug', String(opts.debug))
  const query = params.toString() ? `?${params}` : ''

  const res = await api.get<Blob>(
    `/api/system-info/pprof/${encodeURIComponent(name)}${query}`,
    {
      responseType: 'blob',
      skipBusinessError: true,
      // CPU / trace 采样会挂住连接几十秒，不能套用默认超时
      timeout: 0,
    }
  )

  const blob = res.data
  if (blob.type.includes('application/json')) {
    const text = await blob.text()
    try {
      const payload = JSON.parse(text) as { message?: string }
      throw new Error(payload.message || text)
    } catch (error) {
      if (error instanceof SyntaxError) throw new Error(text)
      throw error
    }
  }

  const stamp = new Date().toISOString().replaceAll(/[-:T]/g, '').slice(0, 14)
  // debug 文本存 .txt；trace 存 .trace；其余是 protobuf，存 .pprof
  const extension = fileExtension(name, opts.debug)
  const objectUrl = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = objectUrl
  link.download = `${name}-${stamp}.${extension}`
  document.body.append(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(objectUrl)
}

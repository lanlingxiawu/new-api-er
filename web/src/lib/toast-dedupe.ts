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
import { toast } from 'sonner'

type ToastMethod = typeof toast.success

const DEDUPED_TYPES = ['success', 'error', 'info', 'warning', 'message'] as const
const INSTALLED = Symbol.for('new-api.toast-dedupe')

/**
 * 同一条弱提示在显示期间只保留一个。
 *
 * 一次操作叠出多个相同提示的两个来源：设置页逐键保存，每个键都提示一次「设置已更新」；
 * 业务失败时 http-client 的全局拦截器与调用方各提示一次同一条服务端消息。这里给没有指定 id
 * 的纯文本提示按「类型 + 文案」生成 id，sonner 对同 id 的提示原地更新而不是再弹一个。
 *
 * http-client.ts 是上游文件（Rule 6），不能改它，所以在应用启动时包一层。
 */
export function installToastDedupe(): void {
  const target = toast as unknown as Record<string | symbol, unknown>
  if (target[INSTALLED]) return
  target[INSTALLED] = true

  for (const type of DEDUPED_TYPES) {
    const original: ToastMethod = toast[type].bind(toast)
    const deduped: ToastMethod = (message, data) => {
      if (typeof message !== 'string' || data?.id !== undefined) {
        return original(message, data)
      }
      return original(message, { ...data, id: `${type}:${message}` })
    }
    target[type] = deduped
  }
}

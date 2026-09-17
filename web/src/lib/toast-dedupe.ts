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

/** 同一连串通知中，相邻两次触发的最大间隔。 */
export const TOAST_BURST_WINDOW_MS = 1500

const bursts = new Map<string, { id: string; lastAt: number }>()
let burstSeq = 0

/**
 * 为「一次操作内部重复触发」的同一事件生成 sonner toast id。
 *
 * 设置页逐键保存：一次点击保存会串行提交多个键，每个键成功都走到同一个 onSuccess。
 * 同一 eventKey 与上一次触发间隔不超过 TOAST_BURST_WINDOW_MS 时复用同一个 id，
 * sonner 对同 id 原地更新，整串保存只显示一条；间隔更长视为新的操作，得到新 id，照常单独提示。
 *
 * 只用于调用方明确知道会连发的场景。错误提示不走这里：同一错误对象只提示一次由
 * handleServerError 的 WeakSet 保证，相互独立的失败即使文案相同也各自提示。
 */
export function burstToastId(eventKey: string, now = Date.now()): string {
  const burst = bursts.get(eventKey)
  if (burst && now - burst.lastAt <= TOAST_BURST_WINDOW_MS) {
    burst.lastAt = now
    return burst.id
  }
  burstSeq += 1
  const id = `burst:${eventKey}:${burstSeq}`
  bursts.set(eventKey, { id, lastAt: now })
  return id
}

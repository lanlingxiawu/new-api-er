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

/**
 * 打开下拉时搜索框的初始内容。
 *
 * 只有自由输入模式（allowCustomValue）的当前值才是可编辑的文本，打开时保留它才能接着改。
 * 纯选择器的 value 是选项键（常为 ID）：拿它预填搜索，会把列表过滤到只剩当前项，
 * 输入框里还显示成一串 ID——想换一个选项就得先手动清空。
 */
export function comboboxInitialSearch(
  value: string,
  allowCustomValue: boolean
): string {
  return allowCustomValue ? value : ''
}

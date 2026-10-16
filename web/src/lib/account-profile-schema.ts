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
import { z } from 'zod'

// 与后端 validator 保持一致：按 Unicode 码点计数，表情也只计一个字符。
function accountTextSchema(maximum: number) {
  return z.string().superRefine((value, context) => {
    if ([...value].length > maximum) {
      context.addIssue({
        code: 'too_big',
        origin: 'string',
        maximum,
        inclusive: true,
      })
    }
  })
}

export const accountUsernameSchema = z
  .string()
  .trim()
  .min(1)
  .pipe(accountTextSchema(20))
export const accountDisplayNameSchema = accountTextSchema(20)
export const accountEmailSchema = accountTextSchema(50)
export const accountRemarkSchema = accountTextSchema(255)

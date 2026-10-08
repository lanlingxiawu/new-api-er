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
import type {
  ControllerRenderProps,
  FieldPath,
  FieldValues,
} from 'react-hook-form'

import type { NumericPresetInputProps } from '@/components/numeric-preset-input'

/**
 * Binds a react-hook-form numeric field to `NumericPresetInput`, the counterpart
 * of `safeNumberFieldProps` for the plain number input. Wrap the component in
 * `FormControl` as usual so the label and form errors stay linked.
 *
 * ```tsx
 * <FormControl>
 *   <NumericPresetInput presets={…} min={1} {...numericPresetFieldProps(field)} />
 * </FormControl>
 * ```
 */
export function numericPresetFieldProps<
  TFieldValues extends FieldValues,
  TName extends FieldPath<TFieldValues>,
>(
  field: ControllerRenderProps<TFieldValues, TName>
): Pick<NumericPresetInputProps, 'value' | 'onChange' | 'onBlur'> {
  const raw = field.value as unknown
  return {
    value: typeof raw === 'number' || typeof raw === 'string' ? raw : undefined,
    onChange: (value) => (field.onChange as (next: number) => void)(value),
    onBlur: field.onBlur,
  }
}

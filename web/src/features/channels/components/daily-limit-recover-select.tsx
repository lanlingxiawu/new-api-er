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
import type { ComponentProps } from 'react'
import { useTranslation } from 'react-i18next'

import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

import { DAILY_LIMIT_RECOVER_MODE_OPTIONS } from '../constants'
import type { DailyLimitRecoverMode } from '../types'

/** 'keep' 只用于批量设置：本次不修改各渠道的恢复方式。 */
export type DailyLimitRecoverSelectValue = DailyLimitRecoverMode | 'keep'

type DailyLimitRecoverSelectProps = Omit<
  ComponentProps<typeof SelectTrigger>,
  'value' | 'onChange' | 'disabled'
> & {
  value: DailyLimitRecoverSelectValue
  onValueChange: (value: DailyLimitRecoverSelectValue) => void
  disabled?: boolean
  allowKeepUnchanged?: boolean
}

/**
 * 每日金额上限的恢复方式选择器（渠道抽屉与批量设置共用）。
 * 其余 props 透传给 SelectTrigger，以便放进 FormControl 时拿到 id / aria 属性。
 */
export function DailyLimitRecoverSelect({
  value,
  onValueChange,
  disabled,
  allowKeepUnchanged = false,
  ...triggerProps
}: DailyLimitRecoverSelectProps) {
  const { t } = useTranslation()
  const items = [
    ...(allowKeepUnchanged
      ? [{ value: 'keep' as const, label: t('Keep unchanged') }]
      : []),
    ...DAILY_LIMIT_RECOVER_MODE_OPTIONS.map((option) => ({
      value: option.value,
      label: t(option.label),
    })),
  ]
  // 显式渲染标签：SelectValue 不带 children 时，在 disabled / 内容未挂载的情况下
  // 可能回退成原始枚举值。
  const selectedLabel =
    items.find((item) => item.value === value)?.label ?? value

  return (
    <Select
      items={items}
      value={value}
      onValueChange={(next) => {
        if (next !== null) onValueChange(next as DailyLimitRecoverSelectValue)
      }}
      disabled={disabled}
    >
      <SelectTrigger {...triggerProps}>
        <SelectValue>{selectedLabel}</SelectValue>
      </SelectTrigger>
      <SelectContent alignItemWithTrigger={false}>
        <SelectGroup>
          {items.map((item) => (
            <SelectItem key={item.value} value={item.value}>
              {item.label}
            </SelectItem>
          ))}
        </SelectGroup>
      </SelectContent>
    </Select>
  )
}

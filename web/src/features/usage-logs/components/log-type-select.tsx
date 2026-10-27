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
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

import { LOG_TYPE_ALL_VALUE, LOG_TYPE_FILTERS } from '../constants'

export function LogTypeSelect(props: {
  value: string
  onValueChange: (value: string) => void
  id?: string
  disabled?: boolean
  className?: string
}) {
  const { t } = useTranslation()
  const items = useMemo(
    () => LOG_TYPE_FILTERS.map((type) => ({ ...type, label: t(type.label) })),
    [t]
  )
  const selected = items.find((type) => type.value === props.value)
  const deprecatedDescription = t(
    'Only used to find historical logs. New records are available in Audit Logs.'
  )

  return (
    <Select
      items={items}
      value={props.value}
      onValueChange={(value) =>
        props.onValueChange(value ?? LOG_TYPE_ALL_VALUE)
      }
      disabled={props.disabled}
    >
      <SelectTrigger
        id={props.id}
        className={props.className}
        aria-label={t('Type')}
        aria-description={
          selected?.deprecated ? deprecatedDescription : undefined
        }
      >
        <SelectValue className='min-w-0'>
          <span className='truncate'>{selected?.label ?? t('All Types')}</span>
          {selected?.deprecated && (
            <Badge
              variant='secondary'
              className='h-4 px-1.5 text-[10px] font-normal'
              title={deprecatedDescription}
            >
              {t('Deprecated')}
            </Badge>
          )}
        </SelectValue>
      </SelectTrigger>
      <SelectContent
        alignItemWithTrigger={false}
        className='max-w-[calc(100vw-2rem)] min-w-52'
      >
        <SelectGroup>
          {items.map((type) => (
            <SelectItem
              key={type.value}
              value={type.value}
              className='[&_[data-slot=select-item-text]]:items-center'
              aria-description={
                type.deprecated ? deprecatedDescription : undefined
              }
            >
              {type.label}
              {type.deprecated && (
                <Badge
                  variant='secondary'
                  className='h-4 px-1.5 text-[10px] font-normal'
                  title={deprecatedDescription}
                >
                  {t('Deprecated')}
                </Badge>
              )}
            </SelectItem>
          ))}
        </SelectGroup>
      </SelectContent>
    </Select>
  )
}

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
import type { Control, FieldPath, FieldValues } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

export function StreamProtocolValidationField<T extends FieldValues>(props: {
  control: Control<T>
  name: FieldPath<T>
  scope: 'user' | 'channel'
  disabled?: boolean
  legacyValue?: boolean | null
}) {
  const { t } = useTranslation()
  const items = [
    {
      value: 'inherit',
      label:
        props.scope === 'user'
          ? t('Inherit channel or global setting')
          : t('Inherit global setting'),
    },
    { value: 'on', label: t('Enabled') },
    { value: 'off', label: t('Disabled') },
  ]
  return (
    <FormField
      control={props.control}
      name={props.name}
      render={({ field }) => {
        let value = 'inherit'
        if (field.value === true) value = 'on'
        if (field.value === false) value = 'off'
        return (
          <FormItem>
            <FormLabel>{t('Stream protocol strict validation')}</FormLabel>
            <Select
              items={items}
              value={value}
              disabled={props.disabled}
              onValueChange={(next) => {
                if (next === null) return
                field.onChange(next === 'inherit' ? null : next === 'on')
              }}
            >
              <FormControl>
                <SelectTrigger
                  className='w-full'
                  ref={field.ref}
                  onBlur={field.onBlur}
                >
                  <SelectValue />
                </SelectTrigger>
              </FormControl>
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
            <FormDescription>
              {t(
                'When enabled, checks streaming responses for protocol errors and incomplete endings. When disabled, skips strict validation and keeps error logging. Priority: user, channel, then global.'
              )}
            </FormDescription>
            {field.value === undefined &&
              typeof props.legacyValue === 'boolean' && (
                <FormDescription>
                  {props.scope === 'user'
                    ? t(
                        'User override: Claude-specific validation {{status}}; other protocols inherit channel or global settings.',
                        {
                          status: props.legacyValue
                            ? t('Enabled')
                            : t('Disabled'),
                        }
                      )
                    : t(
                        'Channel override: Claude-specific validation {{status}}; other protocols inherit global settings.',
                        {
                          status: props.legacyValue
                            ? t('Enabled')
                            : t('Disabled'),
                        }
                      )}
                  <Button
                    type='button'
                    variant='link'
                    size='sm'
                    disabled={props.disabled}
                    onClick={() => field.onChange(props.legacyValue)}
                  >
                    {props.legacyValue
                      ? t('Enable for all protocols')
                      : t('Disable for all protocols')}
                  </Button>
                </FormDescription>
              )}
            <FormMessage />
          </FormItem>
        )
      }}
    />
  )
}

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
import { Check, ChevronsUpDown } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { cn } from '@/lib/utils'

type GroupComboboxProps = {
  groups: string[]
  value?: string
  onValueChange: (value: string) => void
  placeholder?: string
  disabled?: boolean
  className?: string
}

/** Group select with a search box, for quickly locating a group in long lists. */
export function GroupCombobox({
  groups,
  value,
  onValueChange,
  placeholder,
  disabled,
  className,
}: GroupComboboxProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)

  const handleSelect = (nextValue: string) => {
    onValueChange(nextValue)
    setOpen(false)
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        render={
          <Button
            type='button'
            variant='outline'
            role='combobox'
            aria-expanded={open}
            disabled={disabled}
            className={cn(
              'w-full justify-between font-normal',
              !value && 'text-muted-foreground',
              className
            )}
          />
        }
      >
        <span className='truncate'>
          {value || placeholder || t('Select a group')}
        </span>
        <ChevronsUpDown className='h-4 w-4 shrink-0 opacity-50' />
      </PopoverTrigger>
      <PopoverContent
        className='w-(--anchor-width) overflow-hidden rounded-xl p-0 shadow-lg'
        onWheel={(event) => event.stopPropagation()}
        onTouchMove={(event) => event.stopPropagation()}
        onPointerDown={(event) => event.stopPropagation()}
      >
        <Command>
          <CommandInput placeholder={t('Search...')} />
          <CommandList className='max-h-60'>
            <CommandEmpty>{t('No group found.')}</CommandEmpty>
            <CommandGroup>
              {groups.map((group) => (
                <CommandItem
                  key={group}
                  value={group}
                  onSelect={() => handleSelect(group)}
                  className='data-[selected=true]:bg-muted gap-2 rounded-lg px-2 py-1.5'
                >
                  <Check
                    className={cn(
                      'h-4 w-4 shrink-0',
                      value === group ? 'opacity-100' : 'opacity-0'
                    )}
                  />
                  <span className='truncate'>{group}</span>
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}

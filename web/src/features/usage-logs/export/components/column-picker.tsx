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
import { ArrowDown, ArrowUp, Plus, Search, X } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ScrollArea } from '@/components/ui/scroll-area'

import type { ExportColumn, ExportColumnGroup } from '../types'

const GROUP_LABELS: Record<ExportColumnGroup, string> = {
  basic: 'Basic',
  tokens: 'Tokens',
  billing: 'Billing',
  performance: 'Performance',
  admin: 'Channel & Routing',
  audit: 'Audit',
}

const GROUP_ORDER: ExportColumnGroup[] = [
  'basic',
  'tokens',
  'billing',
  'performance',
  'admin',
  'audit',
]

interface ColumnPickerProps {
  columns: ExportColumn[]
  selected: string[]
  maxColumns: number
  onChange: (next: string[]) => void
}

/**
 * Two-pane column picker: available columns on the left (grouped, searchable),
 * the ordered selection on the right. Order matters — it is the column order in
 * the exported file — so the right pane supports move up/down rather than being
 * a plain checkbox list.
 */
export function ColumnPicker({
  columns,
  selected,
  maxColumns,
  onChange,
}: ColumnPickerProps) {
  const { t } = useTranslation()
  const [search, setSearch] = useState('')

  const byKey = useMemo(() => {
    const map = new Map<string, ExportColumn>()
    for (const col of columns) map.set(col.key, col)
    return map
  }, [columns])

  const grouped = useMemo(() => {
    const term = search.trim().toLowerCase()
    const out = new Map<ExportColumnGroup, ExportColumn[]>()
    for (const col of columns) {
      if (selected.includes(col.key)) continue
      if (
        term &&
        !t(col.label).toLowerCase().includes(term) &&
        !col.key.toLowerCase().includes(term)
      ) {
        continue
      }
      const list = out.get(col.group) ?? []
      list.push(col)
      out.set(col.group, list)
    }
    return out
  }, [columns, selected, search, t])

  const atLimit = selected.length >= maxColumns

  const add = (key: string) => {
    if (atLimit || selected.includes(key)) return
    onChange([...selected, key])
  }
  const remove = (key: string) => onChange(selected.filter((k) => k !== key))
  const move = (index: number, delta: number) => {
    const target = index + delta
    if (target < 0 || target >= selected.length) return
    const next = [...selected]
    ;[next[index], next[target]] = [next[target], next[index]]
    onChange(next)
  }

  return (
    <div className='grid gap-3 md:grid-cols-2'>
      {/* Available */}
      <div className='flex min-h-0 flex-col gap-2'>
        <div className='relative'>
          <Search className='text-muted-foreground pointer-events-none absolute top-1/2 left-2 size-4 -translate-y-1/2' />
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={t('Search columns')}
            className='pl-8'
          />
        </div>
        <ScrollArea className='h-72 rounded-md border'>
          <div className='space-y-3 p-2'>
            {GROUP_ORDER.map((group) => {
              const items = grouped.get(group)
              if (!items?.length) return null
              return (
                <div key={group} className='space-y-1'>
                  <div className='text-muted-foreground px-1 text-xs font-medium'>
                    {t(GROUP_LABELS[group])}
                  </div>
                  {items.map((col) => (
                    <button
                      key={col.key}
                      type='button'
                      disabled={atLimit}
                      onClick={() => add(col.key)}
                      className='hover:bg-accent flex w-full items-center justify-between rounded-md px-2 py-1.5 text-left text-sm disabled:cursor-not-allowed disabled:opacity-50'
                    >
                      <span className='truncate'>{t(col.label)}</span>
                      <Plus className='text-muted-foreground size-3.5 shrink-0' />
                    </button>
                  ))}
                </div>
              )
            })}
            {grouped.size === 0 && (
              <p className='text-muted-foreground p-3 text-center text-sm'>
                {t('No columns match your search')}
              </p>
            )}
          </div>
        </ScrollArea>
      </div>

      {/* Selected */}
      <div className='flex min-h-0 flex-col gap-2'>
        <div className='flex h-9 items-center justify-between px-1'>
          <span className='text-sm font-medium'>
            {t('Selected')}{' '}
            <Badge variant='secondary'>
              {selected.length} / {maxColumns}
            </Badge>
          </span>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            disabled={selected.length === 0}
            onClick={() => onChange([])}
          >
            {t('Clear')}
          </Button>
        </div>
        <ScrollArea className='h-72 rounded-md border'>
          <div className='space-y-1 p-2'>
            {selected.map((key, index) => {
              const col = byKey.get(key)
              return (
                <div
                  key={key}
                  className='bg-muted/40 flex items-center gap-1 rounded-md px-2 py-1.5 text-sm'
                >
                  <span className='text-muted-foreground w-6 shrink-0 text-xs tabular-nums'>
                    {index + 1}
                  </span>
                  <span className='flex-1 truncate'>
                    {col ? t(col.label) : key}
                  </span>
                  <Button
                    type='button'
                    variant='ghost'
                    size='icon'
                    className='size-6'
                    disabled={index === 0}
                    aria-label={t('Move up')}
                    onClick={() => move(index, -1)}
                  >
                    <ArrowUp className='size-3.5' />
                  </Button>
                  <Button
                    type='button'
                    variant='ghost'
                    size='icon'
                    className='size-6'
                    disabled={index === selected.length - 1}
                    aria-label={t('Move down')}
                    onClick={() => move(index, 1)}
                  >
                    <ArrowDown className='size-3.5' />
                  </Button>
                  <Button
                    type='button'
                    variant='ghost'
                    size='icon'
                    className='size-6'
                    aria-label={t('Remove')}
                    onClick={() => remove(key)}
                  >
                    <X className='size-3.5' />
                  </Button>
                </div>
              )
            })}
            {selected.length === 0 && (
              <p className='text-muted-foreground p-3 text-center text-sm'>
                {t('Pick at least one column to export')}
              </p>
            )}
          </div>
        </ScrollArea>
      </div>
    </div>
  )
}

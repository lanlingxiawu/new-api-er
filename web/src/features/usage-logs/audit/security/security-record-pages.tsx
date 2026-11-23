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
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

export function SecurityRecordPages<T>(props: {
  items: T[]
  children: (items: T[]) => ReactNode
}) {
  const { t } = useTranslation()
  const [page, setPage] = useState(0)
  const current = Math.min(
    page,
    Math.max(0, Math.ceil(props.items.length / 10) - 1)
  )
  const start = current * 10
  if (!props.items.length) return null
  return (
    <>
      {props.children(props.items.slice(start, start + 10))}
      <div className='flex flex-wrap items-center justify-between gap-2 pt-2'>
        <p className='text-muted-foreground text-xs'>
          {t('Showing {{start}}–{{end}} of {{count}} loaded records', {
            start: start + 1,
            end: Math.min(start + 10, props.items.length),
            count: props.items.length,
          })}
        </p>
        {props.items.length > 10 && (
          <div className='ms-auto flex items-center gap-2'>
            <Button
              variant='outline'
              size='sm'
              disabled={current === 0}
              onClick={() => setPage(current - 1)}
            >
              {t('Previous page')}
            </Button>
            <Button
              variant='outline'
              size='sm'
              disabled={start + 10 >= props.items.length}
              onClick={() => setPage(current + 1)}
            >
              {t('Next page')}
            </Button>
          </div>
        )}
      </div>
    </>
  )
}

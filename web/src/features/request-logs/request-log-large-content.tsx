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
import { useVirtualizer } from '@tanstack/react-virtual'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import { LOG_ROWS_PER_PAGE } from './request-log-content'
import { runRequestLogJob } from './request-log-worker-client'

function IndexedContent({
  content,
  offsets,
}: {
  content: string
  offsets: Uint32Array
}) {
  const { t } = useTranslation()
  const scrollRef = useRef<HTMLDivElement>(null)
  const [page, setPage] = useState(0)
  const rowCount = offsets.length - 1
  const pages = Math.ceil(rowCount / LOG_ROWS_PER_PAGE)
  const start = page * LOG_ROWS_PER_PAGE
  const virtualizer = useVirtualizer({
    count: Math.min(LOG_ROWS_PER_PAGE, rowCount - start),
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 20,
    overscan: 8,
  })

  const changePage = (next: number) => {
    scrollRef.current?.scrollTo({ top: 0, left: 0 })
    setPage(next)
  }

  return (
    <>
      <div
        ref={scrollRef}
        tabIndex={0}
        className='bg-muted/60 border-border h-72 overflow-auto rounded-lg border p-3 font-mono text-xs'
      >
        <div
          className='relative'
          style={{ height: virtualizer.getTotalSize() }}
        >
          {virtualizer.getVirtualItems().map((row) => (
            <div
              key={start + row.index}
              className='absolute top-0 left-0 h-5 w-max min-w-full whitespace-pre'
              style={{ transform: `translateY(${row.start}px)` }}
            >
              {content
                .slice(
                  offsets[start + row.index],
                  offsets[start + row.index + 1]
                )
                .replace(/[\r\n]+$/, '')}
            </div>
          ))}
        </div>
      </div>
      {pages > 1 && (
        <div className='flex items-center justify-between gap-2'>
          <Button
            variant='ghost'
            size='sm'
            disabled={page === 0}
            onClick={() => changePage(page - 1)}
          >
            {t('Previous')}
          </Button>
          <span className='text-muted-foreground text-xs'>
            {page + 1} / {pages}
          </span>
          <Button
            variant='ghost'
            size='sm'
            disabled={page + 1 === pages}
            onClick={() => changePage(page + 1)}
          >
            {t('Next')}
          </Button>
        </div>
      )}
    </>
  )
}

export function RequestLogLargeContent({ content }: { content: string }) {
  const { t } = useTranslation()
  const [result, setResult] = useState<{
    content: string
    offsets?: Uint32Array
    failed?: boolean
  }>()

  useEffect(() => {
    const controller = new AbortController()
    void runRequestLogJob({ type: 'index', content }, controller.signal).then(
      (offsets) => {
        if (!controller.signal.aborted) {
          setResult({ content, offsets: offsets as Uint32Array })
        }
      },
      () => {
        if (!controller.signal.aborted) setResult({ content, failed: true })
      }
    )
    return () => controller.abort()
  }, [content])

  return (
    <>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Large text is shown in sections. Copy or download for full-text selection and search.'
        )}
      </p>
      {result?.content === content && result.offsets ? (
        <IndexedContent content={content} offsets={result.offsets} />
      ) : (
        <p role='status' className='text-muted-foreground py-6 text-sm'>
          {result?.content === content && result.failed
            ? t(
                'Unable to display this text. Copy or download to view the complete content.'
              )
            : t('Loading...')}
        </p>
      )}
    </>
  )
}

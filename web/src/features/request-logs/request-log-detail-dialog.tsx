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
import { useQuery } from '@tanstack/react-query'
import { Copy, Download, Loader2 } from 'lucide-react'
import { memo, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

import { getRequestLogDetail } from './api'
import {
  formatRequestLogContent,
  SMALL_LOG_CONTENT_LIMIT,
} from './request-log-content'
import { RequestLogLargeContent } from './request-log-large-content'
import { runRequestLogJob } from './request-log-worker-client'

interface Props {
  id: number | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

function formatBytes(bytes?: number): string {
  if (!bytes || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit += 1
  }
  return `${value.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`
}

const Block = memo(function Block({
  title,
  content,
  note,
}: {
  title: string
  content?: string
  note?: string
}) {
  const { t } = useTranslation()

  const formatted = useMemo(() => formatRequestLogContent(content), [content])

  const handleCopy = async () => {
    if (!content) return
    try {
      await navigator.clipboard.writeText(formatted)
      toast.success(t('Copied'))
    } catch {
      toast.error(t('Copy failed'))
    }
  }

  return (
    <div className='space-y-1.5'>
      <div className='flex items-center justify-between gap-2'>
        <div className='text-sm font-medium'>{title}</div>
        <Button
          variant='ghost'
          size='sm'
          className='h-6 shrink-0 gap-1 px-2 text-xs'
          disabled={!content}
          onClick={handleCopy}
        >
          <Copy className='h-3 w-3' />
          {t('Copy')}
        </Button>
      </div>
      {note && <p className='text-muted-foreground text-xs'>{note}</p>}
      {formatted.length > SMALL_LOG_CONTENT_LIMIT ? (
        <RequestLogLargeContent content={formatted} />
      ) : (
        <pre className='bg-muted/60 border-border max-h-72 overflow-auto rounded-lg border p-3 font-mono text-xs leading-relaxed break-all whitespace-pre-wrap'>
          {formatted || '-'}
        </pre>
      )}
    </div>
  )
})

export function RequestLogDetailDialog({ id, open, onOpenChange }: Props) {
  const { t } = useTranslation()
  const [isDownloading, setIsDownloading] = useState(false)
  const downloadController = useRef<AbortController | null>(null)
  useEffect(() => {
    setIsDownloading(false)
    return () => {
      downloadController.current?.abort()
      downloadController.current = null
    }
  }, [id, open])
  const { data, isLoading, isFetching, isError, error } = useQuery({
    queryKey: ['request-log-detail', open ? id : null],
    queryFn: ({ signal }) => getRequestLogDetail(id as number, signal),
    gcTime: 0,
    enabled: open && id !== null,
  })
  const canDownload =
    open &&
    id !== null &&
    data?.id === id &&
    !isLoading &&
    !isFetching &&
    !isError &&
    !isDownloading

  const handleDownload = async () => {
    if (!canDownload || !data) return
    if (downloadController.current) return
    const controller = new AbortController()
    downloadController.current = controller
    setIsDownloading(true)
    let objectUrl: string | undefined
    let link: HTMLAnchorElement | undefined
    try {
      const blob = (await runRequestLogJob(
        { type: 'download', data },
        controller.signal
      )) as Blob
      if (controller.signal.aborted) return
      objectUrl = URL.createObjectURL(blob)
      link = document.createElement('a')
      link.href = objectUrl
      link.download = `request-log-${data.id}.json`
      document.body.appendChild(link)
      link.click()
      toast.success(t('Request log download started'))
      // Give the browser time to consume the URL; finally also runs if the dialog unmounts.
      await new Promise((resolve) => window.setTimeout(resolve, 1000))
    } catch {
      if (!controller.signal.aborted) {
        toast.error(t('Unable to download the request log. Please try again.'))
      }
    } finally {
      link?.remove()
      if (objectUrl) URL.revokeObjectURL(objectUrl)
      if (downloadController.current === controller) {
        downloadController.current = null
        setIsDownloading(false)
      }
    }
  }

  // 被截断的头部无法可靠遮蔽凭据，后端对非超级管理员不返回；说明原因，免得误以为数据丢失。
  const headersNote = (content?: string) =>
    data?.headers_withheld && !content
      ? t(
          'These headers were cut at the size limit and are hidden because credentials in them could not be masked'
        )
      : undefined

  const renderBody = () => {
    if (isLoading) {
      return (
        <div className='text-muted-foreground flex flex-1 items-center justify-center gap-2 py-12'>
          <Loader2 className='h-5 w-5 animate-spin' />
          {t('Loading...')}
        </div>
      )
    }
    if (isError) {
      // 后端已经把日志被清理之类的原因翻译好了，直接展示比通用文案有用
      return (
        <div className='text-destructive flex flex-1 items-center justify-center px-5 py-12 text-center'>
          {error instanceof Error && error.message
            ? error.message
            : t('Failed to load')}
        </div>
      )
    }
    return (
      <div className='min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4'>
        <Block
          title={t('Request Headers')}
          content={data?.request_headers}
          note={headersNote(data?.request_headers)}
        />
        <Block
          title={`${t('Request Body')} (${formatBytes(data?.request_body_size)})`}
          content={data?.request_body}
        />
        <Block
          title={t('Response Headers')}
          content={data?.response_headers}
          note={headersNote(data?.response_headers)}
        />
        <Block
          title={`${t('Response Body')} (${formatBytes(data?.response_body_size)})`}
          content={data?.response_body}
        />
      </div>
    )
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0 max-sm:h-dvh max-sm:w-screen max-sm:max-w-none max-sm:rounded-none sm:w-[92vw] sm:max-w-3xl'>
        <DialogHeader className='space-y-1 border-b px-5 py-4 pr-12'>
          <DialogTitle>{t('Request Log Detail')}</DialogTitle>
          <DialogDescription className='font-mono text-xs break-all'>
            {data?.request_id ? `Request ID: ${data.request_id}` : ' '}
          </DialogDescription>
        </DialogHeader>
        {open && renderBody()}
        <DialogFooter className='mx-0 mb-0 shrink-0 px-5 py-3'>
          <Button
            variant='outline'
            disabled={!canDownload}
            onClick={handleDownload}
          >
            {isDownloading ? (
              <Loader2 className='h-4 w-4 animate-spin' />
            ) : (
              <Download className='h-4 w-4' />
            )}
            {isDownloading ? t('Downloading...') : t('Download JSON')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

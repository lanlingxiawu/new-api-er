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
import { useTranslation } from 'react-i18next'
import { Copy, Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { getRequestLogDetail } from './api'

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

function prettify(raw?: string): string {
  if (!raw) return ''
  const trimmed = raw.trim()
  if (
    (trimmed.startsWith('{') && trimmed.endsWith('}')) ||
    (trimmed.startsWith('[') && trimmed.endsWith(']'))
  ) {
    try {
      return JSON.stringify(JSON.parse(trimmed), null, 2)
    } catch {
      return raw
    }
  }
  return raw
}

function Block({ title, content }: { title: string; content?: string }) {
  const { t } = useTranslation()

  const handleCopy = async () => {
    if (!content) return
    try {
      await navigator.clipboard.writeText(prettify(content))
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
      <pre className='bg-muted/60 border-border max-h-72 overflow-auto rounded-lg border p-3 font-mono text-xs leading-relaxed break-all whitespace-pre-wrap'>
        {content ? prettify(content) : '-'}
      </pre>
    </div>
  )
}

export function RequestLogDetailDialog({ id, open, onOpenChange }: Props) {
  const { t } = useTranslation()
  const { data, isLoading, isError, error } = useQuery({
    queryKey: ['request-log-detail', id],
    queryFn: () => getRequestLogDetail(id as number),
    enabled: open && id !== null,
  })

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
        <Block title={t('Request Headers')} content={data?.request_headers} />
        <Block
          title={`${t('Request Body')} (${formatBytes(data?.request_body_size)})`}
          content={data?.request_body}
        />
        <Block title={t('Response Headers')} content={data?.response_headers} />
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
        {renderBody()}
      </DialogContent>
    </Dialog>
  )
}

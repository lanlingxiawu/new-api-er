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
    <div className='space-y-1'>
      <div className='flex items-center justify-between'>
        <div className='text-sm font-medium'>{title}</div>
        <Button
          variant='ghost'
          size='sm'
          className='h-7 gap-1 text-xs'
          disabled={!content}
          onClick={handleCopy}
        >
          <Copy className='h-3.5 w-3.5' />
          {t('Copy')}
        </Button>
      </div>
      <pre className='bg-muted max-h-72 overflow-auto rounded-md p-3 text-xs whitespace-pre-wrap break-all'>
        {content ? prettify(content) : '-'}
      </pre>
    </div>
  )
}

export function RequestLogDetailDialog({ id, open, onOpenChange }: Props) {
  const { t } = useTranslation()
  const { data, isLoading, isError } = useQuery({
    queryKey: ['request-log-detail', id],
    queryFn: () => getRequestLogDetail(id as number),
    enabled: open && id !== null,
  })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='max-h-[90vh] max-w-4xl overflow-y-auto'>
        <DialogHeader>
          <DialogTitle>{t('Request Log Detail')}</DialogTitle>
          <DialogDescription>
            {data?.request_id ? `Request ID: ${data.request_id}` : ''}
          </DialogDescription>
        </DialogHeader>
        {isLoading ? (
          <div className='text-muted-foreground flex items-center justify-center gap-2 py-12'>
            <Loader2 className='h-5 w-5 animate-spin' />
            {t('Loading...')}
          </div>
        ) : isError ? (
          <div className='text-destructive py-12 text-center'>
            {t('Failed to load')}
          </div>
        ) : (
          <div className='space-y-4'>
            <Block
              title={t('Request Headers')}
              content={data?.request_headers}
            />
            <Block
              title={`${t('Request Body')} (${formatBytes(data?.request_body_size)})`}
              content={data?.request_body}
            />
            <Block
              title={t('Response Headers')}
              content={data?.response_headers}
            />
            <Block
              title={`${t('Response Body')} (${formatBytes(data?.response_body_size)})`}
              content={data?.response_body}
            />
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}

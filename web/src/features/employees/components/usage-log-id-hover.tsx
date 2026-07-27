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
import { useEffect, useState } from 'react'
import { Info } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
} from '@/components/ui/hover-card'
import { Skeleton } from '@/components/ui/skeleton'
import type { UsageLog } from '@/features/usage-logs/types'
import { getUsageLogById } from '../api'

const usageLogPreviewCache = new Map<number, UsageLog | null>()

function formatTs(ts: number) {
  if (!ts) return '-'
  return new Date(ts * 1000).toLocaleString()
}

function UsageLogPreviewContent({
  log,
  loading,
}: {
  log?: UsageLog | null
  loading: boolean
}) {
  const { t } = useTranslation()

  if (loading) {
    return (
      <div className='space-y-2'>
        <Skeleton className='h-4 w-36' />
        <Skeleton className='h-3 w-48' />
        <Skeleton className='h-3 w-40' />
      </div>
    )
  }

  if (!log) {
    return (
      <div className='text-muted-foreground text-sm'>
        {t('No matching consumption log found')}
      </div>
    )
  }

  const fields = [
    { label: t('Time'), value: formatTs(log.created_at) },
    { label: t('User ID'), value: `#${log.user_id}` },
    { label: t('Username'), value: log.username || '-' },
    { label: t('Token'), value: log.token_name || '-' },
    { label: t('Model'), value: log.model_name || '-' },
    {
      label: t('Channel'),
      value: log.channel_name || (log.channel ? `#${log.channel}` : '-'),
    },
    { label: t('Group'), value: log.group || '-' },
    { label: t('Request ID'), value: log.request_id || '-' },
  ]

  return (
    <div className='max-h-[420px] space-y-2 overflow-y-auto pr-1'>
      <div className='flex items-center justify-between gap-3'>
        <div className='font-medium'>{t('Consumption Log')}</div>
        <Badge variant='outline'>#{log.id}</Badge>
      </div>
      <div className='grid gap-1.5 text-xs'>
        {fields.map((field) => (
          <div
            key={field.label}
            className='grid grid-cols-[88px_minmax(0,1fr)] gap-2'
          >
            <span className='text-muted-foreground'>{field.label}</span>
            <span className='break-words font-mono leading-5'>
              {field.value}
            </span>
          </div>
        ))}
      </div>
      <div className='grid grid-cols-3 gap-2 border-t pt-2 text-xs'>
        <div>
          <div className='text-muted-foreground'>{t('Quota')}</div>
          <div className='font-mono'>{log.quota ?? 0}</div>
        </div>
        <div>
          <div className='text-muted-foreground'>{t('Prompt')}</div>
          <div className='font-mono'>{log.prompt_tokens ?? 0}</div>
        </div>
        <div>
          <div className='text-muted-foreground'>{t('Completion')}</div>
          <div className='font-mono'>{log.completion_tokens ?? 0}</div>
        </div>
      </div>
    </div>
  )
}

export function UsageLogIdHover({ logId }: { logId?: number | null }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [log, setLog] = useState<UsageLog | null | undefined>(
    logId ? usageLogPreviewCache.get(logId) : undefined
  )
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (!open || !logId) return
    if (usageLogPreviewCache.has(logId)) {
      setLog(usageLogPreviewCache.get(logId) ?? null)
      return
    }
    let cancelled = false
    setLoading(true)
    getUsageLogById(logId)
      .then((res) => {
        if (cancelled) return
        const next = res.success ? (res.data ?? null) : null
        usageLogPreviewCache.set(logId, next)
        setLog(next)
      })
      .catch(() => {
        if (!cancelled) setLog(null)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [logId, open])

  if (!logId) return <span className='text-muted-foreground'>-</span>

  return (
    <HoverCard open={open} onOpenChange={setOpen}>
      <HoverCardTrigger
        delay={1000}
        render={
          <button
            type='button'
            className='hover:text-primary inline-flex items-center gap-1 rounded-sm font-mono text-xs underline-offset-2 hover:underline'
            aria-label={t('View consumption log details')}
          />
        }
      >
        #{logId}
        <Info className='size-3' />
      </HoverCardTrigger>
      <HoverCardContent
        align='start'
        className='bg-popover/100 w-[min(520px,calc(100vw-32px))] border shadow-lg backdrop-blur-none'
      >
        <UsageLogPreviewContent log={log} loading={loading} />
      </HoverCardContent>
    </HoverCard>
  )
}

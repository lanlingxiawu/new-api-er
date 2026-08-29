import { Check, Copy } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { StatusBadge, type StatusBadgeProps } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import dayjs from '@/lib/dayjs'
import { formatLogQuota, formatUseTime } from '@/lib/format'

import { getLogTypeConfig } from '../lib/utils'
import type { UpstreamLogItem } from '../types'

export function DetailRow({
  label,
  value,
  mono = false,
}: {
  label: string
  value: React.ReactNode
  mono?: boolean
}) {
  return (
    <div className='grid min-w-0 grid-cols-[7rem_minmax(0,1fr)] gap-3 text-sm'>
      <span className='text-muted-foreground'>{label}</span>
      <span className={mono ? 'min-w-0 font-mono text-xs break-all' : ''}>
        {value}
      </span>
    </div>
  )
}

export function CopyValue({ value }: { value: string }) {
  const { t } = useTranslation()
  const { copiedText, copyToClipboard } = useCopyToClipboard({ notify: false })
  const copied = copiedText === value

  return (
    <span className='flex min-w-0 items-start gap-1.5'>
      <span className='min-w-0 break-all'>{value || '-'}</span>
      {value && (
        <Button
          type='button'
          variant='ghost'
          size='icon'
          className='size-6 shrink-0'
          aria-label={t('Copy')}
          onClick={() => copyToClipboard(value)}
        >
          {copied ? (
            <Check className='size-3.5' />
          ) : (
            <Copy className='size-3.5' />
          )}
        </Button>
      )}
    </span>
  )
}

/**
 * Normalized upstream log fields. Shared by the standalone details dialog and
 * the side-by-side compare pane so both read identically — an admin comparing
 * a local record against its upstream counterpart should not have to re-learn
 * a second layout.
 */
export function UpstreamLogDetailBody({ item }: { item: UpstreamLogItem }) {
  const { t } = useTranslation()
  const typeConfig = getLogTypeConfig(item.type)
  const otherEntries = item.other
    ? Object.entries(item.other).filter(([, value]) => value != null)
    : []

  return (
    <div className='space-y-3'>
      <section className='space-y-2'>
        <h3 className='text-sm font-medium'>{t('Overview')}</h3>
        <div className='bg-muted/35 space-y-2 rounded-lg p-3'>
          <DetailRow
            label={t('Type')}
            value={
              <StatusBadge
                label={t(typeConfig.label)}
                variant={typeConfig.color as StatusBadgeProps['variant']}
                size='sm'
                copyable={false}
              />
            }
          />
          <DetailRow
            label={t('Created')}
            value={dayjs.unix(item.created_at).format('YYYY-MM-DD HH:mm:ss')}
          />
          <DetailRow label={t('Upstream Log ID')} value={item.id} mono />
          <DetailRow label={t('Model')} value={item.model_name || '-'} />
          <DetailRow
            label={t('Stream')}
            value={item.is_stream ? t('Yes') : t('No')}
          />
          {item.token_name && (
            <DetailRow label={t('Token')} value={item.token_name} />
          )}
        </div>
      </section>

      <section className='space-y-2'>
        <h3 className='text-sm font-medium'>{t('Request')}</h3>
        <div className='bg-muted/35 space-y-2 rounded-lg p-3'>
          <DetailRow
            label={t('Request ID')}
            value={<CopyValue value={item.request_id} />}
            mono
          />
          {item.upstream_request_id && (
            <DetailRow
              label={t('Upstream Request ID')}
              value={<CopyValue value={item.upstream_request_id} />}
              mono
            />
          )}
          <DetailRow
            label={t('Tokens')}
            value={`${item.prompt_tokens} / ${item.completion_tokens}`}
          />
          <DetailRow label={t('Quota')} value={formatLogQuota(item.quota)} />
          <DetailRow
            label={t('Duration')}
            value={formatUseTime(item.use_time)}
          />
        </div>
      </section>

      {item.content && (
        <section className='space-y-2'>
          <h3 className='text-sm font-medium'>{t('Error')}</h3>
          <p className='bg-destructive/10 text-destructive rounded-lg p-3 text-sm break-all'>
            {item.content}
          </p>
        </section>
      )}

      {otherEntries.length > 0 && (
        <section className='space-y-2'>
          <h3 className='text-sm font-medium'>{t('Additional Information')}</h3>
          <div className='bg-muted/35 space-y-2 rounded-lg p-3'>
            {otherEntries.map(([key, value]) => (
              <DetailRow key={key} label={key} value={String(value)} mono />
            ))}
          </div>
        </section>
      )}
    </div>
  )
}

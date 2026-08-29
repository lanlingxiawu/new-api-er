import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { StatusBadge, type StatusBadgeProps } from '@/components/status-badge'

import { getLogTypeConfig } from '../lib/utils'
import type { UpstreamLogItem } from '../types'
import { UpstreamLogDetailBody } from './upstream-log-detail-body'

export function UpstreamLogDetailsDialog({
  item,
  open,
  onOpenChange,
}: {
  item: UpstreamLogItem | null
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  if (!item) return null

  const typeConfig = getLogTypeConfig(item.type)

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={
        <span className='flex items-center gap-2'>
          {t('Upstream Log Details')}
          <StatusBadge
            label={t(typeConfig.label)}
            variant={typeConfig.color as StatusBadgeProps['variant']}
            size='sm'
          />
        </span>
      }
      description={t(
        'View normalized fields returned by the upstream instance.'
      )}
      contentClassName='sm:max-w-2xl'
      contentHeight='min(68dvh, 620px)'
      bodyClassName='space-y-3 sm:pr-3'
    >
      <UpstreamLogDetailBody item={item} />
    </Dialog>
  )
}

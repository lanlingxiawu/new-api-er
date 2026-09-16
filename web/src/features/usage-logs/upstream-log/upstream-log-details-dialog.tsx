import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { StatusBadge, type StatusBadgeProps } from '@/components/status-badge'

import { LogDetailBody } from '../components/dialogs/log-detail-body'
import { upstreamItemToUsageLog } from '../lib/upstream-log-item'
import { getLogTypeConfig } from '../lib/utils'
import type { UpstreamLogItem } from '../types'

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
      {/* 与本站日志详情共用同一个主体，排版一致；上游日志不挂载本站的流式诊断。 */}
      <LogDetailBody
        log={upstreamItemToUsageLog(item)}
        isAdmin
        showStreamDiagnostic={false}
      />
    </Dialog>
  )
}

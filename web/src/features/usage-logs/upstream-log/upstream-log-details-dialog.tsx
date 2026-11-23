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
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { StatusBadge, type StatusBadgeProps } from '@/components/status-badge'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import { LogDetailBody } from '../components/dialogs/log-detail-body'
import type { LogDetailSection } from '../components/dialogs/log-detail-grid'
import { hasLogDetailSections } from '../components/dialogs/log-detail-sections'
import { isTieredBillingLog, parseLogOther } from '../lib/format'
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
  const [section, setSection] = useState<LogDetailSection>('billing')
  useEffect(() => {
    setSection('billing')
  }, [item, open])
  if (!item) return null
  const log = upstreamItemToUsageLog(item)
  const showSections = hasLogDetailSections(log, { isAdmin: true })
  const detailContent = (
    <LogDetailBody
      log={log}
      isAdmin
      showStreamDiagnostic={false}
      section={showSections ? section : undefined}
    />
  )

  const typeConfig = getLogTypeConfig(item.type)

  return (
    <Tabs
      value={section}
      onValueChange={(value) => setSection(value as LogDetailSection)}
      className='contents'
    >
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
        navigation={
          showSections && (
            <TabsList className='h-9 w-full sm:w-auto'>
              <TabsTrigger value='billing'>{t('Usage & billing')}</TabsTrigger>
              <TabsTrigger value='diagnostics'>
                {t('Request diagnostics')}
              </TabsTrigger>
            </TabsList>
          )
        }
        contentClassName={
          isTieredBillingLog(log.type, parseLogOther(log.other))
            ? 'sm:max-w-4xl lg:max-w-5xl'
            : 'sm:max-w-3xl'
        }
        scrollAreaClassName='max-h-[min(72dvh,720px)]'
        bodyClassName='space-y-3 sm:pr-3'
      >
        {showSections
          ? (['billing', 'diagnostics'] as const).map((value) => (
              <TabsContent key={value} value={value}>
                {value === section && detailContent}
              </TabsContent>
            ))
          : detailContent}
      </Dialog>
    </Tabs>
  )
}

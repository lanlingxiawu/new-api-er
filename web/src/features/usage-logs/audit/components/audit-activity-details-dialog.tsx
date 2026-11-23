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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { loginMethodLabel } from '@/features/security/components/login-session-utils'
import dayjs from '@/lib/dayjs'

import { DetailRow } from '../../components/dialogs/log-detail-layout'
import type { AuditActivity } from '../api'
import { activityChanges, activityLabel, activitySource } from '../lib/activity'

export function AuditActivityDetailsDialog({
  entry,
  accountName,
  accountLabel = 'Username',
}: {
  entry: AuditActivity
  accountName?: string
  accountLabel?: 'Username' | 'Customer'
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const changes = activityChanges(entry, t)
  return (
    <Dialog
      open={open}
      onOpenChange={setOpen}
      title={t('Log Details')}
      trigger={
        <Button variant='ghost' size='sm' className='h-7 px-2'>
          {t('Details')}
        </Button>
      }
      contentClassName='sm:max-w-xl'
      bodyClassName='space-y-3'
    >
      <p className='text-sm font-medium'>{activityLabel(entry, t)}</p>
      <div className='flex flex-wrap items-center gap-2 text-xs'>
        <StatusBadge
          label={entry.success ? t('Success') : t('Failed')}
          variant={entry.success ? 'success' : 'danger'}
          copyable={false}
        />
        <span className='text-muted-foreground tabular-nums'>
          {dayjs.unix(entry.created_at).format('YYYY-MM-DD HH:mm:ss')}
        </span>
      </div>
      {accountName && <DetailRow label={t(accountLabel)} value={accountName} />}
      {(entry.target?.name || entry.target?.id) && (
        <DetailRow
          label={t('Target')}
          value={
            <span>
              {entry.target.name}
              {entry.target.id && (
                <span className='text-muted-foreground'>
                  {' '}
                  {t('(ID: {{id}})', { id: entry.target.id })}
                </span>
              )}
            </span>
          }
        />
      )}
      {changes && <p className='text-muted-foreground text-sm'>{changes}</p>}
      <DetailRow
        label='IP'
        value={
          <span className='inline-flex items-center gap-1'>
            {entry.ip || '—'}
            {entry.ip && <CopyButton value={entry.ip} className='size-5' />}
          </span>
        }
      />
      <DetailRow
        label={t('Client')}
        value={t(entry.client || 'Unknown client')}
      />
      <DetailRow
        label={t('Operation source')}
        value={activitySource(entry.source, t)}
      />
      {entry.login_method && (
        <DetailRow
          label={t('Sign-in method')}
          value={loginMethodLabel(entry.login_method, t)}
        />
      )}
      {entry.request_id && (
        <DetailRow
          label={t('Request ID')}
          mono
          value={
            <span className='inline-flex max-w-full items-center gap-1'>
              <span className='min-w-0 break-all'>{entry.request_id}</span>
              <CopyButton
                value={entry.request_id}
                className='size-5 shrink-0'
              />
            </span>
          }
        />
      )}
    </Dialog>
  )
}

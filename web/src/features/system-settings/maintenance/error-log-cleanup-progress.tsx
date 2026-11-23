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
import { useTranslation } from 'react-i18next'

import { formatTimestampToDate } from '@/lib/format'

import type { CleanupRun } from './data-cleanup-api'

export function ErrorLogCleanupProgress(props: {
  run: CleanupRun
  stale: boolean
  lastReadAt: number
}) {
  const { t } = useTranslation()
  const progress = props.run.progress
  let message = ''
  if (props.stale) {
    message = t(
      'Cleanup status could not be refreshed. Retry to see the latest progress.'
    )
  } else if (props.run.status === 'pending') {
    message = t('Cleanup is waiting for a worker.')
  } else if (props.run.status === 'running') {
    message = t('Cleanup is running in the background.')
    if (progress?.lease_valid === false) {
      message = t('Cleanup has lost its heartbeat. Waiting for task recovery.')
    } else {
      switch (progress?.phase) {
        case 'preparing':
          message = t('Preparing error log cleanup.')
          break
        case 'locating':
          message = t('Locating the next available log records.')
          break
        case 'retrying':
          message = t('Cleanup is retrying after a temporary delay.')
          break
        case 'saving':
          message = t('Saving cleanup progress.')
          break
      }
    }
  }
  return (
    <>
      {message && <p>{message}</p>}
      {props.stale && props.lastReadAt > 0 && (
        <p className='text-muted-foreground'>
          {t('Last status update: {{date}}', {
            date: formatTimestampToDate(props.lastReadAt, 'milliseconds'),
          })}
        </p>
      )}
      {progress && progress.checkpoint_at > 0 && (
        <p className='text-muted-foreground'>
          {t('Progress saved at {{date}}.', {
            date: formatTimestampToDate(progress.checkpoint_at, 'seconds'),
          })}
        </p>
      )}
    </>
  )
}

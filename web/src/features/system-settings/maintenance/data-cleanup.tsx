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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { DateTimePicker } from '@/components/datetime-picker'
import { Button } from '@/components/ui/button'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { formatTimestampToDate } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  getDataCleanup,
  previewDataCleanup,
  startDataCleanup,
  type CleanupKind,
  type CleanupRun,
} from './data-cleanup-api'

export function FinancialDetailCleanup() {
  const { t } = useTranslation()
  const isRoot = useAuthStore(
    (state) => state.auth.user?.role === ROLE.SUPER_ADMIN
  )
  const [kind, setKind] = useState<CleanupKind>('ledger')
  if (!isRoot) return null
  return (
    <div className='flex min-w-0 flex-col gap-4'>
      <ToggleGroup
        value={[kind]}
        onValueChange={(values) => {
          if (values[0] === 'ledger' || values[0] === 'commission') {
            setKind(values[0])
          }
        }}
        variant='outline'
        aria-label={t('Cleanup target')}
      >
        <ToggleGroupItem value='ledger'>{t('Ledger details')}</ToggleGroupItem>
        <ToggleGroupItem value='commission'>
          {t('Commission details')}
        </ToggleGroupItem>
      </ToggleGroup>
      <DataCleanupControl key={kind} kind={kind} />
    </div>
  )
}

export function DataCleanupControl(props: { kind: CleanupKind }) {
  const { t } = useTranslation()
  const labelId = useId()
  const client = useQueryClient()
  const [date, setDate] = useState<Date | undefined>(
    () =>
      new Date(Date.now() - (props.kind === 'error_logs' ? 30 : 90) * 86400000)
  )
  const [preview, setPreview] = useState<CleanupRun | null>(null)
  const [runId, setRunId] = useState<string>()
  const queryKey = ['data-cleanup', props.kind, runId]
  const task = useQuery({
    queryKey,
    queryFn: () => getDataCleanup(props.kind, runId),
    refetchInterval: (query) => {
      const status = query.state.data?.status
      return status === 'pending' || status === 'running' ? 1500 : false
    },
  })
  const previewMutation = useMutation({
    mutationFn: (before: number) => previewDataCleanup(props.kind, before),
    onSuccess: setPreview,
  })
  const startMutation = useMutation({
    mutationFn: startDataCleanup,
    onSuccess: (run) => {
      client.setQueryData(['data-cleanup', props.kind, run.id], run)
      setRunId(run.id)
      setPreview(null)
      toast.success(t('Cleanup started. You can leave this page.'))
    },
  })
  const active =
    task.data?.status === 'pending' || task.data?.status === 'running'
  const dateValid = date !== undefined && Number.isFinite(date.getTime())
  let title = t('Clean ledger details')
  if (props.kind === 'commission') title = t('Clean commission details')
  if (props.kind === 'error_logs') title = t('Clean error usage logs')
  const description =
    props.kind === 'error_logs'
      ? t(
          'Only error usage logs are deleted. Consumption and audit logs are kept. Keep at least the latest 24 hours.'
        )
      : t(
          'Balances, totals and manual adjustments are kept. Deleted details cannot be restored or exported. Keep at least the latest 30 days.'
        )
  let countText = ''
  if (preview) {
    countText = preview.count_exact
      ? t('{{count}} records match this cleanup.', { count: preview.matched })
      : t('At least {{count}} records match this cleanup.', {
          count: preview.matched,
        })
  }
  return (
    <div
      role='group'
      aria-labelledby={labelId}
      className='flex min-w-0 flex-col gap-4'
    >
      <span id={labelId} className='text-sm font-medium'>
        {title}
      </span>
      <p className='text-muted-foreground text-xs'>{description}</p>
      <DateTimePicker value={date} onChange={setDate} />
      <div className='flex flex-wrap gap-3'>
        <Button
          type='button'
          variant='destructive'
          disabled={
            !dateValid ||
            active ||
            task.isPending ||
            previewMutation.isPending ||
            startMutation.isPending
          }
          onClick={() => {
            if (dateValid) {
              previewMutation.mutate(Math.floor(date.getTime() / 1000))
            }
          }}
        >
          {previewMutation.isPending ? t('Loading...') : t('Preview cleanup')}
        </Button>
        {task.isError && (
          <Button
            type='button'
            variant='outline'
            onClick={() => void task.refetch()}
          >
            {t('Retry')}
          </Button>
        )}
      </div>
      {task.data && (
        <div role='status' className='rounded-md border p-3 text-sm'>
          {active && <p>{t('Cleanup is running in the background.')}</p>}
          {task.data.status === 'succeeded' && <p>{t('Cleanup completed.')}</p>}
          {task.data.status === 'failed' && (
            <p className='text-destructive'>
              {t(
                'Cleanup stopped. Completed deletions are kept. Preview again to clean the remaining records.'
              )}
            </p>
          )}
          <p className='text-muted-foreground'>
            {props.kind === 'error_logs' && task.data.status === 'failed'
              ? t('At least {{count}} records deleted.', {
                  count: task.data.deleted,
                })
              : t('{{count}} records deleted.', { count: task.data.deleted })}
          </p>
          <p className='text-muted-foreground'>
            {t('Before {{date}}', {
              date: formatTimestampToDate(task.data.before, 'seconds'),
            })}
          </p>
        </div>
      )}
      <ConfirmDialog
        open={preview !== null}
        onOpenChange={(open) => {
          if (!open && !startMutation.isPending) setPreview(null)
        }}
        title={title}
        destructive
        isLoading={startMutation.isPending}
        confirmText={t('Confirm deletion')}
        desc={
          <div className='space-y-2'>
            <p>
              {t(
                'Permanently delete records before {{date}} (your local time).',
                {
                  date: preview
                    ? formatTimestampToDate(preview.before, 'seconds')
                    : '',
                }
              )}
            </p>
            <p>{countText}</p>
            <p>{description}</p>
          </div>
        }
        handleConfirm={() => {
          if (preview) startMutation.mutate(preview.id)
        }}
      />
    </div>
  )
}

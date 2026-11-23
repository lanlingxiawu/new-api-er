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
  startErrorLogCleanup,
  type CleanupKind,
  type CleanupRun,
} from './data-cleanup-api'
import { ErrorLogCleanupProgress } from './error-log-cleanup-progress'

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
      new Date(Date.now() - (props.kind === 'error_logs' ? 30 : 3) * 86400000)
  )
  const [confirmation, setConfirmation] = useState<{
    before: number
    run?: CleanupRun
  } | null>(null)
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
    onSuccess: (run) => setConfirmation({ before: run.before, run }),
  })
  const startMutation = useMutation({
    mutationFn: (request: { before: number; run?: CleanupRun }) =>
      request.run
        ? startDataCleanup(request.run.id, request.run.status === 'failed')
        : startErrorLogCleanup(request.before),
    onMutate: () =>
      client.cancelQueries({ queryKey: ['data-cleanup', props.kind] }),
    onSuccess: (run) => {
      client.setQueryData(['data-cleanup', props.kind, run.id], run)
      setRunId(run.id)
      setConfirmation(null)
      toast.success(t('Cleanup started. You can leave this page.'))
    },
  })
  const active =
    task.data?.status === 'pending' || task.data?.status === 'running'
  const dateValid = date !== undefined && Number.isFinite(date.getTime())
  let actionLabel = t('Preview cleanup')
  if (props.kind === 'error_logs') actionLabel = t('Clean error usage logs')
  if (previewMutation.isPending) actionLabel = t('Loading...')
  let stoppedMessage = t(
    'Cleanup stopped. Completed deletions are kept. Preview again to clean the remaining records.'
  )
  if (props.kind === 'error_logs') {
    stoppedMessage = t(
      'Cleanup stopped. Completed deletions are kept. You can start a new cleanup.'
    )
  }
  if (task.data?.can_resume) {
    stoppedMessage = t(
      'Cleanup stopped. Completed deletions are kept. You can continue from saved progress.'
    )
  }
  let title = t('Clean ledger details')
  if (props.kind === 'commission') title = t('Clean commission details')
  if (props.kind === 'error_logs') title = t('Clean error usage logs')
  const description =
    props.kind === 'error_logs'
      ? t(
          'Only error usage logs are deleted. Consumption and audit logs are kept. Keep at least the latest 24 hours.'
        )
      : t(
          'Balances, totals and manual adjustments are kept. Deleted details cannot be restored or exported. Keep at least the latest 3 days.'
        )
  let countText = ''
  const preview = confirmation?.run
  if (preview) {
    if (preview.status === 'failed') {
      countText = t(
        'Continue from saved progress. The original cutoff date and cleanup range are kept.'
      )
    } else if (!preview.count_exact && preview.matched === 0) {
      countText = t(
        'No matches found in this preview. The full cleanup may find more records.'
      )
    } else {
      countText = preview.count_exact
        ? t('{{count}} records match this cleanup.', { count: preview.matched })
        : t('At least {{count}} records match this cleanup.', {
            count: preview.matched,
          })
    }
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
            task.isError ||
            previewMutation.isPending ||
            startMutation.isPending
          }
          onClick={() => {
            if (dateValid) {
              const before = Math.floor(date.getTime() / 1000)
              if (props.kind === 'error_logs') {
                setConfirmation({ before })
              } else {
                previewMutation.mutate(before)
              }
            }
          }}
        >
          {actionLabel}
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
        {task.data?.can_resume && !active && !task.isError && (
          <Button
            type='button'
            variant='outline'
            disabled={previewMutation.isPending || startMutation.isPending}
            onClick={() => {
              if (task.data) {
                setConfirmation({ before: task.data.before, run: task.data })
              }
            }}
          >
            {t('Continue cleanup')}
          </Button>
        )}
      </div>
      {task.isError && !task.data && (
        <p role='status' className='text-muted-foreground text-sm'>
          {t(
            'Cleanup status could not be refreshed. Retry to see the latest progress.'
          )}
        </p>
      )}
      {task.data && (
        <div role='status' className='rounded-md border p-3 text-sm'>
          {props.kind === 'error_logs' ? (
            <ErrorLogCleanupProgress
              run={task.data}
              stale={task.isError}
              lastReadAt={task.dataUpdatedAt}
            />
          ) : (
            active && <p>{t('Cleanup is running in the background.')}</p>
          )}
          {!task.isError && task.data.status === 'succeeded' && (
            <p>{t('Cleanup completed.')}</p>
          )}
          {task.data.status === 'failed' && (
            <p className='text-destructive'>{stoppedMessage}</p>
          )}
          <p className='text-muted-foreground'>
            {props.kind === 'error_logs'
              ? t('At least {{count}} records deleted.', {
                  count: task.data.deleted,
                })
              : t('{{count}} records deleted.', { count: task.data.deleted })}
          </p>
          {props.kind === 'error_logs' && (
            <p className='text-muted-foreground'>
              {t('{{count}} records scanned.', { count: task.data.scanned })}
            </p>
          )}
          <p className='text-muted-foreground'>
            {t('Before {{date}}', {
              date: formatTimestampToDate(task.data.before, 'seconds'),
            })}
          </p>
        </div>
      )}
      <ConfirmDialog
        open={confirmation !== null}
        onOpenChange={(open) => {
          if (!open && !startMutation.isPending) setConfirmation(null)
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
                  date: confirmation
                    ? formatTimestampToDate(confirmation.before, 'seconds')
                    : '',
                }
              )}
            </p>
            {countText && <p>{countText}</p>}
            <p>{description}</p>
          </div>
        }
        handleConfirm={() => {
          if (confirmation) startMutation.mutate(confirmation)
        }}
      />
    </div>
  )
}

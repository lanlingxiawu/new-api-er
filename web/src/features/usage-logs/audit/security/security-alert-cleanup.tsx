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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { handleServerError } from '@/lib/handle-server-error'

import { cleanupAlerts, deleteAlert, type SecurityAlert } from './api'

export function SecurityAlertCleanup(props: {
  alert?: SecurityAlert
  onDeleted?: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [date, setDate] = useState('')
  const [more, setMore] = useState(false)
  const cutoff = date
    ? Math.floor(new Date(`${date}T23:59:59`).getTime() / 1000)
    : 0
  const valid =
    !!props.alert ||
    (Number.isFinite(cutoff) && cutoff > 0 && cutoff <= Date.now() / 1000)
  const mutation = useMutation({
    mutationFn: () =>
      props.alert
        ? deleteAlert(props.alert.id, props.alert.version)
        : cleanupAlerts(cutoff),
    onSuccess: (result) => {
      toast.success(
        t('Alerts queued for deletion: {{count}}.', { count: result.queued })
      )
      setMore(result.has_more)
      setOpen(false)
      void queryClient.invalidateQueries({ queryKey: ['audit-security'] })
      void queryClient.invalidateQueries({ queryKey: ['audit'] })
      props.onDeleted?.()
    },
    onError: (error) =>
      handleServerError(
        error,
        t('The alert may have changed. Refresh and try again.')
      ),
  })
  const title = props.alert ? t('Delete alert') : t('Clean up closed alerts')
  return (
    <>
      <Button
        type='button'
        variant='outline'
        className='text-destructive'
        onClick={() => setOpen(true)}
      >
        {title}
      </Button>
      {more && (
        <span role='status' className='text-muted-foreground text-xs'>
          {t('More closed alerts remain. Run cleanup again to continue.')}
        </span>
      )}
      <ConfirmDialog
        open={open}
        onOpenChange={(value) => {
          if (!mutation.isPending) setOpen(value)
        }}
        title={props.alert ? `${title} #${props.alert.id}` : title}
        destructive
        disabled={!valid}
        isLoading={mutation.isPending}
        confirmText={mutation.isPending ? t('Loading...') : t('Delete')}
        handleConfirm={() => {
          if (valid && !mutation.isPending) mutation.mutate()
        }}
        desc={
          props.alert
            ? t(
                'Permanently delete this alert, its evidence, investigation history and delivery records? Original audit logs are retained.'
              )
            : t(
                'Delete up to 100 visible resolved or false-positive alerts closed on or before the selected date. List filters do not apply. Original audit logs are retained.'
              )
        }
      >
        {!props.alert && (
          <Field>
            <FieldLabel htmlFor='alert-cleanup-date'>
              {t('Closed on or before')}
            </FieldLabel>
            <Input
              id='alert-cleanup-date'
              type='date'
              value={date}
              disabled={mutation.isPending}
              onChange={(event) => setDate(event.target.value)}
            />
            {!!date && !valid && (
              <p role='alert' className='text-destructive text-sm'>
                {t('Choose a date before today.')}
              </p>
            )}
          </Field>
        )}
      </ConfirmDialog>
    </>
  )
}

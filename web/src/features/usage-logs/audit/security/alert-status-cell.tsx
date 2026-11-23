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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { MultiSelect } from '@/components/multi-select'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import {
  Popover,
  PopoverContent,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
} from '@/components/ui/popover'
import { Textarea } from '@/components/ui/textarea'
import {
  isCustomerAccessDenied,
  saveCustomerAlert,
} from '@/features/customer-console/audit-api'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import { AlertAssignees } from './alert-assignees'
import { archiveAlert, securityGet, saveAlert, type SecurityAlert } from './api'
import {
  alertFormSchema,
  alertStatuses,
  type AlertFormValues,
} from './lib/alert-form'
import { securityConclusions } from './lib/security-display'

export function AlertStatusCell(props: {
  alert: Pick<
    SecurityAlert,
    | 'id'
    | 'status'
    | 'version'
    | 'assignee_id'
    | 'assignees'
    | 'manual_assignee_ids'
    | 'conclusion'
    | 'archived_at'
  >
  customerID?: number
  onAccessDenied?: () => void
  onProcessed?: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [version, setVersion] = useState(props.alert.version)
  const user = useAuthStore((state) => state.auth.user)
  const initialAssignees =
    props.alert.manual_assignee_ids ??
    props.alert.assignees
      ?.filter((person) => person.source === 'manual')
      .map((person) => person.id) ??
    (props.alert.assignee_id ? [props.alert.assignee_id] : [])
  const [selectedAssignees, setSelectedAssignees] = useState(initialAssignees)
  const [conclusion, setConclusion] = useState(props.alert.conclusion ?? '')
  const assignees = useQuery({
    queryKey: [
      'audit-security',
      user?.id,
      user?.role,
      user?.permissions,
      'assignees',
      props.alert.id,
    ],
    queryFn: ({ signal }) =>
      securityGet<{ id: number; username: string; role: number }[]>(
        `assignees?alert_id=${props.alert.id}`,
        signal
      ),
    enabled: open && props.customerID === undefined,
    gcTime: 0,
    retry: false,
  })
  const form = useForm<AlertFormValues>({
    resolver: zodResolver(alertFormSchema),
    defaultValues: { status: 'open', note: '' },
  })
  const save = useMutation({
    mutationFn: (values: AlertFormValues) => {
      if (props.customerID !== undefined) {
        return saveCustomerAlert(props.customerID, props.alert.id, {
          version,
          status: values.status,
          note: values.note,
          conclusion,
        }).then(() => undefined)
      }
      if (
        selectedAssignees.join(',') === initialAssignees.join(',') &&
        conclusion === (props.alert.conclusion ?? '')
      ) {
        return saveAlert(props.alert.id, version, values.status, values.note)
      }
      return saveAlert(props.alert.id, version, values.status, values.note, {
        assignee_ids: selectedAssignees,
        conclusion,
      })
    },
    onSuccess: async () => {
      toast.success(t('Saved successfully'))
      setOpen(false)
      props.onProcessed?.()
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['audit-security'] }),
        queryClient.invalidateQueries({ queryKey: ['audit'] }),
        queryClient.invalidateQueries({
          queryKey: ['customer-audit', user?.id],
        }),
      ])
    },
    onError: (error) => {
      if (props.customerID !== undefined && isCustomerAccessDenied(error)) {
        setOpen(false)
        props.onAccessDenied?.()
        return
      }
      handleServerError(
        error,
        t('The alert may have changed. Refresh and try again.')
      )
      void queryClient.invalidateQueries({ queryKey: ['audit-security'] })
    },
  })
  const archive = useMutation({
    mutationFn: () =>
      archiveAlert(props.alert.id, version, !props.alert.archived_at),
    onSuccess: async () => {
      setOpen(false)
      props.onProcessed?.()
      toast.success(t('Saved successfully'))
      await queryClient.invalidateQueries({ queryKey: ['audit-security'] })
    },
    onError: (error) =>
      handleServerError(
        error,
        t('The alert may have changed. Refresh and try again.')
      ),
  })
  const busy = save.isPending || archive.isPending
  return (
    <Popover
      open={open}
      onOpenChange={(nextOpen) => {
        if (busy) return
        if (nextOpen) {
          // Keep the reviewed version even when the list refreshes in the background.
          setVersion(props.alert.version)
          setSelectedAssignees(initialAssignees)
          setConclusion(props.alert.conclusion ?? '')
          form.reset({
            status: props.alert.status as AlertFormValues['status'],
            note: '',
          })
          save.reset()
        }
        setOpen(nextOpen)
      }}
    >
      <PopoverTrigger
        render={<Button type='button' variant='outline' size='sm' />}
      >
        {t(
          alertStatuses[props.alert.status as keyof typeof alertStatuses] ??
            'Not handled'
        )}
      </PopoverTrigger>
      <PopoverContent
        align='end'
        className='w-[min(320px,calc(100vw-2rem))] p-3'
      >
        <PopoverHeader>
          <PopoverTitle>
            {t('Security alert')} #{props.alert.id}
          </PopoverTitle>
        </PopoverHeader>
        <Form {...form}>
          <form
            className='space-y-3'
            onSubmit={form.handleSubmit((values) => save.mutate(values))}
          >
            <FormField
              control={form.control}
              name='status'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Handling status')}</FormLabel>
                  <FormControl>
                    <Combobox
                      aria-label={t('Handling status')}
                      value={field.value}
                      onValueChange={(value: string | null) => {
                        if (value !== null) field.onChange(value)
                      }}
                      onBlur={field.onBlur}
                      ref={field.ref}
                      disabled={busy}
                      options={Object.entries(alertStatuses).map(
                        ([value, label]) => ({ value, label: t(label) })
                      )}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            {props.customerID === undefined && (
              <FormItem>
                <FormLabel htmlFor={`alert-assignees-${props.alert.id}`}>
                  {t('Assignee')}
                </FormLabel>
                {props.alert.assignees?.some(
                  (person) => person.source === 'owner'
                ) && (
                  <AlertAssignees
                    assignees={props.alert.assignees.filter(
                      (person) => person.source === 'owner'
                    )}
                  />
                )}
                <MultiSelect
                  id={`alert-assignees-${props.alert.id}`}
                  placeholder={t('Assignee')}
                  selected={selectedAssignees.map(String)}
                  disabled={busy || assignees.isPending || assignees.isError}
                  searchByLabel
                  maxVisibleChips={3}
                  options={[
                    ...(Array.isArray(assignees.data)
                      ? assignees.data.map((person) => ({
                          value: String(person.id),
                          label: `${person.username} #${person.id}`,
                        }))
                      : []),
                    ...selectedAssignees
                      .filter(
                        (id) =>
                          !Array.isArray(assignees.data) ||
                          !assignees.data.some((person) => person.id === id)
                      )
                      .map((id) => ({
                        value: String(id),
                        label:
                          props.alert.assignees?.find(
                            (person) => person.id === id
                          )?.username || `#${id}`,
                      })),
                  ]}
                  onChange={(values) => {
                    setSelectedAssignees(
                      values.map(Number).sort((a, b) => a - b)
                    )
                  }}
                />
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'The assigned employee follows current customer ownership.'
                  )}
                </p>
                {selectedAssignees.length > 10 && (
                  <p role='alert' className='text-destructive text-sm'>
                    {t('Must contain at most {{count}} items', { count: 10 })}
                  </p>
                )}
                {assignees.isError && (
                  <Button
                    type='button'
                    size='sm'
                    variant='link'
                    onClick={() => void assignees.refetch()}
                  >
                    {t('Retry')}
                  </Button>
                )}
              </FormItem>
            )}
            <FormItem>
              <FormLabel>{t('Conclusion')}</FormLabel>
              <Combobox
                aria-label={t('Conclusion')}
                value={conclusion}
                disabled={busy}
                options={[
                  { value: '', label: t('Not recorded') },
                  ...Object.entries(securityConclusions).map(
                    ([value, label]) => ({ value, label: t(label) })
                  ),
                ]}
                onValueChange={(value) => {
                  if (value !== null) setConclusion(value)
                }}
              />
            </FormItem>
            <FormField
              control={form.control}
              name='note'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Investigation note')}</FormLabel>
                  <FormControl>
                    <Textarea {...field} maxLength={1000} disabled={busy} />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <div className='flex justify-end gap-2'>
              {props.customerID === undefined &&
                (props.alert.status === 'resolved' ||
                  props.alert.status === 'false_positive') && (
                  <Button
                    type='button'
                    size='sm'
                    variant='outline'
                    disabled={busy}
                    onClick={() => archive.mutate()}
                  >
                    {t(props.alert.archived_at ? 'Restore' : 'Archive')}
                  </Button>
                )}
              <Button
                type='button'
                variant='outline'
                size='sm'
                disabled={busy}
                onClick={() => setOpen(false)}
              >
                {t('Cancel')}
              </Button>
              <Button
                type='submit'
                size='sm'
                disabled={busy || selectedAssignees.length > 10}
              >
                {save.isPending ? t('Saving...') : t('Save')}
              </Button>
            </div>
          </form>
        </Form>
      </PopoverContent>
    </Popover>
  )
}

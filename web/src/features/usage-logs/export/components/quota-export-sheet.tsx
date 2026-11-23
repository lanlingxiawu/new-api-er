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
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { CompactDateTimeRangePicker } from '../../components/compact-date-time-range-picker'
import { getDefaultTimeRange } from '../../lib/utils'
import { createQuotaExportJob } from '../api'
import { buildQuotaExportRequest } from '../quota-export'

export function QuotaExportSheet(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  maxRangeSec?: number
}) {
  const { t } = useTranslation()
  const id = useId()
  const user = useAuthStore((state) => state.auth.user)
  const root = (user?.role ?? 0) >= ROLE.SUPER_ADMIN
  const queryClient = useQueryClient()
  const [range, setRange] = useState<{ start?: Date; end?: Date }>(
    getDefaultTimeRange
  )
  const [form, setForm] = useState({
    userId: '',
    username: '',
    modelName: '',
    useGroup: '',
    channelId: '',
    tokenId: '',
    nodeName: '',
    header: true,
    csvBom: true,
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
  })
  const start = range.start && Math.floor(range.start.getTime() / 1000)
  const end = range.end && Math.floor(range.end.getTime() / 1000)
  const maxRange = props.maxRangeSec ?? 31 * 86400
  const tooLong =
    start !== undefined && end !== undefined && end - start > maxRange
  const rangeValid =
    start !== undefined &&
    end !== undefined &&
    start > 0 &&
    end >= start &&
    !tooLong
  const request =
    rangeValid && start !== undefined && end !== undefined
      ? buildQuotaExportRequest({ ...form, start, end, root })
      : undefined
  const create = useMutation({
    mutationFn: createQuotaExportJob,
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ['log-export-jobs', 'quota', 'quota_admin'],
      })
      props.onOpenChange(false)
    },
    onError: (error) => handleServerError(error, t('Export failed')),
  })
  const fields: Array<{
    key:
      | 'userId'
      | 'username'
      | 'modelName'
      | 'useGroup'
      | 'channelId'
      | 'tokenId'
      | 'nodeName'
    label: string
  }> = [
    { key: 'userId', label: 'User ID' },
    { key: 'username', label: 'Username' },
    { key: 'modelName', label: 'Model' },
    { key: 'useGroup', label: 'Group' },
    { key: 'channelId', label: 'Channel ID' },
    ...(root
      ? [
          { key: 'tokenId' as const, label: 'Token ID' },
          { key: 'nodeName' as const, label: 'Node' },
        ]
      : []),
  ]

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Quota Data')}
      contentClassName='sm:max-w-3xl'
      bodyClassName='space-y-5 sm:pr-2'
      footer={
        <>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button
            disabled={!request || create.isPending}
            aria-busy={create.isPending}
            onClick={() => {
              if (!request) return
              create.mutate(request)
            }}
          >
            {create.isPending ? t('Loading...') : t('Export')}
          </Button>
        </>
      }
    >
      <FieldGroup className='grid grid-cols-1 gap-4 sm:grid-cols-2'>
        <Field
          className='sm:col-span-2'
          data-invalid={!rangeValid || undefined}
        >
          <FieldLabel>{t('Date Range')}</FieldLabel>
          <CompactDateTimeRangePicker
            start={range.start}
            end={range.end}
            onChange={setRange}
            disabled={create.isPending}
            maxRangeSec={maxRange}
          />
          {rangeValid && (
            <FieldDescription>
              {t('The time range cannot exceed {{days}} days', {
                days: maxRange / 86400,
              })}
            </FieldDescription>
          )}
          {!rangeValid && (
            <p role='alert' className='text-destructive text-sm'>
              {tooLong
                ? t('The time range cannot exceed {{days}} days', {
                    days: maxRange / 86400,
                  })
                : t('Please select a valid time range')}
            </p>
          )}
        </Field>
        {fields.map(({ key, label }) => (
          <Field key={key}>
            <FieldLabel htmlFor={`${id}-${key}`}>{t(label)}</FieldLabel>
            <Input
              id={`${id}-${key}`}
              value={form[key]}
              inputMode={key.endsWith('Id') ? 'numeric' : 'text'}
              maxLength={key.endsWith('Id') ? 16 : 64}
              disabled={create.isPending}
              onChange={(event) =>
                setForm({ ...form, [key]: event.target.value })
              }
            />
          </Field>
        ))}
        <Field>
          <FieldLabel htmlFor={`${id}-timezone`}>{t('Timezone')}</FieldLabel>
          <Input
            id={`${id}-timezone`}
            value={form.timezone}
            disabled={create.isPending}
            onChange={(event) =>
              setForm({ ...form, timezone: event.target.value })
            }
          />
        </Field>
      </FieldGroup>
      <div className='grid gap-4 border-t pt-4 sm:grid-cols-2'>
        <label className='flex items-center gap-2 text-sm'>
          <Switch
            checked={form.header}
            onCheckedChange={(value) => setForm({ ...form, header: value })}
          />
          {t('Include header row')}
        </label>
        <label className='flex items-center gap-2 text-sm'>
          <Switch
            checked={form.csvBom}
            onCheckedChange={(value) => setForm({ ...form, csvBom: value })}
          />
          {t('Add BOM (Excel reads UTF-8 correctly)')}
        </label>
      </div>
      {rangeValid && !request && (
        <p role='alert' className='text-destructive text-sm'>
          {t('Invalid parameters')}
        </p>
      )}
    </Dialog>
  )
}

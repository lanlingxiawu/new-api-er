import { useMutation, useQueryClient } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
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
import { handleServerError } from '@/lib/handle-server-error'

import { CompactDateTimeRangePicker } from '../../components/compact-date-time-range-picker'
import { LogTypeSelect } from '../../components/log-type-select'
import { getDefaultTimeRange } from '../../lib/utils'
import { createSelfExportJob } from '../api'
import { SelfExportFilterSelect } from './self-export-filter-select'

const route = getRouteApi('/_authenticated/usage-logs/$section')

export function SelfExportSheet(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  maxRangeSec?: number
}) {
  const { t } = useTranslation()
  const search = route.useSearch()
  const id = useId()
  const [filters, setFilters] = useState(() => ({
    type:
      Number(Array.isArray(search.type) ? search.type[0] : search.type) || 0,
    model_name: search.model ?? '',
    token_name: search.token ?? '',
    group: search.group ?? '',
  }))
  const queryClient = useQueryClient()
  const [range, setRange] = useState<{ start?: Date; end?: Date }>(() => {
    const defaults = getDefaultTimeRange()
    return {
      start: search.startTime ? new Date(search.startTime) : defaults.start,
      end: search.endTime ? new Date(search.endTime) : defaults.end,
    }
  })
  const start = range.start && Math.floor(range.start.getTime() / 1000)
  const end = range.end && Math.floor(range.end.getTime() / 1000)
  const maxRange = props.maxRangeSec ?? 30 * 86400
  const tooLong =
    start !== undefined && end !== undefined && end - start > maxRange
  const valid =
    start !== undefined &&
    end !== undefined &&
    start > 0 &&
    end >= start &&
    !tooLong
  const create = useMutation({
    mutationFn: createSelfExportJob,
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ['log-export-jobs', 'usage', 'usage_self'],
      })
      props.onOpenChange(false)
    },
    onError: (error) => handleServerError(error, t('Export failed')),
  })

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('New Export')}
      contentClassName='overflow-hidden sm:max-w-lg'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button
            disabled={!valid || create.isPending}
            aria-busy={create.isPending}
            onClick={() => {
              if (!valid || start === undefined || end === undefined) return
              create.mutate({
                filters: {
                  start_timestamp: start,
                  end_timestamp: end,
                  type: filters.type,
                  model_name: filters.model_name.trim() || undefined,
                  token_name: filters.token_name.trim() || undefined,
                  group: filters.group.trim() || undefined,
                },
                options: {
                  timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
                  header: true,
                  csv_bom: true,
                },
              })
            }}
          >
            {create.isPending ? t('Loading...') : t('Export')}
          </Button>
        </>
      }
    >
      <FieldGroup>
        <Field data-invalid={!valid || undefined}>
          <FieldLabel>{t('Date Range')}</FieldLabel>
          <CompactDateTimeRangePicker
            start={range.start}
            end={range.end}
            onChange={setRange}
            disabled={create.isPending}
            maxRangeSec={maxRange}
          />
          {valid && (
            <FieldDescription>
              {t('The time range cannot exceed {{days}} days', {
                days: maxRange / 86400,
              })}
            </FieldDescription>
          )}
          {!valid && (
            <p role='alert' className='text-destructive text-sm'>
              {tooLong
                ? t('The time range cannot exceed {{days}} days', {
                    days: maxRange / 86400,
                  })
                : t('Please select a valid time range')}
            </p>
          )}
        </Field>
        <Field>
          <FieldLabel htmlFor={`${id}-type`}>{t('Type')}</FieldLabel>
          <LogTypeSelect
            id={`${id}-type`}
            value={String(filters.type)}
            onValueChange={(value) =>
              setFilters({ ...filters, type: Number(value) || 0 })
            }
            disabled={create.isPending}
          />
        </Field>
        {(
          [
            { key: 'model_name', label: 'Model' },
            { key: 'token_name', label: 'Token' },
            { key: 'group', label: 'Group' },
          ] as const
        ).map(({ key, label }) => (
          <Field key={key}>
            <FieldLabel htmlFor={`${id}-${key}`}>{t(label)}</FieldLabel>
            <SelfExportFilterSelect
              id={`${id}-${key}`}
              field={key}
              value={filters[key]}
              disabled={create.isPending}
              onChange={(value) => setFilters({ ...filters, [key]: value })}
            />
          </Field>
        ))}
      </FieldGroup>
      <Button
        type='button'
        variant='ghost'
        size='sm'
        disabled={create.isPending}
        onClick={() =>
          setFilters({ type: 0, model_name: '', token_name: '', group: '' })
        }
      >
        {t('Clear filters')}
      </Button>
    </Dialog>
  )
}

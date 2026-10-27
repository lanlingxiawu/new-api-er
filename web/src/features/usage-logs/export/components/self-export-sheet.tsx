import { useMutation, useQueryClient } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
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
  const start = range.start?.getTime()
  const end = range.end?.getTime()
  const maxRange = props.maxRangeSec ?? 30 * 86400
  const valid =
    start !== undefined &&
    end !== undefined &&
    start > 0 &&
    end >= start &&
    end - start <= maxRange * 1000
  const create = useMutation({
    mutationFn: createSelfExportJob,
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ['log-export-jobs', 'self'],
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
      description={t(
        'Export runs in the background. Download the compressed CSV when it is ready.'
      )}
      contentClassName='sm:max-w-md'
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
                  start_timestamp: Math.floor(start / 1000),
                  end_timestamp: Math.floor(end / 1000),
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
        <Field>
          <FieldLabel>{t('Date Range')}</FieldLabel>
          <CompactDateTimeRangePicker
            start={range.start}
            end={range.end}
            onChange={setRange}
            disabled={create.isPending}
          />
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
      <p className='text-muted-foreground text-sm'>
        {t(
          'Choose up to {{days}} days. Only the filters shown here apply. Leave a field empty to include all values.',
          { days: maxRange / 86400 }
        )}
      </p>
      {!valid && (
        <p role='alert' className='text-destructive text-sm'>
          {t('Please select a valid time range')}
        </p>
      )}
    </Dialog>
  )
}

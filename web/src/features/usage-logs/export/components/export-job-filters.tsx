import dayjs from 'dayjs'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { ComboboxInput } from '@/components/ui/combobox-input'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { cn } from '@/lib/utils'

import { CompactDateTimeRangePicker } from '../../components/compact-date-time-range-picker'
import type { ExportJob, ExportJobListFilters } from '../types'
import { ExportFilterSelect } from './export-filter-select'

export function ExportJobFilters(props: {
  value: ExportJobListFilters
  onApply: (filters: ExportJobListFilters) => void
  self?: boolean
  employee?: boolean
  jobs?: Pick<ExportJob, 'job_id' | 'username' | 'created_at'>[]
  hasMoreJobs?: boolean
  loadingMoreJobs?: boolean
  onLoadMoreJobs?: () => void
}) {
  const { t } = useTranslation()
  const id = useId()
  const [draft, setDraft] = useState(props.value)
  const [error, setError] = useState(false)
  const statusItems = [
    { value: '', label: t('All') },
    { value: 'pending', label: t('Queued') },
    { value: 'running', label: t('Running') },
    { value: 'ready', label: t('Completed') },
    { value: 'failed', label: t('Failed') },
    { value: 'canceled', label: t('Canceled') },
  ]
  const advancedFields = [
    {
      key: 'scope' as const,
      label: t('Export scope'),
      items: [
        { value: 'all', label: t('All visible exports') },
        { value: 'mine', label: t('My exports') },
      ],
    },
    {
      key: 'source' as const,
      label: t('Export source'),
      items: [
        { value: '', label: t('All') },
        ...(!props.employee
          ? [{ value: 'admin', label: t('Administrator export') }]
          : []),
        { value: 'employee', label: t('Employee export') },
        { value: 'self', label: t('Self-service export') },
      ],
    },
    {
      key: 'mode' as const,
      label: t('Export mode'),
      items: [
        { value: '', label: t('All') },
        { value: 'detail', label: t('One row per log') },
        { value: 'summary', label: t('Aggregated summary') },
        { value: 'both', label: t('Detail + summary') },
      ],
    },
  ]
  return (
    <form
      className='space-y-3'
      onSubmit={(event) => {
        event.preventDefault()
        if (
          draft.created_after &&
          draft.created_before &&
          draft.created_after > draft.created_before
        ) {
          setError(true)
          return
        }
        setError(false)
        props.onApply({
          ...draft,
          creator_keyword: undefined,
          job_id: draft.job_id?.trim() || undefined,
        })
      }}
    >
      <FieldGroup
        className={cn(
          'grid gap-3 sm:grid-cols-2',
          props.employee ? 'xl:grid-cols-3' : 'xl:grid-cols-4'
        )}
      >
        {!props.self && (
          <>
            {!props.employee && (
              <Field>
                <FieldLabel>{t('Creator')}</FieldLabel>
                <ExportFilterSelect
                  field='customer'
                  label={t('Creator')}
                  showId
                  value={draft.creator_id ? String(draft.creator_id) : ''}
                  onChange={(value) =>
                    setDraft({
                      ...draft,
                      creator_id: value ? Number(value) : undefined,
                      creator_keyword: undefined,
                    })
                  }
                />
              </Field>
            )}
            <Field>
              <FieldLabel>{t('Customer')}</FieldLabel>
              <ExportFilterSelect
                field='customer'
                label={t('Customer')}
                base={props.employee ? '/api/user/employee/export' : undefined}
                value={draft.customer_id ? String(draft.customer_id) : ''}
                onChange={(value) =>
                  setDraft({
                    ...draft,
                    customer_id: value ? Number(value) : undefined,
                  })
                }
              />
            </Field>
          </>
        )}
        <Field>
          <FieldLabel htmlFor={`${id}-status`}>{t('Status')}</FieldLabel>
          <Select
            items={statusItems}
            value={draft.status ?? ''}
            onValueChange={(value) =>
              setDraft({ ...draft, status: value || undefined })
            }
          >
            <SelectTrigger id={`${id}-status`} className='w-full'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent alignItemWithTrigger={false}>
              <SelectGroup>
                {statusItems.map((item) => (
                  <SelectItem key={item.value} value={item.value}>
                    {item.label}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </Field>
        <Field>
          <FieldLabel>{t('Task creation time')}</FieldLabel>
          <CompactDateTimeRangePicker
            start={
              draft.created_after
                ? new Date(draft.created_after * 1000)
                : undefined
            }
            end={
              draft.created_before
                ? new Date(draft.created_before * 1000)
                : undefined
            }
            onChange={(range) =>
              setDraft({
                ...draft,
                created_after: range.start
                  ? Math.floor(range.start.getTime() / 1000)
                  : undefined,
                created_before: range.end
                  ? Math.floor(range.end.getTime() / 1000)
                  : undefined,
              })
            }
          />
        </Field>
      </FieldGroup>
      <Collapsible>
        <div className='flex flex-wrap items-center gap-2'>
          <Button type='submit' size='sm'>
            {t('Search')}
          </Button>
          <Button
            type='button'
            size='sm'
            variant='outline'
            onClick={() => {
              setDraft({})
              setError(false)
              props.onApply({})
            }}
          >
            {t('Reset')}
          </Button>
          <CollapsibleTrigger
            render={<Button type='button' size='sm' variant='ghost' />}
          >
            {t('More filters')}
          </CollapsibleTrigger>
          <Button
            type='button'
            size='sm'
            variant='ghost'
            onClick={() => {
              const now = Math.floor(Date.now() / 1000)
              setDraft({
                ...draft,
                created_after: now - 86400,
                created_before: now,
              })
            }}
          >
            {t('Last 24 hours')}
          </Button>
        </div>
        <CollapsibleContent className='pt-3'>
          <FieldGroup className='grid gap-3 sm:grid-cols-2 xl:grid-cols-4'>
            {!props.self &&
              advancedFields.map(({ key, label, items }) => (
                <Field key={key}>
                  <FieldLabel htmlFor={`${id}-${key}`}>{label}</FieldLabel>
                  <Select
                    items={items}
                    value={draft[key] ?? (key === 'scope' ? 'all' : '')}
                    onValueChange={(value) =>
                      setDraft({ ...draft, [key]: value || undefined })
                    }
                  >
                    <SelectTrigger id={`${id}-${key}`} className='w-full'>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent alignItemWithTrigger={false}>
                      <SelectGroup>
                        {items.map((item) => (
                          <SelectItem key={item.value} value={item.value}>
                            {item.label}
                          </SelectItem>
                        ))}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                </Field>
              ))}
            <Field>
              <FieldLabel htmlFor={`${id}-job`}>
                {t('Export task ID')}
              </FieldLabel>
              <ComboboxInput
                id={`${id}-job`}
                allowCustomValue
                placeholder={t('Select or paste task ID')}
                emptyText={t('No results found')}
                options={(props.jobs ?? []).map((job) => ({
                  value: job.job_id,
                  label: job.job_id,
                  description: [
                    dayjs.unix(job.created_at).format('MM-DD HH:mm'),
                    job.username,
                  ]
                    .filter(Boolean)
                    .join(' · '),
                }))}
                value={draft.job_id ?? ''}
                onValueChange={(value) =>
                  setDraft({ ...draft, job_id: value || undefined })
                }
                dropdownFooter={
                  props.hasMoreJobs && props.onLoadMoreJobs ? (
                    <div className='border-t p-1'>
                      <Button
                        type='button'
                        variant='ghost'
                        size='sm'
                        className='w-full'
                        disabled={props.loadingMoreJobs}
                        onMouseDown={(event) => event.preventDefault()}
                        onClick={props.onLoadMoreJobs}
                      >
                        {props.loadingMoreJobs
                          ? t('Loading...')
                          : t('Load more')}
                      </Button>
                    </div>
                  ) : undefined
                }
              />
            </Field>
          </FieldGroup>
          {!props.self && (
            <p className='text-muted-foreground mt-2 text-xs'>
              {t(
                'Find exports created by this customer or by an employee for this customer.'
              )}
            </p>
          )}
        </CollapsibleContent>
      </Collapsible>
      {error && (
        <p role='alert' className='text-destructive text-sm'>
          {t('Start time must not be after end time.')}
        </p>
      )}
      {Object.values(props.value).some(
        (value) => value !== undefined && value !== ''
      ) && (
        <p className='text-muted-foreground text-xs'>
          {t('Filters applied. Reset to see all visible exports.')}
        </p>
      )}
    </form>
  )
}

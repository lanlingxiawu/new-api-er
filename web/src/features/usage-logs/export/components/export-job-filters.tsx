import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import { CompactDateTimeRangePicker } from '../../components/compact-date-time-range-picker'
import type { ExportJobListFilters } from '../types'
import { ExportFilterSelect } from './export-filter-select'

export function ExportJobFilters(props: {
  value: ExportJobListFilters
  onApply: (filters: ExportJobListFilters) => void
  self?: boolean
  employee?: boolean
}) {
  const { t } = useTranslation()
  const id = useId()
  const [draft, setDraft] = useState(props.value)
  const [error, setError] = useState(false)
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
          creator_keyword: draft.creator_keyword?.trim() || undefined,
          job_id: draft.job_id?.trim() || undefined,
        })
      }}
    >
      <FieldGroup className='grid gap-3 sm:grid-cols-2 xl:grid-cols-4'>
        {!props.self && (
          <>
            <Field>
              <FieldLabel htmlFor={`${id}-creator`}>
                {t('Creator username')}
              </FieldLabel>
              <Input
                id={`${id}-creator`}
                maxLength={64}
                value={draft.creator_keyword ?? ''}
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    creator_keyword: e.target.value || undefined,
                  })
                }
              />
            </Field>
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
          <NativeSelect
            id={`${id}-status`}
            value={draft.status ?? ''}
            onChange={(e) =>
              setDraft({ ...draft, status: e.target.value || undefined })
            }
          >
            <NativeSelectOption value=''>{t('All')}</NativeSelectOption>
            <NativeSelectOption value='pending'>
              {t('Queued')}
            </NativeSelectOption>
            <NativeSelectOption value='running'>
              {t('Running')}
            </NativeSelectOption>
            <NativeSelectOption value='ready'>
              {t('Completed')}
            </NativeSelectOption>
            <NativeSelectOption value='failed'>
              {t('Failed')}
            </NativeSelectOption>
            <NativeSelectOption value='canceled'>
              {t('Canceled')}
            </NativeSelectOption>
          </NativeSelect>
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
            {!props.self && (
              <>
                <Field>
                  <FieldLabel htmlFor={`${id}-scope`}>
                    {t('Export scope')}
                  </FieldLabel>
                  <NativeSelect
                    id={`${id}-scope`}
                    value={draft.scope ?? 'all'}
                    onChange={(e) =>
                      setDraft({
                        ...draft,
                        scope: e.target.value as 'all' | 'mine',
                      })
                    }
                  >
                    <NativeSelectOption value='all'>
                      {t('All visible exports')}
                    </NativeSelectOption>
                    <NativeSelectOption value='mine'>
                      {t('My exports')}
                    </NativeSelectOption>
                  </NativeSelect>
                </Field>
                <Field>
                  <FieldLabel htmlFor={`${id}-creator-id`}>
                    {t('Creator ID')}
                  </FieldLabel>
                  <Input
                    id={`${id}-creator-id`}
                    type='number'
                    min={1}
                    step={1}
                    value={draft.creator_id ?? ''}
                    onChange={(e) =>
                      setDraft({
                        ...draft,
                        creator_id: e.target.value
                          ? Number(e.target.value)
                          : undefined,
                      })
                    }
                  />
                </Field>
                <Field>
                  <FieldLabel htmlFor={`${id}-source`}>
                    {t('Export source')}
                  </FieldLabel>
                  <NativeSelect
                    id={`${id}-source`}
                    value={draft.source ?? ''}
                    onChange={(e) =>
                      setDraft({
                        ...draft,
                        source: e.target.value || undefined,
                      })
                    }
                  >
                    <NativeSelectOption value=''>{t('All')}</NativeSelectOption>
                    {!props.employee && (
                      <NativeSelectOption value='admin'>
                        {t('Administrator export')}
                      </NativeSelectOption>
                    )}
                    <NativeSelectOption value='employee'>
                      {t('Employee export')}
                    </NativeSelectOption>
                    <NativeSelectOption value='self'>
                      {t('Self-service export')}
                    </NativeSelectOption>
                  </NativeSelect>
                </Field>
              </>
            )}
            {!props.self && (
              <Field>
                <FieldLabel htmlFor={`${id}-mode`}>
                  {t('Export mode')}
                </FieldLabel>
                <NativeSelect
                  id={`${id}-mode`}
                  value={draft.mode ?? ''}
                  onChange={(e) =>
                    setDraft({ ...draft, mode: e.target.value || undefined })
                  }
                >
                  <NativeSelectOption value=''>{t('All')}</NativeSelectOption>
                  <NativeSelectOption value='detail'>
                    {t('One row per log')}
                  </NativeSelectOption>
                  <NativeSelectOption value='summary'>
                    {t('Aggregated summary')}
                  </NativeSelectOption>
                  <NativeSelectOption value='both'>
                    {t('Detail + summary')}
                  </NativeSelectOption>
                </NativeSelect>
              </Field>
            )}
            <Field>
              <FieldLabel htmlFor={`${id}-job`}>
                {t('Export task ID')}
              </FieldLabel>
              <Input
                id={`${id}-job`}
                maxLength={36}
                value={draft.job_id ?? ''}
                onChange={(e) =>
                  setDraft({ ...draft, job_id: e.target.value || undefined })
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

import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'

import { TROUBLESHOOTING_FILTERS } from '../employee-filters'
import type { AnomalyFilters, NumericFilters } from '../types'

const ANOMALY_LABELS: Record<string, string> = {
  estimated_usage: 'Billed from estimate',
  mixed_usage: 'Partly estimated',
  no_usage: 'Not billed',
  stream_error: 'Stream ended abnormally',
  settlement_unsettled: 'Settlement incomplete',
  retried: 'Retried across channels',
  quota_saturated: 'Quota overflowed and was capped',
}

export type EmployeeTroubleshootingFilters = Pick<
  AnomalyFilters,
  'anomaly_only' | 'anomaly_kinds' | 'min_retry_count'
> &
  Pick<
    NumericFilters,
    | 'charged'
    | 'quota_min'
    | 'completion_tokens_min'
    | 'completion_tokens_max'
    | 'use_time_min'
  >

export function EmployeeTroubleshootingFilters(props: {
  allowed: string[]
  value: EmployeeTroubleshootingFilters
  onChange: (value: EmployeeTroubleshootingFilters) => void
  anomalyKinds: string[]
  quotaPerUnit: number
  maxFilterValues: number
  disabled: boolean
}) {
  const { t } = useTranslation()
  const id = useId()
  const allowed = (key: string) => props.allowed.includes(key)
  if (!TROUBLESHOOTING_FILTERS.some((item) => allowed(item.value))) return null
  const active =
    Object.values(props.value).some(
      (value) =>
        value != null &&
        value !== false &&
        (!Array.isArray(value) || value.length > 0)
    ) || props.value.charged === false
  const quotaPerUnit = props.quotaPerUnit > 0 ? props.quotaPerUnit : 500000
  const quotaUnitUSD = (1 / quotaPerUnit)
    .toFixed(8)
    .replace(/0+$/, '')
    .replace(/\.$/, '')
  const numericFields = [
    {
      value: 'quota_min',
      label: t('Min quota units ({{usd}} each)', { usd: quotaUnitUSD }),
    },
    { value: 'completion_tokens_min', label: t('Min output tokens') },
    { value: 'completion_tokens_max', label: t('Max output tokens') },
    { value: 'use_time_min', label: t('Min duration (s)') },
    { value: 'min_retry_count', label: t('Min retries') },
  ] as const
  return (
    <details className='rounded-md border px-3 py-2'>
      <summary className='cursor-pointer text-sm font-medium'>
        {t('Troubleshooting filters')}
        {active && (
          <Badge variant='secondary' className='ml-2'>
            {t('Active')}
          </Badge>
        )}
      </summary>
      <FieldGroup className='mt-3 gap-3'>
        {(allowed('anomaly_only') || allowed('anomaly_kinds')) && (
          <Field>
            <FieldLabel id={`${id}-anomalies`}>{t('Anomalies')}</FieldLabel>
            <ToggleGroup
              multiple
              value={
                props.value.anomaly_only
                  ? ['any']
                  : (props.value.anomaly_kinds ?? [])
              }
              aria-labelledby={`${id}-anomalies`}
              disabled={props.disabled}
              variant='outline'
              size='sm'
              spacing={2}
              className='flex-wrap'
              onValueChange={(values) => {
                if (values.includes('any') && !props.value.anomaly_only) {
                  props.onChange({
                    ...props.value,
                    anomaly_only: true,
                    anomaly_kinds: undefined,
                  })
                } else {
                  const kinds = values.filter((value) => value !== 'any')
                  if (kinds.length <= props.maxFilterValues) {
                    props.onChange({
                      ...props.value,
                      anomaly_only: false,
                      anomaly_kinds: kinds,
                    })
                  }
                }
              }}
            >
              {allowed('anomaly_only') && (
                <ToggleGroupItem value='any'>
                  {t('Any anomaly')}
                </ToggleGroupItem>
              )}
              {allowed('anomaly_kinds') &&
                props.anomalyKinds.map((kind) => (
                  <ToggleGroupItem key={kind} value={kind}>
                    {t(ANOMALY_LABELS[kind] ?? kind)}
                  </ToggleGroupItem>
                ))}
            </ToggleGroup>
          </Field>
        )}
        {allowed('charged') && (
          <Field>
            <FieldLabel id={`${id}-charged`}>{t('Charged')}</FieldLabel>
            <ToggleGroup
              value={[
                props.value.charged == null
                  ? 'any'
                  : String(props.value.charged),
              ]}
              aria-labelledby={`${id}-charged`}
              disabled={props.disabled}
              variant='outline'
              size='sm'
              spacing={2}
              className='flex-wrap'
              onValueChange={(values) =>
                props.onChange({
                  ...props.value,
                  charged:
                    !values.length || values[0] === 'any'
                      ? null
                      : values[0] === 'true',
                })
              }
            >
              <ToggleGroupItem value='any'>{t('Any')}</ToggleGroupItem>
              <ToggleGroupItem value='true'>
                {t('Charged (cost > 0)')}
              </ToggleGroupItem>
              <ToggleGroupItem value='false'>
                {t('Free (cost = 0)')}
              </ToggleGroupItem>
            </ToggleGroup>
          </Field>
        )}
        {allowed('charged') && allowed('completion_tokens_max') && (
          <Field>
            <FieldLabel>{t('Quick filters')}</FieldLabel>
            <Button
              type='button'
              size='sm'
              className='w-fit'
              disabled={props.disabled}
              variant={
                props.value.charged && props.value.completion_tokens_max === 0
                  ? 'default'
                  : 'outline'
              }
              onClick={() =>
                props.onChange({
                  ...props.value,
                  charged:
                    props.value.charged &&
                    props.value.completion_tokens_max === 0
                      ? null
                      : true,
                  completion_tokens_max:
                    props.value.charged &&
                    props.value.completion_tokens_max === 0
                      ? null
                      : 0,
                })
              }
            >
              {t('Charged with zero output')}
            </Button>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Matches rows with no output tokens that still cost money. Note that embeddings and per-call pricing legitimately produce no output tokens.'
              )}
            </p>
          </Field>
        )}
        <div className='grid gap-3 sm:grid-cols-2'>
          {numericFields
            .filter((item) => allowed(item.value))
            .map((item) => (
              <Field key={item.value}>
                <FieldLabel htmlFor={`${id}-${item.value}`}>
                  {item.label}
                </FieldLabel>
                <Input
                  id={`${id}-${item.value}`}
                  type='number'
                  min={0}
                  max={Number.MAX_SAFE_INTEGER}
                  step={1}
                  disabled={props.disabled}
                  value={props.value[item.value] ?? ''}
                  onChange={(event) =>
                    props.onChange({
                      ...props.value,
                      [item.value]:
                        event.target.value === ''
                          ? null
                          : Number(event.target.value),
                    })
                  }
                />
                {item.value === 'quota_min' &&
                  props.value.quota_min != null &&
                  props.value.quota_min > 0 && (
                    <p className='text-muted-foreground text-xs'>
                      {t('≈ ${{usd}}', {
                        usd: props.value.quota_min / quotaPerUnit,
                      })}
                    </p>
                  )}
              </Field>
            ))}
        </div>
        {(props.value.anomaly_only ||
          props.value.anomaly_kinds?.length ||
          props.value.min_retry_count != null) && (
          <p className='text-muted-foreground text-xs'>
            {t(
              'Anomaly filters have no index to use, so the export still scans the whole time range — it takes as long as an unfiltered export, but the file is much smaller.'
            )}
          </p>
        )}
      </FieldGroup>
    </details>
  )
}

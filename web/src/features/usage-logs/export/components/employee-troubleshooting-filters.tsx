import { Check } from 'lucide-react'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'

import { TROUBLESHOOTING_FILTERS } from '../employee-filters'
import type { AnomalyFilters, NumericFilters } from '../types'

const filterOptionClassName =
  'h-auto min-h-9 justify-start gap-2 py-2 text-left whitespace-normal aria-pressed:border-primary aria-pressed:bg-primary aria-pressed:text-primary-foreground aria-pressed:hover:bg-primary/90 aria-pressed:hover:text-primary-foreground'
const filterCheckClassName =
  'invisible size-3.5 group-aria-pressed/toggle:visible'

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
  const zeroOutputCharged =
    props.value.charged === true && props.value.completion_tokens_max === 0
  const quotaUnitUSD = (1 / quotaPerUnit)
    .toFixed(8)
    .replace(/0+$/, '')
    .replace(/\.$/, '')
  const numericFields = [
    { value: 'completion_tokens_min', label: t('Min output tokens') },
    { value: 'completion_tokens_max', label: t('Max output tokens') },
    {
      value: 'quota_min',
      label: t('Min quota units ({{usd}} each)', { usd: quotaUnitUSD }),
    },
    { value: 'use_time_min', label: t('Min duration (s)') },
    { value: 'min_retry_count', label: t('Min retries') },
  ] as const
  return (
    <details className='rounded-lg border'>
      <summary className='focus-visible:ring-ring cursor-pointer rounded-lg px-4 py-3 text-sm font-medium focus-visible:ring-2 focus-visible:outline-none'>
        {t('Troubleshooting filters')}
        {active && <Badge className='ml-2'>{t('Active')}</Badge>}
      </summary>
      <FieldGroup className='gap-5 border-t p-4'>
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
              className='grid grid-cols-2 items-stretch gap-2 sm:grid-cols-3'
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
                <ToggleGroupItem value='any' className={filterOptionClassName}>
                  <Check aria-hidden='true' className={filterCheckClassName} />
                  {t('Any anomaly')}
                </ToggleGroupItem>
              )}
              {allowed('anomaly_kinds') &&
                props.anomalyKinds.map((kind) => (
                  <ToggleGroupItem
                    key={kind}
                    value={kind}
                    className={filterOptionClassName}
                  >
                    <Check
                      aria-hidden='true'
                      className={filterCheckClassName}
                    />
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
              className='grid grid-cols-1 items-stretch gap-2 sm:grid-cols-3'
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
              <ToggleGroupItem value='any' className={filterOptionClassName}>
                <Check aria-hidden='true' className={filterCheckClassName} />
                {t('Any')}
              </ToggleGroupItem>
              <ToggleGroupItem value='true' className={filterOptionClassName}>
                <Check aria-hidden='true' className={filterCheckClassName} />
                {t('Charged (cost > 0)')}
              </ToggleGroupItem>
              <ToggleGroupItem value='false' className={filterOptionClassName}>
                <Check aria-hidden='true' className={filterCheckClassName} />
                {t('Free (cost = 0)')}
              </ToggleGroupItem>
            </ToggleGroup>
          </Field>
        )}
        {allowed('charged') && allowed('completion_tokens_max') && (
          <Field>
            <FieldLabel>{t('Quick filters')}</FieldLabel>
            <div className='flex flex-wrap gap-2'>
              <Button
                type='button'
                size='sm'
                className='h-auto min-h-9 gap-2 py-2 whitespace-normal'
                disabled={props.disabled}
                variant={zeroOutputCharged ? 'default' : 'outline'}
                aria-pressed={zeroOutputCharged}
                onClick={() =>
                  props.onChange({
                    ...props.value,
                    charged: zeroOutputCharged ? null : true,
                    completion_tokens_max: zeroOutputCharged ? null : 0,
                  })
                }
              >
                <Check
                  aria-hidden='true'
                  className='invisible size-3.5 group-aria-pressed/button:visible'
                />
                {t('Charged with zero output')}
              </Button>
            </div>
            <p className='text-muted-foreground text-xs leading-relaxed'>
              {t(
                'Matches rows with no output tokens that still cost money. Note that embeddings and per-call pricing legitimately produce no output tokens.'
              )}
            </p>
          </Field>
        )}
        {numericFields.some((item) => allowed(item.value)) && <Separator />}
        <div className='grid gap-4 sm:grid-cols-2'>
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

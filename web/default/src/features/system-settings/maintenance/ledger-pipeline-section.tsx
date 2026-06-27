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
import * as z from 'zod'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { getLedgerPipelineStatus } from '../api'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { Switch } from '@/components/ui/switch'
import { cn } from '@/lib/utils'
import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import {
  numberInputNoSpinnerClassName,
  safeNumberFieldProps,
} from '../utils/numeric-field'

/**
 * IMPORTANT: react-hook-form 7 interprets dotted `name` strings as nested
 * paths. We model the form internally with a nested object and only flatten
 * back to the server-side key format right before persisting.
 */
const schema = z.object({
  ledger_pipeline_setting: z.object({
    flush_interval_sec: z.number().int().min(1),
    outer_batch_size: z.number().int().min(1),
    inner_batch_size: z.number().int().min(1),
    paired_flush_max_per_cycle: z.number().int().min(1),
    full_drain: z.boolean(),
    buf_max_entries: z.number().int().min(1),
    dedup_mem_max_entries: z.number().int().min(1),
    dedup_use_redis: z.boolean(),
    dedup_redis_ttl_sec: z.number().int().min(1),
    flush_db_timeout_sec: z.number().int().min(1),
  }),
})

type FormValues = z.infer<typeof schema>

type FlatDefaults = {
  'ledger_pipeline_setting.flush_interval_sec': number
  'ledger_pipeline_setting.outer_batch_size': number
  'ledger_pipeline_setting.inner_batch_size': number
  'ledger_pipeline_setting.paired_flush_max_per_cycle': number
  'ledger_pipeline_setting.full_drain': boolean
  'ledger_pipeline_setting.buf_max_entries': number
  'ledger_pipeline_setting.dedup_mem_max_entries': number
  'ledger_pipeline_setting.dedup_use_redis': boolean
  'ledger_pipeline_setting.dedup_redis_ttl_sec': number
  'ledger_pipeline_setting.flush_db_timeout_sec': number
}

type LedgerPipelineSectionProps = {
  defaultValues: FlatDefaults
}

function formatUnixTime(value: number) {
  if (!value) return '-'
  return new Date(value * 1000).toLocaleString()
}

function formatQueueNumber(value: number) {
  return new Intl.NumberFormat().format(value)
}

function buildFormDefaults(defaults: FlatDefaults): FormValues {
  return {
    ledger_pipeline_setting: {
      flush_interval_sec: defaults['ledger_pipeline_setting.flush_interval_sec'],
      outer_batch_size: defaults['ledger_pipeline_setting.outer_batch_size'],
      inner_batch_size: defaults['ledger_pipeline_setting.inner_batch_size'],
      paired_flush_max_per_cycle:
        defaults['ledger_pipeline_setting.paired_flush_max_per_cycle'],
      full_drain: defaults['ledger_pipeline_setting.full_drain'],
      buf_max_entries: defaults['ledger_pipeline_setting.buf_max_entries'],
      dedup_mem_max_entries:
        defaults['ledger_pipeline_setting.dedup_mem_max_entries'],
      dedup_use_redis: defaults['ledger_pipeline_setting.dedup_use_redis'],
      dedup_redis_ttl_sec:
        defaults['ledger_pipeline_setting.dedup_redis_ttl_sec'],
      flush_db_timeout_sec:
        defaults['ledger_pipeline_setting.flush_db_timeout_sec'],
    },
  }
}

function normalizeFormValues(values: FormValues): FlatDefaults {
  const s = values.ledger_pipeline_setting
  return {
    'ledger_pipeline_setting.flush_interval_sec': s.flush_interval_sec,
    'ledger_pipeline_setting.outer_batch_size': s.outer_batch_size,
    'ledger_pipeline_setting.inner_batch_size': s.inner_batch_size,
    'ledger_pipeline_setting.paired_flush_max_per_cycle':
      s.paired_flush_max_per_cycle,
    'ledger_pipeline_setting.full_drain': s.full_drain,
    'ledger_pipeline_setting.buf_max_entries': s.buf_max_entries,
    'ledger_pipeline_setting.dedup_mem_max_entries': s.dedup_mem_max_entries,
    'ledger_pipeline_setting.dedup_use_redis': s.dedup_use_redis,
    'ledger_pipeline_setting.dedup_redis_ttl_sec': s.dedup_redis_ttl_sec,
    'ledger_pipeline_setting.flush_db_timeout_sec': s.flush_db_timeout_sec,
  }
}

const numericFields: Array<{
  name: keyof Omit<
    FormValues['ledger_pipeline_setting'],
    'full_drain' | 'dedup_use_redis'
  >
  label: string
  description: string
  min: number
}> = [
  {
    name: 'flush_interval_sec',
    label: 'Flush Interval',
    description:
      'Seconds between flushes. Throughput/min = paired max × 60 ÷ interval.',
    min: 1,
  },
  {
    name: 'outer_batch_size',
    label: 'Outer Batch Size',
    description:
      'Records per outer loop (error-recovery granularity).',
    min: 1,
  },
  {
    name: 'inner_batch_size',
    label: 'Inner Batch Size',
    description:
      'Rows per SQL INSERT statement. Larger = fewer round-trips.',
    min: 1,
  },
  {
    name: 'paired_flush_max_per_cycle',
    label: 'Paired Flush Max Per Cycle',
    description:
      'Max paired records per flush cycle. Ignored when Full Drain is on.',
    min: 1,
  },
  {
    name: 'buf_max_entries',
    label: 'Buffer Max Entries',
    description:
      'Per-queue buffer ceiling. Oldest entries dropped on overflow to avoid OOM.',
    min: 1,
  },
  {
    name: 'dedup_mem_max_entries',
    label: 'Dedup Memory Max Entries',
    description:
      'Dedup set ceiling. Rebuilt on overflow; DB ON CONFLICT still guarantees idempotence.',
    min: 1,
  },
  {
    name: 'dedup_redis_ttl_sec',
    label: 'Dedup Redis TTL',
    description:
      'TTL of each Redis dedup key (seconds), covering the retry window. Used only when Dedup via Redis is on.',
    min: 1,
  },
  {
    name: 'flush_db_timeout_sec',
    label: 'Flush DB Timeout',
    description:
      'Per-flush DB write deadline (seconds). On timeout the flush is retried with backoff, so one stuck SQL statement cannot stall the whole flush loop.',
    min: 1,
  },
]

export function LedgerPipelineSection({
  defaultValues,
}: LedgerPipelineSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const statusQuery = useQuery({
    queryKey: ['ledger-pipeline-status'],
    queryFn: getLedgerPipelineStatus,
    refetchInterval: 10000,
  })

  const formDefaults = useMemo(
    () => buildFormDefaults(defaultValues),
    [defaultValues]
  )

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: formDefaults,
  })

  const flushIntervalSec = form.watch(
    'ledger_pipeline_setting.flush_interval_sec'
  )
  const pairedFlushMaxPerCycle = form.watch(
    'ledger_pipeline_setting.paired_flush_max_per_cycle'
  )

  const theoreticalPerMinute =
    flushIntervalSec > 0 ? (pairedFlushMaxPerCycle * 60) / flushIntervalSec : 0
  const [targetRpm, setTargetRpm] = useState(() =>
    Math.max(1, Math.round(theoreticalPerMinute))
  )
  const recommendedPairedFlushMaxPerCycle =
    flushIntervalSec > 0 ? Math.ceil((targetRpm / 60) * flushIntervalSec) : 0

  const runtimeStatus = statusQuery.data?.data
  const queueCards = runtimeStatus
    ? [
        { key: 'cost', label: 'Cost queue', data: runtimeStatus.snapshot.cost },
        { key: 'pair', label: 'Paired queue', data: runtimeStatus.snapshot.pair },
        {
          key: 'commission',
          label: 'Commission queue',
          data: runtimeStatus.snapshot.commission,
        },
      ]
    : []

  const baselineRef = useRef<FlatDefaults>(defaultValues)
  const baselineSerializedRef = useRef<string>(JSON.stringify(defaultValues))

  useEffect(() => {
    const serialized = JSON.stringify(defaultValues)
    if (serialized === baselineSerializedRef.current) return
    baselineRef.current = defaultValues
    baselineSerializedRef.current = serialized
    form.reset(buildFormDefaults(defaultValues))
    const nextTargetRpm =
      defaultValues['ledger_pipeline_setting.flush_interval_sec'] > 0
        ? Math.max(
            1,
            Math.round(
              (defaultValues['ledger_pipeline_setting.paired_flush_max_per_cycle'] *
                60) /
                defaultValues['ledger_pipeline_setting.flush_interval_sec']
            )
          )
        : 1
    setTargetRpm(nextTargetRpm)
  }, [defaultValues, form])

  const onSubmit = async (values: FormValues) => {
    const normalized = normalizeFormValues(values)
    const changedKeys = (
      Object.keys(normalized) as Array<keyof FlatDefaults>
    ).filter((key) => normalized[key] !== baselineRef.current[key])

    if (changedKeys.length === 0) {
      toast.info(t('No changes to save'))
      return
    }

    for (const key of changedKeys) {
      await updateOption.mutateAsync({ key, value: normalized[key] })
    }

    baselineRef.current = normalized
    baselineSerializedRef.current = JSON.stringify(normalized)
    form.reset(buildFormDefaults(normalized))
  }

  return (
    <SettingsSection title={t('Ledger Pipeline')}>
      <div className='grid gap-3 md:grid-cols-3'>
        {queueCards.map((item) => (
          <div
            key={item.key}
            className={cn(
              'rounded-xl border bg-muted/20 p-4 text-sm',
              item.key === 'pair' && 'border-primary/30'
            )}
          >
            <div className='font-medium'>{item.label}</div>
              <div className='mt-2 space-y-1 text-muted-foreground'>
                <div>
                  Backlog: {formatQueueNumber(item.data.backlog)}
                </div>
                <div>
                  Dropped: {formatQueueNumber(item.data.dropped)}
                </div>
                <div>
                  Last flush: {formatQueueNumber(item.data.last_flush_items)}
                </div>
                <div>
                  Flush latency: {formatQueueNumber(item.data.last_flush_took_ms)} ms
                </div>
                <div>
                  Updated at: {formatUnixTime(item.data.last_flush_at)}
                </div>
              </div>
            </div>
        ))}
      </div>

      <div className='rounded-xl border bg-muted/20 p-4 text-sm text-muted-foreground'>
        <div>
          Theoretical capacity:{' '}
          {formatQueueNumber(
            Math.round(
              runtimeStatus?.theoretical_pair_rpm ??
                theoreticalPerMinute
            )
          )}{' '}
          records/min
        </div>
        <div className='mt-1'>
          Only covers this ledger path. End-to-end RPM can bottleneck elsewhere.
        </div>
      </div>

      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
            saveLabel='Save pipeline settings'
          />

          <FormField
            control={form.control}
            name='ledger_pipeline_setting.full_drain'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Full Drain')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Drain the whole buffer each cycle. Ignores Paired Flush Max.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name='ledger_pipeline_setting.dedup_use_redis'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Dedup via Redis')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Dedup commission aggregation across instances via shared Redis. Multi-instance MUST enable this to avoid double-counting; single instance can leave it off.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <Separator />

          <FormItem>
            <FormLabel>Target RPM</FormLabel>
            <FormControl>
              <Input
                className={numberInputNoSpinnerClassName}
                type='number'
                inputMode='numeric'
                min={1}
                step={1000}
                value={targetRpm}
                onChange={(event) => {
                  const next = Number(event.target.value)
                  if (!Number.isNaN(next) && next > 0) {
                    setTargetRpm(next)
                  }
                }}
              />
            </FormControl>
            <FormDescription>
              Recommended paired max per cycle:{' '}
              {formatQueueNumber(recommendedPairedFlushMaxPerCycle)}
            </FormDescription>
            <FormDescription>
              Tuning tip: raise Paired Flush Max first; if still not enough,
              lower Flush Interval. When Full Drain is on, this recommendation
              is only a reference.
            </FormDescription>
          </FormItem>

          {numericFields.map((item) => (
            <FormField
              key={item.name}
              control={form.control}
              name={`ledger_pipeline_setting.${item.name}`}
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t(item.label)}</FormLabel>
                  <FormControl>
                    <Input
                      className={numberInputNoSpinnerClassName}
                      type='number'
                      inputMode='numeric'
                      min={item.min}
                      step={1}
                      {...safeNumberFieldProps(field)}
                    />
                  </FormControl>
                  <FormDescription>{t(item.description)}</FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          ))}
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}

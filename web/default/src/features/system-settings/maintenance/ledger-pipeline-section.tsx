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
    cost_outer_batch_size: z.number().int().min(0),
    inner_batch_size: z.number().int().min(1),
    settlement_flush_max_per_cycle: z.number().int().min(1),
    cost_flush_max_per_cycle: z.number().int().min(0),
    full_drain: z.boolean(),
    buf_max_entries: z.number().int().min(1),
    dedup_mem_max_entries: z.number().int().min(1),
    dedup_use_redis: z.boolean(),
    dedup_redis_ttl_sec: z.number().int().min(1),
    flush_db_timeout_sec: z.number().int().min(1),
    fallback_queue_capacity: z.number().int().min(1),
    shutdown_timeout_sec: z.number().int().min(1),
    cache_ttl_secs: z.array(z.number().int().min(1)).length(5),
    cache_ttl_jitter_percent: z.number().int().min(0).max(100),
  }),
  ledger_retry_setting: z.object({
    retry_flush_interval_sec: z.number().int().min(1),
    stat_upsert_max_retries: z.number().int().min(1),
    retry_queue_max_entries: z.number().int().min(1),
    allow_concurrent_flush: z.boolean(),
  }),
})

type FormValues = z.infer<typeof schema>

type FlatDefaults = {
  'ledger_pipeline_setting.flush_interval_sec': number
  'ledger_pipeline_setting.outer_batch_size': number
  'ledger_pipeline_setting.cost_outer_batch_size': number
  'ledger_pipeline_setting.inner_batch_size': number
  'ledger_pipeline_setting.settlement_flush_max_per_cycle': number
  'ledger_pipeline_setting.cost_flush_max_per_cycle': number
  'ledger_pipeline_setting.full_drain': boolean
  'ledger_pipeline_setting.buf_max_entries': number
  'ledger_pipeline_setting.dedup_mem_max_entries': number
  'ledger_pipeline_setting.dedup_use_redis': boolean
  'ledger_pipeline_setting.dedup_redis_ttl_sec': number
  'ledger_pipeline_setting.flush_db_timeout_sec': number
  'ledger_pipeline_setting.fallback_queue_capacity': number
  'ledger_pipeline_setting.shutdown_timeout_sec': number
  'ledger_pipeline_setting.cache_ttl_secs': number[]
  'ledger_pipeline_setting.cache_ttl_jitter_percent': number
  'ledger_retry_setting.retry_flush_interval_sec': number
  'ledger_retry_setting.stat_upsert_max_retries': number
  'ledger_retry_setting.retry_queue_max_entries': number
  'ledger_retry_setting.allow_concurrent_flush': boolean
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
      cost_outer_batch_size: defaults['ledger_pipeline_setting.cost_outer_batch_size'],
      inner_batch_size: defaults['ledger_pipeline_setting.inner_batch_size'],
      settlement_flush_max_per_cycle:
        defaults['ledger_pipeline_setting.settlement_flush_max_per_cycle'],
      cost_flush_max_per_cycle:
        defaults['ledger_pipeline_setting.cost_flush_max_per_cycle'],
      full_drain: defaults['ledger_pipeline_setting.full_drain'],
      buf_max_entries: defaults['ledger_pipeline_setting.buf_max_entries'],
      dedup_mem_max_entries:
        defaults['ledger_pipeline_setting.dedup_mem_max_entries'],
      dedup_use_redis: defaults['ledger_pipeline_setting.dedup_use_redis'],
      dedup_redis_ttl_sec:
        defaults['ledger_pipeline_setting.dedup_redis_ttl_sec'],
      flush_db_timeout_sec:
        defaults['ledger_pipeline_setting.flush_db_timeout_sec'],
      fallback_queue_capacity:
        defaults['ledger_pipeline_setting.fallback_queue_capacity'],
      shutdown_timeout_sec:
        defaults['ledger_pipeline_setting.shutdown_timeout_sec'],
      cache_ttl_secs: defaults['ledger_pipeline_setting.cache_ttl_secs'],
      cache_ttl_jitter_percent:
        defaults['ledger_pipeline_setting.cache_ttl_jitter_percent'],
    },
    ledger_retry_setting: {
      retry_flush_interval_sec:
        defaults['ledger_retry_setting.retry_flush_interval_sec'],
      stat_upsert_max_retries:
        defaults['ledger_retry_setting.stat_upsert_max_retries'],
      retry_queue_max_entries:
        defaults['ledger_retry_setting.retry_queue_max_entries'],
      allow_concurrent_flush:
        defaults['ledger_retry_setting.allow_concurrent_flush'],
    },
  }
}

function normalizeFormValues(values: FormValues): FlatDefaults {
  const s = values.ledger_pipeline_setting
  const r = values.ledger_retry_setting
  return {
    'ledger_pipeline_setting.flush_interval_sec': s.flush_interval_sec,
    'ledger_pipeline_setting.outer_batch_size': s.outer_batch_size,
    'ledger_pipeline_setting.cost_outer_batch_size': s.cost_outer_batch_size,
    'ledger_pipeline_setting.inner_batch_size': s.inner_batch_size,
    'ledger_pipeline_setting.settlement_flush_max_per_cycle':
      s.settlement_flush_max_per_cycle,
    'ledger_pipeline_setting.cost_flush_max_per_cycle':
      s.cost_flush_max_per_cycle,
    'ledger_pipeline_setting.full_drain': s.full_drain,
    'ledger_pipeline_setting.buf_max_entries': s.buf_max_entries,
    'ledger_pipeline_setting.dedup_mem_max_entries': s.dedup_mem_max_entries,
    'ledger_pipeline_setting.dedup_use_redis': s.dedup_use_redis,
    'ledger_pipeline_setting.dedup_redis_ttl_sec': s.dedup_redis_ttl_sec,
    'ledger_pipeline_setting.flush_db_timeout_sec': s.flush_db_timeout_sec,
    'ledger_pipeline_setting.fallback_queue_capacity': s.fallback_queue_capacity,
    'ledger_pipeline_setting.shutdown_timeout_sec': s.shutdown_timeout_sec,
    'ledger_pipeline_setting.cache_ttl_secs': s.cache_ttl_secs,
    'ledger_pipeline_setting.cache_ttl_jitter_percent': s.cache_ttl_jitter_percent,
    'ledger_retry_setting.retry_flush_interval_sec': r.retry_flush_interval_sec,
    'ledger_retry_setting.stat_upsert_max_retries': r.stat_upsert_max_retries,
    'ledger_retry_setting.retry_queue_max_entries': r.retry_queue_max_entries,
    'ledger_retry_setting.allow_concurrent_flush': r.allow_concurrent_flush,
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
  // ── Throughput controls ────────────────────────────────
  {
    name: 'flush_interval_sec',
    label: 'Flush Interval',
    description: 'Seconds between flushes.',
    min: 1,
  },
  {
    name: 'settlement_flush_max_per_cycle',
    label: 'Settlement Queue: Max Per Cycle',
    description: 'Settlement queue (cost + commission pairs): max records flushed per cycle. Ignored when Full Drain is on.',
    min: 1,
  },
  {
    name: 'cost_flush_max_per_cycle',
    label: 'Cost Queue: Max Per Cycle',
    description: 'Cost queue (cost-only records): max records flushed per cycle. 0 = drain all each cycle (default). Caps the DB burst when recovering from an outage. Ignored when Full Drain is on.',
    min: 0,
  },
  // ── Batch sizing ───────────────────────────────────────
  {
    name: 'outer_batch_size',
    label: 'Batch Size (Settlement Queue)',
    description: 'Records per outer loop for the settlement queue — error-recovery/transaction granularity. Also serves as the fallback for the cost queue when Cost Queue Batch Size is 0.',
    min: 1,
  },
  {
    name: 'cost_outer_batch_size',
    label: 'Batch Size (Cost Queue)',
    description: 'Cost queue (cost-only records) batch size per write iteration. 0 = inherit the settlement queue batch size.',
    min: 0,
  },
  {
    name: 'inner_batch_size',
    label: 'Inner Batch Size',
    description: 'Rows per SQL INSERT statement. Must be ≤ outer batch size.',
    min: 1,
  },
  // ── Buffer and dedup ───────────────────────────────────
  {
    name: 'buf_max_entries',
    label: 'Buffer Max Entries',
    description: 'Per-queue buffer ceiling. Overflow evicts oldest entries to the fallback file; only permanently lost if the fallback queue is also full.',
    min: 1,
  },
  {
    name: 'dedup_mem_max_entries',
    label: 'Dedup Memory Max Entries',
    description: 'Dedup set ceiling. Rebuilt on overflow; DB ON CONFLICT still guarantees idempotence.',
    min: 1,
  },
  {
    name: 'dedup_redis_ttl_sec',
    label: 'Dedup Redis TTL',
    description: 'TTL of each Redis dedup key (seconds). Used only when Dedup via Redis is on.',
    min: 1,
  },
  // ── Timeouts and safety bounds ─────────────────────────
  {
    name: 'flush_db_timeout_sec',
    label: 'Flush DB Timeout',
    description: 'Per-flush DB write deadline (seconds). Recommended < flush interval.',
    min: 1,
  },
  {
    name: 'fallback_queue_capacity',
    label: 'File Write Queue Capacity',
    description:
      'Capacity of the fallback file-write queue. When full, overflow entries are dropped and the circuit breaker is permanently disabled.',
    min: 1,
  },
  {
    name: 'shutdown_timeout_sec',
    label: 'Shutdown Timeout',
    description:
      'Covers goroutine stop, final DB flush, and fallback drain. Must be less than the HTTP server shutdown timeout (30 s).',
    min: 1,
  },
]

// Labels for the per-cache TTL array. Order MUST match the backend CacheIdx*
// constants in setting/operation_setting/ledger_setting.go.
const cacheTtlLabels: Array<{ label: string; description: string }> = [
  {
    label: 'Channel Cost Ratio Cache TTL',
    description: 'Per-settlement channel cost-ratio cache (seconds). L1 memory → Redis → DB. Default 300s.',
  },
  {
    label: 'Inviter Cache TTL',
    description: 'user → inviter id cache used for commission attribution (seconds). Default 300s.',
  },
  {
    label: 'Employee Profile Cache TTL',
    description: 'user → employee profile cache used for commission attribution (seconds). Default 300s.',
  },
  {
    label: 'Tier Level Cache TTL',
    description: 'Per-employee tier-level L1 memory cache, prefetched during the settlement flush (seconds). Default 300s.',
  },
  {
    label: 'Tier Definitions Cache TTL',
    description: 'Commission tier-definitions cache (seconds). Default 300s.',
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

  // Watch both sub-objects to derive all dynamic hints in one pass
  const pWatch = form.watch('ledger_pipeline_setting')
  const rWatch = form.watch('ledger_retry_setting')

  const flushSec = Math.max(1, pWatch.flush_interval_sec || 8)
  const outerBatch = Math.max(1, pWatch.outer_batch_size || 2000)
  const innerBatch = pWatch.inner_batch_size || 500
  const settlementMax = pWatch.settlement_flush_max_per_cycle || 15000
  const dbTimeout = pWatch.flush_db_timeout_sec || 30
  const shutdownSec = pWatch.shutdown_timeout_sec || 25
  const costOuterBatch = pWatch.cost_outer_batch_size ?? 0
  const effectiveCostOuterBatch = costOuterBatch > 0 ? costOuterBatch : outerBatch
  const costFlushMax = pWatch.cost_flush_max_per_cycle ?? 0
  const bufMax = pWatch.buf_max_entries || 100000
  const dedupMem = pWatch.dedup_mem_max_entries || 200000
  const dedupTtl = pWatch.dedup_redis_ttl_sec || 21600
  const retryInterval = Math.max(1, rWatch.retry_flush_interval_sec || 2)
  const maxRetries = rWatch.stat_upsert_max_retries || 10

  const theoreticalPerMinute = (settlementMax * 60) / flushSec
  const [targetRpm, setTargetRpm] = useState(() =>
    Math.max(1, Math.round(theoreticalPerMinute))
  )
  const recommendedSettlementFlushMaxPerCycle = Math.ceil((targetRpm / 60) * flushSec)
  const recommendedRetryIntervalSec = Math.max(1, Math.floor(flushSec / 4))
  // Minimum dedup TTL: covers from record entry (T=0) to last retry attempt.
  // First flush fires within flushSec; then maxRetries retry cycles at retryInterval each.
  const minDedupRedisTtl = flushSec + maxRetries * retryInterval

  // Dynamic per-field hints derived from backend execution logic
  type FieldHint = { text: string; warn: boolean }
  const fieldHints: Partial<Record<string, FieldHint>> = {
    flush_interval_sec: {
      text: t('Throughput at current config: {{value}} settlement records/min', {
        value: formatQueueNumber(Math.round(theoreticalPerMinute)),
      }),
      warn: false,
    },
    inner_batch_size: innerBatch > outerBatch
      ? { text: t('Must be ≤ settlement batch size ({{value}})', { value: outerBatch }), warn: true }
      : { text: t('Recommended ≈ batch size ÷ 4 = {{value}}', { value: Math.max(1, Math.floor(outerBatch / 4)) }), warn: false },
    // settlement_flush_max_per_cycle need not be a multiple of outer_batch_size —
    // the flush loop slices buf[:pairedMax] then iterates in outerBatch steps;
    // the last iteration simply processes the remaining records.
    settlement_flush_max_per_cycle: {
      text: settlementMax % outerBatch !== 0
        ? t('Settlement queue: ≈ {{count}} batch(es) per cycle (last batch: {{last}} records)', {
            count: Math.ceil(settlementMax / outerBatch),
            last: settlementMax % outerBatch,
          })
        : t('Settlement queue: {{count}} batch(es) per flush cycle', {
            count: settlementMax / outerBatch,
          }),
      warn: false,
    },
    cost_outer_batch_size: costOuterBatch === 0
      ? { text: t('0 = inherit settlement batch size ({{value}})', { value: outerBatch }), warn: false }
      : { text: t('Cost queue: {{cost}} per batch; settlement queue: {{settlement}}', { cost: costOuterBatch, settlement: outerBatch }), warn: false },
    cost_flush_max_per_cycle: costFlushMax === 0
      ? { text: t('Cost queue: 0 = drain all each cycle (default, no cap)'), warn: false }
      : {
          text: t('Cost queue: ≈ {{count}} batch(es) per cycle; remainder deferred to next cycle', {
            count: Math.ceil(costFlushMax / effectiveCostOuterBatch),
          }),
          warn: false,
        },
    buf_max_entries: {
      text: t('{{value}}× settlement batch size', { value: Math.floor(bufMax / outerBatch) }),
      warn: bufMax < outerBatch * 5,
    },
    dedup_mem_max_entries: dedupMem <= bufMax
      ? {
          text: t('Should exceed buffer max ({{value}}) to avoid frequent set rebuilds', {
            value: formatQueueNumber(bufMax),
          }),
          warn: true,
        }
      : {
          text: t('Covers buffer max ({{value}}) ✓', { value: formatQueueNumber(bufMax) }),
          warn: false,
        },
    // Formula from code: key is set at entry (T=0); must survive until the last retry attempt.
    // Lifecycle: initial flush (≤ flushSec) + maxRetries retry cycles (× retryInterval each).
    dedup_redis_ttl_sec: {
      text: `${t('Retry lifecycle: {{sec}} s flush + {{retries}} × {{interval}} s = {{min}} s min', {
        sec: flushSec,
        retries: maxRetries,
        interval: retryInterval,
        min: minDedupRedisTtl,
      })}${dedupTtl < minDedupRedisTtl ? ` — ${t('current value too short')}` : ' ✓'}`,
      warn: dedupTtl < minDedupRedisTtl,
    },
    flush_db_timeout_sec: dbTimeout >= flushSec
      ? {
          text: t('Recommended < flush interval ({{sec}} s) — a DB stall at this value blocks the next flush cycle', {
            sec: flushSec,
          }),
          warn: true,
        }
      : {
          text: t('Recommended ≤ {{value}} s (flush {{sec}} s − 2)', {
            value: Math.max(1, flushSec - 2),
            sec: flushSec,
          }),
          warn: false,
        },
    shutdown_timeout_sec: shutdownSec >= 30
      ? { text: t('Must be < HTTP shutdown timeout (30 s)'), warn: true }
      : {
          text: t('Covers goroutine stop + final DB flush. Recommended 20–25 s to leave room for HTTP shutdown (30 s hard limit)'),
          warn: false,
        },
  }

  const runtimeStatus = statusQuery.data?.data
  const queueCards = runtimeStatus
    ? [
        {
          key: 'cost',
          label: t('Cost queue'),
          description: t('Cost records, no commission'),
          data: runtimeStatus.snapshot.cost,
          showDropped: true,
          capacity: (runtimeStatus.buf_max_entries as number) ?? null,
          maxRetries: null as number | null,
        },
        {
          key: 'pair',
          label: t('Settlement queue'),
          description: t('Cost + commission pairs for commission-bearing requests'),
          data: runtimeStatus.snapshot.pair,
          showDropped: true,
          capacity: (runtimeStatus.buf_max_entries as number) ?? null,
          maxRetries: null as number | null,
        },
        {
          key: 'cost_retry',
          label: t('Cost retry queue'),
          description: t('Cost records retrying after flush failure'),
          data: runtimeStatus.snapshot.cost_retry,
          showDropped: false,
          capacity: (runtimeStatus.retry_queue_max_entries as number) ?? null,
          maxRetries: (runtimeStatus.stat_upsert_max_retries as number) ?? null,
        },
        {
          key: 'pair_retry',
          label: t('Settlement retry queue'),
          description: t('Settlement pairs retrying after flush failure'),
          data: runtimeStatus.snapshot.pair_retry,
          showDropped: false,
          capacity: (runtimeStatus.retry_queue_max_entries as number) ?? null,
          maxRetries: (runtimeStatus.stat_upsert_max_retries as number) ?? null,
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
              (defaultValues['ledger_pipeline_setting.settlement_flush_max_per_cycle'] *
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
    ).filter((key) => {
      const next = normalized[key]
      const prev = baselineRef.current[key]
      if (Array.isArray(next) || Array.isArray(prev)) {
        return JSON.stringify(next) !== JSON.stringify(prev)
      }
      return next !== prev
    })

    if (changedKeys.length === 0) {
      toast.info(t('No changes to save'))
      return
    }

    for (const key of changedKeys) {
      const value = normalized[key]
      // Array-valued options (e.g. cache TTLs) are stored as a JSON string.
      await updateOption.mutateAsync({
        key,
        value: Array.isArray(value) ? JSON.stringify(value) : value,
      })
    }

    baselineRef.current = normalized
    baselineSerializedRef.current = JSON.stringify(normalized)
    form.reset(buildFormDefaults(normalized))
  }

  return (
    <SettingsSection title={t('Ledger Pipeline')}>
      <div className='grid gap-3 sm:grid-cols-2'>
        {queueCards.map((item) => (
          <div
            key={item.key}
            className={cn(
              'rounded-xl border bg-muted/20 p-4 text-sm',
              item.key === 'pair' && 'border-primary/30'
            )}
          >
            <div className='font-medium'>{item.label}</div>
            <div className='mt-1 text-xs text-muted-foreground/70'>{item.description}</div>
              <div className='mt-2 space-y-1 text-muted-foreground'>
                <div>
                  {t('Backlog:')}{' '}
                  {formatQueueNumber(item.data?.backlog ?? 0)}
                  {item.capacity != null && (
                    <span className='text-muted-foreground/60'> / {formatQueueNumber(item.capacity)}</span>
                  )}
                </div>
                {item.showDropped && (
                  <div>
                    {t('Dropped:')} {formatQueueNumber(item.data?.dropped ?? 0)}
                  </div>
                )}
                <div>
                  {t('Last flush:')} {formatQueueNumber(item.data?.last_flush_items ?? 0)}
                </div>
                <div>
                  {t('Flush latency:')} {formatQueueNumber(item.data?.last_flush_took_ms ?? 0)} ms
                </div>
                {item.maxRetries != null && (
                  <div>{t('Max retries:')} {item.maxRetries}</div>
                )}
                {item.showDropped && (
                  <div>
                    {t('Updated at:')} {formatUnixTime(item.data?.last_flush_at ?? 0)}
                  </div>
                )}
              </div>
            </div>
        ))}
      </div>

      <div className='rounded-xl border bg-muted/20 p-4 text-sm text-muted-foreground'>
        <div>
          {t('Theoretical capacity: {{value}} records/min', {
            value: formatQueueNumber(
              Math.round(
                runtimeStatus?.theoretical_pair_rpm ??
                  theoreticalPerMinute
              )
            ),
          })}
        </div>
        <div className='mt-1'>
          {t('Only covers this ledger path. End-to-end RPM can bottleneck elsewhere.')}
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
            <FormLabel>{t('Target RPM')}</FormLabel>
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
              {t('Recommended paired max per cycle: {{value}}', {
                value: formatQueueNumber(recommendedSettlementFlushMaxPerCycle),
              })}
            </FormDescription>
            <FormDescription>
              {t(
                'Tuning tip: raise Paired Flush Max first; if still not enough, lower Flush Interval. When Full Drain is on, this recommendation is only a reference.'
              )}
            </FormDescription>
          </FormItem>

          {numericFields.map((item) => {
            const hint = fieldHints[item.name]
            return (
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
                    {hint && (
                      <FormDescription
                        className={
                          hint.warn
                            ? 'text-destructive'
                            : 'text-primary/70'
                        }
                      >
                        {hint.text}
                      </FormDescription>
                    )}
                    <FormMessage />
                  </FormItem>
                )}
              />
            )
          })}

          <Separator />
          <div className='text-sm font-medium text-foreground'>
            {t('Hot-path Cache TTLs')}
          </div>
          <FormDescription>
            {t(
              'Expiry (seconds) of the caches on the settlement/flush hot path, one per cache. Give them different values so they do not all expire on the same tick — the synchronized expiry causes the periodic flush-latency spike.'
            )}
          </FormDescription>
          {cacheTtlLabels.map((item, idx) => (
            <FormField
              key={idx}
              control={form.control}
              name={`ledger_pipeline_setting.cache_ttl_secs.${idx}`}
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t(item.label)}</FormLabel>
                  <FormControl>
                    <Input
                      className={numberInputNoSpinnerClassName}
                      type='number'
                      inputMode='numeric'
                      min={1}
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

          <FormField
            control={form.control}
            name='ledger_pipeline_setting.cache_ttl_jitter_percent'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Cache TTL Jitter (%)')}</FormLabel>
                <FormControl>
                  <Input
                    className={numberInputNoSpinnerClassName}
                    type='number'
                    inputMode='numeric'
                    min={0}
                    max={100}
                    step={1}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Random ±% spread applied to each cache entry’s TTL so entries expire staggered instead of in one synchronized wave. 0 disables jitter. Default 20%.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <Separator />
          <div className='text-sm font-medium text-foreground'>{t('Retry Queue')}</div>

          <FormField
            control={form.control}
            name='ledger_retry_setting.allow_concurrent_flush'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Allow Concurrent Flush')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Off (default): retry uses TryLock — skips the cycle when the main flush is in progress, eliminating concurrent DB writes to the same tables. On: retry runs concurrently with the main flush for higher retry throughput at the cost of connection-pool contention.'
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
            name='ledger_retry_setting.retry_queue_max_entries'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Retry Queue Max Entries')}</FormLabel>
                <FormControl>
                  <Input
                    className={numberInputNoSpinnerClassName}
                    type='number'
                    inputMode='numeric'
                    min={1}
                    step={1}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Per-queue ceiling for costRetryQueue and pairRetryQueue (each capped independently). Overflow writes to the fallback file.'
                  )}
                </FormDescription>
                <FormDescription className='text-primary/70'>
                  {t('{{value}}× outer batch size', {
                    value: Math.floor((rWatch.retry_queue_max_entries || 50000) / (pWatch.outer_batch_size || 2000)),
                  })}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='ledger_retry_setting.retry_flush_interval_sec'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Retry Flush Interval')}</FormLabel>
                <FormControl>
                  <Input
                    className={numberInputNoSpinnerClassName}
                    type='number'
                    inputMode='numeric'
                    min={1}
                    step={1}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Wake-up interval (seconds) of the retry goroutine. Uses TryLock to avoid concurrent DB writes with the main flush: skips the cycle if the main flush is in progress.'
                  )}
                </FormDescription>
                <FormDescription className='text-primary/70'>
                  {t('Recommended')}:{' '}
                  <span className='font-medium'>≤ {recommendedRetryIntervalSec} s</span>{' '}
                  {t('(flush interval {{sec}} s ÷ 4)', { sec: flushSec })}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='ledger_retry_setting.stat_upsert_max_retries'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Max Retries')}</FormLabel>
                <FormControl>
                  <Input
                    className={numberInputNoSpinnerClassName}
                    type='number'
                    inputMode='numeric'
                    min={1}
                    step={1}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Max attempts before a failed record is written to the fallback file for manual backfill.'
                  )}
                </FormDescription>
                <FormDescription className='text-primary/70'>
                  {t('Affects Redis dedup TTL: {{sec}} s flush + {{retries}} × {{interval}} s = {{min}} s min', {
                    sec: flushSec,
                    retries: maxRetries,
                    interval: retryInterval,
                    min: minDedupRedisTtl,
                  })}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}

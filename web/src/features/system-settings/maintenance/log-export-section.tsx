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
import { useEffect, useMemo, useRef } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

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

import { SettingsForm } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import {
  numberInputNoSpinnerClassName,
  safeNumberFieldProps,
} from '../utils/numeric-field'

/**
 * react-hook-form 7 interprets dotted `name` strings as nested paths.
 * We model the form with nested objects and flatten before persisting.
 */
const schema = z.object({
  log_export_setting: z.object({
    enabled: z.boolean(),
    user_cooldown_sec: z.number().int().min(0).max(86400),
    // 0 是有意义的取值：停止接受新任务，但不打断运行中的任务。
    max_concurrent_jobs: z.number().int().min(0).max(16),
    max_active_jobs_per_user: z.number().int().min(1).max(20),
    admin_max_range_sec: z.number().int().min(1).max(31622400),
    timeout_sec: z.number().int().min(1).max(86400),
    job_ttl_hours: z.number().int().min(1).max(720),
    max_templates_per_user: z.number().int().min(1).max(500),

    batch_size: z.number().int().min(1).max(50000),
    batch_sleep_ms: z.number().int().min(0).max(60000),
    batch_query_timeout_sec: z.number().int().min(1).max(300),
    window_sec: z.number().int().min(60).max(86400),
    max_rows_per_sec: z.number().int().min(1).max(500000),
    cpu_soft_limit: z.number().int().min(1).max(100),
    // 0 = 一键刹车：所有运行中的任务立即暂停。
    cpu_hard_limit: z.number().int().min(0).max(100),
    cpu_check_interval_ms: z.number().int().min(1).max(60000),
    gzip_level: z.number().int().min(1).max(9),
    rows_per_file: z.number().int().min(1).max(5000000),
    max_parts: z.number().int().min(1).max(1000),
    xlsx_max_rows: z.number().int().min(1).max(1000000),
    min_free_disk_mb: z.number().int().min(1).max(1048576),
    offpeak_only: z.boolean(),
    offpeak_window: z.string(),

    download_token_ttl_sec: z.number().int().min(1).max(3600),
    download_session_ttl_sec: z.number().int().min(1).max(43200),
    max_concurrent_downloads_per_user: z.number().int().min(1).max(16),
  }),
})

type FormValues = z.infer<typeof schema>
type Settings = FormValues['log_export_setting']

export type LogExportFlatDefaults = {
  [K in keyof Settings as `log_export_setting.${K & string}`]: Settings[K]
}

type LogExportSectionProps = { defaultValues: LogExportFlatDefaults }

const FIELD_KEYS = [
  'enabled',
  'user_cooldown_sec',
  'max_concurrent_jobs',
  'max_active_jobs_per_user',
  'admin_max_range_sec',
  'timeout_sec',
  'job_ttl_hours',
  'max_templates_per_user',
  'batch_size',
  'batch_sleep_ms',
  'batch_query_timeout_sec',
  'window_sec',
  'max_rows_per_sec',
  'cpu_soft_limit',
  'cpu_hard_limit',
  'cpu_check_interval_ms',
  'gzip_level',
  'rows_per_file',
  'max_parts',
  'xlsx_max_rows',
  'min_free_disk_mb',
  'offpeak_only',
  'offpeak_window',
  'download_token_ttl_sec',
  'download_session_ttl_sec',
  'max_concurrent_downloads_per_user',
] as const satisfies ReadonlyArray<keyof Settings>

function buildFormDefaults(d: LogExportFlatDefaults): FormValues {
  const settings = {} as Settings
  for (const key of FIELD_KEYS) {
    // @ts-expect-error index write over a discriminated key union
    settings[key] = d[`log_export_setting.${key}`]
  }
  return { log_export_setting: settings }
}

function normalizeFormValues(v: FormValues): LogExportFlatDefaults {
  const out = {} as LogExportFlatDefaults
  for (const key of FIELD_KEYS) {
    // @ts-expect-error index write over a discriminated key union
    out[`log_export_setting.${key}`] = v.log_export_setting[key]
  }
  return out
}

type NumberField = {
  name: Exclude<keyof Settings, 'enabled' | 'offpeak_only' | 'offpeak_window'>
  label: string
  description: string
  min: number
  max?: number
}

const quotaFields: NumberField[] = [
  {
    name: 'user_cooldown_sec',
    label: 'Export Cooldown (s)',
    description:
      'Minimum seconds between two export jobs from the same admin. A rejected request does not consume the cooldown.',
    min: 0,
    max: 86400,
  },
  {
    name: 'max_concurrent_jobs',
    label: 'Max Concurrent Jobs',
    description:
      'Export jobs allowed to run at once. Set to 0 to stop accepting new jobs; running jobs are not interrupted.',
    min: 0,
    max: 16,
  },
  {
    name: 'max_active_jobs_per_user',
    label: 'Max Active Jobs Per User',
    description: 'Unfinished jobs one admin may keep at the same time.',
    min: 1,
    max: 20,
  },
  {
    name: 'admin_max_range_sec',
    label: 'Max Export Time Range (s)',
    description:
      'Longest time span a single export may cover. Also drives the range hint in the export dialog.',
    min: 1,
    max: 31622400,
  },
  {
    name: 'timeout_sec',
    label: 'Job Timeout (s)',
    description:
      'Working-time budget for one job. Time yielded to the resource gate does not count, so a throttled job is not killed.',
    min: 1,
    max: 86400,
  },
  {
    name: 'job_ttl_hours',
    label: 'Job Retention (hours)',
    description: 'How long finished jobs and their part files are kept before cleanup.',
    min: 1,
    max: 720,
  },
  {
    name: 'max_templates_per_user',
    label: 'Max Templates Per User',
    description: 'Upper bound on saved column templates per admin.',
    min: 1,
    max: 500,
  },
]

const throttleFields: NumberField[] = [
  {
    name: 'batch_size',
    label: 'Batch Size',
    description: 'Rows fetched from the log database per batch.',
    min: 1,
    max: 50000,
  },
  {
    name: 'batch_sleep_ms',
    label: 'Batch Sleep (ms)',
    description:
      'Base pause after each batch. The main lever for reducing database pressure — raising it slows exports down linearly.',
    min: 0,
    max: 60000,
  },
  {
    name: 'batch_query_timeout_sec',
    label: 'Batch Query Timeout (s)',
    description: 'Timeout for a single batch query, so a slow query cannot hold a connection.',
    min: 1,
    max: 300,
  },
  {
    name: 'window_sec',
    label: 'Scan Window (s)',
    description:
      'Time slice scanned at a time. Smaller windows keep index ranges tight; below 60s the value is clamped to avoid flooding the database with empty queries.',
    min: 60,
    max: 86400,
  },
  {
    name: 'max_rows_per_sec',
    label: 'Max Rows Per Second',
    description: 'Token-bucket ceiling on export read speed.',
    min: 1,
    max: 500000,
  },
  {
    name: 'cpu_soft_limit',
    label: 'CPU Soft Limit (%)',
    description: 'Above this CPU usage the export progressively sleeps longer.',
    min: 1,
    max: 100,
  },
  {
    name: 'cpu_hard_limit',
    label: 'CPU Hard Limit (%)',
    description:
      'Above this CPU usage the export pauses. Set to 0 as an emergency brake: running jobs pause within a second and keep their progress.',
    min: 0,
    max: 100,
  },
  {
    name: 'cpu_check_interval_ms',
    label: 'CPU Check Interval (ms)',
    description: 'How often the resource gate re-reads CPU usage while paused.',
    min: 1,
    max: 60000,
  },
]

const outputFields: NumberField[] = [
  {
    name: 'gzip_level',
    label: 'Gzip Level (1-9)',
    description:
      'Compression level for csv.gz. Level 1 costs 2-3x less CPU than the default with under 10% larger files. Applies to newly started parts only.',
    min: 1,
    max: 9,
  },
  {
    name: 'rows_per_file',
    label: 'Rows Per Part',
    description: 'Rows written before a new part file is started. Applies to newly started parts only.',
    min: 1,
    max: 5000000,
  },
  {
    name: 'max_parts',
    label: 'Max Parts',
    description: 'Part limit for one job. Exceeding it fails the job instead of filling the disk.',
    min: 1,
    max: 1000,
  },
  {
    name: 'xlsx_max_rows',
    label: 'Excel Row Limit',
    description: 'Above this row count the export falls back to CSV, which is far cheaper to produce.',
    min: 1,
    max: 1000000,
  },
  {
    name: 'min_free_disk_mb',
    label: 'Min Free Disk (MB)',
    description:
      'Jobs are refused when free space is below this, and aborted if it drops mid-run. Part files land in the system temp directory.',
    min: 1,
    max: 1048576,
  },
]

const downloadFields: NumberField[] = [
  {
    name: 'download_token_ttl_sec',
    label: 'Download Link Lifetime (s)',
    description: 'How long a freshly issued download link stays valid before first use.',
    min: 1,
    max: 3600,
  },
  {
    name: 'download_session_ttl_sec',
    label: 'Resume Window (s)',
    description:
      'After first use a link stays usable for this long so interrupted downloads can resume.',
    min: 1,
    max: 43200,
  },
  {
    name: 'max_concurrent_downloads_per_user',
    label: 'Max Concurrent Downloads',
    description: 'Simultaneous part downloads allowed per admin.',
    min: 1,
    max: 16,
  },
]

export function LogExportSection({ defaultValues }: LogExportSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const formDefaults = useMemo(
    () => buildFormDefaults(defaultValues),
    [defaultValues]
  )

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: formDefaults,
  })

  const baselineRef = useRef<LogExportFlatDefaults>(defaultValues)
  const baselineSerializedRef = useRef<string>(JSON.stringify(defaultValues))

  useEffect(() => {
    const serialized = JSON.stringify(defaultValues)
    if (serialized === baselineSerializedRef.current) return
    baselineRef.current = defaultValues
    baselineSerializedRef.current = serialized
    form.reset(buildFormDefaults(defaultValues))
  }, [defaultValues, form])

  const onSubmit = async (values: FormValues) => {
    const normalized = normalizeFormValues(values)
    const changedKeys = (
      Object.keys(normalized) as Array<keyof LogExportFlatDefaults>
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

  const renderNumberFields = (fields: NumberField[]) =>
    fields.map((item) => (
      <FormField
        key={item.name}
        control={form.control}
        name={`log_export_setting.${item.name}`}
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t(item.label)}</FormLabel>
            <FormControl>
              <Input
                className={numberInputNoSpinnerClassName}
                type='number'
                inputMode='numeric'
                min={item.min}
                max={item.max}
                step={1}
                {...safeNumberFieldProps(field)}
              />
            </FormControl>
            <FormDescription>{t(item.description)}</FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
    ))

  return (
    <SettingsSection title={t('Usage Log Export Settings')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
            saveLabel='Save log export settings'
          />

          <FormField
            control={form.control}
            name='log_export_setting.enabled'
            render={({ field }) => (
              <FormItem className='flex flex-row items-center justify-between gap-4'>
                <div className='space-y-0.5'>
                  <FormLabel>{t('Enable Background Export')}</FormLabel>
                  <FormDescription>
                    {t(
                      'When off, new export jobs are refused. Running jobs keep going; cancel them from the export center.'
                    )}
                  </FormDescription>
                </div>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </FormItem>
            )}
          />

          <Separator />

          <div>
            <h4 className='text-sm font-medium'>{t('Quotas and Limits')}</h4>
          </div>
          {renderNumberFields(quotaFields)}

          <Separator />

          <div>
            <h4 className='text-sm font-medium'>{t('Resource Throttling')}</h4>
          </div>
          {renderNumberFields(throttleFields)}

          <FormField
            control={form.control}
            name='log_export_setting.offpeak_only'
            render={({ field }) => (
              <FormItem className='flex flex-row items-center justify-between gap-4'>
                <div className='space-y-0.5'>
                  <FormLabel>{t('Off-peak Only')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Restrict exports to the window below. Jobs created outside it wait instead of failing, and the wait does not consume the job timeout.'
                    )}
                  </FormDescription>
                </div>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='log_export_setting.offpeak_window'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Off-peak Window')}</FormLabel>
                <FormControl>
                  <Input placeholder='02:00-06:00' {...field} />
                </FormControl>
                <FormDescription>
                  {t(
                    'Server local time, HH:MM-HH:MM. Windows crossing midnight (22:00-04:00) are supported; an unparsable value falls back to the default instead of blocking exports.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <Separator />

          <div>
            <h4 className='text-sm font-medium'>{t('Output Files')}</h4>
          </div>
          {renderNumberFields(outputFields)}

          <Separator />

          <div>
            <h4 className='text-sm font-medium'>{t('Downloads')}</h4>
          </div>
          {renderNumberFields(downloadFields)}
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}

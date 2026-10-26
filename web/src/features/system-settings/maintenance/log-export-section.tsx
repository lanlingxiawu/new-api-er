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

import { numericPresetFieldProps } from '@/components/numeric-preset-field'
import {
  NumericPresetInput,
  type NumericPreset,
} from '@/components/numeric-preset-input'
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
import { useSettingsSaveConfirmation } from '../components/settings-save-confirmation'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
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
    employee_export_enabled: z.boolean(),
    employee_customer_manage_enabled: z.boolean(),
    employee_max_customers_per_job: z.number().int().min(1).max(1000),
    user_cooldown_sec: z.number().int().min(0).max(86400),
    // 0 是有意义的取值：停止接受新任务，但不打断运行中的任务。
    max_concurrent_jobs: z.number().int().min(0).max(16),
    max_active_jobs_per_user: z.number().int().min(1).max(20),
    user_max_range_sec: z.number().int().min(1).max(31622400),
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
  'employee_export_enabled',
  'employee_customer_manage_enabled',
  'employee_max_customers_per_job',
  'user_cooldown_sec',
  'max_concurrent_jobs',
  'max_active_jobs_per_user',
  'admin_max_range_sec',
  'user_max_range_sec',
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
  name: Exclude<
    keyof Settings,
    | 'enabled'
    | 'employee_export_enabled'
    | 'employee_customer_manage_enabled'
    | 'offpeak_only'
    | 'offpeak_window'
  >
  label: string
  description: string
  min: number
  max?: number
  // Special values offered in a dropdown; `min`/`max` then bound ordinary numbers only.
  presets?: NumericPreset[]
}

const quotaFields: NumberField[] = [
  {
    name: 'employee_max_customers_per_job',
    label: 'Customers per export',
    description:
      'Maximum customers in one employee export, including all-customer exports.',
    min: 1,
    max: 1000,
  },
  {
    name: 'user_cooldown_sec',
    label: 'Export Cooldown (s)',
    description:
      'Minimum seconds between exports from the same account. Rejected requests do not start the cooldown.',
    min: 1,
    max: 86400,
    // GetUserCooldownSec treats 0 as unset and applies DefaultLogExportUserCooldownSec.
    presets: [{ value: 0, label: 'Default 300 seconds' }],
  },
  {
    name: 'max_concurrent_jobs',
    label: 'Max Concurrent Jobs',
    description:
      'Export jobs allowed to run at once. Set to 0 to stop accepting new jobs; running jobs are not interrupted.',
    min: 1,
    max: 16,
    presets: [{ value: 0, label: 'Stop accepting new jobs' }],
  },
  {
    name: 'max_active_jobs_per_user',
    label: 'Max Active Jobs Per User',
    description: 'Maximum unfinished export tasks per account.',
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
    name: 'user_max_range_sec',
    label: 'User Export Time Range (s)',
    description: 'Maximum time range for users exporting their own logs.',
    min: 1,
    max: 31622400,
  },
  {
    name: 'timeout_sec',
    label: 'Job Timeout (s)',
    description:
      'Maximum elapsed time for one job, including time waiting for resources.',
    min: 1,
    max: 86400,
  },
  {
    name: 'job_ttl_hours',
    label: 'Download retention (hours)',
    description:
      'Download availability after the last task update, usually completion. Expired files are cleaned up when a new export starts or the server restarts.',
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
      'Pause between batches. Increase this value to reduce database load and slow down exports.',
    min: 1,
    max: 60000,
    presets: [{ value: 0, label: 'No pause' }],
  },
  {
    name: 'batch_query_timeout_sec',
    label: 'Batch Query Timeout (s)',
    description:
      'Timeout for a single batch query, so a slow query cannot hold a connection.',
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
      'Pause exports when CPU usage exceeds this value. Set to 0 to pause all exports; tasks keep their progress.',
    min: 1,
    max: 100,
    presets: [{ value: 0, label: 'Pause all exports' }],
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
      'Compression level for CSV.GZ files. Lower levels generally use less CPU but produce larger files. Applies to new file parts.',
    min: 1,
    max: 9,
  },
  {
    name: 'rows_per_file',
    label: 'Rows Per Part',
    description:
      'Rows written before a new part file is started. Applies to newly started parts only.',
    min: 1,
    max: 5000000,
  },
  {
    name: 'max_parts',
    label: 'Max Parts',
    description:
      'Part limit for one job. Exceeding it fails the job instead of filling the disk.',
    min: 1,
    max: 1000,
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
    description:
      'How long a freshly issued download link stays valid before first use.',
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
    description: 'Maximum simultaneous file downloads per account.',
    min: 1,
    max: 16,
  },
]

export function LogExportSection({ defaultValues }: LogExportSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const requestSaveConfirmation = useSettingsSaveConfirmation()

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

  useResetForm(form, buildFormDefaults(defaultValues))

  useEffect(() => {
    const serialized = JSON.stringify(defaultValues)
    if (serialized === baselineSerializedRef.current) return
    baselineRef.current = defaultValues
    baselineSerializedRef.current = serialized
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
    await requestSaveConfirmation(async () => {
      for (const key of changedKeys) {
        await updateOption.mutateAsync({ key, value: normalized[key] })
      }
      baselineRef.current = normalized
      baselineSerializedRef.current = JSON.stringify(normalized)
      form.reset(buildFormDefaults(normalized))
    })
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
              {item.presets ? (
                <NumericPresetInput
                  presets={item.presets}
                  min={item.min}
                  max={item.max}
                  {...numericPresetFieldProps(field)}
                />
              ) : (
                <Input
                  className={numberInputNoSpinnerClassName}
                  type='number'
                  inputMode='numeric'
                  min={item.min}
                  max={item.max}
                  step={1}
                  {...safeNumberFieldProps(field)}
                />
              )}
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
          <FormField
            control={form.control}
            name='log_export_setting.employee_export_enabled'
            render={({ field }) => (
              <FormItem className='flex flex-row items-center justify-between gap-4'>
                <div className='space-y-0.5'>
                  <FormLabel>{t('Enable employee customer exports')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Applies to every active employee. Employees export only their own customers, using the built-in reconciliation templates and enabled custom templates.'
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
            name='log_export_setting.employee_customer_manage_enabled'
            render={({ field }) => (
              <FormItem className='flex flex-row items-center justify-between gap-4'>
                <div className='space-y-0.5'>
                  <FormLabel>
                    {t('Allow employees to manage customer exports')}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      'When enabled, active employees can download, cancel and delete self-service exports from their current customers. When disabled, they can only view those exports. Requires employee customer exports to be enabled.'
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

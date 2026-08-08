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
import { useEffect, useMemo, useRef } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
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
import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { useSettingsSaveConfirmation } from '../components/settings-save-confirmation'
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
  business_stats_fallback_backfill_setting: z.object({
    enabled: z.boolean(),
    use_separate_fallback_dir: z.boolean(),
    status_cache_seconds: z.number().int().min(1),
    max_read_line_bytes: z.number().int().min(1),
    write_batch_size: z.number().int().min(1),
    flush_interval_sec: z.number().int().min(0),
    batch_sleep_ms: z.number().int().min(0),
  }),
})

type FormValues = z.infer<typeof schema>

type FlatDefaults = {
  'business_stats_fallback_backfill_setting.enabled': boolean
  'business_stats_fallback_backfill_setting.use_separate_fallback_dir': boolean
  'business_stats_fallback_backfill_setting.status_cache_seconds': number
  'business_stats_fallback_backfill_setting.max_read_line_bytes': number
  'business_stats_fallback_backfill_setting.write_batch_size': number
  'business_stats_fallback_backfill_setting.flush_interval_sec': number
  'business_stats_fallback_backfill_setting.batch_sleep_ms': number
}

type FallbackBackfillSectionProps = {
  defaultValues: FlatDefaults
}

function buildFormDefaults(defaults: FlatDefaults): FormValues {
  return {
    business_stats_fallback_backfill_setting: {
      enabled: defaults['business_stats_fallback_backfill_setting.enabled'],
      use_separate_fallback_dir: defaults['business_stats_fallback_backfill_setting.use_separate_fallback_dir'],
      status_cache_seconds:
        defaults['business_stats_fallback_backfill_setting.status_cache_seconds'],
      max_read_line_bytes:
        defaults['business_stats_fallback_backfill_setting.max_read_line_bytes'],
      write_batch_size:
        defaults['business_stats_fallback_backfill_setting.write_batch_size'],
      flush_interval_sec:
        defaults['business_stats_fallback_backfill_setting.flush_interval_sec'],
      batch_sleep_ms:
        defaults['business_stats_fallback_backfill_setting.batch_sleep_ms'],
    },
  }
}

function normalizeFormValues(values: FormValues): FlatDefaults {
  const s = values.business_stats_fallback_backfill_setting
  return {
    'business_stats_fallback_backfill_setting.enabled': s.enabled,
    'business_stats_fallback_backfill_setting.use_separate_fallback_dir': s.use_separate_fallback_dir,
    'business_stats_fallback_backfill_setting.status_cache_seconds':
      s.status_cache_seconds,
    'business_stats_fallback_backfill_setting.max_read_line_bytes':
      s.max_read_line_bytes,
    'business_stats_fallback_backfill_setting.write_batch_size':
      s.write_batch_size,
    'business_stats_fallback_backfill_setting.flush_interval_sec':
      s.flush_interval_sec,
    'business_stats_fallback_backfill_setting.batch_sleep_ms': s.batch_sleep_ms,
  }
}

const numericFields: Array<{
  name: keyof Omit<
    FormValues['business_stats_fallback_backfill_setting'],
    'enabled'
  >
  label: string
  description: string
  min: number
  step: number
}> = [
  {
    name: 'status_cache_seconds',
    label: 'Status Cache TTL',
    description:
      'Seconds the file status is cached in memory, sparing the filesystem from frequent polls.',
    min: 1,
    step: 1,
  },
  {
    name: 'max_read_line_bytes',
    label: 'Max Read Line Bytes',
    description:
      'Max byte length of a JSON log line. Longer lines abort the task and keep the file for retry.',
    min: 1,
    step: 1024,
  },
  {
    name: 'write_batch_size',
    label: 'Write Batch Size',
    description:
      'Records buffered before each batch flush; also the in-memory buffer cap.',
    min: 1,
    step: 1,
  },
  {
    name: 'flush_interval_sec',
    label: 'Flush Interval',
    description:
      'Min seconds between flushes. Set 0 for size-only mode (flush only when full).',
    min: 0,
    step: 1,
  },
  {
    name: 'batch_sleep_ms',
    label: 'Batch Sleep',
    description:
      'Milliseconds the task sleeps after each flush to ease DB write pressure.',
    min: 0,
    step: 50,
  },
]

export function FallbackBackfillSection({
  defaultValues,
}: FallbackBackfillSectionProps) {
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

  const baselineRef = useRef<FlatDefaults>(defaultValues)
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
      Object.keys(normalized) as Array<keyof FlatDefaults>
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

  return (
    <SettingsSection title={t('Fallback Backfill')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
            saveLabel='Save fallback backfill settings'
          />

          <FormField
            control={form.control}
            name='business_stats_fallback_backfill_setting.enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable Fallback Backfill')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Master switch. When off, the banner is hidden, status reports no file, and triggering is rejected.'
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

          <FormField
            control={form.control}
            name='business_stats_fallback_backfill_setting.use_separate_fallback_dir'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Use Separate Fallback Directory')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Write fallback files to a dedicated "fallback/" subdirectory inside the app log dir, keeping them separate from regular logs. Off (default): fallback files share the app log directory.'
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

          {numericFields.map((item) => (
            <FormField
              key={item.name}
              control={form.control}
              name={`business_stats_fallback_backfill_setting.${item.name}`}
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t(item.label)}</FormLabel>
                  <FormControl>
                    <Input
                      className={numberInputNoSpinnerClassName}
                      type='number'
                      inputMode='numeric'
                      min={item.min}
                      step={item.step}
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

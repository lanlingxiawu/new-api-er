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
import { Switch } from '@/components/ui/switch'
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
  log_query_setting: z.object({
    stat_cache_enabled: z.boolean(),
    stat_cache_ttl_hours: z.number().int().min(1),
    current_hour_ttl_sec: z.number().int().min(0),
    warm_enabled: z.boolean(),
    warm_hours: z.number().int().min(0),
    query_timeout_ms: z.number().int().min(1000),
  }),
})

type FormValues = z.infer<typeof schema>

type FlatDefaults = {
  'log_query_setting.stat_cache_enabled': boolean
  'log_query_setting.stat_cache_ttl_hours': number
  'log_query_setting.current_hour_ttl_sec': number
  'log_query_setting.warm_enabled': boolean
  'log_query_setting.warm_hours': number
  'log_query_setting.query_timeout_ms': number
}

type LogQuerySectionProps = {
  defaultValues: FlatDefaults
}

function buildFormDefaults(defaults: FlatDefaults): FormValues {
  return {
    log_query_setting: {
      stat_cache_enabled: defaults['log_query_setting.stat_cache_enabled'],
      stat_cache_ttl_hours: defaults['log_query_setting.stat_cache_ttl_hours'],
      current_hour_ttl_sec: defaults['log_query_setting.current_hour_ttl_sec'],
      warm_enabled: defaults['log_query_setting.warm_enabled'],
      warm_hours: defaults['log_query_setting.warm_hours'],
      query_timeout_ms: defaults['log_query_setting.query_timeout_ms'],
    },
  }
}

function normalizeFormValues(values: FormValues): FlatDefaults {
  return {
    'log_query_setting.stat_cache_enabled':
      values.log_query_setting.stat_cache_enabled,
    'log_query_setting.stat_cache_ttl_hours':
      values.log_query_setting.stat_cache_ttl_hours,
    'log_query_setting.current_hour_ttl_sec':
      values.log_query_setting.current_hour_ttl_sec,
    'log_query_setting.warm_enabled': values.log_query_setting.warm_enabled,
    'log_query_setting.warm_hours': values.log_query_setting.warm_hours,
    'log_query_setting.query_timeout_ms':
      values.log_query_setting.query_timeout_ms,
  }
}

export function LogQuerySection({ defaultValues }: LogQuerySectionProps) {
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

    for (const key of changedKeys) {
      await updateOption.mutateAsync({ key, value: normalized[key] })
    }

    baselineRef.current = normalized
    baselineSerializedRef.current = JSON.stringify(normalized)
    form.reset(buildFormDefaults(normalized))
  }

  return (
    <SettingsSection title={t('Usage Log Query Acceleration')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
            saveLabel='Save log query settings'
          />

          <FormField
            control={form.control}
            name='log_query_setting.stat_cache_enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Accelerate usage log totals')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Reuse hourly results so the log list and usage badge load fast. Totals stay exact. Turn off to query the log table directly every time.'
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
            name='log_query_setting.current_hour_ttl_sec'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Current Hour Refresh Interval')}</FormLabel>
                <FormControl>
                  <Input
                    className={numberInputNoSpinnerClassName}
                    type='number'
                    inputMode='numeric'
                    min={0}
                    step={5}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Seconds before the current hour is recounted. Lower means fresher numbers and more database work. Set 0 to always count in real time.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='log_query_setting.stat_cache_ttl_hours'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Hourly Result Retention')}</FormLabel>
                <FormControl>
                  <Input
                    className={numberInputNoSpinnerClassName}
                    type='number'
                    inputMode='numeric'
                    min={1}
                    step={24}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Hours to keep each hourly result. Keep this shorter than your log retention so cleaned-up logs stop being counted.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='log_query_setting.warm_enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Precompute hourly results')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Compute each finished hour in the background (about one query per hour) so the first visit is already fast.'
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
            name='log_query_setting.warm_hours'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Hours To Precompute On Startup')}</FormLabel>
                <FormControl>
                  <Input
                    className={numberInputNoSpinnerClassName}
                    type='number'
                    inputMode='numeric'
                    min={0}
                    step={12}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'How far back to precompute when the service starts. Set 0 to only precompute hours that finish from now on.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='log_query_setting.query_timeout_ms'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Log Query Timeout')}</FormLabel>
                <FormControl>
                  <Input
                    className={numberInputNoSpinnerClassName}
                    type='number'
                    inputMode='numeric'
                    min={1000}
                    step={1000}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Milliseconds before a log count or usage query is abandoned, so one slow query cannot hold a database connection.'
                  )}
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

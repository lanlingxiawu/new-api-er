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
  payment_setting: z.object({
    user_export_max_rows: z.number().int().min(1),
  }),
  export_setting: z.object({
    user_export_enabled: z.boolean(),
    rate_limit_cooldown_sec: z.number().int().min(1),
    hard_ceiling_rows: z.number().int().min(1),
  }),
})

type FormValues = z.infer<typeof schema>

type FlatDefaults = {
  'payment_setting.user_export_max_rows': number
  'export_setting.user_export_enabled': boolean
  'export_setting.rate_limit_cooldown_sec': number
  'export_setting.hard_ceiling_rows': number
}

type ExportSettingsSectionProps = {
  defaultValues: FlatDefaults
}

function buildFormDefaults(defaults: FlatDefaults): FormValues {
  return {
    payment_setting: {
      user_export_max_rows: defaults['payment_setting.user_export_max_rows'],
    },
    export_setting: {
      user_export_enabled: defaults['export_setting.user_export_enabled'],
      rate_limit_cooldown_sec: defaults['export_setting.rate_limit_cooldown_sec'],
      hard_ceiling_rows: defaults['export_setting.hard_ceiling_rows'],
    },
  }
}

function normalizeFormValues(values: FormValues): FlatDefaults {
  return {
    'payment_setting.user_export_max_rows':
      values.payment_setting.user_export_max_rows,
    'export_setting.user_export_enabled':
      values.export_setting.user_export_enabled,
    'export_setting.rate_limit_cooldown_sec':
      values.export_setting.rate_limit_cooldown_sec,
    'export_setting.hard_ceiling_rows': values.export_setting.hard_ceiling_rows,
  }
}

export function ExportSettingsSection({
  defaultValues,
}: ExportSettingsSectionProps) {
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
    <SettingsSection title={t('Billing Export Settings')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
            saveLabel='Save billing export settings'
          />

          <FormField
            control={form.control}
            name='export_setting.user_export_enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Allow user billing export')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Turn regular user CSV export on or off. Admin export stays available.'
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
            name='payment_setting.user_export_max_rows'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('User Export Row Limit')}</FormLabel>
                <FormControl>
                  <Input
                    className={numberInputNoSpinnerClassName}
                    type='number'
                    inputMode='numeric'
                    min={1}
                    step={1000}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Max rows per export for regular users. Admin exports bypass this but still hit the hard ceiling.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='export_setting.rate_limit_cooldown_sec'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Export Rate Limit Cooldown')}</FormLabel>
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
                    'Min seconds between exports per user. Requests within the cooldown return 429.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='export_setting.hard_ceiling_rows'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Export Hard Ceiling Rows')}</FormLabel>
                <FormControl>
                  <Input
                    className={numberInputNoSpinnerClassName}
                    type='number'
                    inputMode='numeric'
                    min={1}
                    step={10000}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Absolute row limit for all exports (incl. admin), guarding the database.'
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

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
import { useQuery } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { Info } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { getBusinessStatsCircuitBreakerStatus } from '../api'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
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
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { useUpdateOption } from '../hooks/use-update-option'
import { safeNumberFieldProps } from '../utils/numeric-field'

const schema = z
  .object({
    'business_stats_circuit_breaker_setting.enabled': z.boolean(),
    'business_stats_circuit_breaker_setting.manual_disabled': z.boolean(),
    'business_stats_circuit_breaker_setting.failure_threshold': z
      .number()
      .int()
      .min(1)
      .max(1000),
    'business_stats_circuit_breaker_setting.initial_cooldown_seconds': z
      .number()
      .int()
      .min(1)
      .max(86400),
    'business_stats_circuit_breaker_setting.max_cooldown_seconds': z
      .number()
      .int()
      .min(1)
      .max(604800),
    'business_stats_circuit_breaker_setting.side_effect_db_timeout_ms': z
      .number()
      .int()
      .min(50)
      .max(30000),
  })
  .refine(
    (values) =>
      values['business_stats_circuit_breaker_setting.max_cooldown_seconds'] >=
      values['business_stats_circuit_breaker_setting.initial_cooldown_seconds'],
    {
      path: ['business_stats_circuit_breaker_setting.max_cooldown_seconds'],
      message: 'Max cooldown must be greater than or equal to initial cooldown',
    }
  )

type FormValues = z.infer<typeof schema>

type BusinessStatsCircuitBreakerSectionProps = {
  defaultValues: FormValues
}

const fields: Array<{
  name: keyof FormValues
  label: string
  description: string
  min: number
  max: number
  suffix: string
}> = [
  {
    name: 'business_stats_circuit_breaker_setting.failure_threshold',
    label: 'Failure threshold',
    description:
      'Open the circuit after this many consecutive post-settlement side-effect failures.',
    min: 1,
    max: 1000,
    suffix: 'times',
  },
  {
    name: 'business_stats_circuit_breaker_setting.initial_cooldown_seconds',
    label: 'Initial cooldown',
    description: 'How long the first automatic circuit-open window lasts.',
    min: 1,
    max: 86400,
    suffix: 'seconds',
  },
  {
    name: 'business_stats_circuit_breaker_setting.max_cooldown_seconds',
    label: 'Max cooldown',
    description:
      'Automatic cooldown doubles after repeated failures and stops at this value.',
    min: 1,
    max: 604800,
    suffix: 'seconds',
  },
  {
    name: 'business_stats_circuit_breaker_setting.side_effect_db_timeout_ms',
    label: 'DB timeout',
    description:
      'Short timeout for post-settlement lookup queries before recording a fallback failure.',
    min: 50,
    max: 30000,
    suffix: 'ms',
  },
]

function formatUnixTime(value: number) {
  if (!value) return ''
  return new Date(value * 1000).toLocaleString()
}

export function BusinessStatsCircuitBreakerSection({
  defaultValues,
}: BusinessStatsCircuitBreakerSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const statusQuery = useQuery({
    queryKey: ['business-stats-circuit-breaker-status'],
    queryFn: getBusinessStatsCircuitBreakerStatus,
    refetchInterval: 10000,
  })

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues,
  })

  useResetForm(form, defaultValues)

  const enabled = form.watch('business_stats_circuit_breaker_setting.enabled')
  const manualDisabled = form.watch(
    'business_stats_circuit_breaker_setting.manual_disabled'
  )
  const status = statusQuery.data?.data

  const onSubmit = async (values: FormValues) => {
    const updates: Array<{ key: string; value: boolean | number }> = []

    for (const key of Object.keys(values) as Array<keyof FormValues>) {
      if (values[key] !== defaultValues[key]) {
        updates.push({ key, value: values[key] })
      }
    }

    for (const update of updates) {
      await updateOption.mutateAsync(update)
    }
  }

  return (
    <SettingsSection title={t('Post-settlement Cost & Commission Guard')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
            saveLabel='Save guard settings'
          />

          <Alert>
            <Info data-icon='inline-start' />
            <AlertTitle className='text-sm'>
              {t('Protect settlement from side effects')}
            </AlertTitle>
            <AlertDescription className='text-muted-foreground text-xs leading-5'>
              <div>
                {t(
                  'These settings only affect post-settlement cost ledgers, commission logs, and business statistics. Main quota settlement remains isolated.'
                )}
              </div>
              {status && (
                <div className='mt-1 flex flex-wrap gap-x-3 gap-y-1'>
                  <span>
                    {status.hard_disabled
                      ? t('Runtime status: hard disabled')
                      : status.open
                        ? t('Runtime status: bypassing side logic')
                        : t('Runtime status: healthy')}
                  </span>
                  <span>
                    {t('Consecutive failures')}: {status.consecutive_failures}
                  </span>
                  {status.disabled_until > 0 && (
                    <span>
                      {t('Automatic circuit open until')}:{' '}
                      {formatUnixTime(status.disabled_until)}
                    </span>
                  )}
                  {status.last_reason && (
                    <span className='break-words'>
                      {t('Latest error')}: {status.last_reason}
                    </span>
                  )}
                </div>
              )}
            </AlertDescription>
          </Alert>

          <FormField
            control={form.control}
            name='business_stats_circuit_breaker_setting.enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable automatic circuit breaker')}</FormLabel>
                  <FormDescription>
                    {t(
                      'When enabled, repeated DB, Redis, memory, or file fallback failures temporarily skip post-settlement side logic.'
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
            name='business_stats_circuit_breaker_setting.manual_disabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>
                    {t('Manually disable post-settlement side logic')}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      'When enabled, settlement skips cost and commission side logic immediately and writes calculated cost and commission fallback records to the fallback log file instead.'
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

          {(manualDisabled || !enabled) && (
            <Alert variant='destructive'>
              <Info data-icon='inline-start' />
              <AlertTitle>{t('Side logic is currently bypassed')}</AlertTitle>
              <AlertDescription>
                {manualDisabled
                  ? t(
                      'Manual disable is on. New post-settlement cost and commission side records will be written to the fallback log file as calculated fallback records.'
                    )
                  : t(
                      'Automatic circuit breaker is disabled. The side logic is bypassed by configuration.'
                    )}
              </AlertDescription>
            </Alert>
          )}

          <Separator />

          {fields.map((item) => (
            <FormField
              key={item.name}
              control={form.control}
              name={item.name}
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t(item.label)}</FormLabel>
                  <FormControl>
                    <div className='flex min-w-0 items-center gap-2'>
                      <Input
                        type='number'
                        inputMode='numeric'
                        min={item.min}
                        max={item.max}
                        step={1}
                        {...safeNumberFieldProps(field)}
                      />
                      <span className='text-muted-foreground shrink-0 text-xs'>
                        {t(item.suffix)}
                      </span>
                    </div>
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

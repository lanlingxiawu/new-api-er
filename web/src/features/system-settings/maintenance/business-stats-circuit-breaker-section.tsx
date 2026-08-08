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
import { useQuery } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { Info } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
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
import { useSettingsSaveConfirmation } from '../components/settings-save-confirmation'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import {
  numberInputNoSpinnerClassName,
  safeNumberFieldProps,
} from '../utils/numeric-field'

/**
 * IMPORTANT: react-hook-form 7 interprets dotted `name` strings as nested
 * paths. If we declare the schema with literal flat keys like
 * `'business_stats_circuit_breaker_setting.enabled'`, the form state diverges
 * from what zod validates and saves silently turn into no-ops. So we model the
 * form internally with a nested object and only flatten back to the server-side
 * key format right before persisting.
 */
const schema = z
  .object({
    business_stats_circuit_breaker_setting: z.object({
      enabled: z.boolean(),
      manual_disabled: z.boolean(),
      failure_threshold: z.number().int().min(1).max(1000),
      initial_cooldown_seconds: z.number().int().min(1).max(86400),
      max_cooldown_seconds: z.number().int().min(1).max(604800),
      side_effect_db_timeout_ms: z.number().int().min(50).max(30000),
    }),
  })
  .refine(
    (values) =>
      values.business_stats_circuit_breaker_setting.max_cooldown_seconds >=
      values.business_stats_circuit_breaker_setting.initial_cooldown_seconds,
    {
      path: [
        'business_stats_circuit_breaker_setting',
        'max_cooldown_seconds',
      ],
      message: 'Max cooldown must be greater than or equal to initial cooldown',
    }
  )

type FormValues = z.infer<typeof schema>

type FlatDefaults = {
  'business_stats_circuit_breaker_setting.enabled': boolean
  'business_stats_circuit_breaker_setting.manual_disabled': boolean
  'business_stats_circuit_breaker_setting.failure_threshold': number
  'business_stats_circuit_breaker_setting.initial_cooldown_seconds': number
  'business_stats_circuit_breaker_setting.max_cooldown_seconds': number
  'business_stats_circuit_breaker_setting.side_effect_db_timeout_ms': number
}

type BusinessStatsCircuitBreakerSectionProps = {
  defaultValues: FlatDefaults
}

function buildFormDefaults(defaults: FlatDefaults): FormValues {
  return {
    business_stats_circuit_breaker_setting: {
      enabled: defaults['business_stats_circuit_breaker_setting.enabled'],
      manual_disabled:
        defaults['business_stats_circuit_breaker_setting.manual_disabled'],
      failure_threshold:
        defaults['business_stats_circuit_breaker_setting.failure_threshold'],
      initial_cooldown_seconds:
        defaults[
          'business_stats_circuit_breaker_setting.initial_cooldown_seconds'
        ],
      max_cooldown_seconds:
        defaults[
          'business_stats_circuit_breaker_setting.max_cooldown_seconds'
        ],
      side_effect_db_timeout_ms:
        defaults[
          'business_stats_circuit_breaker_setting.side_effect_db_timeout_ms'
        ],
    },
  }
}

function normalizeFormValues(values: FormValues): FlatDefaults {
  const s = values.business_stats_circuit_breaker_setting
  return {
    'business_stats_circuit_breaker_setting.enabled': s.enabled,
    'business_stats_circuit_breaker_setting.manual_disabled': s.manual_disabled,
    'business_stats_circuit_breaker_setting.failure_threshold':
      s.failure_threshold,
    'business_stats_circuit_breaker_setting.initial_cooldown_seconds':
      s.initial_cooldown_seconds,
    'business_stats_circuit_breaker_setting.max_cooldown_seconds':
      s.max_cooldown_seconds,
    'business_stats_circuit_breaker_setting.side_effect_db_timeout_ms':
      s.side_effect_db_timeout_ms,
  }
}

const numericFields: Array<{
  name: keyof FormValues['business_stats_circuit_breaker_setting']
  label: string
  description: string
  min: number
  max: number
  suffix: string
}> = [
  {
    name: 'failure_threshold',
    label: 'Failure threshold',
    description:
      'Open the circuit after this many consecutive side-effect failures.',
    min: 1,
    max: 1000,
    suffix: 'times',
  },
  {
    name: 'initial_cooldown_seconds',
    label: 'Initial cooldown',
    description: 'Length of the first auto circuit-open window.',
    min: 1,
    max: 86400,
    suffix: 'seconds',
  },
  {
    name: 'max_cooldown_seconds',
    label: 'Max cooldown',
    description:
      'Cooldown doubles on repeated failures, capped here.',
    min: 1,
    max: 604800,
    suffix: 'seconds',
  },
  {
    name: 'side_effect_db_timeout_ms',
    label: 'DB timeout',
    description:
      'Lookup timeout after settlement. Timeout counts as failure.',
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
  const requestSaveConfirmation = useSettingsSaveConfirmation()
  const statusQuery = useQuery({
    queryKey: ['business-stats-circuit-breaker-status'],
    queryFn: getBusinessStatsCircuitBreakerStatus,
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

  const baselineRef = useRef<FlatDefaults>(defaultValues)
  const baselineSerializedRef = useRef<string>(JSON.stringify(defaultValues))

  useEffect(() => {
    const serialized = JSON.stringify(defaultValues)
    if (serialized === baselineSerializedRef.current) return
    baselineRef.current = defaultValues
    baselineSerializedRef.current = serialized
    form.reset(buildFormDefaults(defaultValues))
  }, [defaultValues, form])

  const status = statusQuery.data?.data

  const enabled = form.watch(
    'business_stats_circuit_breaker_setting.enabled'
  )
  const manualDisabled = form.watch(
    'business_stats_circuit_breaker_setting.manual_disabled'
  )

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
                      'Off: side logic always runs without circuit protection (no auto-skip on failure). On: consecutive side-effect DB query failures trip the circuit temporarily; auto-recovers after cooldown.'
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
                      'Skip cost/commission side logic immediately; calculated fallback records go to the log file.'
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

          {numericFields.map((item) => (
            <FormField
              key={item.name}
              control={form.control}
              name={`business_stats_circuit_breaker_setting.${item.name}`}
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t(item.label)}</FormLabel>
                  <FormControl>
                    <div className='flex min-w-0 items-center gap-2'>
                      <Input
                        className={numberInputNoSpinnerClassName}
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

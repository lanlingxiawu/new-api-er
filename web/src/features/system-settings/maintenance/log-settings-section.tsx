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
import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useId, useMemo, useRef } from 'react'
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
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  SettingsForm,
  SettingsFormGrid,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import { safeNumberFieldProps } from '../utils/numeric-field'
import { DataCleanupControl } from './data-cleanup'
import { HistoryLogCleanup } from './history-log-cleanup'
import { ServerLogFiles } from './server-log-files'

const logSettingsSchema = z.object({
  errorLogBatchSize: z.number().int().min(1).max(10000),
  errorLogBatchTimeoutSec: z.number().int().min(1).max(10),
  errorLogAutoContinue: z.boolean(),
  LogConsumeEnabled: z.boolean(),
  auditRetentionDays: z
    .number()
    .int()
    .min(0)
    .max(3650)
    .refine((value) => value === 0 || value >= 7),
  log_setting: z.object({
    user_export_enabled: z.boolean(),
    max_range_days: z.number().int().min(1),
  }),
})

type LogSettingsFormValues = z.infer<typeof logSettingsSchema>

type LogSettingsSectionProps = {
  defaultValues: {
    auditRetentionDays: number
    LogConsumeEnabled: boolean
    'log_setting.user_export_enabled': boolean
    'log_setting.max_range_days': number
    'data_cleanup_setting.error_log_batch_size'?: number
    'data_cleanup_setting.error_log_batch_timeout_sec'?: number
    'data_cleanup_setting.error_log_auto_continue'?: boolean
  }
}

export function LogSettingsSection(props: LogSettingsSectionProps) {
  const { t } = useTranslation()
  const isRoot = useAuthStore(
    (state) => state.auth.user?.role === ROLE.SUPER_ADMIN
  )
  const queryClient = useQueryClient()
  const updateOption = useUpdateOption(undefined, false)
  const recordingTitleId = useId()
  const searchTitleId = useId()
  const formDefaults = useMemo<LogSettingsFormValues>(
    () => ({
      errorLogBatchSize:
        props.defaultValues['data_cleanup_setting.error_log_batch_size'] ??
        5000,
      errorLogBatchTimeoutSec:
        props.defaultValues[
          'data_cleanup_setting.error_log_batch_timeout_sec'
        ] ?? 5,
      errorLogAutoContinue:
        props.defaultValues['data_cleanup_setting.error_log_auto_continue'] ??
        false,
      LogConsumeEnabled: props.defaultValues.LogConsumeEnabled,
      auditRetentionDays: props.defaultValues.auditRetentionDays,
      log_setting: {
        user_export_enabled:
          props.defaultValues['log_setting.user_export_enabled'],
        max_range_days: props.defaultValues['log_setting.max_range_days'],
      },
    }),
    [props.defaultValues]
  )
  const form = useForm<LogSettingsFormValues>({
    resolver: zodResolver(logSettingsSchema),
    defaultValues: formDefaults,
  })

  const baselineRef = useRef(formDefaults)
  const { dirtyFields, isSubmitting } = form.formState
  useEffect(() => {
    baselineRef.current = formDefaults
    form.reset(formDefaults, { keepDirtyValues: true })
  }, [formDefaults, form])

  const onSubmit = async (values: LogSettingsFormValues) => {
    const changes = [
      [
        'errorLogBatchSize',
        'data_cleanup_setting.error_log_batch_size',
        values.errorLogBatchSize,
        baselineRef.current.errorLogBatchSize,
      ],
      [
        'errorLogBatchTimeoutSec',
        'data_cleanup_setting.error_log_batch_timeout_sec',
        values.errorLogBatchTimeoutSec,
        baselineRef.current.errorLogBatchTimeoutSec,
      ],
      [
        'errorLogAutoContinue',
        'data_cleanup_setting.error_log_auto_continue',
        values.errorLogAutoContinue,
        baselineRef.current.errorLogAutoContinue,
      ],
      [
        'auditRetentionDays',
        'audit_log_setting.retention_days',
        values.auditRetentionDays,
        baselineRef.current.auditRetentionDays,
      ],
      [
        'LogConsumeEnabled',
        'LogConsumeEnabled',
        values.LogConsumeEnabled,
        baselineRef.current.LogConsumeEnabled,
      ],
      [
        'log_setting.user_export_enabled',
        'log_setting.user_export_enabled',
        values.log_setting.user_export_enabled,
        baselineRef.current.log_setting.user_export_enabled,
      ],
      [
        'log_setting.max_range_days',
        'log_setting.max_range_days',
        values.log_setting.max_range_days,
        baselineRef.current.log_setting.max_range_days,
      ],
    ] as const
    const pending = changes.filter(
      ([, key, value, previous]) =>
        value !== previous &&
        (isRoot || !key.startsWith('data_cleanup_setting.'))
    )
    if (pending.length === 0) {
      toast.info(t('No changes to save'))
      return
    }
    try {
      for (const [field, key, value] of pending) {
        await updateOption.mutateAsync({ key, value })
        form.resetField(field, { defaultValue: value })
        if (
          field !== 'log_setting.user_export_enabled' &&
          field !== 'log_setting.max_range_days'
        ) {
          baselineRef.current = { ...baselineRef.current, [field]: value }
        } else {
          const name =
            field === 'log_setting.user_export_enabled'
              ? 'user_export_enabled'
              : 'max_range_days'
          baselineRef.current = {
            ...baselineRef.current,
            log_setting: { ...baselineRef.current.log_setting, [name]: value },
          }
        }
      }
      toast.success(t('Setting updated successfully'))
    } catch (error) {
      handleServerError(error)
    }
  }

  return (
    <SettingsSection title={t('Log Maintenance')} className='gap-6'>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={isSubmitting}
          />
          <fieldset
            disabled={isSubmitting}
            className='flex min-w-0 flex-col gap-4'
            data-dirty={Object.keys(dirtyFields).length > 0}
          >
            <div
              role='group'
              aria-labelledby={recordingTitleId}
              className='flex min-w-0 flex-col gap-4'
            >
              <h4 id={recordingTitleId} className='font-medium'>
                {t('Log recording')}
              </h4>
              <FormField
                control={form.control}
                name='LogConsumeEnabled'
                render={({ field }) => (
                  <SettingsSwitchItem>
                    <SettingsSwitchContent>
                      <FormLabel>{t('Record quota usage')}</FormLabel>
                      <FormDescription>
                        {t(
                          'Track per-request consumption to power usage analytics. Keeping this on increases database writes.'
                        )}
                      </FormDescription>
                    </SettingsSwitchContent>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                    <FormMessage />
                  </SettingsSwitchItem>
                )}
              />
            </div>

            <FormField
              control={form.control}
              name='auditRetentionDays'
              render={({ field }) => (
                <FormItem className='max-w-xs'>
                  <FormLabel>{t('Audit log retention (days)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={0}
                      max={3650}
                      {...safeNumberFieldProps(field)}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Audit records older than this are removed when the log cleanup task runs. Enter 0 to keep them forever.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <Separator />

            <div
              role='group'
              aria-labelledby={searchTitleId}
              className='flex min-w-0 flex-col gap-4'
            >
              <h4 id={searchTitleId} className='font-medium'>
                {t('Log search and export')}
              </h4>
              <SettingsFormGrid>
                <FormField
                  control={form.control}
                  name='log_setting.user_export_enabled'
                  render={({ field }) => (
                    <SettingsSwitchItem>
                      <SettingsSwitchContent>
                        <FormLabel>{t('Allow users to export logs')}</FormLabel>
                        <FormDescription>
                          {t(
                            'Users can download their own logs from the usage log page. Administrators can always export.'
                          )}
                        </FormDescription>
                      </SettingsSwitchContent>
                      <FormControl>
                        <Switch
                          checked={field.value}
                          onCheckedChange={field.onChange}
                        />
                      </FormControl>
                      <FormMessage />
                    </SettingsSwitchItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name='log_setting.max_range_days'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>
                        {t('Longest log time range (days)')}
                      </FormLabel>
                      <FormControl>
                        <Input
                          type='number'
                          min={1}
                          step={1}
                          {...safeNumberFieldProps(field)}
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'The longest time range a user can view at once in the logs and on the dashboard, and the longest range any export can cover.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </SettingsFormGrid>
            </div>
            {isRoot && (
              <>
                <Separator />
                <SettingsFormGrid>
                  <FormField
                    control={form.control}
                    name='errorLogBatchSize'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Error log batch limit')}</FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={1}
                            max={10000}
                            step={1}
                            {...safeNumberFieldProps(field)}
                          />
                        </FormControl>
                        <FormDescription>
                          {t(
                            'Starts with up to 2000 records and adjusts to this limit. Slower batches are reduced automatically.'
                          )}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  <FormField
                    control={form.control}
                    name='errorLogBatchTimeoutSec'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>
                          {t('Error log batch timeout (seconds)')}
                        </FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={1}
                            max={10}
                            step={1}
                            {...safeNumberFieldProps(field)}
                          />
                        </FormControl>
                        <FormDescription>
                          {t(
                            'The time limit for scanning, deleting and committing one batch.'
                          )}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  <FormField
                    control={form.control}
                    name='errorLogAutoContinue'
                    render={({ field }) => (
                      <SettingsSwitchItem>
                        <SettingsSwitchContent>
                          <FormLabel>
                            {t('Automatically continue error log cleanup')}
                          </FormLabel>
                          <FormDescription>
                            {t(
                              'Continue the same cleanup range when its time slice ends. Stop on errors.'
                            )}
                          </FormDescription>
                        </SettingsSwitchContent>
                        <FormControl>
                          <Switch
                            checked={field.value}
                            onCheckedChange={field.onChange}
                          />
                        </FormControl>
                        <FormMessage />
                      </SettingsSwitchItem>
                    )}
                  />
                </SettingsFormGrid>
              </>
            )}
          </fieldset>
        </SettingsForm>
      </Form>

      <Separator />

      <HistoryLogCleanup />

      <Separator />

      <HistoryLogCleanup
        scope='audit'
        onComplete={() => {
          void queryClient.invalidateQueries({ queryKey: ['audit'] })
        }}
      />

      <Separator />
      <DataCleanupControl kind='error_logs' />

      <Separator />

      <ServerLogFiles />
    </SettingsSection>
  )
}

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
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import * as z from 'zod'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { handleServerError } from '@/lib/handle-server-error'

import {
  SettingsForm,
  SettingsControlGroup,
  SettingsControlChildren,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { useSavePolicy } from './use-save-policy'

const sensitiveSchema = z.object({
  StreamProtocolValidationEnabled: z.boolean().optional(),
  CheckSensitiveEnabled: z.boolean(),
  CheckSensitiveOnPromptEnabled: z.boolean(),
  SensitiveWords: z.string().optional(),
})

type SensitiveFormValues = z.infer<typeof sensitiveSchema>

type RequestChecksSectionProps = {
  defaultValues: SensitiveFormValues
  legacyValue?: boolean
}

export function RequestChecksSection({
  defaultValues,
  legacyValue,
}: RequestChecksSectionProps) {
  const { t } = useTranslation()
  const updateOption = useSavePolicy()
  const form = useForm<SensitiveFormValues>({
    resolver: zodResolver(sensitiveSchema),
    defaultValues,
  })

  useResetForm(form, defaultValues)

  const onSubmit = async (values: SensitiveFormValues) => {
    const updates = Object.entries(values).filter(
      ([key, value]) =>
        value !== defaultValues[key as keyof SensitiveFormValues]
    )

    try {
      if (updates.length > 0) {
        await updateOption.mutateAsync(
          Object.fromEntries(
            updates.map(([key, value]) => [
              key === 'StreamProtocolValidationEnabled'
                ? 'stream_protocol_validation_setting.enabled'
                : key,
              String(value ?? ''),
            ])
          )
        )
      }
    } catch (error) {
      handleServerError(error)
    }
  }

  return (
    <SettingsSection title={t('Request checks')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={form.formState.isSubmitting}
            saveLabel='Save'
          />
          <SettingsControlGroup>
            <FormField
              control={form.control}
              name='StreamProtocolValidationEnabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>
                      {t('Stream protocol strict validation')}
                    </FormLabel>
                    <FormDescription>
                      {t(
                        'When enabled, checks streaming responses for protocol errors and incomplete endings. When disabled, skips strict validation and keeps error logging. Priority: user, channel, then global.'
                      )}
                    </FormDescription>
                    {field.value === undefined &&
                      typeof legacyValue === 'boolean' && (
                        <FormDescription>
                          {t(
                            'Global defaults: Claude-specific validation {{status}}; other protocols enabled.',
                            {
                              status: legacyValue
                                ? t('Enabled')
                                : t('Disabled'),
                            }
                          )}
                          <Button
                            type='button'
                            variant='link'
                            size='sm'
                            disabled={form.formState.isSubmitting}
                            onClick={() => field.onChange(legacyValue)}
                          >
                            {legacyValue
                              ? t('Enable for all protocols')
                              : t('Disable for all protocols')}
                          </Button>
                        </FormDescription>
                      )}
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value ?? true}
                      onCheckedChange={field.onChange}
                      disabled={form.formState.isSubmitting}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />
          </SettingsControlGroup>
          <h3 className='text-sm font-medium'>{t('Request text filtering')}</h3>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Checks text extracted from supported requests against keywords, ignoring case. A match rejects the request before upstream processing and does not affect channel health. Images, audio and generated responses are not checked.'
            )}
          </p>
          <Alert>
            <AlertDescription>
              {form.watch('CheckSensitiveEnabled') &&
              form.watch('CheckSensitiveOnPromptEnabled') &&
              form.watch('SensitiveWords')?.trim()
                ? t(
                    'Request text filtering will apply after saving these settings.'
                  )
                : t(
                    'To filter request text, enable filtering and prompt inspection, and add at least one keyword.'
                  )}
            </AlertDescription>
          </Alert>
          <SettingsControlGroup>
            <FormField
              control={form.control}
              name='CheckSensitiveEnabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t('Enable filtering')}</FormLabel>
                    <FormDescription>
                      {t(
                        'Blocks messages when sensitive keywords are detected.'
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

            <SettingsControlChildren>
              <FormField
                control={form.control}
                name='CheckSensitiveOnPromptEnabled'
                render={({ field }) => (
                  <SettingsSwitchItem>
                    <SettingsSwitchContent>
                      <FormLabel>{t('Inspect user prompts')}</FormLabel>
                      <FormDescription>
                        {t(
                          'When enabled, prompts are scanned before reaching upstream models.'
                        )}
                      </FormDescription>
                    </SettingsSwitchContent>
                    <FormControl>
                      <Switch
                        disabled={!form.watch('CheckSensitiveEnabled')}
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                  </SettingsSwitchItem>
                )}
              />
            </SettingsControlChildren>
          </SettingsControlGroup>

          <FormField
            control={form.control}
            name='SensitiveWords'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Blocked keywords')}</FormLabel>
                <FormControl>
                  <Textarea
                    rows={12}
                    placeholder={t('Enter one keyword per line')}
                    {...field}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Each line represents one keyword. Leave blank to disable the list but keep the switch states.'
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

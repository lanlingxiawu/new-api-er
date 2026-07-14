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
import type { Resolver } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useTranslation } from 'react-i18next'
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
import { FormDirtyIndicator } from '../components/form-dirty-indicator'
import { FormNavigationGuard } from '../components/form-navigation-guard'
import {
  SettingsForm,
  SettingsFormGrid,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useSettingsForm } from '../hooks/use-settings-form'
import { useUpdateOption } from '../hooks/use-update-option'

const contactInfoSchema = z.object({
  ContactEmail: z.string().optional(),
  ContactPhone: z.string().optional(),
  ContactWechat: z.string().optional(),
  ContactQQ: z.string().optional(),
  ContactTelegram: z.string().optional(),
  ContactDiscord: z.string().optional(),
})

type ContactInfoFormValues = z.infer<typeof contactInfoSchema>

type ContactInfoSectionProps = {
  defaultValues: ContactInfoFormValues
}

function normalizeValue(value: unknown): string {
  if (value === undefined || value === null) return ''
  return typeof value === 'string' ? value : String(value)
}

export function ContactInfoSection({ defaultValues }: ContactInfoSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const normalizedDefaults: ContactInfoFormValues = {
    ContactEmail: normalizeValue(defaultValues.ContactEmail),
    ContactPhone: normalizeValue(defaultValues.ContactPhone),
    ContactWechat: normalizeValue(defaultValues.ContactWechat),
    ContactQQ: normalizeValue(defaultValues.ContactQQ),
    ContactTelegram: normalizeValue(defaultValues.ContactTelegram),
    ContactDiscord: normalizeValue(defaultValues.ContactDiscord),
  }

  const { form, handleSubmit, handleReset, isDirty, isSubmitting } =
    useSettingsForm<ContactInfoFormValues>({
      resolver: zodResolver(contactInfoSchema) as Resolver<
        ContactInfoFormValues,
        unknown,
        ContactInfoFormValues
      >,
      defaultValues: normalizedDefaults,
      onSubmit: async (_data, changedFields) => {
        for (const [key, value] of Object.entries(changedFields)) {
          await updateOption.mutateAsync({
            key,
            value: normalizeValue(value).trim(),
          })
        }
      },
    })

  return (
    <>
      <FormNavigationGuard when={isDirty} />

      <SettingsSection title={t('Contact Information')}>
        <Form {...form}>
          <SettingsForm onSubmit={handleSubmit}>
            <SettingsPageFormActions
              onSave={handleSubmit}
              onReset={handleReset}
              isSaving={isSubmitting || updateOption.isPending}
              isResetDisabled={!isDirty}
            />
            <FormDirtyIndicator isDirty={isDirty} />
            <p className='text-muted-foreground -mt-2 text-sm'>
              {t(
                'Configure contact methods shown in the home page footer. Leave a field empty to hide it.'
              )}
            </p>
            <SettingsFormGrid>
              <FormField
                control={form.control}
                name='ContactEmail'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Email')}</FormLabel>
                    <FormControl>
                      <Input placeholder='support@example.com' {...field} />
                    </FormControl>
                    <FormDescription>
                      {t('Shown as a mailto link in the footer')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='ContactPhone'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Phone')}</FormLabel>
                    <FormControl>
                      <Input placeholder='+86 400 000 0000' {...field} />
                    </FormControl>
                    <FormDescription>
                      {t('Shown as a tel link in the footer')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='ContactWechat'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('WeChat')}</FormLabel>
                    <FormControl>
                      <Input placeholder={t('WeChat ID')} {...field} />
                    </FormControl>
                    <FormDescription>
                      {t('Shown as text in the footer')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='ContactQQ'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('QQ')}</FormLabel>
                    <FormControl>
                      <Input placeholder={t('QQ or QQ group number')} {...field} />
                    </FormControl>
                    <FormDescription>
                      {t('Shown as text in the footer')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='ContactTelegram'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Telegram')}</FormLabel>
                    <FormControl>
                      <Input placeholder='https://t.me/yourchannel' {...field} />
                    </FormControl>
                    <FormDescription>
                      {t('Enter a full URL to render a link, otherwise shown as text')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='ContactDiscord'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Discord')}</FormLabel>
                    <FormControl>
                      <Input placeholder='https://discord.gg/yourserver' {...field} />
                    </FormControl>
                    <FormDescription>
                      {t('Enter a full URL to render a link, otherwise shown as text')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </SettingsFormGrid>
          </SettingsForm>
        </Form>
      </SettingsSection>
    </>
  )
}

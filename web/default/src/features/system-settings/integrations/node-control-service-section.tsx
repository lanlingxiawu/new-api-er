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
// xiugai 添加号池节点功能
import * as z from 'zod'
import { useForm } from 'react-hook-form'
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
import { SettingsForm } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { useUpdateOption } from '../hooks/use-update-option'
import { removeTrailingSlash } from './utils'

const createSchema = (t: (key: string) => string) =>
  z.object({
    NodeControlServiceUrl: z.string().refine((value) => {
      const trimmed = value.trim()
      if (!trimmed) return true
      return /^https?:\/\//.test(trimmed)
    }, t('Provide a valid URL starting with http:// or https://')),
  })

type FormValues = z.infer<ReturnType<typeof createSchema>>

type NodeControlServiceSectionProps = {
  defaultValues: FormValues
}

export function NodeControlServiceSection({
  defaultValues,
}: NodeControlServiceSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const schema = createSchema(t)

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues,
  })

  useResetForm(form, defaultValues)

  const onSubmit = async (values: FormValues) => {
    const newUrl = removeTrailingSlash(values.NodeControlServiceUrl)
    const oldUrl = removeTrailingSlash(defaultValues.NodeControlServiceUrl)
    if (newUrl !== oldUrl) {
      await updateOption.mutateAsync({
        key: 'NodeControlServiceUrl',
        value: newUrl,
      })
    }
  }

  return (
    <SettingsSection title={t('Node Control Service')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)} autoComplete='off'>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
            saveLabel='Save Node Control Service settings'
          />
          <FormField
            control={form.control}
            name='NodeControlServiceUrl'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Node Control Service URL')}</FormLabel>
                <FormControl>
                  <Input
                    type='url'
                    inputMode='url'
                    placeholder='https://127.0.0.1:8888'
                    autoComplete='off'
                    {...field}
                    onChange={(e) => field.onChange(e.target.value)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Base URL of the node control service. Used to fetch node pool status and account information.'
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
// end

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
import { useState } from 'react'
import * as z from 'zod'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
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
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { clearAllRequestLogs } from '../api'
import {
  SettingsControlGroup,
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { useUpdateOption } from '../hooks/use-update-option'

const requestLogSchema = z
  .object({
    RequestLogEnabled: z.boolean(),
    RequestLogUsername: z.string(),
    RequestLogMaxBodyKB: z.number().int().min(1).max(10240),
    RequestLogMinCount: z.number().int().min(0),
    RequestLogMaxCount: z.number().int().min(1),
  })
  .refine((values) => values.RequestLogMinCount < values.RequestLogMaxCount, {
    path: ['RequestLogMinCount'],
    message: 'Retained count must be less than the cleanup threshold',
  })

type RequestLogFormValues = z.infer<typeof requestLogSchema>

type RequestLogSettingsSectionProps = {
  defaultValues: RequestLogFormValues
}

export function RequestLogSettingsSection({
  defaultValues,
}: RequestLogSettingsSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const form = useForm<RequestLogFormValues>({
    resolver: zodResolver(requestLogSchema),
    defaultValues,
  })

  useResetForm(form, defaultValues)

  const [isClearing, setIsClearing] = useState(false)
  const [showClearDialog, setShowClearDialog] = useState(false)

  const handleClearRequestLogs = async () => {
    setIsClearing(true)
    try {
      const res = await clearAllRequestLogs()
      if (!res.success) {
        throw new Error(res.message || t('Failed to clear request logs'))
      }
      const count = res.data ?? 0
      toast.success(
        count > 0
          ? t('{{count}} request log entries cleared.', { count })
          : t('No request logs to clear.')
      )
      setShowClearDialog(false)
    } catch (error) {
      const message =
        error instanceof Error
          ? error.message
          : t('Failed to clear request logs')
      toast.error(message)
    } finally {
      setIsClearing(false)
    }
  }

  const onSubmit = async (values: RequestLogFormValues) => {
    const updates: Array<{ key: string; value: string | boolean | number }> = []

    if (values.RequestLogEnabled !== defaultValues.RequestLogEnabled) {
      updates.push({
        key: 'RequestLogEnabled',
        value: values.RequestLogEnabled,
      })
    }
    if (values.RequestLogUsername.trim() !== defaultValues.RequestLogUsername) {
      updates.push({
        key: 'RequestLogUsername',
        value: values.RequestLogUsername.trim(),
      })
    }
    if (values.RequestLogMaxBodyKB !== defaultValues.RequestLogMaxBodyKB) {
      updates.push({
        key: 'RequestLogMaxBodyKB',
        value: values.RequestLogMaxBodyKB,
      })
    }
    if (values.RequestLogMinCount !== defaultValues.RequestLogMinCount) {
      updates.push({
        key: 'RequestLogMinCount',
        value: values.RequestLogMinCount,
      })
    }
    if (values.RequestLogMaxCount !== defaultValues.RequestLogMaxCount) {
      updates.push({
        key: 'RequestLogMaxCount',
        value: values.RequestLogMaxCount,
      })
    }

    for (const update of updates) {
      await updateOption.mutateAsync(update)
    }
  }

  return (
    <SettingsSection title={t('Request Log')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)} autoComplete='off'>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
            saveLabel='Save request log settings'
          />
          <FormField
            control={form.control}
            name='RequestLogEnabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable Request Log')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Record downstream request headers/body and the response headers/body returned to the client for relay requests. Increases database writes significantly.'
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
            name='RequestLogUsername'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Request Log Username')}</FormLabel>
                <FormControl>
                  <Input
                    placeholder={t('Leave empty to record all users')}
                    autoComplete='off'
                    {...field}
                    onChange={(event) => field.onChange(event.target.value)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'If empty, requests and responses of all users are recorded. If filled, only the specified username is recorded.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='RequestLogMaxBodyKB'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Max Body Size (KB)')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    inputMode='numeric'
                    min={1}
                    value={field.value}
                    onBlur={field.onBlur}
                    name={field.name}
                    ref={field.ref}
                    onChange={(event) =>
                      field.onChange(Number(event.target.value))
                    }
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Each field (request/response body and headers) is truncated to this size. Content beyond the limit is dropped and marked as truncated.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='RequestLogMinCount'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Min Log Count')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    inputMode='numeric'
                    min={0}
                    value={field.value}
                    onBlur={field.onBlur}
                    name={field.name}
                    ref={field.ref}
                    onChange={(event) =>
                      field.onChange(Number(event.target.value))
                    }
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'When request logs exceed the max count, older logs are purged and only the newest min count is kept (logs are stored in Redis only).'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='RequestLogMaxCount'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Max Log Count')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    inputMode='numeric'
                    min={1}
                    value={field.value}
                    onBlur={field.onBlur}
                    name={field.name}
                    ref={field.ref}
                    onChange={(event) =>
                      field.onChange(Number(event.target.value))
                    }
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'When the request log count exceeds this value, a cleanup is triggered.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <SettingsControlGroup className='space-y-3'>
            <div>
              <h4 className='text-sm font-medium'>
                {t('Clear all request logs')}
              </h4>
              <p className='text-muted-foreground text-sm'>
                {t(
                  'Remove all request logs stored in Redis. This action cannot be undone.'
                )}
              </p>
            </div>
            <div>
              <Button
                type='button'
                variant='destructive'
                onClick={() => setShowClearDialog(true)}
                disabled={isClearing}
              >
                {isClearing ? t('Clearing...') : t('Clear request logs')}
              </Button>
            </div>
          </SettingsControlGroup>
        </SettingsForm>
      </Form>
      <AlertDialog open={showClearDialog} onOpenChange={setShowClearDialog}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t('Confirm clearing request logs')}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t('This will permanently remove all request logs stored in Redis.')}{' '}
              {t('This action cannot be undone.')}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={isClearing}>
              {t('Cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={(event) => {
                event.preventDefault()
                handleClearRequestLogs()
              }}
              disabled={isClearing}
            >
              {isClearing ? t('Clearing...') : t('Clear request logs')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </SettingsSection>
  )
}

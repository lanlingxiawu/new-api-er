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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import {
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
  Sheet,
  SheetContent,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import { useAuthStore } from '@/stores/auth-store'

import { getMonitorSettings, saveMonitorSettings } from './api'
import { AutoBanFields } from './auto-ban-fields'
import { IPProvidersField } from './ip-providers-field'
import {
  autoBanValidation,
  defaultAutoBanRules,
  excludedAccountIDs,
  parseAutoBanRules,
} from './lib/auto-ban'
import {
  parsePrivateProviderHosts,
  providerSettingsWithDraft,
  validProviderSettings,
} from './lib/ip-providers'
import { SettingsSection } from './settings-section'

const defaults: Record<string, string> = {
  enabled: 'false',
  capture_enabled: 'false',
  relay_ip_enabled: 'false',
  suspended: 'false',
  scan_interval_seconds: '10',
  failure_threshold: '5',
  batch_threshold: '5',
  key_view_threshold: '10',
  notification_enabled: 'false',
  notification_emails: '',
  webhook_url: '',
  notification_cooldown_seconds: '1800',
  max_notifications_per_hour: '20',
  alert_retention_days: '180',
  archive_retention_days: '365',
  ip_intelligence_enabled: 'false',
  ip_intelligence_providers_json: '[]',
  ip_intelligence_timeout_seconds: '5',
  ip_intelligence_max_lookups_per_cycle: '5',
  auto_ban_enabled: 'false',
  auto_ban_include_staff: 'false',
  auto_ban_exclude_root: 'true',
  auto_ban_excluded_user_ids_json: '[]',
  auto_ban_rules_json: JSON.stringify(defaultAutoBanRules),
}
const numberFields = [
  [
    'scan_interval_seconds',
    'Scan interval (seconds)',
    5,
    60,
    'Time between detection scans.',
    'monitor',
  ],
  [
    'failure_threshold',
    'Sign-in failure threshold',
    1,
    1000,
    'Failures for one account within 5 minutes; also checks successful sign-ins after failures within 15 minutes.',
    'monitor',
  ],
  [
    'batch_threshold',
    'Sensitive operation threshold',
    1,
    1000,
    'Resources affected by one operator within 5 minutes.',
    'monitor',
  ],
  [
    'key_view_threshold',
    'Channel key access threshold',
    1,
    1000,
    'Distinct channel keys viewed by one operator within 5 minutes.',
    'monitor',
  ],
  [
    'alert_retention_days',
    'Alert retention (days)',
    30,
    365,
    'Counted from closure. Original audit logs are kept.',
    'monitor',
  ],
  [
    'archive_retention_days',
    'Archive retention (days)',
    30,
    3650,
    'Counted from closure. Open alerts are kept.',
    'monitor',
  ],
  [
    'ip_intelligence_timeout_seconds',
    'IP lookup timeout (seconds)',
    1,
    8,
    'Skip on timeout. A failed lookup does not mean the IP is safe.',
    'ip',
  ],
  [
    'ip_intelligence_max_lookups_per_cycle',
    'IP lookups per scan',
    1,
    20,
    'Maximum IP lookups per scan. Cached results do not count.',
    'ip',
  ],
  [
    'notification_cooldown_seconds',
    'Notification cooldown (seconds)',
    60,
    86400,
    'Time between repeat notifications. Higher risk can notify sooner.',
    'notifications',
  ],
  [
    'max_notifications_per_hour',
    'Notification limit per hour',
    1,
    100,
    'Total across all destinations over the last hour.',
    'notifications',
  ],
] as const
const switchFields = [
  [
    'enabled',
    'Enable monitoring',
    'Detect unusual activity in audit logs.',
    'monitor',
  ],
  [
    'capture_enabled',
    'Capture sign-in security events',
    'Record sign-in failures and other security events.',
    'monitor',
  ],
  ['suspended', 'Suspend monitoring', '', 'monitor'],
  [
    'relay_ip_enabled',
    'AI request IP monitoring',
    'Check public IPs in newly saved AI request logs. Past logs and requests without a recorded IP are not checked.',
    'ip',
  ],
  [
    'ip_intelligence_enabled',
    'Enable IP lookup',
    'Configure and enable a lookup service first.',
    'ip',
  ],
  [
    'notification_enabled',
    'Enable notifications',
    'Send alerts to email or a webhook.',
    'notifications',
  ],
] as const
const tabs = [
  ['monitor', 'Monitoring'],
  ['auto', 'Automatic response'],
  ['ip', 'IP lookup'],
  ['notifications', 'Notifications'],
] as const

export function SettingsDrawer({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: ['audit-security', userId, 'settings'],
    queryFn: ({ signal }) => getMonitorSettings(signal),
    gcTime: 0,
  })
  const [tab, setTab] = useState<string>('monitor')
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})
  const schema = z
    .record(z.string(), z.string())
    .superRefine((values, context) => {
      for (const [key, label, min, max] of numberFields) {
        const value = Number(values[key])
        if (
          !values[key]?.trim() ||
          !Number.isInteger(value) ||
          value < min ||
          value > max
        ) {
          context.addIssue({
            code: 'custom',
            path: [key],
            message: t(
              '{{field}} must be an integer from {{min}} to {{max}}.',
              { field: t(label), min, max }
            ),
          })
        }
      }
      const autoError = autoBanValidation(values, t)
      if (autoError) {
        context.addIssue({
          code: 'custom',
          path: ['auto_ban_rules_json'],
          message: autoError,
        })
      }
      const ids = excludedAccountIDs(values._excludedIDs)
      if (!ids || ids.length > 100) {
        context.addIssue({
          code: 'custom',
          path: ['auto_ban_excluded_user_ids_json'],
          message: ids
            ? t('Exclude no more than 100 accounts.')
            : t('Account IDs must be positive integers separated by commas.'),
        })
      }
      if (!validProviderSettings(values.ip_intelligence_providers_json)) {
        context.addIssue({
          code: 'custom',
          path: ['ip_intelligence_providers_json'],
          message: t(
            'Migrate the old provider settings to a self-hosted IP lookup service first.'
          ),
        })
      } else {
        const providers = JSON.parse(
          values.ip_intelligence_providers_json || '[]'
        ) as { enabled: boolean; endpoint?: string }[]
        if (
          values.ip_intelligence_enabled === 'true' &&
          !providers.some((provider) => provider.enabled)
        ) {
          context.addIssue({
            code: 'custom',
            path: ['ip_intelligence_providers_json'],
            message: t('Configure and enable a lookup service first.'),
          })
        }
        for (const provider of providers) {
          const parsed = z
            .url({ protocol: /^https$/ })
            .safeParse(provider.endpoint)
          const url = parsed.success ? new URL(parsed.data) : undefined
          if (
            !url ||
            /^\s*https:\/\/[^/?#]*@/i.test(provider.endpoint ?? '') ||
            url.username ||
            url.password ||
            url.search ||
            url.hash
          ) {
            context.addIssue({
              code: 'custom',
              path: ['ip_intelligence_providers_json'],
              message: t(
                'Use an HTTPS URL without credentials, query parameters or a fragment.'
              ),
            })
          }
          if (
            !values._ipDailyLimit?.trim() ||
            !Number.isInteger(Number(values._ipDailyLimit)) ||
            Number(values._ipDailyLimit) < 0 ||
            Number(values._ipDailyLimit) > 1000000000
          ) {
            context.addIssue({
              code: 'custom',
              path: ['ip_intelligence_providers_json'],
              message: t(
                '{{field}} must be an integer from {{min}} to {{max}}.',
                { field: t('Daily lookup limit'), min: 0, max: 1000000000 }
              ),
            })
          }
          if (!parsePrivateProviderHosts(values._ipAllowedPrivateHosts)) {
            context.addIssue({
              code: 'custom',
              path: ['ip_intelligence_providers_json'],
              message: t(
                'Enter hostnames or IP addresses separated by commas, without ports or paths.'
              ),
            })
          }
        }
      }
      if (
        values.notification_emails &&
        (values.notification_emails.split(';').length > 10 ||
          values.notification_emails
            .split(';')
            .some((email) => !z.email().safeParse(email.trim()).success))
      ) {
        context.addIssue({
          code: 'custom',
          path: ['notification_emails'],
          message: t('Invalid email address'),
        })
      }
      if (values.webhook_url) {
        const url = z.url({ protocol: /^https$/ }).safeParse(values.webhook_url)
        if (
          !url.success ||
          new URL(url.data).username ||
          new URL(url.data).password ||
          new URL(url.data).hash
        ) {
          context.addIssue({
            code: 'custom',
            path: ['webhook_url'],
            message: t('Please enter a valid URL'),
          })
        }
      }
      if (
        values.notification_enabled === 'true' &&
        !values.notification_emails &&
        !values.webhook_url
      ) {
        context.addIssue({
          code: 'custom',
          path: ['notification_emails'],
          message: t('No notification destination'),
        })
      }
    })
  const form = useForm<Record<string, string>>({
    defaultValues: {
      ...defaults,
      _excludedIDs: '',
      _ipAllowedPrivateHosts: '',
      _ipDailyLimit: '0',
    },
    resolver: zodResolver(schema),
    shouldFocusError: false,
  })
  const { reset } = form
  useEffect(() => {
    if (!query.data) return
    const next = Object.fromEntries(
      Object.entries(defaults).map(([key, fallback]) => [
        key,
        query.data[key] ?? fallback,
      ])
    )
    let ids: unknown
    try {
      ids = JSON.parse(next.auto_ban_excluded_user_ids_json)
    } catch {
      ids = undefined
    }
    const provider = validProviderSettings(next.ip_intelligence_providers_json)
      ? JSON.parse(next.ip_intelligence_providers_json || '[]')[0]
      : undefined
    reset(
      {
        ...next,
        _excludedIDs: Array.isArray(ids)
          ? ids.join(', ')
          : next.auto_ban_excluded_user_ids_json,
        _ipAllowedPrivateHosts: Array.isArray(provider?.allowed_private_hosts)
          ? provider.allowed_private_hosts.join(', ')
          : '',
        _ipDailyLimit: String(provider?.daily_limit ?? 0),
      },
      { keepDirtyValues: true }
    )
  }, [query.data, reset])
  const values = form.watch()
  const update = (key: string, value: string) => {
    form.setValue(key, value, { shouldDirty: true })
    // These custom fields share cross-field rules. Clear resolved errors after
    // an attempted save, including the replay rule's capture dependency.
    if (form.formState.isSubmitted) void form.trigger()
  }
  const verification = useSecureVerification()
  const saveButtonRef = useRef<HTMLButtonElement>(null)
  const save = useMutation({
    mutationFn: async (snapshot: Record<string, string>) => {
      const proof = await verification.requestVerification({
        scope: 'audit.security.settings',
        context: { values: snapshot },
      })
      if (!proof) return false
      await saveMonitorSettings(snapshot, proof.proof_token)
      return true
    },
    onSuccess: async (saved) => {
      if (!saved) return
      toast.success(t('Saved successfully'))
      await queryClient.invalidateQueries({ queryKey: ['audit-security'] })
      onClose()
    },
    onError: () => toast.error(t('Save failed')),
  })
  useEffect(() => {
    if (save.isError || (save.isSuccess && !save.data)) {
      const frame = requestAnimationFrame(() =>
        saveButtonRef.current?.focus({ preventScroll: true })
      )
      return () => cancelAnimationFrame(frame)
    }
  }, [save.isError, save.isSuccess, save.data])
  const errors = Object.values(form.formState.errors).flatMap((error) =>
    error?.message ? [String(error.message)] : []
  )
  return (
    <Sheet
      open
      onOpenChange={(open) => {
        if (!open && !save.isPending) onClose()
      }}
    >
      <SheetContent
        side='right'
        className={sideDrawerContentClassName('sm:max-w-2xl')}
        showCloseButton={!save.isPending}
      >
        <SheetHeader
          className={sideDrawerHeaderClassName('shrink-0 pr-12 sm:pr-12')}
        >
          <SheetTitle>{t('Monitoring settings')}</SheetTitle>
        </SheetHeader>
        <Tabs
          value={tab}
          onValueChange={setTab}
          className='shrink-0 px-4 pt-4 sm:px-6'
        >
          <TabsList
            className='grid w-full grid-cols-2 group-data-horizontal/tabs:h-auto sm:grid-cols-4'
            aria-label={t('Monitoring settings categories')}
          >
            {tabs.map(([key, label]) => (
              <TabsTrigger
                key={key}
                value={key}
                className='min-w-0 whitespace-nowrap'
              >
                {t(label)}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
        <div className={sideDrawerFormClassName('[scrollbar-gutter:stable]')}>
          {query.isPending && <LoadingState />}
          {query.isError && (
            <ErrorState
              title={t('Failed to load settings')}
              onRetry={() => void query.refetch()}
            />
          )}
          {query.data && !query.isError && (
            <form
              id='security-monitor-settings-form'
              onSubmit={form.handleSubmit(
                (data) => {
                  if (save.isPending) return
                  const snapshot = Object.fromEntries(
                    Object.keys(defaults).map((key) => [key, data[key]])
                  )
                  snapshot.auto_ban_excluded_user_ids_json = JSON.stringify(
                    excludedAccountIDs(data._excludedIDs)
                  )
                  snapshot.auto_ban_rules_json = JSON.stringify(
                    parseAutoBanRules(data.auto_ban_rules_json)
                  )
                  snapshot.ip_intelligence_providers_json =
                    providerSettingsWithDraft(
                      data.ip_intelligence_providers_json,
                      data._ipAllowedPrivateHosts,
                      data._ipDailyLimit
                    )
                  save.mutate(snapshot)
                },
                (invalid) => {
                  const key = Object.keys(invalid)[0]
                  let nextTab = 'monitor'
                  if (key.startsWith('auto_ban')) nextTab = 'auto'
                  else if (key.startsWith('ip_')) nextTab = 'ip'
                  else if (
                    key.startsWith('notification') ||
                    key === 'max_notifications_per_hour' ||
                    key === 'webhook_url'
                  ) {
                    nextTab = 'notifications'
                  }
                  setTab(nextTab)
                  setExpanded((current) => ({ ...current, [nextTab]: true }))
                  requestAnimationFrame(() =>
                    document
                      .querySelector<HTMLElement>(`#monitor-${CSS.escape(key)}`)
                      ?.focus()
                  )
                }
              )}
            >
              <FieldGroup>
                {errors.length > 0 && (
                  <div
                    role='alert'
                    className='text-destructive space-y-1 text-sm'
                  >
                    {[...new Set(errors)].map((error) => (
                      <p key={error}>{error}</p>
                    ))}
                  </div>
                )}
                {tab === 'auto' ? (
                  <AutoBanFields
                    values={values}
                    disabled={save.isPending}
                    onChange={update}
                    excludedIDs={values._excludedIDs}
                    onExcludedIDsChange={(value) =>
                      update('_excludedIDs', value)
                    }
                    expandRules={!!expanded.auto}
                  />
                ) : (
                  <>
                    {switchFields
                      .filter(([, , , group]) => group === tab)
                      .map(([key, label, description]) => (
                        <Field
                          key={key}
                          orientation='horizontal'
                          data-disabled={save.isPending}
                        >
                          <FieldContent>
                            <FieldLabel htmlFor={`monitor-${key}`}>
                              {t(label)}
                            </FieldLabel>
                            {description && (
                              <FieldDescription
                                id={`monitor-${key}-description`}
                              >
                                {t(description)}
                              </FieldDescription>
                            )}
                          </FieldContent>
                          <Switch
                            id={`monitor-${key}`}
                            aria-describedby={
                              description
                                ? `monitor-${key}-description`
                                : undefined
                            }
                            checked={values[key] === 'true'}
                            disabled={save.isPending}
                            onCheckedChange={(checked) =>
                              update(key, String(checked))
                            }
                          />
                        </Field>
                      ))}
                    {tab === 'ip' && (
                      <IPProvidersField
                        value={values.ip_intelligence_providers_json}
                        privateHosts={values._ipAllowedPrivateHosts}
                        dailyLimit={values._ipDailyLimit}
                        onPrivateHostsChange={(value) =>
                          update('_ipAllowedPrivateHosts', value)
                        }
                        onDailyLimitChange={(value) =>
                          update('_ipDailyLimit', value)
                        }
                        disabled={save.isPending}
                        onChange={(value) =>
                          update('ip_intelligence_providers_json', value)
                        }
                      />
                    )}
                    {tab === 'notifications' && (
                      <>
                        <Field>
                          <FieldLabel htmlFor='monitor-notification_emails'>
                            {t('Recipient emails')}
                          </FieldLabel>
                          <Input
                            id='monitor-notification_emails'
                            placeholder={t('Separate emails with semicolons')}
                            value={values.notification_emails}
                            maxLength={2048}
                            disabled={save.isPending}
                            onChange={(event) =>
                              update('notification_emails', event.target.value)
                            }
                          />
                        </Field>
                        <Field>
                          <FieldLabel htmlFor='monitor-webhook_url'>
                            {t('Webhook URL')}
                          </FieldLabel>
                          <Input
                            id='monitor-webhook_url'
                            type='url'
                            value={values.webhook_url}
                            maxLength={2048}
                            disabled={save.isPending}
                            onChange={(event) =>
                              update('webhook_url', event.target.value)
                            }
                          />
                        </Field>
                      </>
                    )}
                    <SettingsSection
                      key={tab}
                      open={!!expanded[tab]}
                      onOpenChange={(open) =>
                        setExpanded((current) => ({ ...current, [tab]: open }))
                      }
                    >
                      <div className='grid gap-4 sm:grid-cols-2'>
                        {numberFields
                          .filter(([, , , , , group]) => group === tab)
                          .map(([key, label, min, max, description]) => (
                            <Field
                              key={key}
                              data-disabled={save.isPending}
                              data-invalid={!!form.formState.errors[key]}
                            >
                              <FieldLabel htmlFor={`monitor-${key}`}>
                                {t(label)}
                              </FieldLabel>
                              <Input
                                id={`monitor-${key}`}
                                aria-describedby={`monitor-${key}-description`}
                                aria-invalid={!!form.formState.errors[key]}
                                type='number'
                                min={min}
                                max={max}
                                step={1}
                                required
                                value={values[key]}
                                disabled={save.isPending}
                                onChange={(event) =>
                                  update(key, event.target.value)
                                }
                              />
                              <FieldDescription
                                id={`monitor-${key}-description`}
                              >
                                {t(description)}
                              </FieldDescription>
                            </Field>
                          ))}
                      </div>
                    </SettingsSection>
                  </>
                )}
              </FieldGroup>
            </form>
          )}
        </div>
        <SheetFooter className={sideDrawerFooterClassName('shrink-0')}>
          <Button
            type='button'
            variant='outline'
            disabled={save.isPending}
            onClick={onClose}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='submit'
            form='security-monitor-settings-form'
            ref={saveButtonRef}
            disabled={save.isPending || !query.data || query.isError}
          >
            {save.isPending ? t('Saving...') : t('Save')}
          </Button>
        </SheetFooter>
        <SecureVerificationDialog {...verification.dialogProps} />
      </SheetContent>
    </Sheet>
  )
}

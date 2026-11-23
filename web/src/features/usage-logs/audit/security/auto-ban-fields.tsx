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
import { ChevronDown } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'

import {
  autoBanThresholds,
  defaultAutoBanRules,
  hasAutomaticActions,
  hasAbuseLookup,
  parseAutoBanRules,
  type AutoBanRule,
} from './lib/auto-ban'
import { automaticBanMode } from './lib/security-display'
import { securityRules, singleActionRuleConditions } from './lib/security-rules'

export function AutoBanFields({
  values,
  disabled,
  onChange,
  excludedIDs,
  onExcludedIDsChange,
  expandRules,
}: {
  values: Record<string, string>
  disabled: boolean
  onChange: (key: string, value: string) => void
  excludedIDs: string
  onExcludedIDsChange: (value: string) => void
  expandRules: boolean
}) {
  const { t } = useTranslation()
  const rules = parseAutoBanRules(values.auto_ban_rules_json)
  const updateRule = (id: string, patch: Partial<AutoBanRule>) => {
    if (rules) {
      onChange(
        'auto_ban_rules_json',
        JSON.stringify(
          rules.map((rule) => (rule.id === id ? { ...rule, ...patch } : rule))
        )
      )
    }
  }
  const mode = automaticBanMode(
    values.enabled === 'true',
    values.suspended === 'true',
    values.auto_ban_enabled === 'true'
  )
  const needsCapture =
    values.auto_ban_enabled === 'true' &&
    rules?.some(
      (rule) => rule.id === 'session_reuse' && hasAutomaticActions(rule)
    )
  return (
    <div className='space-y-6'>
      <Field orientation='horizontal'>
        <FieldContent>
          <FieldLabel htmlFor='monitor-auto_ban_enabled'>
            {t('Automatic response')}
          </FieldLabel>
          <FieldDescription>
            {t('Run the selected actions when a rule matches.')}
          </FieldDescription>
        </FieldContent>
        <Switch
          id='monitor-auto_ban_enabled'
          checked={values.auto_ban_enabled === 'true'}
          disabled={disabled}
          onCheckedChange={(checked) =>
            onChange('auto_ban_enabled', String(checked))
          }
        />
      </Field>
      <Alert role='status'>
        <AlertDescription>{t(mode)}</AlertDescription>
      </Alert>
      <div className='text-muted-foreground space-y-2 text-sm'>
        <p>
          {t(
            'Clear all API keys present when cleanup starts. Applications will need new keys.'
          )}
        </p>
        <p>
          {t(
            'Clear access tokens, including legacy tokens, without signing out browser sessions.'
          )}
        </p>
        <p>
          {t(
            'Cleared credentials cannot be restored. Only new events trigger cleanup.'
          )}
        </p>
      </div>
      {needsCapture && (
        <Field orientation='horizontal'>
          <FieldContent>
            <FieldLabel htmlFor='auto-ban-capture-enabled'>
              {t('Capture sign-in security events')}
            </FieldLabel>
            <FieldDescription>
              {t(
                'Enable sign-in security event capture to use the session replay rule.'
              )}
            </FieldDescription>
          </FieldContent>
          <Switch
            id='auto-ban-capture-enabled'
            checked={values.capture_enabled === 'true'}
            disabled={disabled}
            onCheckedChange={(checked) =>
              onChange('capture_enabled', String(checked))
            }
          />
        </Field>
      )}
      <p className='text-muted-foreground text-sm'>
        {t(
          'IP rules require current abuse evidence. Proxy use, location changes, or failed lookups alone do not trigger actions.'
        )}
      </p>
      {values.auto_ban_enabled === 'true' &&
        rules?.some(
          (rule) => rule.id.startsWith('risky_ip_') && hasAutomaticActions(rule)
        ) &&
        !hasAbuseLookup(values) && (
          <Alert variant='destructive'>
            <AlertDescription>
              {t(
                'Enable IP lookup and configure an abuse data source before using high-risk IP rules.'
              )}
            </AlertDescription>
          </Alert>
        )}
      <div id='monitor-auto_ban_rules_json' tabIndex={-1} className='space-y-3'>
        {!rules && (
          <p role='alert' className='text-destructive text-sm'>
            {t('Invalid rule settings. Check them and try again.')}
          </p>
        )}
        {rules?.map((rule) => (
          <Collapsible
            key={`${rule.id}:${expandRules}`}
            defaultOpen={expandRules}
            className='rounded-lg border'
          >
            <div className='flex min-h-14 items-center gap-3 px-4 py-2'>
              <p
                id={`auto-response-${rule.id}`}
                className='min-w-0 flex-1 text-sm font-medium'
              >
                {t(securityRules[rule.id])}
              </p>
              <CollapsibleTrigger
                render={
                  <Button
                    type='button'
                    variant='ghost'
                    size='sm'
                    className='group shrink-0'
                  />
                }
              >
                {t('Rule conditions')}
                <ChevronDown
                  className='size-3.5 group-data-[panel-open]:rotate-180'
                  aria-hidden
                />
              </CollapsibleTrigger>
            </div>
            <fieldset
              aria-labelledby={`auto-response-${rule.id}`}
              className='space-y-2 border-t p-4'
            >
              <legend className='sr-only'>{t('Actions on match')}</legend>
              <p className='text-muted-foreground text-xs'>
                {t('Actions on match')}
              </p>
              <div className='flex flex-wrap gap-x-5 gap-y-3'>
                {(
                  [
                    ['selected', 'Ban account'],
                    ['clear_api_keys', 'Clear API keys'],
                    ['clear_access_tokens', 'Clear access tokens'],
                  ] as const
                ).map(([key, label]) => (
                  <Field key={key} orientation='horizontal' className='w-auto'>
                    <Checkbox
                      id={`auto-ban-${rule.id}-${key}`}
                      checked={rule[key]}
                      disabled={disabled}
                      onCheckedChange={(checked) =>
                        updateRule(rule.id, { [key]: checked === true })
                      }
                    />
                    <FieldLabel htmlFor={`auto-ban-${rule.id}-${key}`}>
                      {t(label)}
                    </FieldLabel>
                  </Field>
                ))}
              </div>
            </fieldset>
            <CollapsibleContent className='space-y-4 border-t p-4'>
              <div className='text-muted-foreground space-y-2 text-sm'>
                {rule.id === 'multi_account_key_access' && (
                  <>
                    <p>
                      {t(
                        'The same IP and client sign in to at least {{loginAccounts}} accounts within {{loginSeconds}} seconds, and at least {{keyAccounts}} of those accounts view keys after sign-in within {{windowSeconds}} seconds.',
                        {
                          loginAccounts: rule.login_accounts,
                          loginSeconds: rule.login_window_seconds,
                          keyAccounts: rule.key_accounts,
                          windowSeconds: rule.window_seconds,
                        }
                      )}
                    </p>
                    <p>
                      {t(
                        'Only accounts that signed in and viewed keys are processed.'
                      )}
                    </p>
                  </>
                )}
                {rule.id === 'multi_source_key_access' && (
                  <p>
                    {t(
                      'One account signs in from at least {{loginIPs}} IPs within {{windowSeconds}} seconds and views keys after sign-in from at least {{keyIPs}} of those IPs.',
                      {
                        loginIPs: rule.login_ips,
                        keyIPs: rule.key_ips,
                        windowSeconds: rule.window_seconds,
                      }
                    )}
                  </p>
                )}
                {rule.id === 'new_source_channel_rewrite' && (
                  <>
                    <p>
                      {t(
                        'One operator changes at least {{channels}} channel addresses from a new source within {{windowSeconds}} seconds.',
                        {
                          channels: rule.channels,
                          windowSeconds: rule.window_seconds,
                        }
                      )}
                    </p>
                    <p>
                      {t(
                        'Management history is at least 1 day old and active within 30 days; the new source first appeared within 24 hours.'
                      )}
                    </p>
                  </>
                )}
                {singleActionRuleConditions[rule.id] && (
                  <p>{t(singleActionRuleConditions[rule.id])}</p>
                )}
                {rule.id === 'session_reuse' && (
                  <p>
                    {t(
                      'An invalidated sign-in credential was used again and confirmed by the system.'
                    )}
                  </p>
                )}
              </div>
              <div className='grid gap-4 sm:grid-cols-2'>
                {Object.entries(autoBanThresholds)
                  .filter(([key]) =>
                    defaultAutoBanRules.some(
                      (item) => item.id === rule.id && key in item
                    )
                  )
                  .map(([key, [label, min, max]]) => (
                    <Field key={key}>
                      <FieldLabel htmlFor={`auto-ban-${rule.id}-${key}`}>
                        {t(label)}
                      </FieldLabel>
                      <Input
                        id={`auto-ban-${rule.id}-${key}`}
                        type='number'
                        min={min}
                        max={max}
                        step={1}
                        required
                        disabled={disabled}
                        value={
                          rule[key as keyof typeof autoBanThresholds] ?? ''
                        }
                        onChange={(event) =>
                          updateRule(rule.id, {
                            [key]:
                              event.target.value === ''
                                ? undefined
                                : Number(event.target.value),
                          })
                        }
                      />
                    </Field>
                  ))}
              </div>
            </CollapsibleContent>
          </Collapsible>
        ))}
      </div>
      <Field orientation='horizontal'>
        <FieldContent>
          <FieldLabel htmlFor='monitor-auto_ban_include_staff'>
            {t('Include administrators and employees')}
          </FieldLabel>
          <FieldDescription>
            {t('When off, administrators and employees receive alerts only.')}
          </FieldDescription>
        </FieldContent>
        <Switch
          id='monitor-auto_ban_include_staff'
          checked={values.auto_ban_include_staff === 'true'}
          disabled={disabled}
          onCheckedChange={(checked) =>
            onChange('auto_ban_include_staff', String(checked))
          }
        />
      </Field>
      <Field orientation='horizontal'>
        <FieldContent>
          <FieldLabel htmlFor='monitor-auto_ban_include_root'>
            {t('Include super administrators')}
          </FieldLabel>
          <FieldDescription>
            {t(
              'When off, super administrators receive alerts only. No automatic actions are taken.'
            )}
          </FieldDescription>
        </FieldContent>
        <Switch
          id='monitor-auto_ban_include_root'
          checked={values.auto_ban_exclude_root === 'false'}
          disabled={disabled}
          onCheckedChange={(checked) =>
            onChange('auto_ban_exclude_root', String(!checked))
          }
        />
      </Field>
      <Field>
        <FieldLabel htmlFor='monitor-auto_ban_excluded_user_ids_json'>
          {t('Excluded account IDs')}
        </FieldLabel>
        <Input
          id='monitor-auto_ban_excluded_user_ids_json'
          placeholder={t('Separate IDs with commas')}
          value={excludedIDs}
          maxLength={4096}
          disabled={disabled}
          onChange={(event) => onExcludedIDsChange(event.target.value)}
        />
        <FieldDescription>
          {t(
            'These accounts receive alerts only. No automatic actions are taken.'
          )}
        </FieldDescription>
      </Field>
    </div>
  )
}

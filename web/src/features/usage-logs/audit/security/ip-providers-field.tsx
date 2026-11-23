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
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'

import { validProviderSettings } from './lib/ip-providers'
import { SettingsSection } from './settings-section'

export interface IPProvider {
  id: string
  name: string
  type: string
  endpoint: string
  enabled: boolean
  priority: number
  api_key: string
  key_configured?: boolean
  clear_secret?: boolean
  daily_limit: number
  capabilities: string[]
  allowed_private_hosts: string[]
}

export function IPProvidersField(props: {
  value: string
  privateHosts: string
  dailyLimit: string
  onPrivateHostsChange: (value: string) => void
  onDailyLimitChange: (value: string) => void
  disabled: boolean
  onChange: (value: string) => void
}) {
  const { t } = useTranslation()
  const valid = validProviderSettings(props.value)
  const provider: IPProvider | undefined = valid
    ? (JSON.parse(props.value || '[]') as IPProvider[])[0]
    : undefined
  const update = (patch: Partial<IPProvider>) => {
    if (provider) props.onChange(JSON.stringify([{ ...provider, ...patch }]))
  }
  return (
    <FieldGroup className='gap-3'>
      <Field>
        <FieldLabel>{t('IP lookup service')}</FieldLabel>
        <FieldDescription>
          {t('Look up the location and risk of sign-in IPs.')}
        </FieldDescription>
      </Field>
      {!valid && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t(
              'Migrate the old provider settings to a self-hosted IP lookup service first.'
            )}
          </AlertDescription>
        </Alert>
      )}
      {provider && (
        <fieldset disabled={props.disabled} className='rounded-md border p-3'>
          <FieldGroup className='gap-3'>
            <Field orientation='horizontal'>
              <FieldLabel htmlFor='ip-service-enabled'>
                {t('Enabled')}
              </FieldLabel>
              <Switch
                id='ip-service-enabled'
                disabled={props.disabled}
                checked={provider.enabled}
                onCheckedChange={(enabled) => update({ enabled })}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor='ip-service-url'>
                {t('Service URL')}
              </FieldLabel>
              <Input
                id='ip-service-url'
                type='url'
                required
                maxLength={2048}
                disabled={props.disabled}
                value={provider.endpoint}
                onChange={(event) => update({ endpoint: event.target.value })}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor='ip-service-key'>
                {t('Access key')}
              </FieldLabel>
              <Input
                id='ip-service-key'
                type='password'
                autoComplete='new-password'
                disabled={props.disabled}
                value={provider.api_key || ''}
                placeholder={
                  provider.key_configured
                    ? t('Configured; leave blank to keep')
                    : t('Not configured')
                }
                onChange={(event) =>
                  update({ api_key: event.target.value, clear_secret: false })
                }
              />
              {provider.key_configured && (
                <Field orientation='horizontal'>
                  <Checkbox
                    id='ip-service-clear-key'
                    disabled={props.disabled}
                    checked={provider.clear_secret ?? false}
                    onCheckedChange={(checked) =>
                      update({ clear_secret: checked === true, api_key: '' })
                    }
                  />
                  <FieldLabel htmlFor='ip-service-clear-key'>
                    {t('Clear saved key')}
                  </FieldLabel>
                </Field>
              )}
            </Field>
            <Field>
              <FieldLabel htmlFor='ip-service-limit'>
                {t('Daily lookup limit')}
              </FieldLabel>
              <Input
                id='ip-service-limit'
                type='number'
                min={0}
                max={1000000000}
                required
                disabled={props.disabled}
                value={props.dailyLimit}
                onChange={(event) =>
                  props.onDailyLimitChange(event.target.value)
                }
              />
              <FieldDescription>{t('0 means unlimited')}</FieldDescription>
            </Field>
            <SettingsSection>
              <Field>
                <FieldLabel htmlFor='ip-service-hosts'>
                  {t('Allowed private network hosts')}
                </FieldLabel>
                <Input
                  id='ip-service-hosts'
                  placeholder={t('Separate hosts with commas')}
                  disabled={props.disabled}
                  value={props.privateHosts}
                  onChange={(event) =>
                    props.onPrivateHostsChange(event.target.value)
                  }
                />
              </Field>
            </SettingsSection>
          </FieldGroup>
        </fieldset>
      )}
      {!provider && valid && (
        <div className='flex flex-wrap justify-end gap-2'>
          <Button
            type='button'
            variant='outline'
            disabled={props.disabled}
            onClick={() =>
              props.onChange(
                JSON.stringify([
                  {
                    id: crypto.randomUUID(),
                    name: 'IP lookup',
                    type: 'custom_ipapi',
                    endpoint: '',
                    enabled: true,
                    priority: 100,
                    api_key: '',
                    daily_limit: 0,
                    capabilities: ['geo', 'vpn', 'proxy', 'tor', 'abuse'],
                    allowed_private_hosts: [],
                  },
                ])
              )
            }
          >
            {t('Configure service')}
          </Button>
        </div>
      )}
    </FieldGroup>
  )
}

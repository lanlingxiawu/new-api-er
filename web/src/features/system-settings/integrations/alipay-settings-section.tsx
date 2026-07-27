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
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { SettingsSwitchField } from '../components/settings-form-layout'

export interface AlipaySettingsValues {
  AlipayEnabled: boolean
  AlipayAppId: string
  AlipayPrivateKey: string
  AlipayPublicKey: string
  AlipaySandbox: boolean
  AlipayMinTopUp: number
  AlipayNotifyUrl: string
  AlipayReturnUrl: string
}

interface Props {
  values: AlipaySettingsValues
  onValueChange: <K extends keyof AlipaySettingsValues>(
    key: K,
    value: AlipaySettingsValues[K]
  ) => void
}

export function AlipaySettingsSection({ values, onValueChange }: Props) {
  const { t } = useTranslation()

  return (
    <div className='space-y-4 pt-4'>
      <div>
        <h3 className='text-lg font-medium'>{t('Alipay Gateway')}</h3>
        <p className='text-muted-foreground text-sm'>
          {t('Configuration for official Alipay payment integration')}
        </p>
      </div>
      <Alert>
        <AlertDescription className='text-xs'>
          {t(
            'Obtain the App ID and key pair from the Alipay Open Platform, and configure the notification and return URLs.'
          )}
        </AlertDescription>
      </Alert>

      <div className='grid gap-4 sm:grid-cols-2'>
        <SettingsSwitchField
          checked={values.AlipayEnabled}
          onCheckedChange={(v) => onValueChange('AlipayEnabled', v)}
          label={t('Enable Alipay')}
          className='border-b-0 py-0'
        />
        <SettingsSwitchField
          checked={values.AlipaySandbox}
          onCheckedChange={(v) => onValueChange('AlipaySandbox', v)}
          label={t('Sandbox mode')}
          className='border-b-0 py-0'
        />
      </div>

      <div className='grid gap-1.5'>
        <Label>{t('App ID')}</Label>
        <Input
          value={values.AlipayAppId}
          onChange={(event) =>
            onValueChange('AlipayAppId', event.target.value)
          }
        />
      </div>

      <div className='grid grid-cols-2 gap-4'>
        <div className='grid gap-1.5'>
          <Label>{t('App Private Key')}</Label>
          <Textarea
            rows={3}
            value={values.AlipayPrivateKey}
            onChange={(event) =>
              onValueChange('AlipayPrivateKey', event.target.value)
            }
            className='font-mono text-xs'
          />
        </div>
        <div className='grid gap-1.5'>
          <Label>{t('Alipay Public Key')}</Label>
          <Textarea
            rows={3}
            value={values.AlipayPublicKey}
            onChange={(event) =>
              onValueChange('AlipayPublicKey', event.target.value)
            }
            className='font-mono text-xs'
          />
        </div>
      </div>

      <div className='grid grid-cols-3 gap-4'>
        <div className='grid gap-1.5'>
          <Label>{t('Minimum top-up (USD)')}</Label>
          <Input
            type='number'
            min={0}
            value={values.AlipayMinTopUp}
            onChange={(event) =>
              onValueChange(
                'AlipayMinTopUp',
                event.target.value === '' ? 1 : event.target.valueAsNumber
              )
            }
          />
        </div>
        <div className='grid gap-1.5'>
          <Label>{t('Callback notification URL')}</Label>
          <Input
            placeholder='https://example.com/api/alipay/webhook'
            value={values.AlipayNotifyUrl}
            onChange={(event) =>
              onValueChange('AlipayNotifyUrl', event.target.value)
            }
          />
        </div>
        <div className='grid gap-1.5'>
          <Label>{t('Payment return URL')}</Label>
          <Input
            placeholder='https://example.com/console/topup'
            value={values.AlipayReturnUrl}
            onChange={(event) =>
              onValueChange('AlipayReturnUrl', event.target.value)
            }
          />
        </div>
      </div>
    </div>
  )
}

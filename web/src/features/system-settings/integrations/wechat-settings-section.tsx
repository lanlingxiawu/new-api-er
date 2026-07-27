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

export interface WechatSettingsValues {
  WechatEnabled: boolean
  WechatAppId: string
  WechatMchId: string
  WechatApiV3Key: string
  WechatMchPrivateKey: string
  WechatMchCertSerialNo: string
  WechatMinTopUp: number
  WechatNotifyUrl: string
}

interface Props {
  values: WechatSettingsValues
  onValueChange: <K extends keyof WechatSettingsValues>(
    key: K,
    value: WechatSettingsValues[K]
  ) => void
}

export function WechatSettingsSection({ values, onValueChange }: Props) {
  const { t } = useTranslation()

  return (
    <div className='space-y-4 pt-4'>
      <div>
        <h3 className='text-lg font-medium'>{t('WeChat Pay Gateway')}</h3>
        <p className='text-muted-foreground text-sm'>
          {t('Configuration for official WeChat Pay (Native) integration')}
        </p>
      </div>
      <Alert>
        <AlertDescription className='text-xs'>
          {t(
            'Obtain the AppID, merchant ID, API v3 key, and merchant API certificate from the WeChat Pay merchant platform, and configure the notification URL.'
          )}
        </AlertDescription>
      </Alert>

      <SettingsSwitchField
        checked={values.WechatEnabled}
        onCheckedChange={(v) => onValueChange('WechatEnabled', v)}
        label={t('Enable WeChat Pay')}
        className='border-b-0 py-0'
      />

      <div className='grid grid-cols-2 gap-4'>
        <div className='grid gap-1.5'>
          <Label>{t('App ID')}</Label>
          <Input
            value={values.WechatAppId}
            onChange={(event) =>
              onValueChange('WechatAppId', event.target.value)
            }
          />
        </div>
        <div className='grid gap-1.5'>
          <Label>{t('Merchant ID')}</Label>
          <Input
            value={values.WechatMchId}
            onChange={(event) =>
              onValueChange('WechatMchId', event.target.value)
            }
          />
        </div>
      </div>

      <div className='grid grid-cols-2 gap-4'>
        <div className='grid gap-1.5'>
          <Label>{t('API v3 Key')}</Label>
          <Input
            type='password'
            value={values.WechatApiV3Key}
            onChange={(event) =>
              onValueChange('WechatApiV3Key', event.target.value)
            }
          />
        </div>
        <div className='grid gap-1.5'>
          <Label>{t('Merchant API Certificate Serial Number')}</Label>
          <Input
            value={values.WechatMchCertSerialNo}
            onChange={(event) =>
              onValueChange('WechatMchCertSerialNo', event.target.value)
            }
          />
        </div>
      </div>

      <div className='grid gap-1.5'>
        <Label>{t('Merchant API Private Key')}</Label>
        <Textarea
          rows={3}
          value={values.WechatMchPrivateKey}
          onChange={(event) =>
            onValueChange('WechatMchPrivateKey', event.target.value)
          }
          className='font-mono text-xs'
        />
      </div>

      <div className='grid grid-cols-2 gap-4'>
        <div className='grid gap-1.5'>
          <Label>{t('Minimum top-up (USD)')}</Label>
          <Input
            type='number'
            min={0}
            value={values.WechatMinTopUp}
            onChange={(event) =>
              onValueChange(
                'WechatMinTopUp',
                event.target.value === '' ? 1 : event.target.valueAsNumber
              )
            }
          />
        </div>
        <div className='grid gap-1.5'>
          <Label>{t('Callback notification URL')}</Label>
          <Input
            placeholder='https://example.com/api/wechat/webhook'
            value={values.WechatNotifyUrl}
            onChange={(event) =>
              onValueChange('WechatNotifyUrl', event.target.value)
            }
          />
        </div>
      </div>
    </div>
  )
}

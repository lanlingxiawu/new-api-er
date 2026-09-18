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

export interface InfiniSettingsValues {
  InfiniEnabled: boolean
  InfiniApiKey: string
  InfiniApiSecret: string
  InfiniWebhookSecret: string
  InfiniSandbox: boolean
  InfiniNotifyUrl: string
  InfiniReturnUrl: string
  InfiniFailUrl: string
  InfiniUnitPrice: number
  InfiniMinTopUp: number
  InfiniCurrency: string
  /** JSON 数组，格式 [{currency,unit_price,min_topup}]，配置后优先于单币种字段 */
  InfiniCurrencies: string
  /** JSON 数组，限定结账页支付方式。例：[1,2] 表示仅显示加密货币和银行卡 */
  InfiniPayMethods: string
  InfiniUseRealtimeRate: boolean
  /** 手动 USD→CNY 汇率（元/美金），实时汇率关闭或不可用时使用 */
  InfiniExchangeRate: number
}

/** Infini 结算币种只支持美元；其它币种的到账换算口径不成立，后端会拒绝下单与保存。 */
const INFINI_SUPPORTED_CURRENCY = 'USD'

function isUnsupportedCurrency(value: string): boolean {
  const trimmed = value.trim()
  return trimmed !== '' && trimmed.toUpperCase() !== INFINI_SUPPORTED_CURRENCY
}

function hasUnsupportedCurrencyInJson(value: string): boolean {
  const trimmed = value.trim()
  if (trimmed === '' || trimmed === '[]') return false
  try {
    const parsed: unknown = JSON.parse(trimmed)
    if (!Array.isArray(parsed)) return false
    return parsed.some(
      (item) =>
        !!item &&
        typeof item === 'object' &&
        typeof (item as { currency?: unknown }).currency === 'string' &&
        isUnsupportedCurrency((item as { currency: string }).currency)
    )
  } catch {
    return false
  }
}

interface Props {
  values: InfiniSettingsValues
  onValueChange: <K extends keyof InfiniSettingsValues>(
    key: K,
    value: InfiniSettingsValues[K]
  ) => void
}

export function InfiniSettingsSection({ values, onValueChange }: Props) {
  const { t } = useTranslation()
  const currencyUnsupported = isUnsupportedCurrency(values.InfiniCurrency)
  const currenciesUnsupported = hasUnsupportedCurrencyInJson(
    values.InfiniCurrencies
  )

  return (
    <div className='space-y-4 pt-4'>
      <div>
        <h3 className='text-lg font-medium'>{t('Infini Payment Gateway')}</h3>
        <p className='text-muted-foreground text-sm'>
          {t('Configuration for Infini merchant API payment integration')}
        </p>
      </div>
      <Alert>
        <AlertDescription className='text-xs'>
          {t(
            'Register at business.infini.money, create an API key in the Developer page, and configure the webhook endpoint. Keep all secrets server-side only.'
          )}
        </AlertDescription>
      </Alert>

      <div className='grid gap-4 sm:grid-cols-2'>
        <SettingsSwitchField
          checked={values.InfiniEnabled}
          onCheckedChange={(v) => onValueChange('InfiniEnabled', v)}
          label={t('Enable Infini')}
          className='border-b-0 py-0'
        />
        <SettingsSwitchField
          checked={values.InfiniSandbox}
          onCheckedChange={(v) => onValueChange('InfiniSandbox', v)}
          label={t('Sandbox mode')}
          className='border-b-0 py-0'
        />
      </div>

      <div className='grid grid-cols-2 gap-4'>
        <div className='grid gap-1.5'>
          <Label>{t('API Key (keyId)')}</Label>
          <Input
            value={values.InfiniApiKey}
            onChange={(e) => onValueChange('InfiniApiKey', e.target.value)}
          />
        </div>
        <div className='grid gap-1.5'>
          <Label>{t('API Secret (signing key)')}</Label>
          <Input
            type='password'
            value={values.InfiniApiSecret}
            onChange={(e) => onValueChange('InfiniApiSecret', e.target.value)}
          />
        </div>
      </div>

      <div className='grid gap-1.5'>
        <Label>{t('Webhook Secret')}</Label>
        <Input
          type='password'
          value={values.InfiniWebhookSecret}
          onChange={(e) =>
            onValueChange('InfiniWebhookSecret', e.target.value)
          }
        />
      </div>

      <div className='grid grid-cols-3 gap-4'>
        <div className='grid gap-1.5'>
          <Label>{t('Currency')}</Label>
          <Input
            placeholder='USD'
            value={values.InfiniCurrency}
            onChange={(e) => onValueChange('InfiniCurrency', e.target.value)}
          />
          <p className='text-muted-foreground text-xs'>
            {t('Only USD is supported. Top-ups in other currencies are rejected at checkout.')}
          </p>
          {currencyUnsupported && (
            <p className='text-destructive text-xs'>
              {t('This currency is not supported and cannot be saved. Change it to USD.')}
            </p>
          )}
        </div>
        <div className='grid gap-1.5'>
          <Label>{t('Unit price (USD per unit)')}</Label>
          <Input
            type='number'
            min={0}
            step={0.01}
            value={values.InfiniUnitPrice}
            onChange={(e) =>
              onValueChange(
                'InfiniUnitPrice',
                e.target.value === '' ? 1 : e.target.valueAsNumber
              )
            }
          />
        </div>
        <div className='grid gap-1.5'>
          <Label>{t('Minimum top-up (units)')}</Label>
          <Input
            type='number'
            min={1}
            value={values.InfiniMinTopUp}
            onChange={(e) =>
              onValueChange(
                'InfiniMinTopUp',
                e.target.value === '' ? 1 : e.target.valueAsNumber
              )
            }
          />
        </div>
      </div>

      <div className='grid gap-4 sm:grid-cols-2'>
        <SettingsSwitchField
          checked={values.InfiniUseRealtimeRate}
          onCheckedChange={(v) => onValueChange('InfiniUseRealtimeRate', v)}
          label={t('Use real-time exchange rate')}
          description={t(
            'Use the live USD/CNY rate; fall back to the manual price below if it is unavailable'
          )}
          className='border-b-0 py-0'
        />
        <div className='grid gap-1.5'>
          <Label>{t('Manual exchange rate (local currency per USD)')}</Label>
          <Input
            type='number'
            min={0}
            step={0.01}
            value={values.InfiniExchangeRate}
            onChange={(e) =>
              onValueChange(
                'InfiniExchangeRate',
                e.target.value === '' ? 0 : e.target.valueAsNumber
              )
            }
          />
          <p className='text-muted-foreground text-xs'>
            {t(
              'How much local currency 1 USD converts to; only used when real-time rate is off or unavailable. Credit = paid USD x this rate / system top-up ratio'
            )}
          </p>
        </div>
      </div>

      <div className='grid grid-cols-3 gap-4'>
        <div className='grid gap-1.5'>
          <Label>{t('Webhook callback URL')}</Label>
          <Input
            placeholder='https://example.com/api/infini/webhook'
            value={values.InfiniNotifyUrl}
            onChange={(e) => onValueChange('InfiniNotifyUrl', e.target.value)}
          />
        </div>
        <div className='grid gap-1.5'>
          <Label>{t('Payment success URL')}</Label>
          <Input
            placeholder='https://example.com/console/topup'
            value={values.InfiniReturnUrl}
            onChange={(e) => onValueChange('InfiniReturnUrl', e.target.value)}
          />
        </div>
        <div className='grid gap-1.5'>
          <Label>{t('Payment failure URL')}</Label>
          <Input
            placeholder='https://example.com/console/topup'
            value={values.InfiniFailUrl}
            onChange={(e) => onValueChange('InfiniFailUrl', e.target.value)}
          />
        </div>
      </div>

      <div className='grid gap-1.5'>
        <Label>{t('Currency options (JSON)')}</Label>
        <Textarea
          rows={4}
          placeholder='[{"currency":"USD","unit_price":1.0,"min_topup":1},{"currency":"EUR","unit_price":0.92,"min_topup":1}]'
          value={values.InfiniCurrencies}
          onChange={(e) => onValueChange('InfiniCurrencies', e.target.value)}
          className='font-mono text-xs'
        />
        {currenciesUnsupported && (
          <p className='text-destructive text-xs'>
            {t('This currency is not supported and cannot be saved. Change it to USD.')}
          </p>
        )}
        <div className='text-muted-foreground space-y-1 text-xs'>
          <p>{t('JSON array. Each item configures one selectable currency for users. Overrides single currency fields above.')}</p>
          <ul className='ml-3 list-disc space-y-0.5'>
            <li><code className='text-xs'>currency</code> — {t('Settlement currency code. Only USD is supported; entries in any other currency are rejected on save and at checkout.')}</li>
            <li><code className='text-xs'>unit_price</code> — {t('Price per quota unit in USD. Example: if 1 unit costs $1 USD, set 1.0')}</li>
            <li><code className='text-xs'>min_topup</code> — {t('Minimum quota units users must buy in one order')}</li>
          </ul>
        </div>
      </div>

      <div className='grid gap-1.5'>
        <Label>{t('Allowed payment methods (JSON)')}</Label>
        <Input
          placeholder='[1,2]'
          value={values.InfiniPayMethods}
          onChange={(e) => onValueChange('InfiniPayMethods', e.target.value)}
          className='font-mono text-xs'
        />
        <p className='text-muted-foreground text-xs'>
          {t('Restrict checkout payment methods. 1=Crypto, 2=Card, 3=Binance Pay, 5=Apple Pay, 6=Google Pay. Leave empty for merchant default.')}
        </p>
      </div>

    </div>
  )
}

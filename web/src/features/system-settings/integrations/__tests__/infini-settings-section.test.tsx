import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import {
  InfiniSettingsSection,
  type InfiniSettingsValues,
} from '../infini-settings-section'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))

describe('Infini v2 credentials', () => {
  it('accepts a multiline private key and explains public-key registration', () => {
    const onValueChange = vi.fn()
    const values: InfiniSettingsValues = {
      InfiniEnabled: false,
      InfiniV2KeyID: '',
      InfiniV2PrivateKey: '',
      InfiniWebhookSecret: '',
      InfiniSandbox: true,
      InfiniNotifyUrl: '',
      InfiniReturnUrl: '',
      InfiniFailUrl: '',
      InfiniUnitPrice: 1,
      InfiniMinTopUp: 1,
      InfiniCurrency: 'USD',
      InfiniCurrencies: '',
      InfiniPayMethods: '',
      InfiniUseRealtimeRate: false,
      InfiniExchangeRate: 7,
    }
    render(
      <InfiniSettingsSection values={values} onValueChange={onValueChange} />
    )
    const input = screen.getByLabelText('Ed25519 private key (PKCS#8 PEM)')
    expect(input.tagName).toBe('TEXTAREA')
    const pem =
      '-----BEGIN PRIVATE KEY-----\nexample\n-----END PRIVATE KEY-----'
    fireEvent.change(input, { target: { value: pem } })
    expect(onValueChange).toHaveBeenCalledWith('InfiniV2PrivateKey', pem)
    expect(
      screen.getByText(
        'Upload your Ed25519 public key in the Infini console to obtain a v2 Key ID. Configure /api/infini/webhook in the console. Leave the private key blank to keep the saved key.'
      )
    ).toBeTruthy()
  })
})

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
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, expect, it, vi } from 'vitest'

import { IPProvidersField, type IPProvider } from './ip-providers-field'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))
afterEach(cleanup)

it('preserves configured keys as blank and submits an explicit clear marker', async () => {
  const provider: IPProvider = {
    id: 'custom-1',
    name: 'Local',
    type: 'custom_ipapi',
    endpoint: 'https://local.example',
    enabled: true,
    priority: 100,
    api_key: '',
    key_configured: true,
    daily_limit: 1000,
    capabilities: ['geo', 'tor', 'future_capability'],
    allowed_private_hosts: ['local.example'],
  }
  const change = vi.fn()
  function Editor() {
    const [value, setValue] = useState(JSON.stringify([provider]))
    const [privateHosts, setPrivateHosts] = useState('local.example')
    const [dailyLimit, setDailyLimit] = useState('1000')
    return (
      <IPProvidersField
        value={value}
        privateHosts={privateHosts}
        dailyLimit={dailyLimit}
        onPrivateHostsChange={setPrivateHosts}
        onDailyLimitChange={setDailyLimit}
        disabled={false}
        onChange={(next) => {
          setValue(next)
          change(JSON.parse(next))
        }}
      />
    )
  }
  render(<Editor />)
  expect(screen.getByLabelText('Access key')).toHaveValue('')
  expect(
    screen.getByPlaceholderText('Configured; leave blank to keep')
  ).toBeVisible()
  fireEvent.change(screen.getByLabelText('Service URL'), {
    target: { value: 'https://updated.example' },
  })
  expect(change).toHaveBeenLastCalledWith([
    expect.objectContaining({
      id: 'custom-1',
      api_key: '',
      key_configured: true,
      capabilities: ['geo', 'tor', 'future_capability'],
    }),
  ])
  const user = userEvent.setup()
  await user.click(screen.getByLabelText('Clear saved key'))
  expect(change).toHaveBeenLastCalledWith([
    expect.objectContaining({ api_key: '', clear_secret: true }),
  ])
  fireEvent.change(screen.getByLabelText('Access key'), {
    target: { value: 'replacement' },
  })
  expect(change).toHaveBeenLastCalledWith([
    expect.objectContaining({ api_key: 'replacement', clear_secret: false }),
  ])
})

it('does not overwrite unreadable provider configuration', () => {
  const change = vi.fn()
  render(
    <IPProvidersField
      value='{broken'
      privateHosts=''
      dailyLimit='0'
      onPrivateHostsChange={vi.fn()}
      onDailyLimitChange={vi.fn()}
      disabled={false}
      onChange={change}
    />
  )
  expect(
    screen.getByText(
      'Migrate the old provider settings to a self-hosted IP lookup service first.'
    )
  ).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Configure service' })
  ).not.toBeInTheDocument()
  expect(change).not.toHaveBeenCalled()
})

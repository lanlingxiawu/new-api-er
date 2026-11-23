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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { LoginProtectionSection } from '../login-protection-section'

function Fixture() {
  const [container, setContainer] = useState<HTMLDivElement | null>(null)
  return (
    <>
      <div ref={setContainer} />
      <SettingsPageProvider
        actionsContainer={container}
        scope='auth.login-protection'
        canEdit
      >
        <LoginProtectionSection enabled={false} maxFailures={5} />
      </SettingsPageProvider>
    </>
  )
}

function renderSettings() {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { mutations: { retry: false } } })
      }
    >
      <Fixture />
    </QueryClientProvider>
  )
}

test('saves the switch and threshold together with the settings scope', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true, data: { applied: true } } })
  renderSettings()
  await userEvent.click(
    screen.getByRole('switch', { name: 'Enable login failure protection' })
  )
  fireEvent.change(
    screen.getByRole('spinbutton', {
      name: 'Maximum consecutive login failures',
    }),
    { target: { value: '10' } }
  )
  await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
  await userEvent.click(
    within(await screen.findByRole('alertdialog')).getByRole('button', {
      name: 'Save Changes',
    })
  )
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith('/api/option/group', {
      scope: 'auth.login-protection',
      module: 'login_protection_setting',
      values: { enabled: 'true', max_failures: '10' },
    })
  )
})

test.each(['0', '101', '1.5', ''])(
  'rejects invalid failure threshold %s before saving',
  async (value) => {
    const put = vi.spyOn(api, 'put')
    renderSettings()
    fireEvent.change(
      screen.getByRole('spinbutton', {
        name: 'Maximum consecutive login failures',
      }),
      { target: { value } }
    )
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    expect(put).not.toHaveBeenCalled()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  }
)

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
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { systemTuningFallbackSettings } from '../../system-tuning/defaults'
import { getSystemTuningSectionContent } from '../../system-tuning/section-registry'

function Fixture(props: { admin: number }) {
  const [container, setContainer] = useState<HTMLDivElement | null>(null)
  return (
    <>
      <div ref={setContainer} />
      <SettingsPageProvider
        actionsContainer={container}
        scope='system-tuning.log-export'
        canEdit
      >
        {getSystemTuningSectionContent('log-export', {
          ...systemTuningFallbackSettings,
          'log_export_setting.admin_cooldown_sec': props.admin,
          'log_export_setting.user_cooldown_sec': 300,
        })}
      </SettingsPageProvider>
    </>
  )
}

function renderSettings(admin: number) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <Fixture admin={admin} />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
})

test.each([-1, 0, 1, 86400])(
  'saves administrator cooldown %s independently',
  async (value) => {
    const user = userEvent.setup()
    const view = renderSettings(30)
    const admin = screen.getByRole('combobox', {
      name: 'Administrator Export Cooldown (s)',
    })
    expect(
      screen.getByRole('combobox', {
        name: 'User and Employee Export Cooldown (s)',
      })
    ).toHaveValue('300')
    fireEvent.change(admin, { target: { value: String(value) } })
    await user.click(
      screen.getByRole('button', { name: 'Save log export settings' })
    )
    await user.click(
      within(await screen.findByRole('alertdialog')).getByRole('button', {
        name: 'Save Changes',
      })
    )
    await waitFor(() =>
      expect(api.put).toHaveBeenCalledExactlyOnceWith('/api/option/', {
        scope: 'system-tuning.log-export',
        key: 'log_export_setting.admin_cooldown_sec',
        value,
      })
    )
    view.unmount()
    renderSettings(value)
    const reloaded = screen.getByRole('combobox', {
      name: 'Administrator Export Cooldown (s)',
    })
    if (value === 0) {
      expect(reloaded).toHaveValue('No cooldown (0)')
    } else if (value === -1) {
      expect(reloaded).toHaveValue('Use user and employee cooldown (-1)')
    } else {
      expect(reloaded).toHaveValue(String(value))
    }
  }
)

test.each(['-2', '86401', '1.5', ''])(
  'rejects invalid administrator cooldown %s',
  async (value) => {
    const user = userEvent.setup()
    renderSettings(-1)
    const admin = screen.getByRole('combobox', {
      name: 'Administrator Export Cooldown (s)',
    })
    fireEvent.change(admin, { target: { value } })
    await user.click(
      screen.getByRole('button', { name: 'Save log export settings' })
    )
    await waitFor(() => expect(admin).toHaveAttribute('aria-invalid', 'true'))
    expect(api.put).not.toHaveBeenCalled()
  }
)

test.each([1, 1000000])(
  'saves the visible XLSX row limit %s',
  async (value) => {
    renderSettings(30)
    const input = screen.getByRole('spinbutton', { name: 'XLSX Row Limit' })
    expect(input).toHaveValue(200000)
    fireEvent.change(input, { target: { value: String(value) } })
    await userEvent.click(
      screen.getByRole('button', { name: 'Save log export settings' })
    )
    await userEvent.click(
      within(await screen.findByRole('alertdialog')).getByRole('button', {
        name: 'Save Changes',
      })
    )
    await waitFor(() =>
      expect(api.put).toHaveBeenCalledExactlyOnceWith('/api/option/', {
        scope: 'system-tuning.log-export',
        key: 'log_export_setting.xlsx_max_rows',
        value,
      })
    )
  }
)

test.each(['0', '1000001', '1.5'])(
  'rejects invalid XLSX row limit %s',
  async (value) => {
    renderSettings(30)
    const input = screen.getByRole('spinbutton', { name: 'XLSX Row Limit' })
    fireEvent.change(input, { target: { value } })
    await userEvent.click(
      screen.getByRole('button', { name: 'Save log export settings' })
    )
    await waitFor(() => expect(input).toHaveAttribute('aria-invalid', 'true'))
    expect(api.put).not.toHaveBeenCalled()
  }
)

test('keeps the previous XLSX row limit when a numeric edit is empty', async () => {
  renderSettings(30)
  const input = screen.getByRole('spinbutton', { name: 'XLSX Row Limit' })
  fireEvent.change(input, { target: { value: '' } })
  await userEvent.click(
    screen.getByRole('button', { name: 'Save log export settings' })
  )
  expect(input).toHaveValue(200000)
  expect(api.put).not.toHaveBeenCalled()
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
})

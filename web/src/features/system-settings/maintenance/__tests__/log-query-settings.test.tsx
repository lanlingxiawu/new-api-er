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

function Fixture() {
  const [container, setContainer] = useState<HTMLDivElement | null>(null)
  return (
    <>
      <div ref={setContainer} />
      <SettingsPageProvider
        actionsContainer={container}
        scope='system-tuning.log-query'
        canEdit
      >
        {getSystemTuningSectionContent(
          'log-query',
          systemTuningFallbackSettings
        )}
      </SettingsPageProvider>
    </>
  )
}

function renderSettings() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <Fixture />
    </QueryClientProvider>
  )
}

const limits = [
  ['Hourly Result Retention', 'stat_cache_ttl_hours', 1, 8760],
  ['Current Hour Refresh Interval', 'current_hour_ttl_sec', 0, 300],
  ['Hours To Precompute On Startup', 'warm_hours', 0, 720],
  ['Log Query Timeout', 'query_timeout_ms', 1000, 60000],
] as const

beforeEach(() => {
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
})

test.each(limits)(
  'saves both runtime boundaries for %s',
  async (label, key, min, max) => {
    renderSettings()
    for (const value of [max, min]) {
      vi.mocked(api.put).mockClear()
      fireEvent.change(screen.getByLabelText(label), {
        target: { value: String(value) },
      })
      await userEvent.click(
        screen.getByRole('button', { name: 'Save log query settings' })
      )
      await userEvent.click(
        within(await screen.findByRole('alertdialog')).getByRole('button', {
          name: 'Save Changes',
        })
      )
      await waitFor(() =>
        expect(api.put).toHaveBeenCalledExactlyOnceWith('/api/option/', {
          scope: 'system-tuning.log-query',
          key: `log_query_setting.${key}`,
          value,
        })
      )
      await waitFor(() =>
        expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
      )
    }
  }
)

test.each(limits)(
  'rejects out of range and non-integer values for %s',
  async (label, _key, min, max) => {
    renderSettings()
    const input = screen.getByLabelText(label)
    for (const value of [min - 1, max + 1, max + 0.5]) {
      fireEvent.change(input, { target: { value: String(value) } })
      await userEvent.click(
        screen.getByRole('button', { name: 'Save log query settings' })
      )
      await waitFor(() => expect(input).toHaveAttribute('aria-invalid', 'true'))
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
      expect(api.put).not.toHaveBeenCalled()
    }
  }
)

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { ConfigGroupSection } from '../hot-config-sections'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

function Workspace({ limit = 180 }: { limit?: number }) {
  const [container, setContainer] = useState<HTMLDivElement | null>(null)
  return (
    <>
      <div ref={setContainer} />
      <SettingsPageProvider
        actionsContainer={container}
        scope='system-tuning.gateway-rate-limit'
        canEdit
      >
        <ConfigGroupSection
          title='Gateway Rate Limiting'
          module='rate_limit_setting'
          defaults={{ global_api_num: limit }}
          fields={[{ key: 'global_api_num', label: 'Request limit', min: 0 }]}
        />
      </SettingsPageProvider>
    </>
  )
}

it('rejects empty/fractional integer settings and saves through the shared page action', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  const client = new QueryClient()
  render(
    <QueryClientProvider client={client}>
      <Workspace />
    </QueryClientProvider>
  )
  const input = screen.getByRole('spinbutton', { name: 'Request limit' })
  for (const value of ['', '1.5']) {
    fireEvent.change(input, { target: { value } })
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(put).not.toHaveBeenCalled()
  }
  fireEvent.change(input, { target: { value: '200' } })
  await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
  await userEvent.click(
    within(await screen.findByRole('alertdialog')).getByRole('button', {
      name: 'Save Changes',
    })
  )
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith('/api/option/group', {
      scope: 'system-tuning.gateway-rate-limit',
      module: 'rate_limit_setting',
      values: { global_api_num: '200' },
    })
  )
  client.clear()
})

it('preserves a draft when server defaults refresh and permits retry after a network failure', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockRejectedValueOnce(new Error('Network Error'))
    .mockResolvedValue({ data: { success: true } })
  const client = new QueryClient({
    defaultOptions: { mutations: { retry: false } },
  })
  const view = render(
    <QueryClientProvider client={client}>
      <Workspace />
    </QueryClientProvider>
  )
  const input = screen.getByRole('spinbutton', { name: 'Request limit' })
  fireEvent.change(input, { target: { value: '200' } })
  view.rerender(
    <QueryClientProvider client={client}>
      <Workspace limit={190} />
    </QueryClientProvider>
  )
  expect(input).toHaveValue(200)
  for (let attempt = 1; attempt <= 2; attempt++) {
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    await userEvent.click(
      within(await screen.findByRole('alertdialog')).getByRole('button', {
        name: 'Save Changes',
      })
    )
    await waitFor(() =>
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    )
    expect(put).toHaveBeenCalledTimes(attempt)
    expect(input).toHaveValue(200)
  }
  client.clear()
})

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { ChannelHealthSection } from '../channel-health-section'
import { defaultRequestPolicySettings } from '../defaults'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

function Workspace() {
  const [container, setContainer] = useState<HTMLDivElement | null>(null)
  return (
    <>
      <div ref={setContainer} />
      <SettingsPageProvider
        actionsContainer={container}
        scope='request-policies.health'
        canEdit
      >
        <ChannelHealthSection defaultValues={defaultRequestPolicySettings} />
      </SettingsPageProvider>
    </>
  )
}

it('validates and saves daily limits with the health page action', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  const patch = vi
    .spyOn(api, 'patch')
    .mockResolvedValue({ data: { success: true, data: { options: {} } } })
  const client = new QueryClient({
    defaultOptions: { mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <Workspace />
    </QueryClientProvider>
  )
  const days = screen.getByRole('spinbutton', {
    name: 'Usage history retention (days)',
  })
  fireEvent.change(days, { target: { value: '6' } })
  await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
  expect(put).not.toHaveBeenCalled()
  fireEvent.change(days, { target: { value: '30' } })
  await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith('/api/option/group', {
      scope: 'request-policies.health',
      module: 'channel_daily_limit_setting',
      values: { retention_days: '30' },
    })
  )
  expect(patch).not.toHaveBeenCalled()
  expect(
    screen.queryByRole('button', { name: /^Save$/ })
  ).not.toBeInTheDocument()
  client.clear()
})

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { SettingsPage } from './settings-page'

vi.mock('@tanstack/react-router', () => ({
  useParams: () => ({ section: 'notice' }),
}))

test('a read-only administrator can retry loading settings while editing remains disabled', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'viewer', role: 10 })
  vi.spyOn(api, 'get')
    .mockRejectedValueOnce(new Error('Unavailable'))
    .mockResolvedValue({ data: { success: true, data: [] } })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SettingsPage
        routePath='/test'
        group='site'
        defaultSection='notice'
        defaultSettings={{ Notice: '' }}
        getSectionMeta={() => ({ titleKey: 'Notice' })}
        getSectionContent={() => <input aria-label='Notice content' />}
      />
    </QueryClientProvider>
  )
  const retry = await screen.findByRole('button', { name: 'Retry' })
  expect(retry).toBeEnabled()
  await userEvent.click(retry)
  expect(
    await screen.findByRole('textbox', { name: 'Notice content' })
  ).toBeDisabled()
})

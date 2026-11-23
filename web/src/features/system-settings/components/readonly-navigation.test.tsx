import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { BillingSettings } from '../billing'
import { ContentSettings } from '../content'
import { SecuritySettings } from '../security'

const route = vi.hoisted(() => ({ section: 'chat' }))
vi.mock('@tanstack/react-router', async (original) => ({
  ...(await original<typeof import('@tanstack/react-router')>()),
  useParams: () => route,
  useSearch: () => ({}),
}))
vi.mock('@/hooks', async (original) => ({
  ...(await original<typeof import('@/hooks')>()),
  useMediaQuery: () => true,
}))
vi.mock('@/features/model-pricing/workspace', () => ({
  ModelPricingWorkspace: ({ children }: { children: ReactNode }) => children,
}))

let client: QueryClient
beforeEach(() => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'viewer', role: 10 })
  vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data: [] } })
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
})
afterEach(() => {
  client.clear()
  useAuthStore.getState().auth.reset()
})

test('read-only chat settings allow switching representation while fields stay disabled', async () => {
  route.section = 'chat'
  render(
    <QueryClientProvider client={client}>
      <ContentSettings />
    </QueryClientProvider>
  )
  const tab = await screen.findByRole('tab', { name: 'JSON' })
  expect(tab).toBeEnabled()
  fireEvent.click(tab)
  expect(tab).toHaveAttribute('aria-selected', 'true')
  expect(await screen.findByRole('textbox')).toBeDisabled()
  expect(
    screen.queryByRole('button', { name: 'Save chat settings' })
  ).not.toBeInTheDocument()
})

test('read-only pricing tabs remain usable on phones while price fields stay disabled', async () => {
  route.section = 'model-pricing'
  render(
    <QueryClientProvider client={client}>
      <BillingSettings />
    </QueryClientProvider>
  )
  const tab = await screen.findByRole('tab', { name: 'Tool prices' })
  expect(tab).toBeEnabled()
  fireEvent.click(tab)
  expect(tab).toHaveAttribute('aria-selected', 'true')
  const inputs = screen.getAllByRole('spinbutton')
  for (const input of inputs) expect(input).toBeDisabled()
})

test('read-only rate limits allow switching to JSON without enabling edits', async () => {
  route.section = 'rate-limit'
  render(
    <QueryClientProvider client={client}>
      <SecuritySettings />
    </QueryClientProvider>
  )
  const toggle = await screen.findByRole('button', { name: 'JSON Mode' })
  expect(toggle).toBeEnabled()
  fireEvent.click(toggle)
  expect(screen.getByRole('button', { name: 'Visual Mode' })).toBeEnabled()
  expect(await screen.findByRole('textbox')).toBeDisabled()
  for (const input of screen.getAllByRole('spinbutton')) {
    expect(input).toBeDisabled()
  }
  expect(screen.getByRole('switch')).toBeDisabled()
  expect(
    screen.queryByRole('button', { name: 'Save rate limits' })
  ).not.toBeInTheDocument()
})

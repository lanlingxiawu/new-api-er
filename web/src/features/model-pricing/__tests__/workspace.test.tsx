import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { pricingOptions } from '../pricing'
import { ModelPricingWorkspace } from '../workspace'

afterEach(() => {
  cleanup()
  useAuthStore.getState().auth.reset()
})

it('preserves the focused model draft when navigation is cancelled', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        entries: [
          {
            model_name: 'example',
            version: 'v1',
            configured: { ModelPrice: 1 },
            effective: { ModelPrice: 1 },
          },
        ],
        options: pricingOptions({}),
        empty_version: 'empty',
      },
    },
  })
  const root = createRootRoute()
  const billing = createRoute({
    getParentRoute: () => root,
    path: '/system-settings/billing/$section',
    validateSearch: (search: Record<string, unknown>) => ({
      model: typeof search.model === 'string' ? search.model : undefined,
    }),
    component: () => (
      <ModelPricingWorkspace>
        <p>All prices</p>
      </ModelPricingWorkspace>
    ),
  })
  const router = createRouter({
    routeTree: root.addChildren([billing]),
    history: createMemoryHistory({
      initialEntries: ['/system-settings/billing/model-pricing?model=example'],
    }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  const price = await screen.findByPlaceholderText('0.01')
  const user = userEvent.setup()
  await user.clear(price)
  await user.type(price, '2')
  await user.click(screen.getByRole('link', { name: 'All Models' }))
  expect(await screen.findByRole('alertdialog')).toHaveTextContent(
    'Unsaved changes'
  )
  await user.click(screen.getByRole('button', { name: 'Stay' }))
  expect(price).toHaveValue('2')
  await waitFor(() =>
    expect(router.state.location.search.model).toBe('example')
  )
  client.clear()
})

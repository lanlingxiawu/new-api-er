import { render, screen, waitFor } from '@testing-library/react'
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { SubscriptionsMutateDrawer } from './subscriptions-mutate-drawer'

vi.mock('./subscriptions-provider', () => ({
  useSubscriptions: () => ({ triggerRefresh: vi.fn() }),
}))

test.each([false, true])(
  'requests payment catalog only with payment view permission: %s',
  async (canView) => {
    useAuthStore.getState().auth.setUser({
      id: 1,
      username: 'admin',
      role: 10,
      permissions: {
        admin_permissions: {
          'system_settings.billing.payment': { view: canView, edit: false },
        },
      },
    })
    const get = vi
      .spyOn(api, 'get')
      .mockResolvedValue({ data: { success: true, data: [] } })
    render(<SubscriptionsMutateDrawer open onOpenChange={vi.fn()} />)
    await waitFor(() => expect(get).toHaveBeenCalled())
    expect(
      get.mock.calls.some(([url]) =>
        url.includes('subscription-product-options')
      )
    ).toBe(canView)
    expect(
      screen.queryByRole('button', { name: '+ Create' })
    ).not.toBeInTheDocument()
  }
)

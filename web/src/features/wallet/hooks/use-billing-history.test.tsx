import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { useBillingHistory } from './use-billing-history'

function setUser(role: number, canViewUsers = false) {
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'tester',
    role,
    permissions: {
      admin_permissions: { 'admin_menu.users': { view: canViewUsers } },
    },
  })
}

beforeEach(() => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: [], total: 0 } },
  })
})

afterEach(() => {
  useAuthStore.setState(useAuthStore.getInitialState(), true)
})

test('does not load a closed billing dialog, loads on open and ignores responses after closing', async () => {
  setUser(ROLE.ADMIN)
  const { result, rerender } = renderHook(
    ({ enabled }) => useBillingHistory({ enabled }),
    { initialProps: { enabled: false } }
  )
  expect(api.get).not.toHaveBeenCalled()
  rerender({ enabled: true })
  await waitFor(() => expect(result.current.loading).toBe(false))
  expect(api.get).toHaveBeenCalledTimes(1)

  let resolve!: (value: unknown) => void
  vi.mocked(api.get).mockImplementationOnce(
    () =>
      new Promise((done) => {
        resolve = done
      })
  )
  act(() => {
    void result.current.refresh()
  })
  rerender({ enabled: false })
  await act(async () => {
    resolve({
      data: { success: true, data: { items: [{ id: 99 }], total: 1 } },
    })
  })
  expect(result.current.records).toEqual([])
  expect(result.current.loading).toBe(false)
})

test.each([
  { role: ROLE.USER, grant: false, admin: false },
  { role: ROLE.ADMIN, grant: false, admin: false },
  { role: ROLE.ADMIN, grant: true, admin: true },
  { role: ROLE.SUPER_ADMIN, grant: false, admin: true },
])(
  'selects an authorized billing scope for role $role with users grant $grant',
  async ({ role, grant, admin }) => {
    setUser(role, grant)
    const { result } = renderHook(() => useBillingHistory())
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.isAdmin).toBe(admin)
    expect(vi.mocked(api.get).mock.calls[0][0].split('?')[0]).toBe(
      admin ? '/api/user/topup' : '/api/user/topup/self'
    )
  }
)

test('switches to personal billing when the users permission is revoked', async () => {
  setUser(ROLE.ADMIN, true)
  const { result } = renderHook(() => useBillingHistory())
  await waitFor(() => expect(result.current.loading).toBe(false))
  act(() => setUser(ROLE.ADMIN, false))
  await waitFor(() => expect(result.current.loading).toBe(false))
  expect(result.current.isAdmin).toBe(false)
  expect(vi.mocked(api.get).mock.lastCall?.[0].split('?')[0]).toBe(
    '/api/user/topup/self'
  )
})

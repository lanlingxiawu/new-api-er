import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { getUserQuotaDates } from './api'

test.each([true, false])(
  'uses the registered quota route for admin=%s',
  async (isAdmin) => {
    const get = vi
      .spyOn(api, 'get')
      .mockResolvedValue({ data: { success: true, data: [] } })
    const params = { start_timestamp: 1, end_timestamp: 2 }
    await getUserQuotaDates(params, isAdmin)
    expect(get).toHaveBeenCalledWith(
      isAdmin ? '/api/data/' : '/api/data/self',
      { params }
    )
  }
)

import { describe, expect, it } from 'vitest'

import { transformFormDataToPayload, userFormSchema } from './user-form'

describe('user channel exclusions', () => {
  it('keeps omission distinct from clearing an existing policy', () => {
    expect(
      transformFormDataToPayload({ username: 'test' }, 1).excluded_channel_ids
    ).toBeUndefined()
    expect(
      transformFormDataToPayload(
        { username: 'test', excluded_channel_ids: [] },
        1
      ).excluded_channel_ids
    ).toEqual([])
    expect(
      transformFormDataToPayload(
        { username: 'test', excluded_channel_ids: [12] },
        1
      ).excluded_channel_ids
    ).toEqual([12])
  })
  it('rejects invalid IDs and more than 256 selections', () => {
    for (const ids of [
      [0],
      [-1],
      [1.5],
      Array.from({ length: 257 }, (_, i) => i + 1),
    ]) {
      expect(
        userFormSchema.safeParse({
          username: 'test',
          excluded_channel_ids: ids,
        }).success
      ).toBe(false)
    }
    expect(
      userFormSchema.safeParse({
        username: 'test',
        excluded_channel_ids: Array.from({ length: 256 }, (_, i) => i + 1),
      }).success
    ).toBe(true)
  })
})

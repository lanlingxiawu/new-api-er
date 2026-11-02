import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { getChannelExclusionOptions } from '../../api'
import { UserChannelExclusionsField } from '../user-channel-exclusions-field'

vi.mock('../../api', () => ({ getChannelExclusionOptions: vi.fn() }))
const api = vi.mocked(getChannelExclusionOptions)
const channel = (id: number, name = `channel-${id}`) => ({
  id,
  name,
  type: 1,
  status: 2,
})
const page = (ids: number[], more = false) => ({
  items: ids.map((id) => channel(id)),
  next_after_id: ids.at(-1) ?? 0,
  has_more: more,
})

function mount(value: number[] = []) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const onChange = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <UserChannelExclusionsField value={value} onChange={onChange} />
    </QueryClientProvider>
  )
  return { onChange }
}

function scrollToEnd() {
  const list = screen.getByRole('listbox')
  Object.defineProperties(list, {
    scrollHeight: { configurable: true, value: 1400 },
    clientHeight: { configurable: true, value: 250 },
    scrollTop: { configurable: true, value: 1150 },
  })
  fireEvent.scroll(list)
}

describe('channel exclusion picker', () => {
  beforeEach(() => {
    api.mockReset()
  })

  it('automatically appends the next page on scroll and stops at the last page', async () => {
    api
      .mockResolvedValueOnce(
        page(
          Array.from({ length: 50 }, (_, i) => i + 1),
          true
        )
      )
      .mockResolvedValueOnce(page([51]))
    const user = userEvent.setup()
    const { onChange } = mount()
    await user.click(screen.getByRole('combobox'))
    await screen.findByRole('option', { name: '#50 channel-50' })
    expect(
      screen.queryByRole('button', { name: 'Load more' })
    ).not.toBeInTheDocument()
    scrollToEnd()
    await screen.findByRole('option', { name: '#51 channel-51' })
    expect(screen.getAllByRole('option')).toHaveLength(51)
    scrollToEnd()
    expect(api).toHaveBeenCalledTimes(2)
    await user.click(screen.getByRole('option', { name: '#51 channel-51' }))
    expect(onChange).toHaveBeenCalledWith([51])
  })

  it('searches beyond loaded pages and resets the cursor while preserving selected chips', async () => {
    api.mockImplementation(async (params) => {
      if (params.ids) return page([99])
      if (params.keyword === 'remote') {
        return { ...page([500]), items: [channel(500, 'remote')] }
      }
      return page([1], true)
    })
    const user = userEvent.setup()
    mount([99])
    const input = screen.getByRole('combobox')
    await user.click(input)
    await screen.findByRole('option', { name: '#1 channel-1' })
    await user.type(input, 'remote')
    await screen.findByRole('option', { name: '#500 remote' })
    expect(api).toHaveBeenCalledWith(
      { after_id: 0, keyword: 'remote' },
      expect.any(AbortSignal)
    )
    expect(screen.getByText('#99 channel-99')).toBeVisible()
    await user.clear(input)
    await screen.findByRole('option', { name: '#1 channel-1' })
  })

  it('keeps loaded options and offers retry inside the popup after a next-page failure', async () => {
    api
      .mockResolvedValueOnce(page([1], true))
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce(page([2]))
    const user = userEvent.setup()
    mount()
    await user.click(screen.getByRole('combobox'))
    await screen.findByRole('option', { name: '#1 channel-1' })
    scrollToEnd()
    const retry = await screen.findByRole('button', { name: 'Retry' })
    expect(retry.closest('[data-slot="combobox-content"]')).not.toBeNull()
    expect(screen.getByRole('option', { name: '#1 channel-1' })).toBeVisible()
    await user.click(retry)
    await screen.findByRole('option', { name: '#2 channel-2' })
    await waitFor(() =>
      expect(
        within(screen.getByRole('listbox')).getAllByRole('option')
      ).toHaveLength(2)
    )
  })
})

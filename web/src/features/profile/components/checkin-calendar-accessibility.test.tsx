import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import { CheckinCalendarCard } from './checkin-calendar-card'

const getStatus = vi.hoisted(() => vi.fn())
vi.mock('../api', () => ({
  getCheckinStatus: getStatus,
  performCheckin: vi.fn(),
}))

it('names both month navigation buttons and lets users move months', async () => {
  getStatus.mockResolvedValue({
    success: true,
    data: { stats: { records: [], checked_in_today: false } },
  })
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={new QueryClient()}>
      <CheckinCalendarCard
        checkinEnabled
        turnstileEnabled={false}
        turnstileSiteKey=''
      />
    </QueryClientProvider>
  )
  const previous = await screen.findByRole('button', { name: 'Previous month' })
  const next = screen.getByRole('button', { name: 'Next month' })
  expect(next).toBeEnabled()
  await user.click(previous)
  expect(getStatus.mock.calls.at(-1)?.[0]).not.toBe(getStatus.mock.calls[0][0])
})

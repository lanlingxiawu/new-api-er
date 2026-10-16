import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { GroupRetrySection } from '../group-retry-section'

beforeEach(() => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: ['default'] },
  })
})
afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

function show() {
  render(
    <QueryClientProvider
      client={
        new QueryClient({
          defaultOptions: {
            mutations: { retry: false },
            queries: { retry: false },
          },
        })
      }
    >
      <GroupRetrySection
        value='{"default":2}'
        statusRules='{"default":"429"}'
        statusEnabled
      />
    </QueryClientProvider>
  )
}

it('saves retry counts, status codes and the switch in one request', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  show()
  fireEvent.change(screen.getByLabelText('Retry attempts'), {
    target: { value: '3' },
  })
  fireEvent.click(screen.getByRole('switch'))
  fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
  expect(put.mock.calls[0][1]).toMatchObject({
    module: 'group_retry_status_setting',
    group_retry_times: '{"default":3}',
    values: { enabled: 'false', rules: '{"default":"429"}' },
  })
  expect(
    screen.queryByRole('button', { name: 'Save retry counts' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Save retry status codes' })
  ).not.toBeInTheDocument()
})

it('keeps the draft after a rejected save and retries the complete form', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValueOnce({
      data: { success: false, message: 'Save rejected' },
    })
    .mockResolvedValue({ data: { success: true } })
  show()
  fireEvent.change(screen.getByLabelText('Retry attempts'), {
    target: { value: '4' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled()
  )
  expect(screen.getByLabelText('Retry attempts')).toHaveValue('4')
  fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(put).toHaveBeenCalledTimes(2))
  expect(put.mock.calls[1][1]).toMatchObject({
    group_retry_times: '{"default":4}',
  })
})

it('rejects an invalid count before saving any status changes', () => {
  const put = vi.spyOn(api, 'put')
  show()
  fireEvent.change(screen.getByLabelText('Retry attempts'), {
    target: { value: '21' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(put).not.toHaveBeenCalled()
})

it('rejects invalid status codes before saving changed counts', () => {
  const put = vi.spyOn(api, 'put')
  show()
  fireEvent.change(screen.getByLabelText('Retry attempts'), {
    target: { value: '3' },
  })
  fireEvent.change(
    screen.getByRole('textbox', { name: 'Retry status codes' }),
    {
      target: { value: '600' },
    }
  )
  fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(put).not.toHaveBeenCalled()
})

it('disables editing and repeat submission while the complete form is saving', async () => {
  let finish!: (value: unknown) => void
  const put = vi.spyOn(api, 'put').mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve
      })
  )
  show()
  fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Saving...' })).toBeDisabled()
  )
  expect(screen.getByRole('switch')).toHaveAttribute('aria-disabled', 'true')
  expect(screen.getByLabelText('Retry attempts')).toBeDisabled()
  expect(put).toHaveBeenCalledTimes(1)
  finish({ data: { success: true } })
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled()
  )
})

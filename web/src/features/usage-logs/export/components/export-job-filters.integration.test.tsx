import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { ExportJobFilters } from './export-job-filters'

const request = vi.hoisted(() => vi.fn())
vi.mock('../employee-api', () => ({ exportRequest: request }))
afterEach(() => {
  cleanup()
  request.mockReset()
})

it('combines a customer selected from the real dropdown with status, then clears it', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  request.mockResolvedValue({
    items: [{ value: '42', label: 'Alice Customer' }],
    next_cursor: '',
  })
  const onApply = vi.fn()
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={client}>
      <ExportJobFilters value={{}} onApply={onApply} employee />
    </QueryClientProvider>
  )
  const customer = screen.getByRole('combobox', { name: 'Customer' })
  await user.click(customer)
  await user.type(customer, 'Alice')
  await user.click(
    await screen.findByRole('option', { name: 'Alice Customer' })
  )
  expect(customer).toHaveValue('Alice Customer')
  await user.selectOptions(screen.getByLabelText('Status'), 'ready')
  expect(onApply).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Search' }))
  expect(onApply).toHaveBeenLastCalledWith(
    expect.objectContaining({ customer_id: 42, status: 'ready' })
  )
  expect(request.mock.calls[0][1]).toContain(
    '/api/user/employee/export/options?field=customer'
  )
  await user.click(screen.getByRole('button', { name: 'Clear' }))
  await user.click(screen.getByRole('button', { name: 'Search' }))
  expect(onApply).toHaveBeenLastCalledWith(
    expect.objectContaining({ customer_id: undefined, status: 'ready' })
  )
  await user.click(screen.getByRole('button', { name: 'Reset' }))
  await waitFor(() => expect(customer).toHaveValue(''))
  expect(onApply).toHaveBeenLastCalledWith({})
  client.clear()
})

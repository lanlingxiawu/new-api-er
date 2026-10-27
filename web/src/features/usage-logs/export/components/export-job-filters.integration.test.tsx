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
  expect(screen.queryByLabelText('Creator username')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'More filters' }))
  expect(screen.queryByLabelText('Creator ID')).not.toBeInTheDocument()
  expect(screen.getByLabelText('Export scope')).toBeInTheDocument()
  const customer = screen.getByRole('combobox', { name: 'Customer' })
  await user.click(customer)
  await user.type(customer, 'Alice')
  await user.click(
    await screen.findByRole('option', { name: 'Alice Customer' })
  )
  expect(customer).toHaveValue('Alice Customer')
  await user.click(screen.getByRole('combobox', { name: 'Status' }))
  await user.click(screen.getByRole('option', { name: 'Completed' }))
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

it('merges creator account and ID into one searchable selector and clears legacy conditions', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  request.mockResolvedValue({
    items: [{ value: '42', label: 'alice · Alice' }],
    next_cursor: '',
  })
  const onApply = vi.fn()
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={client}>
      <ExportJobFilters
        value={{ creator_keyword: 'old-account' }}
        onApply={onApply}
      />
    </QueryClientProvider>
  )
  const creator = screen.getByRole('combobox', { name: 'Creator' })
  await user.click(creator)
  await user.type(creator, '42')
  await user.click(
    await screen.findByRole('option', { name: 'alice · Alice · #42' })
  )
  await user.click(screen.getByRole('button', { name: 'Search' }))
  expect(onApply).toHaveBeenLastCalledWith(
    expect.objectContaining({ creator_id: 42, creator_keyword: undefined })
  )
  expect(creator).toHaveValue('alice · Alice · #42')
  await user.click(screen.getByRole('button', { name: 'Clear' }))
  await user.click(screen.getByRole('button', { name: 'Search' }))
  expect(onApply).toHaveBeenLastCalledWith(
    expect.objectContaining({
      creator_id: undefined,
      creator_keyword: undefined,
    })
  )
  client.clear()
})

it('selects a task from loaded jobs and continues loading without applying filters', async () => {
  const client = new QueryClient()
  const user = userEvent.setup()
  const onApply = vi.fn()
  const onLoadMoreJobs = vi.fn()
  const task = 'd52b9f04-d004-4864-aa89-be77de97d8b0'
  render(
    <QueryClientProvider client={client}>
      <ExportJobFilters
        value={{}}
        onApply={onApply}
        self
        jobs={[{ job_id: task, username: 'Alice', created_at: 100 }]}
        hasMoreJobs
        onLoadMoreJobs={onLoadMoreJobs}
      />
    </QueryClientProvider>
  )
  await user.click(screen.getByRole('button', { name: 'More filters' }))
  await user.click(screen.getByRole('combobox', { name: 'Export task ID' }))
  await user.click(screen.getByRole('button', { name: 'Load more' }))
  expect(onLoadMoreJobs).toHaveBeenCalledOnce()
  await user.click(screen.getByRole('option', { name: new RegExp(task) }))
  expect(onApply).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Search' }))
  expect(onApply).toHaveBeenLastCalledWith(
    expect.objectContaining({ job_id: task })
  )
  client.clear()
})

it('resolves the selected creator outside the first candidate page after applying filters', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  request.mockImplementation((_method, url: string) =>
    Promise.resolve({
      items: url.includes('keyword=77')
        ? [{ value: '77', label: 'far-user' }]
        : [{ value: '1', label: 'first-user' }],
      next_cursor: '',
    })
  )
  render(
    <QueryClientProvider client={client}>
      <ExportJobFilters value={{ creator_id: 77 }} onApply={vi.fn()} />
    </QueryClientProvider>
  )
  await waitFor(() =>
    expect(screen.getByRole('combobox', { name: 'Creator' })).toHaveValue(
      'far-user · #77'
    )
  )
  client.clear()
})

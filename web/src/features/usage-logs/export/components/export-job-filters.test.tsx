import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { ExportJobFilters } from './export-job-filters'

vi.mock('./export-filter-select', () => ({ ExportFilterSelect: () => null }))
afterEach(cleanup)

it('submits combined filters only on search and clears all conditions on reset', async () => {
  const onApply = vi.fn()
  const user = userEvent.setup()
  const { container } = render(
    <ExportJobFilters value={{}} onApply={onApply} />
  )
  await user.click(screen.getByRole('button', { name: 'More filters' }))
  expect(screen.queryByLabelText('Creator ID')).not.toBeInTheDocument()
  expect(screen.queryByLabelText('Creator username')).not.toBeInTheDocument()
  expect(container.querySelector('select')).toBeNull()
  for (const [field, option] of [
    ['Status', 'Completed'],
    ['Export scope', 'My exports'],
    ['Export source', 'Employee export'],
    ['Export mode', 'Aggregated summary'],
  ]) {
    await user.click(screen.getByRole('combobox', { name: field }))
    await user.click(screen.getByRole('option', { name: option }))
  }
  expect(onApply).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Search' }))
  expect(onApply).toHaveBeenLastCalledWith(
    expect.objectContaining({
      status: 'ready',
      scope: 'mine',
      source: 'employee',
      mode: 'summary',
    })
  )
  await user.click(screen.getByRole('button', { name: 'Reset' }))
  expect(onApply).toHaveBeenLastCalledWith({})
  expect(screen.getByRole('combobox', { name: 'Status' })).toHaveTextContent(
    'All'
  )
})

it('submits text filters on Enter and hides cross-user filters in the self view', async () => {
  const user = userEvent.setup()
  const onApply = vi.fn()
  render(<ExportJobFilters value={{}} onApply={onApply} self />)
  expect(screen.queryByLabelText('Creator username')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'More filters' }))
  await user.type(
    screen.getByRole('combobox', { name: 'Export task ID' }),
    'a2a2{Enter}'
  )
  await user.click(screen.getByRole('button', { name: 'Search' }))
  expect(onApply).toHaveBeenLastCalledWith(
    expect.objectContaining({ job_id: 'a2a2' })
  )
})

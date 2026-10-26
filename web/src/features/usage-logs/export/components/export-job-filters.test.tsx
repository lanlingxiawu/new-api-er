import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { ExportJobFilters } from './export-job-filters'

vi.mock('./export-filter-select', () => ({ ExportFilterSelect: () => null }))
afterEach(cleanup)

it('submits combined filters only on search and clears all conditions on reset', async () => {
  const onApply = vi.fn()
  const user = userEvent.setup()
  render(<ExportJobFilters value={{}} onApply={onApply} />)
  await user.type(screen.getByLabelText('Creator username'), 'Alice')
  await user.selectOptions(screen.getByLabelText('Status'), 'ready')
  expect(onApply).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Search' }))
  expect(onApply).toHaveBeenLastCalledWith(
    expect.objectContaining({ creator_keyword: 'Alice', status: 'ready' })
  )
  await user.click(screen.getByRole('button', { name: 'Reset' }))
  expect(onApply).toHaveBeenLastCalledWith({})
  expect(screen.getByLabelText('Creator username')).toHaveValue('')
})

it('submits text filters on Enter and hides cross-user filters in the self view', async () => {
  const user = userEvent.setup()
  const onApply = vi.fn()
  render(<ExportJobFilters value={{}} onApply={onApply} self />)
  expect(screen.queryByLabelText('Creator username')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'More filters' }))
  await user.type(screen.getByLabelText('Export task ID'), 'a2a2{Enter}')
  expect(onApply).toHaveBeenLastCalledWith(
    expect.objectContaining({ job_id: 'a2a2' })
  )
})

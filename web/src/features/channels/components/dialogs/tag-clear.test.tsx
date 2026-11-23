import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { EditTagDialog } from './edit-tag-dialog'
import { TagBatchEditDialog } from './tag-batch-edit-dialog'

const edit = vi.hoisted(() => vi.fn())
vi.mock('../../api', () => ({
  editTagChannels: edit,
  getTagModels: async () => ({ success: true, data: '' }),
  getAllModels: async () => ({ success: true, data: [] }),
  getGroups: async () => ({ success: true, data: [] }),
}))
vi.mock('../channels-provider', () => ({
  useChannels: () => ({ currentTag: 'original' }),
}))
afterEach(cleanup)

it.each([
  { name: 'edit', DialogComponent: EditTagDialog },
  { name: 'batch', DialogComponent: TagBatchEditDialog },
])('sends an explicit empty tag from $name', async ({ DialogComponent }) => {
  edit.mockReset().mockResolvedValue({ success: true })
  const user = userEvent.setup()
  const onOpenChange = vi.fn()
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <DialogComponent open onOpenChange={onOpenChange} />
    </QueryClientProvider>
  )
  const input = await screen.findByDisplayValue('original')
  await waitFor(() =>
    expect(
      screen.queryByText('Loading current models...')
    ).not.toBeInTheDocument()
  )
  await user.clear(input)
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() => expect(edit).toHaveBeenCalledOnce())
  expect(edit.mock.calls[0][0]).toMatchObject({ tag: 'original', new_tag: '' })
  expect(onOpenChange).toHaveBeenCalledWith(false)
})

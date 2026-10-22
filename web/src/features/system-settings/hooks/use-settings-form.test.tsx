import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { expect, it, vi } from 'vitest'

import { useSettingsForm } from './use-settings-form'

it('preserves unsaved fields on refresh while updating untouched fields and the saved baseline', async () => {
  const submit = vi.fn().mockResolvedValue(undefined)
  function Form(props: { first: string; second: string }) {
    const { form, handleSubmit } = useSettingsForm({
      defaultValues: props,
      onSubmit: submit,
    })
    return (
      <form onSubmit={handleSubmit}>
        <input aria-label='first' {...form.register('first')} />
        <input aria-label='second' {...form.register('second')} />
        <button type='submit'>Save</button>
      </form>
    )
  }
  const view = render(<Form first='original' second='before' />)
  fireEvent.change(screen.getByLabelText('first'), {
    target: { value: 'draft' },
  })
  view.rerender(<Form first='updated remotely' second='after' />)
  expect(screen.getByLabelText('first')).toHaveValue('draft')
  expect(screen.getByLabelText('second')).toHaveValue('after')
  fireEvent.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(submit).toHaveBeenCalledWith(
      { first: 'draft', second: 'after' },
      { first: 'draft' }
    )
  )
})

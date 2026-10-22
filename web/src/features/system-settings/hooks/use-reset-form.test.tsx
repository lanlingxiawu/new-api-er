import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { useForm } from 'react-hook-form'
import { afterEach, expect, it } from 'vitest'

import { useResetForm } from './use-reset-form'

afterEach(cleanup)

function Form(props: { first: string; second: string }) {
  const form = useForm({ defaultValues: props })
  useResetForm(form, props)
  return (
    <>
      <input aria-label='first' {...form.register('first')} />
      <input aria-label='second' {...form.register('second')} />
    </>
  )
}

it('keeps unsaved fields while refreshing untouched settings', () => {
  const view = render(<Form first='original' second='before' />)
  fireEvent.change(screen.getByLabelText('first'), {
    target: { value: 'draft' },
  })
  view.rerender(<Form first='remote' second='after' />)
  expect(screen.getByLabelText('first')).toHaveValue('draft')
  expect(screen.getByLabelText('second')).toHaveValue('after')
})

it('keeps nested edits across a partial-save refresh', () => {
  function NestedForm(props: { saved: string; pending: number }) {
    const form = useForm({ defaultValues: { settings: props } })
    useResetForm(form, { settings: props })
    return (
      <>
        <input aria-label='saved' {...form.register('settings.saved')} />
        <input
          aria-label='pending'
          type='number'
          {...form.register('settings.pending', { valueAsNumber: true })}
        />
      </>
    )
  }
  const view = render(<NestedForm saved='old' pending={1} />)
  fireEvent.change(screen.getByLabelText('pending'), { target: { value: '3' } })
  view.rerender(<NestedForm saved='committed' pending={1} />)
  expect(screen.getByLabelText('saved')).toHaveValue('committed')
  expect(screen.getByLabelText('pending')).toHaveValue(3)
})

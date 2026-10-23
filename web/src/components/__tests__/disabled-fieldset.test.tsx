import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'

import { Checkbox } from '@/components/ui/checkbox'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Switch } from '@/components/ui/switch'

test.each([
  { Component: Switch, role: 'switch' },
  { Component: Checkbox, role: 'checkbox' },
])(
  '$role respects read-only fieldsets and works after edit access is restored',
  async ({ Component, role }) => {
    const change = vi.fn()
    const submit = vi.fn((event) => event.preventDefault())
    const fixture = (disabled: boolean) => (
      <form onSubmit={submit}>
        <fieldset disabled={disabled}>
          <label htmlFor='control'>Setting</label>
          <Component id='control' onCheckedChange={change} />
        </fieldset>
      </form>
    )
    const view = render(fixture(true))
    const control = screen.getByRole(role, { name: 'Setting' })
    expect(control).toBeDisabled()
    const user = userEvent.setup()
    await user.click(screen.getByText('Setting'))
    expect(change).not.toHaveBeenCalled()
    view.rerender(fixture(false))
    await user.click(screen.getByText('Setting'))
    expect(change).toHaveBeenCalledTimes(1)
    control.focus()
    await user.keyboard(' ')
    expect(change).toHaveBeenCalledTimes(2)
    expect(submit).not.toHaveBeenCalled()
  }
)

test('radio choices respect read-only fieldsets and retain keyboard navigation when editable', async () => {
  const change = vi.fn()
  const fixture = (disabled: boolean) => (
    <fieldset disabled={disabled}>
      <RadioGroup onValueChange={change} defaultValue='one'>
        <RadioGroupItem value='one' aria-label='One' />
        <RadioGroupItem value='two' aria-label='Two' />
      </RadioGroup>
    </fieldset>
  )
  const view = render(fixture(true))
  const second = screen.getByRole('radio', { name: 'Two' })
  expect(second).toBeDisabled()
  await userEvent.click(second)
  expect(change).not.toHaveBeenCalled()
  view.rerender(fixture(false))
  screen.getByRole('radio', { name: 'One' }).focus()
  await userEvent.keyboard('{ArrowRight}')
  expect(second).toBeChecked()
  expect(change).toHaveBeenCalledTimes(1)
})

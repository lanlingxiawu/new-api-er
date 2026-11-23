import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { ComboboxInput } from '../combobox-input'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

it.each([false, true])(
  'positions suggestions in the correct coordinate space (dialog=%s)',
  async (inDialog) => {
    const user = userEvent.setup()
    const change = vi.fn()
    const input = (
      <ComboboxInput
        aria-label='Model'
        options={[{ value: 'test', label: 'Test model' }]}
        onValueChange={change}
      />
    )
    render(inDialog ? <div role='dialog'>{input}</div> : input)
    const combobox = screen.getByRole('combobox')
    vi.spyOn(combobox, 'getBoundingClientRect').mockReturnValue({
      left: 40,
      top: 200,
      bottom: 232,
      width: 300,
    } as DOMRect)
    if (inDialog) {
      const dialog = screen.getByRole('dialog')
      vi.spyOn(dialog, 'getBoundingClientRect').mockReturnValue({
        left: 16,
        top: 100,
        bottom: 700,
      } as DOMRect)
      Object.defineProperty(dialog, 'scrollTop', {
        value: 20,
        configurable: true,
      })
    }
    await user.click(combobox)
    const popup = (await screen.findByRole('listbox')).parentElement!
    expect(popup).toHaveStyle({
      position: inDialog ? 'absolute' : 'fixed',
      top: inDialog ? '156px' : '236px',
      left: inDialog ? '24px' : '40px',
      width: '300px',
    })
    if (inDialog) expect(screen.getByRole('dialog')).toContainElement(popup)
    vi.spyOn(combobox, 'getBoundingClientRect').mockReturnValue({
      left: 50,
      top: 180,
      bottom: 212,
      width: 280,
    } as DOMRect)
    fireEvent.scroll(window)
    await waitFor(() =>
      expect(popup).toHaveStyle({
        top: inDialog ? '136px' : '216px',
        left: inDialog ? '34px' : '50px',
        width: '280px',
      })
    )
    await user.click(screen.getByRole('option', { name: 'Test model' }))
    expect(change).toHaveBeenCalledWith('test')
  }
)

it.each([
  { top: 100, bottom: 600, inputTop: 500, popupTop: 396, maxHeight: 388 },
  { top: 400, bottom: 600, inputTop: 500, popupTop: 96, maxHeight: 88 },
])('keeps suggestions inside a short dialog ($top–$bottom)', async (bounds) => {
  const change = vi.fn()
  render(
    <div role='dialog'>
      <ComboboxInput
        aria-label='Group'
        options={Array.from({ length: 20 }, (_, i) => ({
          value: `group-${i}`,
          label: `Group ${i}`,
        }))}
        onValueChange={change}
        dropdownFooter={<div>Load more</div>}
      />
    </div>
  )
  vi.spyOn(screen.getByRole('dialog'), 'getBoundingClientRect').mockReturnValue(
    {
      left: 16,
      top: bounds.top,
      bottom: bounds.bottom,
    } as DOMRect
  )
  vi.spyOn(
    screen.getByRole('combobox'),
    'getBoundingClientRect'
  ).mockReturnValue({
    left: 40,
    top: bounds.inputTop,
    bottom: bounds.inputTop + 32,
    width: 300,
  } as DOMRect)
  await userEvent.click(screen.getByRole('combobox'))
  const popup = (await screen.findByRole('listbox')).parentElement!
  expect(popup).toHaveStyle({
    top: `${bounds.popupTop}px`,
    transform: 'translateY(-100%)',
    'max-height': `${bounds.maxHeight}px`,
  })
  expect(popup).toContainElement(screen.getByText('Load more'))
  await userEvent.click(screen.getByRole('option', { name: 'Group 19' }))
  expect(change).toHaveBeenCalledWith('group-19')
})

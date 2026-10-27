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

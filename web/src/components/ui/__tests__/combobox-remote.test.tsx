/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { ComboboxInput } from '../combobox-input'

afterEach(cleanup)

it('keeps remote matches and lets the keyboard use and leave pagination controls', async () => {
  const user = userEvent.setup()
  const change = vi.fn()
  const search = vi.fn()
  const more = vi.fn()
  render(
    <ComboboxInput
      aria-label='Employee'
      options={[{ value: '42', label: 'Ada', description: 'Team North' }]}
      onValueChange={change}
      filterOptions={false}
      onSearchValueChange={search}
      dropdownFooter={<button onClick={more}>Load more</button>}
    />
  )
  await user.click(screen.getByRole('combobox'))
  await user.type(screen.getByRole('combobox'), 'North')
  expect(search).toHaveBeenLastCalledWith('North')
  expect(screen.getByRole('option', { name: /Ada\s*Team North/ })).toBeVisible()
  await user.tab()
  expect(screen.getByRole('button', { name: 'Load more' })).toHaveFocus()
  expect(screen.getByRole('listbox')).toBeVisible()
  await user.keyboard('[Enter]')
  expect(more).toHaveBeenCalledOnce()
  expect(change).not.toHaveBeenCalled()
  await user.keyboard('[Escape]')
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  expect(screen.getByRole('combobox')).toHaveFocus()
})

it('closes the popup when keyboard focus leaves its pagination footer', async () => {
  const user = userEvent.setup()
  render(
    <ComboboxInput
      aria-label='Employee'
      options={[]}
      onValueChange={vi.fn()}
      dropdownFooter={<button>Retry</button>}
    />
  )
  await user.click(screen.getByRole('combobox'))
  await user.tab()
  expect(screen.getByRole('button', { name: 'Retry' })).toHaveFocus()
  await user.tab()
  expect(
    screen.queryByRole('button', { name: 'Retry' })
  ).not.toBeInTheDocument()
  expect(screen.getByRole('combobox')).toHaveAttribute('aria-expanded', 'false')
})

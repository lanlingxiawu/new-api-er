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
import { useState } from 'react'
import { afterEach, expect, it } from 'vitest'

import { Checkbox } from '../checkbox'

afterEach(cleanup)

function Selection() {
  const [selected, setSelected] = useState(false)
  return (
    <>
      <label>
        <Checkbox checked={selected} onCheckedChange={setSelected} />
        Select record
      </label>
      <Checkbox
        aria-label='Select page'
        indeterminate={!selected}
        checked={selected}
        onCheckedChange={setSelected}
      />
      <Checkbox aria-label='Unavailable' disabled />
    </>
  )
}

it('supports mixed selection, labels and keyboard selection without enabling disabled records', async () => {
  const user = userEvent.setup()
  render(<Selection />)
  expect(screen.getByRole('checkbox', { name: 'Select page' })).toHaveAttribute(
    'aria-checked',
    'mixed'
  )
  await user.tab()
  expect(screen.getByRole('checkbox', { name: 'Select record' })).toHaveFocus()
  await user.keyboard('[Space]')
  expect(screen.getByRole('checkbox', { name: 'Select record' })).toBeChecked()
  expect(screen.getByRole('checkbox', { name: 'Select page' })).toBeChecked()
  await user.click(screen.getByText('Select record'))
  expect(
    screen.getByRole('checkbox', { name: 'Select record' })
  ).not.toBeChecked()
  expect(screen.getByRole('checkbox', { name: 'Select page' })).toHaveAttribute(
    'aria-checked',
    'mixed'
  )
  await user.click(screen.getByRole('checkbox', { name: 'Unavailable' }))
  expect(
    screen.getByRole('checkbox', { name: 'Unavailable' })
  ).not.toBeChecked()
})

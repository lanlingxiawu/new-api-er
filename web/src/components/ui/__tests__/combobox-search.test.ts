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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { comboboxInitialSearch } from '../combobox-search'

describe('comboboxInitialSearch', () => {
  // A pure picker's value is an option key, often an id. Seeding the search with it
  // filters the list down to the current item and shows the raw id in the input —
  // exactly the "pick again after selecting" problem on the upstream-log channel
  // selector (and on the channel-type selector in the channel drawer).
  test('a pure picker opens with an empty search so every option is visible', () => {
    assert.equal(comboboxInitialSearch('883929262', false), '')
  })

  // A free-text field's value IS the text the user typed; reopening must keep it so
  // it can be edited.
  test('a free-text field keeps its current text when reopened', () => {
    assert.equal(comboboxInitialSearch('My Claude', true), 'My Claude')
  })

  test('an empty value stays empty in both modes', () => {
    assert.equal(comboboxInitialSearch('', false), '')
    assert.equal(comboboxInitialSearch('', true), '')
  })
})

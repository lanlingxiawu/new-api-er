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
import {
  getCoreRowModel,
  getPaginationRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it } from 'vitest'

import { DataTablePagination } from '../pagination'

const rows = [{ id: 1 }, { id: 2 }, { id: 3 }]
const emptyRows: { id: number }[] = []

function Fixture(props: {
  empty?: boolean
  compact?: boolean
  cursor?: {
    hasNextPage: boolean
    isFetching?: boolean
    allowPageSizeChange?: boolean
    numberedPages?: boolean
  }
}) {
  const table = useReactTable({
    data: props.empty ? emptyRows : rows,
    columns: [{ accessorKey: 'id' }],
    getCoreRowModel: getCoreRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    initialState: { pagination: { pageIndex: 0, pageSize: 2 } },
    manualPagination: !!props.cursor,
    pageCount: props.cursor ? -1 : undefined,
  })
  return (
    <DataTablePagination
      table={table}
      compact={props.compact}
      cursor={props.cursor}
    />
  )
}
it('moves between pages in compact mode and disables the boundary actions', async () => {
  const user = userEvent.setup()
  render(<Fixture compact />)
  expect(screen.getByText('1 / 2')).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Go to previous page' })
  ).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Go to next page' }))
  expect(screen.getByText('2 / 2')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Go to next page' })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Go to previous page' }))
  expect(screen.getByText('1 / 2')).toBeVisible()
})
it('shows a valid empty page with navigation disabled', () => {
  render(<Fixture empty compact />)
  expect(screen.getByText('1 / 1')).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Go to previous page' })
  ).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Go to next page' })).toBeDisabled()
})
it('keeps page size selection available in the default layout', () => {
  render(<Fixture />)
  expect(screen.getByRole('combobox')).toBeVisible()
})

it('reuses page size and numbered controls for opted-in cursor callers without claiming a total', async () => {
  const result = render(
    <Fixture
      cursor={{
        hasNextPage: true,
        allowPageSizeChange: true,
        numberedPages: true,
      }}
    />
  )
  await userEvent.click(screen.getByRole('combobox', { name: 'Rows per page' }))
  await userEvent.click(screen.getByRole('option', { name: '100' }))
  expect(
    screen.getByRole('combobox', { name: 'Rows per page' })
  ).toHaveTextContent('100')
  expect(screen.queryByText('Total:')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Go to last page' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: /^Go to page 3/ })
  ).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /^Go to page 2/ }))
  expect(screen.getByRole('button', { name: /^Go to page 2/ })).toHaveAttribute(
    'aria-current',
    'page'
  )
  result.rerender(
    <Fixture
      cursor={{
        hasNextPage: true,
        isFetching: true,
        allowPageSizeChange: true,
        numberedPages: true,
      }}
    />
  )
  expect(screen.getByRole('combobox', { name: 'Rows per page' })).toBeDisabled()
  expect(screen.getByRole('button', { name: /^Go to page 1/ })).toBeDisabled()
})

it.each([false, true])(
  'shows cursor pages without inventing totals and respects loading and end boundaries (compact=%s)',
  async (compact) => {
    const result = render(
      <Fixture compact={compact} cursor={{ hasNextPage: true }} />
    )
    expect(screen.getByRole('navigation', { name: 'Page' })).toBeVisible()
    expect(screen.getByText('Page 1')).toBeVisible()
    expect(screen.getByText(/Rows on this page:/)).toBeVisible()
    expect(screen.queryByText('Total:')).not.toBeInTheDocument()
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Go to last page' })
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Go to previous page' })
    ).toBeDisabled()
    await userEvent.click(
      screen.getByRole('button', { name: 'Go to next page' })
    )
    expect(screen.getByText('Page 2')).toBeVisible()
    result.rerender(
      <Fixture
        compact={compact}
        cursor={{ hasNextPage: true, isFetching: true }}
      />
    )
    expect(
      screen.getByRole('button', { name: 'Go to next page' })
    ).toBeDisabled()
    expect(
      screen.getByRole('button', { name: 'Go to previous page' })
    ).toBeDisabled()
    result.rerender(
      <Fixture compact={compact} cursor={{ hasNextPage: false }} />
    )
    expect(
      screen.getByRole('button', { name: 'Go to next page' })
    ).toBeDisabled()
    await userEvent.click(
      screen.getByRole('button', { name: 'Go to previous page' })
    )
    expect(screen.getByText('Page 1')).toBeVisible()
  }
)

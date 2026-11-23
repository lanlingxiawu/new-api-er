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
  flexRender,
  getCoreRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it } from 'vitest'

import type { AuditActivity } from '../api'
import { useAuditActivityColumns } from '../components/audit-activity-columns'
import { AuditActivityDetailsDialog } from '../components/audit-activity-details-dialog'

afterEach(cleanup)

it.each([
  ['channel.update', 'Update Channel'],
  ['option.update', 'Update system settings'],
  ['log_export.job_create', 'Start export'],
  ['access_token.request', 'Request with access token'],
  ['channel.veridrop_detect', 'Start channel detection'],
  ['channel.veridrop_detect_batch', 'Start detection for selected channels'],
  ['channel.veridrop_detect_enabled', 'Start detection for enabled channels'],
])(
  'shows the name of %s without restoring private metadata',
  async (action, label) => {
    const activity = {
      event_id: 'named-event',
      created_at: 1788600600,
      category: 'operation',
      action,
      success: true,
      ip: '192.0.2.1',
      client: 'Chrome · Windows',
      source: 'session',
      request_id: 'support-request',
      content: 'private-content',
      route: '/private/route',
      other: { op: { params: { key: 'private-key', name: 'private-target' } } },
    } satisfies AuditActivity & Record<string, unknown>
    render(<AuditActivityDetailsDialog entry={activity} />)
    await userEvent.click(screen.getByRole('button', { name: 'Details' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByText(label)).toBeVisible()
    expect(dialog).not.toHaveTextContent(
      /private-|\/private\/route|Operation audit/
    )
  }
)

it('shows a compact activity without rendering raw metadata or configuration values', async () => {
  const activity = {
    event_id: 'event',
    created_at: 1788600600,
    category: 'operation',
    action: 'token.update',
    success: true,
    ip: '192.0.2.1',
    client: 'Edge · Windows',
    source: 'session',
    request_id: 'support-request',
    target: { resource: 'token', id: '86', name: 'My key' },
    change_summary: {
      fields: ['name', 'status', 'group'],
      total: 7,
      outcome: 'committed',
    },
    other: {
      changes: { targets: [{ name: 'secret-config' }] },
      op: { params: { subject_ref: 'account:private' } },
    },
    route: '/internal/route',
    user_agent: 'secret-user-agent',
    content: 'secret-content',
  } satisfies AuditActivity & Record<string, unknown>
  render(<AuditActivityDetailsDialog entry={activity} />)
  await userEvent.click(screen.getByRole('button', { name: 'Details' }))
  const dialog = within(screen.getByRole('dialog'))
  expect(dialog.getByText('My key')).toBeVisible()
  expect(dialog.getByText('Edge · Windows')).toBeVisible()
  expect(dialog.getByText('support-request')).toBeVisible()
  expect(dialog.queryByRole('table')).not.toBeInTheDocument()
  expect(screen.getByRole('dialog')).not.toHaveTextContent(
    /secret-|account:private|\/internal\/route|HTTP|Actor identity/
  )
})

function ActivityTable({ entry }: { entry: AuditActivity }) {
  const columns = useAuditActivityColumns()
  const table = useReactTable({
    columns,
    data: [entry],
    getCoreRowModel: getCoreRowModel(),
  })
  return (
    <table>
      <tbody>
        {table.getRowModel().rows.map((row) => (
          <tr key={row.id}>
            {row.getVisibleCells().map((cell) => (
              <td key={cell.id}>
                {flexRender(cell.column.columnDef.cell, cell.getContext())}
              </td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  )
}

it.each(['My key', ''])(
  'identifies a target by ID even when its name is %j',
  (name) => {
    render(
      <ActivityTable
        entry={{
          event_id: 'named-target',
          created_at: 1788600600,
          category: 'security',
          action: 'token.update',
          success: true,
          ip: '',
          client: '',
          source: 'session',
          request_id: '',
          target: { resource: 'token', id: '86', name },
          change_summary: {
            fields: ['name', 'status', 'group'],
            total: 4,
            outcome: 'committed',
          },
        }}
      />
    )
    expect(screen.getByText('(ID: 86)')).toBeVisible()
    expect(
      screen.getByText('Changed 4 fields: Name, Status, Group…')
    ).toBeVisible()
  }
)

it.each([
  ['not_committed', 'No changes were saved'],
  ['unchanged', 'No changes'],
  ['unavailable', 'Field change details were not recorded'],
  ['future_outcome', 'Field change details were not recorded'],
])(
  'does not claim fields changed when the receipt is %s',
  async (outcome, message) => {
    const entry: AuditActivity = {
      event_id: 'failed-update',
      created_at: 1788600600,
      category: 'security',
      action: 'token.update',
      success: false,
      ip: '',
      client: '',
      source: 'session',
      request_id: '',
      change_summary: { fields: ['name'], total: 1, outcome },
    }
    render(<ActivityTable entry={entry} />)
    expect(screen.getByText(message)).toBeVisible()
    expect(screen.queryByText(/Changed fields:/)).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Details' }))
    expect(within(screen.getByRole('dialog')).getByText(message)).toBeVisible()
    expect(screen.getByRole('dialog')).not.toHaveTextContent('Changed fields:')
  }
)

it('shows a partial result with only public field names in the list and details', async () => {
  const entry: AuditActivity = {
    event_id: 'partial',
    created_at: 1788600600,
    category: 'security',
    action: 'access_token.update',
    success: false,
    ip: '',
    client: '',
    source: 'session',
    request_id: '',
    change_summary: {
      fields: ['name', 'private-field'],
      total: 1,
      outcome: 'partial',
    },
  }
  render(<ActivityTable entry={entry} />)
  const summary =
    'Changed fields: Name · Some changes were saved before the operation failed.'
  expect(screen.getByText(summary)).toBeVisible()
  expect(screen.queryByText(/private-field/)).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Details' }))
  expect(within(screen.getByRole('dialog')).getByText(summary)).toBeVisible()
})

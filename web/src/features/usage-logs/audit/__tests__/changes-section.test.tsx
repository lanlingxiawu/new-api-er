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
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, expect, it } from 'vitest'

import zh from '@/i18n/locales/zh.json'

import { AuditChangesSection } from '../components/audit-changes-section'

afterEach(cleanup)

async function renderChanges(
  outcome: string,
  fields: unknown[],
  truncated = false,
  action = 'channel.update'
) {
  const i18n = createInstance()
  await i18n.init({ lng: 'zh', resources: { zh } })
  const view = render(
    <I18nextProvider i18n={i18n}>
      <AuditChangesSection
        entry={{
          action,
          success: outcome !== 'partial',
          other: {
            changes: {
              version: 1,
              outcome,
              truncated,
              targets: fields.length
                ? [
                    {
                      resource: 'channel',
                      id: '42',
                      name: 'Test channel',
                      consistency: 'observed',
                      fields,
                    },
                  ]
                : [],
            },
          },
        }}
      />
    </I18nextProvider>
  )
  return { ...view, i18n }
}

it('distinguishes zero, false, null and empty strings in old and new values', async () => {
  const { i18n } = await renderChanges('committed', [
    {
      path: ['priority'],
      old: { state: 'value', value: 9 },
      new: { state: 'value', value: 0 },
    },
    {
      path: ['auto_ban'],
      old: { state: 'value', value: true },
      new: { state: 'value', value: false },
    },
    {
      path: ['name'],
      old: { state: 'value', value: null },
      new: { state: 'value', value: '' },
    },
  ])
  for (const label of ['0', '否', '空值（null）', i18n.t('Empty')]) {
    expect(screen.getByText(label)).toBeInTheDocument()
  }
  const heading = screen.getByText(i18n.t('Field')).parentElement
  expect(heading).toHaveTextContent(i18n.t('Before'))
  expect(heading).toHaveTextContent(i18n.t('After'))
  expect(
    screen.getByText(
      i18n.t('Earlier values may be affected by concurrent edits.')
    )
  ).toBeInTheDocument()
})

it('folds creation defaults without dropping recorded zero or false values', async () => {
  const { i18n } = await renderChanges(
    'committed',
    [
      {
        path: ['name'],
        old: { state: 'absent' },
        new: { state: 'value', value: 'New channel' },
      },
      {
        path: ['priority'],
        old: { state: 'absent' },
        new: { state: 'value', value: 0 },
      },
      {
        path: ['auto_ban'],
        old: { state: 'absent' },
        new: { state: 'value', value: false },
      },
    ],
    false,
    'channel.create'
  )
  expect(screen.getByText('New channel')).toBeVisible()
  expect(screen.queryByText(i18n.t('Before'))).not.toBeInTheDocument()
  expect(screen.queryByText('0')).not.toBeInTheDocument()
  await userEvent.click(
    screen.getByRole('button', {
      name: i18n.t('Other fields ({{count}})', { count: 2 }),
    })
  )
  expect(screen.getByText('0')).toBeVisible()
  expect(screen.getByText(i18n.t('No'))).toBeVisible()
  expect(screen.queryByText(i18n.t('Not set'))).not.toBeInTheDocument()
})

it('never renders or copies credential values even in malformed input', async () => {
  const { container } = await renderChanges(
    'partial',
    [
      {
        path: ['key'],
        old: { state: 'value', value: 'old-secret' },
        new: { state: 'value', value: 'new-secret' },
      },
      {
        path: ['SMTPPassword'],
        old: { state: 'value', value: 'smtp-secret' },
        new: { state: 'redacted', value: 'hidden-secret' },
      },
    ],
    true
  )
  expect(screen.getAllByText('敏感值已隐藏')).toHaveLength(4)
  expect(container.textContent).not.toContain('secret')
  expect(screen.queryByRole('button')).not.toBeInTheDocument()
  expect(screen.getByText(/部分变更已保存/)).toBeInTheDocument()
  expect(screen.getByText(/仅保留部分变更详情/)).toBeInTheDocument()
})

it.each([
  ['unchanged', zh.translation['No changes']],
  ['not_committed', '未保存任何变更'],
  ['unavailable', zh.translation['Field change details were not recorded']],
])('explains %s without inventing values', async (outcome, label) => {
  await renderChanges(outcome, [])
  expect(screen.getByText(label)).toBeInTheDocument()
})

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { SettingsPageProvider } from '../../components/settings-page-context'
import type { RelayErrorRule, SystemTuningSettings } from '../../types'
import { SystemTuningSettings as SystemTuningSettingsPage } from '../index'
import {
  emptyRuleRow,
  parseRows,
  toRow,
  toRule,
  validateRuleRow,
} from '../relay-error-display-rules'
import { RelayErrorDisplaySection } from '../relay-error-display-section'

vi.mock('@tanstack/react-router', async (importOriginal) => ({
  ...(await importOriginal()),
  useParams: () => ({ section: 'relay-error-display' }),
}))

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.setUser(null)
})

describe('request group rules', () => {
  it('preserves exact and historical scopes when editing or reordering; old rules apply to all groups', () => {
    const rules: RelayErrorRule[] = [
      { source: 'any', action: 'keep' },
      {
        source: 'upstream',
        action: 'replace',
        message: 'Busy',
        groups: ['vip', ' VIP ', 'deleted'],
      },
    ]
    const rows = parseRows(JSON.stringify(rules))
    expect(rows[0].groups).toEqual([])
    expect(rows.map(toRule)).toEqual(rules)
    expect([...rows].reverse().map(toRule)).toEqual([...rules].reverse())
    expect(toRule({ ...rows[1], groups: ['vip', 'vip'] }).groups).toEqual([
      'vip',
    ])
  })

  it('validates group count, Unicode length, empty names and unresolved auto', () => {
    const row = { ...emptyRuleRow(0), action: 'keep' as const }
    for (const groups of [
      [''],
      [' '],
      ['auto'],
      ['组'.repeat(129)],
      Array.from({ length: 101 }, (_, i) => `g${i}`),
    ]) {
      expect(validateRuleRow({ ...row, groups }, (s) => s).groups).toBeTruthy()
    }
    for (const groups of [
      [],
      ['VIP', ' vip ', '*'],
      ['组'.repeat(128)],
      Array.from({ length: 100 }, (_, i) => `g${i}`),
    ]) {
      expect(
        validateRuleRow({ ...row, groups }, (s) => s).groups
      ).toBeUndefined()
    }
    expect(toRow({ source: 'any', action: 'keep' }, 0).groups).toEqual([])
  })
})

const rules: RelayErrorRule[] = [
  {
    name: 'Quota',
    source: 'any',
    action: 'replace',
    message: 'Busy',
    groups: ['archived'],
  },
]
const settings = {
  'relay_error_display_setting.enabled': true,
  'relay_error_display_setting.hide_upstream_errors': false,
  'relay_error_display_setting.default_message': '',
  'relay_error_display_setting.rules': JSON.stringify(rules),
} as SystemTuningSettings

function workspace(initial = settings, canEdit = true, groupsAvailable = true) {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    const groupRequest = String(url).includes('/api/group/')
    if (groupRequest && !groupsAvailable) throw new Error('Network error')
    return {
      data: {
        success: true,
        data: groupRequest ? ['default', 'vip', 'auto'] : { skipped: [] },
      },
    }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const content = (value: SystemTuningSettings) => (
    <QueryClientProvider client={client}>
      <SettingsPageProvider
        actionsContainer={null}
        scope='system-tuning.relay-error-display'
        canEdit={canEdit}
      >
        <RelayErrorDisplaySection settings={value} />
      </SettingsPageProvider>
    </QueryClientProvider>
  )
  const view = render(content(initial))
  return {
    client,
    get,
    rerender: (value: SystemTuningSettings) => view.rerender(content(value)),
  }
}

it('keeps historical selections, saves groups and preserves unsaved edits across a settings refresh', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  const view = workspace()
  await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
  const dialog = await screen.findByRole('dialog')
  const groups = within(dialog).getByRole('combobox', {
    name: 'Applies to groups',
  })
  await userEvent.click(groups)
  await userEvent.click(await screen.findByRole('option', { name: 'vip' }))
  await userEvent.keyboard('{Escape}')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Confirm' }))
  view.rerender({ ...settings, 'relay_error_display_setting.rules': '[]' })
  expect(screen.getByText('Quota')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(put).toHaveBeenCalled())
  const sent = put.mock.calls[0][1] as { values: { rules: string } }
  expect(JSON.parse(sent.values.rules)[0].groups).toEqual(['archived', 'vip'])
  view.client.clear()
})

it('previews the chosen group and discards results after any sample field changes', async () => {
  let resolve!: (value: unknown) => void
  const post = vi.spyOn(api, 'post').mockImplementation(
    () =>
      new Promise((done) => {
        resolve = done
      })
  )
  const { client } = workspace()
  await userEvent.click(screen.getByRole('button', { name: /Test an error/ }))
  fireEvent.change(screen.getByLabelText('Error message'), {
    target: { value: 'quota' },
  })
  fireEvent.change(screen.getByRole('combobox', { name: 'Request group' }), {
    target: { value: 'vip' },
  })
  await userEvent.click(screen.getByRole('button', { name: 'Test' }))
  expect(post.mock.calls[0][1]).toMatchObject({
    sample: { group: 'vip', message: 'quota' },
  })
  fireEvent.change(screen.getByLabelText('Error code'), {
    target: { value: 'changed' },
  })
  await act(async () =>
    resolve({
      data: {
        success: true,
        data: {
          replace: true,
          message: 'Old result',
          status_code: 503,
          rule_index: 0,
          rule_name: 'Quota',
        },
      },
    })
  )
  expect(screen.queryByText('Old result')).not.toBeInTheDocument()
  post.mockResolvedValue({
    data: {
      success: true,
      data: {
        replace: true,
        message: 'Current result',
        status_code: 503,
        rule_index: 0,
        rule_name: 'Quota',
      },
    },
  })
  await userEvent.click(await screen.findByRole('button', { name: 'Test' }))
  expect(await screen.findByText('Current result')).toBeInTheDocument()
  fireEvent.change(screen.getByRole('combobox', { name: 'Request group' }), {
    target: { value: 'default' },
  })
  expect(screen.queryByText('Current result')).not.toBeInTheDocument()
  client.clear()
})

it('disables editing for viewers while showing scopes', async () => {
  const { client } = workspace(settings, false)
  expect(screen.getByText('archived')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Edit' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Add rule' })).toBeDisabled()
  expect(screen.getByRole('switch')).toBeDisabled()
  client.clear()
})

it('retains selections when groups fail to load and permits custom names and retry', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  const { client, get } = workspace(settings, true, false)
  await screen.findByText(
    'Could not load groups. Existing selections are kept; enter a group name or retry.'
  )
  await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
  const dialog = await screen.findByRole('dialog')
  const input = within(dialog).getByRole('combobox', {
    name: 'Applies to groups',
  })
  expect(within(dialog).getByRole('toolbar')).toHaveTextContent('archived')
  await userEvent.type(input, 'custom-group{Enter}')
  expect(within(dialog).getByRole('toolbar')).toHaveTextContent('archived')
  expect(within(dialog).getByRole('toolbar')).toHaveTextContent('custom-group')
  get.mockImplementation(async (url) => ({
    data: {
      success: true,
      data: String(url).includes('/api/group/') ? ['vip'] : { skipped: [] },
    },
  }))
  await userEvent.click(within(dialog).getByRole('button', { name: 'Retry' }))
  await waitFor(() =>
    expect(
      within(dialog).queryByRole('button', { name: 'Retry' })
    ).not.toBeInTheDocument()
  )
  expect(within(dialog).getByRole('toolbar')).toHaveTextContent('archived')
  expect(within(dialog).getByRole('toolbar')).toHaveTextContent('custom-group')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Confirm' }))
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(put).toHaveBeenCalled())
  const sent = put.mock.calls[0][1] as { values: { rules: string } }
  expect(JSON.parse(sent.values.rules)[0].groups).toEqual([
    'archived',
    'custom-group',
  ])
  client.clear()
})

it('keeps changes made during a pending save and lets a failed save be retried', async () => {
  let resolve!: (value: unknown) => void
  const put = vi.spyOn(api, 'put').mockImplementation(
    () =>
      new Promise((done) => {
        resolve = done
      })
  )
  const view = workspace()
  await userEvent.click(screen.getByRole('switch'))
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  await userEvent.click(screen.getByRole('switch'))
  view.rerender({ ...settings, 'relay_error_display_setting.enabled': false })
  await act(async () => resolve({ data: { success: true } }))
  expect(screen.getByRole('switch')).toBeChecked()
  expect(screen.getByText('Unsaved changes')).toBeInTheDocument()
  put.mockResolvedValueOnce({
    data: { success: false, message: 'Please retry' },
  })
  await userEvent.click(await screen.findByRole('button', { name: 'Save' }))
  await screen.findByRole('button', { name: 'Save' })
  expect(screen.getByText('Unsaved changes')).toBeInTheDocument()
  put.mockResolvedValueOnce({ data: { success: true } })
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(screen.queryByText('Unsaved changes')).not.toBeInTheDocument()
  )
  expect(put).toHaveBeenCalledTimes(3)
  view.client.clear()
})

it('applies a settings refresh deferred by an open rule dialog', async () => {
  const view = workspace()
  await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
  await screen.findByRole('dialog')
  view.rerender({
    ...settings,
    'relay_error_display_setting.rules': JSON.stringify([
      { name: 'Server refresh', source: 'any', action: 'keep' },
    ]),
  })
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(await screen.findByText('Server refresh')).toBeInTheDocument()
  expect(screen.queryByText('Quota')).not.toBeInTheDocument()
  view.client.clear()
})

it('closes the rule dialog on a second Escape without clearing group selections', async () => {
  const view = workspace()
  await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
  const dialog = await screen.findByRole('dialog')
  await userEvent.click(
    within(dialog).getByRole('combobox', { name: 'Applies to groups' })
  )
  await userEvent.click(await screen.findByRole('option', { name: 'vip' }))
  await userEvent.keyboard('{Escape}')
  expect(screen.getByRole('dialog')).toBeInTheDocument()
  expect(screen.queryByRole('option', { name: 'vip' })).not.toBeInTheDocument()
  expect(within(dialog).getByRole('toolbar')).toHaveTextContent('vip')
  await userEvent.keyboard('{Escape}')
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  view.client.clear()
})

it('keeps preview interactive for viewers on the real settings page', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'viewer', role: 10 })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    const request = String(url)
    if (request === '/api/option/') {
      return {
        data: {
          success: true,
          data: Object.entries(settings).map(([key, value]) => ({
            key,
            value: String(value),
          })),
        },
      }
    }
    return {
      data: {
        success: true,
        data: request.includes('/api/group/')
          ? ['default', 'vip']
          : { skipped: [] },
      },
    }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SystemTuningSettingsPage />
    </QueryClientProvider>
  )
  const toggle = await screen.findByRole('button', { name: /Test an error/ })
  expect(toggle).toBeEnabled()
  await userEvent.click(toggle)
  expect(screen.getByRole('combobox', { name: 'Request group' })).toBeEnabled()
  fireEvent.change(screen.getByLabelText('Error message'), {
    target: { value: 'quota' },
  })
  expect(screen.getByRole('button', { name: 'Test' })).toBeEnabled()
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Edit' })).toBeDisabled()
  client.clear()
})

it('does not replace a clean saved draft with a settings refresh received during save', async () => {
  let resolve!: (value: unknown) => void
  vi.spyOn(api, 'put').mockImplementation(
    () =>
      new Promise((done) => {
        resolve = done
      })
  )
  const view = workspace()
  await userEvent.click(screen.getByRole('switch'))
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  view.rerender({ ...settings })
  await act(async () => resolve({ data: { success: true } }))
  expect(screen.getByRole('switch')).not.toBeChecked()
  expect(screen.queryByText('Unsaved changes')).not.toBeInTheDocument()
  view.client.clear()
})

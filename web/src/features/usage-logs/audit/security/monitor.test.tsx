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
import { toast } from 'sonner'
import { afterEach, expect, it, vi } from 'vitest'

import { SectionPageLayout } from '@/components/layout'
import { hasPermission } from '@/lib/admin-permissions'

import { AlertConfirmButton } from './alert-confirm-button'
import { AlertStatusCell } from './alert-status-cell'
import {
  getMonitorSettings,
  saveAlert,
  deleteAlert,
  cleanupAlerts,
  exportAlerts,
  saveMonitorSettings,
  securityGet,
  type SecurityAlert,
} from './api'
import {
  SecurityAlertDialog,
  SecurityMonitor as SecurityMonitorContent,
} from './monitor'

function SecurityMonitor() {
  return (
    <SectionPageLayout fixedContent>
      <SectionPageLayout.Title>Audit Logs</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <SecurityMonitorContent />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

const requestVerification = vi.hoisted(() => vi.fn())
const authFixture = vi.hoisted(() => ({
  auth: { user: { id: 1, role: 100 }, session: { sid: 'review-session' } },
}))

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, values?: Record<string, unknown>) =>
      key.replaceAll(/\{\{(\w+)\}\}/g, (placeholder, name: string) =>
        values?.[name] === undefined ? placeholder : String(values[name])
      ),
    i18n: { language: 'en' },
  }),
}))
vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('@/stores/auth-store', () => ({
  useAuthStore: Object.assign(
    (selector: (state: unknown) => unknown) => selector(authFixture),
    { getState: () => authFixture }
  ),
}))
vi.mock('@/lib/admin-permissions', () => ({ hasPermission: vi.fn(() => true) }))
vi.mock('@/features/auth/secure-verification', () => ({
  useSecureVerification: () => ({
    isActive: false,
    dialogProps: {},
    requestVerification,
  }),
  SecureVerificationDialog: () => null,
}))
vi.mock('./api', () => ({
  securityGet: vi.fn(),
  saveAlert: vi.fn(),
  getMonitorSettings: vi.fn(),
  saveMonitorSettings: vi.fn(),
  archiveAlert: vi.fn(),
  exportAlerts: vi.fn(),
  deleteAlert: vi.fn(),
  cleanupAlerts: vi.fn(),
}))

afterEach(() => {
  cleanup()
  vi.resetAllMocks()
  authFixture.auth.user = { id: 1, role: 100 }
  authFixture.auth.session = { sid: 'review-session' }
})

it.each(['health', 'alerts', 'both'] as const)(
  'shows one retry notice when %s fails without presenting failure as an empty result',
  async (failed) => {
    const healthy = {
      enabled: true,
      ready: true,
      dropped: 0,
      last_scan: Math.floor(Date.now() / 1000),
    }
    let recovered = false
    vi.mocked(securityGet).mockImplementation(async (path) => {
      if (!recovered && (failed === 'both' || failed === path)) {
        throw new Error('Unavailable')
      }
      return path === 'health' ? healthy : { items: [alert], next_cursor: 0 }
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <SecurityMonitor />
      </QueryClientProvider>
    )
    const message =
      'Unable to load security monitoring. Check settings and retry.'
    await screen.findByText(message)
    expect(screen.getAllByText(message)).toHaveLength(1)
    expect(screen.getAllByRole('button', { name: 'Retry' })).toHaveLength(1)
    expect(
      screen.queryByText('No alerts match these filters')
    ).not.toBeInTheDocument()
    if (failed !== 'health') {
      expect(screen.queryByRole('table')).not.toBeInTheDocument()
    } else {
      expect(await screen.findByRole('table')).toBeVisible()
    }
    if (failed === 'alerts') {
      expect(await screen.findByText('Monitoring active')).toBeVisible()
    }
    const callsBeforeRetry = vi
      .mocked(securityGet)
      .mock.calls.map(([path]) => path)
    recovered = true
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() =>
      expect(screen.queryByText(message)).not.toBeInTheDocument()
    )
    expect(await screen.findByRole('table')).toBeVisible()
    const retried = vi
      .mocked(securityGet)
      .mock.calls.slice(callsBeforeRetry.length)
      .map(([path]) => path)
    expect(retried.sort()).toEqual(
      failed === 'both' ? ['alerts', 'health'] : [failed]
    )
    client.clear()
  }
)

it.each([
  ['login_failures', 'Attempted sign-in account'],
  ['recovery_requests', 'Password recovery account'],
  ['ip_abuse', 'Submitted account'],
])(
  'labels the submitted account for %s separately from the operator',
  async (rule, label) => {
    const attempted = {
      ...alert,
      rule,
      actor_id: 0,
      attempted_account: '<script>attempted-user</script>',
      target_user_id: 31,
      target_role: 1,
      target_username: 'target-customer',
    }
    vi.mocked(securityGet).mockImplementation(async (path) => {
      if (path === 'health') {
        return { enabled: true, ready: true, dropped: 0, last_scan: 100 }
      }
      if (path === 'alerts/7') {
        return { alert: attempted, events: [], actions: [], deliveries: [] }
      }
      return { items: [attempted], next_cursor: 0 }
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <SecurityMonitor />
      </QueryClientProvider>
    )
    expect(await screen.findByText(attempted.attempted_account)).toBeVisible()
    expect(screen.getByText('Unidentified account')).toBeVisible()
    expect(screen.getByText('target-customer (#31)')).toBeVisible()
    expect(screen.getByText(new RegExp(label))).toBeVisible()
    expect(document.querySelector('script')).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: /#7$/ }))
    const dialog = await screen.findByRole('dialog', {
      name: 'Security alert #7',
    })
    expect(
      await within(dialog).findByText(attempted.attempted_account)
    ).toBeVisible()
    expect(within(dialog).getByText(label)).toBeVisible()
    expect(
      within(dialog).getAllByText('Target account').length
    ).toBeGreaterThan(0)
    expect(within(dialog).getByText('target-customer (#31)')).toBeVisible()
    expect(within(dialog).getAllByText('Unidentified account')[0]).toBeVisible()
    client.clear()
  }
)

it('shows the username and account ID in the list and the opened alert', async () => {
  const namedAlert = { ...alert, actor_username: 'audit-reviewer' }
  vi.mocked(securityGet).mockImplementation(async (path) => {
    if (path === 'health') {
      return { enabled: true, ready: true, dropped: 0, last_scan: 100 }
    }
    if (path === 'alerts/7') {
      return { alert: namedAlert, events: [], actions: [], deliveries: [] }
    }
    return { items: [namedAlert], next_cursor: 0 }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SecurityMonitor />
    </QueryClientProvider>
  )
  expect(await screen.findByText('audit-reviewer (#2)')).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: /#7$/ }))
  const dialog = await screen.findByRole('dialog', {
    name: 'Security alert #7',
  })
  expect(await within(dialog).findByText('audit-reviewer (#2)')).toBeVisible()
  client.clear()
})

it('requires confirmation before deleting the reviewed alert version', async () => {
  vi.mocked(securityGet).mockResolvedValue({
    alert,
    events: [],
    actions: [],
    deliveries: [],
  })
  vi.mocked(deleteAlert).mockResolvedValue({ queued: 1, has_more: false })
  const onClose = vi.fn()
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SecurityAlertDialog id={7} onClose={onClose} />
    </QueryClientProvider>
  )
  await userEvent.click(
    await screen.findByRole('button', { name: 'Delete alert' })
  )
  expect(deleteAlert).not.toHaveBeenCalled()
  const confirm = screen.getByRole('alertdialog', { name: 'Delete alert #7' })
  expect(
    within(confirm).getByText(/Original audit logs are retained/)
  ).toBeVisible()
  await userEvent.click(within(confirm).getByRole('button', { name: 'Delete' }))
  await waitFor(() => expect(deleteAlert).toHaveBeenCalledWith(7, 3))
  await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
  client.clear()
})

it('cleans closed alerts only after choosing a past cutoff and confirms the batch scope', async () => {
  mockAlertList()
  vi.mocked(cleanupAlerts).mockResolvedValue({ queued: 100, has_more: true })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SecurityMonitor />
    </QueryClientProvider>
  )
  await userEvent.click(
    screen.getByRole('button', { name: 'Clean up closed alerts' })
  )
  const confirm = screen.getByRole('alertdialog')
  expect(within(confirm).getByRole('button', { name: 'Delete' })).toBeDisabled()
  fireEvent.change(within(confirm).getByLabelText('Closed on or before'), {
    target: { value: '2000-01-01' },
  })
  expect(cleanupAlerts).not.toHaveBeenCalled()
  await userEvent.click(within(confirm).getByRole('button', { name: 'Delete' }))
  await waitFor(() =>
    expect(cleanupAlerts).toHaveBeenCalledWith(
      Math.floor(new Date('2000-01-01T23:59:59').getTime() / 1000)
    )
  )
  expect(
    await screen.findByText(
      'More closed alerts remain. Run cleanup again to continue.'
    )
  ).toBeVisible()
  client.clear()
})

it('pages retained investigation records without requesting details again', async () => {
  vi.mocked(securityGet).mockResolvedValue({
    alert,
    events: [],
    actions: Array.from({ length: 11 }, (_, i) => ({
      id: i + 1,
      actor_id: 2,
      action: 'investigating',
      note: `review-${i + 1}`,
    })),
    deliveries: [],
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SecurityAlertDialog id={7} onClose={vi.fn()} />
    </QueryClientProvider>
  )
  await userEvent.click(
    await screen.findByRole('tab', { name: 'Investigation history' })
  )
  expect(screen.getByText('review-1')).toBeVisible()
  expect(screen.queryByText('review-11')).not.toBeInTheDocument()
  await userEvent.click(
    within(
      screen.getByRole('tabpanel', { name: 'Investigation history' })
    ).getByRole('button', { name: 'Next page' })
  )
  expect(screen.getByText('review-11')).toBeVisible()
  await userEvent.click(screen.getByRole('tab', { name: 'Overview' }))
  await userEvent.click(
    screen.getByRole('tab', { name: 'Investigation history' })
  )
  expect(screen.getByText('review-11')).toBeVisible()
  expect(securityGet).toHaveBeenCalledOnce()
  client.clear()
})

it('reads historical alerts even when detection is disabled after startup', async () => {
  vi.mocked(securityGet).mockImplementation(async (path) =>
    path === 'health'
      ? {
          enabled: false,
          ready: false,
          dropped: 0,
          last_scan: 0,
        }
      : { items: [], next_cursor: 0 }
  )
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SecurityMonitor />
    </QueryClientProvider>
  )
  expect(await screen.findByText('Monitoring disabled')).toBeVisible()
  expect(
    vi.mocked(securityGet).mock.calls.some(([path]) => path === 'alerts')
  ).toBe(true)
  client.clear()
})

it('shows alerts and requests a new cursor when filters change', async () => {
  const user = userEvent.setup()
  vi.mocked(securityGet).mockImplementation(async (path) =>
    path === 'health'
      ? { enabled: true, ready: true, dropped: 0, last_scan: Date.now() / 1000 }
      : {
          items: [
            {
              id: 7,
              rule: 'sensitive_operation',
              severity: 2,
              status: 'open',
              actor_id: 2,
              occurrences: 1,
              last_seen: Date.now() / 1000,
            },
          ],
          next_cursor: 7,
        }
  )
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SecurityMonitor />
    </QueryClientProvider>
  )
  expect(
    await screen.findByRole('button', { name: 'Sensitive operation #7' })
  ).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Go to next page' }))
  await waitFor(() =>
    expect(securityGet).toHaveBeenCalledWith(
      'alerts',
      expect.any(AbortSignal),
      expect.objectContaining({ before: 7 })
    )
  )
  const pages = vi
    .mocked(securityGet)
    .mock.calls.filter(([path]) => path === 'alerts')
  expect(pages.at(-1)?.[2]?.start).toEqual(pages[0]?.[2]?.start)
  expect(pages.at(-1)?.[2]?.end).toEqual(pages[0]?.[2]?.end)
  expect(pages[0]?.[2]?.start).toEqual(expect.any(Number))
  await user.click(screen.getByRole('button', { name: 'Resource' }))
  await user.click(await screen.findByRole('option', { name: 'Channels' }))
  await waitFor(() =>
    expect(securityGet).toHaveBeenCalledWith(
      'alerts',
      expect.any(AbortSignal),
      expect.objectContaining({ resource: 'channel', before: 0 })
    )
  )
  expect(screen.getByRole('combobox', { name: 'Resource' })).toHaveValue(
    'Channels'
  )
  await user.click(screen.getByRole('button', { name: 'Handling status' }))
  await user.click(await screen.findByRole('option', { name: 'Confirmed' }))
  await waitFor(() =>
    expect(securityGet).toHaveBeenCalledWith(
      'alerts',
      expect.any(AbortSignal),
      expect.objectContaining({
        resource: 'channel',
        status: 'resolved',
        before: 0,
      })
    )
  )
  await user.click(screen.getByRole('button', { name: 'Resource' }))
  await user.click(await screen.findByRole('option', { name: 'Resource: All' }))
  await waitFor(() =>
    expect(securityGet).toHaveBeenCalledWith(
      'alerts',
      expect.any(AbortSignal),
      expect.objectContaining({ resource: '', status: 'resolved', before: 0 })
    )
  )
  expect(screen.getByRole('combobox', { name: 'Resource' })).toHaveValue(
    'Resource: All'
  )
  client.clear()
})

it('uses the shared footer for cursor pages and resets mobile scroll while preserving it on background refresh', async () => {
  const original = window.matchMedia.bind(window)
  const media = vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
    ...original(query),
    matches: query === '(max-width: 640px)',
  }))
  mockAlertList()
  vi.mocked(securityGet).mockImplementation(async (path, _signal, params) =>
    path === 'health'
      ? { enabled: true, ready: true, dropped: 0, last_scan: 0 }
      : { items: [{ ...alert, id: params?.before ? 6 : 7 }], next_cursor: 7 }
  )
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  try {
    const { container } = render(
      <QueryClientProvider client={client}>
        <SecurityMonitor />
      </QueryClientProvider>
    )
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Go to next page' })
      ).toBeEnabled()
    )
    const pagination = screen.getByRole('navigation', { name: 'Page' })
    expect(
      pagination.closest('[data-scroll-restoration-id="section-page-content"]')
    ).toBeNull()
    expect(
      within(pagination).getByRole('button', { name: /^Go to page 1/ })
    ).toHaveAttribute('aria-current', 'page')
    expect(
      within(pagination).getByRole('combobox', { name: 'Rows per page' })
    ).toHaveTextContent('20')
    expect(within(pagination).queryByText('Total:')).not.toBeInTheDocument()
    const scroll = container.querySelector<HTMLElement>(
      '[data-scroll-restoration-id="mobile-table-content"]'
    )
    if (!scroll) throw new Error('Mobile scroll container is missing')
    scroll.scrollTop = 600
    await act(() => client.invalidateQueries({ queryKey: ['audit-security'] }))
    expect(scroll.scrollTop).toBe(600)
    await userEvent.click(
      screen.getByRole('button', { name: 'Go to next page' })
    )
    await waitFor(() => expect(scroll.scrollTop).toBe(0))
    expect(
      within(pagination).getByRole('button', { name: /^Go to page 2/ })
    ).toHaveAttribute('aria-current', 'page')
    scroll.scrollTop = 450
    await userEvent.click(
      screen.getByRole('button', { name: 'Go to previous page' })
    )
    await waitFor(() => expect(scroll.scrollTop).toBe(0))
  } finally {
    client.clear()
    media.mockRestore()
  }
})

const alert: SecurityAlert = {
  id: 7,
  version: 3,
  rule: 'sensitive_operation',
  severity: 2,
  status: 'open',
  actor_id: 2,
  resource_type: 'channel',
  first_seen: 100,
  last_seen: 100,
  occurrences: 1,
  matched_count: 2,
}

function mockAlertList(row: SecurityAlert = alert) {
  vi.mocked(securityGet).mockImplementation(async (path) =>
    path === 'health'
      ? { enabled: true, ready: true, dropped: 0, last_scan: Date.now() / 1000 }
      : { items: [row], next_cursor: 0 }
  )
}

it('confirms an alert from the list with one click and no form or extra metadata', async () => {
  const user = userEvent.setup()
  mockAlertList()
  vi.mocked(saveAlert).mockImplementation(async () => {
    mockAlertList({ ...alert, status: 'resolved', version: 4 })
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SecurityMonitor />
    </QueryClientProvider>
  )
  await user.click(await screen.findByRole('button', { name: 'Confirm' }))
  await waitFor(() =>
    expect(saveAlert).toHaveBeenCalledWith(7, 3, 'resolved', '')
  )
  expect(await screen.findByText('Processed · Confirmed')).toBeVisible()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(saveAlert).toHaveBeenCalledTimes(1)
  expect(
    vi
      .mocked(securityGet)
      .mock.calls.some(
        ([path]) => path === 'alerts/7' || path.startsWith('assignees')
      )
  ).toBe(false)
  client.clear()
})

it('shows read-only statuses without management permission', async () => {
  vi.mocked(hasPermission).mockReturnValue(false)
  mockAlertList()
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SecurityMonitor />
    </QueryClientProvider>
  )
  expect(await screen.findByText('Unprocessed')).toBeVisible()
  expect(
    screen.getByText(
      'You can view these records. Processing requires security management permission.'
    )
  ).toBeVisible()
  expect(
    screen.queryByRole('columnheader', { name: 'Actions' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Confirm' })
  ).not.toBeInTheDocument()
  expect(saveAlert).not.toHaveBeenCalled()
  expect(
    screen.queryByRole('button', { name: /Confirm this page/ })
  ).not.toBeInTheDocument()
  client.clear()
})

it('changes the page size through the shared footer and resets the cursor without changing filters or export scope', async () => {
  const user = userEvent.setup()
  vi.mocked(securityGet).mockImplementation(async (path) =>
    path === 'health'
      ? { enabled: true, ready: true, dropped: 0 }
      : { items: [alert], next_cursor: 7 }
  )
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SecurityMonitor />
    </QueryClientProvider>
  )
  await screen.findByRole('button', { name: 'Sensitive operation #7' })
  const firstParams = vi
    .mocked(securityGet)
    .mock.calls.find(([path]) => path === 'alerts')?.[2]
  expect(firstParams).toMatchObject({ limit: 20, before: 0 })
  await user.click(screen.getByRole('button', { name: /^Go to page 2/ }))
  await waitFor(() =>
    expect(securityGet).toHaveBeenCalledWith(
      'alerts',
      expect.any(AbortSignal),
      expect.objectContaining({ before: 7, limit: 20 })
    )
  )
  await user.click(screen.getByRole('combobox', { name: 'Rows per page' }))
  await user.click(await screen.findByRole('option', { name: '100' }))
  await waitFor(() =>
    expect(securityGet).toHaveBeenCalledWith(
      'alerts',
      expect.any(AbortSignal),
      expect.objectContaining({
        before: 0,
        limit: 100,
        start: firstParams?.start,
        end: firstParams?.end,
      })
    )
  )
  expect(screen.getByRole('button', { name: /^Go to page 1/ })).toHaveAttribute(
    'aria-current',
    'page'
  )
  await user.click(screen.getByRole('combobox', { name: 'Rows per page' }))
  await user.click(await screen.findByRole('option', { name: '10' }))
  await waitFor(() =>
    expect(securityGet).toHaveBeenCalledWith(
      'alerts',
      expect.any(AbortSignal),
      expect.objectContaining({ before: 0, limit: 10 })
    )
  )
  await user.click(
    screen.getByRole('button', { name: 'Export alerts (up to 100)' })
  )
  expect(exportAlerts).toHaveBeenCalledExactlyOnceWith(
    expect.objectContaining({
      limit: 100,
      before: undefined,
      cursor: undefined,
    })
  )
  client.clear()
})

it('confirms all pending alerts on the displayed page once and preserves repeat counts', async () => {
  let rows: SecurityAlert[] = [
    alert,
    {
      ...alert,
      id: 8,
      status: 'investigating',
      occurrences: 2,
      repeat_until: Date.now() / 1000 + 3600,
    },
    { ...alert, id: 9, status: 'resolved' },
    { ...alert, id: 10, status: 'false_positive' },
  ]
  vi.mocked(securityGet).mockImplementation(async (path) =>
    path === 'health'
      ? { enabled: true, ready: true, dropped: 0 }
      : { items: rows, next_cursor: 0 }
  )
  vi.mocked(saveAlert).mockImplementation(async (id) => {
    rows = rows.map((row) =>
      row.id === id
        ? { ...row, status: 'resolved', version: row.version + 1 }
        : row
    )
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SecurityMonitor />
    </QueryClientProvider>
  )
  await userEvent.click(
    await screen.findByRole('button', { name: 'Confirm this page (2)' })
  )
  await waitFor(() =>
    expect(toast.success).toHaveBeenCalledExactlyOnceWith('Confirmed 2 alerts.')
  )
  expect(saveAlert).toHaveBeenCalledTimes(2)
  expect(saveAlert).toHaveBeenCalledWith(7, 3, 'resolved', '')
  expect(saveAlert).toHaveBeenCalledWith(8, 3, 'resolved', '', {
    observed_occurrences: 2,
  })
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(
    await screen.findByRole('button', { name: 'Confirm this page (0)' })
  ).toBeDisabled()
  client.clear()
})

it('continues after a version conflict and reports partial success once without retrying', async () => {
  const rows = Array.from({ length: 5 }, (_, index) => ({
    ...alert,
    id: index + 1,
  }))
  vi.mocked(securityGet).mockImplementation(async (path) =>
    path === 'health'
      ? { enabled: true, ready: true, dropped: 0 }
      : { items: rows, next_cursor: 0 }
  )
  vi.mocked(saveAlert).mockImplementation(async (id) => {
    if (id === 2) {
      throw new Error('Conflict', {
        cause: { success: false, message: 'The alert changed' },
      })
    }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SecurityMonitor />
    </QueryClientProvider>
  )
  await userEvent.click(
    await screen.findByRole('button', { name: 'Confirm this page (5)' })
  )
  await waitFor(() =>
    expect(toast.error).toHaveBeenCalledExactlyOnceWith(
      'Confirmed 4; failed 1; not processed 0.',
      { description: 'Refresh the list before trying again.' }
    )
  )
  expect(saveAlert).toHaveBeenCalledTimes(5)
  expect(toast.success).not.toHaveBeenCalled()
  client.clear()
})

it.each([
  'refresh',
  'account',
  'permission',
  'unmount',
  'network',
  'forbidden',
])(
  'bounds confirmation concurrency and handles %s while requests are in flight',
  async (change) => {
    let rows = Array.from({ length: 6 }, (_, index) => ({
      ...alert,
      id: index + 1,
    }))
    const deferred: {
      resolve: () => void
      reject: (error: unknown) => void
    }[] = []
    vi.mocked(securityGet).mockImplementation(async (path) =>
      path === 'health'
        ? { enabled: true, ready: true, dropped: 0 }
        : { items: rows, next_cursor: 0 }
    )
    vi.mocked(saveAlert).mockImplementation(
      () =>
        new Promise<void>((resolve, reject) =>
          deferred.push({ resolve, reject })
        )
    )
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const view = render(
      <QueryClientProvider client={client}>
        <SecurityMonitor />
      </QueryClientProvider>
    )
    await userEvent.click(
      await screen.findByRole('button', { name: 'Confirm this page (6)' })
    )
    await waitFor(() => expect(saveAlert).toHaveBeenCalledTimes(4))
    const busy = screen.getByRole('button', { name: 'Confirming 0/6...' })
    expect(busy).toBeDisabled()
    fireEvent.click(busy)
    expect(
      screen.getByRole('combobox', { name: 'Rows per page' })
    ).toBeDisabled()
    expect(
      screen
        .getAllByRole('button', { name: 'Confirm' })
        .every((button) => button.hasAttribute('disabled'))
    ).toBe(true)
    if (change === 'account') authFixture.auth.user = { id: 2, role: 100 }
    if (change === 'permission') authFixture.auth.user = { id: 1, role: 10 }
    if (change === 'unmount') view.unmount()
    if (change === 'refresh') {
      rows = [{ ...alert, id: 99 }]
      await act(() =>
        client.invalidateQueries({ queryKey: ['audit-security'] })
      )
    }
    await act(async () => {
      for (const [index, item] of deferred.slice(0, 4).entries()) {
        if (index === 0 && change === 'network') {
          item.reject(new Error('Offline'))
        } else if (index === 0 && change === 'forbidden') {
          item.reject({ response: { status: 403 } })
        } else item.resolve()
      }
    })
    if (change === 'refresh') {
      await waitFor(() => expect(saveAlert).toHaveBeenCalledTimes(6))
      expect(vi.mocked(saveAlert).mock.calls.map(([id]) => id)).toEqual([
        1, 2, 3, 4, 5, 6,
      ])
      await act(async () => deferred.slice(4).forEach((item) => item.resolve()))
      await waitFor(() =>
        expect(toast.success).toHaveBeenCalledExactlyOnceWith(
          'Confirmed 6 alerts.'
        )
      )
    } else {
      expect(saveAlert).toHaveBeenCalledTimes(4)
      expect(toast.success).not.toHaveBeenCalled()
      if (change === 'network' || change === 'forbidden') {
        await waitFor(() =>
          expect(toast.error).toHaveBeenCalledExactlyOnceWith(
            'Confirmed 3; failed 1; not processed 2.',
            { description: 'Refresh the list before trying again.' }
          )
        )
      } else {
        expect(toast.error).not.toHaveBeenCalled()
      }
    }
    client.clear()
  }
)

it('confirms only the displayed repeat count and shows later hits without another action', async () => {
  const repeat = {
    ...alert,
    resource_type: 'user',
    occurrences: 2,
    repeat_until: Math.floor(Date.now() / 1000) + 3600,
  }
  mockAlertList(repeat)
  vi.mocked(saveAlert).mockImplementation(async () => {
    const confirmed = {
      ...repeat,
      status: 'resolved',
      version: 5,
      occurrences: 3,
      confirmed_occurrences: 2,
    }
    vi.mocked(securityGet).mockImplementation(async (path) => {
      if (path === 'alerts/7') {
        return { alert: confirmed, events: [], actions: [], deliveries: [] }
      }
      if (path === 'health') return { enabled: true, ready: true, dropped: 0 }
      return { items: [confirmed], next_cursor: 0 }
    })
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SecurityMonitor />
    </QueryClientProvider>
  )
  await userEvent.click(await screen.findByRole('button', { name: 'Confirm' }))
  await waitFor(() =>
    expect(saveAlert).toHaveBeenCalledExactlyOnceWith(7, 3, 'resolved', '', {
      observed_occurrences: 2,
    })
  )
  expect(await screen.findByText('New since confirmation: 1')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Confirm' })
  ).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /#7$/ }))
  const dialog = await screen.findByRole('dialog')
  expect(
    await within(dialog).findByText('New since confirmation: 1')
  ).toBeVisible()
  expect(
    within(dialog).queryByRole('button', { name: 'Confirm' })
  ).not.toBeInTheDocument()
  client.clear()
})

it.each(['ordinary', 'expired', 'malicious', 'archived'])(
  'keeps exact-version confirmation for %s alerts',
  async (kind) => {
    const row = { ...alert, repeat_until: Math.floor(Date.now() / 1000) + 3600 }
    if (kind === 'ordinary') delete (row as Partial<SecurityAlert>).repeat_until
    if (kind === 'expired') row.repeat_until = 1
    if (kind === 'malicious') Object.assign(row, { conclusion: 'malicious' })
    if (kind === 'archived') Object.assign(row, { archived_at: 1 })
    const client = new QueryClient()
    render(
      <QueryClientProvider client={client}>
        <AlertConfirmButton alert={row} />
      </QueryClientProvider>
    )
    await userEvent.click(screen.getByRole('button', { name: 'Confirm' }))
    await waitFor(() =>
      expect(saveAlert).toHaveBeenCalledExactlyOnceWith(7, 3, 'resolved', '')
    )
    client.clear()
  }
)

it.each(['hits', 'manual', 'severity', 'expired', 'different window'])(
  'handles a background %s change while confirming the displayed repeat snapshot',
  async (change) => {
    vi.mocked(hasPermission).mockReturnValue(true)
    const repeat = {
      ...alert,
      resource_type: 'user',
      occurrences: 2,
      repeat_until: Math.floor(Date.now() / 1000) + 3600,
    }
    const original = { alert: repeat, events: [], actions: [], deliveries: [] }
    vi.mocked(securityGet).mockResolvedValue(original)
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <SecurityAlertDialog id={7} onClose={vi.fn()} />
      </QueryClientProvider>
    )
    await screen.findByRole('button', { name: 'Confirm' })
    const latest = {
      ...original,
      alert: {
        ...repeat,
        version: 4,
        occurrences: change === 'manual' ? 2 : 3,
      },
    }
    if (change === 'severity') latest.alert.severity = 3
    if (change === 'expired') latest.alert.repeat_until = 1
    if (change === 'different window') latest.alert.repeat_until++
    vi.mocked(securityGet).mockResolvedValue(latest)
    await act(async () => {
      client.setQueryData(
        ['audit-security', 1, 100, undefined, 'detail', 7],
        latest
      )
    })
    await screen.findByText(
      'The alert or delivery records have changed. Refresh to load the latest details.'
    )
    const confirm = screen.getByRole('button', { name: 'Confirm' })
    if (change === 'hits') {
      expect(confirm).toBeEnabled()
      await userEvent.click(confirm)
      await waitFor(() =>
        expect(saveAlert).toHaveBeenCalledExactlyOnceWith(
          7,
          3,
          'resolved',
          '',
          { observed_occurrences: 2 }
        )
      )
    } else {
      expect(confirm).toBeDisabled()
      expect(saveAlert).not.toHaveBeenCalled()
    }
    client.clear()
  }
)

it('blocks duplicate confirmation and does not confirm a newer event with an older response', async () => {
  let finish!: () => void
  vi.mocked(saveAlert).mockImplementation(
    () =>
      new Promise<void>((resolve) => {
        finish = resolve
      })
  )
  const client = new QueryClient()
  const view = render(
    <QueryClientProvider client={client}>
      <AlertConfirmButton alert={alert} />
    </QueryClientProvider>
  )
  await userEvent.click(screen.getByRole('button', { name: 'Confirm' }))
  const saving = await screen.findByRole('button', { name: 'Saving...' })
  expect(saving).toBeDisabled()
  await userEvent.click(saving)
  expect(saveAlert).toHaveBeenCalledTimes(1)
  view.rerender(
    <QueryClientProvider client={client}>
      <AlertConfirmButton alert={{ ...alert, version: 4 }} />
    </QueryClientProvider>
  )
  await act(async () => finish())
  expect(await screen.findByRole('button', { name: 'Confirm' })).toBeEnabled()
  expect(screen.queryByText('Confirmed')).not.toBeInTheDocument()
  expect(saveAlert).toHaveBeenCalledWith(7, 3, 'resolved', '')
  client.clear()
})

it('keeps confirmation available after a failure without silently retrying or clearing metadata', async () => {
  vi.mocked(saveAlert).mockRejectedValue(
    new Error('The alert may have changed. Refresh and try again.')
  )
  const client = new QueryClient()
  render(
    <QueryClientProvider client={client}>
      <AlertConfirmButton alert={alert} />
    </QueryClientProvider>
  )
  await userEvent.click(screen.getByRole('button', { name: 'Confirm' }))
  await waitFor(() => expect(toast.error).toHaveBeenCalled())
  expect(screen.getByRole('button', { name: 'Confirm' })).toBeEnabled()
  expect(screen.queryByText('Confirmed')).not.toBeInTheDocument()
  expect(toast.success).not.toHaveBeenCalled()
  expect(saveAlert).toHaveBeenCalledExactlyOnceWith(7, 3, 'resolved', '')
  client.clear()
})

it('loads assignees for each alert without reusing another alert candidate list', async () => {
  const user = userEvent.setup()
  vi.mocked(securityGet).mockImplementation(async (path) =>
    path === 'assignees?alert_id=7'
      ? [{ id: 10, username: 'eligible-admin', role: 10 }]
      : [{ id: 1, username: 'root-only', role: 100 }]
  )
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  })
  render(
    <QueryClientProvider client={client}>
      <AlertStatusCell alert={alert} />
      <AlertStatusCell alert={{ ...alert, id: 8, status: 'investigating' }} />
    </QueryClientProvider>
  )
  await user.click(screen.getByRole('button', { name: 'Not handled' }))
  await waitFor(() =>
    expect(screen.getByRole('combobox', { name: 'Assignee' })).toBeEnabled()
  )
  await user.click(screen.getByRole('combobox', { name: 'Assignee' }))
  expect(
    await screen.findByRole('option', { name: 'eligible-admin #10' })
  ).toBeVisible()
  await user.keyboard('{Escape}')
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  await user.click(screen.getByRole('button', { name: 'Investigating' }))
  await waitFor(() =>
    expect(screen.getByRole('combobox', { name: 'Assignee' })).toBeEnabled()
  )
  await user.click(screen.getByRole('combobox', { name: 'Assignee' }))
  expect(
    await screen.findByRole('option', { name: 'root-only #1' })
  ).toBeVisible()
  expect(
    screen.queryByRole('option', { name: 'eligible-admin #10' })
  ).not.toBeInTheDocument()
  expect(securityGet).toHaveBeenCalledWith(
    'assignees?alert_id=7',
    expect.any(AbortSignal)
  )
  expect(securityGet).toHaveBeenCalledWith(
    'assignees?alert_id=8',
    expect.any(AbortSignal)
  )
  client.clear()
})

it('saves multiple manual assignees while preserving a read-only customer owner', async () => {
  const user = userEvent.setup()
  vi.mocked(securityGet).mockResolvedValue([
    { id: 10, username: 'first-admin', role: 10 },
    { id: 11, username: 'second-admin', role: 10 },
  ])
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <AlertStatusCell
        alert={{
          ...alert,
          assignees: [
            { id: 3, username: 'customer-owner', role: 1, source: 'owner' },
          ],
          manual_assignee_ids: [],
        }}
      />
    </QueryClientProvider>
  )
  await user.click(screen.getByRole('button', { name: 'Not handled' }))
  expect(screen.getByText('customer-owner #3')).toBeVisible()
  await waitFor(() =>
    expect(screen.getByRole('combobox', { name: 'Assignee' })).toBeEnabled()
  )
  await user.click(screen.getByRole('combobox', { name: 'Assignee' }))
  expect(
    screen.queryByRole('option', { name: /customer-owner/ })
  ).not.toBeInTheDocument()
  await user.type(
    screen.getByRole('combobox', { name: 'Assignee' }),
    'first-admin'
  )
  await user.click(
    await screen.findByRole('option', { name: 'first-admin #10' })
  )
  expect(screen.getByRole('combobox', { name: 'Assignee' })).toHaveAttribute(
    'aria-expanded',
    'true'
  )
  await user.type(
    screen.getByRole('combobox', { name: 'Assignee' }),
    'second-admin'
  )
  await user.click(
    await screen.findByRole('option', { name: 'second-admin #11' })
  )
  await user.keyboard('{Escape}')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(saveAlert).toHaveBeenCalledWith(7, 3, 'open', '', {
      assignee_ids: [10, 11],
      conclusion: '',
    })
  )
  client.clear()
})

it('keeps the reviewed version and the note when a concurrent update rejects saving', async () => {
  const user = userEvent.setup()
  vi.mocked(saveAlert).mockRejectedValue(
    new Error('The alert may have changed. Refresh and try again.')
  )
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const view = render(
    <QueryClientProvider client={client}>
      <AlertStatusCell alert={alert} />
    </QueryClientProvider>
  )
  await user.click(screen.getByRole('button', { name: 'Not handled' }))
  await user.type(screen.getByLabelText('Investigation note'), 'Needs review')
  view.rerender(
    <QueryClientProvider client={client}>
      <AlertStatusCell alert={{ ...alert, version: 4, status: 'resolved' }} />
    </QueryClientProvider>
  )
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(saveAlert).toHaveBeenCalledWith(7, 3, 'open', 'Needs review')
  )
  expect(toast.error).toHaveBeenCalled()
  expect(screen.getByLabelText('Investigation note')).toHaveValue(
    'Needs review'
  )
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  await user.click(screen.getByRole('button', { name: 'Confirmed' }))
  expect(screen.getByRole('combobox', { name: 'Handling status' })).toHaveValue(
    'Confirmed'
  )
  expect(screen.getByLabelText('Investigation note')).toHaveValue('')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(saveAlert).toHaveBeenLastCalledWith(7, 4, 'resolved', '')
  )
  client.clear()
})

it('keeps alert details read-only and displays the investigation history', async () => {
  vi.mocked(hasPermission).mockReturnValue(false)
  vi.mocked(securityGet).mockResolvedValue({
    alert,
    events: [],
    actions: [
      { id: 1, actor_id: 2, action: 'investigating', note: 'Needs review' },
    ],
    deliveries: [],
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SecurityAlertDialog id={7} onClose={vi.fn()} />
    </QueryClientProvider>
  )
  expect((await screen.findAllByText('Matched rule')).length).toBeGreaterThan(0)
  expect(
    screen.getAllByText('Sensitive operation', { exact: true })[0]
  ).toBeVisible()
  expect(
    within(screen.getByRole('tabpanel', { name: 'Overview' })).getByText(
      'Trigger condition'
    )
  ).toBeVisible()
  expect(
    screen.getByText('Maximum impact per operation').parentElement
  ).toHaveTextContent('2')
  expect(
    screen.getByText('Alert trigger count').parentElement
  ).toHaveTextContent('1')
  await userEvent.click(
    screen.getByRole('tab', { name: 'Investigation history' })
  )
  expect(await screen.findByText(/Needs review/)).toBeVisible()
  expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
  expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Save' })).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Confirm' })
  ).not.toBeInTheDocument()
  client.clear()
})

it.each(['Overview', 'Evidence', 'Investigation history', 'Notifications'])(
  'confirms directly from the %s detail tab and updates the displayed snapshot',
  async (tab) => {
    vi.mocked(hasPermission).mockReturnValue(true)
    let saved = false
    vi.mocked(securityGet).mockImplementation(async () => ({
      alert: saved ? { ...alert, version: 4, status: 'resolved' } : alert,
      events: [],
      actions: [],
      deliveries: [],
    }))
    vi.mocked(saveAlert).mockImplementation(async () => {
      saved = true
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <SecurityAlertDialog id={7} onClose={vi.fn()} />
      </QueryClientProvider>
    )
    await screen.findByRole('button', { name: 'Confirm' })
    await userEvent.click(screen.getByRole('tab', { name: tab }))
    await userEvent.click(screen.getByRole('button', { name: 'Confirm' }))
    await waitFor(() =>
      expect(saveAlert).toHaveBeenCalledWith(7, 3, 'resolved', '')
    )
    await userEvent.click(screen.getByRole('tab', { name: 'Overview' }))
    await waitFor(() => {
      const overview = screen.getByRole('tabpanel', { name: 'Overview' })
      expect(within(overview).getByText('Confirmed')).toBeVisible()
    })
    expect(
      screen.queryByRole('button', { name: 'Confirm' })
    ).not.toBeInTheDocument()
    expect(screen.getAllByRole('dialog')).toHaveLength(1)
    client.clear()
  }
)

it('detects delivery changes without replacing the detail being read', async () => {
  const user = userEvent.setup()
  const original = {
    alert,
    events: [],
    actions: [
      {
        id: 1,
        actor_id: 0,
        action: 'system_reopen',
        note: '',
        created_at: 200,
        from_status: 'resolved',
        to_status: 'open',
        event_id: 'late-event',
        event_at: 100,
        detected_at: 200,
        source: 'repair',
      },
    ],
    deliveries: [{ id: 1, kind: 'email', status: 'pending', attempts: 0 }],
  }
  vi.mocked(securityGet).mockResolvedValue(original)
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SecurityAlertDialog id={7} onClose={vi.fn()} />
    </QueryClientProvider>
  )
  await user.click(
    await screen.findByRole('tab', { name: 'Investigation history' })
  )
  expect(screen.getByText('Confirmed → Not handled')).toBeVisible()
  expect(screen.getByText('late-event')).toBeVisible()
  expect(screen.getByText('Historical rescan')).toBeVisible()
  await user.click(screen.getByRole('tab', { name: 'Notifications' }))
  expect(await screen.findByText('Pending', { exact: true })).toBeVisible()
  const latest = {
    ...original,
    deliveries: [{ ...original.deliveries[0], status: 'sent', attempts: 1 }],
  }
  vi.mocked(securityGet).mockResolvedValue(latest)
  await act(async () => {
    client.setQueryData(
      ['audit-security', 1, 100, undefined, 'detail', 7],
      latest
    )
  })
  expect(
    await screen.findByText(
      'The alert or delivery records have changed. Refresh to load the latest details.'
    )
  ).toBeVisible()
  expect(screen.getByText('Pending', { exact: true })).toBeVisible()
  expect(screen.queryByText('Sent', { exact: true })).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Refresh' }))
  expect(await screen.findByText('Sent', { exact: true })).toBeVisible()
  expect(screen.queryByText('Pending', { exact: true })).not.toBeInTheDocument()
  client.clear()
})

it('refetches the same complete-day range on search and advances it after midnight', async () => {
  const clock = vi
    .spyOn(Date, 'now')
    .mockReturnValue(new Date('2026-11-23T13:39:46').getTime())
  vi.mocked(securityGet).mockImplementation(async (path) =>
    path === 'health'
      ? { enabled: false, ready: false }
      : { items: [], next_cursor: 0 }
  )
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SecurityMonitor />
    </QueryClientProvider>
  )
  await screen.findByText('Monitoring disabled')
  const range = {
    start: new Date('2026-11-22T00:00:00').getTime() / 1000,
    end: new Date('2026-11-23T23:59:59').getTime() / 1000,
    before: 0,
  }
  expect(securityGet).toHaveBeenCalledWith(
    'alerts',
    expect.any(AbortSignal),
    expect.objectContaining(range)
  )
  vi.mocked(securityGet).mockClear()
  clock.mockReturnValue(new Date('2026-11-23T14:00:00').getTime())
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(securityGet).toHaveBeenCalledWith(
      'alerts',
      expect.any(AbortSignal),
      expect.objectContaining(range)
    )
  )
  clock.mockReturnValue(new Date('2026-11-24T00:01:00').getTime())
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(securityGet).toHaveBeenCalledWith(
      'alerts',
      expect.any(AbortSignal),
      expect.objectContaining({
        start: new Date('2026-11-23T00:00:00').getTime() / 1000,
        end: new Date('2026-11-24T23:59:59').getTime() / 1000,
        before: 0,
      })
    )
  )
  client.clear()
})

it('keeps advanced filters when collapsed, searches them, and resets the cursor and filters', async () => {
  const clock = vi
    .spyOn(Date, 'now')
    .mockReturnValue(new Date('2026-11-23T13:39:46').getTime())
  const user = userEvent.setup()
  vi.mocked(securityGet).mockImplementation(async (path) =>
    path === 'health'
      ? { enabled: false, ready: false }
      : { items: [], next_cursor: 7 }
  )
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SecurityMonitor />
    </QueryClientProvider>
  )
  await screen.findByText('Monitoring disabled')
  expect(screen.queryByRole('textbox', { name: 'IP' })).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Expand' }))
  await user.type(screen.getByRole('textbox', { name: 'IP' }), '203.0.113.9')
  await user.click(screen.getByRole('button', { name: /Collapse/ }))
  await user.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(securityGet).toHaveBeenCalledWith(
      'alerts',
      expect.any(AbortSignal),
      expect.objectContaining({ ip: '203.0.113.9', before: 0 })
    )
  )
  await user.click(screen.getByRole('button', { name: 'Go to next page' }))
  await waitFor(() =>
    expect(securityGet).toHaveBeenCalledWith(
      'alerts',
      expect.any(AbortSignal),
      expect.objectContaining({ before: 7 })
    )
  )
  clock.mockReturnValue(new Date('2026-11-24T00:01:00').getTime())
  await user.click(screen.getByRole('button', { name: 'Reset' }))
  await waitFor(() =>
    expect(
      [...vi.mocked(securityGet).mock.calls]
        .reverse()
        .find(([path]) => path === 'alerts')?.[2]
    ).toMatchObject({
      ip: '',
      view: 'id',
      before: 0,
      start: new Date('2026-11-23T00:00:00').getTime() / 1000,
      end: new Date('2026-11-24T23:59:59').getTime() / 1000,
    })
  )
  await user.click(screen.getByRole('button', { name: 'Expand' }))
  expect(screen.getByRole('textbox', { name: 'IP' })).toHaveValue('')
  client.clear()
})

it('keeps provider errors visible while health details are collapsed', async () => {
  const user = userEvent.setup()
  vi.mocked(securityGet).mockImplementation(async (path) =>
    path === 'health'
      ? {
          enabled: true,
          ready: true,
          last_scan: Date.now() / 1000,
          capture_enabled: true,
          providers: [
            {
              id: 'selfhost',
              name: '新加坡自建 IP 情报',
              enabled: true,
              daily_limit: 100,
              used: 3,
              last_error: 'timeout',
            },
          ],
        }
      : { items: [], next_cursor: 0 }
  )
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SecurityMonitor />
    </QueryClientProvider>
  )
  expect(
    await screen.findByText(
      'Provider unavailable. Check configuration and retry.'
    )
  ).toBeVisible()
  expect(screen.queryByText('新加坡自建 IP 情报')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Details' }))
  expect(await screen.findByText('IP lookup')).toBeVisible()
  expect(screen.queryByText('新加坡自建 IP 情报')).not.toBeInTheDocument()
  expect(screen.getByText('Request timed out')).toBeVisible()
  expect(screen.getByText('Processed event time')).toBeVisible()
  client.clear()
})

it('validates and verifies settings submitted from the dialog footer', async () => {
  const user = userEvent.setup()
  vi.mocked(securityGet).mockResolvedValue({ enabled: false, ready: false })
  vi.mocked(getMonitorSettings).mockResolvedValue({ failure_threshold: '5' })
  requestVerification.mockResolvedValue({ proof_token: 'ui-test-proof' })
  vi.mocked(saveMonitorSettings).mockResolvedValue(undefined)
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SecurityMonitor />
    </QueryClientProvider>
  )
  await user.click(screen.getByRole('button', { name: 'Monitoring settings' }))
  await user.click(await screen.findByRole('button', { name: 'More settings' }))
  const threshold = await screen.findByLabelText('Sign-in failure threshold')
  fireEvent.change(threshold, { target: { value: '0' } })
  await user.click(screen.getByRole('button', { name: 'Save' }))
  expect(threshold).toBeInvalid()
  expect(requestVerification).not.toHaveBeenCalled()
  expect(saveMonitorSettings).not.toHaveBeenCalled()
  fireEvent.change(threshold, { target: { value: '8' } })
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(requestVerification).toHaveBeenCalledWith({
      scope: 'audit.security.settings',
      context: { values: expect.objectContaining({ failure_threshold: '8' }) },
    })
  )
  expect(saveMonitorSettings).toHaveBeenCalledWith(
    expect.objectContaining({ failure_threshold: '8' }),
    'ui-test-proof'
  )
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  client.clear()
})

it('uses activity cursors and removes time restrictions for unresolved alerts', async () => {
  const user = userEvent.setup()
  vi.mocked(securityGet).mockImplementation(async (path) =>
    path === 'health'
      ? { enabled: false, ready: false }
      : { items: [], next_cursor: 'opaque-activity' }
  )
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <SecurityMonitor />
    </QueryClientProvider>
  )
  await user.click(
    await screen.findByRole('button', { name: 'Security monitoring' })
  )
  await user.click(
    await screen.findByRole('option', { name: 'Recently updated' })
  )
  await user.click(screen.getByRole('button', { name: 'Go to next page' }))
  await waitFor(() =>
    expect(securityGet).toHaveBeenCalledWith(
      'alerts',
      expect.any(AbortSignal),
      expect.objectContaining({ view: 'activity', cursor: 'opaque-activity' })
    )
  )
  const activity = vi
    .mocked(securityGet)
    .mock.calls.filter(
      ([path, , params]) => path === 'alerts' && params?.view === 'activity'
    )
  expect(activity.at(-1)?.[2]?.start).toBe(activity[0]?.[2]?.start)
  expect(activity.at(-1)?.[2]).not.toHaveProperty('before')
  await user.click(screen.getByRole('button', { name: 'Security monitoring' }))
  await user.click(await screen.findByRole('option', { name: 'Unprocessed' }))
  await waitFor(() => {
    const calls = vi
      .mocked(securityGet)
      .mock.calls.filter(([path]) => path === 'alerts')
    const params = calls.at(-1)?.[2]
    expect(params).toMatchObject({ view: 'unresolved', before: 0 })
    expect(params).not.toHaveProperty('start')
    expect(params).not.toHaveProperty('end')
    expect(params).not.toHaveProperty('cursor')
  })
  client.clear()
})

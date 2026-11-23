import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  act,
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { toast } from 'sonner'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { createAppQueryClient } from '@/lib/query-client'
import { createServerError } from '@/lib/server-error-message'

import { EmployeeCustomerExport } from './employee-customer-export'

const request = vi.hoisted(() => vi.fn())
vi.mock('../employee-api', async (original) => ({
  ...(await original<typeof import('../employee-api')>()),
  exportRequest: request,
}))
vi.mock('@/stores/auth-store', () => ({
  useAuthStore: (selector: (state: unknown) => unknown) =>
    selector({ auth: { user: { id: 10 } } }),
}))

beforeEach(() => {
  request.mockReset()
  request.mockImplementation(async (_method: string, url: string) => {
    if (url.endsWith('/capabilities')) {
      return {
        enabled: true,
        max_range_sec: 86400 * 31,
        max_customers: 20,
        columns: [],
        anomaly_kinds: ['stream_error', 'retried'],
        quota_per_unit: 500000,
        max_filter_values: 20,
        templates: ['invoice', 'custom'].map((key) => ({
          key,
          name: key,
          mode: key === 'custom' ? 'both' : 'detail',
          summary_dims:
            key === 'custom'
              ? ['date', 'model_name', 'token_name', 'group']
              : [],
          columns: [],
          allowed_filters: [
            'model_name',
            'token_name',
            'group',
            'anomaly_only',
            'anomaly_kinds',
            'charged',
            'quota_min',
            'completion_tokens_min',
            'completion_tokens_max',
            'use_time_min',
            'min_retry_count',
          ],
        })),
      }
    }
    if (url.includes('/options?')) return { items: [], next_cursor: '' }
    if (url.endsWith('/estimate')) {
      return { rows: 2, capped: false, available: true }
    }
    return { jobs: [] }
  })
})
afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

it.each([
  ['3', 'Manage'],
  ['7', 'Login'],
])(
  'marks retired type %s while allowing historical exports',
  async (logType, label) => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const user = userEvent.setup()
    render(
      <QueryClientProvider client={client}>
        <EmployeeCustomerExport prefill={{ customerId: 42, logType }} />
      </QueryClientProvider>
    )
    await user.click(
      await screen.findByRole('button', { name: 'Export customer data' })
    )
    const type = screen.getByRole('combobox', { name: 'Type' })
    expect(type).toHaveTextContent(`${label}Deprecated`)
    expect(type).toHaveAccessibleDescription(
      'Only used to find historical logs. New records are available in Audit Logs.'
    )
    await user.click(type)
    for (const retired of ['Manage', 'Login']) {
      expect(
        within(
          screen.getByRole('option', { name: new RegExp(`^${retired}`) })
        ).getByText('Deprecated')
      ).toBeVisible()
    }
    for (const current of [
      'All Types',
      'Top-up',
      'Consume',
      'System',
      'Error',
      'Refund',
    ]) {
      expect(
        within(screen.getByRole('option', { name: current })).queryByText(
          'Deprecated'
        )
      ).not.toBeInTheDocument()
    }
    await user.click(
      screen.getByRole('option', { name: new RegExp(`^${label}`) })
    )
    await waitFor(() =>
      expect(request).toHaveBeenCalledWith(
        'post',
        expect.stringContaining('/estimate'),
        expect.objectContaining({ type: Number(logType) }),
        { quiet: true }
      )
    )
    await user.click(screen.getByRole('button', { name: 'Start Export' }))
    await waitFor(() =>
      expect(request).toHaveBeenCalledWith(
        'post',
        '/api/user/employee/export/jobs',
        expect.objectContaining({ type: Number(logType) })
      )
    )
    client.clear()
  }
)

it.each([undefined, '5'])(
  'uses the selected type for estimates, options and creation (prefill=%s)',
  async (logType) => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const user = userEvent.setup()
    render(
      <QueryClientProvider client={client}>
        <EmployeeCustomerExport prefill={{ customerId: 42, logType }} />
      </QueryClientProvider>
    )
    await user.click(
      await screen.findByRole('button', { name: 'Export customer data' })
    )
    const type = screen.getByRole('combobox', { name: 'Type' })
    expect(type).toHaveTextContent(logType ? 'Error' : 'All Types')
    await waitFor(() =>
      expect(request).toHaveBeenCalledWith(
        'post',
        expect.stringContaining('/estimate'),
        expect.objectContaining({
          type: Number(logType ?? 0),
          customer_ids: [42],
        }),
        { quiet: true }
      )
    )
    await user.click(type)
    await user.click(await screen.findByRole('option', { name: 'Consume' }))
    await waitFor(() =>
      expect(request).toHaveBeenCalledWith(
        'post',
        expect.stringContaining('/estimate'),
        expect.objectContaining({ type: 2 }),
        { quiet: true }
      )
    )
    await waitFor(() =>
      expect(
        request.mock.calls.some(
          (call) => call[1].includes('/options?') && call[1].includes('type=2')
        )
      ).toBe(true)
    )
    await user.click(type)
    await user.click(await screen.findByRole('option', { name: 'Error' }))
    await user.click(screen.getByRole('combobox', { name: 'Template' }))
    await user.click(await screen.findByRole('option', { name: 'custom' }))
    expect(type).toHaveTextContent('Error')
    await waitFor(() =>
      expect(request).toHaveBeenCalledWith(
        'post',
        expect.stringContaining('/estimate'),
        expect.objectContaining({ type: 5, template_id: 'custom' }),
        { quiet: true }
      )
    )
    const estimate = [...request.mock.calls]
      .reverse()
      .find((call) => call[1].endsWith('/estimate'))?.[2]
    await user.click(screen.getByRole('button', { name: 'Start Export' }))
    await waitFor(() =>
      expect(request).toHaveBeenCalledWith(
        'post',
        '/api/user/employee/export/jobs',
        estimate
      )
    )
    client.clear()
  }
)

it.each([
  ['invoice', '0'],
  ['invoice', '2'],
  ['invoice', '5'],
  ['custom', '0'],
  ['custom', '2'],
  ['custom', '5'],
])(
  'submits %s with log type %s and shows export history',
  async (template, logType) => {
    const client = createAppQueryClient()
    const user = userEvent.setup()
    render(
      <QueryClientProvider client={client}>
        <EmployeeCustomerExport prefill={{ customerId: 42 }} />
      </QueryClientProvider>
    )
    await user.click(
      await screen.findByRole('button', { name: 'Export customer data' })
    )
    if (template === 'custom') {
      await user.click(screen.getByRole('combobox', { name: 'Template' }))
      await user.click(await screen.findByRole('option', { name: 'custom' }))
      expect(screen.getByText('Detail + summary')).toBeVisible()
    }
    if (logType !== '0') {
      await user.click(screen.getByRole('combobox', { name: 'Type' }))
      await user.click(
        await screen.findByRole('option', {
          name: logType === '2' ? 'Consume' : 'Error',
        })
      )
    }
    await user.click(screen.getByRole('button', { name: 'Start Export' }))
    await waitFor(() =>
      expect(request).toHaveBeenCalledWith(
        'post',
        '/api/user/employee/export/jobs',
        expect.objectContaining({
          template_id: template,
          type: Number(logType),
          customer_ids: [42],
        })
      )
    )
    await waitFor(() =>
      expect(
        screen.getByRole('tab', { name: 'Export history' })
      ).toHaveAttribute('aria-selected', 'true')
    )
    client.clear()
  }
)

it.each([
  ['invoice', 'cooldown'],
  ['custom', 'cooldown'],
  ['custom', 'busy'],
  ['custom', 'forbidden'],
])(
  'reports %s creation failure (%s) once and preserves the form for retry',
  async (template, reason) => {
    const client = createAppQueryClient()
    const user = userEvent.setup()
    const notify = vi.spyOn(toast, 'error').mockReturnValue('export-error')
    const success = vi.spyOn(toast, 'success').mockReturnValue('export-success')
    const messages: Record<string, string> = {
      cooldown: 'Please wait 5 minutes before exporting again.',
      busy: 'Too many export tasks. Please try again later.',
      forbidden: 'Access denied. Please check your export permissions.',
    }
    const message = messages[reason]
    const failure =
      reason === 'forbidden'
        ? Object.assign(new Error('Request failed with status code 403'), {
            response: { status: 403, data: { success: false, message } },
          })
        : createServerError({ success: false, message })
    const respond = request.getMockImplementation()
    let rejectCreation = true
    request.mockImplementation(async (method: string, url: string) => {
      if (
        method === 'post' &&
        url === '/api/user/employee/export/jobs' &&
        rejectCreation
      ) {
        throw failure
      }
      return respond?.(method, url)
    })
    render(
      <QueryClientProvider client={client}>
        <EmployeeCustomerExport prefill={{ customerId: 42, logType: '2' }} />
      </QueryClientProvider>
    )
    await user.click(
      await screen.findByRole('button', { name: 'Export customer data' })
    )
    if (template === 'custom') {
      await user.click(screen.getByRole('combobox', { name: 'Template' }))
      await user.click(await screen.findByRole('option', { name: 'custom' }))
    }
    await user.click(screen.getByRole('button', { name: 'Start Export' }))
    await waitFor(() => expect(notify).toHaveBeenCalledWith(message))
    expect(notify).toHaveBeenCalledTimes(1)
    expect(success).not.toHaveBeenCalled()
    expect(screen.getByRole('tab', { name: 'New Export' })).toHaveAttribute(
      'aria-selected',
      'true'
    )
    expect(screen.getByRole('combobox', { name: 'Template' })).toHaveValue(
      template
    )
    expect(screen.getByRole('combobox', { name: 'Type' })).toHaveTextContent(
      'Consume'
    )
    expect(screen.getByText('#42')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Start Export' })).toBeEnabled()
    const original = request.mock.calls.find(
      (call) =>
        call[0] === 'post' && call[1] === '/api/user/employee/export/jobs'
    )?.[2]
    rejectCreation = false
    await user.click(screen.getByRole('button', { name: 'Start Export' }))
    await waitFor(() => expect(success).toHaveBeenCalledWith('Export started'))
    expect(
      request.mock.calls
        .filter(
          (call) =>
            call[0] === 'post' && call[1] === '/api/user/employee/export/jobs'
        )
        .map((call) => call[2])
    ).toEqual([original, original])
    await waitFor(() =>
      expect(
        screen.getByRole('tab', { name: 'Export history' })
      ).toHaveAttribute('aria-selected', 'true')
    )
    expect(notify).toHaveBeenCalledTimes(1)
    client.clear()
  }
)

it('keeps error type and combined filters identical in estimates and creation, including false and zero', async () => {
  const client = createAppQueryClient()
  const user = userEvent.setup()
  const respond = request.getMockImplementation()
  request.mockImplementation(async (method: string, url: string) => {
    if (url.includes('/options?')) {
      const field = new URL(url, 'http://localhost').searchParams.get('field')
      const options: Record<string, string> = {
        model_name: 'model_a',
        token_name: 'key-b',
        group: 'default',
      }
      const value = options[field ?? '']
      return {
        items: value ? [{ value, label: value }] : [],
        next_cursor: '',
      }
    }
    return respond?.(method, url)
  })
  render(
    <QueryClientProvider client={client}>
      <EmployeeCustomerExport prefill={{ customerId: 42, logType: '5' }} />
    </QueryClientProvider>
  )
  await user.click(
    await screen.findByRole('button', { name: 'Export customer data' })
  )
  for (const [label, value] of [
    ['Model', 'model_a'],
    ['Token', 'key-b'],
    ['Group', 'default'],
  ]) {
    await user.click(screen.getByRole('combobox', { name: label }))
    await user.click(await screen.findByRole('option', { name: value }))
  }
  await user.click(screen.getByText('Troubleshooting filters'))
  await user.click(screen.getByRole('button', { name: 'Free (cost = 0)' }))
  await user.click(
    screen.getByRole('button', { name: 'Stream ended abnormally' })
  )
  for (const [label, value] of [
    ['Min output tokens', '0'],
    ['Max output tokens', '0'],
    ['Min quota units (0.000002 each)', '0'],
    ['Min duration (s)', '5'],
    ['Min retries', '2'],
  ]) {
    await user.type(screen.getByLabelText(label), value)
  }
  const filters = {
    model_name: 'model_a',
    token_name: 'key-b',
    group: 'default',
    charged: false,
    anomaly_only: false,
    anomaly_kinds: ['stream_error'],
    completion_tokens_min: 0,
    completion_tokens_max: 0,
    quota_min: 0,
    use_time_min: 5,
    min_retry_count: 2,
  }
  for (const [type, label] of [
    [2, 'Consume'],
    [5, 'Error'],
  ] as const) {
    await user.click(screen.getByRole('combobox', { name: 'Type' }))
    await user.click(await screen.findByRole('option', { name: label }))
    await waitFor(() =>
      expect(request).toHaveBeenCalledWith(
        'post',
        '/api/user/employee/export/estimate',
        expect.objectContaining({ type, filters }),
        { quiet: true }
      )
    )
  }
  expect(
    await screen.findByText('At most 2 rows before anomaly filtering')
  ).toBeVisible()
  const estimate = [...request.mock.calls]
    .reverse()
    .find(
      (call) =>
        call[1].endsWith('/estimate') &&
        call[2].type === 5 &&
        call[2].filters.min_retry_count === 2
    )?.[2]
  await user.click(screen.getByRole('button', { name: 'Start Export' }))
  await waitFor(() =>
    expect(request).toHaveBeenCalledWith(
      'post',
      '/api/user/employee/export/jobs',
      estimate
    )
  )
  client.clear()
})

it.each([
  ['negative', 'Min retries', '-1'],
  ['fraction', 'Min duration (s)', '0.5'],
  ['unsafe integer', 'Min quota units (0.000002 each)', '9007199254740992'],
  ['inverted bounds', 'Min output tokens', '1'],
])(
  'rejects %s conditions and stops validating them when the template withdraws them',
  async (_reason, label, value) => {
    const client = createAppQueryClient()
    const user = userEvent.setup()
    render(
      <QueryClientProvider client={client}>
        <EmployeeCustomerExport prefill={{ customerId: 42, logType: '5' }} />
      </QueryClientProvider>
    )
    await user.click(
      await screen.findByRole('button', { name: 'Export customer data' })
    )
    await user.click(screen.getByText('Troubleshooting filters'))
    if (_reason === 'inverted bounds') {
      await user.type(screen.getByLabelText('Max output tokens'), '0')
    }
    await user.type(screen.getByLabelText(label), value)
    expect(screen.getByRole('button', { name: 'Start Export' })).toBeDisabled()
    await act(async () => {
      client.setQueryData(
        ['employee-export-capabilities', 10],
        (data: { templates: { allowed_filters: string[] }[] }) => ({
          ...data,
          templates: data.templates.map((template) => ({
            ...template,
            allowed_filters: [],
          })),
        })
      )
    })
    await waitFor(() =>
      expect(screen.queryByLabelText(label)).not.toBeInTheDocument()
    )
    expect(
      screen.queryByText('Minimum must not exceed maximum')
    ).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Start Export' })).toBeEnabled()
    await user.click(screen.getByRole('button', { name: 'Start Export' }))
    const submitted = request.mock.calls.find(
      (call) =>
        call[0] === 'post' && call[1] === '/api/user/employee/export/jobs'
    )?.[2]
    expect(submitted?.type).toBe(5)
    expect(submitted?.filters).toEqual({})
    client.clear()
  }
)

it('clears template-specific conditions while retaining the selected error log type', async () => {
  const client = createAppQueryClient()
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={client}>
      <EmployeeCustomerExport prefill={{ customerId: 42, logType: '5' }} />
    </QueryClientProvider>
  )
  await user.click(
    await screen.findByRole('button', { name: 'Export customer data' })
  )
  await user.click(screen.getByText('Troubleshooting filters'))
  await user.click(
    screen.getByRole('button', { name: 'Charged with zero output' })
  )
  await user.click(
    screen.getByRole('button', { name: 'Retried across channels' })
  )
  await user.type(screen.getByLabelText('Min retries'), '2')
  await user.click(screen.getByRole('combobox', { name: 'Template' }))
  await user.click(await screen.findByRole('option', { name: 'custom' }))
  expect(screen.getByLabelText('Max output tokens')).toHaveValue(null)
  expect(screen.getByLabelText('Min retries')).toHaveValue(null)
  expect(
    screen.getByRole('button', { name: 'Charged with zero output' })
  ).toHaveAttribute('aria-pressed', 'false')
  expect(screen.getByRole('combobox', { name: 'Type' })).toHaveTextContent(
    'Error'
  )
  await user.click(screen.getByRole('button', { name: 'Start Export' }))
  const submitted = request.mock.calls.find(
    (call) => call[0] === 'post' && call[1] === '/api/user/employee/export/jobs'
  )?.[2]
  expect(submitted?.type).toBe(5)
  expect(submitted?.template_id).toBe('custom')
  expect(submitted?.filters).toEqual({})
  client.clear()
})

it('drops conditions removed by a refreshed template before retrying an error export', async () => {
  const client = createAppQueryClient()
  const user = userEvent.setup()
  vi.spyOn(toast, 'error').mockReturnValue('export-error')
  const respond = request.getMockImplementation()
  let restricted = false
  request.mockImplementation(async (method: string, url: string) => {
    if (url.endsWith('/capabilities')) {
      const data = await respond?.(method, url)
      return {
        ...data,
        templates: data.templates.map(
          (template: { allowed_filters: string[] }) => ({
            ...template,
            allowed_filters: restricted ? [] : template.allowed_filters,
          })
        ),
      }
    }
    if (url.includes('/options?field=model_name')) {
      return {
        items: [{ value: 'model_a', label: 'model_a' }],
        next_cursor: '',
      }
    }
    if (
      method === 'post' &&
      url === '/api/user/employee/export/jobs' &&
      !restricted
    ) {
      restricted = true
      throw createServerError({
        success: false,
        message: 'The template has changed. Please retry.',
      })
    }
    return respond?.(method, url)
  })
  render(
    <QueryClientProvider client={client}>
      <EmployeeCustomerExport prefill={{ customerId: 42, logType: '5' }} />
    </QueryClientProvider>
  )
  await user.click(
    await screen.findByRole('button', { name: 'Export customer data' })
  )
  await user.click(screen.getByRole('combobox', { name: 'Model' }))
  await user.click(await screen.findByRole('option', { name: 'model_a' }))
  await user.click(screen.getByText('Troubleshooting filters'))
  await user.type(screen.getByLabelText('Min retries'), '1')
  await waitFor(() => {
    const estimate = [...request.mock.calls]
      .reverse()
      .find((call) => call[1].endsWith('/estimate'))?.[2]
    expect(estimate?.filters).toEqual({
      model_name: 'model_a',
      min_retry_count: 1,
    })
  })
  await user.click(screen.getByRole('button', { name: 'Start Export' }))
  await waitFor(() =>
    expect(
      screen.queryByRole('combobox', { name: 'Model' })
    ).not.toBeInTheDocument()
  )
  expect(await screen.findByText('Scans about 2 rows')).toBeVisible()
  expect(
    screen.queryByText('At most 2 rows before anomaly filtering')
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Start Export' }))
  const attempts = request.mock.calls.filter(
    (call) => call[0] === 'post' && call[1] === '/api/user/employee/export/jobs'
  )
  expect(attempts).toHaveLength(2)
  expect(attempts[0][2].filters).toEqual({
    model_name: 'model_a',
    min_retry_count: 1,
  })
  expect(attempts[1][2].type).toBe(5)
  expect(attempts[1][2].filters).toEqual({})
  client.clear()
})

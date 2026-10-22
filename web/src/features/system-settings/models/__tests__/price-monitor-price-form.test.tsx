/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Toaster, toast } from 'sonner'
import { afterEach, assert, expect, test, vi } from 'vitest'

import {
  combineBillingExpr,
  splitBillingExprAndRequestRules,
} from '@/features/pricing/lib/billing-expr'
import { api } from '@/lib/api'

import { PriceMonitorPriceFormDialog } from '../price-monitor-price-form'

type PricingRecord = Record<string, unknown>

type MockEntry = {
  model_name: string
  version: string
  configured: PricingRecord
  effective: PricingRecord
  completion_locked?: boolean
}

type CapturedChange = {
  model_name: string
  expected_version: string
  pricing: PricingRecord
  reset?: boolean
}

type CapturedApply = {
  changes: CapturedChange[]
}

const UNSUPPORTED_TASK_REASON =
  'Task pricing must be converted manually using the task usage schema.'

function mockApi(options: {
  entries: MockEntry[]
  convert?: (
    model: string,
    pricing: PricingRecord
  ) => Record<string, unknown> | Promise<Record<string, unknown>>
}) {
  const calls = {
    convert: [] as { model_name: string; pricing: PricingRecord }[],
    apply: [] as CapturedApply[],
  }
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/option/model_pricing') {
      return {
        data: {
          success: true,
          data: {
            entries: options.entries,
            empty_version: 'empty',
            token_price_factor: 0.5,
          },
        },
      }
    }
    throw new Error(`unexpected GET ${String(url)}`)
  })
  vi.spyOn(api, 'post').mockImplementation(async (url, body) => {
    const payload = (body ?? {}) as {
      model_name?: string
      pricing?: PricingRecord
      changes?: CapturedChange[]
    }
    if (url === '/api/option/model_pricing/convert') {
      const model = String(payload.model_name)
      calls.convert.push({
        model_name: model,
        pricing: structuredClone(payload.pricing ?? {}),
      })
      if (!options.convert) {
        throw new Error(`unexpected conversion for ${model}`)
      }
      return {
        data: {
          success: true,
          data: await options.convert(model, payload.pricing ?? {}),
        },
      }
    }
    if (url === '/api/price_monitor/preview_price') {
      return {
        data: {
          success: true,
          data: { context_version: 'ctx-1', models: [] },
        },
      }
    }
    if (url === '/api/price_monitor/apply_price') {
      calls.apply.push(structuredClone(payload) as CapturedApply)
      return {
        data: {
          success: true,
          data: {
            updated_models: (payload.changes ?? []).map(
              (change) => change.model_name
            ),
            refreshed: true,
          },
        },
      }
    }
    if (url === '/api/price_monitor/evaluate') {
      return {
        data: {
          success: true,
          data: { results: [], coverage: 'scenarios_only', checked_at: 100 },
        },
      }
    }
    throw new Error(`unexpected POST ${String(url)}`)
  })
  return calls
}

function renderForm(models: string[]) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const props = {
    open: true,
    models,
    checkedAt: 100,
    items: [],
    sources: [],
    onOpenChange: vi.fn(),
    onSaved: vi.fn(async () => {}),
  }
  render(
    <QueryClientProvider client={client}>
      <Toaster />
      <PriceMonitorPriceFormDialog {...props} />
    </QueryClientProvider>
  )
  return props
}

afterEach(() => {
  toast.dismiss()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

test('saves three visual tiers with the existing request rules and blocks an unfinished tier', async () => {
  const rules = '(header("x-plan") == "fast" ? 2.00 : 1)'
  const pricing = {
    'billing_setting.billing_mode': 'tiered_expr',
    'billing_setting.billing_expr': combineBillingExpr(
      'tier("base", p * 5 + c * 25)',
      rules
    ),
  }
  const calls = mockApi({
    entries: [
      {
        model_name: 'three-tiers',
        version: 'v1',
        configured: pricing,
        effective: pricing,
      },
    ],
  })
  renderForm(['three-tiers'])
  const user = userEvent.setup()
  await screen.findByRole('textbox', { name: 'Input price' })
  expect(
    screen.queryByRole('combobox', { name: 'Editor mode' })
  ).not.toBeInTheDocument()
  for (const threshold of ['32000', '128000']) {
    await user.click(screen.getByRole('button', { name: 'Add tier' }))
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled()
    const fields = screen.getAllByRole('textbox', { name: 'Condition value' })
    const field = fields.at(-1)
    assert(field)
    fireEvent.change(field, {
      target: { value: threshold },
    })
  }
  await user.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(calls.apply).toHaveLength(1))
  const expression = String(
    calls.apply[0]?.changes[0]?.pricing['billing_setting.billing_expr']
  )
  expect(expression).toContain('len <= 32000')
  expect(expression).toContain('len <= 128000')
  expect(expression).toContain('tier("base", p * 5 + c * 25)')
  expect(splitBillingExprAndRequestRules(expression).requestRuleExpr).toBe(
    rules
  )
})

test('does not discard an unfinished tier when switching models and recovers after deleting it', async () => {
  const entries = ['model-a', 'model-b'].map((model_name, index) => {
    const pricing = {
      'billing_setting.billing_mode': 'tiered_expr',
      'billing_setting.billing_expr': `tier("base", p * ${index + 5} + c * 25)`,
    }
    return {
      model_name,
      version: 'v1',
      configured: pricing,
      effective: pricing,
    }
  })
  mockApi({ entries })
  renderForm(entries.map((entry) => entry.model_name))
  const user = userEvent.setup()
  fireEvent.change(
    await screen.findByRole('textbox', { name: 'Input price' }),
    { target: { value: '7' } }
  )
  await user.click(screen.getByRole('button', { name: 'Add tier' }))
  expect(screen.getByRole('button', { name: 'model-b' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled()
  expect(
    screen.getByText('Complete this tier before switching models or saving.')
  ).toBeVisible()
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Branch actions 1' }))
  await user.click(screen.getByRole('menuitem', { name: 'Remove branch' }))
  await user.click(screen.getByRole('button', { name: 'model-b' }))
  expect(screen.getByRole('textbox', { name: 'Input price' })).toHaveValue('6')
  await user.click(screen.getByRole('button', { name: 'model-a' }))
  expect(screen.getByRole('textbox', { name: 'Input price' })).toHaveValue('7')
  expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled()
})

test('reloads current pricing when the same dialog is closed and reopened', async () => {
  const pricing = {
    'billing_setting.billing_mode': 'tiered_expr',
    'billing_setting.billing_expr': 'tier("base", p * 5 + c * 25)',
  }
  const options = {
    entries: [
      {
        model_name: 'reopen',
        version: 'v1',
        configured: pricing,
        effective: pricing,
      },
    ],
  }
  mockApi(options)
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const props = {
    models: ['reopen'],
    checkedAt: 100,
    items: [],
    sources: [],
    onOpenChange: vi.fn(),
    onSaved: vi.fn(async () => {}),
  }
  const view = render(
    <QueryClientProvider client={client}>
      <PriceMonitorPriceFormDialog {...props} open />
    </QueryClientProvider>
  )
  expect(
    await screen.findByRole('textbox', { name: 'Input price' })
  ).toHaveValue('5')
  view.rerender(
    <QueryClientProvider client={client}>
      <PriceMonitorPriceFormDialog {...props} open={false} />
    </QueryClientProvider>
  )
  const updated = {
    ...pricing,
    'billing_setting.billing_expr': 'tier("base", p * 9 + c * 25)',
  }
  options.entries = [
    {
      model_name: 'reopen',
      version: 'v2',
      configured: updated,
      effective: updated,
    },
  ]
  view.rerender(
    <QueryClientProvider client={client}>
      <PriceMonitorPriceFormDialog {...props} open />
    </QueryClientProvider>
  )
  await waitFor(() =>
    expect(screen.getByRole('textbox', { name: 'Input price' })).toHaveValue(
      '9'
    )
  )
})

test('legacy per-token model auto-converts on open and edits in the visual editor', async () => {
  vi.stubGlobal(
    'confirm',
    vi.fn(() => true)
  )
  const calls = mockApi({
    entries: [
      {
        model_name: 'legacy-token',
        version: 'v1',
        configured: { ModelRatio: 2, CompletionRatio: 4 },
        effective: { ModelRatio: 2, CompletionRatio: 4 },
        completion_locked: false,
      },
    ],
    convert: () => ({
      effective: { ModelRatio: 2, CompletionRatio: 4 },
      expression: 'tier("base", p * 2 + c * 4)',
    }),
  })
  const props = renderForm(['legacy-token'])
  // The visual editor shows tier fields directly; no conversion dialog opens.
  const input = await screen.findByRole('textbox', { name: 'Input price' })
  expect(input).toHaveValue('2')
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(calls.convert).toEqual([
    {
      model_name: 'legacy-token',
      pricing: { ModelRatio: 2, CompletionRatio: 4 },
    },
  ])
  fireEvent.change(input, { target: { value: '3' } })
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(calls.apply).toHaveLength(1))
  expect(props.onOpenChange).toHaveBeenCalledWith(false)
  const change = calls.apply[0]?.changes[0]
  expect(change?.model_name).toBe('legacy-token')
  expect(change?.expected_version).toBe('v1')
  expect(change?.pricing['billing_setting.billing_mode']).toBe('tiered_expr')
  expect(change?.pricing['billing_setting.billing_expr']).toContain('p * 3')
  // Legacy fields stay in the draft as historical configuration.
  expect(change?.pricing.ModelRatio).toBe(2)
  expect(change?.pricing.CompletionRatio).toBe(4)
})

test('legacy per-request model converts to a fixed price and saves the edited expression', async () => {
  vi.stubGlobal(
    'confirm',
    vi.fn(() => true)
  )
  const calls = mockApi({
    entries: [
      {
        model_name: 'legacy-fixed',
        version: 'v9',
        configured: { ModelPrice: 0.01 },
        effective: { ModelPrice: 0.01 },
      },
    ],
    convert: () => ({
      effective: { ModelPrice: 0.01 },
      expression: 'tier("request", fixed(0.01))',
    }),
  })
  renderForm(['legacy-fixed'])
  const input = await screen.findByRole('textbox', {
    name: 'Price per request',
  })
  expect(input).toHaveValue('0.01')
  fireEvent.change(input, { target: { value: '0.02' } })
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(calls.apply).toHaveLength(1))
  const change = calls.apply[0]?.changes[0]
  expect(change?.expected_version).toBe('v9')
  expect(change?.pricing['billing_setting.billing_mode']).toBe('tiered_expr')
  expect(change?.pricing['billing_setting.billing_expr']).toBe(
    'tier("request", fixed(0.02))'
  )
  expect(change?.pricing.ModelPrice).toBe(0.01)
})

test('change summary compares converted prices against legacy effective prices', async () => {
  vi.stubGlobal(
    'confirm',
    vi.fn(() => true)
  )
  mockApi({
    entries: [
      {
        model_name: 'legacy-token',
        version: 'v1',
        configured: { ModelRatio: 2, CompletionRatio: 4 },
        // effective carries inherited default ratios that are not configured;
        // the expression inherits them the same way, so they are not changes.
        effective: {
          ModelRatio: 2,
          CompletionRatio: 4,
          CreateCacheRatio: 5,
          ImageRatio: 3,
        },
        completion_locked: false,
      },
    ],
    convert: () => ({
      effective: { ModelRatio: 2, CompletionRatio: 4 },
      // token_price_factor is 0.5: ModelRatio 2 / CompletionRatio 4 are $1 / $4.
      expression: 'tier("base", p * 1 + c * 4)',
    }),
  })
  renderForm(['legacy-token'])
  await screen.findByRole('textbox', { name: 'Input price' })
  // Conversion alone shows the conversion note instead of rows that pretend
  // the prices used to be unpriced.
  expect(
    await screen.findByText('Converted to dynamic pricing', { exact: false })
  ).toBeInTheDocument()
  expect(
    screen.queryByText(
      (_content, element) =>
        element?.tagName === 'P' &&
        (element.textContent ?? '').includes('Not separately priced →')
    )
  ).not.toBeInTheDocument()
  // Editing compares against the legacy effective price, not "unpriced".
  fireEvent.change(screen.getByRole('textbox', { name: 'Input price' }), {
    target: { value: '3' },
  })
  expect(
    await screen.findByText(
      (_content, element) =>
        element?.tagName === 'P' &&
        element.textContent === '0–∞ · Input price: $1 → $3'
    )
  ).toBeInTheDocument()
})

test('an already expression-backed model opens without a conversion call', async () => {
  vi.stubGlobal(
    'confirm',
    vi.fn(() => true)
  )
  const pricing = {
    'billing_setting.billing_mode': 'tiered_expr',
    'billing_setting.billing_expr': 'tier("request", fixed(2))',
  }
  const calls = mockApi({
    entries: [
      {
        model_name: 'fixed-model',
        version: 'v3',
        configured: pricing,
        effective: pricing,
      },
    ],
  })
  renderForm(['fixed-model'])
  expect(
    await screen.findByRole('textbox', { name: 'Price per request' })
  ).toHaveValue('2')
  expect(calls.convert).toHaveLength(0)
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
})

test('unsupported conversion lists the problem, blocks saving and shows the reason in the editor area', async () => {
  vi.stubGlobal(
    'confirm',
    vi.fn(() => true)
  )
  const calls = mockApi({
    entries: [
      {
        model_name: 'legacy-task',
        version: 'v1',
        configured: { ModelRatio: 2 },
        effective: { ModelRatio: 2 },
        completion_locked: false,
      },
    ],
    convert: () => ({
      effective: { ModelRatio: 2 },
      unsupported_reason: UNSUPPORTED_TASK_REASON,
    }),
  })
  renderForm(['legacy-task'])
  // The problems alert lists the model with the server's reason.
  await screen.findByText(
    (_content, element) =>
      element instanceof HTMLSpanElement &&
      element.textContent === `legacy-task: ${UNSUPPORTED_TASK_REASON}`
  )
  // The editor area shows the reason instead of the editor.
  expect(
    screen.getByText(UNSUPPORTED_TASK_REASON, { selector: 'p' })
  ).toBeInTheDocument()
  expect(
    screen.queryByRole('combobox', { name: 'Editor mode' })
  ).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled()
  expect(calls.apply).toHaveLength(0)
})

test('batch sessions auto-convert legacy entries and submit only changed models', async () => {
  vi.stubGlobal(
    'confirm',
    vi.fn(() => true)
  )
  const expressionPricing = {
    'billing_setting.billing_mode': 'tiered_expr',
    'billing_setting.billing_expr': 'tier("base", p * 2)',
  }
  const calls = mockApi({
    entries: [
      {
        model_name: 'expr-a',
        version: 'v1',
        configured: expressionPricing,
        effective: expressionPricing,
      },
      {
        model_name: 'legacy-b',
        version: 'v2',
        configured: { ModelRatio: 1 },
        effective: { ModelRatio: 1 },
        completion_locked: false,
      },
    ],
    convert: (model) => {
      if (model !== 'legacy-b') throw new Error(`unexpected model ${model}`)
      return { effective: { ModelRatio: 1 }, expression: 'tier("base", p * 1)' }
    },
  })
  renderForm(['expr-a', 'legacy-b'])
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled()
  )
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(calls.convert.map((call) => call.model_name)).toEqual(['legacy-b'])
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(calls.apply).toHaveLength(1))
  const changes = calls.apply[0]?.changes ?? []
  // The untouched expression model is not submitted.
  expect(changes.map((change) => change.model_name)).toEqual(['legacy-b'])
  expect(changes[0]?.expected_version).toBe('v2')
  expect(changes[0]?.pricing['billing_setting.billing_expr']).toBe(
    'tier("base", p * 1)'
  )
  expect(changes[0]?.pricing.ModelRatio).toBe(1)
})

test('a percentage batch adjustment remounts the active editor with scaled prices', async () => {
  vi.stubGlobal(
    'confirm',
    vi.fn(() => true)
  )
  const expressionPricing = {
    'billing_setting.billing_mode': 'tiered_expr',
    'billing_setting.billing_expr': 'tier("base", p * 2)',
  }
  const calls = mockApi({
    entries: [
      {
        model_name: 'expr-a',
        version: 'v1',
        configured: expressionPricing,
        effective: expressionPricing,
      },
      {
        model_name: 'legacy-b',
        version: 'v2',
        configured: { ModelRatio: 1 },
        effective: { ModelRatio: 1 },
        completion_locked: false,
      },
    ],
    convert: (model) => {
      if (model !== 'legacy-b') throw new Error(`unexpected model ${model}`)
      return { effective: { ModelRatio: 1 }, expression: 'tier("base", p * 1)' }
    },
  })
  renderForm(['expr-a', 'legacy-b'])
  const input = await screen.findByRole('textbox', { name: 'Input price' })
  expect(input).toHaveValue('2')
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled()
  )
  await userEvent.click(
    screen.getByRole('button', { name: 'Increase by percent' })
  )
  fireEvent.change(screen.getByRole('textbox', { name: 'Percentage' }), {
    target: { value: '10' },
  })
  await userEvent.click(
    screen.getByRole('button', { name: 'Apply adjustment' })
  )
  // The editor remounts with the adjusted expression instead of its own state.
  await waitFor(() =>
    expect(screen.getByRole('textbox', { name: 'Input price' })).toHaveValue(
      '2.2'
    )
  )
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(calls.apply).toHaveLength(1))
  const changes = calls.apply[0]?.changes ?? []
  expect(changes.map((change) => change.model_name).sort()).toEqual([
    'expr-a',
    'legacy-b',
  ])
  expect(savedExpression(changes, 'expr-a')).toContain('2.2')
})

test('saving stays blocked while the active editor has invalid fields', async () => {
  vi.stubGlobal(
    'confirm',
    vi.fn(() => true)
  )
  const pricing = {
    'billing_setting.billing_mode': 'tiered_expr',
    'billing_setting.billing_expr': 'tier("request", fixed(0.01))',
  }
  mockApi({
    entries: [
      {
        model_name: 'fixed-model',
        version: 'v3',
        configured: pricing,
        effective: pricing,
      },
    ],
  })
  renderForm(['fixed-model'])
  const input = await screen.findByRole('textbox', {
    name: 'Price per request',
  })
  fireEvent.change(input, { target: { value: '0.02' } })
  const saveButton = screen.getByRole('button', { name: 'Save changes' })
  await waitFor(() => expect(saveButton).toBeEnabled())
  // An incomplete editor field blocks saving; the draft keeps the last value.
  fireEvent.change(input, { target: { value: '' } })
  await waitFor(() => expect(saveButton).toBeDisabled())
  fireEvent.change(input, { target: { value: '0.03' } })
  await waitFor(() => expect(saveButton).toBeEnabled())
})

test('restoring defaults clears the model conversion problem and unblocks saving', async () => {
  vi.stubGlobal(
    'confirm',
    vi.fn(() => true)
  )
  const calls = mockApi({
    entries: [
      {
        model_name: 'legacy-fail',
        version: 'v2',
        configured: { ModelRatio: 1 },
        effective: { ModelRatio: 1 },
        completion_locked: false,
      },
    ],
    convert: () => ({
      effective: {},
      unsupported_reason: UNSUPPORTED_TASK_REASON,
    }),
  })
  renderForm(['legacy-fail'])
  await screen.findByText(
    (_content, element) =>
      element instanceof HTMLSpanElement &&
      element.textContent === `legacy-fail: ${UNSUPPORTED_TASK_REASON}`
  )
  expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled()
  await userEvent.click(
    screen.getByRole('button', { name: 'Restore default pricing' })
  )
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled()
  )
  expect(
    screen.queryByText((_content, element) =>
      Boolean(element?.textContent?.includes(UNSUPPORTED_TASK_REASON))
    )
  ).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(calls.apply).toHaveLength(1))
  const change = calls.apply[0]?.changes[0]
  expect(change?.model_name).toBe('legacy-fail')
  expect(change?.expected_version).toBe('v2')
  expect(change?.reset).toBe(true)
})

test('splitting a priced range compares against its existing price', async () => {
  const pricing = {
    'billing_setting.billing_mode': 'tiered_expr',
    'billing_setting.billing_expr': 'tier("base", p * 5 + c * 25)',
  }
  mockApi({
    entries: [
      {
        model_name: 'split-model',
        version: 'v1',
        configured: pricing,
        effective: pricing,
      },
    ],
  })
  renderForm(['split-model'])
  await screen.findByRole('textbox', { name: 'Input price' })
  await userEvent.click(screen.getByRole('button', { name: 'Add tier' }))
  await userEvent.type(
    screen.getByRole('textbox', { name: 'Condition value' }),
    '128000'
  )
  fireEvent.change(screen.getByRole('textbox', { name: 'Input price' }), {
    target: { value: '7' },
  })
  expect(screen.queryByText(/Not separately priced/)).not.toBeInTheDocument()
  expect(screen.getByText(/\$5.*\$7/)).toBeInTheDocument()
})

test('merging differently priced ranges requests scenario comparison', async () => {
  const pricing = {
    'billing_setting.billing_mode': 'tiered_expr',
    'billing_setting.billing_expr':
      'len <= 128000 ? tier("short", p * 3) : tier("base", p * 5)',
  }
  mockApi({
    entries: [
      {
        model_name: 'merge-model',
        version: 'v1',
        configured: pricing,
        effective: pricing,
      },
    ],
  })
  renderForm(['merge-model'])
  await screen.findByRole('textbox', { name: 'Input price' })
  await userEvent.click(
    screen.getByRole('button', { name: 'Branch actions 1' })
  )
  await userEvent.click(screen.getByRole('menuitem', { name: 'Remove branch' }))
  expect(screen.queryByText(/Not separately priced/)).not.toBeInTheDocument()
  expect(
    screen.getByText(
      /Custom pricing rules changed; compare an example before saving./
    )
  ).toBeInTheDocument()
})

function savedExpression(changes: CapturedChange[], model: string): string {
  const change = changes.find((candidate) => candidate.model_name === model)
  return String(change?.pricing['billing_setting.billing_expr'] ?? '')
}

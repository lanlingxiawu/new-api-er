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
  flexRender,
  getCoreRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterAll, afterEach, beforeEach, expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import type { UsageLog } from '../../data/schema'
import type { LogOtherData } from '../../types'
import { useCommonLogsColumns } from '../columns/common-logs-columns'

vi.mock('@lobehub/icons', () => ({}))
vi.hoisted(() => {
  vi.stubGlobal('localStorage', {
    getItem: () => null,
    setItem: () => undefined,
    removeItem: () => undefined,
  })
})
afterAll(() => vi.unstubAllGlobals())

function makeLog(other: LogOtherData, type = 2): UsageLog {
  return {
    id: 1,
    user_id: 1,
    created_at: 1,
    type,
    content: '',
    username: 'user',
    token_name: 'token',
    model_name: 'wan2.5-i2v-preview',
    quota: 5000,
    prompt_tokens: 0,
    completion_tokens: 0,
    use_time: 0,
    is_stream: false,
    channel: 1,
    channel_name: '',
    token_id: 1,
    group: 'default',
    ip: '',
    other: JSON.stringify(other),
    request_id: 'req-1',
    upstream_request_id: '',
  }
}

function DetailPreview(props: {
  other: LogOtherData
  isAdmin: boolean
  canUseAdminActions?: boolean
  type?: number
  ids?: Pick<UsageLog, 'request_id' | 'upstream_request_id'>
}) {
  const table = useReactTable({
    data: [{ ...makeLog(props.other, props.type), ...props.ids }],
    columns: useCommonLogsColumns(
      props.isAdmin,
      false,
      props.canUseAdminActions
    ),
    getCoreRowModel: getCoreRowModel(),
  })
  const cell = table
    .getRowModel()
    .rows[0].getAllCells()
    .find((item) => item.column.id === 'content')
  if (!cell) throw new Error('The log must have a content column')
  return flexRender(cell.column.columnDef.cell, cell.getContext())
}
const plugin = {
  key: 'incho',
  name: 'Incho',
  version: '1.0.1',
  author: { name: 'Plugin maintainer' },
}
const previousConfig = useSystemConfigStore.getState().config
let client: QueryClient
const i18n = createInstance()
beforeEach(async () => {
  await i18n.init({
    lng: 'en',
    resources: { en },
    interpolation: { escapeValue: false },
  })
  useSystemConfigStore
    .getState()
    .setConfig({ currency: { ...DEFAULT_CURRENCY_CONFIG } })
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  client.setQueryData(['status'], {}, { updatedAt: Date.now() + 60_000 })
  client.setQueryData(
    ['pricing'],
    { data: [], vendors: [] },
    { updatedAt: Date.now() + 60_000 }
  )
})
afterEach(() => {
  client.clear()
  useSystemConfigStore.getState().setConfig(previousConfig)
})
function renderPreview(
  other: LogOtherData,
  isAdmin = true,
  type?: number,
  canUseAdminActions?: boolean,
  ids?: Pick<UsageLog, 'request_id' | 'upstream_request_id'>
) {
  render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <DetailPreview
          other={other}
          isAdmin={isAdmin}
          type={type}
          canUseAdminActions={canUseAdminActions}
          ids={ids}
        />
      </QueryClientProvider>
    </I18nextProvider>
  )
  return screen.getByRole('button', { name: /./ })
}

test.each([
  {
    other: {
      stream_status: { status: 'error', end_reason: 'client disconnected' },
    },
    label: 'Stream Status',
  },
  { other: { reasoning_effort: 'high' }, label: 'Reasoning Effort' },
  { other: { is_system_prompt_overwritten: true }, label: 'System Prompt' },
  {
    other: { is_model_mapped: true, upstream_model_name: 'mapped-model' },
    label: 'Model Mapping',
  },
] satisfies Array<{ other: LogOtherData; label: string }>)(
  'keeps public $label visible without diagnostic navigation for self views',
  async ({ other, label }) => {
    fireEvent.click(renderPreview(other, false, 2, false))
    const dialog = within(await screen.findByRole('dialog'))
    expect(dialog.queryByRole('tab')).not.toBeInTheDocument()
    expect(dialog.getByText(label)).toBeVisible()
    expect(dialog.queryByText('Upstream exchange')).not.toBeInTheDocument()
    expect(dialog.queryByText('Root Diagnostics')).not.toBeInTheDocument()
  }
)

test.each([
  {
    name: 'successful stream',
    other: { stream_status: { status: 'ok' } },
    type: 2,
  },
  {
    name: 'mapping without a target',
    other: { is_model_mapped: true },
    type: 2,
  },
  {
    name: 'empty conversion chain',
    other: { request_conversion: [''] },
    type: 2,
  },
  {
    name: 'refund conversion',
    other: { request_path: '/v1/chat/completions' },
    type: 6,
  },
] satisfies Array<{ name: string; other: LogOtherData; type: number }>)(
  'does not show an empty diagnostic tab for $name',
  async ({ other, type }) => {
    fireEvent.click(renderPreview(other, true, type))
    const dialog = within(await screen.findByRole('dialog'))
    expect(dialog.queryByRole('tab')).not.toBeInTheDocument()
  }
)

test.each([
  { viewer: 'employee', showAdminFields: true, canUseAdminActions: false },
  { viewer: 'admin', showAdminFields: true, canUseAdminActions: true },
  { viewer: 'user', showAdminFields: false, canUseAdminActions: false },
])(
  'shows request policy decisions for the $viewer log view without granting admin actions',
  async ({ showAdminFields, canUseAdminActions }) => {
    fireEvent.click(
      renderPreview(
        {
          admin_info: {
            use_channel: [275, 275],
            request_policy: [
              {
                attempt: 1,
                channel_id: 275,
                group: 'Gemini_Vertex',
                elapsed_ms: 226,
                decision: {
                  action: 'select',
                  reason: 'channel_selected',
                  source: 'routing',
                },
              },
              {
                attempt: 1,
                channel_id: 275,
                elapsed_ms: 1142,
                status: 429,
                decision: {
                  action: 'failure',
                  reason: 'upstream_failure',
                  source: 'upstream',
                },
              },
              {
                attempt: 2,
                channel_id: 275,
                elapsed_ms: 2033,
                decision: {
                  action: 'stop',
                  reason: 'attempt_budget_exhausted',
                  source: 'global',
                },
              },
            ],
          },
        },
        showAdminFields,
        5,
        canUseAdminActions
      )
    )
    const dialog = within(await screen.findByRole('dialog'))
    if (showAdminFields) {
      expect(dialog.getByText('Request policy decisions')).toBeVisible()
      const flow = within(
        dialog.getByRole('list', { name: 'Request decision flow' })
      )
      const steps = flow.getAllByRole('listitem')
      expect(steps).toHaveLength(3)
      expect(steps[0]).toHaveTextContent('Channel selected · #275')
      expect(steps[0]).toHaveTextContent('Attempt 1 · 226 ms · Gemini_Vertex')
      expect(steps[1]).toHaveTextContent(
        'Upstream request failed · #275 · HTTP 429'
      )
      expect(steps[2]).toHaveTextContent('Total attempt budget exhausted')
      expect(steps[2]).toHaveTextContent('Source: Global default')
    } else {
      expect(
        dialog.queryByText('Request policy decisions')
      ).not.toBeInTheDocument()
      expect(
        dialog.queryByRole('list', { name: 'Request decision flow' })
      ).not.toBeInTheDocument()
    }
    if (canUseAdminActions) {
      expect(dialog.getByRole('button', { name: 'Find similar' })).toBeVisible()
    } else {
      expect(
        dialog.queryByRole('button', { name: 'Find similar' })
      ).not.toBeInTheDocument()
    }
  }
)

test.each([undefined, []])(
  'omits the request policy section when an employee log has no decisions (%j)',
  async (events) => {
    fireEvent.click(
      renderPreview({ admin_info: { request_policy: events } }, true, 5, false)
    )
    const dialog = within(await screen.findByRole('dialog'))
    expect(
      dialog.queryByText('Request policy decisions')
    ).not.toBeInTheDocument()
    expect(
      dialog.queryByRole('list', { name: 'Request decision flow' })
    ).not.toBeInTheDocument()
  }
)

test.each([
  { viewer: 'employee', fields: true, actions: false },
  { viewer: 'admin', fields: true, actions: true },
  { viewer: 'ordinary user', fields: false, actions: false },
])(
  'limits upstream milestones for $viewer without granting admin actions',
  async ({ fields, actions }) => {
    fireEvent.click(
      renderPreview(
        {
          admin_info: {
            upstream_exchange: [
              { conn_ms: 0, wrote_ms: 5, first_byte_ms: 28, end_ms: 29 },
              { conn_ms: 1000 },
            ],
          },
        },
        fields,
        2,
        actions
      )
    )
    const dialog = within(await screen.findByRole('dialog'))
    if (fields) {
      fireEvent.click(dialog.getByRole('tab', { name: 'Request diagnostics' }))
      expect(dialog.getByText('Upstream exchange')).toBeVisible()
      expect(dialog.getByText('Attempt 1')).toBeVisible()
      expect(dialog.getByText('Attempt 2')).toBeVisible()
      expect(dialog.getByText('0 ms')).toBeVisible()
      expect(dialog.getAllByText('Not observed')).toHaveLength(3)
      expect(dialog.getAllByText('HTTP call returned')).toHaveLength(2)
      expect(dialog.queryByText(/Declared body size:/)).not.toBeInTheDocument()
    } else {
      expect(dialog.queryByText('Upstream exchange')).not.toBeInTheDocument()
      expect(dialog.queryByText('0 ms')).not.toBeInTheDocument()
    }
    expect(
      dialog.queryByRole('button', { name: 'Query Upstream' })
    ).not.toBeInTheDocument()
    expect(dialog.queryByText('Root Diagnostics')).not.toBeInTheDocument()
  }
)

test('shows observed intervals and known size without legacy protocol metadata', async () => {
  const attempt = {
    conn_ms: 43,
    wrote_ms: 639000,
    first_byte_ms: 655000,
    end_ms: 655000,
    wrote_ok: true,
    request_body_bytes: 327680,
    protocol: 'HTTP/2.0',
  }
  fireEvent.click(
    renderPreview({
      admin_info: {
        upstream_exchange: [attempt],
      },
    })
  )
  const dialog = within(await screen.findByRole('dialog'))
  fireEvent.click(dialog.getByRole('tab', { name: 'Request diagnostics' }))
  const cardElement = dialog.getByText('Upstream exchange').parentElement
  if (!cardElement) throw new Error('The exchange card must be present')
  const card = within(cardElement)
  expect(
    card.getByText('Connection → write end').parentElement
  ).toHaveTextContent('10m 39s')
  expect(
    card.getByText('Write end → first byte').parentElement
  ).toHaveTextContent('16.0s')
  expect(card.getByText('Completed')).toBeVisible()
  expect(card.getByText('Declared body size: 327,680 B')).toBeVisible()
  expect(card.queryByText(/Protocol:/)).not.toBeInTheDocument()
  expect(card.queryByText('Before first connection')).not.toBeInTheDocument()
  expect(card.queryByText('Acquiring connection')).not.toBeInTheDocument()
})

test('separates connection acquisition without counting earlier attempts as preparation', async () => {
  fireEvent.click(
    renderPreview({
      admin_info: {
        upstream_exchange: [
          {
            get_conn_ms: 100,
            conn_ms: 1600,
            wrote_ms: 1600,
            first_byte_ms: 29700,
            end_ms: 29700,
          },
          { get_conn_ms: 30000, conn_ms: 30010, end_ms: 30020 },
        ],
      },
    })
  )
  const dialog = within(await screen.findByRole('dialog'))
  fireEvent.click(dialog.getByRole('tab', { name: 'Request diagnostics' }))
  expect(dialog.getAllByText('Before first connection')).toHaveLength(1)
  expect(
    dialog.getByText('Before first connection').parentElement
  ).toHaveTextContent('100 ms')
  const acquisitions = dialog.getAllByText('Acquiring connection')
  expect(acquisitions[0].parentElement).toHaveTextContent('1.5s')
  expect(acquisitions[1].parentElement).toHaveTextContent('10 ms')
  expect(
    dialog.getByText('Write end → first byte').parentElement
  ).toHaveTextContent('28.1s')
})

test.each([
  { get_conn_ms: 0, conn_ms: 0, expected: '0 ms' },
  { get_conn_ms: 20, conn_ms: 10, expected: undefined },
  { get_conn_ms: -1, conn_ms: 10, expected: undefined },
  { get_conn_ms: 10, conn_ms: undefined, expected: undefined },
])(
  'validates acquisition timing $get_conn_ms → $conn_ms',
  async ({ get_conn_ms, conn_ms, expected }) => {
    fireEvent.click(
      renderPreview({
        admin_info: {
          upstream_exchange: [{ get_conn_ms, conn_ms, end_ms: 30 }],
        },
      })
    )
    const dialog = within(await screen.findByRole('dialog'))
    fireEvent.click(dialog.getByRole('tab', { name: 'Request diagnostics' }))
    if (expected) {
      expect(
        dialog.getByText('Acquiring connection').parentElement
      ).toHaveTextContent(expected)
      expect(
        dialog.getByText('Before first connection').parentElement
      ).toHaveTextContent(expected)
    } else {
      expect(dialog.queryByText('Acquiring connection')).not.toBeInTheDocument()
    }
  }
)

test.each([
  { wrote_ok: false, expected: 'Failed' },
  { wrote_ok: undefined, expected: 'Result not recorded' },
])(
  'preserves first-write status: $expected',
  async ({ wrote_ok, expected }) => {
    fireEvent.click(
      renderPreview({
        admin_info: {
          upstream_exchange: [
            {
              conn_ms: 0,
              wrote_ms: 10,
              first_byte_ms: 5,
              end_ms: 20,
              wrote_ok,
              request_body_bytes: 0,
            },
          ],
        },
      })
    )
    const dialog = within(await screen.findByRole('dialog'))
    fireEvent.click(dialog.getByRole('tab', { name: 'Request diagnostics' }))
    const cardElement = dialog.getByText('Upstream exchange').parentElement
    if (!cardElement) throw new Error('The exchange card must be present')
    const card = within(cardElement)
    expect(card.getByText('First write ended')).toBeVisible()
    expect(card.getByText(expected)).toBeVisible()
    expect(card.getByText('Declared body size: 0 B')).toBeVisible()
    expect(card.queryByText(/Protocol:/)).not.toBeInTheDocument()
    expect(
      card.getByText('The first byte arrived before the request write ended.')
    ).toBeVisible()
    expect(card.queryByText('Write end → first byte')).not.toBeInTheDocument()
  }
)

test.each([true, false])(
  'keeps request IDs without inventing timing diagnostics: privileged=%s',
  async (privileged) => {
    fireEvent.click(
      renderPreview({}, privileged, 2, false, {
        request_id: '202611210341066442531858268d9d6olW2bnr0',
        upstream_request_id: '202611210341091838560558268d9d6jCf5Hw0x',
      })
    )
    const dialog = within(await screen.findByRole('dialog'))
    expect(
      dialog.queryByRole('tab', { name: 'Request diagnostics' })
    ).not.toBeInTheDocument()
    expect(dialog.queryByText('Upstream exchange')).not.toBeInTheDocument()
    expect(
      dialog.getByText('202611210341066442531858268d9d6olW2bnr0')
    ).toBeVisible()
    expect(
      dialog.getByText('202611210341091838560558268d9d6jCf5Hw0x')
    ).toBeVisible()
  }
)

test.each([true, false])(
  'channel rejection diagnostics respect the privileged log view=%s',
  async (privileged) => {
    const preview = renderPreview(
      {
        admin_info: {
          user_channel_routing: {
            reason: 'no_eligible_channel_after_user_exclusion',
            phase: 'selection',
            excluded_count: 30,
            rejected_count: 25,
            channel_ids: [125001, 125002],
            truncated: true,
          },
        },
      },
      privileged,
      5
    )
    fireEvent.click(preview)
    const dialog = within(await screen.findByRole('dialog'))
    if (privileged) {
      expect(dialog.getByText('User channel routing')).toBeVisible()
      expect(
        dialog.getByText('No available channels remain after user exclusions')
      ).toBeVisible()
      expect(dialog.getByText('125001, 125002')).toBeVisible()
      expect(
        dialog.getByText('Only a sample of rejected channels is shown.')
      ).toBeVisible()
    } else {
      expect(dialog.queryByText('User channel routing')).not.toBeInTheDocument()
      expect(
        dialog.queryByText('no_eligible_channel_after_user_exclusion')
      ).not.toBeInTheDocument()
      expect(dialog.queryByText('125001, 125002')).not.toBeInTheDocument()
    }
  }
)

test.each([
  [
    'no_eligible_channel_after_user_exclusion',
    'selection',
    '排除后无可用渠道',
    '渠道选择',
  ],
  ['user_channel_excluded', 'pin', '该渠道已被此用户排除', '指定渠道'],
  [
    'user_channel_policy_unavailable',
    'strict_affinity',
    '无法读取用户渠道排除规则',
    '严格亲和',
  ],
  [
    'candidates_excluded',
    'origin_task',
    '已跳过被排除的候选渠道',
    '原任务渠道',
  ],
  [
    'user_channel_excluded',
    'origin_task_pin',
    '该渠道已被此用户排除',
    '原任务指定渠道',
  ],
  [
    'user_channel_excluded',
    'midjourney_origin_task',
    '该渠道已被此用户排除',
    'Midjourney 原任务渠道',
  ],
  [
    'user_channel_excluded',
    'websocket_create',
    '该渠道已被此用户排除',
    'WebSocket 请求创建',
  ],
  ['future_reason', 'future_phase', 'future_reason', 'future_phase'],
])(
  'localizes channel routing reason %s and phase %s',
  async (reason, phase, reasonLabel, phaseLabel) => {
    i18n.addResourceBundle('zh-CN', 'translation', zh.translation, true, true)
    await i18n.changeLanguage('zh-CN')
    fireEvent.click(
      renderPreview(
        {
          admin_info: {
            user_channel_routing: {
              reason,
              phase,
              excluded_count: 1,
              rejected_count: 1,
              channel_ids: [125001],
              truncated: false,
            },
          },
        },
        true,
        5
      )
    )
    const dialog = within(await screen.findByRole('dialog'))
    expect(dialog.getByText(reasonLabel)).toBeVisible()
    expect(dialog.getByText(phaseLabel)).toBeVisible()
    if (reason !== reasonLabel) {
      expect(dialog.queryByText(reason)).not.toBeInTheDocument()
    }
    if (phase !== phaseLabel) {
      expect(dialog.queryByText(phase)).not.toBeInTheDocument()
    }
  }
)

test('keeps log details open when the parent refreshes with unchanged data', async () => {
  const other = { model_price: 0.25 }
  const wrapper = ({ children }: { children: React.ReactNode }) => (
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    </I18nextProvider>
  )
  const { rerender } = render(<DetailPreview other={other} isAdmin />, {
    wrapper,
  })
  fireEvent.click(screen.getByRole('button', { name: 'Per-call · $0.25' }))
  expect(await screen.findByRole('dialog')).toBeVisible()
  rerender(<DetailPreview other={other} isAdmin />)
  expect(screen.getByRole('dialog')).toBeVisible()
})

test.each([
  {
    name: 'fixed expression zero price',
    other: {
      billing_mode: 'tiered_expr',
      billing_unit: 'request' as const,
      fixed_price: 0,
      matched_tier: 'free',
      expr_b64: btoa('tier("free", fixed(0))'),
    },
    expected: 'free · Per-call $0/request',
  },
  {
    name: 'fixed expression trace outside the display grammar',
    other: {
      billing_mode: 'tiered_expr',
      billing_unit: 'request' as const,
      fixed_price: 0.01,
      matched_tier: 'priority',
      expr_b64: btoa(
        'param("fast") == true ? tier("priority", fixed(0.01)) : tier("tokens", p * 2)'
      ),
    },
    expected: 'priority · Per-call $0.01/request',
  },
  {
    name: 'parsed token expression',
    other: {
      billing_mode: 'tiered_expr',
      matched_tier: 'standard',
      expr_b64: btoa('tier("standard", p * 2 + c * 10)'),
    },
    expected: 'standard · $2 / $10/M',
  },
  {
    name: 'per-call',
    other: { model_price: 0.25 },
    expected: 'Per-call · $0.25',
  },
  {
    name: 'standard',
    other: { model_ratio: 1, completion_ratio: 2 },
    expected: 'Standard · $2 / $4/M',
  },
  {
    name: 'zero price fallback',
    other: { model_price: 0, group_ratio: 1 },
    expected: 'Group Ratio 1x',
  },
  { name: 'missing price fallback', other: {}, expected: '—' },
])('$name stays visible without a plugin counter', ({ other, expected }) => {
  const preview = renderPreview({
    ...other,
    admin_info: { task_plugin: plugin },
  })
  expect(preview.textContent).toBe(expected)
})

test.each([
  {
    tier: 'standard',
    billingUnit: 'token' as const,
    fixedPrice: undefined,
    expected: 'Dynamic Pricing · standard',
  },
  {
    tier: 'long_context',
    billingUnit: 'token' as const,
    fixedPrice: undefined,
    expected: 'Dynamic Pricing · long_context',
  },
  {
    tier: 'free_small_output',
    billingUnit: 'request' as const,
    fixedPrice: 0,
    expected: 'free_small_output · Per-call $0/request',
  },
])(
  'nested expression preserves the recorded $tier settlement in the list',
  ({ tier, billingUnit, fixedPrice, expected }) => {
    const expression = `len <= 272000
      ? (c > 5
        ? tier("standard", p * 2 + c * 10 + cr * 0.1 + cc * 2.5)
        : tier("free_small_output", fixed(0)))
      : (c > 5
        ? tier("long_context", p * 5 + c * 22.5 + cr * 0.5)
        : tier("free_small_output", fixed(0)))`
    const preview = renderPreview({
      billing_mode: 'tiered_expr',
      billing_unit: billingUnit,
      fixed_price: fixedPrice,
      matched_tier: tier,
      expr_b64: btoa(expression),
    })
    expect(preview.textContent).toBe(expected)
  }
)

test.each([
  { name: 'missing expression', expr: undefined },
  { name: 'invalid encoded expression', expr: '%' },
  {
    name: 'tier absent from the parsed expression',
    expr: btoa('tier("different", p * 99 + c * 99)'),
  },
])(
  'token pricing with $name preserves the saved tier without guessing prices',
  ({ expr }) => {
    const preview = renderPreview({
      billing_mode: 'tiered_expr',
      matched_tier: 'recorded',
      expr_b64: expr,
    })
    expect(preview.textContent).toBe('Dynamic Pricing · recorded')
  }
)

test.each([false, true])(
  'dynamic pricing without a saved tier uses a localized neutral fallback for task=%s',
  async (isTask) => {
    i18n.addResourceBundle('zh', 'translation', zh.translation)
    const preview = renderPreview({
      is_task: isTask,
      billing_mode: 'tiered_expr',
      expr_b64: btoa(
        isTask
          ? 'tier("base", u("clips") * 0.25)'
          : 'tier("base", p * 2 + c * 10)'
      ),
    })
    expect(preview.textContent).toBe(
      'Dynamic Pricing · Special billing expression'
    )
    await act(() => i18n.changeLanguage('zh'))
    expect(
      screen.getByRole('button', { name: '动态计费 · 特殊计费表达式' })
    ).toBeVisible()
  }
)

test('top-up content_parts render in the viewer language', async () => {
  i18n.addResourceBundle('zh', 'translation', {
    'Online top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}':
      '在线充值成功，充值额度：{{quota}}，支付金额：{{amount}}',
  })
  await i18n.changeLanguage('zh')
  const preview = renderPreview(
    {
      content_parts: [
        {
          key: 'Online top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}',
          params: { quota: '$10.000000', amount: '10.00' },
        },
      ],
    },
    false,
    1
  )
  expect(preview.textContent).toBe(
    '在线充值成功，充值额度：$10.000000，支付金额：10.00'
  )
})

test('quota saturation remains first and only billing adds to the counter', () => {
  const preview = renderPreview({
    model_price: 0.25,
    admin_info: {
      task_plugin: plugin,
      quota_saturation: {
        op: 'round',
        kind: 'overflow',
        original: 3e9,
        clamped: 2147483647,
      },
    },
  })
  expect(preview.textContent).toBe('Quota clamped+1')
})

test.each([true, false])(
  'plugin information in the opened dialog respects admin=%s',
  async (isAdmin) => {
    const preview = renderPreview(
      { model_price: 0.25, admin_info: { task_plugin: plugin } },
      isAdmin
    )
    expect(preview.textContent).toBe('Per-call · $0.25')
    fireEvent.click(preview)
    const dialog = within(await screen.findByRole('dialog'))
    if (isAdmin) {
      fireEvent.click(dialog.getByRole('tab', { name: 'Request diagnostics' }))
      expect(dialog.getByText('Incho')).toBeVisible()
      expect(dialog.getByText('1.0.1')).toBeVisible()
      expect(dialog.getByText('Plugin maintainer')).toBeVisible()
    } else {
      expect(dialog.queryByText('Incho')).not.toBeInTheDocument()
      expect(dialog.queryByText('Plugin maintainer')).not.toBeInTheDocument()
    }
  }
)

test.each([
  {
    expression: 'tier("music", u("clips") * 0.25)',
    tier: 'music',
    expected: 'music · clips $0.25/unit',
  },
  {
    expression:
      'u("mode") == "pro" ? tier("pro", u("seconds") * 0.8) : tier("std", u("seconds") * 0.4)',
    tier: 'pro',
    expected: 'pro · seconds $0.8/second',
  },
  {
    expression: 'tier("tokens", u("tokens") * 9.8 / 1000000)',
    tier: 'tokens',
    expected: 'tokens · tokens $9.8/1M token',
  },
  {
    expression: 'tier("free", u("clips") * 0)',
    tier: 'free',
    expected: 'free · clips $0/unit',
  },
  {
    expression: 'tier("mixed", 0.1 + u("clips") * 0.25 + u("units") * 0.14)',
    tier: 'mixed',
    expected:
      'mixed · clips $0.25/unit · units $0.14/credit · Additional charge $0.1/request',
  },
])(
  'task expression $tier shows its recorded unit price',
  ({ expression, tier, expected }) => {
    client.setQueryData(['pricing'], {
      data: [
        {
          model_name: 'wan2.5-i2v-preview',
          billing_expr: 'tier("current", u("clips") * 99)',
          billing_usage_schema: {
            clips: { type: 'number', unit: 'count' },
            seconds: { type: 'number', unit: 'second' },
            tokens: { type: 'number', unit: 'token' },
            units: { type: 'number', unit: 'credit' },
            mode: { enum: ['pro', 'std'] },
          },
        },
      ],
      vendors: [],
    })
    const preview = renderPreview({
      is_task: true,
      billing_mode: 'tiered_expr',
      expr_b64: Buffer.from(expression).toString('base64'),
      matched_tier: tier,
      model_price: 0,
      admin_info: { task_plugin: plugin },
    })
    expect(preview.textContent).toBe(expected)
  }
)

test('task log prices use localized unit labels from pricing metadata', async () => {
  client.setQueryData(['pricing'], {
    data: [
      {
        model_name: 'wan2.5-i2v-preview',
        billing_usage_schema: {
          images: {
            type: 'number',
            unit: 'count',
            unitLabel: { en: 'image', zh: '张' },
          },
        },
      },
    ],
    vendors: [],
  })
  const preview = renderPreview({
    is_task: true,
    billing_mode: 'tiered_expr',
    expr_b64: btoa('tier("images", u("images") * 0.25)'),
    matched_tier: 'images',
  })
  expect(preview).toHaveTextContent('images · images $0.25/image')
  await act(() => i18n.changeLanguage('zh-CN'))
  expect(
    screen.getByRole('button', { name: /images · images/ })
  ).toHaveTextContent('images · images $0.25/张')
})

test('task log prices select the executing provider’s schema', () => {
  client.setQueryData(['pricing'], {
    data: [
      {
        model_name: 'wan2.5-i2v-preview',
        billing_usage_schema: { seconds: { type: 'number', unit: 'second' } },
        billing_plugin_variants: [
          {
            plugin_key: 'beta',
            plugin_name: 'Beta',
            billing_expr: 'tier("images", u("images") * 0.25)',
            billing_usage_schema: {
              images: {
                type: 'number',
                unit: 'count',
                unitLabel: { en: 'image' },
              },
            },
          },
        ],
      },
    ],
    vendors: [],
  })
  const preview = renderPreview({
    is_task: true,
    billing_mode: 'tiered_expr',
    expr_b64: btoa('tier("images", u("images") * 0.25)'),
    matched_tier: 'images',
    admin_info: {
      task_plugin: { key: 'beta', name: 'Beta', version: '1.0.0' },
    },
  })
  expect(preview).toHaveTextContent('images · images $0.25/image')
})

test.each(['missing schema', 'unsupported expression', 'unknown tier'])(
  'task pricing with %s preserves the saved tier without guessing prices',
  (scenario) => {
    if (scenario !== 'missing schema') {
      client.setQueryData(['pricing'], {
        data: [
          {
            model_name: 'wan2.5-i2v-preview',
            billing_usage_schema: { clips: { type: 'number', unit: 'count' } },
          },
        ],
        vendors: [],
      })
    }
    const expression =
      scenario === 'unsupported expression'
        ? 'tier("music", max(u("clips"), 1) * 0.25)'
        : 'tier("music", u("clips") * 0.25)'
    const preview = renderPreview({
      is_task: true,
      billing_mode: 'tiered_expr',
      expr_b64: Buffer.from(expression).toString('base64'),
      matched_tier: scenario === 'unknown tier' ? 'old' : 'music',
    })
    expect(preview.textContent).toBe(
      `Dynamic Pricing · ${scenario === 'unknown tier' ? 'old' : 'music'}`
    )
  }
)

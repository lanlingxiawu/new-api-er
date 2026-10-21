import { strict as assert } from 'node:assert'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import i18next from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { initReactI18next } from 'react-i18next'

import { useCommonLogsColumns } from '../src/features/usage-logs/components/columns/common-logs-columns'
import { LogDetailBody } from '../src/features/usage-logs/components/dialogs/log-detail-body'
import type { UsageLog } from '../src/features/usage-logs/data/schema'
import { useAuthStore } from '../src/stores/auth-store'

await i18next.use(initReactI18next).init({
  lng: 'en',
  fallbackLng: 'en',
  resources: { en: { translation: {} } },
})

export function TokenCell(props: { log: UsageLog; admin: boolean }) {
  const columns = useCommonLogsColumns(props.admin)
  const column = columns.find(
    (item) => 'accessorKey' in item && item.accessorKey === 'prompt_tokens'
  )
  assert.ok(column && typeof column.cell === 'function')
  const Cell = column.cell
  return (
    <Cell
      {...({ row: { original: props.log } } as Parameters<typeof Cell>[0])}
    />
  )
}

const cases = [
  {
    name: 'read only',
    other: { cache_tokens: 1234 },
    visible: true,
    value: '1,234',
  },
  {
    name: 'write only',
    other: { cache_creation_tokens: 2345 },
    visible: true,
    value: '2,345',
  },
  {
    name: '5m only',
    other: { cache_creation_tokens_5m: 3456 },
    visible: true,
    value: '3,456',
  },
  {
    name: '1h only',
    other: { cache_creation_tokens_1h: 4567 },
    visible: true,
    value: '4,567',
  },
  { name: 'all zero', other: {}, visible: false },
  { name: 'ordinary', other: {}, visible: true, input: 17, output: 9 },
]
const query = new QueryClient()
for (const admin of [false, true]) {
  for (const example of cases) {
    const log = {
      type: 2,
      prompt_tokens: example.input ?? 0,
      completion_tokens: example.output ?? 0,
      created_at: 1,
      request_id: 'fixture',
      model_name: 'claude',
      channel: 1,
      quota: 1,
      other: JSON.stringify(example.other),
    } as UsageLog
    const list = renderToStaticMarkup(<TokenCell log={log} admin={admin} />)
    const detail = renderToStaticMarkup(
      <QueryClientProvider client={query}>
        <LogDetailBody log={log} isAdmin={admin} showStreamDiagnostic={false} />
      </QueryClientProvider>
    )
    assert.equal(
      detail.includes('Token Breakdown'),
      example.visible,
      `${example.name}: detail`
    )
    if (example.value) {
      assert.ok(
        list.includes(example.value),
        `${example.name}: list cache quantity`
      )
      assert.ok(
        detail.includes(example.value),
        `${example.name}: detail cache quantity`
      )
      assert.ok(list.includes('0 / 0'), `${example.name}: zero regular tokens`)
    }
    if (!example.visible) assert.match(list, />-<\/span>/)
  }
}
for (const role of [1, 10, 100]) {
  useAuthStore.getInitialState().auth.user = {
    id: 1,
    username: 'fixture',
    role,
  }
  useAuthStore.setState((state) => ({
    auth: { ...state.auth, user: { id: 1, username: 'fixture', role } },
  }))
  for (const available of [false, true]) {
    const log = {
      type: 2,
      created_at: 1,
      request_id: 'fixture',
      other: JSON.stringify({ stream_diagnostic_available: available }),
    } as UsageLog
    const html = renderToStaticMarkup(
      <QueryClientProvider client={query}>
        <LogDetailBody log={log} isAdmin={role >= 10} showStreamDiagnostic />
      </QueryClientProvider>
    )
    assert.equal(
      html.includes('Upstream diagnostics (Root only)'),
      role === 100 && available
    )
  }
}
query.clear()
process.stdout.write(
  'Passed: cache-only list/detail quantities, zero and ordinary usage, Root diagnostic visibility\n'
)

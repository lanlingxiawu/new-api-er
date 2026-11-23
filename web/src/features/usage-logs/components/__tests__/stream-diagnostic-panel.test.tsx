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
import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { StreamDiagnosticPanel } from '../dialogs/stream-diagnostic-panel'

const originalAuth = useAuthStore.getState().auth
const clients: QueryClient[] = []

function renderDiagnostic(detail: Record<string, unknown>, role = 100) {
  useAuthStore
    .getState()
    .auth.setUser({ id: 7, username: 'diagnostic-reader', role })
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: detail },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <StreamDiagnosticPanel
        requestId='req-observation'
        createdAt={1234}
        attempt={2}
      />
    </QueryClientProvider>
  )
  return get
}

async function openDiagnostic(detail: Record<string, unknown>) {
  const get = renderDiagnostic(detail)
  expect(get).not.toHaveBeenCalled()
  await userEvent.click(
    screen.getByRole('button', { name: 'Upstream diagnostics (Root only)' })
  )
  await screen.findByText('Gateway-observed termination source')
  return get
}

beforeEach(async () => {
  await i18next.changeLanguage('en')
})

afterEach(async () => {
  for (const client of clients) client.clear()
  clients.length = 0
  useAuthStore.setState({ auth: originalAuth })
  await i18next.changeLanguage('en')
})

describe('private stream termination observations', () => {
  test.each([1, 10])(
    'does not expose or fetch diagnostics for role %i',
    (role) => {
      const get = renderDiagnostic(
        { termination: { first_observed: { side: 'downstream' } } },
        role
      )
      expect(screen.queryByRole('button')).not.toBeInTheDocument()
      expect(
        screen.queryByText('Gateway-observed termination source')
      ).not.toBeInTheDocument()
      expect(get).not.toHaveBeenCalled()
    }
  )

  test('loads the existing Root endpoint on demand and separates downstream cancellation from an initiating actor', async () => {
    const get = await openDiagnostic({
      termination: {
        first_observed: {
          side: 'downstream',
          operation: 'request_cancel',
          error_class: 'canceled',
          after_ms: 79525,
        },
      },
    })
    expect(get).toHaveBeenCalledWith('/api/log/claude-diagnostic', {
      params: { request_id: 'req-observation', created_at: 1234, attempt: 2 },
      signal: expect.any(AbortSignal),
    })
    const row = screen.getByRole('group', { name: 'First termination signal' })
    expect(within(row).getByText('Downstream')).toBeInTheDocument()
    expect(within(row).getByText('Request cancellation')).toBeInTheDocument()
    expect(within(row).getByText('canceled')).toBeInTheDocument()
    expect(within(row).getByText('After 79,525 ms')).toBeInTheDocument()
    expect(
      screen.getByText(
        'These observations do not establish whether the client, SDK, or a proxy initiated the disconnect.'
      )
    ).toBeInTheDocument()
    expect(screen.queryByText(/user cancel/i)).not.toBeInTheDocument()
  })

  test('shows gateway timeout evidence without reclassifying it as an upstream disconnect', async () => {
    await openDiagnostic({
      termination: {
        first_observed: {
          side: 'gateway',
          operation: 'timeout',
          error_class: 'deadline',
          after_ms: 80000,
          timeout_kind: 'total_timeout',
          timeout_seconds: 80,
        },
      },
    })
    const row = screen.getByRole('group', { name: 'First termination signal' })
    expect(within(row).getByText('Gateway')).toBeInTheDocument()
    expect(within(row).getByText('Timeout')).toBeInTheDocument()
    expect(within(row).getByText('total_timeout')).toBeInTheDocument()
    expect(within(row).getByText('80 s')).toBeInTheDocument()
    expect(within(row).queryByText('Upstream')).not.toBeInTheDocument()
  })

  test.each([
    ['write', 'Write'],
    ['flush', 'Flush'],
    ['read', 'Read'],
    ['ping', 'Ping'],
    ['protocol', 'Protocol validation'],
    ['panic', 'Panic'],
  ])(
    'renders the observed %s operation and WebSocket code without interpreting its sender',
    async (operation, label) => {
      await openDiagnostic({
        termination: {
          first_observed: {
            side: 'upstream',
            operation,
            error_class: 'other',
            after_ms: 0,
            ws_close_code: 1006,
          },
        },
      })
      const row = screen.getByRole('group', {
        name: 'First termination signal',
      })
      expect(within(row).getByText('Upstream')).toBeInTheDocument()
      expect(within(row).getByText(label)).toBeInTheDocument()
      expect(within(row).getByText('After 0 ms')).toBeInTheDocument()
      expect(within(row).getByText('1006')).toBeInTheDocument()
    }
  )

  test('shows ordinary EOF and cleanup separately without calling normal completion a failure', async () => {
    await openDiagnostic({
      termination: {
        upstream_read: {
          side: 'upstream',
          operation: 'read',
          error_class: 'eof',
          after_ms: 12,
        },
        upstream_read_after_close: false,
        upstream_close: {
          side: 'gateway',
          operation: 'close',
          error_class: 'cleanup',
          after_ms: 13,
        },
      },
    })
    expect(
      screen.getByText('No abnormal termination signal was recorded.')
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('group', { name: 'First termination signal' })
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('group', { name: 'Upstream read ended' })
    ).toHaveTextContent('eof')
    expect(
      screen.getByRole('group', { name: 'Gateway started closing upstream' })
    ).toHaveTextContent('cleanup')
    expect(
      screen.getByText(
        'An upstream read ending does not by itself indicate an incomplete response.'
      )
    ).toBeInTheDocument()
    expect(
      screen.queryByText(
        'This read result was observed after the gateway started closing upstream.'
      )
    ).not.toBeInTheDocument()
  })

  test('identifies read errors observed after cleanup without replacing the first observation', async () => {
    await openDiagnostic({
      termination: {
        first_observed: {
          side: 'downstream',
          operation: 'write',
          error_class: 'broken_pipe',
          after_ms: 8,
        },
        upstream_read: {
          side: 'upstream',
          operation: 'read',
          error_class: 'closed',
          after_ms: 10,
        },
        upstream_read_after_close: true,
        upstream_close: {
          side: 'gateway',
          operation: 'close',
          error_class: 'cleanup',
          after_ms: 9,
        },
      },
    })
    expect(
      screen.getByRole('group', { name: 'First termination signal' })
    ).toHaveTextContent('broken_pipe')
    expect(
      screen.getByText(
        'This read result was observed after the gateway started closing upstream.'
      )
    ).toBeInTheDocument()
  })

  test('separates upstream usage from received and written output', async () => {
    await openDiagnostic({
      usage_evidence: { input_tokens: 20, output_tokens: 3 },
      delivery_evidence: {
        accepted: true,
        received_response: true,
        protocol_complete: false,
        effective: false,
        received_output_estimate: 4,
        delivered_output_estimate: 0,
      },
    })
    expect(screen.getByText('Delivery decision')).toBeInTheDocument()
    expect(
      screen.getByText('Upstream response accepted: Yes')
    ).toBeInTheDocument()
    expect(
      screen.getByText('Business response received: Yes')
    ).toBeInTheDocument()
    expect(screen.getByText('Protocol complete: No')).toBeInTheDocument()
    expect(
      screen.getByText('Effective content delivered: No')
    ).toBeInTheDocument()
    expect(screen.getByText('Received output estimate: 4')).toBeInTheDocument()
    expect(screen.getByText('Written output estimate: 0')).toBeInTheDocument()
    expect(screen.getByText(/"input_tokens": 20/)).toBeInTheDocument()
  })

  test('does not invent termination evidence for historical diagnostics', async () => {
    await openDiagnostic({
      error: 'context canceled',
      usage_evidence: { input_tokens: 21, output_tokens: 0 },
    })
    expect(
      screen.getByText(
        'Termination observations were not recorded for this request.'
      )
    ).toBeInTheDocument()
    expect(screen.queryByText('Delivery decision')).not.toBeInTheDocument()
    expect(screen.queryByText('Request cancellation')).not.toBeInTheDocument()
    expect(screen.queryByText(/HTTP response/)).not.toBeInTheDocument()
  })

  test('distinguishes an earlier retry observation from the current HTTP response', async () => {
    await openDiagnostic({
      termination: {
        current_response: 2,
        first_observed: {
          side: 'upstream',
          operation: 'read',
          error_class: 'unexpected_eof',
          after_ms: 9,
          response_index: 1,
        },
        upstream_read: {
          side: 'upstream',
          operation: 'read',
          error_class: 'eof',
          after_ms: 25,
          response_index: 2,
        },
        upstream_close: {
          side: 'gateway',
          operation: 'close',
          error_class: 'cleanup',
          after_ms: 26,
          response_index: 2,
        },
      },
    })
    expect(screen.getByText('Current HTTP response: #2')).toBeInTheDocument()
    expect(
      within(
        screen.getByRole('group', { name: 'First termination signal' })
      ).getByText('HTTP response #1')
    ).toBeInTheDocument()
    expect(
      within(
        screen.getByRole('group', { name: 'Upstream read ended' })
      ).getByText('HTTP response #2')
    ).toBeInTheDocument()
    expect(
      within(
        screen.getByRole('group', { name: 'Gateway started closing upstream' })
      ).getByText('HTTP response #2')
    ).toBeInTheDocument()
    i18next.addResourceBundle('zh', 'translation', zh.translation)
    await act(() => i18next.changeLanguage('zh'))
    expect(screen.getByText('当前 HTTP 响应：#2')).toBeInTheDocument()
    expect(screen.getByText('HTTP 响应 #1')).toBeInTheDocument()
  })

  test('does not invent HTTP response numbers for WebSocket observations', async () => {
    await openDiagnostic({
      termination: {
        current_response: 0,
        first_observed: {
          side: 'upstream',
          operation: 'read',
          error_class: 'closed',
          after_ms: 7,
          ws_close_code: 1006,
          response_index: 0,
        },
      },
    })
    expect(screen.getByText('1006')).toBeInTheDocument()
    expect(screen.queryByText(/HTTP response/)).not.toBeInTheDocument()
  })

  test('keeps unmeasured completion and output unknown when validation is disabled', async () => {
    await openDiagnostic({
      validation: {
        enabled: false,
        source: 'user',
        config_origin: 'canonical',
      },
      delivery_evidence: { accepted: true },
    })
    expect(
      screen.getByText('Protocol complete: Not validated')
    ).toBeInTheDocument()
    expect(
      screen.getByText('Written output estimate: Unknown')
    ).toBeInTheDocument()
    expect(
      screen.getByText('Received output estimate: Unknown')
    ).toBeInTheDocument()
    expect(screen.queryByText('Protocol complete: No')).not.toBeInTheDocument()
  })

  test('preserves unknown protocol values as escaped evidence instead of guessing a source', async () => {
    await openDiagnostic({
      termination: {
        first_observed: {
          side: 'future_side',
          operation: '<script>future_op</script>',
          error_class: 'future_error',
          after_ms: 1,
        },
      },
    })
    expect(screen.getByText('Unknown')).toBeInTheDocument()
    expect(screen.getByText('future_side')).toBeInTheDocument()
    expect(screen.getByText('<script>future_op</script>')).toBeInTheDocument()
    expect(screen.getByText('future_error')).toBeInTheDocument()
    expect(document.querySelector('script')).toBeNull()
  })

  test('updates the observation labels when the interface language changes', async () => {
    i18next.addResourceBundle('zh', 'translation', zh.translation)
    await openDiagnostic({
      termination: {
        first_observed: {
          side: 'downstream',
          operation: 'request_cancel',
          error_class: 'canceled',
          after_ms: 1,
        },
      },
    })
    await act(() => i18next.changeLanguage('zh'))
    expect(screen.getByText('网关观察到的终止来源')).toBeInTheDocument()
    expect(screen.getByText('请求取消')).toBeInTheDocument()
    expect(screen.queryByText('用户取消')).not.toBeInTheDocument()
  })
})

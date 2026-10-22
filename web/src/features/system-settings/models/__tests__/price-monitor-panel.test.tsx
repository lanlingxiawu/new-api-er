import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, within } from '@testing-library/react'
import { afterEach, assert, expect, test, vi } from 'vitest'

import * as api from '../../api'
import { SettingsSaveConfirmationProvider } from '../../components/settings-save-confirmation'
import { PriceMonitorPanel } from '../price-monitor-panel'

afterEach(() => vi.restoreAllMocks())

test('shows prices and loss warnings without raw expression difference rows', async () => {
  vi.spyOn(api, 'getPriceMonitorStatus').mockResolvedValue({
    success: true,
    message: '',
    data: {
      config: {
        enabled: true,
        interval_minutes: 360,
        timeout_seconds: 10,
        include_official: true,
        include_models_dev: false,
        model_whitelist: '',
        custom_endpoints: {},
      },
      snapshot: {
        checked_at: 1,
        status: 'success',
        source_total: 1,
        source_ok: 1,
        source_err: 0,
        model_count: 1,
        item_count: 1,
        access_password: '',
        password_expire_at: 0,
      },
      running: false,
      last_attempt_at: 1,
      last_attempt_error: '',
      storage_scope: 'database',
      is_master: true,
      pricing_version: 1,
    },
  })
  vi.spyOn(api, 'getPriceMonitorChannelCosts').mockResolvedValue({
    success: true,
    message: '',
    data: { checked_at: 1, items: [] },
  })
  const source = {
    key: 'official',
    name: 'Official pricing',
    type: 'official' as const,
  }
  vi.spyOn(api, 'getPriceMonitorResults').mockResolvedValue({
    success: true,
    message: '',
    data: {
      checked_at: 1,
      total: 1,
      page: 1,
      page_size: 20,
      source_headers: [source],
      available_source_headers: [source],
      available_models: ['tiered-model'],
      applied_filters: { source_keys: [], comparison: 'all' },
      items: [
        {
          model: 'tiered-model',
          prices: {
            official: {
              mode: 'per_token',
              input: 2.1,
              output: 8.4,
              input_different: true,
              different: true,
              loss_kinds: ['configured'],
              comparison_status: 'different',
              differences: [
                { min: 0, max: 512000, variable: 'p', left: 2.1, right: 0.3 },
                { min: 512001, variable: 'img_cr', left: 4.2, right: 0.6 },
              ],
            },
          },
        },
      ],
    },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const view = render(
    <QueryClientProvider client={client}>
      <SettingsSaveConfirmationProvider>
        <PriceMonitorPanel
          canEdit={false}
          canRepairPricing={false}
          canEditCostRatio={false}
        />
      </SettingsSaveConfirmationProvider>
    </QueryClientProvider>
  )
  const row = (
    await screen.findByRole('cell', { name: 'tiered-model' })
  ).closest('tr')
  assert(row)
  expect(within(row).getByText('$2.1')).toBeVisible()
  expect(within(row).getByText('$8.4')).toBeVisible()
  expect(within(row).getByText('Configured loss')).toBeVisible()
  expect(within(row).queryByText(/0–512000.*p:/)).not.toBeInTheDocument()
  expect(within(row).queryByText(/512001–∞.*img_cr:/)).not.toBeInTheDocument()
  view.unmount()
  client.clear()
})

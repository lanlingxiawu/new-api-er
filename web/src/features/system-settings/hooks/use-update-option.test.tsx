import assert from 'node:assert/strict'
import { test } from 'node:test'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderToString } from 'react-dom/server'

import { api } from '@/lib/api'

import { SettingsPageContext } from '../components/settings-page-access-context'
import { useUpdateOption } from './use-update-option'

test('option writes can explicitly opt out of the page scope for root-only settings', async () => {
  const originalAdapter = api.defaults.adapter
  const requests: unknown[] = []
  api.defaults.adapter = async (config) => {
    requests.push(JSON.parse(config.data as string))
    return {
      config,
      status: 200,
      statusText: 'OK',
      headers: {},
      data: { success: true, message: '' },
    }
  }

  const client = new QueryClient({
    defaultOptions: { mutations: { retry: false } },
  })
  let mutation: ReturnType<typeof useUpdateOption> | undefined
  function CaptureMutation(props: { scope?: string }) {
    mutation = useUpdateOption(props.scope)
    return null
  }

  try {
    for (const scope of ['', undefined, 'site.notice']) {
      renderToString(
        <QueryClientProvider client={client}>
          <SettingsPageContext.Provider
            value={{
              actionsContainer: null,
              titleStatusContainer: null,
              suppressSectionHeader: false,
              scope: 'operations.performance',
              canEdit: true,
            }}
          >
            <CaptureMutation scope={scope} />
          </SettingsPageContext.Provider>
        </QueryClientProvider>
      )
      assert.ok(mutation)
      for (const value of [true, false]) {
        await mutation.mutateAsync({ key: 'pprof_setting.enabled', value })
      }
    }

    assert.deepEqual(requests, [
      { key: 'pprof_setting.enabled', value: true },
      { key: 'pprof_setting.enabled', value: false },
      {
        key: 'pprof_setting.enabled',
        value: true,
        scope: 'operations.performance',
      },
      {
        key: 'pprof_setting.enabled',
        value: false,
        scope: 'operations.performance',
      },
      { key: 'pprof_setting.enabled', value: true, scope: 'site.notice' },
      { key: 'pprof_setting.enabled', value: false, scope: 'site.notice' },
    ])
  } finally {
    api.defaults.adapter = originalAdapter
    client.clear()
  }
})

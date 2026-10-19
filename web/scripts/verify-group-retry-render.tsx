import { strict as assert } from 'node:assert'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import i18next from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { initReactI18next } from 'react-i18next'

import { guardDraft } from '../src/features/system-settings/system-tuning/group-retry-guard'
import { GroupRetryGuardFields } from '../src/features/system-settings/system-tuning/group-retry-guard-fields'
import { GroupRetrySection } from '../src/features/system-settings/system-tuning/group-retry-section'

await i18next.use(initReactI18next).init({
  lng: 'en',
  fallbackLng: 'en',
  resources: { en: { translation: {} } },
})

for (const mode of ['fixed', 'progressive'] as const) {
  for (const enabled of [false, true]) {
    const html = renderToStaticMarkup(
      <GroupRetryGuardFields
        id={0}
        value={{ ...guardDraft(), enabled, mode }}
        disabled={false}
        active={false}
        available={false}
        onChange={() => {}}
      />
    )
    const enabledFieldCount = mode === 'fixed' ? 5 : 8
    assert.equal(
      (html.match(/type="number"/g) ?? []).length,
      enabled ? enabledFieldCount : 0,
      `Parameters are visible only while enabled: ${mode}, enabled=${enabled}`
    )
    assert.equal(html.includes('RPM threshold'), enabled)
    assert.equal(html.includes('Observation window (seconds)'), enabled)
    assert.equal(html.includes('Trigger conditions'), enabled)
    assert.equal(html.includes('Cooldown and recovery'), enabled)
    if (enabled) {
      assert.ok(
        html.indexOf('Cooldown mode') <
          html.indexOf('Initial cooldown (seconds)')
      )
    }
  }
}
process.stdout.write(
  'Parameters expand only for enabled groups in both cooldown modes\n'
)

const client = new QueryClient()
const section = renderToStaticMarkup(
  <QueryClientProvider client={client}>
    <GroupRetrySection
      value='{"":0}'
      statusRules='{}'
      statusEnabled={false}
      guardRules='{}'
      guardEnabled
    />
  </QueryClientProvider>
)
assert.equal(
  section.split('Select a group.').length - 1,
  0,
  'An untouched new group must not show an error before submission'
)
assert.equal(
  (section.match(/type="number"/g) ?? []).length,
  0,
  'Global enable must not reveal parameters before group enable'
)
client.clear()
process.stdout.write(
  'Section: no premature error, disabled group parameters hidden\n'
)

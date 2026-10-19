import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({ url: 'http://localhost/' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'KeyboardEvent',
  'PointerEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
})

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider, notifyManager } =
  await import('@tanstack/react-query')
const { default: i18next } = await import('i18next')
const { initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { GroupRetrySection } = await import('../group-retry-section')
const { guardDraft, parseGuardDraft } = await import('../group-retry-guard')

await i18next.use(initReactI18next).init({
  lng: 'en',
  fallbackLng: 'en',
  resources: { en: { translation: {} } },
})
notifyManager.setScheduler(queueMicrotask)
const originalAdapter = api.defaults.adapter
let saveSucceeds = false
const saves: { values: Record<string, string> }[] = []
api.defaults.adapter = async (config) => {
  if (config.method === 'put' && config.url === '/api/option/group') {
    saves.push(JSON.parse(config.data))
    return {
      config,
      status: 200,
      statusText: 'OK',
      headers: {},
      data: { success: saveSucceeds, data: { applied: saveSucceeds } },
    }
  }
  assert.equal(
    config.url,
    '/api/option/group-retry/status',
    'No real network calls are allowed'
  )
  return {
    config,
    status: 200,
    statusText: 'OK',
    headers: {},
    data: { success: true, data: { available: false, groups: [] } },
  }
}

after(() => {
  api.defaults.adapter = originalAdapter
  domWindow.close()
})

function button(container: ParentNode, name: string) {
  const result = [
    ...container.querySelectorAll<HTMLButtonElement>('button'),
  ].find((item) => item.textContent?.trim() === name)
  assert.ok(result, `Button ${name} must exist`)
  return result
}

async function mountGroups() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  client.setQueryData(['groups'], {
    success: true,
    data: ['alpha', 'beta', 'gamma'],
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const rule = parseGuardDraft({
    ...guardDraft(),
    cooldown: '17',
    multiplier: '3',
    maximum: '100',
  })
  assert.ok(rule)
  await act(async () =>
    root.render(
      <QueryClientProvider client={client}>
        <GroupRetrySection
          value='{"alpha":1,"beta":2}'
          statusRules='{}'
          statusEnabled={false}
          guardEnabled
          guardRules={JSON.stringify({ alpha: rule, beta: rule })}
        />
      </QueryClientProvider>
    )
  )
  return { container, root, client }
}

test('group modes keep drafts, cooldown preview caps, and removal can be undone', async () => {
  const { container, root, client } = await mountGroups()
  try {
    const group = container.querySelector<HTMLElement>(
      'section[aria-label="alpha"]'
    )
    assert.ok(group)
    assert.equal(group.querySelectorAll('input[type="number"]').length, 0)
    const groupSwitch =
      group.querySelector<HTMLButtonElement>('[role="switch"]')
    assert.ok(groupSwitch)
    await act(async () => groupSwitch.click())
    assert.equal(group.querySelectorAll('input[type="number"]').length, 8)
    assert.ok(group.textContent?.includes('17 → 51 → 100'))
    const count = group.querySelector<HTMLInputElement>('[id$="-count"]')
    const setValue = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      'value'
    )?.set
    assert.ok(count)
    assert.ok(setValue)
    await act(async () => {
      setValue.call(count, '12')
      count.dispatchEvent(new Event('input', { bubbles: true }))
    })
    assert.ok(group.textContent?.includes('at least 12 responses'))
    await act(async () => groupSwitch.click())
    assert.equal(group.querySelectorAll('input[type="number"]').length, 0)
    assert.ok(!group.textContent?.includes('Trigger conditions'))
    await act(async () => groupSwitch.click())
    assert.equal(
      group.querySelector<HTMLInputElement>('[id$="-count"]')?.value,
      '12'
    )
    const masterSwitch = container.querySelector<HTMLButtonElement>(
      '#group-retry-guard-enabled'
    )
    assert.ok(masterSwitch)
    await act(async () => masterSwitch.click())
    assert.equal(group.querySelector('[role="switch"]'), null)
    assert.equal(group.querySelectorAll('input[type="number"]').length, 0)
    await act(async () => masterSwitch.click())
    assert.equal(
      group.querySelector<HTMLInputElement>('[id$="-count"]')?.value,
      '12'
    )
    await act(async () => button(group, 'Fixed cooldown').click())
    assert.equal(group.querySelectorAll('input[type="number"]').length, 5)
    await act(async () => button(group, 'Progressive cooldown').click())
    assert.equal(
      group.querySelector<HTMLInputElement>('[id$="-multiplier"]')?.value,
      '3'
    )
    assert.ok(group.textContent?.includes('17 → 51 → 100'))
    const remove = group.querySelector<HTMLButtonElement>(
      'button[aria-label="Remove"]'
    )
    assert.ok(remove)
    await act(async () => remove.click())
    assert.equal(container.querySelector('section[aria-label="alpha"]'), null)
    await act(async () => button(container, 'Undo removal').click())
    assert.equal(
      container.querySelector<HTMLInputElement>(
        'section[aria-label="alpha"] [id$="-cooldown"]'
      )?.value,
      '17'
    )
    assert.equal(container.querySelectorAll('section[aria-label]').length, 2)
  } finally {
    await act(async () => root.unmount())
    client.clear()
    container.remove()
  }
})

test('retry counts accept ten and reject eleven without submitting', async () => {
  const { container, root, client } = await mountGroups()
  saves.length = 0
  saveSucceeds = false
  try {
    const input = container.querySelector<HTMLInputElement>(
      '#group-retry-times-0'
    )
    const setValue = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      'value'
    )?.set
    assert.ok(input)
    assert.ok(setValue)
    await act(async () => {
      setValue.call(input, '11')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => button(container, 'Save').click())
    assert.equal(saves.length, 0)
    assert.ok(
      container.textContent?.includes('Must be -1, or between 0 and 10.')
    )
    await act(async () => {
      setValue.call(input, '10')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => button(container, 'Save').click())
    assert.equal(saves.length, 1)
    assert.equal(JSON.parse(saves[0].values.times).alpha, 10)
  } finally {
    await act(async () => root.unmount())
    client.clear()
    container.remove()
  }
})

test('adding a group opens unused options; save failure keeps the draft and success clears it', async () => {
  const { container, root, client } = await mountGroups()
  saves.length = 0
  saveSucceeds = false
  try {
    await act(async () => button(container, 'Add group').click())
    assert.ok(!container.textContent?.includes('Select a group.'))
    const options = [
      ...document.querySelectorAll<HTMLElement>('[role="option"]'),
    ]
    assert.deepEqual(
      options.map((option) => option.textContent?.trim()),
      ['gamma']
    )
    await act(async () => options[0].click())
    assert.ok(container.querySelector('section[aria-label="gamma"]'))
    await act(async () => button(container, 'Save').click())
    assert.equal(saves.length, 1)
    assert.ok(container.textContent?.includes('Unsaved changes'))
    assert.equal(button(container, 'Save').disabled, false)
    assert.deepEqual(JSON.parse(saves[0].values.times), {
      alpha: 1,
      beta: 2,
      gamma: 0,
    })
    saveSucceeds = true
    await act(async () => button(container, 'Save').click())
    assert.equal(saves.length, 2)
    assert.equal(button(container, 'Save').disabled, true)
    assert.ok(!container.textContent?.includes('Unsaved changes'))
  } finally {
    await act(async () => root.unmount())
    client.clear()
    container.remove()
  }
})

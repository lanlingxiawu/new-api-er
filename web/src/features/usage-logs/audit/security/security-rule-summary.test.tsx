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
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { cleanup, render, screen } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, expect, it } from 'vitest'

import en from '@/i18n/locales/en.json'
import fr from '@/i18n/locales/fr.json'
import ja from '@/i18n/locales/ja.json'
import ru from '@/i18n/locales/ru.json'
import vi from '@/i18n/locales/vi.json'
import zhTW from '@/i18n/locales/zh-TW.json'
import zh from '@/i18n/locales/zh.json'

import type { SecurityAlert, SecurityFinding } from './api'
import { securityTime } from './lib/security-display'
import { SecurityRuleSummary } from './security-rule-summary'

afterEach(cleanup)
const resources = { en, zh, 'zh-TW': zhTW, fr, ja, ru, vi }
const alert: SecurityAlert = {
  id: 7,
  rule: 'bulk_sensitive_operations',
  severity: 3,
  status: 'open',
  actor_id: 2,
  resource_type: 'channel',
  first_seen: 100,
  last_seen: 200,
  occurrences: 4,
  matched_count: 15,
  version: 1,
}

it.each([true, false])(
  'shows retained source times or an explicit historical gap (compact=%s)',
  async (compact) => {
    const i18n = createInstance()
    await i18n.init({ lng: 'en', resources })
    const sourceAlert = { ...alert, rule: 'new_source_sensitive_operation' }
    const snapshot: SecurityFinding = {
      id: 1,
      alert_id: sourceAlert.id,
      rule: sourceAlert.rule,
      rule_version: 2,
      trigger_event_id: 'source-event',
      trigger_at: 1730000000,
      detected_at: 1730000001,
      source: 'live',
      subject_ref: 'account:2',
      actor_id: 2,
      actor_role: 1,
      resource_type: 'account',
      target_id: 0,
      ip: '8.8.8.8',
      request_id: 'source-request',
      severity: 1,
      threshold: 1,
      window_seconds: 0,
      matched_count: 1,
      truncated: false,
      dimensions: {
        source_first_seen: 1730000000,
        baseline_first_seen: 1729800000,
        baseline_last_seen: 1729999900,
      },
    }
    const view = render(
      <I18nextProvider i18n={i18n}>
        <SecurityRuleSummary
          alert={sourceAlert}
          snapshot={snapshot}
          compact={compact}
        />
      </I18nextProvider>
    )
    for (const [label, value] of [
      ['New source first observed', snapshot.dimensions.source_first_seen],
      ['Known source first observed', snapshot.dimensions.baseline_first_seen],
      ['Known source last observed', snapshot.dimensions.baseline_last_seen],
    ] as const) {
      expect(screen.getByText(label).parentElement).toHaveTextContent(
        securityTime(value)
      )
    }
    view.rerender(
      <I18nextProvider i18n={i18n}>
        <SecurityRuleSummary
          alert={sourceAlert}
          snapshot={{
            ...snapshot,
            dimensions: { baseline: 1, new_window_seconds: 86400 },
          }}
          compact={compact}
        />
      </I18nextProvider>
    )
    expect(
      screen.getByText('Source timing was not recorded for this alert.')
    ).toBeVisible()
    expect(
      screen.queryByText('New source first observed')
    ).not.toBeInTheDocument()
  }
)

it.each([
  [
    'impossible_travel',
    'Unusual travel between sign-ins',
    'Sign-in distance and travel speed exceeded the recorded thresholds. IP location is approximate.',
  ],
  ['sensitive_operation', 'Sensitive operation', 'sensitive-operation policy'],
  [
    'bulk_sensitive_operations',
    'Bulk sensitive operations',
    'same resource type within 5 minutes',
  ],
  [
    'bulk_key_access',
    'Bulk channel key access',
    'distinct channels within 5 minutes',
  ],
  [
    'login_failures',
    'Repeated sign-in failures',
    'same account or unidentified source within 5 minutes',
  ],
  [
    'password_spray',
    'Password spraying',
    'at least 20 sign-in failures across at least 5 account identifiers within 5 minutes',
  ],
  [
    'login_after_failures',
    'Sign-in after repeated failures',
    'preceding 15 minutes',
  ],
  [
    'recovery_requests',
    'Repeated password recovery',
    'at least 3 password recovery requests within 10 minutes',
  ],
  [
    'permission_failures',
    'Repeated permission failures',
    'at least 10 permission denials within 5 minutes',
  ],
  [
    'session_reuse',
    'Reused sign-in credential',
    'An invalidated sign-in credential was used again and confirmed by the system.',
  ],
  [
    'new_login_source',
    'Sign-in from a new source',
    'established for at least one day and active within the last 30 days',
  ],
])('explains the recorded %s rule', async (rule, label, condition) => {
  const i18n = createInstance()
  await i18n.init({ lng: 'en', resources })
  render(
    <I18nextProvider i18n={i18n}>
      <SecurityRuleSummary alert={{ ...alert, rule }} />
    </I18nextProvider>
  )
  expect(screen.getByText('Matched rule')).toBeVisible()
  expect(screen.getByText(label, { exact: true })).toBeVisible()
  expect(screen.getByText(condition, { exact: false })).toBeVisible()
})

it.each([
  [{ detected: 1 }, 'The event IP was identified as VPN, proxy or Tor.'],
  [{ detected: 1, tor: 1 }, 'The event IP was identified as a Tor exit.'],
])(
  'describes the stored anonymous-network policy without rewriting history',
  async (dimensions, condition) => {
    const i18n = createInstance()
    await i18n.init({ lng: 'en', resources })
    const snapshot: SecurityFinding = {
      id: 1,
      alert_id: alert.id,
      rule: 'anonymous_network',
      rule_version: 2,
      trigger_event_id: 'anonymous-event',
      trigger_at: 100,
      detected_at: 100,
      source: 'live',
      subject_ref: 'account:2',
      actor_id: 2,
      actor_role: 1,
      resource_type: 'account',
      target_id: 0,
      ip: '203.0.113.42',
      request_id: '',
      severity: 1,
      threshold: 1,
      window_seconds: 0,
      matched_count: 1,
      dimensions,
      truncated: false,
    }
    render(
      <I18nextProvider i18n={i18n}>
        <SecurityRuleSummary
          alert={{ ...alert, rule: 'anonymous_network' }}
          snapshot={snapshot}
        />
      </I18nextProvider>
    )
    expect(screen.getByText(condition)).toBeVisible()
  }
)

it('distinguishes peak matching counts from alert occurrences without inventing historical thresholds', async () => {
  const i18n = createInstance()
  await i18n.init({ lng: 'zh', resources })
  render(
    <I18nextProvider i18n={i18n}>
      <SecurityRuleSummary alert={alert} />
    </I18nextProvider>
  )
  expect(
    screen.getByText('5 分钟内最大累计影响数').parentElement
  ).toHaveTextContent('15')
  expect(screen.getByText('触发次数').parentElement).toHaveTextContent('4')
  expect(
    screen.getByText('此告警未保存触发时的阈值，无法还原当时的具体数值。')
  ).toBeVisible()
  expect(screen.queryByText(/阈值为 5/)).not.toBeInTheDocument()
})

it('preserves unknown rule identifiers and zero counts without assigning a known rule', async () => {
  const i18n = createInstance()
  await i18n.init({ lng: 'en', resources })
  render(
    <I18nextProvider i18n={i18n}>
      <SecurityRuleSummary
        alert={{
          ...alert,
          rule: 'future_rule',
          matched_count: 0,
          occurrences: 0,
        }}
      />
    </I18nextProvider>
  )
  expect(screen.getByText('future_rule')).toBeVisible()
  expect(
    screen.getByText(
      'This rule is not recognized. Review the retained records.'
    )
  ).toBeVisible()
  expect(screen.getAllByText('0', { exact: true })).toHaveLength(2)
})

it('explains all emitted backend rules in every supported language', async () => {
  const backend = ['rules.go', 'store.go']
    .map((file) =>
      readFileSync(resolve('..', 'service/auditsecurity', file), 'utf8')
    )
    .join('\n')
  const rules = new Set(
    [...backend.matchAll(/Rule: "(\w+)"/g)].map((match) => match[1])
  )
  for (const match of backend.matchAll(/rule (?::=|=) "(\w+)"/g)) {
    rules.add(match[1])
  }
  const automaticRules = readFileSync(
    resolve('..', 'setting/operation_setting/audit_auto_ban.go'),
    'utf8'
  )
  for (const match of automaticRules.matchAll(/"id":"(\w+)"/g)) {
    rules.add(match[1])
  }
  expect(rules.size).toBe(13)
  for (const language of Object.keys(resources)) {
    const i18n = createInstance()
    const missing: string[] = []
    await i18n.init({
      lng: language,
      fallbackLng: false,
      resources,
      saveMissing: true,
      missingKeyHandler: (_languages, _namespace, key) => missing.push(key),
    })
    for (const rule of rules) {
      const view = render(
        <I18nextProvider i18n={i18n}>
          <SecurityRuleSummary alert={{ ...alert, rule }} />
        </I18nextProvider>
      )
      expect(view.container.textContent, `${language}: ${rule}`).not.toContain(
        rule
      )
      expect(view.container.textContent).not.toMatch(/\{\{/)
      view.unmount()
    }
    expect(missing, language).toEqual([])
  }
})

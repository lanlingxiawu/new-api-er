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

import type { AuditActivity } from '../api'
import { AuditDetailFields } from '../components/audit-detail-fields'
import { activityLabel } from './activity'
import { auditActionLabel } from './audit-action-labels'
import { auditFieldLabel } from './audit-field-labels'
import { auditParameterValue } from './audit-parameter-value'

afterEach(cleanup)
const resources = { en, zh, 'zh-TW': zhTW, fr, ja, ru, vi }

it.each([
  ['auth.recovery_requested', 'Password recovery account'],
  ['auth.recovery_rejected', 'Password recovery account'],
  ['recovery_requests', 'Password recovery account'],
  ['auth.login_failed', 'Attempted sign-in account'],
  ['login_failures', 'Attempted sign-in account'],
  ['password_spray', 'Attempted sign-in account'],
  ['ip_abuse', 'Submitted account'],
  ['anonymous_network', 'Submitted account'],
  [undefined, 'Submitted account'],
])(
  'labels account evidence using its known context: %s',
  async (context, label) => {
    const i18n = createInstance()
    await i18n.init({ lng: 'en', resources })
    expect(auditFieldLabel('attempted_account', i18n.t, context)).toBe(label)
  }
)

it('gives every allowed account activity a localized name without private template parameters', async () => {
  const source = readFileSync(resolve('..', 'model/audit_activity.go'), 'utf8')
  const allowed = source
    .split('switch action {')[1]
    .split('a.Action = action')[0]
  const actions = [...allowed.matchAll(/"([\w.]+)"/g)].map((match) => match[1])
  expect(actions).toContain('log_export.job_create')
  expect(actions).toContain('channel.update')
  // These actions are passed through a task helper rather than the static
  // middleware registry, which previously left them out of the projection.
  const detection = readFileSync(
    resolve('..', 'controller/channel_veridrop.go'),
    'utf8'
  )
  for (const match of detection.matchAll(/"(channel\.veridrop_[\w]+)"/g)) {
    expect(actions).toContain(match[1])
  }
  for (const language of Object.keys(resources)) {
    const i18n = createInstance()
    await i18n.init({ lng: language, fallbackLng: false, resources })
    for (const action of actions) {
      const entry = { action } as AuditActivity
      const key = activityLabel(entry, ((key: string) => key) as typeof i18n.t)
      expect(key, action).not.toBe('Operation audit')
      expect(i18n.exists(key), `${language}: ${action}`).toBe(true)
      expect(
        activityLabel(entry, i18n.t),
        `${language}: ${action}`
      ).not.toMatch(/\{\{|Not recorded/)
    }
    expect(
      activityLabel({ action: 'unknown.private' } as AuditActivity, i18n.t)
    ).toBe(i18n.t('Operation audit'))
  }
})

it('covers every public backend security fact, sensitive field and recorded action in all seven locales', async () => {
  const source = readFileSync(
    resolve('..', 'service/auditsecurity/rules.go'),
    'utf8'
  )
  const facts = source.split('type Facts struct {')[1].split('\n}')[0]
  const sensitive = source
    .split('var sensitiveFields = map[string]bool{')[1]
    .split('\n}')[0]
  // publicSecurityEvent removes execution provenance before serializing facts.
  const internalFacts = new Set([
    'identity',
    'login_target',
    'live_policy_at',
    'first_observed_at',
    'new_management_source',
    'source_baseline',
  ])
  const fields = [
    'outcome',
    'key_accounts',
    'login_ips',
    'key_ips',
    ...[...facts.matchAll(/json:"(\w+)/g)]
      .map((match) => match[1])
      .filter((field) => !internalFacts.has(field)),
    ...[...sensitive.matchAll(/"(\w+)":/g)].map((match) => match[1]),
    'name',
    'access_tokens',
    'revoked_count',
    'legacy_revoked',
    'other_display_field',
  ]
  const actions = [
    'audit_security.disable_account',
    'user.access_tokens.delete_all',
    ...new Set(
      [...source.matchAll(/"((?:channel|user|auth|access_token)\.[\w.]+)"/g)]
        .map((match) => match[1])
        // The quota family is matched by prefix in the backend, not recorded as an action.
        .filter((action) => !action.endsWith('_'))
    ),
  ]
  for (const language of Object.keys(resources)) {
    const i18n = createInstance()
    await i18n.init({ lng: language, fallbackLng: false, resources })
    const translate = i18n.t.bind(i18n)
    for (const field of fields) {
      const key = auditFieldLabel(
        field,
        ((key: string) => key) as typeof translate
      )
      expect(i18n.exists(key), `${language}: field ${field}`).toBe(true)
      expect(auditFieldLabel(field, translate), field).not.toBe(field)
    }
    expect(
      auditFieldLabel('accounts', translate, 'multi_account_key_access')
    ).toBe(translate('Signed-in accounts'))
    expect(auditFieldLabel('accounts', translate, 'password_spray')).toBe(
      translate('Account identifiers')
    )
    expect(i18n.exists('Signed-in accounts'), language).toBe(true)
    for (const action of actions) {
      const key = auditActionLabel(action, (key) => key)
      expect(key, action).toBeDefined()
      expect(i18n.exists(key ?? ''), `${language}: action ${action}`).toBe(true)
      expect(auditActionLabel(action, translate)).not.toMatch(/\{\{/)
    }
  }
})

it('localizes ordinary audit parameters, nested records and empty values without rewriting custom mappings', async () => {
  const i18n = createInstance()
  await i18n.init({ lng: 'zh', resources })
  const values = {
    ids: [12, 13],
    count: 0,
    changed: false,
    submitted_fields: ['key', 'base_url', 'status'],
    target_role: 100,
    from: 1,
    to: 3,
    nested_record: { target_role: 10, count: 0 },
    model_mapping: { status: 'open', model_name: 'user-defined-model' },
    empty_array: [],
    empty_object: {},
    empty_value: null,
    future_field: 'keep-original-value',
  }
  render(
    <I18nextProvider i18n={i18n}>
      <AuditDetailFields
        fields={Object.entries(values).map(([key, value]) => ({
          key,
          action: 'channel.status_update',
          label: auditFieldLabel(key, i18n.t),
          value,
        }))}
      />
    </I18nextProvider>
  )
  expect(screen.getByText('目标 ID 列表')).toBeVisible()
  expect(screen.getByText('提交的字段')).toBeVisible()
  expect(
    screen.getByText(
      `${zh.translation.Key}, ${zh.translation['Base URL']}, ${zh.translation.Status}`
    )
  ).toBeVisible()
  expect(screen.getByText('超级管理员')).toBeVisible()
  expect(screen.getByText('管理员')).toBeVisible()
  expect(screen.getByText(zh.translation.Enabled)).toBeVisible()
  expect(screen.getByText(zh.translation['Auto Disabled'])).toBeVisible()
  expect(screen.getAllByText('0')).toHaveLength(2)
  expect(screen.getByText('否')).toBeVisible()
  expect(screen.getByText('status')).toBeVisible()
  expect(screen.getByText('open')).toBeVisible()
  expect(screen.getByText('model_name')).toBeVisible()
  expect(screen.getByText('user-defined-model')).toBeVisible()
  expect(screen.getAllByText(zh.translation.None)).toHaveLength(3)
  expect(screen.getByText('future_field')).toBeVisible()
  expect(screen.getByText('keep-original-value')).toBeVisible()
})

it('uses operation context for enums without confusing numeric values with statuses', async () => {
  const i18n = createInstance()
  await i18n.init({ lng: 'zh', resources })
  expect(auditParameterValue('from', 1, 'user.quota_add', i18n.t)).toBe(1)
  expect(
    auditParameterValue('to', 3, 'price_monitor.cost_ratio.update', i18n.t)
  ).toBe(3)
  expect(auditParameterValue('status', 3, 'channel.update', i18n.t)).toBe(
    zh.translation['Auto Disabled']
  )
  expect(auditParameterValue('status', 3, 'token.update', i18n.t)).toBe(
    zh.translation.Expired
  )
  expect(
    auditParameterValue(
      'action',
      'disable_all_keys',
      'channel.multi_key_manage',
      i18n.t
    )
  ).toBe('禁用全部密钥')
  expect(
    auditParameterValue(
      'status',
      'false_positive',
      'audit_security.status',
      i18n.t
    )
  ).toBe('误报')
  expect(
    auditParameterValue('reason', 'invalid_code', 'auth.login_failed', i18n.t)
  ).toBe(zh.translation['Invalid code'])
  expect(
    auditParameterValue(
      'reason',
      'expired',
      'employee.performance_adjust',
      i18n.t
    )
  ).toBe('expired')
  expect(auditParameterValue('target_role', 999, 'user.update', i18n.t)).toBe(
    999
  )
})

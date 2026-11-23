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
import type { AuditLog } from '../api'

export type AuditChangeValue = {
  state: 'value' | 'absent' | 'redacted' | 'unavailable' | 'truncated'
  value?: unknown
  value_type?: string
}

export type AuditFieldChange = {
  path: string[]
  old: AuditChangeValue
  new: AuditChangeValue
}
export type AuditTargetChange = {
  resource: string
  id: string
  name: string
  consistency: string
  fields: AuditFieldChange[]
}
export type AuditChanges = {
  outcome: string
  truncated: boolean
  targets: AuditTargetChange[]
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function readValue(value: unknown, sensitive: boolean): AuditChangeValue {
  if (sensitive) return { state: 'redacted' }
  if (!isRecord(value)) return { state: 'unavailable' }
  if (value.state === 'value' && 'value' in value) {
    return {
      state: 'value',
      value: value.value,
      value_type:
        typeof value.value_type === 'string' ? value.value_type : undefined,
    }
  }
  if (
    value.state === 'absent' ||
    value.state === 'redacted' ||
    value.state === 'truncated'
  ) {
    return { state: value.state }
  }
  return { state: 'unavailable' }
}

export function readAuditChanges(
  entry: Pick<AuditLog, 'action' | 'other' | 'success'>
): AuditChanges | null {
  const raw = entry.other?.changes
  if (isRecord(raw) && raw.version === 1 && Array.isArray(raw.targets)) {
    const targets: AuditTargetChange[] = []
    let fieldCount = 0
    for (const target of raw.targets.slice(0, 100)) {
      if (!isRecord(target) || !Array.isArray(target.fields)) continue
      const fields: AuditFieldChange[] = []
      for (const field of target.fields.slice(0, 1000)) {
        if (fieldCount >= 1000) break
        if (
          !isRecord(field) ||
          !Array.isArray(field.path) ||
          !field.path.length ||
          !field.path.every((part) => typeof part === 'string')
        ) {
          continue
        }
        const path = field.path.slice(0, 8) as string[]
        const sensitive = path.some((part) =>
          /^(key|password|original_password|client_secret|token_hash|token_hint|access_token|refresh_token|header_override|param_override|other)$|(^|_)(secret|password|api_key|private_key|account_balance_token)($|_)/i.test(
            part
              .replaceAll(/([A-Z]+)([A-Z][a-z])/g, '$1_$2')
              .replaceAll(/([a-z])([A-Z])/g, '$1_$2')
          )
        )
        fields.push({
          path,
          old: readValue(field.old, sensitive),
          new: readValue(field.new, sensitive),
        })
        fieldCount++
      }
      targets.push({
        resource: String(target.resource ?? ''),
        id: String(target.id ?? ''),
        name: typeof target.name === 'string' ? target.name : '',
        consistency: String(target.consistency ?? ''),
        fields,
      })
    }
    return {
      outcome: typeof raw.outcome === 'string' ? raw.outcome : 'unavailable',
      truncated: raw.truncated === true,
      targets,
    }
  }
  // Historical adapters are action-specific. Missing snapshots are never
  // reconstructed from the current resource or guessed from a request.
  const op = entry.other?.op
  if (!entry.success || !isRecord(op) || !isRecord(op.params)) return null
  const params = op.params
  let field = ''
  if (entry.action === 'token.status_update') field = 'status'
  if (
    ['user.quota_add', 'user.quota_subtract', 'user.quota_override'].includes(
      entry.action
    )
  ) {
    field = 'quota'
  }
  if (entry.action === 'price_monitor.cost_ratio.update') field = 'cost_ratio'
  let old = params.from
  let next = params.to
  if (entry.action === 'access_token.update') {
    field = 'scopes'
    old = params.previous_scopes
    next = params.scopes
  }
  if (!field || old === undefined || next === undefined) return null
  return {
    outcome: 'committed',
    truncated: false,
    targets: [
      {
        resource: entry.action.split('.')[0],
        id: String(params.id ?? params.target_user_id ?? params.token_id ?? ''),
        name: String(params.name ?? params.target_username ?? ''),
        consistency: 'historical',
        fields: [
          {
            path: [field],
            old: { state: 'value', value: old },
            new: { state: 'value', value: next },
          },
        ],
      },
    ],
  }
}

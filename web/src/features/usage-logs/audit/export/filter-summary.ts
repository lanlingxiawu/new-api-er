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
import type { TFunction } from 'i18next'

const filterLabels: Record<string, string> = {
  success: 'Result',
  category: 'Category',
  actor_type: 'Actor identity',
  auth_method: 'Operation source',
  ip: 'IP',
  ip_risk: 'IP risk',
  route: 'Route',
  route_missing: 'Route',
  username: 'Username',
  token_ref: 'Token identifier',
  exclude_token_ref: 'Token identifier',
  request_id: 'Request ID',
}
const filterValues: Record<string, Record<string, string>> = {
  category: {
    login: 'Login',
    security: 'Account security',
    operation: 'Operation audit',
    access_token: 'Access Token',
  },
  actor_type: {
    root: 'Super administrator',
    admin: 'Administrator',
    employee: 'Employee',
    user: 'User',
    unclassified_user: 'User (employee status not recorded)',
    unknown: 'Unknown identity',
  },
  auth_method: {
    session: 'Web session',
    access_token: 'Access Token',
    unknown: 'Unknown source',
  },
  ip_risk: {
    abnormal: 'Abnormal IPs only',
    attention: 'Needs attention',
    high: 'High risk',
  },
}

export function auditFilterSummary(
  filters: Record<string, unknown>,
  t: TFunction,
  scope: 'all' | 'self' = 'all'
): string {
  return (
    Object.entries(filters)
      .flatMap(([key, value]) => {
        if (
          !filterLabels[key] ||
          value === undefined ||
          value === null ||
          value === '' ||
          value === 'all'
        ) {
          return []
        }
        if (key === 'route_missing' && !value) return []
        if (
          scope === 'self' &&
          [
            'actor_type',
            'route',
            'route_missing',
            'username',
            'ip_risk',
          ].includes(key)
        ) {
          return []
        }
        const label = t(filterLabels[key])
        let displayed = String(value)
        if (key === 'token_ref' && scope === 'self') {
          displayed = t('Current token')
        } else if (key === 'exclude_token_ref') displayed = t('Other tokens')
        else if (key === 'success') {
          displayed = t(
            value === true || value === 'true' ? 'Success' : 'Failed'
          )
        } else if (key === 'route_missing') displayed = t('Not recorded')
        else if (filterValues[key]?.[String(value)]) {
          displayed = t(filterValues[key][String(value)])
        }
        return [t('{{label}}: {{value}}', { label, value: displayed })]
      })
      .join(' · ') || t('All')
  )
}

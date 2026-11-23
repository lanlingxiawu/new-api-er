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
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'

import type { SecurityProviderHealth } from './api'

const errors: Record<string, string> = {
  quota_exceeded: 'Daily quota exhausted',
  timeout: 'Request timed out',
  http_403: 'Provider access denied',
  http_429: 'Provider rate limit reached',
  invalid_response: 'Invalid provider response',
}
export function ProviderHealth({
  providers,
}: {
  providers: SecurityProviderHealth[]
}) {
  const { t } = useTranslation()
  return (
    <StaticDataTable
      data={providers}
      getRowKey={(row) => row.id}
      columns={[
        { id: 'name', header: t('Data provider'), cell: () => t('IP lookup') },
        {
          id: 'status',
          header: t('Status'),
          cell: (row) => {
            if (!row.enabled) return t('Disabled')
            if (row.circuit_until > Date.now() / 1000) {
              return t('Temporarily unavailable')
            }
            if (row.daily_limit > 0 && row.used >= row.daily_limit) {
              return t('Daily quota exhausted')
            }
            return t('Available')
          },
        },
        {
          id: 'used',
          header: t('Daily lookup usage (UTC)'),
          cell: (row) => `${row.used} / ${row.daily_limit || t('Unlimited')}`,
        },
        {
          id: 'error',
          header: t('Last error'),
          cell: (row) =>
            row.last_error
              ? t(
                  errors[row.last_error] ??
                    'Provider unavailable. Check configuration and retry.'
                )
              : '—',
        },
      ]}
    />
  )
}

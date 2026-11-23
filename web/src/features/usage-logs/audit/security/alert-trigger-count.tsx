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

import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import type { SecurityAlert } from './api'

export function AlertTriggerCount({ alert }: { alert: SecurityAlert }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const added =
    alert.repeat_until &&
    alert.status === 'resolved' &&
    alert.confirmed_occurrences != null
      ? Math.max(0, alert.occurrences - alert.confirmed_occurrences)
      : 0
  return (
    <div className='space-y-1 tabular-nums'>
      <span>{formatNumber(alert.occurrences, locale)}</span>
      {added > 0 && (
        <p className='text-muted-foreground text-xs whitespace-normal'>
          {t('New since confirmation: {{amount}}', {
            amount: formatNumber(added, locale),
          })}
        </p>
      )}
    </div>
  )
}

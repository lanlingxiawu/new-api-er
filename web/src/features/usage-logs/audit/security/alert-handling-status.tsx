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

import { StatusBadge, type StatusVariant } from '@/components/status-badge'

const handlingStatuses: Record<
  string,
  { label: string; variant: StatusVariant }
> = {
  open: { label: 'Unprocessed', variant: 'warning' },
  investigating: { label: 'In progress', variant: 'info' },
  resolved: { label: 'Processed · Confirmed', variant: 'neutral' },
  false_positive: { label: 'Processed · False positive', variant: 'neutral' },
}

export function AlertHandlingStatus({ status }: { status: string }) {
  const { t } = useTranslation()
  const display = handlingStatuses[status]
  return (
    <StatusBadge copyable={false} variant={display?.variant ?? 'neutral'}>
      {t(display?.label ?? 'Unknown')}
    </StatusBadge>
  )
}

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
import type { SecurityAlert } from '../api'

export function canConfirmRepeatAlert(alert: SecurityAlert) {
  return (
    (alert.repeat_until ?? 0) > Date.now() / 1000 &&
    !alert.archived_at &&
    alert.rule === 'sensitive_operation' &&
    alert.severity === 2 &&
    (alert.status === 'open' || alert.status === 'investigating') &&
    alert.conclusion !== 'malicious' &&
    alert.conclusion !== 'false_positive' &&
    alert.occurrences > 0 &&
    alert.version >= alert.occurrences
  )
}

// Hits advance both counters; manual edits and responses advance only Version.
// The API repeats this check atomically and remains the authority for saving.
export function canConfirmRepeatSnapshot(
  shown: SecurityAlert,
  latest: SecurityAlert
) {
  return (
    canConfirmRepeatAlert(shown) &&
    canConfirmRepeatAlert(latest) &&
    shown.id === latest.id &&
    shown.status === latest.status &&
    shown.repeat_until === latest.repeat_until &&
    shown.occurrences <= latest.occurrences &&
    shown.version - shown.occurrences === latest.version - latest.occurrences
  )
}

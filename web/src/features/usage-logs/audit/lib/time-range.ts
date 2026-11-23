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
import { dateToUnixTimestamp, getNormalizedDateRange } from '@/lib/time'

export function defaultAuditTimeRange(): { start: number; end: number } {
  // Include all of yesterday and today in the viewer's local time zone.
  const range = getNormalizedDateRange(1, new Date(Date.now()))
  return {
    start: dateToUnixTimestamp(range.start),
    end: dateToUnixTimestamp(range.end),
  }
}

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
import type { CommissionLogPageParams } from '../types'

export const COMMISSION_LOG_MAX_RANGE_SECONDS = 31 * 86400

export function commissionLogQueryParams(params: CommissionLogPageParams) {
  const query = new URLSearchParams({
    pagination: 'cursor',
    page_size: String(params.page_size),
    start_time: String(params.start_time),
    end_time: String(params.end_time),
  })
  if (params.employee_user_id) {
    query.set('employee_user_id', String(params.employee_user_id))
  }
  if (params.customer_user_id) {
    query.set('customer_user_id', String(params.customer_user_id))
  }
  if (params.channel_id) query.set('channel_id', String(params.channel_id))
  if (params.model_name) query.set('model_name', params.model_name)
  if (params.loss_status) query.set('loss_status', params.loss_status)
  if (params.cursor) {
    query.set('cursor_created_at', String(params.cursor.created_at))
    query.set('cursor_id', String(params.cursor.id))
  }
  return query
}

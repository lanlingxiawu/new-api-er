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
import assert from 'node:assert/strict'

import { describe, test } from 'vitest'

import { getAuditTargetUser } from '../format'

describe('getAuditTargetUser', () => {
  test('reads target_user_id written by the backend', () => {
    assert.deepEqual(
      getAuditTargetUser({
        op: {
          action: 'user.topup_complete',
          params: { target_user_id: 42, target_username: 'alice' },
        },
      } as never),
      { id: 42, username: 'alice' }
    )
  })

  // 中间件兜底记录的 action 是 generic，用户 ID 只存在 audit_info.params.user_id 里。
  // 这正是 format.ts 里那段回退逻辑的注释所描述的场景。
  test('falls back to audit_info params on middleware-fallback rows', () => {
    const target = getAuditTargetUser({
      op: { action: 'generic', params: {} },
      audit_info: { params: { user_id: 9 } },
    } as never)
    assert.equal(target?.id, 9)
    assert.equal(target?.username, undefined)
  })

  // 资源类操作的 params.id 是该资源的 ID，不能当成用户。
  test('does not read a resource id as the target user', () => {
    assert.equal(
      getAuditTargetUser({
        op: { action: 'channel.update', params: { id: 7 } },
      } as never),
      null
    )
  })
})

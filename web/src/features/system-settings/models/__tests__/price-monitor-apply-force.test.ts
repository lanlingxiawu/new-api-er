/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'

import { describe, test } from 'vitest'

import { shouldForceApply } from '../price-monitor-apply-force'

describe('shouldForceApply', () => {
  test('第一次提交不带 force', () => {
    assert.equal(
      shouldForceApply({ confirmed: false, serverViolations: 0 }),
      false
    )
  })

  // 「低于渠道最高价」是客户端自己算出来的提示，服务端还没按分组折扣校验过这批价格。
  // 若这次确认就带上 force，服务端的保本价校验会被整单跳过，管理员永远看不到
  // PRICE_BELOW_FLOOR 违规清单，写入却被审计成强制提交。
  test('客户端确认后仍不得跳过服务端保本价校验', () => {
    assert.equal(
      shouldForceApply({ confirmed: true, serverViolations: 0 }),
      false
    )
  })

  // 服务端已经返回过违规清单，管理员看过后再点一次，这才是真正的强制提交。
  test('服务端已判定仍亏损时，再次确认才带 force', () => {
    assert.equal(
      shouldForceApply({ confirmed: true, serverViolations: 2 }),
      true
    )
  })
})

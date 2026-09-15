/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

/**
 * 改价提交是否带 `force`。
 *
 * 弹窗里有两种「需要再确认一次」的来源，但只有后者可以越过服务端校验：
 *  - 客户端提示「低于渠道最高价」：纯展示判断，服务端从未看过这批价格，必须让它先按
 *    分组折扣算一遍保本价（可能返回 PRICE_BELOW_FLOOR）；
 *  - 服务端已返回保本价违规：管理员看过违规清单后仍坚持，这时才允许 force。
 */
export function shouldForceApply(state: {
  confirmed: boolean
  serverViolations: number
}): boolean {
  return state.confirmed && state.serverViolations > 0
}

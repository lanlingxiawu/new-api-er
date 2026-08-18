# 价格巡检：标准阶梯价格对比规则优化

状态：已实现并通过价格巡检定向测试。

## 1. Goals and Scope

优化价格巡检矩阵中 `tiered_expr` 价格的差异判定，避免两条表达式原文不同、但解析后价格阶梯完全一致时仍被标红。

本次范围：

- 调整后端巡检快照生成时的差异标记逻辑。
- 对可安全解析的标准阶梯表达式，按解析后的阶梯、条件、输入/输出价格和附加价格 lane 比较。
- 对无法安全解析的动态表达式，继续按原有保守逻辑处理，不猜测价格。
- 更新相关 Go 单元测试。

不在范围内：

- 不修改价格抓取来源。
- 不修改后台或分享页接口结构。
- 不修改前端表格布局和筛选交互。
- 不自动修复平台价格。

## 2. Data Flow

现有流程保持不变：

```text
价格巡检任务
  -> 读取平台价格、官方价格、渠道价格
  -> priceMonitorCell 将每个来源转换为 PriceMonitorPriceCell
  -> markPriceMonitorDifferences 标记来源相对平台是否不同
  -> buildPriceMonitorMatrix 写入 snapshot.json
  -> 后台 / 分享页读取快照展示
```

本次只修改 `markPriceMonitorDifferences` 在 `priceMonitorModeExpression` 分支里的比较规则：

- 如果平台和来源都能解析出标准阶梯，并且都不是动态表达式，则比较 `Tiers` 的规范化价格结构。
- 如果任一侧是动态表达式，继续按表达式原文比较，避免误判复杂计费规则。
- 差异标记仍写入现有 `Different` / `ModeDifferent` 字段，响应结构不变。

## 3. API Contracts

不新增、删除或修改 API。

受影响但契约不变的接口：

- `GET /api/price_monitor/results`
- `POST /api/price_monitor/public_query`
- 现有 `PriceMonitorPriceCell` JSON 字段保持不变。

鉴权保持不变：

- 后台查询仍走既有管理权限。
- 分享页查询仍走动态访问口令。

## 4. Data Model Changes

无数据库变更。

无新表、无新列、无迁移、无索引变更。

快照文件 `price_monitor/snapshot.json` 的结构不变。`priceMonitorMatrixVersion` 已递增到 9，让部署后的下一次巡检明确生成新规则快照。

## 5. Config Parameters

不新增配置项。

## 6. Key Business Logic and Edge Cases

### 标准阶梯表达式

当两侧都是 `tiered_expr`，且 `parsePriceMonitorExpression` 返回 `dynamic=false` 时，以解析后的结构为准：

- 阶梯数量必须一致。
- 每个阶梯的条件变量、条件操作符、条件值必须一致。
- 输入价格、输出价格使用现有 `nearlyEqual` 容差比较。
- 附加价格 lane 的 key 和价格必须一致。

这样可以覆盖“表达式原文有差异，但页面展示价格完全一致”的场景。

### 动态表达式

以下表达式继续视为动态或不可安全规范化：

- 包含请求头、请求参数、时间条件等运行时规则。
- 包含无法由当前安全解析器识别的函数或复杂运算。
- 标准阶梯数量超出当前支持范围。

这些表达式仍按原文比较。理由是它们的最终价格可能依赖请求上下文，不能只看展示价格推断等价。

### 阶梯顺序

沿用当前解析顺序。当前 UI 展示也是按解析顺序输出，因此比较逻辑与用户看到的顺序保持一致。

### 官方与渠道比较筛选

`channel_official` 过滤已经使用 `priceMonitorPriceCellsEqual` 比较标准化价格结构，本次重点补齐“来源相对平台”的差异标记。实现后，平台、官方、渠道三者在同一标准阶梯价格下不会被错误标红。

## 7. Error Handling Strategy

本次不新增用户可见错误。

解析失败或无法安全解析时不抛错，继续走现有动态表达式保守比较逻辑。巡检任务不会因为单个表达式无法规范化而失败。

## 8. Interaction With Existing Subsystems

- 价格巡检：仅影响快照生成时的差异标记。
- 后台页面：读取相同字段，红色高亮会减少误报。
- 分享页：读取相同字段，展示一致性跟随后端快照。
- 计费系统：只读取配置快照用于巡检展示，不参与实际扣费。
- AI relay：不接入、不调用、不改变。

## 9. Main Chain Impact

本功能不触碰 AI relay 主链路。

同步执行内容：

- 仅在后台价格巡检任务或管理/分享查询中执行。
- 不在客户端 AI 请求的 middleware、relay handler、provider dispatch 或 response streaming 上执行。

异步/后台内容：

- 价格巡检任务本身仍沿用现有后台任务机制。

## 10. Shared Resource Audit

共享资源访问保持现状：

- DB：本次不新增 DB 访问，不修改表。
- Redis：不新增 Redis key，不读写 Redis。
- 文件：继续使用现有 `price_monitor/snapshot.json`。
- 内存：继续使用现有 `PriceMonitorSnapshot` 原子指针快照。
- goroutine / worker pool：不新增 worker，不在 relay 请求中启动 goroutine。

AI relay 主链路不访问 `price_monitor/snapshot.json` 或价格巡检快照内存结构，因此没有主链路资源冲突。

## 11. Concurrency Analysis

本次不触碰 AI relay chain，严格高并发约束不适用。

对巡检任务自身：

- 每个价格 cell 只增加一次轻量结构比较。
- 不新增 DB call。
- 不新增 Redis call。
- 不新增锁。
- 不新增 goroutine。

即使在大量模型和渠道下，成本也与现有矩阵构建规模线性一致，且发生在后台巡检任务中。

## 12. Test Plan

先补测试再实现：

- `markPriceMonitorDifferences`：表达式原文不同但解析后的标准阶梯一致时，不标记差异。
- `markPriceMonitorDifferences`：标准阶梯价格不同仍标记差异。
- `markPriceMonitorDifferences`：动态表达式原文不同仍标记差异。
- 保留现有 `priceMonitorPriceCellsEqual` 标准化比较测试。

实现后运行：

- `go test ./controller`
- 如时间允许，运行 `go test ./...`

## 13. Implementation Notes

实际实现：

- `markPriceMonitorDifferences` 的 `priceMonitorModeExpression` 分支改为：两侧都不是动态表达式时调用 `priceMonitorPriceCellsEqual` 做结构比较；任一侧动态时仍按 `Expr` 原文保守比较。
- `priceMonitorPriceCellsEqual` 的 lane 比较改为按 lane key 匹配，避免表达式中 `p/c/cr/cc` 书写顺序不同导致误报。
- 默认 `comparison=all` 不再返回只表示运行状态或非差异的无效项：`source_failed`、`placeholder` 和无差异来源会被裁剪；这些状态仍可通过专门的 `source_failed` 筛选查看。
- 已补充回归测试覆盖标准阶梯等价、标准阶梯差异、动态表达式保守比较，以及默认差异视图裁剪无效来源。

实现后更新本设计文档状态和实际实现记录。

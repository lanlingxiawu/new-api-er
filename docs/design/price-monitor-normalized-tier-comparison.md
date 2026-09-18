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

## 14. 单档阶梯与按量计费的等价比较（2026-09-15）

### 14.1 问题

§6 只处理了「两侧都是 `tiered_expr`」。来源给的是**只有一档、不带条件**的表达式（如
`tier("base", p * 2 + c * 8 + cr * 0.5)`，解析为一档「全部输入长度」），而平台按量计费时，
`markPriceMonitorDifferences` 在比较价格之前就因 `Mode` 不同返回 `ModeDifferent`。正式环境与测试服务器都能复现
（「平台与官方不一致」里 gpt-4-turbo、gpt-4.1-mini、gpt-4.1-nano、gpt-4o 价格完全相同却整格标红）。
该逻辑自 2115e3f05（2026-08-12）起即如此。

测试服务器快照的统计：

| 来源 | 数量 | 情况 |
|---|---|---|
| 官方 | 14 | 价格完全相同，纯误报 |
| 官方 | 15 | 价格确有差异，但只显示「计费模式不同」，看不出是哪一项 |
| 渠道 | 61 | 同上；且亏损判定（`priceMonitorMeasuredFactor`）、保本下限、「高于平台」都要求模式相同，这些渠道被整体跳过，**亏损不可见** |

### 14.2 规则

「只有一档、无条件、非动态，且所有分项都能用按量计费表达」的表达式单元格，与同价的按量单元格等价。

- **平台按量、来源单档**：来源单元格直接换算成按量单元格（`priceMonitorFlattenSingleTier`）再做逐字段比较。
  之后的差异筛选（input / output / cache）、「高于平台」、亏损判定与保本下限读的都是这个单元格，口径一致，
  前端也因此能逐项标红（按量单元格按 `input_different` 等逐项着色，阶梯单元格只能整格按 `mode_different` 着色），
  前端与 `price_monitor_loss.go` 无需改动。
- **平台单档、来源按量**：只在比较时借用平台的换算结果，**不改写平台单元格**。改价接口靠平台的计费模式判断能否
  行内改价——平台实际按表达式计费时写 `ModelRatio` 不生效，必须继续拒绝。此方向也不参与亏损判定（平台不可行内改价）。
- **不换算**：多档或带条件的阶梯、动态表达式、含按量计费表达不了的分项（`img_o` 图片输出等）——
  换算过去会丢掉这些分项及其真实差异，仍按「计费模式不同」保守判定。
- **1 小时缓存写入（`cc1h`）**：按量平台没有这一项的配置，但计费按「5 分钟缓存写入价 × 6/3.75」计价
  （`relay/helper/price.go` 的 `claudeCacheCreation1hMultiplier`，controller 侧常量 `priceMonitorCacheWrite1hMultiplier`
  必须与之一致），所以可比。`priceMonitorComparablePlatformLanes` 在**比较时**为平台补上推导出的 1 小时价，
  差异标记、「高于平台」、实测倍数、保本下限累加器都读它；不写回平台单元格（否则每个配置了缓存写入的模型都多一行），
  来源没报 1 小时价时也不插空行。平台没配 5 分钟缓存写入时推不出 1 小时价，与其它单侧分项一样算差异。
- **1 小时维度的修复**：它没有独立配置项，建议价与保本下限把 1 小时的要求除以系数折算进 `create_cache_ratio`，
  与 5 分钟的要求取大（同 §13.1 锁定补全倍率折算进 `model_ratio` 的思路），保证建议价能清掉 1 小时维度上的亏损。

测试服务器上，修复第一版（尚未处理 `cc1h`）部署后「平台对比官方」从 41 降到 25，但 Claude 系 61 个渠道单元格与
7 个官方单元格因带 `cc1h` 仍被跳过——这正是亏损不可见的那批，因此补上了上面两条。

`priceMonitorMatrixVersion` 递增到 16（15 是尚未处理 `cc1h` 的第一版）：`priceMonitorSnapshotNeedsRefresh`
在启动时发现旧版本快照即重新巡检，部署后无需等待下一个巡检周期。

### 14.3 不在范围

gpt-4.1 的平台补全倍率配置为 4.000424808836024（输出价 $8.00085，界面四舍五入显示为 $8），与官方 $8 的差异是真实
配置差异，本规则不掩盖它。

### 14.4 测试

`controller/price_monitor_single_tier_test.go`：同价单档不再计入「平台对比官方」；价格不同时标到具体字段；
多档 / 不可表达分项 / 动态表达式保持模式不同；平台单档时平台单元格不被改写；单档渠道重新参与亏损判定、
保本下限与「高于平台」。

## 15. 表达式识别的两份清单

`controller/price_monitor.go` 里有两份必须跟着 `pkg/billingexpr` 走的清单，各自对应一种失效方式：

**`priceMonitorDynamicMarkers`（动态记号）** —— `|||`、`header(`、`param(`、`hour(`、`minute(`、
`weekday(`、`month(`、`day(`，覆盖 `pkg/billingexpr/compile.go` 的 `usesRequestProbe` 全部函数。
少写一个，随时间或请求浮动的表达式会被当成固定价与上游静态价比对，把按月 / 按日促销的模型误报成亏损。
注意有些写法只在**单个** `tier(...)` 之外带时间条件（例如 `tier("standard", p*2 + c*8) * (month("UTC") == 11 ? 0.5 : 1)`），
阶梯本身能安全解析，只有这份记号清单能拦住它。

**`priceMonitorExpressionVariables`（计费变量）** —— `p|c|cr|cc|cc1h|img|img_cr|img_o|ai|ao`，
三处正则（系数提取、安全阶梯主体、阶梯条件的数字格式）共用同一份常量拼接，避免再次漂移。
少写一个变量，`priceMonitorSafeTierBody` 会拒绝整个阶梯主体、该表达式被判为动态，含该变量的模型整行
**静默**退出价格比对、亏损判定与保本下限——没有任何告警，只是永远不再报差异。`img_cr` 就是这样漏掉的
（`setting/billing_setting/builtin_billing.go` 的 `gpt-image-2` 系列内置价就带它）。

`img_cr` 对应新的分项键 `image_cache_read`（前端文案见 `price-monitor-panel.tsx` 的 `laneLabel`）。
与 `img_o` 一样，平台侧没有对应的按量配置项，因此 `priceMonitorTokenLaneKey` 不认它：含该分项的单档
表达式不会被换算成按量单元格，而是保留阶梯形态按分项逐项比较。

改动 `priceMonitorMatrixVersion`（现为 18）会让已存快照在下次巡检时重建，解析口径变更必须一并提升。

**测试**：`controller/price_monitor_expression_variables_test.go`。

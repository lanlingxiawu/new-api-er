# 行内改价的「保本下限」逐项校验

状态：**已实现**
日期：2026-09-07

## 1. 目标与范围

行内改价（`POST /api/price_monitor/apply_price`）目前允许管理员填入任意非负价格，
**没有任何「这个价格是否真的止住了亏损」的校验**。本设计给每个可改字段计算一条
**保本下限**，在编辑时逐项比对并提示。

范围内：

- **移除「目标毛利价」档位**（见 §10）。改价档位简化为「保本价 / 手填」两档。
- 按**字段**（`model_ratio` / `completion_ratio` / `cache_ratio` / …）分别计算下限，
  而不是给整行一个总判定。
- 下限取**所有可比渠道**里最严格的那一个，而不是只看触发判定的那一个渠道。
- 前端在建议价与手填价两种模式下都做逐项比对与标注。
- 后端在 `apply_price` 落库前复算同一条下限（前端可绕过，服务端必须自证）。

范围外：

- 阶梯表达式（`tiered_expr`）计价的模型：行内改价本来就不支持（§4.4 既有决定），下限也不产出。
- 自动改价 / 定时止损：本设计只做「人在回路」的校验与提示。
- 用户专属倍率：售价系数不含它（与既有 `loss_risk` 判定口径一致），UI 沿用现有标注。

## 2. 为什么现在的实现不够

### 2.1 只保护了一个渠道

`findRepairTarget` 遍历 `header.type === 'channel'`，返回**第一个** `isRepairable` 的单元格：

```ts
for (const header of headers) {
  if (header.type !== 'channel') continue
  const source = item.prices[header.key]
  if (!isRepairable(source)) continue
  return { ... }   // 第一个就返回
}
```

建议价由这一个渠道的报价推导。若同一模型下另有报价更高的渠道，按第一个渠道修完之后
**对更贵的那个仍然亏损**。这正是本次要解决的「避免于上游最高的亏损」。

### 2.2 判定塌缩成一个标量

```ts
const projected = projectedMeasuredFactor(target, values)   // Math.max(...ratios)
const cleared = projected <= sellFactor
```

`projectedMeasuredFactor` 把 input / output / 各 lane 的比值取最大值。管理员只看到
「整体清没清干净」，看不出**是哪一项**没达标，也就无法只调那一项。

### 2.3 手填模式没有校验

```ts
function adviceValuesFor(target, mode) {
  if (mode === 'breakeven') return advice.breakeven
  if (mode === 'target') return advice.target
  return undefined            // manual
}
```

`manual` 返回 `undefined` → `projected` 为 `undefined` → `cleared` 恒为 false，
但**不产生任何逐项反馈**。管理员手填一个仍然亏损的价格，界面不会拦、也不会提示。

### 2.4 服务端零校验

`ApplyPriceMonitorPrice` 只校验：快照未过期、版本/取值乐观并发、字段白名单、值非负且有限。
**没有任何亏损相关的判断**。前端提示可被绕过（直接调接口）。

## 3. 计算口径

### 3.1 单渠道单字段的约束

对渠道 `s`、维度 `d`，止损条件是「平台售价 × 该渠道流量的售价系数 ≥ 上游报价」：

```
P_d × g_s  ≥  C_{s,d}
```

- `P_d`：平台在维度 `d` 上的**展示价**（每百万 token）
- `g_s`：该渠道的售价系数 `sell_factor`（`priceMonitorSellFactor`，取该渠道实际服务分组里最小的分组倍率）
- `C_{s,d}`：渠道 `s` 在维度 `d` 上的上游报价

因此单渠道下限：`P_d ≥ C_{s,d} / g_s`。

### 3.2 跨渠道取最严

```
floor_d = max over s ∈ 可比渠道  ( C_{s,d} / g_s )
```

**注意除的是各渠道自己的 `g_s`**，不是全局最小倍率——不同渠道可能服务不同分组，
售价系数不同，报价最高的渠道未必是约束最紧的那个。先除再取 max 才正确。

### 3.3 展示价 → option 字段值

下限最终要和输入框里的 option 字段值比对，换算沿用 `priceMonitorAdviceValues` 的既有逆运算：

| 字段 | 换算 |
|---|---|
| `model_ratio` | `floor_input / 2` |
| `completion_ratio` | `floor_output / floor_input` |
| lane 类（`cache_ratio` 等） | `floor_lane / floor_input` |
| `model_price`（per_request） | `floor_price` |

> `completion_ratio` 与 lane 类是**相对 input 的比值**，所以它们的下限依赖 input 的取值。
> 这带来一个真实的耦合：管理员调低 `model_ratio` 会抬高 `completion_ratio` 的下限。
> 见 §5.2。

### 3.4 「可比」的定义

完全复用 `priceMonitorMeasuredFactor` 的既有规则，不另立一套：

- 跳过 `unavailable_reason` 非空的单元格（抓取失败 / 占位价 / 该来源无此模型）
- 跳过 `mode` 与平台不一致的单元格
- 跳过 `Dynamic` 或阶梯条件不匹配的表达式单元格
- 只有两侧都配置了的维度才参与（单侧有值不算）

若某字段没有任何可比渠道，该字段**不产出下限**（UI 显示「—」，不做判断），
而不是产出 0 —— 后者会被误读成「随便填都安全」。

## 4. 数据流与 API

### 4.1 在后端计算，随巡检快照产出

下限**必须由后端算**，理由与 `suggestion` 完全相同：展示价与 option 字段值的换算只应存在
一处，前端不重复实现（这条在 `PriceMonitorPriceAdvice` 的注释里已经写死）。

更关键的是**前端目前拿不到算下限所需的数据**：`applyPriceMonitorLossVerdicts` 只在
**命中亏损**的单元格上写 `sell_factor`：

```go
kinds := priceMonitorLossKinds(measured, context.Configured, context.SellFactor)
if len(kinds) == 0 { continue }
source.SellFactor = floatPointer(context.SellFactor)   // 只有命中才写
```

而下限要考虑**所有**可比渠道（包括没命中亏损的），它们的 `sell_factor` 前端根本看不到。

### 4.2 新增结构

在矩阵项上新增一个与来源无关的字段：

```go
// PriceMonitorRepairFloor 是「把平台价改到多少才对所有可比渠道都不亏」的逐字段下限。
type PriceMonitorRepairFloor struct {
    Mode string `json:"mode"`
    // Fields 是可直接与改价输入框比对的 option 字段值下限。
    Fields map[string]float64 `json:"fields,omitempty"`
    // Display 是对应的展示价下限（每百万 token），用于文案。
    Display map[string]float64 `json:"display,omitempty"`
    // Binding 记录每个字段的下限由哪个渠道决定，便于管理员追因。
    Binding map[string]string `json:"binding,omitempty"`
}
```

挂在 `PriceMonitorMatrixItem` 上：`RepairFloor *PriceMonitorRepairFloor`。

**为什么不挂在 platform 单元格上**：它描述的是「这一行所有渠道的联合约束」，
不属于任何单个来源；挂在 item 上语义更准，也避免 `queryPriceMonitorMatrix` 里
按 `displayKeys` 过滤单元格时被连带丢掉。

### 4.3 计算时机

与 `applyPriceMonitorLossVerdicts` 同一趟（`runPriceMonitorCheck` 内），新增
`applyPriceMonitorRepairFloors(headers, items, contexts)`。复用已有的
`priceMonitorLossContext`（已含每渠道的 `SellFactor`），不额外查库。

**开关**：跟随既有的 `LossDetectionEnabled`。关掉亏损判定时下限也不产出——
两者口径同源，分别开关只会让人困惑。

### 4.4 `apply_price` 服务端复算

`ApplyPriceMonitorPrice` 在构造 patch 时，对每个字段与快照里的 `RepairFloor.Fields` 比对，
低于下限则拒绝并回传具体字段与差额；请求带 `force: true` 时放行（§7-1）。

## 5. 边界与取舍

### 5.1 下限来自快照，可能已过期

`RepairFloor` 是巡检时刻算的。管理员改价时上游可能已经涨价。
既有的 `checked_at` 快照校验已经覆盖这个方向（快照更新过就拒绝），不额外处理。

### 5.2 比值型字段的耦合

`completion_ratio` / lane 类的下限依赖当前 input。若管理员在同一次提交里同时改了
`model_ratio` 和 `completion_ratio`，用**提交值**里的 input 重算这些字段的下限，
而不是用快照里的 input——否则会给出自相矛盾的提示。

前端逐项校验时同样按输入框的**当前值**实时重算，保证所见即所得。

### 5.3 `sell_factor` 为 0 或缺失

`priceMonitorSellFactor` 在渠道无分组或分组倍率全为 0 时回退为 1（免费分组视为不参与）。
沿用该回退，不产出无穷大下限。

### 5.4 只命中 configured 亏损的行

仍然**不给**改价入口（既有设计，改价不改毛利率）。但如果该行同时有 measured 亏损，
下限照常产出。

## 6. 前端改动

`price-monitor-apply-dialog.tsx`：

1. 每个字段行增加一列「保本下限」，显示 `RepairFloor.Fields[field]`，
   并用 tooltip 标注 `Binding[field]`（由哪个渠道决定）。
2. 输入值 `< floor` 时该行标红 + `FormMessage` 提示，**逐项**而不是整行。
3. `manual` 模式同样走这条校验（这是 §2.3 的直接修复）。
4. 保留现有的 `projectedMeasuredFactor` 汇总结论作为整体状态，但它不再是唯一信号。
5. 档位从 `breakeven | target | manual` 简化为 `breakeven | manual`（§10）。

样式复用现有表格与 `text-destructive`，不引入新组件（Rule 6）。

## 7. 已定的取舍

| # | 决策点 | 采用 | 理由 |
|---|---|---|---|
| 1 | 服务端遇到低于下限的字段 | **拒绝，但支持 `force: true` 覆盖** | 默认拦住误操作且服务端自证安全；同时保留「亏损引流」这类刻意低价的口子。纯前端警告等于没有服务端保证——直接调接口即可绕过。 |
| 2 | 下限是否计入**官方价格**来源 | **只看渠道** | 渠道报价才是真实采购成本。官方价是对比基准，计入会把下限抬到没有成本依据的高度，导致过度提价。 |
| 3 | 没有任何可比渠道的字段 | **不产出下限**（UI 显示「—」） | 产出 0 会被误读成「随便填都安全」。 |

`force` 的交互：前端检出低于下限时，主按钮变为二次确认（列出具体哪些字段、差多少），
确认后带 `force: true` 重发。审计日志记录 `forced: true` 与被突破的字段。

## 8. 测试计划

- `priceMonitorRepairFloor` 单测：多渠道不同 `sell_factor` 时取 `max(C/g)` 而非 `max(C)/min(g)`；
  单侧缺失维度不参与；不可比来源被跳过；无可比渠道时不产出。
- 比值型字段耦合：同一次提交里改了 input 时，`completion_ratio` 下限随之变化。
- `apply_price`：低于下限的提交被拒绝且**一个字段都不写**；带 `force: true` 时放行并记审计。
- 真库回归：`RepairFloor` 随快照序列化 / 反序列化不丢字段。
- 前端无测试框架，靠 `typecheck` + 手工验收（Rule 15.7）。

## 9. 与现有子系统的关系

- **不触及 relay 主链路**：全部发生在巡检任务与管理接口上，无同步调用、无共享 Redis/DB 热点，
  因此不需要 Rule 0 的「主链路影响」与并发分析章节。
- **计费**：只影响管理员改价前的提示与校验，不改变任何计费公式。
- **`PatchPricingOptions`**：下限校验发生在进入事务**之前**，不改变既有的行锁与版本语义。


## 10. 移除「目标毛利价」档位

改价档位只保留**保本价**与**手填**。目标毛利价（`P_i(m) = C_i / (g × (1-m))`）去掉——
毛利目标是定价策略，不该混在「止损修复」这个动作里；而且它和本设计的保本下限是两条
不同口径的建议价，同时存在只会让管理员困惑该信哪个。

移除清单（该配置**未暴露在设置页 UI**，因此改动面比预期小）：

| 位置 | 改动 |
|---|---|
| `controller/price_monitor.go` | `PriceMonitorPriceAdvice` 去掉 `Margin`、`Target` 字段 |
| `controller/price_monitor_loss.go` | `priceMonitorPriceAdvice` 去掉 `targetMargin` 形参与 `advice.Target` 赋值；`applyPriceMonitorLossVerdicts` 去掉该形参 |
| `controller/price_monitor_task.go:208` | 调用处去掉 `setting.TargetMargin` 实参 |
| `setting/price_monitor_setting/config.go` | 删除 `TargetMargin` 字段、`defaultTargetMargin`、`maximumTargetMargin` 及 `Normalized()` 里的钳制 |
| `controller/price_monitor_loss_test.go` | 删除 `TestPriceMonitorPriceAdviceTargetMargin` |
| `web/.../price-monitor-apply-dialog.tsx` | `RepairMode` 去掉 `'target'`；删除该档位的选项与列；`adviceValuesFor` 去掉分支 |
| `web/.../system-settings/types.ts` | `PriceMonitorPriceAdvice` 去掉 `margin`、`target` |
| `web/src/i18n/locales/*.json`（7 个） | 删除 `"Target margin price"` |

**兼容性**：`config.target_margin` 会从 `/api/price_monitor/status` 的响应里消失。
该字段没有前端消费方（已核实），DB 里 `price_monitor_setting.target_margin` 的残留行由
ConfigManager 忽略，无需迁移。


## 11. 实现回填

与设计一致的部分不再重复，只记录实现时定下来的细节与两个设计文档没写到的坑。

### 11.1 `RepairFloor` 必须按下标写回

`applyPriceMonitorRepairFloors` 一开始写成 `for _, item := range items`，
`item.RepairFloor = ...` 落在 range 的**值拷贝**上，全部丢失。
判定那一趟（`applyPriceMonitorLossVerdicts`）用值拷贝没事，只是因为它改的是
`item.Prices` —— map 是引用类型。改成 `for i := range items { item := &items[i] }`。
回归用例 `TestRepairFloor_DividesEachChannelBySellFactorBeforeMax` 里
「floor must be written back to the slice element」这条断言就是钉这个的。

### 11.2 比值型字段的基准：用**平台当前** input，不是下限自身的 input

设计 §5.2 只说了「用提交值里的 input 重算」，没说管理员**没改** `model_ratio` 时用什么。
实现里一开始退回 `floor.Display["model_ratio"]`（下限自身的 input），这是错的：
下限 input ≥ 当前 input，基准取大会让比值门槛变**松**，管理员只改
`completion_ratio` 时会放过真正的亏损。正确基准是平台**当前** input。
见 `priceMonitorEffectiveInput` 与 `TestFloorViolation_RatioFieldUsesEffectiveInput`。

### 11.3 判定用 `>=` 而不是 `>`

恰好等于下限就是保本，不算亏损。浮点比较统一走 `nearlyEqual`，
避免 `0.1+0.2` 这类误差把保本价判成违规。

### 11.4 前端档位与二次确认

`RepairMode` 现为 `breakeven | manual`。低于下限时主按钮先变成
「N 个模型低于保本下限」，再点一次才变「仍然应用」并带 `force: true` 提交；
任何输入改动都会重置该确认状态。

### 11.5 未做

- **并发**：下限只在巡检时算，管理员改价期间上游涨价不会实时反映；
  由既有的 `checked_at` 快照校验兜底（快照变了就拒绝）。
- **`force` 的权限**：目前任何有 `billing.model-pricing` 编辑权的管理员都能强制。
  没有为它单设更高的权限位。


## 12. 第三方审计修订（9 项）

外部审计发现 9 个问题，其中 5 个是本设计与每日限额功能的真实业务缺陷。逐条修订：

### 12.1 校验必须建立在「合并后的完整生效价格」上（审计 #3）

原实现只遍历 `item.Fields`（本次提交的字段）。比值型字段的实际售价是
`input × ratio`，把 input 从 20 砍到 2 而不动 `completion_ratio(=1)`，输出价同样从
20 掉到 2；因为 `completion_ratio` 没出现在提交里就被整个跳过，接口返回成功。

改为遍历 `floor.Fields`，缺失字段用**当前生效值**补齐后再判。
回归：`TestFloorViolations_CatchesUnsubmittedKnockOn`。

### 12.2 补齐时必须读当前价，不能读巡检快照（审计 #4）

`priceMonitorEffectiveInput` 原本回退到快照里的 `platform.Input`。快照可能是几小时前的，
期间平台价已被改过；客户端只要取到最新 `pricing_version`，版本校验就拦不住，
于是拿旧 input 算出的比值门槛偏松。改为读 `ratio_setting` 的实时值。
回归：`TestFloorViolations_UsesCurrentPricingNotSnapshot`。

### 12.3 未持久化的默认价不是「未配置」（审计 #5）

`readPricingOptionForUpdate` 只看 options 表，而内置默认价在
`ratio_setting.InitRatioSettings()` 时才灌进内存映射、并不落库。巡检页显示的是生效价、
客户端照它回传 `expected`，这里却判 `exists=false` → 永远 `ErrPricingValueConflict`，
刷新多少次都改不动。

新增 `pricingEffectiveValue` 作为比对兜底（**只比对、不写回**，否则会凭空持久化几百个键）。

**只在 `Expected != nil` 时兜底**：`Expected == nil` 表示客户端认为该模型完全没有配置，
那本来就该按 options 表的事实判断。对 nil 也套用会凭空制造冲突，并让结果依赖内存映射
被哪个用例先填过——第一版就是这么把
`TestPatchPricingOptionsExpectedNilMeansUnconfigured` 变成顺序依赖用例的。

### 12.4 改价按钮的权限与接口不一致（审计 #9）

前端用巡检页自身的编辑权开按钮，后端要的是
`SystemSettingsEdit("billing.model-pricing")`。结果是有定价权的人点不到、
没定价权的人点了必然失败。新增 `canRepairPricing` 单独传递。

### 12.5 真库用例不得破坏开发库定价（审计 #1）

`TestPatchPricingOptions_OnRealDatabase` / `_LockedTxIsNotReusable` 直接覆盖
`ModelRatio` 再整行删除。本文件里 `TestPricingOptionsLockingOnRealDatabase` 早有
「不触碰开发库里的活价格配置」的备份/还原写法，新增用例当时没沿用。
已抽出 `preservePricingOption` 并改用；另外补了 `ensureOptionMap()`，
否则单独 `-run` 时 `common.OptionMap` 为 nil 会 panic（用例不能依赖别的用例先跑过）。

## 13. 未提交代码审计修订（2026-09-15）

两处缺陷都经真实复现确认（先写断言正确行为的用例，在修复前失败）。

### 13.1 补全倍率被系统锁定的模型

**现象**：`gpt-3.5*`、`gpt-5`（不带点）、`gpt-5.4*`、`o1/o3`、`claude-3*`、Claude 4 系
（sonnet/opus/haiku-4）、`mistral-*` 等模型的补全倍率由 `getHardcodedCompletionModelRatio`
锁定，`GetCompletionRatio` 对它们**不读** `CompletionRatio` 覆盖值。旧实现照样写入并报告
`applied:[completion_ratio]`，保本复算也用提交值判「达标」——实测提交 10、库里存 10、
计费仍按 5，低于下限 8，亏损持续而界面显示已修复。

**修复**：

- `ApplyPriceMonitorPrice`：锁定模型提交非 null 的 `completion_ratio` 时整单拒绝
  （`price_monitor.completion_ratio_locked`，文案指明应改模型倍率）。（原先放行的删除 null 已在 §14 改为一律拒绝。）
- 建议价 `priceMonitorAdviceValues` 与保本下限 `buildPriceMonitorRepairFloor` 接收锁定倍率
  （`priceMonitorLockedCompletionRatio`）：输出侧的要求折算进 input（`input ≥ output / 锁定倍率`），
  只产出 `model_ratio`，不再产出 `completion_ratio`；下限的署名取决定它的那一侧渠道。
  前端弹窗的字段列表完全来自建议价，因此自然不再出现不可改的输入框，前端无需改动。
- `advice.Current` 只作乐观并发的 expected，按原样换算、不做折算，避免浮点误差让 expected 与库值对不上。
- 服务端复算本身此前就是对的：未提交 `completion_ratio` 时读 `GetCompletionRatio`，拿到的正是锁定值。

用例：`controller/price_monitor_apply_guard_test.go`（拒绝锁定字段且整单不写、锁定模型改
`model_ratio` 放行、删除锁定覆盖放行、建议价折算、下限折算及署名）。

### 13.2 删除覆盖（null）按删除后的回退值复算（已被 §14.3 取代）

**现象**：`priceMonitorEffectiveFieldValue` 把 null 当成「没提交」，用删除**前**的当前值复算。
删除 gpt-4o 上 `completion_ratio=8` 的覆盖，计费回退到硬编码 4（低于下限 6），却没有
`PRICE_BELOW_FLOOR`、直接成功。

**修复**：null 时改用 `ratio_setting.PricingValueWithoutOverride(optionKey, model)`——各 getter
「表里没有该模型条目」时的返回值与 ok，逐一对应 getter 的未命中分支；
`setting/ratio_setting/pricing_fallback_test.go` 用表里不存在的模型名对照真实 getter 锁住一致性，
任何一边改了回退逻辑都会失败。`priceMonitorEffectiveInput` 走同一口径。删除导致的违规
`submitted=true`（是管理员这次操作的直接后果）。

前端不会发出 null（手填模式清空输入框即跳过该字段），本缺陷只能经直接调用接口触发；修复后
直接调用方收到 `PRICE_BELOW_FLOOR`，可带 `force` 重发。

用例：`TestApplyPriceMonitorPriceChecksDeletionAgainstFallback`、
`TestApplyPriceMonitorPriceAllowsDeletionAboveFloor`、`TestFloorViolations_DeletionUsesFallbackValue`（均随 §14.3 删除）。

## 14. 审查修订（2026-09-15）

对价格巡检（含管理页与改价弹窗）做了一轮审查，修复与删减如下。快照版本升到 17（结构有变，启动后自动重建）。

### 14.1 行内改价冲掉内存里的整张价格表（高）

`readPricingOptionForUpdate` 只读 options 表，写回后 `updateOptionMap` 按整表替换内存。某项价格（如 `CacheRatio`）从没在倍率页保存过时，表里是空的、内存里是内置默认表：改一个模型，其它模型的默认价全部被冲掉（缓存读取回落到 1、按全价计费；若是 `ModelRatio`，靠默认倍率的模型直接「价格未配置」）。

修复：option 从没保存过（值为空）时，以内存整表为底打补丁，写回的是「默认表 + 本次修改」。已保存过的 option 仍按表里的值打补丁（`pricingEffectiveValue` 只参与比对，不并入写回）。用例 `TestPatchPricingOptions_UnsavedOptionKeepsBuiltinDefaults`。

### 14.2 保本价与保本下限合为一套

原先每个亏损渠道单元格各带一份 `suggestion.breakeven`（只按这一个渠道算），行上另有 `repair_floor`（所有可比渠道取最严）。两者口径不同：弹窗的「保本价」取第一个命中渠道，可能自己就低于保本下限。

现在只保留 `repair_floor`：它就是这一行的保本价。新增 `repair_floor.current`——各字段当前的原始 option 值，作为改价请求的 expected（字段缺失 = 当前未配置，expected 传 null）；由构建矩阵时读取的原始值填充，不从展示价倒推（锁定或内置规则下的输出价并不来自 `CompletionRatio`）。删除 `PriceMonitorPriceAdvice` / `PriceMonitorAdviceValues` / `priceMonitorPriceAdvice` / `priceMonitorAdviceValues`。改价入口只在「有 measured 亏损的渠道 + 行有 repair_floor + 平台按量或按次」时出现。

### 14.3 改价接口的字段约束

- 不接受删除（null）：删覆盖后回退到哪个值取决于各 getter 的内置规则，接口没法自证安全；前端本就不发 null。随之删除 `PricingValueWithoutOverride` 回退镜像及其测试。
- 字段必须与平台计费模式一致：按量模型只接受比值字段（写 `model_price` 会把它悄悄切成按次计费且不受下限约束），按次模型只接受 `model_price`（比值字段不生效）。
- 单次改价模型数上限改为常量 100（原 `apply_max_models` 配置项无设置入口）。

### 14.4 平台输出价取计费实际值

补全倍率被系统锁定、或映射表里没有该模型时，平台单元格的输出价改为 `input × GetCompletionRatio(model)`。原先只读映射表，Claude 4、gpt-4o 等锁定模型的平台输出价为空，输出维度不参与亏损判定与保本下限。

### 14.5 其它

- 分享页（持分享口令即可访问）不再返回 `repair_floor`：逐字段保本价与决定它的渠道是内部定价信息。
- 删除亏损判定开关 `loss_detection_enabled`（没有设置入口，判定始终开启）。
- 前端：
  - 改价弹窗不再因 30 秒轮询被重置（targets 记忆化，只在打开时初始化）。
  - 客户端下限检查与服务端一致（覆盖下限里的每个字段，未提交的用当前值）。
  - 服务端返回 `PRICE_BELOW_FLOOR` 时列出违规字段并进入强制确认，只弹一次提示。
  - 改价成功的模型在本快照内标为「已修改，下次巡检后更新」，不再能重复提交。
  - 删除「改后成本系数」列。
  - 阶梯表达式行提示去模型定价设置里修改。

### 14.6 改价弹窗按价格编辑（2026-09-15，用户确认）

- 档位改为「最高价 / 手动设置」。「最高价」= 每一项取**现价与渠道最高价中较高的一个**：只抬价、不降价（HK 测试发现按字面取渠道最高价会把已高于所有渠道的项调低，例如缓存读取 $0.075 → $0.03，用户确认改为取较高值）；没有渠道报价的项保持现价。渠道最高价 = 该项在所有可比渠道**原始报价**里的最高值（不区分渠道、不除售价系数）。后端在 `repair_floor.highest` 给出（展示价，按量为每百万 token、按次为每次；1 小时缓存写入折算进缓存写入价），`repair_floor.locked_completion_ratio` 给出锁定的补全倍率。锁定模型的输入价取 `max(输入最高价, 输出最高价 / 锁定倍率)`，输出价随输入价联动、不可单独改。
- 手动设置与模型定价页一样按价格填写（复用 `PriceInput`：`$ … $/1M`；按次为 `$ … 每次`），输入框下显示换算出的倍率；提交时换算：`model_ratio = 输入价 / 2`，其余比值字段 `= 该项价格 / 输入价`，按次即价格本身。只提交有变动的字段；输入价变了时比值字段按新输入价重算，保持填写的价格不变。
- 弹窗按模型分卡片，每行「项目 / 当前价格 / 渠道最高价 / 新价格」。每一项新价格与渠道最高价比较，低于时行内警告、卡片标出数量，应用前需再确认一次（带 `force`）。
- 服务端的保本校验（报价 ÷ 售价系数，§3）保持不变：分组有折扣时，即使不低于渠道原始报价也可能被判仍亏损，弹窗会标出这些项并要求确认。
- 已知：监控矩阵把音频输出价按「输入价 × audio_completion_ratio」展示，而模型定价页按「音频输入价 × audio_completion_ratio」换算，两者口径不一致；弹窗跟随监控矩阵与服务端下限的口径，是否统一待确认。

# 价格巡检：亏损风险判定与页面内改价

> 2026-09-15 审查修订：逐渠道的 `suggestion`（建议价）已删除，改价的保本价统一取行上的 `repair_floor`（含 `current` 作为 expected）；亏损判定开关 `loss_detection_enabled` 与 `apply_max_models` 配置项已删除；改价接口不再接受 null 删除；快照版本为 17。详见 [price-monitor-inline-repair-floor.md §14](price-monitor-inline-repair-floor.md)，本文相关段落以其为准。
>
> 2026-09-10 实现核对补充：当前代码在所有分组倍率为零时也回退 sellFactor=1，与下文“全部为零则不判定”存在差异；改价的 null 删除语义与下限校验尚需统一；数据库事务后的各份倍率内存映射是逐项发布。具体事实与待确认修正见 [整体设计第 8 节](uncommitted-changes-overview.md)。这些边界未在本轮修改实现，文中既有测试记录亦未重新验证。

状态：**已实现**。后端 + 管理页 + 分享页均已落地，价格巡检定向测试与 `go test ./...` 全绿（一个无关的既有失败见 §13）。

关联文档：
- `docs/design/price-monitor-above-platform-filter.md`（已实现；**本设计不修改其任何口径**）
- `docs/design/price-monitor-comparison-model-counts.md`（统计口径约束）
- `docs/design/model-price-consistency-monitor.md`（巡检总体设计）

---

## 1. Goals and Scope

### 1.1 问题陈述

已实现的 `above_platform` 只比较裸倍率，**漏报真实亏损**。平台自身的成本模型定义在 `service/employee_commission.go:299` `calcCostQuota`：

```text
revenue = P × g × tokens          P = 平台列表价，g = 生效分组倍率
cost    = revenue / g × r         r = 渠道成本系数 ChannelCostConfig.cost_ratio（默认 1.0）
        = P × r × tokens
profit  = P × tokens × (g − r)
```

`渠道价 > 平台价` 等价于「实测成本系数 > 1.0」，隐含假设所有分组倍率都是 1.0。当 `g = 0.7`、实测成本系数 `0.85` 时已经在亏 15%，现状不报。

发现问题后也没有修复路径：巡检是独立顶级页，改价在 系统设置 › 计费 › 倍率设置，要换页、重选渠道、重拉一次上游、逐字段手选。

### 1.2 目标

| # | 目标 |
|---|---|
| G1 | 新增与平台成本模型一致的亏损判定 `loss_risk`，区分**实测成本风险**与**配置成本风险** |
| G2 | 在巡检页直接改平台价，给出**保本价 / 目标毛利价**，支持批量 |
| G3 | 改价具备数据库级并发安全，不会互相覆盖 |

### 1.3 范围

**In scope**

- 新增 comparison 取值 `loss_risk` 与统计计数 `loss_risk`；命中单元格附带判定依据与建议价。
- 新增 `POST /api/price_monitor/apply_price`：按模型局部改价，支持批量，单次一个事务。
- 新增 `model.PatchPricingOptions` 共享能力：事务内行锁重读 + 持久化版本号校验。
- 倍率设置页接入同一版本号，使跨页覆盖可被检测并拒绝。

**Out of scope**

- `above_platform` 的判定、统计、筛选、分享页按钮**一律不动**。
- 不改抓取来源与抓取实现；models.dev 不参与亏损判定与统计；正式官方来源也不参与（理由见 §3.2）。
- 不自动改价、不自动禁用渠道或模型；所有写操作由管理员显式触发。
- 不支持 `billing_mode` / `billing_expr`（阶梯表达式）的行内编辑（理由见 §4.4）。
- 分享页只读，不提供改价入口。
- 不回写 `ChannelCostConfig.cost_ratio`。巡检只**读**它做判定。

**已知覆盖缺口（不是遗漏，是本次的明确边界）**

`loss_risk` 的售价系数**不包含用户专属倍率**。`ResolveGroupRatio`（`setting/ratio_setting/user_exclusive_ratio.go:155`）的第一优先级是用户专属倍率，但求它的最小值需要扫 `users` 表的 `group_ratios` 字段——`users` 是 relay 路径读取的表，无合适索引，为一个最小值扫全表违反 Rule 0 的共享资源约束。因此当 `userExclusiveGroupRatioEnabled` 开启时，本判定会**低估**亏损面，UI 必须显式标注（§3.3），不得让绿色被理解为「安全」。

---

## 2. Data Flow

```text
[巡检任务 · master node · 后台 goroutine]
  抓取平台/官方/渠道价格（不变）
    -> buildPriceMonitorMatrix / markPriceMonitorDifferences（不变）
    -> above_platform 相关判定（不变，逐位保留）
    -> priceMonitorLossVerdict(model, channelSource)               ← G1 新增
         sellFactor = min over 可达分组对 of ResolveGroupRatio(nil, ...)
         measured   = max over 可比维度 of (渠道值 / 平台值)
         configured = GetChannelCostRatio(channelId)
         lossKinds  = {"measured"}   若 measured   > sellFactor
                    ∪ {"configured"} 若 configured > sellFactor
         suggestion = 逐维度保本价 / 目标毛利价                     ← G2
    -> countPriceMonitorComparisonModels（新增 LossRisk 计数）
    -> 保存 snapshot.json（结构不变，仅单元格多几个字段）

[管理端 · 巡检页]
  GET /api/price_monitor/results?comparison=loss_risk
    -> 命中行带 loss_kinds / sell_factor / measured_factor /
       configured_factor / suggestion
    -> 含 measured：可勾选 -> 批量预览（现值 / 建议值 / 改后系数 / 改后判定）-> 一次确认
    -> 仅 configured：不显示改价按钮，显示指向 渠道成本系数 / 分组倍率 的链接
    -> POST /api/price_monitor/apply_price
         -> model.PatchPricingOptions
              事务内按 option key 升序行锁重读
              校验 PricingConfigVersion + 逐模型字段 expected
              只 patch 目标模型键 -> 版本 +1 -> 提交
         -> 既有 updateOptionMap 刷新内存快照
```

---

## 3. G1 — 亏损风险判定

### 3.1 两类风险，两套数学

设某可比维度上平台列表价 `P`、渠道价 `C`、生效分组倍率 `g`、渠道成本系数 `r`。

**实测成本风险（measured）** — 上游实际报价高于我们的售出价：

```text
每单位收入 = P × g
每单位实际采购成本 = C          （C 由上游决定，不随 P 变化）
亏损 ⟺ P × g < C ⟺ measured > g       其中 measured = C / P
保本价 P* = C / g
```

**配置成本风险（configured）** — 平台账本自己认为在亏：

```text
profit = P × tokens × (g − r)
亏损 ⟺ r > g
```

**两者必须分开，因为修复动作完全相反：**

- measured 类：`C` 固定，抬高 `P` 能真实止损。
- configured 类：`P` 是 `profit` 表达式的公因子，**改价不改变毛利率**，只会等比放大绝对亏损额。正确动作是调高分组倍率 `g` 或修正 `r`。

因此**不得把两者合成一个 `max()`**——那会导致对 configured 类给出一个数学上无效的改价按钮。两个系数各自与 `sellFactor` 比较，`loss_kinds` 记录命中了哪几类，UI 按类型分派动作（§4.4）。

`measured` 与 `configured` 差距大说明 `cost_ratio` 配置已过期，UI 作为提示文字展示（「配置成本系数 1.00，实测 1.35，建议核对渠道成本系数」），**不作为第三类判定**，不影响 `flagged`。

### 3.2 sellFactor — 最低可达分组倍率

必须直接复用计费真正使用的 `ResolveGroupRatio`，而不是自行 `min(GetGroupRatio, GetGroupGroupRatio)`，否则优先级会与实际计费不一致。

```go
// usingGroups = channel.GetGroups()（model/channel.go:307）
// userGroups  = GetGroupGroupRatioCopy() 的键集合（新增一行 accessor，见 §11）
sellFactor := 1.0
found := false
for _, using := range usingGroups {
    candidates := []string{""}                 // "" 代表无 userGroup 覆盖，走全局分组倍率
    candidates = append(candidates, userGroups...)
    for _, user := range candidates {
        ratio, _ := ratio_setting.ResolveGroupRatio(nil, user, using)
        if ratio == 0 {                        // 免费分组
            continue
        }
        if !found || ratio < sellFactor {
            sellFactor, found = ratio, true
        }
    }
}
if !found {
    return                                     // 无可用分组，不判定
}
```

- 第一个参数传 `nil`：语义即「不含用户专属倍率的最低售价系数」，与 §1.3 的覆盖声明一致。
- **可达性**：`using` 只取该渠道实际服务的分组。不遍历全部分组组合——那会把用户无法使用的组合算进最小值，制造误报。`GetGroupGroupRatio` 是 `userGroup → usingGroup → ratio` 的嵌套表（`setting/ratio_setting/group_ratio.go:112`），逐对求值天然只覆盖已配置的组合。
- `ratio == 0` 表示免费分组，与 `calcCostQuota` 中 `groupRatio == 0` 视为免费赠送的处理一致：跳过；全部为 0 则该 source 不判定。
- 渠道分组为空 → `sellFactor = 1.0`，退化为现状口径。
- **正式官方来源不参与 `loss_risk`。** 官方价不是我们的采购成本，把它当成本会把「官方涨价」误报成「我们在亏」。官方价继续由 `above_platform` 承担市场信号的角色。

### 3.3 覆盖范围提示

~~快照与 status 响应附带 `exclusive_ratio_enabled`，开启用户专属分组倍率时在 `loss_risk` 视图顶部显示覆盖范围提示条。~~

**已移除（2026-09-16，用户决定）**：管理页与分享页不再显示该提示，`exclusive_ratio_enabled` 字段一并从 status 与查询响应中删除。判定口径不变：售价系数仍只按全局分组倍率计算，不含用户专属倍率。

### 3.4 判定与新增字段

```text
flagged ⟺ len(loss_kinds) > 0
loss_kinds ⊆ {"measured", "configured"}
```

相等使用既有 `nearlyEqual` 容差判为不亏。可比维度的定义与不可比情形（`unavailable_reason` 非空 / 计费方式不同 / 动态表达式 / 档位结构不一致 / 维度单侧缺失）**完全复用** `priceMonitorSourceAbovePlatform` 已有的规则，不另立一套。

`PriceMonitorPriceCell` 新增（仅 `flagged` 时写入）：

```go
LossKinds        []string                 `json:"loss_kinds,omitempty"`
SellFactor       *float64                 `json:"sell_factor,omitempty"`
MeasuredFactor   *float64                 `json:"measured_factor,omitempty"`
ConfiguredFactor *float64                 `json:"configured_factor,omitempty"`
Suggestion       *PriceMonitorPriceAdvice `json:"suggestion,omitempty"`
```

既有 `Highest` 字段与打分规则不变。`priceMonitorMatrixVersion` 13 → **14**，让旧快照触发一次刷新。

### 3.5 与 `above_platform` 的关系

**`above_platform` 保持现状，一个字节都不改。** 两者是不同事件：

| | `above_platform` | `loss_risk` |
|---|---|---|
| 语义 | 来源裸价高于平台裸价 | 售价系数下真实亏损 |
| 来源 | 渠道 + 正式官方 | **仅渠道** |
| 用途 | 市场价格变化 / 官方调价信号 | 止损 |
| 分组倍率 | 不参与 | 参与 |
| 动作 | 人工判断 | 保本价 / 分组倍率修正 |

差集有意义：「比平台贵但仍有利润」说明定价空间还够，不需要动作；「不比平台贵但仍在亏」说明分组倍率过低，改价无用。压成一个筛选会丢掉这个区分，也会让公开查询接口的同一个 `comparison` 取值在不同部署下含义不同。

`PriceMonitorComparisonModelCounts` 新增 `LossRisk int \`json:"loss_risk"\``，与 `AbovePlatform` 并列。顶部统计卡由 6/8 张变为 7/9 张。

### 3.6 边界与异常

| 情况 | 处理 |
|---|---|
| 平台该模型价格缺失 | 模型整行不进矩阵（现状不变） |
| 平台某维度值为 0 | 该维度不可比（除零），跳过；所有维度都不可比则该 source 不判定 |
| 渠道 `missing` / `placeholder` / `source_failed` | 不判定 |
| 计费方式不同 / 动态表达式 / 档位结构不一致 | 不判定 |
| 渠道分组为空 | `sellFactor = 1.0` |
| 分组倍率全为 0 | 跳过该 source |
| `GetChannelCostRatio` 查询失败 | 返回既有默认 1.0；`configured` 类按 `1.0 > sellFactor` 判定。分组倍率 < 1 时会命中，这是正确的保守行为 |
| 正式官方 / models.dev 来源 | 不参与 |
| 一个模型多个渠道命中 | 统计只计一次 |

---

## 4. G2 — 建议价与页面内改价

### 4.1 建议价按维度独立计算

判定用 `measured = max over dims`（任一维度亏就算亏），但**建议价逐维度独立算**，避免用同一个标量放大非瓶颈维度、让客户多付冤枉钱。

```text
保本价      P_i* = C_i / g
目标毛利价  P_i(m) = C_i / (g × (1 − m))      m = target_margin，默认 0.15
```

`m = 0` 时退化为保本价。只对参与判定的可比维度给建议，不可比维度不出现在建议里，前端展示为「保持不变」。

```go
type PriceMonitorPriceAdvice struct {
    Mode      string                   `json:"mode"`
    Margin    float64                  `json:"margin"`
    Breakeven PriceMonitorAdviceValues `json:"breakeven"`
    Target    PriceMonitorAdviceValues `json:"target"`
}

type PriceMonitorAdviceValues struct {
    Input  *float64                `json:"input,omitempty"`
    Output *float64                `json:"output,omitempty"`
    Price  *float64                `json:"price,omitempty"`
    Lanes  []PriceMonitorPriceLane `json:"lanes,omitempty"`
    Tiers  []PriceMonitorPriceTier `json:"tiers,omitempty"`
}
```

展示价与 option 字段值的换算沿用巡检已有规则（`priceMonitorCell` 中 `inputPrice = model_ratio × 2`，输出按 `completion_ratio` 换算）。写回时的反向换算与展示换算**共用同一个函数**，避免两处漂移。

### 4.2 确认框必须自证有效

确认框对每个模型显示四列：**现值 / 建议值 / 改后 measured / 改后判定**。

改后 `measured' = C_i / P_i'`，按定义满足 `measured' ≤ sellFactor`，因此确认框能直接证明「这次改完不再命中」。这是本设计的核心约束——**任何不能自证清除命中的改价动作都不应该出现在按钮上**。直接把平台价填成渠道价就是典型反例：改完 `measured' = 1.0`，在 `g < 1` 时仍然亏损。

### 4.3 批量

- `loss_risk` 视图下每行加复选框（复用巡检页既有 `Checkbox`，不新造）。表头全选只作用于当前页，且只勾选含 `measured` 的可改行。
- 「批量止损」打开同一个确认组件，列出全部选中模型的四列预览，底部汇总「共 N 个模型，M 个字段」。
- 一次提交 = 一个请求 = **一个数据库事务**。部分失败整体回滚，不产生半改状态。
- 单次上限 `apply_max_models`（默认 100）。超出由前端分批串行提交，每批独立事务；失败时明确告知已成功的批次数，不谎报全部成功。

### 4.4 动作按风险类型分派

| `loss_kinds` | UI 动作 |
|---|---|
| 含 `measured` | 「改为保本价」「改为目标毛利价」「手动改价」，均走 `apply_price` |
| 仅 `configured` | **不显示改价按钮**。显示说明「改模型价不会改变毛利率」+ 指向 渠道成本系数、分组倍率 的链接 |
| 两者都有 | 显示改价按钮（解决 measured），同时显示 configured 的说明与链接。确认框明确标注「本次操作不解决配置成本风险」 |
| `tiered_expr` / `dynamic` | 不显示改价按钮，显示指向 倍率设置 的链接。表达式改动需要完整编辑器与校验（`pkg/billingexpr/expr.md`），塞进行内输入框只会做出一个半残编辑器——按 Rule 6，缺失的后端能力必须显式告知，不能假装支持 |

操作成功后：该行本地打「已处理」标记（当轮有效，下次巡检按新快照重算）；`queryClient.invalidateQueries` 使 system-options 缓存失效；toast 提示改了哪几个字段。

---

## 5. G3 — 数据库级并发安全

### 5.1 为什么需要

`UpdateOptionsBulk`（`model/option.go:286`）事务内是 `FirstOrCreate` + `Save`，**没有版本列、没有条件更新、没有行锁**。若在事务之外读取副本、比对 `expected`、再调用它，两个并发请求都能通过比对，后写覆盖先写（TOCTOU）。巡检的 `checked_at` 只能证明快照没变，与价格配置是否被改无关。

### 5.2 方案：持久化版本号 + 事务内行锁重读

新增 Option 键 `PricingConfigVersion`（int64 字符串，初值 0）。**任何**写价格类 option 的路径都必须在同一事务内 `+1`。

新增共享能力 `model/pricing_options.go`：

```go
// PatchPricingOptions 在单个事务内完成 行锁 → 校验版本 → 校验目标字段 → 局部改写 → 递增版本。
// patch 只修改自己声明的模型键，不触碰同一 option JSON 中的其他模型。
func PatchPricingOptions(expectedVersion int64, patches []PricingPatch) (newVersion int64, applied map[string][]string, err error)

type PricingPatch struct {
    OptionKey string   // "ModelRatio" / "CompletionRatio" / ...
    Model     string
    Expected  *float64 // nil 表示「客户端所见为未配置」
    Value     *float64 // nil 表示删除该模型键
}
```

事务内步骤：

1. 按 option key **升序**逐行读取（固定顺序避免死锁），MySQL / PostgreSQL 加 `clause.Locking{Strength: "UPDATE"}`；
2. 读取并校验 `PricingConfigVersion == expectedVersion`，不等 → `ErrPricingVersionConflict`；
3. 解析每个 option 的 JSON，校验目标模型当前值 == `Expected`，不等 → `ErrPricingValueConflict`，**整个请求不落任何一个字段**；
4. 只 patch 目标模型键并重新序列化；与当前值相等的字段计入 `unchanged` 不写；
5. `PricingConfigVersion += 1`；
6. 提交后统一走既有 `updateOptionMap` 刷新内存快照。

### 5.3 跨数据库兼容（Rule 2）

- **MySQL / PostgreSQL**：`SELECT ... FOR UPDATE` 行锁，标准 CAS。
- **SQLite**：不支持 `FOR UPDATE`，按 `common.UsingSQLite` 跳过 locking 子句。SQLite 单写者模型下，若另一事务在本事务读取后提交了写入，本事务升级为写事务时会得到 `SQLITE_BUSY` / snapshot 冲突并**失败回滚**，而不是静默覆盖。语义仍然安全，只是表现为报错重试而非冲突错误码。这一差异按 Rule 15.5 在测试中显式记录。
- 不新增列、不新增表——版本号是 `Option` 表里的一行。

### 5.4 倍率设置页必须接入

倍率设置页仍是整块覆写（迁移到局部更新不在本次范围），但它**必须**在保存时读取并递增 `PricingConfigVersion`。否则版本校验只能防住巡检页自己人，管理员在倍率设置页的一次整块保存仍会静默清掉行内改价的结果。

接入代价是在既有保存路径上加一次版本读写。整块覆写与行内改价并发时，后者收到 `ErrPricingVersionConflict` 并提示刷新——这是正确行为，覆盖被检测到了。

**`lockedPricingTx` 返回值不可复用（验收时发现，线上 apply_price 整条挂掉）**：
`tx.Clauses(...)` 会把链式状态**固化**成一个具体 Statement，此后在同一个实例上再调
`.Where()` 是**累加**而不是新起一条链。原实现把 `locked := lockedPricingTx(tx)` 同时交给
`readPricingVersionForUpdate` 与 `readPricingOptionForUpdate`，第二条查询退化成

```sql
WHERE key = 'PricingConfigVersion' AND key = 'ModelRatio'
```

命中 0 行，`FirstOrCreate` 返回 `record not found`，接口固定报「请稍后重试」——
**行内改价从上线起就没成功过一次**。

关键在于这个坑**只在真库上出现**：`lockedPricingTx` 的 SQLite 分支原样返回 `tx`，
不固化 Statement，每条链都是干净的。而 §11 的那批行为用例全部跑在内存 SQLite 上
（刻意避开改动开发库的真实价格），于是整个 `FOR UPDATE` 路径从未被执行过，
`TestPricingOptionsLockingOnRealDatabase` 也只验证了「单条加锁读能被方言接受」。

修法两层：`lockedPricingTx` 末尾加 `.Session(&gorm.Session{})`（后续每条链在克隆的
Statement 上构造，Locking 子句照常保留），并且 `PatchPricingOptions` 不再复用同一个实例、
每条语句各取一次。新增两个**真库**回归用例：
`TestPatchPricingOptions_OnRealDatabase`（完整 patch 流程）与
`TestPatchPricingOptions_LockedTxIsNotReusable`（直接盯住条件累加）。

**加锁顺序必须与 `PatchPricingOptions` 一致（实现后的审计修订）**：两条路径都要碰
`PricingConfigVersion` 行与价格 option 行，顺序反了就是锁顺序反转。首版实现里
`updatePricingOption` 先 `Save` 价格行（取排他锁）再 `SELECT ... FOR UPDATE` 版本行，
而 `PatchPricingOptions` 是先锁版本行再锁价格行——倍率页整块保存与巡检页行内改价并发时，
MySQL / PostgreSQL 会检测到死锁并回滚其中一个事务。现已统一为**先版本行、后价格行**。
SQLite 不加 `FOR UPDATE`，不受影响。

---

## 6. API 契约

### 6.1 `GET /api/price_monitor/results` · `POST /api/price_monitor/public_query`

鉴权不变。`comparison` 新增合法取值 `loss_risk`，非法值仍回退 `all`。命中单元格新增 §3.4 的字段。分享页可以查看 `loss_risk`，但**不返回 `suggestion`**，也没有任何改价入口——建议价属于内部定价信息。

### 6.2 `GET /api/price_monitor/status`

`data.snapshot.comparison_model_counts` 新增 `loss_risk`；`data` 新增 `exclusive_ratio_enabled`（bool）与 `pricing_version`（int64，供改价请求携带）。

### 6.3 `POST /api/price_monitor/apply_price`

```
权限：middleware.AdminAuth() + middleware.RequirePermission(authz.SystemSettingsEdit("billing.model-pricing"))
```

与 `router/api-router.go:335` 的 `rest_model_ratio` 使用同一权限位——都是「修改模型价格」，不应有两套授权语义。

```jsonc
{
  "checked_at": 1735689600,
  "pricing_version": 42,
  "items": [
    {
      "model": "gpt-4o",
      "fields":   { "model_ratio": 2.5, "completion_ratio": 3.0 },
      "expected": { "model_ratio": 2.0, "completion_ratio": 3.0 }
    }
  ]
}
```

```jsonc
{
  "success": true, "message": "",
  "data": {
    "pricing_version": 43,
    "results": [{ "model": "gpt-4o", "applied": ["model_ratio"], "unchanged": ["completion_ratio"] }]
  }
}
```

支持字段与 `RATIO_SYNC_FIELDS` 对齐并加 `model_price`：`model_ratio` · `completion_ratio` · `cache_ratio` · `create_cache_ratio` · `image_ratio` · `audio_ratio` · `audio_completion_ratio` · `model_price`。

审计日志：`recordManageAudit(c, "price_monitor.price.apply", {models, fields})`，只记键名不记值，与其他管理操作一致。

---

## 7. 数据模型变更

**无新增表、无新增列、无索引变更、无迁移。**

- 新增一个 Option 键 `PricingConfigVersion`。
- 巡检快照仍是只保留最新一份的本地 `price_monitor/snapshot.json`，仅单元格结构增加几个可选字段，不属于持续增长数据。

---

## 8. 配置项（Rule 12）

加在 `setting/price_monitor_setting/config.go` 的 `PriceMonitorSetting`：

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `loss_detection_enabled` | bool | `true` | 是否产出 `loss_risk` 判定与统计。关闭时该筛选项与统计卡隐藏 |
| `target_margin` | float64 | `0.15` | 目标毛利价用，clamp 到 0–0.9 |
| `apply_max_models` | int | `100` | 单次批量改价上限，clamp 1–500 |

`loss_detection_enabled` 是本设计的 kill switch：判定口径此前从未在真实数据上验证过，若上线后发现口径有偏差，需要能立即关掉而不必回滚部署。

`Normalized()` 负责全部 clamp 与非法值归一，沿用现有写法；`validatePriceMonitorSettingsRequest` 同步校验并加入 `SaveConfigGroup` 的值映射。

---

## 9. 错误处理（Rule 9 / Rule 10 / Rule 13）

| 场景 | 处理 |
|---|---|
| `apply_price` 参数非法 / 模型不在快照 | `ApiErrorI18n(c, MsgInvalidParams)` |
| `pricing_version` 或字段 `expected` 不匹配 | `ApiErrorI18n(c, MsgPricingConfigChanged)` — 新 key，文案「价格配置已被其他管理员修改，请刷新后重试」。整个请求不落任何字段 |
| `checked_at` 与当前快照不符 | `ApiErrorI18n(c, MsgPriceMonitorSnapshotStale)` — 新 key，文案「巡检结果已更新，请刷新页面」 |
| 模型为 `tiered_expr` / `dynamic` | `ApiErrorI18n(c, MsgPriceMonitorExprNotEditable)` — 新 key，文案指向倍率设置页 |
| 单次超过 `apply_max_models` | `ApiErrorI18n(c, MsgPriceMonitorTooManyModels)` — 新 key |
| SQLite 写冲突 / 事务失败 | `logger.LogError` 记原始错误 + `ApiErrorI18n(c, MsgRetryLater)` |
| 判定逻辑内部异常 | 只读内存快照，不发起外部调用；单个 source 判定失败跳过该 source 并 `common.SysError`，不阻断整轮巡检 |

按 Rule 6，所有前端错误文案描述下一步动作，不暴露 Go 错误串。

4 个新 i18n key 同步加到 `i18n/keys.go` + `i18n/locales/{en,zh-CN,zh-TW}.yaml`（本仓库后端 locale 为 yaml，与 CLAUDE.md 中写的 json 不符——以代码为准）。前端新增文案直接写入 7 份 locale 保持 diff 收敛，不跑 `i18n:sync`。

---

## 10. Main Chain Impact（Rule 0 / Rule 8）

巡检不在 relay 链路上，但**改价会写 relay 间接读取的配置**，仍需完整审计。

**同步执行在 relay goroutine 上的部分：无。**

| 共享资源 | 本功能的访问 | relay 是否访问 | 结论 |
|---|---|---|---|
| `Option` 表（价格类键 + `PricingConfigVersion`） | `apply_price` 管理员手动触发，事务内加行锁 | relay **不读该表**，读 `ratio_setting` 内存快照 | 与倍率设置页同一条既有写入路径。频率为人工操作级（远低于 1 QPS）。行锁只锁价格类 option 行，持有时间为单次事务，不跨任何上游调用 |
| `ratio_setting` 内存快照 | 巡检只读 `Get*Copy()`；`apply_price` 经既有 setter 发布；多节点靠既有 `SyncOptions`（`model/option.go:244`）定期从 DB 重载收敛 | relay 每请求读 | 读写均走既有 `config.Snapshot`，本身并发安全，无新增锁。多节点下非写入节点在一个 `SyncOptions` 周期内看到旧价——与现有倍率设置页行为完全一致，不是新引入的问题 |
| `ChannelCostConfig` / `GetChannelCostRatio` | 巡检每轮按渠道读一次（默认 6 小时一轮） | relay 结算路径读（`service/employee_commission.go`） | 同一份 L1 内存 + Redis 缓存，纯读且命中同一缓存。渠道数远小于请求数，缓存早被 relay 预热。无冲突 |
| `channels` 表 | 巡检已有的 `GetAllChannels` 读（不变） | relay 读缓存，不直接查表 | 现状不变，未新增查询 |
| `users` 表 | **不访问**（§1.3 明确不扫 `group_ratios`） | relay 读缓存 | 无 |
| Redis / goroutine 池 / 连接池 | 不新增访问、不新起 goroutine，判定在巡检自己的 goroutine 内同步执行 | — | 无冲突 |

**并发分析**：判定逻辑是纯内存计算，随巡检轮次执行（默认 6 小时一轮）。`apply_price` 为人工触发，每次 1 个事务。全部与 100k RPM 的 relay 负载无量级关系。

**功能开关**：`loss_detection_enabled` 默认 true 但可即时关闭；`above_platform` 不受任何开关影响，行为恒定。

---

## 11. 测试计划（Rule 15.2 — 先写用例再实现）

### 11.1 `controller/price_monitor_loss_test.go`（新增）

| 技术 | 用例 |
|---|---|
| 等价类 | 渠道来源命中；正式官方来源**不参与**；models.dev 不参与 |
| 判定拆分 | 仅 measured / 仅 configured / 两者都命中 / 都不命中，`loss_kinds` 内容精确断言 |
| 关键回归 | **仅 configured 的行不产生 `suggestion`**（防止给出数学上无效的改价动作） |
| 边界值 | `measured` 恰等于 `sellFactor`（不亏）；容差内（不亏）；容差外（亏） |
| 边界值 | `sellFactor = 0`（免费分组）跳过；平台维度值为 0 跳过；全部维度不可比则不判定 |
| 条件覆盖 | `GroupGroupRatio` 更低 / 全局 `GroupRatio` 更低 / 两者都配置时取最小；渠道未服务的分组被可达性过滤掉 |
| 路径覆盖 | 三种计费模式各自的 `measured` 计算与逐维度建议价 |
| 建议价 | 保本价满足「改后 `measured' ≤ sellFactor`」；`target_margin = 0` 时等于保本价；逐维度独立而非统一缩放 |
| 隔离 | **`above_platform` 的判定、统计、筛选结果在 `loss_detection_enabled` 开与关两种情况下逐位不变** |
| 统计 | `ComparisonModelCounts.LossRisk` 与筛选结果口径一致，一模型多渠道只计一次 |
| 覆盖提示 | `exclusive_ratio_enabled` 随特性开关正确透出 |

### 11.2 `model/pricing_options_test.go`（新增）

版本递增；行锁路径与 SQLite 路径分别覆盖并显式记录差异；只改目标模型键、不触碰同一 JSON 中其他模型；`Value = nil` 删除键；`Expected = nil` 与「实际未配置」匹配。

### 11.3 `controller/price_monitor_apply_price_test.go`（新增）

- 版本匹配 / 不匹配；字段 `expected` 匹配 / 不匹配（不落任何字段）。
- **并发回归**：两个 goroutine 同时提交同一模型，恰好一个成功、一个收到冲突错误，最终值等于成功那一方。
- 跨页场景：倍率设置页整块保存后，持旧版本号的 `apply_price` 被拒绝。
- `checked_at` 陈旧拒绝；`tiered_expr` 拒绝；相等字段进 `unchanged` 不写；多 option key 的事务性（其一写失败则全部回滚）。
- 批量：超过 `apply_max_models` 拒绝；批量中部分字段冲突时整体回滚。
- 权限缺失返回 403。
- 按 Rule 15.5 走项目真实 DB 环境，SQLite 内存库仅作回退。

### 11.4 既有测试

`price_monitor_above_platform_test.go` **全部保留、不修改**。本设计不改 `above_platform` 口径——若实现过程中该文件出现任何需要改预期值的情况，说明实现越界了。这是本次的护栏。

实现完成后运行 `go test ./...` 与 `web/ bun run typecheck`，全绿方视为完成。

### 11.5 实现记录

- 判定逻辑落在 `controller/price_monitor_loss.go`：`priceMonitorSellFactor` / `priceMonitorMeasuredFactor` / `priceMonitorLossKinds` / `priceMonitorPriceAdvice` / `applyPriceMonitorLossVerdicts`。判定作为矩阵构建后的**独立一趟**执行，`buildPriceMonitorMatrix` 与 above_platform 的既有代码路径一个字节未改。
- `PriceMonitorPriceAdvice` 实现时新增了 `current` 字段（平台当前的 option 字段值）。设计初稿让前端从展示价反推 expected，那会把展示价与 option 值的换算复制到 TS 里；改为后端用同一个函数算出 `current`，换算只存在一处。
- `apply_price` 的 handler 在 `controller/price_monitor_apply_price.go`，字段名到 option 键的映射也在该文件。
- 测试：`controller/price_monitor_loss_test.go`、`controller/price_monitor_apply_price_test.go`、`model/pricing_options_test.go`。
- **测试环境的一个重要约束**：`ModelRatio` 等价格 option 是共享开发库里正在运行实例的**活配置**，用真库跑改价测试等于改掉开发环境的模型价格。因此这三个文件都沿用 `useConfigGroupDB` 的先例，用独立命名的内存 SQLite（`file:...?mode=memory&cache=shared`，每用例一个名字），跨方言 SQL 契约由 `TestPricingOptionsLockingOnRealDatabase` 单独在真库上用本功能新增的 `PricingConfigVersion` 键验证。
- `go test ./...` 中 `TestChannelFieldsAreClassified` 失败，原因是工作区里另一个进行中的功能（`docs/design/channel-daily-quota-limit.md`）给 `model.Channel` 加了 4 个 JSON 字段但尚未在 `controller/channel_authz.go` 中归类。与本设计无关，未做改动。

---

## 12. 改动面清单

**后端**
- `controller/price_monitor.go` — `loss_risk` comparison 分支、单元格新字段、matrix version 14
- `controller/price_monitor_loss.go`（新）— `sellFactor` / `measured` / `configured` 判定与建议价
- `controller/price_monitor_http.go` — `apply_price` handler、status 新增 `exclusive_ratio_enabled` 与 `pricing_version`
- `controller/price_monitor_page.go` — 分享页新增 `loss_risk` 快捷筛选与覆盖范围提示（不返回建议价、无改价入口）
- `model/pricing_options.go`（新）— `PatchPricingOptions` 共享 CAS 能力
- `model/option.go` — `PricingConfigVersion` 键；倍率设置页保存路径接入版本递增
- `router/api-router.go` — `apply_price` 路由
- `setting/ratio_setting/group_ratio.go` — 新增一行 `GetGroupGroupRatioCopy()` accessor
- `setting/price_monitor_setting/config.go` — 3 个新配置
- `i18n/keys.go` + `i18n/locales/{en,zh-CN,zh-TW}.yaml` — 4 个新 key

**前端**
- `web/src/features/system-settings/models/price-monitor-panel.tsx` — `loss_risk` 筛选与统计卡、两类风险的差异化动作、批量选择、覆盖范围提示条
- `web/src/features/system-settings/models/price-monitor-apply-dialog.tsx`（新）— 四列预览确认面板
- `web/src/features/system-settings/types.ts` — 新字段与配置项类型
- `web/src/i18n/locales/*.json` × 7 — 新文案

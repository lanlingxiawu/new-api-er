# 提成阶梯 — 员工等级 + 业绩/提成 月度自动重置 设计方案

## Context（背景与目标）

当前 `model/commission_tier.go` 中的提成等级体系是**纯累计制**：
- `EmployeeTierLevel.TierId` 记录员工当前等级，`EmployeeTierLevel` 懒创建。
- `TryAutoUpgradeTier(userId, currentProfit)` 每次 `user_extensions.profit_total_quota`（**生命周期累计利润**）增加时被调用（见 `model/business_stats_buffer.go` 的 `applyEmployeeExtDelta`），用累计利润 USD 与 `EmployeeCommissionTier.threshold_usd` 比较，**只升不降**。
- 因此等级一旦达到，理论上永久有效（除非管理员手动调级）。
- 管理后台"员工管理 -> 提成阶梯/员工"列表（`web/default/src/features/employees/index.tsx`）目前展示：
  - "Total Profit"/"Total Commission"（历史累计，来自 `model.GetCommissionStatsByEmployeeIds`，即 `EmployeeCommissionDailyStat` 按 `employee_user_id` 全量 SUM）；
  - "Current Performance"（`current_performance_quota`/`usd`，配合"Performance Target"做距下一等级的进度条），目前在 `controller/employee.go` 中**直接等于** `total_profit_quota`（即也是历史累计）。

业务需求：希望提成等级 + 相关业绩/提成展示具备"**月度周期**"语义——每月在指定的"日期+时分秒"自动重置一次，并提供启用开关，配置入口放在"员工管理 -> 提成阶梯"页面（即现有 `TiersTab`，frontend 中文标题"提成阶梯"/"Commission Tiers"）。具体要求：

1. 新增可配置的"启用开关 + 每月重置日 + 时分秒"，置于"提成阶梯"Tab。
2. 新增定时任务，按调度执行重置。
3. **关键耦合点 ①（等级）**：若只重置 `tier_id`，`TryAutoUpgradeTier` 仍用"生命周期累计利润"判断，重置后第一笔提成就会把等级立刻打回原状——必须引入"**周期利润基准**"，让自动升级判断的是"自上次重置以来新增的利润"。
4. **关键耦合点 ②（业绩/提成展示）**：管理员还希望"本期业绩"和"本期提成额"两个**展示指标**也随同一次重置归零重新累计，但**不能删除任何历史数据**——"Total Profit"/"Total Commission"（历史累计列）、`employee_commission_logs`（不可变台账）、`employee_commission_daily_stats`（按日明细）、以及 `commission_pending_quota`/`commission_settled_quota`（待结算/已结算资金状态）**均保持不变、不清零**。

### 已确认的关键策略

1. **重置目标等级**：重置到员工当前所在分组（Group）内 **Level 最小** 的等级（保留分组归属与保底提成率），`tier_id == 0`（未定级）的员工保持不变。
2. **手动/自定义来源（source=manual/custom）的等级**：与 `source=auto` 的员工**统一参与重置**，行为简单可预期。
3. **"业绩/提成跟随重置"的范围**：仅新增两个**周期性展示指标**——"本期业绩"（已存在，语义调整）与"本期提成额"（新增）。两者均定义为 `UserExtension` 中对应累计字段（`profit_total_quota`/`commission_total_quota`）相对于"上次重置时快照值"的增量。**不**触碰 `commission_pending_quota`/`commission_settled_quota`（待结算/已结算资金状态，独立的结算流程，不随等级周期重置），**不**删除/改写任何明细表或日聚合表。
4. **无需新增其他周期指标**（如"本期新增客户数"等暂不需要）。
5. **配置入口**：员工管理 -> "提成阶梯"（`TiersTab`，`web/default/src/features/employees/index.tsx`）。

---

## 1. 配置项设计

新建 `setting/operation_setting/commission_tier_reset_setting.go`，沿用 `checkin_setting.go` 的 `config.GlobalConfig.Register` 模式（`option.go` 的 `updateOptionMap` 已支持 `module.field` 格式的通用 key 读写，前端可直接复用现有的 `useUpdateOption` / `PUT /api/option`）：

```go
type CommissionTierResetSetting struct {
    Enabled     bool  `json:"enabled"`
    ResetDay    int   `json:"reset_day"`    // 1-31；若当月天数不足，自动取该月最后一天
    ResetHour   int   `json:"reset_hour"`   // 0-23
    ResetMinute int   `json:"reset_minute"` // 0-59
    ResetSecond int   `json:"reset_second"` // 0-59
    LastResetAt int64 `json:"last_reset_at"` // 运行态：上次实际执行重置的"调度时刻"
}
```

- 默认值：`Enabled=false, ResetDay=10, ResetHour=0, ResetMinute=0, ResetSecond=0`。
- `LastResetAt` 是**运行态**字段，由后台任务通过 `model.UpdateOption("commission_tier_reset_setting.last_reset_at", ...)` 写入，不通过管理员表单提交（表单只提交 `enabled/reset_day/reset_hour/reset_minute/reset_second`）。
- `NextResetAt`（下次预计重置时间）**不持久化**，由 `enabled/reset_*` + `last_reset_at` 实时计算，仅用于前端展示。
- 时区：与 `model/subscription.go` 的 `calcNextResetTime` 一致，统一使用 `time.Now()`（服务器本地时区），前端文案标注"按服务器时区计算"。

校验范围：`ResetDay∈[1,31]`、`ResetHour∈[0,23]`、`ResetMinute/ResetSecond∈[0,59]`，非法值在 controller 层拒绝。

---

## 2. 数据模型变更（`model/commission_tier.go`）

`EmployeeTierLevel` 新增**两个**基准快照字段（同一次重置中一并写入，均来自 `UserExtension`）：

```go
type EmployeeTierLevel struct {
    Id          int64  `json:"id"`
    UserId      int    `json:"user_id" gorm:"uniqueIndex;not null"`
    TierId      int64  `json:"tier_id" gorm:"not null;default:0"`
    Source      string `json:"source" gorm:"type:varchar(16);default:'auto'"`
    EffectiveAt int64  `json:"effective_at" gorm:"default:0"`
    Remark      string `json:"remark,omitempty" gorm:"type:varchar(256);default:''"`
    UpdatedBy   int    `json:"updated_by,omitempty" gorm:"default:0"`

    // 上次重置时的快照（quota 单位），用于计算"本期"增量。
    // 周期值 = UserExtension.XxxTotalQuota - BaselineXxxQuota（结果 clamp 到 >=0）。
    BaselineProfitQuota     int64 `json:"baseline_profit_quota" gorm:"column:baseline_profit_quota;not null;default:0"`
    BaselineCommissionQuota int64 `json:"baseline_commission_quota" gorm:"column:baseline_commission_quota;not null;default:0"`
}
```

- 走 GORM `AutoMigrate`（`model/main.go` 中已有的迁移列表），新增列默认值 0，三种数据库均兼容（符合 Rule 2，无需 `ALTER COLUMN`）。
- `EmployeeTierLog.Source` 新增取值 `"reset"`（仅文档约定，字段类型 `varchar(16)` 已够长，无需改表）。
- **设计取舍说明**：两个基准字段都来自 `UserExtension`（而非 `EmployeeCommissionDailyStat` 按日 SUM），是为了保证"本期业绩"与 `TryAutoUpgradeTier` 内部用于等级判定的数值**完全一致**（同一次重置、同一行 `UserExtension` 快照），避免"进度条显示的本期业绩"与"实际触发升级的本期利润"出现两套口径。"Total Profit/Total Commission"（历史累计列）继续走原有的 `EmployeeCommissionDailyStat` SUM，两者口径不同但互不影响、互不删除。

---

## 3. 核心算法变更

### 3.1 `TryAutoUpgradeTier` 改造（仅用 `BaselineProfitQuota`）

```go
func TryAutoUpgradeTier(userId int, currentProfit int64) {
    ...
    level, err := GetOrCreateTierLevel(userId)
    ...
    periodProfit := currentProfit - level.BaselineProfitQuota
    if periodProfit < 0 {
        periodProfit = 0
    }
    periodProfitUsd := common.QuotaToUSD(periodProfit)
    // 后续逻辑（找 currentGroup/currentLevel、在分组内找 bestTier、只升不降）不变，
    // 只是把比较对象从 currentProfitUsd 换成 periodProfitUsd
    ...
    _ = SetTierLevel(userId, bestTier.Id, "auto", 0, "", periodProfitUsd)
}
```

- **向后兼容**：`BaselineProfitQuota` 默认 0 ⇒ `periodProfit == currentProfit`，未启用月度重置时行为与现状完全一致。
- `EmployeeTierLog.ProfitSnapshotUsd`（auto 来源）语义从"终身累计利润"变为"本周期累计利润"，不影响历史记录（历史行不回填）。

### 3.2 `GetEffectiveCommissionRate` / `SetTierLevel` 等读路径

不变——只读 `tier_id` 对应的 `rate`，与基准无关。

### 3.3 "本期业绩 / 本期提成"展示指标公式（供 controller 使用）

```go
ext := <UserExtension for userId>          // ProfitTotalQuota, CommissionTotalQuota
level := <EmployeeTierLevel for userId>    // BaselineProfitQuota, BaselineCommissionQuota

periodProfitQuota := max(0, ext.ProfitTotalQuota - level.BaselineProfitQuota)
periodCommissionQuota := max(0, ext.CommissionTotalQuota - level.BaselineCommissionQuota)
```

- `periodProfitQuota` 即新的 `current_performance_quota/usd`（与 `TryAutoUpgradeTier` 内部口径一致，"Performance Target"进度条数值与实际升级判定保持同步）。
- `periodCommissionQuota` 即新增的 `current_commission_quota/usd`（"本期提成额"，纯展示，无内部算法依赖）。
- 重置功能关闭、且从未触发过重置时，两个 baseline 均为 0 ⇒ 上述两个"本期"值 = `UserExtension` 对应累计值（与启用前的展示基本一致，详见边界问题 #16）。

---

## 4. 重置批处理：`ResetEmployeeTierLevelsForPeriod`

新增函数（`model/commission_tier.go`）：

```go
func ResetEmployeeTierLevelsForPeriod(resetAt int64, batchSize int) (processed int, err error)
```

处理逻辑（每批一个事务，分批循环直到返回 0，便于复用于"立即重置"和定时任务）：

1. 查询一批 `EmployeeTierLevel`：`WHERE effective_at < resetAt ORDER BY id LIMIT batchSize`（用 `effective_at < resetAt` 作为"本周期尚未处理"的幂等守卫——崩溃重启后重新调用会自动跳过已处理的行，不会重复重置）。
2. 收集这批的 `user_id`，批量查询 `user_extensions`（新增 `GetProfitAndCommissionTotalsByUserIds(userIds []int) (map[int]struct{ProfitTotalQuota, CommissionTotalQuota int64}, error)`，单条 `WHERE user_id IN (...)` 查询，避免 N+1，仿照 `GetTierLevelsByUserIds` 的写法）。
3. 对每一行：
   - 若 `TierId == 0`：保持 `tier_id` 不变（无分组上下文，无需调级），但仍刷新两个 baseline。
   - 若 `TierId != 0`：在 `GetAllTiersCached()` 中找到该 tier 所在 `Group`，再找该 `Group` 内 `Level` 最小的 tier 作为目标 `targetTierId`（若当前已是最小 Level，目标等于自身）。
   - 更新该行：`TierId=targetTierId(或不变)`, `BaselineProfitQuota=ext.ProfitTotalQuota`, `BaselineCommissionQuota=ext.CommissionTotalQuota`, `Source="reset"`, `EffectiveAt=resetAt`, `UpdatedBy=0`, `Remark="月度自动重置"`。
   - 写入一条 `EmployeeTierLog{FromTierId, ToTierId: targetTierId, Source:"reset", ProfitSnapshotUsd: QuotaToUSD(ext.ProfitTotalQuota), OperatedAt: resetAt}` 用于审计（无论等级是否变化都记录，因为"周期基准归零"本身就是一次有意义的事件）。
4. 返回本批处理行数；调用方循环直到返回 0。

**明确不涉及**（保证"不删除历史数据"）：
- `employee_commission_logs`（不可变台账）——不读不写。
- `employee_commission_daily_stats` / `platform_channel_daily_stats`（按日聚合）——不读不写，"Total Profit/Total Commission"列继续基于其全量 SUM。
- `user_extensions.profit_total_quota` / `commission_total_quota`（生命周期累计值本身）——只读不写，继续累加，永不清零。
- `user_extensions.commission_pending_quota` / `commission_settled_quota`（资金结算状态）——完全不涉及。

---

## 5. 定时任务：`service/commission_tier_reset_task.go`

完全仿照 `service/subscription_reset_task.go` 的结构（`gopool.Go` + `time.Ticker` + `common.IsMasterNode` + `atomic.Bool` 防重入）：

```go
const (
    tierResetTickInterval = 1 * time.Minute
    tierResetBatchSize    = 300
)

func StartCommissionTierResetTask() {
    once.Do(func() {
        if !common.IsMasterNode { return }
        gopool.Go(func() {
            ticker := time.NewTicker(tierResetTickInterval)
            defer ticker.Stop()
            for { runCommissionTierResetCheck(); <-ticker.C }
        })
    })
}
```

### 5.1 调度算法（无状态计算，避免 NextResetAt 与配置漂移）

辅助函数 `lastScheduledTimeAtOrBefore(now time.Time, cfg) int64`：
- `day := min(cfg.ResetDay, daysInMonth(now.Year(), now.Month()))`
- `candidate := time.Date(now.Year(), now.Month(), day, cfg.ResetHour, cfg.ResetMinute, cfg.ResetSecond, 0, now.Location())`
- 若 `candidate > now`：取上一个月，同样按 `daysInMonth` 夹紧后重算 `candidate`。
- 返回 `candidate.Unix()`。

`runCommissionTierResetCheck()`：
```go
cfg := operation_setting.GetCommissionTierResetSetting()
if !cfg.Enabled { return }
now := time.Now()
due := lastScheduledTimeAtOrBefore(now, cfg)

if cfg.LastResetAt == 0 {
    // 首次启用：armed，不立即触发本月/上月已过的那次
    persistLastResetAt(due)
    return
}
if due <= cfg.LastResetAt { return } // 还没到下一次

if !runningFlag.CompareAndSwap(false, true) { return }
defer runningFlag.Store(false)

model.FlushBusinessStatBuffers() // 先把缓冲区里的利润/提成增量落盘，缩小边界误差
for {
    n, err := model.ResetEmployeeTierLevelsForPeriod(due, tierResetBatchSize)
    if err != nil { logger.LogWarn(...); return }
    if n == 0 { break }
}
persistLastResetAt(due)
```

要点说明：
- **首次启用不立刻触发**：把 `LastResetAt` 直接设为"最近一次已经过去的调度时刻"而不执行重置，下一次真正触发是**下个月**对应的时刻。避免管理员刚打开开关就意外清零所有人的"本期业绩/本期提成"。
- **重新启用（曾禁用过）**：`LastResetAt` 仍是旧值，`due > LastResetAt` 成立 ⇒ 触发**一次**重置（视为"恢复调度，从现在开始计"），不会为期间错过的多个月份重复触发。
- **修改调度参数**（日期/时分秒）立即在下一个 tick 生效，无需额外的"配置变更回调"——`due` 每次都按当前配置实时计算。
- `persistLastResetAt` 同时更新内存中的 `commissionTierResetSetting.LastResetAt` 和 `model.UpdateOption("commission_tier_reset_setting.last_reset_at", ...)`。

### 5.2 启动注册

在 `main.go` 中与 `StartSubscriptionQuotaResetTask()` 相邻处调用 `service.StartCommissionTierResetTask()`。

---

## 6. 管理后台 API 变更

### 6.1 新增：重置配置/手动触发（路由挂载到 `employeeAdminRoute`，紧邻现有 `/tiers*`）

- `GET /api/admin/employee/tiers/reset-config`
  返回 `{enabled, reset_day, reset_hour, reset_minute, reset_second, last_reset_at, next_reset_at}`，`next_reset_at` 实时计算（`lastScheduledTimeAtOrBefore` 的"下个月"分支），仅供前端展示。
- `POST /api/admin/employee/tiers/reset-now`
  立即执行一次重置：`resetAt := time.Now().Unix()`；`model.FlushBusinessStatBuffers()` + 循环 `ResetEmployeeTierLevelsForPeriod(resetAt, batchSize)`；成功后 `persistLastResetAt(resetAt)`。需前端二次确认弹窗，并将 `operated_by` 写入本次所有 `EmployeeTierLog`（区别于定时任务的 `OperatedBy=0`）。

`enabled/reset_day/reset_hour/reset_minute/reset_second` 复用现有通用 `PUT /api/option`（`controller/option.go` 的 `UpdateOption`），与 `checkin_setting.enabled/min_quota/max_quota` 走同一条路径，无需新增 controller 方法。

新增 controller 函数（`controller/employee.go`）：`AdminGetTierResetConfig` / `AdminTriggerTierReset`。

### 6.2 修改：`AdminListEmployees`（`controller/employee.go` ~L80-243）

新增一路并行查询（加入现有 `errgroup`）：

```go
var extByUserId map[int]*model.UserExtension // 新增：批量取 ProfitTotalQuota/CommissionTotalQuota
eg.Go(func() error {
    var err error
    extByUserId, err = model.GetUserExtensionsByUserIds(employeeUserIds) // 新增辅助函数，仿 GetTierLevelsByUserIds
    return err
})
```

在组装 `item` 时：

```go
var periodProfit, periodCommission int64
if ext, ok := extByUserId[emp.UserId]; ok {
    if lvl, ok2 := tierLevelsByUserId[emp.UserId]; ok2 {
        periodProfit = ext.ProfitTotalQuota - lvl.BaselineProfitQuota
        periodCommission = ext.CommissionTotalQuota - lvl.BaselineCommissionQuota
    } else {
        periodProfit = ext.ProfitTotalQuota
        periodCommission = ext.CommissionTotalQuota
    }
    if periodProfit < 0 { periodProfit = 0 }
    if periodCommission < 0 { periodCommission = 0 }
}

item.CurrentProfitQuota = periodProfit          // 语义变化：原为 totalProfitQuota，现为"本期"
item.CurrentProfitUsd = common.QuotaToUSD(periodProfit)
item.CurrentCommissionQuota = periodCommission  // 新增字段
item.CurrentCommissionUsd = common.QuotaToUSD(periodCommission)
```

`EmployeeWithUser` struct 新增两个字段：
```go
CurrentCommissionQuota int64   `json:"current_commission_quota"`
CurrentCommissionUsd   float64 `json:"current_commission_usd"`
```

`TotalProfitQuota`/`TotalProfitUsd`/`TotalCommissionQuota`/`TotalCommissionUsd`（来自 `GetCommissionStatsByEmployeeIds`）**保持不变**，继续表示历史累计。

`findNextTier(...)` 的 `currentProfitUsd` 入参改用 `item.CurrentProfitUsd`（本期值，已是新口径）——逻辑本身不变，只是输入数值口径同步更新，与 `TryAutoUpgradeTier` 一致。

---

## 7. 前端

### 7.1 `TiersTab`（`web/default/src/features/employees/index.tsx`，配置入口已确认放在此 Tab）

新增设置卡片（仿 `web/default/src/features/system-settings/general/checkin-settings-section.tsx` 的表单结构：`Switch` + 字段 + `useUpdateOption`）：

- 标题："等级与业绩月度重置设置"
- 开关：`commission_tier_reset_setting.enabled`
- 展开后：
  - 每月重置日（1-31，下拉或数字输入，文案注明"若当月无该日期，自动取该月最后一天"）
  - 时/分/秒（数字输入或时间选择器）
  - 只读展示："下次重置时间 / 上次重置时间"（来自新增的 `GET /tiers/reset-config`，react-query）
  - "立即重置"按钮 + 二次确认弹窗（说明：将把所有员工等级重置为各自分组最低等级，并将"本期业绩/本期提成"归零重新累计；不影响历史累计数据、台账明细及待结算/已结算提成余额；会写入审计日志，不可撤销）

等级变更日志（`source=reset` 的 `EmployeeTierLog`，已有的"等级变更日志"Tab 通过 `AdminListTierLogs` 展示）会自然出现，新增一个 `reset` 来源的徽章样式（如"系统重置"）。

### 7.2 `EmployeesTab` 列表（同文件，员工列表）

- 新增列 **"本期提成"（Current Commission）**：`current_commission_quota`，紧邻现有 "Total Commission" 或 "Current Performance" 列，使用 `formatBusinessAmount` 展示，与 "Total Commission"（历史累计）并列对比。
- "Current Performance"（本期业绩）列**语义变化说明**：开启月度重置前，数值与"Total Profit"基本一致（baseline=0）；首次重置后将变为"自上次重置以来的利润"。建议在列头 tooltip / `FormDescription` 中补充说明，避免管理员误解为数据丢失。

### 7.3 i18n

在 `web/default/src/i18n/locales/zh.json` 与 `en.json` 新增上述文案 key（"等级与业绩月度重置设置"、"本期提成"、"立即重置"及确认弹窗文案、"系统重置"徽章等），再跑 `bun run i18n:sync` 同步 fr/ru/ja/vi 占位。

---

## 8. 边界问题清单

| # | 边界情况 | 处理方式 |
|---|---|---|
| 1 | 重置后立刻被 `TryAutoUpgradeTier` 用终身累计利润打回原等级 | 引入 `BaselineProfitQuota`，自动升级判定改为"周期利润 = 累计利润 - 基准" |
| 2 | `tier_id=0`（未定级）员工重置后丢失分组信息 | 重置目标=本组最低 Level；`tier_id=0` 不动，无分组上下文，跳过调级但仍刷新两个 baseline |
| 3 | 重置日期（如 31）在当月不存在（2 月、30 天月） | `daysInMonth` 夹紧到当月最后一天 |
| 4 | 首次开启开关时不应"补跑"已经过去的本月调度点 | `LastResetAt==0` 时只 arm（记录 due），不执行重置 |
| 5 | 关闭一段时间后重新开启 | 视为"从现在恢复"，最多触发一次，不为错过的月份重复执行 |
| 6 | 管理员中途修改"重置日/时/分/秒" | `due` 每个 tick 按当前配置实时计算，自动生效，无需额外回调 |
| 7 | 重置边界处的"漏算/多算"利润/提成（buffer flush 周期 5s） | 重置前先同步调用 `model.FlushBusinessStatBuffers()`，确保 `UserExtension` 是最新值再快照 |
| 8 | 多实例部署并发重复执行 | 复用 `common.IsMasterNode` 守卫，仅主节点跑定时任务 |
| 9 | 批处理过程中崩溃/重启 | `effective_at < resetAt` 作为幂等守卫，已处理行不会重复处理；分批+独立事务 |
| 10 | 手动/自定义来源（manual/custom）等级 | 已确认：与 auto 一并统一重置 |
| 11 | "立即重置"与定时调度撞期 | 手动重置同样写 `LastResetAt`，避免同周期内被定时任务再次触发 |
| 12 | 历史 `EmployeeCommissionLog.CommissionRate`（结算时快照） | 不受影响——结算时已固化费率，重置只影响"未来"判定 |
| 13 | 新建/重新启用员工的 `EmployeeTierLevel` 行（懒创建，baseline=0） | 其"本期业绩/提成"=全部累计值；对全新员工等价于 0（历史本就为 0），符合预期 |
| 14 | 删除某个 Level（`DeleteTier`）与"本组最低 Level"目标 | 现有 `DeleteTier` 已校验 `EmployeeTierLevel` 引用计数，不会出现目标 tier 被删的悬挂引用 |
| 15 | 时区 | 统一 `time.Now()`（服务器本地时区），与 `subscription.go` 的 `calcNextResetTime` 一致；前端注明"按服务器时区" |
| 16 | "本期业绩/本期提成"（基于 `UserExtension`）与"Total Profit/Total Commission"（基于 `EmployeeCommissionDailyStat` SUM）口径不同 | 两者数据源独立维护，正常流程下应保持同步；重置功能关闭/未触发时两个"本期"值近似等于对应"Total"值。实施时建议抽查 `AdminBackfillBusinessStats` 是否会导致两者历史性偏差（属于既有数据一致性问题，非本功能引入，若发现偏差需另行评估） |
| 17 | "本期提成"与 `commission_pending_quota`/`commission_settled_quota` 的关系 | 二者完全独立："本期提成"是只读展示的周期增量统计；待结算/已结算余额是资金状态机，不受月度重置影响，员工的提成发放流程不变 |

---

## 9. 实施步骤建议（供后续编码阶段参考，本次不写代码）

1. **数据模型**：`EmployeeTierLevel` 新增 `BaselineProfitQuota`/`BaselineCommissionQuota` + AutoMigrate；新增 `GetProfitAndCommissionTotalsByUserIds` / `GetUserExtensionsByUserIds` 批量查询辅助函数（`model/commission_tier.go` 或 `model/user_extension.go`）。
2. **核心算法**：改造 `TryAutoUpgradeTier`（周期利润，仅 `BaselineProfitQuota`）；新增 `ResetEmployeeTierLevelsForPeriod`（同时刷新两个 baseline）。
3. **配置**：新增 `commission_tier_reset_setting.go`（含校验范围、`GetCommissionTierResetSetting`/`IsCommissionTierResetEnabled` 等只读访问器）。
4. **定时任务**：`service/commission_tier_reset_task.go`（调度算法 + flush + 批处理循环），在 `main.go` 注册启动。
5. **管理 API**：
   - `AdminGetTierResetConfig` / `AdminTriggerTierReset`，路由挂载到 `employeeAdminRoute`。
   - `AdminListEmployees` 改造：新增 `current_commission_quota/usd`，`current_performance_*` 改为基于 baseline 的周期值，`EmployeeWithUser` 结构体相应扩字段。
6. **前端**：
   - `TiersTab` 新增"等级与业绩月度重置设置"卡片 + "立即重置"按钮 + 等级日志 `reset` 来源徽章映射。
   - `EmployeesTab` 列表新增"本期提成"列，"Current Performance"列补充说明文案。
   - i18n 同步（zh/en 先行，`bun run i18n:sync` 同步其余语言占位）。
7. **测试**：
   - 单元测试 `lastScheduledTimeAtOrBefore`（覆盖月末夹紧、跨年、首次 arm、重新启用）。
   - 单元测试 `TryAutoUpgradeTier`（baseline=0 向后兼容；baseline>0 后周期利润计算正确，只升不降仍成立）。
   - 单元测试 `ResetEmployeeTierLevelsForPeriod`（重复调用同一 `resetAt` 幂等；`tier_id=0` 跳过调级但刷新两个 baseline；本组最低 Level 选取正确；`EmployeeTierLog` 记录正确）。
   - 单元测试 `AdminListEmployees` 的本期业绩/提成计算（baseline=0 时近似等于累计值；baseline>0 时为差值且 clamp 到 0）。
   - 集成验证：手动触发"立即重置" → 检查 `EmployeeTierLevel`/`EmployeeTierLog` 数据、员工列表"本期业绩/本期提成"归零、"Total Profit/Total Commission"/`commission_pending_quota`/`commission_settled_quota`/明细台账均不变；再造一笔消费验证不会被立刻打回原等级。

# 员工业绩手工调整设计文档

> 版本：v1.1
> 日期：2026-07-08
> 状态：已实施
> 关联文档：[员工利润提成系统设计文档](employee-commission-design.md)

---

## 1. 背景与目标

阶梯提成系统（见 [employee-commission-design.md](employee-commission-design.md)）中，员工业绩来自客户消费的自动结算。但存在需要**人工干预业绩**的场景：

- 线下成交、补偿、纠错等无法经由 API 消费自动产生的业绩；
- 历史周期漏记，需要**补录**到过去某个自然月；
- 误操作或退款，需要**冲销**已录入的调整。

目标：提供一个管理员侧的「业绩加/减」入口，让上述调整能进入既有的业绩/提成/等级/统计体系，**且不新增任何数据表、不改动既有列表/日历/导出/结算逻辑**。

### 非目标 / 边界

- 不改动 AI 中继链路（本功能是纯管理端操作，见 §8）。
- 不提供独立的「调整记录列表」接口：调整记录统一在既有的**业务概览台账明细**中查看，撤销通过**手动追加相反数**完成（详见 §5.3）。

---

## 2. 核心思路：把手工调整建模为一笔同构结算

不新增表，而是把一次手工调整建模成**一笔与真实结算同构的加法流水**：

- 往 `employee_commission_logs` 插入一条 **sentinel 流水行**（`model_name` 为固定标记，`channel_id / customer_user_id = 0`，`log_id = NULL`）；
- 同步上盘到两张日聚合表：`employee_commission_reset_period_daily_stats`（本期聚合）与业务日聚合表（累计聚合）；

因为这条流水与自动结算的流水结构完全一致，既有的**列表 / 日历 / 导出 / 等级重估**逻辑无需任何改动即可正确显示与计算。

### 关键取舍

| 决策 | 内容 | 理由 |
|---|---|---|
| 决策 A | **仅当前周期**调整触发等级**双向**重估；历史周期封存不动。 | 历史等级由 `employee_tier_logs` 还原，补录不应改写历史。 |
| 决策 B | 业绩=`profit`；同时令 `revenue = profit`、`cost = 0`。 | 保持 `profit = revenue − cost` 不变式，令既有聚合/展示零改动。 |

---

## 3. 数据流

```
管理员点击「调整业绩」
  → POST /api/admin/employee/:id/performance  { profit_usd, reason, period_start_at }
  → controller.AdminAddEmployeePerformance
        · USD → quota：QuotaFromDecimalChecked（int32 饱和 + NaN 处理，越界显式报错）
  → model.AddEmployeePerformance
        · 解析归属桶 (reset_started_at, stat_date)
        · 解析费率（当前周期=现有效费率；历史周期=还原的历史等级费率）
        · 事务：插 sentinel 流水 + 双日聚合表 upsert
        · RecordLogWithAdminInfo 记录操作审计
        · 当前周期 → reevaluateTierByPeriodProfit（双向重估）
  → 返回 PerformanceAdjustmentResult（回执）
```

---

## 4. API 契约

所有接口挂在 `employeeAdminRoute` 组下，鉴权 `middleware.AdminAuth()`（Rule 11）。

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| POST | `/api/admin/employee/:id/performance` | `AdminAddEmployeePerformance` | 追加业绩（`profit_usd` 可正可负、非 0） |
| POST | `/api/admin/employee/performance/:logId/revert` | `AdminRevertPerformanceAdjustment` | 撤销某笔调整（后端保留，当前 UI 未接入，见 §5.3） |

**请求体（Add）：**

```jsonc
{
  "profit_usd": 12.5,        // 业绩金额（USD，可负，非 0）
  "reason": "线下成交补录",   // 可选
  "period_start_at": 0        // 0=当前周期；>0=补录到该历史周期（取其自然月起点）
}
```

**响应：** 统一 `{"success":bool,"message":string,"data":PerformanceAdjustmentResult}`，HTTP 200（Rule 9）。`data` 含 `log_id / reset_started_at / stat_date / profit_quota / commission_quota / commission_rate / is_historical`。

---

## 5. 关键业务逻辑

### 5.1 归属桶解析（`resolveManualPerfBucket`）

一笔调整落到哪个 `(reset_started_at, stat_date)`，逻辑与结算路径完全一致：

- `createdAt` 落在**当前本期** → `reset_started_at = effectiveResetStartedAt(level, createdAt)`（含 baseline 锚定）；
- 落在**历史周期** → `reset_started_at = 该历史周期自然起点`；
- `stat_date = localDayStart(createdAt)`。

add 与 revert 复用同一函数，保证补偿行与原行落在**同桶同天**，聚合可精确净为 0。

历史周期的 `createdAt` 取该周期结束时刻 `PeriodEndAt`（不超过当前时间、不早于 `PeriodStartAt`），使 `stat_date` 归入该周期最后一天。

### 5.2 费率解析

- **当前周期**：`GetEffectiveCommissionRate(employeeUserId)`（现有效费率）；
- **历史周期**：用 `resolveEmployeeTierAt(userId, period.PeriodEndAt)` 从 `employee_tier_logs` 还原该期结束时刻的等级，取其费率。

提成额度用 decimal 精度计算，正/负利润各有 ±1 保底（`calcManualPerfCommissionQuota`），与 `service.calcCommissionQuota` 语义一致；因 model 不能反向依赖 service，此处内联同逻辑。

### 5.3 撤销（`RevertEmployeePerformance`）

撤销一笔调整 = 标记原行 `settle_status = 2`（已撤销）+ 插一条**相反数补偿行**（`model_name = 管理员减少业绩`）+ 对两张日聚合表上盘负增量（含 `record_count = -1`），使聚合精确净为 0。并发保护：仅当原行 `settle_status <> 2` 时才翻转，避免重复撤销。

> **撤销入口**：UI 不接入 revert 接口；管理员通过**再追加一笔相反数的业绩调整**来冲销（例如误加 +$10 → 追加 −$10）。`RevertEmployeePerformance` / revert 路由作为后端能力保留，供程序化调用。

### 5.4 等级双向重估（`reevaluateTierByPeriodProfit`）

**仅手工调整路径**使用双向重估（组织化自动结算仍用「只升不降」的 `TryAutoUpgradeTier`）：

- 在员工当前分组内，取阈值 ≤ 本期业绩(USD) 的**最高**等级为目标；均不达标则回落到该分组**最低**等级（不解绑分组）；
- 目标与现等级不同则以 `source=auto` 变更并留痕（tier log）；
- 未绑定等级的员工只做升级（沿用 `TryAutoUpgradeTier`），不主动绑定；绑定的等级已被删除时跳过，避免静默切组。

### 5.5「应达等级」只读提示

单员工历史周期查询会填充 `EligibleTierLevel / EligibleTierRate / TierUnderpromoted`（见 `model/employee.go` 的周期统计结构）：当该期业绩已达更高等级阈值、但历史等级未提升时 `TierUnderpromoted=true`，前端在历史等级徽标旁提示「业绩已达 Lx，历史等级未提升」。**纯只读，不改任何等级。**

---

## 6. 数据模型变更

**无新增表、无新增列。** 复用：

| 表 | 用途 | 手工调整写入 |
|---|---|---|
| `employee_commission_logs` | 提成流水 | sentinel 行（add）/ 补偿行（revert） |
| `employee_commission_reset_period_daily_stats` | 本期聚合 | `upsertCommissionResetPeriodDailyStatTx` |
| 业务日聚合表 | 累计聚合 | `upsertCommissionDailyStatTx` |
| `employee_tier_levels` / `employee_tier_logs` | 等级 & 变更留痕 | 当前周期重估时 `SetTierLevel` |

**Sentinel 常量：**

- `ManualPerformanceModelName = "管理员添加业绩"` —— 原始调整行；
- `ManualPerformanceRevertModelName = "管理员减少业绩"` —— 撤销补偿行（与原调整区分，便于在佣金流水/台账视图中识别内部反向补偿行）；
- `settle_status = 2` —— 复用「已撤销」语义。

---

## 7. 错误处理

- 金额为 0 / 过小（USD → quota 后为 0）→ `profit amount is too small`；
- USD 越界（`QuotaFromDecimalChecked` 触发 clamp）→ 显式报错 `profit amount out of range`，**不静默钳制**（管理员单次操作，宁可报错也不悄悄截断一笔巨额调整；与全局 billing 饱和策略一致）；
- 员工不存在 → `employee profile not found`；
- revert 目标非手工调整行 / 已撤销 → 对应 message。

均走 `{"success":false,"message":...}` + HTTP 200（Rule 9）。

---

## 8. 主链路影响与共享资源（Rule 0 / Rule 8）

**本功能不在 AI 中继链路上**：由管理员 HTTP 请求触发，同步执行于管理端 goroutine，与 relay 热路径无交集，不引入任何 relay 侧同步调用或延迟。

**共享资源审计：**

| 资源 | relay 是否也访问 | 说明 |
|---|---|---|
| `employee_commission_logs` | 是（异步结算写入） | 手工调整为管理员低频单次写入，单事务提交；无热路径锁竞争。 |
| `employee_commission_reset_period_daily_stats` / 业务日聚合表 | 是（异步 buffer 刷盘） | 复用相同 upsert 帮助函数；写入走 GORM 事务，非 relay goroutine，低频。 |
| `employee_tier_levels` / `employee_tier_logs` | 否（结算只调 `TryAutoUpgradeTier`，同表） | 重估仅在当前周期手工调整时触发，低频。 |

结论：无 relay 热路径新增同步 DB 写、无新缓存/Redis 键、无新锁；共享表访问为低频管理操作，不构成对 relay 的争用。

---

## 9. 前端（两套 UI，Rule 6）

- **Default**（`web/default`）：`features/employees/components/performance-adjust-dialog.tsx` + `api.ts` 的 `addEmployeePerformance`；员工列表行「调整业绩」入口。
- **Classic**（`web/classic`）：`pages/Business/index.jsx` 的 `PerformanceModal`，调用 `POST /api/admin/employee/:id/performance`。

交互：输入业绩金额（正/负）、原因、可选「补录到历史周期」（仅过去自然月，当前月不算历史，避免勾选补录却落到当前周期触发等级重估）。预估提成仅当前周期前端试算，历史周期费率由后端还原、前端不试算。i18n key 同步到两套 UI 全部语言。

> **约束（i18n）：** classic 的 locale JSON key 必须置于 `translation` 命名空间内。置于顶层会被 i18next 静默忽略、回退显示原始 key（页面看似正常、文案却是英文键名，难以察觉）。default 为扁平结构（key=英文源串），置于顶层即可。

---

## 10. 与既有子系统的交互

- **提成结算**：sentinel 行结构与自动结算一致，`RecordCostAndSettleEmployeeCommission` 等无需感知本功能。
- **台账/定向冲销**：手工调整的补偿行以独立 `model_name` 区分，与逐笔消费冲销互不干扰。
- **等级月度重置**（`ResetEmployeeTierLevelsForPeriod`）：本期业绩由 `reset_period_daily_stats` 动态聚合，手工调整自然纳入当期业绩，重置逻辑无需感知。

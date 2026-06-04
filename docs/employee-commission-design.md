# 员工利润提成系统设计文档

> 版本：v1.0  
> 日期：2026-06-01  
> 状态：实施中

---

## 1. 背景与目标

在现有 API 网关基础上，增加一套**员工利润提成**机制：

- 管理员可将普通用户升级为「员工」，配置提成比例和业绩目标
- 员工通过邀请码带来客户，客户每次 API 消费后自动结算提成
- 提成 = (用户消费额度 − 成本额度) × 提成比例
- 员工身份不影响正常登录，控制台增加「我的提成」入口
- 员工邀请的用户充值**不触发**邀请返佣（`AffQuota`）

---

## 2. 核心公式

```
提成额度 = max(0, profit) × commission_rate

profit      = revenue_quota − cost_quota
cost_quota  = base_cost_quota × channel_cost_ratio

# 按 Token 计费
base_cost_quota = revenue_quota / group_ratio

# 按次/按价格计费（MJ / Task / Image）
base_cost_quota = revenue_quota / group_ratio

# tiered_expr 计费
base_cost_quota = BillingSnapshot.EstimatedQuotaBeforeGroup
                  × (actual_revenue / estimated_revenue_after_group)
```

> `group_ratio` 是对用户的加价倍率，剔除后得到原始成本基准。  
> `channel_cost_ratio` 是渠道实际采购折扣系数（默认 1.0，< 1 表示渠道有折扣）。

---

## 3. 数据库设计

### 3.1 新建表（共 4 张）

#### `user_extensions` — 用户扩展统计表

所有用户均可拥有，记录提成汇总及业绩数据。

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | |
| user_id | INTEGER UNIQUE NOT NULL | 关联 users.id |
| commission_total_quota | BIGINT default 0 | 累计已产生提成（quota 单位） |
| commission_pending_quota | BIGINT default 0 | 待结算提成 |
| commission_settled_quota | BIGINT default 0 | 已结算提成（已转入余额） |
| revenue_customer_count | INTEGER default 0 | 有效消费客户数 |
| revenue_total_quota | BIGINT default 0 | 邀请客户累计消费 |
| extra | TEXT | JSON 预留扩展字段 |
| created_at | BIGINT | autoCreateTime |
| updated_at | BIGINT | autoUpdateTime |

#### `employee_profiles` — 员工档案表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | |
| user_id | INTEGER UNIQUE NOT NULL | 关联 users.id |
| commission_rate | REAL NOT NULL default 0.1 | 提成比例 0.0～1.0 |
| target_quota | BIGINT default 0 | 业绩目标（quota 单位，0=不设限） |
| commission_rules | TEXT | JSON 预留多档提成规则扩展 |
| status | INTEGER default 1 | 1=启用 2=禁用 |
| remark | VARCHAR(255) | 备注 |
| created_at | BIGINT | autoCreateTime |
| updated_at | BIGINT | autoUpdateTime |

#### `channel_cost_configs` — 渠道成本配置表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | |
| channel_id | INTEGER UNIQUE NOT NULL | 关联 channels.id |
| cost_ratio | REAL NOT NULL default 1.0 | 渠道成本系数（< 1 表示折扣） |
| remark | VARCHAR(255) | 备注 |
| created_at | BIGINT | autoCreateTime |
| updated_at | BIGINT | autoUpdateTime |

#### `employee_commission_logs` — 提成日志表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | |
| employee_id | INTEGER index | employee_profiles.id |
| employee_user_id | INTEGER index | 员工 user_id（冗余） |
| customer_user_id | INTEGER index | 消费用户 user_id |
| log_id | INTEGER index | logs.id（消费日志） |
| model_name | VARCHAR(255) | 模型名称 |
| channel_id | INTEGER | 渠道 ID |
| revenue_quota | BIGINT | 用户消费额度（可为负，退款） |
| cost_quota | BIGINT | 成本额度 |
| profit_quota | BIGINT | 利润额度 |
| commission_quota | BIGINT | 提成额度（可为负，冲销） |
| commission_rate | REAL | 快照：提成比例 |
| cost_ratio | REAL | 快照：渠道成本系数 |
| group_ratio | REAL | 快照：分组倍率 |
| settle_status | INTEGER default 0 | 0=待结算 1=已结算 2=已撤销（v2 实现） |
| settled_at | BIGINT default 0 | 结算时间 |
| created_at | BIGINT index | autoCreateTime |

### 3.2 变更现有表

**`model/log.go`**：`RecordConsumeLog` 返回值从 `void` 改为 `int`，返回插入的 `log.Id`。

---

## 4. 精度方案

全链路使用 `shopspring/decimal`，仅在写入数据库时转为 `int64`。

```
cost_quota  = decimal(revenue_quota) / decimal(group_ratio) * decimal(cost_ratio) → Round(0) → int64
profit      = revenue_quota - cost_quota
commission  = decimal(profit) * decimal(commission_rate) → Round(0) → int64
```

最小保底：`profit > 0 && commission_rate > 0` 时 commission 最小为 1。

tiered_expr 计费的成本反推：
```
base_cost = actual_revenue / group_ratio
```
使用 Snapshot 中的 `EstimatedQuotaBeforeGroup` 仅作为比例验证，实际以 `actual_revenue / group_ratio` 计算，避免估算误差传播。

---

## 5. 结算链路

```
用户 API 请求
    ↓
PreConsumeBilling（预扣费）
    ↓
SettleBilling（结算实际扣费）
    ↓
RecordConsumeLog（写 logs 表，返回 log.Id）  ← 改造点
    ↓ gopool.Go（异步）
TrySettleEmployeeCommission(relayInfo, quota, logId)
    ├─ 查 users.inviter_id
    ├─ 查 employee_profiles（判断是否员工且启用）
    ├─ 跳过：员工自身消费 / profit≤0 / quota=0
    ├─ decimal 精度计算
    ├─ INSERT employee_commission_logs
    └─ UPDATE user_extensions（原子 += ）
```

挂钩位置（3 处）：
- `service/text_quota.go` → `PostConsumeTextQuota`
- `service/quota.go` → `PostWssConsumeQuota`
- `service/task_billing.go` → `RecordTaskBillingLog`

退款冲销：`quota < 0` 时走相同公式，产生负值 commission_quota，同步原子减 user_extensions 汇总。

---

## 6. 返佣屏蔽

现有注册返佣触发点（`model/user.go`）：

```go
// user.Insert() 和 FinalizeOAuthUserCreation()
if common.QuotaForInviter > 0 {
    _ = inviteUser(inviterId)  // 增加 AffQuota
}
```

改造：在调用 `inviteUser` 前，检查 inviterId 是否是员工：

```go
if common.QuotaForInviter > 0 && !IsEmployee(inviterId) {
    _ = inviteUser(inviterId)
}
```

---

## 7. API 接口

### 管理员接口（需 admin role）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/admin/employee | 员工列表（分页） |
| POST | /api/admin/employee | 创建员工档案 |
| PUT | /api/admin/employee/:id | 更新员工档案 |
| DELETE | /api/admin/employee/:id | 删除员工档案（软删，status=2） |
| GET | /api/admin/employee/commission | 提成日志（分页、筛选） |
| GET | /api/admin/employee/commission/summary | 按员工汇总统计 |
| GET | /api/admin/channel/cost | 渠道成本配置列表 |
| POST | /api/admin/channel/cost | 设置渠道成本系数 |
| DELETE | /api/admin/channel/cost/:channel_id | 删除渠道成本配置（恢复默认 1.0） |

### 员工自查接口（需登录，仅本人数据）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/user/employee/profile | 查询自己的员工档案 |
| GET | /api/user/employee/commission | 我的提成记录（分页） |
| GET | /api/user/employee/commission/summary | 我的提成汇总 |

---

## 8. 前端结构

### 管理员页面：`web/default/src/features/employees/`

```
employees/
  api.ts                        API 请求封装
  types.ts                      EmployeeProfile / CommissionLog / ChannelCostConfig
  constants.ts                  状态常量
  index.tsx                     员工管理主页（Tab: 员工列表 / 提成记录 / 渠道成本）
  components/
    employees-table.tsx         员工列表表格
    employee-form-dialog.tsx    创建/编辑员工（提成比例、业绩目标）
    commission-logs-table.tsx   提成明细表（含汇总卡片）
    channel-cost-table.tsx      渠道成本配置表
    channel-cost-form-dialog.tsx
```

### 员工控制台：`web/default/src/features/employee-console/`

```
employee-console/
  api.ts
  types.ts
  index.tsx                     提成概览页
  components/
    commission-summary-cards.tsx  数据卡片（本月/累计）
    commission-history-table.tsx  明细（客户 ID 脱敏为 #XXXX）
```

### 动态菜单

在 `components/layout/config/` 中，`is_employee=true` 时侧边栏显示「我的提成」入口。

---

## 9. 字段兼容性说明

| 规则 | 处理方式 |
|------|---------|
| Rule 1（JSON） | 全部使用 `common.Marshal/Unmarshal` |
| Rule 2（多数据库） | 所有新表使用 GORM AutoMigrate，无手写 DDL；float 字段用 `REAL`（GORM 自动映射） |
| Rule 6（指针类型） | 请求 DTO 中可选字段均用指针 + omitempty |

---

## 10. 实施顺序

1. `model/user_extension.go` + `model/employee.go`
2. `model/main.go` — AutoMigrate 注册
3. `model/log.go` — RecordConsumeLog 返回 log.Id，修复所有调用方
4. `service/employee_commission.go`
5. 挂钩 text_quota / quota / task_billing
6. `model/user.go` — 屏蔽员工邀请人返佣
7. `controller/employee.go` + 路由注册
8. 前端 features/employees + features/employee-console

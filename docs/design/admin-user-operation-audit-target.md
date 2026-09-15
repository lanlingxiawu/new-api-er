# 管理操作审计：补全「被操作用户」

状态：已实现
日期：2026-09-07

## 1. 目标与范围

### 1.1 问题

管理员对用户执行的管理操作（调额度、禁用两步验证、重置通行密钥、解绑 OAuth、补单等）在操作日志里**看不出对象是谁**。管理员在日志页面只能看到「增加用户额度 $5」这样的文案，无法判断改的是哪个用户，因此无法审计。

### 1.2 目标

让**每一条针对用户的管理审计日志**在列表文案、详情弹窗和导出文件中都能明确显示被操作用户（用户名 + 用户 ID）。

### 1.3 范围

- 后端：统一 `other.op.params` 中被操作用户的表示，补齐缺失埋点，中间件兜底从路由参数提取目标用户。
- 前端：审计文案模板补全目标用户；详情弹窗新增「被操作用户」字段；兼容历史日志。
- 导出：新增两个审计列。
- 后端 / 前端 i18n 同步。

### 1.4 不在范围内

按已确认的口径：

- **不改数据库表结构**。不新增 `logs.target_user_id` 列，因此**本轮不提供「按被操作用户筛选日志」的检索能力**——只解决「看得出是谁」，不解决「按谁反查」。若后续需要检索，另开设计（涉及 MySQL / PostgreSQL / SQLite / ClickHouse 四路迁移与大表加索引）。
- **不改日志归属**。管理审计日志仍归属操作者（`Log.UserId` = 管理员），被操作用户本人在自己的日志页面**看不到**这些记录。
- 不回填历史日志。历史记录的 `Content` 已入库，`other.op.params` 缺 target 字段，只做「优雅降级」渲染，不做数据订正。
- 不改任何 API 的请求 / 响应结构与鉴权等级。

## 2. 现状与根因

### 2.1 现有机制

| 组件 | 位置 | 作用 |
|---|---|---|
| `RecordOperationAuditLog` | `model/log.go:308` | 写 `type=3` 日志，`UserId`/`Username` = 操作者；`other.op = {action, params}`、`other.admin_info`、`other.audit_info` |
| `recordManageAuditFor` | `controller/audit.go:99` | handler 内精细埋点，注入 `params.target_user_id` |
| `finishAdminAudit` | `middleware/audit.go:135` | 未手动埋点的管理写操作兜底，路由参数只进 `audit_info.params` |
| `auditContentTemplates` | `controller/audit.go:18` | action → 英文兜底文案，渲染进 `Log.Content`（导出用） |
| `AUDIT_TEMPLATES` + `renderAuditContent` | `web/src/features/usage-logs/lib/format.ts:374` | action → i18n 模板，展示期本地化渲染 |

### 2.2 三个根因

**根因 A：`target_user_id` 存了但从来不渲染。**
`recordManageAuditFor` 会把目标用户 ID 写进 `op.params.target_user_id`，但 `AUDIT_TEMPLATES` 里没有任何模板引用它，详情弹窗（`details-dialog.tsx:549`）也只渲染 `operationText` 和 `audit_info`，从不显示目标用户。

**根因 B：多数 `user.*` 模板不含用户标识。**
现有模板的用户信息来自各 handler 自行传的 `username`/`id`，而这两个键**并非所有埋点都传**：

| action | 埋点位置 | params 现状 | 模板文案 | 界面能否看出用户 |
|---|---|---|---|---|
| `user.create` | `controller/user.go:1175` | `username`, `role`, `target_user_id` | `Created user {{username}} (role {{role}})` | 有用户名，**无 ID** |
| `user.update` | `controller/user.go:855` | `username`, `id`, `target_user_id` | `Updated user {{username}} (ID: {{id}})` | ✅ |
| `user.delete` | `controller/user.go:1096` | `username`, `id`, `target_user_id` | `Deleted user {{username}} (ID: {{id}})` | ✅ |
| `user.manage` | `controller/user.go:1258/1373` | `action`, `username`, `id`, `target_user_id` | `Performed {{action}} on user {{username}} (ID: {{id}})` | ✅ |
| `user.quota_add` | `controller/user.go:1299` | `quota`, `target_user_id` | `Increased user quota by {{quota}}` | ❌ **完全看不出** |
| `user.quota_subtract` | `controller/user.go:1311` | `quota`, `target_user_id` | `Decreased user quota by {{quota}}` | ❌ |
| `user.quota_override` | `controller/user.go:1320` | `from`, `to`, `target_user_id` | `Overrode user quota from {{from}} to {{to}}` | ❌ |
| `user.binding_clear` | `controller/user.go:897` | `bindingType`, `username`, `target_user_id` | `Cleared {{bindingType}} binding for user {{username}}` | 有用户名，无 ID |
| `user.reset_passkey` | `controller/passkey.go:477` | `username`, `id`, `target_user_id` | `Reset the user passkey` | ❌ **params 有值，模板不用** |
| `user.2fa_disable` | `controller/twofa.go:569` | 仅 `target_user_id` | `Force-disabled two-factor authentication for the user` | ❌ |
| `user.oauth_unbind` | 中间件兜底 | 空 | `Removed an OAuth binding for the user` | ❌ **连 target 都没有** |
| `user.topup_complete` | 中间件兜底 | 空 | `Completed top-up order for the user` | ❌ |
| `subscription.user_plan_reset` | `controller/subscription.go:470` | `plan_id`, `target_user_id` | 模板未登记 | ❌ |

**根因 C：中间件兜底不提取目标用户。**
`finishAdminAudit` 把路由参数塞进 `audit_info.params`（管理员可见但不参与文案渲染），`op.params` 只在 `action == "generic"` 时写 `method`/`route`。所以走兜底的用户相关操作（如 `DELETE /api/user/:id/oauth/bindings/:provider_id`）文案里没有用户，只有在导出的 `audit_route`/`audit_path` 列里才能间接看出。

### 2.3 附带发现的两个缺陷

**C1：操作者操作自己时 target 被吞掉。**

```go
// controller/audit.go:104
if _, ok := params["target_user_id"]; !ok && targetUserId > 0 && targetUserId != operatorUserId {
    params["target_user_id"] = targetUserId
}
```

`targetUserId != operatorUserId` 这个条件导致 root 通过管理接口操作自己时（例如 `PUT /api/user/` 改自己的记录），日志里**没有 target_user_id**。审计上「对自己动手」恰恰是最需要留痕的场景。本轮修正为无条件注入。

**C2：`AdminCompleteTopUp` 无法得知目标用户。**
`model.ManualCompleteTopUp(tradeNo, ip)`（`model/topup.go:381`）内部已解析出 `userId`，但不返回，handler 拿不到，因此只能走兜底且无 target。

## 3. 数据流

```
管理员发起写请求
  → AdminAuth/RootAuth 鉴权通过 → beginAdminAudit 包装 ResponseWriter
  → handler 执行业务
      ├─ 成功且有精细埋点：recordManageAuditForUser(c, targetId, targetName, action, params)
      │     → params 注入 target_user_id / target_username
      │     → auditContentEN(action, params) 渲染英文 Content
      │     → RecordOperationAuditLog 写 logs(type=3)
      │     → markAuditLogged(c)
      └─ 未埋点（含失败提前返回）：
            finishAdminAudit
              → 按「路由 → 目标用户参数名」表解析 target_user_id
              → 写入 op.params（同时保留 audit_info.params 原有行为）
              → gopool 异步 RecordOperationAuditLog

管理员查看日志
  → GET /api/log/ (type=3)
  → 前端 normalizeAuditParams(op.params)：target_* 缺失时回退 username/id
  → 选模板：params 有目标用户 → 带用户模板；否则 → 无用户降级模板（历史日志）
  → 列表单元格渲染 / 详情弹窗额外渲染「被操作用户」行

导出
  → target_user_id / target_username 两个新列从 other.op.params 取值（AdminOnly）
  → content 列继续用入库时的英文 Content（新日志已含目标用户）
```

## 4. API 契约

**无新增端点，无请求 / 响应结构变更，无鉴权等级变更。**

`GET /api/log/` 返回的 `Log.other` JSON 中，`op.params` 新增两个可选键：

```jsonc
{
  "op": {
    "action": "user.quota_add",
    "params": {
      "quota": "$5.00",
      "target_user_id": 42,        // 已存在，本轮改为无条件写入
      "target_username": "alice"   // 新增
    }
  }
}
```

`op` 对普通用户可见（不含敏感信息），与现状一致；`admin_info` / `audit_info` 仍按 `model/log.go:126` 对普通用户剥离。

> 注：本轮**不新增查询参数**。日志列表的 `username` 筛选语义不变（筛的是操作者），不做二义性改造。

## 5. 数据模型变更

**无。** 不新增表、不新增列、不新增索引、无迁移。所有信息写入既有的 `logs.other` TEXT 列。

`other` 每条管理日志增加约 30–40 字节（`"target_username":"..."`）。管理写操作量级为每天数十至数百条，对表体积无实质影响。

## 6. 配置参数

**无。** 不新增 `setting/` 配置项——审计留痕是安全基线，不应可关闭。

## 7. 关键逻辑与边界

### 7.1 统一目标用户表示

约定 `op.params` 中被操作用户的规范键为：

- `target_user_id`：int，被操作用户 ID
- `target_username`：string，被操作用户名（**操作发生时的快照**，用户改名后日志不变，这是审计期望的行为）

`controller/audit.go` 调整：

```go
// recordManageAuditFor 保留（兼容无用户名的调用点），内部回查用户名。
func recordManageAuditFor(c *gin.Context, targetUserId int, action string, params map[string]interface{})

// 新增：调用方已持有用户对象时直接传，避免一次数据库查询。
func recordManageAuditForUser(c *gin.Context, targetUserId int, targetUsername string, action string, params map[string]interface{})
```

注入规则：

1. `targetUserId <= 0` → 不注入（非用户类操作，如 `channel.*` 走 `recordManageAudit`，其 targetUserId 传的是操作者自己，见下条）。
2. `recordManageAudit`（资源类操作）当前把操作者 ID 当 targetUserId 传入，**依赖 C1 的 `!=` 条件来避免误注入**。移除该条件后必须改为显式区分：`recordManageAudit` 不再走 `recordManageAuditFor`，直接调用底层，永不注入 target 字段。这是本轮唯一一处需要小心的重构。
3. 调用方已在 `params` 里显式给了 `target_user_id` 的（`subscription.go:470`），不覆盖。
4. `target_username` 为空时：`recordManageAuditFor` 调 `model.GetUsernameById(targetUserId, false)` 补齐；查不到则留空，模板降级为只显示 ID。

### 7.2 中间件兜底提取目标用户

新增登记表，**只对显式登记的路由**把路由参数解析为用户 ID——不能把 `:id` 一律当用户 ID，因为 `/api/subscription/admin/user_subscriptions/:id` 的 `:id` 是订阅 ID。

```go
// auditRouteTargetUserParam 登记「METHOD + 路由模板」→ 该路由中代表被操作用户的路由参数名。
var auditRouteTargetUserParam = map[string]string{
    "DELETE /api/user/:id/oauth/bindings/:provider_id": "id",
    "DELETE /api/user/:id/bindings/:binding_type":      "id",
    "DELETE /api/user/:id":                             "id",
    "DELETE /api/user/:id/reset_passkey":               "id",
    "DELETE /api/user/:id/2fa":                         "id",
    "POST /api/subscription/admin/users/:id/subscriptions":       "id",
    "POST /api/subscription/admin/users/:id/subscriptions/reset": "id",
    "PUT /api/admin/customer/:id/user":                 "id",
    "DELETE /api/admin/employee/:id/customer/:user_id": "user_id",
}
```

边界：

- 参数非数字或 `<= 0` → 不写入，不报错。
- 已登记但 handler 已精细埋点的路由（如 `DELETE /api/user/:id`），兜底不会执行（`ContextKeyAuditLogged` 已置位）；**但业务失败提前返回时兜底仍会跑**，此时目标用户依然能记下来——这正是保留这些条目的价值。
- 兜底路径**不查数据库**取用户名（在鉴权链路上，且异步 goroutine 里已有一次 `GetUsernameById`），只记 `target_user_id`；前端渲染时只显示 ID。

### 7.3 补齐缺失埋点

| 端点 | 处理 |
|---|---|
| `POST /api/user/topup/complete` | `ManualCompleteTopUp` 改为返回 `(userId int, err error)`；handler 用 `recordManageAuditFor` 记 `user.topup_complete`，params 带 `trade_no` |
| `DELETE /api/user/:id/oauth/bindings/:provider_id` | 维持兜底，由 7.2 的登记表提供 target |
| `user.2fa_disable` | `controller/twofa.go:569` 已持有 `targetUser`，改用 `recordManageAuditForUser` 传用户名 |
| `subscription.user_plan_reset` | 前后端模板都补登记（当前前端 `AUDIT_TEMPLATES` 缺该条，渲染返回 null 回落到原始 `content`） |

### 7.4 模板改造与历史日志兼容

**这是本设计里最容易出错的一点**：直接给现有模板加 `{{target_username}}` 会让历史日志渲染出「增加用户额度 $5，用户  (ID: )」这种半截文案。

方案：**双模板 + 归一化**。

```ts
// 有目标用户时使用
const AUDIT_TEMPLATES: Record<string, string> = {
  'user.quota_add':
    'Increased quota of user {{target_username}} (ID: {{target_user_id}}) by {{quota}}',
  // ...
}

// 无目标用户（历史日志 / 兜底未登记路由）时使用，保持原有文案不变
const AUDIT_TEMPLATES_NO_TARGET: Record<string, string> = {
  'user.quota_add': 'Increased user quota by {{quota}}',   // 与现状逐字一致
  // ...
}
```

`renderAuditContent` 流程：

1. `normalizeAuditParams(params)`：`target_user_id ??= id`、`target_username ??= username`（兼容 `user.update` 等已有 `username`/`id` 的历史日志——它们能自动升级到带用户的新文案）。
2. 有 `target_user_id` 或 `target_username` → 查 `AUDIT_TEMPLATES`；否则查 `AUDIT_TEMPLATES_NO_TARGET`。
3. 两表都未命中 → 返回 `null`，调用方回落到原始 `content`（现状行为不变）。

只有 `user.*`、`subscription.user_plan_reset` 这类目标是用户的 action 需要两套模板；`channel.*`、`option.*` 等资源类 action 不涉及，保持单表。

### 7.5 详情弹窗

管理审计区（`details-dialog.tsx` 的 `showManageAuditSection`）新增一行「被操作用户」，取值优先级：

`op.params.target_username`（有则显示 `alice (ID: 42)`）→ `op.params.target_user_id`（只显示 `ID: 42`）→ `op.params.username`/`id`（历史日志）→ `audit_info.params` 中登记路由对应的参数 → 都没有则**不渲染该行**（不显示「-」，避免暗示"没有目标用户"其实是"记录缺失"）。

同时把该区块现有的操作者信息行标签明确为「操作者」，消除与列表「用户」列的歧义。

### 7.6 导出

`model/log_export_columns.go` 新增：

- `opParamsCol("target_user_id", "target_user_id", "Target User ID")`
- `opParamsCol("target_username", "target_username", "Target Username")`

两列 `AdminOnly: true`、`NeedOther: true`、`Group: LogExportGroupAudit`，需新增 `rowCtx.opParams(l)` 访问器（与现有 `adminInfo`/`auditInfo` 同构，读 `other.op.params`）。两列加入内置 `auditColumns` 模板，插在 `audit_method` 之前。

历史日志缺字段 → `otherValue` 返回空串，与现有列行为一致。

## 8. 错误处理

- 审计写入失败沿用现状：`RecordOperationAuditLog` 内部 `common.SysLog` 记录，不影响业务响应。
- `GetUsernameById` 回查失败 → 用户名留空，只记 ID，不阻断日志写入，不影响业务响应。
- 路由参数解析失败 → 静默跳过 target 注入，日志照常写。
- 前端模板缺失 / 参数缺失 → 逐级降级到无用户模板 → 原始 `content`，任何情况下不渲染空占位符或 `undefined`。
- 不新增任何面向用户的错误响应，因此不涉及 `ApiErrorI18n` 新键。

## 9. 主链路影响（Rule 0）

**本功能完全不接触 AI 中继链路。**

- 埋点只发生在 `AdminAuth()` / `RootAuth()` 保护的管理接口；relay 路由不经过 `beginAdminAudit`/`finishAdminAudit`。
- `finishAdminAudit` 新增逻辑为一次 map 查表 + `strconv.Atoi`，O(1)，无 DB / 无 Redis。
- `recordManageAuditForUser` 相比现状**减少**一次数据库查询（调用方已持有用户名时不再回查）；`recordManageAuditFor` 的回查仅在管理写操作发生，频次为每天数十至数百次。

**共享资源审计：**

| 资源 | 本功能访问 | 中继链路是否访问 | 冲突 |
|---|---|---|---|
| `logs` 表 | 写 `type=3` 行（管理操作频次） | 写 `type=2` 消费日志（异步批量管道） | 同表不同类型；本功能写入量级比消费日志低 3~4 个数量级，且不经过异步管道，不与其争队列/批次 |
| `users` 表 | 读（`GetUsernameById`，仅缺用户名时） | 读（走缓存） | 只读，低频，无锁竞争 |
| Redis | 不访问 | — | 无 |
| 内存结构 / 协程池 | `finishAdminAudit` 沿用现有 `gopool`（现状即如此，不新增） | relay 使用独立路径 | 无变化 |

不新增索引；不新增查询模式（本轮不做按目标用户检索）。

## 10. 与现有子系统交互

- **日志导出**（`model/log_export_columns.go`、`controller/log_export.go`）：新增两列 + 内置模板变更；已保存的用户自定义模板不受影响（未选中的新列不会自动加入）。
- **计费 / 中继 / 缓存**：无交互。
- **权限**：新增列 `AdminOnly`，普通用户导出时被过滤，与 `admin_info`/`audit_info` 现有策略一致。
- **前端 i18n**：新增约 10 条模板字符串（带目标用户的版本），需同步 en / zh / zh-TW / fr / ru / ja / vi 七个 locale。按 CLAUDE.md Rule 6，直接编辑 locale JSON，不跑 `i18n:sync`（避免回填无关键）。旧文案键**保留**（`AUDIT_TEMPLATES_NO_TARGET` 仍在用）。
- **后端 i18n**：`auditContentTemplates` 是英文兜底基线，不走 `i18n/locales/*.yaml`，无需改后端 i18n 文件。

## 11. 测试用例（Rule 15.2，先写用例再实现）

### 11.1 `controller/audit_test.go`（新增）

`recordManageAuditForUser` / `recordManageAuditFor` 的参数注入——等价类 + 边界：

| # | 输入 | 期望 |
|---|---|---|
| 1 | targetUserId=42, username="alice", params=nil | `params.target_user_id==42`、`target_username=="alice"` |
| 2 | targetUserId=42 **== 操作者 ID** | 仍注入（覆盖 C1 回归） |
| 3 | targetUserId=0 | 不注入任何 target 键 |
| 4 | targetUserId=-1 | 不注入 |
| 5 | params 已含 `target_user_id=99` | 不被覆盖 |
| 6 | targetUsername="" 且用户存在 | 回查补齐 |
| 7 | targetUsername="" 且用户不存在 | 只写 ID，不 panic，不阻断 |
| 8 | `recordManageAudit`（资源类，channel.*） | **不注入** target 键（重构回归） |

`auditContentEN` 模板渲染：带 target 的模板渲染出用户名与 ID；params 缺 target 时占位符渲染为空串（`os.Expand` 现有行为）。

### 11.2 `middleware/audit_test.go`（新增）

| # | 请求 | 期望 |
|---|---|---|
| 1 | `DELETE /api/user/7/oauth/bindings/3` | `op.params.target_user_id == 7` |
| 2 | `DELETE /api/subscription/admin/user_subscriptions/7`（未登记） | 无 `target_user_id` |
| 3 | 路由参数为 `abc` | 无 `target_user_id`，无错误 |
| 4 | 路由参数为 `0` / `-1` | 不写入 |
| 5 | `DELETE /api/admin/employee/3/customer/9` | `target_user_id == 9`（取 `user_id` 而非 `id`） |
| 6 | handler 已 `markAuditLogged` | 兜底不写第二条日志（现状回归） |
| 7 | GET 只读请求 | 不产生审计日志（现状回归） |

### 11.3 `model/log_export_columns_test.go`（补充）

- `other.op.params` 含 target 两键 → 两个新列取到正确值。
- `other` 为空 / `op` 缺失 / `params` 缺失 → 新列返回空串，不 panic。
- 非管理员导出 → 两个新列被过滤（`AdminOnly`）。
- `auditColumns` 内置模板包含新列且列顺序符合预期。

### 11.4 `model/topup_test.go`（补充）

- `ManualCompleteTopUp` 返回值变更后，成功路径返回正确 `userId`；失败路径返回 0 + error（现有用例断言不回归）。

### 11.5 前端

无测试框架（Rule 15.7）。以 `bun run typecheck` + `bun run lint` 为门槛，并人工核对：

- 新日志（有 target）渲染带用户名 + ID 的文案。
- 历史日志（无 target）文案与改造前**逐字一致**。
- `user.update` 等有 `username`/`id` 的历史日志，经归一化后自动升级为新文案。
- 详情弹窗「被操作用户」行在无数据时不渲染。

### 11.6 全量

实现完成后跑 `go test ./...`（Rule 15.8），全绿方可交付。

## 12. 实施清单

**后端**

1. `controller/audit.go`：拆分 `recordManageAudit`（资源类，不注入 target）与 `recordManageAuditFor`/`recordManageAuditForUser`（用户类，无条件注入）；移除 C1 的 `!= operatorUserId` 条件；`auditContentTemplates` 补 target 占位符 + 补 `subscription.user_plan_reset` 之外遗漏的 action。
2. `middleware/audit.go`：新增 `auditRouteTargetUserParam` 表与提取逻辑。
3. `controller/twofa.go:569`、`controller/passkey.go:477`、`controller/user.go` 各埋点：改用 `recordManageAuditForUser` 传已持有的用户名。
4. `model/topup.go:381`：`ManualCompleteTopUp` 返回 `userId`；`controller/topup.go:570`：新增 `user.topup_complete` 埋点。
5. `model/log_export_columns.go`：新增 `rowCtx.opParams` + 两个新列 + 加入 `auditColumns`。

**前端**

6. `web/src/features/usage-logs/lib/format.ts`：`normalizeAuditParams`、`AUDIT_TEMPLATES_NO_TARGET`、`renderAuditContent` 改造、模板补全。
7. `web/src/features/usage-logs/types.ts`：`LogOtherData.op.params` 类型补 `target_user_id`/`target_username`。
8. `web/src/features/usage-logs/components/dialogs/details-dialog.tsx`：新增「被操作用户」行；操作者标签明确化。
9. `web/src/i18n/locales/{en,zh,zh-TW,fr,ru,ja,vi}.json`：新增带目标用户的模板字符串与「被操作用户」「操作者」标签。

**测试**

10. 按 §11 补测试，跑 `go test ./...` + `bun run typecheck` + `bun run lint`。

**文档**

11. 实现后回写本文档，记录实际实现与设计的偏差。

## 13. 已知遗留

- **仍无法按被操作用户检索日志。** 这是本轮明确排除的范围（不改表）。管理员只能按时间 + 类型筛出管理日志后逐条查看。若后续需要，方案是 `logs` 新增 `target_user_id` 索引列 + 四路迁移，另开设计。
- **被操作用户本人看不到针对自己的管理操作。** 按已确认口径维持现状。
- 中间件兜底路径只记 target 用户 ID、不记用户名（避免在鉴权链路上多一次查询），这类日志的文案会显示为「ID: 42」而非用户名。

## 14. 实现记录（与设计的偏差）

实现完成后回写。以下是实际代码与 §7 设计不一致的地方，均为实现期发现的问题。

### 14.1 模板改用单一 `{{target}}` 占位符

设计写的是模板里放 `{{target_username}} (ID: {{target_user_id}})` 两个占位符。实现时发现这在**兜底路径上会破**：`finishAdminAudit` 出于「不在鉴权链路上多查一次库」的考虑只记 `target_user_id`、不记用户名，双占位符会渲染出 `Removed an OAuth binding for user  (ID: 7)`。

改为在渲染期先把目标用户拼成一个字符串（`formatAuditTargetUser`），模板只留一个 `{{target}}`：

| 记录到的字段 | 渲染 |
|---|---|
| 用户名 + ID | `alice (ID: 42)` |
| 只有 ID（兜底路径） | `ID: 42` |
| 只有用户名 | `alice` |
| 都没有（历史日志） | 走 `AUDIT_TEMPLATES_NO_TARGET` |

后端的英文兜底模板（`auditContentTemplates`）仍用两个占位符，因为它只服务 handler 埋点路径，那里用户名一定已解析。

### 14.2 `getAuditTargetUser` 增加 action 白名单门控（设计遗漏的缺陷）

设计里的归一化规则是「`target_user_id ??= id`、`target_username ??= username`」。实现时发现这会**把渠道 ID 当成用户 ID**：`channel.update` 的埋点参数是 `{name, id}`，其中 `id` 是渠道 ID；`channel.delete` 等同理，兜底路径的 `audit_info.params.id` 也一样。按设计直接回退读取，渠道操作的详情弹窗会显示「被操作用户 ID: 5」。

修正为分级信任：

- `target_user_id` / `target_username` —— 后端只在用户类操作写入，**无条件信任**（含 action 为 `generic` 的兜底行）。
- 旧的 `username` / `id` 与 `audit_info.params.*` —— **仅当 action 是用户类**（`user.*` 前缀或 `subscription.user_plan_reset`）时才回退读取。

### 14.3 `auditContentEN` 折叠多余空格

占位符缺值（用户名回查失败）时 `os.Expand` 会留下 `Deleted user  (ID: 42)`。渲染后按空白切分再以单空格拼回，避免导出文本出现断句。这改变了一条既有测试的预期（`redemption.create` 空参数从 `Created  redemption codes named  ( each)` 变为 `Created redemption codes named ( each)`）。

### 14.4 精简 handler 的重复参数

`user.create` / `delete` / `manage` / `binding_clear` / `reset_passkey` 原先各自传 `username` + `id`。既然 `target_username` / `target_user_id` 已统一承载，这些 handler 不再重复传，只保留操作特有参数（`role`、`action`、`bindingType`…）。历史日志的旧键由 §14.2 的回退逻辑继续兼容。

### 14.5 `ManualCompleteTopUp` 幂等路径也返回用户 ID

设计只说「返回 userId」。实现时把赋值提到加载订单之后、状态判断之前，因此：

- 订单不存在 / 未提供订单号 → 返回 `0`（确实没有审计对象）；
- 订单已成功（幂等命中）或状态非法 → 仍返回用户 ID，失败的补单尝试同样能定位到人。

### 14.6 补登记 `subscription.plan_reset`

排查时发现该 action 后端有英文模板、前端 `AUDIT_TEMPLATES` 没有，日志会回落到原始 `content`。顺手补齐，和 `subscription.user_plan_reset` 一起登记。

### 14.7 导出列标签

`target_username` 列复用已存在的 i18n 键 `Target User`；只有 `Target User ID` 是新键。

### 14.8 测试

新增 `controller/audit_test.go`（10 个用例）、`middleware/audit_test.go` 追加 `auditTargetUserID` 表驱动用例（8 个分支）+ 登记表自检、`model/log_export_columns_test.go` 追加 4 个用例。更新既有测试：`controller/gen_ctrl_audit_test.go` 三处（模板变更）、`model/topup_test.go` 三处（签名变更）。

`go test ./...` 全绿；`bun run typecheck` 通过；`bun run lint` 在本次改动的两个前端文件上无 error（仓库其余 lint error 为既有问题，未在本轮处理）。

### 14.9 仍然遗留

§13 的三条遗留全部保持不变——尤其是**仍无法按被操作用户检索日志**，这是「不改表」档位的固有结果。

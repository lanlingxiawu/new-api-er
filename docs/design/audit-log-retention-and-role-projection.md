# 日志与审计：保留策略、角色投影与渠道名快照

状态：已实现
日期：2026-09-19

本文覆盖一组合并后暴露的日志/审计缺陷的修复口径。四件事互相独立，但都落在
「谁能看到哪条日志的哪个字段」与「这些表会不会无上限增长」这两条线上。

---

## 1. 渠道名快照：写入端与兜底查询

### 1.1 问题

上游把 `channel_name` 列入 `model/log_other.go` 的敏感字段黑名单（`SetPublic` 拒绝）
并删掉了写入点，全仓因此没有任何地方再写渠道名快照。后果有两个：

1. 渠道删除且 `platform_channel_daily_stats` 的名称快照缺失时，
   `ResolveChannelDisplayNames` 的最后一级兜底 `GetChannelNameSnapshotsFromLogs`
   永远返回空，佣金明细的「渠道」列空白。
2. 该查询的 `other LIKE '%channel_name%'` 永远凑不满 limit，必须扫穿 channel_id
   区间，而且用 `context.Background()` 没有超时——占的是消费日志异步管线同一个
   LOG_DB 连接池。

### 1.2 口径

**恢复写入端，而不是摘掉兜底。** 渠道名是审计上必须留存的事实：渠道删除后
`channels` 表再也回答不了「这条日志跑在哪个渠道」。写入放在
`service.AppendRelayLogAdminInfo`，用 `other.SetAdmin("channel_name", ...)`：

- 落在 `admin_info` 下即自动对普通用户不可见，不违反上游把它从 public 剔除的意图；
- 取值只读 `relayInfo.ChannelName`（distributor 已写入的内存快照），
  取不到时退回上下文键 `constant.ContextKeyChannelName`，**不新增任何数据库查询**（Rule 0）；
- 取不到名字时不写空字段，避免每条日志多一个无意义的键。

### 1.3 兜底查询的上界

`GetChannelNameSnapshotsFromLogs` 现在有三层上界：

| 约束 | 值 | 理由 |
|---|---|---|
| 时间窗 | `ChannelNameSnapshotLookbackDays = 30` | `LIKE` 不走索引且命中数凑不满 limit，没有窗口就会沿 channel_id 索引扫穿整个区间 |
| 超时 | 3s（`channelNameSnapshotQueryTimeout`） | 解析不出名字只让「渠道」列留空，不值得拖住 LOG_DB 连接 |
| LIKE 模式 | `%"channel_name":%` | 匹配 JSON 键本身，而不是任意含该子串的取值 |

解析时优先读 `admin_info.channel_name`（当前写入形状），取不到再读顶层
（角色隔离之前的历史形状）。更早的已删渠道靠 `platform_channel_daily_stats`
的名称快照解析——这是正常路径，logs 只是最后一级兜底。

---

## 2. 员工视角日志的角色投影

2026-11-13 按当前代码校正：`/api/log/employee` 使用用户认证，controller 额外
验证 `model.IsEmployee`，model 限定为员工本人及其客户的日志。返回前使用
`model.formatEmployeeLogs`：保留真实日志 ID 和渠道编号，清空渠道名称列，移除
顶层 `channel_name`、`root_info`、`audit_info`，以及 `admin_info.channel_name`
和平台承担的超时成本 `admin_info.timeout_absorbed`。其余 `admin_info` 保留，
包括 `request_policy`；员工错误正文不作普通用户视角的脱敏。

员工查看「请求策略决策」复用上述接口返回的 `other.admin_info.request_policy`，
从日志表格经 `DetailsDialog` 传入 `LogDetailBody`，使用既有
`PolicyDecisionRecord` 展示选择渠道、失败、重试和终止记录。新增可选显示参数
`canViewPolicyDecisions`，由管理员/员工日志视角的 `showAdminFields` 提供；未传时
沿用 `isAdmin`，保持其他详情调用方的行为。普通用户视角隐藏记录，无记录时不显示
空分区。此参数只控制策略记录，不开放「查询上游」「查同类」等管理员操作。

这是已有数据的前端显示权限修正，不增加接口、配置、存储字段、数据库查询或迁移；
错误处理沿用现有日志加载逻辑。没有新增共享资源访问，转发主链、日志写入、计费、
Redis、连接池和后台任务均不变。回归测试通过真实表格详情入口覆盖员工、管理员、
普通用户、缺失/空记录及管理员操作边界。

---

## 3. `audit_logs` 保留策略

### 3.1 问题

`AccessTokenAudit()` 挂在 apiRouter / webRouter / NoRoute 上，**每个带鉴权的后台请求
都写一行**，量级远超原来的登录日志。而 `log_cleanup` 系统任务与 ClickHouse TTL
都只覆盖 `logs`，`audit_logs` 没有任何保留期。

### 3.2 配置（Rule 12）

`setting/operation_setting/audit_log_setting.go`：

| 字段 | 默认 | 区间 | 含义 |
|---|---|---|---|
| `audit_log_setting.retention_days` | 180 | 0 或 7–3650 | 0 = 永久保留、不清理 |

注册进 `service/settingsaccess/scopes.go` 的 `operations.logs` 分区。该分区因此
从 `scope()` 改成 `configScope()`：ConfigManager 支撑的配置保存走 `SaveConfigGroup`，
而 `AllowsGroup` 要求 `GroupKeys[module]` 存在，普通 `scope()` 构造出来的是空 map。
前端入口在「运营 → 日志维护」区块（`log-settings-section.tsx`）。

### 3.3 清理执行

挂在既有的 `log_cleanup` 系统任务上：消费日志删完后调用
`service.runAuditLogCleanup`，按保留期换算出的时间上界分批删除。

- 批大小沿用 `logCleanupBatchSize`，单次任务最多 `auditLogCleanupMaxBatches = 2000` 批：
  审计表首次清理可能积压数百万行，不能把连接占到任务超时，剩余行数留给下一次任务。
- **失败只记 warn，不把任务判失败**——消费日志已经删干净了。
- ClickHouse 新建审计表不带 TTL；初始化移除旧表的 TTL。所有保留配置统一由清理任务执行，0 跳过清理，正值按当前时间上界删除。

**2026-11-15 修复前实测：** 固定 TTL 与 3.2 的永久/超过 180 天保留配置冲突；配置为 0 时，181 天前的测试记录经原生合并从 1 条变为 0 条，近期对照仍为 1 条。随后已按 3.4 方案修复，真实 ClickHouse 25.8.33.6 新建/旧表升级、再次初始化无 DDL、原生合并保留旧行及 365 天→30 天应用清理切换均通过。全部发生于隔离库，详见 [上线审计修复补充](release-audit-20261115.md#本分支修复补充2026-11-15)。

### 3.4 三库兼容（Rule 2）

2026-11-15 已实施修复（用户要求修复本分支问题）：ClickHouse 新审计表不再附加 TTL；初始化检查现有审计表的建表元数据，仅发现 TTL 时执行 `ALTER TABLE audit_logs REMOVE TTL`。统一由现有清理任务读取当前保留配置，设置变更无需动态 DDL；0 跳过清理，正值沿用按时间清理。初始化失败向上返回错误，不能在旧 TTL 仍存在时宣称迁移完成。SQLite/MySQL/PG 分支不变，无表列/索引变化。已验证新库、旧 180 天 TTL 升级、第二次初始化无 DDL、永久及 365 天/30 天配置切换、原生合并保留旧行和应用清理边界。仅操作独立测试库；已被历史 TTL 删除的数据不能由此恢复。

该迁移仅在启动访问日志表元数据，移除 TTL 可能短暂占用日志表元数据锁；运行时不新增 relay 查询或连接池，现有后台清理仍共享日志数据库 I/O。生产执行前仍需在实际 schema 副本演练。语法依据：[ClickHouse REMOVE TTL](https://clickhouse.com/docs/reference/statements/alter/ttl)、[system.tables](https://clickhouse.com/docs/reference/system-tables/tables)。

`DELETE ... LIMIT` 只有 MySQL 支持，PostgreSQL 与默认编译的 SQLite 下 GORM 会
**静默丢掉 LIMIT**，一条语句删空整段区间并长时间持有事务与连接。因此
`DeleteOldAuditLogBatch` 先 `Pluck` 一批主键再按主键删——两条语句，三库行为一致，
且真正是分批。`logs` 的 `DeleteOldLogBatch` 同样有这个缺陷，已按同一形状修正，
取主键的排序（`created_at, id`）跟索引 `idx_created_at_id` 一致。
ClickHouse 分支与 `logs` 一致：`ALTER TABLE ... DELETE ... SETTINGS mutations_sync = 1`
一次删完并返回行数，让调用方的进度循环一轮结束。

---

## 4. 管理日志口径归一

`RecordLogWithAdminDetails` 仍往 `logs` 写 `type=3`（管理），而前端已把「管理」
类型标为 deprecated 并提示「新记录请看审计日志」——审计口径断成两半。

改法：`type=3` 的三个写入点全部改走审计表。

| 原写入点 | 新 action | 归属 |
|---|---|---|
| `model.AddEmployeePerformance` | `employee.performance_adjust` | 操作者（被操作员工进 `op.params.target_*`） |
| `model.RevertEmployeePerformance` | `employee.performance_revert` | 同上 |
| `controller.logBlockedMutualInvitation` | `customer.bind_blocked` | 同上 |

留痕从 model 层上移到 controller 层：只有那里拿得到请求上下文
（IP / request_id / 路由 / 操作者身份），这也是 `RecordOperationAuditLog` 的既有约定。
为此 `AddEmployeePerformance` 去掉了 `operatedBy` 参数、结果结构体带上 `Reason`；
`RevertEmployeePerformance` 改为返回 `*PerformanceRevertResult`。

content 模板登记在 `controller/audit.go` 的 `auditContentTemplates`，
前端渲染模板在 `web/src/features/usage-logs/lib/format.ts` 的 `AUDIT_TEMPLATES`，
七语 i18n 同步。

**保留在 `logs` 的例外**：`service/employee_commission.go` 的「互相邀请跳过提成」
写的是 `type=4`（系统），不是管理类型，前端也没有对它的 deprecated 标记；
它发生在结算侧、频次由中继量决定，不适合逐条进审计表。因此保持不变。
`type=3` 全部迁走后，前端对「管理」类型的 deprecated 标记与文案已属实，无需改动。

---

## 5. 导出列注册表

### 5.1 补齐合并新增的字段

| 分组 | 新增列 |
|---|---|
| tokens | `image_cache_tokens`、`billing_tokens` |
| billing | `billing_unit`、`fixed_price`、`image_count`、`request_rules`、`tool_surcharges`、`usage_facts` |
| admin | `billing_model`、`conversion_diagnostics`、`channel_affinity`、`task_plugin` |
| root | `upstream_task_id`、`task_node_name`、`task_plugin_runtime` |

`builtin:billing` 因此能复算四种账单形态：按 token（倍率 + token 明细）、
按次固定价（`billing_unit` / `fixed_price`）、按图片数（`image_count`）、
任务用量（`usage_facts`），外加工具调用附加费（`tool_surcharges`）。

`web_search_call_count` / `file_search_call_count` / `web_search_price` /
`file_search_price` 四列已停写（逐工具计费改由 `tool_surcharges` 承载），
但历史日志里仍有值：保留列并在标签上标注「历史」，同时从 `builtin:billing` 移除。
直接删列会让含这些 key 的用户模板在 `ResolveLogExportColumns` 处报未知列而整单失败。

### 5.2 root 可见性

`other_raw` 原样导出整条 `other`（含 `root_info`），此前只标了 `AdminOnly`，
而导出扫描不经 `FormatAdminLogs`——普通管理员可以用它把 root 专属诊断整段导走。

新增 `LogExportColumn.RootOnly`，并加 `ResolveLogExportColumnsForRole(keys, viewerRole)`：

- `other_raw` 与三个 `root_info` 列标 `RootOnly`；
- 建任务时按 `c.GetInt("role")` 解析，任务里存 `creator_role`，
  后台执行时按同一角色重新解析（历史任务该字段为 0，按非 root 处理）；
- 列目录接口不把 root 专属列下发给普通管理员——列在那儿却导不出来只会让人以为出了故障；
- 旧的两档入口 `ResolveLogExportColumns(keys, isAdmin)` 按管理员判权，同样挡住 root 列。

`LogExportMaxColumns` 从 100 提到 150：`builtin:full` 当前 98 列，原上限已无余量。

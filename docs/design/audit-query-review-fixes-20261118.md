# 审计列表与查询回归修复（2026-11-18）

用户在两轮审计后明确要求“修复”。本次范围为七项已复现缺陷及一项测试契约修正，沿用已批准的权限、文案和 IP 记录要求。

2026-11-23 后续修正：审计前端的默认滚动时间窗已改为本地时区昨天至今天的完整自然日；接口缺省回退仍为最近 24 小时，分页固定范围和手动时间保持不变。当前实现与验证见[完整自然日修复说明](audit-self-security.md#frontend-lifecycle-and-copy)。下文保留当时的修复背景。

## Existing Logic and Related-Path Findings

- `self_access.go:GetSelfSecurityAlert` 只按 action.alert_id 查询，遗漏 `SecurityResponseLink` 指向的共享处置；`account_notify.go` 使用该投影判断是否等待处置。`account_response.go:loadAlertResponses` 已有同账号、最新处置组与旧独立处置的合并规则，个人接口应复用。
- `store.go:Apply` 只为 IP 风险规则写 `SecurityAlertEvent`。列表、统计、导出却依赖该索引匹配证据 IP，导致 `bulk_key_access` 等规则漏查。风险标记查询已有 `ipRiskRules` 白名单，证据入索引不等于标记 IP 为风险。
- 个人、员工客户、管理活动、关联记录接口每次重新计算默认时间窗，而游标绑定时间窗。客户/活动/关联游标目前只含绑定摘要，无法恢复默认窗口；个人游标已包含起止时间。
- `ListAlertAssignees` 先截取 100 个账号再验权限，后续有效人员永远无法出现。现有 MultiSelect 支持本地搜索，接口为数组。
- `DataTablePage` 只监听 TanStack 页码，但监控、个人安全、客户记录和 IP 风险列表使用独立游标。
- 客户列表 Refresh 只 refetch 固定截止时间；IP 风险错误 Retry 重发失效游标。个人安全已有滚动默认时间窗的交互可复用。
- 本地化测试枚举内部 `Facts`，而 `publicSecurityEvent` 明确移除执行身份、策略时间、首次观察时间、新管理来源标记。测试应约束实际公开字段。

## Goals and scope / Data flow

1. 个人详情复用处置摘要加载器，再投影为现有个人 DTO；通知等待共享组完成，终态通知遍历该组关联的同账号可见告警，保持去重和冷却。
2. 后台 Apply 对所有规则保存有界证据关联，仍仅对 IP 风险规则更新风险修订号。列表、统计、导出共用现有索引条件。遵守既定的[新功能不回填旧数据](new-feature-no-history-backfill.md)要求，不恢复已删除的历史证据补录任务；旧告警只有在后续正常命中、重新保留证据时才为这些证据建立关联。
3. 新游标携带起止时间；HTTP 参数缺省时恢复游标窗口，显式参数仍优先，并由服务层校验身份、完整筛选条件、位置和修订号。旧游标携带显式时间时仍兼容；缺少窗口的旧游标不能凭摘要恢复时间。
4. 负责人按账号主键分批枚举、逐个验证当前权限，返回所有有效候选并按用户名排序。保持数组协议和现有本地搜索，不把任意截断移到另一个位置；请求仍受查询超时限制。
5. 通用表格增加可选 `scrollResetKey`，游标列表传入已应用筛选/游标的稳定值，仅查询或页面变化归顶，不因后台刷新归顶。
6. 客户列表区分默认滚动窗、手动窗和事件调查窗。默认 Refresh 推进窗口并回第一页；手动/事件窗口保持固定。风险 Retry 回第一页，普通日志重试维持当前页。

本地化测试改为检查实际公开的 Facts，并增加后端投影测试，确认四个内部来源字段不会返回客户端。补齐公开字段 `permission_increase` 的标签映射，复用七语言已有的 `Changed access token permissions`，没有新增或修改译文。

## API contracts and authorization

不新增路由、不变更响应 DTO、不放宽权限。个人安全保持 UserAuth + dashboard session 且只读本人；客户审计保持当前员工/客户归属复核；管理列表/关联/候选人员保持 AdminAuth 及审计权限门禁。游标新增内部 Start/End 字段，不接受跨身份、跨筛选或跨修订重用。无新文案和配置。

## Data model / Index design

无新表、字段或配置。复用 `audit_security_alert_events` 的事件/告警唯一键及告警/IP 索引、处置组链接索引。证据随原告警保留和清理路径删除。读查询继续有界时间与 keyset；不添加无界日志查询或 JSON LIKE 扫描。

复核 `model/user.go` 后发现 role/status 没有索引。新增 `idx_users_security_assignees(role,status,deleted_at,id)`，由 User AutoMigrate 创建，支持按角色/启用状态/软删除筛选后的主键续查，避免完整候选查询扫描普通用户。候选分批 100 条且不锁 users；索引字段不包含 relay 更新的 quota/used_quota/request_count。索引构建只在启动迁移阶段执行；发布前仍须在实际生产 schema 上演练。

## Main Chain Impact

本次没有同步 relay 代码、队列提交或新增每请求 goroutine。证据写入在已有安全 worker 事务内；通知在已有后台批次中；其余为管理/个人读取与前端状态。AI 消费及错误日志继续无条件记录 ClientIP，个人开关不影响记录。

## Shared Resource Audit

安全 alerts/findings/alert_events/actions/response_groups/response_links/deliveries/activity/risk 表与 relay 热路径隔离。管理查询和 worker 沿用独立审计连接池，不新增连接；users 仅按主键/现有角色状态读取，无行锁/写入；Casbin 策略只读。无新增 Redis 命名空间或缓存，通知继续现有独立池和限流。前端状态为组件局部，既有鉴权/HTTP 文件不修改。

## Concurrency Analysis / Errors and lifecycle

30k/100k RPM 下 relay 每请求新增 DB/Redis 调用、锁、goroutine 均为 0。每条命中证据数受已有保留上限约束，关联批写最多 100 条/批，主键冲突幂等。共享组通知按告警主键分批读取，在现有 worker context 内完成，事务失败保留 pending 以便重试；个人通知 payload 仍不暴露管理信息。查询失败走现有本地化错误；显式时间非法、游标损坏/跨身份等失败关闭。前端失效游标由用户 Retry 重新查询，不无限自动循环。

## Verification

先将两轮复现加入正式回归；覆盖本人/跨账号投影、共享组通知等待与终态、非 IP 规则证据查询/统计及无风险误标、默认游标延续及篡改拒绝、超过 100 个候选、默认刷新/自定义窗口、风险重试、手机分页归顶。执行 SQLite、真实 MySQL/PostgreSQL、控制器/权限路由、前端组件、类型检查、涉及文件 lint、生产构建及真实 Chrome 手机布局检查。测试使用自有临时数据库与本地传输 fixture，生产环境不操作。

### 实际结果（2026-11-18）

本轮修改前，四个后端复现用例及四个前端复现用例均确认失败；修复后通过。原始日志与执行脚本保存在 `_local/audit-query-fixes-20261118/`，`changed-sources.json` 记录本轮 35 个源码/测试文件的 SHA-256。

| 检查 | 结果 |
| --- | --- |
| `go test ./service/auditsecurity -count=1 -v -timeout=8m`，独立 SQLite | 122 项顶层测试通过、9 项跳过、0 失败；跳过项需要真实数据库或 Redis，相关数据库查询计划另在下面两种真实数据库执行。 |
| MySQL **5.7.44**、PostgreSQL **18.7** 定向业务回归 | 每种数据库 34 项顶层测试通过、1 项 Redis 恢复测试跳过、0 失败；覆盖共享处置、个人通知、候选人员、证据 IP 查询/统计/导出、查询计划与权限边界。 |
| `TestSecurityAssigneeUserIndexMigration`，SQLite/MySQL/PostgreSQL | 新建 users 表、从生产参考版本 `127c82331f979f2937cd1035149820eb79e6e1b6` 的 User 持久化结构升级均通过；新索引存在、用户数据保留、第二次迁移没有 DDL。候选人员 EXPLAIN 实际使用新索引。 |
| 控制器与路由定向回归 | 14 项顶层测试通过；实际 HTTP handler 覆盖个人、客户审计、客户风险、活动、关联五组接口的默认时间窗翻页、参数变更拒绝与相关权限门禁。 |
| `TestRelayLogsAlwaysRecordClientIP` | 9 个场景通过，包括设置缺省、关闭、开启、IPv6、可信/不可信代理；保持无原始请求或地址无效时不伪造 IP。 |
| `bun run test`，审计与客户详情组件 | 19 个测试文件、234 项测试通过。 |
| 类型、格式与构建 | `bun run typecheck`、涉及文件 lint、`bun run build`、根模块 `go build` 均通过；涉及源码 UTF-8 无 BOM，`git diff --check` 通过。 |
| Chrome，390×844 手机视口 | 构建产物配合本地 API fixture，列表滚动至 1200px 后“下一页”和“上一页”均回到 0px，首条记录标题可见，无 JavaScript 错误；组件测试另验证后台刷新保持滚动位置。 |

可复现入口：`python _local/audit-query-fixes-20261118/run_checks.py backend|http|frontend|typecheck|build`（每次传一个检查名）；`python _local/audit-query-fixes-20261118/run_matrix.py mysql` / `postgres`；`python _local/audit-query-fixes-20261118/mobile_scroll.py`。各脚本清空应用 DSN，数据库矩阵仅使用自有 `audit_test_*` 临时数据库并完成清理；保留已有测试容器。实际环境、命令与结果分别见 `mysql-results.json`、`postgres-results.json` 及对应日志。

验证边界：没有连接或部署生产；users 索引迁移验证针对受影响表及旧版模型，不等同于整个应用的生产 schema 演练；PostgreSQL 9.6、ClickHouse、Redis 恢复及生产规模负载本轮未运行。浏览器使用模拟 API，不能视为真实登录后的全栈验收。五个上游鉴权/HTTP 文件与本轮开始时的副本一致。旧告警不补录、旧版缺少时间窗的游标需刷新重查，均为前述明确边界。

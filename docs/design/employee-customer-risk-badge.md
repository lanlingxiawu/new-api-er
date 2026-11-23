# 审计日志未确认风险数字徽章（员工与管理员）

日期：2026-11-23。状态：**已实现并完成本地验证，未部署**。

2026-11-23 交互复核结论：补齐点击落点、全部时间的未确认列表、收起状态的数字和处理反馈。用户随后明确要求“开始实现”，并确定前端刷新间隔为 1 分钟。

同日用户指出“已处理和未处理没有做区分”：修订为明确的“未处理 / 已处理 / 全部”筛选，并把每行状态标签与确认按钮分开。未处理包含尚未确认完成的处理中记录；已处理包含已确认和误报。下文未确认计数与未处理分组指同一待办集合。

## Goals and Scope

在截图所示侧栏“审计日志”菜单右侧显示未确认风险数量。2026-11-23 用户追加“管理员也加上”，范围扩展为员工和管理员：

| 当前身份与权限 | 徽章统计范围 |
| --- | --- |
| 具备 `audit.read` 和 `audit_security.read` 的管理员 | 当前管理员在“安全监控”中有权查看的全部未确认风险；普通管理员遵守 `visibility=RoleAdminUser` 边界。 |
| 超级管理员 | 全部未确认风险，与现有超级管理员安全监控可见范围一致。 |
| 有效员工，且不具备上述管理员查看资格 | 当前归属客户中该员工获授权查看的全部未确认风险，对应“我的客户 → 风险记录”。 |
| 其余用户 | 不显示风险徽章、不发送计数请求。 |

兼具员工身份且可查看安全监控的管理员优先使用管理员范围，只展示一个数字；两个来源不直接相加。普通管理员可见范围与员工客户投影不是包含关系，因此不把两者简单相加解释为总量。没有安全监控查看权限、但仍是有效员工的账号继续使用其客户范围。员工本人“我的安全”不另行累加。

- `open`、`investigating` 属于未确认；`resolved`、`false_positive` 不计入。
- 员工及管理员的风险列表均提供“未处理 / 已处理 / 全部”筛选；徽章仅统计未处理，已处理记录保留在有权查看的历史列表中，继续遵守原保留/清理策略。
- 按风险记录 ID 去重，每条记录计 1；命中次数 `occurrences` 不作为徽章数量。
- 统计全部仍未确认的记录，不随页面日期、客户选择或分页变化；超过默认两天范围的待办仍会提醒。
- 0 条隐藏，1–99 条显示数字，至少 100 条显示 `99+`。数字和现有侧栏徽章采用同一外观。
- 侧栏展开及移动端显示行内徽章；收起时在审计图标右上侧显示同一个数字，tooltip 补充范围和数量。均复用现有 Badge 和主题样式。
- 有徽章时点击整个“审计日志”菜单直接进入所统计范围的“未处理（全部时间，含处理中）”；员工进入“我的客户 → 风险记录”，管理员进入“安全监控”。页面的其他入口保留既有默认行为。
- 单条确认、状态修改或管理员批量确认结束后立即重新读取数量；页面可见期间每 60 秒（1 分钟）刷新，重新聚焦过期页面时刷新。刷新周期按 2026-11-23 用户“1分钟即可”确定。

## Existing Logic and Related-Path Findings

以下记录为实施前对代码及相关测试的调查；其中原有能力的限制已由后文所列实现补齐：

| 代码入口 | 已核实行为及复用方式 |
| --- | --- |
| `web/src/hooks/use-sidebar-data.ts:useSidebarData`、`use-sidebar-view.ts:useSidebarView` | “审计日志”已有独立导航项。`SidebarBadges` 目前只承载模型定价徽章，可以增加审计风险徽章字段。 |
| `web/src/components/layout/components/nav-group.tsx:NavBadge/SidebarMenuLink` | 已通过项目 `Badge` 渲染菜单数字。复用其尺寸和主题语义色；增加可选辅助说明，其他导航项保持原有默认值。 |
| `web/src/features/system-settings/models/pricing-issues.ts` | 已有 Query 获取业务数据、格式化数字、传入导航的用例，可沿用这种组合方式；不把业务请求写进通用导航组件。 |
| `web/src/features/usage-logs/audit/index.tsx`、`components/employee-customer-audit.tsx` | 员工页签由 `is_employee` 控制，客户筛选为空时显示当前员工全部客户。前端资格仅控制展示，后端仍须授权。 |
| `web/src/features/customer-console/customer-audit-dialog.tsx:CustomerRecordList`、`audit-api.ts` | 风险接口只返回 20 条和 `next_cursor`，没有总数或未确认计数。不能把当前页长度当总数，也不应让侧栏翻完所有页面。Query 使用 `customer-audit` 前缀。 |
| `audit/lib/time-range.ts:defaultAuditTimeRange` | 当前前端默认昨天 00:00 至今天 23:59:59；后端省略时间仍默认滚动 24 小时。徽章是全部未确认数量，辅助文案明确这一口径。 |
| `security/alert-confirm-button.tsx`、`alert-status-cell.tsx` | 确认提交 `status=resolved`；成功失效 `['customer-audit', userID]`。列表和客户详情共用确认组件，将计数 Query 放在该前缀下即可同步刷新。冲突不自动重试写操作。 |
| `router/api-router.go`、`middleware/access_token_routes.go`、`controller/employee_customer_audit.go` | 客户接口位于 `UserAuth` 组，另有 dashboard session-only 规则。`employeeAuditContext` 使用 3 秒截止时间及 `AuditReadDB(false)`；既有错误转换可复用。 |
| `model/employee_customer_audit.go:RequireEmployeeAuditAccess`、`model/employee_audit_list.go` | 直接读取当前启用账号、启用员工档案；客户归属来自 `users.inviter_id`。普通角色、未软删除且非有效员工的客户可见，禁用客户仍可调查。客户按 ID 每批 200 个查询。 |
| `service/auditsecurity/customer_access.go:customerAlertAccessible/accountAlertAccessible`、`login_target.go:alertAccountID` | 风险权限包含规则版本、历史快照证明和可信登录目标；不能只按 `actor_id` 或负责人统计。旧记录最多检查 101 条快照，身份不完整时拒绝。 |
| `service/auditsecurity/employee_customer_list.go:ListEmployeeCustomerAlerts` | 汇总风险先授权客户，再核验每条告警，最后复核客户归属。最终身份查询限制 21 条；新计数不能直接向该方法传 100 个 ID。 |
| `service/auditsecurity/investigation.go:UpdateAlertInvestigation`、`store.go`、`deletion.go` | 确认以版本条件更新；重新出现风险可回到 open；清理标记立即使记录不可见；自动清理只处理已关闭记录。未确认记录不能因超过保留天数而在徽章中被静默丢弃。 |
| `service/auditsecurity/alert_query.go:ListAlerts` | 既有管理端 `unresolved` 是无日期条件的活动待办查询。本设计沿用这一业务口径，但使用员工客户权限及专用索引。 |
| `model/audit_query.go:InitAuditReadPools` | 审计 main/log/employee-snapshot 池分别最多 2 个连接；计数复用 main 读取池，不借用 relay 或安全 worker 的连接。 |
| `router/api-router.go:securityAudit`、`service/authz/resources_audit.go` | 管理安全接口统一经过 AdminAuth、AuditRead 和 AuditSecurityRead。查看资格与 manage 分开；只有查看权限的管理员也可看到提醒，确认仍要求原 manage 权限。 |
| `model/audit_security.go:SecurityVisibleQuery` | 普通管理员只能读取 `visibility=RoleAdminUser`；Root 无该附加限制。新管理员计数复用此方法，不按负责人、客户或登录用户 ID 收窄，也不由客户端指定 visibility。 |
| `controller/audit_security.go:GetSecuritySummary/securityQueryContext`、`service/auditsecurity/summary.go` | 现有 summary 只接受 `view=id`，要求日期范围，扫描最多 10,001 条并返回多个统计字段及容量；不适合作为全部未确认的轻量侧栏计数。管理查询已使用 3 秒 context 和同一隔离审计池。 |
| `web/src/features/usage-logs/audit/index.tsx`、`lib/admin-permissions.ts:hasPermission` | 安全监控页签要求管理员角色及两个查看权限，Root 由既有权限函数放行；新 Hook 沿用同一条件，不仅判断 role。 |
| `security/use-alert-batch-confirmation.ts`、`alert-status-cell.tsx`、`security-alert-cleanup.tsx` | 批量确认结束、状态修改、归档和删除均失效 `audit-security`；管理员计数放在该前缀下，部分成功也重新读取真实数量。 |
| `routes/_authenticated/usage-logs/audit.tsx`、`audit/index.tsx:AuditLogs` | 路由没有 search 校验，scope 是默认 `all` 的组件状态；员工实际回退到 `self`。只给原菜单加数字并不能把人带到对应风险，需要显式入口状态及权限回退。 |
| `customer-audit-dialog.tsx:CustomerAuditContent/CustomerRecordList` | 内层默认“操作日志”，风险页没有未确认筛选，所有请求都带日期；空态文案为“该时间范围内没有记录”。仅跳到“我的客户”或仅删除前端日期参数都不能实现待办入口。 |
| `controller/employee_customer_audit.go:employeeCustomerAuditFilter`、两种 `DecodeCursor`、`queryAccountAlertRecords` | 当前客户接口/游标强制有限时间窗，候选按 `(last_seen,id)` 排序；查询还被“我的安全”复用。必须为员工风险新增显式 `view=unresolved` 分支，不能放宽共用日志/个人安全的时间校验。 |
| `security/monitor.tsx:SecurityMonitor`、`security-filter-bar.tsx`、`security/lib/alert-form.ts` | 管理端已有 `view=unresolved`，会移除日期、禁用状态筛选。当前中文入口称“未结案告警（全部时间）”，而 resolved 已显示“已确认”；主列表改为“未处理 / 已处理 / 全部”的统一分组，底层四个状态不改。 |
| `components/ui/sidebar.tsx:SidebarMenuButton/SidebarMenuBadge` | 菜单 tooltip 仅在桌面收起状态启用，按钮有 overflow-hidden，通用 SidebarMenuBadge 在收起时隐藏。复用组件时需明确补充展开时的提示及图标侧数字，不能直接依赖现有默认行为。 |
| `service/auditsecurity/assignment.go:filterAlertAssignee` | 负责人筛选包括手动分配，与员工客户权限不同；不能用 `assignee_id=当前员工` 替代客户授权。计数与新待办列表继续复用实际客户身份核验。 |
| `security/alert-confirm-button.tsx`、`security/security-alert-columns.tsx`、`customer-audit-dialog.tsx:riskColumns` | 可操作行的“处理状态”单元格直接渲染确认按钮，open 和 investigating 都只显示“确认”；只读管理员却能看到状态文本。需要所有权限下一致显示状态，确认动作放到单独操作列。 |
| `service/auditsecurity/investigation.go:UpdateAlertInvestigation` | resolved/false_positive 设置 closed_at，open/investigating 清除 closed_at 及归档状态；已存在业务上的未完成/已完成分组，不增加新状态或已读标记。自动处置结果独立存储，不能拿处置成功代替人工处理状态。 |
| `service/auditsecurity/alert_query.go`、`summary.go`、`export.go` | 管理查询支持单一 status，却没有已确认与误报合并的列表视图；导出复用 ListAlerts，summary 另建查询。新增已处理视图需要同步验证/筛选这些路径，不能只改前端选项。 |

已阅读 `controller/employee_customer_audit_test.go`、`service/auditsecurity/customer_access_test.go` 的权限、跨客户批次、分页及历史身份案例，以及 `query_plan_test.go`、前端客户确认与撤权测试。相关设计背景为 [客户审计权限](employee-customer-audit-access.md)、[客户默认汇总](employee-customer-audit-default-list.md)、[个人安全及默认时间范围](audit-self-security.md)。

实施前确认的能力缺口是**两种范围的轻量未确认计数 API、员工风险的全部时间未处理列表、两种范围合并已确认/误报的已处理筛选，以及明确导航和行状态**。本方案先补齐后端再接入 UI，不在前端过滤当前页来模拟完整待办。用户已明确要求同时覆盖员工与管理员，并区分已处理/未处理；随后明确要求“开始实现”，上述范围优先级、直达行为与 `99+` 展示规则已落实。

## Data Flow and API Contracts

侧栏业务 Hook → 根据现有角色和查看权限选择一个范围 → 现有认证 HTTP 客户端 → 对应路由组及会话检查 → 独立审计读取池 → 服务端可见范围内的未确认风险 → 仅返回数量 → Query 缓存 → 现有导航徽章。

员工分支额外执行实时员工资格、客户归属、风险身份核验和返回前复核；管理员分支通过既有安全监控权限中间件及 `SecurityVisibleQuery` 限定范围。

新增 `GET /api/user/employee/customers/security/pending-count`：

- 放入现有 `employeeCustomerRoute`，复用 `DisableCache()`、`UserAuth()`；加入 `accessTokenSessionRule`，控制器复用 `employeeAuditContext` 再次检查会话。
- 员工 ID 仅来自认证上下文，不接受员工 ID、客户 ID、日期、状态或游标参数。拒绝非空查询参数，避免被误认为支持筛选。
- 成功响应为 `{"success":true,"data":{"count":7}}`。`count` 为 0–100 的整数，100 明确表示“至少 100 条”，供前端显示 `99+`，不是精确总量。
- 普通用户、停用员工、PAT、模型 API Key 不获得该数据；认证和拒绝响应沿用现有中间件/控制器约定。
- 不新增确认状态存储或已读标记；详情和确认 API 保持原样，风险列表新增下述可选视图。

新增 `GET /api/audit/security/pending-count`：

- 放入现有 `securityAudit` 路由组，继承 `DisableCache()`、`AdminAuth()`、`AuditRead`、`AuditSecurityRead`；加入 `accessTokenSessionRule`，控制器明确检查 dashboard 会话，复用 `securityQueryContext`。
- 同样不接受查询参数；角色仅来自认证上下文。返回相同的 `{"success":true,"data":{"count":7}}`，上限 100。
- 这是查看接口，不要求 `AuditSecurityManage`。PAT/API Key 不获得此会话提示接口的数据；计数不会给没有查看权限的管理员泄露风险是否存在。
- 两个 endpoint 都只读，前端一次只启用一个，不请求列表、summary 或容量统计来计算数量。

扩展现有员工风险列表（不是增加无界日志查询）：

- `GET /api/user/employee/customers/security/alerts?view=unresolved&cursor=...` 和 `GET /api/user/employee/customers/:id/security/alerts?view=unresolved&cursor=...`。沿用现有 session-only、UserAuth、实时员工资格和客户授权。
- `view` 缺省保持当前全部状态的日期列表。`view=unresolved` 仅用于风险列表，只接受 view、cursor；非空日期、request_id、IP、状态、负责人等筛选及未知参数拒绝。另允许下述 `view=handled` 日期视图，其他 view 值拒绝；操作日志不接受这两种视图。前端在未处理模式下不发送旧日期字段。
- 未确认模式仅返回当前可见、未归档、未清理且状态为 open/investigating 的记录，与员工徽章同口径；选中单个客户时再收窄客户范围。
- 返回原有 `{"success":true,"data":{"items":[...],"next_cursor":"..."}}`，每页 20 条，保留客户信息投影及原处置结果；不增加总数或后台全量预载。
- 新模式按 `(客户 ID,风险 ID)` 升序使用独立游标，绑定员工、单客户/全部客户、view 和投影版本，长度限制沿用 2048；拒绝旧日期游标、其他身份/客户/视图的游标。按客户连续处理较早创建的待办；刷新回到首批，可看到期间新建或重新打开的风险。
- 管理员未处理落点复用现有 `GET /api/audit/security/alerts?view=unresolved`，不改变其列表/确认权限。

补充“已处理 / 全部”查询合同：

- 两个员工风险列表新增 `view=handled`，保留 start/end/cursor 合同，范围仍最多 31 天；`status IN ('resolved','false_positive')` 在候选分页之前执行，主键加载后再次检查。缺省 view 返回同一日期范围的全部状态，不对当前页做前端过滤或拼接多页。
- 已处理和全部沿用当前 `last_seen` 事件时间及 `(last_seen,id)` 游标顺序；界面将日期明确标为“事件时间”，默认昨天至今天。已处理是历史记录检索，不声称当前日期页包含全部历史。单条确认成功提供原记录详情入口，按既有客户/管理员详情接口直接读取 ID，较早发生的事件不会因刚确认而在默认日期范围内自动出现。
- 为客户/员工日期筛选增加可选 View 字段并绑定游标；序列化空值省略以保留原缺省游标绑定。操作日志拒绝处理视图，个人安全调用继续使用原日期查询且不附加状态分组。
- 管理列表新增 `view=handled`，使用原日期/可见级别/归档边界，并在分页前限定 resolved/false_positive；允许兼容的单状态细分，不兼容状态和未知 view 拒绝。`view=id` 对应全部状态。管理员原“已归档”仍为独立入口，归档不会重新变为未处理。
- 管理导出通过既有 ListAlerts 复用已处理条件；summary 的控制器允许带日期的 handled 视图，并在服务中加入同一状态集合。容量统计仍是原有全局容量，不将其当当前筛选总数。新增视图不改变授权层级。

点击菜单 → 经过校验的路由 search → 权限内选择范围和未确认视图 → 对应列表 API → 原列表/详情确认 → 既有 Query 失效 → 列表和徽章重新读取。页面筛选改变不会收窄侧栏计数。

## Data Model and Index Design

新增三个普通复合索引，无新表、列、默认值或数据回填：

| 表 / 索引 | 列顺序及用途 |
| --- | --- |
| `audit_security_alerts.idx_sec_customer_pending` | `(actor_id,status,id,purge_state,archived_at)`：按已授权客户及单一未确认状态，顺序读取候选 ID。 |
| `audit_security_alerts.idx_sec_target_pending` | `(target_user_id,status,id,purge_state,archived_at)`：可信失败登录目标使用同样读取方式。 |
| `audit_security_alerts.idx_sec_alert_pending` | `(status,visibility,purge_state,archived_at,id)`：按未确认状态读取管理员/Root 可见的至多 100 个 ID；status 前置同时覆盖 Root 无 visibility 过滤的路径。 |
| 复用 `users.idx_users_customer_audit` | `(inviter_id,role,id,deleted_at)`：当前客户集合的批次读取。 |
| 复用 `employee_profiles` 用户唯一键、告警主键及 `idx_sec_finding_alert_id` | 员工资格、候选记录读取、历史身份快照核验。 |

员工候选只选 `id` 和当前身份列，单状态查询 `open` 或 `investigating`，`purge_state` 允许空字符串/NULL，`archived_at IS NULL`。按 `(身份列,id)` 升序游标继续，每批最多 100 条；两个身份通道分别读取并按告警 ID 去重。客户集合仍按 200 个批次顺序处理。

管理员分支复用 `SecurityVisibleQuery`，附加 `status IN ('open','investigating')`、空/NULL 清理标记及未归档条件，仅 `SELECT id LIMIT 100`，以返回行数作为封顶计数；不需要详情、历史身份证明、排序、分页或客户查询。旧 `idx_sec_alert_status` 以 visibility 开头，不能作为 Root 路径的跨数据库性能保证。

员工新增未确认列表复用两个 customer/target pending 索引：客户批次、身份通道及单个状态沿用计数扫描，按 `(身份列,id)` 升序读取并继续跳过未通过身份核验的记录。候选必须满足 `alertAccountID=该身份列的客户 ID`；两路按实际客户 ID 和风险 ID 合并去重，不能把目标字段指向另一客户的记录排到错误位置。每路取至多 21 条可见记录，客户批次按 ID 顺序推进，合并得到 21 条即可停止；下一页从上页最后一条的客户继续，而不是跳过该客户。单客户模式只读取已授权的这一个客户。该列表不要求 last_seen 或全局风险 ID 排序，不额外添加三个索引之外的列表索引。

这里查询的是当前活动待办，不能套用最近两天/31 天限制而漏掉旧待办。历史日志、命中、统计表仍保持既有时间窗查询；本接口不查询 `audit_logs`，也不使用全表 COUNT/GROUP BY。新增状态索引把已确认历史记录排除在候选扫描范围外。

已处理历史查询沿用现有客户 actor/target 时间索引和管理端 visibility/time 索引限定日期范围，再筛选关闭状态；不将已关闭历史纳入无日期待办扫描。本次不因状态分组新增表、字段或其他索引；实施时补验实际 GORM 的状态筛选、候选过滤量、排序和续页 EXPLAIN，既有待办 SQL 探针不代替该验收。

通过 GORM 索引标签和现有 `MigrateAuditSecurity` 管理索引，保留原日期索引服务列表查询。新增索引会增加安全后台告警写入/状态变更的索引维护成本，实施验收须覆盖新库、上一版升级、再次启动无 DDL；生产建索引须在目标 schema 副本演练后按发布流程安排。

### Design-time query plan evidence

2026-11-23 在本机现有测试容器的**连接级临时表**中使用 100,000 条合成记录，覆盖 2,000 个客户、已关闭历史和两种未确认状态；一次筛选 200 个客户，检查两个身份通道及首批/续批。

- MySQL `26.10.0`：8 种新增索引查询均选择对应的覆盖索引范围读，保留身份及状态条件；原索引方案需要读取对应客户的历史记录并排序。
- PostgreSQL `18.7`：8 种查询均选择对应新增索引；首批 actor 查询可使用 Index Only Scan，其他情况可使用 Bitmap Index Scan 后对小结果排序。以 open 的 actor 首批为例，旧方案候选索引读 6,667 行、过滤 6,536 行，新方案返回 100 行、过滤 1 行。
- 两种数据库每种查询在索引变更前后的返回数量一致。连接已关闭，临时表随连接释放；未修改项目业务表。
- 证据在本地忽略目录 `web/_local/employee-audit-badge-design/plan.json`。首次汇总脚本未兼容 MySQL 新版 JSON EXPLAIN 格式，SQL 探针和报告已完成，随后重新解析报告验证了全部 16 个新增索引计划。

这是设计阶段的候选 SQL 检查，不代表 GORM 实现、迁移、全应用启动或生产容量已验收；实现后的结果见文末。已查阅本地 `docs/production-environment.md` 和 [发布清单](../release-checklist.md)：环境记录为 MySQL 9.7.4 主库及独立 PostgreSQL 18.7 日志库。本机测试 MySQL 为 26.10.0，不能替代目标服务器版本与 schema 的部署前演练，本次不部署。

追加管理员设计 SQL 探针：在同版本 MySQL/PostgreSQL 的连接级临时表中使用 100,000 条混合可见级别、状态、归档和清理标记的记录，对普通管理员、Root 分别检查原索引与新增索引。封顶数量均符合合成数据的预期，临时连接已关闭。

- MySQL 原 Root 查询使用表扫描，新增 `idx_sec_alert_pending` 后两种角色均使用其索引范围读。
- PostgreSQL Root 使用新增 Index Only Scan；普通管理员优化器选择原 `idx_sec_alert_status` 的 Bitmap Index Scan，Index Cond 同时包含 visibility 和 status，符合限定范围读取要求。
- 初次探针将“必须选新索引”作为预期，PostgreSQL 普通管理员分支因此断言失败；完整复查确认其选择了等价的现有索引，而非无界扫描。保留此事实，实际验收要求核对过滤边界、访问量和结果，不用固定索引名称替代这些合同。
- 完整计划和初次失败说明位于 `web/_local/employee-audit-badge-design/admin-plan.json`；不据此声明实现或迁移通过。

追加员工待办列表 SQL 探针：同版本 MySQL/PostgreSQL 的连接级临时表各 100,000 条记录，覆盖单客户/200 客户、两种身份列、两种未确认状态、首批/续批，每个数据库 16 个案例。

- 首次尝试全局风险 ID 倒序，32 个返回结果虽然正确，但两种数据库都存在为了排序而反向扫描主键的路径。PostgreSQL 一例返回 17 条却过滤 99,983 条；“用了索引”不足以证明没有大范围历史扫描。报告保留在 `web/_local/employee-audit-badge-design/ui-pending-list-plan.json`，不采用此查询。
- 修订为 `(客户 ID,风险 ID)` 升序后，32 个案例返回及续页顺序正确，全部选用对应客户/状态 pending 索引，未使用主键代替范围筛选。完整计划位于 `web/_local/employee-audit-badge-design/ui-pending-list-index-order-plan.json`。两次探针均只使用连接级临时表，连接已关闭。
- 这些探针尚未覆盖完整身份过滤及端到端延迟。完整 GORM 验收仍需覆盖“大量客户但 0 条待办、只有少数可见待办、历史身份被大量过滤”的场景。100 是返回数量上限，不是数据库访问量上限，不能以 3 秒超时替代查询成本验收。

追加已处理历史筛选探针：同版本 MySQL/PostgreSQL 各使用 100,000 条连接级临时数据及现有时间索引，分别检查客户 actor/target、管理员/Root、2 天/31 天、首批/续批，共 32 个查询，返回结果和排序均与预期一致。优化器使用时间索引或主键分页；PostgreSQL 样本最多过滤 4,268 行，不能据此推断生产规模的成本。报告为 `web/_local/employee-audit-badge-design/handled-history-plan.json`，连接已关闭，项目表未修改。实际 GORM、稀疏数据、summary/导出和权限投影仍待实施验收。

## Key Business Logic and Edge Cases

以下为员工分支：

1. 每次请求实时校验员工资格，然后按当前归属获取客户 ID 批次。所有查询携带同一个 3 秒 context。
2. 分别扫描两种状态和两种身份通道的候选；按主键批量读取权限证明所需的明确字段，再调用现有 `customerAlertAccessible`。计数不加载证据正文和处置记录；末尾复用的客户归属复核方法会读取客户名称，但计数响应只返回整数。
3. 候选加载后再次检查仍是未确认、未归档且未清理，并确认 `alertAccountID` 属于当前已授权客户批次。隐藏/旧版本不可信记录跳过后继续扫描，不能把候选条数直接当可见数量。
4. 最多收集 100 个不同的可见告警 ID 及其客户 ID；达到 100 即停止，返回 100。未达到时必须遍历完全部相关客户和候选，才可返回准确的 0–99。
5. 返回前再次检查员工资格。将已计入的客户 ID 去重，以最多 21 个一组复用 `EmployeeAuditCustomerIdentities` 复核；发现转移、删除、提权或成为有效员工时返回原范围变化错误，不返回部分数量。零记录也复核员工资格。
6. 查询与确认、客户变更允许正常在途竞争；不锁定客户或告警，不承诺一个跨请求的强一致快照。下次轮询收敛，确认成功立即触发重读。
7. 自动处置成功不等于人工已确认；只按真实 `status` 判断。重新打开的告警重新计入。
8. 截止时间、数据库错误、身份证明读取失败都返回失败，不能把部分扫描结果伪装成准确数字或 0。

管理员分支在已验证的查看权限内，执行一次上述限定状态及可见级别的 ID 查询。风险规则、原始操作者或当前负责人不产生额外筛选，因此与管理员“安全监控 → 未处理（全部时间）”保持同一未筛选范围。普通管理员不可见的 Root 风险不计入；Root 可以计入员工客户投影拒绝展示的管理/历史风险。并发确认、重新打开、归档及清理按当前数据库状态和下一次刷新收敛。

员工未确认列表与计数共用未确认状态、清理/归档和客户身份的判定，不复用负责人筛选。每批候选按主键读出包含 archived_at 的明确字段，再复核仍符合视图；不足 21 条时继续跳过隐藏候选。汇总结束复核员工及最后至多 21 条的客户归属，再截取 20 条并加载现有处置结果。单客户模式也在返回前复核归属，空列表仍复核员工资格。任一查询失败不返回部分列表。

现有日期列表、操作日志以及调用 `queryAccountAlertRecords` 的个人安全入口保留原验证和排序；新增待办查询与游标放在独立的风险业务分支，避免把本功能的无日期语义扩散到其他记录接口。

### 处理状态的唯一口径

| 界面分组 | 后端状态 | 每行状态显示 | 计入徽章 | 确认操作 |
| --- | --- | --- | --- | --- |
| 未处理 | open | 未处理 | 是 | 有处理权限时显示确认按钮 |
| 未处理 | investigating | 处理中 | 是，说明尚未确认完成 | 有处理权限时显示确认按钮 |
| 已处理 | resolved | 已处理 · 已确认 | 否 | 不再显示确认按钮；详情保留原权限下的状态编辑 |
| 已处理 | false_positive | 已处理 · 误报 | 否 | 不再显示确认按钮；详情保留原权限下的状态编辑 |
| 全部 | 以上四种状态 | 使用对应标签 | 徽章仍只统计未处理 | 按每行实际状态和权限决定 |

分组只由 status 决定。成功禁用账号、撤销会话等处置结果仍显示在原“处置结果”列，不使 open/investigating 自动进入已处理。确认失败不迁移分组、不减少数字；重新打开后回到未处理并重新计数。归档、清理中及不可见记录仍遵循各入口已有过滤和授权，不把不存在的权限范围解释成已处理。

## Frontend Lifecycle and Interaction

### 用户路径与页面指引

| 场景 | 进入后的可见状态 |
| --- | --- |
| 员工点击有徽章的“审计日志” | “我的客户”已选中 → “风险记录”已选中 → “全部客户” → “未处理”，说明为“全部时间，含处理中”，从第一页展示待办。 |
| 管理员点击有徽章的“审计日志” | “安全监控”已选中 → “未处理”，说明为“全部时间，含处理中”，清空负责人、规则、状态及其他附加筛选，从第一页展示当前有权查看的待办。 |
| 管理员兼任员工 | 徽章、菜单说明和落点统一使用管理范围；提示不称为“客户总数”或“全站全部”。客户页仍可主动查看其授权客户。 |
| 0 条或计数尚不可用 | 不显示数字，保留原审计页入口；已经进入待办页的用户不会因数字变为 0 而被自动跳走。 |

- 整行菜单是一个链接，数字不另设按钮，不要求精确点击小徽章。只打开页面、切换页签或查看详情均不会自动确认记录。
- 桌面展开/收起时，悬停或键盘聚焦提示“客户未处理风险记录：N”或“未处理安全告警：N”；`99+` 的说明为“至少 100”。链接的可访问名称包含相同范围和数量。移动端点击后由已选页签、视图名称和一行说明给出相同含义，不依赖 hover。
- 展开/移动端复用行内 NavBadge；桌面收起时复用同一 Badge 外观，在 SidebarMenuItem 内作为按钮的兄弟元素定位，避免被按钮 overflow-hidden 截断。数字不截成圆点，不挡住整个图标，pointer-events-none、aria-hidden；实际点击仍落在链接上。验收必须查看 1、99、99+ 和键盘焦点的布局。
- 员工风险筛选复用当前 LogsFilterToolbar/Combobox，提供“未处理 / 已处理 / 全部”；管理员复用已有 view 筛选，同样明确这三个选项，原最近命中、最近更新、归档入口保留。未处理隐藏日期输入，说明“显示全部时间的未处理记录，包含处理中的记录。”；已处理和全部恢复日期选择器，一行说明明确按事件时间筛选。
- 每行“处理状态”始终显示上述文字标签，复用 StatusBadge 的主题变体：未处理 warning、处理中 info、两种已处理状态 neutral；不只依赖颜色。确认按钮移动到独立“操作”列，已处理行不显示确认动作。管理员状态列放在命中规则后，初始视口即可看到；仅操作列固定右侧，避免两个固定列重叠。员工、可处理管理员及只读管理员都看到同一套状态标签；详情仍复用原状态编辑组件。
- 员工未确认列表按客户连续排列，同一客户较早创建的记录优先；“最近事件时间”是每条记录的时间字段，表头不显示排序箭头。现有按日期风险列表仍保留最近事件顺序。
- 侧栏数字始终统计全部可见未处理风险。查看全部范围复用侧栏徽章入口；员工也可清除客户选择，管理员可使用“重置”清除附加筛选。重置保持当前处理分组，切换到已处理/全部时清空旧游标并恢复日期筛选，切回未处理时移除日期参数。没有另加重复的“查看全部”按钮。
- 页脚继续显示“本页条数”，不把 99+ 或当前页长度称作总条数；不足 100 的徽章数字也不是当前页条数。界面不用展示轮询周期、SQL、游标或权限代码。
- 确认过程中按钮显示现有进行中文案并禁止重复提交；成功反馈“已处理”，重新读取后该记录移出未处理、归入已处理，数字随真实结果变化。在全部列表中保留该行并更新状态。单条成功提示提供“查看记录”，复用原详情入口，避免旧事件因历史日期筛选而被误认为删除。保留详情窗口中的已确认反馈及返回列表操作；管理员批量部分失败时仍保留失败原因及未处理记录。
- 未处理模式下发生确认/状态变更后回到第一页再读，避免最后一页被清空时误显“全部已处理”。仅第一页、无附加筛选、列表成功且为空时显示“暂无未处理风险记录”；有附加筛选时显示“未找到符合条件的未处理风险记录”，清除筛选使用上述现有入口。请求失败显示加载失败及重试，不展示完成空态。不会因失败、权限丢失或进入页面而将计数写成 0。
- 有查看、无处理权限的管理员可查看提醒和详情；操作区提示“你可以查看风险记录；确认操作需要处理权限。”，不展示可用的确认/批量确认按钮，也不暗示数字只能由当前用户清零。
- 已处理成功空页显示“所选事件时间内没有已处理记录”，全部成功空页显示“所选事件时间内没有风险记录”；与“当前没有未处理风险”、筛选无结果和加载失败明确区分。不为已处理/全部新增伪总数，页脚仍为本页条数。

### 路由与状态同步

- 在审计路由使用项目既有 `validateSearch` 模式校验可选 `scope`、`tab`、`view`。有徽章时的明确链接分别为 `/usage-logs/audit?scope=customers&tab=risk&view=unresolved` 与 `/usage-logs/audit?scope=security&view=unresolved`。
- 三个处理分组分别对应 view=unresolved、view=handled、缺省/id；切换时同步 URL 和 Query key。日期列表中的有效事件时间可随现有筛选状态保留，跨分组不得复用旧游标；刷新及后退恢复正确分组。
- URL 仅控制展示状态，不授予权限；范围不可用时沿用已有安全回退和权限错误处理，不保留旧数字、旧客户数据或可操作按钮。缺省 search 保持既有入口行为，未知值规范化为允许的默认值。
- scope/tab/view 与 URL 同步，切换离开未确认视图时同步清除/更新 search；浏览器刷新、后退/前进要恢复对应视图。重复点击相同徽章入口也要重置到全部范围、第一页并重新读取，不能只在组件首次 useState 时消费参数。
- 徽章点击生成仅用于视图复位的 `auditPendingVisit` 历史状态标记；采用时间与随机值组合，普通 HTTP 来源无 `crypto.randomUUID` 时仍可使用。页内分组/页签导航用 `state: true` 保留该标记，避免无意重挂载员工视图并清空所选客户。
- 通过 NavLink 的可选 search 传给 TanStack Link，现有 url/菜单配置匹配保留 pathname。进入时清空客户/规则/负责人等附加筛选和旧游标；正常轮询数量变化不能重置用户正在查看的列表，只有明确导航或确认刷新触发对应行为。
- CustomerAuditView 以可选视图/页签状态与回调承接路由，默认值保持现有客户详情弹窗行为。切换客户不把待办意外重置为操作日志；从风险详情跳到关联操作日志时明确切换 tab，并恢复原有有限事件时间窗。

### 请求与刷新

- 在 `hooks/use-audit-pending-badge.ts` 增加轻量业务 Hook/API，独立于表格、详情和监控页面模块。使用已有角色/权限函数先选择管理员范围，再选择员工范围；不具备任何范围时禁用。
- 管理员 Query key 使用 `['audit-security', userID, role, permissions, 'pending-count', sessionID]`；员工使用 `['customer-audit', userID, 'pending-count', sessionID, role, is_employee]`。一次只启用所选范围的 Query，传递 `AbortSignal`。账号、会话、角色、查看权限、员工资格或范围变化后不复用旧数量。
- `staleTime=60s`、`refetchInterval=60s`、后台标签页不轮询、窗口聚焦时刷新过期数据；使用 React Query 的共享缓存与去重，不新增全局事件监听或独立计时器。
- 计数和带 AbortSignal 的客户接口设置 `disableDuplicate`，由 Query 管理请求去重，避免 StrictMode 或快速切页时复用已取消的 HTTP promise。只修改独立业务模块，受保护的认证/HTTP 基础文件保持不变。
- `retry=false`、`gcTime=0`、`meta.errorToast=false`、`meta.errorRedirect=false`。首次加载和请求失败隐藏徽章，不显示虚假的 0；403 后停止定时/聚焦重试，身份变化或重新挂载后重新校验。非权限临时错误保留下一次正常轮询机会。`lib/query-client.ts` 仅在查询显式设置 `errorRedirect=false` 时跳过全局 500 跳转，其余查询保持原有行为。
- 员工确认成功、详情确认及状态保存已有的 `['customer-audit', userID]` 失效覆盖员工计数；管理员单条/批量确认、状态保存、归档和删除的 `['audit-security']` 失效覆盖管理计数。批量部分失败也读取实际剩余数量，不在前端直接按尝试条数扣减。其他会话的操作最多延迟一个正常轮询周期体现。
- 管理范围请求 403 后隐藏原数量并停止自动重试，不因为网络或权限错误自动混用客户计数；只有当前资料/权限明确变更或重新挂载后才重新选择范围和校验。
- 数字沿用 `formatNumber` + `toIntlLocale`；为 `BaseNavItem` 增加可选徽章说明，复用现有 Badge/Tooltip。审计项的展开提示通过现有 Tooltip 组合实现，不能误认为 SidebarMenuButton 默认就在展开状态显示 tooltip；其他导航项保留默认行为。
- 待办列表显示期间沿用各自列表的刷新机制，确认组件已有 Query 失效负责刷新；计数和列表不承诺跨请求快照，其他会话并发操作允许短暂差异。主动点击查看全部/刷新时一并重新读取对应计数，避免用户需要等待轮询来对齐。徽章提供 `onNavigate` 回调，经侧栏配置传入菜单点击处理，执行 `refetch({ cancelRefetch: false })`；已有计数请求进行中时复用该请求。
- 前端新增文案按 i18n-translate 工作流补齐 en、zh、zh-TW、fr、ja、ru、vi，检查同步差异范围及 UTF-8 无 BOM。后端复用既有错误文案，无需新错误键。

## Config Parameters

无新增用户配置或环境变量。100 条为显示上限，200 个为现有客户批次，3 秒为现有审计请求截止时间，60 秒为该提示的刷新周期；它们不改变风险检测或处置设置。

## Error Handling

员工控制器复用 `employeeCustomerAuditResponse`；管理员控制器沿用 `GetSecuritySummary` 的 `ApiErrorI18n`/日志处理模式。参数错误、客户范围变化和暂时不可用使用现有 i18n 消息，权限失败沿用 403；底层异常先由 `logger.LogError(c, ...)` 记录，日志及响应均不携带凭据或 SQL 内容。前端后台提示查询不连续弹 Toast，审计页原有错误提示和重试入口继续负责用户主动操作反馈。新增未确认列表的空态、错误和无操作权限状态必须可区分；无法读取数量只表示暂不可用，不代表风险清零。

## Main Chain Impact

新增执行只发生在 dashboard GET 请求中；没有步骤执行在 AI relay goroutine，也没有新增 relay 后台任务。30k/100k RPM 的每个 AI 请求新增 DB/Redis 调用、锁和 goroutine 均为 0。使用现有隔离审计池，不修改认证刷新、计费、配额、路由分发、上游调用或响应流；不需要 relay 执行流开关。

## Shared Resource Audit

| 资源 | 与 relay 的关系及处理 |
| --- | --- |
| `users` | relay 配额读写共享表；员工分支只通过审计池的现有归属索引和主键普通读，无锁、无写入、无新 users 索引。管理员沿用既有认证授权，不额外查询客户。 |
| `employee_profiles` | 员工分支按已有用户唯一键读取，不访问或修改提成/员工内存缓存；管理员计数不访问此表。 |
| `audit_security_alerts`、`audit_security_findings` | 与安全后台检测/调查共享；候选只读，每条记录的快照核验最多 101 行，但总候选数并未因数字封顶而固定。新增索引的 DDL 与后台写入成本须演练，不在繁忙运行期间临时创建索引。 |
| 数据库连接池 | 只复用最多 2 个连接的审计 main 读取池；串行批次、3 秒截止，包含等待连接时间；不增加池、不占 relay 或员工快照专用池。 |
| Redis / 内存 / worker / 文件 | 无 Redis 键、后端跨请求缓存、后台 goroutine 或业务文件；客户每批最多 200 个，候选每次最多 100 个。两种状态 × 两个身份通道合并时最多临时保留 400 条可见记录，再去重截取 100 条；列表相应为 84/21 条。浏览器 Query 缓存仅属当前登录范围。 |

## Concurrency Analysis

这是低频 dashboard 查询，不承接 AI 请求 RPM。管理员分支在原有认证授权之外只有 1 次告警索引读取，返回至多 100 个 ID。

员工分支设实际读取客户批次数为 B、候选页数为 P、需要历史证明的候选数为 L、最终客户去重数为 C（最多 100）：约 1 次初始员工资格检查、B 次客户批次读取、P 次候选读取、至多 P 次主键记录读取、L 次最多 101 行的历史快照读取；最终再校验员工并按最多 21 个客户分组复核（每组现有方法包含员工检查及客户查询）。客户耗尽可能多 1 次空批次读取。

员工分支的两个状态和两个身份通道顺序执行，不能并发放大数据库负载。达到 100 个可见告警即结束；结果不足 100 时不人为截断为成功。隐藏记录密集或客户极多时可能触及截止时间，返回暂时不可用而不扩大扫描时间。M 个启用徽章的可见员工/管理员会话正常稳态定时轮询约 `M/60` 次请求/秒，用户操作及聚焦失效刷新另计；混合身份仍只发一个请求，后台标签暂停；无 Redis 调用、分布式锁或新增 worker。

新增员工待办列表只在打开该列表或主动操作时读取，复用至多 21 条可见候选合并及原有处置结果加载；达到 21 条即可停止后续客户批次，稀疏或无待办时仍可能读取全部剩余客户。它与计数竞争同一个最多 2 连接的审计读取池，而非彼此隔离。索引方案和后台暂停可以减少开销，不能据此声称任何规模都只耗一次查询；需在实施验收中记录稀疏待办、隐藏候选和多会话下的读取量、延迟及超时率，发现持续超时应调整读取方案，不能通过扩大连接池/超时或返回部分数字掩盖。

## Concrete Changes and Verification

实施文件：

1. `service/auditsecurity/pending_alerts.go`：管理员/员工计数、员工未确认列表及独立游标，复用现有身份核验和投影。控制器分派无日期待办分支，`employee_customer_list.go` / `customer_access.go` 透传已处理视图；个人安全及日志保留日期语义。
2. `model/audit_security.go`：三个复合索引标签，沿用现有迁移入口。
3. `controller/employee_customer_audit.go`、`controller/audit_security.go`、`router/api-router.go`、`middleware/access_token_routes.go`：两个计数 endpoint、session-only 声明、风险 view 分支和既有错误路径。`model/employee_customer_audit.go` / `employee_audit_list.go` 的筛选/游标增加风险 View 绑定；`alert_query.go` / `summary.go` 同步已处理状态集合，导出继续复用列表。
4. `web/src/hooks/use-audit-pending-badge.ts`：轻量计数 API/Hook、范围选择；`hooks/use-sidebar-view.ts`、`hooks/use-sidebar-data.ts`、`components/layout/types.ts`、`components/layout/components/nav-group.tsx`：徽章、辅助说明和 search 接线；`lib/audit-navigation.ts`：路由类型和校验。
5. `routes/_authenticated/usage-logs/audit.tsx`、`features/usage-logs/audit/index.tsx`、`components/employee-customer-audit.tsx`、`features/customer-console/customer-audit-dialog.tsx` / `audit-api.ts`：可校验路由状态、员工待办视图、模式对应请求/空态及确认后的分页复位。
6. `security/monitor.tsx`、`security-filter-bar.tsx`：直达未处理视图、三个明确分组、清除附加筛选及只读状态；`security-alert-columns.tsx` 与客户风险列分离状态标签和操作；既有确认/批量确认组件必要的成功回调与单条记录查看入口；七语言词条。

验收关注点（下方另列实际执行结果与范围，以下清单不表示每一种组合都执行过浏览器测试）：

- 后端：0/1/20/21/99/100/101；跨 200 个客户；open/investigating 计入而已确认/误报/清理中/归档不计；旧日期仍计入；命中次数不影响数量；actor/可信 target 不漏不重；完整/缺失/混合历史身份；多批隐藏记录；账号停用、客户转移/提权/删除、跨员工、伪造参数、PAT/API Key、超时不返回部分成功。
- 生命周期：确认后减少、全部处理后 0、重新打开后增加；返回前归属变化拒绝；员工本人和其他员工的客户不进入计数。
- 管理员权限：普通管理员仅计入自身可见级别、Root 覆盖全局；缺少任一查看权限拒绝，有 read 无 manage 仍可查看；不因负责人筛选遗漏可见记录；无会话/PAT/API Key 拒绝。管理员兼任员工只选一个范围、不重复相加；查看权限撤销后清除原数字。
- 管理员生命周期：单条与批量确认、批量部分失败、状态重新打开、归档和清理后正确刷新；全部已确认、99/100 边界及远早于默认日期的待办。
- 入口与范围：从任意页点击员工/管理员徽章，首次即为正确范围的未确认列表，包含 31 天以前待办；不残留旧客户/负责人/规则/日期/游标。兼任身份的提示和落点与其计数一致，进入页面不确认记录。
- 员工新列表：全部/单客户视图与计数同一可见集合；跨 200 客户按 `(客户 ID,风险 ID)` 分页不漏不重，单个客户跨页不被跳过，隐藏候选不占页，21 条边界；非法/跨范围/日期游标拒绝；view 缺省和日志/个人安全入口的时间校验不变。
- UI 状态：展开、收起、移动端均可见 1/99/99+；悬停、键盘和触屏路径可理解；刷新/后退/前进/同页再次点击能恢复正确视图；确认末页最后一条后回第一页，错误/权限丢失不显示已完成；筛选后空列表不声称全部清零；只读管理员能看到处理权限说明。
- 处理分组：open/investigating 只在未处理及全部出现，resolved/false_positive 只在已处理及全部出现；处理中明确显示且继续计数，处置成功但未确认仍属未处理。分组在数据库分页前生效，员工/管理员列表、summary 和导出筛选一致，切换分组后的旧游标拒绝或按现有无状态分页约定清空。
- 确认反馈：未处理移出、已处理按事件日期可查、全部保留更新后的状态；单条旧日期事件可由成功提示直接查看详情。已处理行无确认按钮；失败不迁移，重新打开重新计入；仅查看者与可操作者状态文案一致。
- 前端：非员工不请求；0 隐藏、99 和 99+；60 秒轮询、确认及详情确认立即刷新；不受当前分页/日期影响；后台暂停/聚焦刷新；账号与权限切换、迟到响应和 403 不展示旧数量；加载/网络失败不伪造数字；桌面/移动、展开/收起、深浅色及语言切换。
- 数据库：实际 MySQL、PostgreSQL 上执行 GORM 查询与新旧 schema 迁移/再次启动，捕获实际 SQL 的 EXPLAIN；SQLite 覆盖跨库查询与索引兼容。生产主/日志双库以及目标 schema 演练按发布清单执行，未执行部分明确记录。
- 运行受影响 Go 包测试、前端 Vitest、`bun run typecheck`、`bun run lint`、构建及 `git diff --check`；不修改受保护的五个认证/HTTP 基础模块。

### 实际验证结果（2026-11-23）

- SQLite：`go test ./service/auditsecurity ./controller -run 'Test(Pending|HandledCustomer|CustomerAudit|EmployeeAudit|EmployeeCustomerAuditHTTPBoundary|SecurityAlertQueryBoundaries)' -count=1` 通过。新增用例覆盖 0/1/99/100/101、跨 200 客户和单客户续页、隐藏候选、状态与可见范围、处理中计入、确认移出、游标跨范围拒绝、读取中客户转移/员工资格撤销/取消不返回部分成功、已处理在分页前筛选。
- 路由与认证：`go test ./router ./middleware -run 'Test.*(AccessToken|Audit|Session|Permission)' -count=1` 通过，包含 session-only 路由注册约束。
- 真实 MySQL 26.10.0（本机 57732）与 PostgreSQL 18.7（本机 57733）：各自在独立临时数据库运行 `go test ./service/auditsecurity -run 'Test(PendingAlert|PendingCustomer|HandledCustomer|CustomerAudit|CustomerAlert|EmployeeAudit|EmployeeCustomer)' -count=1 -timeout=10m`，全部通过，最后一轮分别约 66.7 秒和 40.5 秒。测试清理仅删除本轮自建库，未改业务数据；执行器与脱敏结果在 `web/_local/employee-audit-badge-design/run-implementation-db-tests.py` 和 `implementation-db-tests.json`。
- 迁移：两种真实数据库覆盖新库创建、移除本次三个索引以模拟此前 schema 后升级、再次运行实际迁移函数无 DDL；未执行服务器完整应用启动或生产 schema 副本演练。模型不新增字段或默认值，独立日志库不经过本次新增查询。
- 查询计划：实际 GORM 查询在 20,000 条历史/待办混合记录上执行 EXPLAIN，覆盖管理员/Root 计数、员工计数与两页待办；MySQL 使用对应范围索引，PostgreSQL 没有 Seq Scan 且索引条件包含 status。既有客户聚合计划测试也通过；该测试允许优化器选择同等限定 actor/target 的新索引，仍断言访问类型和估算行数上限。大规模稀疏客户、多并发会话的延迟和目标服务器容量未作压测。
- 前端：`bun run test src/hooks/__tests__/audit-pending-badge.test.tsx src/hooks/__tests__/sidebar-view.test.tsx src/features/customer-console/customer-audit-dialog.test.tsx src/features/usage-logs/audit/__tests__/employee-customer-audit.test.tsx src/features/usage-logs/audit/security/monitor.test.tsx`，5 文件 92 项通过。包含 60 秒轮询、后台暂停/聚焦、共享查询、权限和会话变化、403 停止、真实 HTTP 包装器在 StrictMode 取消后重挂载、分组和确认后的分页/直接查看记录。
- `bun run build:check`（TypeScript 检查与生产构建）通过；全量 `bun run lint` 通过，有 7 条原有文件警告；本次变更的 24 个前端源码文件 lint 无警告。七语言各新增 17 个相同 key，现有 key/value 不变，UTF-8 无 BOM。
- `git diff --check` 通过；五个受保护的认证/HTTP 模块相对当前分支 HEAD 均无修改。本机 `main` 仍是 `web/default/` 的旧目录结构，不存在约定的 `main:web/src/lib/auth-session.ts` 等路径，因此无法据此声称五文件与 `main` 字节一致；本次没有同步或改写这些基础模块。
- 浏览器：本机 Chrome/Playwright 隔离上下文，使用明确的测试 API fixture，不连接生产。员工及 Root 管理员均验证桌面展开/收起、390px 手机菜单、深浅色、2/99+ 徽章、40 天前未处理记录、三种分组、已处理无确认按钮、浏览器前进/后退/刷新和重复点击徽章清除客户筛选；无页面错误或横向溢出。截图及结果在 `web/_local/employee-audit-badge-design/`，计数 0/1/99/100 和只读管理员由自动化组件/Hook 测试覆盖。
- 本次不部署。生产建索引前仍需按发布清单在目标服务器版本和 schema 副本演练；没有以本地测试替代该步骤。

### 实现后仔细复查（2026-11-23）

复查发现并修复四个可复现问题，均先增加回归用例确认失败，再修复并验证：

1. 从徽章进入后切换处理分组，路由默认丢弃历史状态标记，造成员工视图重挂载、所选客户意外清空。页内导航保留历史状态；仅再次点击徽章才清除客户筛选。新增实际 Router、侧栏配置、菜单及页面组合测试，浏览器也覆盖选客户后往返已处理/未处理。
2. 菜单渲染期间调用 `crypto.randomUUID`，在普通 HTTP 等不提供该 API 的环境中导致页面错误。视图标记改用不依赖安全上下文的生成方式；回归用例移除该 API，验证进入和重复点击仍正确复位。
3. 徽章查询即使禁用 Toast，HTTP 500 仍触发 QueryCache 的全站错误页跳转。新增明确的可选 `errorRedirect` 标志，仅徽章禁用跳转；实际应用 QueryClient 测试覆盖错误时隐藏、下一分钟恢复，并确认普通查询的 500 跳转保持原样。
4. 点击徽章原先只刷新列表，没有重新读取尚未过期的计数。现经业务回调刷新计数；实际导航测试覆盖点击后数字由 2 变 1、再次点击变 0 后隐藏，正常轮询仍为 60 秒。

复查后的最终验证：

- 前述 5 个前端测试文件加 `src/lib/__tests__/server-error-notifications.test.ts`，共 6 文件 **117 项通过**。新增回归测试在初次类型检查中发现两个不受支持的测试查询选项，移除后 `bun run build:check` 的类型检查及生产构建通过。
- 全量 `bun run lint` 通过，仍只有上述 7 条原有警告；本次 25 个前端源码文件单独 lint 无警告。
- 再次运行员工/管理员浏览器检查：展开、收起、手机、深浅色、处理分组、历史导航、重复徽章入口及保留客户均通过，无页面错误和横向溢出。使用本地测试 API fixture，未连接生产。
- 七语言仍各新增相同的 17 个键，既有翻译值不变且无 BOM；受保护的认证/HTTP 文件无改动，`git diff --check` 通过。
- 此轮修复仅涉及前端交互和查询错误处理，后端查询、权限、索引及此前真实数据库测试代码没有变化；沿用前述已完成的数据库验证及其限制。未部署。

# 员工客户风险记录漏显修复

日期：2026-11-24。状态：**用户确认后已实施，三库回归及实际浏览器验收通过；未部署到生产。**

后续补漏：用户再次要求修复密码恢复、批量敏感操作及负责人 NULL 筛选，见 [告警自动归属补漏](audit-assignment-completion.md)。本页的验证记录属于第一阶段；新增范围的验证单独记录于该文档。

## Goals and Scope

用户提供同一客户的两张截图：超级管理员的安全监控中，客户 `test #3` 在 01:36:34 的事件触发“高危操作 #48”；员工能看到同一时间的密码变更操作。用户随后明确：**员工的“风险记录”页签也找不到该条告警**。

本次修复客户本人触发的 `sensitive_operation` 在所属员工风险入口漏显的问题，覆盖单客户和全部客户列表、未处理列表及角标、风险详情、确认处理和管理员看到的当前客户负责人。继续使用同一告警 ID、风险等级和处理状态。

修改员工风险授权和新告警的角色隔离，不改变密码修改流程、风险判定等级或操作日志的“成功/失败”含义。前端现有风险表格已具备展示能力，无需新增风险列、API 或文案。批量告警由后续补漏覆盖。

## Existing Logic and Related-Path Findings

下表记录修复前的实际实现和相关测试合同，用于说明修改依据；修复后的逻辑及验证结果见后文。

| 代码位置 | 已验证行为及本次衔接 |
| --- | --- |
| `controller/user.go:UpdateSelf`、`controller/audit.go:recordUserSecurityAudit` | 密码请求完成后记录 `user.password_change`，使用当前会话用户和角色，并记录真实成功状态；客户自身安全操作没有 `admin_info`。本次不修改该流程。 |
| `service/auditsecurity/rules.go:Normalize/highRisk/Evaluate` | 密码变更归为 `resource_type=user`；成功事件风险等级为 2，生成 `sensitive_operation`。管理员截图的高风险和员工操作日志的成功可以同时成立。 |
| `service/auditsecurity/customer_access.go:customerAccountRuleVersion/accountAlertAccessible` | 员工风险可见性依赖规则白名单。当前没有 `sensitive_operation`，版本得到 0 后直接拒绝，即使 actor 就是当前客户。此处是代码中的漏显原因。 |
| `customer_access.go:ListCustomerAlerts/GetCustomerAlert/UpdateCustomerAlert`、`employee_customer_list.go`、`pending_alerts.go` | 单客户、汇总、未处理数量和列表、详情及更新最终共用同一身份检查；只修改前端或某个列表不能修复完整生命周期。 |
| `finding_snapshot.go:alertRuleVersion`、`store.go:commitSecurityBatch` | `sensitive_operation` 当前版本为 3；只有白名单中的账号规则会在普通合并查询中显式限定 actor ID 和 actor role。不能直接把版本 3 当作已经按角色隔离的新格式开放；新版只覆盖可证明是普通账号自身操作的事件。 |
| `customer_access.go:accountAlertAccessible` | 旧格式已有有界身份证明：1～100 次触发、完整快照数量、最新快照 ID、每条快照同账号/同规则且角色允许；不能仅凭告警头上的 actor 或展示用户名认领历史记录。 |
| `assignment.go:loadAlertAssignees/customerAccountAlertIdentity/filterAlertAssignee` | 当前客户负责人展示采用 Go 身份检查，筛选/统计使用 SQL 身份条件。现有 SQL 只检查 latest_finding_id 对应快照存在，而 Go 要求它是最大快照 ID；此差异也须修正，确保详情、负责人筛选及未分配筛选一致。 |
| `self_access.go`、`account_notify.go:queueAccountNotification` | 本人安全列表及个人安全通知也复用 `accountAlertAccessible(..., selfView=true)`。直接扩大共享白名单会额外开放本人视图并触发个人通知，因此本次显式保持这两个入口原来的规则范围。 |
| `investigation.go:UpdateAlertInvestigation` | 客户风险确认在事务中重新授权，使用原有版本条件更新并追加员工处理历史；不会因此授予指派、清理或管理权限。 |
| `repeat_operation.go:repeatOperationKey` | 管理员额度调整的重复告警键包含规则版本。统一升级该规则会拆开原有重复告警；本方案保留管理员/Root 事件的版本 3 和原键，只将符合普通账号自身操作条件的事件升级为 4。 |
| `store.go:commitSecurityBatch`、`finding_snapshot.go:persistFindingSnapshot` | 事件回执控制是否执行 Evaluate，已经处理的 IP 补全不重新执行独立高危操作判定。告警版本、detection_key 和重复操作键都必须使用同一个事件版本解析结果；快照包含角色、资源和目标，但没有 action 字段。 |
| `login_target.go:accountEventAccessible` | 一般证据目前按账号/角色投影，未校验资源和目标；新开放规则需加自己的证据条件，不能借用可信登录失败目标分支或显示同账号针对其他人的操作。 |
| `setting/operation_setting/audit_auto_ban.go:ParseAutoBanRules` | 自动处置规则不接受 sensitive_operation；本次不扩大该范围。同一事件命中的其他合法规则仍按原逻辑处置。 |
| `model/employee_customer_audit.go`、`model/employee_audit_list.go` | 每次请求重新检查员工启用状态、客户当前 `inviter_id`、普通角色及非有效员工身份，拒绝客户转移、提权或员工撤权后的访问。 |
| `controller/employee_customer_audit.go`、`router/api-router.go`、`middleware/access_token_routes.go` | 员工接口需要 `UserAuth` 和 dashboard 会话，使用独立审计池，单次请求总超时 3 秒。普通风险列表有时间窗与游标；未处理列表使用独立的跨时间查询。 |
| `web/src/features/customer-console/customer-audit-dialog.tsx`、`audit-api.ts`、`web/src/hooks/use-audit-pending-badge.ts` | 风险页签直接显示客户风险 API 的 items，没有额外剔除高危操作；已有规则名称、等级、详情和确认组件。角标最多计 100，前台每 60 秒刷新；确认后的缓存失效限当前 QueryClient，其他浏览器的列表没有实时推送。 |

已核对 `customer_access_test.go` 的历史快照、角色隔离、授权撤销、隐藏行补页和处理历史用例，以及 `assignment_test.go`、`pending_alerts_test.go`、`self_access_test.go` 的相关合同。原负责人测试未覆盖“latest_finding_id 存在但不是最新”的情况。

截图 #48 的数据库原始行及完整快照未现场读取，不能声称已验证线上该行的历史证明。当前代码确定会拒绝此类规则；实施后按同样事件构造回归，并对保留完整快照的既有记录开放读取。

## Data Flow

客户完成密码变更 → 原审计记录及安全后台扫描 → 原规则生成高危 finding → 后台按新版账号/角色边界合并告警、持久化判定快照 → 员工通过现有接口读取 → 实时客户授权 → 规则/资源/历史身份校验 → 客户安全 DTO → 原有风险列表及详情。

历史告警走读取时的完整快照核验，不重放密码修改、不重扫旧日志、不创建副本或改写旧告警身份。现有合格历史记录保留原 ID。

员工确认 → 原 actions API → 事务内再次核验归属及告警权限 → 乐观版本更新和员工动作历史 → 原风险/活动修订号 → 原前端缓存失效。新告警、清理、并发确认和撤权继续沿用现有冲突处理。

## Business Logic and Concrete Changes

### 员工授权与新事件版本

保留 `customerAccountRuleVersion` 的现有共享规则集合。在 `accountAlertAccessible` 增加仅供 `selfView=false` 使用的 sensitive_operation 分支；不能只改 `customerAlertAccessible` 包装函数，因为列表直接调用共享检查。新规则的账号隔离版本统一定义为 4，读写两端共用此值。

`finding_snapshot.go:alertRuleVersion` 改为同时接收事件。只有规则为 sensitive_operation，且事件满足以下全部条件时返回 4：actor ID 为正数，触发角色是普通用户，资源为 user/account，TargetID 为 0 或等于 actor ID。TargetID 在 Normalize 中来自 target_user_id，而非 access token 的 token_id；0 表示该事件没有单独指定目标账号。

管理员/Root、缺少可信身份证明、channel 资源及指向其他账号的事件仍返回 3。后续补漏将客户自身 bulk_sensitive_operations 同样隔离为版本 4，并将服务端证明的普通账户密码恢复结果隔离为版本 5；恢复结果保留匿名操作者 role=0。不读取当前 users 角色来重判历史事件，当前是否属于员工客户仍由请求时的归属检查决定。

同步更新全部版本调用点：`store.go:commitSecurityBatch/findingDetectionKey` 和 `repeat_operation.go:repeatOperationKey`。快照沿用所属告警的版本。现有管理员重复操作键必须保持完全相同，不能因为本次员工展示修复而另起告警或重复通知。

`store.go` 对 sensitive_operation 的版本 4 合并显式追加 actor ID/role 条件；其他规则使用原有合并逻辑。新版写入的历史证据也限定同账号、普通角色、同资源以及 TargetID 为 0/本人。不改变 Evaluate 的风险阈值、扫描游标、事件回执或管理员处置语义。

### 历史证明与读取边界

所有新增授权都要求当前有效员工拥有该普通客户，告警 ActorID 等于客户 ID、资源属于 user/account 且未清理。告警的 TargetUserID/TargetRole 专用于可信登录失败目标，不能用它们为 sensitive_operation 认领客户。

| sensitive_operation 告警 | 员工读取条件 |
| --- | --- |
| 版本 4 | 告警触发角色为普通用户；信任新版写入边界，不要求逐条回读快照，也不施加旧版本的 100 次上限。 |
| 版本 1～3 | 告警头角色为普通用户或旧格式未知值 0；NULL 按现有 Go 读取语义等同 0。另须通过下述完整快照证明。已知管理员/Root 角色直接拒绝。 |
| 版本 5 | 后续补漏专用于可信密码恢复结果，要求 account 资源及匿名操作者 role=0。 |
| 版本 0、负数或大于 5 | 拒绝。不能将未知版本视为可证明的旧版本。 |

旧版本证明必须同时满足：

- occurrences 为 1～100，latest_finding_id 为正数；按 ID 降序最多读取 101 条快照，实际总数恰好等于 occurrences，最大 ID 恰好等于 latest_finding_id。
- 每条快照 actor ID 等于客户、actor role 为普通用户、rule 与告警相同、rule_version 与告警相同、resource_type 与告警相同；TargetID 必须明确为 0 或该客户。允许合并本人不同安全操作，不要求不存在于快照中的 action 字段。
- 身份字段缺失、NULL、混合角色/账号/资源、不同版本或其他目标均拒绝。特别是 target_id 的 NULL 不能扫描成整数 0 后放行：新增规则的快照读取采用可区分 NULL 的投影字段；其他规则原有查询语义不扩大。

完整快照不足或超过 100 次的旧记录继续交由管理员调查，不能从用户名、IP 或保留的最多 20 条 evidence 推断整个告警归属。这是明确的历史兼容边界，不能承诺所有旧高危告警都自动出现在员工端。

### 负责人 SQL 与证据投影

`assignment.go:customerAccountAlertIdentity` 单独组合上述员工敏感操作分支，不通过扩大本人共用白名单实现。新旧版本、角色、资源、目标和快照证明与 Go 授权逐项等价，适用于负责人展示、筛选、未分配、summary/export。

共用的旧快照 SQL 使用 `latest_finding_id = (SELECT MAX(af.id) ... WHERE af.alert_id = audit_security_alerts.id)`，与 Go 最大 ID 判断一致，并让数据库按告警索引定位最新快照。新增资源、版本和目标的反向校验都显式处理 NULL，不能仅写 `<>` 后依赖 NOT EXISTS；新增身份分支在缺字段时须确定为 false，避免未分配筛选的 NOT 遇到 UNKNOWN 后漏行。此修正不改变人工负责人记录，人工指派仍不能授予员工客户访问权。

`login_target.go:accountEventAccessible` 对本次规则优先执行专用条件：员工视图、同客户、普通角色、与告警一致的 user/account 资源、TargetID 为 0/本人；不能落入可信匿名登录目标的例外分支。其余规则按原逻辑处理。继续返回原客户 DTO，最多 20 条证据和 100 条处理历史，隐藏管理员内部备注、身份和凭据。

### 相关入口与刷新

单客户、全部客户、时间窗、已处理、跨时间未处理、数量、详情和 actions 共用上述授权。本人安全视图和个人通知继续被原共享白名单排除；自动封禁规则集合不变。同一事件命中其他规则所产生的合法处置仍照常显示。

员工确认后，当前页面查询按原逻辑失效；管理员在另一浏览器确认后，员工列表需按现有刷新交互重新读取，角标在页面前台按 60 秒周期或满足条件的窗口聚焦刷新。角标 100 表示至少 100 条，不能把 99+ 当作精确计数或跨浏览器实时同步。无需前端新接口、缓存协议或文案。

## API Contracts

沿用现有路由和 `{success,message,data}` 响应格式，不增加请求或响应字段：

| 接口 | 授权与输入/输出 |
| --- | --- |
| GET `/api/user/employee/customers/security/alerts` | dashboard 会话、有效员工；`start,end,cursor` 或 `view=unresolved,cursor`；返回带客户身份的风险摘要及 next_cursor。 |
| GET `/api/user/employee/customers/:id/security/alerts` | 同上且实时客户归属；返回该客户风险摘要和 next_cursor；`view=handled` 继续限制已处理记录。 |
| GET `/api/user/employee/customers/security/pending-count` | dashboard 会话、有效员工；无筛选参数；返回现有有上限的 count。 |
| GET `/api/user/employee/customers/:id/security/alerts/:alert_id` | 实时客户归属及记录身份校验；返回原 alert/events/actions 受限投影。 |
| POST `/api/user/employee/customers/:id/security/alerts/:alert_id/actions` | 同上；原 `{version,status?,conclusion?,note?}`；确认后返回同一告警的最新摘要。 |

每页 20 条、时间窗上限 31 天、游标绑定、未处理跨时间查询、禁止 PAT/API Key 和缓存响应均保持现有合同。管理接口继续要求其原有管理角色及权限，不向员工开放。

## Data Model, Index Design and Configuration

没有新增表、列、索引、配置参数或 schema 迁移。`rule_version=4/5` 是已存在字段的新语义版本；管理员及其他事件保留版本 3。不执行自动批量数据回填，后续补漏提供显式限定范围的历史维护入口。

| 现有访问路径 | 索引及边界 |
| --- | --- |
| 员工与客户归属 | users 主键及 `idx_users_customer_audit`、employee_profiles 的 user_id 索引；按现有客户批次读取。 |
| 客户时间窗风险列表 | `idx_sec_customer_time` / `idx_sec_target_time`，继续时间/ID 游标与隐藏行补页。 |
| 未处理列表/数量 | `idx_sec_customer_pending` / `idx_sec_target_pending`，按客户、状态、ID 有界扫描，沿用原数量上限。 |
| 告警合并 | `idx_sec_episode_v2` 限定规则、subject、可见性、资源、版本和 15 分钟范围，actor/role 是现有查询模式的附加约束。 |
| 旧身份证明 | findings 的 `(alert_id,id)` 索引；Go 侧最多读取 101 行，SQL 按 alert_id 验证总数、最大 ID 及完整身份。SQL COUNT 不具有 101 行读取上限，须单独验证实际计划和耗时。 |
| 员工确认历史 | 告警主键/版本、actions 的 alert_id 及时间/ID 索引。 |

复用已有索引，不为本次增加大表 DDL。已捕获实际负责人筛选 SQL，并在 MySQL/PostgreSQL 的 5,000 条告警及快照样本上验证 EXPLAIN：旧快照的数量、最大 ID 和身份校验均按 alert_id 使用索引，没有全表读取 findings。具体结果见验收记录。

## Error Handling

继续使用 `employeeCustomerAuditResponse`：归属/员工资格失效为 403；不可访问记录走既有 not_found；版本变化或清理竞态走 conflict；数据库及超时错误记录请求上下文，返回本地化的 unavailable，不把失败伪装成空列表。前端沿用重试和撤权后清缓存逻辑。没有新增用户文案。

## Upgrade and Rollback

旧记录通过完整证明后按原 ID 读取，状态、确认历史及快照均不改写。升级后的首个普通账号自身事件可能开始版本 4 的新记录，与 15 分钟窗口内版本 3 的旧记录并存；这是不同事件形成的两个版本记录，不是复制 #48。管理员的普通合并和重复额度告警继续使用版本 3。如果同一账号后续的管理事件按原合并规则加入旧版本记录，使快照出现混合角色，该旧记录会失去员工访问资格；新版普通客户记录保持独立。回归已覆盖此边界。

沿用扫描租约、回执和 detection_key，重试不能重复计数或新增快照；已处理事件的 IP 补全不重新执行独立高危操作判定。升级、混合版本部署和回滚均不得清空回执、回退扫描游标或重放历史；测试覆盖升级前已处理事件与升级后的新事件。

回滚不需要数据库回退：管理员仍保留告警及处理历史，旧程序员工入口会恢复当前不支持 sensitive_operation 的行为。重新升级后再次按上述规则判断。生产 #48 是否满足证明需在实际授权环境核对；如果快照不完整，应明确告知具体原因，不能伪造归属或补造快照。

## Main Chain Impact

AI relay goroutine 不执行本次新增判断。改动发生在 dashboard 风险查询和既有安全后台的告警合并；密码修改、令牌认证、配额结算和响应流不增加工作。后台继续受现有安全监控开关、批次和独立池约束。

在 30k/100k RPM 的 AI 请求负载下，每个 AI 请求新增 DB、Redis、锁和 goroutine 数均为 0。

## Shared Resource Audit

| 资源 | 与 relay 的关系及措施 |
| --- | --- |
| users / employee_profiles | 共享业务表；员工查询沿用独立审计池的普通索引读，风险确认的授权不锁用户额度行，不修改客户/员工状态或缓存。 |
| audit_security_alerts / findings / actions / assignees / 风险与活动修订号 | 安全后台及 dashboard 既有资源；本次保留原有事务/版本写入，没有 relay 新读写；历史证明按告警 ID 索引读取，Go 行数有上限，SQL 完整证明成本另行核验。 |
| 安全后台回执、管理员通知与处置关联表 | 新事件仍走原后台生命周期；个人通知对本次新增员工规则维持拒绝，处置资格不增加。 |
| audit_logs | 原始审计来源不变；修复不扫描或回填全历史日志。 |
| 连接池 | 读取复用审计 main/log 池，各最多 2 个连接；后台复用既有安全池，不借用 relay 池、不新增池。 |
| Redis、全局内存、文件、worker | 无新键、全局缓存、文件句柄、线程或 goroutine；只增加现有请求/后台批次中的规则和资源判断。 |

## Concurrency Analysis

本次是非 relay 管理查询。新版客户风险沿用原列表查询数；旧风险每条仍最多执行一次有界 findings 身份读取。该规则先前立即拒绝，修复后符合候选的旧记录会进入原有快照读取，因此旧告警密集时会增加审计池查询工作，受 3 秒总截止时间控制；不能承诺完全没有查询成本增加。

后台每个命中仍写原告警与快照，角色限制增加查询谓词；无新增独立后台任务。负责人筛选中的相关子查询已实测使用原 alert_id 索引。100k RPM relay 的新增调用仍为 0。

## Validation and Acceptance

已先补回归并确认修复前失败，再实施修复；验收范围如下，以真实存储及服务返回验证：

- `customer_access_test.go`：普通客户成功修改密码，经 Normalize/Evaluate 和实际后台持久化形成告警后，管理员及所属员工读取同一 ID；单客户/汇总/未处理/数量/详情一致；确认后移入已处理并减少未饱和角标，重复旧版本确认失败。跨浏览器按真实刷新行为验收。
- 历史版本 1/2/3 的完整同客户快照保持原 ID 可见；1/100/101 次边界、快照数与 occurrences 不一致、缺失/过时的 latest_finding_id、混合客户/角色/资源/版本、NULL 字段、其他目标、未知版本、清理中都按上述规则处理。新版 4 超过 100 次仍可读取。
- 覆盖管理员修改客户密码、普通角色但指向其他账号、其他员工客户、客户转移/提权/成为有效员工、员工停用等拒绝情形。证据中注入同客户不同资源、不同目标或管理员操作时不显示。
- `assignment_test.go`：新旧合格告警的当前客户负责人、详情、负责人筛选/未分配筛选及 summary/export 一致。对相同数据逐例比较 Go 与 SQL；包含 latest_finding_id 存在但不是最大值、NULL 资源/目标、人工负责人存在与不存在。修正最大 ID 条件后回归原账号规则。
- 现有 `self_access_test.go`、`account_notify_test.go`：本次规则不新增本人访问或个人通知；其他原有账号规则仍可用。保留管理员内部信息和跨账号证据不可见的断言。
- 后台回归：普通账号自身事件为 4，管理员/未知角色/跨目标/channel 事件及 bulk 规则保持 3；写入版本、快照版本和判定键一致。管理员重复额度告警键与升级前相同，已确认的重复记录继续按原策略合并；同事件重试、升级边界、IP 补全不重复产生敏感操作快照或通知。
- 隐藏历史记录穿插可见记录的 0/20/21 条分页、同时间戳游标和隐藏行补页；时间窗与未处理查询、角标 99/100 上限、并发确认/清理及过期版本继续满足既有合同。
- 复用现有隔离数据库 fixture，跑 SQLite、真实 MySQL 和 PostgreSQL；不向共享业务库插入合成数据，不全表清空。记录实际版本、命令、结果和 EXPLAIN。无 schema 改动，迁移专项不适用。
- 浏览器使用实际后端分别以管理员、所属员工登录，核对新建合成客户事件、风险页签、角标、详情及确认；已有生产 #48 的最终可见性须在相同授权环境核查其快照，不能用模拟界面冒充现场验证。

已查阅 `docs/production-environment.md` 和 `docs/release-checklist.md`。数据库版本及服务器信息以该本地环境文档为准，不复制到设计文件；旧记录不当作本轮执行结果。

### 实施与测试结果

实现文件为 `customer_access.go`、`finding_snapshot.go`、`store.go`、`repeat_operation.go`、`assignment.go`、`login_target.go`，均在 `service/auditsecurity/`。前端、认证入口、用户密码修改和 relay 文件没有变更。

新增回归包括 `TestCustomerSensitiveRiskLifecycle`、`TestCustomerSensitiveRiskVersionsAndUpgrade`、`TestCustomerSensitiveRiskIdentityAndAssignment`、`TestAssignmentLegacyLatestMustBeNewest`、`TestCustomerSensitiveRiskAssignmentQueryPlan`；管理员重复操作用例新增版本不变断言。原个人查询计划断言也适配 PostgreSQL 的等价索引选择：时间条件作为 Filter 时，仍须有账号索引条件、无 Seq Scan，并通过 EXPLAIN ANALYZE 确认扫描工作量不超过 100 行。

实际工具链：Go 1.26.11，测试所用 Go SQLite 驱动的引擎版本为 3.50.4。MySQL 5.7.44、PostgreSQL 18.7 使用本轮独立 Docker 容器；每个服务测试通过既有 fixture 创建并回收专用数据库。没有操作共享开发库或生产库。

| 检查 | 命令和结果 |
| --- | --- |
| SQLite 安全审计包全量 | `go test ./service/auditsecurity -count=1`，通过，255.935 秒；需真实数据库的计划用例在下两项执行。 |
| MySQL 相关回归 | `TEST_ADMIN_TOKEN_DSN` 指向临时 MySQL audit_test 库，执行 `go test ./service/auditsecurity -run '^Test(Customer\|EmployeeAudit\|Pending\|Assignment\|Self\|Personal\|RepeatOperation)' -count=1 -json`；198 项通过（含子用例）、0 失败、0 跳过，215.364 秒。 |
| PostgreSQL 相关回归 | 同上命令，`TEST_ADMIN_TOKEN_DSN` 指向临时 PostgreSQL audit_test 库；198 项通过（含子用例）、0 失败、0 跳过，116.881 秒。 |
| 完整程序构建 | `go build -o <临时目录>/customer-risk-20261124-final.exe .`，通过；此二进制用于浏览器验收。 |
| 差异检查 | `git diff --check`，通过。无 schema 改动，无需执行新迁移或数据回填。 |

两个真实数据库均捕获并验证了实际 `ListAlerts` 查询：MySQL 的四个 findings 证明子查询使用 `idx_sec_finding_alert_id`、访问类型 `ref`；PostgreSQL 使用相同索引进行按 alert_id 的 Index/Index Only Scan，其中最大 ID 为反向索引读取，没有 findings 顺序扫描。测试判断访问范围而非要求优化器固定选择某个等价索引名。该结果验证给定样本的查询计划，不代表生产规模压测。

### 实际浏览器验收

本轮程序在 `localhost:18827` 运行，主库使用隔离 MySQL，日志库使用隔离 PostgreSQL，页面为现有构建产物。通过实际设置、用户和员工 API 创建测试账号，再在浏览器切换客户、员工、超级管理员身份；没有模拟接口返回。

1. 普通客户 `riskclient #3` 在“安全与访问”页面实际完成密码修改，日志记录 `user.password_change` 成功；安全后台生成 `sensitive_operation` 版本 4、告警 #1。
2. 所属员工 `riskstaff #2` 的审计入口出现客户未处理角标 1。“我的客户 → 风险记录”显示该客户的高危操作、风险级别高、状态未处理。
3. 员工打开“安全告警 #1”，证据显示账户密码变更及成功结果。点击确认后提示“风险记录已处理”；详情变为已确认，处理历史显示来源员工、待处理到已确认。
4. 员工未处理列表变为空，角标消失；数据库中仍是同一 ID，状态为 resolved、occurrences 为 1。
5. 超级管理员重新进入安全监控，看到同一高危操作 #1、已处理/已确认，以及自动分配的当前员工。详情处理记录显示 `riskstaff #2` 在 02:35:12 执行确认，与员工侧一致。

验收后回收本轮临时应用及容器。没有新增前端文案或修改认证公共文件。

测试日志存于本机临时目录的 `customer-risk-20261124-mysql-final-tests.jsonl` 和 `customer-risk-20261124-pg-final-tests.jsonl`。生产 #48 的完整快照未读取，不能用合成案例替代该条记录的现场验收；证据不全或超过 100 次的历史版本继续保持不可见边界。

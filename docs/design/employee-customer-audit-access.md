# 员工查看客户审计与处理风险

日期：2026-11-17。状态：**已实施，功能定向验证通过；整包回归保留两个既有失败，未部署**；用户已补充确认，范围为**客户自己的操作日志和风险处理历史**。管理员对客户执行的管理操作不包含在客户操作日志中。本文细化 `audit-security-customer-response.md` 第 5 节，不扩大到该文档中的多负责人、AI IP 采集或自动封禁。

## Goals and Scope

- 在“我的客户”行操作中提供“审计与风险”入口，分开展示操作日志、风险记录；详情提供精简证据、概览和处理历史，风险操作统一一键确认（2026-11-22 调整）。
- 员工只访问当前归属自己的普通客户。每个读写请求重新核验员工与客户关系；禁用客户仍可调查，转移、删除或提升为管理员/有效员工后不再可访问。
- 员工能处理客户风险，但不能获得全局审计权限；删除/清理、导出、管理员指派和通知配置继续使用原权限入口。

## Existing Logic and Related-Path Findings

| 已读取的实现 | 已验证行为与调整 |
| --- | --- |
| `router/api-router.go`、`controller/customer.go`、`model/customer.go` | 员工客户接口已有 `UserAuth`；路径 `:id` 是客户用户 ID。真实归属来自 `users.inviter_id`，不能依赖可能滞后的客户资料副表。 |
| `model/employee.go`、`model/employee_export.go` | 员工资格缓存存在 TTL；新数据权限使用当前数据库状态，不把缓存、导出模板授权或前端 `is_employee` 当作最终授权。 |
| `controller/audit.go`、`model/audit_log.go`、`model/audit_changes.go` | 管理操作的 `user_id` 是操作员，目标客户在管理参数中。按客户 `user_id` 查询并使用普通用户/self 投影，移除 AdminInfo/RootInfo/AuditInfo，保留允许客户查看的字段变更。 |
| `service/auditsecurity/{rules,store,finding_snapshot}.go` | 部分认证告警为 Root 可见，证据可能关联匿名或其他账号；旧合并键不含 actor ID，不能直接把整个管理员详情接口开放给员工。 |
| `service/auditsecurity/investigation.go`、`model/audit_triage.go` | 已有版本冲突、状态转换、动作历史和风险/活动修订号。员工复用这些生命周期，但备注单独记入动作历史，不能覆盖管理员内部结论备注。 |
| `model/quota_reserve.go`、`model/audit_query.go` | relay 的额度回退更新 `users` 行。审计已有独立连接池，因此新授权只做按主键的普通读，不新增用户行锁或 relay 调用。 |
| `web/src/features/customer-console/index.tsx`、`usage-logs/audit/` | 客户列表使用 `customer_user_id`；全局审计/安全组件绑定管理员 API。新增独立客户请求适配层，复用展示和表单，不通过伪造管理员角色调用旧接口。 |

## Data Flow and Authorization

浏览器会话 → `UserAuth` → 明确校验 dashboard session → 独立审计主库连接池读取当前操作员、员工资料及客户 → 按客户固定条件查询日志库/安全表 → 服务端白名单投影 → 前端展示。

授权要求：操作员账号启用且员工档案启用；客户存在、普通用户角色、`inviter_id = 操作员 ID`，且无启用员工档案。禁用客户可调查。权限查询不使用 `CustomerProfile.employee_id`、缓存或客户端传入的角色。

保存风险处理：在短事务内重新授权并读取客户可见告警 → `id + version + purge_state` 条件更新 → 追加 `SecurityAction(source=employee)` → 更新已有风险/活动修订号 → 返回安全投影。员工记录实际操作员 ID/角色供管理员审计。

撤权以请求授权检查为边界：客户转移提交后开始的新请求立即按新归属拒绝；已通过授权的在途请求可能完成，不承诺收回已经发送的数据。避免为串行化转移而锁定 relay 同时更新的用户额度行。

## API Contracts

全部位于现有 `/api/user/employee/customers/:id` 组，要求登录的 dashboard 会话和上述实时客户授权。四条路由均在 `middleware/access_token_routes.go` 显式声明 `accessTokenSessionRule`，控制器再次检查会话身份。不授予 `audit.read` 或 `audit_security.read/manage` 全局权限，不接受 PAT/API Key。

| 方法与后缀 | 输入 | 返回 |
| --- | --- | --- |
| GET `/audit` | `start,end,cursor`，可选 `request_id,ip` | `{items: AuditLog[], next_cursor}`，已执行客户安全投影 |
| GET `/security/alerts` | `start,end,cursor` | `{items: CustomerSecurityAlert[], next_cursor}` |
| GET `/security/alerts/:alert_id` | 告警 ID | 客户告警摘要、同账号证据及最多 100 条处理历史 |
| POST `/security/alerts/:alert_id/actions` | `{version,status?,conclusion?,note?}` | 更新后的客户告警摘要 |

列表固定每页 20 条，默认最近 24 小时，最大跨度 31 天。时间与 ID 游标顺序稳定，不执行 COUNT/OFFSET。游标绑定用户、客户、范围和筛选，不能换客户复用。前端显示“下一页”而非虚假的总条数。

2026-11-17 审计修正：相对本次审阅基线 HEAD，客户列表实现为新增代码。已复现先截取 20 行再执行身份过滤，导致空页或不足页但后续仍有可见数据。按用户确认直接修复：按现有联合索引顺序分批读取，每批最多 21 行，过滤后收集至 21 条可见记录或范围耗尽；仅返回前 20 条，发现第 21 条可见记录时才返回游标，游标绑定最后一条实际返回记录。所有数据不可见时正常返回空列表且无游标；不能仅因某批为空就停止。继续沿用控制器 3 秒总超时和逐次查询的请求 context，超时走原有可重试错误响应，不返回不完整的成功页。日志投影与历史身份核验不变。

本次修正不新增字段、索引、写入或连接池。授权仍在独立审计池按用户主键普通读取，不锁定 relay 共用的 `users` 行；分页扫描只读取客户范围内的 `audit_logs`、`audit_security_alerts` 和历史身份快照。已有 `idx_audit_customer_cursor`、`idx_sec_customer_time` 及快照索引覆盖原有查询形状，分批结果内存保持有界，3 秒截止时间限制隐藏记录密集时的总读取成本。

状态沿用 `open/investigating/resolved/false_positive`；结论沿用已有枚举；备注最多 1,000 字符。不接受负责人、归档、删除等附加写入字段。正常返回沿用 `common.ApiSuccess`。

## Customer Risk Identity and Projection

2026-11-24 按当前实现校正：账号规则集合由 `customerAccountRuleVersion` 定义，包括 `login_after_failures`、`new_login_source`、`session_reuse`、`permission_failures`、`anonymous_network`、`ip_abuse`、`impossible_travel`、`multi_account_key_access`、`multi_source_key_access`、`risky_ip_login`、`risky_ip_sensitive_operation`、`new_source_sensitive_operation`。通常要求正数客户 ActorID 和普通用户 ActorRole；下述会话重放及可信登录目标是明确的例外。密码喷洒、恢复请求和管理操作不能凭用户名、IP 或任意 target_id 推断客户归属。

可信登录失败目标另由 `login_target.go` 校验：只有 `login_failures`、`anonymous_network`、`ip_abuse` 的版本 2，且 actor ID/role 为 0、资源为 account、服务端可信 target_user_id/target_role 明确指向普通客户时才开放。证据必须通过对应可信目标校验；没有可信目标的匿名事件仍不可见。

2026-11-24 已修复普通客户自己的 `sensitive_operation` 漏显：新事件使用按账号及角色隔离的版本 4，仅开放 user/account 资源和本人目标；版本 1～3 须通过完整同客户快照证明（1～100 次、最新快照一致、普通角色、同资源及本人目标）。列表、角标、详情、确认和负责人筛选共用该边界，本人安全视图及个人通知范围不扩大。实现及三库验证见 [员工客户风险记录漏显修复](employee-customer-sensitive-risk-visibility.md)；尚未部署到生产。

同日追加修复：可信密码恢复结果保留匿名操作者 role=0，以版本 5 证明受影响普通账户；客户自己的批量敏感操作使用版本 4/5 并隔离混合身份。SQL 的 NULL 语义与显示统一，历史恢复及批量记录通过显式、有界的证据校验维护命令升级。具体边界及验收见 [告警自动归属补漏](audit-assignment-completion.md)。

会话重放的已核实例外：`model/user_session.go/RotateUserSessionRefresh` 只有匹配已知旧刷新凭据、且超过竞争宽限窗口时才报告 reuse；`service/auth_session.go` 写入已确认的会话所属 UserId，操作者 ActorRole 仍为 0。此特定规则允许按正数账号 ID 及当前客户归属访问安全摘要，保留原日志的未知操作者与 Root 可见性。证据中 Role=0 的事件也只允许 `auth.session_refresh_reuse`，其他匿名事件不能套用此例外。

新告警记录触发角色；上述规则合并键增加 ActorID/ActorRole，并升级规则版本，隔离历史不确定的账号合并记录。旧记录仅在有完整、同账号普通用户判定快照时开放：最多检查 100 条，数量须匹配 occurrences，最新快照须匹配 latest_finding_id；证据不足、混合账号或超过这个核验上限时仍由管理员调查，不后台扫描或补造历史归属。

客户 DTO 明确列出规则、风险、状态、首次/最近时间、命中数量、结论、版本。证据仅取同客户普通用户事件；不返回其他账号/匿名事件、SubjectRef、内部管理信息、通知投递目标或凭据。Root 原始可见性不变，员工使用独立客户摘要权限。

处理历史返回状态/结论变化与时间。管理员和系统记录只显示通用来源及安全状态变化，隐藏内部备注、管理员姓名/ID和指派信息；员工来源的排查备注可被当前归属员工查看。员工备注不读写 `SecurityAlert.ConclusionNote`。管理员仍能在原入口查看完整处理历史。

## Data Model and Index Design

不增加业务表。`SecurityAlert` 增加触发角色 `actor_role`；旧记录默认读取为 0，必须按前述历史快照规则核实，不能仅凭该默认值授权。复用已有 `SecurityAction.Source` 的 `employee` 值。

| 表与索引 | 访问模式 |
| --- | --- |
| `audit_logs: idx_audit_customer_cursor(user_id,created_at,event_id)` | 单客户时间窗口和倒序游标；MySQL/PostgreSQL/SQLite 均使用 GORM 索引 |
| `audit_security_alerts: idx_sec_customer_time(actor_id,last_seen,id)` | 单客户有界告警列表；状态和清理标记是附加过滤 |
| 现有 `idx_sec_episode_v2` | 先按规则、subject、可见性、资源、版本及 15 分钟窗口缩小合并候选，再检查账号/角色，不新增冗长的重复合并索引 |
| 现有快照 `(alert_id,id)`、新增 `idx_sec_action_cursor(alert_id,created_at,id)` | 历史身份核验、按时间与 ID 稳定读取最近 100 条动作 |

索引/列通过现有迁移入口建立，覆盖独立日志库。ClickHouse 日志仍按现有月分区与 `(created_at,event_id)` 排序进行有界读取，不套用关系库索引 DDL。主库不支持 ClickHouse。

EXPLAIN 使用独立临时数据库和 10,050 条合成记录验证；不向共享开发库或生产库写入数据。真实迁移验证覆盖新建、升级及第二次启动无 DDL。生产采用 MySQL 主库 + PostgreSQL 日志库，版本与部署验收以 `docs/production-environment.md`、`docs/release-checklist.md` 为准；本次未连接生产服务器或执行部署。

## Frontend Lifecycle

“我的客户”行操作打开宽版“审计与风险”对话框，固定显示客户名/用户 ID；两个页签独立分页、刷新、加载、空态和重试。操作日志复用现有字段/详情显示，风险记录复用状态、规则和结论文案；2026-11-22 起以公共确认按钮替代状态表单。

查询 key 包含当前登录用户和客户 ID；切换客户重置筛选与游标，不复用上一客户数据。403 清空该客户内容并显示归属/权限已改变提示。写入时保留用户实际审阅的版本；失败不自动重试，冲突刷新后允许再次确认；成功刷新客户告警及历史。

“查看关联审计”只在本客户内设置请求 ID/IP及事件时间范围，不跳入全局审计查询。员工入口不请求全局负责人、通知或跨账号关联 API。前端新增文案完整覆盖七种语言；不修改受保护的认证/HTTP 文件。

## Errors and Configuration

无新配置或开关。参数、时间范围/游标错误使用现有参数错误；版本冲突/清理中使用现有告警冲突文案；无客户权限返回 403，不泄露目标是否存在；不可见告警返回统一不可用信息。数据库错误记录请求上下文与内部错误，向用户返回可重试的本地化消息。所有读取有请求超时，无静默失败或虚构后台行为。

## Main Chain Impact / Shared Resource Audit

- relay 同步路径新增 DB/Redis/锁/协程调用均为 0；本功能仅 dashboard 管理请求和已有安全检测后台。
- `users`、`employee_profiles` 仅按主键/唯一键普通查询，用已有独立审计池，不增加用户行锁。风险写入只更新安全表及已有修订号。
- 日志与安全查询复用独立审计池，最多两个连接/池；不增加新池、Redis namespace、全局缓存或后台任务。安全后台合并仍在原 worker 和批次事务中。
- 无消费日志扫描、额度更新或新的 relay 采集行为。100k RPM relay 不因此增加任何每请求工作。

## Implementation and Verification

2026-11-21 员工入口补充：审计主页新增“我的客户”页签，按客户账号、编号或备注搜索候选后查看单个客户；客户名和编号显示在列表及详情。主页面与原客户弹窗共享 `CustomerAuditView`，继续调用现有四个客户接口及实时归属校验，不将 `/api/audit/self` 扩大为客户查询。切换客户或登录身份清空原记录/详情，403 后仍可重新选择客户。详细范围与验收见 [字段可见性设计](audit-user-employee-field-visibility.md#员工客户标识与筛选补充2026-11-21)。

2026-11-22 默认展示调整：进入“我的客户”直接加载当前员工名下全部可访问客户的最近 24 小时操作日志，风险记录同样支持汇总；客户下拉改为可选筛选，清除后恢复全部客户。新增两个 session-only 汇总 GET，沿用实时员工/客户授权与安全投影，分批读取和全局游标分页。每行显示自己的客户身份，风险详情、关联审计和处理仍走该客户的原接口。实现及验证见 [默认展示设计](employee-customer-audit-default-list.md)。

实现位置：`model/employee_customer_audit.go`（实时授权/日志游标）、`service/auditsecurity/customer_access.go`（风险范围与投影）、`controller/employee_customer_audit.go`、现有路由；安全模型/合并/调查逻辑；前端 `customer-console` 入口和客户独立请求/展示组件。

重点回归：本人客户/其他员工客户；转移、停用、删除、提升角色；客户端注入 user_id/actor_role/目标 ID；原管理员接口仍拒绝员工；跨客户游标；混合/缺失历史快照；跨账号证据与管理员备注不可见；员工备注不覆盖内部备注；版本冲突、重新打开、清理竞态；切换客户与 403 清缓存；关联审计不扩大范围。

## 验证与发布边界

回归保护会话专用路由、实时客户归属、历史身份投影、状态写入及隐藏记录补页；覆盖 0/1/20/21/43 条可见记录、连续隐藏批次、相同时间戳和真正耗尽后的空游标。查询计划测试捕获首批与续页的实际 SQL，允许优化器选用等价的有界索引，不能只按索引名称判定正确性。

真实数据库验证使用独立主库/日志库及临时数据。发布前仍需完成目标 schema 副本演练，并按启用情况验证 ClickHouse；本地组件或定向回归不代替完整发布验收。

## 员工确认与精简证据（2026-11-22）

员工通过一次点击确认告警，详情优先展示简明证据；沿用已有权限和安全字段。

### Existing Logic and Related-Path Findings

- `customer-audit-dialog.tsx` 和 `customer-risk-detail.tsx` 使用 `AlertStatusCell`，点击“待处理”会打开状态、结论和备注表单。管理员列表和详情已使用 `AlertConfirmButton`，具有提交中禁用、成功状态、错误提示和版本匹配保护，员工直接复用此组件的交互。
- `controller/employee_customer_audit.go:EmployeeCustomerSecurityAction` 已接受 `{version,status}`；`service/auditsecurity/investigation.go:UpdateAlertInvestigation` 在客户范围内重新授权、按版本更新状态，并写入 `source=employee` 的处理历史。省略 conclusion/note 保留已有结论和管理员内部备注，不需要修改后端或增加接口。
- `GetCustomerAlert` 已返回经客户身份校验的 `CustomerSecurityEvent`：action、at、ip、success 及关联定位 ID。当前页面没有显示 action，却默认展示长请求 ID；将这些已有证据改为可读事件名称、时间、IP、结果即可。不能使用管理员 `SecurityEventCard`，它会展示更宽的 facts、资源和内部调查字段；继续组合公共 `DetailSection/DetailRow`、`CopyButton`、`StatusBadge` 和 `SecurityRecordPages`。

### 行为与生命周期

- 汇总列表、单客户列表和详情均显示“确认”；点击一次通过该行客户的原 POST 接口提交 `version` 和 `status=resolved`，成功显示“已确认”。原已关闭/误报状态不变，不要求填写表单。
- 确认组件明确区分员工和管理员请求合同。员工不发送管理员的重复命中确认参数、不回退到管理员 API。提交期间禁用，失败不自动重试；版本冲突刷新员工范围缓存后允许再次确认；403 调用原撤权处理并清除可见记录。旧版本响应不得确认后来重新打开的告警。
- 风险详情默认打开“证据”，仅显示已有客户事件的名称、时间、来源 IP 和成功/失败，保留 IP 复制及本客户内的“查看关联审计”。未知动作使用通用操作名称，不输出原始 action/facts。请求 ID 留作关联定位，不在简版证据堆叠展示；概览和处理历史仍可切换查看，证据继续按 10 条分页。
- 数据流、存储、API 授权、后端字段白名单和错误响应均沿用现有逻辑。无新数据模型、索引、SQL、配置、连接池、Redis 命名空间或后台任务；不修改 relay。前端只在用户点击时执行原有写入，并失效相关客户查询缓存。
- 验证覆盖一次点击与客户路由、列表/详情同步刷新、重复点击、旧版本响应、业务冲突和撤权，以及精简证据、分页、关联定位和管理员确认回归。文案复用已有七语键。

### 实施与验证

- 已按上述范围实施；确认按钮使用客户/管理员区分的类型合同，员工分支必须提供撤权回调。共享 `activityLabel` 接受 action 投影，为证据复用既有安全名称及未知动作降级。详情缩小为 `sm:max-w-3xl`、内容高度 `min(60dvh, 30rem)`。

回归覆盖员工与管理员请求隔离、重复点击、版本冲突、403 撤权、旧版本响应和证据分页；浏览器验证应覆盖桌面、窄屏及深色模式。该交互调整不新增后端接口或数据库行为，验证结果与部署状态按具体版本单独记录。

# 失败登录告警关联目标账户

日期：2026-11-22。状态：已实现。

失败登录告警关联登录流程实际查到的目标账户，供本人查看并按当前客户归属分配员工。新告警直接保存目标快照；历史告警仅通过显式维护工具、依据完整可信证据修复，不在启动或请求路径自动回填。

## Goals and Scope

将两种身份分开保存：`actor` 表示发起操作的人，失败登录仍未知；`login_target` 表示登录流程实际查到的目标账户，有独立的用户 ID 和当时角色。用户名唯一，因此登录流程能找到目标账户；没有通过认证不影响记录目标账户，但目标账户不是已经认证的操作者。

本次覆盖密码登录失败、有效二次验证流程中的验证码/Passkey 失败与锁定采集，以及由这些事件产生的单账户告警：`login_failures`、`ip_abuse`、`anonymous_network`。密码喷洒等跨账户告警继续保留完整管理视图，不能以最新一次尝试的账户代表整条告警。

目标账户本人在“我的安全”查看只读摘要及自己的事件；当前归属员工通过现有客户安全入口查看、调查，并自动显示为负责人。手动增加负责人仍只允许符合权限的管理员。

本次关联用于可见范围和调查归属。账户封禁、凭据清理及个人外发通知仍按现有已认证事件规则执行；不能把被攻击的账户当成攻击者执行处置。原始审计的“仅自己”仍表示实际操作者，失败登录的新增本人入口是“我的安全”。

## Existing Logic and Related-Path Findings

以下为实施前阅读代码核对的行为和修改落点。旧文档中“失败登录绝不关联用户”的约定由本设计的目标账户语义取代；不改写已认证操作者的原合同。实施结果和验证边界见末节。

| 代码位置 | 已核实的行为和本次落点 |
| --- | --- |
| `model/user.go:89,1436`、`controller/user.go:59` | Username 有唯一约束；`ValidateAndFill` 对输入去首尾空白，按 `username = ? OR email = ?` 查询，再校验密码和状态。密码错误时 `user.Id/Role/Username` 已填入。复用这次实际查询结果，避免按展示字符串重新查用户，也不另改邮箱/大小写匹配语义。 |
| `model/audit_security_capture.go:47`、`model/audit_other.go:27` | Capture 目前只取 context 中的操作者 ID/role，记录经过清理的 attempted_account 和原始标识的 HMAC，投递到容量 1024 的非阻塞队列。没有目标用户快照。 |
| `service/login_verification.go:46,146`、`controller/secure_verification.go:38`、`controller/login_verification.go:40,81` | 验证码错误包装仅携带账号文字；有效验证流程和 Passkey 附加验证入口已经持有 State.UserID/Role。可传递同一次有效流程中的目标快照，无须加用户查询。无效/过期流程不能从请求字段构造目标身份。 |
| `service/auditsecurity/rules.go:58,279` | Normalize 为元数据白名单；登录及 auth 事件的完整管理视图为 Root 可见。失败次数按 subject_ref 聚合；同 IP 多账户另生成 password_spray。目标字段须独立于 Identity，并仅在指定失败动作中接受。 |
| `service/auditsecurity/store.go:276,497`、`finding_snapshot.go:11,71` | 告警按规则、subject_ref、可见级别、资源和版本合并；现有可信账号规则额外按 actor ID/角色隔离。证据有界，命中快照独立保存；更新使用 id+version 冲突重试。需给目标账户增加合并隔离，保留原并发协议。 |
| `model/audit_security.go:28,56`、`model/audit_triage.go:38` | 规范事件和命中快照已有 TargetID；告警主表没有目标用户字段。可复用前两者的 TargetID，并给告警增加目标 ID/角色、给命中快照增加目标角色。 |
| `service/auditsecurity/self_access.go:52,137`、`customer_access.go:14,109,195` | 本人/客户列表及详情固定按 actor_id；login_failures 被规则白名单排除，事件投影也拒绝匿名失败。不能只修改列表 SQL 或显示姓名。 |
| `service/auditsecurity/assignment.go:15,24,130`、`employee_customer_list.go:23` | 自动负责人通过 `customer.inviter_id → employee.id` 派生，要求客户为普通非员工用户、员工和 profile 有效。负责人筛选有单独 SQL 身份判定；员工总列表也用 ActorID 填客户信息，均须同步支持目标账户。 |
| `service/auditsecurity/investigation.go:29`、`model/employee_customer_audit.go:20` | 员工调查在事务内重新检查当前员工资格和客户归属，保留版本校验；员工不能自行指派其他员工。 |
| `response_eligibility.go:15`、`account_response.go`、`account_notify.go:67` | 自动处置要求真实 actor 与 AuditIdentity；人工处置也取告警 ActorID。个人通知入队以 ActorID 调用个人权限判断。新增目标可见性不得把这些调用的账号替换成目标用户。 |
| `actor_usernames.go:19`、管理列表/详情/关联/导出 | 名称在通过权限检查后按用户 ID 批量补充，仅用于显示。目标名称复用同一批量读取能力，不逐行查用户。 |
| `model/audit_log.go:267`、`service/auditsecurity/public_event.go:11` | 原始审计投影和监控公共事件都移除内部 Identity。新增 LoginTarget 同样作为内部来源证明移除，对外只返回允许展示的目标 ID/角色/名称，避免把内部结构直接渲染为未翻译 JSON。 |
| `web/src/features/usage-logs/audit/security/security-alert-columns.tsx:86`、`security-alert-dialog.tsx:338`、`security-event-card.tsx` | 当前展示“未识别账号”和“尝试登录账号”。复用字段布局增加独立“目标账户”，保留尝试文字；不把目标姓名填入 Operator。 |
| `web/src/features/usage-logs/audit/self/index.tsx:47`、`web/src/features/customer-console/customer-audit-dialog.tsx` | 已有按用户/客户区分的 Query key、取消请求、错误清空和游标重置。使用现有界面及缓存生命周期。 |

相关既有合同：`audit-security-attempted-account.md`、`audit-self-security.md`、`audit-security-customer-response.md`。实施后更新这些文档中匿名失败、自动分配和个人可见范围的相关段落。

目前没有读取截图所在服务器的告警/源日志；不能断言 #39 有可恢复的目标 ID。已确认当前旧采集代码只保存尝试账号文字，这种记录本身不足以恢复事件发生时的用户 ID；历史修复必须给出逐条结果。

## Data Flow

1. 密码登录既有查询得到 User，或二次验证读取有效服务端流程；以独立 `AuditLoginTarget{UserID, Role}` 快照绑定本次采集。只有已实际查到的账户才设置；不写 context 的 `id/role/auth_version`，不构造 AuditIdentity。
2. `CaptureSecurityEvent` 复制快照到 `AuditOperation.LoginTarget`，保留 attempted_account；沿用非阻塞队列和后台写源日志。客户端 JSON 中同名字段一律不是快照来源。
3. Normalize 先清除 params 中伪造的 login_target，再只接受服务端结构化字段。限定 `auth.login_failed/auth.login_locked`、`success=false`、正数目标 ID、已知角色。写入规范 Facts.LoginTarget，并用已有 SecurityEvent.TargetID 保存目标 ID。其他动作不能继承此关联。
4. 已知目标的失败事件以 `"login-target:" + SecurityAccountRef(userID)` 作为监控层相关性键；成功登录的监控事件使用同一稳定键，使用户名/邮箱、改名前后对同一账户计数一致。原始审计中的输入文字和原 HMAC 不改写。未知目标保持既有匿名相关性键。
5. 后台检测、告警合并及命中快照保存目标信息。只有单账户规则可把目标写到告警主表；password_spray 不填单一目标。账号是否存在、使用何种输入，不改变公开登录失败响应。
6. 查询先鉴权，再按已认证 actor 或已记录目标账户判断本人/客户范围；仅投影本账户事件。自动负责人按这个已确认的账户 ID 读取当前归属。

密码解密失败等发生在用户查询之前的失败、不存在账号、无效验证流程仍保留未知目标。采集不为这些情况增加同步查询，也不在后台根据可能变化的当前用户名补造新事件的身份。

## API Contracts and Authorization

沿用现有接口、认证组和 `{success,message,data}`，不新增公共查询或修改归属的 HTTP 接口。

| 接口 | 变化及权限 |
| --- | --- |
| GET `/api/audit/self/security/alerts`、`/alerts/:id` | 现有 UserAuth + dashboard 会话；用户 ID 只取会话。增加属于本人的目标型告警，原 SelfSecurityAlert/Detail DTO 保持；详情仅含本目标的时间、动作、IP、结果，不返回负责人、调查备注、其他账户或原始 Facts。仍只读。 |
| GET `/api/user/employee/customers/:id/security/alerts`、`/alerts/:alert_id`，以及 `/api/user/employee/customers/security/alerts` | 当前有效员工、客户当前归属、客户普通且非员工；增加目标型告警。沿用 CustomerSecurityAlert/Detail DTO。目标在事件发生时属于管理员/Root 的记录不向客户员工开放。 |
| POST 既有客户 `/security/alerts/:alert_id/actions` | 保留 version/status/conclusion/note 合同，在同一事务内按目标客户复核归属。禁止分配、封号及凭据清理字段。 |
| GET `/api/audit/security/alerts`、详情、关联及现有导出接口 | 保留 AdminAuth、审计权限和 Root 原始证据隔离。告警响应增加可选 `target_user_id,target_role,target_username`；事件继续使用 target_id，命中快照补 target_role。管理员列表的 actor_id 筛选仍只表示操作者，不能悄悄改成目标查询。 |
| 既有负责人筛选及 `/api/audit/security/assignees` | 自动负责人和“未分配”筛选均纳入目标关联；手动候选仍只为符合该告警可见性和管理权限的管理员。 |

普通用户/员工获准读取的是独立投影，不获得全局安全监控权限。用户、员工转移/停用、删除或角色变化时沿用现有实时数据库权限检查，不以历史负责人授权。

## Data Model and Index Design

不改 users、tokens、channels，不新增业务表。

| 对象 | 调整 |
| --- | --- |
| AuditOperation / 源 audit_logs.other | 增加可选 LoginTarget 结构化字段；原 JSON 列和 Scanner/Valuer 保持。既有日志库 SQLite/MySQL/PostgreSQL/ClickHouse 序列化路径须兼容，无日志库新列。 |
| SecurityEvent / Facts | 复用 target_id；Facts 增加经过白名单处理的 LoginTarget。事件 target_role/target_username 为 gorm 忽略的展示字段，从已授权内部事件投影，不新增事件表列；公共投影移除 LoginTarget 证明，不暴露任何认证凭据。 |
| audit_security_alerts | 新增可空 target_user_id、target_role 整数列；缺省 NULL/0 表示未关联，不设置跨库不一致的 tag 默认值。TargetUsername 只读展示，gorm 忽略。 |
| audit_security_findings | 复用 target_id，新增可空 target_role 整数列；记录触发时快照。旧快照不按当前用户名覆写。 |

新增 `idx_sec_target_time(target_user_id,last_seen,id,purge_state)`。本人/单客户目标列表的第一步只扫描 id、last_seen，带目标 ID、时间窗、清理状态与 `(last_seen,id)` 游标，再按主键批量读最多一页详细字段；索引覆盖新扫描的筛选和排序列。原 actor 查询保留 `idx_sec_customer_time`，详情按主键定位，快照按既有 `(alert_id,id)`，归属按 users 主键及现有邀约/profile 索引。

列表分 actor、target 两条有界索引查询，沿各自原始候选游标继续收集可见记录，合并后按 `(last_seen DESC,id DESC)` 排序、按告警 ID 去重、取 20+1 条。避免 broad OR 导致整表扫描，也不能先取混合 20 条再过滤而漏页。员工总列表沿用当前分批客户 ID 和归并方式，分别处理两条身份路径。

告警合并继续用已有 episode 索引按规则/相关性键/时间限制候选，并额外核对 target_user_id/target_role。稳定账户键本身已隔离不同目标，无须为合并再加宽索引。历史候选按已有 last_seen/ID 索引限定时间后有界扫描；不跑无时间范围的全表回填。

迁移走 `MigrateAuditSecurity`；SQLite 只加列/索引，MySQL/PostgreSQL 使用原适配器。索引部署先在目标 schema 副本演练；不删除上线已有索引。新列、索引在旧数据上创建，启动过程不做历史身份回填。

真实 MySQL/PostgreSQL 的索引计划、新建与升级迁移、第二次迁移无 DDL 已在本地验证；完整服务以 MySQL 主库、PostgreSQL 日志库验证从修改前版本升级和重启。生产 schema 副本及生产规模验收未执行，本次没有部署。

## Business Logic and Edge Cases

### 单账户边界与版本

新 login_failures 使用规则版本 2，旧版 1 保留原记录；ip_abuse/anonymous_network 已有版本 2 不更改已认证账户的版本合同。目标型访问同时要求已知规则版本、正数目标 ID、已知目标角色、account 资源、未知 actor 和同目标失败证据。未知未来版本拒绝。旧版只有通过历史修复核验后才可建立目标关联。

新增读取用的“告警所属账户”判定，分别返回已认证 actor 或登录目标，并统一用于本人/客户读权限、员工总列表客户填充、自动负责人和负责人 SQL 筛选。管理处置与认证校验继续使用原 actor/Identity 逻辑，不能复用读取归属来授予处置资格。

证据归属按每条 event 独立检查：目标型事件必须为失败/锁定动作、成功标志为 false，且 event.TargetID、服务端目标快照及当前读取账号一致。混合账户证据不外泄，密码喷洒不能通过填一个 TargetID 变为个人告警。只展示允许的 IP/动作/时间/结果；本人不显示员工或管理员调查备注。

已知目标使用稳定 ID 计数，相关性键为 `login-target:` 加上用户 ID 的 SecurityAccountRef；前缀位于 HMAC 输出之外，与未知输入的 account 命名空间隔离。`3`、`login-target:3` 等任意尝试文本不能碰撞可信目标键。未知目标继续依原输入 HMAC 计数；旧匿名事件不在升级时自动改成当前账号事件。成功登录关联失败的 15 分钟窗口会从新稳定键重新积累，原事件和告警仍可由 Root 查询。

### 归属生命周期

- 客户无员工、员工/profile 停用、客户自身是员工或已升为管理员时，不自动分配员工。
- 客户转移后新员工取得当前允许范围，旧员工失去访问；历史调查记录继续保留当时执行人。
- 目标用户改名不改变历史 target_user_id，显示名可更新；删除后不把原告警转给后来使用该名称的账户，管理端仍可保留 ID 和原始尝试文字。
- 每次调查写入仍检查 version；后台合并、人工确认、归属变更及清理冲突按原事务/版本语义处理。
- 获取不到显示名仅显示 `#ID`；不能因此删除目标关联或猜测另一个账号。

## Historical Repair Lifecycle

提供一次性、有界的维护入口，默认只生成候选/跳过报告；在隔离库测试后，对指定数据库、时间窗（每次最多 31 天）和明确的告警 ID 范围应用。不会在每次启动或页面加载时隐式补关联。

可靠依据限定为事件发生时已经保存的明确目标 ID：命中快照/规范事件中的目标 ID，以及同一 event_id 对应的服务端目标角色/账户快照。只出现 attempted_account、当前同名用户、IP 相同或稍后一次成功登录，均不作为历史 ID 证明。当前旧采集实现可能没有留下这些信息，所以有些已有告警会明确报告无法修复。

每条候选要求：规则和版本在白名单；无既有目标或已有同一目标；完整命中快照集合与 occurrences/latest_finding_id 一致（沿用旧身份验证最多 100 条的上限）；全部相关目标证据一致，且没有混合用户/角色；原事件的动作和结果满足失败规则。源证据已清理、缺字段、名称被清理截断后无法核对、混合目标或超出核验上限均跳过，不推测。

经核验的关联补入告警目标字段，将告警 rule_version 升至目标读取合同版本 2，并递增告警 version、更新 updated_at。在现有 investigation 活动 Result 中记录来源事件 ID、原告警版本、原规则版本和关联前后值，Source 为 repair。原始 audit_logs、actor ID/role、尝试文字、首次/最近事件时间、命中计数和不可变命中快照保持原事实；历史快照的规则版本也不覆写。旧证据若没有足够的目标快照供个人/员工详情逐条验证，整条跳过，不能只让列表显示。

每条写入使用 id+version 条件、事务及活动 revision；发现并发更新、清理或已经修复则跳过/报告。再次运行幂等。每批最多 100 候选，批次 deadline 为 30 秒、单条写入 deadline 为 5 秒，不持 users 行锁、不触发通知、处置、重开或重新计费。报告逐条返回 candidate、repaired、already_linked、unsupported、missing_evidence、conflicting_evidence、purged、version_conflict，可按状态汇总。

运行前必须核对指定环境的历史证据；无法证明时保留原告警并报告跳过原因。当前用户名唯一不能证明历史身份从未变化。

维护命令位于 `cmd/audit-login-target-repair`，先完成常规版本迁移，再显式设置目标主库的 SQL_DSN。工具不自动加载 .env、不迁移数据库、不读取当前用户名。以下占位符由运行者按目标环境替换：

```text
go run ./cmd/audit-login-target-repair -start <Unix秒> -end <Unix秒> -min-id <最小告警ID> -max-id <最大告警ID>
```

默认只报告，添加 `-apply` 才写入。每行输出一批 JSON，分页自动继续；中断后可用相同范围重跑，已有可靠关联返回 already_linked。错误退出不回滚已经提交的前批；工具不打印 DSN 或认证材料。

## Frontend and Copy

管理列表复用现有 Operator 单元格的 `space-y-1`/小字字段结构：保留操作者“未识别账号”；另显示“目标账户：用户名 (#用户ID)”；原“尝试登录账号”继续展示实际输入（可能为邮箱）。详情、命中快照、关联及事件展示一致，不将目标填入 actor_username。

复用 `AlertAssignees`、业务 `Dialog`、`DetailRow` 和现有分页/加载/错误/刷新行为。目标显示直接从响应派生，不加额外 useEffect 状态或逐行网络请求。本人和员工界面沿用既有只读/调查布局及按账号隔离的 query key；刷新及切换账号清理旧游标和详情。`account-response-section.tsx` 已有的处置目标仍取 actor/既有处置回执，不把新目标字段代入封禁按钮的参数或可用性判断。

本次使用的 `Target account` 已在七种语言存在：目标账户 / 目標帳戶 / Target account / Compte cible / 対象アカウント / Целевой аккаунт / Tài khoản mục tiêu。`Attempted sign-in account`、`Unidentified account`、`Automatically assigned employee` 也沿用现有文案；本功能没有新增 locale key。受保护的上游 auth-session/http-client/api 等文件未修改。

## Config Parameters and Error Handling

不新增设置项或环境变量；沿用 CaptureEnabled、Enabled、Suspended。没有新 relay 执行路径，因此不引入 relay 功能开关。

采集队列满继续计数后返回；新增元数据不影响登录结果。Normalize 遇到非法目标字段只丢弃关联，保留匿名告警。后台数据库错误记录 SysError 并遵循既有重试/检查点；个人/员工查询错误沿用现有 ApiErrorI18n 和用户可读消息，身份不匹配统一不可见，不泄漏账号存在性或内部错误。历史工具报告失败原因类别，不能记录密码、验证码、令牌或 DSN。

## Main Chain Impact

AI relay goroutine 新增执行为 0：不修改 relay、计费、令牌校验、流式响应或相关中间件。Dashboard 登录只复制已查询到的 ID/角色、按原方式非阻塞入队；不增加登录用户查询、不新增每请求 goroutine。

元数据序列化、归并、关联与历史修复均在现有后台/维护流程。本人/员工查询使用已有审计独立连接池。新增索引构建与回填的 I/O 影响在部署前隔离演练中测量，不能用“未改 relay 代码”代替共享数据库资源检查。

## Shared Resource Audit

| 资源 | 主链关系与隔离方式 |
| --- | --- |
| users、employee_profiles | users 与认证/计费共用。复用登录原查询；管理读路径按主键批量补姓名、当前归属查询复用既有索引。不加 users 写入/行锁，不读配额大字段。 |
| audit_logs | 原源日志异步写入，仅扩展已有 other JSON。历史工具只读取主库已规范化的事件及快照，不回读独立日志库重建缺失身份。独立日志池、写队列不扩容。 |
| audit_security_events/alerts/findings/actions/activity | 主链不直接访问，使用现有监控池；新增小型列和一个索引。合并保留 CAS，历史每条短事务，避免大事务和启动回填。 |
| 连接池 | worker 主库最多 2、日志池最多 1；读取沿用已有独立池。维护工具串行小批运行，不占用或扩展 relay 连接池。 |
| Redis / 内存 / goroutine | 无新增 Redis key、共享缓存、分布式锁或 worker；现有采集队列每条仅增加有界目标快照。 |

## Concurrency Analysis

30k RPM（约 500/s）与 100k RPM（约 1,667/s）的 AI 请求新增同步/异步 DB、Redis、锁和 goroutine 均为 0。登录采集每次新增固定数量整数的复制，用户查询增量为 0；背景处理沿用既有一条源记录、规范事件、告警及快照的写入，不新增独立每事件归属查询。

本人页面按最多 31 天、20 条一页查询两条索引路径；名称批量查询复用现有流程。员工列表沿当前客户批次扫描，遇过滤记录继续推进原候选游标，受现有查询 context 限制。历史修复与索引部署需单独测量数据库 I/O，不能与 relay 业务高峰争抢资源。

## Verification Plan

先扩展已有回归文件，不为单一功能在所有层重复建测试框架。

1. `controller` 登录/验证用例：真实唯一用户名、邮箱、空白/大小写遵循既有数据库规则；密码错误/锁定/已禁用目标；不存在账号、查询前失败、无效/过期/重放流程；确认目标 ID 来自实际查到的账户，actor/Identity 保持未知，登录响应不泄漏目标存在性，客户端伪造字段无效。
2. `attempted_account_test.go`、`rules_test.go`、`store_test.go`：采集→JSON Scanner→Normalize→队列/扫描→告警/快照/列表/详情/关联/导出；同用户的用户名/邮箱计数一致，不同 ID/角色不能合并；密码喷洒不产生单账户归属；IP 补检保留目标且不扩大其事件范围。
3. `self_access_test.go`、`customer_access_test.go`、`assignment_test.go`：本人/他人、普通/员工/管理员/Root；个人只读；员工当前归属、转移、停用、客户升权；多负责人、“未分配”筛选、详情与列表一致；两条索引路径翻页不丢不重，隐藏候选不会提前结束。
4. 处置/通知现有回归：失败目标关联不能触发封禁、API Key/访问令牌清理或新增个人投递；已认证账户的原行为不回退。
5. 历史修复：原始目标证据完整时补关联；仅尝试文字即使当前有唯一用户也跳过；混合/缺失/清理/超上限/角色冲突拒绝；CAS 冲突、再次运行幂等、不修改原始身份、快照及计数，不制造通知/处置。
6. 前端既有 `monitor.test.tsx`、`security-event-card.test.tsx`、个人/客户测试：未知操作者与目标账户同时显示、缺姓名显示 ID、账号切换、分页、错误恢复、本人无管理按钮。使用实际 API 的浏览器联调覆盖 Root/本人/对应员工/无关员工。
7. 数据库：真实 MySQL/PostgreSQL 的新库、从前版升级、两次完整启动且第二次无 DDL；主库和独立日志库组合、other JSON 往返、目标索引 EXPLAIN、并发调查和回填。保持 SQLite/ClickHouse 兼容，按当前本地 release checklist 的本轮矩阵执行并单独注明未测项，不以 mock/编译替代。生产规模 DDL 和目标 schema-only 演练属于部署前验收。
8. 完成实现后执行相关 `go test`、前端 Vitest、`bun run typecheck`、`bun run lint`、生产构建及上游受保护文件一致性检查。本方案不改 relaykit；若实际修改其 API，追加 GOWORK=off 独立构建。

已查阅本地 `docs/production-environment.md` 和 `docs/release-checklist.md`。生产环境记录为 MySQL 主库、PostgreSQL 日志库，确切生产版本未确认；不能以本机或香港测试实例版本替代生产版本。

依据已核对的 [OWASP ASVS 5.0.0](https://asvs.owasp.org/)、[Authentication](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html#authentication-responses)、[Session Management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html#logging-sessions-life-cycle-monitoring-creation-usage-and-destruction-of-session-ids) 和 [Logging](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html#data-to-exclude)：保留统一认证失败响应、服务端会话和对象权限边界，仅记录必要目标标识，不记录可用凭据，采集失败不影响登录。

## 实现与验证边界

实际实现集中在 `model/audit_security_capture.go`、既有密码/验证码/Passkey 失败出口，以及 `service/auditsecurity/{login_target,rules,store,customer_access,self_access,assignment,login_target_repair}.go`。管理页面复用原单元格、详情字段和事件卡片显示目标账户。个人通知和账户处置仍使用已认证 ActorID。

`login_target_test.go` 覆盖可信目标与尝试文本的命名空间隔离、归属与事件权限、角色和账户隔离、历史修复的证据要求及幂等、CAS 冲突，以及新库/旧版升级和第二次迁移无 DDL。查询计划测试分别验证有界目标索引扫描与主键详情读取；允许优化器采用位图扫描或等价有界计划，不要求 Index Only Scan。

发布前仍须按目标数据库完成 schema 副本演练、完整应用启动及大表索引耗时验证；本地查询计划不能外推生产数据分布和并发负载。SQLite、ClickHouse、独立日志库及 Redis 组合按实际启用范围验收。历史修复需要另行核验该环境证据并显式运行，不因部署新版自动执行。

# 客户审计、告警协作与账户安全处置

2026-11-22 后续实现：[失败登录目标账户](audit-login-target-account.md) 支持本人查看、当前客户员工调查和自动员工归属，并提供仅接受可靠历史证据的维护工具。target 与已认证 actor 独立保存，目标关联不授予封禁、凭据清理或个人通知资格；下文首版仅按 ActorID 的读取说明以新文档为准。历史修复需在指定环境显式执行。

日期：2026-11-17，状态更新于 2026-11-18。**本方案七项范围已完成本地实现及专项验证，未部署。** 员工范围见 [员工查看客户审计与处理风险](employee-customer-audit-access.md)；抽屉、参数说明及回归见 [全功能集成复核后的确认修复](release-frontend-fixes-20261117.md)；自动员工/多人分配、AI IP 观察、个人通知及最终验证边界见 [遗漏功能补齐](audit-security-completion-20261118.md)。下文历史调查及首版处置设计保留其日期语境，不代表当前实现仍有这些缺口。

2026-11-18 状态纠正：可配置自动封禁、两种自动凭据清理及后续边界加固已经在本地实现，分别见 [自动封禁](audit-auto-ban-and-ui-refinement.md)、[自动清理](audit-automatic-credential-cleanup.md)、[边界加固](audit-security-boundary-hardening.md)；本文第 7 节为历史首版设计，不能覆盖后续已确认方案。当前 worker 已无历史来源回扫，保留首次启用起点、120 秒重叠和 IP 待检重试。多人/自动归属分配、AI 请求 IP 观察、个人通知的补齐及逐项验收见 [遗漏功能补齐](audit-security-completion-20261118.md)，未通过本轮验证的项目不标记完成。

本次按用户“审计实际代码逻辑，不要过度设计，也检查相关联逻辑”的要求复核。以当前代码为准，补充 [原安全监控设计](audit-security-monitoring.md)。工作区已有其他未提交改动，不能据此认定本方案已经实施。以下明确区分已验证的问题、既有能力和拟议改动。

## 1. 本次已完成的三项修复

### P1：IP 查询未完成的事件被永久跳过

- 修复前：外查预算在各页重置，耗尽时连缓存也跳过；`Apply` 一旦保存基础规则回执，后续拿到 IP 情报也无法补出告警。
- `service/auditsecurity/ip_provider.go/lookupProviders` 先读缓存，再检查外查预算；`worker.go/scan` 的实时页、待检和修复页共用一个剩余额度。
- `model.SecurityEvent` 只增加 IPCheckState、IPNextAttemptAt 及 `idx_sec_ip_pending`。`ip_rules.go/retryIPIntelligence` 每轮按索引读取最多 20 条到期待检，失败至少延后 60 秒，事件超过 24 小时标 skipped；旧回执保持空状态，不回扫历史审计或消费日志。
- `store.go/commitSecurityBatch` 复用原回执、判定和告警事务：仅 pending 可以补写 IP 情报，基础规则、计数和来源基线不重复执行。IP 结果和 done 状态同时提交，仍检查 scanner 租约/fence；补检不修改实时/修复游标。
- 预算持续不足、供方持续失败时，待检可能到期跳过；未查询不表示安全。本次未增加待检统计 UI，也未实现新 AI 请求 IP 采集。

### P1：API Key 清理的角色校验与禁用不原子

- 修复前：`service/user_api_keys.go` 的角色校验与禁用分开，目标在两者之间提升为同级后仍会被清理。
- `model/user_api_keys.go/DeleteUserTokensBatch` 增加 operatorID，按用户 ID 固定顺序锁定操作者与目标，在同一短事务中核验启用状态和严格角色层级，再禁用本批凭据。竞争提权先提交时，清理被拒绝；清理先提交时，角色修改等待禁用事务结束。
- Redis 栅栏和清缓存仍在事务外；沿用每批 100、冻结 ID 上界、5 秒批次/20 秒调用限时、部分失败和软删除。服务把新权限错误映射为已有拒绝合同，控制器二次验证不变。
- 参考既有 `ClearUserAccessTokens` 锁顺序；没有重写访问令牌清理或增加 relay 认证调用。

### P2：可选负责人不一定能查看和处理告警

修复前只检查启用的管理员角色，没有检查该条告警的 visibility 或 AuditRead / AuditSecurityRead / AuditSecurityManage；未发现分配本身绕过读取权限。现在 `GET /api/audit/security/assignees` 必须携带正数 alert_id，先检查告警可见性，再返回最多 100 名候选中的合格管理员。Root-only 告警只查 Root 候选；其他告警按数据库中的当前授权及 deny 优先规则过滤。

`service/auditsecurity/assignees.go` 的同一校验在候选查询和 `UpdateAlertInvestigation` 保存时复用；候选加载后撤权会使保存失败，清空负责人仍支持。前端 `alert-status-cell.tsx` 的请求和缓存 key 均包含 alert_id，避免串用其他告警候选。本次保留既有单负责人模型；多人和归属员工自动分配尚未实施。

最初的复现代码保留在忽略目录 `_local/audit-security-review-20261117/`，属于修复前的历史证据，不能作为修复后的通过标准。正式回归和验证结果见第 13 节。

## 2. Goals and Scope

以下保留用户已确认的完整扩展需求。七项均已按本文及后续确认的专项设计实施；具体测试范围与尚未执行的上线验收见 2026-11-18 补齐记录：

1. 员工查看当前归属客户的审计日志；未知非空 UA 原样显示。
2. 页面、接口、详情与关联证据具有一致的数据权限。
3. 保留多人负责人：当前归属员工自动负责，其他负责人只能是有权限的管理员；分配不扩权。
4. 监控设置右侧抽屉，解释参数的单位、时间窗口、作用和限制。已补齐 IP 缓存时长，桌面右侧及手机全宽、固定按钮区、验证取消后草稿和焦点、保存回查均已实际浏览器验证。
5. 新 AI 日志持久化后观察 IP，不扫描消费日志大表或历史请求正文。
6. 封禁账户、清理访问令牌、清理 API Key 支持人工和自动，分别可配置。
7. 本人资料增加账号安全通知，使用现有邮箱、Webhook、Bark、Gotify。

设计收敛：复用事件回执、告警、动作记录、投递表与现有 worker；仅多人分配需要新关系表。不新增独立处置任务表、通用工作流、账户摘要副本、归属同步任务、专用个人通知限额系统或额外采集调度器。已有错误处理、权限复核、缓存失效和数据库兼容要求仍需保留。

## 3. Existing Logic and Related-Path Findings

下表记录 2026-11-17 设计前调查。多人单选、个人区域隐藏、管理员通知门控等缺口已在 2026-11-18 修复；不将历史描述作为当前状态。

| 已核实的调用链 | 既有行为及复用边界 |
| --- | --- |
| `router/api-router.go` → `controller/audit.go`、`audit_filters.go` → `model/audit_log.go` | 管理审计要求 AdminAuth + AuditRead；本人审计强制当前用户。已新增独立员工客户接口，按当前归属授权及时间/ID 游标分页，未将原管理列表的 COUNT + OFFSET 移植到客户列表。 |
| `model/log.go/getEmployeeCustomerLogUserIds`、`employee_export.go`；`controller/employee.go`、`model/customer.go` | 当前客户归属依据 users.inviter_id；客户资料副表可能滞后。有效员工、普通客户及排除员工客户的检查需复用业务条件，不能直接拿导出权限或缓存的员工资料当授权。 |
| `model/audit_log.go/ProjectAuditLogs`；`controller/audit.go` 的管理审计入口；`service/auditsecurity/rules.go/Normalize` | 用户操作人和管理对象不同；管理审计记录操作者，不能把 target_id 当作客户操作。RootInfo/AdminInfo/AuditInfo 和字段变更投影已有边界。 |
| `service/auditsecurity/{store,investigation,alert_query}.go`；`model/audit_security.go` | 已有乐观版本、动作历史、判定快照、关联事件、归档/关闭时间、游标和分批清理。ActorID 在可信账户规则中可复用，不需复制 account_user_id。普通管理员仍不能直接读 Root-only auth 原文。 |
| `service/auditsecurity/worker.go`；`model/audit_query.go`、`model/main.go` | 既有一个 supervisor、最多一个周期 worker；500 行来源页、20 事件提交、租约/fence、120 秒重叠及有界 24 小时修复。当前读取池初始化只负责连接，安全表由独立迁移入口处理；不能依赖监控开关替代 schema 初始化。 |
| `model/relay_log_pipeline.go/insertRelayLogs`、`model/log.go/createLog` | 消费/错误日志有后台写入及恢复路径；成功确认后可观察 IP。当前实现已记录 ClientIP，历史空值不能补造。观察不能阻塞结算 continuation。 |
| `middleware/request_logger.go` → `model/request_log_segment.go/Record` | 请求诊断是后台批量文件写入及索引登记，不能把扫描消费日志当作覆盖此来源；观察在写入成功和索引处理完成后、索引锁之外。 |
| `model/user_session.go/RotateUserSessionRefresh` → `service/auth_session.go` | 刷新竞争、无效凭据和已验证重放分支不同；重放已经撤销对应会话并更新认证栅栏。监控只决定额外账户处置，不重复实现会话防重放。 |
| `model/user.go/UpdateWithTx`；访问令牌/API Key 清理服务 | 已有 auth_version、用户缓存更新、会话撤销、PAT/旧令牌清理及 API Key 栅栏。通用 User.Update 会回写多个字段，封禁需窄更新，不能把旧对象的角色/额度写回。 |
| `service/security_verification.go`；`router/api-router.go` 用户管理组 | 已有禁用、两类清理的二次验证 scope；管理接口要求 AdminMenuUsersView。监控入口复用这些条件，不另外设计认证协议或新增一套 Respond 权限。 |
| `service/auditsecurity/{store,worker,notify,delivery_lifecycle}.go` | 管理员通知的入队、worker 调用、发送前检查三处均受管理员开关影响。个人投递必须分别判断，不能只加 UserSetting 字段。已有投递认领、5 次重试和退避可复用。 |
| `service/user_notify.go`、`notify-limit.go`、`account_security.go` | 已有四类通道和按用户/通知类型限流；缺少目的地时 NotifyUser 返回 nil，不代表已发送。新调用需传递 context 并区分跳过/失败。密码与绑定变更的必要安全通知保持原行为。 |
| `web/src/features/usage-logs/audit/`；`profile/components/tabs/notification-tab.tsx` | 未知 UA 展示及员工客户访问见员工专项；负责人仍为单选，候选缓存带 alert_id。设置现为 `security/settings-drawer.tsx` 的右侧 Sheet，二次验证 Dialog 嵌套其中；个人通知区域仍受 quotaRemindEnabled 或管理员条件控制，新账号通知要独立显示。 |

## 4. 生产样本与边界

仅读用户指定 `C:\Users\li bai\Desktop\datalog`，保留聚合统计，不保存账号/IP 原文；三个审计文件有重叠，不能相加。

| 样本 | 已验证结果 |
| --- | --- |
| audit_logs_20261116.csv.gz，全量 106,278 行 | 105,161 行 access_token；181 个用户/IP 组合；53 条 login 均成功。 |
| audit_logs_last7days.csv，全量 797,004 行 | access_token 约 98.97%；874 个用户/IP 组合；空 UA 4,107 行。 |
| audit_logs_last2months.csv.gz，全量 2,939,596 行 | 实际约 26 天，非两整月；access_token 约 99.22%；1,545 个用户/IP 组合。 |
| consume_logs_last15days.csv.gz，仅顺序前 100,000 行 | 约 37 分钟、空 IP 67,216 行。非随机抽样，不能外推 15 天总量或缺失率；未全扫约 1.84 GB 压缩文件。 |

正常 PAT GET 占多数，不凭次数或未知 UA 判定账户被盗。样本不能证明认证失败采集已开启。channel.update 的 changed_fields 不能替代实际字段前后值。主库 MySQL、日志库 PostgreSQL 见 [生产环境](../production-environment.md)；未连接生产，数据库版本尚待环境记录补齐。

## 5. 员工范围、告警投影和多人分配

2026-11-17 补充：用户确认员工可查看当前归属客户自己的操作日志和风险处理历史，并修改状态、结论、排查备注；不包含管理员对客户的管理操作。此子集的实现与验证以 [员工客户审计与风险](employee-customer-audit-access.md) 为准；多人分配、自动处置与 AI IP 观察仍是后续范围。

**客户审计：** 新请求每次实时核验操作者启用、员工档案启用；目标为当前 inviter_id 指向该员工的普通非员工客户。禁用客户仍可调查，提升为管理员/员工、转移或删除后失去客户访问资格。一次查询一个客户，默认 24 小时、最多 31 天，20 条/页、最多 100，按时间/event_id 游标，不 COUNT、不巨量客户 IN、不增加员工导出。

服务端强制 UserID 和客户安全投影，客户端 actor_role/self_view 等参数不能扩权；管理员对客户的内部管理事件不因 target_id 匹配而自动公开。写操作在同一短事务内实时复核归属，版本化更新告警。原先“锁用户行串行化转移”的提议撤回：relay 额度回退也更新 users 行，新功能不得增加该行锁竞争。转移提交后的新请求按新归属拒绝，已经授权的在途操作可能完成。

**账户告警：** 用“可信规则 + 触发快照的 ActorID”确认账户，限定成功登录相关规则、已验证会话重放、新 AI IP 观察；匿名失败用户名、跨账号喷洒和管理操作目标不作为授权依据。账户规则合并加入已有 actor_id 条件并升级规则版本，防止用户名复用导致跨账号合并；旧证据不能证明同一账号的告警不开放员工范围，不回扫历史补造归属。

员工列表/详情返回服务端构造的白名单 DTO：本账号时间、规则、风险、状态、结论及处置结果；不返回跨账号原始证据、内部备注、凭据或管理元数据。复用已有快照，无需持久化另一份摘要。员工可以更新状态/结论并追加排查备注；备注记入现有 SecurityAction，不能覆盖或读取管理员 ConclusionNote。处理历史保留管理员/系统的安全状态变化和通用来源，仅员工来源显示备注；隐藏管理员身份与指派信息。管理员原有可见性保持；仅 Root 可见的原始告警，其额外管理员负责人也只能选实际有权限的 Root。

**分配：** 新关系表只保存手工管理员，当前归属员工由实时客户关系派生。返回 `assignees:[{id,username,role,source}]`，source 为 owner/manual；最多 10 名人工作为输入上限，自动员工另计。员工只读显示，转移客户自然换人，不建自动关系回填/同步任务。

候选接口必带 alert_id；启用管理员须同时有读取、处理权限且能看到该告警，复用 `authz.ReadCurrentPermissionsFromDB` 的当前权限及 deny 优先规则。保存重新检查，其他员工不可选。手工 `assignee_ids` 省略=不改，空数组=清空人工分配；自动员工不受影响。沿用 alert.version、动作记录、活动游标失效。旧单人字段作为读取兼容回退，写多选时同步首位人工负责人/0；无须启动历史分配批量迁移。

## 6. Data Flow：AI IP 观察与漏检修复

`持久化成功 → 非阻塞最小元数据入队 → 现有采集周期归并 → 紧凑 audit_logs 观察 → 现有增量扫描/事件回执 → IP 规则 → 告警 → 独立决策通知和处置`。

- 在消费/错误日志成功确认及诊断日志成功落盘入口复制 UserID、IP、时间；不复制请求 ID。覆盖兼容单条写入、重试成功、近期恢复成功路径，超过 120 秒的历史回放不采集。无有效用户/IP、非 AI 请求不进入检测，不复制正文/Header/gin.Context。
- 开关关闭立即返回；开启也只做有限标量复制和非阻塞 channel 投递。新 IP 队列与认证队列分开，固定容量 1,024；沿用现有周期先处理认证、再处理 IP，不新增 per-request goroutine 或新调度器。
- 采集端规范化公网 IPv4/IPv6，按用户/IP/10 分钟桶合并；缓存上限 10,000 项，稳定观察 ID 用于跨节点/重复写入去重。同桶重复不是请求次数；不把 AI 出口写入人的登录/设备/不可能旅行基线。
- 复用现有 writer 池，IP 新观察每节点最多 5 行/秒，2 秒写超时，排队超过 120 秒丢弃；丢弃/空 IP/滞后进入健康状态。暂停/关闭停止新采集；不承诺补齐退出、队列满或日志未保存造成的覆盖缺口。
- 不扫描消费 logs、不遍历旧文件、不建立永久 IP 库。新增 `security / relay.ip_observed` 来源及候选规则，不落入高危操作计数。ClickHouse 来源不能假设 event_id 唯一约束有效，仍由主库事件回执去重。

**补齐实际漏检：** enrichment 先排除基础和 IP 均完成的事件；缓存查询不受外查剩余额度阻止，实时/待检/修复共享同轮预算。基础规则仍按 event_id 只执行一次；IP 检测在同一已有回执上记录 pending/done/skipped 和 next_attempt_at。新 pending 只保留 24 小时内事件、按到期索引有界重试，失败延后至少 60 秒；过期标记未完成，不能显示安全。旧回执默认无待检状态，不触发历史回扫。

只有取得有效结果才提交 IP 检测完成；IP findings 与完成标记在同一带租约校验的事务中写入，避免修复重复累计。延迟检测只补 IP 结果，不重跑登录失败计数、设备首次出现或自动处置。晚到地理结果不倒退最新登录位置基线；缺失的历史轨迹不宣称已补齐。供方超时、限额、熔断时保留待检，基础规则和来源游标仍可推进。

## 7. 人工与自动处置：复用单项操作

本节为首版方案记录。实际策略范围、三个独立自动开关、处置恢复和停止语义已经由后续确认的 [自动封禁](audit-auto-ban-and-ui-refinement.md)、[自动凭据清理](audit-automatic-credential-cleanup.md)、[边界加固](audit-security-boundary-hardening.md) 替代；不能按下面的首版白名单或 unknown 恢复建议覆盖现有代码。

| 操作 | 范围和复用方式 |
| --- | --- |
| disable_account | 短事务校验当前目标，窄更新 status/auth_version；沿用用户缓存栅栏及浏览器会话撤销。不得回写角色、余额、额度；不强行中断已输出的流。解封仍走用户管理。 |
| clear_access_tokens | 复用 PAT + 旧访问令牌撤销；不是 API Key，也不代替浏览器会话管理。 |
| clear_api_keys | 既有管理接口权限竞争已修复；未来监控入口复用禁用→缓存栅栏→软删除，每批 100、冻结开始 ID 上界、单次总限时 20 秒。之后新建 Key 不在本次范围。 |

人工每次提交一项动作。权限为现有 AdminAuth、AuditRead、AuditSecurityRead、AuditSecurityManage、AdminMenuUsersView，加浏览器会话和对应既有二次验证 scope；当前操作者必须高于目标，不允许操作自己/Root。目标由可信告警导出，不接受任意 target_user_id。验证 alert.version 后执行，配置和角色在实际修改前再检查；员工仅做排查，不授予破坏性操作。

自动模式已由用户确认需要。**首版触发白名单仍为本方案建议**：仅完整、实时且不超过 120 秒的已验证 session_reuse，目标为当前普通非员工用户。认证层已经撤销涉事会话，监控只执行被开启的额外动作。失败登录、VPN/代理、AI IP 风险、共享出口、权限失败、未知 UA、历史修复都只告警，不能据此自动清空凭据或封号。

在现有 SecurityAction 增加执行去重键、结果状态及有界结果，不建多步骤任务表。自动动作随新判定记为 pending，复用 worker 每轮最多认领一项，在扫描前做一次有界调用；人工操作直接执行并返回结果。每项 auto 开关独立生成一条动作，去重键绑定触发事件/动作；人工键绑定操作者、告警及 request_id。三种动作互不作为对方的成功前提。

结果明确区分 pending/running/succeeded/partial/failed/unknown/skipped；保存目标、已确认数量、API Key 上界和原因码，不存凭据或整份配置。相同请求只返回原结果，**不自动重放破坏性操作**。进程退出或结果落库不明的 running 记录改为 unknown，由管理员检查后发起新操作；部分禁用/删除不显示全部成功。后台最多一次自动尝试，不增加检查点恢复、阶段调度或自定小时配额系统。

执行在告警事务外，不持锁调用 Redis/外部网络；自动执行走明确系统策略分支，不伪造 Root 会话。暂停、关开关、事件过期、目标升权后未开始动作标 skipped；已提交禁用的缓存收尾仍完成。操作结果写活动记录，不自动结案，不回馈触发新自动处置。清理需排除有效 running 操作，过期 unknown 不无限阻止既有保留清理。

## 8. 账号安全通知

`UserSetting.account_security_notify_enabled *bool` 默认未订阅，缺省请求保留原值；沿用本人设置接口，不创建新配置接口。普通用户即使站点不启用余额提醒，也能设置通知通道和账号安全开关。

只对可信触发账号发送白名单摘要及本人的处置结果，不根据管理 target_id、匿名用户名或共享 IP 猜测收件人。链接指向本人安全与访问页面。密码/绑定变更的既有必要安全提醒不受此开关控制。

复用 SecurityDelivery，增加 `kind=account`、recipient_user_id 和 nullable 去重键。个人与管理员入队分别判断；worker 和发送前判断都按 kind 区分，管理员 notification_enabled/收件人缺省不能阻断已订阅个人。原管理员每小时计数只统计管理员投递；个人复用 `notify_limit:<user_id>:account_security` 及现有通知限流设置，不再建一套每人小时统计。

目的地只保存配置 HMAC 指纹，发送时读取最新偏好/通道；取消订阅、变更地址或删除账号取消旧消息，禁用账号仍可收到自身处置结果。新增 context 发送入口，保留旧调用兼容；Redis、HTTP/SMTP、Worker 转发都传递实际 deadline，沿用 URL/DNS/重定向防护。缺少目的地是 skipped，不能把 NotifyUser 的 nil 当 sent。

个人每轮最多 3 条、整批外发预算 8 秒，复用 5 次重试与既有退避；单个收件人限流只延后该投递，慢端点不会占满整轮 25 秒而饿死管理员通知。个人同告警冷却以实际 sent_at 判断，发送前再次检查，不与管理员 NotifiedAt 混用。发送后结果入库失败仍可能重发，这是既有至少一次语义；通知失败不回滚告警或处置。

## 9. API Contracts 与页面状态

沿用 `{success,message,data}`，业务/参数错误用 ApiErrorI18n；认证/权限由中间件处理 401/403，存储错误记录真实原因并返回通用提示。越权对象不返回额外数据。

| 接口 | 请求、响应与权限 |
| --- | --- |
| GET `/api/user/employee/customers/:id/audit` | UserAuth + 浏览器会话 + 实时员工/客户归属；start/end、limit/cursor 及现有安全筛选；返回 items/next_cursor/has_more。路由筛选沿用文本输入，不新增路由聚合接口。 |
| GET `/api/user/employee/customers/:id/security/alerts`、`/alerts/:alert_id` | 同上，单客户有界查询；仅第 5 节 DTO，不开放原始关联、全局风险统计、导出或归档接口。 |
| POST `/api/user/employee/customers/:id/security/alerts/:alert_id/actions` | 同上且事务内复核；version/status/conclusion/note，note ≤ 1,000 字；拒绝分配、内部备注及处置字段。 |
| 既有 GET `/api/audit/security/assignees` | 必带 alert_id 和逐告警资格检查，既有 Read + Manage，响应 `{id,username,role}[]` 只包含人工候选管理员；自动员工在告警 DTO 中派生并只读展示。 |
| 既有 POST `/api/audit/security/alerts/:id/actions` | 增加 assignee_ids，保留旧 assignee_id 输入兼容，二者同时出现拒绝；省略与空数组含义不同，沿用版本冲突提示。 |
| 新 POST `/api/audit/security/alerts/:id/responses` | 第 7 节管理员条件及现有 proof；version/action/request_id；返回 action_id/outcome/confirmed_count，未知数量不填成功数。结果从既有告警动作详情读取，无需新任务查询 API。 |
| 既有 GET `/api/option/?scope=security.audit-monitor`、PUT `/api/option/group` | 沿用 Root、设置权限、二次验证和配置快照；不加自定义保存端点。 |
| 既有 PUT `/api/user/setting`、GET `/api/user/self` | 仅本人，新增可选账号安全通知偏好，保留其他偏好。 |

员工路由放既有 UserAuth 组，不放宽 AdminAuth；新员工和处置端点不加入 PAT 白名单。候选及详情缓存 key 包含 viewer、权限、客户、alert_id；身份变更/403 时取消请求、删除旧缓存，不展示上一个客户的占位数据。非 Root UI 不因为获派而获得原始证据入口。

未知 UA 保留原始文本，识别用的截断不截掉原文；显示沿用省略、完整提示和复制，空值才显示“未记录”，不把 UA 传进 t() 或 HTML。右侧抽屉复用现有 Sheet/ConfigDrawer、按钮区及字段说明，保存失败保留草稿，验证后恢复焦点；七种前端语言及后端语言同步，不改上游拥有的认证/HTTP 文件。

## 10. Data Model、索引、迁移与保留

| 对象 | 最小调整与访问模式 |
| --- | --- |
| audit_logs | 新紧凑观察类型；扫描复用时间/event_id 索引。客户列表用 `(user_id,actor_role,created_at,event_id)`；先在 schema 副本验证既有索引覆盖，确需补充才增加。消费 logs 不加列/索引。 |
| audit_security_events（本次已实施） | IPCheckState、IPNextAttemptAt；待检索引 `(ip_check_state,ip_next_attempt_at,event_id)`，只查有限 pending，复用现有 48 小时回执清理。旧状态空值不回填；两字段不对外序列化。 |
| audit_security_alerts | 不加 account_user_id 或摘要列；账户合并补 actor_id 校验。客户查询需 `(actor_id,last_seen,id)`，现有 visibility-leading 索引不能直接承诺覆盖员工范围；以真实 EXPLAIN 验证。 |
| 新 audit_security_assignees | 仅手工管理员：`(alert_id,user_id)` 复合主键，按 user_id 筛选用 `(user_id,alert_id)`；列表有界批量取，自动员工实时派生。 |
| audit_security_actions | nullable ExecutionKey 唯一索引、Outcome、Result（有界 TEXT）；`(outcome,id)` 用于少量自动 pending/running；分配快照放 Result，不加 audience 或任务关联表。旧记录 ExecutionKey 为 NULL。 |
| audit_security_deliveries | RecipientUserID、nullable DeliveryKey 唯一索引；待发合并 `(alert_id,recipient_user_id,created_at)`，实际发送冷却 `(alert_id,recipient_user_id,sent_at)`；已有 sent_at/due 索引继续用于管理员限额和投递。 |
| users.setting | JSON 中的个人偏好，无用户表新列或 relay 热路径索引。 |

使用 GORM、common JSON、跨库 TEXT/NULL、lockForUpdate；不引入 JSONB/部分索引/MySQL TEXT 默认值。新增结构通过现有迁移入口管理，旧分配读时兼容，不新增历史账号推断或后台大批回填。

清理沿用 closed_at、归档保留和现有分批流程，增加负责人关系删除；动作与投递已在其生命周期内。进行中的告警不按 last_seen 删除。先确认动作结果，再允许清除仍在执行的操作记录；关配置不反向撤销已完成处置。回退先关新功能并结束当前调用，保留新结构；旧版本只能表达首位负责人，回退期间禁止旧界面覆盖多选。

按 [发布清单](../release-checklist.md) 验证真实 MySQL ≥5.7.8、PostgreSQL ≥9.6、SQLite 的新建/旧版升级/二次启动无 DDL；生产 MySQL 主库 + PostgreSQL 日志库组合及 ClickHouse 来源写入也在范围内。新增索引先在生产 schema-only 副本验证 DDL/查询计划，不直接到生产试错。

## 11. Config Parameters 与说明

新增项均进 `setting/operation_setting` 的 audit_security_setting，配置白名单、范围校验、验证快照与现有保存接口同步。三个 allow_* 和对应 auto_* 默认全 false，auto 开启必须 allow 开启。relay_ip_enabled 默认 false，依赖监控和 IP 情报开启；固定队列/批量上限不变成新环境变量。

| 抽屉项目 | 必须向用户说明 |
| --- | --- |
| 启用、采集认证事件、暂停 | 检测与认证采集不同；暂停停止新工作，必要缓存收尾仍执行；启用不等于自动处置。 |
| 扫描间隔（秒，现有 5–60） | 增量检测最短间隔，受批量和耗时影响；不是每条日志的完成保证。 |
| 登录失败、高危操作、密钥查看阈值 | 分别解释 5 分钟失败次数、5 分钟同操作者/资源操作次数、5 分钟不同渠道数量；失败后成功规则使用 15 分钟关联窗口。正常 PAT GET 不计高危操作。 |
| AI 请求 IP 观察 | 只覆盖开启后成功保存且进入队列的新数据，按用户/IP 合并；无法覆盖时可查看丢弃/滞后，不扫描历史大表。 |
| IP 查询超时/每轮上限 | 秒/外查次数；实时、待检、修复共用，缓存不耗外查次数；未查询不表示安全。 |
| IP 缓存和服务 | 当前本地实际最多 60 秒，不展示无效的 1,440 分钟可编辑承诺；自建服务承担长期缓存。服务地址、能力、每日额度、私有主机精确白名单说明清楚；密钥留空保留、显式清除。 |
| 移动速度/最小距离 | 公里/小时与公里，两条件共同判定，只用于人的成功登录，AI 服务器出口不参加。 |
| 管理员通知/冷却/小时上限 | 管理员收件人及限额；个人账号通知按本人订阅和现有个人通知限流，不依赖管理员收件人。 |
| 三种允许处置及自动开关 | 每行明确封禁、访问令牌、API Key 范围；自动首版仅可信会话重放，开关关闭时人工和自动都不能新执行。 |
| 普通/归档保留天数 | 按结案时间保留，进行中告警不删；与原始审计日志保留期不同。 |

## 12. Main Chain Impact

**本次三项修复：** 没有增加或改动 relay 中间件、日志采集 hook、同步 DB/Redis/外部调用或 per-request goroutine。IP 读写继续在已有监控 worker 及独立池运行；新增查询只访问有界回执主键集合或 pending 复合索引。API Key 清理确实访问共享 users/tokens 和既有 token 缓存：只锁两个主键用户行、处理最多 100 个 Key，锁内不调用 Redis；与管理改角色的行更新串行，正常 relay 缓存读取没有新增锁。负责人权限读取只在管理接口访问 users/casbin_rule，不修改认证缓存。没有新增连接池、队列或常驻 goroutine。

以下是尚未实施的完整扩展影响分析：

同步：AI 请求认证、路由、上游发送、流式输出和计费无新增 DB/Redis/外部调用。观察在已有日志持久化成功后，仅一次有限非阻塞投递；兼容单条入口同样不能做 IP 查询/JSON 编码或创建 goroutine。失败内部捕获，不传播到日志/结算 continuation 或 relay 响应。

异步：归并、紧凑观察写入、IP 情报、告警、通知和处置由现有固定 worker 承担。慢情报可降低覆盖率，不能反压业务；暂不另拆采集线程或扩大连接池。处置对后续认证的禁用是用户配置的业务行为。

### Shared Resource Audit

| 资源 | relay 关系与处理 |
| --- | --- |
| logs/日志写入队列、诊断文件/索引 | 业务共享；不新增查询/文件句柄/正文复制，观察在成功和锁释放之后，不占结算 continuation 做监控。 |
| audit_logs / audit_security_* | 与业务可能同实例；使用已有独立池，不新扩池。当前查询池 main/logs/employees 各最多 2，worker 主库最多 2、来源/写入各 1；连接隔离不等于磁盘隔离。 |
| users、tokens、user_access_tokens、user_sessions | 认证/计费共享；主键或有界 user_id 查询，窄更新和最多 100 Key 批次，锁内不调用 Redis，不回写余额；授权查询不放每次 AI 请求上。 |
| 认证缓存/栅栏 | 有意复用 user:<id>、auth:user:fence、auth:user:version 及既有 profile version；token:<HMAC> 和 token:fence。清理必须沿用原认证协议，不另造不生效的缓存副本。 |
| IP 队列/缓存 | 监控独立、有界，与认证采集队列分开；不保存请求对象、不增加共享 goroutine 池压力。 |
| 通知限流 Redis | notify_limit:<user_id>:account_security，独立于认证与余额类型；传递真实 deadline，不用另起 goroutine 假装取消底层调用。 |

### Concurrency Analysis

30k RPM 约 500 请求/秒，100k RPM 约 1,667 请求/秒。两种日志同时成功保存时每请求最多约两次非阻塞投递；新增同步 DB/Redis/网络调用为 0，per-request goroutine 为 0。每节点缓存固定上限，极端新 IP 以可观测丢弃降级，不无限积压。

新增审计写量上限 5 × 节点数/秒；单节点极限仍有 432,000 行/天，必须结合实际去重率、节点数和保留期检查存储。现有 500 行/10 秒仅为理想读取上限，不保证有 IP 外查时达到。外查所有来源共用现有预算，自动每周期一项有界操作、通知最多三条。监测来源滞后、丢弃、连接等待、日志写入/首包 p95/p99；积压时停新 IP 观察，不靠扫描大表或增加并发补救。尚未做 30k/100k 负载验收。

## 13. Error Handling 与验证记录

权限/版本冲突提示刷新，参数错误提示修改；采集缺口、IP 待检、通知跳过与处置部分成功分别显示，不能统称已完成。请求日志用 logger(context)，后台用 SysError，不输出凭据、原始样本或内部 SQL 给用户。

修复前历史复现（不是当前验收命令）：

```text
go test -overlay ./_local/audit-security-review-20261117/overlay.json ./service/auditsecurity -run '^TestReview' -count=1 -v
# 2 项通过：成功复现零预算丢缓存、已有回执阻止补检。
go test ./_local/audit-security-review-20261117/credential_review_test.go -run '^TestReview' -count=1 -v
# 1 项通过：成功复现校验后角色提升仍被清 Key。
```

最初复现仅使用内存缓存和临时 SQLite。本次修复已追加真实数据库回归，结果如下。

### 2026-11-17 修复验证

所有数据库均为本机 Docker 测试实例内本次新建的隔离库，测试结束后删除。MySQL **26.10.0**、PostgreSQL **18.7**、SQLite **3.50.4**；API Key 缓存测试使用单独临时 Redis 7 容器。没有连接生产、处理真实账户或调用外部 IP 情报服务。

| 验证命令/范围 | 结果 |
| --- | --- |
| `go test ./service/auditsecurity -count=1 -timeout=8m -v`，分别使用 MySQL/PostgreSQL worker 库；来源测试同时覆盖 SQLite/MySQL/PostgreSQL | 两次各 51 个顶层测试通过；1 个顶层 ClickHouse 专用测试及来源 ClickHouse 子用例未运行，未计入通过。 |
| `go test ./model -run '^TestUserAPIKeys' -count=1 -v`，MySQL 主库 + PostgreSQL 日志库，以及反向组合，均启用隔离 Redis | 两次各 6 项通过：分批/上界、其他用户隔离、Redis 故障后重试、取消/数据库错误、并发删除、当前角色校验、事务阻塞后的并发提权拒绝（含子用例）。 |
| `go test ./controller -run '^Test(AdminClearUserAPIKeys\|RejectNegativeSecurityAssignee\|SecurityAssigneeQuery)' -count=1 -v`，分别用 MySQL/PostgreSQL | 两次各 7 项通过；覆盖二次验证、角色层级、批次间撤权、部分失败、会话要求及非法负责人查询。 |
| SQLite `TestSecurityAssignees* / TestIPRetry* / TestIPCache* / TestLegacySecurity*` | 5 项通过；IP 补检不重复基础告警、不倒退扫描游标、过期租约不能提交、待检退避和过期均覆盖。 |
| `TestLegacySecuritySchemaUpgradeAndRestart`、`TestMonitoringDatabaseLifecycle` | 三种数据库均覆盖建表/旧结构升级和二次迁移无 DDL；保留旧回执，不自动安排历史 IP 回扫。 |
| `TestPendingIPQueryUsesDueIndex`，10,050 条回执、10 条待检 | MySQL 使用 `idx_sec_ip_pending` range；PostgreSQL 使用该索引 Index Scan。相关历史查询、来源扫描和告警并发更新原合同也通过。 |
| `bun run test -- src/features/usage-logs/audit/security/monitor.test.tsx` | 12 项通过，包含同一页面切换告警后只展示该告警候选。API 为组件测试 mock，不冒充浏览器端到端。 |
| `bun run typecheck`（隔离 Bun 1.4.4）、`bun run lint`、`bun run build`、`go build ./...` | 均通过；lint 有 8 条既有警告。系统 Bun 1.3.13 不支持 `bun check`，初次退出后已用项目版本完成标准检查，没有修改 package.json。 |

可复现入口及原始测试输出位于忽略目录 `_local/audit-security-fix-20261117/run_checks.py` 和同目录日志；脚本只允许已知本机测试容器，DSN 不写入记录。前端上游拥有的认证/HTTP 文件未修改。新增代码复用既有表和规则，没有新增通用任务系统。

验证边界：未做生产 schema-only 演练、生产规模 DDL/负载验收、真实浏览器端到端或 ClickHouse 实例测试；本次没有发布部署。当前最老受支持 MySQL/PostgreSQL 版本未另起实例验证，不能由当前版本通过外推所有版本。

2026-11-18 后续验证已覆盖员工转移/停用/升权、多人分配权限/旧字段/并发版本、成功持久化才观察 AI IP、队列降级及不污染登录基线、个人通知门控/四通道/变更/取消/冷却/限流/重试、真实页面与接口。多人分配和个人冷却索引的真实 MySQL/PostgreSQL EXPLAIN 已验证；relaykit 本轮增加可选通知偏好并完成独立构建。最终范围与证据以 [补齐记录](audit-security-completion-20261118.md) 为准。

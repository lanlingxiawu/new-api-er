# 安全监控遗漏功能补齐与验收

日期：2026-11-18。用户本轮明确要求继续完成归属员工自动分配，并检查、补齐遗漏。沿用已确认的 [完整需求](audit-security-customer-response.md)，不把旧文档中的“设计”当作实现证据。尚未部署。

### 2026-11-18 IP 记录要求复核

用户再次明确：AI 请求 IP 必须记录，无论用户个人开关开启还是关闭。该行为已由 [AI 请求固定记录来源 IP](ai-request-ip-logging.md) 实现：消费/错误日志构造直接保存 `ClientIP()`，不读取 `record_ip_log`。本轮新增的 `relay_ip_enabled` 只决定已保存 IP 是否进入后台风险分析，个人安全通知开关只决定是否通知；两者均不能关闭原始日志的 IP 记录。此前交付说明把它们概括为“均默认关闭”，没有区分记录与分析，现予纠正。沿用已确认的仅后端规则，个人设置页面保持原样。

## 需求与当前证据

| 需求 | 本轮开始时的实际状态 | 本轮验收 |
| --- | --- | --- |
| 员工客户审计、风险调查及权限隔离 | 已有 customer_access.go、员工专用接口及前端 | 真实登录/API 验证本人、当前客户、外部客户拒绝；三库专项回归通过 |
| 当前归属员工自动负责、最多 10 名人工管理员协作 | 未实现；只有 assignee_id，保存只接受合格管理员 | 已实现；浏览器多选保存、刷新、清空人工保留员工，三库归属变更/权限/并发测试通过 |
| 右侧监控设置、参数说明、固定标签容器 | 已有 settings-drawer.tsx | 新 IP 开关接入；手机短屏标签切换、滚动和固定按钮验证通过 |
| 新 AI 请求日志持久化后的 IP 观察 | 基础 IP 记录已独立于用户开关；尚缺持久化后的风险观察入口 | 已实现；三处持久化入口、写失败/恢复、开关/去重/隐私及身份边界通过 |
| 人工/自动封禁及两类凭据清理 | 后续专项已实现；旧总设计的“未实现”已过时 | 沿用既有授权实现；auditsecurity 整包回归通过，并移除界面“不会封禁”的过时提示 |
| 个人账号安全通知与四种既有通道 | 未找到 account_security_notify_enabled 或个人投递分支 | 已实现；三类角色页面保存/刷新/退订，通道、冷却、重试、恢复及脱敏专项通过 |
| 我的安全、详情去除嵌套线框 | 已实现并完成上轮专项 | 手机列表和只读详情回归通过，事件使用分隔线，无两层卡片框 |
| 前端检查报告 F01–F06 | 报告明确为检查发现，未修复 | 六项均修复并在生产构建浏览器验证；另修复多选搜索后继续选人的交互 |

## Existing Logic and Related-Path Findings：分配

- `model/employee_customer_audit.go:RequireEmployeeCustomerAccess` 以当前 `users.inviter_id`、启用用户和员工档案、普通非员工客户为准；客户禁用仍允许调查。自动负责人采用同一条件，不采用可能滞后的客户资料副表。
- `service/auditsecurity/customer_access.go:customerAlertAccessible` 已对可信规则、账户及角色隔离版本、旧判定快照完整性进行校验。归属负责人必须通过同一身份校验；匿名失败、跨账号证据、管理操作目标不得推导归属。
- `assignees.go` 已校验启用管理员、逐告警 visibility、当前 AuditRead/AuditSecurityRead/AuditSecurityManage（deny 优先）。Root-only 告警只能手工分配 Root；自动员工仅通过原有客户脱敏入口操作，不获得原始管理数据。
- `investigation.go` 在短事务中按 version 比较更新，追加 SecurityAction，并推进风险/活动 revision；分配扩展接入同一事务。员工更新接口拒绝分配字段。
- `alert_query.go`、`summary.go`、`export.go`、`store.go:GetAlert`、列表/详情/处理弹层都仍依赖单人字段；必须一并更新，避免只在编辑器支持多人。
- `model/audit_cleanup.go` 分批删除告警及子记录；新关系表需一并清理。自动归属实时派生，不增加同步任务、回填或用户行锁。

## 分配实现与生命周期

1. 新增 `audit_security_assignees(alert_id,user_id)` 复合主键与 `(user_id,alert_id)` 索引，仅保存人工管理员。使用现有迁移入口，旧 `assignee_id` 保留读回退；首次多选写入时同步首位 ID/0，不扫描历史告警回填。
2. 列表/详情读取有限页后批量加载人工关系和姓名；归属员工由当前用户/档案关系及可信告警身份派生。返回 `assignees:[{id,username,role,source}]`，source 为 owner/manual。历史处理记录保存当时的分配快照，不随客户转移改写历史。
3. POST `/api/audit/security/alerts/:id/actions` 增加可选 `assignee_ids`；省略保留、空数组清空人工分配、最多 10 个不重复正整数。与旧 `assignee_id` 同时提供拒绝。保存逐人复查权限与告警可见性；并发过期 version 回滚全部关系和历史。
4. 自动员工不在人工候选中，不可手工移除。转移客户、员工停用、客户升权/成为员工/删除后立即按新关系派生；没有有效归属时显示未分配（人工负责人仍保留）。分配不改变任何授权规则。
5. 负责人筛选及统计识别任一人工负责人和当前自动员工；未分配筛选须没有两种负责人，不能只检查旧单人字段。所有查询继续使用已有角色、时间范围、分页及 purge 条件。
6. 前端复用 MultiSelect、既有处理 Popover、姓名展示和主题样式；自动员工单独只读展示。候选失败允许重试，保存状态/备注时不无意清空负责人，背景刷新不覆盖正在编辑的版本与选择。
7. 沿用原管理鉴权与仅会话写入；没有新公开接口、配置或通知承诺。业务错误复用 invalid_assignee / conflict；存储错误记录并返回通用不可用提示。

## Main Chain Impact / Shared Resource Audit

分配仅发生在管理/客户查询及调查写入，relay goroutine 无新逻辑。新关系表、告警及动作表只供审计子系统使用。读取 users/employee_profiles/casbin_rule 使用已有独立审计池、有限主键/索引查询，不更新用户、额度或认证缓存，不加用户行锁。没有 Redis、新连接池、后台同步或每请求 goroutine。

## 测试设计（先于实现）

- 归属：新/旧可信告警、混合/匿名/未来版本、无归属、转移、员工账号/档案停用、客户禁用/删除/升权/成为员工；管理员不能借分配读 Root-only 原文。
- 人工：0/1/10/11 人、重复/非正数、普通员工/无权限/停用/被删管理员、候选后撤权、旧字段兼容、双字段拒绝、空数组与省略、过期版本完整回滚。
- 读路径：列表、详情、动作历史、导出、任一负责人筛选、未分配统计、旧单人回退；自动员工与人工重复身份不重复展示。
- 生命周期：真实 MySQL/PostgreSQL 新库/旧结构升级与二次无 DDL；关系索引 EXPLAIN；告警删除清除关系；其他关联测试回归。
- 浏览器：多选保存/清空/重开/刷新、自动归属只读、员工越权拒绝、手机短屏滚动与焦点；类型、lint、构建及相关 Vitest。

本轮九条七语言新增文案已由用户确认，见 `audit-security-completion-copy.json`。

## Existing Logic and Related-Path Findings：AI 请求 IP 观察

实际日志持久化入口是 `model/log.go:createLog`、`model/relay_log_pipeline.go:insertRelayLogs`（含重试/文件回放）和 `model/request_log_segment.go:RequestLogWriter.Record`。`RecordErrorLog`、`buildConsumeLog` 和 `middleware/request_logger.go:recordRequestLogEntry` 已直接取 `ClientIP()`；用户设置缺省、false、true 均不能阻止记录。持久化入口都有服务端绑定的 UserId、Ip、CreatedAt；无原始请求的后台记录、地址无法解析或历史日志仍可能缺少 IP，不能从请求正文补取。仅消费/错误日志及请求日志属于风险观察范围，管理/充值日志排除。原始请求记录写入成功并更新索引后才可观察。

`worker.go` 已有固定监督循环、最多一个采集/检测工作周期和独立主库/日志库连接池；`ip_rules.go` 已有公网过滤、限次查询、缓存、24 小时延迟重试与 anonymous_network/ip_abuse 规则。复用此链路，避免另建扫描器或同步外部调用。`store.go` 的登录基线、敏感操作关联和自动响应只适用于认证/管理事件，观察事件显式排除。

### 生命周期、配置与接口

- 新配置 `audit_security_setting.relay_ip_enabled`，默认 false，通过已有设置读写接口，沿用现有管理权限。仅在监控、IP 情报均启用且未暂停时采集风险观察事件；关闭、暂停或尚未配置 IP 情报均不影响原始日志记录 IP。停用不回放观察队列或历史日志。
- 持久化成功后非阻塞入独立容量 1024 的最小元数据队列。字符串长度有界，不保留正文、URL、header、密钥或完整日志对象。原始请求时间超过 120 秒的历史回放不采集，避免启用时意外回扫；长请求由成功保存时记录为新观察。
- 后台每周期最多消费 200 项、写入 5 个不同用户/IP/10 分钟窗口的观察。队列等待超过 120 秒丢弃；固定上限 10000 的去重表，超额丢弃；跨节点用独立 HMAC event_id 幂等。常规日志与请求日志两路重复不会重复告警。
- 用户角色/员工身份由独立后台池读取当前快照；这是观察时身份，不能用于认定请求发生时的权限或自动惩罚。用户已删除或未知时丢弃。事件 action=relay.ip_observed，单独 subject_ref，不混入登录来源、设备、异地登录或账号接管判断。
- 复用安全审计源、规则告警、现有账号归属/个人投影；没有新增公开接口、日志列或历史数据查询。健康接口增加启用、队列、丢弃、缺失 IP 和最近观察指标，便于判断采集是否有效。写入/查询失败计数并记录内部错误，不影响日志或 AI 响应。

### Main Chain Impact / Shared Resource Audit / Concurrency Analysis

常规异步日志写入器及文件写入器中只增加有界长度检查和非阻塞 channel send；旧同步 createLog 路径同样没有新增 I/O、锁、goroutine。100k RPM 下每成功日志最多一次 O(1) 入队尝试，队列满即丢弃。配置用已有原子快照；队列与后台去重 map 独立于 relay。后台最多 5 次/秒的 users 主键读取和 audit_logs 写入（复用独立池，单次 2 秒，整个周期 25 秒），最多 20 次/扫描周期的既有 IP 外查预算不增加；新逻辑不使用 Redis、tokens/channels、额度、认证缓存、请求日志索引锁。写入沿用 audit_logs 唯一 event_id 与时间索引；告警/证据沿用已有索引和保留清理。

### 验证

先覆盖开关、无 IP/内网/非法 IP、队列满、过期、重复日志、不同用户、10 分钟窗口、写失败、当前身份、暂停清空；验证观察只产生 IP 风险，不产生登录基线或自动响应。真实数据库检验重复写入与扫描，验证既有日志写入失败时不发观察事件。

## Existing Logic and Related-Path Findings：个人安全通知

`dto.UserSetting`、`controller/user.go:UpdateUserSetting` 已有 email/webhook/Bark/Gotify 的统一目的地；现有 NotifyUser 没有 context，缺失目的地还返回 nil，因此不能直接作为持久投递队列的成功证据。`notify.go` 只投递管理员邮箱/Webhook，且 worker 在总通知关闭时不调用 deliver。`self_access.go` 已定义严格的本人告警与处置结果投影，复用其授权判断；不能发送管理详情 DTO。

`store.go` 的新 finding 与管理员 outbox 在同一事务；`account_response.go` 创建人工/自动动作，`model/security_account_response.go` 及凭据清理更新终态。新增动作的个人通知标记使终态通知可从崩溃恢复，不需要扫描旧记录或在用户行事务中联网。

### 生命周期与数据契约

- 在既有个人设置接口增加可选 `account_security_notify_enabled`。默认 false、省略保留旧值、显式 false 退订；普通用户、员工、管理员均可选择，站点余额提醒关闭不隐藏此项或通道设置。没有新接口或权限提升。
- SecurityDelivery 增加隐藏 recipient_user_id、可空唯一 delivery_key；kind=account，destination 只保存目的地及密钥的独立 HMAC 指纹，不保存明文。索引 `(alert_id,recipient_user_id,created_at)` 支持最近待发合并，`(alert_id,recipient_user_id,sent_at)` 支持实际发送时间的冷却。发送前再次复查冷却，长期积压消息刚发送后也不能连续重复；沿用已有 due/sent/alert 索引和清理。
- 新可信本人告警在同一事务创建 outbox，不受管理员 notification_enabled 控制；默认不追发历史。重复 finding 在冷却期抑制；已有待发同告警/同收件人/同目的地合并。处置结果不丢弃，冷却期内延后发送。
- SecurityAction 增加隐藏 account_notify_pending（仅新处置设置 true，无默认迁移回填）；`(account_notify_pending,id)` 索引支持每周期最多 20 项终态通知处理。标记清除和 outbox 在同一事务，旧动作不追发。
- 每次投递重新读账号、订阅和目的地，用户删除/退订/地址或密钥变化取消；用户禁用仍能接收本人安全结果。只发送本人告警编号、查看入口及本人处置 action/outcome/count，不发送调查备注、负责人、策略阈值、请求标识或他人事件。
- 独立个人批次每周期最多 3 条，整个批次外发预算 8 秒，给管理员投递保留时间；沿用 claim、超时恢复、5 次重试和失败状态。复用通知 JSON/Bark/Gotify 协议与 SMTP/Resend 底层实现，独立 HTTP 连接，DNS、拨号、HTTP、邮件及 Worker 都受剩余预算约束。投递结果仍用外层周期 context 落库，避免单个发送超时后不能记录失败。目的地遵守现有 fetch SSRF 配置并在拨号时复查，禁止跟随重定向；Worker 保留既有配置和认证协议。
- 个人通知使用现有 `notify_limit:<user>:account_security` 频控，与管理员全局小时额度分开；Redis 复用审计独立小连接池，不占 relay 池，内存回退沿用既有通知计数。失败只记录不含目的地或凭据的内部错误，前端只展示通用状态。

### Main Chain Impact / Shared Resource Audit

通知设置保存和 outbox/动作标记都不在 relay 热路径。后台读取 users.setting/email 主键、不更新用户权限/配额；使用现有审计主库池。audit_security_actions/deliveries 为独立审计表。Redis 新通知 type 与额度/令牌 key 不重叠，独立池最多 1 个连接；HTTP/SMTP 独立连接受 8 秒约束，无每请求 goroutine。AI 请求观察与个人通知仅通过已生成的 IP 告警关联，没有同步依赖。

### 验证

覆盖默认退订、显式开关和省略保留、四通道 payload/签名/Worker、目的地缺失与更改、退订、删除及禁用用户、未知/他人/管理告警隔离、合并/冷却/幂等、终态恢复、管理员关闭独立发送、取消与超时、限频和失败重试。真实数据库新增列/索引新库升级二次启动无 DDL，并独立构建 relaykit。

## Existing Logic and Related-Path Findings：六项前端交互

F01 首页的 class 字符串缺空格，CSS `.is-open/.is-expanded` 根本不命中；沿用原视觉，菜单使用现有 Sheet 模态能力和当前首页样式，内部滚动/焦点/Esc 一并处理。F04 PublicHeader 同样将手工 fixed div 与 body overflow 改用已有 Sheet，保留导航权限/登录提示处理，提供短屏滚动及返回触发器。

F02 AnimatedOutlet 按 routeId 复用是为保存仪表盘参数状态，不能改成 pathname 强制卸载。读取本地 TanStack Router 实现确认 `scrollRestoration`、`scrollToTopSelectors` 和 `data-scroll-restoration-id` 支持独立容器的历史位置；在 router 开启，SectionPageLayout 标记滚动区，新导航归零、后退恢复，组件保持原复用方式。F03 实际容器在 `components/data-table/layout/data-table-page.tsx`，按 pageIndex/pageSize 重置移动容器，后台刷新不重置；历史恢复仍由 router 在渲染完成后处理。

F05 GeminiSettingsCard 接入既有 FormNavigationGuard；保存全部成功后 reset 为已保存值，失败/取消保留 dirty。F06 Main 增加唯一 content ID 和 tabIndex=-1，SkipToMain 激活后显式聚焦，避免仅变更 hash。均无 API、存储、配置或共享资源改动。先记录浏览器验收断言：菜单可见/展开、短屏菜单底部可达、Esc/焦点回归、分区新导航与后退、移动翻页归首、Gemini 离开/留下/保存、跳到正文实际焦点。

浏览器回归纠正了两个仅靠静态检查未发现的问题：TanStack 的每条 scroll selector 只匹配第一个元素，因此滚动归零改为分别指定分区与手机列表的 restoration ID，避免父容器覆盖子列表；Base UI 多选在搜索选择后关闭时会冻结退出查询，快速继续选择可能仍只显示旧结果。共享 MultiSelect 在 item-press 时保留弹层，清空查询后继续选人，保留 Esc/点击外部关闭行为，并补充搜索连续选两人的组件断言。

首次启用的 source_start 在采集前初始化，扫描仍按 scan_interval_seconds 执行。扫描间隔测试现在分别检查覆盖起点与扫描游标，避免把提前建立覆盖起点误判为提前扫描。

## 验证记录与边界

证据目录：`_local/audit-completion-20261118/`。测试只使用本机隔离容器、临时库、临时账号和本地 HTTP/SMTP fixtures；没有连接生产或真实 IP 情报供应商，没有发布部署。

- 前端使用隔离 **Bun 1.4.4**：`bun run typecheck`（1,654 文件/2 项目）、`bun run lint`（0 错误，7 条既有警告）、`bun run build` 通过。系统 Bun 1.3.13 不支持 `bun check`，已用项目版本重跑标准命令，未改包管理脚本绕过检查。
- `bun run test` 指定 profile/settings、audit/security/monitor、settings-drawer、audit/self 四个文件，**55 项通过**。包含个人订阅默认/保存、多人选择/版本、设置校验/二次验证、本人脱敏/分页。
- `go test ./service/auditsecurity -count=1 -timeout=8m` 整包通过；controller/router/model/operation_setting 相关测试通过；`service` 的通知限流回归使用隔离真实 MySQL，通过。`cd relaykit && GOWORK=off go build ./...` 通过。
- `run_matrix.py`：**MySQL 26.10.0**、**PostgreSQL 18.7** 上的分配、个人通知、AI IP、本人/客户权限、迁移/查询计划专项均通过。EXPLAIN 验证账户待通知、人工负责人、创建时间及实际发送时间索引。SQLite 用于单元失败注入和补充回归，不替代真实数据库。
- `app_fixture.py`：MySQL 主库 + PostgreSQL 日志库，分别测试新库和由线上参考提交 `127c82331f979f2937cd1035149820eb79e6e1b6` 生成的旧库升级；每种均启动两次，第二次主库/日志库 DDL 均为 0。旧版本通过已有干净工作树构建，未使用本地 HEAD 冒充线上版本。
- Chrome 生产构建预览：390×640、390×844、844×390、1440×900；14 项检查覆盖六项 F01–F06、自动员工与两位人工管理员保存/刷新/清空、IP 设置手机滚动、普通用户/员工/无授权管理员的本人告警和外部拒绝、员工当前客户入口、个人订阅开启/刷新/退订，全部通过，未捕获页面异常。手机详情已复核明暗两种主题。浏览器保存与查询均连接实际后端。

批准文案核对：8 条前端 × 7 种语言共 56 个值与批准稿一致、无 BOM；1 条通知正文已写入实际后端 en/zh-CN/zh-TW YAML 并逐值核对。其余后端语言不在本项目支持范围内。

本轮未改动五个受保护的上游鉴权/HTTP 文件，其 Git 状态无差异。本地 `main` 不包含其中的 TypeScript 路径，因此与该引用逐字节比较无法执行，未将其记为通过；没有为满足比较而替换这些文件。

未执行：生产服务器 schema-only 副本演练、真实生产数据唯一性核验、30k/100k RPM 实测、ClickHouse 实例、Safari/Firefox 和实体手机。日志观察已以成功/失败持久化与后台规则贯通测试覆盖；未把这些测试称为完整 AI 渠道链或生产上线验收。ClickHouse 来源仍需在使用该部署方式时另行验收。

收尾：最终安全监控整包、两种真实数据库专项再次通过；应用升级/新库第二次启动 DDL 仍为 0。3000/5178 测试服务已停止，本轮 MySQL 容器与 PostgreSQL 临时库已删除；临时凭据和三个测试二进制已删除。保留脚本、脱敏测试日志、构建 SHA256、截图和结果 JSON，详见 `verification.json`、`cleanup.json`。既有共享 PostgreSQL 容器和工作区其他修改保持原状。

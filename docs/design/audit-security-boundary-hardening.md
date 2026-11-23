# 安全监控全链路边界复核与加固方案

日期：2026-11-18。状态：**已按确认方案及全部 12 条七语言文案实施，本地回归通过；未部署。**

这次范围是异常发现、证据归属、动作授权、清理执行、人工恢复和页面反馈的完整链路，不限于 IP 或 API Key。承接已批准的 [自动清理设计](audit-automatic-credential-cleanup.md)，保留“封禁账户 / 清空 API Key / 清空访问令牌”三个独立动作及清理默认关闭的决定。

## Goals and Scope

1. 补齐单账号、单来源、单次操作的检测入口，包括创建唯一一个 API Key、读取已有 Key、签发 PAT、安全设置变更和渠道敏感变更。
2. 修复事件晚到、身份版本在入队前变化、业务已提交但审计遗漏等问题，避免漏判和恢复后误处置。
3. 使多个规则命中同一事件时共享实际动作；提供有权限的人工停止，并区分停止新动作与完成已提交批次的收尾。
4. 新规则默认只告警。Root 可按规则独立选择已有三个动作；旧规则选择保持不变。新规则不依赖账户拥有多个 Key，也不要求先重复创建或取钥。
5. 不将 VPN、异地、陌生 UA、失败登录、请求量增加或未知 IP 单独等同于盗号。
6. 本次不增加“退出全部网页登录”第四个动作，也不新增 AI 请求行为检测器。二者的实际覆盖边界必须明确，不把清理凭据写成彻底收回所有访问能力。

## Existing Logic and Related-Path Findings

以下记录实施前的实际实现及复现结果，用于解释修改原因，不表示这些缺口仍未修复。“复现”是调用实际函数的合成用例，不冒充浏览器端到端测试；最终实现和验收见文末。

| 编号 | 已确认现状 | 影响与拟处理 |
| --- | --- | --- |
| F01 | `account_compromise.go:successfulAccountKeyAccess/EvaluateAccountCompromise` 统计不同登录账号、取钥账号和来源 IP，不查账户拥有多少 Key。一个 Key 被不同来源查看仍可命中。已复现。 | 不把 Key 库存数量作为条件，不将原多来源阈值统一改成 1。 |
| F02 | 单账号、单 IP、只查看一次 Key 不命中四条自动规则；`token.create` 也不属于 `store.go` 当前保留的敏感候选。已复现。 | 纳入真实创建事件；新增单次风险组合规则。创建成功与密钥已返回分开记证据。 |
| F03 | `ip_rules.go:enrichSecurityEvents` 仅查询成功登录 IP；`ip_abuse` 只有告警，未列入自动规则。 | IP 检查扩展至明确的敏感操作；建立可配置的高危 IP 规则，不直接使用界面的红色风险标记。 |
| F04 | `ip_retry` 可以补齐普通 IP 告警，但 `queueAutomaticAccountResponse` 只允许 scanner，直接增加 IP 规则仍会错过异步查询结果。 | 记录首次采集来源与时间；允许新事件在原 120 秒截止前完成补证，不延长时间，不处理历史导入。 |
| F05 | PAT 签发、改密码、关闭 MFA、解绑和注册 Passkey 可生成 `sensitive_operation`，但不是四条自动规则。单个渠道实际改地址也达不到默认 10 个渠道阈值。前五类已用规则函数复现。 | 按实际发生的敏感动作统一进入新规则，不要求攻击者先大量重复操作。 |
| F06 | `store.go` 仅在首次处理当前事件时进行组合判定。取钥先入库、较早发生的登录后入库时，登录不会重新判定后面的已存取钥，重叠扫描也跳过已处理事件。已用规则函数复现，并核实入库路径。 | 对新到关联事实，重判有限时间范围内受影响的已存触发事件；仍以原事件 ID、时间、身份和截止时间为准。 |
| F07 | `account_response.go` 入队时才读取当前 `users.auth_version/role`，没有原事件认证版本。执行前的版本检查只能防住入队后的变化。 | 采集服务端事件身份及有效版本；恢复发生在事件与入队之间时也跳过旧事件。缺版本的旧记录只告警。 |
| F08 | `Disable2FA`、`PasskeyDelete` 在 DB 修改后还有缓存发布/当前会话推进步骤，失败可能在记录审计前返回。 | 明确返回是否已提交和提交后的版本；已提交操作即使后续失败也记录真实结果，不把 HTTP 失败当成回滚。 |
| F09 | 密码重置公共入口使用匿名上下文；不能从用户输入的邮箱推导可以自动处置的账户。现有代码已记录部分提交/清理失败事实。 | 只接收事务返回的真实目标 ID/版本作为恢复事实；匿名申请、猜测用户名或邮箱不授权账户动作。 |
| F10 | `UpdateAlertInvestigation` 不停止待执行动作；封禁及清理授权不检查人工误报结论。本次 SQLite 合成告警已标误报，封禁仍提交，两种清理仍进入成功路径。 | 增加 Root 可显式停止本次处置的控制。员工的低权限排查结论不能隐式撤销 Root 策略。 |
| F11 | 当前 execution key 按告警 ID 与目标去重。不同规则对应不同告警；清理只继承同次排队的本规则封禁版本。 | 多规则共享一个处置组，避免重复清理和本组封禁导致其他已选清理被误判为版本过期。 |
| F12 | 当前一个告警事件内已成功的动作不会重复执行；同一 15 分钟合并告警里的后续新攻击可能只追加告警。 | 区分重复投递与新的成功操作；同一事件只执行一次，已完成处置后较新的合格事件可创建新组。恢复后的旧版本事件仍禁止执行。 |
| F13 | 清 API Key 不清 PAT；清 PAT/旧版访问令牌不退出网页登录。清理期间或之后新建的凭据不自动属于原批次。 | 保留已确认的独立语义与固定上界；不得在后台悄悄加封禁或退出会话。持续攻击的新事件重新判定。 |
| F14 | 监控来源是审计日志。攻击者直接用已泄露的模型 Key 调 AI、不登录控制台、不取钥时，不进入当前检测。 | 如实列为覆盖空白。本次不把登录监控宣传成 AI 盗刷检测；另建后台使用行为采集方案后才能扩展。 |

主要代码：`service/auditsecurity/{worker,store,rules,account_compromise,account_response,ip_rules,ip_provider,investigation,customer_access,notify}.go`，`model/{audit_log,audit_security,security_account_response,security_credential_cleanup,user_api_keys,user_access_tokens_clear}.go`，`controller/{token,access_token,user,twofa,passkey,audit_security_response}.go`，`setting/operation_setting/audit_auto_ban.go`。

需要纠正旧文档的宽泛表述：[自动封禁设计](audit-auto-ban-and-ui-refinement.md) 中“多规则重合不会重复处置同一事件”，当前代码只保证单告警的执行键去重，尚未实现跨规则统一处置；本方案 F11 才补足该合同。既有测试通过不证明未覆盖的边界已通过。

## Proposed Rules and Sensitive Operations

原四条规则及其阈值继续保留。新增三条；所有新增动作选择默认为 false。

| 内部 ID | 界面候选名称 | 成立条件 |
| --- | --- | --- |
| `risky_ip_login` | 高危 IP 登录 | 可信账号成功登录一次，且该 IP 有符合下节条件、尚未过期的明确恶意活动证据。无需创建 Key。 |
| `risky_ip_sensitive_operation` | 高危 IP 敏感操作 | 已认证账号从上述 IP 成功执行一次允许列表中的敏感操作。允许浏览器或 PAT 入口，不强制要求近期重新登录。 |
| `new_source_sensitive_operation` | 新来源敏感操作 | 账号在最近 30 天有至少已观察 24 小时的其他来源记录，当前来源首次观察不超过 24 小时，且成功执行一次敏感操作。无历史记录时只建立观察记录，不推断“已知安全”或“已被盗”。 |

“新来源”仅指来源 IP 的变化，不声称识别真实设备或真人。UA 摘要只能辅助关联，不能证明设备可信。新来源规则有正常换网误报的可能，因此默认只告警，由 Root 决定是否自动处置。

敏感操作分类必须依据成功提交/实际返回证据：

| 类别 | 纳入 | 不作为同等证据 |
| --- | --- | --- |
| 模型 API Key | 成功创建；成功单条查看；批量实际返回至少一个 Key | 空返回、失败、请求的 ID 数量、仅改名称、列出脱敏 Key |
| 访问令牌 | 成功签发 PAT/旧令牌；实际增加权限范围 | 名称修改、提交但未变化、主动减权或删除自己令牌 |
| 账户安全 | 实际改密、关闭 MFA、增删 Passkey、改变登录绑定 | 验证失败、只申请重置、普通资料/昵称修改 |
| 权限和资金控制 | 实际升权、授权范围变更、人工修改额度/关键账户配置 | 正常消费扣费、自动结算、仅打开管理页面 |
| 渠道 | 成功查看上游密钥；实际替换密钥或地址；改变代理/请求头覆盖等会改变请求目标或认证的设置；删除渠道 | 普通模型列表维护、排序、权重、未发生变化的表单提交 |

账号动作针对实际认证操作者。管理员修改客户时不能把被修改的客户误当攻击者；使用管理员访问令牌时保持既有 AdminInfo 归属。Root 由界面“包含超级管理员”独立控制，默认关闭，对应兼容存储字段 `auto_ban_exclude_root=true`；管理员/员工仍需显式包含。2026-11-20 范围调整见 `audit-automatic-credential-cleanup.md`。

### IP evidence eligibility

- 有效公网 IP，规范化 IPv4-mapped IPv6；未知、内网、无效地址不参与外查或自动 IP 规则。
- `is_abuser=true` 仅是必要条件。还须有当前启用服务的 abuse 来源、更新时间、过期时间及匹配该 IP 的证据，使用现有 schema 2 来源与证据字段，不发明评分。
- 自动动作只接受精确地址或等价 `/32`、`/128` 证据。宽网段、只有旧版布尔值、缺失来源或已过期的数据可以用于人工告警，不用于自动账户操作。
- VPN、代理、Tor、机房 ASN、位置变化及综合“高危”徽标不是恶意活动证据。IP 服务超时/配额耗尽/未返回字段表示未知，不表示安全或恶意。
- 首次执行前核验原证据仍有效、当前服务配置未改变。使用现有缓存和查询预算，外查在事务外进行；不持有 users 锁等待网络。
- 改变 IP 服务、启停或相关能力时推进策略 epoch，使旧任务失效。更新缓存不能修改已保存的历史判定依据。

已额外核实 `main.go`、`router/plugin-router.go`、`common/trusted_proxies.go`：主路由和插件路由共用可信代理配置，提供显式列表替换和 `none` 模式；默认包含回环、私网及一个部署代理地址。来自不可信直连端的转发头不会自动成为客户端 IP。实际部署的代理拓扑与配置仍须验收，不能从本地默认值推断生产配置正确。

## Data Flow and Lifecycle

1. **业务提交与采集。** 管理及认证入口记录服务端拥有的 actor ID、角色、认证方式、认证版本和必要的会话摘要。摘要用独立 HMAC 域，不保存 session ID、Key、PAT、密码或完整请求体。密码/MFA 等会自行推进版本的操作同时记录操作前及提交后的有效版本。
2. **规范化。** `Normalize` 增加严格白名单事实，覆盖模型 Key 创建 ID、PAT 创建 ID、实际变更、提交结果、有效认证版本。不能从客户端任意参数读取/覆盖这些服务端事实。元数据超限或身份不全时只告警。
3. **增量扫描。** 保留首次启用起点、500 行来源页、20 行事务批次、600 秒关联窗口和 1000 条证据上限。新增敏感候选进入同一监控库；不全表回扫审计或消费日志。
4. **迟到关联。** 新登录到达后，按 actor/IP 两个索引分别读取发生时间前后 600 秒内的登录/取钥窗口，每个窗口最多读取 1001 条以识别截断。仅重判登录之后 600 秒内的原取钥触发，分别应用账户或 IP 对应规则；同一窗口复用证据，避免逐个触发再次查询。每事务最多 20 个输入、40 次窗口查询、1000 个重判触发和 1000 个判定结果。截断只告警。使用原 event ID 去重，重复重判不累计 occurrence 或通知；不把到达时间当发生时间。
5. **IP 补证。** 同一 worker 预算内查询登录和敏感操作；首次采集保存 live/history 来源、初次观察时间和原策略。重试完成时重新校验原事件距当前不超过 120 秒、在策略启用后、身份版本仍适用。超过截止只更新告警，不自动处置。
6. **规则判定。** 完整判定快照保留具体触发、原阈值、证据和身份。新来源判定在写入当前来源观察结果前取快照；当前事件不得成为自己的成熟历史依据。
7. **统一处置。** 同一账号、认证版本、策略内，同一触发事件的命中合并为一个处置组，各规则所选动作取并集。同一动作仅执行一次。一个账号同一版本/策略最多一个未完成组；后续命中附加关联，不扩大已开始的清理范围或延长截止时间。
8. **执行。** 继续采用封禁 → 访问令牌 → API Key 顺序，但保留动作独立性。仅允许同一组中已确认的封禁版本递增被后续清理继承；不能借其他人工操作或不相关告警的版本继续执行。未开始的每个动作仍检查角色、员工范围、排除列表、策略和时效。
9. **清理和重试。** 沿用固定 Key 上界、每批 100 条、缓存 fence、每批 3 次失败/10 秒间隔、10 分钟发起批次上限和提交回执。零凭据是成功且数量 0；不重复算删除量；不拿重试重新扫描“现在的全部凭据”。
10. **新事件。** 处置组终态后，发生时间严格晚于上一组最后触发的新合格事件可创建新组。同一事件重放永不重开。恢复引起认证版本变化时，旧事件只能记录告警；不能读取新版本替代旧版本。原组清理期间新建的凭据不被原组扩大范围删除。
11. **人工停止。** Root 可针对当前组显式停止。待开始的动作停止；已经准备的 Key 批次仅完成其固定 ID 的缓存/删除回执收尾，随后不发起新批次。已完成封禁/撤销不回滚。改变排查结论、归档或员工备注本身不撤销自动策略。
12. **显示与通知。** 各相关告警显示同一真实动作结果和数量，保留规则原因。停止、成功、部分成功、失败、未知数量分别显示；通知仍按已持久化结果发送，不能把排队成功写为清理成功。

此流程仍是事后后台监控，不能撤回已经返回的 Key、已经提交的恶意修改或已经发生的费用。普通用户更换网络也可能符合可选的新来源规则，不宣称零误报。

## API Contracts and Authorization

- 设置继续走 GET `/api/option/?scope=security.audit-monitor` 和 PUT `/api/option/group`；保存仍要求 Root 浏览器会话及绑定完整 values 的 `audit.security.settings` proof。只扩展规则 JSON，不添加另一套设置 API。
- `auto_ban_rules_json` 兼容旧四规则输入，缺失的三条新规则补为全部动作关闭。返回规范化七条。拒绝未知 ID、重复 ID、错误布尔类型和无效阈值。旧四规则阈值不改变。
- 高危 IP 规则选择任何动作时，必须启用 IP 查询并配置具备 abuse 能力的服务；服务缺少精确有效证据时运行结果仍降为告警。仅保存告警配置时不强制启用自动动作。
- 告警列表/详情沿用现有权限；返回兼容的 `response` 和 `responses`，增加安全的处置组标识与 `can_stop`。原始认证版本、会话摘要、策略恢复信息不下发客户页面。
- 复用 POST `/api/audit/security/alerts/:id/responses`，新 action 为 `stop_automatic_response`，请求含 `{version,action,request_id,response_group_id}`。现有 `disable_account` 合同不变。
- 停止操作必须是 Root 浏览器会话；PAT、管理员、员工均不能调用。新增 `audit.security.response.stop` proof 绑定 alert ID、version、group ID 与 action；服务端在事务中复核。过期版本沿用既有“状态已变化，请刷新”错误，不停止其他组。重复 request_id 返回同一回执。
- 员工客户页面保持归属与字段白名单，只看本客户的安全摘要；不能读取共享来源的其他账号证据，也不显示停止按钮。新规则必须加入服务端客户投影允许列表及版本管理，不能只补前端标题。

## Data Model and Index Design

新增协调表只在监控主库，不写日志库，不将主库 schema 复制到 ClickHouse。

| 表/字段 | 用途与索引 |
| --- | --- |
| `audit_security_response_groups` | 持久化 target_user_id、原 auth_version、policy_at、trigger_event_id/at、last_trigger_at、state、stopped_at、ban_action_id。确认后的封禁版本和各动作进度保留在对应 action 回执中。主键 ID；trigger_key 唯一；可空 active_key 唯一，表示账号/版本/策略的唯一未完成组，终态置 NULL。`(target_user_id, auth_version, policy_at, created_at, id)` 支持上一组查询；`(state, created_at, id)` 支持终态孤立组回收。 |
| `audit_security_response_links` | alert_id、group_id 复合唯一索引；`(group_id, alert_id)` 反向索引。用于跨规则显示同一结果、停止及清理引用，不从 JSON 扫描关联。 |
| `audit_security_actions.response_group_id` | 可空字段，历史动作保持 NULL；索引 `(response_group_id, action, id)`。新执行键按组和动作生成，原 execution key 不回写。 |
| `audit_security_actions` 目标索引 | `(target_user_id, outcome, id)` 支持检查该账号是否仍有未完成的历史无组动作。 |
| `audit_security_events.response_group_id` | 可空字段，原事件通过既有 event_id 主键永久指向首次处置组；补证或重放不能重新开启旧组。 |
| `audit_security_findings.detection_key` | 可空唯一键，使用规则、版本、目标和原 event ID 的 HMAC 标识；重复补证不重复累计或通知，历史快照保留 NULL。 |
| 现有事件 facts | 增加服务端身份、提交有效版本、live 来源和首次观察事实；无需更改日志表的主键或补扫历史。事实缺失按未知处理。 |

历史无组动作继续使用原恢复合同，不改写旧回执、不自动归并历史清理。投影通过有限告警 ID 一次批量读取 links/groups/actions，兼容旧 AlertID 查询，避免逐行 HTTP/SQL。保留当前 due、actor/time、IP/time、baseline 索引；新关联查询必须带时间范围和 Limit。

统一新路径的加锁顺序为 group → alert（ID 升序）→ action（ID 升序）→ users。扫描先计算本事务的有限判定及组，再持久化告警/链接/动作；不能一边按不稳定顺序锁告警、一边反向获取组锁。停止和执行遵守同一顺序。旧无组回执只走旧顺序，不在旧事务里追加新组锁。

新增表以 GORM 兼容三种主库；NULL 唯一键、旧回执保留和重复启动已在本地矩阵验证。自动清理保留被未完成处置组引用的告警、actions 和 links；共享组不能随其中一条告警删除。终态组仅在创建超过两天且所有告警引用清除后回收，每次最多 100 组。迁移覆盖全新、旧结构升级和二次启动无 DDL；旧四规则由配置解析补齐，历史回执不归并。

## Config Parameters

不新增环境变量。新配置仅为 `auto_ban_rules_json` 中三条规则的既有 `selected`、`clear_api_keys`、`clear_access_tokens` 布尔字段；无数量阈值。总开关、角色范围、排除账号及 IP 服务沿用已有设置。

IP 服务配置变化纳入 `auto_ban_effective_at` 更新依赖。旧配置不会因升级自动启用新的账户操作。移动速度与距离仍固定为已确认的 1000 公里/小时、200 公里，不重新放回设置页面。

## Error Handling and Recovery

- 无身份、版本、实际提交证据、有效来源或完整历史时只告警；不能为了“覆盖”而猜测目标账号或取当前版本补证。
- 配额/超时/服务失败保留 pending/unknown 与下一次有限重试；超出原自动时限只告警。不把 unknown 当 false，也不把加载中当 0。
- 事务失败记录内部原因，对客户端复用已翻译的可重试错误；提交不明先读回执，不能重复改账号/删凭据。
- 业务已提交而后续缓存或当前会话更新失败时，保留“已提交 + 后续失败”事实，沿用既有修复路径，不伪装成功或回滚。
- 停止、策略变更、人工解封/改密/权限变化后不开始新动作；已提交批次仍完成固定范围的缓存收尾。
- 处置组排队、动作回执、告警版本和活动 revision 在同一事务推进。后台任务重启、租约失效、双 worker 竞争以持久回执及 fence 为准。
- 通知失败不回滚安全动作；通知限频不能改变检测或执行结果。

## Main Chain Impact

本方案不新增 AI 请求同步检测，不在 relay goroutine 发起 IP 查询、SQL、额外 Redis 调用或监控锁，不为每个 AI 请求创建 goroutine。新增采集位于管理/认证入口，判定、补证和自动操作由既有有限后台 worker 执行。

封禁和凭据撤销通过既有认证缓存协议影响后续请求；已经鉴权的流式连接和异步任务沿用既有合同，不因新告警中断或改变结算。不能声称动作一开始就阻断了所有在途请求。

### Shared Resource Audit

| 共享资源 | relay 是否使用 | 隔离与限制 |
| --- | --- | --- |
| users | 是，认证/额度 | 监控独立池，主键条件短事务；只改状态和认证版本；不回写余额/配额快照，不持锁外查。 |
| tokens | 是，鉴权/计费 | 沿用 user_id/ID 有界查询、批次 100、固定上界和缓存失效；软删除保留计费关联。 |
| PAT、旧 access_token、浏览器会话 | 管理认证；部分共享用户状态 | PAT 清理沿用用户锁；不全表扫描会话；本方案不增加自动退出会话动作。 |
| token 缓存、fence、用户认证状态缓存 | 是 | 复用已有失效协议，不发明同前缀键或覆盖额度缓存。IP 证据与监控状态使用独立结构。 |
| 审计、security 表、组与关联表 | 监控/管理使用 | 复用隔离的监控主库池最多 2 连接、日志池读/写各 1；组和链接查询有索引与上限。 |
| worker、IP 缓存、通知队列 | 非 relay | 沿用有限 worker、IP 缓存最多 10000 项、现有查询预算；不借用 relay 工作池。 |

### Concurrency Analysis

30k / 100k RPM AI 流量下，本方案新增同步 relay DB/Redis/外查/锁/每请求 goroutine 数均为 **0**；共享 users/tokens 上的后台写争用仍需实测。处置维持每轮最多 5 个动作、5 秒总预算、单项 1 秒；API Key 单批最多 100 条。新表事务只涉及有限命中与至多三个实际动作，不随 AI 请求量按比例增加。

不能由上述静态分析声称容量测试已通过。本轮进行了功能和数据库回归，未进行 30k 或 100k RPM 压测。

## Boundary Acceptance Matrix

下表保留边界验收目标；本轮实际覆盖由文末测试表及对应测试代码界定，不把这些目标视为所有组合均完成端到端验收。

| 类别 | 必须覆盖 | 预期 |
| --- | --- | --- |
| 数量 | 0/1/多 Key；单条/批量；重复 ID；仅请求未返回；只建不读 | 单个有效敏感操作足够进入新规则；不按库存、请求数量扩大证据。 |
| 身份 | 匿名、普通用户、PAT、管理员代客、员工、Root；缺失/伪造事实 | 只处置可信操作者；Root 保护与员工默认排除；匿名失败不能封指定账号。 |
| 登录 | 新账号、首次来源、MFA/Passkey/OAuth 登录、失败后成功、随机/真实 refresh 重放、正常并发刷新 | 第一笔历史不成为成熟基线；失败/重试/正常竞争不等于被盗；真实重放保留原规则。 |
| 安全设置 | 改密、找回密码、解绑、MFA/Passkey 增删；实际未变化；提交后收尾失败 | 已提交事实不漏；合法安全验证流程仍保留；服务端返回真实版本。 |
| 权限/渠道 | 升权、额度修改、单渠道密钥/地址替换、批量部分成功、正常维护 | 统计实际成功对象；不处理被操作客户；不把日常结算/模型维护当盗号。 |
| IP | 精确恶意证据/宽网段；共享 NAT；VPN；IPv4/IPv6；伪造转发头；超时/过期/服务切换 | 来源入口继续遵守可信代理配置；只按可信证据授权；不连带处置同 IP 其他账号。 |
| 时间 | 59/60、119/120/121、299/300、599/600 秒；未来时间；乱序/跨节点时钟差 | 按各规则原时间和期限；无延长自动窗口；允许范围内迟到事实补判。 |
| 重复/竞争 | 同事件重复页、两个 worker、同事件多规则、新事件同告警、任务重启 | 一组一个实际动作；不重复删除或计数；新事件与重复投递可区分。 |
| 恢复 | 入队前/后改密或解封；排除账号；关闭规则；标误报；显式停止；新增合法 Key | 恢复后的旧证据失效；低权限排查不能撤销策略；原批次范围不扩张。 |
| 清理 | 无凭据、过期/停用凭据、批次边界 99/100/101、并发创建、人工删除、清理中重启 | 固定上界、已确认数量、幂等回执；不碰余额/订阅/日志/上游渠道密钥。 |
| 故障 | 主/日志 DB、Redis、IP 服务、通知失败；提交结果未知；证据截断；队列积压 | 不宣称成功或安全；不阻塞主链；恢复不扩大操作范围。 |
| 页面/API | 旧四规则保存、默认全关、三动作独立、权限拒绝、取消/失败/重试、分页/筛选/导出 | 后端校验与页面一致；结果可追踪；旧配置不自动授权新增规则。 |
| 布局/i18n | 七语言、桌面/手机、浅/深色、标签切换、长内容/错误/加载状态 | 外框宽高稳定、内部滚动；文案需按确认稿写入，状态与数量不混淆。 |
| 独立客户端 | 已泄露 Key 直接 SDK/CLI 调 AI、无控制台会话；处置前后/在途请求 | 验证撤销后续鉴权；不假称当前审计扫描能发现所有直接 AI 盗用。 |

前端修改需覆盖规则目录、表单 parser/依赖校验、详情原因、响应状态、停止入口、客户风险投影、筛选及通知。复用固定 Sheet/Dialog 尺寸和稳定滚动槽；不改上游拥有的 auth-session/http-client/api 等五个文件。

数据库验收遵守 [生产环境](../production-environment.md) 与 [发布清单](../release-checklist.md)：真实 MySQL + PostgreSQL + SQLite、主日志分库、Redis 实际鉴权与失效、全新/升级/第二次启动无 DDL、schema-only 演练。版本及命令在实施后记录；本方案不借用上次矩阵通过结果代替本次新增表验收。

## Pre-implementation Characterization

2026-11-18 执行 `go run ./_local/audit-boundary-review-20261118`，调用实际 Normalize/Evaluate/EvaluateAccountCompromise 及模型处置入口；每个处置用例使用独立内存 SQLite 和虚构账号，Redis 关闭，不读取/修改真实账号凭据。

结果见 `_local/audit-boundary-review-20261118/characterization.log`：

- 一个现有 Key、5 个登录 IP、3 个取钥 IP：命中 `multi_source_key_access`。
- 单账号单 IP 单次取钥、单次创建：未命中现有自动规则；创建也没有普通敏感告警。
- 取钥已到、较早登录后到：未补判。
- PAT 签发、改密、关闭 MFA、解绑、注册 Passkey：只有普通敏感告警。
- 已标误报告警：封禁仍 committed；两种清理仍 succeeded。清理用例没有凭据，只证明授权路径未停止，不证明删除了正数条凭据。

这是一组**现状特征复现**，不是修复后通过，也不是完整浏览器/服务集成验收。本轮未部署，未启用真实自动策略。

另执行 `go test ./middleware -run ^TestConfigureTrustedProxies -count=1 -v`：6 个顶层测试全部通过，覆盖不可信来源伪造转发头、多跳转发、禁用代理、显式配置替换与无效配置。运行进程显式将 `SQL_DSN`/`LOG_SQL_DSN` 设为空，harness 确认跳过 DB 初始化；这些用例不依赖数据库。日志为 `_local/audit-boundary-review-20261118/trusted-proxy-tests.log`，不把该结果扩展为生产代理验收。

## Implementation and Verification

实际代码按以下链路落地：

- `model/audit_identity.go`、认证/安全控制器和模型提交接口：服务端身份快照、提交后认证版本、刷新重放原身份；已提交但缓存收尾失败的操作仍留下事实。原始身份从审计/客户接口投影中剔除。
- `service/auditsecurity/sensitive_operations.go`、`response_eligibility.go`、`ip_rules.go`：三条单次规则、精确有效 IP 证据、成熟来源基线、120 秒原事件期限；只有一次创建或查看 Key 也可检测。权限降低、未提交和无实际变更不冒充敏感成功。
- `store.go`、`model/security_response_group.go`：迟到窗口复用、判定去重、跨规则动作并集、原事件与原组绑定、只继承同组封禁版本。身份/策略变化后不读取当前版本替代旧证据。
- `model/security_account_response.go`、`security_credential_cleanup.go`：三个动作独立，固定凭据范围和提交回执；人工停止仅限 Root 浏览器会话及绑定组的二次验证，准备好的固定批次完成收尾后停止。低权限排查、误报标记或删除不能代替停止授权。
- `account_response.go`、`alert_query.go`、`customer_access.go`、删除和通知流程：关联告警共享实际结果，仍显示较新的人工动作和未完成历史动作；客户投影不泄露其他账号证据；活动组保留，终态孤立组有界回收。
- 前端七规则表单、详情和客户摘要：新增三规则默认只告警，三动作独立，依赖校验和停止状态齐全。复用固定外框及内部滚动；没有增加未经确认的产品文案。

测试只使用本机隔离数据库、合成账号和测试凭据，不读取或更改生产账号。原始日志保存在 `_local/audit-boundary-implementation-20261118/`，其中数据库连接配置不写入文档。

| 验证 | 本轮结果与证据 |
| --- | --- |
| 安全子系统完整回归 | `go test ./service/auditsecurity ./setting/operation_setting ./i18n -count=1` 在 SQLite、真实 MySQL、真实 PostgreSQL 均通过。后续迟到窗口调整另在 PostgreSQL 定向通过，最终 SQLite/MySQL 完整回归通过；最终索引和投影补测在两种服务器数据库通过。日志 `full-security-mysql.log`、`full-security-postgres.log`、`boundary-final-postgres.log`、`final-sqlite-suite.log`。 |
| 新规则和生命周期 | `boundary_rules_test.go` / `boundary_lifecycle_test.go` 覆盖单 Key 只清理不封禁、匿名/伪造/缺失身份、精确/宽网段/过期 IP 证据、成熟来源、IP 补证不更新身份和期限、迟到去重、同组版本继承、旧事件不重开、停止准备批次、审查/删除不能终止活动组。 |
| 新表迁移与查询 | `TestBoundaryMigrationUpgradesPreviousSchemaAndRestartsWithoutDDL`：新建后去掉本次字段模拟旧结构，保留旧事件和 sync_pending 回执，再升级并执行两次迁移，无重复 DDL。`TestBoundaryResponseQueryPlans`：2000 组数据，active/previous/link/action/orphan 查询均使用对应索引；PG 的清理查询无额外排序。日志 `boundary-schema-mysql.log` / `boundary-schema-postgres.log`；SQLite 同用例包含在最终完整回归。 |
| 主库和独立日志库 | `go test ./controller -run 'Test(AuditDatabaseMatrix\|IndependentAuditLogStores)$' -count=1 -v`：SQLite/MySQL/PG 主库新建及升级、MySQL/PG 独立日志库新建及升级、重复启动和存储投影通过。日志 `audit-matrix-mysql.log`。ClickHouse 分支因未配置实例跳过。 |
| 停止 HTTP 与安全提交 | MySQL/PG 的 `controller-{mysql,postgres}.log`：无 proof、管理员、PAT、错误组 proof 均拒绝；Root 绑定 proof 成功，重复 request_id 返回同一回执；MFA/Passkey 提交及回滚相关回归通过。 |
| Redis 和实际鉴权 | `TestSecurityCleanupFencesLateCacheRefillAndQuotaSettlement` 验证清理后旧缓存回填、晚到额度结算不能复活 Key；`TestAutomaticCredentialCleanupAuthenticationIsolation` 验证两种独立清理后的实际鉴权。`model-postgres.log`、`credential-auth-mysql.log`。`TestAuditCommittedVersionSurvivesCachePublishFailure` 验证提交后缓存故障仍返回真实版本；找回密码用例验证邮箱规范化及唯一目标。 |
| 前端行为与静态检查 | 10 个测试文件、92 项测试通过，覆盖设置、处置、验证流程和客户摘要。`bunx tsc -b` 通过；本次 12 个前端文件 scoped oxlint 无错误。项目现有 `bun run typecheck` 脚本使用不支持的 `bun check -b`，因此运行实际 TypeScript 构建器验证。 |
| 构建与格式 | 最终 `bun run build` 和 `go build -o _local/audit-boundary-implementation-20261118/new-api.exe .` 均通过。12 个前端文件采用移除版权头后的 stdin 格式比对，全部一致；本次改动的 `git diff --check` 通过。日志 `frontend-build.log`、`backend-build.log`、`frontend-format.log`。 |
| 七语言浏览器页面 | Chrome 合成 API fixture，7 语言 × 2 主题 × 2 视口 × 3 页面 = 84 项通过，零 pageerror；验证三动作独立、依赖错误、停止确认/取消、详情和客户结果、切换标签宽高差小于 1px、视口内无横向溢出。证据 `ui-qa.py`、`ui-geometry.json` 和同目录截图。这不是连接真实后端的浏览器端到端测试。 |
| 文案与受保护文件 | 12 条 × 7 语言与确认稿逐项一致，UTF-8 无 BOM。五个上游前端文件与当前 HEAD 字节一致，未修改。当前本地 `main` 仍是缺少这些文件的旧分支，不能用它覆盖当前上游认证实现。 |

数据库实测版本：SQLite **3.50.4**；MySQL **26.10.0**；PostgreSQL **18.7 (Debian 18.7-1.pgdg13+2)**；Redis **7.4.11**。服务器数据库通过本地 Docker 隔离实例执行，`test-db.py` 为每条命令注入测试 DSN；服务 worker 测试创建独立测试库。

尚未执行的发布验收：生产实际版本和 schema-only 升级演练、生产数据新唯一约束预检、ClickHouse 独立日志库、本次完整浏览器与真实后端联调、30k/100k RPM 压测、生产可信代理拓扑核验。上述项目需在发布环境完成；本轮不宣称通过，也未部署或启用真实自动策略。

## 2026-11-21 Evidence and Correlation Corrections

### Existing Logic and Related-Path Findings

- `store.go` 的 `commitSecurityBatch` 分别加载通用关联历史与盗号组合历史。`newFindingWork` 原先采用规则显式提供的 `Evidence`，却继续继承通用历史的 `truncated`。因此，即使高危 IP 登录、高危 IP 敏感操作、新来源敏感操作的当前事件证据完整，也会因无关历史超过 1000 条而被标成证据不完整，阻止已配置的自动动作。
- `account_compromise.go` 的 `EvaluateAccountCompromise` 按秒级时间、事件编号排序；`multi_source_key_access` 在同一循环收集登录与取钥。相同秒内取钥事件编号排在登录前，会漏掉本应符合 `key.At >= login.At` 的关联。多账号取钥分支已分别收集两类事件，不存在这个顺序依赖。
- `response_evidence_scope_test.go` 在修复前已复现上述失败：三个单次规则错误标记截断；登录编号排在取钥之后时，组合规则无命中。真正组合历史截断仍会阻止自动动作，作为反向回归保留。

### Scope, Lifecycle and Main Chain Impact

修复沿用现有规则、阈值、等级、动作选择、可信身份和 120 秒原事件期限，不实施已撤回方案中的降级或强制仅告警。显式证据采用其自身 `Truncated`；多来源规则先收集登录，再按实际时间关联取钥。晚于取钥的登录仍不能补足该取钥的先前登录条件。

数据流仍为后台扫描 → 证据关联 → 告警/处置组 → 现有后台执行器。API、认证授权、模型、迁移、配置项、错误处理与失效协议均沿用本文已实现的约定。没有新增 DB/Redis 查询、连接、锁或 goroutine；证据内存处理使用现有有界历史。`security_*`、`users`、`tokens`、PAT 表和认证缓存的读写范围、索引与隔离池不变，主 AI 中继链路新增同步开销为 0。

修复后已覆盖：完整单次证据在通用历史截断时仍能完成选中的三个动作；真正的组合证据截断仍跳过处置；同秒编号顺序不影响命中；未来登录不回溯授权；并执行现有身份、期限、迟到补判、幂等、动作独立及普通代理降噪回归。

### Verification

执行命令：`python _local/auto-response-evidence-fix-20261121/run_checks.py`。脚本清空业务主库/日志库 DSN，在本机隔离数据库使用合成账号和凭据；只创建和删除本次拥有的测试库。数据库口令未写入日志。

| 环境 | 命令与结果 |
| --- | --- |
| SQLite（项目驱动，本轮未查询引擎版本） | `go test ./service/auditsecurity -count=1 -v`：141 个顶层测试通过，9 个环境专项跳过，退出码 0。跳过项为 3 个独立 Redis 用例、5 个服务器数据库查询计划用例和 1 个 ClickHouse 用例。 |
| MySQL 26.10.0 | 同一包通过脚本中的 `-run` 精确选择新增和相关的 21 个顶层测试：全部通过，无跳过，退出码 0。 |
| PostgreSQL 18.7 (Debian 18.7-1.pgdg13+2) | 同上 21 个顶层测试全部通过，无跳过，退出码 0。 |

结果、版本和完整日志保存在 `_local/auto-response-evidence-fix-20261121/results.json`、`sqlite.log`、`mysql.log`、`postgres.log`。新增回归先复现失败再验证修复；`gofmt` 和相关 `git diff --check` 通过。本轮未改 SQL、schema、缓存失效或 relay 路径，未重做 Redis/ClickHouse 专项、迁移或压力验收；未部署或修改线上自动策略。

## Review and Copy Approval

新增/变更产品文案统一列于 [七语言文案稿](audit-security-boundary-hardening-copy.json)，用户已明确采用全部文案，组件、七种前端语言包和适用后台消息已写入。已有三个动作、通用保存/取消/刷新错误继续沿用已确认文案；此后其他新增文案仍须先确认。

本方案、新增规则、处置组和停止权限已按用户“采用方案及全部文案，开始实施”获得确认；实现期间未重复请求已授予的授权。

## References

- [OWASP Authentication Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)：风险信号、敏感操作复核及避免攻击者利用锁定造成拒绝服务。IP/设备信号不能替代认证身份。
- [OWASP Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)：会话生命周期、事件关联和避免将原始会话标识写入日志。
- [OWASP ASVS](https://asvs.owasp.org/)：本次阅读站点列出的稳定版为 5.0.0；用于检查验证、会话、权限与日志边界，不据此声称项目通过认证。

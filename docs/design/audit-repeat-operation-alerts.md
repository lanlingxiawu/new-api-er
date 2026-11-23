# 重复操作告警聚合与确认

重复操作聚合和一键确认已在本地实现，尚未部署。系统依据服务端凭据来源和具体操作区分重复事件；访问令牌不代表机器人身份或可信来源。

## Goals and Scope

保留每次操作的检测和证据，减少已确认的、条件完全相同的重复操作占据待处理列表。界面继续一次点击确认，不增加处置表单、关注状态、白名单或新设置项。

本次聚合仅覆盖使用服务端验证的访问令牌、成功执行、实际改变普通用户余额的 `user.quota_add` / `user.quota_subtract`，且命中规则为 `sensitive_operation`、等级为高（2）。首次正常创建待处理告警，同一操作模式从首次事件起最多合并 24 小时；确认后同类重复继续累计，不反复重开。

“相同模式”同时要求：相同操作者及角色、认证版本、令牌种类及不可用作认证的指纹、授权范围摘要、操作类型、规范化 IP、客户端摘要、目标用户角色、准确额度变化量、资源类型和可见性。普通用户目标 ID 可以不同；金额或其他条件改变即为另一条待处理告警。24 小时为固定上界，不因持续命中向后延长。

余额覆盖、权限变更、密钥操作、渠道操作、身份/证据缺失等不进入此次扩展聚合。批量敏感操作、风险 IP、新来源及其他安全规则照常独立判断和处置。这不是完整行为异常模型，也不能识别持有相同凭据并模仿原操作模式的攻击者；其检测仍依赖既有批量、来源和关联规则。

## Existing Logic and Related-Path Findings

| 代码 | 已核实行为与本方案衔接 |
| --- | --- |
| `middleware/auth.go:authHelper/setAccessTokenContext` | 凭据已验证后记录 `access_token_ref/id/scopes/legacy`。这些是服务端上下文，不能从请求参数或 User-Agent 推断凭据身份。无需额外查询令牌表。 |
| `model/audit_identity.go:captureAuditIdentity`、`model/audit_log.go:RecordAuditLog` | 业务日志保存认证方式及身份版本，但没有从上下文复制 TokenRef。独立 HTTP 访问审计有该字段，不能拿它替代业务操作事实。补充可信凭据元数据，仍不记录可用令牌。 |
| `controller/user_quota.go:manageUserQuota`、`model/user_quota_adjustment.go:AdjustUserQuota` | 成功路径保存事务内实际 from/to 和增减额度，但返回快照未带目标角色，日志也没有 target_role；目标角色未知时 Normalize 会限制为 Root 可见。事务已经读取目标 User，方案只把该次读取的 Role 带入审计，不追加 SQL，不改变余额计算/锁/缓存结算。已知普通目标按既有可见性规则投影，旧未知角色仍保持原限制。 |
| `service/auditsecurity/rules.go:Normalize` | `Facts.TokenID` 指操作对象，例如新创建的 PAT，不是调用者令牌。新凭据来源只从受保护的 Identity/TokenRef 获取；任意 Params 不能覆盖它。 |
| `rules.go:Evaluate/highRisk` | 等级来自规则，不是加权评分。成功额度增减为高；5 分钟内敏感操作达到 BatchThreshold 会另建严重规则。汇总 UI 不得移除这些基础事件或改变其计数。 |
| `store.go:commitSecurityBatch` | 现有分组按规则/账号/可见性/资源/规则版本和相邻 15 分钟合并，忽略调用令牌、IP 和具体动作。resolved/false_positive 每次新命中均重开。新逻辑只替换合格重复操作的分组查询；普通查询必须排除新重复组，防止其他操作误合入。 |
| `store.go` 的证据拼接 | 通用历史按账号与动作拼接，会掺入其他令牌/来源的关联记录。新重复组只追加本组实际命中的事件，不把通用历史当作本组重复命中；保留原有有界证据和全部 finding 快照。 |
| `investigation.go:UpdateAlertInvestigation` | 当前确认要求精确 Version；每次检测同时使 Version/Occurrences 各加一，人工编辑、归档、清理、处置组修改只增加 Version。此不变量允许对严格同类重复确认已展示快照，同时拒绝期间发生的人工/处置变更。 |
| `finding_snapshot.go`、`account_response.go`、`response_eligibility.go` | finding 去重、证据、通知和自动处置独立于人工状态；继续逐次运行，不因一次确认跳过判定。保留原身份、120 秒事件期限、动作选择和失败回执。 |
| `alert_query.go`、`summary.go`、`relations.go` | 当前待处理仅 open/investigating，支持 ID/活动游标，活动变化推进 revision。复用现有四个状态和分页合同，聚合后仍推进活动 revision。账号关联继续保留 SubjectRef，不改为令牌摘要。 |
| `security/alert-confirm-button.tsx`、`security-alert-dialog.tsx` | 已实现一次提交；详情冻结阅读快照，后台变化要求刷新。重复组仅在可证明只是同类新增时允许确认旧阅读快照；其他变化继续禁用并提示刷新。 |
| `model/audit_cleanup.go`、`service/auditsecurity/deletion.go` | 留存按 ClosedAt，批量清理与删除有 Version/活动处置保护。聚合不自动删除、归档或改写旧告警；已归档/正在清理的重复组不再接收新事件。 |

## Trigger Levels and Automatic Response

| 情况 | 当前等级/默认阈值 | 本次决策 |
| --- | --- | --- |
| 普通 VPN/代理 | 无单独告警 | 沿用已确认降噪；保留事实。 |
| Tor、连续登录失败 | 中；失败默认 5 次/5 分钟 | 保留，不据此封禁被尝试的账号。 |
| IP 滥用、失败后成功、密码喷洒、频繁找回/权限拒绝、异地移动 | 高；各自原阈值 | 保留关联、有效证据与原等级。 |
| 单次额度增减、其他敏感操作 | 高 | 只对符合本文精确条件的重复额度操作聚合，等级不降低。 |
| 批量敏感操作/取钥 | 严重；默认 5 次敏感操作或 10 个渠道/5 分钟 | 保留独立告警及重开，不能因为使用访问令牌免除。 |
| 权限变更、会话重用、风险 IP 成功登录/敏感操作、新来源敏感操作、盗号组合 | 严重 | 原规则、自动处置资格和所选动作保留。 |

默认值来自 `DefaultAuditSecuritySetting`，不是已读取的线上配置。不会在没有新的业务依据时把所有等级提高/降低，也不改为 VPN/IP 评分封禁。针对自动处置的错误截断和同秒关联漏判已在上一轮修复，本方案不恢复已被否决的强制仅告警策略。

## Data Flow and Business Logic

1. 管理鉴权成功 → 原处理器执行 → 同步审计仅复制已经存在的安全上下文/事务快照 → 原日志存储。UI 和日志显示“访问令牌”，不显示“已识别机器人”。
2. 后台读取并 Normalize。解析身份匹配、实际成功、from/to/正额度、普通目标角色；额度与实际变化不一致、客户端摘要为空、IP 无效、身份不全均按原路径告警，不进入新聚合。客户端摘要只用于区分，不能证明可信。
3. 先运行全部原检测/组合/IP 规则。仅合格 sensitive_operation 计算带版本前缀的 SHA-256 分组键；包括上述全部边界，不存可用凭据，也不把密钥目标 ID 当作调用令牌。
4. 查找相同键且 `event.At < repeat_until <= event.At + 86400`、未归档/清理的组，选择结束时间最近的一条；没有则创建 open 组，repeat_until 固定为 event.At + 86400。两个时间条件保证迟到事件不能回并到起点晚于它的组，也不能无限延长窗口。索引查 ID 后重新读取完整行，再检查归档、清理和分组边界；若期间发生变更，复用原三次事务重试，不能将新事件写进隐藏组。
5. 每次新命中仍增加 Occurrences/Version、更新最近事件和峰值、保存 finding、实际命中证据和风险关联；重复回执不重复累计。普通已确认组保持 resolved，ConfirmedOccurrences 不自动增加；误报、恶意结论、风险升级不享有此保持行为。
6. 一次确认仅记录用户已见数量。表格和详情显示总触发数及“确认后新增 N 次”，不强制再次操作；新条件创建另一条待处理告警。用户仍可用原详细入口重新打开/更改结论，不增加关注状态。
7. 新组、金额/凭据/来源/授权/角色改变、时间窗到期会要求重新确认。新风险由原独立告警处理，不把它隐藏在已确认的普通操作组。

## API Contracts and Authorization

复用 AdminAuth + `AuditSecurityView/Manage` 下的 GET `/api/audit/security/alerts`、GET `/api/audit/security/alerts/:id`、POST `/api/audit/security/alerts/:id/actions`。确认仍要求浏览器会话；PAT 不能自行确认自己的调用。无新增公共端点、批量接口或权限。

返回 SecurityAlert 增加可选 `repeat_until`、`confirmed_occurrences`。分组键和凭据来源保持内部字段；个人/客户 DTO 不直接序列化管理字段，既有投影与角色边界保留。

快捷确认请求仍为 `{version, status:"resolved", note:""}`；合格重复组另带 `observed_occurrences`。只有该字段存在、仅执行确认、没有备注/负责人/结论变更、组未归档/清理/过期且仍为普通高等级时启用快照确认。其他请求仍走精确版本合同。

快照确认校验 `0 < observed_occurrences <= current.occurrences`、`observed_occurrences <= version <= current.version`，以及 `version - observed_occurrences == current.version - current.occurrences`。写入时以主键、相同差值及原状态/分组边界作为原子条件，避免“先检查再覆盖”竞争；保存 confirmed_occurrences 为用户提交的已见值，不替其确认后续事件。该方式依赖上述唯一命中增量不变量，必须有跨路径回归；期间任何人工状态/分配/归档/清理/处置更新仍冲突，不能自动刷新后重试。

请求完成后复用已有 QueryClient 刷新，不增加轮询。旧成功响应只能标记其对应的确认范围；页面以服务端结果和新增数展示。旧前端不传 observed_occurrences 时仍兼容原精确版本确认。

## 确认交互与查询生命周期

列表和详情复用 `AlertConfirmButton`、`Button` 和 `StatusBadge`：有管理权限时直接确认，保存中防重复提交；只读和误报状态不提供快捷确认。确认不填写负责人、结论或备注，原详细编辑保留在详情处理记录中。该组件承载告警版本与已见次数的业务约束，不替代通用删除确认组件。

成功显示绑定提交的 ID/Version，不能遮盖后来的新事件。详情冻结阅读快照；仅可证明是严格同类新增时允许确认旧快照，其余变化仍需刷新。失败使用现有错误提示，不自动换版本重试。完成回调复用 `refetch({cancelRefetch:false})`，等待正在进行的详情刷新。

`securityGet` 使用共享 HTTP 客户端已有的 `disableDuplicate` 选项，由 React Query 负责相同查询去重和取消。这样确认后的替代查询不会复用刚取消的 HTTP Promise，避免保存成功后列表出现 `ERR_CANCELED`。共享认证/HTTP 模块、轮询周期和权限不变。`security/api.test.ts` 通过真实 QueryClient 与可取消 Axios adapter 覆盖列表、详情、健康查询和并发去重。

## Data Model, Indexes and Migration

`audit_security_alerts` 新增可空列：`repeat_key varchar(64)`（内部）、`repeat_until bigint`、`confirmed_occurrences bigint`；不改变 Status 枚举。旧行保持 NULL，不批量回填身份、组或确认数。

索引 `idx_sec_repeat_episode` 为 `(repeat_key, repeat_until, purge_state, archived_at, id)`。查询先只取 id，以等值键和有界结束时间区间走覆盖索引，再按主键加载最多一条；原 15 分钟查询增加 `repeat_key IS NULL`，不会混入重复组。旧索引保持不动。迁移使用 GORM 跨 SQLite/MySQL/PostgreSQL，不添加 TEXT 默认值，不把测试 DDL 用于生产。

已在三库针对实际 SecurityAlert 表检查 4,000 条不同分组/时间/归档分布的 EXPLAIN，均使用该覆盖索引。验收覆盖新建、移除本次新增字段恢复上版结构后插入旧行再升级，以及连续两次重启不产生 DDL。另保留更早版本结构的升级回归。生产发布前仍须针对目标 schema 做升级演练，本次没有访问生产数据库或部署。

AuditIdentity 增加服务端凭据种类、指纹、授权摘要；序列化到现有审计 JSON，不增审计表列。RecordAuditLog 同步保存既有 TokenRef 列。UserQuotaAdjustment 只增加返回快照 Role，quota SQL 不变；日志/客户/详情投影继续移除内部身份材料。无配置项。

## Error Handling and Lifecycle

指纹/身份/金额证据不足时保留原检测，不静默忽略。数据库写失败时整个扫描事务/游标回滚，复用原三次版本冲突处理及健康告警；确认失败通过原冲突/不可用文案反馈，不伪装已确认。原错误日志使用 logger/common SysError，禁止凭据、完整请求或连接串。

窗口内主动归档后，新事件创建新待处理组，不把新记录写进隐藏归档；删除/清理中的组同样排除，活动处置保护不变。重复命中不修改原人工确认时间；24 小时窗口短于当前最小 30 天留存。人工“清理已关闭”仍是显式删除动作，版本与活动处置边界保持。

命中快照和通知回执保留原上限/留存，不把证据摘要误称全量。原管理员通知冷却和账号通知策略保留；一次确认不取消已排队通知或自动处置。没有取消处置、自动标记“已授权”或放宽封禁证据。

## Main Chain Impact

主 AI relay goroutine 新增 SQL、Redis、锁、网络调用、goroutine 均为 0。仅管理审计复制固定长度凭据摘要；额度事务使用已读取的目标角色，不新增查询或修改结算。关联/合并在现有后台 worker，UI 仍使用管理 API。

## Shared Resource Audit and Concurrency Analysis

| 资源 | 与 relay 的关系及增量 |
| --- | --- |
| `audit_logs` / 审计队列 | 原管理审计写入，现有 TokenRef 和 JSON 多出少量安全元数据；不接入消费日志热路径，不增加每 AI 请求写入。 |
| `audit_security_alerts/findings/events/actions/alert_events`、扫描租约和 revision | 监控专用表/隔离管理连接池，relay 不访问。合格命中以索引查询替代原分组查询，再增加至多一次主键读取；原快照/动作写入次数不变。 |
| `users` | 额度调整已有锁内 User 查询，返回 Role 不增加 SQL、锁持续业务或新缓存写。确认不查目标用户或令牌表。 |
| `tokens/channels/PAT`、认证 Redis | 只沿用原处置/管理调用；此次不新增访问或修改命名空间。聚合不作为认证许可。 |
| 内存/worker/池 | 每 20 个输入的原有事务和有限 worker；每个合格事件固定摘要及最多 20 条证据，不全表扫描、不新增池/无界 goroutine。 |

30k/100k AI RPM 下本功能每 AI 请求新增资源访问仍为 0；管理请求和监控 backlog 依原有预算处理。任何聚合失败不应影响 AI 请求。新索引只增加安全告警表的写入成本，需实际检查迁移与查询计划，不能把此分析当作压测。

## Implementation Map and Verification Plan

- 服务端凭据来源：`model/audit_identity.go`、`model/audit_log.go`；扩展现有 identity 测试和真实业务审计采集测试。必须拒绝 Params 冒充调用凭据，验证网页/无身份/PAT/legacy 四类及指纹不含可用令牌。
- 目标角色：`model/user_quota_adjustment.go`、`controller/user_quota.go`；扩展额度操作原测试，核对权限、目标角色、from/to、不变提交、失败不进入聚合及余额/缓存合同不变。
- 聚合/确认：`model/audit_security.go`、`service/auditsecurity/store.go`、`investigation.go`、`controller/audit_security.go`；新业务测试覆盖固定 24 小时内/边界/外、迟到、重复回执、同账号不同凭据/IP/金额/角色/动作、历史空元数据、误报/恶意/人工重开、归档、清理及并发确认。新通用组不能合入重复组。
- 安全保留：每次原命中/批量计数/自动处置仍发生；确认后风险 IP 补证、新来源、批量和取钥关联照常告警、依原选择处置。真正缺失/过期证据仍不能执行。客户端或 UA 相同不是授权依据。
- 前端：`security/api.ts`、`alert-confirm-button.tsx`、`security-alert-columns.tsx`、`security-alert-dialog.tsx`、`security-rule-summary.tsx`，复用 StatusBadge/Button/DetailRow。新增 `alert-trigger-count.tsx` 同时用于列表及详情已有触发次数行，不额外复制一行计数；`lib/repeat-operation.ts` 共享快照资格检查。新文案为状态说明，不要求用户填写新字段。
- SQLite、真实 MySQL、PostgreSQL 运行行为/并发/迁移/索引回归；独立日志库存取验证 JSON 兼容。记录各自版本/命令/退出码/跳过项，不把旧特征测试当作新实现通过。

## 验证与发布边界

回归测试随实现保留，覆盖三库的固定窗口、凭据/金额/来源隔离、并发确认、人工修订冲突、归档/清理竞态、自动处置，以及新库和旧 schema 升级后再次迁移无 DDL。前端覆盖一键确认、只读状态、旧响应、请求取消与新增触发次数；新增文案覆盖七种语言。

旧告警缺少可信凭据元数据，不追溯聚合；新行为从升级后采集到的合格事件开始生效。生产发布仍须针对目标 schema 演练迁移，本地定向回归不代替完整发布验收。

## Security References

2026-11-22 查阅 [OWASP ASVS 官方稳定版 5.0.0](https://asvs.owasp.org/)、[Authentication](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)、[Session Management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)。本次适用点为服务端鉴权/审计身份、会话隔离、权限变化和日志中的敏感信息保护；不宣称本项目通过整个标准认证。

[Logging Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html) 要求保留内部系统等来源的事件、避免直接记录访问令牌，并结合重复次数、关联和上下文评估告警；因此本文保留每个事件，不依据“机器人”名称豁免，不把 IP/User-Agent 相同当作安全证明。

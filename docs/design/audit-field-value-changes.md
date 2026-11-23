# 审计日志字段新旧值

日期：2026-11-16  
状态：用户于 2026-11-16 明确“开始实施”；已实现，完成本地专项验证，未部署

## 目标与范围

用户要求审计日志保存并展示新旧值，重点是渠道，并明确同时补齐用户、令牌、系统设置。密钥不显示：本方案进一步在写入端排除具体凭据，避免数据库、接口或导出仍然包含被界面隐藏的秘密。

统一展示“字段 / 旧值 / 新值”，只列本次操作实际写入且发生变化的字段。创建显示“未存在 → 新值”，删除显示“旧值 → 已删除”。凭据只记录发生了设置、替换或删除，不保存值、片段、长度或新增指纹。

本次覆盖人工管理操作及其管理 PAT 入口；包含已有批量入口。自动余额刷新、请求消耗、自动禁用/恢复、自动凭据刷新等运行时过程不扩展为逐请求配置审计。读取密钥的既有事件仍保留访问事实，不添加返回的密钥。历史缺失的新旧值不能从当前配置反推。

## Existing Logic and Related-Path Findings

以下为设计前核实的实现情况。工作区已有大量其他修改，本任务在其基础上实施，保留其他修改；实施后的差异与验证结果见末节。

| 代码位置 | 已核实行为与影响 |
| --- | --- |
| `model/audit_other.go`：`AuditOther`、`AuditOperation`、`AuditFields` | `other` 已容纳结构化元数据；`AuditFields.UnmarshalJSON` 保留原始 JSON 数值精度。可以增加可选变更结构，不需要增加数据库列。 |
| `controller/audit.go`：`recordManageAudit`、`writeManageAudit`；`middleware/audit.go`：`finishAdminAudit` | handler 手动记录后设置去重标记，中间件仅兜底；兜底没有业务旧值，不能通过通用请求正文抓取实现正确差异。 |
| `model/audit_log.go`：`RecordAuditLog`、`AuditLogStorageRow` | 管理审计当前同步写独立的 `audit_logs`；写入失败记录错误，不回滚已完成业务。ClickHouse 有单独序列化适配。 |
| `model/audit_log.go`：`ProjectAuditLogs` | self 视角移除管理和请求扩展，但保留 `op.params`。因此新增渠道/系统值不能直接放到不受投影控制的参数中。 |
| `controller/access_token.go`：`GetAuditLogs`；`model/audit_query.go`：`QueryAuditEvents` | 列表与事件回查均调用投影；列表已有页码、筛选和查询超时，新增字段无需改变分页。 |
| `service/auditexport/source.go`：`Reader.Read` | 导出在序列化 `other` 前调用同一个投影函数；可以保持现有 CSV 列，让脱敏差异随 `other` 导出。风险记录回查也要使用相同规则。 |
| `service/auditsecurity/rules.go`：`Normalize`、`Facts` | 安全告警只接受严格字段白名单，不保存任意值。继续使用现有 `changed_fields/count/changed` 等判断；不能把新旧值复制进告警和通知。 |
| `controller/channel.go`：`UpdateChannel`；`model/channel.go`：`UpdateConfiguration` | 控制器的旧渠道在事务前读取；保存事务锁定渠道 ID，更新渠道、限额、能力和成本，然后回读。现有比较只保留部分字段名，缺少名称、标签等完整字段和新旧值；事务前对象不能冒充并发安全的旧值。 |
| `model/admin_token_channel.go`：`UpdateCreatedChannelsWithChanges` | PAT 批量管理已有同一事务内的前后对象、所有权限制、最多 100 条、实际变更 IDs 和字段名；这是最直接的差异采集点。 |
| `controller/channel.go`：标签、批量状态、批量标签、删除；`model/channel_daily_limit_batch.go` | 日限额批量当前返回真实变化数；其他部分入口仅记目标状态、提交字段或数量。标签编辑及日限额分两步，存在第一步已保存而后一步失败的生命周期，不能笼统把整个请求当作未变更。 |
| `model/channel.go`：`UpdateChannelStatus`；`controller/channel.go`：`ManageMultiKeys` | 状态方法也供运行时调用，使用共享状态锁和每渠道轮询锁；多密钥操作的审计刻意在解锁后执行。不能把 JSON 编码、日志写入或额外查库放入这些锁。 |
| `model/channel_priority.go`：`ReorderChannelPriority` | 已返回每个渠道的 `previous/priority`，并用 revision、事务及已有锁防止陈旧排序；预览不能记为已修改。 |
| `controller/channel_upstream_update.go`：`applyChannelUpstreamModelUpdates` | 已有标准化前后模型列表；配置保存后能力更新可能失败，全量应用逐批遍历并允许部分失败，需保存已完成部分。 |
| `controller/employee.go`：`AdminUpsertChannelCost`、`AdminDeleteChannelCost`；`controller/price_monitor_channel_cost_http.go` | 成本有独立保存入口；价格监控已记 `from/to`，但旧值取自缓存。成本实际存放在 `channel_cost_configs`，不是 `channels.cost_ratio`。 |
| `controller/user.go`：`UpdateUser`、`ManageUser`；`model/user.go`：`UpdateWithTx`、`EditWithTxGroupRatios` | 用户编辑只记少量字段名。管理启停/升降级已持有旧角色与状态；部分保存有行锁，普通编辑并非一律锁定。提交后还会刷新权限/缓存、撤销会话，存在 `committed/followup_failed`。不能丢弃已提交差异。 |
| `controller/user.go`：`UpdateSelf`、`UpdateUserSetting`；`model/user.go`：`UpdateUserSetting` | 自己的资料、语言、侧栏和通知设置有独立入口；设置从存量合并，通知方式切换会清空旧方式的凭据。密码变更单独走安全验证及会话轮换，不能采集原密码或散列。 |
| `controller/user_quota.go`：`manageUserQuota` | 额度操作已使用 `AdjustUserQuota` 返回的真实 `Before/After`，失败没有已提交余额；直接适配既有结果，不另读缓存余额。 |
| `service/authz/override.go`：`UpdateUserPermissionsInTx` | 已在事务内读取并比较实际权限覆盖规则；权限数据不在 users 普通字段中，须在此提取授权变化，保留默认继承与显式允许/拒绝的区别。 |
| `controller/token.go`：`UpdateToken`；`middleware/audit.go`：`TokenOperationAudit` | API 令牌已有字段白名单比较，状态修改已有 `from/to`，普通编辑没有保存值。需要在原中间件事件上附加差异，避免再写一条重复日志。 |
| `model/token.go`：`Update`、`SelectUpdate`；`model/user_access_token.go`：`UpdateUserAccessToken` | API 令牌更新前有缓存失效及 fence；PAT 更新按所有者写入后回读。它们并非都返回原子前后快照，不得为审计绕开缓存/所有权或修改运行时 token 方法。 |
| `controller/access_token.go`：`UpdateAccessToken` | PAT 改名和改权限使用不同事件；改权限已保存 `previous_scopes/scopes`，仅改名不读旧名。创建/撤销也有既有事件。 |
| `controller/option.go`：`UpdateOption`、`UpdateOptionGroup` | 目前仅记录 key 或 module；单项、分组、请求策略、价格、Passkey 域名有不同保存分派，不能只改一个控制器。分组保存可出现 DB 已提交而内存应用失败。 |
| `model/option.go`：`UpdateOption`；`model/config_group.go`：`SaveConfigGroup`、`persistOptionsTx` | 普通单项分支未检查 `FirstOrCreate/Save` 的错误；在这个分支仅依据 nil 返回值记录“已保存”不可靠。本任务需补齐该保存结果检查，并加回归，不能伪造提交成功。分组值可能被服务端规范化。 |
| `model/request_policy.go`：`UpdateRequestPolicyOptions`；`model/model_pricing_config.go`：`mutateModelPricingOptions` | 请求策略和定价已有独立管理锁、事务及发布流程；定价还有版本检查、默认继承和管理员覆盖。审计接入其提交结果，不另建价格写入或计算路径。 |
| `controller/custom_oauth.go`；`model/custom_oauth_provider.go` | 自定义 OAuth 设置存在独立表，更新先读后修改对象，ClientSecret 为空时保留；成功后更新 OAuth 注册表，删除受已有绑定检查限制。当前靠中间件兜底，不属于 options 通用保存器。 |
| `controller/payment_compliance.go`、`controller/pricing.go`、`controller/price_monitor_http.go` | 合规确认逐项写设置，存在部分完成；重置倍率走 ModelRatio 专用保存分派；价格监控设置调用 SaveConfigGroup。这些设置入口须一起接入，不能仅覆盖设置页通用保存按钮。 |
| `web/src/features/usage-logs/audit/lib/audit-details.ts`、`components/audit-log-details-dialog.tsx` | 详情已有操作、对象、参数、令牌状态及额度专用显示；当前没有统一字段对照。可复用弹窗、`DetailSection`、字段翻译和值渲染。 |

已有相关设计：`audit-security-monitoring.md`、`audit-log-retention-and-role-projection.md`、`audit-log-export.md`。本文新增的值仅用于原始审计；不改变安全监控“渠道分组/模型/优先级/权重是常规维护”的规则，也不将值纳入告警 Facts。

## 数据流与生命周期

请求 → 既有鉴权、二次验证与校验 → 业务读到旧配置 → 保存标准化配置 → 确认提交范围 → 构建白名单差异并脱敏 → 既有审计写入 → 服务端权限投影 → 详情/导出。

1. 采集在各业务保存点显式接入，不注册全局 GORM 回调，不读取整个请求体作为日志，不在异步 worker 中重新查询旧值。
2. 首选已有事务内锁定的旧对象与已写入对象。渠道单条编辑可将已有 `Select(id)` 锁定读取扩大为所需配置投影，复用原回读；PAT 批量与优先级排序直接复用已有结果。
3. 对目前不具备原子快照的入口，保留原业务并发语义，使用保存前观察值和本次实际写入字段，标记 `consistency=observed`；只有经过相同事务/既有锁保护的前后结果可标记 `atomic`。页面对此显示“旧值来自修改前读取”。不为了日志给 relay 共用数据增加大范围行锁，不把事务外读值描述为精确的上一版。
4. 比较范围必须等于实际写集：GORM 忽略的零值、未提交字段、只读统计、并发发生的余额扣减不得混入。新值从标准化写集或受保护的回读生成；不使用请求后不受保护的整行回查冒充本次写入结果。
5. 创建只在成功取得 ID 后记录；删除在执行前保留安全快照，在成功删除后挂接。目标不存在不能伪造删除快照。取消、验证失败及完整回滚没有已提交差异。
6. 批量逐目标归档，保留 ID 和名称快照；先固定本次作用域，标签改名不能按新标签重新扩大采集范围。原入口允许分步/部分完成的，分别记录已提交目标或阶段，汇总为 `partial`，保留失败数量与后续处理失败状态。
7. 对无法可靠确定是否保存的步骤，标记 `unavailable`，记录无凭据的内部诊断；不把“未记录”显示为“无变化”。明确已保存的步骤可以继续列出。
8. 原审计数据库故障仍不改变业务结果。日志存储不是业务库的跨库事务，本方案不承诺断电/进程崩溃时绝对不丢审计；不新增 outbox 或可靠消息队列。

## 数据结构与兼容

在 `AuditOther` 新增可选 `changes`，由 `model/audit_changes.go` 定义安全的变更结构与纯比较/投影规则。继续保留 `op.params` 的 action、目标、数量和现有字段名，兼容安全规则、旧客户端和导出消费者。

示意结构：

```json
{
  "changes": {
    "version": 1,
    "outcome": "committed",
    "targets": [{
      "resource": "channel",
      "id": "42",
      "name": "渠道 A",
      "operation": "update",
      "visibility": "admin",
      "consistency": "atomic",
      "fields": [
        {"path": ["priority"], "old": {"state": "value", "value": 1}, "new": {"state": "value", "value": 5}},
        {"path": ["key"], "old": {"state": "redacted"}, "new": {"state": "redacted"}}
      ]
    }],
    "truncated": false
  }
}
```

- `outcome`：`committed / unchanged / not_committed / partial / unavailable`。不覆盖原 HTTP/业务结果；已提交但后处理失败仍能显示差异。
- `operation`：`create / update / delete`。`path` 为片段数组，自定义模型名/权限资源中的点号不被误拆。
- `old/new.state`：`value / absent / redacted / unavailable / truncated`。`value:null`、空串、0、false、空列表、未存在分别表达；具体值不使用会丢零值的标量 `omitempty`。
- `value` 沿用 `common.*` 和原始 JSON 保真；超过 JavaScript 安全整数范围的整数使用十进制字符串并附 `value_type=integer`，前端不转 Number。额度显示沿用业务单位，不能把配额计数解释为美元。
- 比较先于脱敏。两个密钥虽然都会显示“已隐藏”，发生替换仍必须有一条变更；相同敏感值不凭掩码判断改变。
- 单次事件的新增 `changes` 编码上限 64 KiB，最多 100 个目标、1,000 个字段，单个可见值最多 4 KiB，嵌套遍历深度最多 8。字段按稳定顺序选取，达到限制标记 `truncated`；已记录数量由 targets/fields 得到，已知总数沿用原 op.params。覆盖所有批量入口不等于超大批量无上限保留全部值。
- 不增加表、列、索引或 JSON 查询。`audit_logs.other` 沿用当前类型和三库/ClickHouse 编码方式；不借本任务改历史迁移。保留期与删除生命周期继续由原审计设置管理。

## 字段范围与脱敏

| 对象 | 保存新旧值 | 只保留变化事实 |
| --- | --- | --- |
| 渠道 | 名称、类型、状态、标签、分组、模型、映射、优先级、权重、自动禁用、测试模型、组织标识、备注、日限额与恢复配置、成本、可公开的传输/插件/模型更新设置 | Key、账号余额 Token、OAuth 凭据、无法安全解释的 `other`、未知或可能承载凭据的扩展 |
| 多密钥 | 模式、总数、按操作时索引标识的启停变化、渠道最终状态 | 所有密钥正文、预览片段、删除原因中的原始上游错误；删除后索引位移不能假装其余密钥被替换 |
| 用户 | 名称、显示名、备注、角色、状态、分组、专属倍率、排除渠道、超时/重试策略、额度调整、权限覆盖、本人资料和通知开关/阈值 | 密码和散列、验证/恢复码、MFA/Passkey 私密材料、Webhook secret、Gotify Token、Bark 凭据路径等 |
| API 令牌 | 名称、状态、到期时间、可编辑额度、无限额度、模型限制、IP 白名单、用户额度查询开关、分组及自动分组/跨组重试 | 令牌 Key、完整 bearer；不复制创建或查看接口中的凭据 |
| PAT | 名称、作用域、有效期及创建/撤销事实；保留继承与显式授权区别 | bearer、TokenHash、TokenHint 不进入新增差异结构；既有安全关联字段合同另行保留 |
| 系统设置 | 已登记的布尔/数字/枚举、非凭据文本、结构化安全子项、请求策略、模型价格/表达式和默认继承状态 | SMTP 密码、API Key、Secret、私钥、数据库连接串、OAuth 密钥、监控提供商凭据、未知自由文本配置 |

安全规则：

1. 按资源及完整字段路径建立显式安全白名单，密钥字段优先隐藏；未知动态值默认隐藏。字段名含 `key` 不等于凭据，例如模型映射的字典键、`task_plugin_key` 是业务标识，须按语义分类。
2. `setting/settings/header_override/param_override` 不允许整体原样保存。已知安全子项可显示，认证头、Cookie、自由请求内容及未知子项只保留变化事实。无效 JSON 不回退为原串输出。
3. URL 去除 userinfo、query、fragment；自定义路径可能包含密钥，默认不输出路径，只有明确安全的固定路径可保留。脱敏后相同但原地址不同，仍保留“地址已变更（部分隐藏）”。Webhook/Bark 等凭据型地址整体隐藏。
4. 比较、复制、JSON 序列化失败不得在诊断中打印原值；日志只包含 request ID、资源 ID、字段路径与错误分类。前端使用文本渲染，CSV 沿用现有公式防护。
5. 用户明确不显示密钥；root 也没有“展开密钥”按钮。列表、详情、复制、原始 JSON 和导出执行相同脱敏合同。

这些规则与 [OWASP Logging Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html#data-to-exclude) 的凭据排除和日志故障隔离要求一致。认证相关采集继续遵循 [ASVS](https://asvs.owasp.org/)、[Authentication](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html) 与 [Session Management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)，保留既有二次验证、会话轮换、所有权和撤销顺序；本次不重新设计认证。

## API 合同与授权

不新增公开读写端点，不更改原请求形状及成功响应。新差异是审计响应的可选扩展。

| 入口 | 接入方式 / 权限 |
| --- | --- |
| `/api/channel/` 的创建、编辑、删除、复制；状态/批量/标签/限额/排序/多密钥/应用上游模型 | 既有 `AdminAuth` 及 ChannelRead/Write/Operate/SensitiveWrite 分类；PAT 继续验证创建者、作用域和二次验证。 |
| 渠道成本管理、价格监控手工成本修改 | 复用各入口现有管理权限；统一关联 `channel` 目标，不另读全量成本配置采集旧值。 |
| 用户管理、`/api/user/self`、`/api/user/setting` | 管理角色/资源权限、目标角色限制、本人身份及敏感操作 proof 保持原样。 |
| `/api/token/`、`/api/user/access_tokens` | UserAuth、所有者及 PAT 浏览器会话/proof 限制不变。API 令牌继续由 `TokenOperationAudit` 写一条事件。 |
| `/api/option/`、`/group`、`/request_policy`、模型定价及 Passkey 域名设置入口 | 沿用原 settings scope、RootAuth 和特定 proof；价格预览、域名预览不输出已提交变更。 |
| `/api/custom-oauth-provider`、合规确认、倍率重置、价格监控设置 | 沿用各入口的 AdminAuth/RootAuth、settings 资源权限和会话限制；仅替换兜底为业务细节记录，不改变登录或支付能力。 |
| `GET /api/audit`、`GET /api/audit/self` 及各自 export | 列表维持既有角色过滤；新增值在 `ProjectAuditLogs` 统一投影，导出复用此函数。 |

`visibility` 由服务端采集器确定，不能从请求指定。渠道与管理用户差异为 admin；root 专属设置/权限差异为 root；个人 API 令牌/PAT 和本人资料允许 self，但必须确认目标归属该事件用户。self 请求即使由管理员发出也删除渠道、管理用户及系统设置差异。投影过滤掉隐藏目标后重新计算可见摘要，不泄露隐藏对象数量。

所有列表、按事件回查、异常 IP 回查、普通/风险导出均验证投影。既有安全告警详情仍显示字段名和脱敏事实，不扩充成配置副本。

## 各入口的实施安排

### 渠道

- `UpdateConfiguration` 为管理调用提供可选结果，复用锁内配置投影和原回读，不在事务中写日志。保存前快照独立复制必要字段，避免 `ChannelInfo` 的 map 浅复制被随后修改。独立成本行仅在成本实际提交时采集。
- `UpdateCreatedChannelsWithChanges` 扩展既有结果，保持 IDs/Fields/CacheChanged 合同；新增差异不改变告警计数。
- 单条/批量状态及多密钥从管理入口收集原有数据，记录实际写入的状态和数量。共享运行时方法不增加查库、日志、配置读取或锁；不具备保护的旧快照显式使用 observed。
- 标签编辑按旧标签固定目标，按实际写入的阶段追加结果；日限额使用规范化后的三项配置，不记录轮次、每日消耗等运行时噪音。批量采集新增查询只读固定 IDs/索引范围和必要字段，受上下文与明细上限约束。
- 优先级排序复用返回的 Changes；应用上游模型复用已有前后列表，包括已保存但能力刷新失败的部分。创建/复制/删除附安全快照；手工刷新凭据只记录凭据变更事实。
- 成本配置的独立入口与渠道表单采用同一差异描述，使用持久化成本读取，避免把缓存值当作真实旧值。仅记录配置，完全不更改结算算法。

### 用户与令牌

- 用户编辑覆盖真正写入的字段，权限从已提交的规则覆盖提取；角色/状态、额度使用已有业务结果。管理编辑的统计余额和 `auth_version/profile_version` 不列为用户修改。
- 本人资料与通知设置补充专门事件，记录服务端合并及清空后的值；密码只记录事实，既有通知、会话更新和缓存失效仍由原路径执行。
- API 令牌以既有字段白名单扩展前后值；创建、删除和批量删除采用同一结构。新增内容挂到当前中间件事件的 `Other.Changes`，不得放进不受投影的普通 params。
- PAT 改名也采集旧名；权限集合标准化后比较，单纯重排不产生差异。仅审计本人拥有的令牌，不将完整模型对象序列化。

### 系统设置

- 单项与分组保存沿用原返回结果，通过可选收集器返回已提交差异；缺少持久化覆盖用 absent 表示，不把有效默认值冒充持久化旧值。空串独立于缺失。
- 普通单项保存补检查 DB 错误。各专用保存器复用原事务及管理锁，提取实际写入的规范化 key/value；不按客户端 payload 推断成功。
- 请求策略、组重试组合设置、流校验兼容别名、模型级/整块定价都归到实际持久化键；定价保留当前版本检查、默认与覆盖语义，不新增价格计算。
- 保存成功后发布失败的差异仍保留，失败事件有已提交目标时统一标记 partial。仅用于审计的读取失败标记 unavailable；原保存流程必需的读取失败仍返回原错误，不能保存空值覆盖配置。
- 设置差异只对请求实际写集比较；不遍历整个 OptionMap，不持有 OptionMap 锁执行差异编码或日志写入。
- 自定义 OAuth 在对象被原地修改前取安全快照，比较规范化保存结果；client_secret 只记录变更，端点 URL 使用同一脱敏规则。保留 slug 冲突、用户绑定检查和注册表更新顺序。合规确认逐项累计真实提交结果；倍率重置、价格监控设置及手工应用价格复用对应保存器的差异，不复制定价或认证逻辑。

## 前端交互

在现有审计详情的目标信息后展示变更区，字段名位于该字段的对照值上方，桌面旧值/新值并列，窄屏上下排列。按目标分组，复用现有 `Dialog`、`DetailSection`、`AuditDetailValue`、复制能力与主题 token；现有 `AuditDetailFields` 是单值参数展示，新增业务组件仅负责两值对照的组合。

- 状态、角色、布尔值与字段名使用现有 action/resource 上下文翻译；模型名、字典键和自定义值按原义显示。
- 0、false、空值、未设置、已删除、已隐藏分别显示；大数字、额度与金额保留精度和单位。
- `unchanged`、`not_committed`、`unavailable` 分别显示没有变更、未保存、未记录。历史无 changes 沿用原详情，不生成空白对照区。旧的 `from/to`、`previous_scopes/scopes` 仅在语义已知且成功的 action 中适配，禁止任意猜测。
- 部分完成、提交后处理失败、observed 旧快照及截断都有明确提示；不能通过查当前配置补齐。
- 不增加每个目标的网络请求或自动加载全部批量详情；从当前事件派生展示，不用 Effect 复制成另一份 state。可见值受 4 KiB 上限约束并自动换行，密钥没有展开或复制入口。
- 页面加载、错误、筛选、分页沿用原实现。七语言文案遵循 `i18n-translate`，受保护的五个 `web/src/lib` 上游文件不修改。

## Main Chain Impact

AI relay goroutine 新增同步工作为零：不采集调用请求体、响应体、费用结算或自动运行时事件，不增加 relay 的 DB/Redis 调用、goroutine、锁或配置分支。

新差异在管理/个人设置 HTTP 请求中计算，日志仍在既有审计点写入。单渠道及多密钥在释放业务锁后构造差异；用户、权限及设置的部分保存器在原管理事务内构造有界本地回执，仅提交后合入请求，事务内不写审计日志。没有新增共享行锁；这些管理事务的少量序列化 CPU 成本不能称为完全零成本。已有管理 API 的缓存发布、能力同步、WebSocket 关闭与会话撤销保持既有顺序。

本次不改变 relay 执行流，因此没有 relay feature flag。若实施发现必须调整 runtime 状态写入语义或增加热路径调用，应先修订本设计并增加开关，不能作为审计功能顺带引入。

## Shared Resource Audit

| 资源 | relay 是否共用 | 处理约束 |
| --- | --- | --- |
| `channels`、`abilities`、`channel_cost_configs` | 是，渠道选择/状态/成本及后台更新使用 | 复用原写入与读结果；新增审计读取仅管理端按 ID、既有 tag 索引投影，限制数量/超时，不为审计新增长事务或共享行锁。 |
| `users`、`tokens` | 是，鉴权与额度更新使用 | 不新增账户/令牌锁，不把消耗字段全部复制；额度沿用原 AdjustUserQuota 结果，缓存 fence 不改变。 |
| `user_access_tokens`、Casbin 规则 | 管理认证使用，部分基础设施共用 | 沿原所有者/目标作用域读取；权限继承不由当前缓存猜测，保持原安全验证与发布。 |
| `custom_oauth_providers`、OAuth 注册表、已有绑定检查 | 登录流程使用，AI relay 不读取提供商配置 | 复用原对象读取和注册表发布，不增加认证请求采集，不读取用户凭据/身份令牌。 |
| `options` 与配置/定价快照 | relay 读其缓存/快照 | 仅管理保存器捕获写集，不在 OptionMap/定价发布锁下写审计，保持原发布原子性。 |
| `audit_logs` | 与消费日志表分离；可能共用 LOG_DB 连接池 | 每次操作沿用一条主事件，新增 JSON 有严格上限；不增加每目标 insert，不触发全表 JSON 检索。 |
| Redis、渠道/用户/令牌缓存、轮询锁 | 与 relay 共用 | 本任务不增加 Redis key、缓存副本或池，不把审计放入缓存写入逻辑/共享锁临界区。 |
| 审计查询/导出独立池、文件写入器 | 已有隔离能力 | 复用原分页、时间窗、节流、权限复核、过期删除；只增加受限 other 内容。 |

新增批量渠道快照查询使用独立的 1 秒超时上下文，最多读取 101 行并保留前 100 行；失败令快照 unavailable。业务原本必需的 DB 读写失败仍走正常错误处理。主库新增读取确实有成本，不能宣称与 relay 共享实例绝无影响；通过索引范围、投影、数量和超时限制约束开销。其他管理快照复用已有读取，PAT 改名补充一次所有者范围读取，成本删除补充一次主键读取，权限清空补充已有用户规则的范围读取。

## Concurrency Analysis

30k RPM 约 500 RPS、100k RPM 约 1,667 RPS 下，每个 AI 请求的新增 DB/Redis 调用、锁和 goroutine 均为 0，新增分配为 0。成本与管理写操作次数有关，不随 AI 请求量增长。

已有前后结果的单渠道/PAT/排序/额度流程新增审计查询为 0；缺少旧值的管理路径最多增加一批有界读取，已经分批的全量操作随原批次采集，不增加逐目标审计查询。仅补读最多 100 个目标，超过部分明确截断。日志每次操作新增差异最多 64 KiB；不扩展事件数量，不引入逐请求 goroutine 或 worker。

有界 metadata 仍会增大日志与列表返回量，需用最大明细事件测试列表和导出内存；不得以界面仅显示几行替代存储限制。批量管理与后台状态更新并发时，未取得原子快照的入口保留 observed 标记，不通过增加 relay 锁来消除这一限制。

## 数据模型、配置与索引

没有新增数据库表、列、索引或用户配置；复用审计保留天数与导出设置。新增查询只使用对象主键/已有所有者或标签索引，审计检索仍通过已有时间/事件/角色索引。不增加按差异 JSON 搜索或全表聚合。

`AuditOther` 序列化属于数据库行为变化，实施必须验证 SQLite 兼容，以及真实 MySQL、PostgreSQL；日志库适配另测 ClickHouse 可用环境，不把缺环境写成通过。生产文档当前记录主库 MySQL、日志库 PostgreSQL，确切版本尚未记录，验收前按 `docs/production-environment.md` 和 `docs/release-checklist.md` 核实，不从历史测试推断。

## 错误处理与可逆性

构造差异失败不改变成功业务，也不回滚；记录安全诊断并显示 unavailable。原业务失败保留原失败事件。已提交但后处理失败复用 partial 与原事件 success/params，不引入独立 followup_failed 字段，不由 HTTP 200 推断提交成功。

旧客户端可忽略可选 changes，旧日志可继续读取；回退旧版不会执行新迁移，但旧版读写结构可能丢弃未知字段，因此不支持用旧版代码改写已有审计行。日志清理和导出过期仍沿原生命周期执行。

## 测试计划与实施顺序

先补行为测试，再实现。仅扩展与本功能直接相关的测试，优先使用现有测试文件，保护上游原样测试。

1. 差异和脱敏：缺失/null/0/false/空集合、未知字段、嵌套凭据、授权头、带凭据 URL、无效 JSON、大整数、稳定排序、所有明细上限；密钥只变更也不能被忽略。断言数据库、API、导出、诊断及告警均无测试凭据。
2. 渠道：单条/批量/PAT/标签/启停/限额/排序/多密钥/创建复制删除/上游应用/成本各入口；无变化、标签合并、显式清零、丢弃只读字段、重复 ID、并发变更、完整回滚及部分提交。复用 `channel_configuration_atomic_test.go`、`admin_token_audit_test.go`、`channel_daily_limit_edit_test.go`。
3. 用户：资料、策略、角色/状态、权限覆盖、额度及自身设置；保持 proof、会话与缓存合同；失败、no-op、提交后刷新失败和个人/管理员可见性。API 令牌和 PAT 覆盖同名、scope 重排、零额度、过期、撤销、越权、批量部分命中与凭据排除。
4. 设置：单项、分组、组合重试、请求策略、价格及别名；数据库写失败不得报提交成功；默认继承/显式覆盖、规范化、持久化成功但应用失败。价格版本检查、表达式与模式同时发布的原测试必须保持通过。
5. 审计/API/导出：管理、root、self（含管理员 self）、缺权限、事件回查、风险回查、CSV/GZ；旧 `from/to` 显示不回归。安全规则仍只按原安全事实判断，不因新增对照值误报或泄密。
6. 前端：在现有 `audit/__tests__/details.test.tsx` 等覆盖字段对照、不同资源状态、历史缺值、空差异、脱敏、截断、observed、部分完成、七语言和窄屏。使用模拟数据做桌面浅色/手机深色视觉核验。
7. 数据库：真实 MySQL、PostgreSQL 主库及独立日志库组合验证持久化、回滚、投影与导出，记录版本/命令/结果；检查前后 schema 和第二次启动无新增 DDL。SQLite 保持兼容测试，不能替代两种真实数据库。新增快照查询核对 EXPLAIN，不扫描共享业务表。
8. 执行相关 Go 包回归（model/controller/middleware/authz/auditsecurity/auditexport），随后按范围做构建及必要完整测试；前端 `bun run test`、`bun run typecheck`、`bun run lint`、`bun run build`。如标准命令不可用，明确记录失败与替代验证。实施完成再更新本文的真实行为、查询数与验证结果。

## 实施与验证记录（2026-11-16）

实现入口：`model/audit_changes.go`、`audit_snapshots.go`、`audit_channel.go`、`audit_option.go`；各管理保存器传入可选收集器，`RecordAuditLog` 挂载 `Other.Changes`，`ProjectAuditLogs` 统一限制 self/admin/root 可见性。前端由 `audit-changes.ts` 解析、`AuditChangesSection` 展示。新增内容不进入风险规则 Facts 和通知正文。

实际实现补充：

- 渠道配置与成本共用原事务提交，但成本行没有额外加锁，所以成本是独立 observed 目标；渠道本身的锁内快照为 atomic。成本复用保存器原读取，不添加审计专用成本查询。
- 渠道批量新增中已知由密钥前缀派生的名称不进入新增快照。密钥、密码、认证头、请求覆盖、未知扩展均在写入端隐藏；安全字段也拒绝异常结构及已知凭据成员。URL 仅保留协议与主机。
- 自定义 OAuth、API 令牌、PAT、管理员/本人用户设置均使用相同展示。旧版 PAT 撤销及本人销户保存状态变化事实。API 令牌模型限制和 IP 白名单按本次需求进入受投影保护的 changes，原 op.params 继续只保存字段名。
- 普通定价表达式按模型记录；插件表达式等未登记动态配置只保留变化事实。内部定价版本号不列为用户修改。旧别名的自动清理仍以当前规范配置键的变化体现，不额外伪造旧别名值。
- 未新增表、列、索引、配置、Redis key、连接池或 relay 调用点。没有部署、迁移或重写历史日志。

验证环境为本机隔离数据库 `newapi_audit_changes_20261116`，不使用生产数据。MySQL `26.10.0`、PostgreSQL `18.7 (Debian 18.7-1.pgdg13+2)`；现有隔离 SQLite 测试报告 `3.50.4`。读取过生产环境文档：实际生产是 MySQL 主库 + PostgreSQL 日志库，其具体版本未知，不能用本机版本替代。

执行命令（SQL_DSN/LOG_SQL_DSN 由本机运行器从 Docker 环境中注入，未写入代码或文档）：

```text
go test -p 1 ./model ./controller ./middleware ./service/authz ./service/auditsecurity ./service/auditexport -run 'Audit|Channel.*(Configuration|DailyLimit|Status|MultiKey|Tag)|UpdateUser|ManageUser|AccessToken.*(Update|Lifecycle)|ConfigGroup|RequestPolicy|ModelPricing|UserSetting|Token.*(Update|Delete)' -skip 'TestAPITokenAuditDatabaseMatrix|TestAIAuditSQLiteFixtureRestoresTokenLookup' -count=1
go build ./...
bun run test src/features/usage-logs/audit/__tests__/changes-section.test.tsx src/features/usage-logs/audit/lib/audit-changes.test.ts src/features/usage-logs/audit/__tests__/details.test.tsx
bunx --no-install tsgo -b
bun run lint
bun run build
```

- MySQL 主库 / PostgreSQL 日志库：上述相关包回归通过。覆盖新增事务回滚、规范化写集、真实日志库 JSON 往返、大整数、脱敏、投影，以及既有权限/导出回归。
- PostgreSQL 主库 / MySQL 日志库：上述相关包最终复跑全部通过（model 9.236 秒、controller 49.767 秒，其余包通过）。首次广域执行中现有 SQLite 邮件测试出现一次投递失败，单独复跑通过（3.529 秒），随后完整专项复跑也通过。
- 完整上述选择、不使用 skip 的初次执行：两组受保护上游测试因旧合同失败，具体为 `TestAPITokenAuditDatabaseMatrix` 和 `TestAIAuditSQLiteFixtureRestoresTokenLookup` 中 `configuration_values_stay_private`；它们要求模型限制/IP 白名单完全不出现在审计中，与已确认的新功能冲突。没有修改或削弱这两个上游文件；新增 `TestAuditTokenConfigurationValuesUseProjectedChanges` 验证值仅进入 changes、原 params 不变、凭据仍被排除。最终专项命令显式排除这两组，不能把它描述为完整测试全绿。
- 前端 64 项测试通过；lint 无错误，剩余为既有模块警告；生产构建与 `go build ./...` 通过。标准 `bun run typecheck` 因本机 Bun 不支持 `bun check -b` 未能执行，使用已安装的 `tsgo -b` 替代通过，未修改项目脚本。
- 用真实组件及合成数据检查 1280px 浅色、390px 深色页面：没有横向溢出；长模型名换行，0、null、密钥隐藏与部分保存提示正确。此为组件视觉验证，不宣称完整登录到保存再回查的端到端验收。
- 渠道 tag 查询 EXPLAIN：MySQL 使用 `idx_channels_tag`。PostgreSQL 空测试表默认顺序扫描；已核实存在 `idx_channels_tag` 和主键索引，关闭顺序扫描后可走索引。没有用空表计划冒充大数据性能结论。
- 未执行 ClickHouse 实例、生产 schema 升级/二次启动 DDL 观测、生产负载或完整浏览器保存链路。无 schema 变更；这些结果不包含在本次本地通过声明中。

## 2026-11-16 审计后修复：检测元数据与展示差异隔离

用户已确认修复本次审计发现的问题。`worker.scanPage` 原先对整个 `other` 应用 16 KiB 上限；30 个渠道的普通模型列表差异即可生成 37,537 字节，导致 `op.params` 和 `admin_info` 一起被替换，破坏规则字段、数量、目标和操作者归因。`Normalize` 实际只依赖这两个成员，不读取 `changes`，因此应在来源数据库投影阶段只构造 `op` / `admin_info`，再按字节应用既有上限；真正超大检测参数仍保留 `evidence_incomplete` 降级。

修改仅限后台来源扫描 SQL 和相应回归测试：MySQL/SQLite 使用 JSON 提取与对象构造，PostgreSQL 使用 JSON 对象投影，ClickHouse 从原生 JSON 序列化结果提取两个成员，缺省成员保持 null。保留现有时间/事件游标、每页 500 条、超时、独立连接池和规则白名单；不修改 API、配置、表结构、原始审计数据或通知合同。扫描只读 `audit_logs`，新增开销是已选中行的数据库 JSON 投影，没有新查询、写入、共享锁、Redis 访问或 relay goroutine 工作。

回归覆盖大差异普通维护无告警、敏感批量仍保留真实 ID/数量、单目标归因、管理员身份和可见性、历史无差异数据、超大检测参数的 ASCII/UTF-8 字节限制，以及来源结果不包含展示差异。使用现有来源生命周期测试在真实 MySQL/PostgreSQL 和 ClickHouse 上验证 SQL、游标和索引，SQLite 做兼容补充。

本次修复验证（独立本地容器，未部署）：

- 修复前通过 Go overlay 注入的原始复现 `TestReviewLargeRoutineChangesKeepSecurityFacts` 失败；修复后同一复现通过（1.279 秒）。持久回归加入 `service/auditsecurity/source_test.go`，不依赖 overlay。
- `go test -count=1 -timeout 300s ./service/auditsecurity -run '^TestCaptureSourceScanAndLateRepair$' -v`：MySQL 9.7.4、PostgreSQL 18.7、SQLite 通过；ClickHouse 初次暴露驱动将 SQL 中的 JSON 字面量花括号误识别为命名参数，改用 `char(123/125)` 构造后专项复跑通过（4.654 秒）。原始失败未删除，保留于 `_local/audit-fix-20261116/test.log`，成功复跑见 `source-clickhouse.log`。
- `go test -count=1 -timeout 300s ./service/auditsecurity`：分别将 `TEST_ADMIN_TOKEN_DSN` 指向隔离 MySQL 9.7.4、PostgreSQL 18.7，整包通过（59.311 / 52.971 秒）。两轮均设置 `AUDIT_TEST_MYSQL_DSN`、`AUDIT_TEST_PG_DSN`、`AUDIT_TEST_CH_DSN`，实际执行全部来源方言回归；ClickHouse 为 25.8.33.6。日志为同目录 `security-mysql.log` / `security-postgres.log`。
- 来源查询计划：MySQL 使用 `idx_audit_time_event`，PostgreSQL 使用 `idx_audit_logs_created_at`；已有旧日志/500 条分页/迟到修复检查通过。未新增 schema、索引或迁移。
- `GOWORK=off go build ./...`、`go vet ./service/auditsecurity`、修改文件 gofmt、UTF-8 无 BOM 和 `git diff --check` 通过。
- 本轮没有运行生产 schema 演练、实际负载测试或完整仓库测试；前次已记录的两组受保护旧审计合同测试未修改，不将本次受影响包通过表述为整个仓库全绿。

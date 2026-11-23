# 按请求分组配置错误替换

状态：用户已确认并完成实现、本地验证；未部署。日期：2026-11-23。

用户已明确：这里的分组是请求分组，不是错误类型分类。本方案扩展现有[错误提示替换](relay-error-message-masking.md)，在每条特殊规则上指定适用分组。

## 1. 目标与范围

- 同一种错误可以在 `default`、`vip` 等分组显示不同提示，也可以只让某些分组保留原文。
- 每条规则支持全部分组或多个指定分组。既有规则缺省为全部分组，升级后顺序与效果保持原样。
- 保持一张有序规则表，第一条满足全部条件的规则生效；指定分组规则需要优先时，将它移到全局规则之前。
- 覆盖现有错误替换已接入的非流式响应、本地中止、受管流式终止帧、Realtime 错误事件、用户错误日志及自助导出。
- 总开关、未命中上游错误的处理方式与默认文案仍为全局设置。某组需要单独兜底时，可添加仅限制分组和来源的规则，放在该组更具体的规则之后。
- 不扩展现有功能尚未接入的异步任务错误，不改变管理员原始错误、重试、自动禁用、计费、日志筛选与公开的 `group` 列含义。

## 2. Existing Logic and Related-Path Findings

以下为本轮阅读实现及测试后核实的事实：

| 代码位置 | 当前行为与本次衔接 |
| --- | --- |
| `setting/operation_setting/relay_error_display_setting.go`：`RelayErrorRule`、`RelayErrorInput`、`compileRelayErrorRule`、`matches`、`decide` | 规则只有来源、状态码、错误码、关键词；输入没有分组。配置保存/同步时编译为不可变快照，错误路径读取 `config.Snapshot`。增加分组条件与输入，沿用首条命中和现有复杂度上限。 |
| `model/config_group.go`：`SaveConfigGroup`；`model/option.go`：`validateRelayErrorDisplaySave`、`relayErrorDisplayRulesToCheck` | 配置组保存先校验、事务持久化，再发布；单项保存走相同校验。未修改的旧规则可保留，运行时跳过无效规则。规则内容比较通过结构体重新编码，新增字段必须参与比较，防止只改分组时被误认为未改。 |
| `router/api-router.go`；`controller/relay_error_display.go` | 配置接口在 `AdminAuth` 下，读/预览需错误提示 scope 的 view 权限，保存需 edit；预览额外核验中间件授权的 scope。预览当前没有样例分组，必须与实际请求一起扩展。 |
| `middleware/auth.go`：`TokenAuth`；`middleware/distributor.go`：`Distribute` | `ContextKeyUsingGroup` 是经过权限检查的请求分组，通常来自令牌分组，令牌未指定时使用用户分组；Playground 可在验证后覆盖。更早的鉴权失败可能尚无该值。不能从未验证的请求 body/header 推断分组。 |
| `service/channel_select.go`：`CacheGetRandomSatisfiedChannel`；`service/relay_timeout.go`：`relayAttemptGroup` | Auto 选中分组保存在 `ContextKeyAutoGroup`；提前推进的 Auto 下标代表下一次尝试，不能用来解释当前错误。现有 `relayAttemptGroup` 已采用实际 Auto 分组优先、否则请求分组的口径。 |
| `service/relay_error_display.go`：三个 `Present*` 入口与 `streamTerminalRelayErrorInput` | HTTP、本地中止与流式/Realtime 共用匹配器；入口已有异常恢复。流式匹配输入还会保存在请求上下文，用于日志与终止帧一致。 |
| `service/relay_error.go`：`ProcessChannelError`；`service/user_channel_exclusion.go`：`RecordUserChannelRoutingRejection` | 普通渠道失败与本地排除失败均经 `model.RecordErrorLog`；传入的公开日志分组来自上下文 `group`，Auto 时可能只有字面量 `auto`。 |
| `service/consume_settlement.go`：`FinalizeConsumptionSettlement`；`model/log.go`：`RecordErrorLog` | 零收费流式失败也经 `RecordErrorLog`，其分组来自结算参数/`RelayInfo.UsingGroup`；入队前已经构造完整日志，异步 worker 不再依赖可变化的 Gin 上下文。此处可统一保存错误所属分组，无须新增日志或改变队列。 |
| `model/log.go`：`formatUserLogs`、`maskErrorLogForUser`、`maskStreamStatusForUser`、`MaskErrorLogContentForUser` | 用户列表先读私有流式匹配输入，再裁剪 `admin_info`，最后改写错误；消费日志中的旧 `stream_status` 文本有独立入口。自助导出走 `MaskErrorLogContentForUser`，需一起传分组。 |
| `model/log_export_job.go`：自助导出的字段准备与 `runUsageBatches` 分支；`model/log_export_columns.go`：`SelectFields` | 先改写错误正文，再裁剪私有 metadata。当前强制选取 type/other/request_id，但 group 只有被选作导出列时才读取；本次必须将分组加入自助导出的内部依赖字段，否则仅导出错误正文时旧日志缺少匹配依据。管理员导出不走用户改写。 |
| `web/.../system-tuning/relay-error-display-section.tsx`、`relay-error-rule-dialog.tsx`、`relay-error-display-rules.ts` | 规则是本地草稿，弹窗确认后仍需页面保存。`toRow/toRule` 显式列举字段，必须同步加入分组，否则前端会丢失它。当前预览仅比较草稿版本，未追踪样例变化，增加样例分组时需同时防止旧响应覆盖新输入。 |
| `web/.../system-tuning/group-retry-section.tsx`；`web/src/features/users/api.ts`；`controller/group.go` | 邻近分组设置使用 `getGroups`、React Query 的 `['groups']` 查询，缓存 5 分钟。`GET /api/group/` 仅需 AdminAuth，从 `GroupRatio` 内存副本返回组名，无需新增接口或额外设置权限。 |
| `web/src/components/multi-select.tsx`；渠道编辑抽屉中的调用 | 现有 MultiSelect 支持搜索、多选、自定义值、已选值保留与 chip 数量限制，可直接复用；表格沿用 StaticDataTable/StatusBadgeList，弹窗沿用项目 Dialog。 |

相关测试已阅读：`relay_error_display_setting_test.go`、`service/relay_error_display_test.go`、`service/relay_error_stream_match_test.go`、`controller/relay_error_display_test.go`、`model/config_group_relay_error_display_test.go`、`model/option_relay_error_display_test.go`。现有合同包括总开关关闭不替换、首条 keep 终止匹配、状态码改写、预览不修改配置，以及错误日志和终止帧匹配输入一致。

调查时确认的缺口是分组匹配、分组编辑/预览与 Auto 错误日志的分组证据；实现已补齐下述能力，复用现有配置系统和分组服务。

## 3. 匹配语义与边界

### 3.1 规则条件

`RelayErrorRule` 增加 `groups?: string[]`，`RelayErrorInput` 增加内部字段 `Group string`。

- 字段缺失、`null` 或空数组表示全部分组；非空数组中任一分组精确命中即可。
- 分组名区分大小写，不使用关键词的大小写折叠，也不支持通配符/正则；名称保留原值。全局作用域用空数组表达，不使用 `*` 哨兵。
- 分组与来源、状态码、错误码、关键词之间为“且”；仍按同一张表从上到下匹配，不做隐含的分组优先重排。
- 每条最多 100 个分组，每个名称最多 128 个 Unicode 字符。拒绝空白名称与保留选择器 `auto`，重复名称按精确值去重；不能把非法非空列表清空后当成全局规则。前后端校验一致。
- 保存时不强制组名必须仍存在于 GroupRatio：历史分组仍可能用于旧日志，暂时删除或改名不能让规则静默扩大为全局。界面保留这些值并注明当前列表中不存在。
- 编译阶段构造每条规则的只读分组集合；匹配时先排除不适用分组，再执行原有条件，规则总数仍最多 100。

示例顺序：

| 顺序 | 适用分组 | 错误条件 | 处理 |
| --- | --- | --- | --- |
| 1 | vip | 上游，关键词 insufficient balance | “当前服务繁忙，请稍后再试”，503 |
| 2 | default | 上游，关键词 insufficient balance | “服务暂时不可用，请稍后重试”，503 |
| 3 | 全部分组 | 上游，状态码 400/422，关键词 context length | 保留原文 |
| 4 | vip | 上游，无其他条件 | vip 的兜底文案 |

第 4 条之后，vip 上游错误不再进入全局默认提示；其他组继续按后续全局规则与原有默认策略处理。

### 3.2 哪个分组决定错误

- 固定分组：使用已验证的请求分组，不能以用户注册分组覆盖令牌或 Playground 已选的分组。
- Auto：已选中实际组时使用当次实际组；与 `relayAttemptGroup` 的已有路由口径一致。跨组重试结束后，返回给客户端的最终错误按最终失败尝试匹配，各条错误日志按各自失败尝试匹配。
- Auto 尚未选到任何实际组、鉴权失败尚未建立分组或上下文为空：视为分组未知，仅全部分组规则有资格命中；全局兜底仍正常工作。用户不能靠伪造请求字段选择替换规则。
- 不按 `ContextKeyAutoGroupIndex`、当前用户资料或渠道现有分组反查历史错误。
- 上下文组名超过匹配长度上限时视为未知，不截断成另一个可能存在的合法组名；日志附加快照因此保持有界。
- 解析分组是复用的业务逻辑：在 `operation_setting` 提供仅接收请求分组/实际 Auto 分组字符串的纯函数，供 service 与 model 共用，避免包循环。读取 Gin 上下文由调用层负责；不改现有重试或计费的组解析。

### 3.3 历史日志与快照

`model.RecordErrorLog` 在现有 `other.JSONString()` 和异步入队之前，记录 `other.admin_info.relay_error_group`。值是当次用于错误匹配的组名；无法确定时记录空字符串。这是原错误日志同一行的附加字段，不改变 `Log.Group`、日志数量、内容或队列行为。`other` 为空时按现有 LogOther 方式初始化。分组快照在功能关闭时也记录，保证以后开启规则能够处理关闭期间写入的日志，与现有流式匹配输入的保存口径一致。

读取规则：

1. 有合法 `relay_error_group` 时使用快照；存在且为空表示已确认未知，不能再猜测。
2. 旧行无该字段时，使用已有 `Log.Group` 中的具体组名。
3. 旧行只有 `auto`、空值，或字段损坏时按未知组处理，不能从当前配置倒推当时路由。

`formatUserLogs` 在裁剪 `admin_info` 之前取得分组。`maskErrorLogForUser` 的流式分支会重建输入，因此必须在分类/流式覆盖后统一设置 Group，防止被覆盖掉。自助导出复用同一读取规则；旧消费日志 `stream_status` 使用该行的具体 `Log.Group`，将 Group 传给 `MaskRelayStreamErrorForUser`。只修改其已有的错误文本，不触碰消费正文和结算字段。

流式终止帧的 `RelayErrorInput` 同样包含分组；结算将已保存的终止输入组先写为该错误行的明确快照，RecordErrorLog 不再用稍后的上下文覆盖它，避免终止与结算之间其他上下文变化影响结果。私有分组字段不返回用户列表或自助导出。管理员继续看到原始错误。

自助导出准备 SELECT 字段时，将 `logGroupCol` 与已有 type/other/request_id 一同补齐，使用现有跨数据库保留字引用；这是匹配依赖，不要求用户在文件里选择“分组”列。不改变扫描时间范围、keyset 游标、条件、批次大小或返回的导出列。

历史规则仍在查询时按当前配置重新计算，清空某条规则的分组即恢复全局作用域，删除规则后按剩余规则/全局兜底处理；分组改名不自动迁移规则或篡改历史证据。

## 4. 数据流、API、存储与配置

配置流程：管理员编辑草稿 → 后端按现有 scope 校验 → 校验 groups 与既有规则 → 现有 options 事务保存 → 发布带分组集合的不可变快照 → 后续错误与用户日志读取生效。

错误流程：认证/路由已有分组上下文 → 原有重试、日志入队和渠道处理 → 最终输出入口构造带 Group 的匹配输入 → 按序决定 keep/replace/edit → 原有协议序列化。成功响应不进入新增匹配逻辑。SSE/Realtime 已发送响应头后仍不能改 HTTP 状态。

| 接口 | 权限及变更 |
| --- | --- |
| `GET /api/option/?scope=system-tuning.relay-error-display` | 现有 AdminAuth + scope view；响应 key 不变，rules JSON 可带 groups。 |
| `PUT /api/option/group` | 现有 AdminAuth + scope edit；`module=relay_error_display_setting`，values 的四个字段不变，只扩展 rules 内部结构。 |
| `PUT /api/option/` | 现有单项保存继续支持，groups 同样经过校验，不能绕过组保存限制。 |
| `POST /api/option/relay-error-display/preview` | 现有 view 权限与精确 scope 检查不变；sample 增加可选 `group`。缺失/空值表示未知组，具体值需符合名称长度限制，`auto` 视为未解析组。其余样例与草稿校验保留。 |
| `GET /api/group/` | 复用现有管理员组名查询，供选择器建议使用；不是保存或请求匹配的授权依据。 |
| `GET /api/option/relay-error-display/presets`、`status` | 保持当前接口。预设无 groups，默认为全部分组；无效分组配置通过现有 skipped 机制显示。 |

保存示例（values.rules 仍是 JSON 字符串，示例展开其内容）：

```json
[{"name":"vip 余额错误","groups":["vip"],"source":"upstream","keywords":["insufficient balance"],"action":"replace","message":"当前服务繁忙，请稍后再试","status_code":503}]
```

预览新增样例字段：`sample: {group: "vip", source: "upstream", status_code: 403, error_code: "insufficient_quota", message: "insufficient balance"}`。返回仍为 `{success, message, data: {replace, message, status_code, rule_index, rule_name}}`，rule_index 始终指向完整有序草稿。

不新增表、列、索引、额外 SQL 查询、迁移、用户配置项或环境变量；增加现有 rules JSON 字段及错误日志 other 的私有字段，自助导出现有 SELECT 增补 group 投影。全局开关仍为 `relay_error_display_setting.enabled`。不修改 relaykit 公共 API 或上游受保护的前端认证/API 文件。

兼容边界：旧规则被新版本读取时为全局规则；旧版本服务不会识别 groups，可能把新规则当全局规则执行。因此发布时需先升级所有写入/服务节点和管理前端，再保存分组规则；回退旧版本前先关闭替换并恢复不含分组规则的配置。当前阶段仅设计，不执行部署或修改线上设置。

## 5. 前端交互与错误处理

- 编辑弹窗在匹配条件中增加“适用分组”MultiSelect，空选显示“全部分组”，支持多选、搜索与输入历史组名。选项从现有 groups 查询和草稿已选组名合并派生，查询与当前状态查询并行，不用 effect 维护第二份选项状态。
- 规则表增加明确的“适用分组”列，显示“全部分组”或复用现有分组 badge 列表；保持全表序号、上下移、编辑、删除和按钮区结构。无需分页或分组切换后隐藏其他规则，避免掩盖全局顺序。
- 测试区域增加“请求分组”单选/可输入控件，提供“未确定分组”。结果展示使用的样例分组；切换样例任何字段或规则后立即失效，异步结果同时核对完整草稿与完整样例快照，过期响应不能覆盖新测试。
- 草稿的解析、序列化、dirty 比较、预设追加、弹窗取消/确认均保留 groups。刷新组名列表不能重置已选分组；后台配置刷新也不能覆盖未保存的规则草稿。保存失败保留草稿并可重试。
- 查询组名失败时显示可重试提示，保留已选项并允许手动输入；不能悄悄清空已选组而变成全局规则。仅 view 权限可预览，编辑/保存按页面已有的 canEdit/服务器 scope 约束控制。
- 对“无条件局部修改”的提醒计入 groups：指定组的规则只接住这些组，不能继续宣称覆盖所有错误。
- 分组多选使用独立可访问名称；在该控件启用 `preserveSelectionOnEscape`，取消 Base UI 在弹层关闭时按 Esc 清空全部选项的默认行为，避免意外扩大为全局规则。MultiSelect 的其他调用方保留原行为。
- 新前端文案按 i18n-translate 技能补齐七语言；后端分组验证错误补齐 en/zh。错误需指出规则及可修正内容，用 `ApiErrorI18n`，记录内部原因但不返回 Go 错误或敏感原文。
- 已保存规则的 groups 存在语义错误（空白名称、auto、数量/长度超限）时，跳过整条规则并在状态接口说明；其余规则和兜底继续生效。结构上无法解码的 JSON 列表沿用既有 rules unreadable 处理。遵守现有“未修改的无效配置不阻止关闭总开关”机制。
- 运行时延续 Present* 的 recover 与原始错误回退；新增日志元数据解析失败仅降级到未知组，不影响日志查询、原始错误记录或中转输出。

## 6. Main Chain Impact

同步新增部分只发生在既有错误处理路径：读取请求上下文内已有的两个组字段、选择当次组、读取配置快照、每条候选规则一次集合查询；`RecordErrorLog` 在原有序列化前加一个有界字符串。成功响应不做分组规则匹配，不增加中间件。

异步部分沿用现有有界日志管线。分组必须在入队之前成为日志事件快照，后台 worker 不读 Gin 上下文，不新增 goroutine、任务、队列或外部调用。重试、自动禁用继续使用原错误，替换仍只影响最终输出及用户视图。

## 7. Shared Resource Audit

| 资源 | 主链是否使用 | 本次访问与隔离 |
| --- | --- | --- |
| relay_error_display_setting 配置快照 | 是，错误路径 | 编译分组集合后整体发布；运行时只读，无新增跨请求锁。 |
| ConfigManager 草稿锁/configGroupMu | 后台配置写入 | 继续只在保存/同步时使用；不在 relay 中取得草稿锁或调用取原始设置的方法。 |
| options 表、主库连接池 | 配置保存/同步已有使用 | 同一配置键 JSON 内容增加，原有事务/语句数量不变；请求匹配不读库。 |
| logs 表、日志库连接池 | 既有异步日志写入 | 同一错误行 other 增加私有字段，不新增行/查询、索引或同步写；不修改 group 列。自助导出只在既有批查询中补读 group，不增加查询次数或连接。 |
| Gin 上下文 | 每请求私有 | 读取既有分组字段；短暂的 Gin 内部读锁不跨请求、无等待上游/数据库的锁。 |
| 分组倍率内存映射 | 路由/计费也使用 | 仅后台 getGroups 读取现有副本；规则匹配不查询、遍历或修改该映射。 |
| Redis、worker/goroutine pool、文件句柄 | 主链已有使用 | 本次不新增访问、命名空间、连接、worker 或句柄；不占用额外队列容量。 |

## 8. Concurrency Analysis

- 30k RPM 约 500 请求/秒，100k RPM 约 1,667 请求/秒。按 10% 失败计算，新增组条件约处理 50/167 次失败每秒；100% 失败的压力场景约 500/1,667 次每秒，不假定一定只有 10% 失败。
- 每次匹配额外 DB/Redis 调用为 0，新增 goroutine 为 0，集合在配置发布时构造，错误路径不分配集合。最多 100 条规则，即全失败场景约 16.7 万次有界名称 hash 查询/秒；Realtime 按错误事件数单独统计，不能以连接数代替事件量。
- 配置内最多 100 × 100 个分组引用，每项最多 128 字符；日志只保存当次一个组，不保存全部规则/候选组。考虑 JSON 对控制字符、HTML 字符的转义，新增日志字段保守控制在约 1 KB/错误行以内，常见短组名远低于此；完整日志队列吞吐仍需部署前负载验收。
- 现有关键词扫描、正则修改和替换日志输出仍有自身成本，不能把全匹配器声称为固定微秒级。新增组过滤不提高原有文本/正则预算；组不匹配时提前跳过原有条件。基准需比较无分组、组命中、组不命中与最大规则列表的耗时/分配。
- 同步工作没有网络或磁盘等待；具体 p95/p99、CPU 与分配结果待实施后实测，不以本节估算冒充通过负载验收。

## 9. 实施与验证计划

先在既有测试文件补充业务用例，再写实现；前端目前未发现本功能专属 Vitest 文件，新增相邻的规则转换与交互测试。

| 验证层 | 必测行为 |
| --- | --- |
| 匹配/配置 | 缺省、null、空数组兼容全局；单组/多组/不匹配/未知组；大小写精确匹配；分组与其他条件的且/或关系；全局与指定组不同排序；keep/replace/edit；空白项、数量及字符数边界；无效旧规则只跳过自身；只改 groups 不能跳过严格校验。 |
| 请求出口 | HTTP、middleware 本地拒绝、SSE、Realtime 共用组语义；固定令牌组与用户组不同；Auto 的实际组与已推进下标不同；失败后跨组重试；开启/关闭、panic 回退及请求 ID/状态码原有合同。 |
| 日志/导出 | 错误入队后改变上下文不改先前组快照；HTTP/流式错误用户视图按同组匹配；流式分类重建输入不丢组；旧具体组可匹配，旧 auto/空/坏 metadata 仅全局；自助导出与列表一致，包括只选正文、不选分组列；管理员保持原文、私有字段不外泄；旧消费日志 stream_status 覆盖组条件。 |
| API/存储 | 真实路由验证 view/edit/scope 拒绝；两条保存路径一致；保存失败不发布；重启/同步后分组仍有效；预览不同 sample.group 返回对应规则且不写库。 |
| 界面 | 旧规则显示全部组；选择多个组、取消/编辑/删除/排序/保存/重开不丢组；组名加载失败与删除组回显；无权限禁止保存；切换样例组后忽略旧预览；后台刷新不覆盖草稿；窄屏/主题及七语言显示。 |
| 性能 | 开关关闭/开启、0/100 条规则、组命中/不命中与并发配置发布；成功请求与失败混合负载，检查 CPU、尾延迟、日志积压；不触发付费真实上游。 |

主要实现文件：

- `setting/operation_setting/relay_error_display_setting.go`：分组数据结构、验证、编译、纯分组解析与日志匹配参数。
- `service/relay_error_display.go`：实际请求/流式输入分组。
- `model/log.go`：错误组快照写入与读取、各用户视图透传；`service/consume_settlement.go`：沿用终止帧已捕获分组。
- `model/log_export_job.go`：为自助导出补齐 group 内部读取依赖；查询保持既有索引、时间范围及 keyset 扫描方式。
- `controller/relay_error_display.go`：样例分组、可修正的验证错误；需要时扩展 i18n 常量和 en/zh 条目。
- `web/src/features/system-settings/types.ts` 与 `system-tuning/relay-error-display-rules.ts`、`relay-error-rule-dialog.tsx`、`relay-error-display-section.tsx`：字段贯穿、选择器、表格、预览、草稿保留和权限；前端七语言文件。

实现后执行相关 Go 包测试、前端 Vitest、`bun run typecheck`、`bun run lint`。已参考 `docs/production-environment.md` 与 `docs/release-checklist.md`：存储回归必须在实际 MySQL/PostgreSQL 项目测试环境及 SQLite 兼容环境覆盖 options JSON、日志 other 往返与自助导出的 group 投影，使用独立测试记录并按行清理；版本与命令在实施交接记录，不能把历史环境版本当本轮实测。自助导出仍走既有按用户/时间/游标的索引，增加投影不新增 WHERE/ORDER BY 列；在实际库核对执行计划及分页结果。无 schema/index 变化，不新增 DDL 或大表扫描；若实现偏离此边界，补做迁移矩阵。ClickHouse 日志路径的 JSON 展示与保留字引用兼容也需覆盖，真实实例无法运行时列明。

部署验收沿用项目官方 main → 认证 mock 的隔离链路，记录实际覆盖协议、规则保存→请求返回→异步日志→用户回查结果；此处是后续验收要求，不表示已经部署或测试。

## 10. 实现与本地验证结果

实现遵循以上语义，没有新增配置键、API 路由、数据库表、列或迁移。规则编译为只读分组集合；HTTP、本地中止、流式和 Realtime 使用同一分组解析函数。错误日志保存实际组快照，流式结算保留终止帧的快照；用户列表和自助导出在删除私有字段之前读取它。损坏的分组字段或 metadata 降级为未知组。旧具体组日志继续可匹配，旧 auto 日志不推测真实组。

前端增加规则分组多选、作用域列、预览请求分组。删除的组继续回显且可以编辑；加载失败保留选择、支持输入和重试，共享 groups 查询会把业务失败作为加载失败处理。保存过程中继续编辑的内容不会被成功响应覆盖；后台刷新不会覆盖未保存草稿，被弹窗或修改暂缓的刷新会在编辑器恢复干净后应用，保存前取得的旧快照不会覆盖成功保存的草稿。预览按完整草稿和样例失效，真实设置页允许仅 view 权限的管理员使用测试区，编辑和保存仍由 canEdit 禁止。分组下拉打开时第一次 Esc 仅关闭下拉，关闭后 Esc 正常关闭规则弹窗，两种情况都不会清空已选分组。新增 11 个文案键已覆盖 en、zh、zh-TW、fr、ja、ru、vi；后端验证提示覆盖 en、zh-CN、zh-TW。

2026-11-23 本地执行记录：

| 检查 | 命令 / 环境 | 结果 |
| --- | --- | --- |
| 后端构建 | `go build ./...` | 复核再次通过；未改 relaykit。 |
| 全量后端 | `go test ./...` | 未通过：无 `SQL_DSN` 时，既有 controller/model 测试在数据库未初始化后空指针，service 的 TestMain 明确拒绝启动；其余包通过。失败点为 `TestAdminAssignCustomerToEmployee_EmployeeNotFound`、`TestUserSessionPreviousRefreshHashSchemaUsesNullableVarchar` 和 service 的环境检查，均不在本功能改动路径。功能相关定向测试及下列实库矩阵通过。 |
| 配置匹配器 | `go test ./setting/operation_setting -count=1`，随后执行分组、预览、错误日志、自助导出定向用例 | 通过；覆盖空/缺省/null、精确名称、100/101 组、128/129 字符、顺序及无效规则。保存测试先验证立即发布，再单独验证从持久化 JSON 重载。 |
| 实库矩阵 | `_local/group-relay-error-20261123/run-tests.py`：MySQL 26.10.0 主库 / PostgreSQL 18.7 日志库，以及反向组合 | 复核再次通过；每组 model 92、controller 20、service 188 条 PASS 记录（含子用例），每组仅有 1 条 SQLite 专属查询计划用例跳过。隔离库、测试 Redis 均已清理。覆盖只修改 groups 的保存校验、持久化后重新发布、各错误出口、流式组快照及只导出正文时的分组匹配。 |
| SQLite 导出 | `go test ./model ./controller -run 'Test(RecordErrorLogCaptures|UserErrorLogs_RequestGroup|SelfLogExport|PreviewRelayErrorDisplay|RelayErrorDisplayInvalid)' -count=1` | 通过；自助导出 fixture 使用独立 SQLite 文件与已有用户时间索引；导出未选择 group 列时也可正确匹配。 |
| 前端回归 | `bun run test`；复核定向运行 `relay-error-groups.test.tsx` 与 `group-retry-section.test.tsx` | 全量 294 个测试文件、3334 个测试通过；复核定向 2 个文件、17 个测试通过。覆盖网络失败/重试、历史组、保存期间继续编辑、暂缓刷新后重放、保存前旧快照隔离、真实只读页面预览、预览过期、两段 Esc 行为及共享 groups 查询业务失败。 |
| 类型与静态检查 | `bunx --no-install tsc -b`、`bun run lint`、本次涉及文件的定向 oxlint | TypeScript 与定向 lint 通过。全量 lint 仍有 7 个警告和 1 个错误，错误位于无关审计改动的 `security-ip-summary.tsx:120`（嵌套三元表达式），本次未修改该文件。仓库 `bun run typecheck` 调用未定义的 `bun check`，因此使用实际 TypeScript 编译器完成类型检查，未修改项目脚本。 |
| 前端构建 | `bun run build` | 通过。 |
| 浏览器 | Chromium + Rsbuild 本地预览；1440/390 px、浅色/深色、规则弹窗、预览分组切换 | 通过；API 全部由隔离 fixture 接管，不写真实系统配置。证据在 `_local/group-relay-error-20261123/`。 |
| i18n / 编码 | 技能要求的写入脚本、`bun run i18n:sync`、七语言键和 BOM 校验 | 77 条翻译齐全、UTF-8 无 BOM；保留工作区其他任务的翻译，只移除 sync 新增的无关回填和报告差异。 |

分组匹配微基准：Windows amd64 / Ryzen 7 8700G，`go test ./setting/operation_setting -run '^$' -bench '^BenchmarkRelayErrorDecide_GroupScopes$' -benchmem -benchtime=200ms -count=1`。关闭约 4.6 ns/op、0 分配；单条全局规则约 506 ns/op；100 条规则 × 每条 100 组时首条命中约 477 ns/op，末条命中约 1.77 μs/op，未命中约 1.60 μs/op，未知组约 1.53 μs/op；启用时沿用文本处理的 3 次分配、约 48–49 B/op。该数据只说明匹配器的有界成本，不能替代整条请求的吞吐/尾延迟验收。

验证边界：未运行真实 ClickHouse 实例、MySQL 5.7/PostgreSQL 9.6 最低版本矩阵，也未执行正式 main→认证 mock 的 30k/100k RPM 全链路压测或生产部署。此次 SQL 仅给既有自助导出 SELECT 补上已正确引用的 group 列，过滤、排序、索引、分页和查询次数均未改变；两种实际日志数据库与 SQLite 导出已通过。上线前仍须统一升级所有处理请求的节点及前端后再保存含 groups 的规则，避免旧程序将其当作全局规则。

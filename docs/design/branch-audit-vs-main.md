# 分支审计：zhuzhan-f0b-latest 对比 main

审计对象：本分支相对 `main` 的全部改动，含未提交工作区。第 2 节的 33 个缺陷与第 5 节确认的日志追踪缺陷已全部修复；修复后经三轮独立审查，追加修复见第 7 节，遗留事项见第 8 节。

## 1. 测试组织

| 类型 | 文件命名 | 运行方式 |
|---|---|---|
| 契约测试（审计时即正确的行为，锁住它） | `*_branch_audit_test.go` | `go test ./...` |
| 回归测试（每个缺陷一条，修复前失败、修复后通过） | `*_branch_audit_regression_test.go` 及各修复新增的测试 | `go test ./...` |

审计期间缺陷测试曾用 `//go:build branchaudit` 隔离以保持基线全绿；修复后标签已全部移除，测试函数名中的 `Defect` 改为 `Regression`，名称后半段描述的是被防止的缺陷。

DB 相关测试使用 `.env` 中的真实 MySQL / PostgreSQL / Redis，SQLite 兜底；只清理自己创建的行。本机无 cgo，**`-race` 未运行**，并发类测试只有在 `-race` 下才有完整意义。

## 2. 缺陷清单（审计时的记录）

表中位置与行号是审计时的代码，修复后已变化；「回归测试」列为修复后在普通测试中运行的用例（名称前缀 `TestBranchAuditRegression_` / `TestRLAuditRegression_` / `TestRelayLogBranchAuditRegression_` 等）。全部条目状态：**已修复**。

严重度：**高** = 权限绕过 / 数据持续丢失 / 核心功能不可用；**中** = 计费偏差、信息泄露、可被外部放大的资源消耗；**低** = 边界条件、极端输入、可观测性。

main 对照列：本分支新增的子系统在 main 中不存在时记为「main 无此功能」。

### 2.1 高

| # | 问题 | 位置 | 失败场景 | main 对照 | 回归测试 |
|---|---|---|---|---|---|
| H1 | 系统设置分区权限可被绕过，写任意（含 root 级）设置 | `middleware/system_settings_auth.go:22-31`；`controller/option.go:177`（UpdateOptionGroup）、`:208`（UpdateOption） | 中间件用 `UnmarshalBodyReusable` 按 Content-Type 解析 `scope`，控制器固定按 JSON 解析且 `scope==""` 时跳过白名单。只拥有 `site.notice` 权限的管理员以 `application/x-www-form-urlencoded` 发送 `{"key":"About","value":"x","x":"&scope=site.notice&y="}`：中间件读到 `scope=site.notice` 放行，控制器读到空 scope 执行无范围写入。支付密钥、OAuth 密钥、`GroupRatio` 均可改；multipart 同样可行；`PUT /api/option/group` 同理 | `/api/option` 整组 `RootAuth`，不存在该问题 | `controller/option_scope_branch_audit_regression_test.go`（3 个） |
| H2 | 消费/错误日志写入口在压力下关闭后永不恢复 | `model/relay_log_pipeline.go` 关闭点 `:305/:352/:369/:404/:457`，唯一开启点 `:763`（`StartRelayLogFlushLoop` 的 once） | fallback 目录达到保留上限或 continuation 队列耗尽后 intake 关闭；运维腾出磁盘、开关管道都无效，直到重启前所有日志被拒；手动回放被当作「正在关闭」拒绝（`:921`）；被拒日志不计入任何计数器（`:1530-1534`），状态页仍显示 enabled / circuit closed。与设计文档 §19.1「恢复容量后重新启用 intake」不符 | 同步 `createLog`，无此状态 | `model/relay_log_pipeline_branch_audit_regression_test.go`：`IntakeStaysStoppedAfterRetentionRecovers`、`ReplayRefusedAfterPressureStop`、`LogsRefusedWhileIntakeStoppedAreNotCounted` |
| H3 | 价格监控设置在当前前端下保存必定失败 | `model/config_group.go:332`（`priceMonitorFields`） | 前端总会提交 `upstream_log_queries_per_host`、`upstream_ratio_refresh_hours`、`upstream_ratio_max_age_days`，控制器校验放行，`SaveConfigGroup` 以「字段不可编辑」拒绝，用户看到「稍后重试」 | main 无此功能 | `controller/price_monitor_branch_audit_regression_test.go`：`TestBranchAuditPriceMonitorSettingsSaveAcceptsChannelCostFields` |

### 2.2 中

| # | 问题 | 位置 | 失败场景 | main 对照 | 回归测试 |
|---|---|---|---|---|---|
| M1 | 专属倍率 `null` 被当作 0 倍率（免费） | `controller/user.go:715-723`（编辑解析）、`:870`（原样落库）；`setting/ratio_setting/user_exclusive_ratio.go:88-89`（relay 解析） | 管理员提交 `{"vip":null}`：`map[string]float64` 把 null 解成 0，`r<0/NaN/Inf` 校验放行，原串落库；relay 解析同样得到 0 且标记为专属规则，该用户在 vip 分组免费使用 | main 无专属倍率 | `controller/group_ratio_branch_audit_regression_test.go`；`setting/ratio_setting/branch_audit_regression_test.go` |
| M2 | `GET /api/group/:group/models` 无任何鉴权 | `router/api-router.go:396`，`controller/token.go:478` | 匿名用户可枚举任意（含隐藏）分组近 30 分钟有付费流量的模型；每次请求对 渠道×模型 逐对执行 logs 表 COUNT，一个匿名请求可触发数百次日志表扫描。无前端调用，无 Rule 11 要求的公开理由 | main 无此路由 | `router/public_route_branch_audit_regression_test.go` |
| M3 | 未扣费的流式失败，错误日志向用户展示上游原文 | 写入 `service/consume_settlement.go:84-92`；展示 `model/log.go` `maskErrorLogForUser`（约 :1180） | 该行错误日志没有 `error_type`/`error_code`，用户视图据此判定为本地错误，「隐藏上游错误」不生效；客户端实时帧已被遮蔽，日志列表和自助导出却显示原文 | main 无遮蔽与结算路径 | `model/log_branch_audit_regression_test.go`：`ZeroChargeStreamFailureErrorLogShowsUpstreamText` |
| M4 | 已扣费的流式失败，消费日志 content 含上游原文且不遮蔽 | `service/consume_settlement.go:81-86`；`model/log.go` `maskLogForUser`（约 :150） | 与设计文档 relay-error-message-masking §4.1「消费日志 content 是我方文本」不符 | 同上 | `model/log_branch_audit_regression_test.go`：`ChargedStreamFailureConsumeLogShowsUpstreamText` |
| M5 | 回放中途取消，已提交批次下次回放重复插入 | `model/relay_log_pipeline.go:1137-1140` | 关机先取消回放；临时文件被丢弃、源文件整体保留，其中已提交但未回写 id 的行下次回放再插一次，产生重复消费日志。SQLite/MySQL/PostgreSQL 均复现 | main 无回放 | `model/relay_log_pipeline_branch_audit_regression_test.go`：`ReplayCancelledMidFileReinsertsCommittedBatches` |
| M6 | 流式改造路径的上游错误帧被转成字节数组文本 | `relay/upstream_stream_buffered.go:329`（把 `json.RawMessage` 传给 `dto.GetOpenAIError(any)`，落入 `relaykit/dto/openai_response.go:433-438` 兜底分支） | 客户端收到 message `"[123 34 109 ...]"`、type/code 为 `unknown_error`；渠道自动禁用的关键词匹配（`service/channel.go:62`）与错误遮蔽规则都拿到乱码 | main `OpenaiHandler` 正确解析同一错误 | `relay/upstream_stream_branch_audit_regression_test.go`：`ErrorFrameMessageIsByteList` |
| M7 | 无有效输出但正常关闭的流按成功计费 | `relay/upstream_stream_buffered.go:231-247` | 只有 role 帧（或无 `error` 键的 `event: error`）后无 `[DONE]` 正常关闭：返回 200、content 为空、`finish_reason:"length"`，按估算输入计费，不重试。同样前缀遇坏帧/读错误却会重试（设计 §18 #3）。判断用的是 `ReceivedAnything` 而非 `HasOutput` | main 非流式拿完整响应体 | `CleanEOFWithoutOutputBilledAsSuccess` |
| M8 | 无 `index` 的 tool_call 增量被合并成一个 | `relay/upstream_stream_aggregate.go:205-208` | 两个不同 id/函数名的调用合并为一个，arguments 变成 `{"x":1}{"y":2}`（非法 JSON）；按次计费的工具计数 2→1 | main 保留两个调用 | `IndexlessToolCallsAreMerged` |
| M9 | 价格检查失败后每分钟重跑，无视配置间隔 | `controller/price_monitor_task.go:90` | 快照缺少来源头或矩阵版本旧时每个 tick 都启动检查，失败不更新快照；新装或升级后所有渠道与官方预设每分钟被拉取一次 | main 无此功能 | `TestBranchAuditPriceMonitorFailedRunIsNotRetriedEveryTick` |
| M10 | 删除的自定义端点仍被记住并优先使用 | `controller/price_monitor_task.go:214`（记忆含固定绝对 URL）；`controller/price_monitor_source.go:37`（优先尝试记忆 URL） | 管理员移除固定端点后，渠道仍按旧主机价格比较并影响亏损判定；与设计 §3.2「渠道变更后端点记忆失效」不符 | main 无此功能 | `TestBranchAuditPriceMonitorForgetsRemovedCustomEndpoint` |
| M11 | 请求日志中间件改变了超限请求体的 relay 结果 | `middleware/request_logger.go:375-378` | `GetBodyStorage` 失败时 body 已被读完并关闭，中间件静默返回；relay 再读得到 `http: invalid Read on closed Body`，不再映射为 413，Go 内部错误串回给客户端（违反 Rule 0 与 Rule 9） | main 无请求日志中间件 | `middleware/request_logger_branch_audit_regression_test.go`：`TooLargeBodyErrorMaskedForRelay` |
| M12 | 请求日志在鉴权前读取完整请求体 | `router/relay-router.go:73`（`RequestResponseLogger` 在 `TokenAuth` 之前）；`captureRequestBody` | 只保留 `RequestLogMaxBodyKB`，却把整个 body（最大 `MAX_REQUEST_BODY_MB`，默认 128MB）读入缓存；被鉴权拒绝的匿名请求照样付出完整读取成本。生产常开请求日志 | 同上 | `ReadsWholeBodyBeforeAuthRejects` |
| M13 | 请求日志列表向非 root 管理员暴露 `?key=` 令牌 | `middleware/request_logger.go:331`（存 `RequestURI()` 含查询串）；`router/request-log-router.go:24-27`（仅详情要求 RootAuth） | Gemini 风格客户端用 `?key=<token>` 鉴权，列表的 Url 字段原样返回令牌，绕过详情页的 root 限制 | 同上 | `ListUrlLeaksQueryStringApiKey` |

### 2.3 低

| # | 问题 | 位置 | 失败场景 | 回归测试 |
|---|---|---|---|---|
| L1 | 成本换算溢出成负数 | `service/channel_daily_limit_upstream.go:41,57`；`service/employee_commission.go:332` | `decimal.IntPart()` 超出 int64 回绕为负：上游消耗被 `recordChannelDailyUpstream` 丢弃；分组倍率极小（如 1e-18，校验允许）时佣金成本变成贷记。现实取值下难以触发 | `service/branch_audit_regression_test.go`（3 个） |
| L2 | 大于 2^53 的整数在日志 other 中被改写 | `model/log.go` `maskLogForUser`（约 :150）、`stripLogChannelNames`（:856-866） | 经 `map[string]any` 重编码，`9007199254740993` 变成 `...992`；main 用 `json.RawMessage` 投影保持原值 | `model/log_other_branch_audit_regression_test.go`；`model/log_branch_audit_regression_test.go`：`EmployeeChannelNameStripLosesIntegerPrecision` |
| L3 | 规则覆盖了状态码，用户日志仍返回上游原状态码 | `model/log.go` `maskErrorLogForUser` | 客户端收到 503，`/api/log/self` 中 `status_code` 仍为上游的 402 | `MaskedErrorLogKeepsOverriddenUpstreamStatus` |
| L4 | 通过校验的正则可在错误路径上耗时约 1s | `setting/operation_setting/relay_error_display_setting.go`（`compileRelayErrorEdits` / `apply`） | 200 字符上限仍接受 `.{1,999}`×24 + `Q`，4000 字符错误上单步约 1.0–1.26s，同步在 relay goroutine 上执行；需管理员刻意构造 | `setting/operation_setting/relay_error_display_branch_audit_regression_test.go` |
| L5 | 插入成功后 continuation 队列满，丢失 log id | `model/relay_log_pipeline.go:270-272`、`:521` | 记账以 log id 0 执行，成本/佣金记录丢失 `log_id` 关联与幂等键；main 传入真实 id | `ContinuationOverflowAfterPersistLosesLogID` |
| L6 | 纯记账事件溢出时写入无日志的 fallback 行 | `model/relay_log_pipeline.go:442` | 占用保留空间并计入 `fallback_total`，回放时跳过；会加速触发 H2 | `AccountingOnlyOverflowWritesLoglessFallbackRecord` |
| L7 | 状态页 backlog 未计入 worker pending | `model/relay_log_pipeline.go:1457-1459` | 状态显示 0，关机统计 `relayLogBacklog()` 却包含 | `StatusBacklogOmitsWorkerPending` |
| L8 | 请求日志列表并非严格按 created_at 倒序 | `model/request_log*.go`（`appendRequestLogIndexBatch` 按提交顺序） | 多 writer（默认 4）时后提交的旧批次排在前面，与设计 §14 不符 | `model/request_log_branch_audit_regression_test.go`：`ListNotNewestFirstAcrossWriters` |
| L9 | `BodyExists` 偏移量溢出 | `model/request_log_segment.go` | offset=MaxInt64 时对 9 字节段判定为存在 | `BodyExistsOffsetOverflow` |
| L10 | 总时限到期后预算闸门仍放行重试 | `service/relay_timeout.go:478-483` + `middleware/relay_timeout.go:272` | 总计时器触发后 `RelayTotalDeadline` 返回「无截止」，被当作「未配置预算」；在已取消的上下文上再选渠道、再预扣，随即超时并记到未联系过的渠道上（额度会退回） | `middleware/relay_timeout_branch_audit_regression_test.go` |
| L11 | 无时限请求仍走流式改造 | `relay/upstream_stream_adapt.go:48` | 用户两项非流式时限均为 -1 时控制器无截止，但仍被改造，函数注释自称「纯风险」 | `UnlimitedTimeoutStillAdapts` |
| L12 | 改造路径忽略渠道 ForceFormat | `relay/upstream_stream_aggregate.go:493-496` | 非标准 usage 字段透给客户端；main 在 ForceFormat 下重编码剥离（`relay/channel/openai/relay-openai.go:334-338`） | `ForceFormatIgnored` |
| L13 | Azure `prompt_filter_results` 被丢弃 | `relay/upstream_stream_aggregate.go:241-286`（`AddFrameExtras`） | 提示词内容过滤结论丢失；main 透传 | `AzurePromptFilterResultsDropped` |
| L14 | sub2api 价格步骤对被拒密钥无上限 | `controller/price_monitor_sub2api.go:343` | sub2api 60s 内 120 个无效密钥即封 IP 60s（封禁同样影响本机到该主机的 relay）；倍率步骤限 20/主机，价格步骤不限 | `SubAPIPriceSourceCapsRejectedKeysPerHost` |
| L15 | 347711c4d5 的空价格修复对 ratio_config 不完整 | `controller/ratio_sync.go:56`（`pricingPayloadHasPrices`） | 只检查键存在，`{"model_ratio":{"m":null}}` 被视为成功来源，所有模型显示「缺失」并计为价差 | `RejectsPayloadWithoutUsablePrices` |
| L16 | 编辑专属倍率后缓存可能回滚最长 60s | `controller/user.go` 约 :882-918；`model/user_auth_cache.go` `writeUserCache` | 编辑 `group_ratios` 不递增 `auth_version`；并发 relay 缓存未命中时读到旧行、在编辑提交后以同版本写回，旧倍率一直计费到过期（`SYNC_FREQUENCY` 默认 60s）。`Group` 字段已有同类防护 | `model/user_cache_branch_audit_regression_test.go` |
| L17 | `UserExclusiveGroupRatioCacheMax` 非法值显示已保存但不生效 | `model/option.go:372`（原值落库）、`:499` | 保存 `-5`/`abc`/`1.5` 返回成功，设置页显示新值，运行时仍用旧值，重启后回到默认 | `model/option_user_group_ratio_branch_audit_regression_test.go` |

## 3. 未列为缺陷但需要知道的事项（含处理状态）

- **Rule 6 上游文件**：`web/src/lib/` 下五个上游文件与 merge-base 字节一致，本分支唯一相关提交 `7e49d26709` 只是目录迁移；与 `main` 的差异全部来自 main 的后续提交（如 `12be9975c0`、`45c3fbe8ae`、`d8cb177440`、`385d2dfd10`），按 Rule 6 整体同步即可。**未处理**（属于合并上游，不在本次修复范围）。
- **Rule 13 硬编码中文**（价格监控源名称、档位标签、分享页）：**已修复**，后端 i18n 渲染，分享页整页按请求语言本地化。
- **价格亏损设计文档漂移**（全 0 分组倍率）：**已修复**，文档改为与代码一致（按售价系数 1 处理）。
- **`interval_minutes` 无上限**：**已修复**，上限 43200 分钟（30 天）。
- **按次计费超时用户的双倍上游调用**：设计如此（non-stream-timeout-loss-prevention §17），未改。
- **`GroupRetryTimes` 从 DB 加载不校验**：未改；手工改库的值只受 `max_total_attempts` 约束。
- **员工视图**：保留 `admin_info` 与未遮蔽错误原文（设计如此），已剥离 `root_info` / `audit_info`。
- **与 main 对齐的行为差异（有意）**：全局 RetryTimes 为负时本分支仍执行 1 次尝试，main 执行 0 次并滞留预扣额度。

## 4. 已验证正确的契约（契约测试覆盖）

- **重试**：各 RetryTimes（0/1/2/3/5/20/50）下尝试次数 = main 的 `RetryTimes+1`；用户 > 分组 > 全局优先级的每个分支（-1、0 继承、< -1、缺失分组、空分组名、校验上限）；`max_total_attempts` 在超时功能关闭时仍生效；预算闸门毫秒级边界、不拦首次尝试。
- **计费（真实 DB + 本地假上游端到端）**：所有尝试只预扣一次；所有失败路径全额退回；重试后成功只扣一次（流式与非流式）；charge 模式只结算最后一次尝试。
- **日志可见性**：user/admin/root 三级投影矩阵、幂等性、先投影后遮蔽、无变化时 other 字节不变、自助导出忽略 `username`、30 天边界。
- **中继日志管道**：批次/周期/重试配置为 0、负数、1 时均能终止且每行只写一次；失败插入后重试的 continuation 恰好一次；回放保留坏行且幂等（三库）；按主键分块回查去重（三库）；熔断开/半开探测。
- **流式改造**：SSE 各种分帧（无空格 `data:`、CRLF、注释行、`[DONE]` 后帧、逐字节读取含多字节 UTF-8）；带 index 的并行 tool_call；多 choice；usage 明细计费；与 main `OpenaiHandler` 的计费一致性；客户端断开及时释放上游；Rule 5 零值保留。
- **价格监控**：亏损计算的零价/单边价/免费分组；sub2api 单位换算与 1MB 截断；跨主机重定向不携带任何凭据；密钥只在头部；快照不含密钥；分享查询拒绝错误/前缀/空/过期密码；只有一个检查同时运行。
- **用户/缓存/鉴权**：Redis HINCRBY 脚本参数顺序、溢出与类型错误不改值；专属倍率解析缓存并发计数与清扫；用户缓存新字段经 Lua 往返、旧 schema 重建；UpdateUser 的角色层级限制与事务回滚；遍历全部 gin 路由，断言每条路由要么有鉴权、要么在显式公开白名单中。

## 5. 审计阶段的未证实项（处理状态）

- **管道积压时 `GetLogTraceByRequestId` 取到早期错误日志**：已证实并**已修复**（同一秒内消费日志优先于错误日志，`model/log.go` `logTraceNewer`）。
- **中继日志管道关机竞态**：已处理——入口在排空前封口，封口后到达的事件被拒收并计数；辅助车道发送与关闭互斥；pending 车道只在持有 `relayLogFlushMu` 时取走，超时仍在运行的刷盘周期持有时关停不碰它（relay-log-async-batch §19.1 已知限制）。
- **支付回调签名校验**：未逐一核验。
- **sub2api 实际 IP 封禁阈值**：未核验，限流按 120 次/60 秒的文档值取 20 次/60 秒/主机/进程。

## 6. 修复摘要

| 范围 | 条目 | 修复要点 |
|---|---|---|
| 鉴权/设置 | H1 | 设置写入必须是 `application/json`；中间件把授权的 scope 写入上下文，控制器按该 scope 校验，不再重新解析 body |
| 中继日志管道 | H2、M5、L5、L6、L7 | 压力下不再关闭 intake（只在关停时关闭）；回放取消只保留未写入的记录；continuation 溢出保留真实 log id；纯记账事件不写 fallback 文件；状态 backlog 计入 pending；计数器区分改道（`continuation_overflowed`）与真实丢失 |
| 价格监控 | H3、M9、M10、L14、L15 | 补齐可编辑字段；失败后按间隔重试；只记忆自动探测端点；sub2api 被拒密钥按主机 60 秒窗口限流（进程级，预留倍率步骤名额，同一运行内复用稳定答复）；价格值必须可用 |
| 专属倍率/用户缓存 | M1、L16、L17 | `null` 倍率在编辑时拒绝、中继解析时跳过；新增 `users.profile_version` 与 Redis 版本下限，阻止陈旧缓存回填覆盖管理员编辑；`UpdateWithTx` 不再写管理员独占的计费列；非法缓存上限值拒绝保存 |
| 路由 | M2 | `GET /api/group/:group/models` 需登录且只能查可用分组；结果按分组缓存 60 秒并合并并发查询 |
| 错误信息隐藏 | M3、M4、L2、L3、L4 | 流式失败的日志与实时帧同一口径匹配规则；消费日志只写我方文本，上游原文仅存 `admin_info.stream_error`（管理员导出附带）；`other` 按 RawMessage 投影保留大整数；用户日志显示规则覆盖后的状态码；正则步骤按编译规模限额，纯文本步骤不走正则，匹配输入有上限 |
| 流式改造 | M6、M7、M8、L11、L12、L13 | 错误帧正确解码；无输出且未结束的流走重试而非计费；无 index 的 tool_call 按 id/名称/参数完整性拆分；仅在存在截止时间时改造；遵守 ForceFormat；透传 Azure `prompt_filter_results` 与每个 choice 的 `content_filter_results` |
| 请求日志 | M11、M12、M13、L8、L9 | 只读取有界前缀并拼回 body，不再吞掉 413 或在鉴权前读完整 body；URL 中的凭据参数在记录时打码（旧记录在恢复与读取时打码）；列表严格按 created_at 倒序；段内偏移无溢出 |
| 成本换算 | L1 | `common.Int64FromDecimal(d, limit)`，单笔成本/佣金封顶 `1<<50`，累加器留足余量 |
| 重试预算 | L10 | 总截止时间过后闸门始终判定耗尽；最小预算读取请求开始时的设置快照 |

## 7. 修复后独立审查的追加修复

三轮独立审查（每轮在新上下文中按 Rule 19 清单审查修复 diff，合并前编号为 Rule 16），追加修复如下：

- **第一轮**：成本饱和到 `MaxInt64` 会让日上限累加器与 `cost_quota + ?` 溢出（改为 `1<<50` 封顶）；M8 同名无 id 的调用仍被合并；M6 无 type 的错误丢失 message；M3 实时帧与日志匹配输入不一致；M4 我方超时被标为上游失败、管理员导出丢失上游原文；L4 纯文本步骤未计入限额、输入无上限；一条失效的已存规则会关闭整个隐藏功能（改为只跳过该规则并在设置页提示）；M2 查询无合适索引（改为缓存）；请求体读取把客户端中途断开当作结束；访问日志与旧请求日志中的 `?key=`；专属倍率隔离未覆盖清理 CAS、`UpdateUserSetting`、`inviteUser` 整行写回；滚动发布期间字段写入会把 schema 标记盖到旧 hash 上；`continuation_dropped` 把改道计为丢失；回放在开头取消时无谓地复制整个文件；关停在锁外读取 pending（数据竞争）并把丢弃的记账只计入 `fallback_errors`。
- **第二轮**：`UpdateWithTx` 以陈旧整行写回会撤销并发的管理员编辑（中）；编辑用户可能写回清理刚删除的倍率；两处邮箱写入未递增版本；访问日志在路径含 `%3F` 时绕过打码（中）；普通分片上无害的 `error` 对象导致改造请求失败（中）；两份凭据参数清单不一致；sub2api 同一密钥并发重复发送、等待不响应 ctx、数字字符串价格被拒；预算闸门读取全局而非请求快照；溢出日志无限流；关停在锁外取 retry 缓冲；价格源覆盖按键计数；时钟回拨后检查停摆；设置弹窗未校验整数。
- **第三轮（错误信息隐藏）**：截止时间晚于上游错误帧触发时错误日志仍显示上游原文（中）；Realtime 的 `error` 及其它终止错误事件未经隐藏直接发给客户端（中）；关键词只在前 4000 字符匹配；截断可能保留半截密钥；状态/预览/预设接口未校验已授权 scope；单键保存绕过规则校验并返回原始错误串；员工视图剥离 `root_info` / `audit_info`。

第三轮中，流式边界、用户缓存、脱敏与 sub2api 的修复只做了作者自查（现 Rule 19 清单），未再做独立审查；错误信息隐藏的修复做了独立审查。

## 8. 遗留事项与运维注意

- **发布前必读**：
  - 新增列 `users.profile_version`（AutoMigrate）。PostgreSQL < 11 上 `ADD COLUMN ... NOT NULL DEFAULT` 在排他锁下重写 users 表，建议维护窗口手动加列；MySQL 5.7 为在线重建。
  - 用户缓存 schema 由 2 升到 4：新旧节点并存期间活跃用户每次请求多一次 DB 读取，旧节点不执行版本下限检查，直到全部节点升级。
  - 设置写接口（`/api/option/*`）只接受 JSON；前端已满足。
- **有意的行为变化**：访问日志路径改为转义形式；凭据参数按子串匹配打码（`max_tokens=` 等也会被打码）；流式结算的文本调整（我方文本入 content、上游原文入 `admin_info.stream_error`）在隐藏功能关闭时同样生效；超过 16 KB 的错误在 keep/无匹配时只返回被规则检查过的部分。
- **已知限制（已写入各设计文档）**：
  - 上游原有字段（status、role、group 等）仍沿用 main 的读改写，存在并发丢失更新（与 main 相同）。
  - 编辑确实改变专属倍率时，另一分组的清理在读与提交之间到达，仍可能写回该分组（毫秒级窗口）。
  - `GET /api/group/:group/models` 的 30 分钟 DISTINCT 查询仍无覆盖索引，每节点每分组每分钟至多一次。
  - sub2api 限流按进程与主机名计算，多节点共用出口 IP 时上限叠加。
  - 关停超时后仍在运行的刷盘周期所持有的事件不会被移交。
  - 无 type、id、index 且参数均为空的同名 tool_call 仍会合并；参数为裸标量时无法判断完整。
- **与本次改动无关但发现的问题**：`ja.json` / `ru.json` / `zh.json` 在 HEAD 中已有 `"????"` 译文；请求日志的请求头原样保存（仅 root 详情可见）。

## 9. 验证命令

```bash
go vet ./...
go test ./... -count=1
cd web && bun run typecheck && bun run lint
```

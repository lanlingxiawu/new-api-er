# 单用户 AI 请求双超时

状态：实现与回归验证完成  
日期：2026-08-06

## 2026-11-10：全局默认继承修正

用户已要求按“全局开启则 0 继承后台默认、关闭则恢复旧逻辑”修复。以下修订取代本文早期版本的全零用户退出和零有效时长退出规则。

### 目标、规则与生命周期

- 总开关开启且 controller 调用启动点时，所有用户进入托管；四项全 `0`、缓存字段缺失也按当前请求模式继承全局默认。
- 用户单项 `0` 继承，正数覆盖，`-1` 不限制；全局单项 `0` 表示不限制。两个有效时长均为 `0` 时仍托管但不创建 timer，防止旧环境变量重新施加限制。
- 仍使用用户的 `first_output` / `idle` 模式；全零用户也会采用该模式。默认 `first_output` 在首次有效输出后停止响应计时，不等价于旧扫描器的持续空闲超时。
- 全局关闭时，新请求完整恢复旧 HTTP Client 和扫描器限制。未调用启动点的任务、fetch、Realtime 等范围不变。
- 数据流：现有设置接口保存 options → 原子发布快照 → 请求入口捕获快照 → 解析当前模式的用户覆盖 → 创建控制器和 writer → 正常输出停止/重置 timer → 结束关闭控制器。保存、关闭或重新开启均只影响后续请求，在途请求保留已捕获快照。

### API、数据模型、配置和错误处理

沿用现有管理员分组配置 API（`system-tuning.relay-timeout` 权限）及管理员用户更新 API；请求/响应字段和权限均不变。不新增表、列、迁移、索引或配置项，不修改持久化逻辑。沿用既有 `relay_timeout`、504、已提交响应终止和计费处理，不改变错误格式或计费算法。默认用户现在会参与既有受管超时处理，因此原来依赖“全零不托管”的非流式超时计费分析不再适用。

### Main Chain Impact

同步部分仅从请求 context 读取当前模式两个整数、一个模式字符串和已捕获的快照，解析后创建请求本地控制器、writer 及最多两个 runtime timer；没有新增 DB、Redis、网络调用或后台任务。timer 到期沿用现有短回调取消请求，handler 返回负责清理。正常路径不创建每请求常驻 goroutine。开关 `relay_timeout_setting.enabled` 可随时恢复新请求的旧链路。

### Shared Resource Audit

共享配置快照沿用原子发布/读取；用户字段沿用现有鉴权缓存，不增加 Redis key 或 DB 访问。控制器锁、状态、timer 都是请求私有。HTTP client 仍浅拷贝并复用同一 Transport/连接池，不创建新池；受管请求移除旧 Client.Timeout 和流扫描超时。长请求占用已有连接的时间可能随新默认值增加，需要按实际时长规划容量。`users`、`options`、账本与日志的读写实现不变。

### Concurrency Analysis

30k RPM 为约 500 RPS，100k RPM 为约 1667 RPS：新增 DB/Redis 调用均为 0，无全局/分布式锁；已有的一次快照读取不变。全零用户也会分配请求控制器和最多两个 timer，数量按在途请求数增长；平均 30 秒时分别约 15,000 / 50,000 个控制器和最多 30,000 / 100,000 个 timer。`first_output` 首次输出后停止响应 timer，`idle` 沿用请求私有锁重置 timer。该估算不代表完成 100k RPM 压测。

### 回归计划

先修改测试，覆盖流式/非流式全零与缺失字段继承、1200→2200 热更新、在途快照不变、正数覆盖、`-1`、全局两个零仍托管、仅另一模式有覆盖、模式选择、关闭与重新开启、无启动点不托管。检查受管 HTTP client 去除旧 Timeout 且复用 Transport，以及退出时停止 timer。随后运行相关 Go 包测试；不调用真实上游。

### 本次验证结果

- 修改测试后、修改实现前，`go test ./middleware -run TestRelayRequestTimeout -count=1` 在全零继承、模式选择、零限制和热更新场景失败，确认测试能捕获原缺陷。
- 修复后 `go test ./middleware ./setting/operation_setting -count=1` 均通过。该轮清空测试进程的 `SQL_DSN` / `LOG_SQL_DSN`，middleware 的 DB 用例按既有 harness 跳过；本次超时用例均执行。
- `go test ./service -run 'Test(RelayHTTPClient|NewRelayHTTPClient|RelayResponseTrace|RelayRequestContext|RelayContextError|RelayTimeoutPerCall|RelayUpstreamContext|ResolveRelayTimeoutOverride|ValidateRelayTimeout|NormalizeRelayStreamResponse|ValidateRelayStreamResponse|RelayRetryBudgetExhausted)' -count=1` 通过。使用临时 PostgreSQL 17.11（`postgres:17-alpine`），harness 创建隔离 schema，验证完成已停止并移除容器。最初因无 SQL_DSN 无法启动服务层测试，随后以该隔离数据库完成验证。
- `go test ./controller -run 'Test(NormalizeRelayTimeoutError|RelayRetryStopsForOwnedTimeout|HandleRelayTimeoutResponse|ShouldReportAdaptedRelayTimeout)' -count=1` 通过。
- `go test -race ./middleware ./setting/operation_setting -run 'Test(RelayRequestTimeout|ReplaceRelayTimeoutSetting|ApplyRelayTimeoutEnvDefaults)' -count=1` 通过。Windows 需 `CGO_ENABLED=1`、`CC=C:/msys64/mingw64/bin/gcc.exe` 并将该 bin 加入 PATH；初次未配置完整工具链时编译失败，补齐后通过。
- `gofmt`、`git diff --check` 通过。未修改数据库行为、前端或 relaykit；未运行三库迁移矩阵、全仓测试或 100k RPM 压测，未部署生产。
- 记录位于 `_local/relay-timeout-inheritance-*.log`。本次只移除两个启动退出条件，既有后台持久化、快照发布和用户数据保持原实现。

## 1. 目标与范围

每个 AI 请求同时支持两个限制，并按流式、非流式分别配置：

| 用户字段 | 语义 |
|---|---|
| `stream_response_timeout` | 流式响应超时秒数；具体按 `stream_response_timeout_mode` 解释 |
| `stream_response_timeout_mode` | 流式响应超时模式：首包超时或连续静默超时 |
| `stream_total_timeout` | 流式请求绝对总时长 |
| `non_stream_response_timeout` | 非流式上游首次响应超时 |
| `non_stream_total_timeout` | 非流式请求绝对总时长 |

同一请求同时启动对应模式的响应计时器和总时长计时器，任一先到期都会取消上游请求。上游调用只由这两个计时器取消，不随客户端断开而取消（与主分支一致，见 non-stream-timeout-loss-prevention.md §23）。超时本身的计费见该文档与 [relay-timeout-cost-bearing.md](relay-timeout-cost-bearing.md)（按用户 `non_stream_timeout_billing`：退款、按用量计费或只收输入）。

本功能覆盖普通 relay 与 Gemini 原生路由。提交即计费的请求不启用单用户超时（与主分支一致）：Midjourney 提交、任务 / 视频提交（`RelayTask`）、Coze 非流式、阿里图片生成与编辑，见 [relay-timeout-cost-bearing.md](relay-timeout-cost-bearing.md) §2。客户端 Realtime/WebSocket 长连接不在范围内；提供商内部使用 WebSocket 的普通 relay 请求仍继承请求超时。

## 2. 热更新全局配置与环境变量回退

新增 DB 持久化、进程内快照热更新配置：

| 配置键 | 默认值 | 说明 |
|---|---:|---|
| `relay_timeout_setting.enabled` | `true` | 总开关；关闭时忽略所有用户覆盖并完全走旧超时链路 |
| `relay_timeout_setting.response_timeout_seconds` | `300` | 流式和非流式响应超时共享默认值，`0` 表示不限制 |
| `relay_timeout_setting.total_timeout_seconds` | `0` | 流式和非流式总时长共享默认值，`0` 表示不限制 |

管理员通过现有系统设置 API 保存后，配置管理器原子发布不可变快照；新请求立即读取新值，在途请求保留启动时的快照，不发生半程变更。总开关关闭时不创建请求级计时器、不包装响应写入器，`RELAY_TIMEOUT`、`STREAMING_TIMEOUT` 和原有 HTTP/流扫描逻辑恢复生效。

部署环境变量继续作为启动回退值：

```env
STREAMING_TIMEOUT=300
RELAY_TIMEOUT=0
RELAY_IDLE_CONN_TIMEOUT=90
```

启动顺序为：代码默认值 → 环境变量回退 → DB 已保存配置。`RELAY_IDLE_CONN_TIMEOUT` 只控制连接池空闲连接，不参与请求超时。若 DB 没有对应配置，响应默认取 `STREAMING_TIMEOUT`，总时长默认取 `RELAY_TIMEOUT`；总开关开启时，所有调用启动点的请求均采用这套默认值和用户覆盖规则。

每个用户字段的取值规则：

- `0`：继承当前请求快照的全局默认值，四项全零也一样；
- `-1`：明确关闭该用户的对应限制；
- `1..604800`：使用用户值，单位秒。

用户值是真正覆盖，不和全局值取最小值。总开关优先级最高；关闭时所有用户值均不生效。

`stream_response_timeout_mode` 的取值规则：

- `first_output`：默认值。`stream_response_timeout` 只限制首包/首次有效输出等待时间，首次有效输出后停止响应 timer。
- `idle`：连续静默超时。`stream_response_timeout` 在首次有效输出后继续作为“距离上一次有效输出的最长静默时间”，每次有效输出都会重置 deadline。

模式字段对所有受管流式请求生效，包括四个时长字段全零的用户。

**接管前提**：总开关开启且 controller 调用启动点。托管请求不再创建旧 `STREAMING_TIMEOUT` ticker，并移除共享 HTTP Client 的 `RELAY_TIMEOUT`。两个有效时长都为 `0` 时，仍创建控制器和 writer，但不安装 timer，以兑现“不限制”的配置。只有总开关关闭或不在启动范围内的请求沿用旧链路。

## 3. 流式与非流式语义

### 3.1 流式请求

- 响应计时器从 relay 开始运行；收到实际写给客户端的有效输出后按 `stream_response_timeout_mode` 处理。
- `first_output` 模式下，首次有效输出停止响应计时器，不再继续重置。该模式只用于限制“首包/首个有效输出等待时间”。
- `idle` 模式下，首次有效输出和后续每次有效输出都会把响应计时器重置为 `stream_response_timeout` 秒；超过该时间没有新的有效输出时，取消上游并结束流。
- SSE 注释、网关心跳 `: PING` 和空行不停止响应计时器；适配器吞掉或缓冲、没有写给客户端的上游事件也不停止。
- `first_output` 模式下，首次有效输出后后续静默不再由响应计时器断开；如需限制整段流的最长运行时间，应使用 `stream_total_timeout`。
- 总时长从 relay 开始运行且永不重置，到期会硬性终止流。
- `idle` 模式下，总时长仍独立运行且不被有效输出重置；响应静默 timer 与总时长 timer 任一先到期都会终止流。
- 总时长为 `0` 或用户覆盖为 `-1` 时，`first_output` 模式下首次有效输出后的流不会被本功能按静默时间或总时长截断；`idle` 模式下仍会受响应静默 timer 管理。

### 3.2 非流式请求

- 响应计时器从 relay 开始运行，到收到主上游 HTTP 首字节或 SDK/WebSocket 首个响应事件时停止。
- 如果该响应触发可重试错误，下一次重试开始前重新启动完整响应窗口；总时长计时器不重置。总时长不受限时，最坏等待上界为 `(RetryTimes + 1) × 响应超时`。
- 总时长覆盖响应体读取、转换、重试和 handler 返回。

## 4. 数据模型与缓存兼容

`users` 表保留四个非空整数列，默认值均为 `0`，并新增一个流式响应超时模式列：

```go
StreamResponseTimeout    int `json:"stream_response_timeout" gorm:"type:int;not null;default:0;column:stream_response_timeout"`
StreamResponseTimeoutMode string `json:"stream_response_timeout_mode" gorm:"type:varchar(32);not null;default:'first_output';column:stream_response_timeout_mode"`
StreamTotalTimeout       int `json:"stream_total_timeout" gorm:"type:int;not null;default:0;column:stream_total_timeout"`
NonStreamResponseTimeout int `json:"non_stream_response_timeout" gorm:"type:int;not null;default:0;column:non_stream_response_timeout"`
NonStreamTotalTimeout    int `json:"non_stream_total_timeout" gorm:"type:int;not null;default:0;column:non_stream_total_timeout"`
```

字段不参与筛选、排序或关联，不新增索引。沿用现有 UserBase Redis 缓存；未启用 Redis 时仍走原有 DB 读取，不新增进程内用户缓存。缺失时长字段按 `0` 处理，缺失模式字段按 `first_output` 处理。此次扩字段不提升基础缓存 schema 版本，避免滚动发布期间新旧实例互相判定缓存失效并制造同步 DB 回源。

旧实例使用逐字段 `HSET` 刷新已有 hash 时不会删除超时字段；只有在 hash 不存在、旧实例从 DB 回源并重新创建缓存时，新字段才会暂时缺失并按默认值处理。新实例将缺失时长作为 `0` 继承默认值。滚动发布期间旧实例仍可能执行历史退出规则，应完成所有实例升级后再依赖一致的全零继承行为。

兼容性语义需要在发布前显式确认：旧版 `stream_response_timeout` 等价于本设计的 `idle`，而新增列、旧 Redis hash 缺字段和非法缓存值都会归一为 `first_output`。因此已经配置非零流式响应超时的存量用户会默认切换为“仅首包超时”。代码不自动回填；若发布策略要求保留旧语义，必须在启用新版本前把这些用户的 `stream_response_timeout_mode` 预填为 `idle`。

数据库列由现有 AutoMigrate 在启动阶段创建，不在 relay 请求内执行。大表上线仍需安排迁移窗口：PostgreSQL 9.6 添加带默认值的非空列可能重写表并持有强锁，MySQL 5.7 也可能重建表或等待 metadata lock；期间用户缓存 miss 的 DB 回源和管理写入可能被阻塞。生产发布应先在目标数据库验证 DDL 计划，并在低峰期执行或采用分阶段加列/回填/加约束策略。

全局配置复用现有 `options` 表和 ConfigManager，不新增表、连接或独立保存接口。

## 5. 数据流

1. 用户鉴权从现有 UserBase Redis 缓存读取四个时长字段和一个模式字段并写入 Gin context；缓存 miss 或未启用 Redis 时仍沿用原有 DB fallback。
2. 超时中间件只读取一次全局不可变快照，不提前包装 writer。总开关关闭则直接放行。
3. 范围内 controller 在请求解析完成并确定 `stream` 后显式启动超时；fetch、回调和 Realtime controller 不启动，因此保持原 writer 和原请求逻辑。
4. 从当前请求快照选择全局默认并解析当前模式的两个用户覆盖值，同时读取 `stream_response_timeout_mode`。创建请求本地控制器，按正数有效时长安装响应和总时长 timer，并管理 writer 和可取消 request context；上游调用使用与客户端连接脱钩、只在控制器截止时刻取消的 context（`service.BindRelayRequestContext` / `RelayUpstreamContext`）。
5. 主上游请求继承该 context；流式有效输出按模式停止或重置响应 timer，非流式首响应停止响应 timer。
6. 可重试的非流式请求在下一次尝试前重启响应 timer；总时长 timer 始终不重置。
7. 任一 timer 到期取消上游并记录 `response_timeout` 或 `total_timeout`；响应包装器拒绝到期后的业务输出，并通过请求本地放行门闩允许 controller 穿过后续 writer 包装层写入标准化超时响应；handler 结束时停止 timer。

## 6. API 与界面

不新增 endpoint。用户超时字段沿用管理员用户读取/更新接口：省略保留原值；单项时长 `0` 继承、`-1` 关闭、正数覆盖，全零也继承；非法值返回现有参数错误。`stream_response_timeout_mode` 在用户超时配置区提供二选一控件：

- 首包超时：保存 `first_output`，前端文案说明“只等待首次有效流式输出，之后不再按静默时间断开”。
- 连续静默超时：保存 `idle`，前端文案说明“每次有效流式输出都会重新计时，静默超过该秒数后断开”。

系统调优页新增“AI 请求超时”热配置区，通过现有分组配置接口保存 `relay_timeout_setting` 三个字段。权限范围为 `system-tuning.relay-timeout`，仅管理员可写。界面明确说明热更新只作用于新请求以及环境变量仅是启动回退。

## 7. 错误处理和协议兼容

- 未提交响应：返回 HTTP `504` 和现有 `relay_timeout` 错误语义。
- Midjourney 兼容响应保持 `code` 为整数 `4`，在 `description` 中表达 relay 超时，避免破坏客户端反序列化（Midjourney 提交不启用单用户超时，此分支不会触发）。
- 已提交流式响应：取消上游并结束流，不能改写已经发送的状态码。
- 客户端主动断开不归类为 relay 超时，也不取消上游调用：受管的非流式、任务提交、轮询与 WebSocket 调用继续执行到完成或我方截止时刻并按实际用量结算；流式在客户端断开时关闭上游并按估算结算（与主分支相同）。
- 超时后停止当前重试链，沿用现有错误日志记录路径；超时错误携带项目既有 `skipRetry` 属性，因此原 `ShouldDisableChannel` 逻辑自然跳过渠道故障/自动禁用，不修改 `processChannelError` 的原有判断。功能关闭时，原有渠道错误处理逻辑不变。
- 已提交响应后若仍有业务代码尝试写入，写入会返回 `context.DeadlineExceeded`，并在请求结束时记录拒写告警，使已提交响应被截断的窄窗口可被观测。
- ping 写入与超时同时发生时，结束原因保持为 `timeout`，不被 `ping_fail` 抢占。
- Xunfei 读取协程只捕获请求 context 和超时观察接口，不持有可被 Gin 复用的 `*gin.Context`；请求结束会关闭 WebSocket，所有通道发送都可被取消。

## 8. 与现有 HTTP Client 的关系

功能开启且请求已托管时，AI relay 主调用通过只受控制器截止时刻约束的上游 context 实施每用户总时长，浅拷贝 client 并移除共享 `Client.Timeout`，但继续复用同一 Transport/连接池。准备性调用继承同一个可取消 context。总开关关闭或未托管调用时，完整保留共享 Client 的旧行为。

仅非流式、仍在等待首字节的主上游请求安装首字节回调；受管请求（流式与非流式）的主上游请求另装 `WroteRequest` 回调，记录请求是否已完整写给上游（我方超时时据此决定是否计费，见 relay-timeout-cost-bearing.md §15）。未受管请求与非主调用不创建 trace。

实现按职责集中，避免把超时算法散落到原 relay 逻辑：

- `middleware/relay_timeout.go` 独占 timer、响应 writer 和到期写入门禁；
- `service/relay_timeout.go` 集中用户覆盖解析、托管状态、请求 context 绑定、HTTP client 派生、首字节 trace 和 context 错误归一；现有 `service/http_client.go` 不承载功能分支；
- `controller/relay_timeout.go` 集中 OpenAI/Claude、Midjourney 和 Task 的协议错误转换；`controller/relay.go` 只保留提交入口启动、重试窗口重启以及结果归一调用；
- 普通 HTTP 渠道统一经过 `relay/channel/api_request.go` 的一个接入点；仅 AWS SDK、WebSocket、轮询和独立上传等绕过该入口的实现保留最小 context 接入。

## 9. Main Chain Impact

同步热路径：鉴权沿用现有 `UserBase.WriteContext`，不增加 DB/Redis 调用；每请求读取一次原子配置快照。controller 显式启动后读取当前模式两个整数和一个模式字符串，创建控制器和最多两个 Go runtime timer。响应包装器通过原子指针发布请求本地状态，不在每个分片上查询 Gin context；流式有效输出执行零分配行分类。`first_output` 首次有效输出停止响应 timer，后续分片不更新 timer；`idle` 每次有效输出进入请求本地短临界区更新 deadline 并 reset 既有 timer，不做 I/O。总开关关闭以及未调用启动点的 fetch/Realtime 等请求不包装 writer、不创建 timer。

没有请求级 DB 查询、新 Redis 往返、分布式锁或无界 goroutine。超时处理不向计费链增加同步逻辑。Xunfei 使用原有每连接读取协程，但请求取消必定关闭连接并允许协程退出。

## 10. Shared Resource Audit

| 资源 | 访问方式 | relay 是否访问 | 结论 |
|---|---|---|---|
| `users` 超时列和模式列 | 管理接口写；现有用户缓存装载时读 | 是 | 无新增请求级 DB 查询，无需索引 |
| `users` 启动迁移 DDL | AutoMigrate 加模式列，仅启动阶段执行 | DB fallback 会访问同表 | 大表发布需迁移窗口或分阶段 DDL，避免阻塞缓存回源 |
| 用户 Redis 缓存 | 现有 UserBase hash 增加四个整数和一个模式字符串 | 是 | 复用现有 key；缺失时长按零处理，缺失模式按 `first_output` 处理；不提升 schema；不新增进程内用户缓存 |
| Gin 请求 context | 鉴权多写一个模式字符串 | 是 | 请求本地，不跨请求共享，不增加 I/O |
| `options` 三个配置行 | 管理接口写；ConfigManager 加载并发布快照 | 热路径只读内存快照 | 无请求级 DB/Redis 调用 |
| 配置快照 | 原子替换、请求开始时读取 | 是 | 不持锁、不缓存可变指针 |
| 请求 timer/control | 每请求本地 | 是 | 不跨请求共享，仅短临界区，不持锁执行 I/O |
| HTTP 连接池 | 复用现有 Transport | 是 | 浅拷贝 client，不新建连接池 |
| 消费/平台成本账本 | 不新增访问 | 是 | 原逻辑不变 |

## 11. Concurrency Analysis

按 100,000 RPM（约 1,667 RPS）：

- 新增 DB：`0 / 请求`；新增 Redis：`0 / 请求`；
- 配置读取：`1 次 atomic load / 请求`；
- 主动新增 goroutine：`0 / 请求`；timer 到期仅运行 Go runtime 短回调；
- 鉴权 context：每个认证请求新增 `1` 次请求本地字符串写入；启动时读取当前模式 `2` 个整数和 `1` 个模式字符串；
- timer：最多 `2 / 受管活跃请求`；总开关关闭或两个有效时长均为 `0` 时为 `0`；
- 锁：仅请求本地控制器短临界区，不跨请求竞争，不持锁执行 I/O；
- 流式有效输出仍做零分配扫描；`first_output` 首次有效输出停止响应 timer，后续有效输出不再更新 deadline 或 reset timer；`idle` 每次有效输出 reset 同一个响应 timer，成本为一次请求本地锁和一次 runtime timer reset。

## 12. 测试与验收

已覆盖：

- 热配置三个字段的边界校验、保存后快照更新、环境变量启动回退；
- 总开关关闭时不托管请求，原 HTTP Client/stream scanner 行为保留；
- 四个用户字段全零或缺失时继承全局热配置；正数覆盖、`-1` 不限制；
- 两个有效时长均为 `0` 时照常托管且不装 timer，不重新启用旧环境变量限制；
- 四个用户字段的 `-1 / 0 / 正数 / 非法值` 和流式/非流式选择；
- 响应 timer 与总时长 timer 独立、流式首次有效输出停止响应 timer、非流式重试重启；
- `stream_response_timeout_mode=idle` 时，流式首次和后续有效输出会重置响应 timer，连续静默超过 `stream_response_timeout` 会以 `response_timeout` 终止；
- `stream_response_timeout_mode=first_output` 时，保持首次有效输出后不再按静默断开的行为；
- 仅修改 `stream_response_timeout_mode` 且四个时长字段全为 `0` 时，使用指定模式和全局默认时长；
- SSE 注释/心跳/空行不停止或重置响应 timer；业务输出在 `first_output` 模式停止 timer，在 `idle` 模式重置 timer；
- Midjourney 超时 `code` 保持整数；
- HTTP 首字节 trace 只捕获请求本地 marker，不捕获可能被 Gin 池复用的 Context；
- 托管的 Midjourney 请求只服从统一请求超时，关闭总开关后才保留原 30/60 秒本地上限；
- 到期后的业务写入被拒绝并留下拒写状态；即使后续增加 writer 包装层，标准化 OpenAI、Task 和 Midjourney 超时响应仍可写出；
- relay 超时继续记录现有错误日志但不触发渠道自动禁用；功能关闭及非超时错误保持原渠道处理路径；
- ping 写入失败与托管超时竞态时，流结束原因保持为 `timeout`；
- 未托管 HTTP Client 继续保留 `RELAY_TIMEOUT`，覆盖 AWS 等沿用共享客户端的旧调用链；
- Xunfei 请求取消后读协程退出，不访问 Gin context；
- Redis 缓存缺失新字段时按 `0` 命中，不触发 schema 冷启动回源；
- 普通、Gemini、任务和视频路由组只增加一层快照中间件；提交 controller 显式启动超时，fetch、回调和 Realtime controller 不启动、不包装 writer；
- `go test ./...`、`go vet ./...`、`go build ./...`；
- `cd relaykit && GOWORK=off go build ./...`；
- `cd web && bun run typecheck`、`bun run build`。

2026-08-07 验证结果：上述 Go 全量测试、vet、build、relaykit 独立 build、前端 typecheck/build 均通过。前端全仓 lint 仍有存量错误；本次修改文件的定向 lint 仅命中 `system-tuning/section-registry.tsx` 原有的 Fast Refresh 导出规则错误，本次新增组件及输入逻辑没有新增 lint 报错。

2026-08-13 追加验证结果：新增 `stream_response_timeout_mode` 后，`go test ./service ./middleware ./controller`、`cd web && bun run typecheck`、`cd web && bun run build` 均通过；`cd web && bun run i18n:sync` 后所有 locale missing/extras 均为 0。`go test ./...` 仍因共享 MySQL 测试库中既有 `users.idx_users_aff_code` 空值重复问题失败，失败点位于 `model/user_session_test.go` 与 `relay/helper/extra_coverage_test.go`，与本次流式模式逻辑无关。

2026-08-13 follow-up verification: expanded coverage for Redis user-cache round trip of `stream_response_timeout_mode`, context defaults for legacy cache entries, controller normalization of whitespace/case, request-start mode normalization for dirty cache values, idle-mode heartbeat handling, idle-mode total-timeout precedence, and local upstream runtime behavior. Fixed the user drawer select trigger so it renders translated mode labels instead of raw `idle` / `first_output` values. `go test ./model -run "TestUser_ToBaseUserAndAccessors|TestUserBase_WriteContext|TestUserCache_RedisRoundTrip"`, `go test ./service ./middleware ./controller`, `go build ./...`, `cd web && bun run typecheck`, `cd web && bun run i18n:sync`, and `cd web && bun run build` passed. `go test ./...` still fails only on the existing shared MySQL duplicate empty `users.idx_users_aff_code` fixtures in `model/user_session_test.go` and `relay/helper/extra_coverage_test.go`.

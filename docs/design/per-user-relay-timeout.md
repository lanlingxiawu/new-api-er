# 单用户 AI 请求双超时

状态：实现与回归验证完成  
日期：2026-08-06

## 1. 目标与范围

每个 AI 请求同时支持两个限制，并按流式、非流式分别配置：

| 用户字段 | 语义 |
|---|---|
| `stream_response_timeout` | 流式首次有效输出及后续连续无有效输出超时 |
| `stream_total_timeout` | 流式请求绝对总时长 |
| `non_stream_response_timeout` | 非流式上游首次响应超时 |
| `non_stream_total_timeout` | 非流式请求绝对总时长 |

同一请求同时启动对应模式的响应计时器和总时长计时器，任一先到期都会取消上游请求。计费、平台实际成本、提成和消费日志继续沿用现有逻辑，不由本功能修改。

本次覆盖普通 relay、Gemini 原生路由、异步任务提交路由及视频任务提交路由。异步任务提交后的查询、回调和后台执行生命周期不受提交超时控制。客户端 Realtime/WebSocket 长连接不在范围内；提供商内部使用 WebSocket 的普通 relay 请求仍继承请求超时。

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

启动顺序为：代码默认值 → 环境变量回退 → DB 已保存配置。`RELAY_IDLE_CONN_TIMEOUT` 只控制连接池空闲连接，不参与请求超时。若 DB 没有对应配置，响应默认取 `STREAMING_TIMEOUT`，总时长默认取 `RELAY_TIMEOUT`；这些默认值只用于已经配置了至少一个非零用户覆盖的用户。

每个用户字段的取值规则：

- 四个字段全部为 `0`：该用户不启用新功能，完整沿用原 `STREAMING_TIMEOUT`、`RELAY_TIMEOUT`、HTTP Client 和流扫描逻辑；
- 任一字段非 `0` 后，其余值为 `0` 的字段继承当前热更新全局默认值；
- `-1`：明确关闭该用户的对应限制；
- `1..604800`：使用用户值，单位秒。

用户值是真正覆盖，不和全局值取最小值。总开关优先级最高；关闭时所有用户值均不生效。

**接管前提**：托管一个请求会同时关掉它的旧保护——流扫描器不再创建 `STREAMING_TIMEOUT` ticker，共享 HTTP Client 的 `RELAY_TIMEOUT` 也被剥离。因此只有在当前模式确实有东西可执行时才接管：解析出的响应超时和总时长同时为 `0` 时，除非该模式的用户字段里显式出现 `-1`，否则不创建控制器、不包装 writer，完整回退旧链路。这挡住两类隐式空档：用户只配置了另一种模式；或全局默认被设为 `0` 而用户对应字段是继承的 `0`。显式 `-1` 是用户主动要求不限制，此时照常接管但不装任何 timer。

## 3. 流式与非流式语义

### 3.1 流式请求

- 响应计时器从 relay 开始运行；收到实际写给客户端的有效输出后重置，而不是停止。
- SSE 注释、网关心跳 `: PING` 和空行不重置；适配器吞掉或缓冲、没有写给客户端的上游事件也不重置。
- 总时长从 relay 开始运行且永不重置，到期会硬性终止流。
- 总时长为 `0` 或用户覆盖为 `-1` 时，持续有输出的流不会被总时长截断。

### 3.2 非流式请求

- 响应计时器从 relay 开始运行，到收到主上游 HTTP 首字节或 SDK/WebSocket 首个响应事件时停止。
- 如果该响应触发可重试错误，下一次重试开始前重新启动完整响应窗口；总时长计时器不重置。总时长不受限时，最坏等待上界为 `(RetryTimes + 1) × 响应超时`。
- 总时长覆盖响应体读取、转换、重试和 handler 返回。

## 4. 数据模型与缓存兼容

`users` 表新增四个非空整数列，默认值均为 `0`：

```go
StreamResponseTimeout    int `json:"stream_response_timeout" gorm:"type:int;not null;default:0;column:stream_response_timeout"`
StreamTotalTimeout       int `json:"stream_total_timeout" gorm:"type:int;not null;default:0;column:stream_total_timeout"`
NonStreamResponseTimeout int `json:"non_stream_response_timeout" gorm:"type:int;not null;default:0;column:non_stream_response_timeout"`
NonStreamTotalTimeout    int `json:"non_stream_total_timeout" gorm:"type:int;not null;default:0;column:non_stream_total_timeout"`
```

字段不参与筛选、排序或关联，不新增索引。沿用现有 UserBase Redis 缓存；未启用 Redis 时仍走原有 DB 读取，不新增进程内用户缓存。缺失字段按 `0` 处理。此次扩字段不提升基础缓存 schema 版本，避免滚动发布期间新旧实例互相判定缓存失效并制造同步 DB 回源。

旧实例使用逐字段 `HSET` 刷新已有 hash 时不会删除四个新字段；只有在 hash 不存在、旧实例从 DB 回源并重新创建缓存时，新字段才会暂时缺失并按 `0` 处理。全零会回退旧链路，因此未配置用户和滚动发布期间缺失新字段的缓存不会被新功能接管；只有编辑过非零覆盖的用户需要由新实例处理。

全局配置复用现有 `options` 表和 ConfigManager，不新增表、连接或独立保存接口。

## 5. 数据流

1. 用户鉴权从现有 UserBase Redis 缓存读取四个字段并写入 Gin context；缓存 miss 或未启用 Redis 时仍沿用原有 DB fallback。
2. 超时中间件只读取一次全局不可变快照，不提前包装 writer。总开关关闭则直接放行。
3. 提交类 controller 在请求解析完成并确定 `stream` 后显式启动超时；四个用户字段全部为 `0` 时直接回退旧链路；fetch、回调和 Realtime controller 不启动，因此保持原 writer 和原请求逻辑。
4. 用户任一字段非 `0` 时，从当前请求快照选择全局默认并解析对应的两个用户覆盖值。两个值都解析为 `0` 且该模式没有显式 `-1` 时同样回退旧链路（见 §2 接管前提）；否则创建请求本地控制器，管理响应 timer、总时长 timer、响应 writer 和可取消 request context。
5. 主上游请求继承该 context；流式下游有效输出重置响应 timer，非流式首响应停止响应 timer。
6. 可重试的非流式请求在下一次尝试前重启响应 timer；总时长 timer 始终不重置。
7. 任一 timer 到期取消上游并记录 `response_timeout` 或 `total_timeout`；响应包装器拒绝到期后的业务输出，并通过请求本地放行门闩允许 controller 穿过后续 writer 包装层写入标准化超时响应；handler 结束时停止 timer。

## 6. API 与界面

不新增 endpoint。四个用户字段沿用管理员用户读取/更新接口：省略保留原值；四项全 `0` 沿用旧逻辑；已启用用户的单项 `0` 继承、`-1` 关闭；非法值返回现有参数错误。

系统调优页新增“AI 请求超时”热配置区，通过现有分组配置接口保存 `relay_timeout_setting` 三个字段。权限范围为 `system-tuning.relay-timeout`，仅管理员可写。界面明确说明热更新只作用于新请求以及环境变量仅是启动回退。

## 7. 错误处理和协议兼容

- 未提交响应：返回 HTTP `504` 和现有 `relay_timeout` 错误语义。
- Midjourney 兼容响应保持 `code` 为整数 `4`，在 `description` 中表达 relay 超时，避免破坏客户端反序列化。
- 已提交流式响应：取消上游并结束流，不能改写已经发送的状态码。
- 客户端主动断开和父 context 更早取消不归类为 relay 超时。
- 超时后停止当前重试链，沿用现有错误日志记录路径；超时错误携带项目既有 `skipRetry` 属性，因此原 `ShouldDisableChannel` 逻辑自然跳过渠道故障/自动禁用，不修改 `processChannelError` 的原有判断。功能关闭时，原有渠道错误处理逻辑不变。
- 已提交响应后若仍有业务代码尝试写入，写入会返回 `context.DeadlineExceeded`，并在请求结束时记录拒写告警，使已提交响应被截断的窄窗口可被观测。
- ping 写入与超时同时发生时，结束原因保持为 `timeout`，不被 `ping_fail` 抢占。
- Xunfei 读取协程只捕获请求 context 和超时观察接口，不持有可被 Gin 复用的 `*gin.Context`；请求结束会关闭 WebSocket，所有通道发送都可被取消。

## 8. 与现有 HTTP Client 的关系

功能开启且请求已托管时，AI relay 主调用通过请求 context 实施每用户总时长，浅拷贝 client 并移除共享 `Client.Timeout`，但继续复用同一 Transport/连接池。准备性调用继承同一个可取消 context，明确设置 `-1` 即允许关闭限制。总开关关闭、用户四项全为 `0` 或非 relay 调用时，完整保留共享 Client 的旧行为。

仅非流式主上游请求安装首字节 trace；没有响应计时器、流式请求或非主调用不创建 trace/request 副本。

实现按职责集中，避免把超时算法散落到原 relay 逻辑：

- `middleware/relay_timeout.go` 独占 timer、响应 writer 和到期写入门禁；
- `service/relay_timeout.go` 集中用户覆盖解析、托管状态、请求 context 绑定、HTTP client 派生、首字节 trace 和 context 错误归一；现有 `service/http_client.go` 不承载功能分支；
- `controller/relay_timeout.go` 集中 OpenAI/Claude、Midjourney 和 Task 的协议错误转换；`controller/relay.go` 只保留提交入口启动、重试窗口重启以及结果归一调用；
- 普通 HTTP 渠道统一经过 `relay/channel/api_request.go` 的一个接入点；仅 AWS SDK、WebSocket、轮询和独立上传等绕过该入口的实现保留最小 context 接入。

## 9. Main Chain Impact

同步热路径在功能开启时新增：每请求一次原子配置快照读取；只有提交类 controller 显式启动后，才读取四个缓存整数。四项全为 `0` 时立即返回，完全沿用旧超时链路；任一项非 `0` 时，才判断流式模式并创建最多两个 Go runtime timer。响应包装器也只在该用户的新分支启动点安装，并通过原子指针一次性发布不可变的请求本地状态，不在每个分片上查询 Gin context；流式有效输出执行零分配行分类、一次请求本地锁和 timer reset。非流式首响应完成后使用原子 settled 状态跳过后续分片扫描。总开关关闭、用户四项全为 `0`，以及未调用启动点的 fetch/Realtime 请求均不包装 writer、不创建 timer。

没有请求级 DB 查询、新 Redis 往返、分布式锁或无界 goroutine。超时处理不向计费链增加同步逻辑。Xunfei 使用原有每连接读取协程，但请求取消必定关闭连接并允许协程退出。

## 10. Shared Resource Audit

| 资源 | 访问方式 | relay 是否访问 | 结论 |
|---|---|---|---|
| `users` 四个超时列 | 管理接口写；现有用户缓存装载时读 | 是 | 无新增请求级 DB 查询，无需索引 |
| 用户 Redis 缓存 | 现有 UserBase hash 增加四个整数 | 是 | 复用现有 key；缺失字段按零处理；不提升 schema；不新增进程内用户缓存 |
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
- 用户分支判断：提交请求读取 `4` 个 Gin context 缓存整数；四项全为 `0` 时立即回到旧链路；
- timer：最多 `2 / 已启用用户的活跃请求`；总开关关闭或用户四项全为 `0` 时为 `0`；
- 锁：仅请求本地控制器短临界区，不跨请求竞争，不持锁执行 I/O；
- 流式每个有效输出一次零分配扫描、deadline 更新和 timer reset。

## 12. 测试与验收

已覆盖：

- 热配置三个字段的边界校验、保存后快照更新、环境变量启动回退；
- 总开关关闭或用户四项全为 `0` 时不托管请求，原 HTTP Client/stream scanner 行为保留；
- 四个用户字段任一非 `0` 时只为该用户启用新分支，其余 `0` 字段继承全局热配置；
- 覆盖只作用于另一种模式、或全局默认为 `0` 导致当前模式两个值都解析为 `0` 时不托管，`RELAY_TIMEOUT` 与 `STREAMING_TIMEOUT` 保持生效；同样场景下用户显式填 `-1` 则照常托管且不装 timer；
- 四个用户字段的 `-1 / 0 / 正数 / 非法值` 和流式/非流式选择；
- 响应 timer 与总时长 timer 独立、流式重置、非流式重试重启；
- SSE 注释/心跳/空行不重置，业务输出重置；
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

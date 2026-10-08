# 非流式超时的上游成本回收

状态：实现与回归验证完成
日期：2026-09-22

## 1. 问题

非流式请求触发用户超时后，我们向用户全额退款，但上游的 token 已经产生，费用照收。这是单边亏损。

现状链路：

1. `middleware/relay_timeout.go:88` 定时器到期 → `cancel(errRelayResponseTimeout)` 取消 request context；
2. `service/relay_timeout.go` `BindRelayRequestContext` 让我方时限（且只有我方时限）取消上游调用，超时即断开上游连接（客户端断开不取消上游调用，见第 23 节）；
3. `controller/relay_timeout.go:16` `normalizeRelayTimeoutError` 把结果改写成 504；
4. `controller/relay.go:203` 的 defer 里 `relayInfo.Billing.Refund(c)` 全额退还预扣额度，不走结算，不产生消费日志。

**流式不存在这个问题。** `service/stream_lifecycle.go:119` 在超时时置 `reason = StreamEndReasonTimeout`、`clientGone = false`，随后按已接收到的 usage 走 `PostTextConsumeQuota` 正常结算。

**亏损可以精确归因到具体用户。** 按 [单用户 AI 请求双超时](per-user-relay-timeout.md) 第 2 节，四个时长字段全为 `0` 的用户根本不进这套逻辑，全局默认值只对已主动配置过至少一个字段的用户生效。因此每一笔超时亏损背后都是一个显式要了短超时的用户。

**根因在于非流式的计量盲区。** `non_stream_response_timeout` 在上游 HTTP 首字节**之前**到期（`service/relay_timeout.go:183` `RelayResponseTraceContext` 在非流式下标记首字节）。而非流式的首字节等价于上游已生成完毕——超时那一刻我们对输出用量是零信息，输入 token 能实算，输出只能拍脑袋。单纯"超时不退款"会变成按估算收费，用户没拿到内容、扣的钱还是估的，是最容易引发投诉的形态。

## 2. 目标与范围

给用户一个可选的超时计费策略：**你要"超过 N 秒就掐掉"，掐掉产生的上游成本由你承担**；而为了让这笔钱收得有据可依，先把非流式请求的上游交互改成流式，使超时时手里握有真实的部分用量。

范围内：

- 新增单用户字段 `non_stream_timeout_billing`，取值 `refund`（默认，即现状）/ `charge`；另有第三档 `input`（只收输入），未转流的 charge 请求超时同样只收输入，见 [relay-timeout-cost-bearing.md](relay-timeout-cost-bearing.md)；
- 该用户取 `charge` 时，其非流式请求对上游改发 `stream: true`，网关聚合后仍以非流式 JSON 一次性返回；
- 超时时按已接收到的真实用量结算，复用现有流式结算链路；
- 超时前已收到输出（正文、推理内容、拒答或工具调用）时，把这部分作为正常的非流式结果返回（HTTP 200，`finish_reason: "length"`）：用户为它付了钱，就应拿到它。一点输出都没有时返回 504。

范围外：

- 全局开关。计费策略与超时字段同级，按用户配置，不做系统级开关。
- 流式请求。已能正确结算，不改。
- 上游账单对账。我们断开后上游是否继续生成并计费不可观测，这部分差额本方案不覆盖，见第 10 节。
- 一期只覆盖 `RelayFormatOpenAI` 的 chat completions。其余格式（Claude Messages、Gemini、Responses、图片/音频/任务类）保持现状走 `refund`。

## 3. 为什么是"转流"而不是"后台续读"

另一条候选路线是超时后只断下游、上游交给后台 worker 读完再结算。它能拿到完整用量、还能把结果留给用户取回，但需要解决三件事：结算不能再碰 `gin.Context`（响应返回后被 Gin 池化复用，必须先快照结算字段）、后台 worker 必须有界（Rule 8）、上游连接释放推迟带来的连接池压力。

转流路线的代价小得多，`relay/channel/openai/chat_via_responses.go:80` 的 `OaiResponsesToChatBufferedStreamHandler` 已经是同形态的先例。代价集中在一处新代码：chat completions chunk 的聚合器。

**关于复用流式结算链路（实现修正）：** 设计阶段设想直接复用 `FinalizeStreamUsage` + `PostTextConsumeQuota`，不新写计费逻辑。实现时发现这条路走不通——`RelayInfo.UseStreamErrors()`（`relay/common/relay_info.go:237`）用 `clientStreamMode` 在请求入口就把下游模式**冻结**为非流，而 `BeginStreamAttempt` 在 `controller/relay.go` 里早于 `TextHelper` 执行。也就是说转流发生时流式接管（`StreamSession`）已经确定不会启用，`FinalizeStreamUsage` 会直接返回。

因此实现采用与 `OaiResponsesToChatBufferedStreamHandler` 相同的形态：独立的 buffered handler 自己扫描 SSE、自己选定 usage、自己调 `PostTextConsumeQuota`。好处是请求在整条链路上保持"非流式"身份，不会沾上流式的副作用（SSE 错误帧、`is_stream` 标记、终止错误写出）；代价是超时结算多了约 20 行代码。这个取舍是实现期做的，不是设计期的遗漏。

## 4. 超时语义必须保持不变（关键约束）

转流是内部实现手段，对用户必须透明。这里有一个会导致**功能退化**的陷阱：

`middleware/relay_timeout.go:104` `MarkResponse` 在收到上游首次响应时停掉 response timer。非流式下"首字节"等价于"生成完毕"，所以 `non_stream_response_timeout` 实际保护的是整个生成时长。转流之后首个 SSE chunk 几秒就到，若照常停表，用户配的 60 秒保护形同虚设，请求可能跑十分钟。

**结论：转流模式下 response timer 不停表。** `MarkResponse` 对内部转流的非流式请求返回 `false`，计时器一直运行到聚合器拿到完整响应（`[DONE]` 或终止性 `finish_reason`）为止。这样用户配 60 秒仍然是"60 秒内没有完整结果就掐"，与现状逐字一致。

实现方式：`relayTimeoutControl` 上新增 `responseHeld atomic.Bool`（`middleware/relay_timeout.go`），`MarkResponse` 一进门就检查它。停表有三条路径——httptrace 的 `GotFirstResponseByte`、下游写入器、SDK 渠道的 `responseMarker`——全部汇流到 `MarkResponse`，所以一处标志即可全部挡住。`service.HoldRelayResponseTimer(c)` 通过接口断言设置它，避免 `relay` 包依赖 `middleware`（与既有的 `WriteRelayTerminalError` 同一模式）。`RestartResponse`（重试路径）会解除 hold：hold 属于单次尝试而不是整个请求，下一次尝试可能落在不转流的渠道上，其首字节应照常停表；若再次转流，由该次尝试重新设置 hold。

同理，`StartRelayRequestTimeout(c, isStream)`（`controller/relay.go:143`）的 `isStream` 参数必须继续传下游的真实模式（`false`），保证读取的是 `non_stream_*` 字段而不是 `stream_*` 字段。转流不得改变 `relayInfo.IsStream` 对超时子系统的取值。

## 5. 数据流

```
客户端非流请求
  → 解析、鉴权、预扣（不变）
  → StartRelayRequestTimeout(c, isStream=false)   // 仍读 non_stream_* 字段
  → [新] shouldUpstreamStream(c, info) 判定
      ├─ false → 原非流式路径（完全不变）
      └─ true  → request.Stream = true
                 request.StreamOptions = {include_usage: true}
                 BeginStreamAttempt（启用流式证据采集）
                 c.Writer 换成聚合写入器
                 → 上游 SSE
                 → 现有流式 handler 逐事件解析、累计 usage 证据
                 → 聚合写入器拦截下游输出，内存聚合，不写客户端
  → 正常完成：聚合成 OpenAITextResponse，一次性写出（Content-Type: application/json）
              按上游 include_usage 的真实 usage 结算
  → 超时：    按已接收证据结算 → PostTextConsumeQuota，不退款
              已有输出 → 组装已收内容，HTTP 200，finish_reason "length"
              无输出   → 504 relay_timeout
```

判定函数 `shouldUpstreamStream` 的全部条件（任一不满足即回退原路径）：

1. 下游是非流式请求（`!info.IsStream`）；
2. 该用户 `non_stream_timeout_billing == "charge"`；
3. 该请求确实会超时：有待到期的时限（`service.RelayRequestDeadline(c)`，响应时限或总时限任一）——不会超时就没有亏损风险，不值得改变上游交互。只看"被超时子系统接管"（`IsRelayTimeoutManaged`）不够：用户两项非流式时限都设为 `-1`（不限时）时控制器照样接管，但没有任何计时器。判定在每次尝试开头执行，此时 `BeginRelayAttempt` 已重启响应窗口，配置了响应时限就一定处于待到期状态；
4. `info.RelayFormat == types.RelayFormatOpenAI` 且 `info.RelayMode == RelayModeChatCompletions`；
5. 渠道在 `streamSupportedChannels`（`relay/common/relay_info.go:410`）中，即 `info.SupportStreamOptions == true`；
6. 上游本身说 OpenAI 协议（`info.ApiType == APITypeOpenAI`）：`RelayFormat` 只是客户端协议，`streamSupportedChannels` 里的 Anthropic/Gemini/AWS 等上游原生 SSE 不是 `chat.completion.chunk`，聚合器会把它们解析成空帧；
7. 未命中 Chat→Responses 策略（`service.ShouldChatCompletionsUseResponsesGlobal`）：该路径由自己的缓冲处理器应答，超时即退款，转流只会改变上游调用方式而不改变计费；
8. 请求参数与流式兼容：`n` 为空或 1、未设置 `logprobs`/`top_logprobs`、非透传模式（`PassThroughBodyEnabled` 与全局 `PassThroughRequestEnabled` 均关闭——透传模式下我们不改写请求体）；
9. 不要求音频输出（`audio` 参数未设置，`modalities` 不含 `audio`；`modalities` 无法解析时按含音频处理）：流式音频只支持 `pcm16`，要 `wav`/`mp3` 的非流式请求转流后可能直接被上游拒绝，聚合器也不组装音频增量；
10. 未使用旧版 `functions`/`function_call`：其流式增量 `delta.function_call` 不在流式 DTO 中，转流后客户端会收到 `finish_reason: function_call` 却没有函数。

条件 3 是把影响面压到最小的关键：没开 `charge` 的用户、没配超时的用户，上游交互一个字节都不变。

## 6. 数据模型变更

`model/user.go` 新增一列，与现有五个超时字段并排：

```go
NonStreamTimeoutBilling string `json:"non_stream_timeout_billing" gorm:"type:varchar(16);not null;default:'refund';column:non_stream_timeout_billing"`
```

- 取值 `refund` / `charge`，空串按 `refund` 处理（兼容历史行）。
- 迁移走 GORM `AutoMigrate` 的加列路径，三库通用，SQLite 用 `ADD COLUMN`（Rule 2）。
- 不加索引：仅按主键随用户一起读出，无查询条件（Rule 8.3）。
- 同步进用户缓存：`model/user_cache.go:33` 的 `UserCache` 加同名字段，`model/user.go:153` 的填充、`model/user_cache.go:51` 的 context 注入、`model/user_auth_cache.go:85` 的 Redis Lua HSET 参数、`model/user.go:990` 的更新列表各加一处。
- `constant/context_key.go` 加 `ContextKeyUserNonStreamTimeoutBilling`。
- `controller/user.go:700` 的可更新字段列表加一项（管理员可改；是否允许用户自助修改见第 8 节）。

## 7. 计费与结算

| 场景 | 结算依据 | 用户是否扣费 |
|---|---|---|
| 转流 + 正常完成 | 上游 `include_usage` 的真实 usage | 是（与现状一致） |
| 转流 + 超时 | `FinalizeStreamUsage` 按已接收证据 | 是（本方案新增） |
| 转流 + 上游错误 | 现有流式失败路径 | 按现有规则 |
| 未转流 + 超时 | 不变，全额 `Refund` | 否 |

转流后整条请求在计费上被现有流式链路接管：`service.BeginStreamAttempt` 建立证据采集，`FinalizeStreamUsage`（`service/stream_lifecycle.go:99`）选择用量来源，`FinalizeStreamFailure`（`service/stream_lifecycle.go:383`）调 `PostTextConsumeQuota`。超时时 `IsRelayRequestTimeout(c)` 为真，`clientGone` 被置 `false`，用量来源走 `upstream`（有确认证据）或 `estimated`（无证据），这些分支都已存在且有测试覆盖。

本方案**不新增任何计费代码**，只是让非流式请求走进这条链路。

消费日志的 `is_stream` 标记：因为流式接管不启用，`info.IsStream` 全程保持 `false`（`relay/compatible_handler.go` 里那句按 Content-Type 翻转 `IsStream` 的赋值对转流请求被跳过），日志天然记录下游真实形态，无需额外处理。`RelayInfo.UpstreamStreamAdapted` 仍然保留，用于在 `service/text_quota.go` 的 other 字段里记 `upstream_stream_adapted: true` 供排查。

超时时由 buffered handler 自己结算并写出终止响应（有输出时是已收内容组成的结果，无输出时是 504），然后置 `relaycommon.StreamHandledKey = true`。这个既有标志的语义正是"已接管终止响应及结算，供控制器跳过重复重试、退款和错误写出"，与本路径需求完全吻合——`controller/relay.go` 的退款 defer 和响应写出都会因它跳过。

## 8. 前端与 i18n

用户编辑页现有超时设置区块（与五个超时字段同处）增加一个下拉：

- 标签：`Timeout billing`
- 选项：`Refund on timeout`（默认）/ `Charge actual usage on timeout`
- 辅助说明必须从用户视角写清代价（Rule 6）：选择 `charge` 后，请求超时中断时将按上游已产生的实际用量计费，而不是全额退还。

权限：**仅管理员可修改**，普通用户在自助设置页只读展示。理由是这个字段直接决定用户是否为未交付的结果付费，属于商务约定，不应由用户自助切换。

i18n：前端 `web/src/i18n/locales/{en,zh,zh-TW,fr,ru,ja,vi}.json` 直接补键，不跑 `bun run i18n:sync`（会顺带回填无关键，污染 diff）。后端无新增用户可见字符串。

## 9. 边界与错误处理

| 情况 | 处理 |
|---|---|
| 渠道不支持流式 | 回退原非流式路径，超时仍 `refund`。这类用户的亏损本方案覆盖不到，需在管理端提示 |
| 上游对 `stream:true` 返回非 SSE | `relay/compatible_handler.go:202` 已有 `Content-Type` 判定；未返回 SSE 时按非流式处理，聚合器直通 |
| 上游不支持 `stream_options.include_usage` | 拿不到确认 usage，`FinalizeStreamUsage` 落到 `estimated` 分支（已有实现） |
| 聚合缓冲超限 | 聚合器持有 `relaycommon.MaxStreamFrameBytes`（200 MiB）的共享预算，覆盖正文、思考内容、拒答文本、引用、工具参数，以及每个新 choice / 工具调用条目（按 128 字节计）和工具调用的 id、名称。耗尽后不再累积文本、不再新建条目，但继续吸收 usage 与已有条目的元数据，并中断读取；响应按截断交付，所有 choice 的 `finish_reason` 报告 `length`（即使上游随被丢弃的内容一起发了 `stop`）。非流式响应体有自然终点，流没有，所以这个上限是必需的而非可选的 |
| 重试 | 见第 9.1 节 |
| 聚合过程中客户端断开 | 不中断读取：上游调用不绑定客户端连接（第 23 节），聚合照常读完，按上游的完整用量结算，与主分支普通非流式请求在客户端断开后读完响应并按完整用量计费一致（上游同样按完整生成向我们计费）。结果写给已断开的客户端时失败被忽略，不改变结算。我方时限在客户端断开后照旧到期，到期后按超时分支处理。读取因其他原因失败时客户端已不在：已有输出按已收到的内容结算，尚无输出按 `service.RelayContextError` 处理（不重试、不扣费）；两者 `stream_status.end_reason` 记 `client_gone` |
| 上游正常关闭但没有可用输出 | 以"上游是否表示已答完"为界（`adaptedStreamAnswered`）：收到 `[DONE]`，或某个 choice 带了 `finish_reason`，即使正文为空也按空回答返回 200 并结算——普通非流式此时同样返回空回答并计费，重试只会再买一遍同样的回答。两者都没有（只有角色帧、usage 帧，或 `event: error` 这类解码为空的帧后就关闭）则与坏帧/读错误同样处理：报 500 走重试/退款，不写 200 |
| 上游错误帧 | `data:` 帧含 `error` 字段时先解码为 `any`（与普通响应体的 `OpenAITextResponse.Error` 相同）再判断。字段不携带任何内容——`null`、`{}`、`[]`、`""`、`false`、`0`，或成员全是这类空值的对象（如 `{"code":0,"message":""}`）——视为普通内容帧照常聚合：有的上游在每个正常 chunk 上都带这样的字段，主分支流式处理器不读该字段。无 `type` 的错误对象（如 `{"code":200}`、`{"message":"success"}`）出现在携带输出的帧上——某个 choice 的 delta 有非空成员（角色、文本、工具调用等）或带 `finish_reason`，或帧带有非空成员的 `usage`（给每个 chunk 都盖这种字段的上游，末尾 usage 帧没有 choices）——同样视为内容：普通路径只在错误带 `type` 时失败（`relay-openai.go` 的 `oaiError.Type != ""`）。带 `type` 的对象即使与输出同帧也是错误。只有错误、不带输出的帧：带 `type` 的对象、无 `type` 但有非空字符串 `message` 的对象（type 记为 `upstream_error`）、非空字符串，按上游错误呈现（type/code/message），渠道自动禁用的关键词匹配读得到上游原文，客户端可见内容仍受错误遮蔽规则约束。其余形态（非零数字、`true`、非空数组、既无 type 也无 message 的对象）报通用的 `upstream stream error`，原始帧只进服务端日志。状态码 500，走重试 |
| 无 `index` 的工具调用增量 | 默认续接最近一个调用（单调用分片发送的上游如此，最近调用带不带 index 都一样）。以下情况为新调用：增量与最近调用都有 id 且不同；否则（任一方无 id）增量带了函数名，且最近调用已有不同的函数名；或同名（同一函数的第二个调用，无 index 也无 id）且满足其一——最近调用的 arguments 已构成一个完整的 JSON 对象/数组，且增量带来非空白 arguments、调用头（`type`）或最近调用没有的 id；最近调用的 arguments 仍为空白，且增量是 arguments 同样空白的调用头（两个空参数调用；带来参数的调用头续接，因为每个分片都重发调用头的上游会以空参数开头）。不带函数名的增量（且无可比较的 id）续接；只重复函数名（无 type、无新 id、arguments 空白）的增量续接，即使参数已闭合（有的上游在后续分片上重复名称）；参数未闭合或非法时一律续接。已知限制：无 type/id/index 的两个同名空参数调用与重复名称无法区分，合为一个；空参数调用之后紧接带参数的同名调用头会被续接（前一个调用丢失）；参数完整或空白后重发的空参数调用头视为新调用；完整性只认对象/数组（按接口约定函数参数是 JSON 对象），参数为裸标量（如 `1`）时其后的同名调用不会拆出。新调用的聚合键取 -1、-2……（独立计数器），上游 index 被钳到 ≥0，因此无论带 index 的调用先到还是后到都不会与之落到同一槽位；输出顺序为到达顺序。同一帧内的多个无 index 调用同样拆开，各 choice 独立判断。arguments 是否完整由写入时逐字节维护的结构状态（字符串/转义/括号深度，`jsonValueTracker`）判断，闭合后首次询问时 `gjson.Valid` 校验一次并缓存，因此每个增量的判断 O(1)，不会随已累积参数长度重复扫描 |
| 渠道开启 ForceFormat | 与 `OpenaiHandler` 一致：普通路径此时把响应经 `dto.OpenAITextResponse` 重新编码，只保留 DTO 建模的字段。重组响应因此不补回 `system_fingerprint`、`service_tier`、`prompt_filter_results`、choice 上的 `content_filter_results`、`refusal`、`annotations` 与上游原样 usage（usage 为实际计费值经 DTO 编码）；只有工具调用/拒答的回合仍输出 `content: null`（`dto.Message.Content` 保留上游的 null） |
| 用户改了字段但请求在途 | 在途请求用启动时读到的值，不半程变更 |

超时的响应（`relay/upstream_stream_buffered.go`）：

- **已有输出**（`chatStreamAggregator.HasOutput`：正文、推理内容、拒答或工具调用任一非空；只有角色或只有 usage 的帧不算）：把已收内容组装成普通 `chat.completion`，HTTP 200，`usage` 为实际结算的用量。被截断的 choice 的 `finish_reason` 为 `"length"`（标准的"输出不完整"信号，客户端 SDK 都认识；不用自造的 `"timeout"`，以免严格校验枚举的 SDK 报错）；上游已发完 `[DONE]`、只是停表前一刻到期的，保留上游自己的结束原因。超时后超时控制器会拦住普通写入，这份结果作为本请求唯一的终止内容，经 `service.WriteRelayTerminalError` 与超时错误走同一道闸门写出。
- **没有输出**：504 + `ErrorCodeRelayTimeout`，与未转流时一致，带我方请求 ID；计费至少按输入（收到用量帧时取上游确认的输入，否则估算输入，输出 0），不低于 input 档，见 [relay-timeout-cost-bearing.md](relay-timeout-cost-bearing.md) §3.2。
- 两种情况都结算一次（有输出按已收用量，无输出至少按输入）、不退款、不重试；控制器仍按 `reportAdaptedRelayTimeout` 为该渠道记一条超时错误日志（管理员排查渠道慢，普通用户在日志里会同时看到这条错误和对应的消费记录）。

## 9.1 重试路径

`relayInfo` 在 `controller/relay.go` 的整个重试循环里**复用**，而每次尝试可能落到不同渠道——上一次支持流式、这一次不支持是完全正常的。因此本功能引入的两处状态都必须是**每次尝试**的属性，不是每次请求的：

| 状态 | 归零位置 | 不归零会怎样 |
|---|---|---|
| `RelayInfo.UpstreamStreamAdapted` | `relay.TextHelper` 开头（只有它会置位，转流请求的每次尝试都经过它；放在这里而不是控制器重试循环，少改一处与上游共用的文件） | 残留的 `true` 会让响应侧按"上游是 SSE"处理一个**并没有改写过 `stream` 参数**的请求：跳过 `IsStream` 翻转，并在上游碰巧返回 SSE 时错误地走进 buffered handler（且 `include_usage` 未开，用量只能估算） |
| `relayTimeoutControl.responseHeld` | `RestartResponse()`，即重试重启响应窗口时 | 重试落到不转流的渠道后，该尝试的首字节仍不停表，`non_stream_response_timeout` 从"首次响应时限"变成"完整响应时限"。方向上更严格而非更松，但确实偏离了用户配置的语义 |

新尝试若再次转流，`applyUpstreamStreamAdaptation` 会重新 hold，所以"清除后重新决定"不会削弱保护。

其余重试相关行为：

- **超时不重试。** 超时错误带 `ErrOptionWithSkipRetry()`，不会产生双倍上游成本；`processChannelError` 里 `IsSkipRetryError` 也使它不触发渠道自动禁用。
- **超时发生在第 N 次尝试**时，`StreamHandledKey` 使控制器跳过退款与响应重写，结算由 buffered handler 完成，`PostTextConsumeQuota` 内部核销 `FinalPreConsumedQuota`。前几次失败尝试不会重复退款——退款 defer 在整个函数只执行一次。
- **转流尝试收到部分内容后遇到可重试错误**（非超时）仍然全额退款，不结算。这是刻意的：用户开启的字段语义是"承担超时的成本"，不是"承担上游故障的成本"。代价是这条路上的上游成本仍由平台吸收，与本功能之前的行为一致。
- **`ReceivedResponseCount`** 由控制器每次尝试归零（既有逻辑，注释说明了它是零响应超额扣费的守卫）。buffered handler 对它的自增因此不会跨尝试累积；超时结算走 `ResponseText2Usage` 不读该计数；没有输出的超时（含零 chunk）由 `service.AdaptedTimeoutWithoutOutput` 补为输入用量（relay-timeout-cost-bearing.md §15）。

## 10. 费用准确性的边界

本方案做到的是"按我们能观测到的真实用量收"，不是"与上游账单对账"。仍然存在的差额：

- 我们断开后上游若继续生成完毕并计费，超出部分不可观测；
- 上游只在流末尾报告 usage 而我们在中途超时时，落到本地估算，与上游实际计量有偏差。

这与 [用户断开后的确认用量与本地估算](client-disconnect-estimation.md) 的"费用准确性"一节是同一个已知边界，本方案不引入独立成本模型。

## 11. Main Chain Impact

**同步执行（relay goroutine 上）：** `shouldUpstreamStream` 判定，内存字段比较加一次读取超时控制器截止时间（该控制器是请求私有的，其互斥锁只在本请求内被计时器回调争用），无 DB/Redis/全局锁；请求体上两个字段的改写；聚合写入器的内存追加，与现有 `StreamWriter`（`relay/common/stream_writer.go:49`）同一量级。

**异步/不变：** 计费结算、消费日志、成本流水沿用现有队列，调用次数不增加。

**对未开启该字段的用户零影响。** 判定的第 2、3 条把改动完全圈在"显式配置了短超时且管理员开了 charge"的用户内。其余请求的上游请求体、handler 选择、writer 包装全部与现状逐字节一致。

**回滚：** 把用户字段改回 `refund` 即刻生效（下一个请求读新值），无需重启或发版。按 [修复不加开关] 的约定这是新增功能而非缺陷修复，配置化是合规的。

**30k → 100k RPM：** 该路径下上游连接持有时长从"一次性响应"变为"整段流"，与现有流式请求的连接特征相同，不引入新的连接形态。由于只对开启的用户生效，连接池增量等于这批用户的请求量，容量评估按该用户群的 RPM 单独核算即可。

## 12. Shared Resource Audit

| 资源 | 本方案访问 | 主链是否同资源 | 结论 |
|---|---|---|---|
| `users` 表 | 加一列，随用户行一起读写 | 是（relay 读用户缓存） | 不新增查询，走现有 `GetUserCache` 缓存；无新索引、无额外扫描 |
| 用户 Redis 缓存 | 现有 Hash 加一个 field（`model/user_auth_cache.go:85`） | 是 | 同 key 同 Lua 脚本，不新增 key、不新增往返 |
| 进程内配置快照 | 不新增 | — | 不涉及 `setting/` 新配置 |
| 流式证据结构 | 复用 `StreamSession`，请求局部 | 是 | 已有同步保护，无新共享结构 |
| 连接池 | 转流请求持有连接更久 | 是 | 见第 11 节，按开启用户的 RPM 单独核算 |
| goroutine | 不新增 | — | 无新 worker，无 `go func()` |

无新 Redis key、无新表、无新锁、无新池。

## 13. Concurrency Analysis

每请求增量：DB 调用 0，Redis 往返 0，锁 0，goroutine 0。

CPU 是**净增加**，且与响应长度成正比：普通非流式只解析一个 JSON，转流要逐 token 解析 N 个 SSE 帧。实测（本机 16 核，mock 上游，网关进程每请求 CPU 时间，3 轮交替 A/B 取中位数）：

| 输出长度 | 普通非流式 | 转流 |
|---|---:|---:|
| 100 帧 | 约 1.25 ms | 约 1.98 ms |
| 30 帧 | — | 约 1.69 ms |

即 100 帧的转流请求比普通非流式多约 0.65–0.8 ms CPU。容量评估按开启 `charge` 用户的 RPM × 平均输出帧数单独核算。

逐帧解码不走反射（`relay/upstream_stream_decode.go`）。性能动因：逐帧 `common.UnmarshalJsonStr` 进 DTO 曾是转流开销的最大头（profile 中 handler 自身 CPU 的 59%，每帧约 3.1 μs / 18 次分配），改为 gjson 单遍遍历后每帧约 1 μs / 4 次分配，100 帧请求的转流额外开销下降约 40%，其中一部分来自分配量下降（183 KB → 61 KB/请求）带来的 GC 减负。行为与原 Unmarshal 逐字段一致——由差分测试和两个 fuzz 目标锁定，不要为省事退回 Unmarshal，也不要在不跑差分 fuzz 的情况下改解码规则。唯一有意的例外是 `created`（第 18 节）：非流式 DTO 把它定义为 `any`，所以这里也宽松读取，差分测试对该字段只比语法。scanner 的 64 KiB 初始缓冲同样经 `sync.Pool` 复用，原本它是短响应最大的一笔分配。

内存：多一份聚合缓冲，量级等于原非流式响应体，上限 `relaycommon.MaxStreamFrameBytes`。

## 14. 测试设计（先于实现）

按 Rule 15.2，实现前先写这些用例。

**`shouldUpstreamStream` 判定（决策覆盖 + 条件覆盖）**

六个条件各自独立取反一次，确认都能拒绝；全满足时通过。边界：`n=nil`/`n=1`/`n=2`；`non_stream_timeout_billing` 取 `""`/`refund`/`charge`/非法值；未接管、接管但无待到期时限（`-1`/`-1`）、有时限三路；渠道在/不在白名单；透传开/关。

**超时语义不退化（本方案最关键的回归）**

- 转流请求收到首个 SSE chunk 后，response timer **不得**停表——断言 `MarkResponse` 返回 `false` 且计时器仍在跑；
- 转流请求在 `non_stream_response_timeout` 秒后仍未拿到完整响应 → 超时触发；
- 对照组：未转流的非流式请求，首字节仍正常停表（现有行为不变）；
- `StartRelayRequestTimeout` 在转流请求上读取的是 `non_stream_*` 而非 `stream_*` 字段。

**计费（等价类划分）**

- 转流 + 正常完成 + 有 `include_usage` → 按上游真实 usage 结算，金额与不转流时一致；
- 转流 + 超时 + 已有输出 → 按已接收证据结算，**不退款**，产生消费日志；已收内容以 HTTP 200 返回，截断处 `finish_reason: "length"`（正文、仅推理、工具调用三种输出；上游已完成时保留原结束原因；结果经超时写入闸门写出）；
- 转流 + 超时 + 无输出（零 chunk、只有角色帧或 usage 帧、只有注释行）→ 结算已收用量，返回 504；
- 未转流 + 超时 → 仍全额 `Refund`，无消费日志（现状回归）；
- 流式请求 + 超时 → 现状回归，不受影响。

**聚合器（`httptest.NewServer` 提供 SSE fixture，不碰真实上游）**

多 chunk 文本拼接；tool_calls 分片按 index 合并；`finish_reason` 落位；`usage` 从末尾 chunk 提取；空 chunk / 注释行 / `[DONE]` 处理；上游中途返回错误事件；上游返回非 SSE 时直通；超出大小预算时截断并按上游异常处理。

**响应形态**

转流请求的客户端响应与未转流时**逐字段等价**（除 `id` 等天然随机字段）：`Content-Type: application/json`、无 SSE 帧、`choices[0].message.content` 完整。

**日志**

转流请求的消费日志 `is_stream` 记为 `false`，other 含 `upstream_stream_adapted: true`。

**模型层**

新列在 MySQL / PostgreSQL / SQLite 三库的迁移与读写；用户缓存与 Redis Lua 的往返保真（参照 `model/user_cache_test.go:63` 的现有模式）。

数据库测试用项目真实 env（Rule 15.5），SQLite 仅作无 DB 环境的回退。

## 15. 实现顺序

1. 模型层：用户字段 + 缓存 + context key + 迁移，配套测试；
2. 判定函数 `shouldUpstreamStream` + 单测（此时还不接线，行为零变化）；
3. 超时语义：`MarkResponse` 的转流分支 + 回归测试（这一步独立可验证）；
4. chat completions 聚合器 + fixture 测试；
5. 接线 `relay/compatible_handler.go`，端到端测试；
6. 日志标记；
7. 前端字段 + 七语言 i18n；
8. `go test ./...` 全绿（Rule 15.8）。

前三步互不依赖且各自可验证，任一步出问题都不影响已合入的部分。

## 16. 实现与验证（2026-09-22）

落地文件：

| 文件 | 内容 |
|---|---|
| `model/user.go`、`model/user_cache.go`、`model/user_auth_cache.go` | 新列 + 缓存字段 + Redis Lua 参数；`userCacheSchemaVersion` 2 → 3，使旧 hash 失效而不是把缺失字段解码成空串 |
| `constant/context_key.go` | `ContextKeyUserNonStreamTimeoutBilling` |
| `service/relay_timeout.go` | `NormalizeNonStreamTimeoutBilling` / `ValidateNonStreamTimeoutBilling` / `NonStreamTimeoutChargesUsage` / `HoldRelayResponseTimer`；`RelayRequestTimeoutSeconds` 从 middleware 下沉至此（middleware 改为委托），避免 relay 依赖 middleware |
| `middleware/relay_timeout.go` | `responseHeld` 标志与 `HoldRelayResponse()` |
| `relay/upstream_stream_adapt.go` | 判定与请求改写 |
| `relay/upstream_stream_aggregate.go` | chunk → chat.completion 聚合器，含预算 |
| `relay/upstream_stream_buffered.go` | buffered handler：扫描、选用量、超时结算、写出；scanner 初始缓冲池。scanner 在此处自建而不经 `helper.NewStreamScanner`（那个构造函数自行分配缓冲，且是上游代码），行缓冲上限由 `adaptedScanMaxBytes` 按同一规则计算，`TestAdaptedScanMaxBytesMatchesHelper` 锁定两边一致 |
| `relay/upstream_stream_decode.go` | 无反射的逐帧解码器，行为与 Unmarshal 进 DTO 一致（见第 13 节） |
| `relay/compatible_handler.go` | 接线三处：请求改写、阻止 `IsStream` 翻转、分流到 buffered handler |
| `controller/user.go` | 管理员可更新字段 + 校验 |
| `service/text_quota.go` | `other.upstream_stream_adapted` |
| `web/src/features/users/**`、`web/src/i18n/locales/*.json` | 下拉字段 + 七语言翻译 |

测试：`relay/upstream_stream_adapt_test.go`（判定，十一个条件各自取反 + n 的边界 + nil + 跨尝试不残留）、`middleware/relay_timeout_hold_test.go` 里的 `TestRelayTimeoutRetryClearsHold` / `TestRelayTimeoutRetryCanReapplyHold`（第 9.1 节的两个不变量）、`relay/upstream_stream_aggregate_test.go`（聚合、工具分片、双 reasoning 字段、usage 择取、预算）、`relay/upstream_stream_buffered_test.go`（SSE 夹具端到端、响应形态、超时结算而非退款）、`middleware/relay_timeout_hold_test.go` 的其余用例（停表抑制的三条路径 + 未抑制时的现状回归）、`model/user_timeout_billing_test.go`（三库列读写、两条更新路径、Redis 往返）。

解码器测试（`relay/upstream_stream_decode_test.go`）：合法/非法语料与参考 Unmarshal 逐字段比对、chunk 复用不串帧、交给聚合器的指针在复用后仍有效、整段聚合输出逐字节一致；`FuzzDecodeStreamChunkValues`（任意字段值经真实 Marshal 编码）与 `FuzzDecodeStreamChunkRaw`（任意字节，接受/拒绝与解码值都须一致，仅豁免已声明的键大小写折叠差异）。原始无效 UTF-8 的帧回退到参考 Unmarshal，因为 encoding/json 会逐字节替换为 U+FFFD 而 gjson 保留原字节。

`handleAdaptedUpstreamStream` 的结算调用通过包级变量 `settleAdaptedTimeout` 注入，因为 `relay` 包没有数据库夹具；生产路径仍直接指向 `service.PostTextConsumeQuota`。

验证结果：`go build ./...`、新增测试全绿、`bun run typecheck`、`bun run lint` 通过。`go test ./...` 有 6 个失败，但在干净的 `HEAD` 上用独立 worktree 跑出**完全相同的 6 个**，均为预存在问题（`user_session_test.go` 系列撞 `users.idx_users_aff_code` 唯一索引——正是 CLAUDE.md Rule 6 里说明的、本仓不采纳 `truncateTables` 导致的上游测试水土不服；其余三个与本功能无关）。

未覆盖：`-race` 需要 cgo/gcc，本机不可用，`responseHeld` 的并发验证缺失。它是 `atomic.Bool`，与同结构里既有的 `allowExpiredWrite` / `lateWriteRejected` 同一模式，但这不等同于跑过竞态检测。

## 17. 外部审查修复（2026-09-24）

转流路径用聚合器**重建**响应，而普通非流式路径是把上游字节原样转发——聚合器没建模的字段就会丢失；它还绕开了普通处理器里的计费动作。外部审查报了 3 项，自查同类又发现 3 项，均先写测试复现（在修复前的代码上失败）再修。

| # | 问题 | 修复 |
|---|---|---|
| 1 | 音频输出请求被转流：响应无 `audio` 仍计费，且流式只支持 `pcm16`，`wav`/`mp3` 请求转流后可能被上游直接拒绝 | 准入排除（第 5 节条件 9） |
| 2 | 按次计价的工具调用漏收：本路径不经过普通处理器，从未调用 `CountBillableToolCall` | 成功与超时结算前都按聚合出的调用计数，每个调用计一次（与流式处理器的去重语义一致） |
| 3 | Chat→Responses 路径被转流：由该路径的旧处理器应答，超时仍退款，`charge` 不兑现 | 准入排除（条件 7） |
| 4 | 旧版 `functions` 的 `function_call` 丢失：客户端收到 200、`finish_reason: function_call` 却没有函数 | 准入排除（条件 10） |
| 5 | `refusal`（结构化输出拒答）与 `annotations`（搜索引用）丢失 | 从原始帧中提取并在写出时补回，见下 |
| 6 | 丢 `system_fingerprint`/`service_tier`；`usage` 带出内部字段（`claude_cache_creation_*`、`input_tokens_details: null`） | 同上；上游报了 usage 时原样输出上游的 usage 对象 |

**第 5、6 项的做法**：这些字段不在 `relaykit` 的流式 DTO 与 `dto.Message` 中，而 `relaykit` 是上游模块，不为此改它。解码器也不动——它与 `Unmarshal` 进 DTO 逐字段等价，由差分测试与 fuzz 锁定。改为在聚合器里另读原始帧（`AddFrameExtras`：先做子串判断，命中才用 gjson 取值），写出前再用 sjson 注入（`DecorateBody`）。只有拒答或只有工具调用、没有正文时 `content` 置 `null`，与非流式一致（Agent 框架据此识别工具调用轮次）；计入聚合预算；上游未报 usage 时仍输出估算值（即实际计费值）。

**取舍**：音频与旧版 `functions` 选择排除而非聚合。前者转流本身就可能让原本成功的请求失败；后者是已弃用接口，聚合需要改上游 DTO。拒答与引用选择补齐而非排除，因为结构化输出和搜索都常用，排除会让这些请求失去超时计费保护。

**开销**（`BenchmarkAddFrameExtras`，每帧都执行）：普通内容帧 126 ns、0 分配；上游每帧都带 `"refusal":null`/`"usage":null` 的最坏情况 390 ns、0 分配。按 100 帧计为 13–39 µs，约占一次转流请求 CPU 的 1–3%。

测试：`relay/upstream_stream_review_test.go` 共 8 个——音频（含 `modalities` 无法解析时从严）、旧版 functions、Chat→Responses（含未命中策略的对照）、字段补回（拒答跨帧拼接、引用按序累积、usage 与上游逐字段相等且不含内部字段）、`"refusal":null` 不算拒答、无上游 usage 时输出估算值、工具计次（每个调用一次而非每个分片一次、未定价函数不计）、超时时在结算前计次。其中 6 个在修复前失败；另 2 个是防矫枉过正的守卫用例，修复前后都通过。

## 18. 三次审计修复（2026-09-24）

| # | 问题 | 修复 |
|---|---|---|
| 1 | 上游拒绝流式的请求（如 OpenAI 要求组织验证才能流式调用部分模型）会直接 400，原本能成功的非流式请求因转流而失败 | `fallBackFromRejectedAdaptation`：转流请求在任何输出之前失败时，只要错误可能与流式有关（除 401/403/408/429 之外的 4xx，以及 500），去掉 `stream`/`stream_options` 在同一渠道立即重发一次普通请求（重启响应窗口，结束挂起）。失败不计费；若错误与流式无关，重发得到同样的错误，用户看到的与未转流时一致。鉴权、限流、超时与上游过载（502/503/529 等 500 以外的 5xx）与流式无关，不重发：重发不计入重试次数、单请求最大尝试次数与剩余预算，上游故障期间会让每次尝试都打上游两次，超时重发还会让等待翻倍，这些交给原有重试逻辑。重发请求体与原请求体一样由调用方持有到响应处理结束再关闭（上游可能在请求体发完前就返回，提前关闭会删掉磁盘缓存文件）。真实上游实测的动因：new-api 上游对无法访问的图片 URL，普通请求返回 400（由模型厂商拒绝），流式请求却返回 500（它在流式路径下自行下载图片计数失败），只认 400 时这个 500 被当作服务端错误重试 3 次，用户拿到与未转流时不同的状态码和上游内部错误。请求体从出站 `BodyStorage` 重读，不另持有 `[]byte`，磁盘缓存模式的内存特性不变 |
| 2 | 转流完成后响应计时器仍处于挂起状态，一路跑过组装、写出与结算；恰在这几毫秒到期会导致**已按全额结算的请求收到 504 且拿不到正文** | 正常读完（`[DONE]`/EOF/截断）后调用 `service.ReleaseRelayResponseTimer`：解除挂起并停表，相当于普通非流式请求收到响应的时刻。读流中途超时表现为读错误，仍走超时分支 |
| 3 | 已收到内容后遇到坏帧，或连接中途断开（`unexpected EOF`、连接重置、单行超长等读错误），整体返回 500 并换渠道重试——丢弃已计费的输出，上游对重试再计一次费 | 已有内容时按截断处理：交付已收内容，`finish_reason` 标 `length` 并结算（与超出预算一致），读错误另记一条 Warn（上游断开与客户端断开分别措辞）；首帧即坏帧仍报错。坏帧与读错误都以"有可用输出"（`HasOutput`：文本、推理、拒答，或有名字/参数的工具调用）为界：只收到角色帧或 usage 帧时没有可交付的内容，仍报错走重试（否则会是"200 + 空回答 + 按输入扣费"）。超时引起的读错误先进超时分支，不受影响。与主分支流式一致：不记渠道错误、不触发自动禁用；但和主分支一样把流的结束写进消费日志 `other.stream_status`（`done` 正常结束、`eof` 无 [DONE] 结束、`scanner_error` 上游断开、`client_gone` 客户端断开、`timeout` 超时、`handler_stop` 坏帧或超出聚合预算，后两者附错误条目），管理员可在使用日志里看到半截交付及原因（列表与详情都显示）。原始读错误文字（如 `connection reset by peer` 含上游地址）不写进日志，只留在服务端输出日志，用户侧只看到结束原因。已有输出后无 [DONE] 正常关闭（且没有 choice 带 `finish_reason`）的上游：转流路径返回 200、截断的 choice 标 `finish_reason: "length"`，`stream_status` 记 `eof`、状态 ok（主分支流式把干净 EOF 同样记为正常结束并补发 `[DONE]`）。本分支的受管流式路径对同一上游**不同**：协议未完成即记 `upstream_incomplete` 并下发终止错误帧（[统一流式错误处理](unified-stream-error-handling.md) 的既定合同：传输 EOF 本身不算 SSE 完成）。两边都向用户表明结果不完整——流式客户端只能靠错误帧得知，非流式客户端靠 `finish_reason: "length"`——并且都按已收到的输出计费，与主分支计费一致；差别只在各自协议里的表达方式和日志标记，是有意保留的（见第 23 节 E）。客户收到的响应只有 `finish_reason: "length"`，不加非标准字段 |
| 4 | 流式 DTO 的 `created` 为 `int64`、非流式为 `any`：把它写成浮点数的兼容上游，非流式可用，转流后**每一帧**都解码失败 | 解码器对 `created` 宽松读取：整数照常；其他数字取整；数字字符串解析；其余丢弃（快照回落到当前时间）。这是解码器相对 Unmarshal 的第三个、也是唯一可达的差异 |
| 5 | 转流请求超时由处理器自行结算并置 `StreamHandledKey`，控制器随即返回，**渠道超时不再上报**——总是卡住的渠道对这部分请求不可见，自动禁用也看不到 | `controller/relay_timeout.go` 的 `reportAdaptedRelayTimeout`：仅对转流且已超时的尝试补一次 `processChannelError`；流式等其他已接管路径维持原行为。`controller/relay.go` 只加一行调用 |

测试：`relay/upstream_stream_review_test.go`（400 等可能与流式有关的失败重发且只重发一次、鉴权/限流/超时/过载不动、重发请求体在函数返回后仍可读、未转流不动、重发仍 400 原样返回；正常结束解除计时、上游错误帧不解除；坏尾帧交付前缀、首帧坏帧报错；浮点 `created` 上游可用）、`relay/upstream_stream_decode_test.go`（`created` 宽松读取九种取值；fuzz 对非整数 `created` 只比语法，两个 fuzz 目标各跑 20 秒通过）、`middleware/relay_timeout_hold_test.go`（解除后仅总时长计时器可结束请求、到期后解除无副作用）、`controller/relay_timeout_test.go`（上报判定的五种组合）、`relay/upstream_stream_buffered_test.go`（断流前已有输出则交付、标 `length` 并结算；什么都没收到、只有角色帧、只有 usage 帧时断流仍报错）。

## 19. 真实上游全面测试（2026-09-25）

用真实上游 nexaxis.ai（本分叉的 new-api 站点）跑转流专项：同一请求由默认用户（原样透传，作基准）与 charge 用户（转流再重组）各发一次，逐字段比较响应结构，并从两个独立来源确认是否转流（我方日志 `other.upstream_stream_adapted` 与上游日志 `is_stream`），计费逐条按上游请求 ID 与上游日志对账。

**覆盖**（41 个场景）：多轮/system、中英 emoji、stop 截断（finish_reason=stop）、max_tokens 截断（length）、采样参数透传、显式 `stream:false`、`stream:false`+`stream_options`、JSON 模式、json_schema 严格输出；图片输入（base64、URL、多图+detail、Claude、Gemini、不可访问的 URL）；对话返回图片（gemini-2.5-flash-image、按次计费的 gpt-4o-image-vip）；推理模型（reasoning_effort、`max_completion_tokens`、kimi 的 `reasoning_content`、Gemini 思考）；工具调用（并行、指定函数、工具结果回传、Claude、Gemini）；Claude 提示缓存（缓存倍率 0.5，验证缓存 token 进入计费）；2500 token 长输出；不应转流的 n=2、logprobs+top_logprobs、旧版 functions、音频输出、透传渠道；其他接口（Responses、Claude 原生 `/v1/messages`、生图 `/v1/images/generations`、向量）不受影响；上游 400 回退、重试后仍转流、客户端中途放弃、首字前超时。

**发现并修复**：
1. **转流改变了错误结果**：new-api 上游在流式路径下自己下载图片计数，URL 不可达时返回 500，而普通请求由模型厂商返回 400；回退只认 400，500 被当服务端错误重试 3 次，用户拿到不同状态码和上游内部报错。回退扩展为"可能与流式有关的失败都重发一次"——4xx 与 500；502/503/529 等过载错误不重发，避免在重试预算之外对故障上游加倍请求（§18 第 1 项）。
2. **只有工具调用时 `content` 形态不对**：OpenAI 非流式为 `null`，重组输出了 `""`（Agent 框架据 `null` 识别工具调用轮次）。改为跟随上游流式帧的表示：帧里给过字符串（含 `""`）就保留 `""`（Claude/Gemini 经 new-api 转换即如此，两种模式一致），只给过 `null` 才输出 `null`。

**保留的差异（上游两种模式本身就不同，或纯表示差异）**：
- Azure 的 `routing`、`latency_checkpoint`、`obfuscation`（`prompt_filter_results` 与 choice 上的 `content_filter_results` 会带出，见第 21 节）：流式与非流式位置不同或只存在于一种模式，重组不带出（反而少暴露上游机房信息）。
- `message.refusal: null`、`choice.logprobs: null`、`message.annotations: []`、Gemini 的 `reasoning_content: ""`：空值字段缺省，JSON 语义等价，不补齐。
- `system_fingerprint`：取流式帧的值；上游流式与非流式给得不一致（`""`/`null`/缺省）时随流式。
- 上游未提供 usage 时，上游流式最后一帧自带其内部估算结构（如 gpt-4o-image-vip），原样转发；计费与上游一致。

**结果**：修复后转流专项 513/513 项检查通过（41 个场景、两侧各发）；A–E 基础/超时/重试/异常回归 208/208 通过；每个用户"余额减少 = 已用增加 = 消费日志合计"全部成立；74 个请求的请求日志均可查、详情可读。该轮测试中"客户端中途放弃"一例未向用户收费（上游计 145 个输出 token）；现行规则是客户端断开不中断上游读取、按上游完整用量结算，见第 23 节与第 9 节"聚合过程中客户端断开"。测试程序与记录见 `tmp/verify-2026-09-25/realtest-adapt/`（不入库）。

## 20. 代码审查修复（2026-09-25）

| # | 问题 | 修复 |
|---|---|---|
| 1 | 回退重发放宽到所有 4xx/5xx 后，上游过载（502/503/529）时每次尝试都会对同一渠道多发一次，且这次重发不计入重试次数、单请求最大尝试次数与剩余预算，还会重启响应窗口 | `adaptedFailureWorthPlainResend` 只对 4xx（401/403/408/429 除外）与 500 重发；其余 5xx 交给原有重试逻辑 |
| 2 | 转流路径漏了普通处理器的三步：OpenAI 类型渠道接 llama.cpp 时 `timings.cache_n` 未读取，缓存部分按全价计费；`finish_reason=content_filter` 未记管理员可见的拒绝原因；上游响应头未透传 | 记录最后一个内容帧，经 `openai.ApplyUsagePostProcessing`（导出包内的 `applyUsagePostProcessing`）补渠道特有的用量字段；与 `OpenaiHandler` 相同地记录 `openai_finish_reason=content_filter`；上游响应头除 `Content-Type` 外按 `ShouldCopyUpstreamHeader` 透传。返回给客户端的 `usage` 仍是上游原文，与普通路径一致 |
| 3 | 回退重发的请求体在函数返回时即关闭，此时响应体尚未读取；上游在请求体发完前就返回时，磁盘缓存的请求体文件会被删掉 | `fallBackFromRejectedAdaptation` 把重发请求体交给调用方，由 `TextHelper` 在处理结束时关闭，与原请求体一致 |
| 4 | 每次尝试把 `UpstreamStreamAdapted` 归零的语句在 `controller/relay.go`（与上游共用） | 移到 `TextHelper` 开头，行为不变 |
| 5 | 聚合内存上限只计文本与工具参数：新增的工具调用条目、id、名称与新 choice 都不计，上游持续发送不同 index 的空参数工具调用时内存无限增长而不触发截断 | 每个新 choice / 工具调用条目按 `aggregatedEntryBytes`（128 字节）计入预算，id、名称在变化时按长度计入；预算用尽后不再新建条目（`choiceAt` 返回 nil） |
| 6 | 最后一帧同时带超限内容与 `finish_reason: stop` 时，内容被丢弃却仍报告 `stop`，客户端无从得知结果不完整 | 预算丢弃过任何输出时，`Snapshot` 一律报告 `length`；上游原始的结束原因仍可由 `UpstreamFinishedWith` 读取，`content_filter` 的拒绝原因据此记录 |
| 7 | 上游忽略 `stream:true` 直接返回普通 JSON 时，只清了转流标记，没解除计时器挂起：普通请求靠"首字节"停表，而这次首字节被挂起吞掉，计时器一直跑过读正文与结算，可能把已完成的请求变成 504 | `abandonUpstreamStreamAdaptation`：清标记并调用 `ReleaseRelayResponseTimer`（解除挂起并停表，等同普通请求首字节的效果） |
| 8 | 上游 usage 报 `prompt_tokens: 0` 时照单全收，输入计 0；普通 `OpenaiHandler` 此时改按估算输入计费 | 同样改按估算输入计费，并在返回体里给出实际计费的 usage（与普通路径改写 usage 一致）；估算 usage（如超时）也经 `ApplyUsagePostProcessing`，与普通流式处理器一致 |
| 9 | `reasoning_content` 与 `reasoning` 拼进同一段文本：上游两个字段都发同一段思考时重复一遍，只发 `reasoning` 的上游被改名为 `reasoning_content` | 两者按别名处理（同 `Message.GetReasoningContent`）：每帧优先 `reasoning_content`（为空时取 `reasoning`）；返回时沿用上游最先使用的字段名 |
| 10 | 转流超时的 504 没有 `(request id: …)`，与其他中转错误不一致 | 追加我方请求 ID |
| 11 | 回退重发对 402（上游余额）与 413（请求体过大，重发还会再传一遍大请求体）也生效，这两种与流式无关 | 402、413 不重发 |

测试：`relay/upstream_stream_parity_test.go` 另含零输入 usage 按估算、估算 usage 补缓存字段、推理字段别名与字段名、空 `reasoning_content` 不遮住 `reasoning`、超时 504 带请求 ID；`relay/upstream_stream_budget_test.go`（工具调用元数据与 choice 条目计入预算、已有工具调用的参数片段照常累积、预算截断覆盖 `stop` 为 `length`、预算内保留上游结束原因、上游忽略流式时解除挂起且未转流时不动）、`relay/upstream_stream_review_test.go`（过载状态码不重发、重发请求体在函数返回后仍可读且由调用方关闭、`TextHelper` 每次尝试从未转流开始）、`relay/upstream_stream_parity_test.go`（llama.cpp 缓存 token 计入结算、content_filter 记录拒绝原因且 stop 不记、上游响应头透传且 `Content-Type` 为 JSON）。

## 21. 分支审计修复（2026-09-29）

来源：[分支对比审计](branch-audit-vs-main.md) M6、M7、M8、L11、L12、L13，均为与主分支 `OpenaiHandler` 行为不一致的缺陷。缺陷修复不加开关。

| # | 问题 | 修复 |
|---|---|---|
| M6 | 错误帧的 `error` 字段以 `json.RawMessage` 交给 `dto.GetOpenAIError(any)`，落入其兜底分支：客户端收到 `"[123 34 109 ...]"`（帧的字节），type/code 变成 `unknown_error`，渠道自动禁用的关键词匹配与错误遮蔽规则拿到乱码。另有两处：无 `type` 但有 `message` 的错误对象丢了 message（报通用错误，自动禁用关键词匹配不到）；正常 chunk 上携带的空 `error`（`{}`、`false`、`0`、`""`）被当成错误 | 先解码为 `any`（与普通响应体的 `OpenAITextResponse.Error` 相同）；空值视为无错误；带 type 或带 message 的对象与非空字符串交 `GetOpenAIError` 按上游错误呈现；其余形态报通用错误，见第 9 节"上游错误帧" |
| M7 | 只有角色帧（或解码为空的帧）后无 `[DONE]` 正常关闭：返回 200、空正文、`finish_reason: "length"` 并按估算输入计费，不重试；同样前缀遇坏帧/读错误却报错重试（第 18 节 #3）。原判断只看"收到过可解析帧"（`ReceivedAnything`） | `adaptedStreamAnswered`：有可用输出，或上游以 `[DONE]`/`finish_reason` 表示已答完，才组装响应，见第 9 节"上游正常关闭但没有可用输出" |
| M8 | 无 `index` 的工具调用增量一律并入最近的调用：两个不同 id/函数名的调用合成一个，arguments 拼成 `{"x":1}{"y":2}`（非法 JSON），按次计费的工具计数 2→1。同名且都无 id 的两个完整调用、以及无 id 的调用后接带 id 的另一函数调用，同样被合并 | 双方都有 id 时按 id 区分；任一方无 id 时按函数名区分，同名则以最近调用的 arguments 是否已是完整 JSON 为界，见第 9 节 |
| L11 | 判定只看 `IsRelayTimeoutManaged`，用户两项非流式时限均为 `-1` 时控制器接管但无截止，请求仍被转流 | 判定改为要求有待到期的时限（第 5 节条件 3） |
| L12 | 渠道 ForceFormat 被忽略：上游 usage 对象原样拼回，非标准字段透给客户端 | ForceFormat 下只保留 DTO 建模的字段，见第 9 节 |
| L13 | Azure 的 `prompt_filter_results`（提示词内容过滤结论，流式中在早期 `choices: []` 帧上）与 choice 上的 `content_filter_results`（输出内容过滤结论）被丢弃 | `AddFrameExtras` 按子串门控读取，`DecorateBody` 写回（前者在顶层，后者在对应 choice 上）；ForceFormat 下与普通路径一样都不带（DTO 未建模）。`prompt_filter_results` 按长度计入聚合预算。`content_filter_results` 在流式中随多数帧重复出现、各自评判截至该帧的文本：每个 choice 只保留最后一个非空对象（替换而非追加，内容变化时才复制，按增长量计入预算，重复帧不重复计费），空对象不覆盖已有结论，只收到过空对象时输出 `{}`；曾被标记 `filtered: true` 的类别不会被之后的结论清除（第 22 节） |

计费与客户端可见变化：M7 使"无输出且上游未表示答完"的请求从"200 + 按输入扣费"变为"500 + 重试/退款"；M8 使按次计价工具按真实调用数计费（含同名无 id 的并行调用），估算用量的每调用附加 token 同步增加；M6 使客户端拿到上游真实的错误信息与错误码（此前是字节数组文本；无 type 的错误对象带出其 message、type 为 `upstream_error`），携带空 `error` 字段的正常 chunk 不再使请求失败并重试；L13 使 Azure 的非流式客户端拿回每个 choice 的 `content_filter_results`。其余不影响计费。

开销：L13 的子串判断与已有的 `"usage"`/`"service_tier"` 判断同一量级（每帧一次 `strings.Contains`，不命中零分配）；Azure 帧命中 `content_filter_results` 时多一次 gjson 遍历，结论未变不分配。L11 每请求多一次请求私有控制器的加锁读取；M6 只在含 `"error"` 的帧上多一次解码；M8 只在工具调用参数写入时逐字节更新结构状态（与写入本身同量级），无 index 的增量多一次 id/名称比较，同名时读缓存的完整性结论（每个调用至多一次 `gjson.Valid`），新调用的键取自独立计数器（第 22 节），均 O(1)，上游持续发送无 index 的调用或分片也不会退化为逐个扫描。

测试：`relay/upstream_stream_branch_audit_regression_test.go`（六个审计用例在修复前失败，另含错误字段各形态、空但已答完的回答照常交付、`adaptedStreamAnswered` 判定表、无 index 调用的续接/拆分/序号不冲突/同名完整调用拆分/无 id 后接带 id 调用、真实控制器下任一时限即转流、ForceFormat 与普通路径逐字段一致（含 `content_filter_results`）、`prompt_filter_results` 计入预算）；`relay/upstream_stream_review_followup_test.go`（同名无 id 并行调用按两次计价、`jsonValueTracker` 分片边界表与只校验一次、无 type 错误对象的 message 进入自动禁用关键词匹配、空 `error` 字段的正常帧照常交付、Azure `content_filter_results` 与普通路径一致、其预算与替换规则）；`relay/upstream_stream_adapt_test.go`（接管但无时限、控制器无法报告截止两种拒绝）。`BenchmarkAddFrameExtras` 普通内容帧与 Azure 过滤结论未变的帧均 0 分配。

## 22. 独立审查修复（2026-09-29）

来源：对第 21 节修复的独立审查，M2、L8。缺陷修复不加开关。

| # | 问题 | 修复 |
|---|---|---|
| M2 | `adaptedStreamErrorFrame` 把任何成员非空的 `error` 对象当错误：`{"error":{"code":200},"choices":[...]}`、`{"error":{"message":"success","code":0},"choices":[...]}` 被判为上游错误——丢弃已收到的内容、退款并重试（上游可能已计费），还可能触发渠道自动禁用。普通路径只在错误带 `type` 时失败 | 无 `type` 的错误对象只在帧不带输出时才是错误（`adaptedFrameCarriesOutput`：某个 choice 的 delta 有非空成员或带 `finish_reason`，或帧有非空 `usage`）；带 `type` 的错误、字符串错误及其余形态保持原判定；只有错误的帧上无 type 错误的 message 仍进入自动禁用关键词匹配，见第 9 节"上游错误帧"。边界：只带角色 delta 的帧也算输出，若其上的无 type 错误确是故障且之后再无输出，请求按第 21 节 M7 的规则报错重试，但该 message 不进入自动禁用匹配 |
| L8a | 同名、无 index 无 id、arguments 为 `""` 或空白的两个调用合为一个：第一个调用丢失，按次计费计 1 | 最近调用参数仍空白时，arguments 同样空白的调用头（带 `type`）拆出新调用；带来参数的调用头续接（兼容每个分片都重发调用头的上游），见第 9 节 |
| L8b | 参数闭合后又重复函数名的尾部增量（如 `{"function":{"name":"f","arguments":""}}`）生成一个空的幻影调用（多计一次工具费与 7 个估算 token） | 同名增量只有在最近调用参数已完整、且增量带来非空白参数、调用头或新 id 时才拆分；只重复名称的增量续接 |
| L8c | 无 index 的新调用取"最大 index + 1"（首个为 0），之后到达的带 index 调用若恰好用了这个值就并入其中 | 无 index 的新调用取 -1、-2……（独立计数器 `indexlessCalls`），上游 index 钳到 ≥0，两个键空间不相交；输出按到达顺序，键值不外露 |
| L8d | `content_filter_results` 保留最后一个结论：之后帧的 `filtered:false` 会清掉先前 `filtered:true` 的类别 | 仍保留最后一个非空结论，但先前标记为 `filtered: true` 的类别若在新结论中未标记或缺失，从旧结论复制过去（`carryFlaggedCategories`）；其余类别随最新结论 |
| L8e | 参数为裸标量时永远不会被判为完整 | 记为已知限制（按接口约定函数参数是 JSON 对象），见第 9 节与 `indexlessToolCallIndex` 注释 |

计费与客户端可见变化：M2 使"每个 chunk 都带无 type 错误对象"的上游从"500 + 重试/退款"变为与普通路径一致的"200 + 按上游 usage 计费"，这类帧不再计入渠道自动禁用；L8a 使同名空参数并行调用按真实调用数计费（多 1 次工具费与 7 个估算 token）；L8b 去掉幻影调用（少计 1 次工具费与 7 个估算 token，客户端不再看到空调用）；L8c 使混合 index 的调用不再被拼接；L8d 使 Azure 客户端拿到的过滤结论不再丢失已触发的类别。

开销：M2 只在 `error` 为无 type 对象的帧上多一次 gjson 遍历 choices/usage（不解码，基准约 1.5µs/帧，与原探测解码同量级）；普通内容帧仍 0 分配（`BenchmarkAdaptedStreamErrorFrame`）。L8 各判断仍 O(1)：空白判断读结构状态，增量参数的空白判断为 `strings.TrimSpace`（不分配）。L8d 在从未出现 `filtered: true` 时与此前相同（未变结论一次字符串比较，`BenchmarkAddFrameExtras` 0 分配）；出现后每个变化或重复的结论多一次 gjson 遍历与至多每个被标记类别一次 `sjson.SetRaw`——Azure 触发过滤后通常随即以 `content_filter` 结束，帧数很少。

测试：`relay/upstream_stream_indep_review_test.go`（修复前失败：无 type 错误对象与内容/角色/工具调用/finish_reason/usage 同帧不是错误、每帧带 `{"code":200}` 或 `{"message":"success"}` 的流与普通路径一样返回 200 并按上游 usage 结算、同名空/空白参数调用头拆分与按两次计价、参数闭合后重复名称不产生幻影、带 index 与无 index 调用先后到达都不冲突、`filtered: true` 类别不被清除或遗漏；另含只有错误的帧仍失败且 message 可被自动禁用匹配、带 type 或字符串错误与内容同帧仍失败、参数未闭合时的调用头续接、每个分片都重发调用头时续接、迟到 id 续接、多 choice 无 index 调用各自独立、裸标量参数的已知限制）。
# 2026-09-29 审计修正：回退重发计入总调用预算

适配后的流式请求若被上游拒绝，仍可在同一渠道回退为普通非流请求，以保持原请求兼容性；但这次回退本质上是一次新的上游调用，必须计入请求级总调用预算。

- 首次适配调用与普通 relay 尝试一样，在 `BeginRelayAttempt` 中占用一次总调用额度。
- 回退重发前必须同时检查 `max_total_attempts` 与剩余总时长预算；任一不足时，不发送回退请求，直接把原始拒绝交给既有错误/重试链。
- 回退获准后立即占用一次总调用额度，再重启本次非流调用的响应计时器。
- `RetryTimes` 仍表示失败后的普通重试次数；回退不额外消耗分组重试次数，但会减少请求级总调用额度，因此后续普通重试可能被总额度拦截。
- `max_total_attempts=2` 时，一次适配调用加一次回退已经耗尽预算，不得再启动下一次普通重试。

## Main Chain Impact

- 同步执行：回退前仅做请求上下文中的计数读取/递增、配置快照读取和本地时间预算判断；无 DB、Redis 或外部调用。
- 异步执行：无新增异步任务。
- 共享资源：复用请求内 `RetryParam`，不增加 Redis key、DB 表、全局锁、连接池或 goroutine。
- 100k RPM：每个可能回退的请求增加 O(1) 本地判断；预算不足时反而少一次上游调用，避免 500 故障期间的双倍放大。

## 23. 客户端断开与异常结束的计费对齐主分支（2026-09-30）

来源：2026-09-29 测试服报告 openai-format D-3（DC1/DC3）、claude-format O1/O8（S01/S13/X01）、relay-control D-2/D-3/D-4/D-5。都是相对主分支的计费或一致性缺陷，修复不加开关，回滚靠回退二进制。

### 23.1 问题

- **A 断开少收**：流式请求在首个业务帧前被客户端断开时按 `none` 释放预扣（收 0），而上游 nexaxis 按 19/44、19/92 token 计费。受超时管理的请求更早一步：上游调用直接绑在客户端连接的 context 上，客户端一断开就取消上游——非流式全额退款，流式在上游响应头到达前断开同样退款，上游照收。可被反复利用（发长请求、首字节前断开）。
- **B 中途结束低计**：Claude 流中途上游报错或客户端断开时，输出按 `message_start` 的 `output_tokens`（Anthropic 恒为 1）计，已交付/已接收的大段内容不计（S01、X01；OpenAI 客户端经 Claude 上游的 S13 同样如此）。
- **C 失败流多一条用量帧**：OpenAI 格式流失败时，错误帧前多出本地合成的 include_usage 尾帧（`id:""`、`created:0`、内部字段，`prompt_tokens` 与实际结算不符）。
- **D 超时说成上游失败**：用户自己的流式时限到期，终止帧写 `upstream_stream_error` / "Upstream stream failed"，消费日志却写我方超时。
- **E 文档漂移**：第 18 节 #3 说转流与流式在"有输出后干净 EOF、无 [DONE]"时行为相同，实际不同；日志管线状态缺少 relay-log-async-batch.md §5 列出的字段。

### 23.2 主分支的实际行为（以 upstream/main 代码为准）

- 上游请求不绑定客户端：`relay/channel/api_request.go` 用 `http.NewRequest`，客户端断开不影响上游调用本身。
- **流式并不在客户端离开后继续读**：`relay/helper/stream_scanner.go` 主循环在 `c.Request.Context().Done()` 时立即 `cleanup()` 关闭上游 `resp.Body`（注释："避免为已放弃的请求继续消费上游 token"）。计费用最后一帧的 usage；没有则 `service.ResponseText2Usage`＝估算输入＋已接收文本估算（OpenAI 再加每个工具调用 7 token）——**首字节前断开也收估算输入**。Claude（`relay-claude.go` `HandleStreamFinalResponse`）在未收到结束事件时输出取 `max(确认值, 文本估算)`，输入为 0 时用估算。
- 非流式：等上游完整响应，按完整 usage 计费；写给已断开客户端的失败被忽略。

### 23.3 决策：流式断开继续取消上游并按估算收费；非流式不再取消

**流式保留"客户端断开即关闭上游"**，理由：

1. 主分支就是这样做的，计费口径对齐是按构造成立的，不依赖上游行为；
2. 我们的上游通常是官方 new-api（如 nexaxis），它在我们断开时同样停止并按"输入＋已生成"计费（DC1 的 19/44 正是如此）。改为继续读到结束会让上游按完整生成计费，平台成本和用户账单都为没人读的输出变大；
3. 不需要后台续读 worker、不延长上游连接占用（Rule 8），也不在响应返回后再碰 `gin.Context`。

剩余差额与主分支相同：上游在最后一帧送达我们之后、察觉断开之前生成的部分（DC1 中的 44 个输出 token）不可观测。

**非流式改为与主分支一致：上游调用不随客户端断开而取消**。非流式在完成前拿不到任何用量，取消后只能按估算输入收费，而 new-api 上游的非流式调用在我们断开后照常生成并按完整用量计费——取消只会让我们少收。继续等到上游完成即可按真实完整用量结算。

我方时限的语义不变：客户端在线时超时立即取消上游；客户端已离开时，上游调用在控制器原本会触发的截止时刻被取消（不会因客户端离开变成不限时）。发送前客户端已离开的请求不发送、照旧释放预扣。

**与 zhuzhan 分支的比对（2026-09-30 确认，保留本修复）**：zhuzhan 的受管请求与本分支修复前相同——超时控制器以客户端请求 context 为父（`middleware/relay_timeout.go` `newRelayTimeoutControl(c.Request.Context(), …)`），`RelayRequestContext` / `BindRelayRequestContext` 把上游调用及 ali 图片轮询、coze 轮询、讯飞 / 火山 WebSocket 绑在它上面，客户端断开即取消上游并走错误退款；未受管请求用 `context.Background()`，与主分支一致。这不是一项计费设计：引入它的 `9405625c4d`（`docs/design/per-user-relay-timeout.md`）写明「计费……继续沿用现有逻辑，不由本功能修改」，只在一处注明「客户端主动断开和父 context 更早取消不归类为 relay 超时」。结果却是同一次断开，开了单用户超时就退款、没开就按实际用量计费——计费随一个超时开关改变，且上游照收（测试服 nexaxis 实测），故作为副作用修正，对齐主分支。

### 23.4 现行计费规则（受管流式 / 非流式）

| 场景 | 修复前 | 现在 | 主分支 |
|---|---|---|---|
| 流式，上游已返回 200，首个业务帧前客户端断开 | 0（释放） | 估算输入 | 估算输入 |
| 流式，已收到内容后客户端断开，无确认用量 | 估算输入＋已接收输出 | 同左 | 同左 |
| 流式，客户端断开，确认输出只来自 `message_start` 或早于最后一段内容 | 确认值（常为 1） | `max(确认值, 已接收估算)` | `max(确认值, 已接收估算)`（Claude）；OpenAI 无末帧 usage 时为估算 |
| 流式，上游中途报错且已交付内容，确认输出过期 | 确认值（常为 1） | `max(确认值, 已交付估算)` | 同左（Claude 未结束）；OpenAI 为估算输入＋已接收估算 |
| 流式，最后一段内容之后上游已报输出计数，流随后中断 | 确认值 | 确认值（上游对全部内容的计数照收） | Claude 取 max；差值只在估算高于上游自己的计数时出现 |
| 流式，确认用量缺输入字段、异常结束 | 输入 0 | 估算输入（显式 0 保留） | Claude 为 0 时用估算 |
| 流式，上游错误且无有效交付 | 0 | 0（不变：上游同样不计费，见测试报告"失败请求"一节） | 估算输入（错误帧被当作普通数据帧，`PostTextConsumeQuota` 只在总 token 为 0 时免费）；本分支刻意不收，不在本次范围 |
| 流式，我方时限切断且无有效交付 | 0 | 超时计费方式 charge：确认用量，否则估算输入＋已接收输出；input：确认输入，否则估算输入，输出 0（[relay-timeout-cost-bearing.md](relay-timeout-cost-bearing.md)）；refund（默认）：0 | 估算输入＋已接收文本（主分支只有旧的空闲超时，没有按用户选择退款的时限） |
| Realtime，握手后未开始任何轮次即断开 | 0 | 0（按轮次计费，不变） | — |
| 非流式（受超时管理），上游作答前客户端断开 | 取消上游，全额退款 | 上游跑完，按完整用量结算 | 按完整用量 |
| Midjourney 提交、扣子轮询、讯飞/火山 WebSocket、阿里异步图片轮询（受超时管理的部分；Midjourney 提交、扣子非流式、阿里生图 / 图片编辑不启用单用户超时，见 relay-timeout-cost-bearing.md），客户端中途离开 | 取消上游工作并退款 | 继续到上游完成（只受我方时限约束），按上游结果结算 | 不绑定客户端，按结果计费 |
| 非流式转流（charge 用户），聚合中客户端断开 | 已有输出按已收结算；无输出退款 | 读完，按完整用量结算 | 按完整用量（主分支不转流） |
| 发送上游请求前客户端已断开（受管） | 释放 | 释放（不发送，没有上游成本） | 上游请求不看客户端状态，照常发送并计费 |

"确认值是否过期"的判定：通用会话在每个观察到的事件后更新 `StreamSnapshot.OutputReportCurrent`（事件带输出计数则为 true，`message_start` 的初值除外；此后再收到内容则为 false；同一帧既有内容又有计数时计数覆盖该帧）；Claude 专用流沿用已有的 `outputUsageCurrent`（最后一个内容块事件之后的 `message_delta` 输出报告）。只有异常结束且确认值过期时才用估算补足，正常结束的上游计数一律照收。

### 23.5 实现

| 位置 | 内容 |
|---|---|
| `relay/common/stream_outcome.go` `SelectUsageSource` | 删去"用户断开且未收到业务响应→none"：会话只有在上游返回成功后才存在，用户断开一律至少按估算 |
| `service/stream_lifecycle.go` `FinalizeStreamUsage` | Realtime 未开始轮次仍为 none；异常结束的确认用量缺输入字段时按估算补输入（同步嵌套 Claude 用量）；异常结束且 `!OutputReportCurrent` 时调用 `SupplementStreamPartialOutput` |
| `service/stream_zero_output.go` | `SupplementStreamPartialOutput`：输出低于估算时提升到估算（`SupplementStreamZeroOutput` 仍只补 0，用于正常结束）；Gemini 嵌套用量只改 candidates，thoughts 保留确认值 |
| `relay/common/stream_session.go` | `OutputReportCurrent`；`classifyStreamSuccessEvent` 新增 `streamSuccessUsageTail`（`choices` 为空数组且带 `usage` 对象）——写入器与候选结束一样只在已记录终止原因时拦下它，健康流中途的用量帧照常放行（C） |
| `relay/channel/claude/strict_stream.go` | 异常结束且 `!outputUsageCurrent` 时用 `SupplementStreamPartialOutput`；我方超时的终止帧用 `timeout_error` 与超时文案（D） |
| `service/stream_lifecycle.go` `StreamTerminalFailureText` / `WriteStreamTerminalError` | 我方时限切断时：错误码 `relay_timeout`（OpenAI/Responses/Realtime）、Claude `timeout_error`、Gemini `504 DEADLINE_EXCEEDED`，文案为带秒数的超时提示（与消费日志、非流式 504 一致），截止前读到的上游错误帧不原样转发；错误提示替换规则仍按既有输入（`relay_timeout`、504）判定（D） |
| `service/relay_timeout.go` `BindRelayRequestContext` / `relayUpstreamLink` / `relayUpstreamTransport` / `RelayHTTPClient` | 上游调用用 `context.WithoutCancel(客户端 ctx)` 派生、带自己的取消；`context.AfterFunc` 监听客户端 ctx：控制器已记录超时→立即取消；否则（客户端离开）按控制器的下一个截止时刻布一个定时器，到点时控制器已记录超时才取消（最多每 10ms 复查，截止后 1s 仍未记录则视为已到期）。受管请求的 `RelayHTTPClient` 克隆客户端并套一层 RoundTripper：每一跳开始时登记、响应体关闭或失败时注销，最后一跳结束即撤下监听与定时器——正常结束的请求不产生 goroutine 或定时器；重定向的下一跳重新登记。未走 `RelayHTTPClient` 的调用方（阿里图片轮询）监听保留到请求结束时执行一次空操作 |
| `model/relay_log_pipeline.go` | 状态接口补 `consume/error/retry.last_flush_items`、`last_flush_took_ms` 与 `oldest_event_age_ms`（E）。年龄取入队缓冲与待写车道的队首、重试队列逐条最早（重试按失败先后追加，队首不一定最早；有上界，只在状态接口扫描），并计入正在落库、尚未返回的批次（刷盘 worker 在落库前后写一个原子量）。只由刷盘 worker 写原子量，入队路径不变 |
| `service/relay_timeout.go` `RelayUpstreamContext` | 非单次 HTTP 交换的上游工作（WebSocket 拨号与连接生命周期、任务轮询）用的 context：与 `BindRelayRequestContext` 共用同一套链接，只由我方时限结束，`release` 撤下监听。`service/midjourney.go`（受管分支改用 `BindRelayRequestContext`）、`relay/channel/coze`（轮询与轮询请求）、`relay/channel/xunfei`（旧路径与受管流式的拨号/连接生命周期）、`relay/channel/volcengine/tts.go`、`relay/channel/ali/image.go`（异步任务轮询，包括受管图片会话：轮询已提交的任务不是流式读取，停下来只会按估算少收）改用它。仍用客户端 context 的只剩 Realtime WebSocket（客户端连接就是会话本身）与百度的 access token 获取（发生在主请求发送前，断开即不发送） |
| `service/stream_lifecycle.go` `StreamTimeoutUsageSource`；`strict_stream.go` | 我方时限切断且没有有效交付：用户的超时计费方式（`non_stream_timeout_billing`）为 charge 时按确认用量或估算输入＋已接收输出收费，refund 时不收费 |
| `relay/channel/openai/helper.go`、`relay/channel/claude/relay-claude.go` | 受管流未确认协议完成（上游失败、我方超时、客户端断开）时不生成合成用量尾帧；写入器的尾帧拦截作为兜底（它只在已记录终止原因时生效，我方超时退出时尚未记录） |

E 的决定：两条路径的行为都保留、改正文档（第 18 节 #3）。受管流式对"无结束标记的干净 EOF"下发错误帧是[统一流式错误处理](unified-stream-error-handling.md)的既定合同——流式客户端没有别的方式得知回答被截断；转流路径返回 200＋`finish_reason: "length"` 是非流式协议里表达同一件事的标准方式，且与主分支流式把干净 EOF 记为正常结束一致。两者都按已收到的输出计费，与主分支计费一致。

### 23.6 Main Chain Impact

**同步（relay goroutine 上）**：受管请求每次上游调用多 `context.WithoutCancel`＋`WithCancelCause`＋`WithValue`、一次 `context.AfterFunc` 登记（无 goroutine）、一次 `http.Client` 浅拷贝与 RoundTripper/响应体包装（约 5 次小分配），每跳两次请求私有互斥锁。每个流式事件多一次布尔赋值；OpenAI 形状的帧在 `choices` 为空数组时多一次 gjson 取 `usage`。结算阶段 O(1)。未受超时管理的请求：`BindRelayRequestContext` 原样返回请求、`RelayHTTPClient` 原样返回共享客户端，零变化。

**异步**：只有在上游调用进行中客户端 ctx 被取消（客户端断开或我方超时）时，`AfterFunc` 才起一个短 goroutine；客户端断开且仍有截止时刻时多一个 `time.AfterFunc` 定时器，随响应体关闭而停止。

**失败静默**：链接状态全部请求私有；控制器接口缺失时退回原绑定方式（取消随客户端）；包装层对未绑定的请求透明。

### 23.7 Shared Resource Audit

| 资源 | 变化 | 主链是否同资源 | 结论 |
|---|---|---|---|
| 上游连接池 | 受管非流式调用在客户端离开后继续持有连接直到上游完成或我方时限到期 | 是 | 与未受管请求及主分支相同；上界是用户配置的时限，同时持有的连接数不超过客户端未离开时的数量 |
| 用户/令牌资金、消费日志、成本流水 | 原先释放/退款的断开请求改为结算，产生消费日志 | 是 | 沿用单次结算与异步日志管线，调用次数不增加（结算替代退款） |
| 请求级超时控制器 | 只读其 `RelayTimeoutKind` / `RelayTimeoutDeadline`（各一次加锁） | 是 | 控制器为请求私有，锁只在本请求内争用 |
| goroutine / 定时器 | 仅在客户端 ctx 于调用进行中被取消时产生，随调用结束回收 | — | 有界于并发请求数 |
| 日志管线原子量 | 刷盘 worker 写、状态接口读 | 否（relay 入队路径不读写） | 无新增锁 |

Redis key、DB 表、全局锁：无新增。

### 23.8 Concurrency Analysis（100k RPM）

每请求 DB 0、Redis 0、全局锁 0、正常路径 goroutine 0。受管请求每次上游调用约 5 次小分配与两次请求私有锁；按 1% 的请求在调用中断开估算，约 17 个/秒的短 goroutine。流式每帧多一次布尔写与（空 choices 帧）一次 gjson 查找，相对每帧 0.2–0.4 ms 的既有 CPU 可忽略。

### 23.9 测试

均为夹具/httptest，不连真实上游；修复前失败的已标出：

- `service/stream_partial_billing_test.go`：首字节前断开按估算输入结算且状态为 settled（修复前 none/released）、Realtime 未开始轮次仍不收费；异常结束的输出取 max 表（Claude 仅 message_start 后上游报错/客户端断开、OpenAI 过期的逐块 usage、缺输入字段按估算——修复前失败；最后一段内容之后的计数照收、正常结束照收上游计数——守卫用例）；我方超时的终止帧在 OpenAI/Responses/Claude/Gemini 四种格式下为超时码与文案（修复前失败），上游失败仍为 `upstream_stream_error`（守卫）。
- `relay/channel/claude/strict_stream_partial_billing_test.go`：S01、X01、首字节前断开、我方超时终止帧（修复前失败）；最后内容后的 message_delta 计数在流中断时照收、正常结束不上调（守卫）。
- `service/relay_upstream_detach_test.go` 与 `middleware/relay_timeout_upstream_detach_test.go`（真实控制器）：客户端断开后上游调用完成并拿到完整 usage、客户端先断开后仍在响应时限处取消并按超时分类、重定向第二跳同样不随断开取消（修复前失败）；客户端在线时超时立即取消、发送前已断开不发送、未受管请求不变、响应体关闭后撤下监听且不留定时器（守卫/新结构）。
- `relay/channel/openai/stream_failure_usage_tail_test.go`：首帧错误、只有角色帧、内容后 EOF、内容后上游错误四种失败都不再出现 `"choices":[]` 尾帧，终止错误帧是最后一帧（修复前失败）；成功流仍补发 include_usage 帧（守卫）。`relay/common/stream_usage_tail_test.go`：尾帧分类、写入器只在失败后拦截、`OutputReportCurrent` 判定。
- `model/relay_log_pipeline_status_fields_test.go`、`controller/relay_log_pipeline_status_test.go`：新状态字段（修复前不存在）。
- 为新规则更新的既有用例：`TestStreamDisconnectUsage`/`TestStreamDisconnectPingAndUpstreamFailure`/`TestUnifiedStreamBillingMatrix`/`TestClaudeBillingDecision`/`TestStrictClientCancellation`（断开不再释放）、`TestStreamAwsExceptionLogMessage`（Bedrock Claude 中途异常取 max）、`TestRealtimeStreamBetweenRoundsTermination`（Realtime 超时帧为 `relay_timeout`）、`TestRelayHTTPClient*`（受管客户端总是克隆并包装共享 transport）。

### 23.10 独立审查后的补充（2026-09-30）

- **A 覆盖所有受管上游工作**：HTTP 之外的上游工作此前仍用客户端 context（`RelayRequestContext`）——Midjourney 提交、扣子轮询、讯飞/火山 WebSocket、阿里异步图片轮询在客户端离开时停止并退款，而上游已接单计费。现统一走 `RelayUpstreamContext` / `BindRelayRequestContext`。多请求流程的后续请求（扣子轮询、阿里任务查询）必须沿用该流程的上游 context：若对每个后续请求重新调用 `BindRelayRequestContext`，客户端离开后它会按"发送前已断开"拒绝发送，流程仍会中途放弃。
- **我方时限切断、没有有效交付的流**（策略决定）：遵从用户的超时计费方式。选择 charge（"超时的上游成本由我承担"）的用户至少按主分支口径收费——有确认用量（如 Claude `message_start` 的输入）用确认用量，否则估算输入＋已接收输出；refund（默认）用户保持不收费，这是用户可选的本分支功能。该字段原本只作用于非流式，现在也决定流式这一种情形；有有效交付的超时流照旧按已交付内容结算（与计费方式无关）。
- **`oldest_event_age_ms` 在日志库故障时不低报**：重试队列逐条取最早入队时间；正在落库、尚未返回的批次（整轮选中后分批落库的剩余部分、重试批次）由刷盘 worker 在落库前记下最早入队时间、返回后清零（`defer`，落库 panic 也会清零）。
- **资源**：客户端离开后，这些 WebSocket 连接与轮询继续到上游完成或我方时限到期，持有时长的上界与客户端在线时相同；每项工作一个请求私有链接（`context.AfterFunc` 登记，无 goroutine），`release` 在工作结束时撤下。
- **热路径**：`classifyStreamSuccessEvent` 只展开一次 `choices`。
- **阿里图片轮询**：不再对不经 `RelayHTTPClient` 的请求调用 `BindRelayRequestContext`（此前每次轮询留下一个到请求结束才触发的监听）。

补充测试（修复前均失败）：`service/relay_upstream_detach_test.go` 的 `TestMidjourneySubmitSurvivesClientDisconnect`、`TestRelayUpstreamContextFollowsOwnTimeoutOnly`；`relay/channel/coze/poll_disconnect_test.go`（客户端离开后轮询到完成；我方时限仍结束轮询——守卫）；`relay/channel/xunfei/disconnect_test.go`（客户端离开后仍读到回答与用量）；`service/stream_partial_billing_test.go` 的 `TestStreamOwnTimeoutWithoutDeliveryFollowsTimeoutBilling` 与 Claude 计费对象缺输入字段用例；`relay/channel/claude/strict_stream_partial_billing_test.go` 的 `TestStrictOwnTimeoutWithoutDeliveryFollowsTimeoutBilling`；`relay/channel/openai/stream_failure_usage_tail_test.go` 的 `TestStreamOwnTimeoutDoesNotEmitSyntheticUsageTail`；`model/relay_log_pipeline_status_fields_test.go` 的重试批次连续失败两次、落库进行中两个用例；`controller/relay_retry_billing_branch_audit_test.go` 的首字节前断开用例改为按倍率计价，能区分"按估算结算"与"预扣原样保留/释放"。原 `TestBranchAuditRelayClientCancelStopsRetryAndRefunds`（客户端断开即取消上游并退款）按新要求改为 `TestBranchAuditRelayClientCancelKeepsUpstreamUntilDeadline`（断开不取消上游、仍受时限约束、不再重试）与 `TestBranchAuditRelayClientCancelBillsCompletedNonStreamUpstream`（上游完成后按完整用量结算）。

### 23.11 终审补充（2026-09-30）

- **上游工作不得比处理器活得更久（M1）**：控制器关闭（`Close`，请求结束）后 `RelayTimeoutDeadline` 返回 false、`RelayTimeoutKind` 为空，链接曾把它读成"不限时"：客户端先离开、请求随后结束时，上游 context 永不取消——旧讯飞流式路径在 `c.Stream` 因断开返回后，读取 goroutine 卡在向 `dataChan` 发送上，永远持有 goroutine 与 WebSocket；响应体未关闭的受管 HTTP 交换同理（受管客户端去掉了 `RELAY_TIMEOUT`）。截止时刻无法区分"已结束"与"不限时"，因此控制器新增结束回调 `AfterRelayClose`（`middleware/relay_timeout.go`，`Close` 在取消请求 context 之后逐一执行；已结束时不登记），链接创建时登记、最后一个交换结束（响应体关闭或 `release`）时撤销；请求结束时仍在进行的链接工作以 `errRelayRequestFinished` 取消。锁顺序：链接锁在外、控制器锁在内；`Close` 在释放控制器锁后才执行回调。
- **阿里异步图片（M2）**：受管图片会话的任务轮询也改用上游 context，客户端离开后照常轮询到任务完成，按上游返回的实际张数结算（此前按估算 1 张 / 输入结算，而任务照样生成并按全部张数计费）。
- **原生 Claude 缺输入字段（L1）**：异常结束且上游没报过 `input_tokens` 时，确认用量分支按估算输入补齐（显式 0 保留），与通用流一致。
- **客户端离开后不再重试（L2）**：`ContinueRelayAttempts` 在客户端已离开（且不是我方超时）时不再开始下一次尝试——它只会为没人读的回答再花上游调用；受管请求的下一次尝试原本会在发送前被拒绝，却对一个没碰过的渠道记一条 `do_request_failed`。对未受管请求同样生效（此前会真的再发一次上游请求）。

补充测试（修复前均失败）：`service/relay_upstream_detach_test.go` 的 `TestBoundUpstreamCancelledWhenRequestFinishesAfterClientLeft`（有无截止时刻两种）、`TestRelayUpstreamContextEndsWithRequest`、`TestContinueRelayAttemptsStopsAfterClientLeft`；`middleware/relay_timeout_upstream_detach_test.go` 的 `TestManagedUpstreamCancelledWhenRequestFinishes`（真实控制器）与 `TestRelayTimeoutAfterRelayCloseRunsOnceAndStops`；`relay/channel/xunfei/disconnect_test.go` 的 `TestXunfeiReaderExitsWhenRequestFinishes`（goroutine 泄漏）；`relay/channel/ali/managed_image_task_test.go` 的 `TestManagedAliTaskPollingSurvivesClientDisconnect`（替代按旧合同"断开即停止轮询"的 `TestManagedAliInitialTaskCancellation`；轮询首等与间隔改为包级变量以便测试缩短）；`relay/channel/claude/strict_stream_partial_billing_test.go` 的 `TestStrictAbnormalEndWithoutInputTokensEstimatesInput`；`controller/relay_retry_billing_branch_audit_test.go` 的 `TestBranchAuditRelayNoRetryAfterClientLeft`（未受管修复前失败；受管子用例为守卫——修复前下一次尝试虽被拒绝而没有发出，但仍记了渠道错误）。

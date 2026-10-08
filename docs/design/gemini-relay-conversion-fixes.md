# Gemini 中继：countTokens 拦截、嵌入动作、跨格式参数与推理映射

来源：`docs/test-reports/2026-09-29-gemini-multimodal.md` 的 D-1、D-3、D-4、D-6、D-7。

## 1. 行为

### 1.1 `:countTokens`（D-1）

- 网关不实现 token 计数。`POST /v1beta/models/{model}:countTokens` 与
  `POST /v1/models/{model}:countTokens` 一律按未注册路由返回 404
  （`controller.RelayNotFound`，与上游 `81336fc69b` #7388 一致）。
- 拦截是 `/v1beta` 与 `/v1` 两个中继路由组的第一个中间件，发生在鉴权、限流、
  选渠道之前（不占限流名额，不会因选渠道失败返回 503），更在预扣与计费之前，
  因此不会产生上游调用、消费日志或成本台账。`/v1` 下其他中继路径都不以
  `:countTokens` 结尾，不受影响。
- 实现：`router/relay-router.go` `rejectGeminiCountTokens`。
- 背景：所有原生 Gemini 路径原本都按生成请求转发，countTokens 会被改写成一次
  真实的 `generateContent` 并计费（Gemini CLI/SDK 会频繁调用它）。

### 1.2 原生嵌入动作（D-1 同根）

- 原生 Gemini 请求（`RelayModeGemini`）路径是 `:embedContent` /
  `:batchEmbedContents` 时，上游动作取路径里的动作，与模型名无关；
  响应也按嵌入解析（两处共用 `nativeGeminiEmbedAction`）。
- 非原生请求（如 OpenAI `/v1/embeddings` 进 Gemini 渠道）仍按模型名前缀判定。
- 实现：`relay/channel/gemini/adaptor.go` `GetRequestURL` / `DoResponse`。

### 1.3 OpenAI → Gemini 请求参数（D-3）

| OpenAI | Gemini | 规则 |
|---|---|---|
| `top_p`、`seed` | `topP`、`seed` | 客户端给了就转发，含 0（Rule 5） |
| `max_completion_tokens` / `max_tokens` | `maxOutputTokens` | 前者优先；给了就转发，含 0 |
| `presence_penalty`、`frequency_penalty` | — | 不映射（与上游 main 一致）：Gemini 2.5/3 对该字段返回 400 "Penalty is not enabled"，而很多 OpenAI SDK 总是带上它 |

Responses → Gemini 的直连转换器（`oai_responses/to_gemini_chat_req.go`）同样
转发显式 0 的 `top_p` / `max_output_tokens`（与 main 一致），并在模型名别名之后用
`reasoning.effort` 走 1.5 的同一映射。

### 1.4 Gemini → OpenAI 请求参数（D-3）

| Gemini | OpenAI | 规则 |
|---|---|---|
| `topP`、`topK`、`maxOutputTokens`、`candidateCount`、`seed` | `top_p`、`top_k`、`max_tokens`、`n`、`seed` | 给了就转发，含 0（与 main 一致） |
| `presencePenalty`、`frequencyPenalty` | — | 不映射（与 main 一致）：OpenAI 推理模型与 Claude 目标不接受 |
| `responseMimeType: application/json` | `response_format` | 有 `responseJsonSchema`（非 null，标准 JSON Schema）时为 `json_schema`（name `response`）；否则（含只有 Gemini 方言的 `responseSchema`，其 nullable/propertyOrdering 等可能被 OpenAI 校验拒绝）为 `json_object`；其他 MIME 不映射 |
| `toolConfig.functionCallingConfig.mode` | `tool_choice` | AUTO/VALIDATED→`auto`，NONE→`none`，ANY→`required`；ANY 只允许一个已声明函数时指定该函数；允许多个时把 tools 收窄到允许的函数再 `required`；允许的名字都没声明时保留全部 tools 并 `required`；只有存在函数工具时才设置 |
| 函数 `parameters` 的 `type` | 小写 JSON Schema 类型 | 只沿子 schema 关键字（properties/items/anyOf/…）递归，不改名为 `type` 的属性与 enum 值 |

### 1.5 推理设置（D-4）

映射表移植自上游 `relaykit/relayconvert/reasoning/gemini.go`（`RenderGemini`、
`EffortFromBudget`），实现在 `relaykit/relayconvert/internal/shared/gemini/reasoning.go`。

**OpenAI `reasoning_effort` → Gemini `thinkingConfig`**（`ApplyReasoningEffort`）

- 优先级：模型名别名（`-thinking`、`-thinking-N`、`-nothinking`、`-low` 等）与
  `extra_body.google.thinking_config` 更具体，已设置 thinkingConfig 时不覆盖；
  模型名带任何思考别名时也不叠加（无论 ThinkingAdapter 开关）。
- 不依赖 `ThinkingAdapterEnabled`：`reasoning_effort` 是标准字段。
- 2.5 系列（按预算）：none→0；minimal/low→1024；medium→8192；high/xhigh/max→24576；
  再夹到模型区间（2.5-pro 128–32768，flash-lite 512–24576，其余 0–24576）。
- 3 系列与 `*-latest`（按等级）：映射到模型支持的 thinkingLevel
  （3-pro 只有 low/high，3.1-pro 无 minimal，3.1-flash-image 只有 minimal/high）。
- 无法关闭思考的模型收到 none 时取最低档（2.5-pro→预算 128，3 系列→最低等级）。
  上游此时报 4xx；本分支转换错误尚不能以 4xx 返回客户端，故取最接近的设置。
- 不可配置（2.5-flash-image、tts、live、3-pro-image）或未知系列（2.0 等）、
  未知取值：不改请求。
- `includeThoughts` 不设置（与上游一致）。
- 实际下发的档位记入 `info.ReasoningEffort`（消费日志 `reasoning_effort`）。

**Gemini `thinkingConfig` → OpenAI `reasoning_effort`**（`OpenAIReasoningEffortFor`）

- 读取：`thinkingLevel`（minimal/low/medium/high，大小写不敏感）优先；否则按预算：
  0→none，≤1024→low，≤8192→medium，更大→high。预算 -1（动态，即模型默认）
  及其他负数、只有 `includeThoughts` 时不映射，目标沿用自己的默认值
  （上游此处映射为 high 并按模型族补默认档；本分支不补，以免把"默认"升级成
  high 推理并多计费）。
- 输出只给已知接受 `reasoning_effort` 的目标模型（按上游模型名），并夹到该模型
  接受的取值；其余模型（gpt-4o、Claude、未知 OpenAI 兼容模型等）一律不设置：

  | 目标模型 | 取值 |
  |---|---|
  | gpt-5.1 / 5.2 / 5.4 及日期快照 | none/low/medium/high；minimal→low |
  | gpt-5 / gpt-5-mini / gpt-5-nano 及日期快照 | minimal 起；none→minimal |
  | 其他 gpt-5 变体（-pro 除外，只接受 high，不设置） | low 起；none/minimal→low |
  | o1/o3/o4（o1-mini、o1-preview 除外，不设置） | low 起；none/minimal→low |
  | Gemini 2.5/3（经 OpenAI 兼容端点） | 同上面的"最接近可用档"规则 |

- 原因：这一步是 OpenAI 中间格式，也喂给 Claude 与 Responses 目标。Claude 转换器
  会把 `reasoning_effort` 变成 `thinking{enabled, budget}`，却没有上游 main 的
  temperature/top_k/max_tokens 约束处理，会让原本能用的 Gemini→Claude 请求被
  Anthropic 400；OpenAI 非推理模型及 GPT-5.1 之前的模型也拒绝相应取值。

### 1.6 Gemini 客户端经 OpenAI 渠道流式的 usageMetadata（D-6）

- `StreamResponseOpenAI2Gemini` 原先跳过"无内容、无结束原因"的帧，
  `include_usage` 的末帧（`choices: []` + usage）因此被丢弃，客户端只看到
  各帧上的估算值（prompt=本地估算，candidates=0）。
- 现在 choices 为空且带非零 usage 的帧会输出一个 `candidates: []` 的 Gemini 帧，
  携带真实 usage（与上游 main 的 `ChatToGeminiStreamState` 行为一致）。
- 计费不变：结算一直用 OpenAI 流的 usage，这里只修正返回给客户端的数字。

### 1.7 流式请求首字节前上游报错的 Content-Type（D-7，未修）

- 现象：`relay/channel/api_request.go` `doRequest` 在发上游请求前就对流式请求调用
  `SetEventStreamHeaders`（为了 ping 保活）；上游首字节前返回 4xx 时，
  `controller/relay.go` 的 `writeError` 用 `c.JSON` 写错误，而 gin 只在
  Content-Type 为空时才设置它，所以响应头仍是 `text/event-stream`，body 是 JSON。
- 上游 main 行为相同。
- 建议修法（不在本次改动范围内的文件）：`writeError` 里在 `!c.Writer.Written()`
  时先把 `Content-Type` 覆盖为 `application/json; charset=utf-8`，并删除
  `event_stream_headers_set` 附带的 `Cache-Control`/`Connection`/
  `Transfer-Encoding`/`X-Accel-Buffering`。已经写出 ping 的连接头部已提交，维持现状。

## 2. Main Chain Impact

1. 同步执行：`rejectGeminiCountTokens` 是一次 `strings.HasSuffix`，挂在
   `/v1beta` 与 `/v1` 中继组上（每个 /v1 中继请求多一次亚微秒级的字符串比较）；
   其余改动都是请求转换内的内存操作
   （每个 Gemini→OpenAI 请求对函数 schema 做一次拷贝式遍历，深度上限 64）。
   无异步部分，无新 goroutine。
2. 共享资源：不读写 Redis、DB、缓存、连接池。`info.ReasoningEffort` 是请求内字段。
3. 100k RPM：每请求新增 0 次 DB、0 次 Redis、0 把锁。
4. 不加开关：均为缺陷修复，回滚靠回退二进制。

## 3. 测试

| 缺陷 | 测试 |
|---|---|
| D-1 | `router/gemini_count_tokens_route_test.go`（/v1beta 与 /v1 都 404 且在 TokenAuth/限流/Distribute 之前）；`relay/channel/gemini/adaptor_action_test.go`（URL 动作；httptest 上游收到 `:embedContent` 并原样回传） |
| D-3 | `oai_chat/to_gemini_chat_req_test.go`、`gemini_chat/to_oai_chat_req_test.go`（显式 0、缺省省略、罚分不映射、response_format、tool_choice 与收窄、schema 小写） |
| D-4 | 同上两文件的推理用例（含按目标模型的取值表）+ `shared/gemini/reasoning_test.go`（各模型族分支） |
| 链路 | `relaykit/relayconvert/gemini_reasoning_chain_test.go`：经注册表的 Gemini→Claude（不开 thinking、保留 temperature/max_tokens）、Gemini→Responses（按模型设置 reasoning）、Responses→Gemini（top_p=0、effort none→预算 0）、OpenAI→Gemini（不带罚分） |
| D-6 | `oai_chat/to_gemini_chat_resp_test.go`（O4 报告的流式夹具，末帧 usage 2/17/19） |

## 4. 与其他子系统的交互

- Claude→Gemini、Responses→Gemini：`reasoning_effort` / `reasoning.effort` 现在
  作用到 Gemini 渠道（1.5）。
- Gemini→Claude：`thinkingConfig` 不进入 Claude 请求（目标模型不在 1.5 的白名单），
  与修复前一致；`toolConfig` 以 `tool_choice` 进入 Claude 转换器（none/any/tool）。
- Gemini→Responses：`reasoning` 只对白名单模型设置。
- 计费：countTokens 不再计费；推理映射改变的是上游实际产生的 thinking token，
  结算仍按上游 usage。

## 5. 2026-10-08 合并后的推理映射

本节覆盖前文 D-4 与推理链路中已失效的描述。合并采用上游
`relaykit/relayconvert/reasoning` 的统一 Intent 转换，旧的
`shared/gemini/reasoning.go` 已删除。Gemini 请求先解析显式 thinking 配置，
再用 `ResolveGeminiDefault` 补全源模型默认值，然后转换至目标协议。
Gemini 2.5 的动态默认预算为 -1；Flash-Lite 默认关闭推理。
转换到 OpenAI 时动态预算会映射为默认推理强度，可能增加实际推理消耗。
Gemini→Claude 不再一律忽略 thinking 配置；目标协议无法表示的预算可能返回转换错误。
实际行为由统一 reasoning 模块及其夹具测试约束。

此合并保持上游映射语义。恢复旧版成本控制策略属于后续独立变更，尚未实施。

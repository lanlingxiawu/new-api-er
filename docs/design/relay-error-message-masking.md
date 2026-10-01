# 中转错误提示可配置替换（隐藏上游细节）

> 状态：已实现（默认关闭）。

## 1. 目标与范围

**目标**：管理员可在后台配置"客户端看到的错误提示"，让用户无法从错误信息推断上游的真实情况（上游是哪家、上游分组名、上游令牌余额、上游内部报错与请求 ID 等）；原始错误仍完整保留给管理员排障。

**默认关闭**：未开启时所有错误提示与现在逐字一致（增强功能，语义不变）。

**范围内**：
- 中转接口（`/v1/*`、`/v1beta/*` 等 relay 路由）返回给客户端的错误：非流式错误 JSON、流式终止错误帧、本地分发错误（无可用渠道等）、超时提示。
- 用户在"使用日志"里看到的**错误日志内容**（目前直接显示上游原文）。

**范围外**：
- 管理员视角（管理员日志、错误日志详情、系统日志）：保持原文，这是排障依据。
- 重试判断、自动禁用渠道的关键词匹配：继续基于原文（替换只发生在"交给客户端"的最后一步）。
- Midjourney / 异步任务接口的错误（格式各异，第二期再做）。
- 成功响应里的上游痕迹（见 §10 附带发现，另行处理）。

## 2. 现状：哪些上游细节会泄露给用户

真实上游压测（nexaxis.ai）与代码梳理确认的泄露点：

| # | 场景 | 用户实际看到的 | 泄露了什么 |
|---|---|---|---|
| 1 | 上游是另一个 new-api，模型无可用渠道 | `No available channel for model X under group ChatGPT_AZ (distributor)` + `type:"new_api_error"` | 上游分组名、上游是 new-api |
| 2 | 上游令牌额度不足 | `token quota is not enough, token remain quota: ＄0.000002, need quota: ＄0.000066` | 上游令牌余额 |
| 3 | 上游内部错误 | `error getting file type: failed to download file from https://…: dial tcp: lookup … on 127.0.0.53:53` | 上游服务器内部实现与网络 |
| 4 | 上游错误的 `type` / `code` / `param` / OpenRouter `metadata` | 原样透传（`SetMessage` 只改 message） | 上游厂商与错误体系 |
| 5 | 流式中途上游报错，且协议一致 | 上游错误帧**逐字节转发** | 同上 |
| 6 | 转流式请求中途上游报错 | `upstream stream error: <上游原始 SSE 片段>` | 上游原始数据 |
| 7 | 用户"使用日志"里的错误日志 | `content` 存的就是上游原文，查询时只删了 `admin_info` | 以上全部 |
| 8 | 本地分发错误 | `No available channel for model X under group Y (distributor)` | **我方**分组名 |
| 9 | 其他格式的上游请求 ID（`req_…`） | 只剥离了 `(request id: …)` 这一种格式 | 上游请求 ID |

现有的脱敏只有硬编码的 URL / 域名 / IP / `api_key:` 掩码与 `(request id: …)` 剥离；渠道的"状态码映射"只能改状态码。没有任何可配置的文案替换。

## 3. 功能设计

### 3.1 配置模型

新增配置 `relay_error_display_setting`（`setting/operation_setting/relay_error_display_setting.go`，按 Rule 12 注册到 ConfigManager，热更新）：

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `enabled` | bool | `false` | 总开关。关闭时一切照旧 |
| `hide_upstream_errors` | bool | `true` | 开启后，**上游来源**的错误若没有命中任何规则，一律替换为下面的兜底文案 |
| `default_message` | string | 空 | 上游错误的兜底文案。留空时用内置文案（i18n `relay_error.default_message`），按每位用户的语言显示；填写后对所有用户原样使用 |
| `rules` | JSON 数组 | `[]` | 按顺序匹配，**第一条命中生效** |

每条规则：

| 字段 | 说明 |
|---|---|
| `name` | 备注，仅后台显示 |
| `source` | `upstream` / `local` / `any`：只匹配上游来源、只匹配本地产生的错误、或都匹配 |
| `status_codes` | 可选，HTTP 状态码列表，如 `[400, 422]` |
| `error_codes` | 可选，错误码列表，如 `["model_not_found", "relay_timeout"]` |
| `keywords` | 可选，关键词列表（不区分大小写的包含匹配），如 `["quota", "余额"]` |
| `action` | `replace`（替换为 `message`）、`keep`（保留原文，用于放行对用户有用的错误，如"上下文超长"）或 `edit`（在原文上局部修改，见 §3.5） |
| `message` | `action=replace` 时的新文案 |
| `edits` | `action=edit` 时按顺序执行的查找替换列表，每项 `{find, replace, regex}`，见 §3.5 |
| `status_code` | 可选，同时改写返回给客户端的 HTTP 状态码（`replace` 与 `edit` 可用） |

一条规则内的多个条件是"且"关系；同一条件内的多个值是"或"关系；未填的条件不参与匹配。匹配条件里的关键词只做包含匹配、不支持正则；局部修改的查找项可以用正则（§3.5）。

### 3.2 替换结果

命中替换（规则 `replace` 或兜底）时，返回给客户端的错误：
- `message` = 配置的文案 + `(request id: <我方请求 ID>)`（保留我方请求 ID，用户报障时管理员可据此查原文）；
- 重新构造为一个本地错误（`types.NewErrorWithStatusCode`），因此 OpenAI 与 Claude 两种格式的 `type` 都是 `new_api_error`，上游的 `type` / `param` / `metadata` 不可能再带出；
- `code` = 我方本地错误码保留；上游来源的错误统一改为 `upstream_error`；
- 状态码：规则指定了就用规则的，否则不变（渠道"状态码映射"已作用在前）。

`keep` 与未命中（且非上游来源或 `hide_upstream_errors=false`）时，保持现有输出逐字不变。

### 3.3 "上游来源"的判定

`NewAPIError` 的来源在创建时已可区分：
- **上游**：由上游 HTTP 错误响应/错误帧构造的（`RelayErrorHandler`、`WithOpenAIError`、流式错误帧、转流式错误帧），以及请求上游失败（`do_request_failed`、`bad_response*`）；
- **本地**：本网关自己产生的（参数校验、额度不足、无可用渠道、超时、限流等），错误码为本地枚举值。

实现：`operation_setting.IsUpstreamRelayErrorKind(errorType, errorCode)`——错误类型为 `openai_error` / `claude_error` / `gemini_error` / `rerank_error` / `upstream_error`，或错误码为 `bad_response*` / `do_request_failed` / `empty_response` / `read_response_body_failed` / `aws_invoke_error` / `channel:aws_client_error` 即为上游。中转主出口与用户日志共用这一个判定。

流式终止帧：没有 `NewAPIError` 可用，按上游来源处理（错误码依次取上游帧的 `error.code`、`code`、`response.error.code`、`response.status_details.error.code`（Realtime 失败的 `response.done`）中第一个非空字符串或数字，`null`、空串或对象不算，缺省为 `upstream_stream_error`；文案依次取 `error.message`、`message`、`response.error.message`、`response.status_details.error.message`，缺省为我方通用提示；文案先取前 16 KB（被切开的最后一段去掉）按日志同样的规则掩码 URL、主机与 `api_key`，再截到 4000 字（截断处被切开的最后一段一并去掉，见 §3.5「文本长度上限」），先掩码后截断，截断不会把地址或密钥切成掩码认不出的半段；被截过的文案标记为"不完整"，规则都没替换时客户端收到截断后的文案而不是原样转发上游帧）；但若流是被**我方**超时切断的，按本地 `relay_timeout`（状态码 504）匹配，此时即使规则都没命中，也不把之前读到的上游错误帧原样转发，而是发我方通用提示（上游帧没有经过判定，原样转发会绕过"隐藏上游错误"）。这一判定由 `operation_setting.StreamFailureRelayErrorInput` 与 `service.streamTerminalRelayErrorInput` 给出。终止帧用的这份匹配输入（错误码 + 文案）记入请求上下文，结算时写进同一条流的错误日志（仅管理员可见的 `admin_info`），用户视图用同一份输入判定（§4.1），两处结果逐项一致。

### 3.5 局部修改（`action=edit`）

整条替换会把对用户有用的信息一起抹掉（例如"对于模型 gpt-5 不可用"里的模型名），而上游细节往往只是其中一段（分组名、上游请求 ID、上游站名）。局部修改在原文上按顺序执行一组查找替换，只改掉需要隐藏的部分：

```text
原文：当前分组 vip-A 下对于模型 gpt-5 无可用渠道 (request id: 2026092812abcd)
规则：查找 `当前分组 \S+ 下`（正则）→ 删除
      查找 `\(request id: [^)]*\)`（正则）→ 删除
      查找 `无可用渠道` → `暂时不可用，请稍后再试`
结果：对于模型 gpt-5 暂时不可用，请稍后再试
```

- **查找**：默认按普通文字匹配，不区分大小写；勾选"正则"后按 Go 的 RE2 正则匹配（同样不区分大小写）。
  - **普通文字**不经过正则：查找内容与文本都按 Unicode 简单大小写折叠（与 `(?i)` 相同的等价关系，如 `k`/`K`/开尔文符号 `K`）归一后用 `strings.Index` 做子串查找，耗时与文本长度成线性、与查找内容无关；替换结果与 `(?i)` + 转义后的 `ReplaceAllLiteralString` 逐字节相同（`relayErrorLiteralEdit`，有随机对比测试）。曾经普通文字也编译成正则且不计入预算，20 项 `a…ab`（199 个 a）在 4000 个 a 上约 108ms；现在同样的规则约 0.4ms。
  - **正则**：RE2 保证匹配耗时与文本长度成线性，不存在回溯爆炸；但线性的系数是编译后程序里可同时活跃的状态数，计数重复会把它成倍放大（`.{1,999}` 一项就是 2000 条指令，200 字符内写 24 个再加 `Q` 约 4.8 万条，在 4000 字符的错误上单步约 1 秒，且同步跑在 relay goroutine 上）。因此保存时限制**复杂度预算**：一条规则所有正则查找项编译后（`(?i)` + 查找，`regexp/syntax` 程序指令数）合计 ≤ 1000（`MaxRelayErrorRuleRegexSize`）。最坏情况（每个状态都活跃）实测约 5–10ns/指令/字符，整条规则在 4000 字符上约 20–50ms（`BenchmarkRelayErrorDecide_WorstRegexOnHugeBody`，i5-12600KF）；常见写法远低于此（`\(request id: [^)]*\)` 18 条、`[a-z0-9-]{3,40}` 约 90 条）。
  - **文本长度上限**：实时错误是 `err.Error()`，上游返回失败时可能带整段响应体；不设上限的话上面的耗时上限就随上游响应体变长。两个上限：
    - **关键词**看前 16 KB（`MaxRelayErrorMatchLen`，字节，按字符边界截）。关键词是普通子串查找、与文本长度成线性，所以能比局部修改看得远：关键词落在第 4000 字之后的规则照样命中（此前只看 4000 字，第 4500 字的 `replace` 规则被跳过，后面的 `keep` 规则把完整原文发了出去）。最坏情况（100 条规则 × 20 个关键词、都是 `a…ab` 这类最坏写法、一个都不命中）在 16 KB 上约 35–40ms（`TestRelayErrorDisplay_KeywordScanIsBounded`，i5-12600KF），与下面正则预算的最坏耗时同一量级；64 KB 时约 140ms，因此不取更大。
    - **局部修改**只在前 4000 个字符（`MaxRelayErrorTextLen`，与预览样例上限同一常量）上执行，结果只由这 4000 字构成（宁可截短，也不把未经处理的尾部发给用户）。
    - **截断方式**：先去掉 `(request id: …)` 再截断，截断处被切开的最后一段（到最后一个空白或 `, ; ( ) [ ] { } < > " ' | 反引号` 为止）整段去掉；截断点正好落在分隔符上时最后一段是完整的，保留（`CapRelayErrorText` / `CutRelayErrorText`）；`- _ . / : = +` 会出现在密钥、地址、ID 里，不算分隔。此前在第 4000 字处硬切：半个密钥不再匹配管理员写的正则、半个 `(request id: …` 不再被识别剥离，都会原样发出。整段没有分隔符时（一个超长 token）什么都不留，改用兜底文案。
    - **超出关键词上限的错误**：`keep` 与未命中本应原样发出，但尾部没有任何规则看过，所以改为发出规则看过的部分（前 16 KB，按上面的方式截断并去掉请求 ID），状态码不变；流式终止帧的输入被截过时（§3.3）同样处理。这类结果按"已替换"输出（重新构造为本地错误，上游的 `type` / `code` 不带出），日志注明是截断而非规则替换。16 KB 以内的错误行为不变：`keep` 与未命中照常发出完整原文。
- **替换为**：留空即删除。普通文字模式下原样替换；正则模式下可用 `$1`、`${name}` 引用分组，`$$` 输出 `$` 本身。Go 会把引用不存在分组的 `$name` 替换成空（`$5 credit` 会丢掉 `$5`），所以保存时拒绝这类引用；注意 `$1x` 在 Go 里是名为 `1x` 的分组，要写成 `${1}x`。
- **执行**：先去掉原文里所有 `(request id: …)`（我方的请求 ID 由调用方最后统一追加），再对全文按顺序执行查找项（前一项的结果是后一项的输入），每项替换全部出现处。先去请求 ID 是为了防止查找项把它改坏：例如删掉 `request id` 会剩下 `(: up-1)`，不再被识别清除，上游的请求 ID 就会漏给客户端；也让实时错误与用户日志（带我方 ID）得到同样的结果。有改动时把连续的空格或制表符合并成一个、去掉首尾空白；一项都没命中时原文原样返回。
- **长度上限**：每一步都可能让文本成倍变长（1 个字符替换成 200 个字符，或 `\b` 这类零宽匹配），执行中文本超过「原文长度 × 4 + 8 KB」即停止并改用兜底文案，保证错误路径不会被放大。
- **结果为空**（或超出长度上限）时改用兜底文案（`default_message`，留空则内置文案），不给客户端发空错误。
- 与 `replace` 一样视为"已替换"：追加我方请求 ID，错误重新构造为本地错误（上游的 `type` / `param` 不再带出），可选改状态码。
- 仍是"第一条命中生效"。想对所有错误做通用清理（例如统一删掉上游请求 ID），放一条不设任何条件的局部修改规则在最后：它接住前面没命中的错误，代替"整条换成兜底文案"。
- 上限：每条规则最多 20 项，查找与替换各 ≤ 200 字符。保存时校验：查找不能为空；正则必须能编译（RE2 语法，不支持前后查找与反向引用），且**不能匹配空字符串**（如 `a*`，否则会在每个字符间插入替换文本；`\b` 这类只在文字间零宽匹配的写法允许，由长度上限兜底）；替换里的分组引用必须存在；正则合计不超过复杂度预算。某一项无效时，保存与测试都返回具体位置：「规则 N 的第 M 项查找替换无效……」（i18n `setting.relay_error_edit_invalid`）；超出复杂度预算时指明合计越界的那一项，并提示减小重复次数或改用普通文字（i18n `setting.relay_error_edit_too_complex`）；其余问题仍是总的无效提示。库里已存的配置若有部分不再通过校验（例如预算上线前保存的超预算规则），加载时**只跳过无效的那几条规则**并记系统日志（§8），错误路径不会执行它们；其余规则、`hide_upstream_errors` 与兜底文案照常生效（不会因一条旧规则让所有用户看到上游原文）。设置页顶部列出被跳过的规则与原因（§6），管理员修改或删除后保存即可。
- 不设任何条件的局部修改规则会接住所有错误（包括上游错误），查找项都没命中时用户看到的是原始错误而不是兜底文案——这是"用通用清理代替整条兜底"的设计本意，弹窗里对这种规则显示提醒。

### 3.4 预设规则（「套用预设规则」追加到现有规则之后，管理员可改）

规则名与文案来自后端 i18n（`relay_error.preset.*`），按管理员界面语言生成：前端传 `?lang=<界面语言>`，后端没有的语言（fr/ru/ja/vi）用英文；不传时按管理员的语言设置。下表为中文版本。

| 顺序 | 名称 | 匹配 | 动作 |
|---|---|---|---|
| 1 | 参数错误放行 | 上游、状态码 400/422、关键词 `context length`/`context_length`/`maximum context`/`temperature`/`max_tokens`/`invalid` | keep |
| 2 | 上游额度与余额 | 上游、关键词 `quota`/`balance`/`余额`/`额度` | replace：`服务繁忙，请稍后重试`，状态码 503 |
| 3 | 上游限流 | 上游、状态码 429 | replace：`请求过于频繁，请稍后重试` |
| 4 | 模型不可用（错误码） | 任意来源、错误码 `model_not_found` | replace：`当前模型暂不可用，请稍后重试或更换模型` |
| 5 | 模型不可用（无可用渠道） | 任意来源、关键词 `no available channel` | 同上 |
| 6 | 超时 | 本地、错误码 `relay_timeout` | replace：`请求处理超时，请稍后重试` |

「模型不可用」拆成两条：同一条规则内条件是"且"，错误码与关键词写在一条里会变成两者都要满足。预设规则由后端 `RelayErrorPresetRules()` 提供（`GET /api/option/relay-error-display/presets`），前端不另存一份。

## 4. 数据流

```
上游错误响应 / 本地错误
  → 重试循环（shouldRetry、自动禁用渠道仍读原文，不受影响）
  → 记录错误日志（原文写库，管理员可见）
  → 【新增】交给客户端前：service.PresentRelayError(c, err)
        按配置匹配 → 改写 message/type/code/param/status
  → 写 JSON 或 SSE 终止帧给客户端

用户查询"使用日志"
  → model.formatUserLogs（按上游 formatLogOtherJSON 的用户可见性裁剪 other）
  → 【新增】错误类日志的 content 按当前配置改写后返回（读取时改写，库里保留原文）
```

读取时改写而不是写入时改写：规则调整后历史日志立即生效；原文仍在库里供管理员查。

### 4.1 用户日志的改写细节

总开关关闭时不做任何检查。兜底文案按日志所属用户的语言（每页只查一次）。

**错误日志**（`type = 错误`）：从 `other` 取 `error_type` / `error_code` / `status_code` 判定来源并匹配规则；命中替换时：
- `content` = 规则文案 + `(request id: …)`；
- 上游来源：`other.error_type` 改为 `new_api_error`、`other.error_code` 改为 `upstream_error`（`other.stream_diagnostic` 含上游原始片段，所有日志读取都经 `Find` 的 `Log.AfterFind`（`model/stream_diagnostic.go`）在进入视图前就已去掉，本功能不再处理）；
- 规则改写了状态码时，`other.status_code`（有此字段时）改为规则的状态码——客户端实际收到的就是它，上游原状态码正是改写要隐藏的信号。

**流式失败的错误日志**：受管流式请求在响应头发出后失败且不收费时，结算（`service.FinalizeConsumptionSettlement`）记一条错误日志。这类行没有 `error_type`，结算写入（以下写入与本功能开关无关，关闭时也一样：`content` 里只放我方文本、上游原文放进 `admin_info` 是日志内容本身的约定，开关只决定用户视图是否改写）：
- `content`：上游失败时是结束原因的原文（`StreamFailureLogMessage`：上游帧的 `error.message` 或我方通用提示，前面可能带我方的计费说明）+ 我方请求 ID，供管理员排障；**我方超时切断**时只写我方的超时提示（`relay.timeout` 英文，含配置的秒数）+ 请求 ID，结束原因的原文进 `other.admin_info.stream_error`（与已扣费失败的消费日志同一处理）。原因：超时行在用户视图里按本地 `relay_timeout` 判定，没有规则命中时原样显示 `content`；上游帧在截止前已读到、截止时间又在终止帧写出前触发时，客户端收到的是通用提示，而 `content` 若是上游的 `error.message`，开着"隐藏上游错误"也会原样出现在用户日志与自助导出里。Claude 严格流自行决定终止帧，结算走同一处，结果相同。
- `other.error_code` = 流式失败标记：我方超时切断为 `relay_timeout`，否则为 `upstream_stream_error`。标记与下面记录的输入同一口径（有记录时以终止帧的判定为准，截止时间晚于终止帧触发也不会让两者不一致）。
- `other.admin_info.stream_error_code` / `stream_error_message`（`operation_setting.RelayStreamMatchCodeKey` / `RelayStreamMatchMessageKey`）= 终止帧匹配时用的错误码与文案（上游帧自带的错误码，没有时为 `upstream_stream_error`；超时为 `relay_timeout`；文案已掩码并截到 4000 字，见 §3.3，因此不存凭据片段与上游地址）；文案被截过时另写 `stream_error_truncated: true`（`RelayStreamMatchTruncatedKey`），用户视图据此与终止帧一样只给出截断后的文案。终止帧不论功能是否开启都会记下这份输入，所以之后开启或改规则时历史日志也与当时的帧同一判定。放在 `admin_info` 里：上游自带的错误码不会出现在用户视图。

用户视图（`model.maskErrorLogForUser`）：按标记判定来源（`upstream_stream_error` → 上游；`relay_timeout` → 本地、状态码 504，`StreamFailureRelayErrorInput`），有记录的输入时用它的错误码、文案与"不完整"标记匹配（列表在按用户可见性裁剪 `other` **之前**读出 `admin_info`，自助导出直接读原始行），因此按上游错误码写的规则、`keep` + `upstream_stream_error`、关键词与局部修改在帧与日志上结果相同；局部修改的结果由帧的文案构成（不带日志里的计费前缀），再追加我方请求 ID。没有记录的行（此前写入的，或终止帧不是由本功能决定的，见 §4.2）按标记与整条 `content` 匹配；更早的行没有 `error_code`，按 `other.stream_result` 识别（`failed` 且非 `client_gone`），视为上游流式错误。响应头已发出，规则的状态码改写没有到达客户端，所以这类行不改写 `status_code`、不改写 `error_code`（本来就是我方的）。

**消费日志**：流式请求输出后中途出错时按已送达部分计费，记为消费日志。`content` 是我方文本、用户视图不改写：结算（与本功能开关无关）只在其后追加我方提示 + 请求 ID——被我方时限切断时是超时提示（`relay.timeout` 英文，含配置的秒数），否则是 `relay.claude_stream_failed` 英文；上游原文写入 `other.admin_info.stream_error`（`admin_info` 在用户视图整体去掉，仅管理员可见；管理员日志详情界面目前不单独展示该字段，可在日志原始数据中查看，管理员导出的「详情」会附带它，见下）。旧流式路径会把结束原因的原文写进 `other.stream_status` 的 `end_error` 与 `errors`。这两处逐条按"上游流式错误"（错误码 `upstream_stream_error`，与该流的终止帧同一输入）匹配规则并改写；`status`、`end_reason`、`error_count` 是我方的分类，保留。

**`other` 的改写方式**：只解码一层（`map[string]json.RawMessage`），只替换被改写的字段，其余字段保持原始 JSON，超出 float64 精度的整数不失真；没有任何改写时 `other` 原样返回，不重新编码。员工视图去渠道名同样如此。

管理员日志接口不经过 `formatUserLogs`，不受影响。

**自助导出**（`/api/log/self/export`）：「详情」列对错误日志做同样的改写（`model.MaskErrorLogContentForUser`），兜底文案按导出请求的语言，`admin_info` 的任何内容都不会出现。**管理员导出**保持原文，并在「详情」后追加 `; stream_error: <上游原文>`（`other.admin_info.stream_error`，即已扣费流式失败的上游原文）：旧同步导出 `/api/log/export` 用 `model.AdminLogExportContent`，导出中心的 `content` 列（导出中心仅管理员可用）同样处理，因此该列 `NeedOther: true`：勾选「详情」时查询会带上 `logs.other`。代价：只选基础列 + 「详情」的自定义模板此前不读 `other`，现在每行多读一列 `other`（一行通常几百字节到几 KB，大批量导出的读取量与内存随之增加）；已勾选任何 `other` 派生列的模板（包括默认模板与旧 14 列模板里的 `retry_chain`）本来就读 `other`，不受影响。

**渠道信息（与本功能开关无关，始终生效，与上游一致）**：
- 写入：错误日志只在 `channel_id` 列记录渠道，`other` 顶层不再写 `channel_id` / `channel_name` / `channel_type`（`controller/relay.go` `channelErrorLogOther`、`service/error.go`）。
- 普通用户的日志列表与令牌查询：`formatUserLogs` 走上游原样的 `model/log_other.go` `formatLogOtherJSON`（用户可见性），去掉 `admin_info` / `root_info` / `audit_info` 与历史敏感键 `channel_id` / `channel_name` / `channel_type` / `reject_reason`，覆盖库里的旧行。其余值按原始 JSON 透传（超出 float64 精度的整数不失真）；`other` 为空时返回空，无法解析时返回 `{}`。顶层的 `channel`（渠道编号列）与上游请求 ID 照常返回，与上游相同。
- 员工查看客户日志（`model.formatEmployeeLogs`）：去掉渠道名（列与旧行 `other` 顶层的 `channel_name`）以及仅 root / 审计可见的 `root_info`、`audit_info`（员工是普通用户账号，连管理员视图都不含 `root_info`）；其余值保持原始 JSON，没有要去掉的键时 `other` 原样返回，无法解析且含这些键时返回 `{}`（与用户视图一致，截断的行不会漏出片段）。保留渠道编号供定位，**保留 `admin_info`**：员工日志页按管理员字段展示渠道列（`admin_info.use_channel` 重试链、`channel_affinity`、`multi_key_index`，`web/src/features/usage-logs` 中 `showAdminFields` 对员工为真），这是有意的设计；错误原文对员工不经本功能改写（按设计员工看原文），`admin_info.stream_error` / `stream_error_code` / `stream_error_message` 只是同一原文（后两者还是掩码、截断后的），不额外暴露信息。
- 删除渠道后的名称解析先查 `channels`（含软删）与日聚合快照，最后才回查旧日志 `other.channel_name`，不再写入对它无影响。

### 4.2 已知限制

- **旧流式路径**：本分叉默认开启 `stream_error_setting`，流式错误统一经 `WriteStreamTerminalError` 输出并受本功能控制。若管理员关闭它，OpenAI 旧流式写出器（上游原样文件 `relay/channel/openai/relay-openai.go` 的 `sendStreamData`）会把上游错误帧原样转发，本功能管不到；此时日志里的同一段文字却会被改写。要隐藏上游错误，请保持 `stream_error_setting` 开启。
- **没有记录终止帧输入的流式失败日志**：记录（§4.1）之前写入的行，以及终止帧不是经 `PresentStreamTerminalMessage` 决定的流（适配器已自行送达错误帧、裸媒体流；Realtime 的上游错误事件经 `PresentRealtimeErrorEvent` 决定，已记录），仍按标记 `upstream_stream_error` 与整条 `content`（可能带我方计费前缀）匹配；按上游错误码或关键词写的规则可能对帧与日志命中不同的规则。不写错误码/关键词条件的规则（含"未命中即兜底"）两者结果一致。
- **实时帧不带请求 ID**：终止帧里的文案不追加请求 ID（帧格式所限），用户日志里的同一文案追加我方请求 ID。
- **修复前写入的消费日志**：已扣费的流式失败在修复前把上游原文追加在消费日志 `content` 里，这些历史行不改写（`content` 无法可靠地拆出我方部分与上游原文）。
- **兜底文案的语言来源不同**：中转接口按请求上下文取语言（用户设置 → Accept-Language），日志列表按用户保存的语言设置（没保存时为英文）。未保存语言设置的用户，接口里看到的内置文案与日志里看到的可能语言不同。只影响"留空用内置文案"的情况；自定义文案不受影响。

## 5. 接入点（尽量不改上游原样文件）

| 出口 | 文件 | 归属 | 改动 |
|---|---|---|---|
| 非流式错误 JSON（主出口，含超时、Realtime） | `controller/relay.go` 写错误的 defer | 上游文件（已有分叉改动） | `SetMessage` 前 1 行 `service.PresentRelayError`；原文已在前一行 `LogError` 记录 |
| 本地分发/鉴权错误 | `middleware/utils.go` `abortWithOpenAiMessage` | **上游原样文件** | 1 行 `service.PresentLocalRelayAbort` + import；仅 relay 路由（`RouteTagKey == "relay"`）生效，后台接口共用此函数但不改写。这是唯一碰到的上游原样文件，合并时注意 |
| 流式终止错误帧 | `service/stream_lifecycle.go` `WriteStreamTerminalError` | 本分叉 | `service.PresentStreamTerminalMessage` 决定文案；命中替换时不再逐字节转发上游错误帧，改发我方错误帧 |
| Claude 严格流 | `relay/channel/claude/strict_stream.go` | 本分叉 | 同上，上游帧与通用失败帧两条分支都走同一决策 |
| Realtime 上游错误事件 | `relay/channel/openai/managed_realtime.go` `managedRealtimeHandler` | 本分叉 | 两类上游事件写给客户端前经 `service.PresentRealtimeErrorEvent`（与终止帧同一决策 `presentStreamErrorMessage`）：可恢复的请求错误（`type:"error"` 且 `error.type` 为 `invalid_request_error`，连接继续，不记录匹配输入，功能关闭时不做任何解析）；以及观察后使会话记为 `upstream_error` 的终止错误事件——`error` 事件、状态为失败（或非限额类 `incomplete`）的 `response.done`、任何带 `error` 对象的事件（如 `conversation.item.input_audio_transcription.failed`），判定与 `relaycommon.IsStreamErrorEvent` 一致。终止错误写出后标记为已送达、不再走 `WriteStreamTerminalError`，所以在这里记下匹配输入供错误日志用（不论功能是否开启）。没有替换（或功能关闭）时原样发出。替换时：`error` 事件重建为 `{"type":"error","error":{"type","code":"upstream_stream_error","message"[,"event_id"]}}`，`type` 只保留"可恢复"分类（`invalid_request_error`，否则 `server_error`），`event_id` 只保留 `error.event_id`（客户端自己事件的 ID），上游顶层 `event_id`、`param`、原 `code` 都不带出；其他类型保持原事件的类型与结构，只把其中的错误对象（`error`、`response.error`、`response.status_details.error`）换成同样的 `{type, code, message[, event_id]}`、顶层 `message` 换成决定的文案，没有错误对象的（如仅 `status:"failed"`）本来就不含上游文本、原样发出 |
| 转流式错误帧 | `relay/upstream_stream_buffered.go` `adaptedStreamErrorFrame` | 本分叉 | message 固定为 `upstream stream error`，上游原始片段只在调用处记日志（**与开关无关，始终生效**） |
| 转流式超时 | `relay/upstream_stream_buffered.go` `writeAdaptedStreamTimeout` | 本分叉 | 走 `PresentLocalRelayAbort`（本地 `relay_timeout` / 504） |
| 用户日志 | `model/log.go` `formatUserLogs` → `maskErrorLogForUser` | 本分叉已改 | 见 §4.1 |
| 流式失败结算日志 | `service/consume_settlement.go` `FinalizeConsumptionSettlement` | 本分叉 | 错误日志写 `other.error_code`（`upstream_stream_error` / `relay_timeout`）与终止帧的匹配输入（`admin_info.stream_error_code` / `stream_error_message`，取自请求上下文）；消费日志 `content` 只追加我方提示（我方超时为超时提示），上游原文进 `admin_info.stream_error`。见 §4.1 |
| 管理员导出 | `controller/log.go` `exportLogsExcel`、`model/log_export_columns.go` `content` 列 | 本分叉 | 「详情」追加 `admin_info.stream_error`（`model.AdminLogExportContent`）；自助导出不变 |

超时不需要单独接入：文本中转的超时在 defer 里经 `normalizeRelayTimeoutError` 变成本地 `relay_timeout` 错误后，走主出口的同一次改写。Midjourney / 异步任务的超时仍在范围外。

每个 `Present*` 函数都 `recover`，panic 时原样输出；命中替换时记一条 Info 日志（规则序号、规则名、原/新状态码），本地中止路径还会把原文记入日志（调用方只记录发出的内容）。

## 6. API 与界面

- **配置读写**：走现有配置组保存 `PUT /api/option/group`（`model.SaveConfigGroup`，Rule 12，不新增保存接口），保存前校验本次改动的部分，失败返回 i18n 文案 `setting.relay_error_display_invalid`。界面只用配置组保存；单项保存 `PUT /api/option/`（`relay_error_display_setting.*` 在作用域允许的键里）同样校验：`model.validateOptionValue` 把这一项合入当前生效配置，两条路径都走 `model.validateRelayErrorDisplaySave(已存配置, 草稿)`，失败时返回相同的 i18n 文案，不落库。校验范围：兜底文案与已存的相同（去首尾空白后比较）时不校验；规则列表按内容（解析后重新编码）与已存的相同时不校验，否则已存列表里原样存在的规则换成一条恒有效的占位规则、新增或改过的规则逐条严格校验（连同条数上限），报错的规则序号仍是管理员看到的序号。这样库里有被运行时跳过的旧规则时（§8），关闭开关、改 `hide_upstream_errors` 或兜底文案都能保存（配置组保存时界面会把原规则重新序列化后一并提交，按内容比较所以仍视为未改）；新写的无效规则照样被拒。`settingsaccess` 登记作用域 `system-tuning.relay-error-display`（常量 `settingsaccess.ScopeRelayErrorDisplay`），权限资源"错误提示"（authz 排序 814）。
- **作用域**：下面三个接口都在 `RequireSystemSettingsScope(view)` 之后再检查中间件批准的作用域（`middleware.SystemSettingsScopeContextKey`）必须是本功能的作用域，或 root 不带作用域时的 `""`（与 `settingsWriteScope` 同一做法；不经中间件直接调用时只允许 root）。中间件只校验"请求里写的那个作用域"的权限，不做这一步的话，只有其他设置页查看权限的管理员带上 `scope=site.notice` 也能读到本功能的状态、预设与预览。不符时返回 `common.invalid_params`（HTTP 200，`success: false`）。
- **预览接口**：`POST /api/option/relay-error-display/preview`，挂在 option 路由组下，`RequireSystemSettingsScope(view)`。请求体 `{setting: <草稿配置>, sample: {source: upstream|local, status_code, error_code, message}}`，样例原文 ≤ 4000 字符（`MaxRelayErrorTextLen`，与局部修改的文本上限同一常量，因此预览不会遇到截断）；返回 `{replace, message, status_code, rule_index, rule_name}`。草稿按"已开启"评估（关闭时也能先验证规则），用后端同一个匹配函数，避免前端再实现一遍导致不一致。
- **预设接口**：`GET /api/option/relay-error-display/presets`，权限同上，返回 §3.4 的规则。
- **状态接口**：`GET /api/option/relay-error-display/status`，权限同上，返回 `{skipped: [{part, rule, reason}]}`——当前生效配置里因不再通过校验而被跳过的部分（§3.5、§8）。`part` 为 `rule`（单条规则，`rule` 为从 1 起的序号）、`default_message`（兜底文案超长，改用内置文案）、`rules`（规则列表无法解析，全部规则未生效）或 `rule_limit`（超过 100 条，只用前 100 条），后三者 `rule` 为 0；`reason` 按管理员语言说明发生了什么、该怎么改：查找替换项问题用 `setting.relay_error_edit_invalid` / `setting.relay_error_edit_too_complex`，其余规则问题用 `setting.relay_error_rule_skipped`，非规则部分分别用 `setting.relay_error_default_message_skipped` / `setting.relay_error_rules_unreadable` / `setting.relay_error_rules_over_limit`。全部有效时为空列表。
- **界面**：系统设置 → 运行参数，「错误提示」分区（`web/src/features/system-settings/system-tuning/relay-error-display-section.tsx`）。结构见 §6.1。七语言文案。状态接口有跳过项时，分区说明下方显示一条红色提示「部分已保存的设置无效，已被跳过」，说明已保存设置的其余部分仍然生效，并逐条列出原因（相同原因只列一次）；保存成功后重新查询。

### 6.1 界面结构

**v1 的问题**：所有内容一次铺开——两个开关、兜底文案、每条规则一张卡片（8 个输入框 + 3 个按钮）、预览区 4 个输入框。套用 6 条预设规则后页面上约 50 个输入框；"来源 / 放行 / 改状态码"等概念同时出现，看不出主次。而绝大多数管理员只需要"上游错误统一显示成一句话"。

**v2 原则**：常用的放最前面且最少；规则只显示摘要，点开再编辑；预览默认收起。后端、配置结构与接口不变，只改前端呈现。

```
错误提示
┌──────────────────────────────────────────────────────────────┐
│ 替换返回给用户的错误提示                              [开关]   │
│ 关闭时，用户看到的错误与原来完全一致。                         │
│                                                              │
│ 未命中规则的上游错误 [显示统一提示 ▾]（另一选项：显示原始错误）│
│ 上游错误的默认文案   [ 服务暂时不可用，请稍后重试        ]     │
│                   留空时按用户语言显示内置文案。               │
└──────────────────────────────────────────────────────────────┘
特殊规则                              [添加预设规则] [添加规则]
按顺序匹配，第一条命中的生效。用于给某类错误单独设置提示，
或让对用户有用的原始错误（如上下文超长）照常显示。
┌───┬──────────────────────────────┬──────────────────────┬─────────┐
│ # │ 匹配条件                      │ 处理                  │ 操作     │
├───┼──────────────────────────────┼──────────────────────┼─────────┤
│ 1 │ 上游 · 400,422 · 关键词 6 个  │ 保持原文              │ ↑ ↓ ✎ 🗑 │
│   │ 参数错误放行（备注，灰字）     │                      │         │
│ 2 │ 上游 · 关键词 quota,balance… │ 「服务繁忙…」 → 503    │ ↑ ↓ ✎ 🗑 │
│ 3 │ 上游 · 429                    │ 「请求过于频繁…」      │ ↑ ↓ ✎ 🗑 │
└───┴──────────────────────────────┴──────────────────────┴─────────┘
（无规则时：暂无规则，所有上游错误都显示上面的统一提示。）

▸ 测试一条错误（默认收起）
   错误原文 [多行文本                                          ]
   来源 [上游 ▾]   状态码 [    ]   错误码 [可选]        [测试]
   → 用户将看到：「服务繁忙，请稍后重试 (request id: …)」 · HTTP 503 · 命中规则 2
                                                            [保存]
```

- **基本设置**：`hide_upstream_errors` 不再是独立开关，改成"未命中规则的上游错误"下拉（显示统一提示 / 显示原始错误），选后者时隐藏"上游错误的默认文案"。总开关关闭时下方内容照常可编辑（便于先配好再开启），但整体降低不透明度提示未生效。
- **规则列表**：复用「渠道亲和」的 `StaticDataTable` + 编辑弹窗模式（`general/channel-affinity`）。匹配条件列用徽标显示来源、状态码、错误码、关键词（超过 3 个显示"+N"），备注以灰字显示在下一行；处理列显示"保持原文"或"文案（→ 状态码）"。上移/下移/编辑/删除为图标按钮。
- **规则编辑弹窗**：分两组。
  - 匹配条件：错误来源（上游 / 本站 / 全部）、状态码、错误码、关键词；组下方一行说明"同一项填多个值时满足任一即可；不同项需同时满足；留空的项不参与匹配"。
  - 处理方式：单选"替换提示 / 保持原文"；选"替换提示"时才显示提示文案与返回状态码（可选，留空不变）。
  - 备注放最后。校验错误在弹窗内对应字段下显示，校验不过不能确定。弹窗确定只改页面草稿，仍由底部"保存"统一提交（与渠道亲和一致）；有未保存的修改时保存按钮旁提示"有未保存的修改"。
- **测试**：默认收起；展开后输入框从 4 个减为"错误原文"必填 + 来源/状态码/错误码一行。结果显示用户将看到的文案、HTTP 状态码、命中的规则序号（点击可打开该规则的编辑弹窗）。总开关关闭时附"以上是开启并保存后的效果"。
- **保存**：规则在弹窗里逐字段校验；保存时再对全部规则（含刚追加的预设与库里已有的）按同一套校验走一遍，发现问题时提示"规则 N：原因"并直接打开该规则的弹窗。测试结果只对应算出它的那份草稿，草稿一改即清空，避免"命中规则 N"指向已被移动或改过的规则。
- **代码位置**：`relay-error-display-section.tsx`（分区）、`relay-error-rule-dialog.tsx`（编辑弹窗）、`relay-error-display-rules.ts`（规则行与配置互转、逐字段校验，与后端 `relay_error_display_setting.go` 的上限一致）。
- **不变**：后端接口、配置键、预设规则来源（后端按界面语言生成）、校验规则与上限。
- **局部修改（§3.5）**：处理方式单选增加"局部修改"。选中后显示查找/替换列表：每行"查找""替换为（留空即删除）""正则"勾选框与删除按钮，下方"添加一项"；返回状态码输入框与"替换提示"共用。前端按同样上限逐项校验：正则先把 RE2 专有写法改写成浏览器认识的形式（去掉 `(?-i)`、`(?s:` 等内联标志，`(?P<name>` 改为 `(?<name>`），依次用 `i`、`iu` 标志编译（`\p{Han}` 需要 `u`），并拒绝 RE2 不支持的前后查找（`(?=`、`(?!`、`(?<=`、`(?<!`）与反向引用（`\1`–`\9`），替换里的 `$` 引用按与后端相同的规则检查；最终以后端保存时的校验为准（后端报错会指明规则与项）。规则列表的"处理"列显示"局部修改 N 项"（及改写后的状态码）。测试区直接显示修改后的结果。

## 7. 配置参数

见 §3.1。配置键：`relay_error_display_setting.enabled` / `.hide_upstream_errors` / `.default_message` / `.rules`（JSON 字符串）。保存时校验：规则数 ≤ 100、单条规则的关键词/错误码各 ≤ 20 个且每个 ≤ 100 字符、文案 ≤ 500 字符（兜底文案同）、改写状态码在 400–599、匹配状态码在 100–599、`source` / `action` 合法、`replace` 必须有文案、`edit` 至少一项且不超过 20 项（查找非空、查找与替换各 ≤ 200 字符、正则可编译且不匹配空字符串、一条规则的正则编译后合计 ≤ 1000 条指令）；校验失败用 i18n 文案报错（Rule 9/13）。

## 8. 边界与错误处理

- 库里的配置部分无效（手工改库、旧版本、限制上线前保存的规则）：加载时只跳过无效部分——无效规则单独跳过（其余规则仍按原序号报告命中）；超过 100 条时只用前 100 条；规则列表无法解析时视为没有规则；兜底文案超长时改用内置文案。开关、`hide_upstream_errors` 照常生效；跳过项有变化时记一次系统日志（多节点定时同步配置会反复重新加载，不重复记录），并通过状态接口提示管理员（§6）。保存只校验本次改动的部分（§6 配置读写）：已存的无效部分不阻止保存其他字段——功能必须随时能关闭——但新增或修改的规则、兜底文案仍严格校验。预览仍整体校验。
- 改写函数内部 panic：`recover` 后输出原错误（Rule 0：新功能失败必须静默）。
- 流式已经输出过正文后才出错：只改写终止帧的文案，不影响已送达内容与计费。
- 状态码改写只在尚未写出响应头时生效（流式已开始则只能改帧内容）。
- 请求 ID 始终追加，便于用户报障。

## 9. Main Chain Impact

- **同步部分**：只在"请求失败、准备写错误"时执行一次：读配置快照（原子指针）+ 按顺序做字符串包含匹配；命中局部修改规则时再对错误原文执行至多 20 次替换。查找项在配置保存时预编译进快照，错误路径上不编译；关键词只看原文前 16 KB（小写化一次，逐个子串查找，最坏约 35–40ms，§3.5），局部修改只看前 4000 字符；普通文字查找为线性子串查找，RE2 匹配耗时与文本长度成线性，每字符的系数由保存时的复杂度预算（§3.5，一条规则正则合计 ≤ 1000 条指令）封顶，因此最坏规则的耗时与上游响应体大小无关。流式终止帧（仅失败路径）无论功能是否开启都解析一次上游错误帧（gjson，小 JSON），在至多 16 KB 上掩码（与日志 content 相同的 `MaskSensitiveInfo`）后截到 4000 字，再把匹配输入存入请求上下文（`c.Set`），结算时写进错误日志的 `admin_info`；Realtime 的上游 `error` 事件同样如此（可恢复的请求错误在功能关闭时不做任何处理），其余 Realtime 事件只多一次类型比较；流式失败结算多写两三个 `other` 字段并读一次超时标记（上下文取值），无外部调用。成功请求零开销。
- **异步部分**：无。
- **共享资源**：只读 ConfigManager 快照；不访问 Redis/DB；请求上下文键 `relay_error_display_stream_input`（每请求私有）；错误日志 `other.admin_info` 多两个字段（经既有异步日志管线写入）；用户日志改写与导出发生在日志查询/导出接口（非 relay 路径）。
- **并发分析（100k RPM）**：即使 10% 请求失败，每秒约 167 次规则匹配，单次为微秒级字符串比较，无锁、无外部调用。
- **实测（2026-09-28）**：基准（`relay_error_display_bench_test.go`，16 核）——兜底/整条替换 0.18µs、1 次分配；局部修改 3 项（文字或正则）约 7µs、约 20 次分配；20 项常见正则 × 4000 字符上游原文 1.75ms、128KB（2026-09-29 复测 2.3ms）；20 项普通文字最坏写法（`a…ab` × 4000 个 a）0.4ms；预算内最坏正则规则（每个状态都活跃）在 1MB 错误上（截到 4000 字）约 46ms。按 10% 失败率 167 次/秒计，常见规则的开销可忽略；最坏正则规则只有管理员刻意写出才会出现，且耗时已与原文长度无关。香港测试服（2 核）100 请求/秒的错误路径压测：开启两条局部修改规则时每请求网关 CPU 4.76/4.87ms，关闭时 4.61ms（差值约 0.2ms，含每次替换多写的两行日志），p99 延迟无系统性差异。

## 10. 附带发现（本方案不处理，单独列出供决策）

1. **成功响应里的上游痕迹**：本分叉返回给客户端的 `usage` 里会带内部计费结构，如 `billing_usage.source: "gemini_chat"`、`claude_cache_creation_1_h_tokens`、`input_tokens` 等（真实上游测试中，上游 nexaxis 同样如此）。这会暴露上游是 Gemini/Claude。是否在返回给客户端时剔除这些非标准字段，需要单独决定（涉及上游原样的响应处理代码）。
2. **Azure 遥测字段**：上游透传的 `routing`、`usage.latency_checkpoint` 会暴露上游是 Azure 及其机房副本名；同上，需要单独决定是否剔除。

## 11. 测试设计（先写测试）

- 匹配函数：来源判定（上游各构造方式 / 本地各错误码）、每种条件单独命中与不命中、多条件"且"、多值"或"、规则顺序（第一条生效）、`keep` 放行、兜底开关、关闭总开关时逐字不变。
- 局部修改：普通文字不区分大小写且特殊字符按字面匹配、正则与分组引用、按顺序执行（后一项作用于前一项的结果）、删除后空白合并、未命中时原文不变、结果为空时用兜底文案、长度放大被截止、先去请求 ID 再编辑（实时、用户日志、流式帧）、改状态码、用户日志与流式终止帧同样生效；校验：无查找项、空查找、超长、超过 20 项、正则编译失败、正则匹配空字符串、引用不存在的分组（`$5`、`$1x`、`${2}`），错误指明规则与项（预览与保存接口）；`PresentLocalRelayAbort` 内部 panic 时返回原始状态码与文案（此前返回零值，会变成 HTTP 200）。
- 改写结果：message/type/code/param/metadata/状态码在 OpenAI、Claude 两种格式下的输出；请求 ID 追加。
- 流式：上游错误帧在开启时不再逐字节转发；转流式错误帧不含上游原始片段。
- 用户日志：错误类 content 改写、管理员接口不改写；消费日志 content 不改写，`stream_status` 的 `end_error`/`errors` 按规则改写；总开关关闭时不查用户语言。流式失败错误日志（新行带 `error_code`、旧行只有 `stream_result`）与终止帧同一判定：上游 / 我方超时（504）、用户断开与自带错误类型的行不算；规则改写状态码时 `status_code` 随之改写（流式失败行除外）；改写后其余 `other` 值（含 > 2^53 的整数）保持原始 JSON，员工视图去渠道名同样。结算端：零收费失败的错误日志 `error_code`、已扣费失败的消费日志 content 只含我方文本且上游原文在 `admin_info.stream_error`（`service` `TestStreamFailureMessagesReachSettlementLogs`）。
- 终止帧与日志同一输入（`service` `relay_error_stream_match_test.go`、`model` `log_error_display_stream_match_test.go`）：各帧形状的错误码/文案提取（`code:null` 视为无错误码、Responses 顶层 `code` 与 `response.error`）；记录的文案已掩码；我方超时后不原样转发上游帧；按上游帧错误码写的规则、`keep` + `upstream_stream_error`、局部修改在实时帧与用户日志（列表与自助导出）结果相同；记录的输入只在 `admin_info`，关闭功能时也不出现在用户视图；帧没有错误码时记 `upstream_stream_error`；截止时间晚于终止帧触发时以帧的判定为准；旧行回退到标记 + content；`storedStreamTerminalInput` 各分支。已扣费失败：我方超时写超时提示，上游失败写通用提示。
- 管理员导出：旧同步导出与导出中心 `content` 列追加 `admin_info.stream_error`，自助导出不含（`controller` `TestExportLogs_StreamErrorOnlyInAdminExport`、`model` `TestAdminLogExportContent`）。
- 正则复杂度预算：恰好等于预算通过、多 1 条指令拒绝；两项各自不超但合计超出时指明第二项；预算按规则计；普通文字查找不计入且为线性（20 项 `a…ab` 在 4000 个 a 上 < 20ms）；普通文字替换与 `(?i)` 转义正则逐字节一致（含开尔文符号、长 s、希腊 sigma、非法 UTF-8 字节的随机对比）；常见写法通过；预算内最坏规则在 1MB 错误上 < 250ms（文本截断生效）；第 4000 字之后、16 KB 以内的关键词照样命中，局部修改结果为截断后的文本且不含被切开的最后一段（半个密钥、半个请求 ID）；16 KB 之外 `keep` 与未命中发出规则看过的部分（`TestRelayErrorDisplay_TextPastTheMatchBoundIsNotSent`）；100 条 × 20 个最坏关键词在 16 KB 上 < 250ms（`TestRelayErrorDisplay_KeywordScanIsBounded`）；流式终止帧文案先掩码后截断（`TestStreamTerminalRelayErrorInputMasksBeforeCapping`），截过的输入记入日志并在用户视图同样只给出截断后的文案；保存与预览返回 `setting.relay_error_edit_too_complex`。
- 多语言：留空的兜底文案按用户语言、预设规则按界面语言（`?lang=`，不支持的语言回退英文）、i18n 未加载时回退内置英文句子而不是消息键。
- 配置校验边界值；库里的配置部分无效时只跳过无效部分（无效规则、超出 100 条、规则列表无法解析、兜底文案超长），兜底隐藏照常生效，命中序号仍按存储位置；状态接口列出跳过项及原因（`controller` `TestGetRelayErrorDisplayStatus`）。
- 截止时间在上游错误帧之后、终止帧之前触发：错误日志 `content` 为我方超时提示、上游原文只在 `admin_info.stream_error`，用户视图（开启隐藏上游错误）不含上游原文，受管流与 Claude 严格流两条路径（`service` `TestStreamFailureLogTimeoutAfterUpstreamFrameKeepsUpstreamTextAdminOnly`）。
- Realtime：上游可恢复与结束本轮的 `error` 事件按配置替换、形状仍为合法 Realtime 错误事件、保留可恢复分类与客户端 `event_id`；`keep` 原样、局部修改生效；功能关闭时逐字节原样；只有结束本轮的错误记下匹配输入（`relay/channel/openai` `realtime_error_display_test.go`）。
- 作用域：状态/预设/预览三个接口只服务本功能作用域或 root 的 `""`（`controller` `TestRelayErrorDisplayHandlersRequireTheirOwnScope`；经真实路由与鉴权、权限中间件：`router` `TestRelayErrorDisplayRoutesServeOnlyTheirOwnScope`，SQLite 内存库）。
- 单项保存：`relay_error_display_setting.*` 经 `PUT /api/option/` 保存时合并校验，无效规则被拒且不落库（`model` `TestValidateOptionValue_RelayErrorDisplay`、`controller` `TestUpdateOption_InvalidRelayErrorRules`）。
- 库里有被跳过的旧规则时两个保存接口都能关闭功能、改其他字段，改动的规则仍严格校验且报错序号正确（`model` `TestValidateOptionValue_RelayErrorDisplayStaleStoredRule`、`TestValidateOptionValue_RelayErrorDisplayStaleDefaultMessage`、`TestRelayErrorDisplayRulesToCheck`、`TestSaveConfigGroup_RelayErrorDisplayStaleRuleCanBeSwitchedOff`；`controller` `TestUpdateOption_RelayErrorDisplayCanBeSwitchedOffWithStaleRule`、`TestUpdateOptionGroup_RelayErrorDisplayCanBeSwitchedOffWithStaleRule`）。
- 员工视图：去掉 `channel_name` / `root_info` / `audit_info`，保留 `admin_info` 与原文，其余值原始 JSON，无法解析且含这些键时为 `{}`（`model` `TestBranchAuditFormatEmployeeLogs_Contract`）。
- 真实上游回归：复用本次 nexaxis 测试程序，覆盖 §2 的 9 个泄露点，开启前后各跑一次对比。

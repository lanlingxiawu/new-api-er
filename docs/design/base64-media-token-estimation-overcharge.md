# 文本通道中的 base64 媒体导致 token 估算暴涨

状态：阶段一已实现并通过测试（2026-09-20）；后续项见文末。

## 背景与结论

一次 Gemini 图像生成请求经 `/v1/chat/completions` 转换下发，流中断后按本地估算计费，记为
**1,299,013 token / $155.8834**。正确口径应为每张图 1,290–1,400 token，放大约 **570–600 倍**。

根因不是"Gemini 模型"，也不是"流中断"。必要条件只有两条，任意组合成立即触发：

- **A. 图片/媒体以 base64 data URL 的形态进入了文本通道**（`delta.content` / `message.content`）；
- **B. 该请求走到了任意一条"按文本估算用量"的分支**。

系统既有原则是"不把媒体字节冒充 token"（见 `relay/common/stream_session.go:1126` 的
`CommitMediaDelivery` 注释，媒体只累计 `MediaBytes`）。本问题正是媒体绕过该原则、
混入文本统计通道后，让这条原则失效。

### 实测放大倍数

用仓库现有估算器对随机字节的标准 base64 编码实测（`service.EstimateToken`）：

| 原始大小 | base64 字符数 | Gemini | Claude | OpenAI |
|---|---|---|---|---|
| 1 KiB | 1,368 | 820 tok（0.599/字符） | 596（0.436） | 551（0.403） |
| 1 MiB | 1,398,104 | 794,611 tok（0.568/字符） | 576,729（0.413） | 532,928（0.381） |

按 Gemini 权重反推，1,299,013 token ≈ 2.29 MB base64 ≈ **1.72 MB 原图**，与事故量级吻合。

注意 OpenAI 文本模型走的是 tiktoken 真分词（`service/token_counter.go:405`
`CountTextToken` 的 `IsOpenAITextModel` 分支），base64 在 tiktoken 下约
0.25–0.33 token/字符，同样是数十万量级——**换用真分词并不能规避本问题**。

---

## A. 文本流里出现 base64 的来源

| 来源 | 位置 | 是否触发 |
|---|---|---|
| Gemini→OpenAI **流式**转换：图片 → `![image](data:...)` | `relaykit/relayconvert/internal/gemini_chat/to_oai_chat_resp.go:235` | 是（本次案例） |
| Gemini→OpenAI **非流式**转换：图片 → `![image](data:...)` | 同文件 `:121` | 是 |
| Gemini→OpenAI **非流式**转换：非图片媒体 → `[media](data:...)`（音频/视频） | 同文件 `:128` | 是（流式分支反而只处理图片，非图片 `InlineData` 被丢弃） |
| **上游自己**在 `delta.content` 里回 data URL | `relay/common/stream_session.go:932-936` 提取 `delta.content` / `message.content` / `delta.reasoning_content` 并写入 `s.text` | 是；任何 OpenAI 兼容渠道（对接的上游 new-api、聚合网关、自建图片网关）都可能这么干，与本地转不转换无关 |
| 客户端把上一轮含 data URL 的助手消息回传（**输入侧**） | `relaykit/dto/openai_request.go:249` 文本 part → `:273` `CombineText` → `service/token_counter.go:231` | 是，见 B-6 |

**不触发的路径**（作为对照，修复时不要动）：

- 渠道适配器自身的 Gemini 计量：`relay/channel/gemini/relay-gemini.go:204-227` 只累计
  `part.Text`，图片走 `imageCount * 1400`（`:227` 与 `:55`），非流式同理
  （`geminiResponseUsageText`，`:68-81`）。注意这份正确计量在流式失败时会被
  `service/stream_lifecycle.go:181-194` 的 `UsageSource == "estimated"` 分支整体丢弃，
  改用交付文本估算——所以"适配器算得对"并不等于"最终收费对"，见 §归属。
- `/v1/images/*`（`b64_json`）：`relay/common/stream_image_json.go:99` 记 `image_count`，
  由 `BuildConfirmedStreamUsage`（`service/stream_lifecycle.go:213`）按张计价。
- **请求方向**的 data URL 拼装（`claude_messages/to_oai_chat_req.go:180`、
  `to_oai_responses_req.go:325`、`gemini_chat/to_oai_chat_req.go:84`）：产物落在结构化
  `image_url.url` 字段，在 `openai_request.go:231` 走 `ToFileSource()` 分支，不进
  `CombineText`，按图片口径计。安全。

结论：**"Gemini 模型"不是条件，"图片经由文本通道下发"才是**。同一模型走原生协议不会出问题；
一旦从 `/v1/messages` 或 `/v1/chat/completions` 走转换就会。

---

## B. 会触发"按文本估算用量"的分支

| # | 触发条件 | 代码 | 需要中断吗 |
|---|---|---|---|
| 1 | 上游协议未完成 / EOF，`UsageSource == "estimated"` | `service/stream_lifecycle.go:176-183`，`CompletionTokens = snapshot.EstimatedOutput` | 是（本次案例） |
| 2 | 客户端断开 | 同上 `:184-186`，改用 `snapshot.ReceivedOutput` | 是 |
| 3 | **流正常结束，但上游没回 usage** | `service/usage_helpr.go:34` `ResponseText2UsageFromStream` → `:22` `ResponseText2Usage`；调用方 `relay/channel/openai/relay-openai.go:199`、`openai/chat_via_responses.go:307`、`claude/relay-claude.go:268`、`cohere`、`coze`、`dify`、`tencent`、`xai`、`cloudflare` | **否** |
| 4 | **流正常结束，上游 usage 的 completion = 0** | `service/stream_lifecycle.go:200-204` → `service/stream_zero_output.go:13` `SupplementStreamZeroOutput`；非 realtime **无条件调用** | **否**；不少图片模型上游就回 0 |
| 5 | **非流式无 usage** | `relay/channel/openai/responses_via_chat.go:173`、`openai/chat_via_responses.go:152`、`openai/relay-openai.go:328`、`service/responses_usage.go:81` | **否** |
| 6 | **输入侧**：客户端回传含 data URL 的历史消息 | `relaykit/dto/openai_request.go:249,273` → `service/token_counter.go:231` → `relay/request_billing.go:51,55` `SetEstimatePromptTokens` | 否 |

第 3、4、5 条意味着：**即使请求完全成功，只要上游 usage 缺失或为 0，同样会按几十万 token 收费**。
本次事故是第 1 条，但这三条的触发面更大。

第 6 条的危害被低估：估出的 prompt token 直接喂给**预扣费**（`relay/request_billing.go:55`
→ `service/billing.go:20` `PreConsumeBilling`）。因此一个"上一轮返回了图片"的多轮对话，
**下一轮请求会在预扣阶段就被判为额度不足而拒绝**，即便最终结算本可拿到上游真实 usage。
这是一个可用性故障，不只是多收费。

已核实状态：1、2 与事故日志数值对齐；3/4/5/6 由代码路径读出（上表行号已逐条核对），
**尚未实跑复现**——实现前先按 §"测试设计"写回归用例坐实。

---

## 归属：main 原有，还是本分支引入

比较基线：`upstream/main`（官方 QuantumNous/new-api，核对时 `972aed1972`）。
本地 `main`（`69a5002981`）是 `upstream/main` 的严格祖先，纯镜像，二者判定一致。

**与上游逐字节相同（`diff <(git show upstream/main:<p>) <p>` 为空）**：
`service/token_estimator.go`、`service/token_counter.go`、`relaykit/dto/openai_request.go`、
`relaykit/relayconvert/internal/gemini_chat/to_oai_chat_resp.go`。
即"base64 按 0.38–0.60 tok/字符计"和"转换层把图片塞进文本通道"都是上游原生行为。

**分支独有（`main` 中不存在该文件）**：`relay/common/stream_session.go`、
`relay/common/stream_writer.go`、`relay/common/stream_image_json.go`、
`service/stream_lifecycle.go`、`service/stream_zero_output.go`、
`service/stream_token_estimator.go`。

| 触发行 | 归属 | 依据 |
|---|---|---|
| A 转换层产生 data URL | main 原有 | 转换文件与上游相同 |
| A 上游自己在 `delta.content` 回 data URL | main 原有 | 上游 `relay/channel/openai/helper.go:123-126` → `relay-openai.go:184` `if !containStreamUsage { ResponseText2Usage(...) }` |
| A `stream_session.go:932` 提取下游文本 | **本分支** | 文件不在 main |
| B-1 中断按 `EstimatedOutput` 计 | **本分支** | `service/stream_lifecycle.go` 不在 main |
| B-2 客户端断开按 `ReceivedOutput` 计 | **本分支** | 同上 |
| B-3 正常结束无 usage | main 原有 | `service/usage_helpr.go:22` 与上游相同；分支只加了更安全的 `ResponseText2UsageFromStream`（无 SSE 时返回空 usage） |
| B-4 `completion = 0` 无条件补估 | **本分支** | `service/stream_zero_output.go` 不在 main；上游只有按渠道的 `patchGeminiZeroCompletionUsage`（只用 `part.Text`）与 claude 版 |
| B-5 非流式无 usage | main 原有 | 上游 `relay-openai.go:305-311` `CountTextToken(choice.Message.StringContent()+…)` |
| B-6 输入侧回传 data URL | main 原有 | 整条链与上游相同 |

**事故那次（570–600 倍）是本分支引入的**：`main` 的 Gemini 渠道无论流式还是非流式都只把
`part.Text` 计入估算，图片按 `imageCount * 1400`，转换出的 `![image](data:...)` 只写给客户端；
本分支在 `service/stream_lifecycle.go:91` 把 `c.Writer` 统一换成 `StreamWriter`，
由 `relay/common/stream_writer.go:179-190` 从**已写给客户端的下游字节**取文本
（`stream_session.go:932`）喂给估算器，失败分支再丢弃适配器算好的 usage
（`stream_lifecycle.go:181-194`），于是上游安全的路径被旁路。

---

## 目标与范围

**目标**

1. 估算路径不得把 base64 媒体正文当作文本计费；
2. 被剥离的媒体按图片口径折算，永不为 0——丢弃会从过收直接变成漏收；
3. 修复后任何路径的收费都不低于 `main` 在同场景下的**正确**收费（见 §不少收不变量）。

**范围内（阶段一，本次实施）**

| 文件 | 改动 |
|---|---|
| `service/media_data_url.go`（新） | `DataURLScrubber` 跨分片增量剥离状态机 |
| `service/stream_token_estimator.go` | `Add` 先过剥离器，返回值改为「剥离后文本估算 + 媒体折算」 |
| `service/media_data_url_test.go`（新）、`service/stream_media_estimation_test.go`（新） | 见 §测试设计 |

**不新增配置项**：折算单价与阈值固定为 `service/media_data_url.go` 的常量。理由是配置面已经很大，
而这两个值属于估算兜底口径、不需要按站点调；代价是没有运行时开关，回滚手段为回退二进制。

**范围外**

- 不改转换层输出形态（`![image](data:...)` 是 OpenAI 生态既成约定，改了会破坏客户端渲染）；
- 不改渠道适配器、结算分支、`Snapshot`/`Evidence` 结构、`SupplementStreamZeroOutput`；
- 不改原生 Gemini 与 `/v1/images/*` 的既有计价；不改任何 API 契约；
- 上游原有的 B-3 / B-5 / B-6 三条入口本次不动，见 §后续项。

---

## 方案（阶段一）：在流式估算器内剥离并折算

三条本地估算来源已经收口在同一个函数，因此只需一处改动：

| 估算来源 | 入口 |
|---|---|
| 交付侧 `EstimatedOutput`（B-1、B-4） | `service/stream_lifecycle.go:91` → `estimator.Add` |
| 接收侧 `ReceivedOutput`（B-2，客户端断开口径） | `service/stream_token_estimator.go:16` → 同一个 `Add` |
| Realtime | `relay/channel/openai/managed_realtime.go:358` → 同一个 `Add` |

### 剥离器

```go
// DataURLScrubber 是跨分片的增量剥离状态机，零值可用。
// Feed 返回应计费的普通文本与本批确认剥离的媒体数量；base64 正文不出现在返回值里。
type DataURLScrubber struct {
    // 全部字段私有：text / header / body / drop 四态 + 两段有界缓冲
}
func (s *DataURLScrubber) Feed(chunk string) (clean string, media int)
```

分片不含 `d`/`D` 时 `Feed` 直接返回原字符串，不进状态机、不分配。

**识别规则**

- 起点：字面量 `data:`（大小写不敏感），其后至 `,` 之间必须出现 `;base64`；
  `data:text/plain,hello` 这类非 base64 data URL 不剥离。
- 终点：`)`、空白（含换行）、`"`、`'`、`` ` ``、`<`、`]` 中任一，或文本结束。
  markdown 形态 `![image](data:...)` 由 `)` 闭合。
- 阈值：base64 段达到 `DataURLMinBase64Len`（常量 256）才判定为媒体；未达阈值的短段
  （内联图标、1×1 占位图）按原文计入文本，避免把几十字符的 svg 图标高估成 1400 token。
- 媒体在**达到阈值那一刻**就计数，不等终止符——流中断在 base64 中途时张数不会丢。
- 头部候选超过 128 字节仍未出现 `;base64,` 时判定为普通文本整体放行，
  防止构造超长 `data:` 前缀逃费。

### 媒体折算

每个确认剥离的 data URL 折算 `DataURLMediaTokens`（常量 1400），与
`relay/channel/gemini/relay-gemini.go:55` 的 `imageCount * 1400` 同源。各厂商暂不分别取值：
这是 fallback 估算而非确认用量，统一常数可审计性更好，且统一取 1400 偏向不漏收一侧。

### 为什么不需要改结算层

媒体在 `Add` 内就折成 token 返回，`EstimatedOutput` / `ReceivedOutput` 天然等于
「剥离后文本估算 + 张数 × 1400」，因此：

- `service/stream_lifecycle.go:181-194` 不用改，也就不需要给 `StreamSnapshot` 加字段——
  新增 `Evidence` 键会让 `:123` 的 `confirmed = len(Evidence) > 0` 与 `SelectUsageSource()`
  改判，属于 Rule 0 禁止的行为漂移；
- `service/stream_zero_output.go` 不用改，它拿的就是这两个值；
- 不会双收：`stream_lifecycle.go:186-190` 的按张 `AddOtherRatio("n", …)` 只在
  `RelayModeImagesGenerations/Edits` 触发，而那条路的张数来自
  `relay/common/stream_image_json.go:99` 的 `image_count`，不经文本通道；
- `UsePrice`（按次计价）时 token 不进费用公式，折算只影响日志数字，不影响账。

### 边界情况

| 情况 | 处理 |
|---|---|
| data URL 被 SSE 分片从中间切开 | 状态机跨批续接；未确认的头部/短正文既不计费也不丢失 |
| 流中断在 base64 中途，data URL 未闭合 | 已达阈值即已计 1 张，张数不丢 |
| 一帧内多个 data URL | 各自独立计数 |
| 头部候选满 128 字节仍无 `;base64,` | 判定普通文本，整体放行计费 |
| 文本中混有正常内容与 data URL | 仅剥离 data URL 区间；剥离时重置词类状态 `word = 0`，不让剥离区两侧的字符被当作同一个词 |
| 上游真实回了以 `data:` 开头的纯文本讨论 | 无 `;base64,` 不剥离 |
| 文本在未决状态下结束（末尾是 `data:` 的真前缀，或未达阈值的短 base64） | 该段不计费，上限：文本状态 ≤4 字节、头部状态 ≤129 字节、正文状态 ≤ `DataURLMinBase64Len`+129 字节。有意为之：估算器没有流结束信号，加 `Flush` 需要改 `StreamWriter` 与结算层，代价大于这点误差；反向（乐观计费再回退）会破坏 `AddEstimatedOutput` 的单调增量契约 |
| 普通文本恰好以 `d` / `da` / `dat` / `data` 结尾 | 该尾巴留到下一批再判定；流内无损，仅流末尾最多 4 字节不计费 |

---

## 不少收不变量（upstream parity）

判据取「不低于 `main` 在同场景下的**正确**收费」：`Estimate(part.Text)`、`imageCount * 1400`、
按次计价的张数。不取「不低于 `main` 的实际收费」——B-3/B-5/B-6 在 main 上本就是错误过收，
以它为下限等于把 bug 固化。

估算路径的输出量：

```
completion = Estimate(strip(text)) + media × DataURLMediaTokens
```

`main` 的正确值是 `max(Estimate(part.Text), 张数 × 1400)`（二者取一，有文本时图片其实免费），
新值是二者相加，故新值恒 ≥ main 正确值。

两条结构性保证，使"剥离 ⇒ 漏收"无法出现：

- **剥离与计数同时发生**：未达阈值的 base64 段根本不剥离，整段按原文计费；达到阈值的那一刻
  立即计 1 张。因此不存在"剥离了但张数为 0"的中间状态。
- **折算单价是常量**：`DataURLMediaTokens = 1400` 不可运行时修改，不存在"配置写坏把媒体算成
  免费输出"的路径；要回到旧行为只能回退二进制。

| 路径 | main 正确值 | 新值 | 关系 |
|---|---|---|---|
| Gemini 流中断 + n 张图 | `max(Est(part.Text), n×1400)` | `Est(part.Text) + n×1400` | ≥ |
| Gemini 正常结束无 usage | 同上 | 同上 | ≥ |
| 上游 `completion = 0` | main 保留 0 | 剥离文本 + `n×1400` | ≥ |
| 图片入口按次计价 | 按张 | 按张（不变） | = |
| 无 `data:` 的普通请求 | — | 逐字节不变 | = |
| OpenAI 兼容渠道回 data URL 且无 usage | base64 巨额（**main 的 bug**） | 本次不改（见 §后续项） | 不变 |

### 上游若是未改动的 main，会不会少收我们的成本

不会，前提是能拿到上游回报的 usage，而默认配置下成立：

- `main` 计费用的 usage 与它回报给我们的 usage 是同一个对象
  （`relay-openai.go:184` → `helper.go:231 GenerateFinalUsageResponse`；gemini 走
  `relay-gemini.go:363`）。拿到 usage chunk 即成为确认用量，我们根本不进估算分支；
- 非流式永不分歧：`main` 在 `relay-openai.go:305-322` 把 fallback usage 回写进响应体；
- 我们默认强制索要 usage：`FORCE_STREAM_OPTION` 默认 true（`common/init.go:175`），
  `ChannelTypeNewAPI(61)` / `Sub2API(60)` 都在 `streamSupportedChannels`
  （`relay/common/relay_info.go:482-506`），`relay/compatible_handler.go:57-66` 强制
  `stream_options.include_usage = true`。

唯一漏收窗口需要三件事同时成立：中间跳跑 main 且**它自己**也按 base64 估算收了我们的钱、
那次流在中间跳**中断**（正常结束时它会把巨额 usage 报给我们，我们原样传导仍对齐）、
我们这一跳因此只能自估。放大该窗口的配置是 body 透传
（`relay/compatible_handler.go:100-111`，全局 `PassThroughRequestEnabled` 或渠道
`PassThroughBodyEnabled`），它会让强制 include_usage 失效——列入 §后续项。

---

## 配置（Rule 12）

**不新增任何配置项。** 折算单价 `DataURLMediaTokens = 1400` 与阈值
`DataURLMinBase64Len = 256` 是 `service/media_data_url.go` 里的常量，没有运行时开关。

取舍：Rule 0 要求 relay 相关改动可瞬时关闭，本方案不满足该条，回滚手段是回退二进制。
换来的是配置面不再扩大，且消除了"折算单价被误配成 0 导致媒体免费"这一类误配风险。
若将来确有按站点调价的需求，再按 Rule 12 补一个只含单价的配置项即可，行为不变。

## 数据模型与 API

无新表、无迁移、无索引、无新端点、无权限变化。

## 错误处理（Rule 9/10）

剥离器不产生错误路径：任何无法解析的输入都退化为「不剥离、按原文估算」，即当前行为。
不记录 base64 正文本身。

---

## Main Chain Impact

**同步执行**（relay goroutine 内）：在已有的逐字符估算循环之前加一次同阶的字节扫描（O(n)）。
无新网络调用、无 DB、无 Redis、无锁、无新协程。

**异步执行**：无新增异步工作。

**降级**：无运行时开关；异常时回退到上一版二进制（服务器部署脚本每次都备份旧二进制）。

**实际是净减负**：剥离后进入权重循环的文本显著变短，含图请求的 CPU 开销**下降**。

## Shared Resource Audit

| 资源 | 本方案访问 | 主链是否同时访问 | 结论 |
|---|---|---|---|
| Redis | 否 | — | 无冲突 |
| DB 表 | 否 | — | 无冲突 |
| 内存结构 | `DataURLScrubber` 是 `StreamTokenEstimator` 的字段，每次尝试/轮次新建；缓冲上限 384 字节 | 否 | 无共享，无锁 |
| 连接池 / 协程池 | 否 | — | 无冲突 |
| 配置快照 | 不访问（参数为编译期常量） | — | 无冲突 |

## Concurrency Analysis

100k RPM ≈ 1,667 req/s。每请求新增：DB 0、Redis 0、协程 0、锁 0。
CPU 为一次字节扫描，含图请求净减少（跳过数十万字符的权重循环）。
内存每次尝试增加上限 384 字节缓冲；无 data URL 时 `Feed` 返回原 chunk 子串，不复制。

---

## 测试设计（Rule 15.2，测试先行）

**`service/media_data_url_test.go`**（新）——等价类 + 边界值：

- 无 `data:` / 有 `data:` 无 `;base64,` / 标准 markdown 图片 / `[media](...)` 形态 / 裸 data URL；
- 终止符逐个覆盖：`)`、空格、`\n`、`"`、`'`、`` ` ``、`<`、`]`、文本结束；
- 阈值边界：base64 段长 255 / 256 / 257；
- 一帧多个 data URL；data URL 紧邻普通文本两侧；
- 增量：在 `data`、`data:`、`;base6`、`;base64,`、base64 正文中间、终止符前各切一刀；
- 头部缓冲溢出：200 字节 `data:` 前缀不含 `;base64,`，断言整体计费；
- 未闭合 data URL（模拟流中断）按 1 张计。

**`service/stream_media_estimation_test.go`**（新）：

- 1 MiB 图片的 `![image](data:image/png;base64,…)` 经 `Add`，断言结果落在
  `1400 + 上下文文本` 区间而非数十万；分片切割位置遍历，结果与一次性输入一致；
- upstream parity：同一输入断言 `got >= Estimate(part.Text)` 且 `got >= 张数 × 1400`；
- 内联小图标（120 字符 base64）仍按原文计费，不被高估成一张图；
- 零变化对照：不含 `data:` 的普通文本与 `data:` 纯讨论文本，断言与 `EstimateTokenByModel`
  完全相等；已有的 `TestStreamTokenEstimatorBoundaries` 同样锁定这一点，不得改动其断言。

全部测试 < 1s、无外部 I/O。

**实跑结果（2026-09-20）**：`TEST_DB_CLEANUP=true go test ./... -p 1` 通过，
仅剩两批与本改动无关的既有失败——`model` 的 3 个上游会话用例（开发库 root 的
`aff_code=''` 撞 `idx_users_aff_code`，数据问题）与 `relay/channel` 的 HTTP2
`TestUpstreamGetBody_*`（Windows 偶发；已在 stash 后的干净 HEAD 上复现，非本次引入）。

---

## 线上链路实测（2026-09-20，香港测试机 43.251.102.17）

拓扑：客户端 → **f0b+修复**（`:3002`，`v1.0.0-rc.23-232-g718df38976-b64fix`）
→ **未修改 main**（`:3005`，`main-69a5002981-unmodified`，独立 SQLite，不共用 MySQL/PG/Redis）
→ **nexaxis.ai**（账号 test 的 key，分组 Gemini）→ 真实 Gemini。
模型 `gemini-2.5-flash-image`，单张图上游确认用量 `completion≈1290–1301`。

| 场景 | 剥离 | f0b 记 completion | f0b quota | main 记 completion（我们的成本） |
|---|---|---|---|---|
| 正常流式（有 usage） | 剥离生效 | **1,298** | 194,709 | 1,298 |
| **客户端中途断开**（B-2） | 剥离生效 | **1,415** | 212,276 | 1,291 |
| **客户端中途断开**（B-2） | 关闭剥离（修复前对照） | **1,012,757** | 151,913,576 | 1,301 |
| 上游不回 usage、正常结束（B-3，本次未修） | 剥离生效 | **1,007,403** | 151,110,480 | **1,007,403** |

结论：

1. **B-2 修复生效**：同一链路上切换剥离，1,012,757 → 1,415（**716 倍**）。
   （对照组是用当时还存在的临时开关测得；开关随后按"不扩大配置面"的决定删除，
   数值本身不受影响——它等价于修复前的代码行为。）
   1,415 = 1,400 媒体折算 + 15 剥离后文本，日志 `admin_info.local_count_tokens=true`
   证实走的是估算路径。
2. **不少收**：修复后的 1,415 高于 main 对同一请求收我们的 1,291，符合 §不少收不变量。
3. **确认用量路径零变化**：有 usage 时两侧都是 1,298，与修复前一致。
4. **B-3 仍暴露**（§后续项 1）：上游显式 `include_usage:false` 时两跳都按 base64 估算，
   f0b 与 main 记的完全相同（1,007,403）——成本与收入同步放大，不产生漏收，
   但终端用户被超收约 780 倍。这条要靠后续项 1 的估算入口剥离才能修。
5. **次生危害实测**：B-3 那一次把 main 实例 root 的余额从 +100,000,000 打到
   −217,649,939（−$435），此后该实例对所有请求返回 403 `insufficient_user_quota`。
   超收不只是多收钱，它会直接让账户不可用——与 §B 第 6 条预测的可用性故障同类。

---

## 后续项（不在本次范围，各自独立可发）

1. **上游原有过收的三条入口**（B-3 / B-5 / B-6）：在 `service.CountTextToken`
   （`token_counter.go:405`）与 `service.EstimateTokenByModel`（`token_estimator.go:216`）
   入口剥离并补回媒体折算。建议以 `CountTextTokenWithMedia(text, model) (tokens, media int)`
   实现、由 `CountTextToken` 内部相加，避免调用方剥离后忘记补回。
   需要先确认「账面收入下调」这一商业决策。B-6 同时是可用性故障（预扣费误拒）。
2. **透传路径注入 `stream_options.include_usage`**（`relay/compatible_handler.go:100-111`），
   或对 `NewAPI` / `Sub2API` 渠道禁止 body 透传。改出站请求体，单独发便于回滚定位。
3. **接收侧 Gemini `inlineData` 张数漏收**：`relay/common/stream_received.go:56-70` 对
   gemini 原生事件取不到文本，导致「Gemini 图片流 + 客户端断开」结算 0。
   这是本次改动之前就存在的漏收，与 base64 过收无因果关系。
4. **对账可观测**：`Diagnostic.EstimatedUsage["output_media_count"]` 落到 `other.admin_info`，
   对「估算路径 + 有媒体」的请求做日常抽查；必要时再加渠道级 `EstimateBase64AsText`
   （默认关）用于对齐个别上游按 base64 收费的成本。

## 已确认的决策

1. `DataURLMediaTokens` 统一取 1400，编译期常量，不按厂商区分、不可运行时配置。
2. `UsePrice` 按次计价时只按张计费，文本不另收（与 `BuildConfirmedStreamUsage` 一致）。
3. 本次只修分支引入的 B-1 / B-2 / B-4；上游原有的 B-3 / B-5 / B-6 留待后续项 1。
4. 透传注入 include_usage 拆为独立改动（后续项 2）。
5. 不引入输出估算硬上限（原 `MaxEstimatedOutputTokens`）：媒体折算后估算值已回到正常量级，
   再加全局硬顶会在长输出场景造成漏收。

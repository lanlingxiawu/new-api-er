# 估算路径的漏收修复（内联媒体不计量 / 方言读不出即按 0 结算）

状态：已实现并通过测试（2026-09-20）。与 [base64 媒体导致 token 估算暴涨](base64-media-token-estimation-overcharge.md)
同属一条估算链——那份文档修的是**超收**，本文修的是同一条链上的**漏收**。

判据：**任何路径的结算量都不得低于上游对同一请求向我们计的量**。
超收要修，但修的顺序不能让我们先把收入降下来、成本还留在上面（见那份文档的 §后续项 1）。

## 三项缺陷

### 1. Gemini 内联媒体（`inlineData`）不折算任何 token

`relay/common/stream_session.go:1024-1035` 的 `CommitDelivery` 已经抽取 Gemini 原生
`candidates[].content.parts[].text`，但 `inlineData.data` 只置 `state.Effective = true`，
不进文本也不折算张数。于是**纯图片响应**（parts 里只有 `inlineData`）在估算路径上
交付侧 `EstimatedOutput` 与接收侧 `ReceivedOutput` 都是 0：

- 客户端断开（B-2）→ 按 `ReceivedOutput = 0` 结算；
- 上游协议未完成（B-1）→ 按 `EstimatedOutput = 0` 结算。

同一请求上游按实际产出（每张约 1290–1400 token）向我们计费，这是净亏。

### 2. 有文本时图片免费

`relay/channel/gemini/relay-gemini.go` 三处把图片当成"文本为 0 时的替补"，而不是叠加项：

| 位置 | 现状 |
|---|---|
| `:52-57` `patchGeminiZeroCompletionUsage` | `if imageCount != 0 && usage.CompletionTokens == 0 { = imageCount*1400 }` |
| `:224-228` 流式无 `usageMetadata` 分支 | 同上 |
| `:118` 非流式无 `usageMetadata` 分支 | **完全不看 imageCount**，只按 `geminiResponseUsageText` 估算 |

所以"一段文字 + 一张图"在缺少 `usageMetadata` 时只按文字收，图白送；非流式更是任何情况下都不计图。
这段代码与 upstream/main 逐字节相同，修完是本仓比上游多收——符合判据。

### 3. 接收侧读不出上游方言时按 0 结算

`relay/common/stream_received.go:56-92` 对无法用通用键读出的方言维护了一张 fallback 表
（cohere / baidu / dify / xunfei / tencent / coze / palm / ollama）。表外的方言只要
`CommitDelivery` 的通用键也读不到内容，`ReceivedOutput` 就是 0，客户端断开即按 0 结算。
逐个补方言永远补不全，且每加一个上游都要重新审计。

## 方案

### 媒体张数贯通估算链（修 1）

- `StreamSession` 增私有计数 `media int`（与 `text` 同生命周期，取走即清零），
  `CommitDelivery` 在 candidates 循环里对 `inlineData.data` 非空的 part 计数。
- 新增 `TakeDeliveredMedia() int`，与 `TakeDeliveredText()` 成对。
- 估算闭包签名从 `func(text string) int` 改为 `func(text string, media int) int`；
  交付侧（`relay/common/stream_writer.go:188-192`）与接收侧
  （`relay/common/stream_received.go:92`、`stream_session.go:829`）都传入张数。
- 折算仍在 `service` 侧完成：`StreamTokenEstimator.AddMedia(n int) int` 按
  `DataURLMediaTokens`（常量 1400）返回本批增量，与 base64 剥离共用同一个常量与同一份累计，
  因此**同一张图不会既按 data URL 又按 inlineData 计两次**——两种形态按下游协议互斥出现。

`relay/common` 不引入对 `service` 的依赖（会成环），乘法留在 `service`。

### 图片计费改为叠加（修 2）

三处都改成 `completion = Estimate(text) + imageCount * service.DataURLMediaTokens`，
非流式分支补上 `geminiResponseInlineImageCount`。常量从 `service` 取，消除第二处 1400 字面量。

### 兜底：交付过内容就不得按 0 结算（修 3）

`service/stream_lifecycle.go` 的 `"estimated"` 分支与 `SupplementStreamZeroOutput` 入参
共用一个取值函数：

```
output = EstimatedOutput
if clientGone {
    output = ReceivedOutput
    if output == 0 && snapshot.Effective {   // 接收侧读不出该方言
        output = EstimatedOutput             // 回落到实际写给客户端的量
    }
}
```

`Effective` 的含义是"已成功写出并刷新过有效内容"，所以这条回落不会把空响应变成有费用；
它只是在"确实交付过内容但接收侧计不出量"时，用交付侧的量代替 0。交付侧同样读不出时仍为 0，
此时结算 0 是正确的（没有任何可计量的产出证据）。

## 不少收不变量

| 场景 | 修复前 | 修复后 | 关系 |
|---|---|---|---|
| Gemini 纯图片 + 客户端断开 | 0 | 张数 × 1400 | 由亏转平 |
| Gemini 纯图片 + 上游协议未完成 | 0 | 张数 × 1400 | 由亏转平 |
| Gemini 文本 + 图片、无 usageMetadata（流式/非流式） | 仅文本 | 文本 + 张数 × 1400 | 增收 |
| 未覆盖方言 + 客户端断开且交付过内容 | 0 | 交付侧估算 | 由亏转平 |
| 上游给了确认用量 | 上游值 | 上游值（不变） | = |
| 无任何有效交付 | 0 | 0（不变） | = |

## Main Chain Impact

**同步执行**：candidates 循环里多一次 `inlineData.data` 判空（已有循环内，无新扫描）；
估算闭包多一个 int 参数。无新网络调用、DB、Redis、锁、协程。

**异步执行**：无新增。

**降级**：按「修复不加开关」的约定不引入配置；回滚为回退二进制。

## Shared Resource Audit

| 资源 | 本方案访问 | 主链是否同时访问 | 结论 |
|---|---|---|---|
| Redis / DB / 连接池 / 协程池 | 否 | — | 无冲突 |
| `StreamSession` 内存状态 | 新增一个 int 计数，仍在既有会话锁内 | 是（同一会话，串行调用） | 无新锁，无竞争 |
| `Evidence` 映射 | **不写入** | 是 | 刻意避开：新增 Evidence 键会改变 `stream_lifecycle.go:123` 的 `confirmed` 判定与 `SelectUsageSource()` |

## 测试设计（Rule 15.2）

改动点自身：

- `relay/common/stream_media_parts_test.go`（新）：`CommitDelivery` 对 Gemini 纯图片计 1 张、
  图文混合计 1 张 + 文本、多张分别计数、空 `data` 不计；`TakeDeliveredMedia` 取走即清零、
  未取走则跨事件累计。
- `service/stream_media_estimation_test.go`：`AddMedia` 增量语义，以及
  data URL 与 inlineData 两种形态折算结果一致、互不叠加。
- `service/stream_lifecycle_test.go`：`estimatedStreamOutput` 五个分支的表驱动用例。
- `relay/channel/gemini/relay_gemini_image_billing_test.go`（新）：流式/非流式在缺
  `usageMetadata` 时 `completion = 文本估算 + 张数×1400`；只回 prompt 侧用量时同样叠加；
  有完整确认用量时数值不变（防回归）。

被牵动的相邻逻辑（同批补齐）：

- **写入器 → 估算器的贯通**（`TestStreamWriterPassesMediaToEstimator`）：刷新前不计入；
  只含媒体、没有文本的批次也必须触发估算闭包并计入 `EstimatedOutput`；张数取走后不跨批重复。
- **接收侧计量**（`TestReceivedEstimatorCountsMedia`）：`ObserveEvent` 后
  `ReceivedOutput` 同时包含文本与张数折算——这是"客户端断开"口径的唯一来源。
- **端到端结算**（`TestFinalizeStreamUsageBillsInlineMediaOnClientGone`）：Gemini 原生流、
  断开发生在 `usageMetadata` 到达之前，最终 `completion` 恰为一张图的折算值而不是 0。
  这一条正是线上链路无法隔离验证的场景（真实 Gemini 每个 chunk 都带 `usageMetadata`，
  结算会走确认用量）。
- **方言兜底端到端**（`TestFinalizeStreamUsageFallsBackWhenDialectUnreadable`）：接收侧读不出的
  上游事件 + 已成功交付的转换结果 → 结算取交付侧估算。
- **零输出补估**（`TestFinalizeStreamUsageSupplementsZeroCompletionWithMedia`）：上游确认
  `completion=0`（图片模型常见）时，补估把已交付张数计入，输入侧确认值保持原样。
- **不与按张计价重复**：`/v1/images` 的 `data[].b64_json` 与 Claude 文本块事件张数为 0，
  图片端点仍只走 `Evidence["image_count"]`。

## 不在本次范围

- 上游原有的超收入口 B-3 / B-5 / B-6（见另一份文档 §后续项 1），需先解决"成本可见"；
- body 透传绕过强制 `include_usage`——**upstream/main 同样如此**（`relay/compatible_handler.go:48-66`
  与 `:100-111` 与上游一致），按"main 有的先不动"的约定保留；
- 音视频 `[media](data:...)` 与图片共用 1400 的偏差；
- 失败重试已消耗的上游成本不回收。

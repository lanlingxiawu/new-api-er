# 图像输入 Token 在日志与导出中的可核对性

状态：已实现
创建：2026-08-31

## 背景

客户 `dreamy@dreamto.ai` 在 2026-08-28 ~ 08-30 期间用 gpt-image-2 产生 69,739 次消费，
系统实扣 $8,750.11。客户导出日志 CSV 后自行核算：

```
输入 152,483,793 × $5/M × 1.9  = $1,448.60
输出 117,981,963 × $30/M × 1.9 = $6,724.97
                                 --------
                                 $8,173.57
```

与实扣相差 **$576.55（6.6%）**，据此报「消耗对不上」。

逐条重算全部 70,134 条消费记录的结论：**计费金额没有错误**（0 条金额异常、0 条重复请求 ID、
106,259 条错误记录全部 0 token / 0 花费、预扣费退还与违规扣费路径均未异常触发）。
真正的问题是**日志与导出没有把「图像输入 token」这一档暴露出来**，客户无法把这 $576 算出来。

缺口的精确来源：`输入 Tokens` 中有 101,062,945 个是图像输入 token（客户上传的参考图），
按 `图像倍率 1.6` 计价（$8/M），而非文本输入的 $5/M。

| 档位 | Tokens | 单价（含分组倍率 1.9） | 金额 |
|---|---:|---:|---:|
| 文本输入 | 51,420,848 | $9.5/M | $488.50 |
| 图像输入 | 101,062,945 | $15.2/M | $1,536.16 |
| 输出 | 117,981,963 | $57/M | $6,724.97 |
| 合计 | | | $8,749.63 |
| 实际扣费 | | | $8,750.11（残差 $0.49 为逐条额度取整）|

### 客户无法自行核对的三个原因

1. **导出列名与内容相反。** `service/text_quota.go` 把 `summary.ImageTokens`
   （来自 `usage.PromptTokensDetails.ImageTokens`，是**输入**图像 token）写进
   `other["image_output"]`，而导出列名为 "Image Output Tokens" /「图像输出 Tokens」。
   客户看到「输出」二字，会认为该值已包含在 `输出 Tokens` 里而跳过。
   定价页对同一倍率的标注本来就是正确的（`web/src/features/pricing/components/model-details.tsx`
   显示 "Image input"）。
2. **`总 Tokens` = `输入 + 输出`**，不含图像列，使图像列看起来像一个与账无关的游离数字。
   实际数据中 `图像 ≤ 输入` 在全部 69,739 行成立（同一尺寸下呈 1508 / 3016 / 4524，
   即 1、2、3 张参考图）。
3. **`文本输入 Tokens` 列对图像请求恒为空。** `service/log_info_generate.go` 只在
   realtime-WS 与音频路径写 `text_input`。而上游其实已返回该值：
   `relay/channel/openai/relay_image.go` 已把 Azure 的 `input_tokens_details.text_tokens`
   解析进 `usage.PromptTokensDetails.TextTokens`，只是从未写入日志。

## 目标与范围

**目标**：让用户仅凭日志详情或导出 CSV 就能自洽地把花费算出来，具体是让
`输入 Tokens = 文本输入 Tokens + 图像输入 Tokens` 这一关系可见。

**范围内**
- 消费日志 `Other` 中新增 `text_input`（图像请求路径）。
- 导出列 `image_output` 的显示名改为「图像输入 Tokens」；`image_ratio` 改为「图像输入倍率」。
- 日志详情弹窗 Token Breakdown 增加「文本输入」行，并明确标注图像行是输入的子集。

**范围外（明确不做）**
- **不改动任何计费公式或金额**。现有 `service/text_quota.go` 的
  「图像 token 从 base 减掉、按 `图像倍率` 加回」逻辑是正确的，本次一行不动。
- 不改 `Other` 的 JSON 键名 `image_output`。历史日志已按该键存储，
  且 `LogExportDefaultColumns` 与用户已保存的列偏好都引用它，改键会让历史数据与用户预设失效。
- 不回填历史日志的 `text_input`。历史行仍可用 `输入 − 图像输入` 自行推导。

## 数据流

```
上游响应 (Azure images)
  input_tokens_details: { text_tokens, image_tokens }
      ↓ relay/channel/openai/relay_image.go  normalizeOpenAIUsage
  usage.PromptTokensDetails.{TextTokens, ImageTokens}
      ↓ service/text_quota.go  calculateTextQuotaSummary
  summary.{TextTokens, ImageTokens}          ← 本次新增 TextTokens
      ↓ service/text_quota.go  （日志 other 组装）
  other["text_input"] / other["image_output"]
      ↓ model.EnqueueConsumeLog（已有的异步批量写入）
  logs.other (JSON)
      ↓
  日志详情弹窗 / CSV 导出
```

## 数据模型变更

无表结构变更、无迁移。仅在既有 `logs.other` JSON 列中多写一个 `text_input` 整数键。

## 配置参数

无新增配置。

## API 契约

无新增端点。既有导出列接口返回的 `image_output` / `image_ratio` 两列的 `label`
文案变化，`key` 不变，前端与已保存的列偏好不受影响。

## 关键业务逻辑与边界

`text_input` 的取值，仅在 `summary.ImageTokens != 0` 时写入：

| 情况 | 取值 |
|---|---|
| 上游返回了 `text_tokens` | 直接用该值 |
| 上游只给了 `image_tokens` | 回退为 `PromptTokens − ImageTokens` |
| `PromptTokens < ImageTokens`（上游口径异常） | 取 0，不产生负数 |
| `ImageTokens == 0` | `text_input` 与 `image_output` 均不写入，行为与改动前完全一致 |

回退分支存在的原因：并非所有渠道都填 `text_tokens`（例如 Gemini 转换路径按 modality 累加，
部分上游只报 `image_tokens`），此时仍应给出一个可核对的文本输入值。

## 错误处理

无新增失败路径。全部是内存内整数赋值，不会返回错误、不会 panic。
`text_input` 缺失时前端该行不渲染，历史日志表现不变。

## 与既有子系统的交互

- **计费**：只读 `summary` 已有字段，不参与 quota 计算。
- **日志**：复用既有的 `model.EnqueueConsumeLog` 异步批量写入通道，不新增写入点。
- **缓存 / Redis**：无交互。

## Main Chain Impact

**同步执行部分**：`calculateTextQuotaSummary` 中新增一次整数赋值
（`summary.TextTokens = usage.PromptTokensDetails.TextTokens`），
以及日志 `other` 组装时的一次比较 + 减法 + map 赋值。二者都在
`service.PostTextConsumeQuota` 内，本就位于响应返回之后的结算阶段。

**异步部分**：日志落库仍由既有的 `model.EnqueueConsumeLog` 缓冲写入，未做改动。

**共享资源审计**：

| 资源 | 本次是否访问 | 主链路是否访问同一资源 | 冲突 |
|---|---|---|---|
| Redis key / namespace | 否 | — | 无 |
| DB 表 | 仅经由既有 `logs` 异步写入通道，未新增查询或写入点 | 是（同一通道） | 无（未增加行数或列数，仅 JSON 内多一个整数键） |
| 内存结构 / map | 仅函数内局部 `other` map，多一个键 | 否 | 无 |
| 连接池 / goroutine 池 | 未新增 | — | 无 |

**并发分析（100k RPM）**：每请求新增 0 次 DB 调用、0 次 Redis 调用、0 个锁、0 个 goroutine；
增量成本为两次整数运算与一个 map 键（约 16 字节，随该请求的 `other` map 一同释放）。
在 100k RPM 下额外分配约 1.6 MB/min 的短命对象，处于现有 `other` map 分配量的噪声范围内，
对 GC 与延迟无可测影响。无需特性开关：该改动不影响任何执行流程分支，最坏情况是多一个日志字段。

## 实现记录

- `service/text_quota.go` — `textQuotaSummary` 增加 `TextTokens` 字段并在
  `calculateTextQuotaSummary` 赋值；日志 `other` 组装处新增 `text_input`。
- `model/log_export_columns.go` — `image_output` 列 Label 改为 "Image Input Tokens"，
  `image_ratio` 列 Label 改为 "Image Input Ratio"。
- `i18n/locales/{en,zh-CN,zh-TW}.yaml` — 两个 `log_export.col.*` 文案同步更新。
- `web/src/features/usage-logs/components/dialogs/details-dialog.tsx` — Token Breakdown
  增加「文本输入 Tokens」行，图像行改名为「图像输入 Tokens（含在输入内）」。
- `web/src/features/usage-logs/types.ts` — `LogOtherData.text_input` 已存在（ws/音频路径用），
  仅补充注释说明它现在也覆盖图像输入路径；`image_output` 补注释说明它存的是输入图像 token。
- `web/src/i18n/locales/{en,zh,zh-TW}.json` — 新增两条文案。

## 给客户的核对公式

```
花费(USD) = [ (输入Tokens − 图像输入Tokens) × 模型倍率
            + 图像输入Tokens × 模型倍率 × 图像倍率
            + 输出Tokens     × 模型倍率 × 补全倍率 ] × 分组倍率 ÷ 500000
```

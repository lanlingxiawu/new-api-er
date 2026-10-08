# 我方超时的成本承担：任务类不受超时、「只收输入」计费档、平台承担可见

状态：已实现（2026-09-30），实现记录见第 15 节
日期：2026-09-30

## 1. 背景与目标

单用户 AI 请求超时（[per-user-relay-timeout.md](per-user-relay-timeout.md)）是本分支特有功能，**main 没有单用户超时**：main 的请求只受共享 HTTP 客户端的全局 `RELAY_TIMEOUT` 约束，Coze 用 `time.Sleep` 轮询到完成，任务提交不被取消。我方超时触发后，上游多数情况照常计费，而用户按 `non_stream_timeout_billing` 退款（默认）或按实际用量收费（`charge`，依赖「转流」，只覆盖 OpenAI chat completions 非流式）。

客户端主动断开的计费已在 [non-stream-timeout-loss-prevention.md](non-stream-timeout-loss-prevention.md) §23 对齐 main，不受本文影响。本文只处理**我方超时**，三项小改动：

1. 提交即计费的请求不启用单用户超时（与 main 一致）；
2. 超时计费新增一档「只收输入」（`input`），默认仍为「退款」；
3. 平台承担的超时成本在日志中可见、可导出。

范围外（改动大，暂不做）：转流扩展到 Claude / Gemini / Responses；平台承担上限；不能转流的请求后台读完；上游日志异步对账；按延迟选渠道。

## 2. 第 1 项：提交即计费的请求不启用单用户超时

### 2.1 规则

以下请求**不调用** `middleware.StartRelayRequestTimeout`，即不受单用户超时托管（`IsRelayTimeoutManaged == false`），行为与 main 相同：上游调用使用 `context.Background()`（外加共享客户端的 `RELAY_TIMEOUT`），执行到完成，按实际结果计费。

| 请求 | 入口 | 判定 |
|---|---|---|
| Midjourney 提交 | `controller.RelayMidjourney`（`controller/relay.go:509`） | 整个处理函数不启动 |
| Suno、视频（`/v1/video/generations`、`/v1/videos`、可灵、即梦）任务提交 | `controller.RelayTask`（`controller/relay.go:599`） | 整个处理函数不启动 |
| Coze 非流式（创建会话后轮询） | `controller.Relay`（`:148`） | 首选渠道类型为 Coze 且非流式 |
| 阿里异步生图 / 图片编辑（万相：提交后轮询） | `controller.Relay` | 首选渠道类型为阿里且 relay mode 为图片生成或图片编辑 |

判定用 Distribute 选定的首选渠道（`channel_type` 上下文键）。重试换到其他类型渠道时仍保持不托管（与 main 相同），不中途启用。

### 2.2 理由

这些请求在上游接单后即产生费用，取消等待或轮询省不下上游成本；超时发生在上游返回提交结果之前时，任务已被上游受理并计费，我方却退款并丢失任务。main 不取消它们。

### 2.3 用户可见变化

在上述请求上，用户配置的非流式 / 流式超时不再生效，请求一直等到完成（与 main 一致）；超时计费档位对它们无意义。用户编辑表单的超时说明中注明「任务提交、Midjourney、Coze 非流式、阿里异步生图不受超时限制」（阿里图片编辑同样豁免）。

### 2.4 不变的部分

讯飞、火山等 WebSocket 与普通流式、OpenAI / Claude / Gemini 等 HTTP 请求仍受单用户超时托管；§23 的客户端断开规则不变。

## 3. 第 2 项：超时计费档「只收输入」

### 3.1 取值

`users.non_stream_timeout_billing`：`refund`（默认，不变）/ `charge`（不变）/ **`input`（新增）**。列类型 `varchar(16)`，无需迁移。`NormalizeNonStreamTimeoutBilling` / `ValidateNonStreamTimeoutBilling` 增加 `input`；未知值仍归一为 `refund`。

### 3.2 结算规则（仅「我方超时」错误码 `relay_timeout` 触发；客户端断开、上游错误不走此路径）

| 场景 | refund | input（新增） | charge |
|---|---|---|---|
| 非流式、未转流，我方超时（上游尚未返回） | 退款 | 按输入结算：预扣时的估算输入（未转流拿不到确认值）；输出 0，不收工具附加费 | 与 input 相同（未转流时拿不到输出）* |
| 非流式、已转流（仅 charge 用户），我方超时且已收到输出 | — | — | 不变：按已接收用量，并返回部分内容 |
| 非流式、已转流（仅 charge 用户），我方超时且没有输出（含上游尚未返回响应头） | — | — | 至少按输入：上游已确认输入（收到用量帧）时用确认值，否则估算输入；输出 0，不收工具附加费；仍返回 504** |
| 流式，我方超时且上游尚未返回响应头（流式会话尚未开始） | 退款 | 与非流式未转流相同：估算输入，输出 0 | 与 input 相同 |
| 流式，我方超时、已开始但无有效交付 | 0 | 确认输入，否则估算输入；输出 0 | 不变：确认用量，否则估算输入＋已接收输出 |
| 流式，我方超时且已有交付 | 按交付结算（不变） | 按交付结算（不变） | 按交付结算（不变） |

所有「按输入」的格子里，按次计费模型、输入计价为 0（免费模型）与请求未完整写给上游的情形都按退款处理（见 §15）。同一请求在三档下满足 charged(charge) ≥ charged(input) ≥ charged(refund)，无有效交付时 refund 为 0；唯一的例外是已转流且上游确认的输入小于本地估算时，charge 按上游确认值（真实值）收取，可能低于 input 档的估算值。

\* charge 用户的未转流请求（非 OpenAI chat 等无法转流的格式）超时后原为退款，现改为按输入收取，与 charge「按实际产生」的语义一致（2026-09-30 确认）。

\*\* 2026-10-01 确认：已转流而没有任何输出的 charge 请求原按 0 收取（低于 input 档），现至少按输入收取。

- **按次计费模型**（`ModelPrice` > 0，无输入 / 输出拆分）：`input` 档按 `refund` 处理（平台承担），在日志中标记（`per_call: true`），避免把整次价格当成「输入」收取；非流式未转流、流式会话尚未开始、已转流而没有输出的 charge 请求与 input 同规则。已开始的流式 charge 不变。
- **阶梯表达式计费**：以输入 token 代入表达式、输出为 0 计算。
- **缓存**：上游已确认的缓存读 / 写 token 按原倍率计入输入；估算时不含缓存。
- 结算走现有文本结算链路（与 charge 档流式超时相同的函数），消费日志 content 写我方文本「请求超过 N 秒时限，按输入计费」（i18n），不含上游原文。

### 3.3 实现位置（与 main 的差异面）

- 非流式：`controller/relay.go` 的结算 defer 里已有本分支钩子 `service.ChargeViolationFeeIfNeeded`；在 `Refund` 之前加一行 `service.SettleRelayTimeoutInputIfNeeded(c, relayInfo, newAPIError)`，返回已处理时跳过 `Refund`。逻辑全部放在本分支文件 `service/relay_timeout_input.go`（新）。`controller/relay.go` 本身已与 main 相差 300 余行，新增约 3 行。
- 流式：`service/stream_lifecycle.go`（本分支文件，`NonStreamTimeoutChargesUsage` 附近）按档位分支。
- 前端：用户编辑抽屉的超时计费下拉新增「只收输入」，说明文案 7 种语言（`web/src/i18n/locales/*.json` 的 `translation` 对象）；后端校验错误文案 i18n（en / zh-CN / zh-TW）。

## 4. 第 3 项：平台承担可见

### 4.1 记录

我方超时结束且平台承担了成本时（refund 档全部；input 档的输出部分；按次计费模型在 input 档），在该请求的错误日志 / 消费日志 `other.admin_info.timeout_absorbed` 写入（仅管理员可见，用户视图照旧剥离 `admin_info`）：

```json
{
  "mode": "refund|input|charge",
  "kind": "non_stream|stream",
  "input_tokens": 1234,          // 确认值或估算
  "input_estimated": true,
  "received_output_tokens": 0,   // 流式已接收的估算输出，非流式为 0
  "absorbed_quota_min": 617,     // 平台至少承担的额度：refund=输入费用（流式加已接收输出）；input=流式已接收输出、非流式 0；按次=整次价格
  "per_call": true               // 仅按次计费模型出现
}
```

`absorbed_quota_min` 是下限：非流式未转流时上游输出不可观测，只能记输入部分；已知的流式接收输出计入。字段名写明「min」，报表按下限解读。

### 4.2 展示与导出

- 管理员日志详情显示「超时平台承担（下限）」一行。
- 日志导出中心新增管理员列 `timeout_absorbed_quota_min`（用户自助导出不含），可按用户 / 渠道汇总。

## 5. 数据流

1. 请求进入 → Distribute 选渠道 → 处理函数判定是否启用单用户超时（第 1 项）；
2. 托管请求我方计时器到期 → 错误归一为 `relay_timeout`；
3. 结算 defer / 流式生命周期按用户档位：refund 退款、input 按输入结算、charge 按实际；
4. 同时写 `timeout_absorbed`（第 3 项），经异步日志管道落库。

## 6. API 与权限

无新增接口。用户更新沿用管理员 `PUT /api/user/`（`non_stream_timeout_billing` 字段新增合法值 `input`）。导出列仅管理员导出可见（沿用现有列权限）。

## 7. 数据模型

无迁移：`non_stream_timeout_billing` 为现有 `varchar(16)`；`timeout_absorbed` 写入日志 `other` JSON。用户缓存已包含该字段（schema 不变）。

## 8. 配置

无新增全局配置；用户级字段新增取值。无开关（功能扩展按用户显式选择生效，默认行为不变）。

## 9. 边界与错误处理

- 只有错误码 `relay_timeout` 触发 input 结算；上游错误、客户端断开、本地校验错误不触发。
- 结算失败（额度不足等）记录错误日志并回退为退款，不影响已返回给客户端的 504。
- 预扣额度小于输入费用时，按结算链路现有规则补扣或记欠（与 charge 档流式超时一致）。
- 重试：超时错误本就 `SkipRetry`，只结算最后一次尝试。
- 第 1 项豁免的请求不会产生 `relay_timeout`，也不写 `timeout_absorbed`。

## 10. Main Chain Impact

- 第 1 项让一部分请求**不再**创建超时控制器，热路径成本下降。
- 第 2、3 项只在我方超时这一失败路径上执行，正常请求零新增开销；判定只读已在上下文中的用户字段。
- 无新增同步 DB / Redis 调用；日志写入沿用异步管道。

## 11. Shared Resource Audit

| 资源 | 访问 | relay 主链是否同样访问 | 冲突 |
|---|---|---|---|
| `users.non_stream_timeout_billing` / 用户缓存 | 读（已有） | 是（已有读取） | 无，新增取值不改结构 |
| 日志 `other` JSON | 写（异步管道） | 是 | 无，新增键在 `admin_info` 下 |
| 结算链路（额度、成本台账） | 超时时结算输入 | 是 | 无，复用现有函数，单请求单次结算 |

## 12. Concurrency Analysis（100k RPM）

超时是低频失败路径；即使 1% 请求超时（1k RPM），每个超时事件增加一次现有结算（异步落库）与一个 JSON 字段，无锁、无 goroutine。第 1 项减少托管请求数，降低每请求分配。

## 13. 与 main 的差异

| 项 | main | 本方案 | 冲突面 |
|---|---|---|---|
| 1 | 无单用户超时，任务 / MJ / Coze / 阿里轮询执行到完成 | 这些请求不启用单用户超时，行为回到 main | `controller/relay.go` 三处启动调用附近加判定（本分支已改动区域）；coze、ali 与 main 的差异不增加 |
| 2 | 无 | 新增档位，逻辑在本分支新文件 | `controller/relay.go` defer 加一行钩子，紧邻现有本分支钩子 |
| 3 | 无 | 日志字段与导出列，全在本分支文件 | 无 main 文件改动 |

## 14. 测试设计（先于实现）

- 第 1 项：`RelayMidjourney` / `RelayTask` 请求在用户配置 1 秒超时时不取消上游（httptest 慢上游 2 秒，结果正常、按实际计费）；Coze 非流式、阿里图片生成同样；OpenAI 非流式、流式、讯飞 WebSocket 仍受超时（回归）。
- 第 2 项：refund / input / charge 三档 × 非流式未转流 / 流式无交付 / 流式有交付的结算矩阵；上游已确认输入 vs 估算输入；缓存 token；按次计费模型在 input 档退款；阶梯表达式输出为 0；只有 `relay_timeout` 触发（上游 500、客户端断开不触发）；结算失败回退退款；用户更新接口接受 `input`、拒绝未知值。
- 第 3 项：各档位 `timeout_absorbed` 字段内容与下限计算；用户视图与自助导出不含；管理员导出含该列。
- 全部在真实 DB（`.env`）上运行；relay 相关使用 httptest 模拟上游。

## 15. 实现记录（2026-09-30）

与上文的对应与实现期决定（代码为准）：

| 项 | 位置 | 说明 |
|---|---|---|
| 1 | `controller/relay_timeout.go` `startRelayRequestTimeout` / `relayBilledOnSubmission`；`controller/relay.go` 启动点 | `Relay` 调 `startRelayRequestTimeout`（豁免判定 + 启动 + 绑定 RelayInfo）；`RelayMidjourney`、`RelayTask` 不启动超时，与 main 一致，其超时响应与任务超时归一分支一并删除（`respondMidjourneyTimeout`、`normalizeRelayTaskTimeout`、`shouldProcessTaskChannelError` 与任务重试里的 `relay_timeout` 判断均为本分支独有，已不可达）。阿里豁免图片生成与图片编辑（万相编辑同样提交后轮询；同步模型豁免后也只是不受单用户超时） |
| 1 重试方向 | — | 豁免按 Distribute 首选渠道在首次尝试前决定，整个请求保持该状态：首选托管渠道、重试落到 Coze 非流式 / 阿里图片时仍受托管（该次尝试到时照常切断并按档位结算）；反之保持不托管。逐次尝试切换需要在请求中途暂停控制器的计时器并改动 `controller/relay.go` 的尝试循环，接受为已知缺口 |
| 2 | `constant/relay_timeout.go`、`service/relay_timeout.go` | `NonStreamTimeoutBillingInput = "input"`；`RelayTimeoutBillingMode(c)` 统一读档位 |
| 2 非流式 | `service/relay_timeout_input.go` `SettleRelayTimeoutInputIfNeeded` / `nonStreamTimeoutPlan`；`controller/relay.go` 结算 defer 一行 | 资格（`nonStreamTimeoutEligible`）：错误码 `relay_timeout` 且 `IsRelayRequestTimeout`；入口为非流式（含已转流但在上游返回响应头前被切断的 charge 请求——转流处理器尚未接管），或流式但流式会话尚未开始（上游尚未返回成功响应头）；无 `StreamResult`、响应尚未写出、本请求尚未结算过（见下）、最后一次尝试本身被我方时限切断（`LastError` 为 `relay_timeout`；时限在两次尝试之间到期时，最后一次上游调用已以错误作答，退款）。决定（`nonStreamTimeoutPlan`）：input 与 charge（未转流）以预扣时的估算输入（不含缓存）走 `PostTextConsumeQuota`，输出 0、不收工具附加费；以下退款：refund、按次计费模型（input 与 charge 同规则，不把整次价格当作「输入」）、输入计价为 0（免费模型，不写 0 额度的消费日志、不计请求次数）、请求未完整写给上游 |
| 2 已结算的请求 | `service/consume_settlement.go` `markRelaySettlementDone` | 时限可能在处理器读完上游响应并结算（消费日志、用量、`Billing.Settle`）之后、写给客户端之前到期，控制器仍把结果归一为 `relay_timeout`。`FinalizeConsumptionSettlement` 与 `EnqueueConsumeLogWithCost` 入口置请求本地标记，超时结算与平台承担记录见到标记即不处理，避免第二条消费日志、重复计数与虚假的承担记录 |
| 2 请求是否送达 | `service/relay_timeout.go` `RelayResponseTraceContext`；`service/relay_timeout_input.go` `relayUpstreamSent` | 受管请求（流式与非流式）的主上游 HTTP 请求装 `httptrace.WroteRequest` 回调，记录请求头与请求体是否完整写出；每次尝试开始（`BeginRelayAttempt`）清空。已开始交换但未写完（拨号 / TLS 挂起、上传被截断）时 input 档（流式与非流式）与非流式 charge 退款，记录 `request_sent: false`、`absorbed_quota_min: 0`。本次尝试没有 HTTP 交换（SDK、WebSocket 渠道）时按已送达处理。流式 charge 不变 |
| 2 转流无输出 | `relay/upstream_stream_buffered.go` 超时分支；`service/relay_timeout_input.go` `AdaptedTimeoutWithoutOutput` | 已转流的 charge 请求在收到任何输出前被切断：用量至少为输入——收到上游用量帧时取其输入（确认值），否则取估算输入；输出 0、不收工具附加费，消费日志写「只收输入」文案，仍返回 504。按次计费模型与输入计价为 0 的免费模型按空用量结算（不收费、不计用量）。记录 `mode: charge`、`absorbed_quota_min: 0`（按次为整次价格）。在上游返回响应头之前就被切断的转流请求没有进入转流处理器，走上一行的非流式兜底 |
| 2 Claude 超时类型 | `relaykit/types/error.go` `claudeErrorTypeForStatus` | 我方超时（504）对 Claude 格式的非流式响应使用 Anthropic 类型 `timeout_error`，与 Claude 流式终止帧（§23.5 of non-stream-timeout-loss-prevention.md）一致；`anthropicErrorTypes` 同时收录 `timeout_error`，上游带来的该类型原样保留。其余 5xx 仍为 `api_error` |
| 2 非流式失败回退 | `service/consume_settlement.go` → `settleRelayTimeoutInputQuota` | 结算先于用量统计执行；资金未提交即失败时 `Billing.Refund` 并不写消费日志、不计用量；资金已提交（令牌调整失败）时保留收费与日志 |
| 2 流式 | `service/stream_lifecycle.go` `FinalizeStreamUsage`；`relay/channel/claude/strict_stream.go` | 两处共用 `service.StreamTimeoutSettlement(c, info, confirmed) (source, inputOnly)`。input 档：确认证据去掉输出侧字段（`StreamInputOnlyEvidence`）后构造用量，确认缓存按倍率计入；证据缺输入字段时按估算输入补齐；无证据时为不含缓存的估算输入；输出估算置 0 |
| 2 工具附加费 | `service/text_quota.go` `calculateTextQuotaSummary` | 只收输入的结算（非流式与流式）不计工具附加费（按模型名推断的 web_search_preview、上游报告的搜索次数等） |
| 2 日志文案 | `i18n` `relay.timeout_input_only`（en / zh-CN / zh-TW） | 存英文（与其余存储文案一致）："The request exceeded the configured N second time limit; charged for input only."，带请求 ID |
| 3 记录 | `service/relay_timeout_input.go` | `admin_info.timeout_absorbed` 另带 `per_call: true`（按次计费模型）、`request_sent: false`（请求未送达上游）。非流式：退款 = 输入费用（按次为整次价格，未送达为 0）；按输入结算 = 0（输出不可观测）。流式：refund = 输入 + 已接收输出费用；input = 含输出与不含输出两次计价之差（阶梯表达式非线性时也成立）；两档都另加上游报告的工具附加费（原生 Claude 流把 `server_tool_use.web_search_requests` 计入）；未送达为 0。流式会话尚未开始的流式请求按非流式规则记录（`kind: stream`）。token 计价不含工具附加费，不改动请求的计费状态 |
| 3 写入位置 | 流式：`FinalizeConsumptionSettlement`（消费日志或零收费错误日志）；非流式退款：渠道错误日志 | 非流式退款请求只有 `processChannelError` 写的渠道错误日志，钩子放在其调用的 `AppendStreamErrorDiagnostic` 开头（`appendRelayTimeoutAbsorbedToErrorLog`，读启动超时时绑定的 RelayInfo），避免再改 `controller/relay.go`。每个请求只记一次：按输入结算的请求记在消费日志，错误日志不重复 |
| 3 可见范围 | `model/log.go` `formatEmployeeLogs`；`model/log_export_columns.go` `timeout_absorbed_quota_min`；`log-detail-body.tsx` | 用户视图的 other 投影整体剥离 `admin_info`；员工视图保留 `admin_info`（重试链等）但删除其中的 `timeout_absorbed`，其余字段保持原始 JSON；导出列 AdminOnly，自助导出剔除；详情行只对真正的管理员角色（`useIsAdmin`）显示——员工在客户日志页也拿到管理员字段开关 |
| 3 详情 | `web/src/features/usage-logs/components/dialogs/log-detail-body.tsx` | 消费日志在「计费明细」、错误日志在「计费路径」处显示「超时平台承担（下限）」 |
| 性能样本 | `service/text_quota.go` | 非流式只收输入的结算不记成功的性能样本（控制器已按失败记录一次） |

已知边界：

- 非流式退款的记录依赖错误日志开关（`ErrorLogEnabled`）；关闭时不记录。结算失败回退为退款时也不记录。
- 关闭 `CountToken` 时预扣估算输入为 0，输入计价为 0，按退款处理。
- 停用统一流式错误处理（旧流式路径，无流式会话）的请求不走本功能，维持退款。

### 测试服复测补充：响应头前超时

2026-09-30 复测 M8/M8c 发现：统一流式会话只有取得成功响应头后才接管结算，
此前我方超时会遗漏 input/charge 的输入结算。控制器现有超时钩子现在也接受
“已创建统一流式会话、尚未接管响应、尚未结算”的请求，仍检查完整写出证据。
refund 全额退款，input/charge 在已写出且输入计价为正时只收估算输入；
退款日志的平台承担记录标为 stream。已有流式结算、已写客户端响应、
旧流式路径及非流转流路径不进入这个补偿入口。

与 main 的差异：该文件及单用户超时计费策略为本分支功能，修复不修改 main
的重试循环。Main Chain Impact：仅超时失败路径增加请求本地状态判断，
沿用现有结算及异步日志队列，无新增数据库查询、Redis 调用或后台任务。
会读取现有 StreamSession 的请求本地互斥锁保护状态，不引入跨请求共享锁；
正常成功请求不执行这一分支。即使 100k RPM 全部超时，也只有每次超时的
常数次请求本地状态检查，结算与日志仍使用现有异步管线。
回归测试 `TestTimeoutStreamBeforeHeadersBilling` 覆盖 OpenAI/Anthropic 渠道三档，
修改前六个场景均失败。
- 重试从托管渠道换到提交即计费的渠道时仍受托管（见上表「重试方向」）。
- Coze 非流式请求未带 `user` 时，默认用户 ID 不是合法 JSON，请求在本地编码失败（与 main 相同的既有问题，不在本次范围）。

Main Chain Impact 补充：受托管的 `Relay` 请求在启动超时时多一次请求本地的 `c.Set`（绑定 RelayInfo）；受托管请求的每次主上游 HTTP 调用多一个 `httptrace.ClientTrace` 与一个原子标记（此前流式请求与已收到首字节的非流式请求不装 trace），回调只写原子量、不持有 gin.Context；每次结算多一次请求本地 `c.Set`。无 I/O、无锁；Coze 非流式、阿里生图 / 编辑、Midjourney、任务提交不再创建超时控制器。其余逻辑只在我方超时的失败路径上执行。

测试（先写、修复前失败、实现后通过）：

- `service/relay_timeout_cost_test.go`：`TestStreamTimeoutSettlementByMode`、`TestSettleRelayTimeoutInputNonStream`（input / charge 未转流 / refund / 按次 input / 按次 charge）、`TestSettleRelayTimeoutInputFallsBackToRefund`、`TestRelayTimeoutErrorLogRecordsAbsorbed`、`TestStreamOwnTimeoutInputOnly`（确认输入、确认缓存、估算输入与已接收输出）、`TestStreamOwnTimeoutRefundAndChargeRecords`、`TestStreamOwnTimeoutInputTieredOutputZero`、`TestStreamOwnTimeoutInputSettlementLog`、`TestStreamOwnTimeoutRefundErrorLogRecordsAbsorbed`；守卫：`TestSettleRelayTimeoutInputOnlyForOwnTimeout`（上游 500、客户端断开、流式、转流、已写出）、`TestRelayTimeoutErrorLogSkipsOtherErrors`、`TestStreamOwnTimeoutWithDeliveryUnchanged`、`TestRealtimeOwnTimeoutNoRecord`、`TestNonStreamTimeoutBillingAcceptsInput`。
- `service/relay_timeout_cost_review_test.go`（独立审查的回归，均在修复前失败）：`TestTimeoutAfterHandlerSettledIsNotSettledAgain`（先 `PostTextConsumeQuota` 再走超时钩子：只一次结算、一条消费日志、不记承担）、`TestTimeoutBetweenAttemptsIsNotCharged`、`TestUnsentRequestIsNotCharged`（非流式、流式、已写出、换尝试清空）、`TestRelayResponseTraceTracksWrittenRequest`（真实传输：已作答 / 连接被拒 / 上传被时限截断 / 未受管不装）、`TestInputOnlyExcludesToolSurcharges`（search-preview 模型不收附加费；上游报告的搜索计入承担）、`TestZeroInputChargeRefunds`（免费模型 input / charge 均退款、无消费日志）。`service/http_client_timeout_test.go` 的 trace 用例按新合同更新（受管请求都装 `WroteRequest`，首字节回调仍只给等待首字节的非流式请求）。
- `relay/channel/claude/strict_stream_timeout_input_test.go`：`TestStrictOwnTimeoutInputOnly`。
- `controller/relay_timeout_cost_bearing_test.go`（真实中间件链 + 真实 DB + httptest 上游）：`TestTimeoutCostNonStreamMatrix`、`TestTimeoutCostUpstreamErrorStillRefunds`、`TestTimeoutCostStreamMatrix`、`TestBilledOnSubmissionIgnoresUserTimeout`（Coze 非流式、Suno 提交、Midjourney 提交在 1 秒时限后正常完成并计费）、`TestRelayBilledOnSubmission`（含阿里图片编辑）、`TestUserUpdateAcceptsInputTimeoutBilling`。
- `model/log_export_timeout_absorbed_test.go`：导出列取值、AdminOnly、自助导出剔除、表头翻译、用户视图剥离、`TestEmployeeLogViewStripsTimeoutAbsorbed`（员工视图删除 `timeout_absorbed`、保留其余 admin_info 与大整数原文）。
- 服务器复测与 2026-10-01 决定的回归（均在修复前失败）：`controller/relay_timeout_before_headers_test.go` `TestTimeoutStreamBeforeHeadersBilling`（OpenAI、OpenAI→Claude、原生 Claude、原生 Gemini 四种协议 × 三档，流式在上游返回响应头前被切断：refund 退款并记录，input / charge 按估算输入结算）；`controller/relay_timeout_charge_floor_test.go` `TestTimeoutChargeFloorInvariant`（非流式无响应头、转流有响应头无输出、流式无响应头、流式有响应头无交付、流式有交付五种情形 × 三档，断言 charged(charge) ≥ charged(input) ≥ charged(refund)、无交付时 refund 为 0）与 `TestClaudeNonStreamTimeoutUsesTimeoutErrorType`；`relay/upstream_stream_timeout_input_test.go` `TestAdaptedTimeoutWithoutOutputBillsInput`（无分片 / 仅角色帧按估算输入、用量帧按确认输入、按次与免费模型不收）；`service/relay_timeout_cost_review_test.go` `TestPreHeaderTimeoutUsesInputFallback`；`relaykit/types/error_timeout_type_test.go` `TestClaudeErrorTypeForRelayTimeout`。

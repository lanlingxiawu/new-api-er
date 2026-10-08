# 渠道重试的次数与时长控制

状态：实现与回归验证完成
日期：2026-09-23

## 1. 问题

### 1.1 时长失控

开启渠道重试后，单个请求的总耗时可以远超用户配置的超时值。三个因素叠加：

**响应超时每次重试完整重置。** `service.RestartRelayResponseTimeout(c)` 在每次重试前把响应计时器重置成完整的 `response_timeout` 窗口（`middleware/relay_timeout.go` 的 `RestartResponse`）。[单用户 AI 请求双超时](per-user-relay-timeout.md) 3.2 节已写明：「总时长不受限时，最坏等待上界为 `(RetryTimes + 1) × 响应超时`」。

**总时长默认不限制。** `relay_timeout_setting.go` 的 `TotalTimeoutSeconds` 默认 `0`，唯一能兜住整体墙钟的闸门默认是关的。界面上它与 `response_timeout` 并列，没有主次提示。

**跨分组重试没有全局上界。** `service/channel_select.go` 在 `auto` 分组下切组时调用 `param.SetRetry(0)` 与 `ResetRetryNextTry()`，而 `controller/relay.go` 的循环条件是 `retryParam.GetRetry() <= common.RetryTimes`。计数归零后循环继续，尝试次数上界变成 `分组数 × (RetryTimes + 1)`。

补充：重试之间**没有退避等待**（不额外增加时长）；[非流式超时的上游成本回收](non-stream-timeout-loss-prevention.md) 的转流路径会让每次尝试更容易跑满 `response_timeout`（首字节不再停表），加剧本问题。

保守假设下：`3 个分组 × (3 + 1) × 300 秒 = 3600 秒`。用户以为配置的是「5 分钟超时」，最坏等待一小时。

### 1.2 重试次数只有一个全局旋钮

`common.RetryTimes` 是唯一的重试次数配置，所有用户、所有分组共用。实际需求是分化的：

- 付费高的用户希望多重试以提高成功率，试用用户不需要；
- 稳定的官方直连分组不需要重试，聚合/低价分组需要多试几个渠道；
- 昂贵分组的每次重试都是真金白银，应当比廉价分组更克制。

当前只能取一个折中值，对谁都不合适。

### 1.3 两者是同一个问题

次数与时长互为因果：次数失控直接导致时长失控，而时长预算又是次数的隐含上界。分开设计会产生互相矛盾的闸门，因此合并为一份方案。

## 2. 目标与范围

让重试次数可按用户与分组分化配置，同时让单请求总墙钟时长可预期、可配置，并且不在明知会超时的情况下继续消耗上游。

范围内：

- 重试次数支持**全局 / 分组 / 用户**三级覆盖；
- `total_timeout` 确立为整条重试链的硬预算，重试前检查剩余预算；
- 每次尝试的响应计时器不超过剩余预算；
- 引入不受分组切换影响的总尝试次数上限；
- 界面明确 `total_timeout` 的主导地位与不配置时的后果。

范围外：

- **令牌级重试配置**。用户与分组两个维度已覆盖需求，再加一级会让优先级链难以解释。
- **重试退避/抖动**。当前无退避，本方案不引入——退避只会让总时长更长。
- **重试触发条件**。不改 `shouldRetry` 的状态码判定集合。
- 流式请求各自的停表规则不变（预算对两种模式一视同仁地施加，但不改停表语义）。

## 3. 保守假设与待校准参数

本设计在**没有生产实测数据**的前提下编写：本地开发库 `options` 表既无 `RetryTimes` 也无 `relay_timeout_setting.*`，走代码默认值（即本地未开启重试，无法复现问题）；`logs` 中仅 866 条测试数据。下列取值按「最坏但常见」假设，**全部可配置**，上线前用实测校准。

| 假设项 | 保守取值 | 校准方式 |
|---|---|---|
| `RetryTimes` | 3 | 查生产 `options` 表 |
| 是否 `auto` 分组 + 跨组重试 | 是，3 个分组 | 查 `UserUsableGroups` 与令牌配置 |
| `response_timeout_seconds` | 300（代码默认） | 查生产配置 |
| `total_timeout_seconds` | 0（代码默认，即无预算） | 查生产配置 |
| 慢请求的成因构成 | 未知 | 查 `logs.use_time` 分布与 `other.admin_info.use_channel` 长度 |

**最后一项决定方案重心**：若慢请求主要是推理模型的正常长耗时，该收紧的是重试触发条件；若主要是少数故障渠道被反复重试，则次数闸门优先级最高。本设计覆盖两者，但默认值取舍需要该数据。

## 4. 前提：没有预算就没有闸门

**时长控制以 `total_timeout > 0` 为前提。**

当 `total_timeout = 0`（当前默认）时，剩余预算无从计算，第 6 节的预算检查与裁剪自动失效，最坏时长退化为 `总尝试次数上限 × response_timeout`。

因此界面提示（第 10 节）不是可用性润色，而是方案生效的前提。按已确认的决定，全局默认值**保持 `0` 不变**（避免截断现有用户原本能跑完的长请求），改为在界面明确告知后果，由管理员显式配置。

## 5. 重试次数的三级配置

### 5.1 优先级与取值语义

优先级对齐既有的 `ratio_setting.ResolveGroupRatio`（用户专属倍率 → 分组倍率 → 全局倍率），保持整个项目的覆盖链一致：

```
用户 retry_times  >  分组 retry_times  >  全局 RetryTimes
```

取值语义与现有的五个超时字段完全一致（Rule：同类配置用同一套约定，降低理解成本）：

| 值 | 含义 |
|---:|---|
| `0` | 继承下一级（用户 `0` → 看分组；分组未配置 → 看全局） |
| `-1` | 显式禁用重试（等价于 0 次重试），**不继承** |
| `>0` | 使用该值 |

`-1` 与 `0` 必须区分：全局 `RetryTimes = 0` 本身就表示「不重试」，若用户级也用 `0` 表达禁用，就无法表达「继承」。这与超时字段踩过的是同一个坑。

解析函数放在 `service` 层，与 `ResolveRelayTimeoutOverride` 并列：

```go
func ResolveRetryTimes(userRetryTimes int, group string) int
```

### 5.2 分组维度在 auto 分组下的语义

`RelayInfo.UsingGroup` 在 auto 跨组重试时会变动（`relay/common/relay_info.go:90`）。因此分组级配额天然是**每个分组独立**的：切到新分组时，按新分组重新解析配额并重新计数。

这正是 `service/channel_select.go` 中 `SetRetry(0)` 现有行为的语义化——上一轮把它判定为纯缺陷并不准确：它实现的是「每分组独立配额」，缺的只是全局兜底。本方案因此**保留该行为并正式化**，另加第 6.3 节的总次数闸门作为第二道闸门。

举例（用户未配置，全局 `RetryTimes=3`）：

| 分组 | 分组配额 | 该分组内最多尝试 |
|---|---:|---:|
| `gpt官方直连` | `-1`（禁用） | 1 次；配额为 0，不再重试（也不切组） |
| `特价0.1` | `5` | 6 次 |
| 未配置的分组 | 继承全局 `3` | 4 次 |

整个请求的总尝试次数仍受 `max_total_attempts` 与时长预算约束。

**切组本身保持 main 原语义**：路由在某分组用满配额时准备好下一组，但下一次尝试是否发生由重试决策决定，它需要剩余次数（`配额 - 计数 > 0`）。所以配额为 0 的分组失败后请求即结束，不会因此切组。本方案只把切组阈值换成按用户/分组解析的配额，不改变切组机制（见第 22 节）。

### 5.3 用户维度

用户级配额对整个请求固定，不随切组变化——它表达的是「这个用户值得多少次重试」，与用哪个分组无关。用户级非零时直接覆盖所有分组的配额。

### 5.4 落点

`common.RetryTimes` 当前有 4 处决策点，全部改为读解析后的值：

| 位置 | 用途 |
|---|---|
| `controller/relay.go:225` | 普通 relay 循环条件 |
| `controller/relay.go:305` | `shouldRetry` 的剩余次数参数 |
| `controller/relay.go:614` | 任务 relay 循环条件 |
| `controller/relay.go:667` | `shouldRetryTaskRelay` 的剩余次数参数 |
| `service/channel_select.go:137` | 跨组切换的配额判断 |

`controller/channel.go:194` 的 `"retry_times": common.RetryTimes` 是返回给前端的全局配置展示，保持不变。

## 6. 时长预算的三条闸门

### 6.1 预算检查：不在明知会超时时继续打上游

重试循环在决定下一次尝试前检查剩余预算：

```
剩余 = deadline - now              // deadline 取自 service.RelayTotalDeadline(c)
若 有总时长 且 剩余 <= 0                          →  停止重试（与 RetryMinBudgetSeconds 无关）
若 有总时长 且 剩余 < RetryMinBudgetSeconds（>0）  →  停止重试，返回上一次的错误
```

`service.RelayTotalDeadline(c)` 只返回整条请求的总截止时间，不回退到单次尝试的响应窗口（否则未配置总时长时每次重试都会被判为预算不足）。控制器不存在、已关闭或未配置总时长时返回 `false`，表示「没有预算」，闸门不生效。

总时长计时器**触发之后**仍返回原截止时间：此时剩余为负，闸门判定耗尽。若返回 `false`，会被读成「没有预算」而放行下一次尝试——该尝试在已取消的 context 上选渠道、预扣，随即超时，并把超时错误记到一个从未联系过的渠道上。由 `TestBranchAuditRegressionBudgetGateAdmitsRetryAfterTotalExpiry` 与 `TestBranchAuditBudgetGateWithoutMinimumStopsOnlyAfterTotalExpiry` 锁定。

同时解决两件事：**时长**——不再把剩余时间浪费在注定超时的尝试上；**成本**——不再向上游发起注定被中断的请求，那种请求上游照常计费而我们只能回 504，与 [非流式超时的上游成本回收](non-stream-timeout-loss-prevention.md) 是同一个亏损的两面。

### 6.2 重试的响应窗口：保持完整

`RestartResponse()` 每次重试都把响应窗口重置为完整的 `responseTimeout`；仅当总预算已耗尽时不再启动响应计时器，由总时长计时器负责取消。

**不按剩余预算截短窗口。** 截短看似能省掉空等，其实不能：截短后的响应计时器与总时长计时器在同一时刻到期，请求一点也没有提前结束；反而让响应计时器抢先触发，把「总预算耗尽」记成「响应超时」，504 文案显示的是响应超时的秒数（例如请求实际跑了 120 秒却提示 300 秒）。由 `TestRelayTimeoutRetryLeavesShortBudgetToTotalTimer` 锁定。

### 6.3 次数闸门：不受分组切换影响的总尝试上限

在 `service.RetryParam` 上增加**只增不减、不被 `SetRetry` 影响**的总尝试计数，循环条件同时校验：

```
for ; retryParam.GetRetry() <= 本分组配额 && retryParam.TotalAttempts() < maxTotalAttempts; ... {
```

`SetRetry(0)` / `ResetRetryNextTry()` 的语义按 5.2 保留——它们服务于「每分组独立配额」这个正确意图。新计数是独立的第二道闸门，不干扰分组遍历。

`maxTotalAttempts` 默认 **0（不限制）**，与第 6.2 节的预算闸门 `retry_min_budget_seconds`（默认 **0，关闭**）一样是增强功能：未配置时每个请求的尝试次数只由「失败请求的重试次数」决定，与引入闸门之前完全一致；管理员显式设置后才生效。

默认值曾为 6（总尝试）和 10 秒（预算），按「生产可能 auto 跨组重试」的假设保守选取。该前提已被否定（生产不用跨组，见第 22 节），而这两个默认值会在未开启「AI 请求超时」时同样生效：重试次数设为 6–10 的用户/分组被悄悄截断到 5 次重试，已配置总时长的部署在剩余不足 10 秒时停止重试——都改变了原有语义，因此改为默认关闭。

### 6.4 闸门关系

| 闸门 | 约束维度 | 生效前提 |
|---|---|---|
| 5.1 三级配额 | 单分组内次数 | 无条件 |
| 6.1 预算检查 | 墙钟 | `total_timeout > 0` |
| 6.3 总次数上限 | 全请求次数 | 无条件 |

6.1 负责「剩余预算不够就不再发起尝试」；已发起的尝试由总时长计时器兜底。6.3 是 `total_timeout = 0` 时唯一的保护。

## 7. 计费正确性

重试、切组、超时与结算相互纠缠，本节逐条列出本方案与计费的交互点。**每一条都必须有对应测试**（第 15 节）。

### 7.1 现状：分组倍率随切组重算，预扣不一定跟着走

`controller/relay.go:397` 在每次 `getChannel` 里重算 `info.PriceData.GroupRatioInfo = helper.HandleGroupRatio(c, info)`。因此切组后**结算用的是新分组的倍率**，而预扣是在重试循环之前按初始分组算的。

两种计费模式的处理不同：

- **分层计费（`tiered_expr`）**：`service.PrepareTieredBillingForSelectedGroup` 每次尝试前调用 `refreshTieredBillingGroup`，比较 `PriceData.GroupRatioInfo.GroupRatio` 与快照的 `snap.GroupRatio`，变化时重算并 `Billing.Reserve` 补足预留。已覆盖。
- **普通计费**：切到更贵的分组后不追加预扣，差额在 `PostConsumeQuota` 结算时补扣。

后者是既有行为，本方案不改变它。但必须记录清楚：**本方案引入的分组配额会改变各分组被尝试的次数分布，从而改变"最终成交在哪个分组"的概率**，因此会更频繁地触发"预扣按 A 组、结算按 B 组"这一既有路径。这不是新缺陷，但放大了它的暴露面，回归测试必须覆盖跨组成交的结算金额。

### 7.2 零尝试路径必须退款（实现约束）

`controller/relay.go:194` 的退款 defer 以 `newAPIError != nil` 为条件。若某个闸门导致重试循环**一次都不执行**，`newAPIError` 为 `nil`，退款不会发生——**预扣的额度会被静默吞掉**。

当前循环条件 `retryParam.GetRetry() <= common.RetryTimes` 在 `RetryTimes >= 0` 时恒真，至少执行一次，所以现状安全。本方案必须保持这个性质：

- **预算检查（6.1）放在循环体末尾**，作为"是否继续重试"的判断，而不是放进循环条件；
- **总次数闸门（6.3）永远不能产生零次尝试**：配置校验拒绝 `< 0`，`0` 解释为"不限制"（默认）而非"零次尝试"，首次尝试不受闸门约束（`service.ShouldAttemptRelay`）；
- **分组配额为 `-1`** 表示"不重试"，即仍尝试 **1 次**，不是"跳过该分组"。

若预算在请求开始前就已耗尽，正确行为是：照常发起第一次尝试 → 超时控制器已 cancel context → 该尝试立刻失败并返回超时错误 → 走既有退款/结算路径。不要用闸门把它拦在循环外。

### 7.3 提前终止路径的结算归属

闸门触发的提前终止，返回的都是**上一次尝试的真实错误**（第 11 节），因此 `newAPIError != nil`，退款 defer 正常执行。需要验证的分支：

| 终止原因 | `newAPIError` | 预期计费 |
|---|---|---|
| 分组配额耗尽且无更多分组 | 上次错误 | 全额退款 |
| 总次数闸门触顶 | 上次错误 | 全额退款 |
| 剩余预算不足 | 上次错误 | 全额退款 |
| 超时（`SkipRetry`） | 超时错误 | 按 7.4 |

### 7.4 与非流式超时计费的交互

[非流式超时的上游成本回收](non-stream-timeout-loss-prevention.md) 让开启 `charge` 的用户在超时时**结算已接收用量而非退款**，通过 `relaycommon.StreamHandledKey` 让控制器跳过退款与响应重写。

交互点：

- 超时错误带 `SkipRetry`，**优先于所有闸门**——闸门不会让一个已超时的请求继续重试，也不会阻止它结算；
- 若闸门在某次尝试**之前**终止（预算不足/次数触顶），该尝试从未发起，没有上游用量，走全额退款，与 `charge` 设置无关；
- `StreamHandledKey` 一旦置位，控制器立即 `return`，闸门不再参与。

三者互不干扰，但必须有交叉测试：`charge` 用户 + 多次重试 + 最后一次尝试超时 → 只结算最后一次的已接收用量，前几次失败尝试不产生任何扣费。

### 7.5 预扣只有一次，上游消耗有多次

无论重试几次，预扣只在循环前发生一次（分层计费会按需追加预留，但不会按尝试次数累加）。结算只发生一次。因此**不存在重复扣费**。

反过来说，N 次尝试消耗了 N 次上游成本，而用户至多支付 1 次——失败时甚至支付 0 次。这是既有的平台侧亏损，与 [非流式超时的上游成本回收](non-stream-timeout-loss-prevention.md) 处理的是同一类问题。本方案通过减少无谓尝试（6.1）缓解它，但不改变"失败不向用户收费"这一原则。

### 7.6 不受影响的部分

以下机制本方案完全不触碰，回归测试需确认其行为逐字不变：

- 预扣与结算的差额核销（`FinalPreConsumedQuota`）；
- 提成归属按 `relayInfo.UsingGroup`（`service/employee_commission.go`）——切组后归属最终成交分组，这是既有语义；
- 消费日志的 `group` 字段取 `relayInfo.UsingGroup`（`service/consume_settlement.go:68`）；
- 违规费用（`ChargeViolationFeeIfNeeded`）与免费模型跳过预扣的判定。

## 8. 配置参数

新增三项，归入既有 `relay_timeout_setting` 与新的分组配置键，随现有系统设置接口保存，无需新端点（Rule 12）：

| 键 | 默认 | 含义 |
|---|---:|---|
| `relay_timeout_setting.retry_min_budget_seconds` | 0 | 剩余预算低于此值不再重试。`0` 关闭该检查（默认，保持原有语义） |
| `relay_timeout_setting.max_total_attempts` | 0 | 单请求总尝试上限，不受分组切换重置。`0` 表示不限制（默认，保持原有语义） |
| `GroupRetryTimes` | `{}` | 分组 → 重试次数的 JSON 映射，形如 `{"特价0.1": 5, "gpt官方直连": -1}` |

`GroupRetryTimes` 与既有的 `GroupRatio` 同构（同为 options 表中的分组 JSON 映射），复用其校验与热更新模式。

三项均提供 `0` / 空关闭语义，出问题可在不发版的情况下退回旧行为。

## 9. 数据模型变更

`model/user.go` 新增一列，与五个超时字段并排：

```go
RetryTimes int `json:"retry_times" gorm:"type:int;not null;default:0;column:retry_times"`
```

- 迁移走 GORM `AutoMigrate` 加列，三库通用（Rule 2）；
- 不加索引：仅随用户主键读出，无查询条件；
- 同步进用户缓存链路：`UserBase`、`ToBaseUser`、`WriteContext`、`user_auth_cache.go` 的 Redis Lua 参数、`EditWithTx` 的更新 map——与 [非流式超时的上游成本回收](non-stream-timeout-loss-prevention.md) 新增 `non_stream_timeout_billing` 时改动的是同一组位置；
- **`userCacheSchemaVersion` 需再次递增**，使旧 hash 失效而非把缺失字段解码成 `0`。注意 `0` 在此语义下是「继承」而非危险值，但仍应递增以保持缓存契约严格。

`constant/context_key.go` 新增 `ContextKeyUserRetryTimes`。

## 10. 前端

**用户编辑抽屉**（`web/src/features/users/components/users-mutate-drawer.tsx`）的超时区块内新增「重试次数」字段，复用同区块既有的 `TimeoutOverrideField` 结构（同为「0 继承 / -1 禁用 / 正整数」语义，可直接复用其数字输入与校验）。

**系统设置**新增分组重试次数的 JSON 编辑，复用 `GroupRatio` 既有的编辑组件与校验提示。

**超时文案补充**（第 4 节的前提）：

- `total_timeout` 是**整个请求的墙钟上限，包含全部重试**；
- `response_timeout` 是**单次尝试**的限制，每次重试重新开始；
- `total_timeout` 为 `0` 时给出可见提示：最坏等待可达 `总尝试次数上限 × response_timeout`。

文案从用户视角写（Rule 6）：说清「你最多会等多久、最多重试几次」，不暴露分组遍历等内部机制。七语言 i18n 直接补键，不跑 `bun run i18n:sync`（会回填无关键）。

## 11. 错误处理

配额耗尽、预算耗尽或次数耗尽时**不引入新错误码**：返回最后一次尝试的真实错误，由既有路径写出。用户该看到的是「上游为什么失败」，而不是「网关放弃了重试」——后者是实现细节。

超时本身仍走既有 504 + `ErrorCodeRelayTimeout` 路径，不变。

## 12. Main Chain Impact

**同步执行（relay goroutine）**：每次重试决策处增加一次配额解析（读进程内热配置快照 + 一次 map 查找）、一次 `time.Now()` 比较、一次整数比较。无 DB、无 Redis、无锁、无新 goroutine。

用户级配额从既有的用户缓存 context key 读取，与五个超时字段同一来源，**不新增任何读取路径**。

**行为变更方向**：只会让请求**更早**结束、上游调用**更少**，不存在延长路径。

**对未配置的用户**：用户列默认 `0`、`GroupRetryTimes` 默认空、两个新配置项若置 `0`，则行为与当前完全一致。

**回滚**：配置项置 `0`、清空分组映射即刻恢复旧行为，无需重启或发版。

**30k → 100k RPM**：每请求增量为常数次内存操作；减少的无谓上游调用反而降低连接池与上游配额压力。

## 13. Shared Resource Audit

| 资源 | 本方案访问 | 主链是否同资源 | 结论 |
|---|---|---|---|
| `users` 表 | 加一列，随用户行读写 | 是 | 不新增查询，走现有 `GetUserCache`；无新索引 |
| 用户 Redis 缓存 | 现有 Hash 加一个 field | 是 | 同 key 同 Lua 脚本，不新增 key 或往返 |
| `relayTimeoutControl` | 读 deadline、写响应窗口 | 是 | 既有 mutex 保护，不新增字段竞争面 |
| `RetryParam` | 新增计数字段 | 是 | 请求局部，单 goroutine 访问 |
| 分组配置快照 | 读 `GroupRetryTimes` | 是（计费读 `GroupRatio`） | 独立键，沿用既有热配置快照机制，不共享可变状态 |

无新 Redis key、无新表、无新锁、无新池。

## 14. Concurrency Analysis

每请求增量：DB 调用 0，Redis 往返 0，锁 0（deadline 读取复用 `NextDeadline()` 既有锁，且仅在重试决策点调用，每请求至多 `配额 + 1` 次），goroutine 0。分组配额查找是 `RWMap` 读，与既有 `GetGroupRatio` 同量级。100k RPM 下为常数级内存操作。

## 15. 测试设计（先于实现）

**三级优先级（决策覆盖 + 等价类）**：用户 `>0` 覆盖分组与全局；用户 `-1` 禁用且不继承；用户 `0` 落到分组；分组已配置则用分组值；分组 `-1` 禁用；分组未配置落到全局；三级全未配置等于当前行为。

**取值边界**：`-1` / `0` / `1` / 极大值；非法值（负数非 `-1`、超上限）被校验拒绝且不写入。

**auto 分组的每分组独立配额**：切组后按新分组重新解析配额并重新计数；配额为 `-1` 的分组只尝试一次即切组；`SetRetry(0)` 后**分组内计数归零但总计数不归零**（本条是 6.3 的核心断言）。

**预算检查（边界值）**：剩余预算恰好等于 / 略高于 / 略低于 `retry_min_budget_seconds` 三档；`retry_min_budget_seconds = 0` 时只在总时长已到期后停止；无 deadline（`total_timeout = 0`）时不检查且不误判为「预算为零」；总时长计时器触发后仍判定为耗尽。

**重试的响应窗口**：剩余预算短于响应窗口时由总时长计时器触发并记为总时长超时；总预算已耗尽后重启不改写超时类型；无 deadline 保持完整窗口。与 [非流式超时的上游成本回收](non-stream-timeout-loss-prevention.md) 的 hold 清除同处 `RestartResponse`，需回归验证互不干扰。

**交互**：超时错误的 `SkipRetry` 优先于所有闸门；配额/预算耗尽时返回上一次的真实错误而非新错误码。

**计费正确性（对应第 7 节，每条一个用例）**：

- **零尝试不可能发生**：任意配置组合（含 `max_total_attempts=1`、分组配额 `-1`、预算已耗尽）下，重试循环至少执行一次；断言预扣在所有终止路径上都被退款或结算，无静默吞没。这是第 7.2 节的核心断言。
- **分组配额 `-1` 仍尝试一次**：断言该分组被实际调用一次后才切组，而不是被跳过。
- **三条提前终止路径都退款**：分组配额耗尽 / 总次数触顶 / 预算不足，各自断言 `Refund` 被调用且金额等于预扣。
- **跨组成交的结算金额**：初始分组 A（倍率 x）、最终成交分组 B（倍率 y），断言结算按 y 计、消费日志 `group` 记 B、提成归属 B。普通计费与 `tiered_expr` 各测一遍——后者额外断言 `Billing.Reserve` 被追加调用。
- **无重复扣费**：多次重试后成功，断言预扣发生一次、结算发生一次，`FinalPreConsumedQuota` 核销正确。
- **与 `charge` 超时计费的交叉**：`non_stream_timeout_billing=charge` 的用户，前 N-1 次尝试失败、最后一次超时，断言只结算最后一次的已接收用量，前几次不产生扣费；且 `StreamHandledKey` 置位后闸门不再介入。
- **免费模型**：`FreeModel` 跳过预扣的请求在各条闸门终止时不产生退款调用，也不报错。

**模型层**：新列在 MySQL / PostgreSQL / SQLite 的迁移与读写；用户缓存与 Redis Lua 往返保真；自助更新路径（`Updates(struct)` 跳零值）不清空管理员配置——参照 `model/user_timeout_billing_test.go` 的现有模式。

**回归**：三项配置均为默认（`0` / 空）时，重试次数与时长与当前实现逐字一致。

数据库测试沿用项目真实 env（Rule 15.5）；不调用真实上游（Rule 15.4）。

## 16. 实施顺序

1. 配置项、用户列、分组映射与校验（此时无行为变化）；
2. 5.1 三级解析函数 + 单测（纯函数，独立可验证）；
3. **第 7.2 节的零尝试保护 + 计费回归基线**：先补齐"任意配置下循环至少执行一次""所有终止路径都退款"的测试，在**接线之前**让它们对当前实现通过。这样后续每一步的计费回归都有基线可比；
4. 接线 5.4 的五个决策点 + 测试；
5. 6.3 总次数闸门 + 测试（`total_timeout = 0` 时唯一的保护）；
6. 6.1 + 6.2 预算检查与裁剪 + 测试（两者必须同时合入）；
7. 跨组成交的计费测试（第 15 节，普通计费与 `tiered_expr` 各一遍）；
8. 前端两处字段 + 文案 + 七语言 i18n；
9. `go test ./...` 全绿（Rule 15.8）；
10. **上线前用生产实测校准第 3 节的参数默认值。**

第 3 步刻意排在接线之前：计费的回归基线必须先建立，否则后面任何一步改坏了扣费都无从发现。第 2、5 步互不依赖；第 4 步依赖第 2 步；第 6 步独立于次数维度。

## 17. 实现与验证（2026-09-23）

落地文件：

| 文件 | 内容 |
|---|---|
| `setting/operation_setting/relay_timeout_setting.go` | `RetryMinBudgetSeconds`、`MaxTotalAttempts` 两个配置项与校验；`RelayMaxTotalAttempts()` 把非正值一律归为「不限制」 |
| `setting/operation_setting/group_retry_setting.go` | 分组配额映射，对齐 `GroupRatio` 的 `RWMap` + `RegisterSnapshot` 模式；`CheckGroupRetryTimes` 校验 |
| `service/relay_timeout.go` | `ResolveRetryTimes` / `ResolveRetryTimesForRequest` 三级解析、`ValidateRetryTimes`、`ShouldAttemptRelay` 闸门、`RelayRetryBudgetExhausted` 预算检查；controller 唯一使用的三个入口 `ContinueRelayAttempts`（循环条件）、`BeginRelayAttempt`（每次尝试开头）、`RemainingRetryBudget`（重试决策的剩余次数），配额按当前尝试所在分组（`auto_group`，否则 `group`）即时解析 |
| `service/channel_select.go` | `RetryParam` 的 `totalAttempts` 与配套方法；跨组切换阈值改用按分组解析的配额。其余与 main 一致 |
| `middleware/relay_timeout.go` | `RestartResponse` 在总预算已耗尽时不再启动响应计时器（第 6.2 节） |
| `controller/relay.go` | 两条重试循环（普通 relay 与任务 relay）各只改三行：`for` 条件、循环开头一次调用、重试决策的剩余次数参数（见第 20 节） |
| `model/user.go`、`user_cache.go`、`user_auth_cache.go` | `retry_times` 列 + 缓存链路；`userCacheSchemaVersion` 3 → 4 |
| `controller/user.go` | 管理端字段与校验 |
| `web/src/features/users/**`、`web/src/i18n/locales/*.json` | 用户抽屉的重试次数字段（`TimeoutOverrideField` 增加可选 `min`/`max`）+ 七语言翻译 |

**零尝试保护的实现方式**：不变量编码在 `ShouldAttemptRelay` 内部——`totalAttempts <= 0` 时无条件返回 `true`，而不是依赖每个调用方把条件顺序写对。预算检查在 `ContinueRelayAttempts` 里、且只在已有尝试后执行，所以它只决定「是否继续」，永远拦不住第一次尝试。两条重试循环都保留三段式 `for`，`IncreaseRetry()` 仍在 post 位置。

**配额取值时机**：循环条件在本次尝试选渠道之前求值，此时 `auto_group` 仍是上一次尝试的分组——正是那次尝试消耗的配额所属分组。重试决策在尝试结束后求值，取的是本次尝试的分组，剩余次数为 `配额 - 计数`，与 main 的 `RetryTimes - retry` 同构。

**测试**：`service/retry_quota_test.go`（三级优先级与取值边界、零尝试保护、配额与总次数闸门、预算检查边界）、`service/channel_select_retry_quota_test.go`（分组配额 `-1` 试一次后路由即指向下一组、充足配额留在本组、**切组不重置总计数**）、`middleware/relay_timeout_hold_test.go`（重试响应窗口 + 既有 hold 用例）、`model/user_timeout_billing_test.go`（新列三库读写、两条更新路径、Redis 往返）。

**实现期修正**：`ResolveRetryTimes` 第一版把「非 `-1` 的负值」视为继承，测试 `TestResolveRetryTimes_UnknownNegativeUserValue` 直接抓到——那会让一个手误写成 `-99` 的配置静默拿到全局配额。已改为任何负值一律视为「不重试」。

**验证结果**：`go build ./...` 通过；新增测试全绿；`bun run typecheck`、`bun run lint` 通过。全量 `TEST_DB_CLEANUP=true go test ./... -p 1` 为 6 个预存在失败，与本功能实现前的基线逐条一致。

首轮全量曾多出一个 `setting/operation_setting` 的 `TestRateLimitDraftAccessIsSerialized`，第二轮未复现，单独跑 5 次全通过：该用例的 reader 协程可能在任何 writer 写入前就读到默认值，而默认值本身不满足它的 identity 断言，属于用例自身的启动竞态，与本功能无关。

**未覆盖**：跨组成交的端到端结算金额（第 7.1 节）需要完整 relay 链路与真实上游，本轮只验证到「配额解析按分组独立」与「切组不重置总计数」这一层；金额层面的验证留给 staging。`-race` 仍受限于本机无 cgo/gcc。

## 18. 审计修复（2026-09-23）

代码审计发现 6 个问题，逐条核实后全部属实并已修复；另在修复过程中自查出第 7 个同类问题。

| # | 问题 | 修复 |
|---|---|---|
| 1 | 转流只看 `RelayFormat`（客户端协议），而 `streamSupportedChannels` 含 Anthropic/Gemini/AWS。客户端发 OpenAI 格式但渠道是 Claude 时，原生 SSE 会被按 chat.completion.chunk 解析成空 chunk，**内容静默丢失仍计费** | 增加 `info.ApiType != constant.APITypeOpenAI` 判定，只适配 OpenAI 线格式上游 |
| 2 | `ChatCompletionsStreamResponse` 没有 error 字段，`{"error":...}` 会静默反序列化成全零 chunk 并被计为"收到内容"，最终返回 **200 + 空正文**且不触发重试 | 新增 `adaptedStreamErrorFrame` 前置识别，错误帧优先于超时判定，走正常重试/退款路径 |
| 3 | 新增的两个重试配置不在 `model/config_group.go` 的 `relayTimeoutFields` 白名单里，**设置接口一律拒绝保存** | 补入白名单；`TestValidateRelayTimeoutFieldsAcceptsRetryControls` 锁定 |
| 5 | 任务链路只在循环前解析配额，切组后未刷新 | 配额不再缓存，两条链路都在每次使用时按当前尝试的分组解析 |
| 6 | 响应计时器重启用 `GetRetry() > 0` 判断，而切组会把该计数归零，**新分组首次尝试不重启计时器** | `service.BeginRelayAttempt` 改用 `TotalAttempts() > 0` |
| 7 | （自查）`group_retry_setting` 注册为 config-group 快照，但该接口的 `default` 分支拒绝未列出的模块，**分组配额只能改库** | 改走与 `GroupRatio` 完全相同的 OptionMap 路径（`model/option.go` + `controller/option.go` 校验） |

修复 7 时一度把新 `case` 插进了 `GroupRatio` 的错误处理中间，切断了它的 `planGroupRatioCleanup`，由既有用例 `TestUpdateOptionDispatchesCleanupOnGroupRemoval` 当场发现并已复原。

新增回归用例：上游协议限定（四种 ApiType）、错误帧识别（含"正文里出现 error 字样不误判"与"错误帧优先于超时"）、白名单可编辑性、分组配额经 option 字符串往返。

全量 `TEST_DB_CLEANUP=true go test ./... -p 1` 回到基线的 6 个预存在失败。

## 19. 二次审计与测试服实测（2026-09-23）

自查 + 测试服多条件实测发现的问题：

| # | 问题 | 修复 |
|---|---|---|
| 8 | 纯 usage 流（无任何内容帧）聚合出的 `choices` 序列化为 **`null`**，而 OpenAI schema 将其定义为数组，SDK 直接迭代会出错；`created` 也可能是 `0` 这个非法时间戳 | `Snapshot` 始终初始化为空数组；`created` 缺失时取当前时间 |

### 测试服实测矩阵（21 项全通过）

| 组 | 覆盖 |
|---|---|
| 转流协议限定 | Anthropic 上游不转流 / OpenAI 上游转流 / 客户端仍收非流式 JSON |
| 上游错误帧 | 纯错误帧、内容后错误帧：均不返回 200、均不扣费 |
| 响应形状 | `choices` 非 null、`created` 为有效时间戳 |
| 超时计费 | 持续吐字仍在 10s 超时（停表抑制生效）、charge 按已收用量扣费、refund 不扣费 |
| 三级配额 | 用户 `-1/0/3` → 1/2/4 次；分组 `4` → 5 次 |
| 跨分组 | 两组 `1` 共 4 次、总闸门 3 封顶、用户级覆盖分组级 |

计费类断言必须在每次测量前后各静默 14 秒：消费日志走异步批量落库，否则上一用例的扣费会落进本次测量窗口（首轮就因此误报过一次"错误帧扣费 500"）。

`auto` 分组实测需要三项前置同时满足，缺一即不切组：令牌 `cross_group_retry=1`、`UserUsableGroups` 含 `auto`、全局 `AutoGroups` 列出候选分组。

服务器测试后已完全恢复：`GroupRatio`(8 键)、`ModelPrice`(94 键)、`UserUsableGroups`、`relay_timeout_setting.enabled=false` 均回到原值，测试渠道/用户/令牌残留为 0，mock 上游已停并清理全部临时文件。

## 20. 合并上游须知

`controller/relay.go` 是上游持续重写的文件（本功能开发期间上游已把 `shouldRetry` 改成 `service.DecideRelayRetry`、给任务循环加了诊断埋点）。所以本功能在其中只留**单行替换**，逻辑全部在分叉独有的 `service/relay_timeout.go`：

| 上游的写法 | 合并后必须是 |
|---|---|
| `for ; retryParam.GetRetry() <= common.RetryTimes; retryParam.IncreaseRetry() {`（两处） | `for ; service.ContinueRelayAttempts(c, retryParam); retryParam.IncreaseRetry() {` |
| 循环开头的计时器重启 | `service.BeginRelayAttempt(c, retryParam)`（它同时计数总尝试次数，漏掉它总次数闸门就失效） |
| 重试决策的 `common.RetryTimes-retryParam.GetRetry()`（两处） | `service.RemainingRetryBudget(c, retryParam)` |

**静默陷阱**：上游的重试决策是两行——`decision := service.DecideRelayRetry(c, err, common.RetryTimes-retryParam.GetRetry())` 与 `if decision.Action != "retry"`。git 只把后一行标为冲突，前一行会被自动合入。直接取上游一侧解决冲突，用户级 / 分组级重试次数在重试决策上就失效了，而编译和其他测试都不会报错。任务循环的 `decideTaskRetry(...)` 同理。

`controller/relay_retry_merge_guard_test.go` 用 AST 检查 `relay.go` 中不得出现 `common.RetryTimes`，上游版本恰好命中上述 4 处。合并后该用例失败即说明有一处没换。

以 upstream/main `d04c118c88` 模拟合并（`git merge-tree`，2026-09-24）：本功能在 `controller/relay.go` 新增 2 个冲突块（Relay 的 `for` 行与决策行，各为单行对上游数行），其余改动落在已有冲突块内或无冲突；不新增冲突文件。

## 21. 作用域修复与本机全量实测（2026-09-24）

### 21.1 作用域登记缺陷

系统设置的读写按作用域白名单过滤（`service/settingsaccess/scopes.go`），而本功能只改了存储层白名单：`system-tuning.relay-timeout` 缺两个新字段，导致**整个「AI 请求超时」区块保存失败**（`AllowsGroup` 按整组校验，root 也不例外）；`system-tuning.group-retry-times` 根本未注册，分组重试页读写全被中间件拒绝。已补登记并加入权限目录，`service/settingsaccess/relay_retry_scope_test.go` 按界面实际载荷锁定，已验证在修复前的代码上失败。三处登记的对照见 [relay-timeout-retry-config-ui.md](relay-timeout-retry-config-ui.md) §6.1。

### 21.2 功能实测（真实网关 + Go mock 上游，46 项全通过）

| 组 | 覆盖 |
|---|---|
| 作用域 | 两个作用域的读、整组保存、白名单外字段/越界值被拒、非法值不覆盖已存值、不能借新作用域改 `GroupRatio`、未注册作用域被拒、未授权管理员读写返回 403 |
| 用户字段 | 统一/分别两种载荷保存与回读、五类非法值被拒且原值不变 |
| 重试次数 | 上游调用计数：分组 -1→1、分组 2→3、用户 1 覆盖→2、用户 -1→1、总上限 2 封顶、总上限 0 不限→6；总时长 3s 且预算阈值 10s→不重试，关闭闸门→4，未设总时长时闸门不生效→4 |
| 超时计费 | charge：3.00s 返回 504、上游被改为流式、按 87 个输出 token 扣 96、余额/已用/日志三方一致、日志标记 `upstream_stream_adapted` 且 `is_stream=false`；refund：504、上游保持非流式、零扣费零日志；charge 正常完成仍返回完整非流式 JSON；charge 用户的流式请求照常流式；未启用超时管理时不转流 |

### 21.3 压测：干净 HEAD 与当前代码 A/B

本机 16 核，负载器、mock、网关同机，100 并发。**同机 A/B 有明显的顺序偏差**：先后跑同一二进制，后跑的一方系统性变慢（前一轮的异步日志还在写 PostgreSQL）。正向 3 轮一度得出「功能未开启的非流式路径 CPU +25%」，反向重跑后结论相反。因此所有结论取正反各 3 轮合并的中位数；重试场景另做 6 轮逐轮交替。主机 CPU 超过 `performance_setting.monitor_cpu_threshold`（95%）时网关以 503 `system_cpu_overloaded` 拒绝请求，这类样本（0 次上游调用）测的是保护阈值而不是网关，已剔除。吞吐受主机饱和影响大，以**每请求 CPU 时间**为主指标。

| 场景 | 每请求 CPU（基线 → 当前） | 结论 |
|---|---:|---|
| 非流式，未开启功能 | 1.15 → 1.23 ms | 区间完全重叠（0.92–1.30 vs 1.00–1.27），噪声 |
| 流式 | 3.42 → 3.45 ms | 无差异 |
| 失败重试 3 次（100 / 50 并发，逐轮交替） | 1.63 → 1.58 / 1.69 → 1.64 ms | 无差异；吞吐与 p99 持平或更好 |
| charge 用户、100 帧转流 | 1.21 → 1.52 ms | +0.3 ms/请求，为转流重组的预期成本，低于第 13 节（非流式超时文档）优化前的 0.65–0.8 ms |

### 21.4 并发计费对账（当前代码，50 并发）

每个场景前后各静默 16 秒等异步日志落库，核对余额减少 = 已用增加 = 日志额度合计，且日志条数与应计费请求数一致：

| 场景 | 请求 | 结果 |
|---|---:|---|
| 未开启功能、正常完成 | 5000 | 5000 条 × 500 = 2,500,000，三方一致 |
| charge、转流后正常完成 | 5000 | 同上；上游流式请求 +5000 |
| charge、全部 3s 超时 | 200 | 全部 504；200 条日志，每条 96，合计 19,200，三方一致 |
| refund、全部 3s 超时 | 200 | 全部 504；零扣费零日志；上游未转流 |
| 失败重试（全局 3 次尝试） | 1000 | 3000 次上游调用，零扣费零日志 |

## 22. 跨组逻辑回退到 main 原语义（2026-09-24）

跨组重试只在**令牌分组为 `auto` 且令牌开了 `cross_group_retry`** 时发生。香港测试服（621 个令牌）与本机开发库均无此类令牌，`AutoGroups` 未配置，系统实际不跨组。重试的需求是在 main 原有机制上做灵活控制（用户级、分组级次数与总闸门），不是改造切组机制，因此把为跨组场景加的改动回退：

- 去掉请求级「待切组」信号（`ContextKeyPendingGroupSwitch`、`PendingGroupSwitch()`），`IncreaseRetry()` 恢复 main 原样；
- `remainingRetryBudget` 恢复为 `配额 - 计数`，不再在配额用尽而路由已备好下一组时额外放行一次。

保留：`RetryParam.totalAttempts`（总闸门与计时器重启依赖它）、跨组阈值使用按用户/分组解析的配额（使切组阈值与循环配额一致）。`service/channel_select.go` 相对 main 只剩这两处。

**回退的动因（防复发记录）**：那段「额外放行一次」改变了 main 在配额为 0 时不切组的行为，并带出两个缺陷。
- 最后一个 auto 分组用满配额后，路由仍会准备「下一组」（下标越界），放行的那一次尝试找不到渠道并带 `SkipRetry` 返回，**把上游真实的 429/500 覆盖成「可用渠道不存在」**，默认 `RetryTimes=0` 即触发。
- 用户级 `retry_times=-1`（界面文案「不重试」）在跨组时仍会在每个分组各试一次，上游按次计费。

回退后两者都不再发生，由 `TestAudit_SpentQuotaEndsRequestDespitePreparedSwitch` 锁定。

另：首次选渠道发生在 `middleware/distributor.go` 的临时 `RetryParam` 上，main 的 `resetNextTry` 实例字段传不到控制器循环。这是 main 自身的行为，跨组不在本系统的使用范围内，未改动。若将来启用 `auto` 跨组，需要把这一点连同上述两个缺陷一起重新设计。

同批修正：第 6.2 节去掉响应窗口截短；路由可靠性页的 `RetryTimes` 旁补充说明可用总尝试上限（默认不限制）进一步封顶尝试次数；`GroupRetryTimes` 校验失败改为返回 i18n 文案（`setting.group_retry_times_invalid`），原始错误只写日志。
# 2026-09-29 审计修正：总尝试数改为总上游调用数

`RetryParam.TotalAttempts()` 的请求级计数覆盖该请求实际发起的所有上游调用，而不只覆盖 controller 重试循环的迭代次数：

- 普通 relay 尝试在 `BeginRelayAttempt` 中计数一次。
- 非流转流适配被拒后的普通非流回退，在真正发出前再计数一次。
- 回退前必须经过与普通重试相同的 `max_total_attempts` 和总时长预算检查。
- 分组切换仍只重置分组内 `Retry`，不会重置总调用计数。

因此 `max_total_attempts` 的用户可见语义是“单请求最多发起多少次上游调用”。该定义能直接限制上游负载与潜在成本，不允许内部兼容回退绕过。

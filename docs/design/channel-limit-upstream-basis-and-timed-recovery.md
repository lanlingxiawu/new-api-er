# 渠道每日上限：上游消耗口径与限时恢复

状态：**已实现**（2026-09-15）。基于 [channel-daily-quota-limit.md](channel-daily-quota-limit.md) 修改；本文描述的是实际代码逻辑。

## 1. 背景与目标

业务场景：某供应商渠道每天只能承受约 1–2 万的消耗，跑多了会被封；需要把量控制住、摊开跑，而不是一天刷完。

改动前的两个问题：

1. **统计口径与上游实际消耗对不上**
   - 「用户消费额」包含我方分组倍率：vip（倍率 10）的请求被放大 10 倍计入，上限过早触发；免费分组（倍率 0）的请求计为 0，上游却照样消耗。
   - 「采购成本」= 消费额 ÷ 分组倍率 × 成本系数，方向正确，但从消费额倒推：免费分组的流量同样计为 0（§4.1）。
2. **只能次日零点恢复**：一旦触顶就停到第二天，无法「每跑一段、歇一会儿」地摊开用量。

目标（用户确认）：

- 统计口径**只保留上游实际消耗**：`上游消耗 = 基础消耗（按模型价格计算、不乘我方分组倍率）× 渠道成本系数`。上游倍率即成本系数。「用户消费额」口径**不保留**（非本功能目的）。
- 恢复方式做成**可选项**：不自动恢复 / 次日零点恢复 / **达到上限 N 分钟后恢复**（恢复时开启新一轮，本轮用量从 0 重新计数）。N 的范围 1–10080（最长 7 天）。
- 管理员手动启用限时渠道**视为开启新一轮**。

不在范围：

- 不查询上游账户余额、不对接上游日志对账（实时性不足，且只有部分渠道类型支持）。
- 不改变平台成本报表与员工提成台账的口径（`calcCostQuota` 保持原样，见 §4.4）。
- 不改全局开关、时区、落库间隔等既有全局配置。

## 2. 用户可见变化

渠道编辑抽屉「每日金额上限」区块：

| 字段 | 改动前 | 改动后 |
|---|---|---|
| 统计口径 | 用户消费额（默认）/ 采购成本 | **移除**。一律按上游消耗统计，区块内以说明文案告知 |
| 恢复方式 | 开关「次日自动恢复」 | 下拉：不自动恢复 / 次日零点恢复 / 达到上限后 N 分钟恢复；选第三项时出现「恢复间隔（分钟）」输入框（默认 60） |
| 上限金额 | 输入框 | 不变 |

- 选「N 分钟后恢复」时，上限的含义是「**每一轮**最多消耗多少」：达到上限即禁用，N 分钟后自动启用并从 0 开始新一轮。
- 渠道列表的用量列：按日模式显示「今日已用」；限时模式显示「本轮已用」，tooltip 追加本轮开始时刻与预计恢复时刻。
- 筛选项「今日已达上限」改名为「已达上限」，含义是「当前正因上限而禁用」，不限哪天触发（跨过零点仍在等待恢复的限时渠道、关闭了自动恢复的渠道都算）。
- 批量设置对话框同步提供恢复方式与分钟数；按 Tag 编辑维持只改金额。

## 3. 数据模型

### 3.1 `channels` 表新增两列（AutoMigrate 增列，三库兼容，存量默认值即现状行为）

| 列 | 类型 | 默认 | 含义 |
|---|---|---|---|
| `daily_limit_recover_minutes` | `int` | `0` | `0` = 按日模式（次日零点恢复或不恢复，由既有 `daily_limit_auto_recover` 决定）；`>0` = 限时模式 |
| `daily_limit_period_start` | `bigint` | `0` | 限时模式下当前一轮的开始时刻（unix 秒）。服务端维护，客户端传入一律忽略 |

恢复方式与列的对应：

| 界面选项 | `daily_limit_auto_recover` | `daily_limit_recover_minutes` |
|---|---|---|
| 不自动恢复 | 0 | 0 |
| 次日零点恢复 | 1 | 0 |
| N 分钟后恢复 | 1（服务端强制） | N（1–10080） |

- 沿用 `daily_limit_auto_recover` 而不新增枚举列：存量数据无需迁移，按日模式的代码路径不变。
- 统计口径字段 `daily_limit_basis` 已从代码中删除；历史库里的列留着无害（AutoMigrate 不删列），客户端回传按只读字段忽略、不算敏感修改。
- `channel_daily_usages` 只保留 `cost_quota`（上游消耗）：`used_quota`、`limit_quota`、`disabled_at` 三个字段已从代码删除，历史库里的列留着无害。

写入规则（`model.Channel` / `model.DailyLimitEdit`）：

- `Channel.Update()` 显式 `Omit` 这两列：间隔只经 `DailyLimitEdit` 写入（map 更新，能写 0），轮次只由服务端写。
- `DailyLimitEdit.RecoverMinutes` 非 nil 时同一条 UPDATE 写：
  ```
  daily_limit_period_start   = CASE WHEN daily_limit_recover_minutes <> ? THEN now ELSE daily_limit_period_start END
  daily_limit_recover_minutes = ?
  daily_limit_auto_recover    = 1        -- 仅当 N > 0
  ```
  即「间隔变了（含按日 ↔ 限时切换）才开启新一轮，原样回传不重置」。**MySQL 的 SET 按书写顺序求值、右侧读到的是已更新的值**，因此 `period_start` 必须排在 `recover_minutes` 之前；GORM 对 map 更新按列名排序生成 SET，`daily_limit_period_start` < `daily_limit_recover_minutes` 恰好满足。SQLite / PostgreSQL 的 SET 右侧始终读旧值。此行为由 `TestDailyLimitEdit_RecoverMinutesChangeStartsNewPeriod`（真实 MySQL）锁定。
- 新建 / 复制渠道：`NormalizeDailyLimitRecovery(now)`——限时模式强制自动恢复并从当前时刻开第一轮；按日模式轮次置 0。

### 3.2 新表 `channel_limit_period_usages`

限时模式的「本轮用量」。与 `channel_daily_usages` 分开：后者的 `stat_date` 是自然日语义，混入轮次会破坏「今日用量」展示、日切清理与「已达上限」筛选。

```go
type ChannelLimitPeriodUsage struct {
    Id          int   `gorm:"primaryKey"`
    ChannelId   int   `gorm:"uniqueIndex:idx_channel_limit_period,priority:1"`
    PeriodStart int64 `gorm:"uniqueIndex:idx_channel_limit_period,priority:2;index:idx_channel_limit_period_start"`
    CostQuota   int64 `gorm:"bigint;default:0"`
    UpdatedAt   int64 `gorm:"bigint;default:0"`
}
```

索引与访问路径：

- 唯一索引 `(channel_id, period_start)`：flush 的 upsert 冲突键（`ON CONFLICT` / `ON DUPLICATE KEY` 由 GORM 生成），也是按轮查询与列表子查询的访问路径。
- 单列索引 `period_start`：保留期清理按时间范围删除。
- 每个限时渠道每轮一行。随既有 `retention_days` 清理，**但每个渠道当前一轮永远保留**（否则一个很久没触顶的限时渠道会被清零、放行一整轮额度），见 `CleanupChannelLimitPeriodUsage`（keyset 分批删除）。渠道删除时同步删除；孤儿行由周期性孤儿清理兜底。

### 3.3 记账载荷 `model.RelayLogAccountingPayload` 新增字段

```go
BaseQuota int64 `json:"base_quota,omitempty"` // 不乘分组倍率的基础消耗
```

载荷随降级 JSONL 落盘，新字段带 omitempty 向后兼容：旧记录读出为 0，按 §4.3 的兜底处理。

## 4. 口径计算

### 4.1 免费分组改动前为何计为 0

1. 所有计费公式都乘分组倍率，倍率 0 时结算额为 0；
2. `RecordCostAndSettleEmployeeCommission` 在 `quota == 0` 时提前返回，早于原来的限额累计调用；
3. `calcCostQuota` 在 `groupRatio == 0` 时返回 0。

### 4.2 新口径

```
上游消耗 = BaseQuota × 成本系数（model.GetChannelCostRatio，未配置时 1.0）
```

BaseQuota 在 relay goroutine 构造记账快照时算出（纯算术），经载荷带到异步记账管线；成本系数在管线上读取。`service/channel_daily_limit_upstream.go`：

- `upstreamBaseQuota(priceData, quota, explicit)`：
  1. `explicit` 非 nil（文本结算路径已按分组倍率 1 精确算过）直接采用；
  2. 分组倍率 > 0：各计费公式对分组倍率都是线性的，`quota ÷ 分组倍率` 即基础消耗；
  3. 分组倍率 = 0 且按次计费：`模型价格 × QuotaPerUnit × 其它倍率`；
  4. 其余（免费分组的按量计费且无 explicit）无法还原，计 0；负数（退款）一律 0——累计器只增不减。
- `upstreamQuota(base, costRatio)`：四舍五入到 quota。

各结算路径的 BaseQuota 来源：

| 路径 | 来源 |
|---|---|
| 文本（`calculateTextQuotaSummary`） | 同一函数内并行算一份分组倍率取 1 的额度：同样的 prompt/completion/缓存/音频输入/其它倍率，按次分支为 `modelPrice × QPU`；加工具附加费的基础额度（`toolSurchargeBase`）。经 `ConsumptionSettlementParams.UpstreamBaseQuota` 显式传入 |
| 阶梯表达式 | `TieredResult.ActualQuotaBeforeGroup` + 工具附加费基础额度（`tieredUpstreamBaseQuota`） |
| Wss / Audio（`service/quota.go`） | 不显式传入，按 `quota ÷ 分组倍率` 推导 |
| MJ（`EnqueueConsumeLogWithCost`） | 同上；免费分组按次计费走模型价格兜底 |
| 异步任务 | 预扣走 `EnqueueConsumeLogWithCost`（同上）；结算差额在 `recordTaskCostAndCommission` 里按任务账本价格推导。预扣 + 差额 = 最终额度，不重复计 |

与结算保持一致的细节：没有可计费用量时为 0；`UsageSource == "none"`（上游未交付）时与结算额一起清零。

已知偏差（均为保守方向或极小范围，接受）：

- 免费分组的 Wss / Audio / 按 token 重算的任务无法还原基础消耗，计 0（分组倍率 0 的实时音频流量极少）。
- 任务失败退款不回减累计（上游多半也已受理；限额是止损控制，宁多勿少）。
- 渠道成本系数显式配置为 0 时，上游消耗恒为 0，上限永远不会触发（`upstreamQuota` 对非正系数返回 0）。这是「成本系数 = 上游倍率」的字面语义；给这类渠道设上限前需先配置真实的成本系数。

部署验证（2026-09-15，HK 测试服，mock 渠道 13，成本系数临时设为 200）：5 笔请求结算额 28+60+10+8+60 = 166（分组倍率 1），本轮用量 33200 = 166 × 200，与 `channel_limit_period_usages` 一致；禁用后 89 秒（60 秒间隔 + ≤30 秒 tick）自动恢复并开启新一轮，新一轮 1 笔 10 × 200 = 2000。

### 4.3 累计位置

`costCommissionSnapshot.record()`（异步记账管线）在调用 `RecordCostAndSettleEmployeeCommission` **之前**调用 `recordChannelDailyUpstream`：免费分组的结算额为 0 会在后者提前返回；业务统计熔断打开时成本台账不落库，限额仍要照常累计。旧载荷 `BaseQuota == 0` 时回退为 `Quota ÷ GroupRatio`（与改动前的采购成本口径等价）。

`recordChannelDailyUpstream` 先查总开关与配置快照，未配置上限的渠道在读成本系数之前就返回。

改动前挂在 `UpdateChannelUsedQuota` 上的「用户消费额」观察者已删除。

### 4.4 与成本报表的差异

平台成本报表与员工提成仍用 `calcCostQuota`（免费分组成本为 0）。因此对免费分组流量，限额口径 > 报表成本。

## 5. 限时恢复

### 5.1 计数

- 累计器计数键 `dailyKey{statDate, channelId, period}`：按日键 `period=false`（statDate 为自然日），轮次键 `period=true`（statDate 存轮次开始时刻）。
- 每笔消耗都记按日键（「今日用量」展示）；限时渠道再记一份轮次键。**触顶判定只看 `limitKey`**：限时模式为轮次键，按日模式为今日键；限时模式的今日键永远不触发禁用。
- `periodStart` 取自配置快照（`LoadDailyLimitConfigs` 增读两列），每个 flush 周期刷新（默认 5 秒），本节点编辑 / 恢复后立即刷新。
- 轮次用量由同一个 flusher 落库到 `channel_limit_period_usages`，失败只回退尚未落库的部分（与按日相同）。
- `pruneOldEntries`：轮次键在渠道不再是限时模式、或已不是当前一轮时作废，清理 total / tripped / rearmed。

### 5.2 触顶与禁用

- 判定：`limit > 0 && 本轮用量 >= limit`，沿用 tripped / rearmed 机制。
- 禁用：`DisableChannelForTimedLimit(channelId, periodStart, today, reason)`，条件 `status = 1 AND daily_limit_recover_minutes > 0 AND daily_limit_period_start = ?`。**这是跨节点正确性的关键**：某节点快照还停在上一轮时，上一轮用量已满，它会立刻再提交一次禁用；条件式更新保证这种「旧轮次触发」必然落空。`daily_limit_disabled_date` 写触发时的自然日，供「已达上限」筛选。
- 按日禁用 `DisableChannelForDailyLimit` 与按日恢复 `RecoverChannelFromDailyLimit` 追加 `daily_limit_recover_minutes = 0`：两种模式的请求互不越界。
- `disableRequestStillValid`：限时请求要求渠道仍是限时模式且轮次未变；按日请求要求渠道仍是按日模式且日期仍为今天。
- 禁用原因：`本轮上游消耗 X，已达上限 Y，自动禁用，N 分钟后自动恢复并开始新一轮`；按日：`每日金额上限触发自动禁用：今日上游消耗 X，上限 Y`。金额按系统额度展示口径（`logger.FormatQuota`）输出并去掉多余的尾零，至少两位小数（`＄1.00`、`＄1.0048`），不再带「额度」后缀；「点数」口径输出 `N 点额度`。

### 5.3 恢复

恢复任务（只在主节点运行）tick 从 60 秒缩短为 30 秒，新增一类候选：

```sql
status = 3 AND daily_limit_disabled_at > 0 AND daily_limit_recover_minutes > 0
  AND daily_limit_auto_recover = 1
  AND daily_limit_disabled_at + daily_limit_recover_minutes * 60 <= ?   -- now
  [AND daily_limit_disabled_at >= enabledAfter]
```

恢复动作 `RecoverChannelFromTimedLimit`（条件式单条 UPDATE，同上条件）：`status = 1`、清零两个标记列、`daily_limit_period_start = now`（开启新一轮），并同步 abilities、写 `other_info.status_reason`。恢复后本节点立即刷新配置快照；其它节点靠 flush 周期收敛，期间的旧轮次禁用请求被 §5.2 的条件拒绝。恢复精度约 30 秒。按日模式的候选查询排除限时渠道，其余不变。

### 5.4 其它状态变化

| 事件 | 行为 |
|---|---|
| 管理员手动启用限时渠道（单个 / 批量 / 按 Tag） | 开启新一轮：`daily_limit_period_start = now`（否则本轮用量仍满，下一笔请求就会再次触发禁用）。按 Tag 启用只给**原本未启用**的渠道开新一轮，已在运行的渠道保持当前一轮（否则点一次启用就凭空多出一整轮额度）。禁用不开新一轮 |
| 修改恢复间隔（含按日 ↔ 限时切换） | 开启新一轮；原样回传不重置；只调金额不重置 |
| 只关闭自动恢复（不带恢复间隔） | 视为切到「不自动恢复」：恢复间隔一并归零，不留下「限时 + 不恢复」这种永远不会恢复的组合 |
| 跨越零点 | 限时模式不因零点重置轮次；「今日用量」照常按日统计 |
| 全局开关关闭 | 不累计、不禁用、不恢复；重新开启后沿用 `enabledAt` 过滤，不补做关闭期间的恢复 |
| 渠道被删除 | 丢弃按日与轮次两份内存状态；两张用量表的行一并删除 |
| 进程重启 | 未 flush 的增量丢失（软上限，与按日相同）；已落库的轮次用量从表中恢复 |

## 6. API 契约（无新增端点，权限不变）

| 接口 | 变化 |
|---|---|
| `POST/PUT /api/channel/` | 可带 `daily_limit_recover_minutes`（0 或 1–10080）；越界返回 `channel.daily_limit.invalid_recover_minutes`（已翻译）。`daily_limit_period_start`、`daily_limit_basis` 只读，传入忽略 |
| `POST /api/channel/batch/daily_limit`（`ChannelSensitiveWrite`） | 请求体 `daily_limit_basis` 移除，新增可选 `daily_limit_recover_minutes` |
| `GET /api/channel/`（列表/搜索） | `daily_usage`：`stat_date, cost_quota, limit_quota, disabled_at, recover_minutes, period_start, period_cost_quota, recover_at`。`disabled_at` 取渠道上的 `daily_limit_disabled_at`（恢复、启用时清零，不会停留在「已达上限」）；`recover_at` 仅限时模式且已被限额禁用时非 0。移除 `used_quota`、`basis` |
| `limit_filter=reached` | `daily_limit_disabled_at > 0`：当前正因上限而禁用 |
| `limit_filter=near`、`sort_by=daily_used/daily_usage_ratio` | 用量取 `cost_quota`；限时渠道取本轮用量（SQL 子查询按 `daily_limit_recover_minutes > 0` 分支到轮次表） |

`daily_limit_recover_minutes` 加入敏感字段集合与敏感变更比较（原样回传不算修改），权限语义与其它限额字段一致。

## 7. 配置参数

不新增全局配置。渠道级新增 `daily_limit_recover_minutes`（§3.1）。

## 8. 错误处理

- 分钟数越界：`channel.daily_limit.invalid_recover_minutes`，en / zh-CN / zh-TW 三份文案。
- 限额子系统的任何错误仍只记日志、绝不影响 relay 响应。
- 恢复任务单条失败记日志、下一轮重试；条件式更新保证重复执行幂等。

## 9. Main Chain Impact

在 relay goroutine 上**同步**执行的新增内容：

1. 结算时多算一份 BaseQuota：纯 decimal 算术，无 IO、无锁，写入记账快照的一个字段。

删除的同步内容：改动前挂在 `UpdateChannelUsedQuota` 上的收入口径观察者（每次结算一次分片锁 + map 累加）已移除——relay 路径上的限额开销比改动前更小。

状态变更时的标记清除（`UpdateChannelStatus` 入口，含上游报错自动禁用）：只对缓存里「配置了上限或带标记」的渠道发那条条件 UPDATE，其余渠道不多一次写库；同一渠道的并发调用合并为同一时刻最多一条；缓存同步只在确实清掉了标记时进行（缓存写要拿 relay 选渠道读的那把全局锁）。

**异步 / 后台**：上游消耗累计（记账管线 worker、任务结算 goroutine）、成本系数读取（内存缓存优先）、flush 落库、触顶禁用（有界队列 worker）、恢复（主节点 tick）。

特性开关：沿用 `channel_daily_limit_setting.enabled`，关闭即不累计、不禁用、不恢复；BaseQuota 的计算为纯算术，不单独加开关。

## 10. Shared Resource Audit

| 资源 | 本功能访问 | relay 是否访问 | 结论 |
|---|---|---|---|
| `channels` 新增两列 | 配置快照读、禁用/恢复条件更新 | relay 选渠道读缓存中的 `status`，不读新列 | 写发生在触顶/恢复瞬间，单行主键更新 |
| `channel_limit_period_usages` | flusher upsert、列表子查询、清理 | 不访问 | 隔离 |
| `channel_daily_usages` | 只写 `cost_quota` | 不访问 | — |
| 累计器分片 map | 记账 worker | 否（观察者已移除） | 与 relay 隔离 |
| 记账载荷 / 降级 JSONL | 多一个字段 | 由 relay 构造 | omitempty，向后兼容 |
| DB 连接池 | flusher 每周期多一批 upsert（每个活跃限时渠道一行） | 共用 | 批量、每周期一次 |

## 11. Concurrency Analysis（100k RPM）

- 每请求新增 DB 调用：0；Redis 调用：0。
- 每请求新增计算：一次 BaseQuota 算术。累计（异步）对限时渠道多一次 map 累加，与按日累加在同一次加锁内完成。
- 锁：沿用累计器分片锁，不新增锁。goroutine：不新增。
- 后台：flush 每周期新增 ≤ 活跃限时渠道数行 upsert；恢复 tick 30 秒一次，一条带条件的候选查询（`status`、`daily_quota_limit` 已有索引，候选集只含被限额禁用的渠道）。

## 12. 测试

| 层 | 文件 | 覆盖 |
|---|---|---|
| service | `channel_daily_limit_upstream_test.go` | `upstreamBaseQuota` 各分支（explicit / 倍率 1、2、0.8 / 免费分组按次含其它倍率 / 负数 / nil）；`upstreamQuota` 取整与非正数；累计入口的开关与未配置门槛 |
| service | `text_quota_upstream_base_test.go` | 文本按量在分组倍率 0 / 0.5 / 1 / 2 下基础消耗不变；按次；阶梯；无可计费用量为 0 |
| service | `channel_daily_limit_test.go` | 限时模式只按轮次判定、今日键不触发；复判与清理跟随当前一轮；排队请求在轮次变化 / 模式切换后作废；轮次落库与回读；失败回退分表；禁用原因文案 |
| model（MySQL） | `channel_limit_timed_test.go` | 旧轮次禁用落空；两种模式互不越界；恢复边界（差 1 秒不到期）与幂等；关闭自动恢复与非限额禁用不恢复；按日候选排除限时渠道；手动启用 / 按 Tag 启用开新一轮；`Update()` 不写两列；间隔变化才开新一轮（锁 MySQL SET 顺序）；轮次表 upsert、查询、保留期清理保留当前一轮 |
| model | `channel_daily_limit_query_test.go`、`channel_daily_usage_test.go` | 筛选只看 `cost_quota`、限时渠道看本轮；分钟数边界 0 / 1 / 10080 / 10081 / −1；配置快照读新列 |
| controller | `channel_daily_limit_edit_test.go`、`channel_test_internal_test.go` | 单渠道编辑写 0、回按日模式、原样回传不重置、改间隔开新一轮；无关保存不伪造轮次；越界报错已翻译；敏感 / 只读字段分类；列表回填本轮用量与恢复时刻；复制渠道开新一轮 |

前端：`bun run typecheck`；七种语言补齐新增文案。

## 13. 审查修订（2026-09-15）

对本功能（含前端）做了一轮审查，修复与删减如下。

修复：

- 按 Tag 启用会重置同 Tag 下正在运行的限时渠道的轮次 → CASE 追加 `status <> 1`（§5.4）。
- 渠道恢复后列表仍显示「已达上限」→ `daily_usage.disabled_at` 改取渠道标记列（§6）。
- 「已达上限」筛选名不副实 → 改为当前正因上限而禁用（§2、§6）。
- 只关闭自动恢复会留下「限时 + 不恢复」→ 恢复间隔一并归零（§5.4）。
- 每次渠道状态变更都多一次不受开关控制的写库 → 只对配置了上限或带标记的渠道执行（§9）。

删减（行为不变）：

- `daily_limit_basis` 字段；用量表的 `used_quota` / `limit_quota` / `disabled_at` 及每次禁用时补写它们的 `MarkChannelDailyUsageDisabled`（唯一用途就是那个会停留在「已达上限」的展示）。
- 渠道删除观察者及配置快照摘除（`dropChannelDailyState` / `evictDailyLimitConfigs`）：删除后残留的增量最多写回一行孤儿记录，由周期性孤儿清理删除。
- 「关闭自动恢复而保持禁用」的每日统计日志、禁用队列满的告警限频、只有测试在用的 `ClearDailyLimitMarks` / `UpdateChannelDailyLimitByTag`、未使用的 `DailyLimitConfig.DisabledStatDay`。
- 全局配置 `flush_interval_seconds`（改为常量 5 秒，设置页入口一并移除；库里残留的该配置项被忽略）。

未修复（已评估）：

- 按日模式下手动启用一个已达上限、且没调高上限的渠道，放行水位只存在本节点内存：其它节点或进程重启后的第一个同步周期会再次禁用它。实际影响可以忽略——同一场景下只要再有一笔消费，任何节点都会再次禁用（按日模式里「手动启用而不调高上限」本来只能放行到下一笔消费）。要彻底一致需要把水位持久化到渠道上，暂不做。

补充的已知偏差（§4.2）：

- 免费分组的 Wss / Audio 在上游未返回用量时计 0；流式失败且结算额为 0（免费分组恒为 0）的请求在构造记账快照之前就返回，不计入上游消耗。
- 总开关由关到开后，开关打开前被禁用的渠道在本进程内不补做自动恢复；master 重启后 `enabledAt` 归零，这些渠道会在下一个 tick 恢复。

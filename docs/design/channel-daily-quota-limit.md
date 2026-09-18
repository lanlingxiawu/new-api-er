# 渠道每日金额上限与自动禁用

> 2026-09-10 实现核对补充：本文“零 IO”“关闭即停止所有影响”“pending 最多两天”等表述不能视为当前全部分支的保证。队列满告警会同步调用 SysError；UpdateChannelStatus 的标记清除会发起条件 UPDATE；持续数据库故障会保留跨日 pending。当前边界与后续设计见 [整体设计第 6–8 节](uncommitted-changes-overview.md)。本补充未修改实现，也未重新运行文中历史测试。

> 2026-09-15 口径与恢复方式变更：「统计口径」（用户消费额 / 采购成本，`daily_limit_basis`）已移除，一律按**上游消耗**（基础消耗 × 成本系数）统计，`used_quota` 与收入观察者停用；恢复方式新增「达到上限 N 分钟后恢复」。同日审查后又删除了：全局配置 `flush_interval_seconds`（改为常量 5 秒）、渠道删除观察者（孤儿行由周期清理兜底）、用量行上的 `limit_quota` / `disabled_at` 及其写入、每日「保持禁用」统计日志、告警限频；「已达上限」筛选改为「当前正因上限而禁用」。本文中与这些相关的章节以 [channel-limit-upstream-basis-and-timed-recovery.md](channel-limit-upstream-basis-and-timed-recovery.md)（§13）为准。

状态：**已实现**（v4 设计 + 实现回填，见 §15）
日期：2026-09-03

修订历史：

- **v6**：验收阶段修订两处。① **单渠道新增/编辑的每日上限校验把 i18n key 原样返回给用户**（`渠道额外设置[channel setting] 格式错误：channel.daily_limit.invalid_amount`），批量与 Tag 路径走 `ApiErrorI18n` 一直是对的，两条单渠道路径此前不是——同一个校验两种文案，违反 Rule 9 / Rule 13，已补齐（§3.1）；② **删除渠道会留下孤儿用量行**，累计器内存状态未随删除回收（§5.5.3）。
- **v5**：第三轮审计（针对已实现代码）修订三处。① **单渠道编辑无法清除上限**——`Channel.Update()` 同样是 `Updates(struct)`，v3 只给批量/Tag 两条路径换了 map 更新，单渠道这条漏了，管理员清空金额保存后上限依旧生效（§11.12 第 3b 条）；② **手动启用被下一个 flush 周期无条件撤销**——重新武装只清 `tripped` 不记水位，`recheckAfterFlush` 会以「当日累计仍 ≥ 上限」为由在 ~5 秒内再次禁用，管理员当天无法人工干预，新增放行水位 `rearmed`（§5.5.2）；③ **最小上限校验从未生效**——`MinDailyQuotaLimit` 是常量 1，`limit > 0 && limit < 1` 恒为假，改为跟随 `common.QuotaPerUnit` 的函数（§3.1 校验表）。
- **v2**：v1 存在 4 个阻断级缺陷（成本口径公式错误、同步回调引入 Redis/DB 读、多节点日切模型不成立、禁用来源标记非原子），均已核实为真实问题并重写。另额外发现 1 个 v1 会引入的严重缺陷（`RegisterRelayLogAccountingHandler` 是单值覆盖注册，二次注册会静默顶掉成本/提成记账），见 §5.3。
- **v4**：按第二轮审计核实结果修订。6 个阻断问题中 5 个属实并已重写——并发状态机改为**条件式列更新**（§6.2、§6.3）、`tripped` 增加**重新武装**机制（§5.5.1）、成本改为**直接取用正式成本路径已算好的 `costRec.CostQuota`**（§5.3，同时取消了 v2/v3 的管线观察者方案）、禁用任务改为**独享有界队列**（§6.4）、补齐**设置接入链路 7 环**（§11.7）；金额换算改用项目现成的 `quotaUnitsToDollars` / `parseQuotaFromDollars` 并跟随 `getCurrencyLabel()`（§11.2）。另修正：`reached` 筛选条件、两级排序保证无上限渠道升降序都排末尾、搜索路径单独接入 `limit_filter`、Tag 模式禁用每日排序、工具栏隐藏 accessor column、Tag 行不聚合今日用量、配置快照跨节点刷新周期、全局开关重开后不补恢复、后端 i18n 实为 `keys.go` + 三个 YAML。唯一未采纳的审计意见是「前端 locale 须经 `add-missing-keys.mjs` + `i18n:sync`」——该脚本在本仓库不存在（§11.9 附核实说明）。
- **v3**：纳入两项此前列为「本期不做」的需求——**批量 / 按 Tag 设置每日上限**（§9.6、§11.12）与**限额筛选 + 按今日用量排序**（§9.4、§9.5、§11.10、§11.11）。这两项要求配置能进 SQL 的 `WHERE` / `ORDER BY` 并能原子批量更新，因此渠道级配置从 `channels.setting` JSON **改为 5 个真实列**（§3.1、§4.3）；禁用来源标记也随之从 `other_info` JSON 换成真实列，恢复扫描与「已达上限」筛选都变成索引查询。`relaykit` 因此不再需要改动。

## 1. 目标与范围

给每个渠道增加「每日金额上限」：当该渠道当日累计消耗金额达到上限后自动禁用（不再被路由选中），次日日切时按渠道级开关自动恢复启用。默认无上限，不改变任何现有渠道的行为。

范围内：

- 渠道级配置：每日上限金额、统计口径（用户消费额 / 采购成本）、次日是否自动恢复。
- 当日消耗的累计、持久化、跨节点汇总与展示。
- 达到上限时的自动禁用、通知、原子状态标记。
- 日切时的自动恢复、历史数据保留与清理。
- 全局总开关与时区配置（可热更新）。
- 渠道列表展示「今日已用 / 上限」，被本功能禁用时给出明确原因。
- **批量与按 Tag 设置每日上限**（勾选行批量 + 现有 Tag 编辑对话框两条通道）。
- **限额筛选**（已设 / 未设 / 今日已达上限 / 接近上限）与**按今日用量、今日使用率排序**。

范围外：

- 按 Token / 用户 / 分组的每日上限（已有独立的用户额度与令牌额度体系）。
- 按小时 / 按月的上限；达到上限前的预警通知（`near` 筛选已可主动发现，主动推送属独立设计）。
- 上限前的降级路由——现有渠道禁用 + 重试机制已覆盖。
- 用量趋势图（`channel_daily_usages` 已留 90 天历史，具备数据基础，但图表与查询接口是独立改动）。

## 2. 已确认的关键决策

| 决策点 | 选择 |
|---|---|
| 统计口径 | 两者可选：每个渠道各自选择「用户消费额」或「采购成本」 |
| 日切时间 | 可配置时区，默认 `Asia/Shanghai`，每日 00:00 切分 |
| 达上限后恢复 | 渠道级开关，默认「次日自动恢复」 |

## 3. 配置项

### 3.1 渠道级配置（`channels` 表新增真实列）

> **v3 变更**：v1/v2 把配置放在 `channels.setting` JSON 里（免迁移）。新增的「限额筛选 + 排序」与「批量/按 Tag 设置」两项需求推翻了这个前提，改为真实列。

改用真实列的三条理由，都是新需求直接推出的：

1. **筛选与排序必须能进 SQL。** JSON 列的解析函数三库不一致（Rule 2 禁止 PostgreSQL 专有的 JSONB 运算符；MySQL 5.7 的 `JSON_EXTRACT` 与 SQLite json1 语法也不同），无法写出跨库通用的 `WHERE` / `ORDER BY`。
2. **批量与按 Tag 设置。** JSON 需要逐渠道 read-modify-write（N 次查询，且与并发保存互相覆盖）；真实列一条 `UPDATE channels SET daily_quota_limit = ? WHERE tag = ?` 原子完成。
3. **观察者需要零 IO 的配置快照。** 真实列可用一条窄查询刷新（`WHERE daily_quota_limit > 0`，命中索引，数百行）；JSON 要全表加载并逐行解析。

`channels` 新增 5 列：

| 列 | 类型 | 说明 |
|---|---|---|
| `daily_quota_limit` | `bigint default 0`，加索引 | 每日上限（quota 单位，与 `used_quota` 同单位）。`<= 0` 表示无上限 |
| `daily_limit_basis` | `varchar(16) default ''` | `revenue` / `cost`；空串归一化为 `revenue` |
| `daily_limit_auto_recover` | `*int default 1` | 次日是否自动恢复。沿用本表 `AutoBan *int gorm:"default:1"` 的跨库布尔写法，不用 `bool` |
| `daily_limit_disabled_at` | `bigint default 0` | 本功能禁用的时刻；`0` 表示当前不是被本功能禁用 |
| `daily_limit_disabled_date` | `bigint default 0` | 触发禁用时对应的 `StatDate`，恢复判定用 |

```go
// model/channel.go
DailyQuotaLimit       int64  `json:"daily_quota_limit" gorm:"bigint;default:0;index"`
DailyLimitBasis       string `json:"daily_limit_basis" gorm:"type:varchar(16);default:''"`
DailyLimitAutoRecover *int   `json:"daily_limit_auto_recover" gorm:"default:1"`
DailyLimitDisabledAt  int64  `json:"daily_limit_disabled_at" gorm:"bigint;default:0"`
DailyLimitDisabledDate int64 `json:"daily_limit_disabled_date" gorm:"bigint;default:0"`
```

后两列**取代** v2 中写进 `other_info` JSON 的 `disable_source` / `disable_stat_date`，是严格更优的方案：原子性同样由单次 `Save` 保证；恢复扫描从「全扫 + 逐行解析 JSON」变成索引查询；「今日已达上限」可以直接作为 SQL 筛选条件；前端也不必解析 `other_info`。

**`relaykit/dto/channel_settings.go` 因此不再改动**，v2 中关于该独立 module 需单独验证构建的要求随之不适用（结论保留在此备查：`relaykit` 确实是独立 module，日后若真要改它，必须跑 `cd relaykit && GOWORK=off go build ./...`）。

保存校验（`Channel.ValidateSettings()`，`model/channel.go:1100`；批量与 Tag 路径复用同一校验函数）：

| 校验 | 失败行为 |
|---|---|
| `DailyQuotaLimit < 0` | 拒绝保存，`channel.daily_limit.invalid_amount` |
| `0 < DailyQuotaLimit < MinDailyQuotaLimit()` | 拒绝保存，避免误填极小值导致渠道立刻被禁 |
| `DailyLimitBasis` 不在 `{"", "revenue", "cost"}` | 拒绝保存，`channel.daily_limit.invalid_basis` |

> **v5 修订**：`MinDailyQuotaLimit` 原本是常量 `1`，于是 `limit > 0 && limit < 1` 对 `int64` 恒为假——这条校验从来没生效过，注释里的保护并不存在（默认换算下 1 quota ≈ $0.000002，填了它渠道第一个请求结算完就被禁用）。现改为函数 `MinDailyQuotaLimit()`，返回「一分钱」对应的 quota（`common.QuotaPerUnit / 100`，兜底不小于 1）。取函数而非常量是因为 `QuotaPerUnit` 可配置（Tokens 模式 / 自定义货币会改它），写死常量在这些配置下等于没有下限。只在保存时校验，不影响存量配置——`LoadDailyLimitConfigs` 不做校验。前端 `channel-form.ts` 的 zod schema 同步加了 `value === 0 || value >= 0.01` 的 refine，让管理员在提交前就能看到提示。

**权限分类（`controller/channel_authz.go` 的 fail-closed 扫描要求每个新列被显式分类）**：

- `daily_quota_limit`、`daily_limit_basis`、`daily_limit_auto_recover` → `channelSensitiveFields`，并在 `channelHasSensitiveChanges` 中各加一条精确的 old-vs-new 比较。这与 v2 把配置放在 `setting`（本就是敏感字段）时的权限语义完全一致，属于金额管控该有的门槛。
- `daily_limit_disabled_at`、`daily_limit_disabled_date` → `channelReadOnlyFields`，并在 `clearChannelReadOnlyFields` 中清零，防止客户端伪造禁用来源标记。

### 3.2 全局配置（`setting/operation_setting/channel_daily_limit_setting.go`）

按 Rule 12 新增热更新配置，用 `RegisterSnapshot` 无锁快照读（同 `relay_timeout_setting.go`）。

| 配置键 | 默认值 | 说明 |
|---|---:|---|
| `channel_daily_limit_setting.enabled` | `true` | 功能总开关（Rule 0 要求的即时关停开关） |
| `channel_daily_limit_setting.timezone` | `Asia/Shanghai` | 日切时区，非法时保存被拒；运行期解析失败回退 `time.Local` 并 `SysError` |
| `channel_daily_limit_setting.flush_interval_seconds` | `5` | 内存增量落库间隔，1–60 |
| `channel_daily_limit_setting.retention_days` | `90` | `channel_daily_usages` 保留天数，7–3650 |

新增 `ResolveChannelDailyLimitLocation(tz) (*time.Location, string)`，解析结果按时区串缓存，避免每次 `time.LoadLocation`。

管理端入口：系统设置 → 模型与路由 → 「路由与可靠性」区块（`routing-reliability-section.tsx`），与渠道自动禁用、渠道测试模式并列。

## 4. 数据模型

### 4.1 新表 `channel_daily_usages`

```go
type ChannelDailyUsage struct {
    Id         int   `json:"id"`
    StatDate   int64 `json:"stat_date"   gorm:"uniqueIndex:idx_channel_daily_usage,priority:1;index:idx_channel_daily_usage_date"`
    ChannelId  int   `json:"channel_id"  gorm:"uniqueIndex:idx_channel_daily_usage,priority:2"`
    UsedQuota  int64 `json:"used_quota"  gorm:"bigint;default:0"` // 用户消费额口径
    CostQuota  int64 `json:"cost_quota"  gorm:"bigint;default:0"` // 采购成本口径
    LimitQuota int64 `json:"limit_quota" gorm:"bigint;default:0"` // 触发禁用时的上限快照（审计用）
    DisabledAt int64 `json:"disabled_at" gorm:"bigint;default:0"` // 本功能自动禁用时间（审计用）
    UpdatedAt  int64 `json:"updated_at"  gorm:"bigint;default:0"`
}
```

`StatDate` = 配置时区下当日 00:00 的 unix 秒（与 `PlatformChannelDailyStat.StatDate` 语义一致）。

> **`DisabledAt` 仅用于审计与展示，不作为恢复的数据来源**（v1 缺陷：若禁用成功但 usage 行写入失败，该渠道将永不恢复）。恢复的唯一依据是渠道自身的原子状态标记，见 §6.3、§7。

索引：

| 索引 | 列 | 访问模式 |
|---|---|---|
| `idx_channel_daily_usage`（唯一） | `(stat_date, channel_id)` | 累计 upsert、单渠道读取、列表批量读 `stat_date = ? AND channel_id IN (...)` |
| `idx_channel_daily_usage_date` | `(stat_date)` | 历史清理的 keyset 扫描 |

容量：渠道数 × 保留天数（500 × 90 ≈ 4.5 万行）。

### 4.2 迁移与跨库写法

`model/main.go` 的 `AutoMigrate` 加入 `&ChannelDailyUsage{}`。新表 + 基础类型，三库无方言差异。

累计 upsert 用 GORM `clause.OnConflict`（同 `model/business_daily_stats.go`），由 GORM 转写为 MySQL `ON DUPLICATE KEY UPDATE` / PG、SQLite `ON CONFLICT DO UPDATE`，以单测在真实项目库上验证。

**历史清理不使用 `DELETE ... LIMIT`**（PostgreSQL 不支持该语法，v1 缺陷）。改为 keyset 两段式，三库通用：

```go
// 1) 按主键取一批
DB.Model(&ChannelDailyUsage{}).
   Where("stat_date < ? AND id > ?", cutoff, lastId).
   Order("id ASC").Limit(1000).Pluck("id", &ids)
// 2) 按主键删除
DB.Where("id IN ?", ids).Delete(&ChannelDailyUsage{})
```

### 4.3 `channels` 表列迁移

5 个新列全部通过 `AutoMigrate(&Channel{})` 的 `ADD COLUMN` 完成。SQLite 只禁止 `ALTER COLUMN`，`ALTER TABLE ... ADD COLUMN` 是支持的（Rule 2），MySQL / PostgreSQL 无问题。

存量数据由列默认值兜底，语义正好是「本功能对现有渠道完全无影响」：

| 列 | 存量渠道取值 | 语义 |
|---|---|---|
| `daily_quota_limit` | `0` | 无上限 |
| `daily_limit_basis` | `''` | 归一化为 `revenue` |
| `daily_limit_auto_recover` | `1` | 次日自动恢复（仅在设了上限后才有意义） |
| `daily_limit_disabled_at` / `_date` | `0` | 不是被本功能禁用 |

`daily_quota_limit` 上的索引服务于两类查询：配置快照刷新（`WHERE daily_quota_limit > 0`）与「已设上限 / 未设上限」筛选。`channels` 表本身规模为数百行，索引开销可忽略。

## 5. 记账数据流（v1 阻断问题 #1、#2 的修订）

### 5.1 核实到的事实

| 事实 | 位置 |
|---|---|
| 平台正式成本口径是 `cost = revenue / group_ratio × cost_ratio`，非 `revenue × cost_ratio` | `service/employee_commission.go:299` `calcCostQuota` |
| `GetChannelCostRatio` 在 L1 内存未命中/过期时会读 Redis 并回源 DB | `model/employee.go:136-170` |
| 结算 `PostTextConsumeQuota` 运行在 relay 请求 goroutine 上（`DoResponse` 之后、handler 返回之前） | `relay/claude_handler.go:228` 等 13 处 |
| 收入流与成本流的覆盖范围**不同**：违规费 `EnqueueConsumeLog(..., nil)` 不产生成本记账，但计入 `used_quota`；音频路径 `LedgerQuota` 可能为 0 而 `quota > 0` | `service/violation_fee.go:132,150`；`service/quota.go:222,244` |

结论：v1 的 `cost = quota × costRatio` 确实与正式成本账口径不一致（分组倍率被误算进成本，例如分组倍率 0.5 时成本被低估一半，导致渠道该禁不禁）；v1 在同步回调里调 `GetChannelCostRatio` 确实会在缓存未命中时给 relay goroutine 引入 Redis + DB 读，违反 Rule 0。**两项均确认为真实缺陷。**

但审计建议的「把记账整体搬到现有异步成本快照」只可部分采纳：收入流与成本流覆盖范围不同，若收入也改从成本快照取，违规费和 `LedgerQuota=0` 的音频消费将不计入每日用量——功能会被静默缩小。

### 5.2 双源记账（修订方案）

两个口径各自对齐**自己的**权威账本，互不串味：

| 口径 | 挂钩点 | 与哪本账完全一致 |
|---|---|---|
| `revenue`（用户消费额） | `model.UpdateChannelUsedQuota` 入口观察者 | `channels.used_quota`，即渠道列表「已用额度」列 |
| `cost`（采购成本） | 正式成本路径就地取 `costRec.CostQuota`（§5.3） | 与平台成本报表**算法一致**（同一次成本系数读取、同一个公式）；不承诺与落库台账逐行一致 |

```
① 收入流（同步，纯内存）
relay 结算 → model.UpdateChannelUsedQuota(channelId, quota)
   └─ channelUsedQuotaObserver(channelId, quota)
        ├─ 快照读总开关；关闭 → return
        ├─ pending[(statDate, channelId)].used += quota      ← 分片锁 + map，无 IO
        └─ 本地预判越限 → 投递到有界禁用队列（§6.4），非阻塞

② 成本流（异步，复用正式成本路径的计算结果）
relay 结算 → EnqueueConsumeLogWithCost → 日志管线 worker（异步）
   └─ RecordCostAndSettleEmployeeCommission
        costRec := buildConsumptionCostRecord(...)           ← 既有：读一次成本系数并算好
        pending[(statDate, channelId)].cost += costRec.CostQuota   ← 新增一行，纯内存
        本地预判越限 → 提交禁用（在守卫之前，不受熔断影响）

③ flusher（每 flush_interval_seconds，所有节点）
   取出并清空 pending（按其自带的 statDate 落库）→ upsert → 回读汇总 → 刷新 total → 复判越限

④ 日切（所有节点做本地状态切换；仅 master 做恢复与清理）
```

`calcCostQuota` 从 `service/employee_commission.go` 提升为包内可复用函数（当前已是包内私有函数，直接调用即可，不复制公式）。成本流里调用 `GetChannelCostRatio` 是安全的——该调用点已在异步日志管线 worker 上，与既有的 `buildConsumptionCostRecord` 处于同一执行上下文。

**收入流观察者的 IO 清单：零。** 只做：一次原子快照读、一次日期边界比较（缓存的 `[dayStart, dayEnd)` 原子读，跨界才重算）、一次分片互斥锁 + map 累加。

### 5.3 成本口径直接复用正式成本记录（v3 阻断问题 #3 的修订）

v3 让成本观察者挂在日志管线上并**自己再算一遍** `calcCostQuota(GetChannelCostRatio(...))`。两个问题已核实：

1. **重复读取成本系数。** 正式成本记录已经在 `service/employee_commission.go:41` 的 `buildConsumptionCostRecord` 里读过一次 `GetChannelCostRatio` 并算出 `costRec.CostQuota`；再读一次，两次之间若管理员改了成本系数，两个数字就会不同。
2. **「与 `consumption_costs.cost_quota` 完全一致」是过度承诺。** `model.CreateConsumptionCost` 只是把记录推入内存缓冲，由后台批量入库（`model/consumption_cost.go:28-44`），该文件注释明确写着「缓冲超限丢弃或刷盘永久失败的记录不会被计入」。外围观察者既不知道是否重复、也不知道是否最终落库。

修订：**不再新增日志管线观察者**，改为在正式成本路径上就地取值。

```go
// service/employee_commission.go —— 两个调用点，各加一行
costRec := buildConsumptionCostRecord(relayInfo, quota, logId, createdAt)
recordChannelDailyCost(costRec.ChannelId, costRec.CostQuota)   // ← 新增，纯内存
guard, ok := model.BeginBusinessStatsSideEffect(...)
```

放在 `buildConsumptionCostRecord` 之后、`BeginBusinessStatsSideEffect` 守卫之前，因此**不受熔断器开启与缓冲丢弃影响**——熔断时成本台账可能不落库，但每日限额仍然照常累计（限额是止损控制，宁可算到也不能漏算）。

调用点只有 `service/employee_commission.go` 的 `RecordCostAndSettleEmployeeCommission` 一处。

**口径承诺相应收紧**（v3 把两件事混为一谈）：

| 承诺 | 是否成立 |
|---|---|
| 与正式成本**计算口径**一致（同一个 `costRec.CostQuota`，同一次成本系数读取） | ✅ 成立 |
| 与最终**落库台账**逐行一致 | ❌ 不承诺。缓冲丢弃、刷盘失败、`log_id` 唯一索引去重都会让台账少于累计值 |

前端「采购成本」口径的说明文案据此调整：成本口径与平台成本报表**算法一致**，但限额累计发生在落库之前，极端情况下（成本缓冲溢出）二者可能有微小差异。

**副作用：v3 的 `AddRelayLogAccountingObserver` 方案整体取消。** `model/relay_log_pipeline.go` 不再需要改动，v2 发现的「`RegisterRelayLogAccountingHandler` 单值覆盖注册会静默顶掉成本/提成记账」风险也随之完全消失——这条结论保留在此备查：该变量确实是单值覆盖语义（`model/relay_log_pipeline.go:84-88`），日后任何人想再挂一个 handler 都必须先改成追加式，否则会顶掉现有记账。

### 5.4 收入观察者的挂钩实现

`model` 不能 import `service`，用原子指针注册：

```go
// model/channel.go
var channelUsedQuotaObserver atomic.Pointer[func(channelId int, quota int)]

func UpdateChannelUsedQuota(id int, quota int) {
    if fn := channelUsedQuotaObserver.Load(); fn != nil && quota > 0 {
        (*fn)(id, quota)   // 内部 recover，纯内存
    }
    if common.BatchUpdateEnabled {
        addNewRecord(BatchUpdateTypeChannelUsedQuota, id, quota)
        return
    }
    updateChannelUsedQuota(id, quota)
}
```

只在入口触发一次；批量模式下的延迟落库走内部 `updateChannelUsedQuota`，不重复触发。`quota <= 0` 忽略。选此单点而非在 8 个调用点分别插入，是为了保证任何现有与将来新增的计费路径都不会漏记。

### 5.5 内存累计结构（v1 阻断问题 #3 的修订）

**键包含日期**，日期在每次记录时就地确定，不依赖任何进程级「当前日期」变量：

```go
type dailyKey struct { statDate int64; channelId int }
type dailyDelta struct { used, cost int64 }

type shard struct {
    mu      sync.Mutex
    pending map[dailyKey]*dailyDelta   // 未落库增量
    total   map[dailyKey]dailyTotal    // 上次 flush 回读的当日汇总
    tripped map[dailyKey]struct{}      // 已提交禁用，防重复
}
// 32 分片，shard = channelId % 32
```

日期跨越自然发生：0 点之后的记录落到新的 `dailyKey`，旧 key 的残留增量在下一次 flush 时**按它自己的 `statDate`** 落库，不会串到新的一天。flush 后清除 `statDate < 今日` 的 `total` / `tripped` 条目，内存自然回收。

这样每个节点都天然正确，不需要「master 通知非 master 重置」，v1 中「非 master 节点日期不切换、把新一天消费写进旧日期」的问题从模型上消失。

#### 5.5.1 `tripped` 的重新武装（v3 阻断问题 #2 的修订）

v3 只在跨日时清 `tripped`，与 §12 承诺的「管理员手动启用后继续消费会再次禁用」直接矛盾——没有任何路径在启用时清除标记，实际结果是**当天再也不会触发禁用**。

修订：`tripped` 在以下四种情况被清除，其中第 1 条是修复本缺陷的主路径。

| 触发 | 位置 | 说明 |
|---|---|---|
| 1. 观察到渠道重新变为启用 | 配置快照刷新（§5.6） | 快照查询顺带取 `status` 与 `daily_limit_disabled_at`；某渠道有 `tripped` 但已是 `status=1` → 清除，重新武装 |
| 2. 禁用的条件更新未命中 | 禁用 worker | `RowsAffected = 0`（状态已不是 1）→ 清除，交下轮重判 |
| 3. 禁用队列已满被丢弃 | 提交入口 | 清除并限频告警，下个 flush 周期重试 |
| 4. 跨日 | flush | 清除 `statDate < 今日` 的条目 |

第 1 条的收敛时间 = 配置快照刷新周期（§5.6，默认 5s），即管理员手动启用后最多 5 秒本节点即完成重新武装；其它节点同理，因为快照是各节点各自刷新的。

必须有回归测试：**禁用成功 → 手动启用 → 再消费 1 quota → 再次禁用**。

#### 5.5.2 放行水位 `rearmed`（v5 修订：只清标记会让手动启用形同虚设）

上表第 1、2 条只清 `tripped` 是不够的，实现里补了一份**放行水位** `shard.rearmed[key]`（清标记时的用量快照）。

缺陷：渠道达到上限被禁用后，当日累计**始终**压在上限之上。管理员手动启用 → 下一轮 flusher 先做第 1 条重新武装，紧接着同一轮的 `recheckAfterFlush` 复判发现「合计 ≥ 上限」→ **在没有任何新消费的情况下立刻再次禁用**。管理员看到的是「点了启用，5 秒后又变回禁用」，且当天无法人工干预——因为调低不了（清除上限当时也不生效，见 §11.12）。这与本节承诺的「继续消费才会再次禁用」不符。

修订后的判定（`limitReachedLocked`）：

```
越限 = 合计 >= 上限  且  （无放行水位  或  合计 > 放行水位）
```

三条约束：

1. **水位只在真的清掉了一个 `tripped` 标记时记录**。从未 `tripped` 的渠道不写水位，因此「管理员把上限调低到今日已用之下 → 下个周期立即禁用」这条既有行为完全不受影响。
2. **上表第 3 条（队列满）与禁用出错时不记水位**，仍用 `clearTripped` 原样重判——那两种情况禁用没能落地，必须重试。
3. **第 2 条（条件更新未命中）改记水位**（`releaseTripped`）。渠道此时已不是启用态，重试没有意义：不记水位会让 `recheckAfterFlush` 每个 flush 周期空转提交一次必定失败的禁用请求（两次 DB 往返）直到日切；更糟的是标记早被清掉，管理员之后手动启用时第 1 条找不到 `tripped`、也就不记水位，本缺陷会从这条路径原样复现。

水位随 `pruneOldEntries` 一起按日清理，并在重新 `tripped` 时丢弃（一次性）。

回归测试：`TestDailyLimit_ManualEnableSurvivesRecheck`（无新消费不得撤销、反复复判也不得）、`TestDailyLimit_ManualEnableThenFlushRecheck`（其它节点推高的用量要能重新触发）、`TestDailyLimit_LoweredLimitStillDisablesImmediately`（调低上限仍即时禁用）、`TestDailyLimit_AlreadyDisabledChannelDoesNotSpin` 与 `TestDailyLimit_DisableWorkerSkipsNonEnabledChannel`（第 2 条记水位）、`TestDailyLimit_PruneClearsRearmMarks`。

#### 5.5.3 渠道删除后的状态回收（v6，验收时发现）

累计器的 pending 增量活在内存里，删除渠道并不会清掉它——下一个 flush 周期会把它
upsert 回 `channel_daily_usages`，留下一条指向已不存在渠道的孤儿记录。E2E 验收后
实测到过一条（`channel_id=26 used=5000 limit=0`）。

修法分两层：

1. **删除时回收**。四条删除路径（`Channel.Delete` / `BatchDeleteChannels` /
   `DeleteChannelByStatus` / `DeleteDisabledChannel`）都汇聚到
   `finalizeChannelDeletion`，在那里：
   - 先经 `channelRemovedObserver`（与 `channelUsedQuotaObserver` 同一套注册机制，
     因为 model 不能 import service）让累计器丢掉该渠道的 **pending / total /
     tripped / rearmed** 四类状态；
   - 再 `DeleteChannelDailyUsageByChannelIds` 删库行。

   **顺序不能反**：先删行的话，内存里残留的 pending 会在下一个周期把行写回来。

2. **周期性兜底**。上面仍留有一个 flush 周期的窗口：若删除恰好发生在 flusher 已经把
   pending 取走、还没落库的瞬间，那批增量还是会写出一行。因此 master 的清理任务
   （`cleanupChannelDailyUsageHistory`）追加一次 `DeleteOrphanChannelDailyUsage`，
   用 `channel_id NOT IN (SELECT id FROM channels)` 保证最终一致。

回归测试：`TestChannelDailyUsage_DeleteByChannelIds`、`TestChannelDailyUsage_DeleteOrphans`
（同时验证 `NOT IN (子查询)` 在真实库上可用）、`TestDailyLimit_DropChannelStateOnDelete`
（含「drain 之后不得再为已删渠道产出增量」这条关键断言）、
`TestDailyLimit_DropChannelStateIsSafeOnEmpty`。

#### 5.5.4 排队中的禁用请求必须重新确认（审计 #2）

禁用请求是在**提交时刻**判定的，worker 执行时世界可能已经变了。队列容量 256、
flush 失败还会退避，请求确实可能在队列里停留可观的时间。原实现直接执行，于是：
关掉总开关、清掉渠道上限、或者已经跨日之后，一个陈旧请求仍然会把渠道禁掉。

`handleDisableRequest` 现在先过 `disableRequestStillValid`，四个条件缺一不可：

1. 总开关仍开着（Rule 0：关掉就必须立即停止一切副作用）；
2. 请求的 `statDate` 仍是当前自然日；
3. 渠道仍配置了正数上限；
4. 按**当前**上限与口径重算，累计仍然越线（上限可能被调高）。

不成立时用 `clearTripped`（**不是** `releaseTripped`）——这不是「已经禁用过了」，
而是「这次判定作废」，要让下一轮 recheck 按当前配置重新判。

回归：`TestDailyLimit_QueuedDisableDropped{WhenSwitchOff,WhenLimitCleared,WhenLimitRaised,AfterDayRollover}`
与 `..._StillValidWhenNothingChanged`。

#### 5.5.5 按 Tag 改上限要锁定改名前的渠道 ID（审计 #8）

`EditTagChannels` 原本在 `EditChannelByTag` 之后按**新标签名**更新每日上限。
把 A 改名成一个已存在的 B 之后，按 B 更新会把原本就属于 B 的渠道一起改掉。
改为在改名**之前**用 `GetChannelIdsByTag` 锁定 ID 集合，之后按 ID 更新。
回归：`TestChannelDailyLimit_TagRenameDoesNotWidenScope`。

#### 5.5.6 统计口径的敏感变更判定要比归一化值（审计 #6）

迁移出来的历史渠道 `daily_limit_basis` 是空串，前端回填等价的 `"revenue"`。
`channelHasSensitiveChanges` 裸比字符串，把「什么都没改」判成敏感修改，
于是只有普通渠道写权限的管理员连改个名字都会被拒。改为比
`NormalizeDailyLimitBasis` 之后的值。回归：`TestChannelAuthz_EmptyBasisEqualsRevenue`。

### 5.6 配置快照与跨节点刷新

观察者需要 `(limit, basis)`，但绝不能在同步路径上读 DB 或缓存。因此维护一份进程内快照：

```go
var limitSnapshot atomic.Pointer[map[int]channelLimitConfig]  // channelId -> {limit, basis, autoRecover}
```

刷新时机与来源：

- **周期刷新**：每个 flush tick（默认 5s）执行一次窄查询，所有节点各自刷新，这就是跨节点变更的传播上限：
  ```sql
  SELECT id, daily_quota_limit, daily_limit_basis, daily_limit_auto_recover,
         status, daily_limit_disabled_at
  FROM channels WHERE daily_quota_limit > 0
  ```
  命中 `daily_quota_limit` 索引，结果为「配置了上限的渠道」，量级数十到数百行。顺带承担 §5.5.1 第 1 条的重新武装判断。
- **本节点立即失效**：单渠道保存、批量、按 Tag 编辑成功后主动触发一次刷新，本节点无延迟。
- 快照为空（无任何渠道配置上限）时，观察者在第一行就返回，全功能零开销。

v3 只写了「失效配置快照」而没有定义跨节点周期，是真实缺口；这里明确为「≤ 1 个 flush 周期」。

**日期边界缓存**：观察者用 `[dayStart, dayEnd)` 的原子缓存避免每次 `time.Date`。时区配置变更时必须一并失效该缓存——挂在同一次快照刷新里检查时区串是否变化，变化则重算边界。

## 6. 触发禁用

### 6.1 判定

```
basisTotal = (basis == cost) ? total.cost + pending.cost : total.used + pending.used
if limit > 0 && basisTotal >= limit → 触发（该 dailyKey 打 tripped 标记，防重复提交）
```

用 `>=`（达到即禁用）。

### 6.2 禁用动作 —— 条件式列更新（v3 阻断问题 #1 的修订）

v3 认为「一次 `SaveWithoutKey`」即可保证原子性。这是错的，已核实 `model.UpdateChannelStatus`（`model/channel.go:826`）有三个并发缺陷：

1. **先写内存缓存再写 DB**（`CacheUpdateChannelStatus` 在 `SaveWithoutKey` 之前）：DB 失败会留下缓存与 DB 不一致。
2. **目标状态相同时直接返回**（`:870` 的 `if channel.Status == status { return false }`）：渠道先被限额禁用、随后在途请求报错触发 `DisableChannel(status=3)` 时，第二次调用直接返回，**限额标记不会被清除**，次日会把一个真坏的渠道自动恢复。
3. **全量 `Save` 是读改写**：与管理员并发启用时，后执行的一方会整行覆盖前一方。

修订：本功能**不走 `UpdateChannelStatus`**，改用条件式列更新，且不引入对该函数的高风险重写。

```go
// model/channel.go —— 只在「当前是启用状态」时才禁用，一条语句完成状态与标记的转换
res := DB.Model(&Channel{}).
    Where("id = ? AND status = ?", channelId, common.ChannelStatusEnabled).
    Updates(map[string]any{
        "status":                    common.ChannelStatusAutoDisabled,
        "daily_limit_disabled_at":   now,
        "daily_limit_disabled_date": statDate,
        "other_info":                /* status_reason / status_time 同步写入 */,
    })
// RowsAffected == 1 才算赢得竞争；随后再同步内存缓存与 abilities
```

顺序固定为 **DB 成功 → 更新内存缓存 → `UpdateAbilityStatus`**，与 v3 的「先缓存后 DB」相反，DB 失败时不产生任何可见状态变化。

`RowsAffected == 0` 表示渠道已不是启用态（被管理员禁用、被报错禁用、或另一节点抢先），此时**清除本节点的 `tripped`**（§5.5.1 第 2 条）并放弃本次禁用，交由下一个 flush 周期重判。

多 Key 渠道：金额上限是渠道级的，条件更新同样只作用于 `channels.status`，不触碰 `MultiKeyStatusList`。

### 6.3 标记列的清除 —— 覆盖状态未变更的路径

标记列的核心不变式是：**`daily_limit_disabled_at > 0` ⟺ 最近一次禁用来自本功能**。破坏它的唯一路径就是 v3 漏掉的「状态相同直接返回」。

修订：在 `service.DisableChannel` / `EnableChannel` 与管理员启停这些**非本功能**的状态变更入口，先无条件执行一条窄更新清除标记，再走原有逻辑：

```sql
UPDATE channels SET daily_limit_disabled_at = 0, daily_limit_disabled_date = 0
WHERE id = ? AND daily_limit_disabled_at > 0
```

这条语句独立于后续的状态写入，但**失败方向是安全的**：

- 清除成功、后续状态写入失败 → 标记没了，最坏结果是次日不自动恢复该渠道，渠道保持禁用等人工处理——安全；
- 反过来「标记残留 + 渠道实际是坏的」才是危险方向，而清除动作在前，这个方向不会发生。

命中主键且带 `daily_limit_disabled_at > 0` 条件，绝大多数调用是零行更新，开销可忽略；这些路径都不在 relay 热路径上（报错禁用发生在重试判定阶段，本就要写 DB）。

**同渠道并发合并**（§15.3 #10）：上游故障时，禁用瞬间的在途失败会在同一渠道上**并发**调用 `UpdateChannelStatus`（各自在 `gopool` 协程里），每条 UPDATE 都占一条与 relay 共用的连接。`clearDailyLimitMarksIfPresent` 因此按渠道合并：同一时刻最多一条在跑；调用方只认「在它到达之后才开始」的那一次清除（到达时已在跑的那条可能早于另一节点写入标记，不能搭车），所以正确性与逐个执行完全相同。实测 100 个并发调用 → 2 条 UPDATE、同时在途最多 1 条。

覆盖面核实：

- 管理员手动启停（单个 `controller/channel.go:1235`、批量 `:1258`）→ `model.UpdateChannelStatus` → 入口清除；
- 上游报错禁用 / 测试恢复 → `service.DisableChannel` / `EnableChannel` → 同一入口；
- `EnableChannelByTag` / `DisableChannelByTag` 用裸 `Update("status", ...)`，实现时直接在同一条 `UPDATE` 的赋值里带上两列清零，无需额外语句。

**恢复判定谓词**（可整条下推 SQL，不依赖时间戳比较、不依赖 usage 表）：

```sql
status = 3 AND daily_limit_disabled_at > 0 AND daily_limit_disabled_date < <今日 StatDate>
```

### 6.4 有界禁用队列（v3 阻断问题 #4 的修订）

v3 用 `gopool.Go` 提交禁用任务并声称「全局新增常驻 goroutine：2」。已核实 `gopool` 的默认池容量是 `math.MaxInt32`（`bytedance/gopkg@v0.1.3/util/gopool/gopool.go:30`），是无界池，与 Rule 8.2「用 worker pool 而非无限 goroutine」冲突；那张开销表也只算了常驻 goroutine，漏了瞬时任务。

修订为本功能独享的有界队列：

```go
disableQueue := make(chan disableRequest, 256)   // 容量 = 预期渠道数量级
// 固定 2 个 worker，for range disableQueue，每个任务独立 recover
```

- **队列满时不阻塞**：`select { case ch <- req: default: }` 直接丢弃，同时清除该渠道的 `tripped`（§5.5.1 第 3 条）并按渠道限频告警（每 5 分钟至多一条），下个 flush 周期自然重试。提交入口可能在 relay goroutine 上（收入流观察者），因此**绝不允许阻塞**。
- 队列与 worker 与全局 `gopool` 完全隔离，不参与共享池的容量竞争。
- 开销表相应更正为：常驻 goroutine = 1 flusher + 2 禁用 worker + 1 master tick = 4，且不随渠道数或请求量增长。

### 6.5 与渠道自动测试恢复的冲突

`controller/channel-test.go:selectChannelsForAutomaticTest` 在被动恢复模式下会挑选所有 `ChannelStatusAutoDisabled` 渠道测试，通过即启用。金额上限禁用的渠道 Key 是好的，测试必过，会被立刻错误恢复。

处理两处：

1. `selectChannelsForAutomaticTest` 跳过满足 §6.3 前两个条件的渠道（当日的也跳过），跳过时记 SysLog。
2. `service.ShouldEnableChannel` 目前签名是 `(newAPIError *types.NewAPIError, status int)`（`service/channel.go:67`），**没有渠道信息**。核实其非测试调用点只有 1 处（`controller/channel-test.go:978`），且该处 `channel` 对象就在作用域内。改为 `ShouldEnableChannel(newAPIError *types.NewAPIError, channel *model.Channel) bool`，内部取 `channel.Status` 并追加每日上限豁免；同步更新该调用点与 `service/gen3_tasks2_test.go` 中的既有断言。

## 7. 日切、恢复与清理（v1 阻断问题 #3 的修订）

拆成两个职责，分别由不同节点承担：

**A. 本地状态切换 —— 每个节点都执行**（随 flusher 的每次 tick 顺带完成，不需要独立的日期变量）：

- flush 时按每条 pending 自带的 `statDate` 落库；
- flush 后清除 `statDate < 今日` 的 `total` / `tripped` 条目。

因为 §5.5 的 key 已含日期，这一步是纯内存清理，无论节点是否 master、是否刚重启、是否跨过零点，行为都正确。

**B. 恢复与清理 —— 仅 master 节点**（`common.IsMasterNode`，1 分钟 tick，同 `commission_tier_reset_task.go`）：

恢复做成**幂等扫描**，不依赖进程内「上次日切日期」，因此 master 停机跨过零点后重启也能正常补做（v1 缺陷）：

v3 起谓词全部由真实列承载，候选集可以整条下推到 SQL，不再需要「拉回所有 `status=3` 渠道再逐个解析 JSON」：

```sql
-- 候选集：渠道表自身状态，不查 usage 表
SELECT id, daily_limit_auto_recover FROM channels
WHERE status = 3 AND daily_limit_disabled_at > 0 AND daily_limit_disabled_date < ?
```

`daily_limit_auto_recover = 1` 的条件**下推到 SQL**（v3 把它留在 Go 里，导致关闭自动恢复的渠道每分钟被扫描并记一条日志，会持续刷屏）：

```sql
SELECT id FROM channels
WHERE status = 3 AND daily_limit_disabled_at > 0
  AND daily_limit_disabled_date < ? AND daily_limit_auto_recover = 1
```

对每个候选执行**条件式更新**，与 §6.2 同样避免读改写与并发覆盖：

```sql
UPDATE channels
SET status = 1, daily_limit_disabled_at = 0, daily_limit_disabled_date = 0
WHERE id = ? AND status = 3 AND daily_limit_disabled_at > 0 AND daily_limit_disabled_date < ?
```

`RowsAffected = 1` 才视为恢复成功，随后同步内存缓存与 abilities、`NotifyRootUser` 通知并记一条 SysLog；`= 0` 说明状态已被他人改动，静默跳过。

「因关闭自动恢复而保持禁用」的渠道不再逐轮记日志：单独一条计数查询，每天首次跳过时记录一条汇总（「N 个渠道因关闭自动恢复保持禁用」），保留可观测性而不刷屏。

清理：每日执行一次（以 `stat_date` 是否已有更早清理记录判断，或简单地每次 tick 都跑一次 keyset 删除——无待删行时是一次命中索引的空查询），按 §4.2 的 keyset 两段式删除。

时区变更：管理员改时区后，新记录落到新时区计算出的 `statDate`。跨时区切换可能把同一自然日切成两段统计——这是管理员显式操作的结果，在设置项文案中提示。夏令时由 `time.Date(y,m,d,0,0,0,0,loc)` 天然处理；默认 `Asia/Shanghai` 无夏令时。

进程重启：内存 `total` 为空，首次 flush 回读补齐当日真实汇总，上限不会失效；渠道禁用状态本就在 DB 中。

## 8. 主链路影响（Rule 0）

### 8.1 同步 vs 异步

| 执行点 | 同步/异步 | 成本 |
|---|---|---|
| 收入累计（`UpdateChannelUsedQuota` 观察者） | 同步（relay goroutine，结算阶段） | 1 次原子快照读 + 1 次日期边界原子比较 + 1 次分片锁 map 累加。**零 IO、零 Redis、零 DB** |
| 成本累计（记账管线观察者） | **异步**（日志管线 worker） | `GetChannelCostRatio`（可能 Redis/DB）+ decimal 计算，均已脱离 relay goroutine |
| 提交禁用 | **异步**（本功能独享的有界队列 + 2 个固定 worker，§6.4） | 每渠道每天至多 1 次；队列满时丢弃并重试，绝不阻塞 |
| flush 落库 | 异步 | 每 5s 一次批量 upsert |
| 恢复 / 清理 | 异步（master） | 每分钟一次窄扫描 |

**失败静默**：观察者、flusher、日切任务全部 `recover()` 兜底，失败只写 SysLog，绝不向上传播。

**总开关**：`enabled = false` 时观察者第一行返回，flusher 与恢复任务空转，可即时关停。

### 8.2 共享资源审计

| 资源 | 本功能的使用 | 主链路是否触碰 | 结论 |
|---|---|---|---|
| DB 表 `channel_daily_usages` | 新表，仅本功能读写 | 否 | 无冲突 |
| DB 表 `channels` | 触发禁用/恢复时写（每渠道每天 ≤ 2 次）；master 每分钟一次 `status=3` 的 id 扫描 | 主链路只读内存缓存 | 写频率极低，与既有报错禁用同路径 |
| DB 表 `abilities` | 经 `UpdateAbilityStatus` 间接写，同上频率 | 主链路读（渠道选择） | 与既有报错禁用完全同路径 |
| DB 连接池 | flusher 每 5s 1 个连接；master 每分钟数个 | 是（共享池） | 占用可忽略 |
| Redis | 收入流不使用；成本流仅经既有的 `GetChannelCostRatio`（本功能不新增任何 Key） | 该缓存主链路结算已在读 | 无新 Key 命名空间，无冲突 |
| 日志管线 worker | 新增一个观察者，纯计算 + 内存累加 | 该管线承载既有成本/提成记账 | 独立 recover，不影响主 handler；单次成本 ≈ 一次 map 写 |
| 内存结构 | 32 分片 map，键 (日期, 渠道)，容量 = 渠道数 × 至多 2 天 | 否 | 有界 |
| Goroutine | 1 flusher + 2 禁用 worker + 1 master tick，全部独享，不使用全局 `gopool`（其默认池容量为 `math.MaxInt32`，无界） | 否 | 固定数量，与 relay 共享池隔离 |

**跨节点一致性**：不引入 Redis、不引入分布式锁。各节点把本地增量以原子 `used_quota + delta` upsert 汇总到同一行，flush 后回读全局汇总再判定，符合 Rule 0「用隔离而非热路径协调解决冲突」。

### 8.3 100k RPM 下的开销

| 指标 | 数值 |
|---|---:|
| 每请求新增 relay goroutine 上的 DB / Redis 调用 | 0 |
| 每请求新增 relay goroutine 上的锁获取 | 1 次分片互斥锁（持有 ~100ns；100k RPM ≈ 1667 QPS 摊到 32 个锁） |
| 每请求新增 goroutine | 0 |
| 每请求异步侧新增 | 1 次 map 累加（成本值直接取自正式成本路径已算好的 `costRec.CostQuota`，**不新增成本系数读取**，§5.3） |
| 全局新增 DB 写 | 每 5s 一次 upsert × 当期活跃渠道数 |
| 全局新增常驻 goroutine | 4（1 flusher + 2 禁用 worker + 1 master tick），不随渠道数或请求量增长 |

### 8.4 这是软上限，不是财务硬额度

必须在设计、设置项文案与前端提示中明确（v1 的「最坏超出 5 秒消费量」表述不准确）。实际超出量由三部分叠加：

1. **跨节点未 flush 的增量**：至多一个 flush 周期（默认 5s）的全网消费；
2. **禁用瞬间已在途的并发请求**：这些请求已经通过渠道选择，会继续执行并在完成后结算计入，禁用不会中断它们；高并发下这部分可能显著大于第 1 项；
3. **单笔大额请求**：一次长上下文请求本身就可能一次性跨过上限。

因此本功能是**运营性软上限**（防止某渠道单日消耗失控），不承诺精确的财务硬限额。管理员可调小 `flush_interval_seconds` 换取更快收敛，但无法消除第 2、3 项。

## 9. API 契约

### 9.1 渠道读写（复用现有接口，无新增端点）

- `POST /api/channel/`、`PUT /api/channel/`：请求体直接带 §3.1 的三个配置列（`daily_quota_limit` / `daily_limit_basis` / `daily_limit_auto_recover`），走 `ValidateSettings()`。权限沿用 `AdminAuth()`。
- 权限细节：三个配置列按 §3.1 归入 `channelSensitiveFields`，因此改上限需要 `ChannelSensitiveWrite`——与 v2 把配置放在 `setting`（本就是敏感字段）时的门槛完全一致，不是新增限制。前端「渠道额外设置」卡片本就整块包在 `<fieldset disabled={sensitiveLocked}>` 内，无权限者看到既有置灰与提示。
- 两个标记列（`daily_limit_disabled_at` / `_date`）是服务端管理字段，客户端传入会被 `clearChannelReadOnlyFields` 清零，无法伪造禁用来源。

### 9.2 渠道列表返回今日用量

`Channel` 新增非持久化字段（同 `AccountBalance` 模式）：

```go
DailyUsage *ChannelDailyUsageView `json:"daily_usage,omitempty" gorm:"-"`

type ChannelDailyUsageView struct {
    StatDate   int64  `json:"stat_date"`
    UsedQuota  int64  `json:"used_quota"`
    CostQuota  int64  `json:"cost_quota"`
    LimitQuota int64  `json:"limit_quota"`
    Basis      string `json:"basis"`
    DisabledAt int64  `json:"disabled_at"`
}
```

`controller/channel_authz.go` 的 fail-closed 扫描要求每个新增 `model.Channel` 字段被显式分类，`daily_usage` 必须加入 `channelReadOnlyFields`（同 `account_balance`），否则 ChannelWrite 管理员的编辑请求会被误判为敏感变更。

填充：列表接口返回前对本页渠道 id 做一次 `WHERE stat_date = ? AND channel_id IN (...)`（命中唯一索引）；本页无任何配置上限的渠道则跳过查询。叠加本节点未 flush 的 pending（多节点下只反映本节点，属展示层近似，UI 不做精确承诺）。仅管理端路径，符合 Rule 8.1。

### 9.3 全局设置

复用 `PUT /api/option/`，键 `channel_daily_limit_setting.*`，ConfigManager 自动持久化。无新增端点。

### 9.4 限额筛选（列表查询新增 `limit_filter`）

`controller/channel.go:113` 的 `buildChannelListQuery(group, statusFilter, typeFilter)` 是列表的唯一查询构造点，同时服务于分页数据、`Count` 总数与 tag 模式，新增筛选加在这里，三条路径自动一致。签名扩展为 `buildChannelListQuery(group, statusFilter, typeFilter, limitFilter, statDate)`。

`limit_filter` 取值与 SQL：

| 取值 | 含义 | 条件 | 是否需要 JOIN |
|---|---|---|---|
| `all`（默认/空） | 不过滤 | — | 否 |
| `configured` | 已设上限 | `daily_quota_limit > 0` | 否 |
| `unlimited` | 未设上限 | `daily_quota_limit <= 0` | 否 |
| `reached` | 今日已达上限 | `daily_limit_disabled_at > 0 AND daily_limit_disabled_date = <今日>` | 否 |
| `near` | 接近上限（≥80% 且未触发） | 见下 | 是 |

前四种全部落在 `channels` 自身的列上，命中 `daily_quota_limit` 索引，零额外开销。只有 `near` 需要今日用量，才追加 LEFT JOIN：

```sql
LEFT JOIN channel_daily_usages u
       ON u.channel_id = channels.id AND u.stat_date = ?
WHERE channels.daily_quota_limit > 0
  AND channels.daily_limit_disabled_at = 0
  AND (CASE WHEN channels.daily_limit_basis = 'cost'
            THEN COALESCE(u.cost_quota, 0)
            ELSE COALESCE(u.used_quota, 0) END)
      >= channels.daily_quota_limit * 0.8
```

跨库要点（Rule 2）：

- `LEFT JOIN` + `CASE WHEN` + `COALESCE` 全部是 ANSI SQL，SQLite / MySQL 5.7 / PostgreSQL 9.6 三库通用，不使用任何方言函数。
- JOIN 走 `channel_daily_usages` 的唯一索引 `(stat_date, channel_id)`，**一对一**，因此 `Count(&total)` 不会因 JOIN 放大行数。这一点必须有测试锁住（§13）。
- 阈值 `0.8` 定义为后端常量 `DailyLimitNearThreshold`，前端展示的「接近上限」配色用同一阈值，避免两边漂移。

> **`reached` 必须带上 `daily_limit_disabled_date = 今日`**（v3 只写了 `daily_limit_disabled_at > 0`）。关闭了自动恢复的渠道会一直保持禁用状态、标记列也一直非零，只用前半个条件会把它在往后每一天都算成「今日已达上限」。这类往日遗留的渠道通过既有的「状态 = 自动禁用」筛选加列表文案识别，不再单列一个筛选项。

**搜索接口不能只改 `buildChannelListQuery`**（v3 的实现路径是错的）。已核实 `/api/channel/search` 的非 Tag 分支走 `model.SearchChannels`（`model/channel.go:401`），它不接收 `statusFilter`，controller 拿到结果后在 **Go 里循环过滤** status 与 type（`controller/channel.go:369-400`），整条搜索路径也没有 SQL 分页。

本期不重构搜索路径（那是独立改动），但 `limit_filter` 必须在 SQL 层生效，否则 `near` 的 JOIN 无从谈起：给 `model.SearchChannels` 增加 `limitFilter` 与 `statDate` 入参，与 `buildChannelListQuery` **共用同一个条件构造 helper**：

```go
func applyChannelLimitFilter(q *gorm.DB, limitFilter string, statDate int64) *gorm.DB
func applyDailyUsageJoin(q *gorm.DB, statDate int64) *gorm.DB   // near / 用量排序共用，避免重复 JOIN
```

这样列表与搜索在限额筛选上行为一致；status / type 在搜索路径上仍走既有的 Go 过滤，不动它，避免把一次功能新增变成搜索路径重构。

### 9.5 按今日用量排序

`model.ChannelSortOptions.Apply` 目前基于 `channelSortColumns` 白名单 + `clause.OrderByColumn`，只能表达单列排序，无法表达 `CASE` / 除法。扩展为「白名单列 + 白名单表达式」两类：

| `sort_by` | 语义 | 排序表达式 |
|---|---|---|
| `daily_used` | 今日用量（按各渠道自身口径） | `COALESCE(CASE WHEN basis='cost' THEN u.cost_quota ELSE u.used_quota END, 0)` |
| `daily_usage_ratio` | 今日用量 / 上限 | `<上述用量> * 1.0 / NULLIF(channels.daily_quota_limit, 0)`，外层由两级排序保证无上限渠道位置（见下） |

用 `clause.OrderBy{Expression: gorm.Expr(...)}` 表达，仍然只接受白名单内的 key，不拼接用户输入。命中这两个 key 时自动追加与 §9.4 相同的 LEFT JOIN（同一个 helper 构造，避免重复 JOIN）。

三处跨库陷阱，实现与测试都要覆盖：

1. **整数除法**：`used * 1.0 / limit` 必须显式乘 `1.0`，否则 SQLite 与 MySQL 走整数除法，比率全部退化成 0 或 1。
2. **除零**：`NULLIF(daily_quota_limit, 0)` 保证无上限渠道得到 `NULL` 而不是报错（PostgreSQL 会直接抛 division by zero）。
3. **NULL 排序位置三库不同**：PostgreSQL 默认 `NULLS LAST`（ASC），MySQL / SQLite 把 NULL 视为最小值。v3 用 `COALESCE(..., -1)` 统一，但那只在**降序**时把无上限渠道排到末尾，升序时它们会跑到最前面——而 UI 与测试都承诺「恒排末尾」。修订为**两级排序**，与升降序无关：

```sql
ORDER BY (CASE WHEN channels.daily_quota_limit > 0 THEN 0 ELSE 1 END) ASC,
         <用量或使用率表达式> DESC|ASC
```

第一级把「有上限」固定排在前、「无上限」固定排在后；第二级才是用户选择的升降序。三库对 `CASE` 表达式排序的行为一致，也不再依赖 NULL 排序规则。

`NewChannelSortOptions` 增加 `statDate` 入参（JOIN 条件需要），由 controller 在构造时按当前时区计算传入。

**Tag 模式下不支持每日用量排序。** 已核实 Tag 模式的流程是「先分页 Tag，再逐个 Tag 查询其子渠道」（`controller/channel.go:150-177`），对子渠道排序不会改变 Tag 本身的顺序，语义无意义。本期取「工作量小」的方案：`tag_mode=true` 时后端忽略 `sort_by=daily_used|daily_usage_ratio` 回退默认排序，前端在 Tag 模式下把这两个排序项置灰并说明原因（§11.11）。按 Tag 聚合用量排序需要改造 Tag 分页查询，留作独立改动。

排序属于管理端路径，`channels` 表规模为数百行，JOIN 后仍是小结果集排序，符合 Rule 8.1 对非 relay 路径的要求。

### 9.6 批量与按 Tag 设置每日上限

两条通道，都因为 §3.1 改成真实列而变成一条原子 `UPDATE`，不需要读改写：

**(1) 按 Tag** —— 复用现有 `PUT /api/channel/tag`：

`ChannelTag` 结构（`controller/channel.go:864`）增加三个指针字段（`nil` = 本次不修改该字段，与现有 `Priority` / `Weight` 语义一致）：

```go
DailyQuotaLimit       *int64  `json:"daily_quota_limit"`
DailyLimitBasis       *string `json:"daily_limit_basis"`
DailyLimitAutoRecover *int    `json:"daily_limit_auto_recover"`
```

`model.EditChannelByTag` 当前已是 9 个位置参数的长签名，再加 3 个不可维护。借此把它改为接收一个 `ChannelTagEdit` 结构体，同步改造现有调用点与测试（纯重构，不改行为）。

**(2) 勾选行批量** —— 新增 `POST /api/channel/batch/daily_limit`：

```json
{ "ids": [1,2,3], "daily_quota_limit": 5000000, "daily_limit_basis": "cost", "daily_limit_auto_recover": 1 }
```

放在 `router/api-router.go` 中已应用 `AdminAuth()` 的渠道分组内（Rule 11，不单独再挂中间件）。响应沿用 `common.ApiSuccess`，返回实际影响行数。

两条通道的共同要求：

- **权限**：与 `ParamOverride` / `HeaderOverride` 的既有做法一致（`controller/channel.go:945`），需 `authz.ChannelSensitiveWrite`，否则 `ApiErrorI18n(c, i18n.MsgAuthInsufficientPrivilege)`。这与 §3.1 的单渠道编辑权限语义一致。
- **校验**：复用 §3.1 的同一套校验函数，不另写一份。
- **收尾**：`model.InitChannelCache()`（与现有 tag 编辑一致）+ 失效每日上限配置快照 + `recordManageAudit`（`channel.tag_edit` / 新增 `channel.batch_daily_limit`）。
- **不做隐式恢复**：批量调高上限**不会**自动启用已被禁用的渠道，与 §12 单渠道编辑的行为保持一致。对话框必须提示这一点，否则管理员会以为「调高了就自动恢复了」。

## 10. 错误处理（Rule 9 / Rule 10）

| 场景 | 处理 |
|---|---|
| 保存渠道时上限非法 | `ApiErrorI18n(c, "channel.daily_limit.invalid_amount")` |
| 口径值非法 | `ApiErrorI18n(c, "channel.daily_limit.invalid_basis")` |
| flush upsert 失败 | `SysError`，**增量保留在内存中，退避重试（5s→10s→…→60s 封顶），告警按渠道限频（每 5 分钟至多一条）** |
| 触发禁用时状态更新失败 | `SysError`，清除 `tripped` 标记，下个 flush 周期重判重试 |
| 恢复单个渠道失败 | `SysError`，继续处理其余渠道 |
| 时区非法 | 保存时校验 `time.LoadLocation`，失败拒绝保存；运行期回退 `time.Local` + `SysError` |
| 观察者 / goroutine panic | `recover()` + `SysError` |

> v1 的「连续失败 12 次后丢弃 pending」已删除（审计意见正确）：pending 是每（日期, 渠道）一个定长计数器，容量上界是渠道数 × 2 天，**不随请求量增长**，没有丢弃的必要；丢弃会造成金额永久少计，正是本功能最不该出现的错误。

日志：后台任务与观察者用 `common.SysLog` / `common.SysError`（非请求上下文，Rule 10）；禁用与恢复各记一条并 `NotifyRootUser`（复用既有去重）。

## 11. 界面设计

以下每一处都已对照现有组件源码确认，控件、样式类名与数据管线均复用既有实现，不引入新的样式方案（Rule 6）。

### 11.1 渠道编辑抽屉 —— 字段落位

位置：抽屉 → 「高级设置」折叠区 → **「渠道额外设置 / Channel Extra Settings」** 卡片（`channel-mutate-drawer.tsx:4224` 的 `CardHeading`），与 Force Format / Thinking to Content / Pass Through Body 等同属渠道级开关，语义一致。

该卡片整体已包在 `<fieldset disabled={sensitiveLocked} className='space-y-4 disabled:opacity-60'>` 内，并在无权限时显示既有的 Alert 提示。三个配置列在后端同属 `channelSensitiveFields`（§3.1），前后端权限语义一致。

三个控件，各自复用卡片内已有的排版结构：

| 控件 | 复用的现有写法 | 位置 |
|---|---|---|
| 每日金额上限 | `<Input type='number' />` 布局同 `priority` / `weight`（`:3799`），但**值必须经货币换算**，见 §11.2 | 放在 `divide-y` 开关组**之上**，用 `<div className='grid gap-4 sm:grid-cols-2'>` 与「统计口径」并排 |
| 统计口径 | `Select` + `SelectTrigger`/`SelectValue`/`SelectContent alignItemWithTrigger={false}`/`SelectGroup`/`SelectItem`，同 `http_protocol`（`:4409`） | 同上一行右侧 |
| 次日自动恢复 | `FormItem className='flex items-center justify-between px-4 py-3'` + `Switch`，同 `pass_through_body_enabled`（`:4295`） | 并入下方 `divide-border divide-y border-y` 开关组 |

联动与禁用态（用户视角，Rule 6）：

- 上限为空或 0 时，「统计口径」与「次日自动恢复」置为 `disabled`，并在描述里说明「填写上限后可配置」——避免用户在无效状态下调整选项却不知为何不生效。
- 「统计口径」选「采购成本」且该渠道未配置成本系数时，`FormDescription` 追加「该渠道未配置成本系数，按 1.0 计算，与消费额口径等价」。成本系数就在同一表单的 `cost_ratio` 字段，可直接 `form.watch('cost_ratio')` 判断，无需额外请求。
- 上限输入框的描述必须写明这是软上限：「达到上限后自动禁用；已在处理中的请求会正常完成，因此当日实际消耗可能略微超出上限」。
- **字段标签不写死「美元」**，必须跟随 `getCurrencyLabel()`——站点可能配置为 CNY、自定义货币或 Tokens 模式，写死会直接误导管理员填错数量级。

### 11.2 渠道编辑抽屉 —— 三处必须同步的管线（易漏）

`channel-mutate-drawer.tsx` 中有三个手工维护的字段清单，新增表单字段若不同步登记会产生静默 bug：

1. **`SENSITIVE_FORM_FIELDS`（`:272`）** —— 决定字段是否受 `sensitiveLocked` 控制。`setting` 派生的字段（`force_format`、`proxy`、`http_protocol` 等）都已在列表中，三个新字段必须一并加入；否则无 `ChannelSensitiveWrite` 权限的管理员能在界面上编辑，保存时却被后端拒绝。
2. **`hasAdvancedSettingsValues()`（`:360`）** —— 决定「高级设置」折叠区是否显示为「已配置」。需加入 `values.daily_quota_limit_amount`（>0）与 `values.daily_limit_basis !== 'revenue'` 判断，否则配了上限的渠道折叠区看起来是空的，用户找不到自己设过的值。
3. **`channel-form-errors.ts` 的字段分区表（`:38`）** —— 决定校验错误能否定位并自动展开到对应分区。

以及 `channel-form.ts` 的三段式：zod schema、`DEFAULT_FORM_VALUES`、`transformChannelToFormDefaults`。v3 起三个字段是渠道对象上的真实列，**不再进 `buildSettingJSON`**。

**金额换算必须用项目现成的双向函数，不能自己乘 `QuotaPerUnit`**（v3 只写了「统一走 `QuotaPerUnit`」，不够）：

| 方向 | 函数 | 位置 |
|---|---|---|
| 回填表单：quota → 当前展示金额 | `quotaUnitsToDollars(units)` | `web/src/lib/format.ts:105` |
| 提交：展示金额 → quota | `parseQuotaFromDollars(amount)` | `web/src/lib/format.ts:83` |

这两个函数处理了裸乘除处理不了的三种情况：**Tokens 模式**（此时数值就是 quota 本身，不做任何换算）、**自定义货币**与**汇率**（`meta.exchangeRate`）。默认 `QuotaPerUnit=500000` 时，若直接 `Number(e.target.value)` 提交，管理员填 `50` 会被存成 50 quota 而不是 50 美元，渠道几乎立刻被禁用——这是必须避免的事故。

同一套换算与标签规则适用于**批量对话框与 Tag 对话框**（§11.12），三处不得各写一份。

需要补的测试（前端无测试框架，作为实现自检项写入本节）：舍入（`Math.round` 后的往返一致性）、最小值（1 quota 对应的展示金额）、极大值（不溢出 `Number.MAX_SAFE_INTEGER`）、Tokens 模式下不换算。

这也顺带消除了 v2 方案里的一个隐患：`buildSettingJSON` 每次保存都会重建整个 `setting` 对象，任何一次遗漏都会把上限一起抹掉；真实列没有这个问题。

### 11.3 渠道列表 —— 今日用量

宿主：`channels-columns.tsx` 的 `BalanceCell`（`balance` 列，表头为 `Used / Remaining`）。在既有的 Used / Remaining 两个 `StatusBadge` 下方追加一行今日用量，仅当该渠道**配置了上限**时渲染。

必须遵守该单元格已有的四条约束（否则会与现有行为不一致）：

| 约束 | 现有实现 | 本功能的处理 |
|---|---|---|
| 敏感信息遮罩 | `sensitiveVisible` 为假时金额显示 `SENSITIVE_MASK = '••••'` | 今日用量同样遮罩，只保留「今日」标签 |
| 卡片布局不显示货币符号 | `showSymbol: layout !== 'card'`（`ChannelRowActionsLayoutContext`） | 沿用同一 `layout` 判断 |
| 长数字降级为紧凑记法 | 超过 `MAX_INLINE_BALANCE_CHARS = 8` 时改用 `compact` + `locale`，精确值放 Tooltip | 今日用量同样处理，`$12.34 / $50.00` 超长时压缩为 `$1.2万 / $5万` |
| Tag 聚合行 | `isTagAggregateRow` 为真时只显示累计 used_quota | **Tag 行既不显示上限也不显示今日用量。** 上限是渠道级配置，聚合无意义；今日用量更不能聚合——子渠道可能有的用 revenue 口径、有的用 cost 口径，两种金额相加没有业务含义。要看明细展开 Tag 即可 |

阈值配色复用该文件已有的 `StatusBadge` variant，不新增颜色：

- < 80%：`variant='neutral'`（与 Used 徽标一致）
- ≥ 80% 且未达上限：`variant='warning'`
- 已达上限：`variant='destructive'`

Tooltip 内展示精确值与口径：`今日已用：$12.3456（采购成本口径） / 上限：$50.0000`。

无上限的渠道不渲染该行、不占位、不显示骨架屏——避免给绝大多数未启用本功能的渠道增加视觉噪音。后端对这类渠道也不返回 `daily_usage`（§9.2），前后端一致。

### 11.4 渠道列表 —— 禁用原因

`channels-columns.tsx:998` 的 `status === 3` 分支已经在前端解析 `channel.other_info` 并把 `status_reason` / `status_time` 渲染进 Tooltip。v3 起判定依据是渠道对象上的 `daily_limit_disabled_at` 列，**不需要解析 JSON**，比 v2 方案更直接、也不会因 `other_info` 结构变化而失效。

改为：当 `channel.daily_limit_disabled_at > 0` 时，**不展示后端那条面向运维的原始 reason 串**，而是渲染一条面向用户、说明下一步的文案（Rule 6：错误信息要说明用户能做什么，而不是暴露内部字符串）：

- 开启自动恢复：`今日消耗已达上限 $50.00，将于明日 00:00（Asia/Shanghai）自动恢复`
- 关闭自动恢复：`今日消耗已达上限 $50.00，该渠道已关闭自动恢复，需手动启用`

时区取自系统设置接口已下发的 `channel_daily_limit_setting.timezone`。

同时更新 `channel-utils.ts` 的两个既有辅助函数：

- `channelNeedsAttention()`：达上限禁用属于「需要关注」，已被 `status === 3` 分支覆盖，无需改动（已确认）。
- `getAttentionReason()`：`status === 3` 目前一律返回 `'Auto-disabled'`；增加分支，`daily_limit_disabled_at > 0` 时返回 `'Daily quota limit reached'`，让概览提示与详情一致。

### 11.5 移动端卡片

`channel-card.tsx` 通过 `renderCell('balance')` / `renderCell('status')` 直接复用列定义，因此 11.3、11.4 的改动自动生效，无需单独实现。两点需要在实现时验证：

- 卡片左列宽度较窄，今日用量行必须能落到紧凑记法（已由 `MAX_INLINE_BALANCE_CHARS` 覆盖）且允许换行不撑破 `overflow-hidden` 容器；
- 卡片视图会隐藏「已启用/手动禁用」徽标，但 `status === 3` 属于「informative states」仍会显示（`:88`），因此达上限禁用在移动端可见。

### 11.6 手动启用时的即时提醒

渠道行的启用开关（power toggle）对处于每日上限禁用状态的渠道，点击启用后除既有成功提示外，追加一条 toast：

`已启用。该渠道今日消耗已达上限，若继续产生消费将再次被自动禁用；如需今日继续使用，请先调高每日上限。`

这是用户最容易困惑的场景（手动启用后过几分钟又被禁），必须在动作发生的当下讲清楚，而不是让用户去猜。

### 11.7 系统设置 —— 完整接入链路（v3 阻断问题 #6 的修订）

v3 只说了「在 `routing-reliability-section.tsx` 加四个字段」，漏掉了这条链路上其余全部环节。已核实：该页面的读写受 `service/settingsaccess` 的**显式键白名单**约束，不加进去新字段既读不出也存不进。

完整链路，缺一不可：

| 环节 | 文件 | 要做的事 |
|---|---|---|
| 1. 作用域白名单 | `service/settingsaccess/scopes.go:45` | `models.routing-reliability` 目前是 `scope(...)`，**没有 `GroupKeys`**。改为 `configScope("models.routing-reliability", "channel_daily_limit_setting", [既有键 + 4 个 `channel_daily_limit_setting.*`], [4 个裸字段名])` |
| 2. 保存路由 | `controller/option.go:523` | 该行用固定模块名列表决定走 `model.SaveConfigGroup` 还是 `UpdateOption`；必须把 `channel_daily_limit_setting` 加进去，否则 ConfigManager 支撑的配置不会正确落到配置组 |
| 3. 服务端校验 | `controller/option.go` 的 key `switch` | 四个键各加一条范围校验（`flush_interval_seconds` 1–60、`retention_days` 7–3650、`timezone` 用 `time.LoadLocation` 验、`enabled` 布尔），与既有 `business_stats_circuit_breaker_setting.*` 的写法一致 |
| 4. 前端类型 | `system-settings/types.ts` | 四个键加入设置项类型 |
| 5. 页面装配 | `system-settings/models/index.tsx` | 把新字段并入该页的读取与提交 |
| 6. 分区注册 | `system-settings/models/section-registry.tsx` | 新字段归入 `models.routing-reliability` 分区（不新建分区，工作量最小） |
| 7. 表单 | `models/routing-reliability-section.tsx` | 四个控件，沿用该 section 既有的 zod schema → `defaults` → `toPayload` 三段式 |

> 关键点：`AllowsGroup(scope, module, values)`（`settingsaccess/registry.go:51-70`）要求 `definition.GroupKeys[module]` 存在，而 `scope(...)` 构造出来的 `GroupKeys` 是空 map。这就是为什么第 1 步必须换成 `configScope`，只加键名到 `scope(...)` 是不够的——`relay_timeout_setting` 等 ConfigManager 配置全部走的是 `configScope`。
>
> 仅在 setting 包里写 `ValidateChannelDailyLimitSetting` **不会**被单键 `PUT /api/option/` 自动调用，所以第 3 步的服务端校验不能省，且要有对应的 controller 测试。

时区改动会影响日切边界，在该字段描述中提示「修改时区会改变日切边界，当天可能被切分为两段统计」。

### 11.8 前端类型

`web/src/features/channels/types.ts` 需同步的类型：

- `Channel`：5 个新列（`daily_quota_limit` / `daily_limit_basis` / `daily_limit_auto_recover` / `daily_limit_disabled_at` / `daily_limit_disabled_date`）+ 可选的 `daily_usage?: ChannelDailyUsageView`。
- `GetChannelsParams`、`SearchChannelsParams`：`limit_filter?: ChannelLimitFilter`（§11.10）。
- `ChannelSortBy`：新增 `daily_used`、`daily_usage_ratio`（§11.11）。
- `TagOperationParams`：三个配置字段（§11.12）。
- 新增批量设置每日上限的请求/响应类型。

### 11.9 i18n

**后端（v3 文件名写错了，已核实）**：实际布局是

- `i18n/keys.go` —— 每条消息先在此声明 `Msg*` 常量；
- `i18n/locales/en.yaml`、`i18n/locales/zh-CN.yaml`、`i18n/locales/zh-TW.yaml` —— **三个 YAML 文件**，不是 v3 写的 `{en,zh}.json`。

`i18n/consistency_test.go` 会用 Go AST 抽取 `keys.go` 中所有 `Msg*` 常量，逐一校验三个 YAML 的键完备性——这是**硬门禁**，漏一个语言就编译测试失败。因此本功能的每条错误消息都必须：声明常量 → 三个 YAML 各加一条。

> 附带说明：仓库根目录 `CLAUDE.md` 的 i18n 章节写的是 `i18n/locales/en.json` / `zh.json`，与实际不符（且漏了 zh-TW 与 `keys.go`）。这份设计以实际代码为准（Rule 7「代码优先于文档」）；建议另行修正 CLAUDE.md，否则会持续误导后续所有涉及后端 i18n 的改动。

**前端**：新增文案 key 同时写入 7 个 locale——`en`、`zh`、`zh-TW`、`fr`、`ru`、`ja`、`vi`（`web/src/i18n/locales/*.json`）。按 CLAUDE.md 直接编辑文件，不跑 `bun run i18n:sync`（后者会回填无关键、污染 diff）。

> 核实说明：审计意见曾要求前端 locale 写入必须先经 `add-missing-keys.mjs` 再跑 `i18n:sync`。该脚本在本仓库中**不存在**——`web/scripts/` 只有 `add-copyright.mjs`、`format-with-protected-headers.mjs`、`sync-i18n.mjs`，`package.json` 也只注册了 `i18n:sync` 一个命令，仓库内亦无 `.claude/skills` 目录。故维持 CLAUDE.md 的直接编辑方案。

含中文的文件一律用编辑器工具写入，不用 PowerShell 重定向（Rule 6 编码要求）。

### 11.10 列表工具栏 —— 限额筛选

渠道列表工具栏已有「分组 / 状态 / 类型」三个筛选控件（对应 `GetChannelsParams` 的 `group` / `status` / `type`），新增第四个「每日上限」下拉，沿用同一位置与控件写法：

| 选项 | 传给后端的 `limit_filter` |
|---|---|
| 全部（默认） | 不传 |
| 已设上限 | `configured` |
| 未设上限 | `unlimited` |
| 今日已达上限 | `reached` |
| 接近上限（≥80%） | `near` |

`GetChannelsParams` 与 `SearchChannelsParams`（`channels/types.ts:286,298`）同步增加可选字段 `limit_filter?: ChannelLimitFilter`，两个列表接口行为一致。

**必须同时新增一个隐藏的 TanStack accessor column。** 通用工具栏渲染筛选控件时会 `props.table.getColumn(filter.columnId)`，取不到列就直接 `return null`（`web/src/components/data-table/toolbar/toolbar.tsx:259-261`），只往 filters 数组里加选项**不会渲染出任何东西**。因此在 `channels-columns.tsx` 增加一个 `id: 'daily_limit'`、`enableHiding` 且默认不可见的 accessor column 承载该筛选。

筛选状态要参与既有的 URL / 查询键持久化机制（与 `status`、`type` 同等对待），否则刷新页面后筛选丢失、分页也会错位。

空结果态沿用表格既有的空状态组件，不新增写法；文案按筛选项区分，例如选「今日已达上限」且无结果时显示「今日没有渠道达到每日上限」，而不是通用的「暂无数据」——用户此时问的是「有没有渠道超限」，答案本身就是有价值的信息。

### 11.11 列表排序 —— 今日用量 / 使用率

不新增列。今日用量已经显示在 §11.3 的 `balance`（Used / Remaining）单元格内，再加一列会把本就拥挤的表格撑宽，移动端卡片也放不下。

改为在 `balance` 列头的排序菜单中增加两项，复用现有的 `sort_by` / `sort_order` 机制：

- 「今日用量」→ `sort_by=daily_used`
- 「今日使用率」→ `sort_by=daily_usage_ratio`

`ChannelSortBy` 类型（`channels/types.ts`）增加这两个字面量。排序方向沿用现有的升/降切换。

**Tag 模式下这两项置灰**（§9.5：Tag 分页与子渠道排序是两层，对子渠道排序改变不了 Tag 顺序）。置灰时 tooltip 说明「标签模式下不支持按每日用量排序，请切换到渠道列表」，而不是让用户点了没反应。

两点用户视角的处理：

- 未设上限的渠道没有「使用率」。后端用两级排序把它们固定排在末尾（§9.5，升降序都成立），前端在按使用率排序时，于表头下方给一行说明「未设上限的渠道排在最后」，避免用户以为排序坏了。
- 按今日用量排序时，各渠道用的是各自的口径（消费额或成本）。这一点在排序菜单项的 tooltip 里说明，避免管理员把两种口径的数字直接横向比较。

### 11.12 批量与按 Tag 设置每日上限

**(1) 勾选行批量** —— `data-table-bulk-actions.tsx` 现有四个动作（启用 / 禁用 / 删除 / 设置标签）。新增第五个「设置每日上限」，按钮与对话框结构完全复用现有的「设置标签」流程（`setShowTagDialog` 那套 `Dialog` + `Label` + `Input` + 取消/确认按钮），只是表单内容换成三个字段。

**(2) 按 Tag** —— `edit-tag-dialog.tsx` 与 `tag-batch-edit-dialog.tsx` 都通过 `editTagChannels` 调 `PUT /api/channel/tag`。这两个对话框用的是朴素的 `Label` + `Input`（不是 react-hook-form），新增字段沿用其现有写法，不引入第二套表单方案。`TagOperationParams`（`channels/types.ts:346`）同步扩展三个可选字段。

两个对话框的共同交互要求：

- **「不修改」必须可表达。** 后端用指针区分「本次不改」与「显式设为 0」。表单上留空 = 不修改；要清除上限必须显式填 `0`。对话框里用占位符和说明写清楚，否则管理员会误以为留空就是清零。
- **必须提示不会自动恢复。** 批量调高上限不会启用已被禁用的渠道（§9.6）。对话框在提交前用既有的 `Alert` 组件提示「调高上限不会自动启用已被禁用的渠道，如需立即恢复请在列表中手动启用」。
- **权限**：无 `ChannelSensitiveWrite` 时，按钮置灰并给出与抽屉内一致的提示，而不是让用户填完再被后端拒绝。
- **结果反馈**：成功后 toast 显示实际影响的渠道数（后端返回行数），失败时显示可操作的错误信息，不透传 Go 错误串。

### 11.13 本期不做（显式记录，非遗漏）

v2 曾把「批量/按 Tag 设置」与「筛选/排序」列为本期不做，**两项均已确认纳入本期**，实现见 §9.4–§9.6 与 §11.10–§11.12。这两项需求同时推动配置存储从 `setting` JSON 改为真实列（§3.1）。

仍然不做的：

- **达到上限前的预警通知**（如 80% 时通知 root）。当前只在触发禁用时通知。`near` 筛选已经能让管理员主动发现接近上限的渠道，主动推送属于独立的通知策略设计。
- **按小时 / 按月上限**，以及**用量趋势图**。`channel_daily_usages` 已保留 90 天历史，具备做趋势图的数据基础，但图表与查询接口是独立改动。

## 12. 边界情况

| 情况 | 行为 |
|---|---|
| 上限为 0 / 留空 | 无上限，观察者判定后立即返回，零开销 |
| 上限调小到低于今日已用 | 下一次结算或下个 flush 周期立即触发禁用 |
| 上限调大 / 口径切换 | 不自动恢复已禁用渠道；管理员手动启用即可（已不越限不会再被禁）。保存后 UI 提示「该渠道当前处于每日上限禁用状态，可手动启用」 |
| 管理员手动启用后仍越限 | 该渠道下次产生消费后会再次被禁用（预期行为，UI 提示说明） |
| 两个口径的覆盖差异 | `revenue` 覆盖一切计入 `channels.used_quota` 的消费；`cost` 与平台成本账 `consumption_costs` 完全一致，因此**违规扣费**（`violation_fee.go` 不产生成本记账）与 `LedgerQuota=0` 的音频消费不计入 cost 口径。已在前端文案说明 |
| 渠道被删除 | 历史行随保留期自然过期；恢复扫描以 `channels` 为准，不存在的渠道天然不在候选集 |
| 多 Key 渠道 | 上限是渠道级，触发时禁用整渠道，不改各 Key 独立状态 |
| 已被上游报错禁用后又达上限 | 已是禁用状态，状态未变更则不写标记列；日切时谓词不成立，不会被本功能恢复（避免恢复坏渠道） |
| 本功能禁用后同一秒内又被报错禁用 | 后者经 `UpdateChannelStatus`（marks=nil）在同一次保存中把 `daily_limit_disabled_at` / `_date` 清零，不再依赖秒级时间戳比较，日切不会误恢复 |
| master 停机跨过零点后重启 | 恢复是对 `status=3` 渠道的幂等扫描，不依赖进程内日期，重启后下一个 tick 即补做 |
| 达上限当日进程重启 | 渠道状态在 DB 中保持禁用；内存汇总首个 flush 周期回读补齐 |
| 全局开关中途关闭 | 停止累计、禁用与恢复；已禁用渠道保持禁用（需手动启用）。开关关闭期间的消费不计入当日累计，重开后从当前 DB 汇总继续 |
| 全局开关关闭后重新打开 | **不补做**关闭期间遗留的自动恢复。恢复扫描只处理开关打开后新产生的限额禁用；关闭期间被禁的渠道需管理员手动启用。避免「一开开关就批量放行」的意外。实现上：开关从关到开时记录一个进程内的生效时刻，恢复扫描跳过 `daily_limit_disabled_at` 早于该时刻的渠道；设置项文案写明这一行为 |
| 退款 / 预扣费返还 | 不经过 `UpdateChannelUsedQuota`，不虚增当日用量 |
| `quota = 0` 的请求 | 两个观察者都忽略 |
| 批量/Tag 设置时字段留空 | 指针为 `nil` = 本次不修改该字段；清除上限必须显式填 `0`。对话框文案写明（§11.12） |
| 批量调高上限 | 与单渠道编辑一致，**不**自动启用已被禁用的渠道；对话框用 Alert 提前说明 |
| 批量设置命中已达上限的渠道 | 只改配置列，不动 `status` 与标记列；若新上限仍低于今日已用，下个 flush 周期会重新判定 |
| Tag 下渠道口径不一致 | 按 Tag 设置会把该 Tag 下所有渠道的口径统一覆盖（这正是批量的语义）；对话框提示影响渠道数 |
| 筛选 `near` 与「已达上限」的关系 | 二者互斥：已触发禁用的渠道归 `reached`，`near` 显式排除 `daily_limit_disabled_at > 0`，避免同一渠道同时出现在两个筛选里造成困惑 |
| 按使用率排序时存在无上限渠道 | 两级排序（先 `CASE WHEN limit>0 THEN 0 ELSE 1 END ASC`）使其**升降序都恒排末尾**，三库一致；前端在表头下方说明（§11.11） |
| 筛选/排序与分页 | JOIN 走 `(stat_date, channel_id)` 唯一索引，一对一不放大行数，`Count` 与分页保持正确（§13 有专门用例） |
| 跨零点时使用 `near` 筛选 / 用量排序 | JOIN 条件用请求时刻按配置时区算出的 `StatDate`，零点后立即切到新的一天（当日用量归零），与列表展示口径一致 |

## 13. 测试计划（Rule 15，先写用例再实现）

**`model/channel_daily_usage_test.go`**（真实项目 DB，无则回退 SQLite；行级清理，不用 `truncateTables`）
- upsert：首次插入 / 重复累加 / 20 goroutine 并发累加总和正确（跨库 upsert 语义）。
- 批量查询：多渠道多日期，`stat_date + channel_id IN` 只返回目标行。
- 清理：keyset 两段式删除的边界（保留期当天保留、前一天删除）、批次大于 1000 行时的翻页正确性。

**`model/channel_status_marks_test.go`**
- `DisableChannelForDailyLimit` 的条件更新：`status=1` 时命中并一次写入 status + 两个标记列；`status` 已是 2 或 3 时 `RowsAffected=0` 且不改任何列（v3 阻断 #1 的回归测试）。
- **状态相同也要清标记**：渠道已被限额禁用（`status=3`）后调用 `service.DisableChannel`（同样是 3），标记列必须被清零——v3 会因 `UpdateChannelStatus` 的「状态相同直接返回」而残留标记，导致次日恢复坏渠道。
- 任意其他状态变更路径把两个标记列清零（单 Key 与多 Key 各一例）；`EnableChannelByTag` / `DisableChannelByTag` 同样清零。
- **DB 失败不改缓存**：条件更新报错时内存缓存与 abilities 保持原状（v3「先缓存后 DB」的回归测试）。
- 并发：10 个 goroutine 同时执行「限额禁用」与「管理员启用」，最终状态与标记列始终自洽（无「status=1 但标记非零」或「status=3 但标记为零却本应非零」的组合），`-race`。

**`service/channel_daily_limit_test.go`**
- 边界值：`limit-1` 不禁用、`limit` 禁用、`limit+1` 禁用、`limit=0` 无上限、`limit<0` 无上限。
- 等价类：`basis` 为 revenue / cost / 空串（归一化）/ 非法值。
- **成本口径公式回归**：`revenue=1000, groupRatio=0.5, costRatio=0.8` → cost=1600（`revenue/groupRatio*costRatio`），断言与 `calcCostQuota` 结果一致；显式断言不等于 v1 的 `revenue*costRatio=800`。
- `groupRatio=0` → cost=0（免费模型）。
- **观察者无 IO 回归**：收入观察者在 Redis 未启用且 DB 句柄被替换为会 panic 的桩时仍正常工作（证明未触达 DB）。
- 分支：总开关关闭不累计不禁用；`quota<=0` 忽略；`tripped` 防重复禁用；flush 失败后增量**保留**并重试（断言不丢弃）。
- **`tripped` 重新武装（v3 阻断 #2 的回归测试）**：禁用成功 → 管理员手动启用 → 配置快照刷新 → 再消费 1 quota → **再次被禁用**。缺少重新武装时此用例失败。
- `tripped` 的另外两条清除路径：条件更新 `RowsAffected=0` 时清除；禁用队列满被丢弃时清除。
- **有界队列（v3 阻断 #4 的回归测试）**：队列填满后提交不阻塞（在超时保护下断言立即返回）、被丢弃的请求清除了 `tripped`、下个周期重试成功。
- **成本取自正式路径（v3 阻断 #3 的回归测试）**：桩掉 `GetChannelCostRatio` 使其被调用即失败，走一遍成本累计，断言仍能累计——证明没有二次读取成本系数。
- 成本累计发生在 `BeginBusinessStatsSideEffect` 之前：熔断器打开时成本台账不落库，但每日累计照常增加。
- 并发：100 goroutine 并发记账，flush 后总额精确相等（`-race`）。

**`service/channel_daily_limit_task_test.go`**
- 日期切分：`Asia/Shanghai` 下 `StatDate` 正确（UTC 边界 15:59:59Z / 16:00:00Z 各一例）；非法时区回退。
- **跨零点累计**：在旧日期写入增量后把时钟推过零点，断言旧增量按旧 `statDate` 落库、新增量按新 `statDate` 落库（v1 多节点缺陷的回归测试）。
- 恢复：谓词成立且自动恢复开启 → 启用；自动恢复关闭 → 保持禁用（且不逐轮刷日志）；标记列被其他路径清零 → 不恢复；当日禁用（`daily_limit_disabled_date == 今日`）→ 不恢复。
- 恢复用条件更新：执行前把渠道状态改成 1 或 2，断言 `RowsAffected=0` 且不产生通知。
- 全局开关关闭→打开：关闭期间被禁的渠道**不被补恢复**；打开后新产生的禁用正常恢复。
- **恢复不依赖 usage 表**：构造「渠道已被禁用但 usage 行缺失」场景，断言仍能恢复（v1 缺陷的回归测试）。
- master 重启跨零点：直接调用恢复函数两次，断言幂等且第一次即完成恢复。

**`model/relay_log_pipeline_*_test.go`** —— v4 起本功能不改动该文件，无需新增用例；既有测试保持全绿即可（§5.3 取消了管线观察者方案）。

**`controller/` 侧**
- `ValidateSettings` 拒绝负数 / 过小金额 / 非法口径。
- 列表批量填充 `daily_usage`：全部无上限时不发起查询。
- `selectChannelsForAutomaticTest` 跳过每日上限禁用渠道；普通 auto-disabled 渠道仍被选中（防回归）。
- `ShouldEnableChannel` 新签名下的既有行为不变（更新 `service/gen3_tasks2_test.go` 现有断言）。
- `channel_authz`：5 个新列各自落在正确的分类集合；`daily_limit_disabled_at` / `_date` 由客户端传入时被 `clearChannelReadOnlyFields` 清零（防伪造禁用标记）。

**`controller/channel_limit_filter_test.go`（§9.4 筛选）**
- 五种 `limit_filter` 取值各一例，构造覆盖：有上限/无上限/已达上限/接近上限/远低于上限。
- `near` 的边界值：79.9%（不命中）、80.0%（命中）、100%（已触发禁用则不属于 `near`，归 `reached`）。
- `basis=cost` 的渠道按 `cost_quota` 判定、`basis=revenue` 按 `used_quota` 判定，同一批数据下两种口径命中结果不同。
- **JOIN 不放大 Count**：同一渠道当日只有一行 usage（唯一索引保证），`Count(&total)` 与不带 JOIN 时相等——这是分页正确性的前提。
- `/api/channel/search` 与 `/api/channel` 在相同 `limit_filter` 下返回一致的渠道集合（v3 只改 `buildChannelListQuery` 时此用例失败）。
- `reached` 不把「往日禁用且关闭自动恢复」的渠道算作今日已达上限（v3 条件缺 `daily_limit_disabled_date = 今日` 的回归测试）。
- Tag 模式下 `sort_by=daily_used` 被忽略并回退默认排序，不报错。

**`model/channel_sort_test.go`（§9.5 排序）**
- `daily_used` / `daily_usage_ratio` 升降序结果正确。
- **无上限渠道在升序与降序下都排末尾**（两级排序），有上限与无上限混排时位置稳定。v3 的 `COALESCE(..., -1)` 单级方案在升序时会把它们排到最前，此用例即为该缺陷的回归测试。
- **整数除法回归**：`used=1, limit=2` 时比率必须是 0.5 而不是 0（漏写 `* 1.0` 时此用例失败）。
- **除零回归**：存在 `daily_quota_limit = 0` 的渠道时排序不报错（`NULLIF` 缺失时 PostgreSQL 会抛错）。
- 非白名单 `sort_by` 被忽略、回退默认排序（防 SQL 注入面扩大）。

**`controller/channel_batch_daily_limit_test.go`（§9.6 批量与 Tag）**
- 按 Tag 设置：仅传上限时不动 basis / auto_recover（指针 nil 语义）；`new_tag` 等既有字段行为不受影响。
- 勾选行批量：`ids` 为空、含不存在的 id、部分成功时的返回行数。
- **权限**：无 `ChannelSensitiveWrite` 时两条通道都返回 `MsgAuthInsufficientPrivilege`，且数据库未被修改。
- 校验复用：批量路径传入负数 / 非法口径时与单渠道保存返回同样的错误 key。
- 批量调高上限**不会**启用已被本功能禁用的渠道（行为一致性回归）。
- 批量后 `InitChannelCache` 与配置快照均被失效（下一次观察者读到新上限）。

**`setting/operation_setting/channel_daily_limit_setting_test.go`**
- 默认值、取值范围校验、时区解析与回退、快照热更新可见性。

**设置接入链路（v3 阻断 #6 的回归测试）**
- `settingsaccess`：`models.routing-reliability` 作用域下 4 个新键 `AllowsOption` 通过；`AllowsGroup("models.routing-reliability", "channel_daily_limit_setting", ...)` 通过（改成 `configScope` 之前此用例失败）；既有键不受影响。
- `controller/option.go`：四个键的范围校验各一例越界拒绝；`channel_daily_limit_setting.*` 走 `SaveConfigGroup` 分支而非 `UpdateOption`。

**`i18n/consistency_test.go`（既有，无需新写）**
- 新增的 `Msg*` 常量必须在 `en.yaml` / `zh-CN.yaml` / `zh-TW.yaml` 三个文件中都存在，否则该测试直接失败——这是后端 i18n 的硬门禁。

**验收门槛**：`go test ./...` 全绿；`cd web && bun run typecheck && bun run lint` 通过。（`relaykit` 本期不改动，无需单独构建验证。）

## 14. 实现清单

后端：
1. `model/channel.go` — 5 个新列（§3.1）；`ValidateSettings` 校验；`channelUsedQuotaObserver` 挂钩；**条件式** `DisableChannelForDailyLimit` 与 `ClearDailyLimitMarks`（§6.2、§6.3，不重写 `UpdateChannelStatus`）；`Channel.DailyUsage` 展示字段；`EditChannelByTag` 改为结构体入参并支持三个新字段；`EnableChannelByTag` / `DisableChannelByTag` 在同一条 UPDATE 里清零标记列；`ChannelSortOptions` 支持**两级表达式排序** + `statDate`（§9.5）；`SearchChannels` 增加 `limitFilter` / `statDate` 入参（§9.4）。
2. `model/channel_daily_usage.go` — 新表、upsert、批量查询、keyset 清理。
3. ~~`model/relay_log_pipeline.go`~~ — **不再改动**（§5.3 改为在正式成本路径就地取值，取消了管线观察者方案）。
4. `model/main.go` — AutoMigrate 注册新表；`&Channel{}` 的 5 个新列随既有 AutoMigrate 生效（§4.3）。
5. `controller/channel_authz.go` — 5 个新列 + `daily_usage` 的分类与清零（§3.1）。
6. `setting/operation_setting/channel_daily_limit_setting.go` — 全局配置 + 快照 + 时区解析缓存。
7. `service/channel_daily_limit.go` — 分片累计器（键含日期）、配置快照周期刷新 + `tripped` 重新武装（§5.5.1、§5.6）、flusher + 退避重试、越限判定、**有界禁用队列 + 2 worker**（§6.4）、收入观察者注册。
8. `service/channel_daily_limit_task.go` — master 幂等恢复扫描 + 历史清理。
9. `service/employee_commission.go` — 在 `RecordCostAndSettleEmployeeCommission` 里加一行，把 `costRec.CostQuota` 交给累计器（守卫之前），公式本身不改（§5.3）。
10. `service/channel.go` — `ShouldEnableChannel` 改签名 + 每日上限豁免；`DisableChannel` / `EnableChannel` 入口调用 `ClearDailyLimitMarks`（§6.3）。
11. `controller/channel-test.go` — `selectChannelsForAutomaticTest` 跳过 + 调用点适配新签名。
12. `controller/channel.go` — `buildChannelListQuery` 增加 `limit_filter` + `statDate`（§9.4）；列表填充 `daily_usage`；`ChannelTag` 扩展三字段 + 敏感权限校验（§9.6）；新增批量设置每日上限 handler。
13. `router/api-router.go` — `POST /api/channel/batch/daily_limit` 挂在既有 `AdminAuth()` 渠道分组内（§9.6）。
14. `main.go` — 启动 flusher 与 master 任务。
15. `i18n/keys.go` + `i18n/locales/{en,zh-CN,zh-TW}.yaml` — 声明 `Msg*` 常量并三语言各补一条（§11.9；`i18n/consistency_test.go` 是硬门禁）。
16. `service/settingsaccess/scopes.go` — `models.routing-reliability` 由 `scope` 改为 `configScope`，登记 4 个新键（§11.7）。
17. `controller/option.go` — `channel_daily_limit_setting` 加入 `SaveConfigGroup` 模块列表 + 四个键的范围校验（§11.7）。

> `relaykit/dto/channel_settings.go` **不再改动**（v3 起配置改为真实列，§3.1）。

前端（§11 逐项对应）：
18. `channels/types.ts` — `Channel` 增加 5 个新列 + `daily_usage?: ChannelDailyUsageView`；`GetChannelsParams` / `SearchChannelsParams` 增加 `limit_filter`；`ChannelSortBy` 增加两个排序键；`TagOperationParams` 增加三字段（§11.10–§11.12）。
19. `channels/lib/channel-form.ts` — zod schema、`DEFAULT_FORM_VALUES`、`transformChannelToFormDefaults`（直接读列）、提交带列字段；**金额一律经 `quotaUnitsToDollars` / `parseQuotaFromDollars` 换算，标签跟随 `getCurrencyLabel()`**（§11.2）。
20. `channels/lib/channel-form-errors.ts` — 三个新字段加入分区表（§11.2）。
21. `channels/components/drawers/channel-mutate-drawer.tsx` — 字段落位（§11.1）、**`SENSITIVE_FORM_FIELDS` 与 `hasAdvancedSettingsValues()` 两处清单必须同步**（§11.2）。
22. `channels/components/channels-columns.tsx` — `BalanceCell` 今日用量行（§11.3，需处理遮罩/卡片布局/紧凑记法/Tag 行四种情况）、`status === 3` 分支的禁用原因文案（§11.4）。
23. `channels/lib/channel-utils.ts` — `getAttentionReason()` 增加 daily_limit 分支（§11.4）。
24. `channels/components/channel-card.tsx` — 无需改代码，但需实机验证窄列换行与徽标显示（§11.5）。
25. 渠道启用开关所在组件 — 手动启用时的越限提醒 toast（§11.6）。
26. `system-settings/{types.ts,models/index.tsx,models/section-registry.tsx,models/routing-reliability-section.tsx}` — 设置接入链路的前端四环（§11.7 表格第 4–7 项）。
27. 列表工具栏组件 + `channels-columns.tsx` — 「每日上限」筛选下拉、**隐藏的 `daily_limit` accessor column**（缺了筛选控件根本不渲染）、查询键持久化、分筛选项空状态文案（§11.10）。
28. `channels/components/channels-columns.tsx` — `balance` 列头排序菜单增加两项、**Tag 模式下置灰**、未设上限排序说明（§11.11）。
29. `channels/components/data-table-bulk-actions.tsx` — 新增「设置每日上限」批量动作与对话框（§11.12）。
30. `channels/components/dialogs/edit-tag-dialog.tsx`、`tag-batch-edit-dialog.tsx` — 三个字段 + 「留空 = 不修改」说明 + 不自动恢复提示 + 同一套货币换算（§11.12）。
31. `channels/api.ts` — 批量设置每日上限的请求函数。
32. `web/src/i18n/locales/{en,zh,zh-TW,fr,ru,ja,vi}.json` — 全部 7 种语言（§11.9）。

## 15. 实现后回填

状态：**已实现**（后端 + 前端），`go build ./... && go vet ./...` 通过，`go test ./...` 全绿，
`bun run typecheck` 通过，`bun run lint` 无本功能引入的新错误。

### 15.1 与设计一致的部分

§3–§12 的方案全部按设计落地，包括：5 个真实列与新表、条件式状态转换与标记清除、
`tripped` 四条清除路径、有界禁用队列、成本口径复用 `costRec.CostQuota`、
限额筛选五档与两级排序、批量与按 Tag 设置、设置接入 7 环、后端 i18n 三个 YAML + `keys.go`、
前端 7 个 locale 各补 42 条文案。

### 15.2 实现期发现的问题与偏差

**1. GORM 的 `DB.Order()` 不接受 `clause.OrderBy`（设计外的坑，已修）**

gorm v1.25.2 的 `DB.Order()` 只处理 `clause.OrderByColumn` 与 `string` 两种类型
（`chainable_api.go:302`），传入 `clause.OrderBy{Expression: ...}` 会走空 switch 被**静默忽略**，
ORDER BY 整个消失。是排序单测（升序断言）把它抓出来的。

实现改为 `clause.OrderByColumn{Column: clause.Column{Name: <raw sql>, Raw: true}}`，
`statDate` 内联成整数字面量（本进程按配置时区算出的 int64，不来自外部输入，无注入面）。

同时确认了设计里已写明的另一点：`clause.OrderBy` 合并时只累加 `Columns`，`Expression`
后者覆盖前者，所以两级排序必须写在同一个表达式里。

**2. 筛选/排序改用相关子查询，不用 LEFT JOIN（对 §9.4/§9.5 的修正）**

设计写的是 LEFT JOIN，实现时发现三个问题：搜索路径的 WHERE 里有裸 `id = ?`，
而 `channel_daily_usages` 同样有 `id` 列，JOIN 后列歧义；`Find(&channels)` 的 `SELECT *`
会带出两张表的同名列，需要额外 `Select` 限定，与既有 `Omit("key")` 相互干扰；Count 还要
额外确认不被放大。改用相关子查询后语句形状不变、Count 天然正确，`(stat_date, channel_id)`
唯一索引让它是一次点查，渠道表数百行代价可忽略。

**3. `EditChannelByTag` 未改成结构体入参（对 §9.6 的修正）**

设计原打算把它改造成结构体再塞三个字段。实际改为独立的 `UpdateChannelDailyLimitByTag` /
`UpdateChannelDailyLimitByIds`，原因有二：`EditChannelByTag` 用 `Updates(struct)` 更新，
GORM 会忽略结构体零值，而 `daily_quota_limit = 0` 恰恰是「清除上限」这个有意义的值，必须用
map 更新；独立函数不触碰既有 Tag 编辑的行为与测试，改动面更小。

**3b. 单渠道编辑同样绕不开 `Updates(struct)`（v5 修订）**

上面第 3 条只修了批量与 Tag 两条路径，**单渠道保存漏了**：`Channel.Update()` 用的也是
`DB.Model(channel).Updates(channel)`，于是管理员在编辑抽屉里把金额清空保存后，
`daily_quota_limit = 0` 被 GORM 静默跳过，上限依旧生效，而 UI 显示已清除。

修法是给单渠道路径补上第三条 map 更新：`controller/channel.go` 的 `UpdateChannel` 里，
`buildChannelDailyLimitEdit` 复用同一个 `model.DailyLimitEdit`，交给
`UpdateChannelDailyLimitByIds` 落库，三条写入路径的语义与校验就此完全一致。

两个实现要点：

- **摘字段必须在 `channel.Update()` 之前**。`Update()` 末尾有一句
  `DB.Model(channel).First(channel, "id = ?", channel.Id)` 把整行重新读回结构体，之后再读
  `channel.DailyQuotaLimit` 拿到的是库里的旧值（正是没写进去的那个），补写就变成把旧值原样
  写回、等于没修。
- **以 `requestData` 里字段是否出现为准**，不是看值是否非零；否则任何一次不带该字段的渠道
  保存都会把上限清零。这与 `channelHasSensitiveChanges` / `clearChannelReadOnlyFields` 的
  既有判定方式一致。

回归测试在 `controller/channel_daily_limit_edit_test.go`，其中
`TestUpdateChannel_ClearsDailyLimitEndToEnd` 走完整的 `UpdateChannel` 处理函数，把这次补写
钉在调用点上（该用例需要 `ChannelSensitiveWrite`，用 root 上下文提交）。

**4. `models.routing-reliability` 由 `scope` 改为 `configScope`**

按 §11.7 执行。补充确认：`AllowsGroup` 要求 `definition.GroupKeys[module]` 存在，而
`scope(...)` 构造出来的 `GroupKeys` 是空 map，只加键名到 `OptionKeys` 不够。已有
`service/settingsaccess/channel_daily_limit_scope_test.go` 锁住这一点，同时断言该作用域
原有的 10 个键没有丢失。

**5. 前端 `BalanceCell` 的 useMemo 位置（lint 抓出的真实缺陷）**

今日用量的 `useMemo` 一开始写在 Tag 行早退 `return` 之后，违反 React hooks 顺序规则
（`rules-of-hooks`）——Tag 行与普通行会走出不同的 hook 序列。已移到所有条件返回之前，
Tag 行的判断放进 memo 内部。同时把依赖里每次渲染都变化的 `withSuffix` 换成它真正依赖的
`tokenSuffix`，否则 memo 永远失效。

**6. 前端排序入口用自定义列头**

设计说「在 balance 列头的排序菜单增加两项」。通用 `DataTableColumnHeader` 只支持单列
升/降/隐藏，因此实现了 `BalanceColumnHeader`，复用既有 DropdownMenu 原语，提供
余额升降 + 今日用量升降 + 今日使用率升降，并在标签模式下把后四项置灰并说明原因。

### 15.3 实现后自审计发现并修复的缺陷

实现完成后对自己写的代码做了一轮审计，发现 7 个真实缺陷，均已修复并补上回归测试。

| # | 缺陷 | 后果 | 修复 |
|---|---|---|---|
| 1 | **重启被误判为「开关从关到开」** —— `lastEnabled` 用零值 `bool`，进程启动时第一次观察到「开启」就被当成一次切换，`enabledAt` 被设成进程启动时间，恢复扫描随即跳过所有更早的禁用 | **任何一次重启都会让此前被限额禁用的渠道永远不再自动恢复**，直接违背 §7 的核心承诺 | `lastEnabled` 改为三态（unknown/off/on），首次观察不算切换。回归测试 `TestDailyLimit_FreshStartIsNotASwitchOn` |
| 2 | **落库部分失败后整批回退** —— `UpsertChannelDailyUsage` 逐条写入无事务包裹，中途失败时前面的条目已落库，而 flusher 把整批放回内存重试 | 已落库的增量被重复累加，**当日金额越算越多**，渠道被提前禁用 | 函数返回已处理条数，flusher 只回退 `deltas[processed:]`。回归测试 `TestDailyLimit_RestorePendingOnlyUnpersisted` |
| 3 | **`CopyChannel` 浅拷贝把禁用标记带给副本** | 副本被当成「今日因限额禁用」，在筛选与状态列显示错误原因，且次日会被恢复任务「恢复」成启用——而它从未被本功能禁用过 | 克隆时清零两个标记列。回归测试 `TestCopyChannelDropsDailyLimitDisableMarks` |
| 4 | **`ClearDailyLimitMarks` 在 `UpdateChannelStatus` 入口无条件写库** | 上游故障时该函数会被调用成千上万次，凭空给 relay 正在读的 `channels` 表加上同等数量的无谓写入 | 改为 `clearDailyLimitMarksIfPresent`（先查内存缓存，确有标记才写）；正常状态变更则把清零并进同一次 `SaveWithoutKey`，不产生额外往返。回归测试两个 |
| 5 | **启动时未预热配置快照** | 首个 flush 周期（默认 5s）内快照为空，这段时间的消费不计入任何渠道的当日用量 | `StartChannelDailyLimitWorkers` 先同步刷新一次 |
| 6 | **注释声称会失效配置快照，代码里并没有** | 单渠道保存 / 批量 / 按 Tag 改完上限后，本节点要等下一个 flush 周期才生效 | 新增 `service.RefreshChannelDailyLimitConfigs()`，在 AddChannel / UpdateChannel / EditTagChannels / 批量四条写路径调用 |
| 7 | **搜索接口不回填 `daily_usage`** | 「每日上限」筛选在搜索下能生效，列表里却看不到今日用量那一行，同一功能两条路径表现不一致 | 搜索响应同样调用 `fillChannelDailyUsage` |

**第二轮审计（针对第一轮修复本身）又发现 2 个：**

| # | 缺陷 | 后果 | 修复 |
|---|---|---|---|
| 8 | **上一轮 #4 的「读缓存跳过写入」优化把它本要防的 bug 放了回来** —— `CacheUpdateChannelStatus` 只同步 `Status`，不同步两个标记列；另一节点写入标记后本节点缓存要等全量同步才可见。于是「缓存说没标记、DB 里其实有」这个危险方向会让清除被跳过 | 标记残留到次日，把一个真坏的渠道恢复成启用——正是 #4 那段代码要防的场景 | 去掉缓存读，恢复成无条件的条件式 UPDATE（`WHERE daily_limit_disabled_at > 0`，命中主键、常态零行）。写放大不成问题：渠道一禁用就从 abilities 摘除，「状态相同」的调用只来自禁用瞬间的在途重试，是几秒的有界突发。回归测试 `TestClearDailyLimitMarks_NotFooledByStaleCache`（已验证：放回旧实现即失败） |
| 9 | 缓存里的标记列写入后一直是陈旧值 | 目前无人从缓存读它，但这正是 #8 的成因，留着就是下一个人的陷阱 | 新增 `CacheUpdateChannelDailyLimitMarks`，禁用/恢复/清除三处写入方顺手同步 |

另外把 `lastManualPendingLogDate` 从普通 `int64` 改为 `atomic.Int64`：生产环境只有 master 的单
tick goroutine 写它，但 `RecoverExpiredDailyLimitChannels` 是导出函数、测试会并发调用，
留着就是一个竞态陷阱。

> #8 值得单独记一笔：它是**修复引入的缺陷**，而且和它要修的是同一个失效模式。教训是
> 「用缓存判断要不要写」这类优化必须先确认缓存在危险方向上不会陈旧——这里恰恰会。

**第三轮审计（2026-09-15，未提交代码审计）：**

| # | 问题 | 后果 | 修复 |
|---|---|---|---|
| 10 | #8 之后 `UpdateChannelStatus` 的注释仍写着「不能在函数入口无条件写库」「只在确实带着标记时才补一条」，与代码相反；且禁用瞬间的在途失败会**并发**打进来，每个各发一条 UPDATE | 实测 200 次重复禁用 → 200 条 `UPDATE channels`（HEAD 为 0）。单条代价约 0.5ms，但并发突发会同时占用与 relay 共用的连接池 | 不回退 #8 的正确性取舍，改为按渠道**合并**：同一时刻最多一条，调用方只认到达之后才开始的清除（见 §6.3）。panic 在合并锁外吞掉并复位，避免该渠道后续调用永久阻塞。注释改正。回归测试 `TestClearDailyLimitMarksIfPresent_LateArrivalsGetTheirOwnClear`、`_SurvivesPanic`、`TestUpdateChannelStatus_ConcurrentRepeatDisablesAreCoalesced`、`_StillClearMarks`（`model/channel_daily_limit_clear_coalesce_test.go`） |

另修正一处**前端风险**：金额在「quota → 展示金额 → quota」往返时若产生浮点误差（自定义货币 + 汇率时最可能），
后端 `channelHasSensitiveChanges` 会把它判成一次真实修改，于是只有 `ChannelWrite` 权限的管理员
连保存一个无关字段都会被拒绝。现在表单记录加载时的原始 quota 与展示金额，金额未被改动时原样回传，
不再重新换算。

### 15.4 已知限制（非缺陷）

- **`-race` 未在本机执行**：环境缺少 gcc，`CGO_ENABLED=1 go test -race` 无法构建。并发用例
  （20/100 goroutine 累计、10 goroutine 并发禁用与启用）本身仍然运行并通过，但没有竞态检测器
  背书。有 CI 或本机装了 gcc 时应补跑 `go test ./model/ ./service/ -race`。
- **搜索路径的 status / type 仍在 Go 侧过滤**，且整条搜索路径没有 SQL 分页（既有实现）。本次
  只把 `limit_filter` 下推到 SQL，没有重构搜索路径——那是独立改动。
- 软上限语义见 §8.4：跨节点未 flush 的增量、禁用瞬间的在途请求、单笔大额请求三者叠加，
  实际消耗可能超出上限。前端文案已明确说明。

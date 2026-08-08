# Relay 消费/错误日志有界异步批量写入

## 状态

- 状态：已实现，本文档已按代码回填
- 实现位置：[model/relay_log_pipeline.go](../../model/relay_log_pipeline.go)、
  [model/log.go](../../model/log.go)、[service/consume_settlement.go](../../service/consume_settlement.go)、
  [setting/operation_setting/relay_log_setting.go](../../setting/operation_setting/relay_log_setting.go)、
  [controller/relay_log_pipeline_status.go](../../controller/relay_log_pipeline_status.go)

## 1. 背景与问题

当前 relay 结算和错误处理会同步写入 `logs`：

- `service.FinalizeConsumptionSettlement` 调用 `model.RecordConsumeLog`；
- `controller.relayErrorHandler` 调用 `model.RecordErrorLog`；
- 两者最终都调用 `LOG_DB.Create`。

当日志库连接池耗尽、SQL 变慢或 `logs` 表等待锁时，请求 goroutine 会等待数据库连接或 INSERT，
使非关键日志系统反向阻塞 AI 响应链。生产环境的日志表规模和持续写入量已足以让这种耦合成为
可用性风险。

消费日志还有一项隐含耦合：同步 INSERT 返回的自增 `logs.id` 会作为
`consumption_costs.log_id` / `employee_commission_logs.log_id` 的关联和幂等键。异步写日志后，
relay goroutine 无法等待自增 ID，因此不能只把 `LOG_DB.Create` 换成 `go func()` 或 channel。

线上 `logs` 已约 4 亿行。本方案把“不修改任何现有表结构、不新增索引、不执行大表迁移”作为
硬约束。

## 2. 目标和范围

### 2.1 目标

1. relay happy path 只做内存对象构造和一次短锁 buffer append，不等待日志数据库。
2. 使用热更新上限的有界 buffer 和固定数量 worker，禁止每请求创建 goroutine。
3. 使用批量 INSERT 降低日志库连接占用、事务数和索引维护开销。
4. 日志库故障、锁等待或队列过载不能影响 relay 响应、额度结算和业务统计。
5. 保留消费成本与员工提成现有 `log_id` 关联和幂等语义。
6. 支持立即关闭异步功能并回退到现有同步逻辑。
7. 优雅退出时在有限时间内排空；超时后把未写入日志交给异步兜底文件队列。

### 2.2 非目标

- 不改变日志查询、导出、清理 API 的响应结构。
- 不保证日志数据库不可用期间绝对零丢失；日志是非关键旁路，系统优先保证 relay 可用。
- 不把 Redis 放进每个 relay 请求的日志路径。
- 不在本次改造中迁移或分区现有 `logs` 大表。
- 不修改 `logs`、`consumption_costs`、`employee_commission_logs` 或其他任何表结构与索引。
- 不异步化管理、登录、充值等非 relay 审计日志；这些日志继续调用同步 `createLog`。

## 3. 总体设计

新增独立的 `relaylog` 管线，仅处理 `LogTypeConsume` 和 `LogTypeError`：

```text
relay goroutine
    |
    | 构造 RelayLogEvent + 短临界区 append
    v
consumeBuf / errorBuf（独立有界 slice，容量每次 append 读取热配置）
    |
    | swap-and-drain；凑满 batch 用容量 1 的 wake channel 唤醒
    v
固定 1 个 DB writer
    |
    | context timeout + CreateInBatches
    v
LOG_DB.logs
    |
    +-- 成功：有界 continuationBuf -> 固定 2 个 continuation worker
    +-- 失败：有界重试
    +-- 超过重试预算：异步兜底文件队列
```

设计选择（对齐现有台账 `business_stats_buffer.go`）：

- 两个独立的有界 slice buffer：消费日志与错误日志物理隔离，错误风暴不能挤掉消费日志容量。
- 每次 append 在各自 mutex 的短临界区内完成，并现场读取 `GetBufMaxEntries()`；与台账
  `bufferCostAndCommissionLedger` / `BufferConsumptionCostRecord` 的动态容量模式一致。
- flush 时在锁内把当前 slice 与新空 slice 交换，立即释放锁；数据库 I/O 全部在锁外执行。
- 容量降低后，下一次 append 按新上限淘汰最旧事件；淘汰项在释放 buffer 锁后进入独立 fallback
  和 continuation 路径，不在主锁内序列化或访问文件。
- 一个 DB writer：同一进程最多占用日志池一个写连接，避免恢复时并发批量写造成连接风暴。
- 成本/提成 continuation 使用热更新上限的有界 slice buffer 和固定 2 个 worker；日志 INSERT
  返回并归还连接后才 append，因此 continuation 处理不会占住 LOG_DB 连接或阻塞 relay。
- 不使用 GORM 默认逐行 Create；使用 `CreateInBatches`，每批一个有超时的数据库操作。
- flush 采用“条数或时间先到即触发”：累计到 `inner_batch_size` 立即写入；低流量下未凑满时，
  到 `flush_interval_ms` 强制写入。
- 满批唤醒使用容量 1 的通知 channel 和非阻塞 send；通知只表达“需要 flush”，不承载日志数据。
- 不复用 `common.relayGoPool`；该池当前容量近似无界，不满足本功能的有界要求。

### 3.1 与现有台账方案的对应关系

| 台账现有模式 | 日志管线对应实现 |
|---|---|
| `LedgerPipelineSetting` + `LedgerRetryQueueSetting` | `RelayLogPipelineSetting` + `RelayLogRetrySetting` |
| slice buffer + mutex + 热读 `GetBufMaxEntries()` | consume/error/continuation 三个独立 slice buffer |
| flush 时取走当前 buffer，DB I/O 在锁外 | `swap-and-drain` 后由单 writer 写 `LOG_DB` |
| `OuterBatchSize` / `InnerBatchSize` | 失败粒度 / 单条 INSERT 行数 |
| `SettlementFlushMaxPerCycle` / `FullDrain` | `FlushMaxPerCycle` / `FullDrain` |
| `businessStatsFlushMu` + retry `TryLock` | 独立 `relayLogFlushMu` + retry `TryLock` |
| `flushDBWithTimeout()` | 日志专属 `LOG_DB.WithContext(timeoutCtx)` |
| 主 buffer → retry queue → fallback | 主 log buffer → retry buffer → relay-log fallback |
| `ShutdownStatsFlush` | `ShutdownRelayLogFlush` |

复用的是经过验证的结构和实现方式，不共享台账的 buffer、mutex、fallback channel 或 DB 连接池，
确保日志故障不会抢占账务恢复资源。

## 4. 数据流

### 4.1 消费成功

1. relay 完成上游响应和额度结算。
2. 生成或取得本次请求的 `request_id`。
3. 构造消费 `Log` 和不含 `gin.Context` 的结算 continuation payload。
4. 调用 `EnqueueConsumeLog`。
5. 入队成功：主链立即继续；成本/提成 continuation 暂由日志事件持有。
6. writer 批量写入日志库，并取得 GORM 回填的各行自增 `logs.id`。
7. 每条成功日志使用其 `logs.id` 异步继续执行既有成本/提成流水逻辑。
8. 入队失败：主链不等待日志数据库，立即沿用现有 `log_id=0/nil` 兼容路径触发成本/提成逻辑；
   日志本身计为丢弃。
9. 日志批次达到最终失败条件：事件转兜底文件，同时用 `log_id=0/nil` 执行 continuation，
   账务不等待日志库恢复。

### 4.2 Relay 错误

1. relay 生成对客户端的错误响应。
2. 构造已脱敏的错误 `Log`。
3. 调用 `EnqueueErrorLog` 非阻塞入队。
4. 错误 buffer 超限时按台账策略淘汰最旧错误并计数，不占用消费日志 buffer。
5. 日志写入结果不改变原 relay 错误响应。

### 4.3 功能关闭

`enabled=false` 表示**停止记录新的 relay 日志**，不是回退到同步写入（定义见 §5.1.1）。
开关由现有后台设置 API 热更新；
worker 常驻但停止接收新的异步事件，已入 buffer 的事件继续排空。由于同步模式本身可能阻塞，生产环境
正常运行应启用异步模式；关闭开关只用于定位或回滚。

### 4.4 优雅退出

1. 先执行 HTTP drain，让在途 relay 请求完成并入队。
2. 停止接受新的异步日志事件。
3. 在 `shutdown_timeout_sec` 内排空消费 buffer、错误 buffer 和重试 buffer。
4. 排空 continuation buffer，保证已决定的成本/提成任务进入既有统计缓冲。
5. 超时后把剩余日志事件交给兜底文件 writer；其 continuation 以 `logID=0/nil` 进入
   continuation buffer。文件 writer 也只等待其独立预算。
6. 日志管线关闭完成后，再结束进程。

该步骤插入现有 `shutdownSequence` 的 HTTP drain 与最终退出之间，并与业务统计 flush 分配独立预算。

## 5. API 合同

不改变现有日志查询响应。为后台页面新增一个只读运行状态 endpoint，完全对齐台账状态接口：

```http
GET /api/admin/system/relay-log-pipeline/status
Authorization: AdminAuth
```

响应继续使用项目标准成功结构：

```json
{
  "success": true,
  "data": {
    "enabled": true,
    "circuit_state": "closed",
    "consume": {
      "backlog": 120,
      "capacity": 20000,
      "dropped": 0,
      "last_flush_items": 500,
      "last_flush_took_ms": 42
    },
    "error": {},
    "retry": {},
    "continuation": {},
    "persisted_total": 123456,
    "fallback_total": 0,
    "db_timeout_total": 0,
    "oldest_event_age_ms": 85,
    "last_success_at": 0,
    "last_error_at": 0
  }
}
```

路由放入现有 `systemAdminRoute`（该 group 已使用 `middleware.AdminAuth()`），与
`GET /api/admin/system/ledger-pipeline/status` 同级。状态读取仅访问内存原子计数和短锁快照，
不访问 LOG_DB、主库或 Redis。

内部 API（[model/relay_log_pipeline.go](../../model/relay_log_pipeline.go)、
[model/log.go](../../model/log.go)、[service/consume_settlement.go](../../service/consume_settlement.go)）：

```go
// 入队；false 表示管道已停止收件，调用方自己兜底 accounting。
func EnqueueConsumeLog(c *gin.Context, userId int, params RecordConsumeLogParams,
    accounting *RelayLogAccountingPayload) bool

// accounting 载荷只带原语字段，才能在进程崩溃后从 JSONL 兜底文件复原。
type RelayLogAccountingPayload struct {
    Version, UserID, ChannelID, Quota int
    ChannelName, UsingGroup, OriginModelName string
    GroupRatio float64
    SurchargeQuota int64
}

// 入队失败/管道关闭时，用 logID=0 单独排队 accounting。
func DispatchRelayLogAccounting(payload RelayLogAccountingPayload, logID int)
func RegisterRelayLogAccountingHandler(handler func(RelayLogAccountingPayload, int))

// 同步刷干缓冲 + 等待 continuation/fallback worker 收敛。阻塞且访问 LOG_DB，
// 只给维护路径和用例用，绝不能在 relay goroutine 上调用。
func DrainRelayLogsSync(timeout time.Duration)
```

`RecordConsumeLog` 原本返回数据库自增 `int logID`。异步模式下不再承诺同步返回 `logs.id`：
成功入队后由 writer 在批量 INSERT 成功、GORM 回填 ID 后执行 continuation；入队失败或最终写入
失败时以 `logID=0` 执行既有兼容逻辑。`RecordConsumeLog` 本身保留同步语义，供非 relay 路径使用。

因此 `service.FinalizeConsumptionSettlement` 不再返回 `logID`（返回值恒为 0，已删除），
`ConsumptionSettlementParams.AsyncCostAndCommission` 也随之删除——成本/提成一律由管道的
continuation worker 调度，不再由调用方决定同步还是 `gopool.Go`。

除上述管理员只读状态路由外，不新增公开接口；配置保存继续复用现有管理员设置 API。

## 6. 数据模型与迁移

本功能不修改任何数据库结构：

- `logs`：不新增列、不修改列、不新增或删除索引；
- `consumption_costs`：不变；
- `employee_commission_logs`：不变；
- 不新增表；
- 不执行数据回填；
- 不增加启动迁移。

成功批量 INSERT 后依赖 GORM 对传入切片逐行回填既有自增 `logs.id`。实现前必须分别用真实
MySQL、PostgreSQL 和 SQLite 验证 `CreateInBatches` 的 ID 回填顺序与完整性。若某数据库驱动
无法可靠回填批量 ID，该数据库采用后台逐条 INSERT 兼容 writer；它仍然只占一个后台连接，
不回退到 relay goroutine，也不修改表结构。

ClickHouse 日志路径当前不依赖传统自增 ID；对应 continuation 沿用现有 `logID=0/nil` 行为。

### 6.1 PostgreSQL 批量适配

PostgreSQL 支持本方案，不需要数据库结构调整：

- 使用 GORM `CreateInBatches` 生成多值 `INSERT ... VALUES (...), (...)`；
- `inner_batch_size=500` 乘以 `Log` 的实际写入列数明显低于 PostgreSQL 单语句参数数量上限；
- 不使用 PostgreSQL 专属 `COPY`，保证 MySQL、PostgreSQL、SQLite 共用主实现；
- 每批使用独立超时上下文，SQL 返回后立即归还连接；
- 不在批量 INSERT 外包裹长事务，不跨批持有连接；
- 实施测试必须验证 PostgreSQL 驱动通过 `RETURNING` 回填的 ID 与输入切片顺序一致。

如果线上实测 500 行单批 SQL 触发包大小、参数数或延迟问题，只调整 `inner_batch_size`，无需修改
表结构。

## 7. 配置参数

在 `setting/operation_setting/relay_log_pipeline_setting.go` 注册
`relay_log_pipeline_setting` 和 `relay_log_retry_setting`，结构与现有
`ledger_pipeline_setting` / `ledger_retry_setting` 对齐：

```go
type RelayLogPipelineSetting struct {
    Enabled                  bool `json:"enabled"`
    ConsumeBufMaxEntries     int  `json:"consume_buf_max_entries"`
    ErrorBufMaxEntries       int  `json:"error_buf_max_entries"`
    ContinuationBufMaxEntries int `json:"continuation_buf_max_entries"`
    OuterBatchSize           int  `json:"outer_batch_size"`
    InnerBatchSize           int  `json:"inner_batch_size"`
    FlushMaxPerCycle         int  `json:"flush_max_per_cycle"`
    FullDrain                bool `json:"full_drain"`
    FlushIntervalMs          int  `json:"flush_interval_ms"`
    WriteTimeoutSec          int  `json:"write_timeout_sec"`
    FallbackQueueCapacity    int  `json:"fallback_queue_capacity"`
    FallbackMaxFileSizeMB    int  `json:"fallback_max_file_size_mb"`
    FallbackMaxFiles         int  `json:"fallback_max_files"`
    ShutdownTimeoutSec       int  `json:"shutdown_timeout_sec"`
}

type RelayLogRetrySetting struct {
    RetryFlushIntervalMs int  `json:"retry_flush_interval_ms"`
    MaxRetries           int  `json:"max_retries"`
    RetryBufMaxEntries   int  `json:"retry_buf_max_entries"`
    AllowConcurrentFlush bool `json:"allow_concurrent_flush"`
    CircuitFailureThreshold int `json:"circuit_failure_threshold"`
    CircuitOpenSec          int `json:"circuit_open_sec"`
}
```

该配置通过现有 ConfigManager 和管理员设置 API 读写，不新增保存 endpoint，不使用环境变量作为
唯一配置源。全部字段支持后台热更新，调大调小都在下一次生效周期内即时生效，没有任何字段需要重启；
`continuation_buf_max_entries` 与 `fallback_queue_capacity` 的容量上界见 §18。

建议默认值：

| 参数 | 默认值 | 理由 |
|---|---:|---|
| enabled | false | 首次上线灰度启用，支持后台热开关 |
| consume_buf_max_entries | 20000 | 100k RPM 下约 12 秒突发容量 |
| error_buf_max_entries | 5000 | 隔离错误风暴 |
| continuation_buf_max_entries | 20000 | 吸收成本/提成后台处理抖动 |
| outer_batch_size | 2000 | 单次失败后的重试/回退粒度，对齐台账 outer batch |
| inner_batch_size | 500 | 每条 SQL 的行数，对齐台账 inner batch |
| flush_max_per_cycle | 15000 | 单周期最大取数，限制数据库恢复时突发 |
| full_drain | false | 默认按周期上限均衡刷盘 |
| flush_interval_ms | 5000 | 与现有 5 秒批量运维习惯一致；只限制未满批次的等待时间 |
| write_timeout_sec | 3 | 防止一个批次长期占住连接 |
| fallback_queue_capacity | 10000 | 后台兜底，不回压 relay |
| fallback_max_file_size_mb | 256 | 有界磁盘占用 |
| fallback_max_files | 8 | 最多约 2GB |
| shutdown_timeout_sec | 20 | 小于 HTTP drain 后的总体退出预算 |

重试配置默认值：

| 参数 | 默认值 | 理由 |
|---|---:|---|
| retry_flush_interval_ms | 2000 | 对齐台账独立 retry loop |
| max_retries | 10 | 对齐台账最大重试次数 |
| retry_buf_max_entries | 50000 | 独立限制重试积压 |
| allow_concurrent_flush | false | 默认与主 flush 互斥，避免争抢 LOG_DB 连接 |
| circuit_failure_threshold | 5 | 连续失败后暂停主动写库，避免故障放大 |
| circuit_open_sec | 10 | 熔断期间只积累/降级，不占数据库连接 |

### 7.1 热更新语义

所有配置通过 getter 读取，代码不跨请求或 flush 周期缓存配置指针。与台账一致，flush/retry loop
在每轮开始时读取最新间隔并创建本轮 `time.After`；因此配置无需重启，最迟在当前等待周期结束后
生效，不新增配置回调或第二套发布机制。

| 配置 | 热更新行为 |
|---|---|
| `enabled` | 立即决定新请求是否记录日志；置 false 后不再入队，既有 buffer 继续排空 |
| buffer max entries | 下一次 append 立即使用新上限；增加后直接扩容，降低后淘汰最旧超限项并送 fallback |
| outer/inner batch size | 下一批立即使用；若当前累计量已达到新 inner batch，立刻唤醒 flush |
| flush max/full drain | 下一周期立即决定最多取多少积压 |
| `flush_interval_ms` | 当前等待周期结束后，下一轮按新间隔创建 timer |
| write timeout | 下一次 DB 写入使用新值，不取消已经执行中的 SQL |
| retry/熔断参数 | retry loop 每轮读取 getter；下一次失败、取数、等待和熔断判定使用新值 |
| fallback 参数 | 容量立即使用软上限；文件大小和数量从下一次滚动/清理开始使用 |
| shutdown timeout | 下一次进程关闭时使用 |

buffer 使用 slice，不在启动时按最大容量预分配完整日志对象。getter 对配置做正数回退和上限
校验，防止误配置导致 OOM。降低容量时沿用台账策略淘汰最旧项；淘汰发生在短锁内，fallback
序列化和 continuation 调度在释放锁后执行。

fallback 写入队列按台账模式采用固定物理 hard cap + 热更新 soft cap：channel 启动时创建一次，
运行中不关闭或替换；每次写入前读取 `GetFallbackQueueCapacity()`。日志 fallback 必须使用独立
channel 和文件 basename，不能与台账共享队列容量，以免错误风暴挤占账务兜底资源。

### 7.2 与现有 `BATCH_UPDATE_INTERVAL` 的关系

现有 `BATCH_UPDATE_ENABLED` / `BATCH_UPDATE_INTERVAL=5` 只控制用户额度、Token 额度、渠道已用
额度和请求次数等聚合更新，不包含 `logs` INSERT。

日志管线不直接复用 `BATCH_UPDATE_INTERVAL`，原因是：

- 两者访问不同数据库资源：额度更新使用主库，日志通常使用独立 `LOG_DB`；
- 日志需要 `inner_batch_size` 满批立即触发，而现有 updater 是固定周期唤醒；
- 两者故障和容量调优应互不影响；
- 修改日志刷盘间隔不能改变额度更新时效。

默认值同为 5 秒只是保持运维认知一致，实际参数分别由
`relay_log_pipeline_setting.flush_interval_ms` 和 `BATCH_UPDATE_INTERVAL` 控制。

### 7.3 后台配置校验

保存配置时执行服务端校验并返回现有设置 API 的标准错误：

- buffer max entries 必须大于 0 且不超过服务端安全上限；
- inner batch size 必须为 1–2,000，outer batch 必须不小于 inner batch；
- flush max per cycle 必须不小于 inner batch；
- flush interval 建议限制为 100–60,000ms；
- write timeout、shutdown timeout 必须为正数并设置合理上限；
- max retries、fallback 文件大小和数量必须有上限；
- 熔断阈值和开启时长必须为正数并设置上限；
- 页面使用 Zod 先校验字段间约束，只提交发生变化的 key；保存方式与台账页面一致，逐 key 调用
  现有设置 API。任一 key 保存失败时停止后续提交、显示用户可执行的错误提示并重新拉取服务端值。

热更新失败时保留上一版有效配置，记录 `common.SysError`，不得影响 relay。

### 7.4 后台配置页面

#### 7.4.1 页面位置与复用关系

在现有“系统设置 → 系统调优”section registry 中新增：

```ts
{
  id: 'relay-log-pipeline',
  titleKey: 'Relay Log Pipeline'
}
```

页面组件建议为：

`web/src/features/system-settings/maintenance/relay-log-pipeline-section.tsx`

必须复用现有 `LedgerPipelineSection` 的页面结构和类名组合：

- `SettingsSection`
- `SettingsForm`
- `SettingsControlGroup`
- `SettingsSwitchItem` / `SettingsSwitchContent`
- `SettingsPageFormActions`
- React Hook Form + Zod
- `useUpdateOption`
- TanStack Query 每 10 秒刷新运行状态

不修改上游保留文件 `web/src/lib/api.ts`；状态请求和类型分别放在既有
`web/src/features/system-settings/api.ts` 与 `types.ts`。

#### 7.4.2 页面布局

页面自上而下分为五部分：

1. **状态总览**
   - 标题旁显示状态 badge：已启用、已停用、熔断、恢复探测。
   - 四张状态卡：消费日志 buffer、错误日志 buffer、重试 buffer、continuation buffer。
   - 卡片完全复用台账卡片样式：
     `rounded-xl border bg-muted/20 p-4 text-sm`。
   - 每张显示 backlog/capacity、dropped、最近刷盘条数、刷盘耗时。
   - 额外显示：最老事件等待时间、累计 fallback、DB timeout、最近成功时间。

2. **启用与刷盘**
   - `Enabled` switch；
   - flush interval；
   - flush max per cycle；
   - full drain switch；
   - 当前配置理论吞吐提示：
     `flush_max_per_cycle × 60000 ÷ flush_interval_ms`。

3. **批次与 Buffer**
   - outer batch size；
   - inner batch size；
   - consume/error/continuation 三个 buffer max entries；
   - 动态提示各 buffer 在当前 100k RPM 估算下可吸收多少秒积压。

4. **重试、熔断与降级**
   - retry interval、max retries、retry buffer max；
   - allow concurrent flush switch，旁边使用 warning alert 说明会增加 LOG_DB 连接竞争；
   - circuit failure threshold、circuit open seconds；
   - fallback queue、文件大小和文件数上限；
   - 实时显示当前 circuit state。

5. **超时与退出**
   - write DB timeout；
   - shutdown timeout；
   - 提示 shutdown timeout 必须小于进程总体退出预算。

桌面端字段组使用与台账相同的响应式 grid；移动端单列。不得创建新的 Card、Input、Switch 视觉
变体，不硬编码颜色，全部使用现有 token 和 Tailwind 类。

#### 7.4.3 表单动态提示

对齐台账页面的 `fieldHints`：

- `inner_batch_size > outer_batch_size`：阻止保存；
- `flush_max_per_cycle < inner_batch_size`：阻止保存；
- write timeout 大于或等于 flush interval：warning，不阻止保存；
- consume buffer 小于五个 outer batch：warning；
- retry buffer 小于一个 outer batch：warning；
- `allow_concurrent_flush=true`：warning；
- fallback 总磁盘上限实时显示
  `fallback_max_file_size_mb × fallback_max_files`；
- 预计正常满批时间显示
  `inner_batch_size ÷ estimated_events_per_second`。

页面不要求管理员理解实现术语：每个提示都说明调整后对“日志延迟、数据库压力、故障期间可保留
时长”的影响。

#### 7.4.4 保存与热更新交互

- 页面加载时从现有 option 列表构建 nested form defaults。
- 保存时 flatten 为：
  - `relay_log_pipeline_setting.*`
  - `relay_log_retry_setting.*`
- 只提交 changed keys；没有改动时提示“没有需要保存的更改”。
- 保存中禁用保存和重置按钮，防止重复提交。
- 保存成功后更新 baseline、reset form，并立即刷新状态 query。
- 某个 key 保存失败时停止剩余提交，重新读取服务端配置，提示：
  “部分设置未保存，已重新加载当前配置，请检查后重试。”
- 后台参数在服务端 getter 的约定周期内热生效，页面不显示“需要重启”。
- 将 `enabled` 从 true 改为 false 时弹确认：
  “关闭后将**停止记录**新的 relay 消费/错误日志（不会回退到同步写入）。
  额度结算与成本/提成不受影响，但这段时间的日志无法事后补齐。”
- 开启 `allow_concurrent_flush` 时弹确认，说明它可能增加 PostgreSQL 连接竞争。

#### 7.4.5 加载、错误与空状态

- 初次设置加载沿用 `SettingsPage` 的 loading state。
- 状态 endpoint 加载失败不禁用配置表单；状态卡显示“暂时无法获取运行状态”，提供重试按钮。
- 保存错误不得显示 Go error、SQLSTATE 或内部配置 key；底层错误写系统日志，前端展示可操作提示。
- 状态轮询不显示全页 loading，不因 10 秒刷新造成卡片闪烁。
- pipeline 未启动时显示“日志管线尚未运行”，而不是全零造成误解。

#### 7.4.6 无障碍与键盘操作

- 所有 Input 通过 `FormLabel` 关联；
- warning 不能只依赖颜色，必须带图标和文本；
- switch 描述明确“开启/关闭后的结果”；
- 保存、重置、确认对话框支持键盘焦点；
- 数值字段错误由 `FormMessage` 暴露，提交失败后聚焦首个错误字段。

#### 7.4.7 前端 i18n

所有新增 UI 文案使用 `t('English source key')`，覆盖：

- 页面和字段标题；
- 字段说明及动态 warning；
- 状态名称、卡片指标；
- 保存、部分失败和状态加载错误；
- 两个高风险开关的确认对话框；
- 单位与插值文案。

实现时必须通过 `web/scripts/add-missing-keys.mjs` 一次写入
`en`、`zh`、`zh-TW`、`fr`、`ja`、`ru`、`vi`，随后运行 `bun run i18n:sync`；禁止直接编辑 locale
JSON。法语、俄语和越南语文案需要按最长文本验证移动端不溢出。

## 8. Buffer、背压和丢弃策略

### 8.1 优先级

- 消费日志：高优先级，拥有独立容量。
- 错误日志：低优先级，错误风暴时优先丢弃。
- 每轮 flush 的条数预算（`flush_max_per_cycle`）先给消费日志用满，错误日志只取剩余额度。
  两者价值不对等——消费日志关联计费与对账，错误日志只用于诊断且从不携带记账负载。正常
  负载下总量远小于每轮预算，两类都会被完整刷完；只有积压时该顺序才生效，而积压时正是要
  保消费日志。代价是错误日志可能持续排队直到自身缓冲满、溢出到 fallback 并被优先丢弃。

### 8.2 Buffer 超限

禁止同步写数据库作为 fallback，因为这会把故障重新传回 relay 主链。

buffer 超限时对齐台账的 oldest-eviction 策略：

- 当前事件正常 append，淘汰最旧的超限事件，避免持续高流量下新日志全部丢失；
- 淘汰的消费事件在锁外以 `logID=0/nil` 进入 continuation，并尝试写独立 fallback；
- 增加原子计数 `relay_log_dropped_total{type,reason=buffer_overflow}`；
- 采用限频日志（例如每 10 秒一次汇总），禁止每次丢弃都打印日志；
- 额度结算、成本流水和客户端响应不受影响。

### 8.3 数据库失败

对齐台账的“主 buffer → 独立 retry buffer → fallback”三层模型：

- 主 flush 失败后，把未成功部分加入独立 `retryBuf`，不塞回入口 buffer。
- 每个事件携带 `RetryCount`；达到 `max_retries` 后写入 fallback。
- `retryBuf` 每次追加后读取热更新的 `retry_buf_max_entries`；超出时淘汰最旧项到 fallback。
- 主 flush 与 retry flush 共享 `relayLogFlushMu`。默认 `allow_concurrent_flush=false`，
  retry loop 使用 `TryLock`；主 flush 正在执行时跳过该轮，避免两个循环争抢 LOG_DB 连接。
- 明确开启 `allow_concurrent_flush=true` 时仍受独立 writer semaphore 保护；默认数据库写并发为 1。
- flush state 对齐台账的 `onFailure/onSuccess` 模式：连续失败达到阈值后打开日志专属熔断器；
  熔断期间不主动访问 LOG_DB，只继续有界积累、淘汰到 fallback。到期进入 half-open，只允许一个
  小批次探测；成功关闭熔断，失败重新打开。
- fallback 队列也满时丢弃批次并发出限频的高等级系统告警。

### 8.4 兜底文件

- 使用独立命名空间/目录，例如 `{LogDir}/relay-log-fallback/`，不与请求日志、业务统计 dead-letter
  文件混用。
- JSON 编解码必须使用 `common.Marshal` / `common.Unmarshal`。
- 文件滚动和总文件数均有上限；不能耗尽磁盘。
- 后台 replayer 以单 worker、小批量、数据库超时方式重放；数据库仍异常时停止本轮，指数退避。
- 兜底文件只保存日志，不保存待执行的账务 continuation。事件进入兜底文件前，continuation
  已以 `logID=0/nil` 执行一次，因此重放日志不会重复计费或重复提成。
- 为避免给 4 亿行 `logs` 表新增唯一索引，fallback 重放采用“至少一次”日志语义；进程在
  “数据库写成功但确认前崩溃”的极端窗口可能生成重复日志，但账务不会重复。

### 8.5 落库顺序：按 relay 响应完成顺序

选出本轮事件后、进入外层分批之前，按 `relayLogEvent.EnqueuedAt` 升序稳定排序一次，再按
`outer_batch_size` 切批写库。

**为什么必须排**：`logs.created_at` 只有秒级精度，列表查询按 `created_at DESC, id DESC`
排序，因此同一秒内的先后完全由自增 `id`——也就是插入顺序——决定。消费与错误走两条独立
缓冲、又按 8.1 的预算成段取出，若直接落库，同一秒里会出现"一整段成功、一整段失败"，而不
是真实的交错时间轴。`EnqueuedAt` 在入队时记录，入队点就是 relay 响应结束的那一刻，按它
排序后 `id` 单调跟随真实响应时间，读路径不必新增任何排序表达式，也不动索引。

**为什么排在外层分批之前**：批的切分点是任意的，只在批内排序仍会让相邻两批之间出现时间
回退。

**为什么不是随机顺序**：读路径侧的 `ORDER BY RAND()/RANDOM()` 用不上索引，要对整个筛选
结果集做全量排序，`logs` 这种持续增长的大表承受不住；且 `LIMIT/OFFSET` 分页每页重新随机，
同一行可能重复出现或一次都不出现。写入侧的批内洗牌则让顺序既不反映真实时间又不可复现。

**不影响的部分**：8.1 的消费优先预算分配不变——排序只作用于已经选出的这一批。整个过程在
relay 侧缓冲锁之外、由后台 flush goroutine 执行，单轮上限 `flush_max_per_cycle`（默认
15000）条指针的稳定排序耗时在毫秒级，不新增任何共享资源。retry buffer 与 fallback 重放
写入的日志仍会拿到远晚于其 `created_at` 的 `id`，属于既有语义，本节不改变。

日志 fallback 参考台账 dead-letter/backfill 模式，但资源必须隔离：

- 复用“调用方预序列化 → 有界 channel → 单文件 writer → shutdown flush/close”的结构；
- 使用独立 `relay_log_fallback` basename、独立 channel 和独立文件状态；
- 重放参考台账 backfill，由受控后台任务按批大小、批间休眠和 DB timeout 执行；
- 不与台账共享 `fallbackQueue`、`businessStatsFlushMu` 或 retry buffer，避免日志故障饿死账务兜底。

## 9. 关键业务逻辑和边界情况

1. **自增 ID 延迟交付**：成功批量写入后才把回填的 `logs.id` 交给 continuation；relay 不等待。
2. **ID 回填校验**：批量成功但任一消费日志 ID 未回填时，该条 continuation 使用 `logID=0`，
   同时记录限频系统错误，禁止猜测或按位置生成 ID。
   实施前通过测试和调用点清单确认当前是否存在多消费事件。
3. **日志开关关闭**：消费日志关闭时业务流水仍正常写入。
4. **错误日志关闭**：不构造、不入队错误日志。
5. **超大字段**：沿用现有内容截断/脱敏规则；buffer 对象不得持有 `*gin.Context`、请求 body、
   response writer 或其他请求生命周期资源。
6. **panic**：writer、fallback writer、replayer 顶层都必须 `recover`，记录系统错误并重新拉起
   或安全退出；panic 不能传播到 relay。
7. **ClickHouse**：验证 GORM `CreateInBatches` 行为；如驱动不支持所需批量语义，使用
   ClickHouse 专用批量接口，但入口 buffer 和不访问 DB 的主链契约不变。
8. **SQLite**：单 writer 可减少 `database is locked`；批次和事务大小须在 SQLite 集成测试中验证。
9. **多实例**：每实例拥有独立 buffer 和 writer；每实例最多占一个日志写连接。数据库自增主键继续
   负责跨实例 ID 唯一性。
10. **热关闭**：`enabled` 从 true 改为 false 后，新请求不再记录日志（**不走同步路径**），旧异步 buffer 继续排空；
    不关闭 stop channel 或重建 worker。重新启用时复用常驻 worker。

## 10. 错误处理与可观测性

后台组件使用 `common.SysLog` / `common.SysError`，不使用请求 logger，因为事件不能保留
`gin.Context`。

至少暴露以下原子指标或现有性能指标项：

- buffer 当前长度、容量和高水位；
- accepted / persisted / retried / fallback / dropped 总数，按 consume/error 分类；
- 批大小和 flush 延迟；
- DB 写耗时与 timeout 数；
- 最老待写事件年龄；
- shutdown 剩余未落库数量。

告警建议：

- 消费日志任意 drop：高优先级告警；
- 错误日志 1 分钟 drop 比例超过 1%：告警；
- consume buffer 持续 30 秒超过 80%：告警；
- fallback 文件出现或持续增长：告警；
- DB writer timeout：立即计数，限频输出错误。

所有客户端响应保持现有 relay 错误协议，后台日志失败不返回给客户端。

## 11. 与现有子系统交互

### 11.1 Billing / quota

额度预扣和结算顺序不变。日志入队失败不能回滚或阻止计费。

### 11.2 成本与提成流水

保留现有 `logs.id` 幂等。日志 writer 只负责在 INSERT 成功后把 ID 交给 continuation，
continuation 再进入既有成本/提成处理流程；日志 writer 不直接写主数据库。

日志入队失败、最终写入失败或驱动未回填 ID 时，使用现有 `logID=0/nil` 兼容路径。该路径目前
不具备 `log_id` 幂等能力，因此必须统计发生次数；正常数据库状态下不应进入该路径。

### 11.3 日志查询与导出

最终落库结构不变。正常高流量下批次通常会先达到 500 条，日志可见延迟远小于 5 秒；只有低流量
且未凑满批次时，延迟才可能接近 `flush_interval_ms`。数据库故障恢复和 fallback 重放时延迟
更长。前端无需修改。

列表查询继续使用 `created_at DESC, id DESC` 这条既有索引顺序，不新增排序表达式；同一秒内
的成功/失败交错由写入侧的到达顺序保证，见 8.5。

### 11.4 日志清理

继续操作同一 `logs` 表。清理任务与 writer 共享 LOG_DB，但 writer 只有一个连接且有超时。
清理仍必须小批次执行，不能持有长事务。

## 12. Main Chain Impact

### 12.1 Relay goroutine 同步执行

- 读取当前功能开关；
- 读取当前有效配置；
- 从已有请求上下文复制所需标量和字符串；
- 复用请求上下文中已有的 request_id；
- 构造固定大小的 `RelayLogEvent`；
- 在独立 buffer mutex 的短临界区内 append；
- 满批时一次非阻塞 wake channel send；
- 更新少量原子计数。

禁止在该阶段执行：

- DB/Redis/文件访问；
- JSON 文件序列化；
- 等待 DB、Redis、文件或可能阻塞的 channel；buffer mutex 只允许内存 append/slice swap；
- 创建 goroutine；
- 重试；
- 保存 `gin.Context` 指针。

### 12.2 异步执行

- 批次聚合与定时 flush；
- LOG_DB 批量 INSERT；
- 重试与退避；
- 兜底文件写入和重放；
- 指标汇总和限频告警。

## 13. Shared Resource Audit

| 资源 | 新功能访问 | relay 主链是否访问 | 隔离措施 |
|---|---|---|---|
| `LOG_DB` 连接池 | writer 每实例最多 1 个并发写连接 | 当前同步日志会访问；异步启用后不再访问 | 独立 writer、写超时；建议配置独立 `LOG_SQL_DSN` |
| `logs` 表 | 批量 INSERT、fallback 重放 | 当前每请求同步 INSERT | 启用后主链只入队；后台单 writer |
| `consumption_costs` | 既有统计管线写入，不改结构 | relay 只通过异步统计管线间接访问 | 沿用既有缓冲和批量写 |
| `employee_commission_logs` | 同上 | 同上 | 同上 |
| Redis | 日志管线不访问 | relay 其他逻辑会访问 | 不新增 key，不争用 Redis 池 |
| 内存 buffer | consume/error 两个有界 slice | 主链短锁 append | 对齐台账 swap-and-drain；容量每次 append 读 getter |
| continuation buffer | 有界 slice | 否 | 固定 2 worker；容量热更新 |
| 重试内存 | 独立 retry slice | 否 | 独立锁、热更新上限、溢出转 fallback |
| fallback 文件队列 | 后台失败批次 | 否 | 独立固定 hard cap、热更新 soft cap |
| 文件句柄/磁盘 | 滚动 fallback 文件 | 否 | 独立目录、文件大小和数量上限 |
| goroutine | 主 flush 1，retry 1，continuation 2，fallback 1，replayer 1 | 主链不创建 | 固定 6 个，不使用无界 pool |

没有新增 Redis namespace。日志管线不使用主 relay 的 goroutine pool，也不对共享资源增加协调锁。

## 14. Concurrency Analysis（100k RPM）

100k RPM 约为 1,667 请求/秒。按每请求一条消费日志、5% 请求额外产生错误日志估算：

- 入口事件：约 1,750 条/秒；
- relay 同步 DB 调用：0；
- relay 同步 Redis 调用：0；
- relay 锁：一次独立 buffer mutex，临界区仅 append、长度判断和必要的 slice 截断，不含 I/O；
- relay 新增 goroutine：0；
- inner_batch_size=500 时，稳定状态约 3.5 次 INSERT/秒；
- 单 writer 的并发 DB 连接上限：1；
- 每个事件按平均 2–4KB 估算，20k+5k buffer 约占 50–100MB；实施时用 benchmark 测得真实值，
  并避免复制不需要的大字段；
- 5 秒 flush interval 只影响未满批次：在 1,750 events/s 下约 286ms 即凑满 500 条并触发；
- 500 events/s 时约 1 秒凑满；低于 100 events/s 时才可能由 5 秒定时器触发；
- 若单次 500 行 INSERT 为 50ms，理论写入能力约 10,000 行/秒，约为目标峰值的 5.7 倍；
- 若 DB 完全不可用，消费 buffer 约能吸收 12 秒、错误 buffer 约能吸收 60 秒（按 5% 错误率）；
  此后按明确策略丢弃，不增长内存，也不阻塞 relay。

必须以实际生产字段大小和 PostgreSQL 批量 INSERT 延迟重新校准。验收压测至少覆盖：

- 1,667、3,333、5,000 events/s；
- DB 延迟 50ms、500ms、超过 timeout；
- DB 完全不可用 60 秒；
- 100% 错误风暴；
- shutdown 时 buffer 50%、100% 占用。

## 15. 测试设计（先测试后实现）

### 15.1 单元测试

1. buffer 容量边界：0/1/N/N+1，超限时按策略淘汰且不执行 I/O。
2. 消费与错误 buffer 隔离：错误 buffer 满不影响消费 append。
3. batch 边界：1、inner_batch_size-1、inner_batch_size、inner_batch_size+1。
4. 双触发：满 `inner_batch_size` 不等待定时器；低流量未满批次在 5 秒到期时写入。
5. 配置隔离：修改日志 flush interval 不改变现有 `BATCH_UPDATE_INTERVAL`，反之亦然。
6. 热增容量：无需重启即可增长到新上限。
7. 热降容量：下一次 append 按台账策略淘汰最旧超限项，淘汰项进入 fallback/continuation。
8. 热改 timer：当前周期结束后下一轮使用新间隔，不重复 flush、不泄漏 timer。
9. 配置读取：每个 flush/retry 周期只取一次本周期所需快照，不在周期中混用新旧批参数。
10. 非法配置：拒绝保存并保留上一版运行配置。
11. writer 超时：context 到期后释放连接并进入重试。
12. 重试分支：首次成功、重试成功、达到上限进入 fallback。
13. flush 互斥：默认模式下 main/retry 不会并发占用 LOG_DB；`TryLock` 失败安全跳过本轮。
14. 熔断状态：closed → open → half-open → closed/open 的全部分支，熔断期间 DB 调用为 0。
15. fallback 满：丢弃、计数、无阻塞。
16. panic recovery：DB writer/fallback/replayer panic 不传播。
17. shutdown：先拒绝新事件，再排空；超时后进入 fallback。
18. 热关闭/重启：并发 append 时不关闭 buffer、不重复启动 worker。
19. event 对象不保存 gin.Context。
20. 多实例并发批量 INSERT 后，各条数据库自增 ID 唯一且正确回填。
21. 到达时刻打戳：`enqueueRelayLog` 写入的 `EnqueuedAt` 落在调用前后区间内且不倒退。
22. 落库顺序（8.5）：同一 `created_at` 秒内交替到达的消费/错误事件，按
    `created_at DESC, id DESC` 读出的顺序等于响应顺序的倒序，两类交错而非各自成块。
23. 跨批顺序：`outer_batch_size` 小于本轮事件数时，全局 `id` 升序仍等于到达顺序。
24. 顺序不侵犯预算：错误事件比消费事件更早到达、且本轮预算只够消费事件时，本轮仍只写
    消费日志，错误事件留在 pending。

### 15.2 数据库集成测试

按项目测试规范优先使用真实 MySQL/PostgreSQL，SQLite 仅作 fallback：

1. 三数据库 `CreateInBatches` 写入字段和自增行为。
2. 批量 INSERT 后每条 `Log.Id` 均被正确回填且与输入行对应。
3. 驱动不支持可靠批量 ID 回填时走后台逐条兼容 writer。
4. 写超时/锁等待后连接归还连接池。
5. 最终失败时 continuation 只以 `logID=0` 执行一次。
6. fallback 重放只补日志，不重复执行账务 continuation。

### 15.3 Relay 回归测试

1. 消费日志 DB 不可用时 relay 仍完成响应和额度结算。
2. 错误日志 DB 不可用时原错误响应保持不变。
3. buffer 超限时 relay 延迟不随 DB timeout 增长。
4. 成功落库时业务成本/提成继续使用真实 `logs.id` 正确去重。
5. 功能开关关闭时保持现有同步行为。

### 15.4 状态 API 测试

1. 管理员可读取完整内存快照。
2. 普通用户被现有 `AdminAuth` 拒绝。
3. endpoint 不执行 DB/Redis 查询。
4. 并发 flush 时读取状态无 data race。
5. pipeline 未启动时返回明确状态而不是 panic。

### 15.5 前端验证

项目当前没有前端测试 runner，不新增测试框架。验证项：

1. Zod 覆盖所有字段边界和跨字段约束。
2. changed-key 保存、无改动、部分保存失败后的 refetch。
3. enabled 与 concurrent flush 确认对话框。
4. 状态轮询失败不影响表单保存。
5. 运行 `bun run typecheck` 和 `bun run lint`。
6. 通过脚本添加七种语言后运行 `bun run i18n:sync`，确认无 missing key。
7. 手工检查桌面/移动端、浅色/深色模式以及法语、俄语、越南语长文本。

### 15.6 性能与竞态

- `go test -race` 覆盖 enqueue、flush、热关闭和 shutdown。
- benchmark 测量 enqueue p50/p99、分配次数和事件真实内存大小。
- 目标：非阻塞 enqueue p99 小于 100µs，零 goroutine/request。
- 实施完成后运行相关包测试及 `go test ./...`。

## 16. 实施阶段

### 阶段 1：验证现有 ID 与失败兼容语义

- 测试先行；
- 在真实 MySQL、PostgreSQL、SQLite 验证 `CreateInBatches` 自增 ID 回填；
- 确认成本/提成现有 `logID=0/nil` 分支不会阻塞 relay；
- 不执行任何数据库迁移。

### 阶段 2：异步管线（开关默认关闭）

- 按台账模式实现双有界 slice buffer、swap-and-drain、单 writer、批量 INSERT、DB 超时、
  独立 retry buffer、flush 互斥和指标；
- 接入消费/错误日志；
- 实现管理员只读状态 API；
- 实现系统调优页面、Zod 校验、changed-key 保存和七语言 i18n；
- 接入 shutdown；
- 完成三数据库测试、故障注入、前端 typecheck/lint 和视觉检查。

### 阶段 3：兜底与灰度

- 实现有界 fallback 文件与 replayer；
- 单实例开启，观察 buffer 水位、drop、DB 写延迟和账务一致性；
- 逐实例灰度，不同时全量开启；
- 稳定后再评估是否把默认值改为 enabled=true。

### 阶段 4：文档回写

- 用实际代码路径、配置默认值、压测结果更新本文；
- 补充生产部署和回滚步骤。

## 17. 回滚方案

1. 将 `relay_log_pipeline_setting.enabled=false`，新请求立即停止记录日志（不回到同步写入）。
2. 先排空已入 buffer 的事件，再停止后台 worker；运行期热开关不关闭 stop channel。
3. 没有数据库 DDL 或数据迁移需要回滚。
4. 成本/提成始终保留既有 `log_id` 语义。

## 18. 待确认决策

实施前需要确认：

1. 是否接受日志库长时间不可用且所有有界缓冲均满时，优先保证 relay、允许丢日志。
2. fallback 文件总上限是否采用建议的约 2GB。
3. 是否接受日志成功写入前，成本/提成 continuation 最多延迟“flush + 有限重试”时间。
4. 是否接受 fallback 重放在极端进程崩溃窗口可能产生重复日志，但绝不重复执行账务。

## 19. 实际实现说明（2026-07-31）

本节以当前代码为准，用于修正文档前述设计阶段的差异。

- 功能默认启用。`enabled=false` 表示停止记录新的 relay 消费/错误日志，不会回退到 relay goroutine 同步写库；额度结算仍按原流程完成，成本/提成后续使用 `logID=0` 的兼容路径。
- relay 同步路径只构造原始值快照并执行 O(1) 的有界入队、拒绝新事件及非阻塞唤醒；热缩容的历史超额清理完全由后台 worker 执行。
- consume、error、retry、continuation、fallback channel 和 worker-local pending 均有硬上限。continuation 最大 20,000，fallback queue 最大 10,000；管理页面使用相同校验上限。
- 正常 flush、retry flush 与 fallback replay 共用 `relayLogFlushMu`，单实例同时最多只有一个 `LOG_DB` writer。replay 使用小批量写入，不在服务启动线程逐条写库。
- fallback JSONL 只保存待补写的日志，不保存可重放账务任务。启动时先快速轮转 active 文件，再由后台按顺序补写 active-history 和 rotated 文件；重放不会再次执行成本、提成或 `QuotaData`。
- fallback 文件数达到配置上限时保留已有未重放文件、停止继续轮转并严重告警，不删除未恢复数据，也不允许磁盘无界增长。
- shutdown 使用一个绝对截止时间，前 75% 用于数据库排空，后 25% 预留给剩余事件的 fallback 交接和辅助 worker 排空。
- 状态接口实际返回 consume/error/retry backlog、capacity、dropped，以及 persisted、fallback、DB timeout、熔断状态、continuation backlog/dropped、fallback backlog/errors 和最近成功/失败时间。页面每 10 秒刷新并突出显示 continuation/fallback 故障。

- 未修改任何数据库表、列或索引。SQLite 已验证 `CreateInBatches` 的 ID 回填；当前环境未提供可隔离的 PostgreSQL/MySQL 集成库，因此这两种数据库的真实驱动 ID 回填仍是上线前验证项。若驱动未回填某行 ID，该行后续安全使用 `logID=0`，不猜测 ID。

### 19.1 有界系统的极端故障语义

额度预扣与最终结算在日志入队之前完成，不依赖日志管道。成本/提成和额度导出后续由固定 worker 执行。

当内存队列、continuation、fallback queue 和有限磁盘同时耗尽时，在“relay 永不阻塞、内存和磁盘都必须有界”的约束下，无法继续无损接收无限事件。实现会停止日志 intake、增加 dropped/error 指标并输出严重系统告警；不会等待数据库、文件或 channel，也不会改变已经完成的主额度结算。运维页面会明确展示该故障，管理员需要恢复日志数据库或 fallback 目录容量/权限后再重新启用 intake。

这是一项明确的 graceful-degradation 边界，不应被描述为绝对零丢失。若未来要求成本/提成后续也具有严格零丢失语义，需要在请求预扣费之前预留持久化槽位或引入独立持久消息系统，属于请求生命周期级别的后续改造。

### 19.2 已完成验证

- buffer 边界、reject-new、热缩容后台淘汰、重试上限、熔断和进程内 continuation 单次执行；
- fallback retention 上限、active/rotated 后台重放，以及重放绝不重复账务；
- SQLite 批量插入 ID 回填；
- relay 事件快照不保留 `gin.Context`、请求体、WebSocket 或 API key；
- 后端相关包编译及聚焦测试；
- 前端 `bun run typecheck`、相关文件定向 lint、七语言 key 完整性和 UTF-8 无 BOM。

仓库全量 lint 仍包含与本功能无关的既有问题；`go test -race` 因当前环境未启用 CGO 未执行。上线前仍需在隔离的 PostgreSQL/MySQL 环境完成批量 ID 回填和连接池故障注入验证。

## 20. 手动回填实现（已落地）

### 20.1 目标与范围

- 进程启动时不自动回填 fallback 文件。
- fallback 文件继续自动轮转、持久化和保留，但只有管理员点击“开始回填”后才写回日志数据库。
- 在 Relay 日志管道设置区显示待回填文件/记录状态、回填按钮、运行进度和最近结果。
- 不修改数据库结构，不改变正常 relay 日志批量写入、重试、计费及提成流程。

### 20.2 数据流

```text
DB 写入最终失败
  -> 有界 fallback queue
  -> JSONL active/rotated 文件
  -> 管理员查看待回填状态
  -> 点击“开始回填”并确认
  -> 后台单任务流式扫描
  -> 与正常 flush 共用 relayLogFlushMu
  -> 小批量写回 LOG_DB.logs
  -> 成功记录从 fallback 文件移除，失败记录继续保留
```

启动时 `prepareRelayLogReplayFiles()` 只执行文件整理：

- 恢复上次原子替换中断遗留的 `.replay.source` / `.replay.tmp` 文件；
- 把遗留的 active `relay-log.jsonl` 重命名为带 `startup.<时间戳>` 的可回填文件；
- 不读取 JSONL 内容、不写 `LOG_DB`、不调用 replay；正常 flush/retry worker 随后启动并使用新的 active 文件。

状态查询和人工触发都会通过目录 glob 获取待回填文件，统计的是文件数而不是记录数。人工任务启动时取得一次待处理文件快照；运行期间新生成的 fallback 文件留待下一次人工回填。

### 20.3 API 合同

保留现有状态接口，并扩展手动回填状态：

```http
GET /api/admin/system/relay-log-pipeline/status
Authorization: AdminAuth
```

新增字段：

```json
{
  "replay": {
    "state": "idle",
    "pending_files": 3,
    "running_file": "",
    "processed_total": 0,
    "failed_total": 0,
    "started_at": 0,
    "finished_at": 0,
    "last_error": ""
  }
}
```

`pending_files` 只通过目录元数据统计，不逐行扫描文件；实现不提供“精确待回填记录总数”，`processed_total` / `failed_total` 只统计本次任务已经处理的记录。`running_file` 只返回文件名。`last_error` 返回稳定的错误标识，由页面映射为可操作提示，不返回数据库原始错误或完整路径。

新增手动触发接口：

```http
POST /api/admin/system/relay-log-pipeline/replay
Authorization: AdminAuth
Content-Type: application/json

{}
```

成功响应沿用项目标准：

```json
{
  "success": true,
  "data": {
    "started": true
  }
}
```

边界行为：

- 已有任务运行时再次触发：业务失败，提示“回填任务正在运行”。
- 没有待回填文件：成功返回 `started=false`，页面提示当前无需回填。
- 服务正在 shutdown：拒绝启动，提示稍后重试。
- 接口通过 CAS 建立 `running` 状态，同步完成目录元数据枚举后启动后台任务；不逐行扫描文件、不等待数据库写入。
- 路由放入现有 `systemAdminRoute`，复用 `middleware.AdminAuth()`。

### 20.4 后端状态机与并发

回填状态为：

```text
idle -> running -> succeeded
                -> partial_failed
                -> failed
```

- 使用 CAS/互斥保护单任务状态，同一实例最多运行一个回填任务。
- 手动任务固定一个 goroutine，不按文件或批次创建 goroutine。
- 每批最多 `inner_batch_size` 条，使用 `write_timeout_sec` 超时。
- 每个数据库批次必须取得现有 `relayLogFlushMu`，因此手动回填、正常 flush 和 retry flush 仍保持单 `LOG_DB` writer。
- 每批完成后释放 writer 锁，让正常新日志批次获得写入机会；手动回填不能长时间独占连接。
- 手动回填不经过 retry buffer，只读取 fallback 记录的 `Log` 字段并写入日志库；不会触发 `Accounting` continuation，也不会处理 `QuotaData`，因此不会重复扣费、提成或额度台账导出。
- 数据库批次成功后，对应行不再写入临时文件；失败批次、无法解析的行和不支持的记录版本保留在临时文件中。任务结束时通过 `source -> target` 两阶段替换，仅在全部成功时删除源文件。
- 启动、状态查询和手动触发会恢复替换中断遗留物：目标缺失时恢复 `.replay.source` 或 `.replay.tmp`，目标已存在时清理对应的陈旧遗留物。正在处理的文件受文件互斥和 active-path 标记保护。
- panic 在任务边界 recover，状态改为 failed，错误仅写系统日志并显示清理后的提示。
- shutdown 不启动新任务；正在运行的任务收到取消信号后停止读取，并保留未完成文件，下次可再次手动触发。

日志回填采用“至少一次”语义。由于本次明确不修改 4 亿级日志表结构，也没有可用于幂等写入的唯一恢复键，如果进程恰好在某批 `INSERT` 已提交、但 fallback 文件尚未完成替换前崩溃，该批日志下次人工回填时可能重复。此风险只影响日志记录，不会导致账务 continuation 或 `QuotaData` 重放。

### 20.5 页面设计

在现有 Relay 日志管道设置区增加“Fallback 手动回填”区域：

- 展示待回填文件数、任务状态、已处理数、失败数、开始/完成时间。
- 主按钮：“开始回填”。
- 点击后显示确认对话框，说明回填会占用日志数据库单 writer，但不会阻塞 relay 请求。
- `running` 时按钮显示“回填中…”并禁用，状态每 2 秒刷新；任务结束后恢复现有 10 秒轮询。
- 无待回填文件时按钮禁用，并显示“当前没有待回填日志”。
- API 启动失败时显示用户可操作的提示，例如检查任务是否正在运行、日志数据库是否可用。
- 后端文件恢复失败、单文件回填失败或任务 panic 会写 `SysError`；页面通过清理后的 `last_error` 显示错误告警。现有 continuation 丢弃数或 fallback 写入错误数非零时，页面继续显示管道关注告警。
- 不提供“删除 fallback”按钮，避免误删未恢复日志。
- 所有新增文案均通过 `t(...)`，已同步到 `en/zh/zh-TW/fr/ru/ja/vi` 七个 locale。

### 20.6 Main Chain Impact

relay goroutine 不读取回填状态、不扫描目录、不访问文件，也不调用回填 API。手动任务完全由管理员请求触发；管理接口执行内存状态 CAS/快照和目录元数据枚举，文件流式读取及数据库写入都在一个后台 goroutine 中完成。

正常 relay enqueue、额度结算与响应路径不变。手动回填只会在后台竞争 `relayLogFlushMu`；锁在每个小批次后释放，正常 flush 不会与回填并发占用多个日志库连接。

### 20.7 Shared Resource Audit

| 资源 | 手动回填访问 | 隔离与约束 |
|---|---|---|
| `LOG_DB` | 小批量 INSERT | 与 flush/retry 共用单 writer mutex 和写超时 |
| `logs` | 只插入 fallback 日志 | 不新增查询、索引或迁移 |
| fallback 目录 | 列目录、流式读写、两阶段替换、崩溃遗留物恢复 | 与文件 writer 使用文件级互斥；启动时归档 active 文件，运行时标记正在回填的文件 |
| 内存状态 | 一个任务状态结构 | 短锁/原子字段，管理接口只读快照 |
| goroutine | 每次有效触发一个 | CAS 保证单实例最多一个 |
| Redis/主数据库 | 不访问 | 无新 key、无主库连接竞争 |

### 20.8 测试设计

已实现的后端聚焦测试覆盖：

1. SQLite `CreateInBatches` 回填 ID 行为。
2. fallback 保留上限及 replay 临时文件按一个逻辑文件计数。
3. 启动只归档 active 文件，不自动写日志数据库。
4. 无文件返回 `started=false`，已有任务返回 `ErrRelayLogReplayRunning`。
5. `.replay.source`、`.replay.tmp` 及完成替换崩溃窗口的恢复。
6. 单任务、8 路并发触发只有一个成功。
7. 成功回填、失败批次保留、取消保留、panic 恢复和清理后的状态错误。
8. 回填与正常 flush 共用 writer mutex。
9. 大于 16 MiB 的单条 JSONL 记录可流式读取。
10. 回填不执行 accounting；实现同样忽略 `QuotaData`。

前端没有测试运行器，使用 TypeScript typecheck、定向 ESLint 和格式检查验证 API 类型、idle/running/empty/error 状态、确认对话框及按钮禁用逻辑。路由位于 `systemAdminRoute`，授权由现有 `AdminAuth()` 组保证。

已知验证边界：

- PostgreSQL/MySQL 的真实批量 ID 回填仍应在隔离环境做上线前验证；当前聚焦集成测试使用 SQLite。
- 未注入进程级 kill 精确复现“数据库提交后、文件替换前”窗口；该窗口按上述至少一次语义处理。
- 前端交互没有自动化浏览器测试，依赖 typecheck/lint 和人工页面验收。

### 20.9 回滚

回滚前端按钮和 POST 路由即可停止人工触发；也可先仅撤下按钮，状态接口和 fallback 文件不受影响。由于没有数据库结构变更，无 DDL 或数据迁移需要回滚。回滚前应等待正在运行的人工任务结束或通过正常服务 shutdown 取消，避免在文件替换中途直接终止进程。

## 21. continuation 与 fallback 队列的容量与热更新

### 21.1 语义

`continuation_buf_max_entries`（延迟计费任务队列）与 `fallback_queue_capacity`
（兜底文件写入队列）与本节其余参数一样支持双向热更新：调大调小都在下一次投递时
立即生效，不需要重启。

取值范围 `1 ~ 100,000`，默认仍为 `20,000` 和 `10,000`。上限同时就是底层 channel 的
物理容量——包初始化时按常量 `MaxRelayLogContinuationBufMaxEntries` /
`MaxRelayLogFallbackQueueCapacity` 一次性分配，配置值只作为投递时的软上限。两者取自
同一个常量，因此配置值恒不超过物理容量，软上限永远可达，不存在"配置写了 5 万、实际
只有 2 万"的偏差。

上限取 100,000 的依据：在 100k RPM 的设计目标（约 1,667 rps）下，10 万条积压意味着
记账已经落后 60 秒；再往上堆的正确处置是修数据库，不是加缓冲。代价是两条 ring buffer
常驻内存，元素各 16 字节，合计 3.2 MB。

历史配置若存有大于 100,000 的值，后端 getter 会钳到 100,000；前端表单在提交时按同一
上限校验并提示范围。

### 21.2 为什么不是别的做法

- **不按配置值分配物理容量**：Go channel 容量不能原地改，配置调大后要等下次启动才真正
  扩容，而 UI 上已经显示成新值。运维在故障中调大队列、以为立刻生效、实际仍按旧容量丢弃
  ——恰恰是最不能出错的时刻。这是本次改造要消除的问题。
- **不在运行时替换 channel**：worker、关停、排空和非阻塞投递同时持有 channel 引用。要让
  卡在 `range oldCh` 上的 worker 换到新 channel 就必须 close 旧的，而 close 前又必须保证
  没有生产者再写且旧队列已排空。这条队列跑的是记账任务，丢一条就是少算一笔提成。
- **不做动态 ring queue**：内存可随积压增长、也无需产品上限，但要重写两条队列的投递、
  消费、关停、排空四处逻辑。在同样达成"双向热更新"的前提下，预分配 3.2 MB 换掉整套重写
  是更划算的交易。

### 21.3 API、数据模型与错误处理

不新增 endpoint、表、列、Redis key 或迁移，继续使用现有 RootAuth 配置保存接口及
`options` 表字段。超出范围的值由前端 Zod 拒绝并提示；后端 getter 用 `bounded()` 钳制，
保证历史配置或绕过前端写入的值同样不会越界。

### 21.4 Main Chain Impact

同步执行在 relay goroutine 上的部分只有两处投递判断（`len(ch)` 比较 + 非阻塞
`select/default`）。本次改造是纯减法：删掉了每次投递的 `cap()` 调用和一次 clamp 比较，
不新增 DB、Redis、锁或 goroutine。100k RPM 下每请求成本变化为负。

共享资源审计：

| 资源 | 变化 | relay 是否使用 | 影响 |
|---|---|---|---|
| continuation channel | 物理容量固定 100,000（原为启动配置值） | 间接，日志异步 continuation | 常驻内存 1.6 MB；投递仍非阻塞 |
| fallback channel | 物理容量固定 100,000（原为启动配置值） | 间接，日志失败降级 | 常驻内存 1.6 MB；与台账 fallback 队列保持隔离 |
| DB / Redis / 文件 | 无变化 | 使用既有资源 | 无额外连接或同步 I/O |
| worker / goroutine | worker 数不变 | 异步旁路 | 不新增 goroutine，不争抢 relay worker |

GC 影响：多出的 3.2 MB 是含指针的 ring buffer，会进入 GC 扫描。相对进程既有堆量级可忽略，
且为固定量，不随流量增长。

### 21.5 测试

1. getter 边界：`0`/负数回退默认值；`100_000` 原样返回；`100_001` 及更大值钳到 `100_000`。
2. 物理容量：两条 channel 的 `cap()` 等于常量，且不受配置值影响。
3. 热扩容：运行中把软上限从小值调大，超过原值的投递立即被接收（不重启）。
4. 热缩容：调小后立即按新软上限丢弃并计数，行为与改造前一致。
5. `go test ./...`、`bun run typecheck`。

启动期的 `ApplyRelayLogAuxQueueCapacities()` 随本次改造删除：物理容量已是常量，无需在配置
加载后重建 channel，包初始化也不再依赖 `operation_setting` 的读取顺序。

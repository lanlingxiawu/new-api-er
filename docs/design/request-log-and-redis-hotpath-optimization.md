# 请求日志写盘 + relay 路径 Redis 往返优化

> 状态：已确认（2026-09-25，不加功能开关），已实现。本文描述实现后的实际逻辑。

## 1. 目标与范围

2026-09-24 压测定位出网关 CPU 的前两大非业务开销：

| 开销 | 本地（Windows） | 服务器（Linux 2 核） | 说明 |
|---|---|---|---|
| 请求日志写盘 `writeRequestLogFile` | 28–39% | 8.5% | 其中 `open()` 占 64%，写入 32% |
| Redis 往返 | 11–16% | 18% | 每请求同步 5 次 + 异步 4–8 次 |
| 请求体抓取 `readLimited` 大块分配 | 1.1% | 2.9%（含 GC） | 每请求固定分配 64 KB |

请求日志开关 A/B（本地）：开 1.20 ms/请求、关 0.83 ms/请求，吞吐差 27%。

**目标**：请求日志保持全量开启，功能与查询体验不变，写盘开销降一个数量级；relay 路径的 Redis 往返减半。

**范围内**：
- 请求日志：存储格式由"每请求一个文件"改为"按 writer 追加的分段文件"；请求体抓取去掉大块分配。
- Redis：用户缓存读合并为 1 次往返；预扣费复用鉴权时已读到的额度；额度增减改为单条 Lua 脚本。

**范围外**：请求日志的抓取规则（截断、过滤、Content-Type）、管理端 API/前端、索引条数上限（生产约 15 万条，保持不变）、订阅路径（生产未启用）、渠道/模型缓存。

## 2. 现状与问题

### 2.1 请求日志

```
relay goroutine: 抓快照 → 非阻塞投递队列
writer（4 个）: 每条 → Marshal → os.WriteFile(<date>/<ts%8>/<ts>_<reqid>.json) → 进内存索引
sweeper（5 分钟）: 不在索引里的文件 → os.Remove
```

问题：
1. **每请求 1 次 open/create + close + 目录项分配，之后再 1 次 unlink**。文件系统元数据操作是主要成本（Windows 上还要叠加 Defender 扫描），与正文大小无关。
2. 生产 `RequestLogMaxCount` ≈ 15 万条（30k RPM 下约 5 小时，100k RPM 下约 1.5 小时），磁盘上常驻约 15 万个小文件。稳态下写多少就删多少：30k RPM 时每秒约 500 次 create + 500 次 unlink，100k RPM 时各约 1,667 次。
3. sweeper 每 5 分钟对约 15 万个文件做一次 `ReadDir`，并与 15 万条的 keep map 逐一比对。
4. 进程启动恢复快照时，对每条索引各做一次 `os.Stat` 以剔除正文已消失的条目，即约 15 万次 stat。
5. `readLimited` 无论请求体多大都先 `make([]byte, 0, 64KB)`，再 `io.ReadAll` 增长分配，再拷贝一次，最后 `string()` 又拷贝一次。

### 2.2 Redis（非流、钱包计费、缓存命中）

| # | 位置 | 命令 | 同步/异步 |
|---|---|---|---|
| 1 | TokenAuth → `GetTokenByKey` | `HGETALL token:<hmac>` | 同步 |
| 2 | TokenAuth → `GetUserCache` | `HGETALL user:<id>` | 同步 |
| 3 | 同上 → `getUserAuthVersionFloor` | `MGET fence version` | 同步 |
| 4 | 预扣费 `NewBillingSession.tryWallet` → `GetUserQuota` | `HGETALL user:<id>` | 同步 |
| 5 | 同上 | `MGET fence version` | 同步 |
| 6–7 | `DecreaseTokenQuota` → `RedisHIncrBy` | `TTL` + `MULTI HINCRBY EXPIRE EXEC` | 异步 gopool |
| 8–9 | `DecreaseUserQuota` → `RedisHIncrBy` | 同上 | 异步 gopool |

结算差额不为 0 时，6–9 再来一遍。即每请求同步 5 次、异步 4–8 次往返。

另有一个潜在竞态：`RedisHIncrBy` 先 `TTL` 再开事务，两步之间键过期时，`HINCRBY` 会新建一个**只有额度字段**的残缺哈希，事务里的 `EXPIRE` 又给它续上 TTL。用户哈希因 schema 校验失败会回源 DB（无害）；令牌哈希没有校验，`cacheGetTokenByKey` 会把它解析成 `Status=0` 的令牌，直到 TTL 到期。

## 3. 方案

### 3.1 请求日志：分段追加文件（R-LOG-1）

**布局**：`<root>/<YYYY-MM-DD>/seg-<HHMMSS>-<进程标记>-<序号>.jsonl`（`model/request_log_segment.go`）
- 每个 writer（`model.RequestLogWriter`）独占一个"活动分段"，只追加、不改写；每条记录 = 与原单文件完全相同的 JSON + `\n`（JSON 会转义正文里的换行，JSONL 安全）。
- 进程标记 = 进程启动时间（36 进制），序号 = 本进程内分段递增序号；打开用 `O_EXCL`，重名时换下一个序号重试。
- 日期取分段打开时的本地日期；保留日期目录层，跨天、长期停机后的整目录删除逻辑不变。
- 写盘 worker 各持一个 writer；逐条写入的 `RecordRequestLog` 走一个带互斥锁的共享 writer（`InitRequestLogStore` 切换根目录时关闭它）。

**写入**：
1. worker 收到一条后，非阻塞地再从队列取最多 63 条（`RequestLogMaxBatch`=64；不等待，不引入延迟）。用户名补齐与过滤仍逐条进行。
2. 逐条 `Marshal` 进同一块缓冲区，记下每条的 offset/length；缓冲达到 1 MB（`requestLogFlushBytes`）就先写出一段，超大批次不会撑出大缓冲；单次超过 4 MB 的缓冲用完即释放。
3. 一次 `write()` 写入活动分段（`O_APPEND`，无用户态缓冲，写完即可被详情读到）。
4. 写成功 → 一次持锁把这一段的条目追加进索引（"先写盘、后进索引"的顺序不变，索引 ⊆ 磁盘仍成立）。写失败 → 这一段丢弃并限流告警，关闭该分段，下一段换新分段。

**轮转**：活动分段满足任一条件即关闭并新开：跨日期、大小 ≥ 32 MB、打开超过 60 秒、根目录变化。每次开新分段都 `MkdirAll` 日期目录（目录可能刚被清理回收；每分钟一次，开销可忽略），原先的目录创建记忆缓存随之删除。稳态句柄数 = writer 数（默认 4），与现状一致。

**索引**：`rel` 字段编码为 `<date>/<seg 文件名>#<offset>#<length>`；仍是一个字符串，快照结构 `requestLogSnapshotEntry` 不变。

**详情读取**：解析 `rel` → `os.Open` 分段 → `ReadAt(offset, length)` → `Unmarshal`。不含 `#` 的 `rel` 按旧的整文件读取，兼容升级前留下的快照条目。偏移越界（分段被截断）或读到的不是完整 JSON 都返回错误，不返回半条数据。

**回收**：sweeper 规则不变，仍是"无索引引用即删"，只是粒度变为分段。
- 活动分段必须显式保护：Linux 上删掉已打开的文件，后续写入会静默丢失。分段在打开前登记进"分段登记表"（活动集合 + 本进程序号，一把小锁），关闭后注销。
- 清理顺序：**先**在锁内复制活动集合并读取当前序号，**再**取索引快照。保留 = 索引引用的分段 ∪ 活动集合 ∪ 本进程序号大于快照值的分段。
  - 正确性：本进程、序号不大于快照值、又不在活动集合里的分段，必然在快照前已关闭；而分段关闭前它写成功的条目都已进索引，所以索引快照对它是完整的。序号更大的是快照之后才打开的，留到下一轮。
  - 历史进程留下的分段早已关闭，条目随快照恢复进索引（恢复先于清理协程启动），只按索引引用判定。
  - 格式不符的 `seg-*` 文件不是本功能写的，按孤儿删除。
- 活动分段所在日期也计入"在用日期"，写入方长时间空闲时它所在的旧日期目录不会被整目录删除。
- 升级前留下的 `<date>/<bucket>/*.json` 继续按原逐文件规则回收。
- 代价：一个分段只要还有 1 条被索引引用就整体保留。索引按 FIFO 淘汰，多占的磁盘上限约为"writer 数 × 1 个分段"（4 × 32 MB），相对 15 万条正文（约 1.5–3 GB）可以忽略。
- 规模：15 万条保留量下，磁盘上是几千个分段而不是 15 万个文件，sweeper 每轮只需列几千个目录项。

**快照恢复**：`requestLogBodyExists` 按分段缓存文件大小，每个分段只 stat 一次；15 万次 stat 降为几千次，重启恢复更快。分段比记录末尾短（进程崩溃时写了一半）的条目视为正文缺失，不恢复。

**规模实测**（本机 Windows，15 万条、每条正文 2 KB，一次性测量用例，未入库）：

| | 分段追加 | 每条一个文件 |
|---|---|---|
| 写入 15 万条 | 0.46 s（含 JSON 序列化） | 32.1 s（不含序列化） |
| 一轮清理 | 6 ms（列 11 个文件） | 777 ms（列 15 万个文件） |
| 快照恢复存在性检查 | 20 ms（11 次 stat） | 3.4 s（15 万次 stat） |

**顺带收益**：同一分钟的日志集中在少数几个文本文件里，服务器上可以直接 `grep <request_id> relay_log/2026-09-25/*.jsonl` 定位，不依赖管理端。

**预期**：open/close/unlink 从每请求各 1 次降到每分段 1 次（30k RPM、平均 15 KB/条时，每个 writer 约 17 秒写满 32 MB，即全局约每分钟十几次）；`write()` 次数按批合并。服务器 writer 开销 8.5% → 约 2%（剩 Marshal 与数据拷贝），本地 Windows 32% → 5% 以内。实现后用同一套压测复测。

### 3.2 请求日志：请求体抓取去掉大块分配（R-LOG-2）

`captureRequestBody` 不再经 `common.GetBodyStorage` 整读请求体，只读有界前缀：`readBodyPrefix` 以请求声明的 `Content-Length` 为大小提示，按 `min(size, maxBytes) + 1` 精确分配一次（多 1 字节判断截断），循环读入（只有原始流自己的 `io.EOF` 算读完，客户端断开的 `io.ErrUnexpectedEOF` 原样交给下游）；提示偏小时扩容到上限补读，未声明长度时按上限分配，结果只由实际读到的内容决定。读到的前缀由 `requestLogBodyTap` 回放给下游、再接着读原始流，下游看到的仍是完整请求体；总字节数在请求结束时由 tap 的计数给出（读到 EOF 为精确值，下游没读完时取 `Content-Length` 与已读字节数中的较大者）。转字符串仍拷贝一次（不引入 `unsafe`）。完整语义见 `request-log-memory-index-disk-store.md` §3.2。

### 3.3 Redis：用户缓存读合并为 1 次往返（R-REDIS-1）

`cacheGetUserBase` 把 `HGETALL user:<id>` 与 `MGET fence version` 放进一个非事务 pipeline，**顺序不变**（先哈希后版本下限）。哈希解析抽成 `common.DecodeRedisHash`（`RedisHGetObj` 也改为调用它），下限解析抽成 `parseUserAuthVersionFloor`（`getUserAuthVersionFloor` 共用）。

安全性论证：现状两条命令之间本就有客户端往返的空隙，另一客户端的"发布 fence"可以插在中间；pipeline 下两条命令在 Redis 端连续执行，空隙只会更小，"先读快照、后读下限、下限更高则拒绝"的判定不变。

### 3.4 Redis：预扣费复用鉴权快照（R-REDIS-2）

`tryWallet` 目前重新调 `GetUserQuota`；而同一请求在 TokenAuth 阶段已把同一份用户缓存写进上下文，`relayInfo.UserQuota` 就是它。

规则：
- `relayInfo.UserQuota >= preConsumedQuota` 且 `> 0` → 直接用快照，不再读 Redis。
- 否则（可能不足）→ 照旧实时读一次，用实时值判定并给出现有的错误信息。只有余额不足的请求才会多这一次读。

等价性：快照与实时读相隔约 1 ms，且都发生在扣减之前；并发请求在"读额度"与"扣额度"之间改变余额的窗口现状就存在，本改动不扩大它。预扣费在重试循环之外只执行一次，不存在重试时用旧快照的问题。另外两处入口同样满足"本请求首次预扣费"：`tiered_settle.go` 只在 `relayInfo.Billing == nil` 时调用，`relay_task.go` 在任务提交请求内调用。

### 3.5 Redis：额度增减改为单条 Lua（R-REDIS-3）

`RedisHIncrBy` 改为 `redis.NewScript`（EVALSHA，未缓存时自动回退 EVAL）：

```lua
if redis.call('PTTL', KEYS[1]) > 0 then
  return redis.call('HINCRBY', KEYS[1], ARGV[1], ARGV[2])
end
return false
```

- 语义与现状一致：只在键存在且有 TTL 时增减；TTL 不变（现状的 `EXPIRE key <剩余 TTL>` 本来就是空操作）。
- 2 次往返 + 5 条命令 → 1 次往返 + 1 条命令。
- 原子执行，消除 §2.2 的残缺哈希竞态。

### 3.6 汇总

| | 现状 | 优化后 |
|---|---|---|
| 同步 Redis 往返（常规请求） | 5 | 2（令牌 1 + 用户 1） |
| 异步 Redis 往返（每次额度变动） | 4 | 2 |
| 请求日志文件元数据操作 | 每请求 open + close + unlink | 每分段 1 次（约每分钟每 writer 1 次） |
| 每请求固定 64 KB 分配 | 有 | 无 |

## 4. 数据流

```
relay goroutine（不变）: 抓快照（R-LOG-2 少一次大分配）→ 非阻塞投递
writer: 取 1 条 + 非阻塞多取至 63 条 → 批量 Marshal → 1 次 write → 1 次持锁进索引
详情: 索引 rel → 分段 + offset/length → ReadAt
sweeper: keep = 索引引用分段 ∪ 活动分段；其余删除
TokenAuth: HGETALL token → pipeline(HGETALL user, MGET floor) → WriteContext
预扣费: 快照足够 → 不读 Redis；否则实时读
扣减/返还: gopool 内 EVALSHA × 2（token、user）
```

## 5. API 契约

无变化。管理端请求日志的列表/详情接口、返回结构、权限（RootAuth）均不变。

## 6. 数据模型

- DB：无变化。
- Redis：键名、命名空间、TTL 均不变；新增一个 Lua 脚本进入 Redis 脚本缓存（一个 SHA，几十字节）。
- 请求日志快照 `request_log:snapshot`：结构不变，`rel` 的取值多一种编码。

## 7. 配置

不新增配置项。分段轮转阈值（32 MB / 60 秒）与批量上限（64）为代码常量。

是否加功能开关：按既定约定"只有新增功能做开关，修复/性能改动用常量 + 回退二进制回滚"，本方案不加开关，回滚方式为替换回上一版二进制。见 §9 待确认。

## 8. Main Chain Impact

**在 relay goroutine 上同步执行的变化**：
- R-LOG-2：请求体抓取少一次 64 KB 分配和两次拷贝，且只读 `maxBytes+1` 字节的前缀（不再整读请求体），只减不增。
- R-REDIS-1：两条 Redis 命令合进一次往返，命令本身不变。
- R-REDIS-2：常规请求少一次 Redis 往返；余额不足时行为与现状相同。

**异步/后台**：R-LOG-1 全部在 writer 与 sweeper 上；R-REDIS-3 在原有的 gopool 异步任务里。

### 8.1 共享资源审计

| 资源 | 本方案的使用 | relay 是否也用 | 冲突分析 |
|---|---|---|---|
| Redis `token:<hmac>` | 读（不变）、Lua 增减 | 是 | 命令语义不变；Lua 执行时间与单条 HINCRBY 同阶（微秒级），不会阻塞 Redis |
| Redis `user:<id>`、fence/version 键 | pipeline 读、Lua 增减 | 是 | 同上；读顺序不变 |
| Redis 连接池（`REDIS_POOL_SIZE=20`） | 往返减半 | 是 | 占用时间减少，只会缓解池压力 |
| Redis `request_log:snapshot`（RequestLogRDB） | 仅进程启停 | 否 | 不变 |
| 请求日志目录 `relay_log/` | writer 追加、sweeper 删除、详情读 | 否 | 句柄数 = writer 数，不变 |
| 请求日志内存索引 `reqLogMu` | writer 按批持锁一次 | 否（relay 不碰） | 持锁次数下降 |
| 写队列 channel | 不变 | relay 非阻塞投递 | 不变；满即丢弃的降级不变 |
| gopool | 额度增减任务数不变，单任务耗时减半 | 是 | 缓解 |

### 8.2 并发分析（100k RPM ≈ 1,667 rps）

- Redis：同步往返 8,333 → 3,333 次/秒；异步 6,667–13,333 → 3,333–6,667 次/秒；每请求命令数约 25 → 约 6。
- 请求日志：100k RPM、平均 15 KB/条时，写入约 25 MB/s，分段轮转约 0.8 次/秒（全局）；open/unlink 从每秒各 1,667 次降到每秒各约 1 次；`write()` 次数 ≤ 1,667/秒，高负载下按批合并后更少。15 万条保留量下约 1.5 小时、约 2.2 GB，对应约 70 个分段。
- 锁：`reqLogMu` 每批 1 次；不新增锁，不在热路径上加锁。
- goroutine：不新增。

## 9. 待确认

1. **功能开关**：按 §7 不加开关、用二进制回滚，是否同意。

已确认：生产保留约 15 万条（`RequestLogMaxCount`），本方案按这个量级设计与验证（§2.1、§3.1、§8.2、§11）。

## 10. 错误处理

- 分段写失败：整批丢弃，限流 `SysError`（沿用 `reportRequestLogWriteFailure`），关闭该分段并在下一批新开一个，避免反复写同一个坏句柄。
- 分段读失败：详情返回"未找到"，非"文件不存在"类错误记 `SysError`（与现状一致）。
- Lua 脚本失败：返回 error，由现有 gopool 回调 `SysLog`，与现状一致；`NOSCRIPT` 由 go-redis `Script.Run` 自动回退 EVAL。
- pipeline 失败：`cacheGetUserBase` 返回 error → `GetUserCache` 回源 DB，与现状一致。

## 11. 测试设计（先写测试）

**model（请求日志）**：
- 批量写 N 条后逐条详情读取，内容与写入一致；offset/length 边界（首条、末条、空正文、含换行与非 ASCII 的正文）。
- 轮转：跨日期、超过大小阈值、超过时长各触发一次，新旧分段都可读。
- sweeper：索引引用的分段保留；无引用的删除；活动分段即使无引用也保留；ClearAll 后活动分段仍保留、已关闭分段被删。
- 兼容：旧格式 `rel`（整文件）可读、可回收；新旧混合的日期目录正确处理。
- 快照：新 `rel` 编码经快照/恢复往返后可读；分段已被删时恢复会剔除该条。
- 写失败：整批不进索引，且下一批换新分段。
- 并发：多 writer 同时写 + 详情读 + sweeper（`-race`）。
- 规模：15 万条索引下，sweeper 单轮耗时、快照恢复耗时（stat 按分段去重）与现状对比，给出数字。

**middleware**：`readBodyPrefix` 在 size = 0 / 1 / maxBytes / maxBytes+1、大小提示偏大/偏小/未知、读错误下的内容、EOF 判定与分配上界；`captureRequestBody` 在文本/非文本/无请求体下的内容、截断标记与字节数，以及下游仍能读到完整请求体。

**Redis（真实 Redis）**：
- `RedisHIncrBy`：键不存在 → 不创建；有 TTL → 增减且 TTL 不变；无 TTL（-1）→ 不增减（与现状一致）；并发 N 次增减结果精确。
- `cacheGetUserBase`：fence 高于快照 → `ErrUserAuthCachePending`；schema 过期 → 回源；正常命中。
- `tryWallet`：快照足够 → 不读 Redis（计数断言）；快照不足但实时足够 → 通过；两者都不足 → 返回现有错误码与文案。

**回归**：`go test ./...`；复跑 2026-09-24 的压测（三场景计费对账 + 106 项异常矩阵，本地与服务器），A/B 交替顺序，对比 ms/请求与 profile 占比。

测试文件：`common/redis_hincrby_script_test.go`、`model/user_cache_pipeline_test.go`、`service/billing_session_quota_snapshot_test.go`、`model/request_log_segment_test.go`、`middleware/request_logger_test.go`（`readLimited` 大小提示）、`middleware/request_logger_dropfix_test.go`（按批写入）。往返次数用 go-redis hook 计数断言，钱包快照用 GORM 查询回调计数断言。

## 12. 验证结果（2026-09-25）

**本地 A/B**（Windows，同机 MySQL/PG/Redis/模拟上游，并发 50，每场景 1 万请求，新旧交替各 5 轮，取中位数）：

| 场景 | 旧 CPU/请求 | 新 CPU/请求 | 旧吞吐 | 新吞吐 |
|---|---|---|---|---|
| 普通非流式 | 1.19 ms | 0.70 ms（−41%） | ~6,600 rps | ~10,900 rps |
| 流式 | 3.19 ms | 2.73 ms（−14%） | ~2,900 rps | ~3,600 rps |
| 转流 | 1.32 ms | 0.99 ms（−25%） | ~4,900 rps | ~6,600 rps |

Redis 每请求 CPU（非流式）约 0.13 ms → 0.08 ms。新版 profile 中请求日志写盘 32.65% → 2.3%。

旧版每轮都出现 `request log dropped under overload`（每轮约 9,000–15,000 条请求日志被丢弃）：每条一个文件的写法跟不上 1 万 rps 级的写入，队列被打满。新版全程零丢弃。

**计费对账**：三场景 × 多轮全部 18/18（输入、输出、扣费、余额三方一致）；本地与服务器异常矩阵 106/106。

**服务器 profile**（HK 测试服 2 核，非流式并发 8、20 秒，与 2026-09-24 同参数）：Redis 总计 17.9% → 10.1%，其中用户缓存读 9.9% → 3.1%、额度增减 8.1% → 4.9%；请求日志写盘 8.7% → 4.8%（剩余部分 58% 是 JSON 序列化、37% 是 write，已无 open）；GC 5.9% → 2.9%；请求体抓取 2.9% → 1.3%。

**当前剩余热点**（非本次范围）：网络收发系统调用；订阅判定每请求一次 COUNT（生产未启用订阅）；`relay.TextHelper` 里对请求结构体的反射深拷贝 `common.DeepCopy`（本地约 5.4%，上游代码）。

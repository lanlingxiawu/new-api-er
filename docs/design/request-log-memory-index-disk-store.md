# 请求日志改造：内存索引 + 本地磁盘正文 + Redis 快照

> 最近修订：2026-08-10。本文为权威设计，全文一致。历史草案中的“孤儿文件宽限期”“pending/ready 两阶段索引”“清空立即触发扫描”均已废弃，不再出现在本文。

## 0. 设计前提与核心取舍

请求日志（relay 请求的下游请求头/请求体、返回头/返回体）是**尽力而为的排障可观测性数据**，不参与计费与审计，**允许丢失单条**。因此本设计的取舍顺序固定为：

**relay 主链路零影响 > writer 简单/低开销 > 单条数据不丢。**

任何为“保证不丢一条”而增加的锁、状态机或额外 goroutine 都不值得——罕见竞态一律归类为“可接受地丢那一条”。三条由此推导出的关键决策：

1. **先写文件、再单次持锁登记索引**（不做 pending/ready 两阶段）。每条日志只加一次锁，且“索引 ⊆ 磁盘”天然成立，详情几乎不会指向缺失文件。
2. **扫描器无宽限期**：磁盘上无索引对应的文件，扫描到即删除，不看 mtime。
3. **“清除全部请求日志”只清内存索引**，不触发扫描；正文文件由后台定时扫描按“无索引即删”统一回收。

## 1. 目标与范围

把请求日志的存储从 Redis 迁移为：

| 数据 | 存放位置 | 生命周期 |
|---|---|---|
| 列表字段 + 索引字段（request_id 等） | 进程内存 | 进程生命周期内；条数超上限时截断；退出前快照到 Redis |
| 完整正文（请求头/请求体/返回头/返回体 + 全部元字段） | 本地磁盘 JSON 文件 | 写入后常驻，由后台扫描按“内存无索引”删除 |
| 内存索引快照 | Redis 单键 | 仅在进程退出时写、启动时读取并立即删除 |

同时解决两个既有问题：写入路径的文件句柄/连接占用必须与请求量解耦（§5）；`use_time` 字段从未被赋值（§10）。

**范围内**：`model/request_log.go` 存储层重写、`middleware/request_logger.go` 写入路径改为有界 writer 池、启动/退出钩子、后台扫描协程、`use_time` 补齐与前端展示、文案与 i18n、原 Redis 键的一次性清理。

**范围外**：抓取逻辑（截断规则、用户名过滤、Content-Type 判断）、API 路由与权限、消费日志（`logs` 表）与业绩统计——完全不变。

**明确的非目标**：不保证进程崩溃（SIGKILL / 断电）时的数据完整性；不保证每一条都物理落盘（§0 允许丢单条）。

## 2. 现状

- 写入：`middleware.RequestResponseLogger` 同步抓快照 → `enqueueRequestLog` 用容量 1000 的 in-flight 信号量 + `gopool.Go` 做有界异步派发 → `model.RecordRequestLog`。
- 存储：Redis 启用时写 `request_log:seq`（INCR 发号）、`request_log:meta:<id>`、`request_log:body:<id>`、`request_log:index`（LPUSH 列表）；未启用 Redis 时退化为进程内切片。
- 查询：无过滤时对 `index` 分页后 MGET meta；有过滤时 `LRANGE 0 -1` + MGET 全量 meta 后在内存过滤——最坏一次列表请求要从 Redis 拉 5000 条 meta。
- 清理：`trimRequestLogsRedis` 每次写入后检查 `LLEN > max`，删除多余条目的 meta/body 并 `LTRIM` 到 min。

**要解决的问题**：正文（最大 4×64 KB/条 × 5000 条）占用 Redis 内存并与主库/业绩统计争抢连接池；列表带过滤时的 MGET 放大；trim 每次写入都跑一遍 pipeline。

## 3. 数据流

```
relay 请求结束
  └─ middleware（同步，relay goroutine）
       ├─ 计算 use_time_ms（进入中间件时记的 time.Now）
       └─ 非阻塞投递到 requestLogQueue（容量 1000），满则丢弃 + 限流告警
            └─ writer 池（固定 4 个常驻 goroutine），每条日志：
                 ├─ 1. 分配自增 id（进程内 atomic，不再走 Redis INCR）与相对路径 rel
                 ├─ 2. 在锁外写磁盘文件 relay_log/<date>/<ts%8>/<ts>_<request_id>.json（完整 JSON）
                 └─ 3. 写成功 → 单次持锁 append 索引条目并按需淘汰；写失败/序列化失败 → 整条丢弃、不登记索引

管理端列表   → 内存索引过滤 + 分页（零 IO）
管理端详情   → 内存索引按 id 定位 → 读对应磁盘文件 → 返回完整 JSON

进程启动     → 从 Redis 读快照键 → 恢复内存索引 → 删除快照键 → 启动 writer 池与扫描协程
进程退出     → 排空写队列（3 s 预算）→ 内存索引整体 JSON 写入 Redis 快照键（TTL 24h）
每 5 分钟    → 扫描 relay_log → 文件不在内存索引中 → 立即删除；空的历史日期目录整体删除
```

**先写文件、再登记索引**：保证“索引 ⊆ 磁盘”恒成立，详情接口不会拿到一个指向缺失文件的索引。写盘失败则整条丢弃（限流日志），不产生索引。

### 3.1 竞态与显式取舍

| 竞态 | 处理 |
|---|---|
| writer 在“文件已落盘、尚未 append 索引”的微秒窗口内，定时扫描（默认 5 分钟一次）恰好扫到该文件 | **归类为可接受地丢那一条**。命中概率 = 微秒窗口 ÷ 5 分钟周期，极低；不为它付宽限期或 pending 状态代价 |
| 清空与写入交叉：清空把索引置空后，仍在途的 writer 落盘并 append，把自己那条放回空索引 | 该条对应真实文件，不是孤儿，保留即可，无需特殊处理 |
| 阈值淘汰与写入交叉 | 淘汰只删索引不删文件（§6.2）；被淘汰文件作为孤儿等下一轮扫描回收 |
| writer 单条 panic | writer 循环内 `recover()` 兜底，丢弃当前条、不影响后续；因为不存在 pending 半登记态，panic 不会留下幽灵索引条目 |

## 4. 磁盘布局

```
<进程可执行文件所在目录>/relay_log/
  ├─ 2026-08-06/
  │    ├─ 0/  1754500000_20260807061320123456789abcd1234xyz98765.json
  │    ├─ 1/  1754500001_....json
  │    └─ …  7/
  └─ 2026-08-07/
       └─ 0/ … 7/
```

- **根目录**：`filepath.Dir(os.Executable())` + `/relay_log`。允许环境变量 `REQUEST_LOG_DIR` 覆盖（部署级配置，按 Rule 12 属 env 而非系统设置）；容器里可挂到独立卷。`os.Executable()` 失败时回退当前工作目录。
- **第一层：日期**。`time.Unix(created_at, 0).Format("2006-01-02")`，用服务器本地时区——与项目既有的兜底文件（`model/business_stats_dead_letter.go`）、签到日期保持一致，也与运维手工翻目录的直觉一致。夏令时/时区调整只影响某条日志落进哪个目录，不影响正确性：索引里存的是完整相对路径，不做反向推导。
- **第二层：`created_at % 8`**，即 `0`–`7` 八个桶。秒级时间戳逐秒轮转，天然均匀，无热点。
- **目录创建**：写入前按需 `MkdirAll`，用一个最多 64 项的已创建路径集合（`RWMutex` 保护）避免每条日志一次 syscall；超过 64 项时整体清空重建（跨天时自然发生）。写入若因目录不存在而失败（清理协程回收了该目录，跨天前后可能发生），作废该条记忆、重建目录后重试一次——否则该 `<date>/<bucket>` 的后续写入会一直失败。
- **文件名**：`<created_at>_<sanitized_request_id>.json`。request id 由 `common.NewRequestId()` 生成（14 位时间 + 9 位纳秒 + 8 位 hex + 8 位随机，全字母数字），仍做净化：只保留 `[A-Za-z0-9._-]`，其余替换 `_`，超 128 字符截断；为空时用 `noreqid-<id>` 兜底。净化是防路径穿越的硬性要求——不能仅凭“生成规则是安全的”这一约定排除 request id 被外部影响的可能。
- **文件内容**：一条完整 `RequestLog` 的 JSON（含大字段），`common.Marshal` 产出，一次写入（`0644`）。不调用 `fsync`——崩溃丢失可接受（§1 非目标）。
- **容量上限**：单文件受 `RequestLogMaxBodyKB` 约束，四个大字段各自截断，≈ 4×64 KB + 元数据 ≈ 260 KB 上限。稳态文件数由扫描协程压到 `MaxCount` 量级，磁盘占用上限 ≈ `MaxCount × 260 KB`（默认 ≈ 1.3 GB，实际远小于此，多数请求体只有几 KB）。设置页文案需说明这一点。

## 5. 写入路径：有界 writer 池（句柄与连接约束）

现实现是“1000 个 in-flight 令牌 + 每条一个 `gopool.Go`”。改成磁盘存储后这个模型不能留：`os.WriteFile` 会 open/close 一个 fd，1000 并发就是峰值 1000 个 fd，叠加 relay 自身的上游连接后很容易撞到 `ulimit -n`；同时 1000 个瞬时 goroutine 也没有必要。

改为**固定容量的写队列 + 固定数量的常驻 writer**：

```go
requestLogQueue = make(chan *model.RequestLog, queueSize) // 默认 1000
// 固定 W 个 writer goroutine，for entry := range requestLogQueue { ... }
```

| 维度 | 改造前 | 改造后 |
|---|---|---|
| 峰值文件句柄 | 最坏 1000（与并发同阶） | **= writer 数（默认 4），与 RPM 完全解耦** |
| goroutine | 最坏 1000 个瞬时 | 4 个常驻 |
| Redis 连接占用 | 每条日志 4 条命令，压 `RequestLogRDB` 连接池（默认 10） | **稳态零**，仅进程首尾各一次 |
| DB 连接 | 0 | 0 |
| 内存驻留上限 | in-flight 上限 × 单条大小 | （队列深度 + writer 数）× 单条大小，同阶 |
| relay goroutine 开销 | O(1) 非阻塞派发 | O(1) 非阻塞 channel 发送（更轻，无 gopool 调度） |

- 投递仍是 `select { case ch <- entry: default: drop }`，队列满即丢弃并按 1/1000 频率打印告警——沿用既有的优雅降级语义（Rule 8），不新增任何阻塞点。
- **未完成量记账**：`pending` 计数在**投递成功前** +1、worker 处理完 -1，被丢弃时回退。不能改成“worker 收到任务后再 +1”——出队与自增之间存在空隙，关停排空会在那一刻看到“队列空且无在途”而提前返回，导致最后几条来不及进索引就被快照落下。实现期间的测试正是先按后者写、被这个竞态打出来的。（注：此处 `pending` 指关停排空用的**在途计数**，与索引条目无关；索引没有 pending 状态。）
- `REQUEST_LOG_MAX_INFLIGHT` 保留为**队列深度**的环境变量名（默认 1000），避免破坏已有部署的调参习惯；新增 `REQUEST_LOG_WRITERS`（默认 4，取值收敛到 1–32）。
- writer 数取 4 的依据：单条写入是一次小文件顺序写，4 路并发足以打满常规 SSD 的小文件写入能力，再高只会增加 fd 与 IO 调度竞争。需要更高吞吐时调大，同时按同样倍数评估 fd 预算。
- **单条处理用 `defer`+`recover` 兜底**：写盘或 marshal panic 时只丢弃当前条、记限流错误，不退出 writer goroutine，也不残留任何索引条目。
- **Redis 连接**：`RequestLogRDB` 现在整个进程生命周期只用到 3 次（启动 GET+DEL、退出 SET，加一次性旧键清理）。当配置了 `REQUEST_LOG_REDIS_DB` 从而创建独立客户端时，把它的 `PoolSize` 由 10 降到 2、`MinIdleConns` 置 0，相比现在少占 8 条 Redis 连接。未配置独立库时 `RequestLogRDB` 仍指向 `RDB`，**不得**改动主客户端的池参数。

## 6. 内存索引

### 6.1 结构

索引条目 = 现有 `RequestLog` 去掉四个大字段，再加一个不导出到 API 的磁盘相对路径：

```go
type requestLogIndexEntry struct {
    meta *RequestLog // 大字段恒为空
    rel  string      // "2026-08-07/3/1754500000_<rid>.json"，相对 relay_log 根目录
}
```

索引条目**只有一种状态**（不区分 pending/ready）：它一旦进入 `reqLogItems`，对应文件必已落盘。存 `rel` 而不是每次由 `created_at`/`request_id` 重新推导，是为了让“索引 ↔ 文件”的对应关系只有一处真值：净化规则或目录分层将来再调整，也不会让老条目失联。

### 6.2 容器与淘汰

用**尾部追加**的切片替换现有的头部插入：

```go
var (
    reqLogMu    sync.Mutex
    reqLogItems []requestLogIndexEntry // 尾部为最新
    reqLogSeq   int64                  // 进程内自增 id
)
```

现实现每条日志都执行 `append([]*RequestLog{log}, memRequestLogs...)`，即每次插入复制整个 5000 元素切片。改为尾部 `append` 后插入是 O(1) 摊还；淘汰在 `len > max` 时执行一次 `copy(items, items[len-min:])` + 截断，即每 `max-min` 次插入才付一次 O(min) 拷贝。读取端倒序遍历得到“最新在前”。

淘汰阈值沿用现有 `effectiveRequestLogLimits()`（保证 `0 <= min < max`，配置错配时退化为 `max/2`，杜绝“永不清理”）。**淘汰只删索引，不碰磁盘文件**——磁盘回收统一交给 §8 的扫描协程，写入路径因此不含任何删除 IO；被淘汰文件作为孤儿等下一轮周期扫描删除。

### 6.3 查询与清理

- 列表：持锁倒序遍历索引，边过滤边计数，只把落在当前页窗口内的条目物化出来，释放锁后直接返回。过滤条件与 `matchRequestLog` 完全一致，不变。
  单次分配从“与索引等大”降到“一页大小”：`total` 要求必须走完全表，但整表复制会让每次翻页都产生一次与索引等大的分配（`MaxCount` 调大后尤其明显）。这把锁只与写盘 worker 竞争，relay goroutine 从不参与，扫描期间短暂持有可以接受。
- 详情：按 id 倒序线性查找（管理员低频操作，5000 条 < 50 µs），拿到 `rel` 后读文件、`common.Unmarshal` 返回。文件缺失时返回 `errRequestLogNotFound`。
- `DeleteOldRequestLog(ts)`：删除 `created_at < ts` 的索引条目并返回条数；文件由扫描协程回收。
- `ClearAllRequestLogs()`：**只清空内存索引并返回条数**。不触发扫描、不做任何磁盘 IO；对应正文文件在下一轮定时扫描时按“无索引即删”回收。实现仅取旧切片长度并置空索引，是 O(1) 内存操作，对接口延迟与性能最友好。

### 6.4 内存占用

单条 meta ≈ 400 B（含 username/model/url/ip/request_id 等字符串），5000 条 ≈ 2 MB，常驻。相比之下旧方案把正文压在 Redis 上。

## 7. Redis 快照

只保留一个键，仍走 `common.RequestLogRDB`：

| 键 | 类型 | 写入时机 | 读取时机 |
|---|---|---|---|
| `request_log:snapshot` | string（JSON 数组） | 进程退出 | 进程启动，读后立即 `DEL` |

- **退出**：`main.go` 关停序列在 `ShutdownRelayLogFlush` 之后追加两步——先 `middleware.DrainRequestLogQueue(3 * time.Second)` 排空写队列（让在途条目落盘并进索引），再 `model.SnapshotRequestLogs()` 把整个索引（含 `rel`）序列化为一个 JSON 数组 `SET ... EX 86400`。TTL 防止“进程再没起来”时快照永久驻留。两步各自设超时、失败只记日志，不阻塞退出。
- **启动**：`main.go` 在 `InitRedisClient` 之后、HTTP 端口绑定之前调用 `model.RestoreRequestLogs()`：`GET` → `Unmarshal` → 装入索引（超过 `max` 时只保留最新 `max` 条）→ `DEL`。恢复条目里 `rel` 对应文件已不存在的直接跳过，避免详情 404。读取设 5 秒超时，失败视为无快照。**必须在扫描协程启动之前完成**，否则第一次扫描会把有效文件当孤儿删掉。
- **Redis 未启用**：快照与恢复都是 no-op。重启后索引为空，磁盘上的旧文件将在下一轮扫描时因“无索引”被删除——这正是需求语义，需在设置页文案说明“未启用 Redis 时重启会丢失请求日志”。
- **单键大小**：5000 条 × ~400 B ≈ 2 MB，一次 SET/GET。实现中对快照条数额外加 `min(len, max)` 上限，避免 `MaxCount` 被配置成极大值时单键无限膨胀。

**旧键一次性清理**：升级后 `request_log:seq` / `request_log:index` / `request_log:meta:*` / `request_log:body:*` 成为死数据。启动后由一个后台 goroutine（延迟 30 秒、`recover()` 兜底、`SCAN COUNT 100` 分批 `DEL`）清理一次，不阻塞启动，不影响 relay。

## 8. 磁盘扫描协程

进程启动（且索引恢复完成）后启动一个 goroutine，`time.Ticker` 每 `REQUEST_LOG_SWEEP_INTERVAL_SEC` 秒（默认 300）触发一次。扫描器**无宽限期**：磁盘上无索引对应的文件，扫描到即删除，不读 `ModTime()`。

1. 持锁构建当前索引中所有 `rel` 的 `map[string]struct{}`（keep set，5000 条约 200 KB，用完即弃），同时取出索引中最早的日期。
2. `ReadDir` 根目录得到日期层。**日期目录名可解析且早于 `今天-1` 天、且索引中没有任何该日期前缀的条目** → 直接 `os.RemoveAll` 整个日期目录，不逐文件读项。这是长期停机后重启（磁盘上堆积远超 `MaxCount` 个文件）的关键优化：整目录删除把逐项比对开销一次性省掉。安全性来自“索引里没有该日期的任何条目 ⇒ 该目录内没有任何在用文件”，与 mtime 无关。
3. 其余日期目录逐桶（`0`–`7`）用 `dir.ReadDir(1000)` **分批**读取目录项，拼成 `<date>/<bucket>/<name>` 与 keep set 比对。
4. **不在 keep set 中的文件立即删除**（无索引即孤儿，不看 mtime）。§3.1 已说明：唯一被此规则误删的是“已落盘、尚未 append 索引”窗口内的在途文件，属于可接受地丢单条。
5. 名字不符合 `<数字>_<id>.json` 的文件、以及根目录下不符合日期格式的条目，同样按孤儿立即删除（本目录归本功能独占，独占性由 §8.1 的归属校验保证）。根目录下的归属标记文件 `.new-api-request-log` 是唯一豁免项——删掉它下次启动就无法再证明目录独占。
6. 处理完一个日期目录后，若其下桶目录已空且日期早于今天，`os.Remove` 掉空目录，避免空壳目录无限累积。
7. 每轮存在删除、目录回收或错误时记一条汇总日志（扫描文件数、删除数、整目录删除数、耗时），无变化时不刷日志；单文件删除失败只累计计数，不中断本轮。整个循环 `recover()` 兜底。

**触发方式**：仅由 `time.Ticker` 周期驱动。清空全部（§6.3）与阈值淘汰（§6.2）都**只删索引、不主动触发扫描**，产生的孤儿文件统一等下一个扫描周期回收。清空按钮的语义因此是“立即清空列表，正文文件在后台下一轮扫描时删除”。

**`INTERVAL=0` 关闭扫描**：ticker 不启动，孤儿文件（含清空/淘汰产生的）不会被回收，磁盘会累积。这是明确取舍——不启用扫描就不承诺物理删除；关闭清理即用 `INTERVAL=0` 表达。设置页文案需说明。扫描期间同时最多打开 1 个目录句柄。

### 8.1 根目录归属校验

第 2、5 步会 `RemoveAll` 整个日期目录、并删除根目录下一切不属于本布局的内容。这个前提只在根目录确实由本功能独占时成立，而 `REQUEST_LOG_DIR` 是自由填写的部署级配置——指向 `/var/log` 或数据盘根目录就会误删无关内容。

因此 `InitRequestLogStore` 在 `MkdirAll` 之后先认领目录：

- 根目录下存在 `.new-api-request-log` 标记文件 → 已认领，正常启用；
- 没有标记，但目录为空或只含 `YYYY-MM-DD` 日期目录（旧版本升级路径）→ 写入标记并启用；
- 否则判定为非独占目录：**整个请求日志功能不启用**（`RequestLogStoreReady()` 返回 false），记 `SysError` 提示改用专用空目录，不删除也不写入任何文件。

选择整体停用而不是“照常写入但关闭清理”：后者会让磁盘无界增长，两种降级都不可接受，让部署方修正配置才是正解。默认路径 `<exeDir>/relay_log` 不受影响。

## 9. 主链路影响（Rule 0）

### 9.1 同步 vs 异步

| 阶段 | 执行位置 | 变化 |
|---|---|---|
| 开关关闭时直接放行 | relay goroutine | 不变（零开销，早于任何计时） |
| 记录起始时间 `time.Now()` | relay goroutine | **新增**，仅在开关开启时，纳秒级 |
| 抓请求头/体、包装 ResponseWriter、抓返回体 | relay goroutine | 不变 |
| 组装 `RequestLog` + 计算 `use_time_ms` + 投递队列 | relay goroutine | 由 gopool 派发改为非阻塞 channel 发送，更轻 |
| 序列化 + 写磁盘文件 | writer 池（4 个常驻 goroutine） | **新增**，替代原 Redis Incr + 3 条 pipeline 命令 |
| 单次持锁 append 索引 + 按需淘汰 | writer 池 | 新增，一次加锁，O(1) 摊还 |
| Redis 写 | — | **移除** |

relay goroutine 上的工作量没有增加（多一次 `time.Now()`，少一次 gopool 调度）；实际变化全部发生在异步 writer 内部。

### 9.2 共享资源审计

| 资源 | 本功能用法 | relay 主链路是否触碰 | 结论 |
|---|---|---|---|
| 磁盘目录 `<exeDir>/relay_log` | 独占读写 | 否 | 隔离。与 `*common.LogDir`（默认 `./logs`，运行日志）、业绩统计兜底文件（`logs/fallback`）均为不同目录 |
| **文件句柄** | 写入峰值 = writer 数（4），扫描期 1 个目录句柄 | relay 大量占用上游连接 fd | **与 RPM 解耦**，恒定 ≤5，见 §5 |
| Redis 键 `request_log:snapshot` | 启动读+删、退出写 | 否 | 隔离，稳态零流量 |
| **Redis 连接** | 独立客户端池由 10 降至 2；未配独立库时复用 `RDB` 且不改其池 | relay 读缓存走 `RDB` | 净释放 8 条连接 |
| Redis 旧键 `request_log:*` | 启动后一次性删除 | 否 | 一次性 |
| 内存 `reqLogItems` + `reqLogMu` | writer 单次 append、管理端读/清理 | 否（relay goroutine 从不持这把锁） | 隔离 |
| goroutine | 4 个 writer + 1 个 ticker，全常驻 | 共享 gopool | 不再向 gopool 提交任务，反而减少竞争 |
| DB 连接池 | 不使用 | — | 无影响 |

### 9.3 并发分析（100k RPM）

100k RPM = 1667 请求/秒。请求日志默认关闭；即使全量开启且不设用户名过滤：

- **relay goroutine**：新增 0 次 DB、0 次 Redis、0 次磁盘 IO、0 次加锁；仅一次 `time.Now()` 与一次非阻塞 channel 发送。
- **writer 池**：1667 次/秒 `WriteFile`，平均 8 KB/条（典型 relay 请求体+响应体），约 13 MB/s 顺序写，由 4 个 writer 承担。SSD 可承受；机械盘或网络盘（NFS/EFS）不可承受——文档明确要求 `relay_log` 落本地 SSD，或用用户名过滤把量级压到个位数 QPS。
- **背压**：写盘变慢时队列（1000）迅速打满，超出部分丢弃并按 1/1000 频率告警。这是既有的优雅降级机制，不新增阻塞点。
- **锁竞争**：`reqLogMu` 每条日志**一次** O(1) 操作，1667 次/秒下最多 4 个 writer 争用；管理端列表持锁只做一次页大小切片复制，清空只做一次置空。
- **磁盘容量**：稳态文件数受 `MaxCount` 约束（扫描协程回收），不随 RPM 增长。峰值上限 ≈ `MaxCount × 单文件上限` 加上一个扫描周期（默认 5 min）内的新增量与淘汰/清空尚未回收的孤儿量。

## 10. `use_time` 补齐

现状：`RequestLog.UseTime` 与前端 `RequestLogItem.use_time` 都存在，但 middleware 从未赋值，恒为 0，列表也没有对应列——是个纯粹的死字段。

- **测量点**：`RequestResponseLogger` 在开关判断之后、`c.Next()` 之前记 `time.Now()`，在 `c.Next()` 返回后算差值。该中间件挂在所有 relay 路由的最前面（早于 `TokenAuth` / `Distribute`，见 `router/relay-router.go`），因此覆盖鉴权、渠道分发、上游调用、流式输出的完整端到端耗时；流式请求的耗时自然包含整个流的持续时间。
- **不复用** `constant.ContextKeyRequestStartTime`：它由 `middleware/distributor.go` 设置，在鉴权失败等未走到分发的请求上不存在，且不含中间件自身之前的开销。自测起点更准也更自足。
- **单位与字段名**：改为毫秒，字段重命名为 `use_time_ms`（Go 侧 `UseTimeMs int64`，JSON `use_time_ms`）。消费日志（`logs` 表）的 `use_time` 是**秒**，同名不同单位是明确的踩坑源；该字段当前无任何读取方，重命名零兼容成本。
- **展示**：前端列表在 `Status` 列后新增“耗时”列，`< 1000 ms` 显示 `123 ms`，否则显示 `1.23 s`；详情弹窗同步展示。

## 11. 配置

复用现有五项系统设置（`operations.request-log` 权限域，语义不变）：`RequestLogEnabled`、`RequestLogUsername`、`RequestLogMaxBodyKB`、`RequestLogMinCount`、`RequestLogMaxCount`。

环境变量（部署级，不进系统设置；全部由 `InitRequestLogStore()` 在 `godotenv.Load` 之后读取，不在包级变量里求值）：

| 变量 | 默认 | 说明 |
|---|---|---|
| `REQUEST_LOG_DIR` | `<exeDir>/relay_log` | 正文文件根目录；必须是本功能专用目录，非独占时整个功能不启用（见 §8.1） |
| `REQUEST_LOG_WRITERS` | `4` | 写盘 worker 数（1–32）；等于峰值文件句柄数 |
| `REQUEST_LOG_MAX_INFLIGHT` | `1000` | 语义由“in-flight 上限”改为“写队列深度”，名称保留 |
| `REQUEST_LOG_SWEEP_INTERVAL_SEC` | `300` | 扫描周期；`0` 关闭扫描（孤儿文件不回收，见 §8） |
| `REQUEST_LOG_REDIS_DB` | 已存在 | 现在只影响快照键所在库；配置后该客户端池降至 2 |

> `REQUEST_LOG_SWEEP_GRACE_SEC` 已**移除**：扫描器无宽限期。旧部署若仍设置该变量，将被忽略，不影响启动。

`.env.example` 同步补充（删除 `REQUEST_LOG_SWEEP_GRACE_SEC` 行）。

## 12. 错误处理（Rule 9/10）

| 场景 | 处理 |
|---|---|
| 根目录创建失败/非独占（启动时） | `common.SysError` 记录并把功能整体标记为不可用，投递路径直接返回。不降级为“只存索引不存正文”——那会产生点不到正文的索引条目，比没有日志更误导 |
| 单条写盘失败 / 序列化失败 / 单条 panic | 不插索引，`recover()` 兜底，按 1/1000 频率 `common.SysError` 限流打印（与丢弃告警一致），不影响响应、不退出 writer |
| 详情读文件失败/文件缺失 | 返回 `errRequestLogNotFound`，控制器 `common.ApiErrorI18n` → HTTP 200 + `success:false`；前端提示“该日志正文已被清理” |
| 快照读写 Redis 失败 | `common.SysLog` 记录并继续，不阻塞启动/退出 |
| 退出时队列未排空 | 超时后丢弃剩余条目并记条数，继续做快照 |
| 扫描协程 panic | `recover()` 后记录，等待下个周期 |

系统级（非请求作用域）日志用 `common.SysLog` / `common.SysError`。

## 13. 前端与 i18n

1. **新增耗时列**（§10），`types.ts` 的 `use_time` 改为 `use_time_ms`。
2. **文案修正**（4 处，均因存储介质变化）：

| 位置 | 现文案 | 改为 |
|---|---|---|
| `Enable Request Log` 描述 | “…显著增加数据库写入” | “…每条请求会在本机 relay_log 目录写入一个 JSON 文件” |
| `Min Log Count` 描述 | “…（日志仅存储在 Redis 中）” | “…（列表索引存于内存，正文存于本机 relay_log 目录；未启用 Redis 时重启后索引丢失）” |
| `Clear all request logs` 描述 | “移除 Redis 中存储的全部请求日志” | “立即清空内存中的请求日志列表；对应正文文件将在后台定时扫描时删除” |
| 清空确认弹窗描述 | 同上 | 同上 |

`web/src/i18n/locales/{en,zh,zh-TW,fr,ru,ja,vi}.json` 直接补新 key、移除旧 key（按 Rule 6 不跑 `i18n:sync`，避免回填无关键）。

## 14. 测试计划（Rule 15）

先写用例再实现。新增 `model/request_log_disk_test.go`、`model/request_log_sweep_test.go`，改写 `model/request_log_test.go`；不依赖数据库，用 `t.TempDir()` 作日志根目录，Redis 沿用 `zz_harness_test.go` 既有路径。

**路径推导与净化**（等价类 + 边界）
- `created_at % 8` 的 8 个取值各产出对应桶；日期层按本地时区正确切分（构造跨日的两个时间戳，断言落入不同日期目录）
- `created_at = 0`；负数时间戳归一化到 `0` 桶与合法日期
- request id 含 `../`、`/`、`\`、空格、超长（>128）、空串 → 净化结果不含分隔符、长度受限、空串走 `noreqid-<id>` 分支
- 净化后的文件名与 `rel` 字段严格一致（同一真值）

**写入与索引**
- 写盘成功 → 文件存在、内容可反序列化回原结构（含 `use_time_ms`）、单次 append 后索引 +1、索引条目大字段为空
- 写盘失败（根目录被替换成文件 / 只读）→ 索引不变、无残留
- 序列化失败 / 单条 panic → 索引不变、writer 不退出、后续条目正常写入
- 目录创建缓存：连续写入同一 `<date>/<bucket>` 只 `MkdirAll` 一次；超过 64 项后清空重建仍能正确写入

**writer 池与句柄约束**
- 队列满 → 投递返回丢弃、丢弃计数 +1、不阻塞调用方（用无 writer 消费的场景构造）
- `REQUEST_LOG_WRITERS` 取 0/负数/超过 32 → 收敛到 1/1/32
- 排空：`DrainRequestLogQueue` 在预算内把队列清空且条目全部落盘；超预算时返回剩余条数且不 panic
- 并发 500 条投递 + 4 writer：`-race` 无竞态，全部落盘且索引条数正确

**淘汰边界**（边界值）
- `max-1`、`max`、`max+1` 条：仅 `max+1` 触发截断且结果恰为 `min` 条，保留最新的
- `min=0`：截断到空
- 错配 `min >= max`、`max <= 0`：退化为 `max/2`，截断仍发生
- 淘汰后磁盘文件数量不变（明确断言“只删索引不删文件”）

**查询与清理**
- 六个过滤条件各自命中/不命中及组合过滤
- 分页：`startIdx` 越界、`num <= 0`、跨页边界、中间页窗口取值正确、`total` 与过滤后条数一致（而非当前页条数）
- 排序：结果严格按 `created_at` 倒序（尾部追加 + 倒序遍历的回归锁）
- 详情：命中返回大字段；文件被删后返回 `errRequestLogNotFound`
- `ClearAllRequestLogs`：只清索引、返回正确条数、**不删磁盘文件**（断言清空后文件仍在，随后一轮扫描才删除）

**扫描**（决策覆盖）
- 有索引的文件保留；**无索引的文件不论 mtime 新旧都立即删除**（无宽限期）
- 非法文件名、根目录下非日期条目 → 立即删除
- 归属标记文件 `.new-api-request-log` 永不删除
- 根目录归属：含无关内容 → 功能不启用且无关文件不被触碰；仅含日期目录 → 认领并补写标记
- 清空后的下一轮扫描把当时无索引文件全部删除；清空瞬间在途 writer 落盘后 append 的那条对应真实文件，保留、不被误删
- `INTERVAL=0` → ticker 不启动，孤儿文件不被回收（断言此取舍）
- 早于 `今天-1` 且索引中无该日期条目的日期目录 → 整目录 `RemoveAll`，且**不**逐项比对（用只读文件构造“逐个删会失败”的场景来锁住走的是 `RemoveAll` 分支）
- 早于今天但索引中仍有该日期条目 → 逐文件判定，索引内文件保留
- 空桶目录/空日期目录被回收；根目录不存在、空目录 → 不 panic
- 单桶 >1000 个文件 → 分批读取全部覆盖
- 扫描与写入并发 → 无 panic，索引内文件不被误删（允许扫描撞上未登记窗口丢单条）

**快照**
- 保存 → 恢复 → 键被删除；恢复后列表/详情可用
- 快照中 `rel` 指向的文件已不存在 → 该条被跳过
- 快照条数 > `max` → 只恢复最新 `max` 条
- Redis 未启用 → 保存与恢复均为 no-op 且不报错

**中间件回归**：`middleware/request_logger_test.go` 现有用例（开关关闭、用户名过滤、丢弃计数）保持通过，断言目标从 Redis 键改为磁盘文件；新增 `use_time_ms > 0` 且覆盖 `c.Next()` 全过程的用例（在 handler 里 sleep 一小段后断言下界）。

`go test ./...` 全绿 + `bun run typecheck` 通过方可视为完成（Rule 15.8）。

## 15. 实现落点

| 文件 | 内容 |
|---|---|
| `model/request_log_store.go` | 环境变量解析（`InitRequestLogStore`）、日期+桶路径推导、request id 净化、磁盘读写、目录记忆与自愈重试 |
| `model/request_log.go` | 内存索引容器（尾部追加 + 淘汰）、`RecordRequestLog`（写盘 → 单次 append 索引）、查询/详情/按时间删除/清空 |
| `model/request_log_snapshot.go` | 快照保存/恢复、遗留 Redis 键一次性清理 |
| `model/request_log_sweep.go` | 清理协程、`SweepRequestLogFiles`（无宽限期）、整目录删除与空目录回收 |
| `middleware/request_logger.go` | 队列 + writer 池、`DrainRequestLogQueue`、单条 `defer`/`recover`、`use_time_ms` 计时、用户名异步解析、头部截断 |
| `main.go` | 启动 `InitRequestLogStore → RestoreRequestLogs → StartRequestLogWriters → StartRequestLogSweeper → StartLegacyRequestLogCleanup`；关停 `DrainRequestLogQueue(3s) → SnapshotRequestLogs` |
| `controller/request_log.go`、`i18n/` | `ApiErrorI18n` + `request_log.not_found` / `request_log.timestamp_required` 双语 |
| `web/src/features/request-logs/`、`.../maintenance/request-log-settings-section.tsx` | 耗时列、`success:false` 判定、错误态展示、文案与 7 语言 i18n |

`REDIS_POOL_SIZE` 对请求日志独立客户端的下调尚未落地——`common.InitRedisClient` 里该客户端与主客户端共用同一处池参数，单独下调需要拆分该函数，收益（省 8 条连接）不足以在本次改动里动 Redis 初始化。稳态零流量已经使这条连接基本闲置。

## 16. 已确认的决策

1. `ClearAllRequestLogs` **只清空内存索引**，不触发扫描；正文文件由后台定时扫描按“无索引即删”回收（§6.3、§8）。清空是 O(1) 纯内存操作、零磁盘 IO。
2. 扫描器**无宽限期**：无索引文件扫描到即删除。为此接受“扫描撞上‘已落盘未登记’微秒窗口丢单条”的罕见竞态（§3.1）——请求日志允许丢单条（§0）。
3. writer **先写文件、再单次持锁 append 索引**，索引条目不分状态（无 pending/ready），每条日志一次加锁。
4. 磁盘**加日期目录**层：`relay_log/<YYYY-MM-DD>/<ts%8>/`。除按需求分桶外，还让长期停机后的清理可以整目录删除，并支持将来按天做保留策略。
5. `use_time` **补上**，改为毫秒并重命名 `use_time_ms`，前端新增耗时列。
6. 文件句柄与连接数与请求量解耦：固定 4 个 writer（峰值 fd = 4），Redis 稳态零流量、独立客户端池由 10 降至 2。
7. **请求/返回头不做脱敏**（见 §17）。
8. **新增环境变量一律不在包级变量里读**（见 §11）。

## 17. 安全边界：头部不脱敏

请求头按原样记录，**不对 `Authorization` / `X-Api-Key` / `Cookie` 等做脱敏或掩码**——这是明确的取舍，不是疏漏。

由此产生的事实，部署方必须知晓：

- 上游 API key、客户端令牌、playground 的后台 JWT 会以**明文**出现在 `relay_log/` 下的 JSON 文件里，以及请求日志详情弹窗里（可一键复制）。
- 正文文件权限为 `0644`，会随备份、镜像、宿主机日志采集一并扩散；相比原先存 Redis（内存、可整体 flush），持久化后暴露面更大。
- 读取入口受 `admin_menu.request_logs:view` 管控，但任何拿到该权限的管理员即可读取全量令牌。

因此运维约束是：**请求日志是临时排障开关，不应长期开启**；开启时优先配 `RequestLogUsername` 缩小范围；`relay_log/` 目录应与备份/采集路径隔离。这些写进设置页文案。

## 18. 本次一并修复的既有缺陷

审计（2026-08-07）在现有实现中确认的问题，在本次改造中一并处理：

| 问题 | 处理方式 |
|---|---|
| `DeleteOldRequestLog` 用 `LREM count=0` 逐 id 全表扫描，删 4000 条 ≈ 2000 万次元素比较，单次 `Exec` 阻塞 Redis 单线程，拖慢 relay 热路径的缓存读 | 随 Redis 存储移除而消失 |
| `ClearAllRequestLogs` 的 `SCAN MATCH` 在默认配置（`RequestLogRDB == RDB`）下遍历**主库全键空间** | 同上；改为纯内存清空 |
| `trimRequestLogsRedis` 先 `LRANGE` 采样、后 `LTRIM`，两步间的并发 `LPUSH` 使被挤出窗口的条目 meta/body 永不删除，形成无 TTL 孤儿键 | 同上 |
| `.env` 中的 `REQUEST_LOG_MAX_INFLIGHT` 静默失效：包级变量在 Go 包初始化期求值，早于 `main() → InitResources() → godotenv.Load(".env")` | 全部请求日志环境变量改由 `InitRequestLogStore()` 读取，该函数在 `InitResources()` 内、`godotenv.Load` 之后调用；队列与 writer 池改为在此刻构建 |
| panic 的请求不产生日志：`c.Next()` 后是顺序代码而非 `defer`，栈展开直接跳过 | 记录逻辑整体移入 `defer`，panic 请求同样落日志（状态码为 500） |
| `matchRequestLogUsername` 在 relay goroutine 上同步调 `GetUserCache`，缓存未命中即一次 DB 查询 | 同步路径只用 context 内已有的 username 做判定；仅“context 无 username 但有 userId”这一少数分支把解析推迟到 writer goroutine |
| 清空条数在 Redis 模式下统计的是**键数**（meta+body+孤儿）而非条目数，UI 显示约 2 倍 | 统一返回索引条目数 |
| 头部不受 `RequestLogMaxBodyKB` 限制，但 UI 文案与代码注释都声称受限 | 头部同样按该上限截断并标记 `...[truncated]`，注释与文案同步 |
| 前端 `api.ts` 从不检查 `success`，后端 `success:false`（HTTP 200）时列表显示“暂无日志”、详情显示四个空白面板 | 两个请求函数在 `success === false` 时抛错，交给 `useQuery` 的 `isError` 分支 |
| 详情查不到时把 `redis: nil` 原样作为 message 返回客户端 | 改为 `errRequestLogNotFound` → i18n 文案 |
| 控制器手写 `c.JSON` 且未 i18n | 改用 `common.ApiErrorI18n` |

实现期间新发现并修复的一条：**关停排空的竞态**（见 §5 在途计数）。写队列的未完成量若由 worker 在出队后自增，排空会在“已出队、尚未记账”的空隙里误判为已完成，最后几条日志来不及进索引就被快照落下。改为投递侧记账。

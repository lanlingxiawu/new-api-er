# 通用日志列表 / 统计接口性能重构设计

状态：**已实现**（B1 + B2 均已落地，全量测试通过；本文档已按实际代码回写，见 §16）
作者：Claude
日期：2026-07-28

> **硬约束（用户指定）**
> 1. 不得放大数据库连接占用，不得给 relay 主链路增加任何负担。
> 2. **不新建数据表**，预聚合改用 Redis / 内存缓存。
> 3. **翻页深度不得收缩**——允许继续翻页，但首屏必须快；总数与统计量走缓存。
>
> 由此得到的方案形态：`logs` 的**行数**与**用量**用同一套按整点小时的缓存分解，
> 两端不足整点的残段实时补齐。结果与今天**逐位相等**，
> 因此**翻页能力、API 契约、数据模型全部零变化**。

> 本功能位于管理后台读路径，不在 AI relay 主链上，按 Rule 8.1 不需满足 100k RPM 标准。
> 但它与 relay 结算路径**共用 `logs` 表**、与 relay **共用 Redis**，
> 因此仍按 Rule 0 做完整共享资源审计，见 §10、§11。

---

## 1. 背景与现状问题

### 1.1 现场实测

管理后台「使用日志 → 通用日志」，默认筛选（今天 00:00 → now+1h，无其它过滤），
浏览器 Network 面板实测（第 1 页，每页 10 行）：

| # | 请求 | 耗时 | 落点 |
|---|---|---|---|
| 1 | `GET /api/log/stat?...` | **7.79s** | [controller/log.go:374](../../controller/log.go#L374) → [model.SumUsedQuota](../../model/log.go#L925) |
| 2 | `GET /api/log?...` → **301** | 1.17s | Gin 尾斜杠重定向，纯浪费的 RTT |
| 3 | `GET /api/log/stat?...&type=0` | **7.82s** | 同 #1，仅 query 多了语义等价的 `type=0` |
| 4 | `GET /api/log?...&type=0` → **301** | 1.50s | 同 #2 |
| 5 | `GET /api/log/?...` | **6.09s** | [controller/log.go:247](../../controller/log.go#L247) → [model.GetAllLogs](../../model/log.go#L525) |
| 6 | `GET /api/log/?...&type=0` | **6.56s** | 同 #5 |

页面返回：总计 **24,985,946** 条 / **2,498,595** 页。

**2498 万是当天那个时间范围的 COUNT 实测结果**（已确认）。
但由此推出的「约 2500 万行/天 ≈ 104 万行/小时」是**外推的速率，不是实测值**——
日志量随流量波动，这个数只用于粗略判断量级，不应被当作事实引用或写进代码注释。
更不要拿页面上的 RPM 去反推日增量，两者口径完全不同，见 §1.4。

本部署日志库为 PostgreSQL（`LOG_SQL_DSN`），与主库 MySQL 分离。
（未配置 `LOG_SQL_DSN` 的部署 `LOG_DB = DB`，见 [model/main.go:218](../../model/main.go#L218)。）

### 1.2 根因

| # | 问题 | 定位 | 代价 |
|---|---|---|---|
| **P0-A** | **列表的 COUNT 扫描全范围**。`Count()` 对 2498 万行做精确计数；同请求里 `LIMIT 10 OFFSET 0` 的取数是微秒级 | [model/log.go:563](../../model/log.go#L563) | **列表 6s 的几乎全部** |
| **P0-B** | **统计的 SUM 无法覆盖索引**。`quota` 不在任何索引中，`SUM(quota) WHERE created_at BETWEEN ? AND ? AND type=2` 必须回表读 2498 万个 tuple | [model/log.go:926](../../model/log.go#L926) | **统计 7.8s 的全部** |
| **P1-C** | **深翻页**。`Offset(startIdx)` 随页码线性放大 | [model/log.go:571](../../model/log.go#L571) | 见 §6.6：本设计**不改变**这条曲线，但每页都省掉了 P0-A 那 6 秒 |
| **P1-D** | **301 重定向**。路由注册为 `logRoute.GET("/")`，前端请求 `/api/log`，每次列表多一个 RTT | [router/api-router.go:401](../../router/api-router.go#L401)、[api.ts:37-41](../../web/src/features/usage-logs/api.ts#L37-L41) | 每次列表 +1 RTT |
| **P1-E** | **重复查询轮次**。`handleApply` 无条件把 `type: [logType]` 写进 URL，而 `type=0`（全部类型）与「不传 type」语义完全相同，query key 却不同，两条重查询全部重跑 | [filter-bar.tsx:209](../../web/src/features/usage-logs/components/common-logs-filter-bar.tsx#L209)、[:221](../../web/src/features/usage-logs/components/common-logs-filter-bar.tsx#L221) | 重查询 ×2 |

**PostgreSQL 特有的放大**：`count(*)` 带 `created_at` 范围只能走完 `idx_created_at_id` 的全部索引项。
而 `logs` 是全库写入最热的表，可见性图长期落后于 autovacuum，index-only scan 退化为回表。

### 1.3 连接占用才是真正的风险

单看「慢 6 秒」还只是体验问题。真正的隐患是**连接被长时间占住**：

- 每打开一次日志页 ≈ **13.9 个连接·秒**（6.09 + 7.79）压在 `LOG_DB` 上；
  截图里因为 P1-E 的重复轮次实际是 **~27.8 连接·秒**。
- 这两条长查询与 relay 结算的 `INSERT INTO logs` 争用同一张表、同一个连接池。
- 若干管理员同时刷新，或有人写了定时拉取，连接池会被这些 8 秒级查询逐步吃掉。

改造的首要指标不是「页面变快」，而是**把单次页面访问的连接占用压到毫秒级**。

### 1.4 顺带发现（不改行为，仅记录）

- **`SumUsedQuota` 的 `logType` 参数从未被使用**——函数体内硬编码 `type = LogTypeConsume`
  （[model/log.go:962](../../model/log.go#L962)）。即页面上的「类型」筛选对统计徽章无效，只影响列表。
  本设计**保持该行为不变**。
- **`SumUsedQuota` 里只有 quota 是贵的**：`rpm` / `tpm` 走最近 60 秒的独立查询
  （[model/log.go:966](../../model/log.go#L966)），本来就便宜。
- **RPM / TPM 与列表总数不可互相换算**。以代码为准
  （[model/log.go:929](../../model/log.go#L929)、[:963-966](../../model/log.go#L963-L966)）：
  `rpm = count(*) WHERE type = LogTypeConsume AND created_at >= now-60s`。
  它与列表总数在**两个维度上都不同口径**——
  ①窗口：60 秒瞬时值 vs 整天；②类型：仅消费类 vs 全类型（`type=0` 不加类型过滤）。
  因此**不能**用 RPM 乘以时长去估算日增量，反之亦然。
- `logs` 表上有 **15 个索引**（实测 `pg_indexes`）。见 §14。

---

## 2. 目标与范围

### 2.1 目标

1. 通用日志列表首屏 **6s → 100ms 量级**。
2. 统计徽章的默认视图 **7.8s → 100ms 量级**。带筛选的首次查询不劣化，见 §6.5。
3. **单次页面访问的 DB 连接占用下降两个数量级**，主库新增访问为 0。
4. **不新建数据表、不做数据库迁移、不新增 `logs` 上的任何索引**。
5. **翻页深度、总数精度、API 契约、数据模型零变化**——用户看到的总计仍是 24,985,946，
   仍可翻到最后一页。
6. 消除每次列表请求的 301 与重复查询轮次。

### 2.2 范围

**加速**（走小时缓存分解）：
- 管理员通用日志列表 `GET /api/log/`（`GetAllLogs`）——无筛选或仅按类型筛选时
- 管理员统计 `GET /api/log/stat`（`SumUsedQuota`）——无筛选时
- 一个轻量的整点预热协程（master 独占）

**不加速，但保持原行为**：
- 自助 / 员工视角 `GET /api/log/self`、`/api/log/employee` 及其 `/stat`。
  这些接口的查询**恒带用户维度过滤**（`user_id` / `username`），
  按 §6.5 属于「带筛选」，走回落路径。它们本来就能吃到
  `idx_user_id_id` / `idx_logs_username`，命中行数远小于全量。

**不包含**（本期）：
- 绘图日志（`midjourneys`）、任务日志（`tasks`）
- 导出路径——已在 [usage-log-export.md](./usage-log-export.md) 单独治理
- `logs` 索引精简与 REINDEX——见 §14
- 请求日志 `/api/request-log`
- 数据看板与 `quota_data` 的任何改动——**明确不动**
- keyset 翻页——见 §6.6

### 2.3 关键设计决策

| # | 决策点 | 选择 | 理由 |
|---|---|---|---|
| **D1** | **列表总数** | **不截断。按整点小时缓存行数，两端残段实时补齐** | 用户明确要求保留翻页能力。**关键洞察：`count(*)` 和 `SUM(quota)` 可以用完全相同的方式分解**——都是按时间可加的量。因此一套机制同时解决 P0-A 与 P0-B，总数仍然精确，翻页深度一点不动 |
| **D2** | **缓存什么** | 每个整点小时一个结构体：**按 `type` 分别的行数 + 消费额度 + token 数** | 一次查询同时供列表 COUNT（含类型筛选）与统计 quota 使用。类型有 7 个实际取值（`LogTypeTopup`..`LogTypeLogin`，[model/log.go:85-92](../../model/log.go#L85-L92)），全部预先分好，**切换类型 tab 也命中缓存** |
| **D3** | **不变量** | 只缓存**已完成**的整点小时 | **已完成整点的行数与额度是不可变量**——这是整个方案成立的根基：缓存**永不需要失效逻辑**。当前整点用短 TTL（默认 15s）单独缓存 |
| **D4** | **缓存介质** | **复用 [`pkg/cachex.HybridCache`](../../pkg/cachex/hybrid_cache.go)** | 现成组件：Redis 可用时走 Redis（跨节点共享），不可用时自动退化为进程内 LRU；自带命名空间隔离。已有 4 处在用（`model/subscription.go`、`service/channel_affinity.go`） |
| **D5** | **缓存怎么填** | **整点预热协程**（master 独占）+ 惰性回源 | 每小时**只查 1 条**。对比建表方案要每天扫 2500 万行 / 约 5000 条查询，轻两个数量级 |
| **D6** | **精度** | 两端不足整点的残段实时查 `logs` 补齐 | 保证结果与直接扫 `logs` **逐位相等**；默认时间范围（今天 00:00）恰好对齐整点，零头残段 |
| **D7** | **不加覆盖索引** | **否决**「给 `logs` 加 `(type, created_at, quota, ...)`」 | ①仍要扫 2498 万条索引项，只从 ~7.8s 降到 ~1-2s；②给全库写入最热的表加第 16 个索引，写放大打在 relay 结算路径上，违反 Rule 0；③MySQL / SQLite 不支持 `INCLUDE` |
| **D8** | **ClickHouse 后端** | 缓存**不启用**，走原路径 | 列存的 `count(*)` 与 `sum()` 本来就快，加缓存是负优化。以 `common.UsingLogDatabase(DatabaseTypeClickHouse)` 分支 |
| **D9** | **301 修复位置** | **后端加路由别名** `logRoute.GET("")` | 一次修复覆盖所有现有与未来调用方（`features/employees/api.ts:170` 也在调 `/api/log?`） |

### 2.4 被否决的两版方案（记录，防止绕回去）

| 方案 | 否决原因 |
|---|---|
| **有界 COUNT**（子查询封顶 1 万行，前端显示 `10000+`） | 会收缩翻页深度（默认每页 100 行时只剩 100 页），是可感知的功能退化。用户明确要求保留翻页 |
| **读主库 `quota_data` 预聚合表** | `quota_data` 在主库 MySQL，而主库连接池由 relay 热路径（tokens/channels/users）共享。把管理后台的读引过去会放大连接占用、影响主链 |
| **在 `LOG_DB` 新建 `log_hourly_stats` 汇总表** | 要 DDL + 迁移 + 每天扫 2500 万行的 rollup 作业。缓存方案用一个「已完成整点不可变」的性质达到同样效果，成本轻两个数量级 |

---

## 3. 数据流

### 3.1 核心：区间分解（列表与统计共用）

给定区间 `[S, E]`，令 `H = now - now%3600`（当前整点）：

```
A = ceil_hour(S)                     ← 第一个完整整点
B = min(H, floor_hour(E))            ← 最后一个完整整点之后

┌──────────┬─────────────────────────────┬──────────────┐
│ [S, A)   │  [A, B)                     │  [B, E]      │
│ 头残段    │  已完成整点区间               │  尾段/当前整点 │
│ 实时查 logs│  逐小时取缓存（未命中则回源）    │  实时查 logs   │
│ ≤ 1 小时  │  命中即 0 次 DB              │  ≤ 1 小时     │
└──────────┴─────────────────────────────┴──────────────┘
```

三段的值直接相加。**因为行数与额度都是按时间可加的量，结果与整段一次查询逐位相等。**

**默认范围（今天 00:00 → now+1h，UTC+8）**：`S` 恰好整点 → **无头残段**；
`floor_hour(E) > H` → `B = H` → 尾段即「当前整点至今」。
即**缓存全命中时只剩 1 条 `logs` 查询（≤1 小时数据），且它本身有 15 秒短缓存**。

若 `E < H`（纯历史区间）且缓存命中，**零 `logs` 访问**。

### 3.2 列表

```
GET /api/log/?p=1&page_size=100&start_timestamp=..&end_timestamp=..&type=..
  → controller.GetAllLogs → model.GetAllLogs           [全部在 LOG_DB]
      ├─ [TOTAL] §3.1 分解，取对应 type 的行数         ← 6s → ms
      └─ [PAGE ] SELECT * FROM logs WHERE <filters>
                 ORDER BY created_at DESC, id DESC
                 LIMIT page_size OFFSET start_idx      ← 不变
  → { items, total }        ← 响应体与今天完全一致
```

### 3.3 统计

```
GET /api/log/stat?start_timestamp=S&end_timestamp=E
  → controller.GetLogsStat → model.SumUsedQuota
      ├─ [QUOTA] §3.1 分解，取 consume 的 quota          ← 7.8s → ms
      └─ [RPM/TPM] 维持现状（logs 最近 60 秒，本来就便宜）
  → { quota, rpm, tpm }     ← 响应体与今天完全一致
```

### 3.4 整点预热协程

```
[master 独占] 每 WarmIntervalSec 检查一次：
  h = 上一个已完成的整点
  若 h 未缓存：
     SETNX logstat:lock:{h}（TTL 60s，防多 master 重复算）
     一条查询算出该小时的全部指标（见 §6.2 的 SQL）
     写入缓存
  启动时：向前补齐最近 WarmHours 个整点（默认 48），批间 sleep
```

**总成本：稳态每小时 1 条查询。** 启动时一次性 48 条（可配，批间 sleep）。

---

## 4. API 契约

### 4.1 唯一的契约变化：新增路由别名，消除 301

```go
// router/api-router.go
logRoute.GET("",  middleware.AdminAuth(), controller.GetAllLogs)  // 新增
logRoute.GET("/", middleware.AdminAuth(), controller.GetAllLogs)  // 既有，不动
```

鉴权不变（`AdminAuth`，Rule 11）。

### 4.2 其余接口：请求与响应体**零变化**

- `GET /api/log/` 的 `total` 仍是精确值（24,985,946），前端分页组件、URL 契约、
  `ensurePageInRange` 全部不动。
- `GET /api/log/stat` 的 `quota` / `rpm` / `tpm` 语义与数值不变。
- **不新增任何响应字段**。缓存命中与否只记服务端日志，不暴露给客户端。

> 唯一的口径差异：当前整点的值有 ≤ `CurrentHourTTLSec`（默认 15 秒）的滞后。
> 对一个本身就是快照的日志列表，这个滞后不可感知。见 §6.4。

### 4.3 前端改动

**只有一处**：[common-logs-filter-bar.tsx](../../web/src/features/usage-logs/components/common-logs-filter-bar.tsx#L209)
的 `handleApply` / `handleReset` 在类型为「全部」时不把 `type` 写进 URL，消除 P1-E 的重复查询轮次。

分页组件、统计徽章、i18n 文案**全部不动**——这是保留精确总数带来的直接收益。

---

## 5. 数据模型变更

### **零。**

不新建表、不加列、不加索引、不做迁移，连 `common.PageInfo` 都不改。

### 5.1 索引使用（Rule 8.3）

全部复用 `logs` 既有索引：

| 查询 | 走的索引 | 扫描上界 |
|---|---|---|
| 单整点全指标（预热 / 回源 / 残段） | `idx_created_at_id (created_at, id)` | ≤ 1 小时数据 |
| 页数据 | `idx_created_at_id` | pageSize 行 + OFFSET |
| rpm/tpm | `idx_created_at_type (created_at, type)` | ≤ 60 秒数据 |

---

## 6. 关键设计说明

### 6.1 缓存键与不变量

```
命名空间： logstat            （已核实与 relay 及全库现有 key 前缀无冲突）
键：       logstat:h:{v}:{hourTs}:{fingerprint}      已完成整点，TTL = StatCacheTTLHours（默认 48h）
           logstat:cur:{v}:{hourTs}:{fingerprint}    当前整点，  TTL = CurrentHourTTLSec（默认 15s）
           logstat:lock:{hourTs}                     预热互斥，  TTL 60s
值：       hourStat 结构体（见 §6.2），JSON 编码
```

- `v`：口径版本号常量。改变分桶/过滤语义时递增，**一次性作废全部旧缓存**，无需逐键清理。
- `hourTs`：UTC 整点秒（`t - t%3600`）。
- `fingerprint`：筛选条件规范化后的稳定哈希；**无筛选时固定为字面量 `all`**（预热只针对它）。
  **`type` 不进指纹**——它作为字段存在值里（§6.2），所以切换类型 tab 仍然命中同一份缓存。

**核心不变量：已完成的整点小时，其行数与额度不再变化。**
因此已完成整点的缓存**不需要任何失效逻辑**。只缓存 `hourTs + 3600 <= now` 的整点。

**唯一会破坏该不变量的是日志清理**：`DeleteOldLogBatch` 删掉旧行后，
缓存里仍是删除前的真实值，很旧的区间可能出现「总数 > 实际可翻的行数」。
处理：`StatCacheTTLHours` 默认 2 天，应短于日志保留期；文档标注该行为。

### 6.2 缓存值与预热查询

```go
type hourStat struct {
    CountByType map[int]int64 `json:"c"` // type(1..7) → 行数
    CountAll    int64         `json:"a"` // 全类型行数
    Quota       int64         `json:"q"` // type=Consume 的 SUM(quota)
    Tokens      int64         `json:"t"` // type=Consume 的 SUM(prompt+completion)
}
```

一条跨库安全的查询同时算出全部字段（不用 PG 专有的 `FILTER`，Rule 2）：

```sql
SELECT
  COUNT(*)                                                       AS count_all,
  SUM(CASE WHEN type = 1 THEN 1 ELSE 0 END)                      AS c1,
  ...                                                            -- type 2..7 同理
  COALESCE(SUM(CASE WHEN type = 2 THEN quota            ELSE 0 END), 0) AS quota,
  COALESCE(SUM(CASE WHEN type = 2 THEN prompt_tokens
                                     + completion_tokens ELSE 0 END), 0) AS tokens
FROM logs
WHERE created_at >= :h AND created_at < :h + 3600
```

一次扫描（≤1 小时数据）产出列表与统计所需的全部指标。

### 6.3 缓存介质：复用 `pkg/cachex.HybridCache`

```go
statHourCache = cachex.NewHybridCache[hourStat](cachex.HybridCacheConfig[hourStat]{
    Namespace:    cachex.Namespace("logstat"),
    Redis:        common.RDB,
    RedisCodec:   cachex.JSONCodec[hourStat]{},
    RedisEnabled: func() bool { return common.RedisEnabled },
    Memory:       func() *hot.HotCache[string, hourStat] { /* LRU，容量见 §7 */ },
})
```

- **Redis 可用** → 走 Redis，跨节点共享，一个节点算过的小时其它节点直接命中。
- **Redis 不可用**（`RedisEnabled == false` 或连接失败）→ 自动退化为进程内 LRU。
  每个节点各自算一次，功能不受影响，只是命中率略低。
- **任一层报错** → 记 `LogWarn`，回源 `logs`。**永不向上抛错**（Rule 0）。

若逐小时 GET 被证明过于啰嗦（默认区间 17 个键 = 17 次 Redis 往返），
再给 `pkg/cachex` 补一个 `MGet` 批量方法——**这是唯一可能需要动 `pkg/cachex` 的地方**，
且是纯增量方法，不改现有行为。

### 6.4 精度与时区

分桶用 `created_at - created_at%3600`，即 **UTC 整点对齐**。

- 默认范围「今天 00:00 → now+1h」在 UTC+8 下恰好对齐整点 → **零头残段**。
- 手选非整点起点（如 09:30）：`ceil_hour(09:30) = 10:00`，
  `[09:30, 10:00)` 由头残段的实时查询补齐（≤ 1 小时）。终点同理。
- 非整小时时区偏移的部署（如 UTC+5:30）：残段机制自动覆盖。

**因此任意区间的结果都与直接扫 `logs` 逐位相等**，唯一差异是当前整点 ≤15 秒的滞后。
这是 §12.2 的核心断言。

### 6.5 无筛选 vs 带筛选 —— 本方案的能力边界（重要）

| 场景 | 首次查询 | 重复查询 |
|---|---|---|
| **无筛选 / 仅按类型筛选**（默认视图，即截图那两条 6s+7.8s） | **快**——预热已算好，只剩当前整点的 ≤1h 查询 | 快 |
| **带其它筛选**（username / model / channel / group / token_name） | **不变快**，与今天同速；受 §8.2 超时保护 | 快（同一指纹的整点值已被缓存） |

理由：
1. 预热只针对 `all` 指纹。为任意筛选组合预热等于要建维度立方体——那正是被否决的「建表」方案。
2. 带筛选的查询**本来就比无筛选快得多**：`username=X` / `channel=N` 能走
   `idx_logs_username` / `idx_logs_channel_id`，命中行数远小于全量；真正慢的恰恰是无筛选那条。
3. 缓存键预留了指纹段（§6.1），将来要支持按筛选组合预热时不必改键结构。

> **注意一个反直觉点**：对带筛选的查询，**不做**逐小时分解。
> 因为分解成 17 条 1 小时查询后，PG 每条都可能改走 `idx_logs_username` 再过滤时间，
> 造成 17 倍放大。带筛选时整段一次查询，与改造前完全一致，并受 §8.2 的超时保护。

**类型筛选是例外**：`type` 不进指纹，而是作为字段存在缓存值里（§6.2）。
所以在页面上切换「消费 / 错误 / 充值」这类最高频的交互，命中的仍是同一份缓存。

### 6.6 深翻页：诚实说明（P1-C）

本设计**不改变**深翻页的成本曲线——`OFFSET` 仍随页码线性放大。以每页 100 行估算
（基准：实测 2498 万行 / 6.09s ⟹ 约 410 万行·秒⁻¹ 的索引扫描速率，线性外推，**非逐点实测**）：

| 页码 | OFFSET | 取数耗时 | 今天还要额外付的 COUNT |
|---|---|---|---|
| 1 | 0 | ~0 | 6s |
| 100 | 9,900 | ~2ms | 6s |
| 1,000 | 99,900 | ~24ms | 6s |
| 10,000 | 1,000,000 | ~240ms | 6s |
| 249,859（末页） | ~25,000,000 | ~6s | 6s |

**每一页都省掉了那 6 秒**，浅页从 6s 降到毫秒级，末页从 12s 降到 6s。
翻页能力一点没少，只是最深处仍然慢——这与今天一致，不是新引入的问题。

彻底解决要上 keyset（成本恒定但失去页码跳转），本期不做，见 §14 N3。
另有一个可选优化：利用已缓存的小时行数直方图把深 `OFFSET` 折算到「某个小时内的浅 OFFSET」，
但跨小时边界的处理会引入正确性风险，同样列入 §14。

### 6.7 回落到原整段查询的全部条件

任一满足即回落（行为与今天一致，不劣化）：

1. 带类型以外的筛选条件（§6.5）
2. `StatCacheEnabled` 配置关闭（逃逸阀）
3. 日志库是 ClickHouse（D8）
4. 无时间范围（`S == 0 && E == 0`）——见 §9 硬约束
5. 区间跨度超过 `StatMaxCachedHours`（默认 720h = 30 天），避免一次拼上千个小时
6. `log_id` / `request_id` / `upstream_request_id` 精确查询（本来就走各自索引，极快）

---

## 7. 配置参数（Rule 12）

`setting/operation_setting/log_query_setting.go`：

```go
type LogQuerySetting struct {
    // —— 缓存 ——
    StatCacheEnabled   bool `json:"stat_cache_enabled"`
    StatCacheTTLHours  int  `json:"stat_cache_ttl_hours"`  // 已完成整点的 TTL
    CurrentHourTTLSec  int  `json:"current_hour_ttl_sec"`  // 当前整点的短 TTL；0 = 每次实时查
    StatMaxCachedHours int  `json:"stat_max_cached_hours"` // 单次查询最多拼多少个整点
    StatMemoryEntries  int  `json:"stat_memory_entries"`   // 无 Redis 时的进程内 LRU 容量

    // —— 预热 ——
    WarmEnabled     bool `json:"warm_enabled"`
    WarmIntervalSec int  `json:"warm_interval_sec"`
    WarmHours       int  `json:"warm_hours"`    // 启动时向前补齐的整点数；0 = 不补齐
    WarmSleepMs     int  `json:"warm_sleep_ms"` // 补齐时批间 sleep

    // —— 通用 ——
    QueryTimeoutMs int `json:"query_timeout_ms"`
}
```

默认值：`StatCacheEnabled=true`、`StatCacheTTLHours=48`、`CurrentHourTTLSec=15`、
`StatMaxCachedHours=720`、`StatMemoryEntries=4096`、`WarmEnabled=true`、
`WarmIntervalSec=300`、`WarmHours=48`、`WarmSleepMs=200`、`QueryTimeoutMs=30000`。

`QueryTimeoutMs` 取 30s 而非更激进的值：它是**兜底闸门，不是性能目标**。
实测最坏一条（无筛选 COUNT 扫 2500 万行）约 6s，留 5 倍余量只掐真正失控的查询；
定得太紧会把「慢但仍然可用」的带筛选查询误杀，那是功能退化而不是优化。

Getter 统一 clamp（与 `log_export_setting` 同风格，复用同包的 `clampInt`）：
`StatCacheTTLHours` → `[1, 8760]`；`StatMaxCachedHours` → `[1, 8760]`；
`StatMemoryEntries` → `[64, 1000000]`；`WarmIntervalSec` → `[60, 3600]`；
`QueryTimeoutMs` → `[1000, 60000]`。

**三个「0 是合法取值」的字段**，getter 必须原样保留 0 而不是当非法值回退默认——
这是这类 clamp 最容易写错的地方，已被 `log_query_setting_test.go` 逐个锁住：

| 字段 | 0 的含义 |
|---|---|
| `CurrentHourTTLSec` | 当前整点每次都实时查，不缓存 |
| `WarmHours` | 启动时不做向前补齐，只预热此后新完成的整点 |
| `WarmSleepMs` | 补齐时不休眠 |

**两个逃逸阀**：`StatCacheEnabled = false`、`WarmEnabled = false`。
全关 = 行为完整回退到改造前。

### 7.1 后台热更新（全部字段，无需重启）

传播链完全复用既有机制，本设计不新增保存端点：

```
设置页保存 → UpdateOption 写 options 表
           → updateOptionMap → handleConfigUpdate 按 "." 拆键
           → config.GlobalConfig.Get("log_query_setting")
           → UpdateConfigFromMap 原地改结构体（ParseBool / Atoi 按字段类型解析）
其它节点 → SyncOptions 周期性 loadOptionsFromDatabase，≤ SyncFrequency 秒内跟上
```

之所以天然是热的：所有 getter 都经 `GetLogQuerySetting()` 取**全局结构体指针并每次读取**，
没有任何一处把配置值缓存到局部变量或启动时快照。

| 字段 | 读取时机 | 生效时机 |
|---|---|---|
| `StatCacheEnabled` | 每次查询 `logStatCacheUsable` | **立即** |
| `StatMaxCachedHours` | 每次查询 `logStatCacheUsable` | **立即** |
| `QueryTimeoutMs` | 每次查询前建 ctx | **立即** |
| `StatCacheTTLHours` | 每次 `sumLogStatRangeAt` | **立即**（对新写入的键；已写入键的 TTL 不回溯） |
| `CurrentHourTTLSec` | 每次 `sumLogStatRangeAt` | **立即** |
| `WarmEnabled` | 预热循环每轮开头 | 下一轮（≤ `WarmIntervalSec`） |
| `WarmIntervalSec` | 每轮 sleep 前 | 下一轮 |
| `WarmSleepMs` | 每轮批间 sleep | 下一轮 |
| **`StatMemoryEntries`** | 进程内 LRU 构造时（`sync.Once`） | **需重建**——见下 |
| `WarmHours` | 仅进程启动时 | 仅启动生效（语义是「启动补齐深度」，不是运行时旋钮） |

**唯一需要额外处理的是 `StatMemoryEntries`**：进程内 LRU 的容量与 janitor 默认 TTL
在首次构造时被 `sync.Once` 定死，不重建就永远用旧值。因此在 `handleConfigUpdate`
里为 `log_query_setting` 挂了后处理钩子（与 `performance_setting` / `billing_setting`
同一位置的既有模式）：

```go
} else if configName == "log_query_setting" {
    resetLogStatCache()   // 丢弃实例，下次使用时按最新配置重建
}
```

重建只是丢弃实例指针，**不动 Redis 里的值**，也不影响正在执行的查询
（已持有旧实例的调用继续用完即可，旧实例仍然有效）。
由 `log_stat_hotreload_test.go` 锁住：开关立刻改变判定、时长立刻生效、
容量变更换新实例、且重建前后查询结果逐位相同。

配置暴露在既有系统调优设置页（`log-query-section.tsx`，与 `log_export_setting` 同页），
不新增保存端点。设置页只呈现运营真正会调的 6 项
（两个开关 + 当前整点 TTL + 整点保留时长 + 启动补齐小时数 + 查询超时）；
`StatMaxCachedHours` / `StatMemoryEntries` / `WarmIntervalSec` / `WarmSleepMs`
仍可经既有的 option API 设置，只是不占用面板空间。

---

## 8. 错误处理（Rule 9、Rule 6）

### 8.1 后端

- 一律走 `common.ApiError` / `ApiErrorI18n`，不新增响应形状。
- **缓存读写失败 → 不报错**：记 `logger.LogWarn` 后回源 `logs`。
- `logs` 查询失败 → 与今天一致，`common.SysError` + 返回 i18n 错误 key。
- **预热协程失败 → 完全静默**：`recover()` + `common.SysError`。永不影响在线请求（Rule 0）。

### 8.2 超时保护

所有 COUNT / SUM 查询统一挂 `context.WithTimeout(QueryTimeoutMs)`，经 `WithContext` 下传，
**缓存路径与回落路径都挂**：

| 路径 | 落点 |
|---|---|
| 缓存分解（含逐整点回源、残段实时查） | `countLogsWithHourCache` / `sumConsumeQuotaWithHourCache` 内的 `logStatQueryContext()` |
| 列表 COUNT 回落 | `countLogsWithHourCache` 末尾的 `fallback().WithContext(ctx).Count()` |
| 统计 SUM 回落 | `SumUsedQuota` 里的 `tx.WithContext(statCtx).Scan()` |

**回落路径更需要这道闸门**：它恰恰是带筛选、无法走缓存的那条，也是最可能长时间
占住连接的一条。宁可给用户「请缩小范围」的提示，也不要让一条查询占住 `LOG_DB`
连接十几秒——这正是 §1.3 要解决的问题。

> 实现注意：`WithContext` 走的是 `Session()`，返回**新的** `*gorm.DB` 而不修改原 `tx`，
> 因此 `GetAllLogs` 在 COUNT 之后继续用 `tx` 做 `Find` 不受影响
> （由 `TestGetAllLogs_FallsBackWhenFiltered` 同时断言总数与返回行覆盖）。

Redis 操作沿用 `cachex` 内置的 2 秒超时（[hybrid_cache.go:15](../../pkg/cachex/hybrid_cache.go#L15)）。

### 8.3 前端文案

**本期不新增任何面向用户的文案**——总数与统计值的语义、精度、展示全部不变。
仅在统计查询超时时，徽章显示 `--` 并可点击重试（不显示 Go 错误串），
这条已有的错误分支复用现有 i18n key。

---

## 9. 与现有子系统的交互

| 子系统 | 交互 | 影响 |
|---|---|---|
| **relay 结算** | 往 `logs` INSERT | 本设计对 `logs` 只读且**减少**扫描量；不新增索引；不调用任何 relay 侧函数 |
| **数据看板 / `quota_data`** | —— | **完全不动** |
| **Redis** | relay 重度使用 | 独立命名空间 `logstat:`，操作全是小键 GET/SET/SETNX。详见 §11.2 |
| **日志导出** | 独立的 keyset 扫描路径 | 不共享代码，不受影响 |
| **日志清理 `DeleteOldLogBatch`** | 删旧 `logs` 行 | 见 §6.1 末段：缓存 TTL 默认 2 天，应短于日志保留期 |
| **ClickHouse 日志后端** | `UsingLogDatabase(DatabaseTypeClickHouse)` | 缓存不启用，走原路径（D8） |
| **多节点 / 切主** | `common.IsMasterNode` | 预热仅 master 跑，且用 Redis SETNX 兜底多 master；缓存本身在 Redis 中天然共享 |

**硬约束（代码层强制）**：统计与 COUNT 查询**必须**带时间范围。
`startTimestamp == 0 && endTimestamp == 0` 时不走缓存路径，且回落路径同样受超时保护。
对应 Rule 8.3「不查无界时间范围」。

---

## 10. Main Chain Impact（Rule 0）

**同步执行在 relay goroutine 上的部分：零。**

改动全部位于：
1. 管理后台的 HTTP 读路径（`AdminAuth` / `UserAuth` 路由组）——不新增 middleware；
2. 一个 master 独占的预热 goroutine——不被任何在线请求调用。

不修改 `relay/`，不修改 relay 调用的任何函数。已核实
`GetAllLogs` / `SumUsedQuota` 的调用方只有 `controller/log.go` 的三处
（`:247`、`:374`、`:401`），无 relay 路径调用者。

**净效果是减轻 relay 压力**：今天每打开一次日志页，就有两条 6~8 秒的大扫描
压在 relay 正在 INSERT 的同一张表上（PG 下还推高 autovacuum 压力）。

---

## 11. 共享资源审计与占用分析（Rule 0）

### 11.1 资源清单

| 资源 | relay 是否访问 | 本设计如何访问 | 冲突评估 |
|---|---|---|---|
| **`logs` 表（LOG_DB）** | ✅ 结算路径 INSERT | 只读：≤1 小时 SUM/COUNT + 页数据 | **改善**。PG MVCC 下读不阻塞写；扫描量下降降低 buffer 争用与 autovacuum 压力 |
| **数据库表结构** | —— | **零 DDL、零迁移、零新表、零新索引** | 无 |
| **主库 `DB`（MySQL）** | ✅ relay 读 tokens/channels/users | **零访问** | **无新增** |
| **`quota_data` / `CacheQuotaData` / `CacheQuotaDataLock`** | ✅ relay 每条消费日志持锁一次 | **完全不碰** | 无冲突 |
| **Redis（`common.RDB`）** | ✅ relay 重度使用 | 独立命名空间 `logstat:`，小键 GET/SET/SETNX | 见 §11.2 |
| **LOG_DB 连接池** | ✅ relay INSERT 用 | 见 §11.3 | **大幅改善** |
| **主库连接池** | ✅ relay 热路径共享 | **不使用** | 无影响 |
| **内存** | —— | 无 Redis 时的 LRU，上限 `StatMemoryEntries`（默认 4096） | < 2 MB，可忽略 |
| **goroutine** | —— | 新增 1 个常驻（master 独占，非请求路径） | 可忽略 |

### 11.2 Redis 专项

Redis 是**单线程**的，且是 relay 的热路径依赖——任何慢命令都会阻塞 relay。因此约束如下：

| 约束 | 本设计的做法 |
|---|---|
| 命名空间隔离 | 前缀 `logstat:`，已核实与现有 key（`auth:session`、`token:`、`user:`、`channel_account_balance:`、`ledger:dedup:*` 等）无冲突 |
| **禁止慢命令** | **只用 GET / SET / SETNX**。不用 `KEYS`、`SCAN`、`HGETALL`、`SORT`，不建大 hash |
| 单键体积 | 一个 `hourStat` 的 JSON，**< 200 字节** |
| 单次页面请求的 Redis 操作数 | ≤ 区间小时数（默认视图 17 次 GET）+ 少量 SET。若嫌啰嗦再补 `MGet`（§6.3） |
| 总内存占用 | 24 键/天 × 7 天 TTL × 指纹数。默认只有 `all` 一个指纹 → **约 170 个键，< 40 KB** |
| 超时 | 沿用 `cachex` 内置 2 秒超时 |
| Redis 挂掉 | 自动退化为进程内 LRU；再不行回源 `logs`。**不报错** |
| 需要更强隔离时 | 仓库已有 `REQUEST_LOG_REDIS_DB` 的先例（请求日志用独立逻辑库）。本设计流量比它小几个数量级，暂不需要 |

结论：以「170 个键 / 40 KB / 每次页面加载十几个 GET」的量级，
对 relay 共享的 Redis 是**可忽略的负载**。

### 11.3 连接占用分析

以「一次管理员打开日志页（列表 + 统计两个请求）」为单位：

| | LOG_DB 连接·秒 | 主库连接·秒 | Redis |
|---|---|---|---|
| **改造前** | COUNT 6.09 + SUM 7.79 ≈ **13.9**；含 P1-E 重复轮次实测 ≈ **27.8** | 0 | 0 |
| **本版（缓存全命中）** | 取数 + rpm/tpm ≈ **< 0.05** | **0** | ~34 次 GET |
| **本版（当前整点缓存失效）** | 额外 1 条 ≤1 小时查询 ≈ **+0.25** | **0** | 同上 |

残段查询的量级：按 §1.1 的基准，1 小时 ≈ **104 万行**，
以 410 万行·秒⁻¹ 推算约 **0.25s**；且该值有 `CurrentHourTTLSec`（默认 15s）短缓存摊薄，
多个管理员并发刷新时只有第一个付这个代价。

→ **LOG_DB 连接占用下降 50~250 倍；主库新增为 0。**

常驻开销（与页面访问无关）：**每小时 1 条查询**（扫约 104 万行，≈0.25s），
master 节点独占，非 master 为 0。新增存储 0（仅 Redis 约 40 KB）。

### 11.4 并发分析（Rule 8）

不在 relay 路径上，按 Rule 8.1 无需满足 100k RPM 标准。数字仅用于共享资源评估：

| 路径 | 改造前 / 请求 | 改造后 / 请求 |
|---|---|---|
| 列表 | LOG_DB × 2（COUNT 全扫 2498 万行 ≈ 6s + 取数） | Redis × ~17 + LOG_DB × 1（取数），ms 级 |
| 统计（无筛选） | LOG_DB × 2（SUM 全扫 2498 万行 ≈ 7.8s + rpm/tpm） | Redis × ~17 + LOG_DB × 1（rpm/tpm），ms 级 |
| 统计（带筛选） | 同上 | 首次同今天；重复命中结果缓存 |
| 主库 | 0 | **0** |
| 锁 | 无 | 预热用 Redis SETNX，非请求路径 |
| goroutine | 无新增 | 后台 +1（master 独占） |

---

## 12. 测试计划（Rule 15，测试先行）

按 Rule 15.2，实现前先写用例。数据库测试用真实 MySQL + PG（Rule 15.5），行级清理（不用 `truncateTables`）。

### 12.1 `model/log_stat_cache_test.go` —— 核心正确性

**主断言**：同一批造数下，
`缓存分解路径的 (total, quota) == 直接对 logs 做整段 COUNT/SUM 的 (total, quota)`（逐位相等）。
这条等式是 D1/D3/D6 全部决策的正确性根基。

| 用例 | 期望 |
|---|---|
| 纯历史区间 + 缓存全命中 | **零 `logs` 访问**，结果 == 整段查询 |
| 纯历史区间 + 缓存全未命中 | 逐小时回源并回填，结果 == 整段查询 |
| 跨当前整点 | 缓存整点 + 当前整点实时，== 整段查询 |
| 起点非整点 | 头残段补齐后 == 整段查询 |
| 终点非整点 | 尾残段补齐后 == 整段查询 |
| 起点终点同一小时内 | 全部由残段覆盖，== 整段查询 |
| **已完成整点不得含未来行** | 只缓存 `hourTs+3600 <= now` 的整点 |
| **当前整点 TTL 生效** | `CurrentHourTTLSec=0` 时每次实时；>0 时窗口内不重查 |
| 跨越整点边界的日志 | 分别落入两个小时桶 |
| **每种 `type`（1..7）单独筛选** | 各自的行数 == 直接 COUNT |
| `type=0`（全部） | `CountAll` == 直接 COUNT |
| quota 只统计 Consume | 造 `type=1` 行 → 不计入 quota |
| 带 username / channel 等筛选 | 回落整段查询（§6.5） |
| `StatCacheEnabled=false` | 回落 |
| 区间跨度 > `StatMaxCachedHours` | 回落 |
| 无时间范围（S=0,E=0） | 回落（§9 硬约束） |
| 缓存读报错 / 写报错 | 结果仍正确，**不返回 error** |

### 12.2 `model/log_stat_warm_test.go` —— 预热与缓存介质

| 用例 | 期望 |
|---|---|
| Redis 可用 | 值写进 Redis，另一「节点」（新 HybridCache 实例）能读到 |
| `RedisEnabled=false` | 自动退化为进程内 LRU，功能正常 |
| Redis 报错 | 回源 `logs`，不 panic 不报错 |
| 非 master 节点 | 预热协程直接返回，零 DB 访问 |
| SETNX 锁 | 两个「master」并发预热同一小时 → 只有一个真正查库 |
| 启动补齐 | 补齐最近 `WarmHours` 个整点，且**不包含当前整点** |
| panic 恢复 | 注入 panic → 被 `recover`，进程不退出 |
| 口径版本号 `v` 递增 | 旧键不再被命中 |
| LRU 容量上限 | 超出 `StatMemoryEntries` 后旧条目被淘汰，不 OOM |

### 12.3 `model/log_stat_cache_wiring_test.go` —— 接入后的对外行为

断言「无论走缓存还是回落，对外结果都与直接扫 logs 一致」：
`GetAllLogs` 的总数（含每种 `type` 单独筛选）、`SumUsedQuota` 的额度、
带 `username` 筛选时不得错用无筛选的缓存值、关掉开关 / 无时间范围时回落、
`SumUsedQuota` 仍然忽略 `logType`（§1.4，防止「类型」筛选突然开始影响统计徽章）。

基线用 `queryLogHourStatRange` 在插入前量一次——它是直查、不写缓存，
所以不会把空值冻结进去；这样即便时间窗里有其它行，断言依然成立。

### 12.4 `router/log_route_alias_test.go`

- 路由表里 `GET /api/log` 与 `GET /api/log/` 都存在且指向同一 handler
- 实际发请求：两条路径都**不返回 3xx**，且状态码一致
  （不带凭证时停在鉴权中间件返回 401——能走到鉴权就证明没被 `RedirectTrailingSlash` 弹走）

### 12.5 `setting/operation_setting/log_query_setting_test.go`

每个 getter 的边界值分析：非法值回退默认 / 低于下限夹到下限 / 高于上限夹到上限 /
区间内原样返回，外加 §7 那三个「0 是合法取值」的字段单独锁死。

### 12.6 实际执行结果

```
TEST_DB_CLEANUP=true go test ./... -p 1
```
（`-p 1` 序列化各包，避免跨包争用共享 MySQL 导致失败集不确定；见 `project-test-harness`）

- **77 个包通过**；本设计新增用例 35 个，全部通过，无 skip。
- 剩余 2 个失败均在 `service` 包，**已逐个验证为预存在**——
  把本设计的改动 `git stash` 后在干净树上复跑，失败完全一致：
  `TestCommissionTierReset_RunNow`、
  `TestLoginfoGenerateTextOtherInfo_DoesNotRecordNegativeFirstResponseTime`。

### 12.7 跨库

本部署 `DB=MySQL` / `LOG_DB=PostgreSQL`，测试同时经过两者：
`logs` 相关断言跑在 PG 上（含 §6.2 的 `SUM(CASE WHEN ...)` 与保留字 `"group"` 引号），
配置与主库断言跑在 MySQL 上。ClickHouse 分支无法在此环境执行，
由 `logStatCacheUsable` / `logStatWarmEnabled` 的分支用例覆盖判定逻辑。

---

## 13. 实施状态

两批均已实现，无 DDL、无迁移，缓存冷启时自动回源，最坏情况即改造前的性能。

| 批次 | 内容 | 落点 |
|---|---|---|
| **B1** | 301 别名（D9） | [router/api-router.go](../../router/api-router.go)：`logRoute.GET("")` 与 `GET("/")` 并存 |
| | `type=0` 不写 URL（P1-E） | [common-logs-filter-bar.tsx](../../web/src/features/usage-logs/components/common-logs-filter-bar.tsx) 的 `logTypeSearch` |
| **B2** | 小时缓存分解（D1~D6） | **新增** [model/log_stat_cache.go](../../model/log_stat_cache.go) |
| | 整点预热（D5） | **新增** [model/log_stat_warm.go](../../model/log_stat_warm.go)，由 [main.go](../../main.go) 的 `StartLogStatWarmLoop()` 启动 |
| | 配置（§7） | **新增** [setting/operation_setting/log_query_setting.go](../../setting/operation_setting/log_query_setting.go) |
| | 接入 | [model/log.go](../../model/log.go)：`GetAllLogs` 的 COUNT、`SumUsedQuota` 的 quota |
| | 设置页 | **新增** [log-query-section.tsx](../../web/src/features/system-settings/maintenance/log-query-section.tsx)，注册进 `system-tuning/section-registry.tsx`；7 个 locale 各补 14 条文案 |

**未改动**（与设计一致）：`relay/`、`quota_data` / 数据看板、日志导出、
`logs` 的表结构与索引、分页组件、`common.PageInfo`、
`GetAllLogs` / `GetLogsStat` 的请求与响应字段。

---

## 14. 已识别但本期不做

| # | 事项 | 说明 |
|---|---|---|
| N1 | **`logs` 索引精简** | 实测该表有 15 个索引。`idx_logs_user_id` 与 `idx_user_id_id(user_id,id)` 冗余；`idx_logs_model_name` 与 `index_username_model_name(model_name,username)` 前缀重叠；`idx_logs_ip` 无已知查询使用。每一个都是 relay 结算 INSERT 的写放大。**删索引不可逆，需先做全量查询审计**，单独立项 |
| N2 | **PG 索引膨胀 / REINDEX** | 实测开发库上 `logs` 活行 1132、堆 480 kB，而索引 **721 MB**——历史大表被清理后留下的索引膨胀。PG 的 `DELETE` 不回收索引空间。生产大概率需要定期 `REINDEX CONCURRENTLY`。单独立项 |
| N3 | **深翻页优化** | §6.6。两条路：①keyset 游标（成本恒定，失去页码跳转）；②用已缓存的小时行数直方图把深 `OFFSET` 折算成小时内浅 `OFFSET`（跨边界处理有正确性风险）。等 B2 上线后看实际是否有人翻到那么深 |
| N4 | **带筛选统计的预热** | §6.5。需要维度立方体，等同建表。缓存键已预留指纹段，将来加不必改键结构 |
| N4b | **带筛选查询的短 TTL 结果缓存** | 只优化「同一筛选反复查看」，对首次查询无帮助；要正确做需按筛选条件生成稳定指纹，复杂度不低而收益有限。**没有实现，也没有留下对应配置项**——宁可没有这个旋钮，也不要一个不生效的配置骗人 |
| N5 | **`SumUsedQuota` 的 `logType` 死参数** | §1.4。修掉会改变页面语义，属产品决策 |
| N6 | **默认时间范围** | 「今天 00:00 → now+1h」在 2500 万条/日的量级下是很重的默认值。改成「最近 1 小时」能进一步降本，属产品决策 |
| N7 | **`UpdateQuotaData()` 缺 master 守卫** | [main.go:113](../../main.go#L113) 每个节点都在跑，靠 `node_name` 进唯一键规避冲突。不是本次要改的，记录在案 |

---

## 15. 约束达成情况

| 约束 | 结论 |
|---|---|
| 翻页深度 | 保留，未收缩 |
| 总数精度 | 保持精确，未截断 |
| 数据表 | 未新建，零 DDL 零迁移 |
| 主库 | 零访问 |
| API / 数据模型 | 零变化 |
| relay 主链 | 零改动 |
| 数据基准 | 2500 万行/天 ≈ 104 万行/小时（当天真实值，已确认） |

### 15.1 测试结论

`go test ./... -count=1 -p 1`（串行，避免跨包争用同一套共享库）：
**77 个包通过，仅 `service` 失败**。

`service` 的 19 个失败是**既有问题，与本设计无关**——已用 `git worktree` 在未改动的
`HEAD` 上跑同一批测试，失败集合**逐条一致**。其根因是该包的测试用硬编码低位 id
（`channels` 401/7001-7003、`users` 91001-91002、`system_task_locks` 的
`locked_by='test-runner'`）且失败时不清理夹具，于是自我延续地污染下一次运行。
这些行已随本次清理一并删除，但只要这些测试再次失败就会重新留下。

**并行跑（默认 `-p`）会额外多出 4 个 `model` 的 system-task 失败**：
`model`/`service`/`controller` 会同时打到同一个 MySQL/PG。这不是代码问题，
是共享库测试harness 的固有限制——验证本设计请用 `-p 1`。

---

## 16. 实现记录（防复发）

三处实现期踩到的坑，记录终结结论，避免以后重犯。

### 16.1 `hot` 缓存：`WithTTL` 必须先于 `WithJanitor`

`hot.NewHotCache(...).WithJanitor().Build()` 会 **panic**：
janitor 拿默认 TTL 当清理间隔，缺省是 0，`time.NewTicker(0)` 直接炸。

影响面：这条路径只在 **Redis 关闭**时才走到（`HybridCache` 是 Redis 优先、
无 Redis 时退化为进程内 LRU），所以单看有 Redis 的环境完全发现不了——
一旦某个部署没配 Redis，就是**启动即崩**。

结论：构造 `hot.HotCache` 时永远先 `WithTTL(...)` 再 `WithJanitor()`。
每个键真正的过期时间仍由 `SetWithTTL` 单独指定，这里的默认值只用于喂 janitor。
由 `TestSumLogStatRange_*` 系列在 `RedisEnabled=false` 下覆盖。

### 16.2 测试不得消耗共享的 `nextTestID` / `uniq`

`model` 包的测试共用 `zz_harness_test.go` 里的全局 id 计数器。
本设计的测试早期版本用 `nextTestID()` 分配时间窗，结果**同一次运行中排在后面的
`TestTask_*` 全部报 `Error 1062 Duplicate entry ... for key 'tasks.PRIMARY'`**。

根因不是新测试有 bug，而是：共享开发库里躺着 **504 行残留的 `tasks` 测试数据**
（`task_public_*`、`vid-nochan` 之类的 fixture，id 在 `800000000+` 的测试保留段）。
Task 测试平时能过，只是因为计数器恰好落在残留行的空隙里；任何新测试只要消耗
若干 id，就会把它们推到撞车位置。

结论：`model/log_stat_cache_test.go` 自带独立 id 段（`900000000+`）与本地计数器，
以及不走共享工厂的 `mkLogStatRow`，**完全不推进全局计数器**。
新增 `model` 测试时应照此办理。

> **后续**：那批残留数据已在本次实施中清理（经用户确认）。清理时发现两类，
> 判定规则不同，混用会误删真实数据：
>
> | 类别 | 判定依据 | 处理 |
> |---|---|---|
> | 显式 id 的测试行（`tasks`/`users`/`channels` 等） | id ≥ `800000000`（harness 保留段） | 可按 id 段删除 |
> | **自增 id 的表**（`logs`、`quota_data` 等） | **不可**按 id 段判定 | 生产量级下自增 id 完全可能超过 8 亿，套用该规则会删真实数据 |
>
> `logs` 的测试残留改用**时间窗**判定（本设计的测试固定落在 2000-01-01 起的历史窗口）。
> 实测该窗口 0 残留，说明每用例的 `t.Cleanup` 是有效的。

### 16.3 缓存版本号声明为 `var` 而非 `const`

`logStatCacheVersion` 是口径版本号，本用于「改语义时一次性作废旧缓存」。
声明成 `var` 多了一个用途：测试给每个用例换一个唯一版本号，
就能在 Redis 与进程内两种后端上都拿到互不干扰的键空间——
否则 Redis 里 7 天 TTL 的键会活过一次 `go test`，让下一次运行读到上一次的陈旧值。

---

> **提交提示**：本仓库 `.gitignore` 第 46 行忽略 `/docs`，本文件不会出现在 `git status` 中，
> 需要纳入版本控制时用 `git add -f docs/design/usage-log-list-performance.md`。

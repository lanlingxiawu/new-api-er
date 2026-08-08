# 启动卡死修复：日志表 schema 迁移改为幂等闸门

状态：**已实现并验证**
作者：Claude
日期：2026-07-30

> **实际实现（2026-07-30）**
>
> - 单库和独立日志库路径都已移除 `Log` 的 `AutoMigrate`，统一进入
>   `migrateLogTable` 幂等闸门。
> - schema 完整时只执行 `HasTable` / `HasColumn` / `HasIndex` catalog 查询，
>   不执行 DDL，也不调用 `ColumnTypes` 或读取 `logs` 数据页。
> - 缺失列/索引逐项补齐；每项 DDL 默认受 3000ms context 预算约束，
>   PostgreSQL 额外设置事务级 `lock_timeout` 和 `statement_timeout`。
> - DDL 失败记录系统错误、继续启动并在下次启动重试，优先保证 relay 可用性。
> - 独立日志库支持 `LOG_SQL_MAX_OPEN_CONNS` / `LOG_SQL_MAX_IDLE_CONNS`；
>   未配置时保持向主库参数回退。
> - `migrateDBFast` 保留以避免无关删除，但已从其模型列表移除 `Log`。
> - GORM SQL 日志在写入时解析当前 Gin writer，因此会跟随日志滚动。

> **本次要解决的现场问题**
> 线上进程在 2026-07-30 12:34:13 被硬杀，之后两次重启都卡在
> `[SYS] using PostgreSQL as database` → `[SYS] database migration started` 之后，
> 第二次启动 `ready in 110773 ms`（110 秒服务完全不可用）。
>
> 本文档只覆盖**"启动为什么要 110 秒"**这一条因果链。
> 12:34 那次硬杀的外部触发因素（OOM / 编排）需要服务器上的 `dmesg`、
> `journalctl`、`docker inspect` 才能定案，不在本文档范围内；
> 但它的**放大器**（PG 连接打满、日志爆量）在 §7 一并处理。

---

## 1. 现场证据

### 1.1 日志文件要先分清"滚动切片"和"真正启动"

[logger.go:113](../../logger/logger.go#L113) 在累计 100 万行后会 `SetupLogger()` 切一个新文件，
文件名同样是 `oneapi-<时间戳>.log`。所以文件数 ≠ 启动次数：

| 文件 | 行数 | 首行 | 性质 |
|---|---|---|---|
| `oneapi-20260721181946.log` | 1,000,908 | relay 错误 | 滚动切片 |
| `oneapi-20260730122755.log` | 1,000,346 | relay 错误 | 滚动切片，12:27:55 → 12:32:56 |
| `oneapi-20260730123256.log` | 361,296 | relay 日志 | 滚动切片，12:32:56 → **12:34:13 无预兆终止** |
| `oneapi-20260730124446.log` | 7 | `initializing token encoders` | **真正启动**，卡在 PG 迁移后被杀 |
| `oneapi-20260730124513.log` | 20,599 | `initializing token encoders` | **真正启动**，`ready in 110773 ms` |

结论：7/21 至 7/30 只有一个进程；它在 12:34:13 被**硬杀**
（没有 `received shutdown signal` 那行，说明没走 SIGTERM 优雅退出）。

### 1.2 110 秒花在哪一步

`oneapi-20260730124513.log` 前 8 行：

```
12:45:13 | using MySQL as database
12:45:13 | database migration started          <- 主库 MySQL，瞬间完成
12:45:13 | system is already initialized at: ...
12:45:13 | using PostgreSQL as database
12:45:13 | database migration started          <- 日志库 PG
12:47:03 | SYNC_FREQUENCY not set, use default value 60     <- 110 秒后
...
ready in 110773 ms
```

唯一夹在两者之间的代码是 [model/main.go:248-250](../../model/main.go#L248-L250) 的
`migrateLOGDB()`，即 [model/main.go:434-439](../../model/main.go#L434-L439)：

```go
func migrateLOGDB() error {
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		return migrateClickHouseLogDB()
	}
	return LOG_DB.AutoMigrate(&Log{})
}
```

而它同步跑在 [main.go:370](../../main.go#L370) 的 `InitResources()` 里，
HTTP 端口还没 bind —— **这 110 秒服务是彻底不可用的，不是"慢"，是"停"**。

### 1.3 为什么日志文件里没有慢 SQL 记录

[model/gorm_logger.go:28](../../model/gorm_logger.go#L28) 把 GORM logger 绑到 `os.Stdout`，
而 [logger.go:67](../../logger/logger.go#L67) 的 `SetupLogger()` 只重绑 `gin.DefaultWriter` /
`gin.DefaultErrorWriter`。**GORM 的慢 SQL（阈值 200ms）从来不会进 `oneapi-*.log`**，
所以那 110 秒在文件里是一片空白 —— 排查时最误导人的一点。

---

## 2. 根因：`AutoMigrate(&Log{})` 不幂等，每次开机都对 `logs` 下发全表级 DDL

在本地 PG（schema 与生产同构，21 列 / 14 个模型索引）连跑两遍 `AutoMigrate(&Log{})`，
两遍下发的语句**完全相同**：

```sql
ALTER TABLE "logs" ALTER COLUMN "username"   SET DEFAULT '';
ALTER TABLE "logs" ALTER COLUMN "token_name" SET DEFAULT '';
ALTER TABLE "logs" ALTER COLUMN "model_name" SET DEFAULT '';
ALTER TABLE "logs" ALTER COLUMN "ip"         SET DEFAULT '';
ALTER TABLE "logs" ALTER COLUMN "request_id"          TYPE varchar(64)  USING "request_id"::varchar(64);
ALTER TABLE "logs" ALTER COLUMN "request_id"          SET DEFAULT '';
ALTER TABLE "logs" ALTER COLUMN "upstream_request_id" TYPE varchar(128) USING "upstream_request_id"::varchar(128);
ALTER TABLE "logs" ALTER COLUMN "upstream_request_id" SET DEFAULT '';
```

### 2.1 为什么每次都重来

1. [model/log.go:63-78](../../model/log.go#L63-L78) 的 6 个字符串列带 `default:''`。
2. PG 把默认值存成 `''::text` / `''::character varying`。
3. `postgres@v1.5.2/migrator.go:466` 用来剥离类型转换的正则
   `'?(.*)\b'?:+[\w\s]+$` **匹配不了 `''::text`**（`''` 之后紧跟 `::`，
   中间没有 `\b` 可落点），于是原样返回 `''::text`。
4. `gorm@v1.25.2/migrator/migrator.go:508` 拿 `''::text` 与 tag 里的 `''` 比较 → 不等
   → `alterColumn = true`。
5. 于是调用 postgres 的 `AlterColumn`，而 `AlterColumn`
   （`postgres@v1.5.2/migrator.go:279-334`）进去之后**无条件重新比对类型**：
   `DatabaseTypeName()` 返回 `varchar`，`DataTypeOf(field)` 返回 `varchar(64)`，
   两者不等且 `varchar` 不在 `typeAliasMap` 里 → 重新下发 `TYPE ... USING ...`。

也就是说：**`default:''` 造成的误判，会顺带把 `TYPE ... USING ...` 也拖出来重跑一遍。**
单独把 `type:varchar(64)` 改成 `size:64` 修不了这个问题（已验证：尺寸比对本来就是通过的）。

### 2.2 110 秒全部花在 ACCESS EXCLUSIVE 锁等待上

生产实测（2026-07-30 13:58，见 [production-tuning.md](../ops/production-tuning.md)）：
`logs` 表 **412,091,379 行 / 530 GB**（堆 276 GB + 索引 123 GB），
PG 16.14，`lock_timeout = 0`，`idle_in_transaction_session_timeout = 0`。

**这 8 条 DDL 本身都是元数据操作，极快。**
`ALTER COLUMN ... TYPE varchar(64) USING ...::varchar(64)` **不会重写表**：
PG 的 `ATColumnChangeRequiresRewrite()` 会剥掉 `RelabelType`，
发现变换表达式就是列本身时跳过重写。这也和时长对得上 ——
276 GB 堆 + 123 GB 索引真要重写是**小时级**，110 秒根本不可能完成。

时间全部消耗在抢锁上：

1. `logs` 持续承接约 500 insert/s；
2. DDL 请求 ACCESS EXCLUSIVE，必须等所有在途事务结束；
3. `lock_timeout = 0` → **无限等待**；
4. PG 锁队列是 FIFO —— DDL 一旦排队，**后续所有对 `logs` 的读写全部排在它后面**；
5. `idle_in_transaction_session_timeout = 0` → 任何一个忘了提交的空闲事务
   都能把 DDL 连同它身后的整条队列**无限期钉住**；
6. 12:44:46 那个尚未被杀的实例也在抢同一把锁，两个实例互相排队 ——
   这解释了为什么 12:44:46 那次干脆没走出迁移。

第 4 点同时解释了 §7.1 的 19,312 条 `too many clients`：
写入被锁在队列里 → Go 连接池不断新开连接 → 撞上 `max_connections = 100`。
**启动 DDL 与连接耗尽不是两起事故，是同一条因果链。**

> **已排除的方向，不要再重查**：曾怀疑是 GORM 每次 `AlterColumn` 都重拉
> `ColumnTypes`、其中 `SELECT * FROM "logs" LIMIT 1` 在堆表死元组上退化成长扫描
> （一次 AutoMigrate 共 7 次）。生产实测该语句
> `Execution Time: 0.041 ms`、`Buffers: shared read=1`、`n_dead_tup = 4`
> —— **假设不成立**，堆表没有膨胀，`pg_repack` 也不需要做。

### 2.3 单库部署同样中招（容易漏掉的分支）

`LOG_SQL_DSN` 为空时，[model/main.go:217-221](../../model/main.go#L217-L221) 让
`LOG_DB = DB` 并**直接 return**，`migrateLOGDB()` 根本不会被调用；
此时 `logs` 表由 [model/main.go:276](../../model/main.go#L276) 的主库 `AutoMigrate` 大列表负责。
所以**只修 `migrateLOGDB()` 对"全部跑在一个 PG 上"的部署无效**，必须两条路径都覆盖。

（MySQL 主库不受影响：MySQL 返回的默认值是不带 cast 的 `''`，比对能通过；
现场日志里主库迁移确实是秒级完成的。但 `logs` 走不走 MySQL 由部署决定，
所以闸门要做成与数据库无关的。）

---

## 3. 目标与非目标

**目标**

1. 常态启动（schema 已就绪）对 `logs` **零 DDL、零 `SELECT * FROM logs`**，
   把 110s 打回百毫秒级。
2. 升级场景（模型新增列 / 新增索引）依然能自动补齐，且只做**必要**的那一件事。
3. 任何情况下，日志表 DDL 不能无限期持有 `logs` 的表锁。
4. 兼容 SQLite / MySQL / PostgreSQL / ClickHouse 四种日志库形态（Rule 2）。

**非目标**

- 不改 `Log` 结构体的列、索引、类型、JSON 契约 —— 数据模型零变化。
- 不修 `logs` 的堆膨胀（`VACUUM` / `pg_repack` 是运维动作，只在 §8 给出建议）。
- 不追查 12:34 硬杀的外部触发因素（需要服务器侧证据）。
- 不改 relay 的日志写入路径。

---

## 4. 方案

### 4.1 核心：把"迁移"换成"先判断、再精准补齐"，彻底不调 `AutoMigrate`

新增一个与数据库无关的 `migrateLogTable(db *gorm.DB) error`，取代两处对 `logs` 的 `AutoMigrate`：

```
migrateLogTable(db):
  m := db.Migrator()
  1. 表不存在        -> m.CreateTable(&Log{})，结束
  2. 解析 &Log{} 的 schema，得到 DBNames 与 ParseIndexes()
  3. 逐列 m.HasColumn / 逐索引 m.HasIndex，收集缺失项
  4. 全部齐全        -> SysLog("log table schema up to date, skipping migration") 并直接返回
  5. 有缺失          -> 在带超时的会话里，对缺失项逐个 m.AddColumn / m.CreateIndex
                        每项前后打 SysLog（列名/索引名 + 耗时）
```

关键设计约束（必须写在代码注释里，Rule 0 / feedback_doc_comment_current_state）：

- **绝对不能用 `Migrator().ColumnTypes()` 来做这个判断** —— 它内部就带
  `SELECT * FROM logs LIMIT 1`，正是我们要消掉的那条慢查询。
  只能用 `HasTable` / `HasColumn` / `HasIndex`，这三个在三种库上都是**纯 catalog 读**，
  不碰 `logs` 的数据页，也不取表锁。
- 判断阶段的成本：21 次 `HasColumn` + 14 次 `HasIndex` ≈ 35 次 catalog 往返，
  实测量级 ~100ms，且**只在 master 节点、只在启动时**发生一次。
- 遍历顺序必须与 GORM 一致：列用 `stmt.Schema.DBNames`，
  索引用 `stmt.Schema.ParseIndexes()`（v1.25.2 返回 `map[string]Index`），
  否则会和 `AutoMigrate` 的判定产生偏差。
- `ChannelName string gorm:"->"` 是只读字段但**仍在 `DBNames` 里**，
  GORM 建表时会建 `channel_name` 列，现场 PG 也确有此列 —— 照常纳入检查，不特殊处理。

这样做比"schema 没就绪就 fallback 到 `AutoMigrate`"更好：
后者会让**每次涉及 `Log` 的版本升级**都重新付一次 110s 停机代价。
精准补齐则是 `AddColumn`（PG 11+ 带常量默认值是纯元数据操作，瞬间完成）
+ 只建真正缺的那个索引。

### 4.2 DDL 会话加锁超时，避免冻住热表

只在**确实要下发 DDL** 时（4.1 的第 1、5 步）套一层：

| 数据库 | 语句 | 说明 |
|---|---|---|
| PostgreSQL | `SET LOCAL lock_timeout`, `SET LOCAL statement_timeout` | 必须在事务内，`SET LOCAL` 随事务结束自动还原 |
| MySQL | `SET SESSION lock_wait_timeout = N` | 控制的是元数据锁等待 |
| SQLite | 无对应设置 | 跳过 |

拿不到锁时 DDL 返回错误：**记 `SysError` 并让启动继续**，不 `return err`。
理由：`logs` 少一个索引会让后台查询变慢，但让整个网关起不来是更坏的结果；
而 `AddColumn` 失败时相关列的读写会走到"列不存在"的错误路径，
这比 relay 全挂要好。下次重启会自动重试。

> 这一条与现状是行为变更：今天 `migrateLOGDB()` 的错误会一路冒到
> `InitResources()` → `return err` → `main()` 里 `common.FatalLog` 退出。
> 改成"记录并继续"需要你确认 —— 见 §9 待确认项 (1)。

### 4.3 新增配置（Rule 12 / 环境变量定位）

这三个是部署/基础设施配置，与既有的 `SQL_MAX_OPEN_CONNS` 同类，
按 CLAUDE.md Rule 12 的例外走 env，不进 `setting/`：

| 变量 | 默认值 | 作用 |
|---|---|---|
| `LOG_SQL_MAX_OPEN_CONNS` | 回退到 `SQL_MAX_OPEN_CONNS` | 日志库连接池上限，与主库解耦（§7.1） |
| `LOG_SQL_MAX_IDLE_CONNS` | 回退到 `SQL_MAX_IDLE_CONNS` | 同上 |
| `LOG_MIGRATE_LOCK_TIMEOUT_MS` | `3000` | §4.2 的锁等待上限；`0` 表示不设置 |

---

## 5. 改动点

| # | 文件 | 位置 | 改动 |
|---|---|---|---|
| 1 | `model/log_migrate.go` | 新文件 | `migrateLogTable(db *gorm.DB) error` + `missingLogColumnsAndIndexes()` + 锁超时包装 |
| 2 | [model/main.go:434-439](../../model/main.go#L434-L439) | `migrateLOGDB()` | `LOG_DB.AutoMigrate(&Log{})` → `migrateLogTable(LOG_DB)` |
| 3 | [model/main.go:265-316](../../model/main.go#L265-L316) | `migrateDB()` | 从 `AutoMigrate` 大列表里移除 `&Log{}`（第 276 行），改为在列表之后调用 `migrateLogTable(DB)`。覆盖 §2.3 的单库部署 |
| 4 | [model/main.go:338-391](../../model/main.go#L338-L391) | `migrateDBFast()` | 该函数**全项目无调用者**（已确认），一并删除；否则它是同一个坑的第二份拷贝 |
| 5 | [model/main.go:241-243](../../model/main.go#L241-L243) | `InitLogDB()` | 连接池改读 `LOG_SQL_MAX_OPEN_CONNS` / `LOG_SQL_MAX_IDLE_CONNS`，回退到主库同名变量 |
| 6 | [model/gorm_logger.go:25-47](../../model/gorm_logger.go#L25-L47) | `newGormConfig` / `sanitizedLogWriter` | writer 改为**写时**取 `gin.DefaultWriter`（`common.LogWriterMu.RLock()` 保护），让慢 SQL 落进 `oneapi-*.log` 并跟随滚动 |
| 7 | [main.go:370-373](../../main.go#L370-L373) | `InitResources()` | 在 `InitLogDB()` 前后加 `SysLog` 打点耗时，下次再出现同类卡顿能直接从日志定位 |
| 8 | `model/log_migrate_test.go` | 新文件 | 见 §6 |
| 9 | `.env.example` | 数据库段 | 补 §4.3 三个变量的注释说明 |

**明确不改**：`model/log.go` 的 `Log` 结构体（列、索引、`default:''`、`type:varchar(...)` 全部保持）。
理由：改 tag 会触发一次真实的 schema 变更（`DROP DEFAULT` 或 `TYPE` 重写），
在 2500 万行表上是分钟级停机；而闸门方案不碰 schema 就能解决问题。

---

## 6. 测试方案（Rule 15，测试先行）

`model/log_migrate_test.go`，跑真实 PG（`LOG_SQL_DSN`）+ 真实 MySQL（`SQL_DSN`），
无 DB 环境时按 Rule 15.5 回退 SQLite `:memory:`。行级清理，不 truncate。

| 用例 | 技术 | 断言 |
|---|---|---|
| `TestMigrateLogTable_UpToDateEmitsNoDDL` | 语句覆盖 + 会退回归 | schema 就绪时，用 GORM 的 `DryRun` / SQL 采集确认**零** `ALTER`、零 `CREATE INDEX`、零 `SELECT * FROM logs`。**这条就是本次 bug 的回归测试** |
| `TestMigrateLogTable_CreatesTableWhenMissing` | 边界（表不存在） | 建表后 21 列 / 14 索引齐全 |
| `TestMigrateLogTable_AddsOnlyMissingColumn` | 判定覆盖 | 手工 `DROP COLUMN upstream_request_id` 后，只下发一条 `ADD COLUMN`，其余列无 DDL |
| `TestMigrateLogTable_AddsOnlyMissingIndex` | 判定覆盖 | 手工 `DROP INDEX idx_created_at_type` 后，只重建该索引 |
| `TestMigrateLogTable_IdempotentAcrossTwoRuns` | 路径覆盖 | 连跑两次，第二次零 DDL（防止 §2.1 那类"看似幂等实则不幂等"复发） |
| `TestMigrateLogTable_DDLErrorDoesNotFailStartup` | 错误路径 | 注入 DDL 失败，函数返回 `nil` 且记录了 `SysError`（取决于 §9(1) 的决定） |
| `TestMigrateLogTable_ClickHouseUnchanged` | 等价类 | ClickHouse 分支仍走 `migrateClickHouseLogDB()`，不进新逻辑 |
| `TestLogTableIndexesMatchModel` | 契约 | `ParseIndexes()` 的 14 个索引名与文档/迁移清单一致，防止今后加索引忘了 |

完成后按 Rule 15.8 跑 `go test ./...` 全量。

---

## 7. 放大器（同一次事故的另两条线，一并处理）

### 7.1 PG 连接被打满

12:27:55–12:33:48 的两个切片里共 **19,312 条**：

```
failed to record log: ... FATAL: sorry, too many clients already
failed to record log: ... FATAL: remaining connection slots are reserved for roles with the SUPERUSER attribute
```

[model/main.go:241-243](../../model/main.go#L241-L243) 把 `SQL_MAX_OPEN_CONNS`
**同时**套在 MySQL 主库池和 PG 日志库池上。生产实测两侧数字：

| 项 | 实测值 |
|---|---|
| 应用 `.env` 的 `SQL_MAX_OPEN_CONNS` | **2000** |
| PG `max_connections` | **100**（`superuser_reserved_connections = 3`） |

**超配 20 倍。** §2.2 的锁队列一旦形成，写入全部堵住，Go 连接池就不断新开连接，
必然瞬间撞满 100 的上限；写日志失败又各自再打一行 ERR，形成放大。
另外 `SQL_MAX_LIFETIME` 未设置、取代码默认 **60 秒**，连接池每 60 秒全量重建，
在 PG（每连接一个 `fork()`）上是持续的后端创建开销。

→ 改动点 #5 拆出 `LOG_SQL_MAX_OPEN_CONNS`；部署侧的即时缓解与具体数值见
[production-tuning.md §2 P0-1](../ops/production-tuning.md)。

滚动重启时新旧实例连接叠加会让这个问题翻倍，这也是 12:27 那个窗口
从进程存活期中段突然开始报错的原因。

### 7.2 日志爆量

正常速率是 9 天 100 万行（`20260721181946.log`）；
事故窗口是 **6 分钟 200 万行 ≈ 420 MB**。
爆量本身是 7.1 的**症状**（每条失败写入都打一行 ERR），
消掉 7.1 即消掉爆量。本次不改日志分级，
但若 12:34 的硬杀最终查明是磁盘打满，需要另开一个日志限流的设计。

### 7.3 12:47:53 的 SIGTERM 与 2 分钟排空超时

服务 12:47:03 才 ready，12:47:53 就收到 SIGTERM（50 秒）。
**发信号的是谁尚未定案**：生产既无 `new-api` 的 systemd unit，也无对应 docker 容器，
所以不能假定是标准编排的健康检查（见 §8 末尾）。优雅退出排空在途请求超时
（同一文件里有 `1m42.740252292s` 的 `/v1/messages`），
12:49:53 `server shutdown error: context deadline exceeded`，12:49:55 退出。

**启动 110s → 健康检查超时 → 被踢 → 再启动 110s** 本身就是个自持循环，
§4.1 修掉启动耗时后这个循环自然断开。
编排侧建议同步调大 `start_period` / `initialDelaySeconds`，
以及把 stop grace period 调到大于最长 relay 请求耗时。

---

## 8. 运维侧建议（不在代码改动范围内）

完整的中间件调优方案已单独成文：[production-tuning.md](../ops/production-tuning.md)。
与本文档因果链直接相关的两条，即使代码改动尚未上线也应该先做：

1. **PG 加熔断丝**：`lock_timeout = 10s` + `idle_in_transaction_session_timeout = 60s`
   + `log_lock_waits = on`。§2.2 的无限锁等待正是 `lock_timeout = 0` 造成的；
   `log_lock_waits` 开着的话，那 110 秒会直接在 PG 日志里写明谁在等谁的锁，
   不需要任何推理。可 `pg_reload_conf()` 热生效。
2. **连接池对齐**：`SQL_MAX_OPEN_CONNS` 2000 → 200，PG `max_connections` 100 → 300。

仍未定案：12:34:13 的硬杀是谁发的。生产上 `systemctl status new-api` 报
Unit not found，`docker ps -a` 里也没有 new-api 容器，`dmesg` 无 OOM 记录 ——
**既不是 systemd 也不是 docker 在管这个进程**，需要先查清它到底由什么拉起。

---

## 9. 待确认项

1. **DDL 失败时是否放行启动**（§4.2）。现状是 `FatalLog` 退出。
   建议改为"记录并继续"，但这会让"索引没建上"变成静默降级 —— 需要你拍板。
2. **是否删除 `migrateDBFast()`**（改动点 #4）。它无调用者，但如果你打算将来启用它，
   我就只给它加同样的闸门而不删。
3. **`LOG_MIGRATE_LOCK_TIMEOUT_MS` 默认 3000ms 是否合适**。太短会导致升级时索引建不上，
   太长会在高负载下冻住 `logs`。

---

## 10. Main Chain Impact（Rule 0）

**同步 / 异步**

- 本次改动全部发生在**进程启动阶段**（`InitResources()`），relay 主链路运行期零新增代码。
- relay 的日志写入路径（`RecordConsumeLog` 等）**不改一行**。
- 改动的净效果是**减少**主链路阻塞：今天启动期间那 8 条 ACCESS EXCLUSIVE DDL 会冻住
  `logs` 的全部写入（含仍在服务的旧实例），改后常态下一条 DDL 都不发。

**共享资源审计**

| 资源 | relay 是否也用 | 冲突与处置 |
|---|---|---|
| `logs` 表（PG/MySQL） | **是**，结算写入 | 这是本次的核心冲突点。改后常态零 DDL、零表锁；仅升级时下发精准 DDL 且带锁超时（§4.2） |
| `logs` 的 catalog 元数据 | 否 | `HasColumn`/`HasIndex` 是 catalog 读，不取表锁、不碰数据页 |
| 日志库连接池 | **是** | 改动点 #5 把它与主库池拆开；总连接数**下降**，不会新增占用 |
| Redis | 否 | 本次不涉及 |
| 内存结构 / goroutine 池 | 否 | 无新增 goroutine，无新增常驻内存 |
| `gin.DefaultWriter` | 是（relay 打日志） | 改动点 #6 复用 [logger.go:105-111](../../logger/logger.go#L105-L111) 既有的 `common.LogWriterMu` 读锁模式，与现有写入方式一致，不引入新的争用点 |

**并发分析**

启动期一次性 ~35 次 catalog 往返，之后归零。
稳态 QPS 影响为 0（无新增周期任务、无新增中间件）。
100k RPM 下的行为与今天完全一致，唯一差别是**不再有启动期的表锁冻结**。

---

## 11. 评审修订：拆分结构迁移与索引迁移（2026-07-31，已实现）

### 11.1 已确认的问题

当前实现与本文 §4.2 的原方案存在以下结构性问题：

1. `LOG_MIGRATE_LOCK_TIMEOUT_MS=3000` 同时限制锁等待和整个 PostgreSQL DDL
   的执行时间。对生产 `logs`（412,091,379 行 / 530 GB）而言，新索引不可能在
   3 秒内完成，因此启动时重试不会形成有效兜底。
2. `runLogMigrationDDL` 使用事务和 `SET LOCAL statement_timeout`，而安全的大表索引
   路径 `CREATE INDEX CONCURRENTLY` 不能在事务内执行。
3. 建表失败当前被记录后转换为 `nil`。缺少整个 `logs` 表与缺少辅助查询索引不是同一
   降级等级；前者会导致所有 relay 消费日志持久化失败。
4. 当前只检测缺失列和缺失索引，不检测已有列的类型、长度、可空性和默认值漂移。
5. `migrateDBFast` 虽然当前无调用者，但已与 `migrateDB` 的日志表迁移语义分叉。

因此，本文此前的“缺列和缺索引统一由启动迁移补齐”以及“所有 DDL 失败均放行启动”
不再作为目标实现。

### 11.2 修订方案

启动迁移拆成两个职责明确的阶段：

#### A. 结构可用性闸门（启动时执行）

- 表不存在：使用短锁等待预算创建 `logs` 表；失败必须返回错误并阻止启动。
- 缺少模型列：逐列使用短锁等待预算执行 `AddColumn`。失败是否阻止启动按列是否进入
  relay 写入契约决定；当前 `Log` 的持久化使用完整模型写入，因此任何缺列均视为
  schema 不可用并阻止启动。
- 已有列：读取数据库 catalog 元数据核对类型族、长度、可空性和默认值。检查必须使用
  各数据库的 catalog 或 GORM 不触碰数据页的元数据能力，不能执行
  `SELECT * FROM logs`，也不能在启动时自动改类型。
- 类型漂移：输出包含列名、期望定义和实际定义的 `SysError`，并阻止启动。类型修复必须
  由显式运维迁移完成，避免启动时改写 530 GB 热表。
- `LOG_MIGRATE_LOCK_TIMEOUT_MS` 只表达“等待取得 DDL 锁”的预算，不再同时作为长 DDL
  的 `statement_timeout`。

#### B. 索引一致性检查（启动时只读）

- 启动时用 catalog 检查模型声明的索引，但不在已有 `logs` 表上自动创建缺失索引。
- 缺失索引必须产生高可见、可检索的系统错误，列出索引名和数据库方言；不得输出
  “schema is up to date”。
- PostgreSQL 的修复由显式运维迁移执行：
  `CREATE INDEX CONCURRENTLY ...`，事务外运行，使用独立连接，并由独立的长任务超时
  管理。不能复用 3 秒启动预算。
- MySQL/SQLite 也不在热生产表启动路径自动建索引；由相同的显式迁移入口按方言执行。
- 本次不在应用后台自动启动索引构建。索引构建会持续占用日志库 I/O 和连接，和 relay
  的日志落库共享资源；默认后台自动执行不满足 Rule 0 的资源隔离要求。
- 新建空表仍由 `CreateTable(&Log{})` 一次性创建模型索引，因为不存在大表扫描或在线
  写入争用。

显式索引迁移入口（CLI、一次性作业或运维 SQL）的具体产品形态属于新增能力，另行设计；
本次实现保证启动路径不会“静默且永远建不上”，并给运维提供确定的缺失索引诊断。

### 11.3 启动失败策略

| 状态 | 启动结果 | 原因 |
|---|---|---|
| `logs` 表不存在且创建失败 | 失败 | relay 消费日志完全不可持久化 |
| 模型列缺失且补列失败 | 失败 | 当前完整模型写入会持续失败 |
| 已有列定义漂移 | 失败并报告差异 | 自动改类型可能重写热表，继续运行又无法保证写入契约 |
| 辅助索引缺失 | 继续，但输出高可见错误 | 写入仍正确；查询性能风险交给显式在线迁移处理 |
| catalog 检查失败 | 失败 | 无法证明日志 schema 可用 |

### 11.4 `migrateDBFast`

保留该函数时，必须在其普通模型迁移完成后执行与 `migrateDB` 相同的单库日志结构闸门；
若 `LOG_SQL_DSN` 非空，则仍由 `migrateLOGDB` 处理独立日志库。两条主库迁移入口通过同一
helper 组合，避免再次语义分叉。删除该无调用函数是后续清理项，不作为本次修复前提。

### 11.4.1 目录读取的往返次数（实现补充）

结构闸门的耗时几乎全部来自元数据往返次数，而不是单条查询本身。最初实现按
"每列一次 `HasColumn` + 每索引一次 `HasIndex`"探测，`Log` 有 21 个字段和 14 个声明索引，
加上表存在性探测与列目录读取共 **37 条语句**。在真实 PostgreSQL 日志库上实测：

| 阶段 | 次数 | 耗时 | 单次 |
|---|---|---|---|
| `HasTable` | 1 | ~0ms | — |
| `HasColumn` 逐列 | 21 | 27.5ms | 1.31ms |
| `loadLogColumnMetadata` 一次读全表目录 | 1 | 2.07ms | 2.07ms |
| `HasIndex` 逐索引 | 14 | 5.84ms | 0.50ms |
| `GetIndexes` 一次读全部索引 | 1 | 0.52ms | 0.52ms |

逐列探测占了总耗时的 78%，而它拿到的信息与 `loadLogColumnMetadata`（本来就要为
§11.2 的定义校验读一次）完全相同。因此实现改为：

1. **列**：先 `loadLogColumnMetadata` 读一次全表目录，用它同时决定"哪些列缺失"和
   "已有列是否漂移"。只有真的补过列时才重读一次目录拿新列定义。
2. **索引**：用 `Migrator.GetIndexes` 一次取回全部索引名做集合比对。个别方言若不支持
   `GetIndexes`，退回逐个 `HasIndex`，行为不变。

效果：**37 条语句 → 16 条，真实 PostgreSQL 上 35ms → 3.0ms**。这个数字随数据库的
`information_schema` / `pg_catalog` 规模和网络 RTT 放大，表数量多或日志库跨机房时
差距会比这里更明显。

回归护栏是 `TestMigrateLogTableStaysWithinRoundTripBudget`：断言语句数 ≤ 20。
只要有人把按列或按索引的循环写回来，数量会重新逼近 37 并被拦下。

### 11.4.1.1 方言适配的两个致命缺陷（实测发现）

这个闸门最初只在 PostgreSQL 日志库上验证过，MySQL 分支带着两个都会**让进程起不来**
的缺陷。两者都只有对真实 MySQL 跑一遍完整闸门才会暴露，单元测试用的 SQLite 碰不到。

**1. `information_schema` 列标签大小写**

MySQL 8 返回的列标签是大写的 `COLUMN_NAME` / `DATA_TYPE` / ...，而扫描用的结构体
tag 是小写 `gorm:"column:column_name"`，GORM 的映射大小写敏感 —— **一列都扫不进来**。
`len(columns) == 0` 的兜底也拦不住：所有行都映射成空列名，map 里留下一个
`key == ""` 的条目，长度是 1。

后果是每一列都被判定为"缺失"，启动时对已存在的列执行 `AddColumn`，
撞上 `Error 1060: Duplicate column name 'id'` 后拒绝启动。

修复：查询里写显式小写别名（`SELECT COLUMN_NAME AS column_name, ...`），
PostgreSQL 分支也一并加上，让两个方言的行为一致。
另外在 `loadLogColumnMetadata` 末尾增加空列名检查——扫描映射失效属于代码缺陷，
必须当场报清楚，而不是让它以 "Duplicate column name" 的形式在下游冒出来。

**2. MySQL 的布尔类型**

MySQL 没有原生布尔类型，`BOOLEAN` 只是 `TINYINT(1)` 的别名。`logColumnDrift` 的
`schema.Bool` 分支原先只接受 `boolean` / `bool`（外加 SQLite 的 `numeric`），
于是把完全正常的 `is_stream tinyint(1)` 判成定义漂移并拒绝启动。

修复：`dialect == "mysql"` 时接受 `tinyint`。

**回归用例**：`TestStartupGateAcceptsEveryConfiguredDatabase` 对 `.env` 里配置的
**每一个**真实库跑完整闸门（本环境是 MySQL 主库 + PostgreSQL 日志库），
任何方言上的误判都会在这里暴露；`TestLoadLogColumnMetadataOnMainDatabase` 单独覆盖
主库方言——原先只测日志库，正是大写列标签能一路活下来的原因；
`TestBoolColumnAcceptsMySQLTinyint` 钉住布尔类型的方言差异。

### 11.4.1.2 启动期改表的两道防护

`logs` 是持续写入的大表（生产 4 亿行 / 530 GB），启动路径上唯一会改表的操作是
`AddColumn`（`CreateTable` 只在表不存在时执行，此时是空表；不存在 `AlterColumn` /
`DropColumn`，也不会在已有表上建索引）。§11.4.1.1 的第一个缺陷证明了它的失效模式：
**一次目录读取错误，就让启动流程去对生产热表连做 21 次改表**。当时是 MySQL 用
`Duplicate column name` 挡住的，属于运气。因此加两道防护：

**防护一：缺失列数不合理即判定为"读坏了"，不碰表。**
一次正常版本升级最多新增一两列；阈值取 3，超过即拒绝启动并列出被判缺失的字段，
提示核对表结构与数据库账号的元数据读取权限。这条防护无条件生效，
拦的是"读取缺陷导致误改表"这一整类问题，而不是某一个具体 bug。

**防护二：`LOG_MIGRATE_AUTO_ADD_COLUMN=false` 可完全禁止启动期改表。**
缺字段一律转为拒绝启动并列出待补字段，由运维用显式在线迁移处理。
默认 `true` 保持既有行为——缺列会导致每条消费日志写入失败，
自动补列对小表部署仍是合理默认值。

执行 `AddColumn` 之前会先打一条日志说明"即将在大表上改表"，并提示该开关，
让改表动作在事后可追溯。

回归用例：`TestGuardRefusesImplausibleColumnAdditions`（构造"全部列被判缺失"，
断言在任何 `ADD COLUMN` 之前就拒绝）、`TestGuardAllowsSmallColumnAdditions`（阈值边界）、
`TestGuardHonoursAutoAddColumnSwitch` 与 `TestMigrateLogTableRespectsAutoAddColumnSwitch`
（开关关闭后完整闸门也必须在改表前停下）。

### 11.4.2 诊断信息语言

本闸门的所有错误与告警使用中文。Rule 13 说非 UI 的后端日志用英文即可，但这些信息会
直接阻止进程启动、读者是需要立刻决定处置方式的运维，因此每条都写清楚三件事：
**发生了什么、为什么选择拒绝启动、下一步该做什么**。例如列定义漂移会输出字段名、
数据库方言、模型期望定义与数据库实际定义，并说明可以做在线迁移、或在确认兼容时
反过来调整模型标签。

### 11.5 测试增补

实现前先增加以下回归用例：

- PostgreSQL 索引 SQL 生成契约：必须包含 `CONCURRENTLY`，且执行器不开启事务。
- 启动迁移发现已有大表缺索引时不调用 `CreateIndex`，但返回可观测的缺失索引结果。
- 建表失败、补列失败、catalog 检查失败均向调用方返回错误。
- 缺索引不使结构闸门失败，且最终状态不能记录为 “up to date”。
- 列类型、长度、可空性、默认值的匹配与漂移分支。
- `migrateDB` 与 `migrateDBFast` 对单库日志表调用相同的结构闸门。

### 11.6 Main Chain Impact

- relay 请求协程中不新增任何同步代码；所有检查仍只在启动阶段执行。
- 启动不再向已有 `logs` 大表发普通 `CREATE INDEX`，消除其 `SHARE` 锁阻塞日志写入的
  风险。
- 启动不发起后台索引构建，避免与 relay 共用日志数据库的 I/O、连接池和检查点带宽。
- 共享资源仅为 `logs` catalog 与启动期必要的表/列 DDL；Redis、内存缓存和 goroutine
  池均无新增使用。

本节取代 §3 目标 2、§4.2、§6 对索引自动创建和 DDL 放行策略的描述。实际实现位于
`model/log_migrate.go`：表/列 DDL 与索引检测已经拆分，PostgreSQL 只设置
`lock_timeout`、不再设置 3 秒 `statement_timeout`；`migrateDB` 与 `migrateDBFast`
通过 `migrateMainLogTableIfNeeded` 复用同一入口。

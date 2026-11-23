# 错误日志清理：有界扫描与超时恢复

日期：2026-11-21。状态：用户已确认设计，源码实现完成；本地验证结果见[验收报告](error-log-cleanup-bounded-scan-test-20261121.md)。尚未部署。

用户要求优化错误日志清理，**不加表、不加索引**。本方案同时保持现有列、任务 JSON 格式和配置项，聚焦生产已经报错的 `id` 扫描及后台批次恢复。依据：[现有实现设计](error-log-cleanup-existing-indexes.md)、[6000 万条日志测试报告](error-log-cleanup-scale-test-20261121.md)。

## Goals and Scope

- 把一次主键扫描限制在有限 ID 区间内，避免为了找到第一条可见记录遍历巨大的已删除前缀。
- 分开限制读取候选数和实际删除数，提高错误比例较低或混合分布时的清理效率。
- 单批超时允许有限次数缩量重试，继续沿用持久化断点和“至少已删除”的计数。
- 保护消费、管理等其他类型日志，以及截止日期之后、负时间和快照上限之外的记录。

默认删除上限仍为每批 100 条、批间等待 200 ms、单批期限 3 秒、任务期限 1800 秒。总任务期限到达或持续故障时仍保留进度供继续清理。该优化降低单次工作量并处理短暂超时，不能保证数据库长期锁阻塞、磁盘饱和或连接故障时仍能完成。

## Existing Logic and Related-Path Findings

以下为设计确认前对实现的调查；原有行为已经验证，本次变更在后文及验收报告中说明：

| 代码位置 | 当前行为及本次影响 |
| --- | --- |
| `router/api-router.go`、`controller/data_cleanup.go` | `/api/data-cleanup` 使用 AdminAuth；错误日志按 `operations.logs` 的 View/Edit 权限授权。创建直接传 `kind,before`，继续传 `preview_id,resume`；查询返回清理凭据及 `can_resume`。 |
| `service/data_cleanup.go:StartErrorLogCleanup` | 验证至少保留最近 24 小时，只捕获 `max_id`，在主库事务内建立已有清理凭据及任务。创建请求不扫描候选、不计数。 |
| `submitDataCleanup`、`prepareErrorLogCleanupTask` | 全量日志、错误日志和财务清理共用 `active_key=log_cleanup`。扫描计划在第一批删除前保存到任务 JSON；继续复制原计划并沿用原日期、快照和游标。 |
| `model/error_log_cleanup.go:errorLogCleanupIndexes` | 识别已有 `(type,created_at,id)`、`(created_at,id)` 和单列主键。仅有 `(created_at,type)` 时仍使用 `id`。识别过程只读元数据。 |
| `scanErrorLogCleanupCandidates` | `id` 模式的上界直接为整个任务的 `max_id`，`LIMIT` 只限制返回行数。生产日志显示该 SELECT 在约 3 秒超时。 |
| `deleteErrorLogCleanupBatch` | 每批先读至多 `batch_size` 条候选，再用类型、时间和快照条件 DELETE；消费记录也会占用主键模式的读取名额。只有事务返回成功才推进内存游标。 |
| `service/data_cleanup.go:dataCleanupHandler.Run` | 任意批次错误立即失败；`batch.Scanned == 0` 立即判定完成。后者不适用于“当前 ID 区间为空，但后面还有数据”的新算法。 |
| `model/data_cleanup.go:UpdateLeasedDataCleanupRun`、`FinishDataCleanupRun` | 进度和终态写入校验当前任务、runner 和租约。日志删除与主库进度不具跨库原子性；终态更新只保存状态，并不补写失败的游标。 |
| `service/system_task.go:runWithLeaseHeartbeat` | 心跳独立续租，丢失租约会取消 handler 的父 context。批间等待及重试需要响应这个取消。 |
| `model/system_task.go:UpdateSystemTaskState` | 当前逐批状态写入没有调用方 context；本次为错误日志 worker 补充带 context 的入口，保留其他调用方的原入口。 |
| `model/relay_log_pipeline.go` | 异步日志写入使用同一张 `logs` 表。完整状态 getter 还会加队列锁、遍历重试队列和读取回放文件，不适合直接加入每批清理循环。 |
| `web/src/features/system-settings/maintenance/data-cleanup.tsx` | 每 1.5 秒轮询活跃任务；界面使用清理凭据显示已扫描数、删除数下限和原截止日期。已有失败继续入口，无需增加 UI 状态或文案。 |

已有 model/service 测试覆盖三种扫描模式、快照保护、续跑所有权、计划 JSON、索引失效、租约隔离及无 schema 变化。现有部分测试把“扫描数不超过删除配置”作为合同，需要只针对新主键扫描更新断言，同时补上空区间后继续扫描的回归。

已完成的规模实验在仅主键的 6000 万表上通过 700 个原有批次；另在约 948 万条无法回收的已删除前缀上复现首批 3 秒超时。并发实验还捕获提交已生效、客户端因超时未确认的批次。**这些是旧实现的实验结果，不是本方案已经实现或通过的证据。**生产真实索引、死元组及磁盘等待仍未知；本方案不依赖先确认这些信息才能实现主键扫描优化。

## Data Flow and API Contracts

1. 管理员确认截止时间，`POST /api/data-cleanup` 接收 `{kind:"error_logs",before:<Unix秒>}`。保持现有授权、日期校验、快照捕获和原子任务创建。
2. worker 领取任务并恢复原计划，通过独立的单连接日志维护池执行有界批次。
3. 每次尝试前检查启用开关、父 context、任务期限和租约；日志事务成功后保存主库凭据，再开始下一批。
4. 完成或停止通过既有终态事务释放任务互斥。`GET /api/data-cleanup?kind=error_logs&id=...` 返回现有 `{success,message,data}`，`data` 继续包含 `id,kind,before,matched,count_exact,deleted,scanned,status,error_key,can_resume`。
5. 失败后原创建人可用 `{preview_id:<凭据ID>,resume:true}` 继续，保持原范围。旧 `POST /api/data-cleanup/preview` 兼容有限预览；财务及旧 `data_cleanup` 类型保留原处理路径。

不新增接口、权限、前端状态或用户可见错误字符串。运行中的退避仍显示后台清理进行中；超过恢复上限后沿用停止和继续清理提示。

## Data Model and Index Access

应用不执行新增表、列或索引的 DDL，不改 GORM 持久化标签。继续使用 `data_cleanup_runs` 的 `max_id,cursor_id,cursor_at,scanned,deleted` 及 `system_tasks.payload` 中既有扫描模式和终点。

`DataCleanupBatch` 增加仅用于 Go 返回值的 `Done`，明确区分“本区间没有候选”和“整个范围完成”；它不是数据库模型，也不进入 HTTP 响应。扫描上限、ID 跨度、失败次数和恢复计数仅保存在当前 worker 内存中，重启后可以保守恢复默认值。

已有 `error_time`、`time` 模式保持原索引访问及坐标规则，只接入完成标记和超时恢复。`id` 模式继续使用主键，因此旧失败任务可直接从原 `cursor_id` 获益，无须重新开始或转换坐标。`(created_at,type)` 不在本轮转换成新的时间游标：缺少跨库一致的 ID 排序能力，切换已执行任务还需要新的坐标转换规则。

主键候选查询形状为：

```sql
SELECT id, created_at, type
FROM logs
WHERE id > :cursor_id AND id <= :window_end
ORDER BY id
LIMIT :scan_limit;
```

默认 `window_end = cursor_id + min(65536, max_id - cursor_id)`；先验证 `0 <= cursor_id <= max_id`，通过差值计算避免相加溢出。`scan_limit = min(10 * delete_limit, 5000)`，默认读取至多 1000 条窄投影，实际删除至多 100 条。只读 `id,created_at,type`，不读日志正文。

这里保留主键扫描先读取候选、再验证时间的做法：ID 和时间不保证同序，不能根据某条较新记录跳过后面的旧时间记录。有限 ID 区间约束一次访问范围，类型/时间在应用筛选和最终 DELETE 中都检查；不把无时间索引的整段历史类型/时间过滤作为访问入口。

实际 DELETE 继续按至多 `delete_limit` 个 ID 点查，并再次检查 `type=LogTypeError`、`created_at>=0`、`created_at<before`、`id<=max_id`。实现时发现 SQLite 对普通类型/时间谓词可能选择大范围时间索引，因此最终 SQL 将这两列写成 `(type + 0)`、`(created_at + 0)` 的剩余检查条件，让 ID 集合决定访问范围；整数比较和 NULL 语义保持一致。真实三库对实际生成的 SELECT/DELETE 执行 EXPLAIN，均使用已有主键，无排序、Bitmap 或全表扫描，未新增索引。

## Batch Logic and Boundaries

顺序处理查询返回的候选，消费及其他受保护行只推进扫描位置；遇到符合条件的错误行则加入本批删除列表。达到删除上限时停在最后一个已处理候选，查询预读但尚未处理的尾部留给下一批，不能越过尚未删除的错误记录。

游标及完成规则：

- 达到删除上限，或读取数量达到 `scan_limit`：推进到最后已处理的 ID，后续继续。
- 返回数少于 `scan_limit`，且所有返回行均已处理：本 ID 区间已扫描完，推进至 `window_end`；包括返回零行的情况。
- 只有 `cursor_id >= max_id` 才算 `id` 模式完成。`Scanned == 0` 本身不能作为完成条件。
- `Scanned` 累加真正处理的可见候选数；空洞跨度不冒充记录数，未处理的预读尾部不提前计数。空区间可以不增加计数但仍保存新的断点。
- 先提交日志事务，再发布内存游标；主库确认保存该断点之后才能继续下一批。即使当前区间为空也等待既有批间间隔，避免空洞形成查询忙循环。

同秒多条、时间乱序、负时间、截止时间相等、快照终点已被删除，以及最后一条是受保护记录，都按上述规则处理。沿用现有最大 ID 快照语义：快照之后生成且 ID 大于上限的新增和回填日志保留；不把 `MAX(id)` 宣称为跨数据库的完整事务时间快照。

旧预览在 `id` 模式也使用有界 ID 区间、至多 1001 条候选，但不推进执行游标。只有已覆盖至 `max_id` 且未触及预览返回上限才能标记 `count_exact=true`；第一个空区间之后可能仍有错误时只能报告非精确零。匹配计数保留每组最多 400 个 ID 的 SQL 点查，并使用相同的剩余检查条件，避免把 SQL NULL 时间误当成 Go 的零值时间。直接创建流程仍只捕获快照。

## Timeout Recovery and Error Handling

只对错误日志 worker 的可恢复超时执行有限重试，财务事务不复用这一新重试分支。

- 单个日志批次最多尝试 4 次（首次加 3 次重试），每次重新建立配置规定的批次 context。捕获期限状态必须发生在调用 `cancel()` 之前。
- 仅在父 context 仍有效、清理仍启用、租约仍有效时，重试本批 deadline 或可识别的传输超时。缺索引、参数错误、SQL/权限错误及无法分类的错误直接停止；不按错误字符串模糊匹配，更不无限重试。
- 三次退避分别为 `max(batch_sleep_ms,500/1000/2000 ms)`，等待可被父 context 取消。每次实际 DELETE 前重新校验租约。
- 日志批次超时后，删除上限减半至至少 1，读取上限由删除上限重新计算，ID 跨度减半至至少 1024。只缩小下一次工作量，不推进失败批次的游标、不改变 cutoff/max_id、不延长单批和总任务期限。
- 连续 10 个批次成功且各自耗时低于批次期限一半，才将工作量翻倍恢复，最高为初始配置和上述扫描上限；再次超时清零恢复计数。
- 日志事务已经返回成功、主库保存进度却超时时，仅重试同一份绝对游标/计数更新，最多同样 4 次，不重做 DELETE，不进入下一批。每次更新仍经过 `UpdateLeasedDataCleanupRun` 的 fencing 条件。保存结果不明确时重复赋相同值是幂等的。
- COMMIT 超时属于结果不明确：从未推进的旧游标重试，DELETE 保持幂等，计数保持下限。不得假定这些事务均已回滚，也不得把一次未确认删除直接计入成功数。

为逐批 `UpdateSystemTaskState` 增加接收 context 的模型入口，错误日志 worker 使用独立 3 秒子期限；原入口委托给该实现并保持既有调用语义。状态写入失败时不开启下一次日志事务。心跳及任务所有权仍由既有系统任务机制负责。

内部错误保留可解包的底层原因，并记录阶段（开始事务、候选读取、删除、提交确认、保存进度）、模式、批次上限、耗时及重试次数。不记录 DSN、日志正文或凭据。重试日志只在失败尝试产生，不逐条打印候选；最终停止沿用 `cleanup.failed` / `cleanup.index_missing` 国际化提示。

## Config and Implementation Files

复用 `data_cleanup_setting` 的五个既有配置，读取/删除上限之间的倍数、ID 跨度和有限恢复策略为内部安全边界，不增加环境变量或用户配置。

- `model/error_log_cleanup.go`：主键有界候选页、三字段投影、独立读删限额、空区间推进、事务阶段错误及有限预览。提供显式批次选项供 worker 缩量；通用入口继续提供默认选项。
- `model/data_cleanup.go`：`DataCleanupBatch.Done` 和默认选项转发；不修改表字段/标签及财务删除逻辑。
- `model/system_task.go`：带 context 的状态写入入口，沿用原租约校验和 MySQL 无变化更新检查。
- `service/data_cleanup.go`：错误日志的批次完成判断、分阶段有限重试、缩量恢复和确认保存进度后再执行下一批。
- 对应 model/service/controller 清理测试：覆盖新边界，保留权限、快照、互斥及财务合同；不修改 upstream 所有的测试文件。

## Main Chain Impact

relay goroutine 每请求新增同步操作为 0。扫描、删除、重试和进度保存均在管理任务 worker；使用已有启用开关。每次 DELETE 的数量上限不增加。错误占比低时，相同时间内可能读更多候选、删更多错误，因此必须实际测量同表写入尾延迟，不能以独立连接池宣称无共享 I/O 影响。

## Shared Resource Audit

| 资源 | 与 relay 的关系 | 处理 |
| --- | --- | --- |
| 日志表及主键、已有其他索引 | 异步日志 writer 使用 | 单个有界读取、至多配置条数的点删除、单连接、有限期限、批间等待及超时缩量；不进行 DDL 或自动 VACUUM。 |
| 磁盘、WAL/redo/undo、数据库缓存 | 共享 | 并发写入验收记录 P95/P99/最大延迟；保留失败证据，持续压力下停止并保存断点。 |
| 日志连接池 | 与 writer 池隔离 | 复用 `OpenDataCleanupDB`，维护池最多 1 连接；每次重试不额外新建并行池。 |
| 主库 `data_cleanup_runs/system_tasks/system_task_locks` | 管理任务资源 | 沿用已有主键、active_key 和租约更新；进度使用绝对值幂等保存。 |
| Redis、日志队列及 relay 缓存 | 本次不增加访问 | 不从清理循环调用完整日志队列状态 getter，避免共享队列锁与文件遍历。 |
| worker、内存 | 同进程 | 复用已有任务 worker 和心跳；最多 5000 个窄候选，无新增逐请求 goroutine 或长期候选缓存。 |

## Concurrency Analysis

30k/100k RPM 的 relay 主链每请求新增 DB/Redis 调用、锁、goroutine 均为 0。后台主键模式正常批次为 1 次候选 SELECT、0 或 1 次 DELETE；PostgreSQL 沿用 1 次事务局部规划设置；主库沿用状态/租约校验与进度保存。空区间也占用一个批次和等待，不新增无界循环。

默认下扫描候选的节奏上限从约 500 条/秒变为约 5000 条/秒，删除节奏上限仍约 500 条/秒，均是忽略 SQL、事务、进度写入和预读的理论上限。错误比例高时主要收益是减少消费记录占用删除批次；持续吞吐和整表耗时以实现后的真实提交测试为准。

## Verification and Handoff

已查阅本地生产环境记录及发布检查表。生产为 MySQL 主库、PostgreSQL 日志库，具体版本及硬件未知；用户日增至少 600 万、错误比例未知。实现验收使用本次独立夹具，不连接生产；测试造表仅存在于隔离库，应用变更保持零新增 schema。

1. 正确性：仅主键/已有时间索引/已有专用索引；同秒、乱序、日期边界、负时间、消费保护、快照后新增及回填。核对保留 ID 集合和校验和，不只比较删除数量。
2. 主键空洞：连续多个空区间、首个空区间后有错误、极大 ID 间隙、最后一行已删、只有受保护记录；断言空区间后不误报完成、断点单调、计数不按 ID 跨度虚增。
3. 满批：查询读到的错误多于删除上限，验证未处理尾部下批仍被删除；不同错误密度和连续爆发后稀疏分布都不漏删。删除上限始终不超配置。
4. 恢复：短暂表锁/慢查询触发超时后解除；持续故障达到尝试上限；等待期间禁用、取消或失租；缩量和恢复边界。进度保存超时后只重写同一凭据；重试中旧 worker 不能覆盖续跑结果。
5. 提交不明确：通过故障注入覆盖“删除已提交、返回失败”，核对幂等重扫和计数下限；真实慢提交作为附加证据，不依赖每次压测都能自然复现。
6. 旧计划、旧预览、仅主键大空前缀的预览非精确零、直接创建不扫描候选、共同 active_key 冲突、终态释放及财务原路径回归。
7. 真实 SQLite、MySQL、PostgreSQL 运行相关模型/服务/控制器回归；包含最低支持范围的 MySQL 5.7 / PostgreSQL 9.6。保存版本、命令、EXPLAIN 和未执行项。对已有表/索引集合前后核对，重复启动相关迁移确认本次零 DDL。
8. 规模：PG 实际 600 万、6000 万；错误比例 30%/50%/80% 为重点，补 0.1%/1% 和爆发；仅主键及已有索引夹具。重新制造无法回收的删除前缀，在不运行 VACUUM 的条件下比较新旧首批及跨空区间行为。
9. 真实提交并发：按既有 70/140/500 条新日志每秒测试，记录清理吞吐、扫描/删除/提交/进度各阶段及读写 P95/P99/最大延迟；包含完成尾部及保存断点后续跑。不能把回滚微基准当成吞吐，也不能把 SQL 负载当成完整 relay 容量验收。

实现已按用户确认完成，没有新增表、列、索引、配置或前端文案。SQLite、MySQL 5.7、PostgreSQL 9.6/18 已运行真实数据库回归；6000 万规模、无法回收的删除前缀、真实提交并发及无错误尾段的结果和限制分别记入[验收报告](error-log-cleanup-bounded-scan-test-20261121.md)。没有连接生产、发布或执行生产维护操作。

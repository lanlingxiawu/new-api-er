# 错误日志清理复用现有索引

日期：2026-11-12。状态：用户在索引评估和优化方案之后明确要求“开始优化这个功能”；已实现并完成下述本地验证，尚未部署生产。

## 目标和范围

生产日志表很大，未建立 `idx_cleanup_created_id`。错误日志清理不再按固定索引名拒绝请求，而是复用可用索引：优先 `(type,created_at,id)`，其次 `(created_at,id)`，最后 `id` 主键。索引按列结构检查；读取元数据失败必须报错，不能伪装成缺索引。部分索引不作为通用候选，PostgreSQL 无效索引不采用。

仅优化 `error_logs`。台账、提成、全类型历史日志清理的权限与删除语义不变。只删除 `type=5` 且 `0 <= created_at < before`、`id <= max_id` 的记录。保留消费/审计记录、汇总、余额和永久去重凭据。ClickHouse 仍返回暂不支持。

## 数据流和扫描算法

1. 错误日志入口改为选择截止日期、确认删除后直接提交。服务端校验权限、开关和 24 小时保留底线，仅通过独立单连接维护池读取最大日志 ID；主库在一个事务内创建清理记录和后台任务。请求阶段不扫描候选、不计数、不生成独立预览记录。财务清理和旧版客户端的预览 API 保持兼容。
2. 按实际索引列选择 `error_time` / `time` / `id` 策略；不在启动或预览中创建、删除日志索引。
3. 最大 ID 在直接确认请求（兼容流程为预览请求）中捕获。正式任务首次执行时选择策略；时间策略用倒序索引读取截止日期之前的最后一个 `(created_at,id)`，固定扫描终点并保存到任务 JSON，始终保留已捕获的最大 ID 删除保护。取终点不附加 `id<=max_id` 或其他后置过滤，避免为找一条终点扫描大量记录。
4. 兼容的旧预览 API 最多读取 1,001 条候选，随后按这批 ID 判断错误类型、日期和快照边界。未耗尽区间时 `count_exact=false`；零匹配仍表示“本次预览未发现，完整扫描可能发现更多”，不能表示整表无匹配。新错误日志界面不调用该 API。
5. 用户确认后创建后台任务。每批最多读取配置指定的候选行，`time` 策略的候选 SELECT 不带 `type=5` 或 `id<=max_id`，避免为了凑满匹配行无限扫描。`error_time` 策略可以带作为索引首列等值的 `type=5`。候选只取 `id,created_at`。
6. 时间游标拆为同一秒 `created_at=cursor_at AND id>cursor_id` 和后续时间 `created_at>cursor_at` 两个有序查询，最多两次 SELECT；避免 OR 游标导致从历史起点重扫。按固定终点裁剪最后一批。主键策略直接按 `cursor_id < id <= max_id` 取候选，不在 SELECT 加时间/类型过滤。
7. 用候选 ID 做有完整保护条件的 DELETE。游标推进到最后一个扫描候选，即使该批没有删除也必须持久化进度。仅候选耗尽才能完成任务，删除数为零不能作为完成条件。
8. 保留小事务、单连接池、批间等待、每批超时、任务总时限、租约检查和与其他日志清理的互斥。独立池隔离连接名额，不能隔离磁盘 I/O。

实测 PostgreSQL 新建表尚无统计信息时会选择 Bitmap Heap Scan + Sort，不能以 LIMIT 保证小批读取。因此预览和删除的维护事务使用事务局部 `set_config` 关闭 seqscan、bitmapscan、sort 的优先选择，促使已有有序索引直接返回候选；事务完成后自动恢复，单连接回归测试确认设置不泄漏。不会修改全局配置或 relay 连接。MySQL 8 不可见索引不作为候选。

## API 契约和权限

复用现有 `/api/data-cleanup` AdminAuth 分组，错误日志仍要求 `operations.logs` Edit/View 及 PAT 的 `ops:write`/`ops:read`，不新增公开路由。

- `POST /preview` 请求不变：`{kind:"error_logs",before:Unix秒}`。返回既有 run 字段，有限预览的 `matched` 是已确认的下界。
- `POST /api/data-cleanup` 支持确认后直接创建错误日志任务：`{kind:"error_logs",before:Unix秒}`。不允许携带 preview_id/resume，不接受财务目标。最大 ID 在本次确认请求中捕获，创建冲突时清理记录和任务一并回滚。
- `POST /api/data-cleanup` 增加可选 `resume` 布尔值：`{preview_id:"run ID",resume:true}` 继续同一错误日志任务。省略时保持原预览确认语义。
- `GET /api/data-cleanup` 增加只读 `can_resume`；仅任务所有者、已停止的新错误日志任务可继续。服务器再次校验所有权和编辑权限。

继续时保留原 cutoff、max_id、扫描终点、策略、游标和累计计数。创建新的 SystemTask 并原子替换 task_id；不接受客户端修改过滤条件。旧任务必须已终止并释放 active_key。已创建的 pending/running 任务重复继续请求直接复用；并发旧快照通过 task_id 比较更新防重，争用方可返回 busy，绝不创建并行清理。成功任务不能继续；财务及旧格式任务维持重新预览流程。若保存的索引策略失效则关闭继续入口，错误日志可重新确认创建任务来选择其他策略。

使用新任务类型 `error_log_cleanup`，旧 worker 不会领取不认识的类型；新 worker 同时保留原 `data_cleanup` 支持。新类型同样排除在普通系统任务查看/清理接口之外。部署应先升级任务 worker 和控制台节点，再创建新任务；不对混合旧控制台版本承诺新交互兼容。

## 数据模型和索引

不增加表、列或索引。扫描策略和终点保存在现有 `system_tasks.payload` JSON 的 `scan_plan` 中；`data_cleanup_runs` 沿用原有 `max_id`、游标和计数字段。内存扫描计划和响应字段 `can_resume` 均由 GORM 忽略。

旧 `data_cleanup` 类型保持旧任务语义。新 `error_log_cleanup` 类型首次执行时选择策略和有限扫描终点，在第一批删除之前通过租约校验持久化到任务 payload；保留确认或预览时的 `max_id`，排除之后插入的记录。继续时把原任务 payload 复制到新任务，恢复同一计划并检查索引，不在中途改变游标坐标。尚未初始化且没有进度的任务可以重新初始化；已有进度却缺失计划的任务拒绝执行。验证三库直接使用原表结构，重复 AutoMigrate 不产生 DDL。

`idx_created_at_type(created_at,type)` 单独存在时不作为跨库时间游标依据：PostgreSQL 不包含 id 排序键，且类型不在前导列；此时使用主键兜底。MySQL 特有优化不作为跨库正确性的前提。

## 配置与错误处理

不增加配置。复用 `data_cleanup_setting` 的 enabled、batch_size（默认100/最大500）、batch_sleep_ms（默认200）、batch_timeout_sec（默认3）、task_timeout_sec（默认1800）。超时保留游标，显示停止及“继续清理”；不自动无限重试。

审计修复：维护连接接收调用方 context，关闭 GORM 的自动探测/自动 Ping，通过 PingContext 在请求或 worker 初始化的 3 秒期限内完成建连。MySQL 跳过版本探测，PostgreSQL 保留 simple protocol，SQLite 保留现有路径；失败关闭专用池，不修改启动与 relay 使用的连接工厂。

所有 worker 对清理记录的运行状态和进度写回均需匹配 run ID、当前 task_id、running 状态、runner 和有效租约；MySQL 无变化更新通过同条件存在性检查确认租约。终态在主库事务中校验租约、完成 SystemTask、更新相同 task_id 的清理记录并释放锁，防止过期 worker 覆盖续跑结果。每次写回有独立的 3 秒上限；日志删除与主库仍不具跨库原子性，最后一批重扫和计数下限语义保持不变。

所有 DB 错误记录底层原因，API 使用既有 cleanup.* 国际化错误，后台异常不传入 relay。日志库 DELETE 与主库进度不具跨库原子性：提交删除后进度保存失败会重复扫描最后一批，但 DELETE 幂等；删除计数可能低估，错误日志始终以“至少已删除”显示累计值，避免续跑成功后伪称精确。

## UI

复用当前按钮区域、ConfirmDialog、React Query 和错误反馈。错误日志点击清理直接打开截止日期确认框，确认前不发删除或预览请求；确认后提交一次创建请求，不显示估算数量。任务显示已扫描数，稀疏错误记录情况下也能看见进度。可继续任务提供“继续清理”，确认框展示原截止日期及从已保存进度继续的说明。失效计划引导开始新清理。提交前取消同目标旧状态查询，避免旧响应覆盖新任务。财务入口保留预览。请求进行中禁止重复提交，关闭页面不停止后台任务。新文案覆盖七个前端语言。

## Main Chain Impact

relay goroutine 新增同步操作为零；全部工作在管理 API 和既有后台任务 worker。无新 relay 中间件、计费钩子或逐请求 goroutine。清理仍受现有 enabled 开关控制。

## Shared Resource Audit

| 资源 | relay 是否使用 | 措施 |
| --- | --- | --- |
| LOG_DB.logs 及现有索引 | 异步日志写入使用 | 不做 DDL；独立单连接维护池；有界候选读取、小批 DELETE、超时和间隔 |
| 主库 data_cleanup_runs | 否 | 管理任务小表，沿用原有列按主键更新游标，无 schema 变更 |
| system_tasks / system_task_locks | 非 relay 管理任务 | 沿用租约和 active_key=log_cleanup 互斥，新类型不增加并发日志清理 |
| Redis、relay 内存缓存和队列 | 本次不访问 | 无新增 key、锁、缓存失效或逐请求分配 |
| goroutine / DB 连接池 | 同进程共享资源 | 复用任务执行器；维护连接上限1，预览同样用维护连接 |

## Concurrency Analysis

30k/100k RPM 下，relay 每请求新增 DB/Redis 调用、锁和 goroutine 均为0。错误日志后台每批最多2次候选 SELECT、1次 DELETE；PostgreSQL 另有1次事务局部规划设置语句；主库沿用租约更新与1次游标更新。默认最多约5批/秒、500候选/秒，真实速度更低。1亿候选至少约55.6小时，需要多次继续清理；不以“有索引”宣称整体扫描成本消失。索引元数据只在预览和任务启动检查，不在每批重复查询。

## 测试设计（实现前）

- 索引等价分区：专用索引、仅旧时间索引、仅主键、同结构不同名、部分/无效索引、元数据错误。查询计划验证不以索引名称存在替代。
- 候选边界：limit=0/1/500/501；同秒多页、0时间、负时间、截止日期前/等于/后；首批无错误但后批有、全部无错误、稀疏错误。
- 快照与游标：乱序时间/ID、快照后新增及回填、时间终点后来被删除、全部候选受保护、最后一批、游标不倒退。
- 生命周期：预览只读、1001候选内零匹配不报精确零；正常完成、超时/取消/DB失败保留进度；继续所有权、财务拒绝、成功拒绝、过期旧预览、重复继续、与全日志清理冲突、旧任务仍运行。
- 不变式：只删错误日志；消费、审计、统计、财务表不改；删除失败回滚且游标不前进；进度写失败重扫不漏删。
- 审计回归：旧 worker 在删除后、写回前过期，续跑已完成后不能覆盖游标/计数/状态；失效租约不能写终态；活跃租约重复无变化写回有效；数据库握手阻塞及预先取消的 context 能终止维护连接初始化。
- 直接创建：确认前无请求、确认后单次请求；无 COUNT/候选扫描，快照后新增日志保留；日期/权限/目标/混合参数拒绝；全局互斥冲突不留下孤立清理记录；财务预览与旧预览确认继续可用。
- 前端：有限零匹配提示；扫描进度；失败任务继续确认原日期；提交中禁用；错误反馈；其他清理不显示继续按钮。
- SQLite/MySQL/PostgreSQL 实例：无新增列、原表重复迁移零 DDL、上述行为、真实 EXPLAIN（非生产 DELETE ANALYZE）。验证任务 JSON 持久化、重新加载、续跑复制、租约失效不能保存、已有进度但丢失计划时拒绝重新初始化。记录版本与命令。前端运行相关 Vitest、typecheck、lint。不能运行的项如实记录。

## 验证结果

测试仅使用内存 SQLite 和本次创建的独立 Docker MySQL/PostgreSQL 实例。外部方言模型测试继续使用随机表前缀或独立 schema；服务工作流测试增加 `CLEANUP_WORKFLOW_TEST_DSN`，每个用例创建独立数据库/schema，主库连接池和维护连接池分开，结束后清理自身对象。

| 实例版本 | 验证结果 |
| --- | --- |
| SQLite 3.50.4 | 三种扫描策略、稀疏预览、失败回滚、游标和终点、索引别名/部分索引、无新增列及重复迁移无 DDL、JSON 计划恢复通过 |
| MySQL 5.7.44 | 无新增列及重复迁移无 DDL；模型行为和真实服务预览/启动/JSON 保存与恢复/停止后继续/所有权/任务互斥通过 |
| MySQL 8.0.46 | 改存 JSON 前已验证扫描逻辑和不可见时间索引回退；本次未重复验证该版本的 JSON 流程 |
| PostgreSQL 9.6.24 | 无新增列及重复迁移无 DDL；模型行为和真实服务 JSON 保存与恢复、续跑工作流通过 |
| PostgreSQL 17.11 | 改存 JSON 前已验证扫描逻辑和事务局部规划设置恢复；本次未重复验证该版本的 JSON 流程 |

代表性候选查询（12,000 条合成数据、旧时间索引、100 条批次）：SQLite 使用 `idx_created_at_id` 覆盖范围扫描；MySQL 5.7/8.0 为 `range` + `Using index`，无 filesort；PostgreSQL 9.6/17 为该索引的 Index Only Scan，无 Bitmap/Sort。此结果不代表生产所有数据分布的实际耗时。

执行命令（从仓库根目录；DSN 均为隔离测试实例，不得指向业务库）：

```powershell
# 不设 CLEANUP_TEST_DSN 时模型清理夹具使用私有 SQLite。
go test ./model -run '^TestErrorLogCleanup' -count=1 -v
# 对上表各 MySQL/PostgreSQL 版本分别设置后执行相同测试。
$env:CLEANUP_TEST_DSN = '<isolated test database DSN>'
go test ./model -run '^TestErrorLogCleanup' -count=1 -v
# 无 schema 变更与不可见索引检查。
go test ./model -run 'TestErrorLogCleanup(NoSchemaChange|InvisibleMySQLIndex)' -count=1 -v

# service TestMain 使用独立测试主库/日志库；其内部再建立隔离 schema/database。
$env:SQL_DSN = '<isolated MySQL test DSN with charset=utf8mb4>'
$env:LOG_SQL_DSN = '<isolated PostgreSQL test DSN>'
go test ./model ./service ./controller -count=1
# 以下分别以 MySQL 与 PostgreSQL DSN 执行。
$env:CLEANUP_WORKFLOW_TEST_DSN = '<isolated workflow test DSN>'
go test ./service -run 'Test(ErrorLogCleanup|DataCleanup|FinancialCleanup)' -count=1 -v
go test ./controller -run 'Test(DataCleanup|ErrorLogCleanup)' -count=1
go build ./...
```

改存 JSON 前三个后端包完整回归通过（model 78s、service 67s、controller 236s）。取消新增列后执行 `go test ./model ./service ./controller -run 'Test(ErrorLogCleanup|DataCleanup|FinancialCleanup)' -count=1`，SQLite 夹具和控制器通过；分别设置 `CLEANUP_TEST_DSN`、`CLEANUP_WORKFLOW_TEST_DSN` 为隔离 MySQL 5.7 / PostgreSQL 9.6 后，相同范围的模型与服务测试通过。新增检查覆盖无 DDL、JSON 持久化/重新加载/续跑复制、失效租约不能写入、丢失计划不能重置已有进度、预览后新增日志保护。跨库回归中修正测试表前缀过长的问题，保留随机隔离和清理。详细输出为 `_local/cleanup-json-{sqlite,mysql,pg}.log`。最终 `go build ./...` 通过。源代码差异未修改 relay 或上游所有的前端认证模块。

前端在 `web/` 执行：

- `bun run test src/features/system-settings/maintenance/__tests__/data-cleanup.test.tsx`：5/5 通过。
- `bun run typecheck`：现有脚本调用 `bun check -b`，本机 Bun 1.3.13 不支持该命令；使用项目已安装的 TypeScript 原生检查器 `bunx --no-install tsgo -b`，检查通过（7.0.0-dev.20260707.2）。未改项目包脚本。
- `bun run lint`：通过，只有其他文件原有警告；改动组件与 API 的 oxfmt 检查通过。
- `bun run build`：通过。
- 五个新文案在七个语言文件均存在，占位符保留，UTF-8 无 BOM；`bun run i18n:sync` 缺失键数为零。临时翻译脚本已移除。

本地详细输出在 `_local/cleanup-opt-*.log`。未连接生产数据库，未对生产日志执行 DDL/删除，也未做目标服务器 schema 副本的部署演练或 30k/100k RPM 压测。本次优化不需要数据库字段或索引迁移；部署应先升级任务 worker 和控制台节点。

### 审计修复与交互简化复验（2026-11-12）

- 直接创建、旧预览兼容、快照后新增日志保护、互斥冲突原子回滚、失效租约写回与终态保护、终态凭据失败整体回滚已验证。过期 worker 的复现现在保持 `cursor=250, scanned=150, deleted=150, status=succeeded`，不会回退到旧批次。
- `go test ./model ./service ./controller -run 'Test(ErrorLogCleanup|DataCleanup|FinancialCleanup)' -count=1`：SQLite 夹具及控制器通过，输出 `_local/cleanup-fix-sqlite.log`。
- 设置隔离 MySQL 5.7.44 的 `SQL_DSN/CLEANUP_TEST_DSN/CLEANUP_WORKFLOW_TEST_DSN`，及隔离 PostgreSQL 9.6.24 的 `LOG_SQL_DSN`，执行上述模型与服务范围通过；将两个 CLEANUP 变量切换为 PostgreSQL DSN 后同样通过。输出 `_local/cleanup-fix-{mysql,pg}.log`。两库均确认不新增列、重复 AutoMigrate 无 DDL；维护连接的 PostgreSQL simple protocol 保持启用。
- MySQL 和 PostgreSQL 的本机无响应握手测试均在调用方 1 秒 context 到期后返回；预先取消的 context 不再尝试建连。SQLite 初始化探测临时传入调用方 context，成功后恢复独立池，后续批次不继承已取消的初始化 context。
- `go test ./model ./service ./controller -count=1`：完整回归通过（83s / 75s / 229s）。本次主库使用独立 MySQL，日志库使用独立 PostgreSQL；真实工作流夹具使用独立 MySQL 数据库。输出 `_local/cleanup-fix-full.log`。
- 前端 `bun run test src/features/system-settings/maintenance/__tests__/data-cleanup.test.tsx`：8/8 通过，覆盖确认前零请求、单次直接提交、固定确认日期、恢复和续跑、提交中禁用、失败重试、财务预览兼容。`bun run lint` 通过（只有其他文件原有警告），oxfmt 检查通过；现有 `bun check -b` 脚本仍不适用于本机，替代的 `bunx --no-install tsgo -b` 通过。
- 七种语言的新提示通过临时 `add-missing-keys.mjs` 写入并运行 `bun run i18n:sync`，缺失键为零；临时翻译脚本已删除。`bun run build` 与更新嵌入资源后的 `go build ./...` 均通过。

本次测试仅操作本次创建的独立 Docker 实例和私有 SQLite。尚未部署生产，未做真实业务数据清理或负载测试。

## 2026-11-19：历史日志清理入口的共享互斥错误修复

### Existing Logic and Related-Path Findings

- `service/data_cleanup.go:submitDataCleanup` 为 `data_cleanup`（台账、提成及旧错误日志）和 `error_log_cleanup` 写入相同的 `active_key=log_cleanup`。该唯一约束是既有跨类型互斥规则，不能删除或改为各类型独立键。
- `service/system_task.go:StartLogCleanupTask` 在创建前及创建失败后都只通过 `model.GetActiveSystemTask` 查询 `type=log_cleanup`。另两种任务持有共享键时，类型查询为空，INSERT 失败，重查仍为空；`controller.CreateLogCleanupSystemTask` 原样返回数据库错误，产生截图中的重复键提示。
- `model.FinishSystemTask`、`MarkSystemTaskLeaseExpired` 和数据清理的终态写入负责释放 `active_key`；不在提交入口强制释放或结束其他任务。
- 普通系统任务 current/detail/list 刻意隐藏财务及错误日志清理记录；历史日志入口不能把另一类型任务当作自己的成功响应，否则会泄露受单独权限控制的清理凭据，并让前端解析错误的进度结构。
- 路由为 `POST /api/system-task/log-cleanup?target_timestamp=<Unix秒>`，保持 AdminAuth 和 `operations.logs` Edit。前端 `history-log-cleanup.tsx` 已能显示 `success=false` 的后端消息，失败后保留确认框供重试，无需改动界面及上游认证模块。

### 修复范围与测试设计

这是既有互斥与错误合同的局部修复，不新增功能、API、配置、表、列、索引或迁移。保持创建前按类型复用已有全日志任务；唯一约束拒绝创建后，按共享 active_key 查找活跃占用者：同类型返回已有任务，其他类型返回既有 `cleanup.busy`。不重写任务的截止日期，不提交第二个清理任务。占用任务正常完成或失败释放键后，再次提交按原流程创建。

控制器复用 `dataCleanupError`：底层错误留在服务端日志，冲突使用已有 en/zh-CN/zh-TW 翻译；未知数据库故障返回 HTTP 500 和 `cleanup.failed`。无效、非正数、溢出的时间戳使用 `cleanup.invalid_date`（HTTP 200 + success=false）。不把另一任务的 payload 或 SQL 错误交给客户端。

实现前新增回归用例覆盖两种占用任务的 pending/running、释放后重试、同类型复用且保留原截止日期；通过真实数据库创建回调确定性地插入竞争者，覆盖首次查询后发生的同类型及跨类型抢占。最终补充成功/失败终态都能重新提交，以及无共享键占用的 task_id 冲突仍保留为数据库故障，不能误判 busy。控制器验证三种后端语言、隐藏其他清理凭据、通用 current 不暴露跨类型任务、数据库故障脱敏及时间戳边界。

### Main Chain Impact

同步变化仅在管理员提交后台任务的错误分支；relay 每请求新增 DB/Redis 调用、锁、goroutine 均为 0。worker、日志删除批次、扫描算法及计费均不变。

### Shared Resource Audit

新增查询只读主库 `system_tasks`，按已有唯一 `active_key` 索引定位至多一条记录；仍使用现有管理 API 连接池，不增加连接或长期事务。检索 `relay/`、`middleware/`、`common/` 未发现访问该任务表的主链路径。复用的 `system_task_locks` 生命周期未改动；不访问 Redis、relay 缓存或新的 worker 池。本次不修改 `logs` 表的访问方式，不新增与日志写入的资源竞争。

### 本次验证

Go 1.26.11，Windows/amd64。数据库均为本次独立 Docker 容器或私有 SQLite；测试夹具按独立数据库/schema 或自身任务 ID 清理。证据目录为 `_local/log-cleanup-conflict-20261119/`，不含提交到版本库的凭据。

| 实例 | 回归与查询计划 |
| --- | --- |
| SQLite 3.50.4 | 共享互斥、确定性竞争、任务释放及重复提交通过；10,000 条历史任务 + 1 条占用任务，查询使用 `idx_system_tasks_active_key` |
| MySQL 26.10.0（utf8mb4） | 清理与任务相关 service/controller 回归通过；最终新增分支通过；EXPLAIN FORMAT=TRADITIONAL 为 `const`、`key=idx_system_tasks_active_key`、`rows=1` |
| PostgreSQL 18.7 | 相同 service/controller 回归及最终分支通过；EXPLAIN 为唯一索引上的 Index Scan，估计 1 行 |

- 修复前的 `before-fix-current.log` 记录原问题复现：跨类型互斥返回原始唯一键错误；控制器提示、故障脱敏、时间戳边界测试失败。首次 MySQL 5.7 夹具未指定服务端 utf8mb4，启动迁移在无关的中文 `tier_group` 默认值处失败，未进入用例；保留 `before-fix.log`，正式验证使用上表的新版本实例和正确字符集。
- `go test ./model -count=1`：完整通过，115.124s；`go test ./service -count=1`：完整通过，88.622s。主库 MySQL，独立日志库 PostgreSQL。
- `go test ./controller -count=1`：完整通过，299.082s。
- 最终定向用例 `go test ./service -run 'TestLogCleanup' -count=1 -v`：分别设置 `CLEANUP_WORKFLOW_TEST_DSN` 为私有 SQLite（变量为空）、隔离 MySQL、隔离 PostgreSQL，均通过。
- 扩展相关回归：`go test -p 1 ./service ./controller -run 'Test(LogCleanup|DataCleanup|ErrorLogCleanup|FinancialCleanup|SysTask|SystemTask|EnqueueSystemTask|CreateLogCleanup)' -count=1 -v`，MySQL 与 PostgreSQL 各通过一次；包括既有数据清理的日期、权限、续跑、租约与实际删除验证。
- 查询计划：`go run ./_local/log-cleanup-conflict-20261119/plan.go <sqlite|mysql|pg>`，外部方言使用专门的 `cleanup_plan` 数据库，10,001 条合成任务；MySQL 另保存传统格式计划。无新增索引，未以此小样本声称生产清理吞吐通过。
- 最终 `go build ./...` 和 `git diff --check` 均通过。

本次不部署、不操作生产数据，未在浏览器或生产环境重放截图中的点击；HTTP 错误合同由真实数据库 + `httptest` 验证。没有 schema 变更，因此不涉及新增迁移的升级或重复 DDL 验收。未触及日志库访问逻辑或 relay，不追加生产删除或负载验收。

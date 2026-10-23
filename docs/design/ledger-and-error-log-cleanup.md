# 按日期清理台账、提成明细和错误使用日志

状态：用户已确认开始实现，本文按实际代码更新。日期：2026-10-23。

## 目标和交互

- 系统设置 → 运行参数 → 台账明细：Root 选择“台账明细”或“提成明细”，选择截止日期和时间，预览后确认清理；两类分别执行。
- 系统设置 → 运维 → 日志维护：具有日志维护权限的管理员选择截止日期和时间，只清理错误使用日志。

日期表示“这个时间之前”，不包含截止时间本身。按浏览器本地时间显示，发送 Unix 秒，后端不重新截断到零点。例如选择 2026-06-01 00:00，只删除早于这一时刻的记录。

财务明细至少保留最近 30 天，同时保护当前提成结算月份；默认选 90 天前。错误日志至少保留最近 24 小时，默认选 30 天前。预览最多扫描 1,001 行，超过上限显示“至少 N 条”，不为准确总数遍历历史大表。

再次确认后才创建任务。后台分批执行，关页不中断，重新打开显示最近任务和已删除数量。失败保留已完成删除，重新预览剩余数据后可发起新任务。不提供一键删除全部数据。

## 范围和业务规则

| 目标 | 删除内容 | 保留内容 |
| --- | --- | --- |
| ledger | consumption_costs 中有效正数 log_id 的过期明细 | 日汇总、钱包、配额、当前月份、无有效日志键的人工调整 |
| commission | employee_commission_logs 中有效正数 log_id 的过期明细 | 提成余额、累计提成、结算、日汇总、人工调整 |
| error_logs | LOG_DB.logs 中严格 type=LogTypeError（5）的过期记录 | 消费、充值、系统及审计日志 |

目标由服务端白名单决定，不能传表名。消费日志即使包含错误文字也不删除。不按 request_id 级联删除，不影响已有全类型历史日志清理的原有语义。

用户再次明确：删除的只是一条条明细记录。清理任务不能删除表或重建表，不能删除、扣减或重算统计汇总；`user_extensions` 中累计、待结算、已结算提成和业绩汇总也必须原样保留。每次只处理所选的一类明细，不连带清理另一类明细。明细列表数量和基于剩余明细的筛选结果会随删除变化，这不代表汇总表被删除。

物理删除无法恢复或再次导出；财务记录留下精简的永久去重凭据。不执行 VACUUM、OPTIMIZE 或表重建，不承诺磁盘文件立即缩小。

支持 SQLite、MySQL、PostgreSQL。**ClickHouse 错误日志清理暂未实现**：预览返回暂不支持，不创建任务、不退回全类型删除。

## API 契约和权限

`router/api-router.go` 新增 `/api/data-cleanup` AdminAuth 分组，controller 按 kind 再校验权限。

| 接口 | 请求 | 权限 |
| --- | --- | --- |
| POST /api/data-cleanup/preview | `{kind:"ledger或commission或error_logs",before:Unix秒}` | 财务 Root；错误日志 operations.logs Edit |
| POST /api/data-cleanup | `{preview_id:"32位随机ID"}` | 同上，预览必须属于当前操作者 |
| GET /api/data-cleanup?kind=ledger&id=可选ID | 指定记录或最近提交任务 | 财务 Root；错误日志 operations.logs View |

个人访问令牌额外要求 ops:write / ops:read，不能绕过角色和设置权限。普通系统任务 list/current/detail 不暴露新类型，普通任务历史删除也排除此类任务。

成功返回 `{"success":true,"message":"","data":...}`。data 含 id、task_id、user_id、kind、before、matched、count_exact、deleted、scanned、status、error_key、created_at、updated_at、expires_at；游标、快照上界、task_sequence 不序列化。尚无任务时 data=null。状态为 preview/pending/running/succeeded/failed。

业务错误用 ApiErrorI18n（HTTP 200 + success=false），鉴权用 401/403；未知数据库错误记录底层原因后返回 500 和安全提示。不会把 SQL/Go 错误返回界面。后端 en/zh-CN/zh-TW、前端七种语言同步维护。

## 数据流和生命周期

1. 预览校验权限、日期、开关、方言和必要索引。财务还检查所有已登记节点均报告支持清理凭据。
2. 捕获目标表最大 ID，按时间索引最多取 1,001 行；财务计数排除无有效 log_id 的记录。将过滤条件、快照、操作者存为 10 分钟有效的服务端预览。随机 ID 只作引用，不接受客户端改过滤条件。
3. 确认时重新鉴权和校验。事务中比较更新 preview→pending，创建 SystemTask，绑定 task_id 和任务自增序号。重复提交已接受预览返回同一任务。
4. 新任务 type=data_cleanup，和既有全日志清理共用唯一 active_key=log_cleanup，防止多节点同时启动重叠清理。复用现有主节点任务领取、租约和 worker。
5. 删除使用独立、最多一个连接的维护池。按 (created_at,id) 游标读取，限制 `0 <= created_at < before AND id <= max_id`。默认扫描 100 行、最多 500 行；保护行也推进游标。
6. 财务在同一事务写永久凭据、删除本批主键、更新游标和计数；失败整批回滚。DELETE 再次带日期、快照和有效 log_id 条件。
7. 错误日志在日志库短事务删除，强制 type=5，进度另外写主库。跨库不具原子性：删除后进程崩溃可能少记计数，失败任务显示“至少已删除 N 条”。
8. 每批前校验任务归属、enabled、节点兼容性及账务队列。存在账务重试或积压超过缓冲上限一半时停止财务清理，稍后重试。批间默认等待 200ms，可取消。
9. 成功、异常、panic、超时均尝试保存结果并结束任务，默认总预算 30 分钟。已领取任务崩溃沿用租约超时失败机制，不自动恢复删除；状态接口反映底层任务失败。

快照后新插入、ID 超出上界的迟到记录不纳入本次操作，下次预览处理。运行凭据长期保存，目前未自动清理过期预览，数量取决于管理员操作频率。

## 永久去重和现有子系统

删除会释放财务 log_id 唯一键，仅靠 Redis TTL 不足。新增 financial_detail_tombstones：复合主键 (kind,log_id)，保存原 created_at 和 run_id，不保存金额、用户或请求内容，不随任务历史删除。

正常异步写入、成本/成对重试、历史回填和直接 CreateCommissionLog 都在事务内调用 PrepareFinancialDetails：**先查仍存在的明细键，再查凭据**。这一顺序避免 READ COMMITTED 下删除事务将行替换成凭据时，两次查询都看不到的窗口。读取失败就让批次失败，不当作未处理继续计提。

成本和提成独立去重；被过滤记录既不重新插入，也不累计对应统计或员工提成。无日志键的人工调整不被删除。原有回填统计落盘方式保留，不新增汇总重建或在线恢复。

首次财务清理前须升级所有写入节点；检查 SystemInstance 的 financial_cleanup_receipts=true，包括仍登记的离线节点。永久退役节点通过已有节点管理移除。首次清理后不能降级到不识别凭据的旧程序；关闭清理开关也不关闭凭据读取。登记检查不能代替禁止未登记旧程序接入数据库的运维措施。

台账和提成列表提示“明细和导出仅包含保留记录，历史汇总保留”。受清理影响的提成列表总数从剩余明细计算，避免拿未删除的日汇总推算分页而出现空页。

台账统计缓存键带清理版本：选重叠范围内 task_sequence 最大的任务，版本含 id/deleted/scanned/status。序号取系统任务自增 ID，避免同秒随机 ID 排序错误。旧异步结果只写旧版本键。导出开始、每页及所有正常出口检查版本，变化则失败提示重试。已经完成的旧导出按原生命周期处理，不强制撤回。

## 数据模型、索引和迁移

- data_cleanup_runs：随机字符串主键、唯一 nullable task_id、操作者、kind、before_time、max_id、游标、预览计数、扫描/删除数、状态/错误、时间戳、task_sequence。before_time 避免 MySQL 保留字 BEFORE。索引 (kind,before_time)、(kind,task_sequence)、created_at。
- financial_detail_tombstones：主键 (kind,log_id)，log_id 禁用自增；created_at、run_id 仅为凭据字段。
- consumption_costs：idx_cost_cleanup_created_id(created_at,id)。
- employee_commission_logs：idx_commission_cleanup_created_id(created_at,id)。
- logs：idx_cleanup_created_id(type,created_at,id)。

扫描只选 id/created_at/log_id，不做 SELECT *、DELETE LIMIT、OFFSET 遍历或 TRUNCATE。凭据按 kind+批量 log_id 点查，每片最多 400 个键。

新表及财务索引接入既有主库迁移。日志维持既有约定：新库建表带索引，**已有 logs 启动只报告缺失索引，不自动给热表建索引**。预览缺索引即拒绝。上线前先在目标 schema 副本演练，再安排在线迁移/维护窗口；财务大表也建议提前建索引，避免启动期长时间 DDL。

日志库示例（仅在索引缺失时执行，使用实际 schema；本次开发未在现有业务表执行）：

```sql
-- PostgreSQL：独立连接，事务外
CREATE INDEX CONCURRENTLY idx_cleanup_created_id ON logs (type, created_at, id);
-- MySQL / InnoDB：先确认在线 DDL 能力
ALTER TABLE logs ADD INDEX idx_cleanup_created_id (type, created_at, id), ALGORITHM=INPLACE, LOCK=NONE;
-- SQLite：维护窗口
CREATE INDEX idx_cleanup_created_id ON logs (type, created_at, id);
```

财务两张表按相同方式建立各自 (created_at,id) 索引。恢复需同时考虑明细、凭据、汇总，不能在线删凭据后直接回填。

## 配置

ConfigManager 注册 setting/operation_setting/data_cleanup_setting.go，使用既有设置 API 持久化，Root 才能写，归入 ledger-detail scope。本次无独立参数编辑表单。

| 参数 | 默认 | 运行时范围 |
| --- | --- | --- |
| data_cleanup_setting.enabled | true | false 阻止新任务，下批前停止现有任务 |
| data_cleanup_setting.batch_size | 100 | 1–500 |
| data_cleanup_setting.batch_sleep_ms | 200 | 100–5000 |
| data_cleanup_setting.batch_timeout_sec | 3 | 1–10 |
| data_cleanup_setting.task_timeout_sec | 1800 | 60–7200 |

日期保留底线为后端常量。任务启动捕获批次参数，每批重新读取 enabled。

## Main Chain Impact

relay goroutine 不增加 DB/Redis 查询、锁或 goroutine；不改 relay 路由、结算计算和流式返回。清理在 SystemTask 后台执行，凭据读取在现有异步财务 writer 中执行。CreateCommissionLog 当前无生产调用方，也加入凭据保护，防止未来入口绕过。

清理有功能开关；永久凭据读取是清理后的正确性要求，不能随开关关闭。后台失败沿用已有重试/降级体系，不向 relay 响应传播。

## Shared Resource Audit

| 资源 | relay 关联 | 隔离和影响 |
| --- | --- | --- |
| 两种财务明细表 | 后台 writer 写 | 独立单连接维护池、历史索引、小事务、批间等待、队列压力退让 |
| logs | 异步日志 writer 写 | 独立维护池、type+时间索引、短事务、与原日志清理互斥 |
| 永久凭据表 | 后台财务 writer 查 | 独立表与复合主键、批量点查，不进 relay goroutine |
| runs、system_tasks/locks、system_instances | 不在 relay 热路径 | 任务/心跳复用现有池，删除使用独立池 |
| 财务缓冲/重试队列 | relay 入队和后台写入 | 只用现有只读压力快照，DB 查询不持有缓冲锁 |
| ledger:stats:v2:* | 管理统计缓存 | 加版本，无 KEYS/全库清空，不改 relay Redis 命名空间 |
| goroutine、文件 | 既有任务和导出使用 | 复用 worker，无每请求 goroutine，不增加清理文件 |

独立池隔离连接配额，但不能消除同一数据库的 I/O、SQLite 写锁竞争。每批限制扫描行数和超时、出现积压即停止，不宣称零影响。不存在新增钱包/渠道/令牌行写入或热路径协调锁。

## Concurrency Analysis

100k RPM（约 1,667 请求/秒）下，每次 relay 新增 DB 调用 0、Redis 调用 0、锁 0、goroutine 0。

后台按每片最多 400 个键点查。成本路径查 live+receipt，且保留原写前成本检查；成对路径分别检查成本与提成。以满批 400 行估算，成对新增约 4×1667/400≈17 次 DB 查询/秒，实际取决于批次配置。不开并行凭据查询，不扩大 writer 数。

清理默认每秒最多启动约 5 批，每批扫描最多 100 行；数据库耗时会进一步降低速度。财务每批主要为 SELECT、凭据 INSERT、DELETE、进度 UPDATE，另有任务和节点检查。不访问钱包，不增加热路径 Redis 锁。

未实测 30k/100k RPM 的尾延迟；静态分析不代替压测。上线前在目标硬件和真实数据分布下检查 EXPLAIN、锁等待、队列积压和 relay p99。

## 测试方案与验证

先写测试，再实现，覆盖日期边界/非法输入、当前月份保护、截止时刻前后、快照后插入、游标推进、错误类型过滤、人工调整保护、事务回滚、重复清理、所有重试/回填入口不恢复明细且不重计汇总、预览所有权和过期、重复确认、任务冲突、老节点拒绝、同秒版本切换及提成分页数量。前端覆盖日期转秒、预览不删除、确认提交、任务恢复。

开发验证使用私有 SQLite、随机前缀 MySQL 表和独立 PostgreSQL schema，清理只移除测试自身对象；没有删除现有业务数据。覆盖新建与模拟旧表缺索引升级、二次迁移无 DDL、日志库查询 EXPLAIN。开发阶段 EXPLAIN 仅用小型测试数据；目标服务器演练与查询计划见后续部署记录。

2026-10-23 验证结果：

- SQLite 3.50.4、MySQL 8.4.4、PostgreSQL 18.3：`go test ./model -run 'TestDataCleanup|TestFinancialCleanup' -count=1`，三种方言通过。外部方言由 CLEANUP_TEST_DSN 选择；测试自动创建隔离表/schema。
- `go test ./model -run '^TestDataCleanupMigrationAndPlan$' -count=1 -v`：三种方言的新库/模拟缺索引升级/第二次迁移无 DDL 通过。小样本 EXPLAIN：SQLite/PG 命中新清理索引；MySQL 空表优化器选择已有 idx_created_at_type 覆盖索引。生产索引选择仍需以真实分布验证。
- 在临时 MySQL 库和 PostgreSQL schema 中运行 model、service、controller 包完整测试，通过；router、i18n、service/settingsaccess、setting/operation_setting 完整测试通过。新增路由的访问令牌规则及相关 middleware 测试通过。
- `go build ./...` 通过；未修改 relaykit 模块。
- `bun run typecheck`、`bun run build` 通过；清理和日志设置前端测试 2 个文件、7 个用例通过。lint 无错误，存在仓库原有警告。
- 额外测试覆盖导出恰好单条命中、提前返回时仍拒绝与清理并发生成的文件。

上线顺序：先在副本演练并补大表索引，主节点完成新表迁移，再升级全部 writer 节点；兼容检查通过后使用清理入口。未执行真实业务清理。ClickHouse 及 30k/100k RPM 压测未完成，不作为已验证项。

## 香港测试服务器部署（2026-10-23）

- 地址：`43.251.102.17`，访问入口 `http://43.251.102.17:3000`；Nginx 转发到应用的 3002 端口。部署版本 `hk-cleanup-20261023-af0f134e5`，Linux amd64 二进制 SHA-256：`e9fe671a93e18ba6a1b321a8d38550ab1962392b17d1116fceb446e0b5bc26be`。
- 升级前备份旧程序、配置、主库完整数据及日志库完整数据。服务器备份目录 `/root/deploy-backups/cleanup-20261023-af0f134e5`，仅 root 可访问；`main-data.sql.gz` 为 60,260,372 字节，`log-data.dump` 为 88,745,277 字节。备份未做完整数据恢复演练。
- 实际数据库为 MySQL 8.0.46 主库、PostgreSQL 14.24 日志库。把服务器实际 schema 导入独立临时库，执行 `python3 -u /root/cleanup-deploy-20261023.py rehearse`，连续启动新程序两次；健康检查通过，规范化后的两次 schema 一致，无实际重复 DDL。既有日志导出缺索引提示属于启动检查建议，不是执行 DDL。
- 对现有财务表提前创建 `(created_at,id)` 索引，对 PostgreSQL 日志表并发创建 `(type,created_at,id)` 索引。历史测试范围 EXPLAIN：MySQL 为 created_at 索引范围扫描，PostgreSQL 为新清理索引的 Index Only Scan；未覆盖所有历史日期分布或高负载查询计划。
- 旧进程正常退出后替换程序；健康检查、部署文件哈希验证通过。当前登记的单个 writer 节点具备 `financial_cleanup_receipts` 能力。
- 执行 `python3 -u /root/cleanup-validation-20261023.py validate`：三类后台清理成功；未登录和普通用户被拒绝；近期日期被拒绝；截止时刻、快照后迟到记录、人工调整及其他日志类型保留；重复提交幂等、任务恢复、永久去重凭据、余额和历史汇总均通过。
- 浏览器实际选择日期、预览命中 1 条、确认删除并刷新恢复结果；提成明细和错误使用日志入口及已完成任务展示正常，页面控制台无错误。测试截止时间为 Unix 120（北京时间 1970-01-01 08:02:00），事先确认该范围没有已有业务记录；只删除专用测试记录。
- 操作入口：系统设置 → 运行参数 → 台账明细（可切换提成明细）；系统设置 → 运维 → 日志维护（清理错误使用日志）。
- 最终 19 项验证通过，公网状态接口返回部署版本。执行 `python3 -u /root/cleanup-validation-20261023.py cleanup` 后，专用账号、登录会话、测试明细、测试任务、测试凭据及两个演练数据库均已移除；临时防火墙规则和演练监听端口已撤销。验证记录保存在备份目录的 `validation-state.json`。未调用真实上游模型，也未做并发负载测试。

## 删除边界复核（2026-10-23）

本轮香港服务器只读核查，没有发起清理、写入测试数据或修改数据库。逐条比较部署前完整备份与当前数据库，以下 7 张表共 9,259 条原有记录全部存在，所有列值一致：

| 表 | 备份记录数 | 当前记录数 | 缺失 / 变化 |
| --- | ---: | ---: | ---: |
| business_daily_stats_coverages | 50 | 50 | 0 / 0 |
| employee_commission_daily_stats | 47 | 47 | 0 / 0 |
| employee_commission_reset_period_daily_stats | 48 | 48 | 0 / 0 |
| employee_customer_commission_daily_stats | 148 | 148 | 0 / 0 |
| platform_channel_daily_stats | 275 | 275 | 0 / 0 |
| quota_data | 8,668 | 8,668 | 0 / 0 |
| user_extensions | 23 | 23 | 0 / 0 |

实际 MySQL 财务明细表与 PostgreSQL logs 没有用户定义的删除触发器，也没有会修改其他表的删除级联外键。当前无待执行/运行中的清理任务；此前验证数据已移除。

新增 `TestDataCleanupOnlyDeletesSelectedDetails`：三类明细使用相同 ID 和历史时间，逐类清理后检查其他类型仍保留、表仍存在；逐列核对 10 类保护对象（4 类日统计、coverage、批次去重凭据、用量统计、用户余额、提成汇总、员工档案）；任意表名或非法 kind 均拒绝。

`go test ./model -run 'TestDataCleanup|TestFinancialCleanup' -count=1 -v` 在 SQLite 3.50.4 上 8 个顶层用例全部通过。本轮本地没有 SQL_DSN，Docker 数据库服务不可用，service 包测试被其 TestMain 阻止启动；未把它记录为通过，也未重新执行 MySQL/PostgreSQL 写入测试。之前部署阶段真实数据库功能验证保留为单独证据。本轮仅增加回归测试与核查记录，未改运行代码或重新部署。

### 明细统计调用链与待修问题

- 在用：台账明细 `/api/admin/employee/consumption-cost-ledger/stats` 在 `consumption_costs` 上执行 COUNT/SUM，因此明细筛选合计随清理改变；提成列表在有清理记录后按剩余 `employee_commission_logs` 计数。
- 在用：使用日志及员工客户日志的列表总数来自 logs；消费额、RPM、TPM 查询限定 `type=LogTypeConsume`，本次只删 type=5 不改变消费类统计。
- 通用函数仍有回退分支：`GetConsumptionCostByChannelWithPlan`、`GetCommissionStatsByEmployeeWithPlan`、`GetCommissionTotalsWithPlan` 遇到 UncoveredRanges/DetailRanges 会直接聚合财务明细。当前业务概览明确传入 DailyOnly 计划，不进入这些分支；`GetConsumptionCostTotals`、`GetConsumptionCostByChannel`、`GetCommissionTotals` 的通用入口在当前仓库未查到业务接口调用，不能把它们描述成当前概览的执行路径。
- 待修：错误日志清理没有使 `logstat` 小时统计缓存失效。无额外筛选时，错误日志/全部日志的历史整点计数可能仍命中删除前缓存，列表记录已消失而总数暂时偏大；TTL 默认 48 小时、实际由日志查询设置决定。带额外筛选直接 COUNT 的路径不受该旧缓存影响。此问题影响列表计数一致性，不会删除财务汇总；本轮只记录发现，尚未修补或部署。

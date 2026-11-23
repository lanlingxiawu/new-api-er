# 按日期清理台账、提成明细和错误使用日志

状态：用户已确认开始实现，本文按实际代码更新。日期：2026-10-23。

2026-11-12 错误日志清理优化见 [复用现有索引与断点续跑](error-log-cleanup-existing-indexes.md)。新错误日志任务不再要求固定名称的专用索引，可复用已有时间索引或主键；界面改为选择日期、确认后直接创建后台任务，无独立预览步骤。最大 ID 在确认请求中捕获，不做匹配计数；财务继续保留预览。本文后续历史部署记录仅描述当时版本。

## 目标和交互

- 系统设置 → 运行参数 → 台账明细：Root 选择“台账明细”或“提成明细”，选择截止日期和时间，预览后确认清理；两类分别执行。
- 系统设置 → 运维 → 日志维护：具有日志维护权限的管理员选择截止日期和时间，只清理错误使用日志。

日期表示“这个时间之前”，不包含截止时间本身。按浏览器本地时间显示，发送 Unix 秒，后端不重新截断到零点。例如选择 2026-06-01 00:00，只删除早于这一时刻的记录。

财务明细至少保留最近 3 天（连续 72 小时），台账和提成明细默认均选 3 天前；本月中早于该截止时间的明细也允许清理。错误日志至少保留最近 24 小时，默认选 30 天前。预览最多扫描 1,001 行，超过上限显示“至少 N 条”，不为准确总数遍历历史大表。

确认后才创建任务。后台分批执行，关页不中断，重新打开显示最近任务和已删除数量。失败保留已完成删除；新格式错误日志任务可由原操作者确认后从已保存的游标继续，财务和旧格式任务需重新预览。错误日志索引策略失效时重新确认创建任务，选择可用策略。不提供一键删除全部数据。

## 范围和业务规则

| 目标 | 删除内容 | 保留内容 |
| --- | --- | --- |
| ledger | consumption_costs 中有效正数 log_id 的过期明细 | 日汇总、钱包、配额、无有效日志键的人工调整 |
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
| POST /api/data-cleanup | `{preview_id:"32位随机ID",resume?:boolean}`；resume 仅供新错误日志任务继续 | 同上，预览或任务必须属于当前操作者 |
| POST /api/data-cleanup | `{kind:"error_logs",before:Unix秒}`；确认后直接创建，不与 preview_id/resume 混用 | operations.logs Edit |
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

新表及财务索引接入既有主库迁移。日志维持既有约定：新库建表带索引，**已有 logs 启动只报告缺失索引，不自动给热表建索引**。财务预览缺必需索引仍拒绝；新错误日志清理按实际索引列选择专用时间索引、旧时间索引或主键，不再强制补专用索引。任何另行安排的索引迁移须先在目标 schema 副本演练；财务大表也建议提前建索引，避免启动期长时间 DDL。

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

先写测试，再实现，覆盖日期边界/非法输入、本月内及跨月的 3 天保留边界、截止时刻前后、快照后插入、游标推进、错误类型过滤、人工调整保护、事务回滚、重复清理、所有重试/回填入口不恢复明细且不重计汇总、预览所有权和过期、重复确认、任务冲突、老节点拒绝、同秒版本切换及提成分页数量。前端覆盖日期转秒、预览不删除、确认提交、任务恢复。

### 3 天保留期调整（2026-11-21）

用户最终确认台账和提成明细均只需保留最近 3 天，替代此前的 5 天要求。沿用现有手动预览、确认和后台清理流程，不新增自动清理任务、API、配置、表、索引或迁移。`service/data_cleanup.go:ValidateDataCleanupDate` 在预览与提交时统一校验 3 天底线，并移除财务明细的当前提成月份限制；前端默认日期及七种语言提示同步调整，后端三种语言错误提示同步。

#### Existing Logic and Related-Path Findings

- `model/data_cleanup.go:DeleteDataCleanupBatch` 只删除所选明细表的有效日志记录，事务内保留永久去重凭据；不修改余额或统计汇总。现有隔离数据库测试逐列核对保护对象。
- `model/commission_period_switch.go:SwitchCommissionPeriod` 通过重分配日汇总对齐当期统计，`controller/employee.go:GetMyEmployeeProfile` 从重置周期日汇总读取本期业绩；解除明细的月份限制不改变这些汇总。
- 明细列表、明细筛选合计和导出只覆盖仍保留的记录，清理后的历史汇总继续保留。现有缓存版本和导出并发保护继续生效。
- 共享资源仍为上文列出的明细表、去重凭据、任务记录及维护连接池。只放宽管理 API 接受的时间范围，沿用索引、批次上限、超时及队列压力退让；relay 同步执行、DB/Redis 调用和 goroutine 数均无新增。

## 验证与发布边界

回归覆盖本月内、跨月与闰年的 3 天边界、截止时刻记录保护、人工调整、余额和汇总不变、永久去重、预览与确认。使用独立临时数据库或测试表/schema，不删除现有业务数据。

上线前先在目标副本演练并预建必要的大表索引，再由主节点完成迁移并升级全部 writer；兼容检查通过后开放清理入口。数据库功能回归不代替 ClickHouse 专项或 30k/100k RPM 压测。

### 明细统计调用链与待修问题

- 在用：台账明细 `/api/admin/employee/consumption-cost-ledger/stats` 在 `consumption_costs` 上执行 COUNT/SUM，因此明细筛选合计随清理改变；提成列表在有清理记录后按剩余 `employee_commission_logs` 计数。
- 在用：使用日志及员工客户日志的列表总数来自 logs；消费额、RPM、TPM 查询限定 `type=LogTypeConsume`，本次只删 type=5 不改变消费类统计。
- 通用函数仍有回退分支：`GetConsumptionCostByChannelWithPlan`、`GetCommissionStatsByEmployeeWithPlan`、`GetCommissionTotalsWithPlan` 遇到 UncoveredRanges/DetailRanges 会直接聚合财务明细。当前业务概览明确传入 DailyOnly 计划，不进入这些分支；`GetConsumptionCostTotals`、`GetConsumptionCostByChannel`、`GetCommissionTotals` 的通用入口在当前仓库未查到业务接口调用，不能把它们描述成当前概览的执行路径。
- 待修：错误日志清理没有使 `logstat` 小时统计缓存失效。无额外筛选时，错误日志/全部日志的历史整点计数可能仍命中删除前缓存，列表记录已消失而总数暂时偏大；TTL 默认 48 小时、实际由日志查询设置决定。带额外筛选直接 COUNT 的路径不受该旧缓存影响。此问题影响列表计数一致性，不会删除财务汇总；本轮只记录发现，尚未修补或部署。

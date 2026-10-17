# 日志数据库查询治理

状态：用户已确认；第一阶段代码已落地，针对性测试通过，整包测试受环境/既有测试阻断；第二阶段待执行计划。日期：2026-10-17。

## 目标与范围

仅处理本项目数据库读路径的连接占用和慢查询，不调整上游参数、通道或 Relay 重试。
依据附件 `oneapi-20261016011035.log.1.gz`，时间范围为 2026-10-16 01:10:35～05:18:47：

- 899 条超过 200ms 的慢 SQL，其中 872 条为 logs 查询，27 条为主库写入。
- logs 查询包含用户列表及计数、按用户名汇总额度、按 token_id 查询最近日志；最大记录耗时约 2282ms。
- 未找到 relay-log 批量写入失败记录；附件不覆盖状态页最近写入错误时间 06:11:59。
- 05:06:46～05:06:47 的 Too many connections 出现在通道 467 的 71 个请求错误中，不能作为本机数据库连接耗尽的证据。

目标分两阶段：先补齐管理查询的取消与超时约束，再根据真实执行计划选择查询/索引优化。超时保护不等于查询加速，不能承诺它消除现有 200ms～2.28s 的慢 SQL。

## 已核对代码与既有设计

参考 `usage-log-list-performance.md`、`startup-log-schema-migration.md`、`relay-log-async-batch.md`。

- `GetUserLogs` 的 COUNT、列表查询未绑定请求 context 或查询超时。
- `GetLogByTokenId` 未绑定请求 context 或查询超时。
- `SumUsedQuota` 的 SUM 回落路径有超时，但 RPM/TPM 查询没有；既有超时基于后台 context，不能随 HTTP 取消。
- 小时统计缓存只加速无筛选查询；用户名等筛选回落到 SQL，与日志证据一致。
- `Limit(logSearchCountLimit).Count(...)` 不限制聚合扫描量。保留精确总数语义，不把结果改成截断总数。
- 已有用户/时间/ID、用户/ID、单列 token_id 等索引。仓库声明不代表生产索引一定完整，也不能据此断言缺索引就是根因。

## 数据流

HTTP 请求 → 现有鉴权和筛选解析 → 请求 context 派生查询预算 → LOG_DB 查询或现有缓存 → 现有响应。
超时/客户端取消后停止后续 SQL，不重试、不另起后台查询。释放 context；数据库驱动完成取消后归还连接。

## API 契约

不增加端点、不改变字段、分页、排序、过滤和总数语义。

- `/api/log/self`、`/api/log/self/stat`：保留 UserAuth。
- `/api/log`、`/api/log/`：保留 AdminAuth，管理员列表与用户列表使用相同的预算规则。
- `/api/log/stat`：保留 AdminAuth。
- `/api/log/token`：保留 TokenAuthReadOnly 及现有访问限制。

model 增加可接收 context 的查询入口，兼容既有调用者；HTTP controller 传入 `c.Request.Context()`。缓存回源若属于该请求，也传递预算；独立预热使用自身后台预算。

## 配置参数

复用 `log_query_setting.query_timeout_ms`（当前默认 30000ms），不增加环境变量，不擅自缩短默认超时。
单次列表 COUNT 与取页共用预算；统计调用的回源、SUM、RPM/TPM 共用请求预算。各阶段不得重置预算导致总耗时无限叠加。

## 业务逻辑与边界

取消或预算耗尽后不得继续下一阶段；COUNT 失败不得伪装成功并返回 total=0。
过滤字段、授权用户、时间边界、空结果、排序和页码保持不变。
不扩大现有缓存适用范围，不假设已结束小时永远不再有迟到写入，不引入新的缓存一致性问题。
已有缓存失败回落必须遵守剩余预算，已取消的请求不能重启一轮整段扫描。

## 数据模型与索引设计

第一阶段无新表、列、索引或迁移，不增大 logs 写入维护成本。
第二阶段先取得目标库的现有索引、表规模及以下查询的普通 EXPLAIN；生产不直接运行 EXPLAIN ANALYZE 大范围查询。

| 访问模式 | 需要验证的候选方向（非已批准 DDL） |
| --- | --- |
| user_id + type + model_name/token_name + 时间范围 COUNT | 检查现有用户时间索引的过滤行数；对热点筛选评估等值列前置的复合索引 |
| 相同筛选按 id 降序取页 | 检查倒扫主键后过滤、深 OFFSET 成本；不能擅自改变排序或取消翻页能力 |
| username + type + 时间范围 SUM(quota) | 检查扫描范围与回表成本，再决定复合索引或查询调整 |
| token_id 等值 + id 降序 LIMIT | 检查现有单列索引与排序；评估 (token_id, id)，而非直接添加 |

仅在执行计划证明收益且写入成本可接受后形成具体迁移补充设计。SQLite、MySQL >=5.7.8、PostgreSQL >=9.6 均须有兼容方案；不在应用启动时对大表盲目建索引。现有 ClickHouse 分支保持兼容。

## 错误处理

复用 controller 的 common 错误响应约定，记录底层错误但不向用户暴露 SQL/连接串。若新增超时提示键，同步后端 en/zh 翻译。客户端已经取消时不进行额外昂贵操作。

## Main Chain Impact

Relay goroutine 新增同步工作为零；无新增异步 Relay hook。变化仅在管理/用户日志查询 HTTP 路径。
日志写入 worker、额度结算、主库连接配置不改动。30k～100k RPM 下不会随 Relay 请求新增 SQL、Redis 操作、锁或 goroutine。

## Shared Resource Audit

- LOG_DB 连接池、logs 表与后台日志 writer 共用：通过有界查询和取消缩短占用；第一阶段不新增连接池、不执行 DDL。
- 管理员列表既有的主库 channels 名称补充查询也绑定相同 context；不增加查询或改变主库写入。既有内存渠道缓存读取不改动。
- 生产已配置独立 LOG_SQL_DSN；是否同实例/同主机仍待确认。不修改主业务 DB 的 tokens/users/channels 写入。
- 既有 logstat 缓存与 Redis 池：不增加命名空间和缓存容量；只审查请求取消能否贯穿回源，不引入 Relay 锁。
- 无新增 worker/goroutine 池、文件句柄或后台预聚合任务。

## Concurrency Analysis

Relay 每请求增量为 DB 0、Redis 0、锁 0、goroutine 0。
管理列表仍为一次 COUNT 加一次分页查询；统计保持既有缓存命中/回源次数，不并行放大查询。超时后不另行重试。
取消保护不能保证 writer 独享连接；若后续证据表明读流量耗尽连接，再单独设计读写资源隔离，不能用增大池上限掩盖数据库压力。

## 测试与验收

先写回归用例再改代码：已取消请求不执行 SQL；查询预算覆盖后续阶段；COUNT/SUM 失败不继续取页/RPM；正常筛选、精确总数、空结果和排序保持一致；兼容入口正常；缓存回落不绕过取消。
使用 testify 和真实项目数据库环境，测试数据限定专属行并清理；无可用数据库时 SQLite fallback，明确其不能验证生产锁等待和执行计划。不得模拟 GORM。
运行相关 model/controller 测试。执行计划与性能验收需在实际 PostgreSQL 日志库或同构测试库完成；不以 SQLite 耗时作为性能证据。

## 待补证据与交付边界

当前工作区仅发现 `.env.example`，尚未取得可用于验证的生产执行计划。无需提供密码；可提供脱敏索引、EXPLAIN 输出。
定位两次日志写入超时还需 2026-10-16 06:11:59 前后日志。本方案不将未定位的写入超时宣称为已修复。
实现后回写实际变更与验证结果；本次设计确认仅授权第一阶段，具体索引改动待执行计划验证后补充。

## 第一阶段实际实现与验证

- `model/log.go`：为 GetAllLogs、GetUserLogs、GetLogByTokenId、SumUsedQuota 增加 WithContext 入口；原入口保留并使用有界后台 context，兼容现有调用方。
- `controller/log.go`：五个日志列表/统计处理器传递 HTTP context；错误记录到请求日志，使用既有 `common.database_error` 国际化提示，避免暴露 SQL 或底层取消错误。
- `model/log_stat_cache.go`：缓存聚合与回落 SQL 共享上层预算；已取消时不再重启整段 COUNT；逐小时读取前检查取消。
- 不改变筛选、排序、页码、精确计数、统计口径或默认 30 秒配置。不新增 SQL、索引或数据库连接。员工专属查询尚未纳入本阶段。
- HybridCache 的 Redis Get/Set 仍使用其既有内部超时；本次没有改动共用 cachex 库。正在进行的单次缓存操作不能被 HTTP context 立即中断，但返回后不会在已取消的 context 下继续启动数据库回源。因此本次保证的是数据库预算，不声称整个 HTTP 请求有严格的同长度墙钟上限。
- 新增 8 个 model 回归测试和 1 个 controller 测试（覆盖五个 handler）。包含取消前零查询、COUNT 后停止取页、SUM 后停止 RPM、缓存回源取消后禁止整段回落、父级 deadline、COUNT/取页相同 deadline、正常筛选与统计结果。
- 验证通过：`go test ./model ./controller -run 'TestLogQuery|TestLogStat|TestLogHourStat|TestSumLogStatRange|TestGetAllLogs_|TestGetUserLogs_|TestGetLogsStat_|TestSumUsedQuota|TestGetLogByToken' -count=1`；`git diff --check`。
- 当前无 SQL_DSN：新增 model 数据库用例使用真实 SQLite 内存数据库执行，不 mock GORM；既有依赖项目数据库的用例按其 harness 跳过。
- 尝试 `go test ./model ./controller`，分别在既有 `TestListChannelVeridropDetectionsFiltersAndPages` 和 `TestListChannelVeridropDetectionTargets` 中因 DB=nil 调用 AutoMigrate 而 panic，未修改这些无关测试。不能宣称整包测试全绿。
- 未访问生产库、未部署、未执行 DDL；尚无生产查询加速或两次写入超时已消除的证据。

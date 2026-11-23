# 新功能不回填旧数据

2026-11-17：用户明确要求“新功能不用管旧数据”，随后要求检查本次新增功能并去除无关紧要的回填。本次按该要求清理已有实现，不新增业务功能。以线上旧版对应的 `127c82331f979f2937cd1035149820eb79e6e1b6` 对照当前工作区，包括未跟踪文件。

## Existing Logic and Related-Path Findings

已检查主库/日志库迁移入口、启动服务、变更文件中的回填与批量更新入口，并追踪调用方和测试。确认本次新增的不必要历史处理集中在安全监控：

| 位置 | 清理前行为 | 本次处理 |
| --- | --- | --- |
| `service/auditsecurity/ip_backfill.go` / `worker.cycle` | 从旧告警保留的 JSON 证据补建风险关联，每轮 20 条 | 删除自动补录实现及调用。新告警仍在原事务内保存风险关联。 |
| `worker.repairSource` / `store.go` | 每小时补扫最近 24 小时原始审计，保存独立 repair 游标 | 删除整个历史扫描入口及专用提交分支。 |
| `worker.scan` / `scanPage` | 首次回看 120 秒；后续窗口回退 120 秒也会越过首次启用时间 | 初次扫描持久化启用起点，任何正常重叠窗口不得越过该起点。 |
| `model/audit_cleanup.go:CleanupAuditSecurityAlerts` | 旧告警没有 `closed_at` 时查询最后一条操作并补写关闭时间，再判断清理 | 删除补写；缺少有效关闭时间时跳过，不猜测、不补写，也不自动删除该记录。 |

其他新增功能没有找到历史补写：员工身份快照与字段变更审计只在新事件写入；渠道创建者记录只在创建渠道时写入；登录保护只处理本次失败/成功；原始字段导出只是只读投影；流式校验旧设置兼容在读取或用户保存时处理，不批量迁移历史日志。

`InitializeUserAuthVersions`、`InitializeExternalIdentityClaims`、旧访问令牌退役时间初始化及账务 fallback 恢复在旧版中已经存在，属于身份唯一性/认证安全/已发生账务恢复，不属于新功能历史补算，本次保留。请求正文恢复、usage 日志失败重投、清理任务续跑也保留。既有索引构建/兼容修复属于结构维护，不是回填业务字段，未在此扩大修改。

## 范围、数据流与生命周期

首次实际启用监控 → 在已有 `audit_security_states` 表保存 `name=source_start`、`cursor_at=首次扫描时刻` → 扫描不早于该时刻的新审计 → 原 Normalize/Evaluate/Apply 事务保存回执、告警和风险关联 → 原查询 API 返回。时间精度沿用审计的 Unix 秒，同秒事件属于起始窗口。

启用起点只初始化一次，worker 重建或应用重启从该持久化值恢复；不因重启重新扫描启用前历史，也不丢弃首次启用后已经进入扫描范围的待处理事件。现有 120 秒重叠保留在起点之后，用于正常异步落库迟到和回执幂等；不再提供跨 24 小时的历史补扫。已有待检事件的 IP 查询重试保留，属于已接收事件的失败恢复。

不新增表、列、索引、配置项或 HTTP 接口；已有状态列保留以避免额外 schema 变更，但不再写回填游标。现有风险 API 的 `historical_complete` / `backfill_done` 字段继续保留，均不声明历史已补齐。Root/Admin/员工的原有授权和审计列表可见性不变；旧审计仍可按原权限查看，不补写旧快照。

起点保存失败或失去扫描租约时，该轮报错退出，不在缺少边界时继续读历史。初始化与游标提交沿用已有 context、租约和日志错误路径。清理跳过旧缺失关闭时间的告警，后续正常人工结案仍会写入明确关闭时间；已标记删除的任务按原有恢复协议收尾。

## Main Chain Impact / Shared Resource Audit

全部变更在监控后台和告警清理，不增加 relay 同步调用。沿用独立监控连接池和既有监控状态表；原始 `audit_logs` 只读，没有 UPDATE。首次 worker 扫描增加一次有界起点读写，重启读取既有起点；运行时在 worker 内缓存该固定值。监控状态、告警与证据表不被 AI relay 主链访问，Redis namespace、计费表、额度池与 relay worker 池不变。移除 24 小时扫描和风险补录会减少共享数据库 CPU、I/O 和事务占用。

30k/100k RPM 下每个 relay 请求新增 DB/Redis/锁/goroutine 均为 0；这里是调用链检查，不是新的全链路容量结论。没有新增长表查询模式或索引。

## 验证要求

使用独立真实 MySQL/PostgreSQL 验证：启用前历史不产生回执/告警/风险关联；启用后新事件正常处理；重启、分页及失败重试不越过起点；旧告警证据不自动补录；缺少关闭时间的旧告警不被补写或删除；新告警的结案保留和有界清理继续通过。保留日志原始字段和既有结构。SQLite 按用户要求跳过。

## 实现与验证结果（2026-11-17）

以上四处已清理，`ip_backfill.go` 已删除，生产代码中不再有 `backfillRisk`、`repairSource` 或 `source_repair` 调用/提交分支。保留的 `BackfillID/BackfillEndID/BackfillDone/LastRepair` 仅维持现有表结构，不再推进历史任务；`notificationSkipReason` 中的 `backfill` 仅是过期事件不发送通知的既有原因代码，不会启动回填。

隔离环境：Windows Go 1.26.11；独立 Docker MySQL **26.10.0**、PostgreSQL **18.7**、Redis **7.4.11**，仅绑定本机随机端口。实际测试由环境变量注入临时 DSN，没有访问共享开发库或生产服务器。SQLite 按要求未执行；ClickHouse 非生产配置，本轮未执行。

| 验证 | MySQL | PostgreSQL |
| --- | --- | --- |
| 501 条启用前旧记录不分析；501 条新记录分页处理；重建 worker 后恢复同一起点，120 秒重叠不越界 | 通过 | 通过 |
| 旧告警 JSON 不补建风险关联；旧回填状态不推进；接口不宣称历史完整 | 通过 | 通过 |
| 起点保存故障时不读取来源日志；第 21 条事件注入失败后保留前 20 条提交并恢复到 45 条，无重复计数 | 通过 | 通过 |
| `closed_at=NULL/0` 即使有有效旧结案动作也不补写、不自动删除 | 通过 | 通过 |
| 正常新告警生成、关联证据、IP 待检重试、结案、清理回滚与有界续跑 | 通过 | 通过 |
| 真实来源扫描、采集、风险筛选/游标、导出读写权限合同 | 通过 | 通过 |
| 新库及旧结构 fixture 升级，第二次迁移无 DDL；旧员工快照仍为 NULL，旧事件不自动安排 IP 检查 | 通过 | 通过 |

执行命令/环境映射：

- `go test ./service/auditsecurity -run '<定向 worker 测试集合>' -count=1 -v -timeout=300s`，分别将 `TEST_ADMIN_TOKEN_DSN` 指向临时 MySQL/PostgreSQL；测试集合、完整命令见 `_local/no-history-20261117/check.py` 和 `results.jsonl`。两组分别用时 42.06/16.59 秒（包括 Go 启动/编译开销）。
- `go test ./service/auditsecurity -run '^(TestCaptureSourceScanWithoutHistoricalBackfill|TestAuditFilterEnhancements|TestAuditFilterUpgrade|TestMonitoringDatabaseLifecycle)$/^mysql$' -count=1 -v -timeout=300s`；PostgreSQL 对应将子测试改为 `postgres`。设置相应 `AUDIT_TEST_*_DSN` 与独立 `AUDIT_TEST_*_UPGRADE_DSN`。两组均通过。
- `go test ./service/auditsecurity -run '^TestScanIntervalChangesWithoutRestart$' -count=1 -v -timeout=60s`，两种真实主库均通过。
- `go test ./service/auditexport -run '^TestAuditExportReaderLifecycle$/^(mysql|postgres)$' -count=1 -v -timeout=120s`，通过。
- `go test ./controller -run '^TestAuditFiltersKeepLegacyTimeContract$' -count=1 -v -timeout=120s`，通过；测试主库 MySQL、日志库 PostgreSQL。
- `go build -o _local/no-history-20261117/candidate.exe .`，通过；`git diff --check` 无空白错误。

完整日志保存在 `_local/no-history-20261117/`。本次为后台处理行为清理，没有新增 DDL，不需要人工修改数据库。以上是定向数据库/模块回归及构建，未重新测量完整应用启动秒数、未重跑生产规模在线 DDL/全链路压力测试，也未部署。此前上线计时必须按其候选版本单独引用。

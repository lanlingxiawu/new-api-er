# 导出中心导出 quota_data

状态：已实现。未提交，未部署。

管理员在使用日志的导出中心导出主库 `quota_data` 的原始小时汇总行。复用现有异步任务、并发槽、冷却、CSV.GZ 分片、进度、取消、下载和过期清理。不新增配置项，不改计费，不把内存里尚未落库的小时缓存写进文件。

## 目标与范围

导出的是表里已经落库的行，一行对一行，不按仪表盘接口再做 `GROUP BY`。时间范围必填，跨度沿用 `log_export_setting.admin_max_range_sec`。可选等值筛选只覆盖表上已有维度。

本次只做管理员导出中心。自助导出、员工导出、审计导出保持原合同。普通管理员看不到 Root 才能看到的 `token_id`、`node_name`，与 `GetFlowQuotaData` 的角色投影一致。列在创建时按创建者角色固定，文件里不提供列选择、模板、汇总维度、分组倍率重算或异常筛选。CSV 表头固定为中文，不随界面语言变化。

有消耗额度。表字段是 `quota`，来自每条消费日志的 `RecordConsumeLogParams.Quota`，按小时累加，库里仍是整数。文件不写这个整数，表头为「消耗额度（美元）」，按任务开始时的 `common.QuotaPerUnit` 换算。`token_used` 是提示词与补全 Token 之和，不是金额。

当前小时可能不完整。`LogQuotaData` 先写入进程内 `CacheQuotaData`，`UpdateQuotaData` 按 `DataExportInterval` 分钟才 `SaveQuotaDataCache`。导出发起时不刷新这份缓存，避免和落库协程抢同一行。

## Existing Logic and Related-Path Findings

| 位置 | 已核对行为 | 本次怎么接 |
| --- | --- | --- |
| `model/usedata.go` `QuotaData` | 主库表。聚合键是 `user_id, username, model_name, created_at, use_group, token_id, channel_id, node_name`。`created_at` 在写入时被截到整小时。度量列是 `count, quota, token_used, cache_read_tokens, cache_creation_tokens` | 导出这些已落库列。`hour` 只是按任务时区格式化后的展示列，不替代 `created_at` |
| `model/usedata.go` 索引标签 | `user_id`、`use_group`、`token_id`、`channel_id`、`node_name` 各有单列索引。`idx_qdt_model_user_name` 是 `(model_name, username)`。`created_at` 只有 `idx_qdt_created_at,priority:2`，结构体里没有这个名字的 priority 1 | 时间范围是主谓词。实施时对真实 MySQL、PostgreSQL 做 `EXPLAIN`。若不能走 `created_at` 范围，再加 `(created_at, id)`，不在设计未验证前建索引 |
| `controller/usedata.go`、`router/api-router.go` `/api/data` | 管理员可查全部或按用户聚合；Root 的流向比普通管理员多 `node_name`、`token_id`；本人接口只查自己，且不含渠道和节点 | 导出列和筛选项沿用这道边界。不新增本人或员工入口 |
| `model/log_export_source.go`、`model/log_export_job.go` `writeLogExport` | 数据源白名单只有 `usage` 和 `audit`。空 `data_source` 视为 usage。审计在用量重算、列解析、汇总之前分派 | 增加 `quota`。未知值仍然失败，不能落回 usage。`quota` 在用量重算之前分派 |
| `model/log_export_scanner.go` | `reader == nil` 时由早到晚扫日志。`reader != nil` 时窗口从范围末尾往前推，进度公式也按倒序解释。这是审计的顺序 | `quota` 要由早到晚，第一片是范围起点。给 reader 增加显式方向，审计继续倒序，quota 正序。不复制第二套限速循环 |
| `controller/log_export.go` `createLogExportJob` | `/api/log/export/jobs` 拒绝请求里的 `data_source` 和 `audit_filters`，服务端固定 usage | 保持拒绝。quota 走新路由，请求里也不接受 `data_source` |
| `model/log_export_management.go` | 任务共用一个 Redis ZSET。列表先按 scope 的 `Permissions` 丢掉别的数据源。下载授权 version 2 只匹配 usage，version 3 只匹配 audit | quota 使用 scope `quota_admin` 和 version 4。usage、audit、employee、self 的授权和列表都不能看到或下载 quota 任务 |
| `controller/audit_export.go` `AuditExportScope` | 导出要求控制台会话，PAT 被 `GetSessionAuthIdentity` 拒绝 | quota 路由同样要求控制台会话 |
| `setting/operation_setting` 导出设置 | 开关、范围、批大小、窗口、超时、并发、冷却、行速率、分片、压缩、磁盘和 TTL 已由使用日志与审计共用 | 不新增 `quota_export_setting`。quota 任务占用同一组名额和冷却 |
| `model/relay_log_pipeline.go` | 中继结算后的额度汇总进入异步日志管线，再调用 `LogQuotaData`。不在中继 goroutine 上写 `quota_data` | 导出只读已提交行，不调用 `LogQuotaData` 或 `SaveQuotaDataCache` |

已核对 `docs/design/usage-log-export.md`、`usage-log-export-unification.md`、`audit-log-export.md`。审计文档里的独立数据源和授权版本是本次要沿用的边界。

## 数据流

提交 → 管理员会话校验 → 固定 `data_source=quota` → 共用槽位和冷却 → Redis 任务 → worker 在用量逻辑前分派 → 主库按时间窗和 `(created_at, id)` 键集分批读 → 按创建时角色渲染列 → 共用 CSV.GZ 分片 → ready → 授权下载 → TTL 清理。

1. `LogExportDataQuota = "quota"`。`GetLogExportJob` 的白名单加入它。缺省和空值仍是 usage。
2. 筛选放在新的 `QuotaExportFilter`，挂在任务上，不扩展 `LogExportFilter`。起止时间同时写入现有 `Filters.StartTimestamp` 和 `Filters.EndTimestamp`，进度、文件名和时间窗继续读这两项。
3. 读取实现放在 `model`，因为表和连接都在 `model.DB`。不注册到审计 reader，也不把 `quota_data` 伪装成 `model.Log`。
4. 扫描复用 `logExportScanner.runBatches` 的取消、超时、CPU 和行速率闸门、磁盘检查、进度保存。reader 声明正序后，窗口从 `StartTimestamp` 向 `EndTimestamp` 推进，下一窗从上一窗结束秒加 1 开始。
5. 每批查询：

```text
created_at >= windowStart AND created_at <= windowEnd
AND (created_at > cursorAt OR (created_at = cursorAt AND id > cursorId))
AND 已设置的等值条件
ORDER BY created_at, id
LIMIT batchSize
```

同一小时有多行，游标必须带 `id`。`batch.Done` 只表示这个窗口读完。空窗口继续，不能把空批当成整段导出结束。`Scanned` 按读到的行数计，限速沿用扫描器，reader 不再计一次。

6. 不开启事务，不用 `FOR UPDATE`。查询使用请求 context 和现有 `batch_query_timeout_sec`。连接来自主库池；并发上限仍是 `max_concurrent_jobs`。
7. 写入复用现有 CSV 转义、公式防护、BOM、gzip 和分片。表头开启时第一行使用下面的中文名。`小时` 按任务时区格式化到小时。消耗额度在任务开始时读取一次 `common.QuotaPerUnit`，整份文件用这一个值：`math.Round(float64(quota) / QuotaPerUnit * 1e6) / 1e6`，与使用日志导出的 `cost_usd` 相同。`QuotaPerUnit` 为 0 时任务失败，不能把消耗写成 0。表里没有历史单价，所以换算用的是导出当时的站点设置，不是每一小时当时的设置。

## 列与筛选

表头固定中文。内部列名仍用英文，只出现在任务记录里，不出现在 CSV 第一行。

| 内部列名 | 表头 | 内容 |
| --- | --- | --- |
| `id` | ID | 行 ID |
| `user_id` | 用户 ID | |
| `username` | 用户名 | |
| `model_name` | 模型 | |
| `hour` | 小时 | 任务时区下的整点 |
| `created_at` | 统计时间 | 小时桶的 Unix 秒 |
| `use_group` | 分组 | |
| `channel_id` | 渠道 ID | |
| `count` | 请求次数 | |
| `quota` | 消耗额度（美元） | `quota / QuotaPerUnit`，四舍五入到 6 位小数 |
| `token_used` | 消耗 Token | 提示词 Token 与补全 Token 之和 |
| `cache_read_tokens` | 缓存读取 Token | |
| `cache_creation_tokens` | 缓存创建 Token | |

Root 在最后增加 `令牌 ID`（`token_id`）和 `节点`（`node_name`）。创建时把内部列名写入任务。执行中若创建者不再是管理员，或文件含 Root 列但当前角色低于 Root，任务以 `access_changed` 失败，不交付已写内容。

筛选白名单：

| 字段 | 规则 |
| --- | --- |
| `start_timestamp`、`end_timestamp` | 必填，结束不早于开始，跨度不超过管理员最大范围，上界不超过 253402300799 |
| `user_id`、`channel_id` | 可选 `*int`。缺省表示不筛。`0` 是表的默认值，允许用来筛空渠道或空用户 |
| `username`、`model_name`、`use_group` | 可选字符串，去空格后长度 1 到 64。空字符串表示不筛 |
| `token_id`、`node_name` | 仅 Root。普通管理员传入即拒绝整单 |

未知字段、`null`、列清单、模板、模式、汇总维度、分组倍率、异常条件和 `data_source` 都拒绝。字符串筛选是等值，不是模糊匹配。

## API

新路由固定数据源，不能用参数切到 usage 或 audit。

| 方法与路径 | 权限 |
| --- | --- |
| `POST /api/data/quota/export/jobs` | `AdminAuth` + 控制台会话 |
| `GET /api/data/quota/export/jobs` | 同上，列表 scope 固定 `quota_admin` |
| `GET /api/data/quota/export/jobs/:job_id` | 同上 |
| `DELETE /api/data/quota/export/jobs/:job_id` | 同上，运行中取消，终态删除文件 |
| `GET /api/data/quota/export/jobs/:job_id/download-url` | 同上 |
| 现有 `/dl/log-export/:token` | 不新增下载路由。version 4 授权绑定 `data_source=quota` 和 `quota_admin` |

创建体：

```json
{
  "filters": {
    "start_timestamp": 0,
    "end_timestamp": 0,
    "user_id": 0,
    "username": "",
    "model_name": "",
    "use_group": "",
    "channel_id": 0,
    "token_id": 0,
    "node_name": ""
  },
  "options": { "header": true, "csv_bom": true, "timezone": "Asia/Shanghai" }
}
```

整型筛选项在 JSON 里缺省，不写 `0`，除非确实要匹配 0。响应沿用现有导出：创建返回 `{job_id}`，列表返回 `jobs/next_cursor/has_more/enabled/max_range_sec`。失败不提供 retry，用户重新提交并重新占用冷却。

`/api/log/export/jobs`、self 和 employee 入口继续拒绝 quota 字段。usage 列表因为 `Permissions` 对 `quota` 返回 false，不会把 quota 任务算进本页结果；quota 列表同样丢掉 usage 和 audit。

下载文件名主体为「额度数据」，后接日期范围和已设置的用户、模型、分组。Root 的令牌或节点条件并入「更多筛选」，避免文件名过长。

## 界面

管理员导出中心在现有任务列表上方增加「使用日志 / 额度数据」切换。额度数据复用 `ExportTaskList`，`dataSource` 为 `quota`，`apiBase` 为 `/api/data/quota/export`，查询键带上数据源、scope 和用户。自助导出中心不显示这次切换。

新建表单只有时间范围、上表筛选项、表头开关、BOM 和时区。Root 才渲染令牌 ID 和节点。说明文字写明三件事：导出的是小时汇总表，消耗额度按当前站点设置换算成美元，当前小时可能还在内存缓存里。表头开关只决定是否写出中文表头，不能改成英文。不提供列选择和预估条数；这条路径没有可复用的廉价计数。

前端七种语言补上新文案。后端用户可见错误沿用现有导出 i18n：参数无效、范围过长、功能关闭、Redis 不可用、任务过多、冷却和磁盘不足。

## 主链与共享资源

导出不进入中继 goroutine，也不新增中继中间件。

| 资源 | 中继是否使用 | 导出行为 |
| --- | --- | --- |
| `quota_data` | 异步管线周期性 `INSERT/UPDATE`，不在请求 goroutine 上 | 只读，无行锁。InnoDB/PostgreSQL 的普通一致性读不阻塞那次落库 |
| 主库连接池 | 中继读用户、令牌、渠道 | 每批一次短查询，受导出并发、批超时和批间休眠限制。不占长事务 |
| `CacheQuotaData` | 异步累计当前小时 | 导出不读不写这把锁 |
| Redis `logexport:` 任务键、槽位、冷却 | 导出子系统自有，中继不读 | 与 usage、audit 共用名额 |

不新增表，不改 `quota_data` 的写入语义。索引只在 `EXPLAIN` 证明时间范围扫全表时追加 `(created_at, id)`，并按 Rule 2 在新库和旧库各启动两次核对 DDL。

## 错误与边界

- 范围内没有行：仍生成只含表头的 CSV.GZ，与现有空导出一致。
- 查询超时、取消、磁盘满、分片过多：沿用现有失败码，删除未完成文件并释放槽位。
- 落库发生在两个批次之间时，新行只要时间戳还在尚未扫描的窗口里就会进入文件。已经扫过的小时不回头补。这与使用日志导出的非快照语义相同。
- 进程内缓存尚未刷入的行不出现在文件中。
- 账号禁用或管理员角色被收回后，下一次权限复核失败，任务不再交付。

## 测试

先写测试再实现。覆盖：

- 同一 `created_at` 下多行的键集不漏、不重，窗口边界不重叠。
- 普通管理员的任务没有 `token_id` 和 `node_name`，传入这两项筛选被拒绝。Root 可以导出并筛选。
- 打开表头时，第一行是中文，消耗额度列的表头是「消耗额度（美元）」。默认 `QuotaPerUnit=500000` 时，`quota=500000` 写出 `1`。同一任务中途修改站点单价不影响已开始的换算。`QuotaPerUnit=0` 时任务失败。
- usage、self、employee 入口拒绝 quota 字段。quota 列表与 usage 列表互不可见。
- version 2 和 version 3 下载授权不能下载 quota 文件，version 4 可以。
- 无控制台会话的 PAT 返回 403。非管理员返回 403。
- 执行中角色下降返回 `access_changed`，文件不进入 ready。
- 空范围、非法范围、未知字段、`channel_id=0` 的等值筛选。
- 真实 MySQL 与 PostgreSQL 上插入隔离行，导出后按 `id` 核对列值，并检查查询计划。SQLite 只作驱动差异的补充。

实施后把实际入口、索引结论和验证命令补回本文。

## 实施记录

2026-11-20 按本文实现。没有新增配置，没有改 `quota_data` 的写入，也没有新增索引。

实际入口：

- 数据源 `model.LogExportDataQuota`（`quota`）。`writeLogExport` 在用量重算之前调用 `writeQuotaDataExport`。
- 管理接口在 `router/api-router.go`：`POST/GET/GET/:job_id/DELETE/:job_id/GET/:job_id/download-url`，组前缀 `/api/data/quota/export/jobs`，中间件 `DisableCache`、`AdminAuth`、`QuotaExportScope`。这五条在 `middleware/access_token_routes.go` 登记为控制台会话，PAT 不能调用。
- 下载仍是 `/dl/log-export/:token`。`quota_admin` 发放 version 4，只匹配 `data_source=quota`。
- 管理员导出中心 `web/src/features/usage-logs/export/index.tsx` 增加「使用日志 / 额度数据」。额度列表用 `apiBase=/api/data/quota/export`、`scope=quota_admin`。新建表单是 `QuotaExportSheet`。自助视图不渲染这次切换。员工模板管理只留在使用日志。额度任务没有来源、模式和客户这三项使用日志筛选，页面上不显示，查询时也不带上；地址栏里原有的这三项切回使用日志后仍然有效。导出关闭时，管理页显示「日志导出功能当前已关闭」。

索引：表上已有 `idx_qdt_created_at (created_at)`。MySQL 8.0.46 在约 5000 行、每行一个小时并 `ANALYZE` 之后，`created_at` 闭区间加 `ORDER BY created_at, id LIMIT 10` 的计划是 `type=range`，`key=idx_qdt_created_at`。PostgreSQL 18.7 对同一形状是 `Index Scan using idx_qdt_created_at`，条件落在 `created_at`。因此不追加 `(created_at, id)`。同一小时内的键集仍在查询条件里用 `id` 推进。小表上 MySQL 的自动测试计划是 `type=index`，不是 `ALL`。

验证：

- 隔离的 MySQL 8.0.46、PostgreSQL 18.7 和 Redis 7.4.11 上，`go test -count=1 -timeout 180s -v ./model/ -run "Test(QuotaExport|WriteQuotaExport)"` 通过。键集、窗口边界、中文表头、美元快照、`channel_id=0`、空文件、角色收回、列表隔离和 version 4 授权都在其中。
- `go test -count=1 -timeout 180s -v ./controller/ -run "TestParseQuotaExportRequest|TestCreateQuotaExportJobRejectsBadRange|TestQuotaExportScopeRequiresDashboardSession|TestUsageSelfAndEmployeeRejectQuotaFields|TestLogExportFileBase_Quota"` 通过。只接了 Redis。覆盖非法范围、控制台会话、usage/self/employee 拒绝 quota 字段，以及文件名里的渠道 0。
- `go test -count=1 -timeout 180s ./router/ -run TestAccessTokenRouteRulesCoverEveryDashboardRoute` 和 `go test -count=1 -timeout 60s ./i18n/ -run "TestLocales_KeySetsIdentical|TestConstants_ResolveInAllLocales"` 通过。
- 前端从 `web/` 运行额度请求单测和导出对话框 20 项测试通过。`bunx --no-install tsgo -b --pretty false` 通过。改动文件的 oxlint 无错误。本机 Bun 1.3.13 没有 `bun check`，`bun run typecheck` 仍会报脚本不存在，这次没有改那个脚本。
- 没有在浏览器里点过导出中心。SQLite 没有跑：模型测试只打开 `SQL_DSN` 指向的 MySQL 或 PostgreSQL。没有做旧库升级两次启动，因为这次没有 DDL。

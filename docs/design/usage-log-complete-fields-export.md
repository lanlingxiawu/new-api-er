# 使用日志完整字段导出

日期：2026-11-17

状态：用户于 2026-11-17 确认实施，已实现并完成本地专项验收；未部署生产。用户需求：在现有导出功能中增加完整字段导出，能够取得排障所需的底层错误、上游响应头和已保存的正文。

## 目标与范围

在管理员导出中心新增 Root 专用的“完整原始字段”模板。文件包含现有可读列，以及一列“原始日志 JSON”，保存这条日志的全部模型存储字段和未经查询脱敏处理的 `other`。例如数据库已保存的 `stream_diagnostic.read_error`，即使没有 `stream_diagnostic_available=true`，也能随文件取得。

“完整”指数据库实际保存的日志记录。正文采集时已有的省略、截断、脱敏和缺失仍如实保留，不承诺恢复当时未保存的数据。导出不读取渠道密钥、不追加请求正文文件、不请求上游补查，也不扩大 relay 的日志采集范围。

本功能复用后台导出任务、时间筛选、CSV gzip、分片、下载和自动清理。现有默认模板、“全部列”、普通管理员、用户自助和员工导出保持原语义。增加完整导出入口，不把已有普通导出隐式升级成私有诊断导出。

## Existing Logic and Related-Path Findings

以下结论以当前工作区实现为准。

| 代码位置 | 已核实的行为 | 本次处理 |
|---|---|---|
| `model/stream_diagnostic.go`，`Log.AfterFind` | 查询 `Log` 后移除 `stream_diagnostic`、`claude_diagnostic`，兼容转换公开流式字段；含诊断标识但 JSON 损坏时把 `other` 改成 `{}` | 保留全局安全边界，仅完整导出的局部查询绕过该钩子 |
| `model/log_export_columns.go`，`other_raw` | Root 列直接返回 `l.Other`，但此时通常已被上述钩子过滤 | 旧列保持兼容，新增明确的完整记录列 |
| 同文件，`BuiltinLogExportTemplates` | “全部列”按注册表自动收集全部列 | 必须明确排除新增原始记录列，另建完整模板 |
| 同文件，`ResolveLogExportColumnsForRole` | 未授权的 Root 列会被静默丢弃 | 对新增完整原始列明确拒绝越权请求，不能生成名为完整、实际不完整的文件 |
| `model/log_export_scan.go`、`model/log_export_scanner.go` | 时间范围、正序 keyset、显式投影、批量查询；使用 `LOG_DB`；普通扫描执行 `AfterFind` | 在同一扫描器中增加受控读取方式，复用筛选、游标和资源闸门 |
| `model/log.go`、`model/log_migrate.go`、`model/main.go` | SQL 日志模型包含 21 个数据库字段；`channel_name` 是只读字段，SQL 迁移包含它，ClickHouse 建表只有其余 20 个字段；可读渠道名也会在查询后另行填充 | 原始记录保留数据库值，不能把查询后补出的名称当成原始值；按日志库方言处理该字段 |
| `model/log_export_job.go`，`writeLogExport` | 当前工作区强制使用 `csv_gz`；明细和汇总可一次扫描；重定价会移除 `other_raw` | 沿用 CSV；完整原始导出拒绝纯汇总和倍率覆盖组合 |
| `model/log_export_template.go`，`dehydrate` | 模板结构校验用普通 Admin 的列解析器，只有 Root 列的模板可能被错误判为空 | 将结构有效性与操作者权限分开校验，支持 Root 保存完整模板 |
| `controller/log_export.go`，`resolveLogExportRequestShape` | 支持内置/自定义模板，也支持直接提交列；前端实际直接提交 `columns` | 是否需要完整读取由最终列集合决定，不能仅依赖模板 ID |
| `model/log_export_management.go`，`Permissions` | 普通 usage 管理员可管理其他管理员创建的普通任务，包括 Root 创建的任务 | 仅新增完整任务增加 Root 限制，保留普通任务的跨管理员管理契约 |
| `controller/log_export.go`，`DownloadLogExport` | 下载凭据换取文件时会重读用户状态和权限；支持分片及 Range | 在统一权限函数中保护完整任务，覆盖签发下载地址和实际下载 |
| `controller/log_export_management_test.go` | 已测试普通管理员可管理 Root 创建的普通任务 | 此回归必须继续通过，不能一概封锁所有 Root 任务 |
| `web/src/features/usage-logs/export/components/new-export-sheet.tsx` | 已有模板选择、自定义列、模板保存、异步任务和倍率覆盖 UI | 复用控件，新增模板和必要说明，不另建导出页面 |

相关设计背景：`usage-log-export.md`、`stream-diagnostic-visibility.md`、`log-export-ratio-override.md`。前两份旧文档中的范围、格式和私有诊断导出描述需要在实现后补充本次明确例外；当前自助/员工/审计导出均已存在，不按旧文档移除。

## 输出契约

### 模板与列

- 内置模板 ID：`builtin:full_raw`；名称：“完整原始字段”；用途：诊断；受众：内部；仅 Root 可见、可用。
- 列 ID：`log_raw`；名称：“原始日志 JSON”；`AdminOnly=true`、`RootOnly=true`、`NeedOther=true`。
- 完整模板包含原“全部列”的列集合，再加 `log_raw`。Root 也可以把 `log_raw` 单独加入自定义模板。
- `builtin:full` 显式排除 `log_raw`。已保存的模板和已创建的任务不会自动改变字段范围。

### 原始记录格式

`log_raw` 是一个 JSON 对象的文本，键使用数据库字段名。SQL 日志库包含：

```text
id, user_id, created_at, type, content, username, token_name, model_name,
quota, prompt_tokens, completion_tokens, use_time, is_stream, channel_id,
channel_name, token_id, group, ip, request_id, upstream_request_id, other
```

`channel_id` 使用数据库名称，区别于现有列表 API 的 `channel` 别名。ClickHouse 不存在 `channel_name` 存储列，原始对象中不伪造该字段；可读“渠道名称”列继续按原方式解析。

原始记录保留时间戳、日志类型数值、额度整数、零值、`false` 和空字符串。SQL 中允许 NULL 的字段通过可空的导出专用读取结构保留 NULL，不由通用 `Log` 的零值吞掉。不得用一般 `Log` 的 `omitempty` JSON 标签直接序列化后冒充完整原始记录。

`other` 保持数据库字符串值，不先解码成 `map[string]any`。这样可保留未知字段、大整数文本、原有字段名、空白、无效 JSON，以及其中的 `stream_diagnostic`、`claude_diagnostic`、`admin_info`、`root_info` 等。消费者解码外层 JSON 后取得的 `other` 字符串应与数据库值一致。若正文以 Base64 或首尾采样形式存储，保留该形式及省略字节标记。

CSV 仍用标准 CSV 转义和 UTF-8。长字段不新增截断。原始 JSON 对象以 `{` 开始，现有公式注入防护不会给其加前缀；普通文本列继续原防护。完整字段导出属于日志数据导出，不是数据库物理备份，不导出 schema、索引或模型外的历史遗留列。

## 数据流与实现边界

1. Root 在现有抽屉选择完整模板，提交原有时间范围和筛选条件。
2. Controller 解析模板/显式列，检查当前身份、范围、列数、模式和倍率覆盖组合。最终列含 `log_raw` 即为完整任务。
3. Redis 任务仍保存既有 `columns` 和 `creator_role`。两者共同约束后台执行，不能凭未校验的客户端开关启用原始读取。
4. Worker 开始扫描前复核创建者仍为启用的 Root。扫描期间在批次边界按至少一秒间隔复核；复核失败停止任务并清理文件。
5. 完整任务使用导出专用可空投影结构读取原始字段。该结构不带 `Log.AfterFind` 钩子，不修改全局 GORM 设置，也不在标准 `Log` 中增加原始数据缓存字段。投影字段固定且显式，按日志库方言选择；不使用 `SELECT *`。
6. 每批原始行另外构建普通 `Log` 视图，显式执行既有 `AfterFind` 兼容转换，再走现有行级筛选、渠道名称解析和普通列渲染。原始投影只用于 `log_raw`，普通渲染不能修改它。
7. 复用 `LogExportBatch`，新增 `runUsageBatches` 保留自助校验和员工逐客户扫描，再进入已有 `runBatches` 回调。完整导出的原始行与普通视图按行对应；普通任务不分配这份额外数据。仅对通过筛选的当前行调用 `common.Marshal`，只保留当前行的临时 JSON，避免整批预先生成大字符串。
8. 复用 `logExportOutput` 写入分片。支持 `detail`、`both`；`both` 的汇总仍从普通视图读取数值，原始 JSON 只进入明细文件。
9. 任务就绪后按现有入口下载；取消、失败、过期和清理沿用当前任务生命周期。

选择导出专用投影而不是全局跳过钩子，是为了同时保留 NULL 和原始名称，并防止未经脱敏的 `Log` 意外进入其他调用方。标准列表、详情、普通导出、自助、员工、审计扫描器继续使用各自既有读取方式；已移除的旧同步导出接口不恢复。

## API 与权限

不新增 HTTP 端点、数据库字段或请求顶层开关。

| 既有入口 | 完整导出的行为 |
|---|---|
| `GET /api/log/export/columns` | Root 返回新增列与模板；其他身份不返回该列或完整模板 |
| `/api/log/export/templates` 相关读写 | 含 `log_raw` 的自定义模板仅 Root 可读取、创建、更新、应用；共享也只共享给 Root。结构校验接受 Root 列，操作者校验仍在服务端执行 |
| `POST /api/log/export/jobs` | 保留原请求结构，接受 `columns` 包含 `log_raw`，或 `template_id=builtin:full_raw`；仅 Root 管理员 scope；非法组合明确返回错误 |
| `/api/log/export/jobs` 相关列表、详情、取消、删除、下载地址 | 含 `log_raw` 的任务通过统一 `LogExportAccess.Permissions` 额外限制为当前 Root 的 usage 管理入口 |
| `/dl/log-export/:token` | 继续验证下载凭据、数据源、任务和用户启用状态，再检查当前 Root 权限；包含打包、分片、Range 后续请求 |
| 用户自助、员工、审计导出入口 | 不提供或接受完整 usage 日志能力，伪造列或套用完整模板均不能绕过 |

创建响应、任务状态、进度和分片结构不变。完整任务不向普通管理员暴露可下载入口，不能通过复制任务 ID、共享模板 ID、旧下载地址绕过限制。已有不含 `log_raw` 的 Root 创建任务维持普通管理员可管理的现状。

创建者角色快照为零或非 Root 的任务不得执行完整读取。复核权限查询使用明确列、用户主键和有界超时；账号不存在、停用、降级或权限读取失败均按访问已变化处理。已完成文件的实际下载依据当前下载者权限校验。

按照 [OWASP 授权指南](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html)，权限检查覆盖服务端每个访问入口和静态文件下载。身份与下载凭据仍复用当前机制；参考 [ASVS](https://asvs.owasp.org/)、[Authentication Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html) 和 [Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html) 检查过期、撤权和绕过情形，不改动登录或会话模块。

## 界面与兼容规则

沿用现有模板 Select、列选择器、提示样式和任务列表。说明文案表达：“包含数据库已保存的完整日志及诊断数据；正文原有省略会保留。”新列、模板、提示和错误文案按项目 i18n 要求覆盖前端七种语言；后端用户可见消息覆盖中英文。

完整原始列只适用于含明细的导出。选择纯汇总或开启分组倍率覆盖时，界面给出直接的组合限制，要求切回明细或关闭倍率覆盖；服务端同样拒绝。不能悄悄丢弃 `log_raw` 或输出被改写的额度后仍称为原始记录。

倍率覆盖继续遵守已确认规则：替换费用，不额外输出原值对照。本次完整模式与倍率覆盖互斥，避免从 `log_raw` 重新泄出被排除的原始费用。

新模板仍可保存为 Root 自定义模板。角色变化后重新拉取目录和模板，不能继续使用上一个角色的缓存能力；即使客户端缓存未更新，后端仍拒绝越权请求。

## 数据模型 配置与索引

- 不迁移 `logs`，不改变日志写入、计费或私有诊断采集结构。导出原始投影是内存结构，不注册 AutoMigrate。
- 不迁移模板表：现有 `columns` TEXT JSON 足够存放新列键。Redis job 的既有字段足以标识完整任务。
- 不增加环境变量或设置项。沿用 `operation_setting` 下导出开关、时间跨度、任务并发、冷却、批次、超时、CPU 闸门、分片、磁盘检查、TTL 和下载限制。
- 为完整原始行限制单批最多 100 行，实际批量取 `min(现有配置, 100)`；普通任务保留原批量。此为原始数据读取的内部内存上限，不是新的用户设置。
- SQL 仍按 `created_at` 和 `id` 正序 keyset；按用户筛选复用 `(user_id, created_at, id)`。ClickHouse 沿用 `(created_at, request_id)` 和时间分区。无新增 WHERE/JOIN/ORDER 条件，无新索引；完整模式只扩大列投影。
- 验收在本地真实 MySQL、PostgreSQL 环境记录有界范围及用户范围的 EXPLAIN，确认未引入全表扫描、OFFSET 或逐条诊断查询。字段投影、NULL、保留字引用在 SQLite 和 ClickHouse 对应路径也必须覆盖。

## Main Chain Impact

relay goroutine 新增同步工作为零；不增加中间件、日志钩子、DB/Redis 操作或协程。不修改 `relay/`、账单结算和请求日志落盘流水线。全部新增处理在原本受并发槽约束的导出后台任务内。

原始数据导出会增加每批传输字节和 JSON 编码量。使用较小批次、已有 CPU/查询超时/磁盘闸门限制影响，不能把“没有热路径代码改动”等同于“没有共享资源消耗”。不增加并发查询、长事务或逐行外部调用。

### Shared Resource Audit

| 资源 | relay 是否使用 | 本次影响及约束 |
|---|---|---|
| `LOG_DB` 的 `logs` 表及连接池 | 异步日志写入使用 | 复用现有导出只读扫描；一任务同一时间最多一条日志查询；缩小原始批次；不改表、不加锁、不跨扫描持有事务 |
| 主库 `users` | 其他业务及权限路径使用 | 原始任务开始和运行期间按批次间隔查询创建者 `id/role/status`，最多约每秒一次；主键查询、有界超时；不写用户表 |
| 主库 `log_export_templates` | 否 | 沿用既有模板 CRUD，无新增查询路径到 relay |
| 渠道名解析及缓存 | 渠道数据由 relay 使用 | 完整模板的可读渠道列沿用既有批量解析/任务缓存；原始行保留存储值，不额外查询诊断或渠道凭据 |
| Redis `logexport:*` | relay 不访问该命名空间 | 继续使用既有任务、槽位、取消、下载凭据和 TTL；不新增 relay key 或全局锁 |
| 导出临时文件、gzip、任务局部内存 | 共享进程 CPU、堆、磁盘 | 原始 JSON 每行构造写出，最多 100 原始行/批；继续分片及取消失败清理；不缓存整个文件 |
| 后台协程/下载并发 | 进程资源共享 | 使用现有任务槽和下载限额；不新增每条记录协程或额外 worker 池 |

### Concurrency Analysis

在 30k 或 100k RPM relay 负载下，新增的每次 relay 请求成本均为 0 次 DB、0 次 Redis、0 把锁、0 个协程。导出成本随被导出的记录数增长，与即时 relay RPM 不形成逐请求绑定。

每个完整导出批次为 1 次有界日志 SELECT，加至多 1 次到期的创建者权限查询；渠道名成本复用已有路径。Redis 操作沿用现有任务进度/取消/槽位流程。日志扫描数量大致为各非空时间窗口 `ceil(行数/批量)` 加边界探测，不逐条查私有诊断。

现有默认任务并发为 1，批次默认 3000；完整模式将单批限制为 100。内存量级为这一批原始字段、普通视图和当前一行编码临时串；不声称固定字节上限，因为历史单条 `other` 的大小并不由导出控制。验收必须用宽字段样本记录内存/耗时，不能仅用短日志证明资源安全。

## 错误处理与审计

越权沿用既有导出权限拒绝路径；参数组合、模板和列错误通过现有 Controller 错误/i18n 助手返回可操作提示。任务期间撤权复用 `access_changed`。DB 查询、JSON 编码和文件写入失败使任务失败并清理未完成分片，不把某行原始字段悄悄写成空值。

原始 `other` 是字符串，因此其中 JSON 无效不构成外层导出编码失败。业务错误日志不得附带整个原始行、正文或密钥。审计只记录操作者、任务 ID、筛选和列集合等现有任务元信息；可从 `log_raw` 列标识完整导出。

取消、进程重启回收、TTL 过期、删除和下载 slot 释放复用现有生命周期；新增路径也必须保证失败/panic 后清理当前未登记分片。

## 验收与实施顺序

实施时先为原缺失建立回归，再实现受控原始投影、权限、模板及界面。测试扩展现有导出和诊断测试文件，覆盖可观察行为，不以孤立 Extract 测试代替数据库到文件的链路。

1. 真实 SQL 记录经过创建任务、批量读取、gzip/CSV 解码后，`log_raw` 中全部字段值一致；`other` 字符串逐字一致，包括未知键、大整数、非法 JSON、空字符串、NULL 和零值。SQL `channel_name` 保留存储值，不能被动态解析覆盖。
2. 使用合成诊断样本重现：`stream_diagnostic_available=false` 但有 `read_error`、旧 `claude_diagnostic`、正文省略标记和 Base64；完整模式包含它们，普通列表和普通导出继续移除私有诊断。
3. 非 Root 伪造显式列、内置模板 ID、自定义共享模板 ID、任务 ID 和下载 URL 均无法获得完整内容；覆盖自助、员工、审计入口及 Role=0 的旧任务结构。
4. Root 创建后降级/停用，运行任务停止并清理；已签发地址在实际下载及 Range 请求时重新拒绝。Root 创建的普通任务仍允许原有管理员管理。
5. 完整模板、自定义模板、仅 `log_raw` 列、`detail`/`both` 正常；纯汇总及倍率覆盖被明确拒绝；原始读取不改变行级异常筛选与汇总结果。
6. 宽字段超过普通表格软件单元格显示能力时，直接解析 CSV 仍得到完整字符串；多行、引号、逗号正确转义；分片、取消、失败和 TTL 清理保持正确。
7. 本地真实 MySQL、PostgreSQL 读写集成和 EXPLAIN，使用测试自建记录并按记录 ID 清理，不清空共享表。SQLite 补充兼容测试；ClickHouse 验证其 20 字段投影与原有游标。无法运行的环境应在验收记录中明确列出，不能以单一方言替代。
8. 运行相关 Go 包测试、前端 Vitest、`bun run typecheck`、`bun run lint`。不修改上游受保护认证文件，不触碰独立 `relaykit` 模块。

生产部署和生产验证由用户操作；本方案不授权直接连接或操作生产服务器。

## 实施记录与本地验收（2026-11-17）

### 实际改动

- `model/log_export_raw.go`：21 字段的可空 SQL 投影、ClickHouse 20 字段投影、逐行原始 JSON、独立普通视图、当前 Root 权限校验。未修改 `Log` 或全局读取钩子，未新增迁移。
- `model/log_export_scan.go`、`log_export_scanner.go`、`log_export_source.go`、`log_export_job.go`：共用原筛选和 keyset 查询；完整任务每批最多 100 行；任务开始、扫描期间和文件完成前复核权限。启动时 worker 与 scanner 各复核一次，运行期间按至少一秒间隔在批次边界复核，完成前再复核一次。普通导出及员工逐客户循环保留。
- `model/log_export_columns.go`、`log_export_template.go`、`log_export_management.go`、`controller/log_export.go`：新增模板和列；模板读写、任务创建/管理、签发下载地址和实际下载统一限制 Root。纯汇总与倍率覆盖组合拒绝；旧“全部列”明确不含 `log_raw`。
- `web/src/features/usage-logs/export/components/new-export-sheet.tsx`：复用模板与列选择器，加入模式冲突和权限变化提示，列目录/模板缓存键包含用户 ID 和角色。五条新文案补齐前端七种语言；后端三份语言文件补齐列名和错误提示。

### 验收环境与命令

全部使用本机隔离的测试实例和合成日志，未访问生产或真实上游。主库为 MySQL；日志库分别为 MySQL 和独立 PostgreSQL，Redis 为本机独立 Redis 7。服务端报告版本分别为 MySQL `26.10.0`、PostgreSQL `18.7 (Debian 18.7-1.pgdg13+2)`。SQLite 内存库仅用于投影兼容补充测试。测试记录按自建行清理，未清空共享表。

在分别指向上述两组隔离实例的 `SQL_DSN`、`LOG_SQL_DSN`、`REDIS_CONN_STRING` 下执行：

```text
go test -p 1 ./model ./controller ./i18n -run 'LogExport|ExportManagement|CompleteLogExport|StreamDiagnostic|EmployeeExport' -count=1 -timeout=15m -v
go test -p 1 ./model -run 'LogExportRaw|CompleteFieldsAcrossBatchesAndParts' -count=1 -timeout=15m -v
go build ./...
```

首组命令在两种日志库下各通过 215 个顶层测试，额外三项测试也各通过。仅既有显式 opt-in 的 `TestLogExportRepriceLargeData` 跳过；首组命令对 `i18n` 包无匹配测试，翻译键由 model 中的导出列翻译测试覆盖，不把该包无测试记作功能覆盖。

前端在 `web/` 执行：

```text
bun run i18n:sync
bun run test src/features/usage-logs/export
bun run lint
bunx --no-install tsgo -b
bun run build --dist-path ../_local/complete-export-20261117/web-dist
```

现有 `bun run typecheck` 脚本指向 `bun check -b`，因项目没有 `check` 脚本而失败；未改动该已有脚本。使用已安装 TypeScript 编译器 `tsgo -b` 完成等价项目类型检查。前端导出模块 8 个文件、32 个测试通过；lint、类型检查和生产构建通过。构建输出保存在本地验收目录，不作为已发布或已部署的证明。

### 已覆盖的结果

- 数据库到 CSV.GZ：完整 21 字段、SQL NULL、零值、false、带逗号/引号/换行的文本、原始 `channel_name`、未知大整数文本、无效 `other` JSON；普通视图和普通导出仍剥离私有诊断。
- 诊断场景：`stream_diagnostic_available=false` 时已有的底层错误仍原样导出；旧 `claude_diagnostic`、Base64、正文省略标记和超过 32 KiB 的正文保留。没有引入诊断逐条补查。
- 权限：非 Root 的显式列、带空白的列名、内置模板、共享模板、任务详情、删除、签发地址和直接下载均被拒绝；下载者降级后旧地址和 Range 请求被拒绝；worker 拒绝旧 role=0、普通管理员、自助、审计及不兼容选项，运行中撤权在下一次复核时停止。原有普通管理员管理 Root 普通任务的回归通过。
- 一次生成明细与汇总时，原有异常行筛选/汇总保持有效；原始旧字段仍保留在 `log_raw`。界面在纯汇总或倍率覆盖冲突时禁止提交并显示处理方法。
- 跨批次/文件：同一秒内 105 条日志，每条诊断正文 40,000 字节，批次为 100 + 5、文件为 50 + 50 + 5；ID 顺序、行数和每条原始 `other` 全部一致。PostgreSQL 一次写出耗时约 74 ms、累计分配 34,370,088 字节；MySQL 约 77 ms、累计分配 34,282,368 字节。此为本地样本的累计分配和耗时，非峰值 RSS、生产容量保证或 100k RPM 压测。
- 查询计划：两种数据库各插入 20,000 条独占样本，分别检查一小时范围、指定用户范围及后续 keyset 页。MySQL 使用 `idx_created_at_id` / `idx_log_export_user_time` 的 index range scan；PostgreSQL 使用对应 index scan。无全表扫描、OFFSET 或新增索引；验收后按样本标记删除自建行。

### 限制与交接

- 这是本次导出功能的专项回归与构建结果，未宣称运行了整个仓库全量测试或生产验收。无模型/schema/索引迁移变更，因此本次没有部署数据库迁移。
- ClickHouse 通过 20 字段投影与游标所需列的单元合同验证，未启动 ClickHouse 实例做集成验证；当前生产环境未启用该日志库。
- 不改变原有日志采集范围；历史未入库的正文、已截断部分不能恢复。导出完整范围是项目 `Log` 模型的存储字段，不是数据库物理备份。
- 本次未连接、修改、重启或部署生产。发布后由用户以 Root 在“使用日志 → 导出 → 新建导出”选择“完整原始字段”，核对目标请求的 `log_raw.other` 和下载结果。

原始验收输出保留于本地 `_local/complete-export-20261117/`，包括两库回归、EXPLAIN、前端检查和构建日志。

# 员工客户数据导出与管理员模板管理

日期：2026-10-01  
状态：已实现。权限模型为“总开关 + 在职员工”；模板全员共用，包括内置的客户对账单和管理员自定义模板。用户于 2026-10-01 确认。

## 1. 目标与范围

“客户数据”指客户的使用日志、消费明细和聚合汇总。客户档案、充值记录、员工佣金不在本功能范围内；如需要，应先调整本设计。

- 管理员通过一个总开关开放员工导出：开启后所有在职员工都能导出，不按员工单独开通。
- 模板全员统一，不按员工分配：
  - 内置一个模板“客户对账单”，沿用导出中心管理员的同名模板，只读、不可删除。
  - 管理员可新增、查看、编辑、删除和启停自定义模板；启用的自定义模板对所有员工可见。
- 员工通过下拉选择客户和模板，按模板确定的字段、顺序、格式和输出模式导出。
- 员工只能导出当前归属自己的客户，不能通过请求参数扩大数据范围或添加列。
- 模型、用户、令牌、渠道、分组筛选均为可搜索下拉；员工端只提供模板开放的筛选项。
- 复用现有后台任务、进度、取消、分片、下载、过期清理和资源限速。

## 2. 已核对的现状

- `controller/log_export.go`、`model/log_export_template.go` 已有管理员个人／共享模板 CRUD，与员工模板相互独立。
- `model/log_export_columns.go` 的 `BuiltinLogExportTemplates()` 提供管理员内置模板，员工端只复用其中的客户对账单（`builtin:customer_invoice`）。
- `/api/log/export/*` 属于管理员入口；原下载函数只允许启用的管理员下载。
- `model/customer.go` 的员工客户列表以 `users.inviter_id` 和普通用户角色为准，不能只依据可能滞后的 `customer_profiles` 判定归属。
- 日志过滤已有单用户 `UserId`，适合按客户分批扫描；主库与日志库可能分离，不能依赖跨库 JOIN。
- 列定义里的 `AdminOnly` 表示只有管理员能读取（渠道 ID、重试链等）；`ResolveLogExportColumns(keys, isAdmin=false)` 会剔除这些列。
- 既有设计见 `usage-log-export.md`、`usage-log-export-anomaly-and-templates.md`、`usage-log-export-throttle-accounting.md`。本功能不改变普通用户自助导出接口。

## 3. 产品交互

### 管理员

- **开关**：系统调优 → 使用日志导出设置 → “启用员工客户数据导出”，默认关闭。说明文字写明对所有在职员工生效。
- **模板**：导出中心 → “员工导出模板”弹窗，分两部分：
  - 内置模板：列出客户对账单，带“内置”徽章以及格式和输出模式，只读。
  - 自定义模板：支持搜索、新增、编辑（右侧抽屉，保存区固定）、删除和启停。配置项包括名称、导出列及顺序、文件格式、输出模式（明细／汇总／明细+汇总）、汇总维度、文件选项和可用筛选项。可选列为员工列目录，即除 `AdminOnly` 以外的全部列（2026-10-01 测试时共 72 个）：
    - 可发给客户的字段，以及客户名称 `username`；
    - 内部字段，如 IP、详情、额度、耗时、首字时间、上游模型、流式状态，选择器中带“内部”徽章；
    - 渠道、重试链等 `AdminOnly` 列始终不开放。

    新建模板时默认预选客户对账单的字段（含 `username`）。
  - 汇总设置里有“汇总包含额度”勾选框。它与导出列中的“额度”（`quota`）是同一个开关：模板含 `quota` 时，员工汇总表才输出额度。
- 删除自定义模板时会提示：员工将无法再使用该模板，使用该模板且尚未完成的任务和下载链接也会失效。删除操作只删模板本身，不删用户或日志。
- 员工管理页面不再提供导出相关入口。

### 员工

在“我的客户”页头和员工客户日志筛选栏提供导出入口，打开右侧抽屉：

1. 在模板下拉中选择模板，内置模板排在前面，默认选中“客户对账单”。模板的列、格式和输出模式以只读方式预览。
2. 选择时间范围和客户：可搜索、多选，也可勾选“全部我的客户”。
3. 设置模板开放的筛选项，提交后在“导出历史”查看进度并下载。

员工不能管理模板、临时增删列或覆盖模板的格式和模式。“全部”始终表示自己的客户；客户集合为空时直接报错，不会省略用户过滤。总开关关闭、账号停用或员工档案停用时，不显示导出按钮。

## 4. 内置模板

| key | 名称 | 列来源 |
| --- | --- | --- |
| `builtin:customer_invoice` | 客户对账单 | 管理员同名模板的列，首列补 `username` |

- 列由 `EmployeeBuiltinExportTemplates()` 从 `BuiltinLogExportTemplates()` 派生，不单独维护，管理员模板调整后会自动同步。派生规则：
  - 剔除 `AdminOnly` 列，保证预览与文件一致，也与“员工日志隐藏渠道”的约定一致。
  - 缺少 `username` 时补到首列，让多客户文件能区分客户。
- 固定为压缩包格式 `csv_gz`（单个分片下载为 `.csv.gz`，多个分片打包成 `.zip`）、带 BOM（Excel 直接打开中文不乱码）、明细模式、带表头，开放模型／令牌／分组筛选。模板不固定时区，任务使用员工浏览器提交的 `timezone`；未提交或服务器 tzdata 中没有该时区时，使用服务器时区，不阻断导出。
- 员工导出复用管理员导出的同一套任务、扫描、写文件和下载链路，没有另写一套。下载规则（单分片直出、多分片打 zip）和下载文件名（客户_模板名_日期范围_筛选条件）见 `usage-log-export.md` §6.5。
- 内置模板永远可用，没有启停和删除操作。员工请求只接受 `builtin:customer_invoice`；计费明细、旧版导出、审计、全部列等其他管理员内置模板一律拒绝。需要更多字段时，由管理员建自定义模板。

## 5. 数据流和生命周期

管理员保存自定义模板 → 主库保存 → 员工读取能力和模板列表 → 提交模板 key、客户选择和筛选条件 → 服务端复核：
- 总开关；
- 账号和员工档案状态；
- 模板（内置 key 或已启用的自定义模板）；
- 客户归属和字段。

复核通过后，保存客户端无法修改的任务快照 → 后台限速扫描日志 → 写明细／汇总分片 → 授权下载 → 到期清理。

- 模板内容在创建任务时写入快照，包括 key、名称、版本、字段、格式、模式、维度和文件选项。之后修改模板只影响新任务。
- 任务记录操作员工 ID、模板 key，以及服务端解析出的客户 ID 集合，这些字段都不接受客户端赋值。
- 扫描开始前复核一次，之后在批次边界按 `employeeExportRevalidateInterval`（1 秒）节流复核：总开关、账号／员工状态、模板仍可用、客户归属。撤权后任务最多再扫描约 1 秒。不逐个时间窗复核，因为多客户、长时间段的任务里，空窗口也会触发复核，查询量会达到日志查询本身的数倍。客户被转移后，包含该客户的旧任务整体失效，不会继续生成混合范围的部分结果。
- 员工停用、总开关关闭、自定义模板停用或删除后，拒绝新任务和后续下载；运行中的任务在下一个批次停止，并清理未完成文件。
- 签发下载链接和实际兑换链接时都会重新复核。已经传出的字节无法撤回，但关闭权限后，后续下载和续传请求都会被拒绝。
- 写文件时，员工任务按非管理员解析列（`ResolveLogExportColumns(cols, false)`）。即使快照里有 `AdminOnly` 列也不会写出。
- 员工任务的汇总分片是否输出“额度”，取决于模板的导出列是否包含 `quota`：包含就输出，否则只有调用次数、tokens 和金额。这样汇总与明细保持一致。管理员任务的汇总始终输出额度。
- 明细+汇总模式下，汇总维度即使不在明细列里，也会加入 SELECT。管理员任务同样如此。
- 员工路由（`/api/user/employee/export/jobs*`）只列出和操作本人的员工任务。同时是管理员的员工，在这里看不到自己的管理员任务。管理员导出中心保持原有语义。
- 空结果、超限、磁盘不足、超时、Redis 不可用，均沿用现有导出任务的反馈方式。

## 6. API 合约

业务响应统一为 `{"success":true,"data":...}` 或 `{"success":false,"message":"本地化提示"}`。未认证由认证中间件处理；权限不足返回 403；业务校验失败使用 `ApiErrorI18n`。

### 管理接口

以下接口位于 `/api/admin/employee-export` 组，使用 `AdminAuth()` 和员工管理权限（`AdminMenuEmployeesView`）：

| 方法与路径 | 请求／返回 |
| --- | --- |
| GET `/columns` | 员工可用列、默认列（含客户名称）和列数上限 |
| GET `/templates` | `keyword,cursor,limit`；返回 `items`（自定义模板）、`builtin`（内置模板）、`next_cursor` |
| GET `/templates/:id` | 返回自定义模板详情 `template` |
| POST `/templates` | 新建自定义模板 |
| PUT `/templates/:id` | 完整更新，带 `version` 防止并发覆盖 |
| DELETE `/templates/:id` | 删除自定义模板 |

模板请求示例：

```json
{
  "name": "客户消费明细",
  "enabled": true,
  "columns": ["username", "created_at", "model_name", "cost_usd"],
  "format": "xlsx",
  "mode": "detail",
  "summary_dims": [],
  "options": {"csv_bom": true, "header": true, "timezone": "Asia/Shanghai"},
  "allowed_filters": ["model_name", "token_name", "group"],
  "version": 1
}
```

模板对象对外带 `key`（内置为 `builtin:*`，自定义为十进制 ID 字符串）和 `builtin` 标记。输出列不能为空，不允许未知列、重复列或 `AdminOnly` 列。请求体中多余的顶层字段会被忽略。

### 员工接口

`/api/user/employee/export` 组使用 `UserAuth()`。除 `/capabilities` 外，其余路由经过 `EmployeeExportGuard`，复核总开关、账号和员工状态：

| 方法与相对路径 | 请求／返回 |
| --- | --- |
| GET `/capabilities` | `enabled,templates,columns,max_range_sec,max_customers`；`templates` 先列内置模板，再列已启用的自定义模板；`columns` 提供所有非 `AdminOnly` 列的标题，供预览使用 |
| GET `/options` | `field,keyword,cursor,template_id`，以及必要的 `customer_ids` 和时间范围；返回 `items,next_cursor`。客户选项每页 50 条，日志值每批最多扫描 500 行 |
| POST `/estimate` | 参数与创建任务相同；返回授权范围内的估算 |
| GET `/jobs` | 本人任务列表 |
| POST `/jobs` | `template_id`（模板 key 字符串）、`timezone`、`customer_ids`、`all_customers`、`start_timestamp`、`end_timestamp`、`filters`；返回 `job_id` |
| GET `/jobs/:job_id` | 进度和公开任务信息 |
| DELETE `/jobs/:job_id` | 取消或删除本人任务 |
| GET `/jobs/:job_id/download-url` | 一次性下载地址及有效期 |

管理员筛选下拉使用 `GET /api/log/export/options`，保持现有管理员导出认证和权限规则。

员工请求中出现其他字段（`columns/options/format/mode/summary_dims` 等）或模板未开放的筛选项，直接报错。`timezone` 只对未固定时区的模板（内置模板）生效，无法识别时回落到服务器时区。客户 ID 必须全部通过归属校验，不会忽略越权项后悄悄导出其余客户。估算、下拉、任务和下载使用同一套范围规则，避免旁路泄露。

## 7. 数据模型、索引和迁移

- `employee_export_templates` 为独立小表，字段：`id,name,enabled,definition,version,created_by,updated_by,created_at,updated_at`。
  - 名称唯一。
  - 列、格式、模式、汇总维度、文件选项和允许的筛选项存入 `definition` TEXT，经 `common.*` 序列化，对外展开为对应字段。
  - 按主键分页。
- 内置模板只在代码中定义，不落库。
- 通过 GORM AutoMigrate 建表，兼容 SQLite、MySQL 5.7.8+、PostgreSQL 9.6+。
- Redis 任务结构带可选的 `employee_scope`（`template_key,template_name,template_version,customer_ids`），老管理员任务按原语义读取。员工任务必须明确带 scope，缺少 scope 的任务不能当作员工任务处理。

日志查询限定时间范围和客户 ID，复用时间窗与 keyset 扫描，不做全表 DISTINCT，也不做无时间上限的统计。按客户逐个扫描后汇总，避免跨日志库查询用户表。多客户任务按客户、时间、日志游标排序。

日志索引 `idx_log_export_user_time(user_id,created_at,id)` 是**可选优化**，不是启用员工导出的前提。新日志表通过模型定义直接带上它；已有的日志表，`migrateLogTable` 发现缺失只会告警，不会在启动时建。没有这个索引时，按客户的查询走现有的 `idx_logs_user_id` 和 `created_at` 索引，不会全表扫描（2026-10-01 测试服 PostgreSQL 日志库约 250 万行，执行计划见下表）。启动告警里“退化为全表扫描”是缺索引时的通用措辞，不适用于这里。

| 查询 | 无新索引 | 有新索引 |
| --- | --- | --- |
| 单客户 + 1 天窗口 | `idx_logs_user_id` 与 `idx_created_at_type` 两个 Bitmap 取交集，再排序 | `Index Only Scan using idx_log_export_user_time`，无额外排序 |
| 单客户 + 30 天 | `idx_logs_user_id` 扫描后按时间过滤，再排序 | 同上 |
| 管理员按时间范围 | `idx_created_at_type` 范围扫描，再增量排序（与新索引无关） | 不变 |

扫描器按时间窗分批查询，每批的时间范围很窄，所以没有新索引时开销主要是每批多一次排序。客户的历史日志很多时，单用户索引扫描的成本随该客户的总行数增长。上线前先在目标库上对上表的查询跑 `EXPLAIN`，开销明显时再建索引：

- PostgreSQL：`docs/migrations/employee-export-postgresql.sql`，使用 `CREATE INDEX CONCURRENTLY`，必须在事务外执行。
- MySQL：`docs/migrations/employee-export-mysql.sql`，先检查索引是否存在；使用 `ALGORITHM=INPLACE, LOCK=NONE`，不回退到阻塞建表。
- SQLite：`docs/migrations/employee-export-sqlite.sql`，在离线维护窗口执行。

建索引会让日志写入多维护一棵 B-tree，所以要在维护窗口执行并观察容量。ClickHouse 不执行 SQL 索引脚本，继续沿用既有的 `(created_at,request_id)` 游标和时间窗。

测试服的日志库还存在一处历史遗留的差异：`idx_created_at_id` 的实际列顺序是 `(id, created_at)`，与代码（及 main）定义的 `(created_at, id)` 相反。这是早期版本建的索引，GORM 不会修改已存在的同名索引，启动检查也只比对索引名，所以一直没有告警。管理员导出目前靠 `idx_created_at_type` 完成时间范围扫描。这个问题与本功能无关，生产库是否同样如此需要单独核对。

## 8. 配置与业务边界

在现有 `log_export_setting` 中：

- `employee_export_enabled`：默认 `false`，员工导出总开关，对所有在职员工生效。
- `employee_max_customers_per_job`：默认 100，允许 1–1000，包含“全部我的客户”解析后的数量。

“在职员工”指账号状态为启用，且 `employee_profiles.status = 1`。

时间跨度、任务并发、冷却、扫描速度、查询超时、分片、汇总组数、XLSX 行数上限都沿用现有设置。员工任务和管理员任务共享现有导出并发预算，不另建不受限的执行器。

自定义模板名去掉首尾空白后为 1–64 字符，在员工模板集合内唯一，也不能与内置模板英文名相同（不区分大小写）。字段已失效的旧模板不能用于新任务，需要管理员编辑。版本冲突时提示刷新。CSV 单元格安全沿用现有 writer。

## 9. 错误处理、审计和国际化

控制器先记录底层错误，再返回本地化的、可操作的提示，不返回 SQL、内部路径或原始 Go 错误。各错误对应的返回：

| 错误 | 返回 |
| --- | --- |
| 越权（非员工、他人客户、模板不可用） | 403 |
| 名下无客户 | `employee_export.no_customers` |
| 超过客户上限 | `employee_export.too_many_customers`，带上限 |
| 模板重名 | `employee_export.name_taken` |
| 版本冲突 | `employee_export.conflict`；前端同时刷新模板列表 |
| 其他参数错误 | `employee_export.invalid` |

前端不重复弹提示：非静默请求由 HTTP 客户端统一提示，调用方不再额外 toast。筛选下拉使用静默请求，把错误和重试显示在控件下方，避免多个下拉同时失败时提示叠加。后台失败在任务状态里给出可读原因，同时释放并发槽并清理文件。

审计记录：
- 自定义模板保存（`employee_export.template_save`）和删除（`employee_export.template_delete`）；
- 导出任务创建，包括操作人、任务 ID、时间范围、列数、格式和筛选条件；员工任务另记 `template_key`、`template_version`、`customer_count`。

审计不记录令牌值或导出文件内容。

前端七种语言同步；后端文案以 `i18n/locales/en.yaml`、`zh-CN.yaml`、`zh-TW.yaml` 为准。内置模板名称是英文原文，前端通过 `t(name)` 翻译；自定义模板名称按管理员输入原样显示。

## 10. Main Chain Impact

relay goroutine 上新增的同步操作为 0：不插入 relay 中间件、计费 hook 或日志写入 hook。导出只发生在独立的管理请求和已有的后台导出任务内，不修改 relay 用户缓存。

### Shared Resource Audit

| 资源 | relay 是否访问 | 隔离和预算 |
| --- | --- | --- |
| `employee_export_templates` | 否 | 独立小表、主键访问 |
| users、employee_profiles | 是／相关业务使用 | 只在导出 API 和批次边界做有界读取，不加行锁，不改 relay 缓存 |
| logs／日志存储 | 是，日志后台写入 | 复用限速扫描、时间窗、查询超时；索引验证后启用 |
| Redis | 共享实例 | 复用导出任务专用键；新增范围状态只在导出命名空间内，不读写 relay key |
| DB／Redis 连接池 | 是 | 复用既有导出并发上限，不为每个客户启动并发查询，不扩大连接池 |
| 导出 worker、文件及下载并发 | 导出专用 | 共用全局预算，分片流式写入，不全量载入内存 |

AI 请求量从当前 30k RPM 增长到 100k RPM 时，每条请求新增的 DB／Redis 调用、锁和 goroutine 都是 0。后台任务仍会与 relay 竞争 CPU、IO 和共享连接，因此复用现有限速、CPU 闸门和取消机制。

员工任务每次复核执行最多 4 次有界查询，整体超时 3 秒。复核至多每秒一次：
1. 账号状态；
2. 员工状态；
3. 自定义模板启用状态（内置模板不查库）；
4. 客户归属。

之后才执行现有的日志扫描。客户按顺序扫描，不为每个客户新增 goroutine。新索引会让日志后台写入多维护一棵 B-tree，所以在线建索引需要单独的维护窗口，并观察容量。

## 11. 测试与验收

- 模型（`model/employee_export_integration_test.go`）：
  - 总开关下任意在职员工可导出，非员工被拒绝；
  - 内置模板集合和顺序；列与管理员模板逐列对应，只差 `AdminOnly` 列；含客户名称；解析时不剔除任何列；返回副本，不共享定义；
  - 只有客户对账单是内置模板；计费明细、旧版导出等其他内置 key、畸形 key 和不存在的 ID 被拒绝；
  - 员工列目录包含内部字段（带内部受众）、不含 `AdminOnly` 列；模板可选内部列，选 `AdminOnly` 列被拒绝；
  - 模板含 `quota` 时员工汇总输出额度，不含时不输出；
  - 自定义模板停用、删除，客户转移，员工／账号停用后，任务失效；
  - 员工任务快照中的 `AdminOnly` 列不会写入文件；
  - 模板列表内置在前、隐藏停用模板；
  - 客户数量边界（超限、名下无客户、转移优先报越权）、自定义模板重名（含内置名）、明细与汇总范围、批次边界撤权与复核节流、筛选项范围、查询计划；
  - 管理员明细+汇总模式中，汇总维度不在明细列时仍能正确分组（`log_export_summary_test.go`）。
- 控制器（`controller/employee_export_test.go`）：
  - 请求字段防覆盖；模板固定的时区优先于员工提交的时区；
  - 未单独配置的新员工可用内置模板导出，使用员工时区；无法识别的时区回落到服务器时区；非员工被拒绝；
  - capabilities 只列出客户对账单这一个内置模板和已启用的自定义模板，列标题中不含 `AdminOnly` 列；
  - 管理员列目录接口返回内部字段并带 `internal` 受众，不含渠道类字段；默认列不含额度；
  - 客户转移后下载与续传被拒绝；模板删除；筛选项按模板开放范围控制（含内置 key）；
  - 各类错误返回不同的本地化提示，且不含内部错误文本；员工路由不暴露管理员任务；任务审计含模板与客户数量。
- 数据库使用项目测试环境（MySQL 主库、PostgreSQL 日志库、Redis），只清理测试行；无环境时回退到 SQLite 内存库。

验收场景：管理员开启总开关 → 任一在职员工无需其他配置，即可选择“客户对账单”导出自己的客户 → 文件字段与预览一致 → 员工不能导出其他员工的客户 → 关闭总开关后员工不能再创建任务或下载旧文件。

## 12. 界面

| 场景 | 位置与展示 |
| --- | --- |
| 开关 | 使用日志导出设置；标签和说明与同区块其他开关一致 |
| 管理模板 | 导出中心 → 员工导出模板；内置模板只读区和自定义模板区分开展示；编辑使用右侧抽屉，保存区固定 |
| 员工导出 | “我的客户”页头和客户日志筛选栏；右侧抽屉，“新建导出／导出历史”分开，先选模板，再选时间和客户，最后设置筛选；提交按钮固定在底部 |
| 结果预览 | 明细列折叠展示；纯汇总只显示汇总维度和指标说明 |

界面使用 Base UI 组件和 Tailwind 主题变量，包含空状态、加载状态、错误重试和长名称截断。估算请求等参数稳定且有效后才发送；估算失败时不弹全局错误。搜索下拉的“加载更多”放在菜单内部，保留搜索上下文。

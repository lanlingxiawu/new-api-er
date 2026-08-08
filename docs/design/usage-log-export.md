# 使用日志导出（下载）功能重构设计

状态：**已实现**（设计已确认并落地；本文档已按实际代码回写，见 §16 实现记录）
作者：Claude
日期：2026-07-27

> 本期定位：**管理员专属的后台导出功能**。CLAUDE.md Rule 8.1 说「管理后台不需要满足高并发设计标准」，但本功能是**长时间、大数据量、持续占用 CPU 与数据库**的后台作业，与普通管理页面的一次性查询性质不同。因此本设计对它按 relay 旁路作业的标准做资源治理——见 §10「资源治理」与 §11「Main Chain Impact」。

---

## 1. 背景与现状问题

现有实现（同步流式 xlsx）：

- 入口：`GET /api/log/export`（管理员）、`GET /api/log/self/export`（自助），见 [router/api-router.go:406-408](../../router/api-router.go#L406-L408)
- Handler：`exportLogsExcel`，见 [controller/log.go:65-232](../../controller/log.go#L65-L232)
- 数据层：`model.ExportLogs`，`FindInBatches(1000)` 回调，见 [model/log.go:625-669](../../model/log.go#L625-L669)
- 限流：`LogExportRateLimit()`，默认 10 分钟 1 次

已确认的缺陷：

| # | 问题 | 影响 |
|---|---|---|
| P0 | **同步长连接**。整个导出在一次 HTTP 请求内完成，nginx/网关默认 60s~300s 超时，大数据量必然中断且无法续传 | 大数据量下功能不可用 |
| P0 | **列写死 14 列**。`other` 里的缓存 token、各类倍率、首字延迟（frt）、计费模式、审计信息全部拿不到 | 拿不到需要的数据 |
| P0 | **无任何资源节流**。批与批之间不休眠、不看系统负载，导出会持续满速读 `logs` 表并满速做 xlsx 压缩——而 relay 结算路径正在往同一张表 INSERT | **可能拖慢正常服务** |
| P1 | **xlsx 格式 CPU 代价高**。xlsx 是 zip + XML，单核压缩 + 大量字符串拼接；单表 1048576 行上限靠切 Sheet 绕过，多 Sheet 文件极难被下游消费 | CPU 占用高、文件大 |
| P1 | **`FindInBatches` 走主键游标**。`WHERE id > last_id` 配合 `created_at` 范围过滤，MySQL 可能选主键索引全扫再过滤，而不是走 `idx_created_at_id` | 大表扫描效率低 |
| P2 | 跨度、批大小、限流全部硬编码，不可运营配置 | 无法按部署规模调整 |
| P2 | 无任务视图：看不到进度、失败原因，无法重下 | 体验差 |
| P2 | 前端注释仍写 "Export logs as CSV"（实际是 xlsx） | 过期注释 |

---

## 2. 目标与范围

### 2.1 目标

1. **管理员导出中心页面**：创建任务、查看进度、分片下载、管理模板。
2. **可选列**：后端维护权威列目录，管理员勾选需要的列。
3. **导出模板**：默认模板 = **「使用日志页面所见即所得」**；另有若干内置预设 + 自定义模板。
4. **分块下载**：后台任务产出分片文件，可逐片下载或打包下载；每片支持 HTTP Range 断点续传。
5. **资源可控**：CPU、数据库、内存、磁盘、带宽全部有明确上界与自适应退让机制，导出跑起来时正常服务不受可感知影响。

### 2.2 范围

**包含**：普通使用日志（`logs` 表，前端 `/usage-logs/common`）的**管理员导出**。

**不包含**（本期）：
- **普通用户自助导出维持现状不变** —— 仍走 `GET /api/log/self/export` 同步 xlsx + 10 分钟 1 次限流。新的后台任务体系**仅对管理员开放**，理由见 §2.3。
- 绘图日志（`midjourneys`）、任务日志（`tasks`）—— 列目录预留 `log_category` 扩展点，不实现。
- 员工/客户视角（`employee` scope）导出 —— 当前前端已隐藏该 scope 的导出按钮，保持隐藏。

### 2.3 关键设计决策

| 决策点 | 选择 | 理由 |
|---|---|---|
| 面向对象 | **仅管理员**（`AdminAuth`） | 后台任务会占用服务器 CPU/磁盘/DB，不能让任意普通用户发起。管理员数量有限、行为可审计，配额与并发才好控制 |
| 分块语义 | 服务端分片文件 + 每片 Range 续传 | 单片可独立重下；Range 让单片本身也能续传 |
| 主格式 | **csv.gz**；xlsx 仅在行数 ≤ 阈值时可选 | csv.gz 无行数上限、写入 CPU 远低于 xlsx、体积小；xlsx 保留给「几万行拿去做表」 |
| 默认模板 | **`builtin:as_displayed`（页面所见）** | 用户最常见的诉求是「把我在页面上看到的这些导出来」 |
| 模板归属 | 内置预设 + 管理员自定义（新表）+ 共享 | 内置覆盖多数场景；自定义解决长尾 |
| 页面形态 | `/usage-logs/export`，usage-logs 的第 4 个 section | 复用现有 tab 导航与 `$section` 路由，不新增顶层菜单 |
| 旧接口 | `GET /api/log/export` 与 `/self/export` 全部保留 | 兼容已有脚本；Redis 未启用时作为降级路径 |

---

## 3. 总体数据流

```
① 创建任务（管理员）
   导出中心 → POST /api/log/export/jobs {filters, columns[], format, template_id?}
   → 校验（角色/时间跨度/列白名单/磁盘余量/并发槽/冷却）
   → 落 job（Redis, TTL 24h）→ 返回 job_id
   → 后台 goroutine 启动（受全局信号量限制，默认同时仅 1 个）

② 后台导出（独立 goroutine，与 HTTP 请求解耦）
   for 时间窗口 (end → start, 每窗 1h):
     for keyset 分页 (created_at desc, id desc):
       ┌ 资源闸门：CPU 水位检查 → 令牌桶限速 → 自适应 sleep
       ├ LOG_DB 查一批（batch_size=3000，每批独立 ctx 超时 10s）
       ├ 渠道名解析（内存缓存，跨页复用）
       ├ 按列投影成 []string（复用行缓冲，other 每行只解析一次）
       ├ 写入当前分片（csv → gzip(level=1) → bufio(4MB) → file）
       ├ 行数达 rows_per_file 则关片、开新片（并复检磁盘余量）
       └ 更新进度（按时间轴推进，绝不 COUNT）+ 检查取消/超时
   → job.status = ready, 记录 parts[]

③ 下载
   轮询 GET /api/log/export/jobs/:job_id → status/progress/parts[]
   → GET /api/log/export/jobs/:job_id/download-url?part=N → 一次性 token
   → 浏览器 GET /dl/log-export/:token（无需自定义头）
   → http.ServeContent 提供 Range/206/断点续传；part=all 时流式 zip

④ 清理
   job TTL 到期 / 手动删除 / 新任务创建时被动清理孤儿文件 / 启动时清理
```

---

## 4. 列目录（Column Catalog）

### 4.1 设计

后端维护**唯一权威**的列注册表（`model/log_export_columns.go`），前端只消费不定义：

```go
type LogExportColumn struct {
    Key       string   // 稳定标识，模板存这个，永不改名
    LabelKey  string   // i18n key；CSV 表头按请求语言渲染
    Group     string   // basic / tokens / billing / performance / admin / audit
    AdminOnly bool     // 供旧的自助同步导出路径复用（新链路本身已是管理员专属）
    NeedOther bool     // 是否依赖 other 字段 —— 决定 SELECT 是否带上 other 列
    Extract   func(*Log, *rowCtx) string
}
```

`Extract` 三类来源：**直接列**（`created_at`、`quota`）、**`other` JSON 路径**（`other.frt`、`other.admin_info.use_channel`）、**计算值**（`total_tokens`、`cost_usd`、`retry_chain`、`tokens_per_sec`）。

**性能约定（重要）**：
- `other` 每行只 `Unmarshal` 一次，结果放 `rowCtx`，所有依赖列共享；
- 若勾选的列**没有任何 `NeedOther=true`**，则 SQL 不 SELECT `other` 列，且完全跳过 JSON 解析——`other` 通常是行宽的大头，这一优化对 IO 和 CPU 都是数量级的；
- `Extract` 返回 `string`，内部一律用 `strconv.*` 而非 `fmt.Sprintf`（后者反射 + 分配，在千万行级别是可观的 CPU）；
- 行切片 `[]string` 在批内复用，不每行新建。

### 4.2 列清单

**basic**：`created_at`、`id`、`type`、`username`、`user_id`、`token_name`、`token_id`、`group`、`model_name`、`upstream_model_name`、`is_model_mapped`、`ip`、`request_id`、`upstream_request_id`、`content`

**tokens**：`prompt_tokens`、`completion_tokens`、`total_tokens`(计算)、`cache_tokens`、`cache_creation_tokens`、`cache_creation_tokens_5m`、`cache_creation_tokens_1h`、`text_input`、`text_output`、`audio_input`、`audio_output`、`image_output`、`web_search_call_count`、`file_search_call_count`

**billing**：`quota`、`cost_usd`(计算)、`billing_source`（订阅扣费标记）、`billing_mode`、`matched_tier`、`model_ratio`、`completion_ratio`、`group_ratio`、`user_group_ratio`、`cache_ratio`、`cache_creation_ratio`、`cache_creation_ratio_5m`、`cache_creation_ratio_1h`、`audio_ratio`、`audio_completion_ratio`、`image_ratio`、`model_price`、`web_search_price`、`file_search_price`

**performance**：`use_time`、`frt`（首字延迟 ms）、`tokens_per_sec`(计算)、`is_stream`、`stream_status`、`reasoning_effort`

**admin**（`AdminOnly=true`）：`channel_id`、`channel_name`（查询时解析，见 §6.3）、`retry_chain`（`use_channel` join `->`）、`is_multi_key`、`multi_key_index`、`usage_billing_path`、`local_count_tokens`、`quota_saturation`、`other_raw`（原始 JSON 全文）

**audit**（`AdminOnly=true`，对 type=1/3/7 有值）：`admin_username`、`admin_id`、`admin_role`、`auth_method`、`payment_method`、`callback_payment_method`、`caller_ip`、`server_ip`、`node_name`、`version`、`audit_method`、`audit_route`、`audit_path`、`audit_status`、`audit_success`
（`login_method`、`user_agent`、`request_path` 属登录日志，日志属主本人可见，标 `AdminOnly=false`）

### 4.3 校验规则

- `columns[]` 逐项按注册表校验，**未知 key 直接报错**（防止模板写坏后静默少列）。
- 新链路是管理员专属，不做权限裁剪；`AdminOnly` 标记仅供旧的自助同步导出路径复用（保持现有「self 导出隐藏渠道/重试列」的安全不变量）。
- `columns[]` 为空 → 用默认模板 `builtin:as_displayed`。
- 列顺序 = 提交顺序（模板即有序列表）。
- 单次导出列数上限 100（`LogExportMaxColumns`）。上限必须容得下 `builtin:full` 的全部 81 列，由 `TestLogExportColumns_BuiltinTemplatesResolve` 兜底。

---

## 5. 导出模板

### 5.1 内置预设

| ID | 名称 | 列 |
|---|---|---|
| **`builtin:as_displayed`** | **页面所见（默认）** | 见 §5.2 |
| `builtin:legacy` | 兼容旧导出 | 时间/渠道/用户/令牌/分组/类型/模型/用时·首字/输入/输出/花费/IP/重试/详情（= 现有 14 列，保证老用户拿到的文件结构不变） |
| `builtin:billing` | 计费明细 | 时间/用户/令牌/分组/模型/全部 tokens/quota/cost_usd/全部倍率/计费模式/命中档位 |
| `builtin:performance` | 性能诊断 | 时间/模型/渠道/重试链/是否流式/use_time/frt/tokens_per_sec/上游请求 ID |
| `builtin:audit` | 审计 | 时间/类型/用户/操作者/auth_method/audit_*/IP/user_agent/详情 |
| `builtin:full` | 全量 | 注册表全部列 |

### 5.2 默认模板：`builtin:as_displayed`（页面所见）

对齐 [common-logs-columns.tsx](../../web/default/src/features/usage-logs/components/columns/common-logs-columns.tsx) 中管理员视角的表格列。页面上一个单元格常常承载多个数据点（例如「渠道」格里同时有渠道 ID、名称、多 key 序号和重试链），导出时**展开成独立列**，因为表格能靠悬浮和徽章表达的信息，表格文件必须落到列上：

| 页面列 | 展开后的导出列 |
|---|---|
| 时间（含类型徽章） | `created_at`、`type` |
| 渠道（ID + 名称 + key 序号 + 重试链弹层） | `channel_id`、`channel_name`、`multi_key_index`、`retry_chain` |
| 用户 | `username` |
| 令牌 | `token_name` |
| 模型（含重定向提示） | `model_name`、`upstream_model_name` |
| 流式（含流状态、tokens/s） | `is_stream`、`stream_status`、`tokens_per_sec` |
| Tokens（`输入 / 输出` + 缓存读写） | `prompt_tokens`、`completion_tokens`、`cache_tokens`、`cache_creation_tokens` |
| 花费（含订阅扣费标记） | `quota`、`cost_usd`、`billing_source` |
| 用时（含首字延迟） | `use_time`、`frt` |
| 详情 | `content` |

共 22 列。其中 `channel_name` 需要查询时解析、`tokens_per_sec` 为计算列，其余为直接列或单层 `other` 取值。

> 与页面的一处有意差异：页面对「敏感信息隐藏」开关会把用户名/渠道名打码成 `••••`，导出**不打码**——导出本身就是管理员刻意取数的动作，打码会让文件无用。该行为在新建抽屉里以一行说明文字提示。

### 5.3 自定义模板

新表 `log_export_templates`（主 DB，非 LOG_DB）：

```go
type LogExportTemplate struct {
    Id          int    `gorm:"primaryKey"`
    UserId      int    `gorm:"index:idx_let_user_name,priority:1"`
    Name        string `gorm:"type:varchar(64);index:idx_let_user_name,priority:2"`
    LogCategory string `gorm:"type:varchar(16);default:'common'"` // 预留
    Columns     string `gorm:"type:text"`   // JSON 有序数组
    Format      string `gorm:"type:varchar(16);default:'csv_gz'"`
    Options     string `gorm:"type:text"`   // JSON：BOM/时区/表头
    IsShared    bool   `gorm:"default:false"`
    CreatedAt   int64
    UpdatedAt   int64
}
```

**索引**：`uniqueIndex(user_id, name)` 防重名（同时覆盖按 user 列表查询）；`index(is_shared)` 供共享模板列表。表规模最多几千行。

**跨 DB 兼容**（Rule 2）：`Columns`/`Options` 用 `TEXT` 存 JSON 字符串（不用 JSONB）；布尔走 GORM 抽象不写裸 SQL；`AutoMigrate` 建表，三库通用。

**可见性**：`user_id = 自己` ∪ `is_shared = true`。共享模板对非属主只读。单用户模板上限 50。

---

## 6. 后端实现

### 6.1 文件划分

```
model/log_export_columns.go     — 列注册表 + Extract（新）
model/log_export_scan.go        — keyset 时间窗口扫描（新）
model/log_export_writer.go      — 分片写入器 csv.gz / xlsx（新）
model/log_export_job.go         — Redis 任务状态机、并发槽、令牌、清理（新）
model/log_export_throttle.go    — 资源闸门：CPU 水位、令牌桶、自适应 sleep（新）
model/log_export_template.go    — 模板 CRUD（新）
controller/log_export.go        — 任务/下载/模板/列目录 handler（新）
setting/operation_setting/log_export_setting.go — 配置（新）
model/log.go                    — 保留 ExportLogs 供同步降级路径（不动）
controller/log.go               — exportLogsExcel 改为复用列注册表（小改）
```

### 6.2 任务状态机（Redis）

复用台账导出的成熟模式（[model/consumption_cost_ledger_export.go](../../model/consumption_cost_ledger_export.go)），**键名空间独立**：

```
logexport:job:<job_id>          任务 JSON，TTL 24h
logexport:user:jobs:<user_id>   用户任务 ZSET（score=创建时间），TTL 24h
logexport:jobs                  全局任务 ZSET，供 Root ?all=true 与启动恢复枚举
logexport:slots                 并发槽 ZSET（member=job_id, score=过期时间戳）
logexport:cooldown:<user_id>    创建冷却 SETNX
logexport:cancel:<job_id>       取消标志
logexport:dl-token:<token>      下载令牌，Lua 原子消费并绑定续传窗口
```

并发槽用 ZSET 而非计数器：acquire 的 Lua 脚本先 `ZREMRANGEBYSCORE` 清掉过期成员再判容量，
进程崩溃遗留的槽位随过期时间自动回收，不需要人工干预（`TestLogExportSlot_ExpiredSlotsSelfHeal`）。

状态：`pending → running → ready | failed | canceled`

```go
type LogExportJob struct {
    JobID      string          `json:"job_id"`
    UserID     int             `json:"user_id"`
    Username   string          `json:"username"`     // 便于管理员视角列表展示
    Status     string          `json:"status"`
    Progress   int             `json:"progress"`     // 0-100，按时间轴推进
    RowCount   int64           `json:"row_count"`
    BytesOut   int64           `json:"bytes_out"`
    Format     string          `json:"format"`
    Columns    []string        `json:"columns"`
    Filters    LogExportFilter `json:"filters"`
    Parts      []LogExportPart `json:"parts"`
    Throttled  int64           `json:"throttled_ms"` // 因资源闸门累计让出的毫秒数，运维可见
    Error      string          `json:"error,omitempty"`
    CreatedAt  int64           `json:"created_at"`
    UpdatedAt  int64           `json:"updated_at"`
    FinishedAt int64           `json:"finished_at,omitempty"`
}

type LogExportPart struct {
    Index     int    `json:"index"`          // 从 1 开始
    Path      string `json:"path,omitempty"` // 服务端绝对路径
    Rows      int64  `json:"rows"`
    Bytes     int64  `json:"bytes"`
    StartTime int64  `json:"start_time"`     // 该片覆盖的时间范围
    EndTime   int64  `json:"end_time"`
}
```

**`Path` 必须参与序列化**：任务状态存在 Redis，重新载入后还要靠它定位文件去下载和清理。
不能为了不泄露服务端路径就标成 `json:"-"`——那样重载后的任务既下载不了也删不掉文件。
正确做法是照常持久化，下发前端时用 `PublicView()` 抹掉路径。
回归用例：`TestLogExportJob_PartPathSurvivesRedisRoundTrip` + `TestLogExportJob_PublicViewStripsPaths`。

**与台账导出的差异**：
- 台账用全局互斥锁；这里用**计数信号量**，但默认值设为 **1**（见 §10.2），配置可调。
- 下载侧不加全局锁——会与 Range 续传（多次独立请求）冲突；改为按用户并发数限制。
- 下载成功**不删文件**——分片下载天然多次请求，删了没法下第二片；生命周期统一由 TTL + 孤儿清理管理。

### 6.3 扫描策略

```go
windowEnd := filter.EndTime
for windowEnd > filter.StartTime {
    windowStart := max(windowEnd-WindowSec, filter.StartTime)
    var cursor *logCursor // (created_at, id)
    for {
        gate.Wait(ctx)                       // ← 资源闸门，见 §10.3
        rows, cost := scanBatch(ctx, windowStart, windowEnd, cursor, BatchSize)
        gate.Observe(cost)                   // ← 用实测查询耗时反馈调节
        fillChannelNames(rows, channelNameCache)
        writeRows(rows)
        if len(rows) < BatchSize { break }
        cursor = lastOf(rows)
        updateProgress(windowStart)
        checkCancelAndTimeout()
    }
    windowEnd = windowStart
}
```

**keyset 条件**（跨 DB 安全写法，不用 row-value 比较）：

```sql
SELECT <仅所需列>
FROM logs
WHERE created_at >= ? AND created_at <= ?
  AND (created_at < ? OR (created_at = ? AND id < ?))   -- 游标
ORDER BY created_at DESC, id DESC
LIMIT ?
```

- 命中现有复合索引 `idx_created_at_id (created_at, id)`；按 `type` 过滤时命中 `idx_created_at_type`。**本期无需新增索引**。
- 时间窗口（默认 1h）把单次扫描的索引区间限死，避免跨月巨型 range scan 长期占用 buffer pool。
- **绝不 `COUNT(*)`**：进度按已推进的时间比例计算。
- **显式列投影**：只 SELECT 勾选列实际依赖的字段；不含 `other` 系列列时不取 `other`。
- 每批查询独立 `context.WithTimeout`（默认 10s），超时即任务失败退出，不让慢查询长期占住连接。
- 过滤条件复用列表接口的 `applyExplicitLogTextFilter`（`%` 通配 + 转义消毒），保证导出与页面所见一致。

**渠道名解析**：遵循既有约定（删除渠道不回写 `logs`，避免锁等待），导出时解析——优先 `CacheGetChannel`，未命中批量查 `channels`，结果（含负缓存）存进跨整次导出复用的 `map[int]string`，摊销后额外 DB 查询 ≈ 0。

### 6.4 分片写入

```go
type partWriter interface {
    WriteHeader([]string) error
    WriteRow([]string) error
    Close() (bytes int64, err error)
}
```

- `csvGzPartWriter`：`file ← bufio(4MB) ← gzip ← csv`。
  - **gzip 等级默认 `1`（BestSpeed）**，可配 1~9。日志 CSV 重复度极高，level 1 与 level 6 的体积差通常 < 10%，但 CPU 差 2~3 倍。这是本功能最大的单点 CPU 开销，必须默认取最省的一档。
  - 可选写 UTF-8 BOM（默认 **true**），否则 Excel 打开中文 CSV 乱码。
- `xlsxPartWriter`：`excelize.StreamWriter`，**仅在预估行数 ≤ `xlsx_max_rows`（默认 20 万）时可选**。写入过程中实际行数超阈值 → **自动降级为 csv.gz**，job 记 `format_downgraded=true`，前端提示。预估值取前端传来的列表 total（列表接口本来就返回 total），不额外跑探测查询。

分片切换：行数达 `rows_per_file`（默认 100 万）→ 关片开新片，并复检磁盘余量。分片数达 `max_parts`（默认 100，≈1 亿行）→ 任务失败并提示收窄范围，防止撑爆磁盘。

文件名：`log-export-<job_id>-part-%04d.csv.gz`，落 `os.TempDir()`。

### 6.5 下载

| 场景 | 行为 |
|---|---|
| 单片 | `http.ServeContent` 直出。**自动支持 Range/206/If-Range**，断点续传由标准库处理 |
| 全部（`part=all`） | `archive/zip` 流式打包，不落盘。内部已是 `.gz`，用 `zip.Store` 不二次压缩（省 CPU） |

- 一次性 token（TTL 60s，Lua 原子取+删），URL `/dl/log-export/:token` 挂在 **gzip 中间件与鉴权组之外**——与台账下载同构：浏览器直接打开无需自定义头，且避免对已压缩内容二次 gzip（同样是省 CPU）。
- **Range 与一次性 token 的冲突**：续传会发多次请求，token 用完即失效会导致续传失败。解决：token 首次消费后**转为「已绑定」并续期到 `download_session_ttl_sec`（默认 30 分钟）**，期间可重复用于同一 part 的 Range 请求；过期后前端自动重新申请（用户无感）。
- 每用户并发下载 ≤ `max_concurrent_downloads_per_user`（默认 2）。

### 6.6 清理

- job TTL 24h；到期后 Redis key 消失，文件由孤儿清理回收。
- `CleanupOrphanLogExportFiles()`：glob `log-export-*`，反查 Redis job key 不存在则删除。触发时机：新建任务时异步触发（`go`，不阻塞请求）+ 进程启动时一次。
- 主动删除任务 → 删 Redis key + 删文件；运行中的任务被删 → 置取消标志，goroutine 下一批检测到后退出并清理半成品。

### 6.7 Redis 未启用时的降级

- 任务类接口返回 503 + i18n 文案「导出任务需要启用 Redis，请联系管理员或使用快速导出」。
- **保留旧的同步接口** `GET /api/log/export`（xlsx 流式），并升级为支持 `columns` 与模板（内置模板可用；自定义模板走主 DB 不依赖 Redis，也可用）。同步路径维持现有 1 个月跨度限制与 `LogExportRateLimit`。
- 前端导出中心检测到 503 → 界面切换为「快速导出」单按钮模式。

---

## 7. API 契约

遵循 Rule 9：业务错误 HTTP 200 + `{"success": false, "message": "..."}`，用 `common.ApiErrorI18n`；仅鉴权（401/403）与不可恢复错误（500）用非 200。
例外：`/dl/log-export/:token` 是文件下载端点，遵循 HTTP 语义（404/403/206/416），与台账下载一致。

### 7.1 列目录与模板

```
GET    /api/log/export/columns            AdminAuth
GET    /api/log/export/templates          AdminAuth   我的 + 共享 + 内置
POST   /api/log/export/templates          AdminAuth
PUT    /api/log/export/templates/:id      AdminAuth   属主或 Root
DELETE /api/log/export/templates/:id      AdminAuth   属主或 Root
```

### 7.2 任务

```
POST   /api/log/export/jobs               AdminAuth
  body: {
    start_timestamp, end_timestamp,        // 必填
    type, model_name, username, token_name, channel, group,   // 与列表接口同名同义
    columns: [...],                        // 空则用 builtin:as_displayed
    template_id: "builtin:billing" | "123",// 与 columns 二选一，columns 优先
    format: "csv_gz" | "xlsx",
    est_rows: 1234567,                     // 前端带上列表 total，用于 xlsx 可行性判断
    options: { csv_bom: true, timezone: "Asia/Shanghai", header: true }
  }
→ { success, data: { job_id } }

GET    /api/log/export/jobs               AdminAuth   我的任务（Root 可加 ?all=true 看全部）
GET    /api/log/export/jobs/:job_id       AdminAuth   状态/进度/分片清单
DELETE /api/log/export/jobs/:job_id       AdminAuth   取消或删除
GET    /api/log/export/jobs/:job_id/download-url?part=1|all   AdminAuth
→ { success, data: { url: "/dl/log-export/<token>", expires_in: 60 } }

GET    /dl/log-export/:token              无中间件（token 即凭证），支持 Range
```

**路由归属**：全部放进 `router/api-router.go` 中已有 `AdminAuth()` 的分组，不单独再挂中间件（Rule 11）。`?all=true` 在 handler 内二次校验 Root 角色。

---

## 8. 配置参数（Rule 12）

新建 `setting/operation_setting/log_export_setting.go`，注册键 `log_export_setting`：

**开关与配额**
| 字段 | 默认 | 说明 |
|---|---|---|
| `enabled` | `true` | **功能开关**，关闭后任务接口不可用（同步降级导出仍可用） |
| `user_cooldown_sec` | `300` | 单管理员两次创建任务的最短间隔 |
| `max_concurrent_jobs` | `1` | 全局同时运行的导出任务数 |
| `max_active_jobs_per_user` | `3` | 单用户未过期任务数上限 |
| `admin_max_range_sec` | `2678400`（31 天） | 最大导出时间跨度 |
| `timeout_sec` | `7200` | 单任务超时 |
| `job_ttl_hours` | `24` | 任务与文件保留时长 |
| `max_templates_per_user` | `50` | 模板数上限 |

**资源治理**（详见 §10）
| 字段 | 默认 | 说明 |
|---|---|---|
| `batch_size` | `3000` | 每批读取行数 |
| `batch_sleep_ms` | `100` | 批间基础休眠 |
| `batch_query_timeout_sec` | `10` | 单批查询超时 |
| `window_sec` | `3600` | 扫描时间窗口（下限 60 秒，防止误配置产生海量空查询） |
| `max_rows_per_sec` | `20000` | 令牌桶行速率上限 |
| `cpu_soft_limit` | `70` | CPU 使用率超过则线性加大休眠 |
| `cpu_hard_limit` | `85` | CPU 使用率超过则暂停导出 |
| `cpu_check_interval_ms` | `1000` | 水位检查间隔 |
| `gzip_level` | `1` | gzip 压缩等级（1=最省 CPU） |
| `rows_per_file` | `1000000` | 单分片行数 |
| `max_parts` | `100` | 分片数上限 |
| `xlsx_max_rows` | `200000` | 超过则降级 csv.gz |
| `min_free_disk_mb` | `2048` | 磁盘可用空间低于此值拒绝创建/中止任务 |
| `offpeak_only` | `false` | 仅在低峰时段运行 |
| `offpeak_window` | `"02:00-06:00"` | 低峰时段（服务器本地时区） |

**下载**
| 字段 | 默认 | 说明 |
|---|---|---|
| `download_token_ttl_sec` | `60` | 下载 URL 有效期 |
| `download_session_ttl_sec` | `1800` | token 首次使用后的续传窗口 |
| `max_concurrent_downloads_per_user` | `2` | 并发下载限制 |

全部走 `config.GlobalConfig.Register`，getter 带 `<=0 → 默认值` 兜底（对齐 `LedgerDetailSetting` 风格）。**不新增环境变量**——现有 `LOG_EXPORT_RATE_LIMIT_*` 保留给同步降级路径。

### 8.1 热更新语义（已确认需求）

所有参数在管理端设置页改完**即时生效、无需重启**。但「即时生效」对一个已经跑起来的导出任务意味着什么，必须逐项讲清楚，否则会出现预期外的行为：

**实现约定**：任何地方都**在使用点现调 getter**（`operation_setting.GetLogExportSetting().GetBatchSleepMs()`），绝不在任务启动时把配置快照进结构体、也不跨请求持有 setting 指针（Rule 12 的明确要求）。导出主循环每批都重新读一次，因此下表的「运行中任务」列才成立。

| 参数 | 对运行中的任务 | 说明 |
|---|---|---|
| `batch_size`、`batch_sleep_ms`、`batch_query_timeout_sec` | **下一批立即生效** | 主要的降压旋钮 |
| `max_rows_per_sec` | **下一批立即生效** | 令牌桶速率热调整 |
| `cpu_soft_limit`、`cpu_hard_limit`、`cpu_check_interval_ms` | **下一次闸门检查立即生效** | 可临时把硬限调到 0 让所有导出立刻暂停 |
| `offpeak_only`、`offpeak_window` | **下一批立即生效** | 打开后运行中的任务会在下一批进入等待，任务保持 `running` |
| `min_free_disk_mb` | 下一次分片切换时生效 | 每片写完复检 |
| `window_sec` | 下一个时间窗口生效 | 当前窗口跑完才换 |
| `rows_per_file`、`gzip_level` | **仅对之后新建的分片生效** | 当前分片的 writer 已构造，中途改压缩等级会让同一文件前后不一致，因此不改已开的片 |
| `max_parts` | 下一次分片切换时校验 | 调小后运行中任务可能因超限而失败——这是符合直觉的止损行为 |
| `timeout_sec` | **不影响运行中任务** | 任务启动时读一次并固化。要提前终止请用「取消」。注意超时按**实际工作时间**判定：闸门让出的时间（CPU 暂停、低峰等待、限速休眠）不计入 |
| `job_ttl_hours` | 下一次任务状态写入时生效 | 每批都会刷新 Redis TTL |
| `max_concurrent_jobs` | 不中断运行中任务 | 只影响新任务能否拿到槽位。调小到低于当前运行数时，运行中的任务照常跑完 |
| `enabled` | 不中断运行中任务 | 关闭后仅拒绝新任务；要停运行中的请用「取消」。**如需一键刹车，用 `cpu_hard_limit=0`** |
| `admin_max_range_sec`、`max_templates_per_user`、`user_cooldown_sec`、`max_active_jobs_per_user` | — | 创建/校验时读取，天然热生效 |
| `download_token_ttl_sec`、`download_session_ttl_sec` | 仅对**新签发**的令牌生效 | 已签发令牌的 TTL 写在 Redis key 上，不会被追溯修改 |
| `max_concurrent_downloads_per_user` | **下一次下载请求立即生效** | 已在传输中的下载不中断 |

界面上对「不影响运行中任务」的几项（`timeout_sec`、`enabled`、`max_concurrent_jobs`）加一行说明文字，避免管理员改完以为运行中的任务会立刻停。

---

## 9. 前端：导出中心页面（管理员）

### 9.1 位置与可见性

- 路由：`/usage-logs/export`，在 `section-registry.tsx` 增加第 4 个 section，复用现有 `$section.tsx` 动态路由，**无需新增路由文件**。
- **仅管理员可见**：tab 在非管理员账号下不渲染；直接访问该路径 → 跳 403 页面（与现有 `_authenticated` 下的管理页一致）。
- 使用日志页现有的导出按钮改为下拉：`快速导出`（沿用同步路径，带当前筛选，所有人可用）/ `高级导出…`（仅管理员，跳转导出中心并携带当前筛选）。
- `use-sidebar-config.ts` 增加 `/usage-logs/export → { section: 'console', module: 'log' }`，跟随「日志」模块显示开关。

> Classic UI 已在 commit `18c7b250f` 删除，仓库只剩 `web/default`，Rule 6 的双 UI 同步本期只涉及 default。

### 9.2 页面结构

```
┌─ 导出中心 ────────────────────────────────────────────────────┐
│ [新建导出]                          系统负载 CPU 34% ▁▂▃  [刷新] │
├───────────────────────────────────────────────────────────────┤
│ 创建时间 │ 范围 │ 筛选摘要 │ 模板 │ 格式 │ 行数 │ 进度 │ 操作    │
│ 10:23  │7/1-7/7 │消费·gpt-4o│页面所见│csv.gz│3,200,000│100% │[下载▾][删]│
│ 10:05  │7/1-7/31│全部      │计费明细│csv.gz│1,204,331│ 47% │[取消]     │
│ 09:40  │6/1-6/30│全部      │全量    │xlsx  │  —      │失败 │[详情][删] │
└───────────────────────────────────────────────────────────────┘
```

顶部显示当前系统 CPU 水位（复用已有的 `GetSystemStatus`），让管理员在高负载时自觉推迟导出；任务因 CPU 闸门降速时，进度行显示「已让出 N 秒以保障服务响应」而不是让人以为卡死。

**下载下拉**（ready 状态）：
```
下载 ▾
 ├ 全部（3 个分片，共 412 MB） → zip
 ├ 分片 1  1,000,000 行 · 7/5 12:00–7/7 23:59 · 156 MB
 ├ 分片 2  1,000,000 行 · 7/3 04:12–7/5 12:00 · 148 MB
 └ 分片 3  1,200,000 行 · 7/1 00:00–7/3 04:12 · 108 MB
```
每片标注行数、覆盖时间范围、体积，管理员可以只下需要的那段。

### 9.3 新建导出抽屉（Sheet）

1. **筛选条件** —— 复用 `common-logs-filter-bar` 的字段组件（时间范围、类型、模型、用户、令牌、渠道、分组）。从使用日志页跳转时自动带入当前筛选。跨度超限时在输入框下方直接提示可选范围，而不是提交后才报错。
2. **列选择** —— 顶部模板下拉（**默认选中「页面所见」**，另有内置 5 项 + 我的 + 共享）；下方双栏穿梭（可选列 / 已选列），右栏可拖拽排序，按 `group` 折叠，顶部搜索过滤；改动后出现「另存为模板」。
3. **格式与选项** —— csv.gz / xlsx 单选（预估行数超阈值时 xlsx 禁用并说明原因）、CSV 是否带 BOM、时区、是否含表头。

底部提示「预计 X 行、约 Y 个分片、预计耗时 Z 分钟」（用列表 total 与 `max_rows_per_sec` 估算）+ [开始导出]。

### 9.4 轮询与状态

- TanStack Query，`refetchInterval`：有 running 任务时 **3s**，全部终态时停止（`refetchInterval: false`），页面失焦时停止（`refetchIntervalInBackground: false`）——避免闲置页面持续打后端。
- 进度条 + 已导出行数实时更新；进度 >60s 无变化时提示「正在扫描无匹配数据的时间段」。
- 完成时 toast + 页面不在前台时闪烁标题。

### 9.5 交互与错误文案（Rule 6 用户视角）

| 场景 | 文案 key |
|---|---|
| 冷却中 | `You can start another export in {{n}} minutes.` |
| 并发满 | `Another export is running. Please wait for it to finish.` |
| 跨度超限 | `Please select a range within {{n}} days.` |
| CPU 高位暂停 | `Export paused because the server is busy. It will resume automatically.` |
| 低峰限制 | `Exports run between {{window}} to avoid impacting service. Your task is queued.` |
| 磁盘不足 | `Not enough server storage for this export. Please free up space or narrow the range.` |
| 格式降级 | `Result exceeded {{n}} rows, so it was exported as CSV instead of Excel.` |
| 分片超限 | `Too much data for one export. Please narrow the time range and try again.` |
| Redis 不可用 | `Background export is unavailable. You can still use Quick Export for smaller ranges.` |
| 令牌过期 | `Download link expired. Click download again.`（前端自动重申，通常无感） |

不暴露 Go 错误字符串；失败任务「详情」弹窗做映射，未知错误统一为「导出失败，请重试或联系管理员」，原始错误只进 SysLog。

### 9.6 i18n

- 前端：`web/default/src/i18n/locales/{en,zh,fr,ru,ja,vi}.json`，key = 英文原文，完成后跑 `bun run i18n:sync`。
- 后端：CSV 表头 + 任务错误文案进 `i18n/locales/{en,zh}.json`（Rule 13），控制器统一 `ApiErrorI18n`。
- 所有含中文的文件用 Write/Edit 工具写入（UTF-8 无 BOM），不用 PowerShell 重定向（Rule 6 编码约定）。

---

## 10. 资源治理

这是本期的核心约束：导出是长时间后台作业，必须在 CPU、数据库、内存、磁盘、带宽五个维度都有硬上界与自适应退让。

### 10.1 CPU

导出的 CPU 开销按占比排序：**gzip 压缩 > CSV 序列化/字符串转换 > JSON 解析 `other` > GORM 扫描**。逐项处理：

| 措施 | 效果 |
|---|---|
| gzip 等级默认 **1（BestSpeed）**，可配 | 相比默认等级 6，CPU 降 2~3 倍，体积仅多 <10% |
| 无 `other` 依赖列时**完全跳过 JSON 解析**，且 SQL 不取该列 | 视勾选列不同，可省掉整体 CPU 的 30%~50% |
| 有 `other` 依赖列时，每行只 `Unmarshal` **一次**，结果共享给所有列 | 避免每列重复解析 |
| `strconv.*` 替代 `fmt.Sprintf`，行切片批内复用 | 减少反射与堆分配，降低 GC 压力（GC 是会影响 relay 延迟的全局开销） |
| xlsx 仅限 20 万行以内 | 避免大表走 zip+XML 这条最贵的路径 |
| zip 打包用 `zip.Store` 不二次压缩；下载端点绕过 gzip 中间件 | 下载阶段几乎零 CPU |
| **全局同时仅 1 个任务**（默认） | 导出的 CPU 占用被限制在约 1 个核 |
| **CPU 水位闸门**（见 §10.3） | 超过软限自动降速，超过硬限暂停 |

**量化**：单任务串行、gzip level 1，实测量级约为**单核 60%~80% 占用**。在常见的 4 核及以上部署上，导出最多吃掉约 1/4 的 CPU 预算，且 CPU 闸门会在整机水位上去时把它压下来。

### 10.2 数据库

| 措施 | 说明 |
|---|---|
| keyset + 时间窗口，命中 `idx_created_at_id` | 顺序索引扫描，不做全表扫，不排序临时表 |
| 只读，无事务、无锁、无 `COUNT(*)` | 与 relay 的 INSERT 不产生锁竞争 |
| 显式列投影 | 不取 `other` 时行宽大幅下降，IO 与网络传输同步下降 |
| `batch_sleep_ms=100` 基础节流 + 令牌桶 `max_rows_per_sec=20000` | 给 DB 留出充足的空闲周期 |
| 每批独立 `ctx` 超时 10s | 慢查询不会长期占住连接池 |
| 全局并发 1 | 最多 1 条连接用于导出 |
| 导出的是**历史时间段** | B+ 树扫描区间与 INSERT 集中的右端页几乎不重叠，缓冲池竞争极低 |
| 部署建议：`LOG_SQL_DSN` 分离日志库 | 项目已支持（[model/main.go:227-233](../../model/main.go#L227-L233)），分离后导出与主库彻底隔离 |

**量化**：`3000 行 / (查询 ~30ms + 休眠 100ms)` ≈ **23k 行/s**，再被令牌桶压到 20k 行/s。1000 万行的导出约需 **8~9 分钟**。相比之下，无节流的满速扫描能到 20 万行/s，但那正是会拖慢正常服务的做法——**这里是有意用时间换服务质量**。

### 10.3 自适应闸门（`log_export_throttle.go`）

每批查询前经过三道闸：

```go
func (g *gate) Wait(ctx context.Context) {
    // ① 低峰时段限制（可选）
    if cfg.OffpeakOnly && !inOffpeakWindow(time.Now()) {
        waitUntilOffpeak(ctx)   // 任务保持 running，进度停在原处并标注原因
    }

    // ② CPU 水位（复用 common.GetSystemStatus()，5s 采样，见 common/system_monitor.go）
    for {
        cpu := common.GetSystemStatus().CPUUsage
        if cpu >= cfg.CPUHardLimit {          // 默认 85%
            sleep(cfg.CPUCheckIntervalMs)      // 暂停，累计 throttled_ms
            continue
        }
        if cpu >= cfg.CPUSoftLimit {           // 默认 70%
            extraSleep = baseSleep * (cpu - soft) / (hard - soft) * 4  // 线性加压，最多 4 倍
        }
        break
    }

    // ③ 令牌桶：行速率不超过 max_rows_per_sec
    g.bucket.WaitN(ctx, batchSize)

    sleep(baseSleep + extraSleep)
}

// 用实测查询耗时反馈：查询本身变慢说明 DB 有压力，同比例加大休眠
func (g *gate) Observe(queryCost time.Duration) {
    if queryCost > g.baselineCost*2 { g.penalty = min(g.penalty*2, maxPenalty) }
    else if queryCost < g.baselineCost*1.2 { g.penalty = max(g.penalty/2, 0) }
}
```

**关于 CPU 采样的前提**：`common.GetSystemStatus()` 只在性能监控开启时（`GetPerformanceMonitorConfig().Enabled`）才更新，否则恒为零值。实现时必须判断：**监控未开启 → 视为「无 CPU 信息」，退化为固定节流（基础 sleep + 令牌桶），绝不把零值当成「CPU 空闲」而放开限速**。这是个容易写错并且后果严重的地方。

### 10.4 内存

单任务峰值内存构成：

| 项 | 大小 |
|---|---|
| 一批 3000 行 `*Log`（含 `other` 字符串，按均值 1KB/行估） | ~4 MB |
| 解析后的 `other` map（逐行解析后即弃） | < 1 MB |
| bufio 写缓冲 | 4 MB |
| gzip 窗口与内部缓冲 | < 1 MB |
| 渠道名缓存（几千渠道） | < 1 MB |
| **合计** | **< 12 MB / 任务**，并发 1 时即整体上界 |

无「一次性把结果读进内存」的路径；批切片按批复用，不随导出总行数增长。

### 10.5 磁盘

- **创建任务前预检**：`common.GetDiskSpaceInfo()`（已有）可用空间 < `min_free_disk_mb`（默认 2GB）→ 拒绝创建，文案提示管理员清理。
- **每个分片写完复检**：不足则任务失败并清理半成品，而不是把磁盘写满导致整个服务不可用（日志、DB 都会跟着挂）。
- 分片数上限 `max_parts=100`；csv.gz 压缩率通常 5~8 倍，100 万行分片约 100~200 MB，满配上限约 10~20 GB——这也是必须有磁盘预检的原因。
- 文件生命周期：job TTL 24h + 孤儿清理 + 启动清理，三重兜底。

### 10.6 网络与下载

- 下载内容已 gzip，绕过 gzip 中间件不二次压缩。
- 每用户并发下载 ≤ 2；分片设计本身鼓励串行逐片下载。
- Range 续传避免断线后重传整个文件，节省重复带宽。

---

## 11. Main Chain Impact（Rule 0）

### 11.1 同步 vs 异步

**relay goroutine 上同步执行的部分：无。** 本功能不在 `relay/` 调用链上，不新增任何 relay 中间件；实际工作在受信号量限制的独立 goroutine 中执行，与触发它的 HTTP 请求解耦，并有 `recover()` 兜底与 ctx 超时。

### 11.2 共享资源审计

| 资源 | 本功能的使用 | relay 是否触碰 | 冲突处理 |
|---|---|---|---|
| **`logs` 表（LOG_DB）** | 只读 keyset 范围扫描 | **是**——relay 结算路径经 `createLog` 逐条 INSERT | 只读无锁；时间窗口限区间；令牌桶 + 自适应休眠；并发 1；不 COUNT；显式列投影；建议 `LOG_SQL_DSN` 分库彻底隔离 |
| **`channels` 表** | 渠道名解析，缓存未命中时批量查 | 是（渠道选择读缓存） | 优先 `CacheGetChannel`；跨整次导出复用 map，摊销后 ≈ 0 次查询 |
| **`log_export_templates`（主 DB）** | CRUD，仅管理页面触发 | 否 | 新表，relay 不访问 |
| **Redis** | `logexport:*` 前缀 | 是（relay 用 token/user/channel 等键） | 前缀独立，与 relay 及台账导出的 `ledger:export:*` 均不冲突。写频率：每批一次 job 更新 ≈ 8 ops/s（含节流后），相对 relay 30k RPM 可忽略 |
| **DB 连接池** | 任务持有 1 条连接做批量查询，休眠期间归还 | 是 | 并发 1 → 最多占 1 条 |
| **CPU** | gzip + 序列化，约 1 核 | **是**（relay 是 CPU 敏感路径） | 见 §10.1、§10.3：gzip level 1、并发 1、软/硬水位闸门（70%/85%）、可选低峰时段 |
| **内存 / GC** | < 12 MB 峰值，低分配设计 | 是（GC 停顿影响 relay 延迟） | 批内复用切片、`strconv` 替代 `fmt`、不缓存整表 |
| **goroutine** | 每任务 1 个，信号量限制 | — | 非 per-request；`recover()` + ctx 超时 |
| **磁盘 / `os.TempDir()`** | 分片文件 | 否（但磁盘写满会拖垮整个进程） | 创建前预检 + 每片复检 + 上限 + 三重清理 |
| **出口带宽** | 下载分片 | 否（共用进程网络） | 每用户并发 ≤ 2；内容已压缩至原始的 15%~20% |

### 11.3 极端情况下的止损手段

按「立刻见效」程度排序（全部为运行时配置，无需改代码或重启，语义详见 §8.1）：

1. **`cpu_hard_limit=0` —— 一键刹车**。所有运行中的任务在下一次闸门检查（≤1s）时进入暂停，任务保持 `running` 不丢进度，压力恢复后把值调回即自动继续。这是最快的止血手段。
2. `batch_sleep_ms` 调大 / `max_rows_per_sec` 调小 —— 下一批生效，线性降压，适合「不想停但想让它慢下来」。
3. `offpeak_only=true` —— 运行中的任务在下一批进入等待，全部导出被赶到低峰时段。
4. `enabled=false` / `max_concurrent_jobs=0` —— 拒绝新任务，**不中断运行中任务**（要停运行中的请用第 1 项或直接取消）。
5. 管理员在页面上取消任务 —— 下一批检测到取消标志后退出并清理半成品。

---

## 12. 边界情况与已知限制

| 情况 | 处理 |
|---|---|
| 导出期间新日志写入 | keyset 按时间**倒序**扫描，新日志时间戳大于导出起点，不会被重复读入；结果是「发起时刻之前」的快照 |
| 时间范围内 0 行 | 正常 `ready`，`row_count=0`，产出仅含表头的分片，前端提示「未匹配到数据」 |
| 管理员在导出中被降权/删除 | 下载时校验属主 + 当前角色，降权后返回 403 |
| 进程重启 | Redis 中 `running` 任务成为孤儿。启动时扫描：`running` 且 `updated_at` 超时的置 `failed` 并清理文件。**不自动续跑**（半成品续写的正确性代价高于让管理员重试） |
| 磁盘写满 | 预检 + 每片复检；触发时任务 `failed` 并清理半成品，文案提示存储不足 |
| CPU 长时间处于硬限以上 | 任务保持 `running` 但进度停滞，页面显示「因服务器繁忙暂停中，已让出 N 秒」；超过 `timeout_sec` 则失败 |
| ClickHouse 作为 LOG_DB | keyset 语句兼容，但 CH 的 `id` 由应用侧生成（见 `assignDisplayLogIds`），排序语义与 MySQL 不同。**本期在 CH 下降级为 `created_at` 单键游标 + 去重窗口**，并在设置页标注「ClickHouse 下导出行序可能与列表不完全一致」 |
| 部署形态 | **已确认单实例部署**。分片文件落本实例 `os.TempDir()`，任务创建、执行、下载都在同一进程内，无跨实例路由问题。若将来横向扩容，需要改动的只有两处：① 分片文件改共享卷或对象存储；② `logexport:slot` 信号量已在 Redis 中，天然支持跨实例计数，无需改动。届时在本文档追加说明即可 |

---

## 13. 测试计划（Rule 15）

### 13.1 单元测试

`model/log_export_columns_test.go`
- 每列 `Extract`：正常值 / `other` 为空 / 非法 JSON / 字段缺失 / 类型不符（如 `frt` 是字符串）
- `builtin:as_displayed` 的列集合与顺序断言（**防止页面改列后模板悄悄漂移**）
- 未知 key → 报错；空 columns → 回落默认模板；列数超 60 → 报错
- `NeedOther` 推导正确：勾选纯直接列时 `needOther=false`
- 边界：`quota=0`、负 quota（退款）、tokens=0、int32 饱和标记

`model/log_export_scan_test.go`（真实 MySQL + PG，SQLite 兜底，Rule 15.5）
- keyset 翻页无重复无遗漏：构造 3000+ 行含**相同 `created_at`** 的数据，验证复合游标正确跨越同秒边界（最易写错处）
- 时间窗口切换点的连续性
- 各过滤组合（type / model 通配 / username 通配 / token / channel / group）
- 空结果、单行结果
- 列投影：不含 other 列时 SQL 不含 `other`（用 GORM DryRun 断言）

`model/log_export_throttle_test.go`（**本期新增的关键测试**）
- CPU < 软限 → 只休眠基础值
- 软限与硬限之间 → 休眠随 CPU 线性增长，且不超过 4 倍上限
- CPU ≥ 硬限 → 循环等待，`throttled_ms` 正确累加
- **性能监控未开启（`GetSystemStatus()` 返回零值）→ 退化为固定节流，绝不放开限速**（对应 §10.3 的陷阱）
- 令牌桶：速率不超过 `max_rows_per_sec`，误差 < 10%
- `Observe()` 反馈：查询耗时翻倍 → penalty 上升；恢复 → penalty 衰减
- 低峰窗口判定：窗口内/外、跨零点窗口（如 `22:00-04:00`）

`model/log_export_writer_test.go`
- csv.gz 可被 `gzip.Reader + csv.Reader` 正确回读，行数/列数/表头一致
- BOM 开关生效；gzip 等级参数生效（1 与 9 产出均可解压）
- 分片切换：`rows_per_file=10` 时 25 行 → 3 片，分布 10/10/5
- xlsx 超阈值自动降级 csv.gz
- 字段含逗号/引号/换行/中文的 CSV 转义正确
- 磁盘不足时的失败路径与半成品清理

`model/log_export_job_test.go`（miniredis）
- 状态机流转、并发信号量抢占与释放、冷却键、令牌原子消费与续期、取消标志、孤儿清理、重启后 `running` 超时任务被置 `failed`

`model/log_export_template_test.go`
- CRUD、重名唯一约束、可见性（自己/共享/他人）、越权修改被拒、数量上限、列校验

`setting/operation_setting/log_export_setting_test.go`
- 每个 getter 的 `<=0 → 默认值` 兜底（表驱动，对齐 `ledger_setting_test.go`）
- **热更新语义**（对应 §8.1）：改配置后再调 getter 立即返回新值；断言实现里没有「启动时快照配置」的写法——用一个跑批的 fake 循环，中途改 `batch_sleep_ms` / `max_rows_per_sec` / `cpu_hard_limit`，验证下一批就采用新值；`gzip_level`、`rows_per_file` 改动**不影响已打开的分片**；`cpu_hard_limit=0` 能让运行中的循环在一次检查周期内进入暂停且不丢进度

`controller/log_export_test.go`（`gin.CreateTestContext` + `httptest`）
- 参数校验：缺时间 / 倒序时间 / 超跨度 / 非法格式 / 非法列 / 非法 template_id
- 鉴权：普通用户访问任务接口 → 403；非属主查/下载 → 403；`?all=true` 非 Root → 403
- Redis 关闭 → 503 且文案正确
- 磁盘不足 → 拒绝创建且文案正确
- Range：`Range: bytes=100-` → 206 + 正确 `Content-Range`；非法 Range → 416
- 令牌过期 → 404；续传窗口内重复使用 → 200

### 13.2 回归测试

- 现有同步导出行为不变：表头、列顺序、**self 导出的渠道列与重试列为空**（当前代码的安全不变量，必须补断言）。
- 普通用户的 `/api/log/self/export` 路径不受本次改动影响。

### 13.3 完成标准

`go test ./...` 全绿；前端 `bun run typecheck` + `bun run lint` 通过。

---

## 14. 实施顺序

1. 配置项 + 列注册表（含 `builtin:as_displayed`）+ 单测
2. 资源闸门 `log_export_throttle.go` + 单测（**先于扫描实现，因为扫描要嵌进闸门**）
3. keyset 扫描 + 分片写入 + 单测（真实 DB）
4. Redis 任务状态机 + 下载令牌 + 单测（miniredis）
5. 控制器 + 路由 + 鉴权 + 单测
6. 模板表 + 迁移 + CRUD + 单测
7. 前端：API 层 → 列选择器 → 新建抽屉 → 任务列表页 → 入口改造
8. i18n 补全（前后端）+ `bun run i18n:sync`
9. 全量测试 + 回写本文档为实际实现

---

## 15. 待确认事项

**已确认**：
- ✅ 面向管理员的后台页面（§2.3）
- ✅ 默认模板 = 使用日志页面所见的列（§5.2）
- ✅ 资源占用需受控，不影响正常服务（§10）
- ✅ **单实例部署** —— 分片文件落本地临时目录的方案成立（§12）
- ✅ **配置支持热更新** —— 所有参数即时生效，逐项语义见 §8.1

**待确认**：

1. **§10 的资源默认值**是否符合预期——尤其 `max_concurrent_jobs=1`、`max_rows_per_sec=20000`（1000 万行约 8~9 分钟）、CPU 软/硬限 70%/85%。想让导出更快就调这三个，代价是对正常服务的挤占更多；反正都能热更新，也可以先按保守值上线再压。
2. **是否需要 `offpeak_only` 低峰模式**（§10.3）——设计里已含，默认关闭。不需要可以砍掉，省一块实现与测试。
3. 是否需要 **Root 查看/终止所有人导出任务**的能力——设计里以 `?all=true` 预留。
4. 文档其余部分（列清单 §4.2、内置模板 §5.1、API §7、页面 §9）是否还有要增删的。

---

## 16. 实现记录

实现与上文设计一致，以下为落地过程中确定或调整的细节。

### 16.1 文件清单

| 文件 | 内容 |
|---|---|
| [setting/operation_setting/log_export_setting.go](../../setting/operation_setting/log_export_setting.go) | 25 个配置项 + 兜底 getter + 低峰窗口解析 |
| [model/log_export_columns.go](../../model/log_export_columns.go) | 81 列注册表、6 个内置模板、列集合解析与投影 |
| [model/log_export_throttle.go](../../model/log_export_throttle.go) | 低峰/CPU 水位/令牌桶三道闸 + 查询耗时反馈 |
| [model/log_export_scan.go](../../model/log_export_scan.go) | keyset 扫描、显式列投影、渠道名解析 |
| [model/log_export_writer.go](../../model/log_export_writer.go) | csv.gz 与 xlsx 分片写入器 |
| [model/log_export_job.go](../../model/log_export_job.go) | Redis 状态机、并发槽、下载令牌、清理、导出主循环 |
| [model/log_export_template.go](../../model/log_export_template.go) | 模板表与 CRUD |
| [controller/log_export.go](../../controller/log_export.go) | 列目录/模板/任务/下载 handler |
| [router/api-router.go](../../router/api-router.go) | `/api/log/export/*`（AdminAuth 组）与 `/dl/log-export/:token` |
| [web/default/src/features/usage-logs/export/](../../web/default/src/features/usage-logs/export/) | 导出中心页面、新建抽屉、列选择器、API 层 |

### 16.2 与设计的差异

1. **列数上限 60 → 100**：`builtin:full` 有 81 列，60 会让「全量」模板自身校验失败。
2. **并发槽用 Redis ZSET**（§6.2）而不是简单计数器，以便过期自愈。
   任务索引 ZSET 的分值用**毫秒**（`CreatedAtMs`），否则同一秒创建的任务顺序随机。
3. **每用户并发下载限制放在进程内**（`sync.Mutex` + map），因为已确认单实例部署；
   Redis 往返对一个纯计数没有价值。横向扩容时这里需要换成 Redis 计数，代码注释已标注。
4. **xlsx 降级有两道**：创建任务时用前端传来的 `est_rows` 预判（避免白跑），
   运行中真实行数超限则返回 `errXlsxRowLimit`，由 `runLogExport` 改用 csv.gz 重跑一次。
5. **列选择器用上下移按钮而非拖拽**：拖拽需要引入新的 dnd 依赖，不值当。
6. **未运行 `bun run i18n:sync`**：该命令会顺带把 zh-TW 缺失的 700+ 条历史 key
   用英文回填，属于与本功能无关的改动。改为只把本功能的 key 直接写进 7 个 locale。

### 16.3 实现后自审发现并修掉的缺陷

按严重度排序，每条都补了会失败的回归用例：

| # | 缺陷 | 后果 |
|---|---|---|
| 1 | **时间窗口两端都是闭区间**，相邻窗口都包含边界那一秒；每个窗口的 keyset 游标独立重置，重复不会被自然过滤 | 落在窗口边界的日志**在导出文件里出现两次**。修法：窗口改为 `[windowEnd-W+1, windowEnd]`，下一个窗口从 `windowStart-1` 收尾（时间戳是整秒，减 1 既不重叠也不留缝）。用例 `TestWriteLogExport_NoDuplicatesAtWindowBoundary` 已验证「去掉修复即失败」 |
| 2 | **ClickHouse 下漏选 `request_id`**：CH 排序键是 `(created_at, request_id)`，而列投影只选用户勾的列 | 游标条件 `request_id < ''` 永不成立，**静默跳过该秒剩余的行**（丢数据）。修法：`ensureCursorFields` 在 CH 下无条件补上该字段 |
| 3 | **`LogExportPart.Path` 标成 `json:"-"`** | 任务状态存 Redis，重载后路径为空，文件既下载不了也删不掉（见 §6.2） |
| 4 | **闸门让出的时间计入任务超时** | 一旦开启低峰模式，白天创建的任务会先干等几小时再以超时失败；CPU 高位暂停同理。修法：改用可取消 ctx，超时按「总耗时 − 让出时间」判定 |
| 5 | **失败时未收尾的分片文件不会被清理** | 半成品一直占磁盘到任务过期（24h）。修法：`writeLogExport` 用命名返回值，在 defer 里按错误删除当前分片 |
| 6 | **`type` 列每行调一次 `i18n.Translate`** | 千万行导出 = 千万次本地化调用，与「CPU 要省」的目标背道而驰。修法：`rowCtx` 内缓存翻译结果（实际用到的 key 只有个位数） |
| 7 | **任务索引 ZSET 用秒作分值** | 同一秒创建的任务排序退化成按 UUID 字典序，列表顺序随机。修法：改用毫秒（`CreatedAtMs`） |
| 8 | **下载端点只校验角色不校验状态** | 被封禁的管理员在续传窗口内仍能取数。修法：`logExportDownloaderAllowed` 同时校验 `Status` |
| 9 | **`window_sec` 没有下限** | 误配成 1 秒会把一次跨月导出拆成上百万个几乎全空的查询，纯粹给 DB 添负担。修法：下限 60 秒 |
| 10 | 前端「高级导出」没有把当前筛选带过去；非管理员访问导出路径时标题显示成「任务日志」 | 承诺的预填没生效；标题与内容不符 |
| 11 | `est_rows` 一直是空的，xlsx 可行性提示形同虚设 | 改为用列表接口的 `total`（一次 `page_size=1` 查询）真实估算，并补上「预计 X 行 · Y 个分片」提示；阈值由后端 `/columns` 下发，前端不再硬编码 |

### 16.3.1 上线前评审补修的两处性能风险

| # | 缺陷 | 后果与修法 |
|---|---|---|
| 12 | **新建导出弹窗的行数估算复用了日志列表接口** | 列表接口在返回前会对命中集做完整 `COUNT`（[model/log.go](../../model/log.go) `GetAllLogs`），而估算的 queryKey 含全部筛选字段、输入框又是逐字 `onChange` —— 管理员敲一个模型名就可能对日志大表跑多次带过滤的 COUNT。且 `rangeTooLong` 只禁用了提交按钮，没拦住估算，超限范围（后端本就会拒绝导出）照样先扫一遍库。<br>修法三层：① 新增专用接口 `GET /api/log/export/estimate`，用 `SELECT count(*) FROM (SELECT 1 FROM logs WHERE ... LIMIT n) t` 做**有界计数**——代价被 `n` 钉死，与数据量无关，`n` 取 `xlsx_max_rows + 1`（判断「超没超 xlsx 上限」只需要这么多），并带查询超时；② 文本筛选 `useDebounce(500)`；③ `enabled` 加 `!rangeTooLong`，超限范围直接不查，接口侧也会拒绝。返回 `capped` 表示真实行数 ≥ rows，前端据此显示「超过 N 行」。 |
| 13 | **调优项只有下限没有上限** | 这些旋钮已暴露在设置页，把 `batch_size`、`max_rows_per_sec`、`rows_per_file`、`xlsx_max_rows` 填成极大值就能绕过所有保护阀（单批查询/内存/写入压力陡增），而 getter 只做 `<=0` 回退。<br>修法：getter 层统一 `clampInt(value, min, max)`，为 15 个数值项设硬上限（如 `batch_size ≤ 5 万`、`max_rows_per_sec ≤ 50 万`、`xlsx_max_rows ≤ 100 万` —— 后者必须低于 Excel 单表硬上限 1048576，否则写到一半必然失败）；前端表单的 zod `.max()` 与 `<input max>` 与后端保持一致，避免「填了却被静默改掉」。 |

### 16.3.2 大表实测（EXPLAIN ANALYZE，50 万行）

开发库的 `logs` 只有 838 行，PG 一律选 Seq Scan，计划没有参考价值。因此在会话级
临时表里造了 50 万行（结构与索引同 `logs`）实测，结论如下：

| 查询 | 计划 | 结论 |
|---|---|---|
| 导出扫描首页 / 带游标页 | `Index Scan Backward using (created_at, id)`，**无 Sort**，0.4~1.0 ms | keyset + 时间窗口按设计生效，索引直接提供顺序，LIMIT 提前收敛 |
| 深页（窗口内 20 万行，游标翻过 19 万） | PG 把 OR 游标重写成 `BitmapOr`，两个分支**都带 Index Cond**，只扫游标以下的 9999 行，3.1 ms / 163 buffers | **OR 形式不存在「每页重扫已返回行」的问题**，无需改成行值比较 |
| 行值游标（对照组） | `Index Scan Backward`，5.9 ms / 3743 buffers | 反而更贵。原设计选 OR 形式（跨库兼容）同时也是更快的那个 |
| 有界估算（30 天范围） | `Limit` 卡在 200001 行即止，46 ms | LIMIT 生效，代价与表大小无关 |
| **有界估算 + 无匹配过滤**（最坏） | `Rows Removed by Filter: 500000`，整段范围被扫穿，85 ms | **LIMIT 限住的是结果、不是工作量** |

最后一行是唯一的真问题：用户敲一个不存在的用户名（或用 `%xx%` 通配）时，数据库
必须扫完整段时间范围才能确定凑不满 limit。已加 **3 秒硬超时**（`logExportEstimateTimeout`，
刻意不做成配置项——它是交互提示的响应预算，不是运营旋钮），超时即放弃估算并返回
`available=false`，前端本来就对此优雅降级。

**下载不写审计**（见 §16.5）：审计日志本身就落在 `logs` 表，而下载是断点续传——同一分片
会发多次 Range 请求，逐次留痕等于给正在保护的大表持续插入写放大。谁在什么时间、按什么
条件导出了哪段数据，创建任务时已经记全，下载再记一遍换不来新信息。

### 16.4 顺带修掉的既有缺陷

**`i18n.Translate` 在 bundle 未初始化时 panic**（[i18n/i18n.go](../../i18n/i18n.go)）。
`main.go` 明确把 `i18n.Init()` 失败当作非致命（"i18n is not critical"）继续启动，
但此后任何 `Translate` 调用都会在 go-i18n 内部空指针 panic，与该函数自身
「翻译不到就返回 key」的兜底意图矛盾。已改为 bundle 为空时直接返回 key，
回归用例 `TestTranslate_NilBundleFallsBackToKey`。

### 16.5 鉴权与审计留痕

**鉴权矩阵**（实际实现）：

| 端点 | 鉴权 | 额外校验 |
|---|---|---|
| `GET /api/log/export/columns`、`.../templates`、`.../jobs*` | `AdminAuth()` 分组 | — |
| `GET /api/log/export/jobs?all=true` | `AdminAuth()` | handler 内二次校验 Root |
| `GET/DELETE /api/log/export/jobs/:job_id` | `AdminAuth()` | 属主或 Root，否则 403 |
| `PUT/DELETE /api/log/export/templates/:id` | `AdminAuth()` | 属主或 Root；`is_shared` 仅 Root 可置 |
| `GET /dl/log-export/:token` | **无中间件**，凭证是 URL 里的一次性令牌 | 令牌 → 任务归属校验 + 复核下载者「当前仍是启用状态的管理员」（角色**和**封禁状态都查，见 §16.3 #8） |

**留痕**：管理员写操作由 [middleware/audit.go](../../middleware/audit.go) 的 `finishAdminAudit`
在鉴权链路内兜底记录，新增接口自动覆盖。本功能在此基础上做了一处加强：

**创建任务手动埋点**（`log_export.job_create`）：兜底只记得下「POST 了哪个路由」，
而事后追查真正需要的是**谁、取了哪段时间、什么筛选条件、多少列、什么格式**。
handler 内记完后置 `ContextKeyAuditLogged`，避免与兜底重复。

**下载不额外留痕**：`/dl/log-export/:token` 不写审计日志。审计日志落在 `logs` 表，
而下载是断点续传，同一分片会发多次 Range 请求，逐次写入就是给正在保护的大表加写放大；
导出行为的关键信息（谁、什么时间、什么条件、哪段数据）在建任务时已经记全。

其余写操作（删除任务、模板 CRUD）在 `auditRouteActions` 里登记了语义化 action，
前端 `AUDIT_TEMPLATES` 有对应的本地化模板，不再显示成裸的 `POST /api/...`。

### 16.6 测试

新增 7 个测试文件，覆盖：列提取的每个分支与畸形 `other`、权限裁剪、闸门的每档水位
（含「CPU 读数不可用 → 保持固定节流」这个最危险的分支）、令牌桶、低峰窗口（含跨零点）、
csv.gz/xlsx 往返与转义、分片轮转、keyset 跨同秒边界不重不漏、窗口切分与一次性扫描等价、
模板 CRUD 与可见性、任务状态机与下载令牌续传、控制器鉴权与 Range/416/404、
以及端到端导出（多窗口、空结果、取消、降级、超分片、时区、表头语言）。

`go test ./...` 中与本功能相关的包全绿。仓库存在两个先于本次改动的失败用例
（`TestReevaluateTierByPeriodProfit_OrphanAndUnbound`，以及并发跑时
`TestGetAllFlowQuotaDates_AdminScopedWithChannelName` 的测试数据 id 撞车），
已确认 stash 掉本次改动后同样复现，与本功能无关。

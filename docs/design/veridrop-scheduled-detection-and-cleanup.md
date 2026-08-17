# 真伪检测定时执行与记录清理设计

## Implementation note: explicit boolean overrides

The detection task payload represents `include_long_context` and
`include_long_context_extreme` as optional booleans. An omitted field inherits
the configured default; an explicitly supplied `false` remains false for that
run. This prevents a global default from overriding an administrator's
per-run choice.

## 状态

已实现并部署到香港测试服务器。

## 目标与范围

在不修改上游 `veridrop-monitor`、不改变检测协议和评分规则的前提下，为当前 NEXAXIS 管理端增加两项能力：

1. 管理员可开启定时批量检测，并配置相邻两次检测的间隔。
2. 管理员可手动清理本系统保存的历史检测记录。

本次仅修改当前项目的调度、结果存储、管理 API 和管理界面。现有 `poll_interval_seconds` 仍只表示单次检测提交后查询上游任务状态的轮询间隔；新增的检测间隔使用独立配置和明确文案，避免两者混淆。

不在本次范围内：

- 修改或部署 `veridrop-monitor`。
- 修改检测项目、评分计算或通过阈值。
- 自动清理记录；本次只提供管理员主动触发的清理。
- 清理仍在等待或运行中的检测记录。

## 用户交互

### 定时检测

“检测设置”增加：

- “定时检测”开关，默认关闭。
- “检测间隔”，单位为分钟，默认 1440 分钟（24 小时），允许 15～43200 分钟。

设置页同时保留“任务状态轮询间隔”，并补充说明它只控制单次检测进度查询，不控制定时检测频率。

开启定时检测需要同时满足真伪检测总开关已开启且上游地址有效。关闭总开关时不会创建新的定时任务。手动批量检测与定时检测复用同一任务类型；如果已有任务等待或运行，本轮定时触发不会重复创建任务。

### 手动清理

“检测记录”工具栏增加“清理记录”按钮。点击后弹出确认框，提供：

- 清理 7 天前记录。
- 清理 30 天前记录（默认选择）。
- 清理 90 天前记录。
- 清理全部历史记录。

确认框明确显示：只清理已经结束的记录，不影响等待中或检测中的记录；清理不可恢复。提交后异步执行，界面提示任务已开始。清理完成后刷新检测记录列表。

按钮沿用当前页面的次要操作样式，不增加新的卡片或嵌套容器。所有新增文案使用现有 `t(...)` 国际化机制，并覆盖 `en`、`zh`、`zh-TW`、`fr`、`ja`、`ru`、`vi`。

## 数据流

### 定时检测

1. Root/有设置权限的管理员通过现有设置 API 保存 `veridrop_monitor_setting.auto_detection_enabled` 和 `veridrop_monitor_setting.detection_interval_minutes`。
2. 主节点现有 system task scheduler 每 15 秒检查已注册的定时任务处理器。
3. 真伪检测处理器在总开关和定时开关均开启时参与调度，并返回配置的检测间隔。
4. 到期且不存在同类型活动任务时，scheduler 创建 `veridrop_detection` 系统任务，payload 为 `{ "batch": true }`。
5. 现有 `RunVeridropDetectionTask` 读取启用渠道、展开模型并调用上游检测；结果继续写入 `channel_veridrop_detections`。
6. 手动批量检测也会更新同类型任务的最近执行时间，因此一次手动批量检测后不会立刻再补跑一次定时检测。

### 清理记录

1. 管理员在检测记录页选择保留范围并确认。
2. 前端调用清理 API，后端校验权限和 `retention_days`。
3. 后端创建独立的 `veridrop_detection_cleanup` 系统任务；如果已有同类型活动任务则返回该任务，不重复创建。
4. 后台处理器每批最多选择并删除 500 条符合条件的终态记录，持续更新系统任务进度。
5. 任务完成后保存 `deleted_count`；失败时记录内部错误，接口和界面只展示可操作的通用错误提示。
6. 页面刷新列表，等待中和运行中的记录始终保留。

## API 契约

### 现有设置 API

不新增设置端点。通过现有设置读写机制增加两个键：

- `veridrop_monitor_setting.auto_detection_enabled`: boolean
- `veridrop_monitor_setting.detection_interval_minutes`: integer，15～43200

`poll_interval_seconds` 的语义和字段名不变。

### 启动检测记录清理

`POST /api/channel/veridrop/results/cleanup`

鉴权：

- `middleware.AdminAuth()`
- 独立真伪检测菜单权限 `admin_menu.veridrop_detection`
- `authz.ChannelSensitiveWrite`

请求：

```json
{
  "retention_days": 30
}
```

规则：

- `retention_days = 0`：删除全部终态记录。
- `retention_days = 1..3650`：删除 `updated_at` 早于对应天数的终态记录。
- 其它值返回参数错误。

成功响应沿用现有 API 包装格式，data 返回系统任务；若任务已存在，同时返回 `created: false`，前端提示“清理任务已在运行”。

不会新增同步批量删除端点，也不会允许按任意 SQL 条件删除。

## 数据模型与索引

不新增业务表，不修改检测记录字段。

新增系统任务类型：

- `veridrop_detection_cleanup`

为 `channel_veridrop_detections` 增加复合索引：

- `(status, updated_at, id)`：支持按终态、截止时间和主键批量选取待删除记录。

删除采用跨数据库一致的 GORM 两步操作：先按索引选择一批 ID，再使用 `WHERE id IN ?` 删除。不得使用数据库专属的 `DELETE ... LIMIT`，确保 SQLite、MySQL 5.7.8+ 和 PostgreSQL 9.6+ 一致可用。

系统任务表沿用现有活动任务唯一键、租约和进度字段，不新增列。

## 配置参数

在 `VeridropMonitorSetting` 增加：

```go
AutoDetectionEnabled     bool `json:"auto_detection_enabled"`
DetectionIntervalMinutes int  `json:"detection_interval_minutes"`
```

默认值：

- `AutoDetectionEnabled: false`
- `DetectionIntervalMinutes: 1440`

Getter 对间隔进行 15～43200 分钟约束。配置继续由 `setting/config` 注册、从 options 表加载并热更新；不新增环境变量或全局变量。

## 关键业务逻辑与边界

- 定时检测只在主节点运行，使用现有数据库租约防止多实例重复执行。
- 定时检测总开关关闭、真伪检测总开关关闭或 Base URL 为空时均不创建新任务。
- 间隔从最近一次同类型任务的 `updated_at` 计算；手动和定时批量检测共享冷却时间。
- 配置在运行中修改时，下一个 scheduler pass 使用新值；不会中断当前任务。
- 清理仅处理 `done`、`error`、`timeout`、`cancelled`、`skipped`，永不删除 `queued`、`running`。
- 清理截止时间在任务创建时固化到 payload，避免长任务执行过程中范围漂移。
- 清理“全部”也只表示全部终态记录。
- 批量删除过程中取消上下文或丢失任务租约时立即停止，剩余记录可由管理员再次清理。
- 定时检测与手动检测复用现有活动任务去重，不并行启动第二轮全量检测。

## 错误处理

- Controller 使用 `common.ApiErrorI18n`/现有成功响应辅助方法，不向前端返回数据库错误。
- 参数非法时返回可本地化的“请选择有效的清理范围”。
- 创建系统任务失败时记录带 request context 的底层错误，前端提示“清理任务启动失败，请稍后重试”。
- 后台查询或删除失败时将清理系统任务标记为失败并使用系统日志记录原因；已删除的批次不回滚，后续可安全重试。
- 定时调度读取配置异常时不影响现有任务和 relay，由下一次 scheduler pass 重试。

## 与现有子系统的交互

- 系统任务：复用 scheduler、活动任务唯一约束、主节点执行和租约心跳。
- 检测服务：定时执行复用现有批量检测 payload 与处理器，不复制上游调用逻辑。
- 权限：读取记录仍使用 `ChannelRead`；清理使用更高的 `ChannelSensitiveWrite`。
- 前端缓存：清理任务提交和完成后使检测结果查询失效并重新获取。
- 账单、配额、用户日志、渠道请求日志：均不访问或清理。
- 上游 `veridrop-monitor`：无代码、配置或数据结构改动。

## Main Chain Impact

本功能不在 AI relay 请求 goroutine、中间件或上游转发流程中执行任何代码。

- 同步 relay 成本：每请求新增 DB 调用 0、Redis 调用 0、锁 0、goroutine 0。
- 定时检测和清理都由主节点现有 system task runner 在后台执行。
- 检测任务继续使用已有 `MaxConcurrent` 限制；清理任务串行、小批次删除。
- 后台任务失败只更新自身系统任务状态并记录日志，不传播到 relay 响应。

## Shared Resource Audit

| 资源 | 本功能访问方式 | relay 是否访问 | 隔离与影响控制 |
| --- | --- | --- | --- |
| `channel_veridrop_detections` | 检测写入、记录清理 | 否 | 独立表；按复合索引和 500 条批次删除 |
| `system_tasks` | 调度、去重、租约、进度 | 否 | 复用现有任务类型隔离和唯一约束 |
| `channels` | 定时检测分批读取启用渠道 | 是 | 现有手动批量检测已使用同一路径；只读、按主键游标、每批最多配置的 `BatchSize` |
| `options`/设置内存 | 管理员保存后由配置系统读取 | relay 不读取新增键 | 不增加 relay 请求时查询 |
| DB 连接池 | 后台查询、写结果和批量删除 | 是 | 检测并发受 `MaxConcurrent` 限制；清理串行且每批 500 条，不持有跨批事务 |
| Redis | 不访问 | 可能访问其它命名空间 | 无新增键或命名空间 |
| goroutine pool | system task runner 分发处理器 | relay 使用其它执行路径 | 每种任务最多一个活动实例；不按记录创建无界 goroutine |

## 并发与大数据分析

以 relay 100,000 RPM 为基准，本功能在 relay 每请求上的成本仍为零。

定时检测每个调度周期：

- scheduler 每 15 秒按现有机制批量读取各类型最新系统任务，不增加独立 ticker。
- 到期时创建 1 条系统任务；存在活动任务时创建 0 条。
- 渠道读取使用 `id > last_id ORDER BY id LIMIT batch_size`，避免 OFFSET。
- 上游检测并发沿用 `MaxConcurrent`（默认 2，最大 20）。

清理任务：

- 同时最多 1 个 `veridrop_detection_cleanup` 活动任务。
- 每批最多读取 500 个 ID，随后执行 1 次按主键集合删除和 1 次进度更新。
- 不使用跨全表长事务，不持有应用锁，不启动每行 goroutine。
- `(status, updated_at, id)` 索引避免按终态和时间范围全表扫描。

## 测试计划

遵循测试先行：先添加以下测试，再实现代码。

### 后端

- 设置边界：间隔 0、14、15、43200、43201 分钟的归一化。
- 调度处理器：总开关关闭、定时开关关闭、Base URL 为空、正常开启；间隔和 batch payload 正确。
- 清理模型：7/30/90 天边界前后记录；`updated_at` 恰好等于截止时间；所有终态可删除；`queued`/`running` 永不删除；多批次删除；空结果幂等。
- 清理服务：`retention_days` 为 -1、0、1、3650、3651；重复提交返回现有活动任务。
- Controller：无权限、非法参数、任务新建、已有任务四条路径。
- 数据库测试使用项目真实数据库，无法使用时才回退 SQLite；测试记录使用独立 ID/时间范围并按行清理，不截断共享表。
- 运行相关包测试后执行 `go test ./...`。

### 前端

- `bun run typecheck`
- 对真伪检测模块运行 oxlint 和 Prettier 检查。
- 通过 i18n 脚本添加七种语言文案并执行 `bun run i18n:sync`。
- `bun run build`
- 浏览器验证：设置保存与重新加载、定时/轮询间隔文案不混淆、四种清理范围、二次确认、无权限时不显示按钮、清理后列表刷新、窄屏弹窗不溢出。

## 实施结果

- 配置字段、调度处理器、异步清理任务、批量删除模型方法和管理 API 已按本文设计实现。
- 前端已增加定时检测开关、检测间隔输入、清理范围确认及清理任务状态展示；新增文案覆盖七种语言。
- `channel_veridrop_detections` 已增加 `(status, updated_at, id)` 复合索引。
- 定向 Go 测试、前端类型检查、Veridrop 模块 lint、Prettier、前端生产构建和 `git diff --check` 均通过。
- 全量 Go 测试中现有 `TestListConsumptionCostLedgerWithInAppTagFilter_Direct` 在整包执行时受共享数据库状态影响失败，但该测试单独执行通过；本功能相关测试全部通过。
- 浏览器自动化运行时当前未提供可连接浏览器，因此本地视觉回归无法执行；部署后使用 API、服务日志和静态资源进行生产态冒烟验证。
- 部署版本：`v1.0.0-rc.23-199-ged2cdcb72-veridrop-schedule-cleanup`。服务器二进制 SHA-256 与本地构建产物一致。
- 部署后确认进程正常、`/api/status` 返回新版本、检测记录 API 正常、两个新增设置键分别显示默认值 `false` 和 `1440`，非法清理范围被业务校验拒绝且未删除数据。

## 增补设计：筛选最新一批

状态：已实现并部署验证。

### 目标与交互

在检测记录筛选栏增加“批次”筛选，提供“全部批次”和“最新一批”。“最新一批”指最近创建的一次批量检测系统任务，包括管理员手动启动的批量检测和定时批量检测；单渠道检测与临时上游手动检测不属于批次。

该筛选可与渠道、模型、结果、模式、更新时间和排序条件叠加。切换批次后立即重新请求服务端数据，并在无批次数据时展示明确空状态，不在前端对当前 100 条结果做伪筛选。

### 数据流与 API

批量任务执行时，处理器把当前 `system_tasks.task_id` 写入内部检测 payload。每条由该批量任务创建的正常、失败或跳过记录都保存同一个 `batch_task_id`。

现有列表端点增加可选参数：

```text
GET /api/channel/veridrop/results?batch=latest
```

服务端先从检测记录中按 `id DESC` 找到最近的非空 `batch_task_id`，再把 `batch_task_id = 最新值` 与其它筛选条件一起下推到数据库。响应增加 `latest_batch_id`，用于前端确认实际应用的批次。没有批次记录时返回空数组，不回退到不可靠的时间区间推断。

现有历史记录没有批次标识，因此不会被错误归入最新批次；功能部署后完成第一轮新批量检测即产生可精确筛选的批次数据。

### 数据模型与索引

`channel_veridrop_detections` 增加：

```go
BatchTaskID string `json:"batch_task_id" gorm:"type:varchar(64);index:idx_veridrop_batch_id,priority:1"`
```

并让现有主键 `id` 作为 `idx_veridrop_batch_id` 的第二列，形成 `(batch_task_id, id)`，支持最新批次定位和批次内倒序分页。迁移继续使用 GORM AutoMigrate，兼容 SQLite、MySQL 和 PostgreSQL。

### 边界与错误处理

- 只有 `payload.Batch == true` 的系统任务写入批次标识。
- 同一批任务内因配置不支持而生成的“已跳过”记录也必须带批次标识。
- 运行中的最新批次可以筛选，列表会随轮询逐步出现新记录。
- 非法 `batch` 参数按现有参数错误约定返回业务错误，不静默忽略。
- 查询失败记录底层日志，并向页面返回可操作的通用加载失败提示。

### Main Chain Impact 与共享资源

本增补仍不进入 AI relay 链路，每个 relay 请求新增 DB/Redis/锁/goroutine 成本均为 0。新增字段只写入独立的 `channel_veridrop_detections` 表；批次筛选只在管理员查询检测记录时执行。`system_tasks.task_id` 仅在后台任务开始时复制到内存 payload，不增加 relay 共享资源访问。

### 测试先行

- 模型测试：最新批次准确选择；批次条件与渠道/结果/排序叠加；没有批次时返回空；单次和手动检测不混入。
- 服务测试：批量任务正常记录和跳过记录均写入任务 ID；非批量任务保持空值。
- Controller 测试：`batch=latest`、未指定批次、非法批次三条路径。
- 前端验证：类型检查、Veridrop 模块 lint、Prettier、生产构建；确认筛选请求由服务端执行并可与现有筛选叠加。

## 增补设计：纳入全局菜单管理

状态：已实现并部署验证。

当前已有按管理员分配的 `admin_menu.veridrop_detection` 权限，能够按账号控制菜单、页面和 API；缺少的是“系统设置 → 侧边栏模块”里的全局开关。此次补齐第二层控制：

1. 在 `SidebarModulesAdmin.admin` 增加 `veridropDetection`，默认 `true`，避免升级后意外隐藏现有入口。
2. 菜单管理界面的“管理区域”增加“真伪检测”开关，说明为控制全站侧边栏中的真伪检测入口。
3. `/channels/detection` 映射到 `admin.veridropDetection`，且必须排在通用 `/channels` 规则之前，避免错误继承“渠道”开关。
4. 最终可见性为“全局菜单开关开启”且“当前管理员拥有 `admin_menu.veridrop_detection:view` 权限”。全局开关关闭时所有账号隐藏侧边栏入口；账号权限仍负责页面和 API 的安全鉴权。
5. 超级管理员仍可从系统设置恢复该开关；全局侧边栏配置只控制展示，不替代现有后端权限校验，也不修改检测任务、结果或上游调用。

兼容现有 `SidebarModulesAdmin` JSON：解析时把缺失的 `veridropDetection` 合并为默认 `true`，保存后才显式写入该键，不需要数据库迁移。

测试与验证：覆盖空配置、旧配置缺键、全局关闭、账号无权限、两层均开启五种情况；执行 TypeScript 类型检查、相关模块 lint、Prettier、七语言同步和生产构建。

## 增补功能实施结果

- 批量检测任务创建的正常、失败和跳过记录均写入同一 `batch_task_id`；单渠道检测与临时上游检测保持空值。
- 结果接口支持 `batch=latest`，在数据库中按最新非空批次标识精确筛选，并返回 `latest_batch_id`；该条件可与渠道、模型、结果、模式、时间和排序共同使用。
- 检测记录页增加“批次”筛选，提供“全部批次”和“最新一批”，选择最新一批时持续轮询，以便正在运行的批量任务逐步显示结果。
- `SidebarModulesAdmin.admin.veridropDetection` 默认开启；系统设置的侧边栏模块管理增加真伪检测开关，`/channels/detection` 使用独立映射，不受通用渠道开关误控制。
- 菜单管理中的管理区模块按实际侧边栏顺序稳定排列：渠道、真伪检测、模型、用户、兑换码、订阅、员工、客户、业务概览、系统设置；旧版已保存 JSON 在解析时也会按默认顺序归一化，自定义模块仍追加保留。
- 最终菜单显示同时要求全局开关开启和当前管理员拥有 `admin_menu.veridrop_detection:view`；页面与 API 的后端鉴权保持不变。
- 新增界面文案已覆盖 `en`、`zh`、`zh-TW`、`fr`、`ja`、`ru`、`vi` 并完成 i18n 同步。
- 聚焦 Go 测试、TypeScript 类型检查、相关文件 lint 和生产构建通过。全量 Go 测试仅有既有共享 MySQL 数据污染导致的 `TestListConsumptionCostLedgerWithInAppTagFilter_Direct` 数量断言失败，与本功能无关。
- 已部署版本 `v1.0.0-rc.23-201-veridrop-batch-menu` 到香港测试服务器；服务端二进制 SHA-256 为 `fb06220148c14e276bee904ec4d0582213ef6644e55745c013ff243efc76dd12`，与本地发布包一致。
- 部署后 `/api/status`、管理员登录、`batch=latest` 空批次响应和非法批次拒绝均完成冒烟验证；新字段迁移可被筛选查询正常访问。

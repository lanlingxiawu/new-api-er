# 审计日志主动清理：复用现有日志清理任务

日期：2026-11-21。用户补充要求：“不需要再加表，共用之前的清理能力”。本版按这一要求实施，替代此前独立任务/API/权限设计。

## Goals and Scope

系统设置 → 运维 → 日志维护提供按截止时间主动清理审计日志。复用已有 log_cleanup 任务、任务表、创建/查询接口、租约和进度，以及 CountOldAuditLog / DeleteOldAuditLogBatch。仅增加“审计日志”清理范围，不能顺带删除消费日志。

入口位于日志维护页。按 2026-11-22 用户的布局反馈，审计清理与历史日志清理、错误日志清理并列展示：标题、日期、快捷时间和清理按钮直接放在页面中，以现有分隔线分组。移除原工具栏遗留的紧凑弹窗，点击清理仅打开项目通用的删除确认框。后端清理行为保持不变。

不增加表、列、索引、迁移、任务类型、权限类型、独立任务框架或定时调度。安全告警及其证据、凭据、账号和计费记录保留。默认历史日志入口及旧 payload 的执行语义保持兼容。

## Existing Logic and Related-Path Findings

| 已核对代码 | 当前行为与本次衔接 |
| --- | --- |
| controller/system_task.go:CreateLogCleanupSystemTask；router/api-router.go | 现有 POST /api/system-task/log-cleanup 接收 target_timestamp，要求 AdminAuth + operations.logs 编辑权限；继续使用该接口，增加 scope 及审计专用时间参数，保留旧时间字段的拒绝保护。 |
| service/system_task.go:StartLogCleanupTask/runLogCleanupTask | 已有任务创建、互斥、进度、租约和终态。新增 scope=audit 分支，复用同一运行循环。 |
| service/system_task.go:runAuditLogCleanup | 历史日志清理完成后按保留期附带清理审计，最多 2000 批，审计失败只记录 warn。新手动范围直接把审计表作为主目标，错误必须反映到任务结果，不能走这个附带阶段。 |
| model/audit_log.go:CountOldAuditLog/DeleteOldAuditLogBatch | 关系库先取有限主键再删；ClickHouse 使用同步 mutation。为复用这些函数，仅显式传入数据库连接，不复制删除实现。 |
| model/system_task.go；service/data_cleanup.go | log_cleanup 的 active_key 已与财务、错误日志清理共用。复用原任务类型，天然加入现有互斥；还需防止 audit 请求被当作 usage 任务复用。 |
| model/audit_security.go:openAuditSecurityDB；model/data_cleanup.go:OpenDataCleanupDB | 已有带初始化超时的维护连接构造。新审计范围按任务复用独立连接构造，单连接、完成关闭，不调整共享 LOG_DB 池。 |
| web/src/features/system-settings/maintenance/history-log-cleanup.tsx | 历史与审计清理复用同一页面内表单：日期、确认、后台启动及进度；按 scope 区分标题、按钮文案和请求参数，移除已无调用方的紧凑弹窗模式。 |
| web/src/features/system-settings/maintenance/log-settings-section.tsx；components/settings-page.tsx | 日志维护页依次组合历史清理、审计清理、错误日志清理及服务器日志管理；审计清理复用历史清理的布局和设置页只读禁用控制。 |
| web/src/features/usage-logs/audit/components/audit-log-viewer.tsx；use-audit-page.ts | 全部/本人/令牌视图共用。列表工具栏移除清理入口；查询使用 gcTime=0，回到该页时重新加载并从第一页开始。 |
| model/audit_log.go:GetUserAccessTokenStatus；model/audit_activity.go | 原始审计删除后，账户活动和旧令牌最后使用信息可能相应减少；不会更改凭据。 |
| service/auditsecurity/worker.go；finding_snapshot.go；ip_risk.go:QueryRiskBatch | 原始日志是检测源，告警证据另存主库。已有风险分页会跳过不存在的原始事件并继续前进。 |

相关设计：audit-log-retention-and-role-projection.md、error-log-cleanup-existing-indexes.md。代码优先；后端实际翻译文件是 en.yaml、zh-CN.yaml、zh-TW.yaml。

## Data Flow and API Contracts

1. 在日志维护页的审计清理区选择截止时间，点击清理并在通用确认框中确认。
2. POST /api/system-task/log-cleanup?target_timestamp=0&scope=audit&audit_target_timestamp=<Unix秒>。审计模式使用独立参数；旧版本控制器看到 target_timestamp=0 会拒绝，不会误删消费日志。
3. 服务端验证日期及 scope；省略 scope 或 usage 表示旧历史日志范围，audit 表示仅原始审计表；其他值拒绝。
4. 在现有 system_tasks.payload 中增加可选 scope 和 audit_target_timestamp，不做 DDL。审计任务的旧 target_timestamp 固定为 0，使旧 runner 拒绝执行；旧 payload 无 scope 时按历史日志处理。
5. 同一个 log_cleanup runner 根据 scope 选择现有计数/删除函数，继续使用 LogCleanupState/LogCleanupResult 和持久化进度。
6. audit 范围成功后直接结束，不执行消费日志删除或保留期附带阶段；usage 范围沿用原行为。
7. 使用现有 GET /api/system-task/current?type=log_cleanup 和 GET /api/system-task/:task_id 查询。两个区块都读取同一活动任务以维持互斥，但只有 payload.scope 与自身一致的区块显示进度及“清理中”（缺省 scope 按 usage 处理）；另一块保持原按钮文字并禁用，仅提示等待，不显示该任务的进度、数量或完成结果。
8. 成功或失败终态使 audit 查询缓存失效；回到审计页时重新读取列表，分页和风险 cursor 随页面重新挂载初始化。失败后可重新确认相同时间处理剩余数据。离开页面不会取消后台任务。

成功响应仍为现有 success/message/data 及 SystemTaskResponse，不新增响应体系。payload.scope 缺失按 usage 解析。现有 deleted_count 在 audit 范围表示审计行数；该字段在 usage 范围继续表示消费日志行数。

## Authorization and Scope

完全沿用已有日志维护权限：创建要求 system_settings.operations.logs.edit，查询要求 system_settings.operations.logs.view，均先经 AdminAuth。2026-11-21 用户明确要求与其他清理功能一致，不增加单独授权。审计清理与历史清理共用 SettingsPage 的只读禁用控制，不单独判断按钮权限，不要求额外的 audit.read、audit.export 或 audit.delete。

日志维护是已有全站维护权限：旧清理任务本来就按保留期删除所有操作者的审计记录。新范围维持这一权限语义，清理截止时间之前的全站审计，不套用列表用户名、IP、分类或角色筛选。弹窗必须明确“所有审计记录，包含当前筛选以外的记录”。不新增 audit.delete，不改已有权限目录或通用任务查询规则。

## Business Logic and Edge Cases

- audit 截止时间必须大于 0 且不晚于当前时间，严格删除 created_at < target_timestamp；等于截止时间和本次操作留痕保留。
- audit 手动范围独立于 retention_days，0 仍可通过明确的手动确认清理；不修改保留期设置。
- 同范围、同时间的活动任务可复用；不同范围或 audit 的不同截止时间返回 cleanup.busy，不能默默把审计请求解释为清理消费日志。旧 usage 同类任务复用保持兼容。
- 关系库继续先取一批主键再删除，批大小有上限。新审计范围使用已有 data_cleanup_setting 的批间隔、单批/任务超时和 Enabled 开关，避免连续占用数据库；不是新的配置体系。
- 新审计范围不能套用旧附带清理的 2000 批成功上限。无匹配数据才成功；到达任务超时或发生错误则失败，已提交删除保留。
- 进度复用现有 total/processed/remaining/progress。计数和删除在后台执行，关系库语句使用短超时；计数超时返回失败，不让管理请求或 relay 等待。
- 中途停止、失租、崩溃不能撤销已经提交的删除；重试只删除仍存在的行。不把未确认的删除数量当作精确结果。
- ClickHouse 继续使用既有一次同步 mutation，而非循环小 mutation；使用任务期限约束等待。超时只表示未确认完成，不声称服务端删除已撤销。沿用当前实现的计数口径，本次不重构数据库 mutation 管理。
- task.Error 对日志清理保存稳定的脱敏错误键，完整原因只进入内部日志；页面显示失败提示，允许重新确认后重试。

## Data Model and Index Design

无表/列/索引/迁移变化。只扩展现有 payload JSON；任务类型及 active_key 仍为 log_cleanup。

复用 audit_logs 的 created_at 索引、idx_audit_time_event(created_at,event_id)、主键以及 system_tasks 的 task_id/active_key 唯一索引。候选查询按 created_at,event_id 排序，并只取有限主键；不使用 DELETE LIMIT 或 OFFSET。计数有时间上界且仅在后台、独立池中进行。

设计阶段已只读打开本地 one-api.db，SQLite 3.49.1 的时间查询使用 idx_audit_time_event 覆盖索引。该证据不代替真实 MySQL/PostgreSQL 的计划和删除测试。新代码不得在运行清理时补建索引。

## Frontend Interaction and i18n

HistoryLogCleanup 通过 scope 复用日期、确认和任务处理实现。审计清理与历史清理使用完全相同的 flex/min-w-0/flex-col/gap-4 区块、font-medium 标题、DateTimePicker 和 flex/flex-wrap/gap-3 按钮行，清理按钮均为默认尺寸的 destructive 变体，区块间使用既有 Separator。无需先打开另一层弹窗选择日期。

最终确认继续使用 ConfirmDialog 的标准标题、说明与底部按钮，审计标题使用已有 Clean audit logs 文案；说明按相邻错误日志清理的 space-y-2 段落排列。复用全部现有翻译，不引入自定义弹窗布局。

清理确认写明截止时间、全站审计范围、筛选不参与删除，以及消费日志和安全告警保留。请求中禁用重复提交；日期不合法或在未来不能确认。任务查询失败可重试；失败不直接渲染 Go/SQL 错误。其他范围任务占用时只显示等待提示，继续跟踪任务以在结束时解除禁用；进度卡片及终态仅显示在所属区块。

沿用设置页生命周期：权限状态变为只读时重新挂载内容并关闭弹窗，离开页面时停止轮询。审计任务结束后使 audit 查询缓存失效，返回审计页时重新挂载列表并初始化分页和风险游标。

前端文案遵循已加载 i18n-translate skill，使用临时 add-missing-keys.mjs 写入七种 locale，并运行 i18n:sync；不修改上游所有的认证/HTTP/API 基础文件。

## Error Handling and Audit Trail

控制器沿用 dataCleanupError、ApiErrorI18n 和认证中间件；新增不合法范围的业务错误翻译。后台任务记录底层原因，新审计范围向客户端提供脱敏失败原因。

继续使用现有 /api/system-task/log-cleanup 的管理审计路由映射，补入 scope/target_timestamp 安全参数，使清理类型可辨认。任务启动和结果仍留在原 system_tasks 中，不增加回执表。

## Main Chain Impact

relay goroutine 新增 DB/Redis 调用、锁、等待、goroutine 均为 0。所有工作发生在既有管理 API 和后台清理 runner；不改变 AI 路由、计费或采样。

audit_logs 同时被既有后台审计写入和 relay IP 观察后台写入访问。新增入口复用现有清理预约，未增加同时运行的清理数量；新审计范围使用独立维护连接及批间等待。物理数据库 I/O 仍共享，不能把连接隔离当作没有存储竞争。

## Shared Resource Audit and Concurrency Analysis

| 资源 | 处理 |
| --- | --- |
| audit_logs | 现有清理目标，后台按时间分批删除；不改写入路径 |
| logs、资金和凭据表 | audit 范围不访问；usage 范围保持原有行为 |
| system_tasks/system_task_locks | 同一任务类型、唯一占用键和心跳，不增加任务框架或并行清理 |
| 日志连接池 | 按任务复用现有独立连接构造，最大 1 连接，任务结束关闭；原历史日志路径不变 |
| 主库控制面连接 | 既有任务领取/进度/心跳；单活动清理、批间等待限制频率 |
| Redis/认证缓存/安全队列/文件 | 本功能不新增访问或修改 |

100k RPM AI 流量下每个 AI 请求新增成本为 0。默认每批 100 条、间隔 200ms，关系库最多约 5 批/秒、10 条删除相关语句/秒，另有任务起止计数和主库进度。ClickHouse 的服务器重写成本单独验证。30k+ RPM 部署验收需对比清理开启前后的 relay 尾延迟，不能由静态分析宣称容量测试通过。

## Planned Changes

- service/system_task.go：payload 范围、活动任务匹配、复用同一计数/删除循环、audit 范围预算及独立连接。
- controller/system_task.go：解析和验证 scope；沿用现有路由/权限。
- model/audit_log.go 及原保留期测试调用：现有清理函数显式接收 DB，以便复用已有独立连接；不改表结构或采集逻辑。
- web/src/features/system-settings/api.ts/types.ts/maintenance/history-log-cleanup.tsx：扩展现有 API 参数、类型和清理组件。
- web/src/features/system-settings/maintenance/log-settings-section.tsx：组合现有审计清理组件，完成后使审计缓存失效。
- web/src/features/usage-logs/audit/components/audit-log-viewer.tsx：移除工具栏清理入口及仅供该入口使用的权限、刷新逻辑。
- 现有控制器/服务/前端测试、相关翻译、本文：补充本次范围及验收结果。

## Validation Plan

覆盖 audit-only 不删除 logs、边界时间、跨 scope 活动任务冲突、同请求复用、错误/取消/超时不报成功、旧 payload/usage 路径兼容。验证维护权限不足时无入口且后端沿用原鉴权、取消确认不发请求、scope 参数正确、任务终态刷新和其他范围占用提示。

在真实 MySQL/PostgreSQL 的隔离库验证计数、有限批次删除、索引计划及独立主库/日志库组合。SQLite 为兼容性补充；ClickHouse 检验既有同步 mutation 路径。不得用全局删除测试触及现有业务数据。

运行相关 Go 包测试、前端 Vitest、typecheck、lint/格式检查及生产构建；记录数据库版本、命令、结果和未完成项。已查阅本地生产环境记录和发布清单；本次不包含部署或执行现有业务数据清理。

## Implementation and Verification

实现已完成，未新增生产表、列、索引、任务类型或配置项；没有部署，也未对现有业务数据执行清理。

实际实现补充：

- 审计请求和 payload 都将旧 target_timestamp 固定为 0，以 audit_target_timestamp 保存截止时间。旧控制器和旧 runner 都会拒绝审计任务，避免混合版本误删消费日志。
- 清理状态继续使用同一组字段；审计主目标删除数写入 deleted_count。历史日志入口的 audit_deleted_count 仍表示按保留期附带清理的数量。
- 任务失败统一保存 cleanup.failed；新增 cleanup.invalid_scope 和 cleanup.invalid_log_date 翻译，避免向审计清理用户显示财务保留期错误。底层原因记录在内部日志。
- HistoryLogCleanup 复用现有 ConfirmDialog 和日期控件，历史与审计模式均直接呈现在维护页。任务进度查询可重试，实际任务范围和截止时间可见。清理成功或失败终态都会刷新列表，以反映可能已提交的部分删除。
- 前端权限测试覆盖日志维护可编辑/只读状态，以及全部审计/本人/令牌视图不再显示清理入口。后端创建与进度接口保持原 AdminAuth 和 operations.logs 权限中间件。

## 验证与发布边界

保留后端创建/租约/互斥回归以及前端日志维护、只读权限、二次确认、任务范围隔离和缓存失效测试。历史日志与审计清理共用布局，但各自只显示属于本范围的任务进度与终态；取消确认不提交。

关系库无新增 schema；目标部署仍按发布清单验证维护连接、真实清理边界和负载影响。ClickHouse 超时后的服务端 mutation 撤销不在本功能能力范围内。

# 管理员使用日志导出冷却独立配置

日期：2026-11-16。状态：用户已确认，已实现并完成下述验证；未部署。

## 目标与范围

在“使用日志导出设置”中增加管理员专用冷却时间。管理员及超级管理员使用新设置，普通用户及非管理员员工继续使用原设置。按创建者当前登录角色判断，不按导出对象、模板或是否导出本人数据判断。

范围为使用日志后台导出。账单、台账、审计日志导出，以及自助接口独立的请求频率限制不属于本次改动。

## Existing Logic and Related-Path Findings

以下为实现前已核实的行为及改动衔接点：

| 位置 | 当前逻辑及本次衔接 |
| --- | --- |
| `setting/operation_setting/log_export_setting.go`：`LogExportSetting`、`GetUserCooldownSec` | 只有 `user_cooldown_sec`；小于等于 0 回退 300 秒，正值上限 86400 秒。配置通过 `config.GlobalConfig.Register` 注册。新增管理员字段和 getter，保留旧字段语义。 |
| `controller/log_export.go`：`createLogExportJob` | 管理员和自助入口共用创建函数；先校验、检查任务数与磁盘，再抢全局槽位，随后检查冷却；任务元数据保存失败时释放槽位并清理冷却。当前两种入口都使用用户冷却。 |
| `controller/employee_export.go`：`prepareEmployeeExport`、`EmployeeCreateExport` | 先校验员工身份、模板、客户归属和过滤条件，之后同样抢槽位、检查用户冷却、保存任务并启动。必须同步接入角色选择，避免漏掉第二个创建入口。 |
| `model/log_export_job.go`：`CheckAndSetLogExportCooldown` | 使用有超时的 Redis `SET NX`，键为 `logexport:cooldown:<user_id>`，TTL 在函数内读取用户冷却；拒绝不续期。Redis 错误记录系统日志并沿用当前放行行为。需改为接收已选择的秒数。 |
| `model/log_export_job.go`：任务保存、启动、取消和列表 | 任务元数据及索引存于 Redis，异步生成文件，完成/失败释放运行槽位；取消、删除、下载和列表不重新计算创建冷却。本次无需改变任务结构或列表分页。 |
| `router/api-router.go` | 管理员入口为 `AdminAuth`，自助和员工入口为 `UserAuth`，员工还经过 `EmployeeExportGuard`；自助创建另有 `LogExportRateLimit`，其限制独立存在。 |
| `service/settingsaccess/scopes.go`、`middleware/system_settings_auth.go` | 设置按 scope 白名单授权，新键必须加入 `system-tuning.log-export`，否则有分区权限的管理员无法读写。 |
| `controller/option.go`、`model/option.go`、`setting/config/config.go` | `/api/option/` 保存键值至既有 `options` 表并发布到内存配置；配置加载只覆盖存在的字段，缺少的新键保留程序默认值。新增键复用此流程。 |
| `web/src/features/system-settings/maintenance/log-export-section.tsx` | 表单通过 schema、`FIELD_KEYS` 与扁平化函数读写配置，使用差异保存和既有确认/反馈流程；现有冷却使用 `NumericPresetInput`，0 显示“默认 300 秒”。 |
| `web/src/components/numeric-preset-input.tsx` | 已支持多个带文案的特殊数值及普通整数范围，能够直接表达继承、关闭、自定义，不需新增控件。 |
| 现有测试 | `model/log_export_job_test.go` 已覆盖首次放行、重复拒绝、清理后放行；`controller/log_export_upstream_cases_test.go` 覆盖槽位忙时不消耗冷却；设置测试覆盖默认回退与上限。扩展这些契约。 |

相关设计为 `usage-log-export.md`、`usage-log-export-unification.md` 和 `employee-customer-log-export.md`。基线文档把旧冷却描述为“单管理员”，但当前实现已对全部创建者共用，应以本次调查为准。

用户已确认本方案的默认值、继承及关闭语义；无需要先补齐的后端能力。下表记录改动前的调查基线，实际行为见后续章节。

## 配置与交互

新增 `log_export_setting.admin_cooldown_sec`（Go 字段 `AdminCooldownSec`）：

| 值 | 行为 |
| --- | --- |
| `-1`，默认 | 沿用用户与员工的冷却设置。旧部署缺少新键时保持原有效冷却，包括已有自定义值。 |
| `0` | 管理员无创建冷却；仍受并发任务数、未完成任务数、磁盘与资源节流限制。 |
| `1..86400` | 管理员独立冷却秒数，不再跟随用户设置。 |

原 `user_cooldown_sec` 保持原值和语义，0 仍为默认 300 秒。界面将其标为“用户与员工导出冷却（秒）”，旁边增加“管理员导出冷却（秒）”，说明涵盖超级管理员。管理员输入框提供“沿用用户与员工设置”和“无冷却”预设；其他数字为自定义秒数。

复用当前 `quotaFields`、`NumericPresetInput`、表单布局与保存按钮；同步 schema、`FIELD_KEYS`、`SystemTuningSettings`、默认值及 section registry。新默认值使用 `??` 回退，保留显式的 0。

新增文案登记到静态翻译键并覆盖 en、zh、zh-TW、fr、ja、ru、vi。沿用当前保存中状态、成功提示、失败提示和表单重置行为，不增加新的确认步骤。

后端新 getter 对 `-1` 返回当前用户有效冷却，对 0 返回 0，对正值限制到一天；异常旧数据小于 -1 时回退继承。新键写入 `/api/option/` 时先验证为 `-1..86400` 整数，非法值记录警告并使用现有 `MsgInvalidParams` 返回，不依赖前端校验。

## 数据流与完整生命周期

1. 设置页通过现有分区 API 读取、修改并保存新键，重载页面后仍能还原其语义；旧配置无需批量回填。
2. 创建任务时，从服务端已认证上下文读取 `role`；`role >= common.RoleAdminUser` 选择管理员 getter，其余选择用户 getter。管理员走自助或员工入口也使用管理员时间，数据权限仍由原入口决定。
3. 保持既有校验、未完成任务数、磁盘检查和全局槽位获取顺序。
4. 将本次选定的有效秒数传给 `CheckAndSetLogExportCooldown(userID, cooldownSec)`。大于 0 时沿用同一账号 Redis 键及原子 `SET NX`；0 时直接跳过该检查，不以零 TTL 创建永久键。
5. 冷却拒绝时释放本次槽位，提示使用本次选定的秒数换算分钟，避免管理员已设短冷却却提示用户的长冷却。
6. 元数据保存失败仍释放槽位；仅本次启用并取得冷却时清理该键，避免无冷却路径误删旧键。成功后沿用现有异步启动、审计记录、进度、取消、下载与过期清理。
7. 同一账号所有入口共用一个键，避免切换入口绕过冷却。修改正值不会重写已存在的 TTL；下次成功创建使用新值。改为 0 后新请求立即跳过冷却，旧键自然到期；随后重新启用正值时，尚未到期的旧键仍会生效。设置说明应明确“已有冷却按原到期时间结束”。

## API 契约与权限

不新增端点，不接收客户端传入的角色或冷却秒数来决定导出权限。

| 接口 | 权限与契约 |
| --- | --- |
| `GET /api/option/?scope=system-tuning.log-export` | `AdminAuth` 加分区查看权限；在现有配置列表中返回新键。 |
| `PUT /api/option/` | `AdminAuth` 加分区编辑权限；请求示例 `{"scope":"system-tuning.log-export","key":"log_export_setting.admin_cooldown_sec","value":30}`，沿用成功/失败响应。Root 的已有无 scope 管理入口保持兼容。 |
| `POST /api/log/export/jobs` | `AdminAuth`；请求不变，成功仍返回 `{"success":true,"data":{"job_id":"..."}}`。 |
| `POST /api/log/self/export/jobs` | `UserAuth` 和现有 `LogExportRateLimit`；请求仍为 `{filters, options}`，只允许本人范围。 |
| `POST /api/user/employee/export/jobs` | `UserAuth` 和员工授权；请求、客户范围及模板约束不变。 |

冷却等业务拒绝复用 `ApiErrorI18n` 的 HTTP 200 / `success:false` 契约及现有提示键。认证、禁止访问及独立请求限流仍由原中间件处理。新增校验不返回底层错误。Redis 检查失败继续使用现有系统日志及降级策略，本次不重构异常策略。

## 数据模型与索引

仅在既有 `options` 键值表中持久化一个配置键；无新 SQL 表、列、索引或迁移。导出任务 JSON、文件格式、查询条件和日志扫描索引均不变；不存在新增大表查询或需要新增 EXPLAIN 的访问模式。既有 Redis 冷却键格式保持兼容。

## Main Chain Impact

新角色分支与冷却检查只执行在管理/自助导出创建请求中，主 AI relay goroutine 新增操作为零。后台日志扫描、压缩、下载和现有节流机制保持原实现。

## Shared Resource Audit

| 资源 | 与 relay 的关系及处理 |
| --- | --- |
| `logexport:cooldown:<user_id>` | 搜索确认由导出创建路径使用，relay 不访问。保持账号级键，启用时一次 `SET NX`，禁用时零次。 |
| `logexport:slots`、任务/索引/取消/下载键 | 导出专用；复用原并发、生命周期与权限，没有新增 namespace 或扫描。 |
| `options` 表与内存配置 | 共用既有配置基础设施；仅低频设置保存增加一个键，创建任务读取内存，不新增 DB 查询或缓存。 |
| `logs`、员工/用户/模板查询涉及的表 | 原导出流程使用，部分也由 relay 访问；本次不改查询或事务，不加锁、索引或额外访问。 |
| Redis/DB 连接池、CPU、磁盘、后台任务 | 既有共享资源。管理员缩短或关闭冷却可能提高连续创建频率，但仍受原全局并发、任务数、批大小、行速率、CPU、磁盘和超时限制。不新建连接池或 worker。 |

## Concurrency Analysis

在 relay 30k 或 100k RPM 下，此变更为每个 AI 请求增加 0 次 DB、0 次 Redis、0 把锁、0 个 goroutine。每个导出创建请求只增加常量时间的角色和配置分支；冷却 Redis 调用仍为启用时 1 次、禁用时 0 次。后台任务创建数量仍由原全局槽位约束。此为代码层分析，未执行混合负载测试。

## 实施文件与验收

- 后端：`setting/operation_setting/log_export_setting.go`、`model/log_export_job.go`、`controller/log_export.go`、`controller/employee_export.go`、`controller/option.go`、`service/settingsaccess/scopes.go`。
- 前端：`maintenance/log-export-section.tsx`、`system-tuning/defaults.ts`、`system-tuning/section-registry.tsx`、`types.ts`（均在 `web/src/features/system-settings/`），以及静态翻译键和七种语言。无需修改受保护的 `web/src/lib/` 文件。
- 文档：本文件及基线 `usage-log-export.md` 的配置、冷却和热更新说明。

已按先补行为测试、再实现的顺序完成：

1. 新键缺失/继承时匹配旧用户自定义值；0 跳过；1、86400 两端有效；小于 -1、超限、小数、非数字输入被拒绝，运行时异常旧数据有安全回退。
2. 管理员、Root、普通用户、员工使用正确配置；管理员自助入口不误用用户值，所有入口不会因路由不同获得第二份冷却。
3. Redis TTL 与选定时长匹配；重复拒绝不延长 TTL；无冷却不写永久键；繁忙不消耗冷却；保存失败释放槽位与本次冷却。
4. 分区权限允许新键读写，越权保持拒绝；前端保存、刷新、0/-1 还原和边界校验正确；翻译完整。
5. 优先扩展既有设置、冷却及创建测试，使用真实 Redis 与唯一测试键并精确清理。执行受影响 Go 包测试、前端相关 Vitest、`bun run typecheck` 和 `bun run lint`。
6. 配置持久化已在真实 MySQL/PostgreSQL 确认保存/重载；遵循当前发布清单，本轮未执行 SQLite。无结构变更，不安排 schema 迁移；本次未做全应用重启或生产 schema 演练。

## 实现与验证结果

两个创建控制器按已认证角色选择有效秒数，并将同一数值用于 Redis TTL 及冷却提示。模型方法的秒数为显式参数，0 在访问 Redis 之前返回；创建失败仅在本次启用冷却时清理冷却键。设置页将用户/员工与管理员两个字段并排显示；七种语言各新增 6 条翻译，脚本同步前后比对未改动其他翻译值。

测试使用本机 Docker 隔离数据库 `logexport_admin_cooldown_20261116`、独立 PostgreSQL 日志库 `logexport_admin_cooldown_20261116_logs`，以及本次专用 Redis 容器；未改动线上或共享开发库中的业务记录。版本为 MySQL `26.10.0`、PostgreSQL `18.7`、Redis `7.4.11`；工具链 Go `1.26.11 windows/amd64`、Bun `1.3.13`。

| 命令/检查 | 结果 |
| --- | --- |
| `go test ./setting/operation_setting ./service/settingsaccess -count=1` | 两包通过，覆盖继承、默认值、边界、配置加载及分区白名单。 |
| `go test ./controller -run 'LogExport\|Employee.*Export\|Export.*Employee' -count=1 -timeout=10m -v` | MySQL 主库 + PostgreSQL 日志库、PostgreSQL 主库 + PostgreSQL 日志库均通过；最终回归包含设置持久化/重载、失败回滚和完整任务生成下载。 |
| `go test ./model -run 'LogExport\|Employee.*Export\|Export.*Employee' -count=1 -timeout=10m -v` | 两种主库均通过，各 139 个顶层测试；可选百万行 `TestLogExportRepriceLargeData` 未启用，明确跳过。 |
| `go test ./controller -run 'TestLogExport(CooldownCreateFailure\|CooldownByCreatorRole)$' -count=1 -timeout=10m -v` | 两种主库均通过；追加覆盖跨入口共享冷却、任务保存失败释放槽位、启用时清理本次冷却及关闭时保留旧键。故障仅注入元数据写入，冷却和并发槽使用真实 Redis。 |
| `go test ./controller -run '^TestLogExportAdminCooldownOption$' -count=1 -timeout=10m -v` | 两种主库均通过；带空白的整数文本在保存前规范化，确认响应成功与实际生效、持久化及重新加载一致。 |
| `bun run test src/features/system-settings/maintenance/__tests__/log-export-settings.test.tsx` | 8 项通过，覆盖继承、0、自定义值和上下限保存/重新加载；非法输入阻止提交。使用现有保存确认弹窗。 |
| `bun run typecheck` | 仓库脚本指向不存在的 `bun check -b`，命令失败；实际运行 `bunx --no-install tsgo -b` 通过，没有改动已有包脚本。 |
| `bun run lint` | 通过；存在其他文件的既有 warning，本次修改文件另做定向 lint。 |
| `bun run i18n:sync` | 通过，七种语言无新增缺失项；新增键 UTF-8 无 BOM。 |
| `bun run build` | 通过，完成生产前端构建。 |

初次前端交互测试因未点击既有保存确认弹窗而失败，补齐实际交互后 8 项通过；格式化后发现测试的多行分支缺少花括号，已修正。未降低断言或更改业务确认流程。

后端测试命令由临时脚本设置隔离 DSN（不包含在文档中）；日志保存在本机 `%TEMP%/logexport-cooldown-tests/`，本次创建的三个临时数据库及专用 Redis 容器已清理。本次未执行全仓测试、浏览器连真实后端验收、混合 relay 压测、SQLite、全应用重启或生产部署；未更改 SQL 查询、表结构、迁移或 relay 执行路径。

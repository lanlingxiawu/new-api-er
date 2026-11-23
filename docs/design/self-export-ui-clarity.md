# 普通用户导出界面优化

## 2026-11-16 修复：日期配置一致性与下拉裁切

### Existing Logic and Related-Path Findings

- `maintenance/log-settings-section.tsx` 保存 `log_setting.max_range_days`，页面说明它同时控制日志查询和导出；`setting/operation_setting/log_setting.go` 的字段注释也是这个约定。`controller/log.go:limitLogTimeRange` 已使用该值。
- 普通用户导出的 `controller/log_export.go:createLogExportJob` 和 `GetLogExportJobs` 却调用 `LogExportSetting.GetUserMaxRangeSec()`，读取独立的旧配置（默认 30 天）。所以日志维护设置为 62 天后，导出列表仍下发 30 天，创建接口也拒绝超过 30 天的区间。这是配置接线不一致，不是管理员设置错误。
- `SelfExportSheet` 已使用列表返回的 `max_range_sec` 禁用超限提交，但只给出笼统错误，提示位于表单末尾；`CompactDateTimeRangePicker` 原本没有范围限制，能够确认超限草稿。
- `ComboboxInput` 把菜单 portal 到最近的 dialog 并使用弹窗局部坐标，保留模态点击正确性。但菜单只向下展开，不考虑 dialog 自身的可见高度，导致弹窗滚动条被撑出、菜单裁切。
- 管理员及员工导出使用独立的 `GetAdminMaxRangeSec()`；本次修复普通用户流程，不改变这两个入口的限制。

### 修复范围、契约与生命周期

恢复普通用户导出与日志维护页面已有的约定：`GetUserMaxRangeSec()` 读取 `GetLogSetting().MaxRangeSeconds()`，列表下发和创建校验沿用同一个 getter。旧 `user_max_range_sec` 序列化字段保留兼容，但不再影响有效限制；运行参数表单移除该重复输入，避免保存一个无效的限额。无新配置、接口、SQL 表、索引或迁移。

按用户后续要求，旧输入位置增加“运维 → 日志维护 → 日志最长时间范围（天）”说明；有该页面查看权限的管理员可直接跳转。七种语言使用实际界面名称，不要求用户了解配置键。

数据流仍为已保存设置 → 普通用户导出列表 `GET /api/log/self/export/jobs` 的 `max_range_sec` → 弹窗草稿与选择器校验 → `POST /api/log/self/export/jobs` 再次校验 → 既有后台任务。路由仍要求用户认证，服务端仍强制当前用户 ID、允许的筛选字段及用户可见列。设置修改影响后续创建和刷新后的界面；已运行的任务保持原范围。提交时配置刚被收紧，沿用现有服务端错误反馈并保留草稿。

日期选择器增加可选 `maxRangeSec`，仅普通用户导出传入。输入框显示关联的最早/最晚时间，确认前拒绝不完整、倒置或超限草稿；超限快捷区间保留为草稿并提示修正，不能直接应用。提交与后端统一按 Unix 秒计算边界。未来日期规则不变，此次没有添加“不得晚于今天”的限制。复用现有七语言文案，把上限说明和错误放到日期字段旁。

继续复用业务 `Dialog` 和 `ComboboxInput`。普通用户弹窗适度加宽、仅正文滚动、底部按钮保持可见；下拉按弹窗和视口可用空间翻转，并约束高度，长列表在菜单内滚动。搜索、历史值输入、令牌分页、失败重试与焦点行为保留。

### Main Chain Impact / Shared Resource Audit

没有 relay goroutine、新中间件、同步 DB/Redis 操作或新后台工作。配置读取沿用内存设置；候选数据沿用现有 API、分页与按用户隔离的 Query 缓存。修改时间跨度只改变后台扫描覆盖的时间段，原有任务并发、批次、限速、查询超时和文件分片限制保持不变。后台导出仍读取与 relay 日志写入共享的 logs / LOG_DB 池；本次不添加查询、不扩大单批规模或并发，不新增连接池或锁。前端 ResizeObserver 和 scroll/resize 监听仅下拉打开时存在，关闭时释放。

### 验证范围

首轮回归覆盖日志维护设为 62 天而旧导出配置仍为 30 天时的列表能力、创建校验（上限前一秒、正好上限、超一秒），以及随后收紧为 7 天；该用例不启动导出 worker。前端覆盖截图的 46 天区间、日期草稿边界与已有筛选行为；下拉覆盖正常位置、短弹窗翻转、限高、选择及滚动跟随。后续按用户要求补充真实 worker 与文件下载验证，见下节；不以历史部署结果代替本次验证。

### 本次本地验证

- `bun run test src/components/ui src/features/usage-logs src/features/system-settings/maintenance/__tests__/log-settings.test.tsx --maxWorkers=4`：54 个文件、655 项通过。默认并发与构建同时运行时曾有两项候选选择断言失败；该文件单独重跑 4 项通过，限制并发后完整相关测试集通过。没有修改这些断言；测试环境已有 `window.scrollTo` 未实现提示。
- `bun x tsgo -b`、`bun run lint` 和 8 个修改文件的 `oxfmt --check` 通过。lint 仍有 7 条既有警告。仓库 `bun run typecheck` 脚本调用 `bun check -b`，当前 Bun 报 `Script not found "check"`，因此直接运行已安装的 TypeScript 原生检查器 `tsgo -b`。
- `go test ./controller -run '^TestSelfLogExportUsesLogMaintenanceRange$' -count=1` 与 `go test ./setting/operation_setting -run '^TestLogExportSetting' -count=1` 通过。控制器测试显式清空 `SQL_DSN`/`LOG_SQL_DSN`，仅使用独立 miniredis 验证列表契约及入参校验，没有启动 worker 或接触业务数据库；无 SQL/迁移变更，未运行数据库矩阵。
- 七种语言各新增一条位置说明，`bun run i18n:sync` 通过；与本次修改前的工作区快照比较，没有改动其他翻译键，UTF-8 无 BOM。
- 最终 `bun run build` 通过（10.7 秒），日志保存在 `_local/export-ui-build.log`。
- 本次仅修改本地代码，没有部署服务器或执行真实用户导出。

### 2026-11-16 实际导出链路复核

追踪了创建、任务保存、后台扫描、文件写入和下载的实际代码：

- `controller/log_export.go:createLogExportJob` 在创建前使用已修复的 `GetUserMaxRangeSec()` 校验普通用户跨度，`buildLogExportFilter` 原样保存起止秒数，并强制当前用户 ID。`model/log_export_job.go:CreateLogExportJob` 把筛选条件存入 Redis，`StartLogExport` 启动现有 worker。
- `model/log_export_scanner.go:runCustomer` 使用任务的 `Filters.StartTimestamp` / `EndTimestamp`，按闭区间窗口从早到晚完整推进，相邻窗口从上一窗口结束后的下一秒开始。窗口宽度上限 1 天只约束单次扫描，不是整个导出的最大跨度。扫描器没有另一个 30 天限制，也没有重新按日志查询的默认日期截断。
- `model/log_export_scan.go:scanLogExportBatch` 按窗口上下界、当前用户及类型/模型/令牌/分组筛选，使用 `(created_at, id)` 游标翻页。`writeLogExport` 按现有分片规则写 CSV.GZ；下载读取已完成分片，不重新查询或收缩时间范围。管理员/员工仍走各自已有创建校验。

新增 `controller/log_export_self_test.go:TestSelfLogExportConfiguredRangeThroughDownload`，直接调用项目控制器，使用真实数据库、Redis、异步 worker、压缩文件和下载处理器，最终解压并精确比对 CSV 行内容与顺序。旧用户上限刻意保留 30 天，管理员上限设为 1 天，验证普通用户只受日志维护的配置约束：

- 配置 62 天：正好 62 天的任务成功，9 条匹配日志分为 3 个压缩分片并全部下载；包含早于 30 天的记录、起止边界及扫描窗口边界，没有重复或遗漏。
- 同一进程将配置收紧为 7 天：新任务只导出该区间的 3 条日志、1 个分片。
- 两个配置下，超过上限 1 秒的创建请求都在启动 worker 前被拒绝。
- 区间前后各 1 秒、另一用户以及类型/模型/令牌/分组分别不匹配的日志均不在下载结果中。测试按行清理 fixture，文件使用独立临时目录。

这是控制器到实际存储、后台执行及文件交付的集成测试；没有经过完整 HTTP 路由/登录中间件或浏览器，不据此宣称部署环境的页面端到端验收完成。生产代码无需再改扫描和写文件逻辑，修复点仍为统一普通用户的配置 getter。

实际运行使用独立本地 Docker 容器：主库 MySQL 26.10.0；日志库分别为 PostgreSQL 18.7 和另一独立 MySQL 数据库；Redis 7.4.11。通过临时环境变量设置 `SQL_DSN`、`LOG_SQL_DSN`、`REDIS_CONN_STRING`，没有使用生产或共享开发库。命令均加 `-count=1 -v -timeout=180s`：

| 命令 | PostgreSQL 日志库 | MySQL 日志库 |
| --- | --- | --- |
| `go test ./controller -run 'LogExport'` | 43 项通过，0 跳过，3.235 秒 | 43 项通过，0 跳过，3.237 秒 |
| `go test ./model -run 'SelfLogExport\|WriteLogExport\|ExportBatchReleasesConnection\|ExportTotalTimeout'` | 25 项通过，0 跳过，8.153 秒 | 25 项通过，0 跳过，8.192 秒 |

每种日志库共 68 个顶层用例通过，覆盖新增下载链路及既有创建限制、用户可见列、分批/窗口边界、文件分片、取消和超时。日志保存在 `_local/export-range-{postgres,mysql}-{controller,model}.log`；额外的单项首跑记录为 `_local/export-range-pg-focused.log`。没有改动数据库模型、SQL 或迁移，本轮没有执行迁移升级矩阵或大数据量压测。

2026-10-27。用户已确认实施检查中提出的五项优化。

## 目标与范围

普通用户新建导出显示并允许编辑类型、模型、令牌、分组和时间；默认继承日志页条件，支持清除非时间筛选。历史摘要准确显示类型。完成任务展示下载截止时间，单文件直接下载，多文件保留选择菜单；零行任务说明没有符合条件的日志。

## 数据流与 API

日志页条件 → 新建弹窗独立草稿 → 用户确认 → 既有 `POST /api/log/self/export/jobs` → 既有后台导出 → 既有列表及签名下载。请求白名单及普通用户身份范围不变。

既有任务列表响应的任务对象增加可选 `expires_at`（Unix 秒）。服务端在完成权限与分页筛选后，批量读取该页任务 Redis TTL，返回服务端当前时间加剩余 TTL；新旧任务均按真实剩余有效期展示，不根据当前配置推测旧任务到期时间。无 TTL 或键已消失时不提供日期，前端不编造截止时间。下载授权仍由原接口实时校验。

## 数据、配置与生命周期

不新增 SQL 表、列、索引或迁移；不新增配置，不改变任务保留、清理、下载令牌和续传机制。截止时间指任务下载有效期，不指一次签名链接的短期有效期。删除、取消、失败仍沿用现有接口和文案。新建弹窗每次打开从当前页面条件初始化，关闭后的未提交草稿不影响下一次导出。

## 交互与错误

沿用 Field、Select、Input、Dialog 及现有主题样式。类型复用日志列表的选项，其他筛选使用带标签输入框，可直接修改或清空；不得额外请求普通用户无权访问的管理员候选接口。新建成功后返回任务列表；提交中禁用按钮。非法时间范围及失败反馈保持现有处理。零行已完成任务保留文件下载，同时提示调整条件；下载单文件直接调用原分片下载方法，多文件保留现有批量/逐文件功能。

## 子系统与共享资源审计

不触及 relay、计费、日志写入或数据库查询。仅列表管理路径新增一个 Redis pipeline，每页最多 100 个 TTL 命令，使用既有 3 秒请求上下文与连接池；键为导出专用 `logexport:job:`，relay 不读写该命名空间。无新增池、锁或后台线程，不影响主请求链。

## 实现前测试方案

- 前端真实组件交互：预填类型/模型/令牌/分组可见、修改与清除后的请求正确；重新打开重置草稿；时间上限仍校验。
- 列表回归：错误类型不能显示为全部；日期显示；零行提示；单文件点击直接下载，多个分片仍可选；管理员既有功能保留。
- Redis 集成：列表截止时间与真实 TTL 一致；配置变更不改变既有截止时间；无 TTL/已过期任务不伪造日期；TTL 查询失败传回现有错误路径。
- 前端相关测试、typecheck、lint、生产构建及后端相关包验证。覆盖香港原程序并用普通用户实际检查、下载和清理测试任务，不新增应用实例。

## 验证与部署结果

### 2026-10-27 员工和普通用户复查

普通用户新建导出的模型、令牌、分组改用现有 `ComboboxInput`，复用当前身份的 `/api/user/models`、`/api/user/self/groups`、`/api/token/` 和 `/api/token/search`；令牌候选按 50 条分页和 300ms 防抖搜索，仅使用名称、不读取密钥。候选缓存按用户隔离、有效期 30 秒。模型和分组只提供当前可用建议，因此保留手动输入历史值；加载失败显示重试，不清空已有条件，不阻止手动输入导出。上述交互替代早期的三个纯输入框，权限与导出请求白名单不变。

本次无后端、SQL、迁移、Redis 或连接池修改。复用现有管理端读取接口，主链无新增逻辑；不改变共享 users/tokens 表结构或写入方式。实现前新增实际下拉选择、历史值输入、加载失败和重试用例；员工与普通用户均需在香港现有应用验证创建、历史筛选、下载和身份隔离。

真实手机截图发现共用 `ComboboxInput` 在带 translate 的 Dialog 内以 fixed + 视口坐标定位，导致菜单额外偏移一个弹窗位置。保留弹窗内 portal（避免被模态层当作外部点击），改为 absolute + 弹窗局部坐标，扣除边框并计入滚动量；普通页面继续 fixed。新增有/无弹窗及滚动定位回归测试，部署后复查普通用户居中弹窗和员工右侧抽屉。

- `cd web && bun run test src/features/usage-logs`：34 个文件、484 项测试通过，包括单文件直接下载、多分片菜单、筛选修改/清除、取消后重新打开、类型和空结果文案。
- `bun run typecheck`、`bun run lint`、`bun run build` 通过；lint 保留 7 条既有警告，无错误。7 种语言各新增 3 条翻译，临时补键脚本已删除。
- `go test ./model -count=1` 通过（75.742 秒）；`go test ./controller -run 'Export' -count=1` 通过（4.697 秒）。使用独立临时 MySQL 8.0.46 主库（127.0.0.1:53319）和 PostgreSQL 15.19 日志库（127.0.0.1:55439），通过 `SQL_DSN` / `LOG_SQL_DSN` 指定。本次无 SQL 行为或迁移变更，没有重跑 SQLite/MySQL/PostgreSQL 的完整迁移矩阵。
- 早期以 PostgreSQL 同库运行全包时，既有分库断言和两条 session 短哈希用例失败；按项目测试 harness 要求改为 MySQL 主库、PostgreSQL 日志库后全包通过。未修改认证实现或受保护的上游测试；不将此结果表述为 PostgreSQL 主库全包通过。
- 香港原 `/root/new-api` 已原地替换，保持原配置与 3002 端口。版本 `hk-20261027-self-export-ui-1c565426b`，SHA256 `60cf0aa285cb3d57683159e0d8a5a7595a9672530c0c79447fc1e9f0e94a8f03`。旧 PID 2798048 正常退出，新 PID 2860603 健康检查通过；备份 `/root/deploy-export-type-20261027/new-api.before-self-export-ui`。
- 浏览器使用真实普通用户完成桌面和 390×844 手机表单检查。错误类型 + 模型 + 分组请求与可见条件一致，下载解压后 1 条错误日志；清除条件后 `type=0`，下载 2 条消耗和 1 条错误日志，均不包含另一账号的日志；无匹配模型时 0 行提示正确，下载只有表头。实际单次点击直接取得文件，列表显示真实 24 小时下载有效期。
- 实测触发既有每用户 10 分钟导出频控（HTTP 429）；连续测试只清理本次临时账号的 `rateLimit:v2:user:LE:<id>` / `logexport:cooldown:<id>`，未修改系统限流设置或其他用户的状态。
- 三个测试导出均通过页面确认删除，列表恢复为空；临时账号与日志行定向清理后计数均为 0，本机两套临时数据库容器已停止删除。
